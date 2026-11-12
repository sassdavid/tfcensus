package migrate

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // G501: terraform's S3 backend stores an MD5 digest; it isn't used for security.
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/sassdavid/tfcensus/internal/buildinfo"
	"github.com/sassdavid/tfcensus/internal/discovery"
)

// Errors from the backend.
var (
	ErrLocked     = errors.New("state is locked")
	ErrChanged    = errors.New("state changed since it was read")
	ErrVersioning = errors.New("bucket versioning is not enabled")
)

const (
	lockSuffix   = ".tflock"
	digestSuffix = "-md5"
	jsonType     = "application/json"
	lockIDAttr   = "LockID"
	jsonNull     = "null"
)

// ObjectReader is the read side of S3 that move uses.
type ObjectReader interface {
	GetObject(ctx context.Context, in *s3.GetObjectInput, opts ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	HeadObject(ctx context.Context, in *s3.HeadObjectInput, opts ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	GetBucketVersioning(
		ctx context.Context, in *s3.GetBucketVersioningInput, opts ...func(*s3.Options),
	) (*s3.GetBucketVersioningOutput, error)
}

// ObjectWriter is the write side of S3. Only this package calls it.
type ObjectWriter interface {
	PutObject(ctx context.Context, in *s3.PutObjectInput, opts ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	CopyObject(ctx context.Context, in *s3.CopyObjectInput, opts ...func(*s3.Options)) (*s3.CopyObjectOutput, error)
	DeleteObject(
		ctx context.Context, in *s3.DeleteObjectInput, opts ...func(*s3.Options),
	) (*s3.DeleteObjectOutput, error)
}

// LockTable is the DynamoDB lock and digest table of a backend. Only this package calls it.
type LockTable interface {
	PutItem(
		ctx context.Context,
		in *dynamodb.PutItemInput,
		opts ...func(*dynamodb.Options),
	) (*dynamodb.PutItemOutput, error)
	DeleteItem(
		ctx context.Context, in *dynamodb.DeleteItemInput, opts ...func(*dynamodb.Options),
	) (*dynamodb.DeleteItemOutput, error)
}

// Clients are the backend clients of one unit, made from its backend credentials.
type Clients struct {
	Reader ObjectReader
	Writer ObjectWriter // nil for read-only commands
	Table  LockTable    // nil when the backend has no dynamodb_table or the command is read-only
	By     string       // who writes, for history records
}

// Unit is a unit the engine reads and writes. Only the default workspace is supported.
type Unit struct {
	Path    string // relative to the repository, with forward slashes
	Dir     string
	Bucket  string
	Key     string
	Region  string
	Table   string
	Lock    discovery.Lock
	Account string // the account the unit deploys to, when discovery knows it
}

func (u *Unit) location() string { return "s3://" + u.Bucket + "/" + u.Key }

// snapshot is a state object as read, with the version it was read at.
type snapshot struct {
	body      []byte
	versionID string
	exists    bool
}

func notFound(err error) bool {
	if apiErr, ok := errors.AsType[smithy.APIError](err); ok {
		switch apiErr.ErrorCode() {
		case "NoSuchKey", "NotFound":
			return true
		}
	}
	return false
}

func preconditionFailed(err error) bool {
	if apiErr, ok := errors.AsType[smithy.APIError](err); ok {
		switch apiErr.ErrorCode() {
		case "PreconditionFailed", "ConditionalRequestConflict", "ConditionalCheckFailedException":
			return true
		}
	}
	return false
}

// quietChecksum stops the SDK from logging to stderr when an object was stored without a checksum,
// as objects written by older terraform versions and emulators are.
func quietChecksum(o *s3.Options) { o.DisableLogOutputChecksumValidationSkipped = true }

// pull reads the unit's state with the version it was read at.
func pull(ctx context.Context, c ObjectReader, u *Unit) (snapshot, error) {
	return getObject(ctx, c, u.Bucket, u.Key, "")
}

func getObject(ctx context.Context, c ObjectReader, bucket, key, version string) (snapshot, error) {
	in := &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)}
	if version != "" {
		in.VersionId = aws.String(version)
	}
	out, err := c.GetObject(ctx, in, quietChecksum)
	if notFound(err) {
		return snapshot{}, nil
	}
	if err != nil {
		return snapshot{}, fmt.Errorf("read s3://%s/%s: %w", bucket, key, err)
	}
	defer func() { _ = out.Body.Close() }()
	body, err := io.ReadAll(out.Body)
	if err != nil {
		return snapshot{}, fmt.Errorf("read s3://%s/%s: %w", bucket, key, err)
	}
	return snapshot{body: body, versionID: aws.ToString(out.VersionId), exists: true}, nil
}

// head returns the current version of the unit's state, and whether it exists.
func head(ctx context.Context, c ObjectReader, u *Unit) (string, bool, error) {
	out, err := c.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(u.Bucket), Key: aws.String(u.Key)})
	if notFound(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("check %s: %w", u.location(), err)
	}
	return aws.ToString(out.VersionId), true, nil
}

