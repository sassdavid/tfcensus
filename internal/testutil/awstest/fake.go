package awstest

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3mem"

	"github.com/sassdavid/tfcensus/internal/testutil"
	"github.com/sassdavid/tfcensus/internal/testutil/vcr"
)

// Fake answers S3 and STS requests in process: S3 from gofakes3, STS from a few canned XML
// responses. Requests keep their real AWS host names, so recorded cassettes replay against the
// SDK's default endpoints.
type Fake struct {
	t       testing.TB
	backend *s3mem.Backend
	s3      http.Handler
	now     time.Time

	// Keys maps an access key ID to its account for sts:GetCallerIdentity. Keys issued by
	// AssumeRole carry their account, so they need no entry.
	Keys map[string]string
	// Deny lists buckets whose every request gets 403 AccessDenied.
	Deny []string
}

// assumedKeyPrefix starts the access key IDs AssumeRole hands out; the account follows it.
const assumedKeyPrefix = "ASIAFAKE"

// NewFake returns a Fake whose objects carry now as their LastModified time.
func NewFake(t testing.TB, now time.Time) *Fake {
	t.Helper()
	clock := gofakes3.FixedTimeSource(now)
	backend := s3mem.New(s3mem.WithTimeSource(clock))
	faker := gofakes3.New(backend,
		gofakes3.WithTimeSource(clock),
		gofakes3.WithTimeSkewLimit(0),
		gofakes3.WithHostBucket(true),
		gofakes3.WithLogger(gofakes3.DiscardLog()),
	)
	return &Fake{t: t, backend: backend, s3: faker.Server(), now: now, Keys: map[string]string{}}
}

// Put stores body at s3://bucket/key, creating the bucket when needed.
func (f *Fake) Put(bucket, key string, body []byte) {
	f.t.Helper()
	if ok, _ := f.backend.BucketExists(bucket); !ok {
		if err := f.backend.CreateBucket(bucket); err != nil {
			f.t.Fatal(err)
		}
	}
	// gofakes3's PUT handler sets Last-Modified; the backend alone doesn't.
	meta := map[string]string{"Last-Modified": f.now.UTC().Format(http.TimeFormat)}
	if _, err := f.backend.PutObject(bucket, key, meta, bytes.NewReader(body), int64(len(body)), nil); err != nil {
		f.t.Fatal(err)
	}
}

// RoundTrip serves req in process.
func (f *Fake) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	switch {
	case strings.HasPrefix(req.URL.Host, "sts."):
		f.sts(rec, req)
	case f.denied(req.URL.Host):
		writeS3Error(rec, http.StatusForbidden, "AccessDenied", "Access Denied")
	default:
		// gofakes3 takes the bucket from Host, which client requests leave empty, and rewrites
		// the URL in place.
		in := req.Clone(req.Context())
		in.Host = req.URL.Host
		f.s3.ServeHTTP(rec, in)
	}
	resp := rec.Result()
	resp.Request = req
	return resp, nil
}

func (f *Fake) denied(host string) bool {
	bucket, _, _ := strings.Cut(host, ".")
	return slices.Contains(f.Deny, bucket)
}

func writeS3Error(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>`+"\n"+
		`<Error><Code>%s</Code><Message>%s</Message><RequestId>0000000000000000</RequestId></Error>`, code, msg)
}

var (
	credentialRE = regexp.MustCompile(`Credential=([A-Z0-9]+)/`)
	roleRE       = regexp.MustCompile(`^arn:aws:iam::(\d{12}):role/(.+)$`)
)

func (f *Fake) sts(w http.ResponseWriter, req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	form, _ := url.ParseQuery(string(body))
	key := ""
	if m := credentialRE.FindStringSubmatch(req.Header.Get("Authorization")); m != nil {
		key = m[1]
	}
	w.Header().Set("Content-Type", "text/xml")
	switch form.Get("Action") {
	case "GetCallerIdentity":
		account, ok := f.Keys[key]
		if rest, assumed := strings.CutPrefix(key, assumedKeyPrefix); assumed {
			account, ok = rest, true
		}
		if !ok {
			stsError(w, "InvalidClientTokenId", "The security token included in the request is invalid.")
			return
		}
		//nolint:gosec // a test fake; account comes from Keys or a key ID it issued
		_, _ = fmt.Fprintf(w, stsIdentityXML, account, account)
	case "AssumeRole":
		m := roleRE.FindStringSubmatch(form.Get("RoleArn"))
		if m == nil {
			stsError(w, "AccessDenied", "not authorized to perform sts:AssumeRole")
			return
		}
		_, _ = fmt.Fprintf(w, stsAssumeXML, m[1], m[2], assumedKeyPrefix+m[1])
	default:
		stsError(w, "InvalidAction", "unsupported action")
	}
}

func stsError(w http.ResponseWriter, code, msg string) {
	w.WriteHeader(http.StatusForbidden)
	_, _ = fmt.Fprintf(w, `<ErrorResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">`+
		`<Error><Type>Sender</Type><Code>%s</Code><Message>%s</Message></Error>`+
		`<RequestId>00000000-0000-4000-8000-000000000000</RequestId></ErrorResponse>`, code, msg)
}

