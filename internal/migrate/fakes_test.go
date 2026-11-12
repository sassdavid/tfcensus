package migrate_test

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/discovery"
	"github.com/sassdavid/tfcensus/internal/migrate"
)

const (
	fixtures   = "../../testdata/move"
	goldenDir  = "../../testdata/golden/move"
	testBucket = "fixture-tfstate-111111111111"
	testTable  = "fixture-tflock"
	testBy     = "arn:aws:iam::111111111111:role/fixture-mover"
)

// objVersion is one version of an S3 object; a delete marker has deleted set.
type objVersion struct {
	id      string
	body    []byte
	deleted bool
}

func etagOf(b []byte) string {
	sum := md5.Sum(b)
	return `"` + hex.EncodeToString(sum[:]) + `"`
}

// fakeS3 is a versioned in-memory S3 with conditional writes.
type fakeS3 struct {
	mu        sync.Mutex
	unversion bool
	objects   map[string][]objVersion
	seq       int
	writes    []string
}

func newFakeS3() *fakeS3 { return &fakeS3{objects: map[string][]objVersion{}} }

func apiErr(code string) error { return &smithy.GenericAPIError{Code: code, Message: code} }

func (f *fakeS3) current(key string) (objVersion, bool) {
	vs := f.objects[key]
	if len(vs) == 0 || vs[len(vs)-1].deleted {
		return objVersion{}, false
	}
	return vs[len(vs)-1], true
}

func (f *fakeS3) add(key string, v objVersion) string {
	f.seq++
	v.id = fmt.Sprintf("v%03d", f.seq)
	if f.unversion {
		v.id = ""
		f.objects[key] = []objVersion{v}
		return v.id
	}
	f.objects[key] = append(f.objects[key], v)
	return v.id
}

// put stores body as a new version, as a writer outside tfcensus would.
func (f *fakeS3) put(key string, body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.add(testBucket+"/"+key, objVersion{body: body})
}

func (f *fakeS3) body(t *testing.T, key string) ([]byte, bool) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.current(testBucket + "/" + key)
	return v.body, ok
}

func (f *fakeS3) GetObject(
	_ context.Context,
	in *s3.GetObjectInput,
	_ ...func(*s3.Options),
) (*s3.GetObjectOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := aws.ToString(in.Bucket) + "/" + aws.ToString(in.Key)
	var v objVersion
	found := false
	if in.VersionId != nil {
		for _, c := range f.objects[key] {
			if c.id == *in.VersionId && !c.deleted {
				v, found = c, true
			}
		}
	} else {
		v, found = f.current(key)
	}
	if !found {
		return nil, apiErr("NoSuchKey")
	}
	return &s3.GetObjectOutput{
		Body: io.NopCloser(bytes.NewReader(v.body)), VersionId: aws.String(v.id), ETag: aws.String(etagOf(v.body)),
	}, nil
}

func (f *fakeS3) HeadObject(
	_ context.Context,
	in *s3.HeadObjectInput,
	_ ...func(*s3.Options),
) (*s3.HeadObjectOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.current(aws.ToString(in.Bucket) + "/" + aws.ToString(in.Key))
	if !ok {
		return nil, apiErr("NotFound")
	}
	return &s3.HeadObjectOutput{VersionId: aws.String(v.id), ETag: aws.String(etagOf(v.body))}, nil
}

func (f *fakeS3) GetBucketVersioning(
	context.Context, *s3.GetBucketVersioningInput, ...func(*s3.Options),
) (*s3.GetBucketVersioningOutput, error) {
	out := &s3.GetBucketVersioningOutput{}
	if !f.unversion {
		out.Status = s3types.BucketVersioningStatusEnabled
	}
	return out, nil
}

func (f *fakeS3) PutObject(
	_ context.Context,
	in *s3.PutObjectInput,
	_ ...func(*s3.Options),
) (*s3.PutObjectOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := aws.ToString(in.Bucket) + "/" + aws.ToString(in.Key)
	body, err := io.ReadAll(in.Body)
	if err != nil {
		return nil, err
	}
	cur, exists := f.current(key)
	if in.IfNoneMatch != nil && exists {
		return nil, apiErr("PreconditionFailed")
	}
	if in.IfMatch != nil && (!exists || etagOf(cur.body) != *in.IfMatch) {
		return nil, apiErr("PreconditionFailed")
	}
	f.writes = append(f.writes, "PutObject "+key)
	return &s3.PutObjectOutput{VersionId: aws.String(f.add(key, objVersion{body: body}))}, nil
}

