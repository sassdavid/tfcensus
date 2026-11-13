package notify_test

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sassdavid/tfcensus/internal/httpx"
)

// call is one request a fake server received.
type call struct {
	Method, Path string
	Auth         string
	Body         string
}

type calls struct {
	mu   sync.Mutex
	list []call
}

func (c *calls) record(r *http.Request) string {
	b, _ := io.ReadAll(r.Body)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.list = append(
		c.list,
		call{Method: r.Method, Path: r.URL.Path, Auth: r.Header.Get("Authorization"), Body: string(b)},
	)
	return string(b)
}

// writes returns the requests that weren't GETs, and forgets every request.
func (c *calls) writes() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, x := range c.list {
		if x.Method != http.MethodGet {
			out = append(out, x.Method+" "+x.Path)
		}
	}
	c.list = nil
	return out
}

func client(t *testing.T) *http.Client {
	t.Helper()
	return httpx.New(httpx.Options{
		Logger:       slog.New(slog.DiscardHandler),
		UserAgent:    "tfcensus/test",
		RetryWaitMin: time.Millisecond,
		RetryWaitMax: 2 * time.Millisecond,
	})
}

type fakeIssue struct {
	Number   int      `json:"number"`
	Title    string   `json:"title"`
	Body     string   `json:"body"`
	State    string   `json:"state"`
	Reason   string   `json:"state_reason,omitempty"`
	Labels   []string `json:"-"`
	Comments []string `json:"-"`
	HTMLURL  string   `json:"html_url"`
}

// fakeGitHub is the part of the GitHub REST API the issues notifier uses. It pages issue lists
// two at a time, so pagination always runs.
type fakeGitHub struct {
	calls

	mu     sync.Mutex
	srv    *httptest.Server
	issues []*fakeIssue
	labels map[string]bool
	prs    int // open pull requests carrying the label, which the list must skip
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{labels: map[string]bool{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

const ghRepo = "/repos/acme/infra-live"

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	body := f.record(r)
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer gh-test-token" {
		http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, ghRepo)
	switch {
	case r.Method == http.MethodGet && path == "/issues":
		f.list(w, r)
	case r.Method == http.MethodPost && path == "/issues":
		var in struct {
			Title  string   `json:"title"`
			Body   string   `json:"body"`
			Labels []string `json:"labels"`
		}
		_ = json.Unmarshal([]byte(body), &in)
		n := len(f.issues) + 1
		is := &fakeIssue{
			Number: n, Title: in.Title, Body: in.Body, State: "open", Labels: in.Labels,
			HTMLURL: fmt.Sprintf("https://github.example/acme/infra-live/issues/%d", n),
		}
		f.issues = append(f.issues, is)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(is)
	case strings.HasPrefix(path, "/issues/") && strings.HasSuffix(path, "/comments") && r.Method == http.MethodPost:
		is := f.issue(strings.TrimSuffix(strings.TrimPrefix(path, "/issues/"), "/comments"))
		var in struct {
			Body string `json:"body"`
		}
		_ = json.Unmarshal([]byte(body), &in)
		is.Comments = append(is.Comments, in.Body)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, "{}")
	case strings.HasPrefix(path, "/issues/") && r.Method == http.MethodPatch:
		is := f.issue(strings.TrimPrefix(path, "/issues/"))
		var in map[string]string
		_ = json.Unmarshal([]byte(body), &in)
		if v, ok := in["title"]; ok {
			is.Title = v
		}
		if v, ok := in["body"]; ok {
			is.Body = v
		}
		if v, ok := in["state"]; ok {
			is.State, is.Reason = v, in["state_reason"]
		}
		_ = json.NewEncoder(w).Encode(is)
	case strings.HasPrefix(path, "/labels/") && r.Method == http.MethodGet:
		if !f.labels[strings.TrimPrefix(r.URL.RawPath, ghRepo+"/labels/")] &&
			!f.labels[strings.TrimPrefix(path, "/labels/")] {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, "{}")
	case path == "/labels" && r.Method == http.MethodPost:
		var in struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal([]byte(body), &in)
		f.labels[in.Name] = true
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, "{}")
	default:
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	}
}

func (f *fakeGitHub) issue(num string) *fakeIssue {
	n, _ := strconv.Atoi(num)
	return f.issues[n-1]
}

func (f *fakeGitHub) list(w http.ResponseWriter, r *http.Request) {
	label := r.URL.Query().Get("labels")
	var open []any
	for range f.prs {
		open = append(open, map[string]any{"number": 900, "title": "pr", "body": "<!-- tfcensus:v1 " +
			`{"kind":"drift","scope":"tg-live/dev/vpc","hash":"x"} -->`, "pull_request": map[string]any{}})
	}
	for _, is := range f.issues {
		if is.State == "open" && slices.Contains(is.Labels, label) {
			open = append(open, is)
		}
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	page = max(page, 1)
	const size = 2
	start, end := (page-1)*size, min(page*size, len(open))
	if end < len(open) {
		w.Header().
			Set("Link", fmt.Sprintf(`<%s%s/issues?state=open&labels=%s&page=%d>; rel="next"`, f.srv.URL, ghRepo, label, page+1))
	}
	if start > len(open) {
		start = len(open)
	}
	_ = json.NewEncoder(w).Encode(open[start:end])
}

func (f *fakeGitHub) open() map[string]*fakeIssue {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]*fakeIssue{}
	for _, is := range f.issues {
		if is.State == "open" {
			out[is.Title] = is
		}
	}
	return out
}