// checkUnchanged fails with [ErrChanged] when the state moved on from snap.
func checkUnchanged(ctx context.Context, c ObjectReader, u *Unit, snap snapshot) error {
	v, exists, err := head(ctx, c, u)
	if err != nil {
		return err
	}
	if exists != snap.exists || v != snap.versionID {
		return fmt.Errorf("%w: %s %s (read at version %q, now %q)", ErrChanged, u.Path, u.location(),
			snap.versionID, v)
	}
	return nil
}

// versioned reports whether the unit's bucket keeps object versions (S3-VER).
func versioned(ctx context.Context, c ObjectReader, u *Unit) (bool, error) {
	out, err := c.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: aws.String(u.Bucket)})
	if err != nil {
		return false, fmt.Errorf("check versioning of s3://%s: %w", u.Bucket, err)
	}
	return out.Status == s3types.BucketVersioningStatusEnabled, nil
}

// readHistory returns the unit's history and its ETag; a missing history is empty.
func readHistory(ctx context.Context, c ObjectReader, u *Unit) (*History, string, error) {
	key := u.Key + HistorySuffix
	out, err := c.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(u.Bucket), Key: aws.String(key)}, quietChecksum)
	if notFound(err) {
		return &History{Version: historyVersion, Unit: u.Path, Records: []Record{}}, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("read history s3://%s/%s: %w", u.Bucket, key, err)
	}
	defer func() { _ = out.Body.Close() }()
	h := &History{}
	if err = json.NewDecoder(out.Body).Decode(h); err != nil {
		return nil, "", fmt.Errorf("read history s3://%s/%s: %w", u.Bucket, key, err)
	}
	if h.Version != historyVersion {
		return nil, "", fmt.Errorf("read history s3://%s/%s: unsupported version %d", u.Bucket, key, h.Version)
	}
	return h, aws.ToString(out.ETag), nil
}

// writeHistory replaces the unit's history, failing if someone else changed it since etag.
func writeHistory(ctx context.Context, w ObjectWriter, u *Unit, h *History, etag string) error {
	body, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return fmt.Errorf("encode history: %w", err)
	}
	key := u.Key + HistorySuffix
	in := &s3.PutObjectInput{
		Bucket: aws.String(u.Bucket), Key: aws.String(key), Body: bytes.NewReader(append(body, '\n')),
		ContentType: aws.String(jsonType),
	}
	if etag == "" {
		in.IfNoneMatch = aws.String("*")
	} else {
		in.IfMatch = aws.String(etag)
	}
	if _, err = w.PutObject(ctx, in); err != nil {
		if preconditionFailed(err) {
			return fmt.Errorf("write history s3://%s/%s: %w", u.Bucket, key, ErrChanged)
		}
		return fmt.Errorf("write history s3://%s/%s: %w", u.Bucket, key, err)
	}
	return nil
}

// lockInfo is the lock record terraform writes, so terraform shows who holds the lock.
type lockInfo struct {
	ID        string    `json:"ID"`
	Operation string    `json:"Operation"`
	Info      string    `json:"Info"`
	Who       string    `json:"Who"`
	Version   string    `json:"Version"`
	Created   time.Time `json:"Created"`
	Path      string    `json:"Path"`
}

func newLockInfo(u *Unit, operation string, now time.Time) ([]byte, error) {
	id, err := newUUID()
	if err != nil {
		return nil, err
	}
	who, _ := os.Hostname()
	return json.Marshal(lockInfo{
		ID: id, Operation: operation, Info: "tfcensus " + operation, Who: "tfcensus@" + who,
		Version: buildinfo.Get().Version, Created: now.UTC(), Path: u.Bucket + "/" + u.Key,
	})
}

// lock takes the unit's backend lock the way terraform does and returns the function that
// releases it.
func lock(ctx context.Context, c *Clients, u *Unit, info []byte) (func(context.Context) error, error) {
	var releases []func(context.Context) error
	release := func(ctx context.Context) error {
		var errs []error
		for _, r := range releases {
			errs = append(errs, r(ctx))
		}
		return errors.Join(errs...)
	}
	if u.Lock == discovery.LockLockfile || u.Lock == discovery.LockBoth {
		r, err := lockObject(ctx, c.Writer, u, info)
		if err != nil {
			return nil, err
		}
		releases = append(releases, r)
	}
	if u.Table != "" && (u.Lock == discovery.LockDynamoDB || u.Lock == discovery.LockBoth) {
		r, err := lockItem(ctx, c.Table, u, info)
		if err != nil {
			return nil, errors.Join(err, release(ctx))
		}
		releases = append(releases, r)
	}
	return release, nil
}

