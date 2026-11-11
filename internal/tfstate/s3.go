package tfstate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

// DefaultWorkspace is the workspace whose state lives at the backend key itself.
const DefaultWorkspace = "default"

const actionGet = "s3:GetObject"

// defaultWorkspacePrefix is the s3 backend's workspace_key_prefix when the config sets none.
const defaultWorkspacePrefix = "env:"

// Client is the part of the S3 API the reader calls. Both calls are reads.
type Client interface {
	GetObject(ctx context.Context, in *s3.GetObjectInput, opts ...func(*s3.Options)) (*s3.GetObjectOutput, error)
	ListObjectsV2(
		ctx context.Context,
		in *s3.ListObjectsV2Input,
		opts ...func(*s3.Options),
	) (*s3.ListObjectsV2Output, error)
}

// Errors from reading a state object.
var (
	ErrAbsent       = errors.New("state object does not exist")
	ErrAccessDenied = errors.New("access denied")
)

// Object is a state object's metadata and its parsed content.
type Object struct {
	Size         int64
	LastModified *time.Time
	State        *State
}

// Fetch reads and parses s3://bucket/key without taking a lock. It returns [ErrAbsent] when the
// key doesn't exist and [ErrAccessDenied] when S3 refuses the read. A parse error comes with the
// object's metadata.
func Fetch(ctx context.Context, c Client, bucket, key string, opts ParseOptions) (*Object, error) {
	out, err := c.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	if err != nil {
		return nil, classify(err, actionGet, bucket, key)
	}
	defer func() { _ = out.Body.Close() }()
	obj := &Object{Size: aws.ToInt64(out.ContentLength), LastModified: out.LastModified}
	obj.State, err = ParseWith(out.Body, opts)
	// Drain what the decoder didn't need, so the connection can be reused.
	_, _ = io.Copy(io.Discard, out.Body)
	if err != nil {
		return obj, err
	}
	return obj, nil
}

func classify(err error, action, bucket, key string) error {
	loc := "s3://" + bucket + "/" + key
	if apiErr, ok := errors.AsType[smithy.APIError](err); ok {
		switch apiErr.ErrorCode() {
		case "NoSuchKey", "NotFound":
			return fmt.Errorf("%w: %s", ErrAbsent, loc)
		case "AccessDenied", "Forbidden":
			need := action
			if action == actionGet {
				need += ", and kms:Decrypt when the bucket uses SSE-KMS"
			}
			return fmt.Errorf("%w: %s on %s; the backend credentials need %s", ErrAccessDenied, action, loc, need)
		}
	}
	return fmt.Errorf("read %s: %w", loc, err)
}

// workspacePrefix returns the key prefix of non-default workspaces.
func workspacePrefix(configured string) string {
	if configured == "" {
		return defaultWorkspacePrefix
	}
	return configured
}

// WorkspaceKey returns the state key of workspace ws, the way the s3 backend lays it out.
func WorkspaceKey(prefix, key, ws string) string {
	if ws == DefaultWorkspace || ws == "" {
		return key
	}
	return workspacePrefix(prefix) + "/" + ws + "/" + key
}

// listKeys returns every key under prefix in bucket.
func listKeys(ctx context.Context, c Client, bucket, prefix string) ([]string, error) {
	var keys []string
	p := s3.NewListObjectsV2Paginator(c, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
		Prefix: aws.String(prefix),
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, classify(err, "s3:ListBucket", bucket, prefix)
		}
		for _, o := range page.Contents {
			keys = append(keys, aws.ToString(o.Key))
		}
	}
	return keys, nil
}

// workspacesIn picks the workspaces of key out of the keys listed under prefix/.
func workspacesIn(keys []string, prefix, key string) []string {
	var out []string
	for _, k := range keys {
		rest, ok := strings.CutPrefix(k, workspacePrefix(prefix)+"/")
		if !ok {
			continue
		}
		ws, tail, ok := strings.Cut(rest, "/")
		if ok && ws != "" && tail == key {
			out = append(out, ws)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}