type fakeComment struct {
	ID      int  `json:"id"`
	Deleted bool `json:"deleted"`
	Content struct {
		Raw string `json:"raw"`
	} `json:"content"`
}

// fakeBitbucket is the pull request comments part of the Bitbucket Cloud API, one comment per page.
type fakeBitbucket struct {
	calls

	mu       sync.Mutex
	srv      *httptest.Server
	comments []*fakeComment
}

const bbComments = "/2.0/repositories/acme/infra-live/pullrequests/7/comments"

func newFakeBitbucket(t *testing.T) *fakeBitbucket {
	t.Helper()
	f := &fakeBitbucket{}
	f.comments = append(f.comments, &fakeComment{ID: 1})
	f.comments[0].Content.Raw = "Looks good to me."
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeBitbucket) serve(w http.ResponseWriter, r *http.Request) {
	body := f.record(r)
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer bb-test-token" {
		http.Error(w, `{"type":"error"}`, http.StatusUnauthorized)
		return
	}
	var in struct {
		Content struct {
			Raw string `json:"raw"`
		} `json:"content"`
	}
	_ = json.Unmarshal([]byte(body), &in)
	switch {
	case r.Method == http.MethodGet && r.URL.Path == bbComments:
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		page = max(page, 1)
		out := map[string]any{"values": []any{}}
		if page <= len(f.comments) {
			out["values"] = []any{f.comments[page-1]}
		}
		if page < len(f.comments) {
			out["next"] = fmt.Sprintf("%s%s?pagelen=100&page=%d", f.srv.URL, bbComments, page+1)
		}
		_ = json.NewEncoder(w).Encode(out)
	case r.Method == http.MethodPost && r.URL.Path == bbComments:
		c := &fakeComment{ID: len(f.comments) + 1}
		c.Content.Raw = in.Content.Raw
		f.comments = append(f.comments, c)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(c)
	case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, bbComments+"/"):
		id, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, bbComments+"/"))
		f.comments[id-1].Content.Raw = in.Content.Raw
		_ = json.NewEncoder(w).Encode(f.comments[id-1])
	default:
		http.Error(w, `{"type":"error"}`, http.StatusNotFound)
	}
}

func (f *fakeBitbucket) ours() []*fakeComment {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*fakeComment
	for _, c := range f.comments {
		if strings.Contains(c.Content.Raw, "tfcensus:v1") {
			out = append(out, c)
		}
	}
	return out
}

// fakeGitHubPR is the part of the GitHub REST API the pull request comment notifier uses. It pages
// comments one at a time with a Link header.
type fakeGitHubPR struct {
	calls

	mu       sync.Mutex
	srv      *httptest.Server
	comments []map[string]any
}

const ghPRComments = "/repos/acme/infra-live/issues/7/comments"

func newFakeGitHubPR(t *testing.T) *fakeGitHubPR {
	t.Helper()
	f := &fakeGitHubPR{comments: []map[string]any{{"id": 1, "body": "Looks good to me.", "html_url": "u1"}}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGitHubPR) bodies() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.comments))
	for i, c := range f.comments {
		out[i], _ = c["body"].(string)
	}
	return out
}

func (f *fakeGitHubPR) serve(w http.ResponseWriter, r *http.Request) {
	body := f.record(r)
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer gh-test-token" {
		http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
		return
	}
	var in struct {
		Body string `json:"body"`
	}
	_ = json.Unmarshal([]byte(body), &in)
	switch {
	case r.Method == http.MethodGet && r.URL.Path == ghPRComments:
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		page = max(page, 1)
		out := []any{}
		if page <= len(f.comments) {
			out = append(out, f.comments[page-1])
		}
		if page < len(f.comments) {
			w.Header().
				Set("Link", fmt.Sprintf(`<%s%s?per_page=100&page=%d>; rel="next"`, f.srv.URL, ghPRComments, page+1))
		}
		_ = json.NewEncoder(w).Encode(out)
	case r.Method == http.MethodPost && r.URL.Path == ghPRComments:
		id := len(f.comments) + 1
		c := map[string]any{"id": id, "body": in.Body, "html_url": "u" + strconv.Itoa(id)}
		f.comments = append(f.comments, c)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(c)
	case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/repos/acme/infra-live/issues/comments/"):
		id, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/repos/acme/infra-live/issues/comments/"))
		f.comments[id-1]["body"] = in.Body
		_ = json.NewEncoder(w).Encode(f.comments[id-1])
	default:
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	}
}