func (f *fakeS3) CopyObject(
	_ context.Context,
	in *s3.CopyObjectInput,
	_ ...func(*s3.Options),
) (*s3.CopyObjectOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	src, version, _ := strings.Cut(aws.ToString(in.CopySource), "?versionId=")
	src, _ = url.PathUnescape(src)
	version, _ = url.QueryUnescape(version)
	for _, v := range f.objects[src] {
		if v.id == version && !v.deleted {
			key := aws.ToString(in.Bucket) + "/" + aws.ToString(in.Key)
			f.writes = append(f.writes, "CopyObject "+key)
			return &s3.CopyObjectOutput{VersionId: aws.String(f.add(key, objVersion{body: v.body}))}, nil
		}
	}
	return nil, apiErr("NoSuchVersion")
}

func (f *fakeS3) DeleteObject(
	_ context.Context,
	in *s3.DeleteObjectInput,
	_ ...func(*s3.Options),
) (*s3.DeleteObjectOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := aws.ToString(in.Bucket) + "/" + aws.ToString(in.Key)
	f.writes = append(f.writes, "DeleteObject "+key)
	f.add(key, objVersion{deleted: true})
	return &s3.DeleteObjectOutput{}, nil
}

// fakeTable is a DynamoDB lock table.
type fakeTable struct {
	mu    sync.Mutex
	items map[string]map[string]string
}

func newFakeTable() *fakeTable { return &fakeTable{items: map[string]map[string]string{}} }

func (f *fakeTable) PutItem(
	_ context.Context, in *dynamodb.PutItemInput, _ ...func(*dynamodb.Options),
) (*dynamodb.PutItemOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	item := map[string]string{}
	for k, v := range in.Item {
		item[k] = v.(*ddbtypes.AttributeValueMemberS).Value
	}
	id := item["LockID"]
	if in.ConditionExpression != nil && f.items[id] != nil {
		return nil, &ddbtypes.ConditionalCheckFailedException{Message: aws.String("exists")}
	}
	f.items[id] = item
	return &dynamodb.PutItemOutput{}, nil
}

func (f *fakeTable) DeleteItem(
	_ context.Context, in *dynamodb.DeleteItemInput, _ ...func(*dynamodb.Options),
) (*dynamodb.DeleteItemOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.items, in.Key["LockID"].(*ddbtypes.AttributeValueMemberS).Value)
	return &dynamodb.DeleteItemOutput{}, nil
}

// fakePlanner returns canned plans per unit directory and keeps the state copies it was given.
type fakePlanner struct {
	mu     sync.Mutex
	plans  map[string][]migrate.ResourceChange
	states map[string][]byte
	hook   func(dir string)
}

func newFakePlanner() *fakePlanner {
	return &fakePlanner{plans: map[string][]migrate.ResourceChange{}, states: map[string][]byte{}}
}

func (p *fakePlanner) Plan(_ context.Context, dir, stateFile string) ([]migrate.ResourceChange, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.hook != nil {
		p.hook(dir)
	}
	if stateFile != "" {
		b, err := os.ReadFile(stateFile)
		if err != nil {
			return nil, err
		}
		p.states[dir] = b
	}
	return p.plans[dir], nil
}

// fakePusher acts like terraform state push: it refuses another lineage or an older serial and
// writes the state re-encoded, as terraform does, so a rollback has to restore the exact bytes.
type fakePusher struct {
	s3    *fakeS3
	units map[string]*migrate.Unit // by dir
	calls []string
}