// lockObject creates the <key>.tflock object that S3 native locking uses.
func lockObject(ctx context.Context, w ObjectWriter, u *Unit, info []byte) (func(context.Context) error, error) {
	key := u.Key + lockSuffix
	_, err := w.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(u.Bucket), Key: aws.String(key), Body: bytes.NewReader(info),
		ContentType: aws.String(jsonType), IfNoneMatch: aws.String("*"),
	})
	if err != nil {
		if preconditionFailed(err) {
			return nil, fmt.Errorf("%w: s3://%s/%s exists", ErrLocked, u.Bucket, key)
		}
		return nil, fmt.Errorf("lock s3://%s/%s: %w", u.Bucket, key, err)
	}
	return func(ctx context.Context) error {
		_, delErr := w.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(u.Bucket), Key: aws.String(key)})
		if delErr != nil {
			return fmt.Errorf("unlock s3://%s/%s: %w", u.Bucket, key, delErr)
		}
		return nil
	}, nil
}

// lockItem creates the DynamoDB lock item that dynamodb_table locking uses.
func lockItem(ctx context.Context, t LockTable, u *Unit, info []byte) (func(context.Context) error, error) {
	id := u.Bucket + "/" + u.Key
	_, err := t.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(u.Table),
		Item: map[string]ddbtypes.AttributeValue{
			lockIDAttr: &ddbtypes.AttributeValueMemberS{Value: id},
			"Info":     &ddbtypes.AttributeValueMemberS{Value: string(info)},
		},
		ConditionExpression: aws.String("attribute_not_exists(" + lockIDAttr + ")"),
	})
	if err != nil {
		if preconditionFailed(err) {
			return nil, fmt.Errorf("%w: %s in table %s", ErrLocked, id, u.Table)
		}
		return nil, fmt.Errorf("lock %s in table %s: %w", id, u.Table, err)
	}
	return func(ctx context.Context) error {
		_, delErr := t.DeleteItem(ctx, &dynamodb.DeleteItemInput{
			TableName: aws.String(u.Table),
			Key:       map[string]ddbtypes.AttributeValue{lockIDAttr: &ddbtypes.AttributeValueMemberS{Value: id}},
		})
		if delErr != nil {
			return fmt.Errorf("unlock %s in table %s: %w", id, u.Table, delErr)
		}
		return nil
	}, nil
}

// restore makes the state at version the current one, byte for byte, and fixes the DynamoDB
// digest. An empty version means the state didn't exist, so the current one is deleted.
func restore(ctx context.Context, c *Clients, u *Unit, version string) error {
	if version == "" {
		_, err := c.Writer.DeleteObject(
			ctx,
			&s3.DeleteObjectInput{Bucket: aws.String(u.Bucket), Key: aws.String(u.Key)},
		)
		if err != nil {
			return fmt.Errorf("delete %s: %w", u.location(), err)
		}
		return setDigest(ctx, c, u, nil)
	}
	old, err := getObject(ctx, c.Reader, u.Bucket, u.Key, version)
	if err != nil {
		return err
	}
	if !old.exists {
		return fmt.Errorf("restore %s: version %s no longer exists", u.location(), version)
	}
	src := url.PathEscape(u.Bucket) + "/" + escapeKey(u.Key) + "?versionId=" + url.QueryEscape(version)
	_, err = c.Writer.CopyObject(ctx, &s3.CopyObjectInput{
		Bucket: aws.String(u.Bucket), Key: aws.String(u.Key), CopySource: aws.String(src),
	})
	if err != nil {
		return fmt.Errorf("restore %s to version %s: %w", u.location(), version, err)
	}
	return setDigest(ctx, c, u, old.body)
}

func escapeKey(key string) string {
	u := url.URL{Path: key}
	return u.EscapedPath()
}

// setDigest writes the MD5 digest terraform checks state reads against, or deletes it when body
// is nil. Backends without a DynamoDB table have no digest.
func setDigest(ctx context.Context, c *Clients, u *Unit, body []byte) error {
	if u.Table == "" || c.Table == nil {
		return nil
	}
	id := u.Bucket + "/" + u.Key + digestSuffix
	key := map[string]ddbtypes.AttributeValue{lockIDAttr: &ddbtypes.AttributeValueMemberS{Value: id}}
	var err error
	if body == nil {
		_, err = c.Table.DeleteItem(ctx, &dynamodb.DeleteItemInput{TableName: aws.String(u.Table), Key: key})
	} else {
		sum := md5.Sum(body) //nolint:gosec // G401: terraform's digest format
		key["Digest"] = &ddbtypes.AttributeValueMemberS{Value: hex.EncodeToString(sum[:])}
		_, err = c.Table.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(u.Table), Item: key})
	}
	if err != nil {
		return fmt.Errorf("update digest %s in table %s: %w", id, u.Table, err)
	}
	return nil
}
