package awstest

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// Router answers AWS requests from canned responses, for writing cassettes of services that
// [Fake] doesn't serve. Each route hands out its stubs in order, so a throttling error followed
// by a success replays the SDK's retry.
type Router struct {
	t      testing.TB
	mu     sync.Mutex
	routes []*route
}

type route struct {
	service, op, match string
	stubs              []Stub
	next               int
}

// NewRouter returns an empty Router. Unused stubs fail the test when it ends.
func NewRouter(t testing.TB) *Router {
	t.Helper()
	r := &Router{t: t}
	t.Cleanup(func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, rt := range r.routes {
			if rt.next < len(rt.stubs) {
				t.Errorf("router: %d unused stubs for %s:%s %q", len(rt.stubs)-rt.next, rt.service, rt.op, rt.match)
			}
		}
	})
	return r
}

// On adds stubs for operation op of service, such as ("ec2", "DescribeSecurityGroupRules").
// When match isn't empty, the request URL or body must contain it. Routes are tried in the
// order they were added.
func (r *Router) On(service, op, match string, stubs ...Stub) *Router {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.routes = append(r.routes, &route{service: service, op: op, match: match, stubs: stubs})
	return r
}

// RoundTrip serves req from the first route with a stub left.
func (r *Router) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
	}
	service, op := Operation(req, body)
	text := req.URL.String() + "\n" + string(body)
	rec := httptest.NewRecorder()
	r.mu.Lock()
	var stub *Stub
	for _, rt := range r.routes {
		if rt.service == service && rt.op == op && strings.Contains(text, rt.match) && rt.next < len(rt.stubs) {
			stub = &rt.stubs[rt.next]
			rt.next++
			break
		}
	}
	r.mu.Unlock()
	if stub == nil {
		r.t.Errorf("router: no stub for %s:%s %s %s", service, op, req.URL, body)
		rec.WriteHeader(http.StatusInternalServerError)
	} else {
		writeStub(rec, contentType(service), *stub)
	}
	resp := rec.Result()
	resp.Request = req
	req.Body = io.NopCloser(bytes.NewReader(body))
	return resp, nil
}

// Operation returns the service and operation of an AWS request: from X-Amz-Target for JSON
// protocols, Action for query protocols, x-id or the path for REST protocols.
func Operation(req *http.Request, body []byte) (string, string) {
	host := req.URL.Host
	service, _, _ := strings.Cut(host, ".")
	if strings.HasPrefix(host, "s3.") || strings.Contains(host, ".s3.") {
		service = "s3"
	}
	if t := req.Header.Get("X-Amz-Target"); t != "" {
		return service, t[strings.LastIndex(t, ".")+1:]
	}
	if form, err := url.ParseQuery(string(body)); err == nil && form.Get("Action") != "" {
		return service, form.Get("Action")
	}
	q := req.URL.Query()
	if id := q.Get("x-id"); id != "" {
		return service, id
	}
	switch service {
	case "s3":
		for sub, op := range map[string]string{
			subPolicy: "GetBucketPolicy", "versioning": "GetBucketVersioning", "lifecycle": "GetBucketLifecycleConfiguration",
			"encryption": "GetBucketEncryption", "publicAccessBlock": "GetPublicAccessBlock",
		} {
			if _, ok := q[sub]; ok {
				return service, op
			}
		}
	case "route53":
		if strings.HasSuffix(req.URL.Path, "/rrset") {
			return service, "ListResourceRecordSets"
		}
		if strings.HasSuffix(req.URL.Path, "/hostedzone") {
			return service, "ListHostedZones"
		}
	}
	return service, strings.TrimPrefix(req.URL.Path, "/")
}

func contentType(service string) string {
	switch service {
	case "config":
		return "application/x-amz-json-1.1"
	case "resource-explorer-2":
		return "application/json"
	case "cloudcontrolapi":
		return "application/x-amz-json-1.0"
	}
	return "text/xml"
}

// JSONError is the body of a JSON protocol error (Config, Resource Explorer, Cloud Control).
func JSONError(code, msg string) string {
	return fmt.Sprintf(`{"__type":%q,"message":%q}`, code, msg)
}

// QueryError is the body of a query protocol error (IAM, STS) and of Route 53's REST-XML errors.
func QueryError(code, msg string) string {
	return `<ErrorResponse><Error><Type>Sender</Type><Code>` + code + `</Code><Message>` + msg +
		`</Message></Error><RequestId>00000000-0000-4000-8000-000000000000</RequestId></ErrorResponse>`
}

// EC2Error is the body of an EC2 error.
func EC2Error(code, msg string) string {
	return `<Response><Errors><Error><Code>` + code + `</Code><Message>` + msg +
		`</Message></Error></Errors><RequestID>00000000-0000-4000-8000-000000000000</RequestID></Response>`
}

// S3Error is the body of an S3 error.
func S3Error(code, msg string) string { return s3ErrorXML(code, msg) }