func (p *fakePusher) Push(_ context.Context, dir, stateFile string) error {
	u := p.units[dir]
	b, err := os.ReadFile(stateFile)
	if err != nil {
		return err
	}
	var next map[string]any
	if err = json.Unmarshal(b, &next); err != nil {
		return err
	}
	p.s3.mu.Lock()
	cur, exists := p.s3.current(u.Bucket + "/" + u.Key)
	p.s3.mu.Unlock()
	if exists {
		var prev map[string]any
		if err = json.Unmarshal(cur.body, &prev); err != nil {
			return err
		}
		if prev["lineage"] != next["lineage"] {
			return fmt.Errorf("cannot import state with lineage %v over unrelated state with lineage %v",
				next["lineage"], prev["lineage"])
		}
		if prev["serial"].(float64) >= next["serial"].(float64) {
			return fmt.Errorf(
				"cannot overwrite existing state with serial %v with serial %v",
				prev["serial"],
				next["serial"],
			)
		}
	}
	next["terraform_version"] = "1.12.9"
	out, err := json.Marshal(next)
	if err != nil {
		return err
	}
	p.calls = append(p.calls, u.Path)
	p.s3.put(u.Key, out)
	return nil
}

// fakePrompter records the confirmations it was asked for and answers with err.
type fakePrompter struct {
	asked []string
	err   error
}

func (p *fakePrompter) ConfirmTyped(_ context.Context, _, want string) error {
	p.asked = append(p.asked, want)
	return p.err
}

// rig wires an engine to the fakes for a set of units.
type rig struct {
	t       *testing.T
	s3      *fakeS3
	table   *fakeTable
	planner *fakePlanner
	pusher  *fakePusher
	units   map[string]*migrate.Unit
}

func newRig(t *testing.T, paths ...string) *rig {
	t.Helper()
	r := &rig{
		t:       t,
		s3:      newFakeS3(),
		table:   newFakeTable(),
		planner: newFakePlanner(),
		units:   map[string]*migrate.Unit{},
	}
	r.pusher = &fakePusher{s3: r.s3, units: map[string]*migrate.Unit{}}
	root := t.TempDir()
	for _, p := range paths {
		dir := filepath.Join(root, filepath.FromSlash(p))
		require.NoError(t, os.MkdirAll(dir, 0o750))
		u := &migrate.Unit{
			Path: p, Dir: dir, Bucket: testBucket, Key: p + "/terraform.tfstate", Region: "eu-west-1",
			Lock: discovery.LockLockfile, Account: "111111111111",
		}
		r.units[p] = u
		r.pusher.units[dir] = u
	}
	return r
}

func (r *rig) seed(path, fixture string) []byte {
	r.t.Helper()
	b, err := os.ReadFile(filepath.Join(fixtures, "states", fixture))
	require.NoError(r.t, err)
	r.s3.put(r.units[path].Key, b)
	return b
}

func (r *rig) engine(write bool) *migrate.Engine {
	r.t.Helper()
	o := migrate.Options{
		Units: r.units,
		Clients: func(_ context.Context, u *migrate.Unit) (*migrate.Clients, error) {
			c := &migrate.Clients{Reader: r.s3, By: testBy}
			if write {
				c.Writer = r.s3
				if u.Table != "" {
					c.Table = r.table
				}
			}
			return c, nil
		},
		Planner: r.planner,
		Clock:   func() time.Time { return time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC) },
	}
	if write {
		o.Pusher = r.pusher
	}
	e, err := migrate.New(o)
	require.NoError(r.t, err)
	r.t.Cleanup(func() { _ = e.Close() })
	return e
}

func (r *rig) captured(path string) *migrate.State {
	r.t.Helper()
	b, ok := r.planner.states[r.units[path].Dir]
	require.True(r.t, ok, "no plan against a state copy of %s", path)
	st, err := migrate.ParseState(b)
	require.NoError(r.t, err)
	return st
}

func (r *rig) history(path string) *migrate.History {
	r.t.Helper()
	b, ok := r.s3.body(r.t, r.units[path].Key+migrate.HistorySuffix)
	if !ok {
		return nil
	}
	h := &migrate.History{}
	require.NoError(r.t, json.Unmarshal(b, h))
	return h
}

func parseFile(t *testing.T, src string, vars map[string]string) *migrate.File {
	t.Helper()
	f, err := migrate.Parse([]byte(src), "split.hcl", nil, vars)
	require.NoError(t, err)
	return f
}

func plan(t *testing.T, name string) []migrate.ResourceChange {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(fixtures, "plans", name))
	require.NoError(t, err)
	changes, err := migrate.ParsePlan(b)
	require.NoError(t, err)
	return changes
}