const stsIdentityXML = `<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">
  <GetCallerIdentityResult>
    <Arn>arn:aws:iam::%s:user/tfcensus-readonly</Arn>
    <UserId>AIDAEXAMPLEUSERID</UserId>
    <Account>%s</Account>
  </GetCallerIdentityResult>
  <ResponseMetadata><RequestId>00000000-0000-4000-8000-000000000000</RequestId></ResponseMetadata>
</GetCallerIdentityResponse>
`

const stsAssumeXML = `<AssumeRoleResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/">
  <AssumeRoleResult>
    <AssumedRoleUser>
      <Arn>arn:aws:sts::%[1]s:assumed-role/%[2]s/tfcensus</Arn>
      <AssumedRoleId>AROAEXAMPLEROLEID:tfcensus</AssumedRoleId>
    </AssumedRoleUser>
    <Credentials>
      <AccessKeyId>%[3]s</AccessKeyId>
      <SecretAccessKey>fake-assumed-secret</SecretAccessKey>
      <SessionToken>fake-session-token</SessionToken>
      <Expiration>2099-01-01T00:00:00Z</Expiration>
    </Credentials>
  </AssumeRoleResult>
  <ResponseMetadata><RequestId>00000000-0000-4000-8000-000000000000</RequestId></ResponseMetadata>
</AssumeRoleResponse>
`

var durationRE = regexp.MustCompile(`(?m)^(\s+duration:) .*$`)

// WriteCassette returns a transport that sends every request to upstream and records it, through
// [Scrubber], into the cassette at path when golden files are being updated. Otherwise it
// returns nil and the caller skips: the cassette is replayed by other tests. Durations are
// zeroed so that rewriting an unchanged cassette leaves no diff.
func WriteCassette(t testing.TB, path string, upstream http.RoundTripper) http.RoundTripper {
	t.Helper()
	if !testutil.Updating() {
		return nil
	}
	rec, err := vcr.New(path, vcr.Options{Record: true, Real: upstream, Scrubber: Scrubber()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err = rec.Stop(); err != nil {
			t.Errorf("save cassette %s: %v", path, err)
			return
		}
		file := strings.TrimSuffix(path, ".yaml") + ".yaml"
		b, readErr := os.ReadFile(file)
		if readErr != nil {
			t.Error(readErr)
			return
		}
		//nolint:gosec // the cassette path the test passed in
		if err = os.WriteFile(file, durationRE.ReplaceAll(b, []byte("$1 0s")), 0o600); err != nil {
			t.Error(err)
		}
	})
	return rec
}

// LoadManifest puts the states listed in manifest into f. Each line names a bucket, a key and a
// file next to the manifest; blank lines and lines starting with # are skipped.
func (f *Fake) LoadManifest(manifest string) {
	f.t.Helper()
	b, err := os.ReadFile(manifest)
	if err != nil {
		f.t.Fatal(err)
	}
	for line := range strings.Lines(string(b)) {
		fields := strings.Fields(line)
		if len(fields) == 0 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		const want = 3
		if len(fields) != want {
			f.t.Fatalf("%s: want bucket, key and file: %q", manifest, line)
		}
		body, readErr := os.ReadFile(filepath.Join(filepath.Dir(manifest), fields[2]))
		if readErr != nil {
			f.t.Fatal(readErr)
		}
		f.Put(fields[0], fields[1], body)
	}
}
