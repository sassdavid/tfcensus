package ghcomment

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/sassdavid/tfcensus/internal/render"
)

// DefaultAPIURL is the GitHub REST API on github.com.
const DefaultAPIURL = "https://api.github.com"

// Scope is the marker scope of a pull request comment, which covers the whole report.
const Scope = "pull-request"

// ErrAuth is wrapped when GitHub refuses the token.
var ErrAuth = errors.New("authentication failed")

// ErrConfig is wrapped when the repository, the pull request or the token is missing.
var ErrConfig = errors.New("pull request comment not configured")

// footer ends every comment. It names no version, so an upgrade doesn't rewrite comments.
const footer = "\n---\nWritten by [tfcensus](https://github.com/sassdavid/tfcensus). The next run edits this comment.\n"

// room is what a comment keeps for the marker and the footer under MaxChars.
const room = 500

const maxErrorBody = 300

// Config says which pull request to comment on.
type Config struct {
	Repo        string // owner/name
	PullRequest int
	APIURL      string // DefaultAPIURL when empty
	Token       string
	DryRun      bool // read with GET only and write nothing
	MaxChars    int  // the longest comment; render.DefaultMDMaxChars when 0
}

// FromEnv fills the empty fields of c from the variables GitHub Actions sets: GITHUB_REPOSITORY,
// GITHUB_API_URL, and the pull request number in the GITHUB_EVENT_PATH file.
func FromEnv(c Config, lookup func(string) (string, bool)) (Config, error) {
	env := func(k string) string {
		v, _ := lookup(k)
		return v
	}
	c.Repo = cmp.Or(c.Repo, env("GITHUB_REPOSITORY"))
	c.APIURL = cmp.Or(c.APIURL, env("GITHUB_API_URL"))
	if c.PullRequest == 0 {
		path := env("GITHUB_EVENT_PATH")
		if path == "" {
			return c, fmt.Errorf("%w: GITHUB_EVENT_PATH is empty, so the pull request is unknown", ErrConfig)
		}
		n, err := PullRequestFromEvent(path)
		if err != nil {
			return c, err
		}
		c.PullRequest = n
	}
	return c, nil
}

// PullRequestFromEvent reads the pull request number from a GitHub Actions event file. It knows
// pull_request, pull_request_target and issue_comment events on a pull request.
func PullRequestFromEvent(path string) (int, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("%w: read GITHUB_EVENT_PATH: %w", ErrConfig, err)
	}
	var ev struct {
		PullRequest *struct {
			Number int `json:"number"`
		} `json:"pull_request"`
		Issue *struct {
			Number      int             `json:"number"`
			PullRequest json.RawMessage `json:"pull_request"`
		} `json:"issue"`
	}
	if err = json.Unmarshal(b, &ev); err != nil {
		return 0, fmt.Errorf("%w: parse GITHUB_EVENT_PATH: %w", ErrConfig, err)
	}
	switch {
	case ev.PullRequest != nil && ev.PullRequest.Number > 0:
		return ev.PullRequest.Number, nil
	case ev.Issue != nil && ev.Issue.Number > 0 && len(ev.Issue.PullRequest) > 0:
		return ev.Issue.Number, nil
	}
	return 0, fmt.Errorf("%w: the GITHUB_EVENT_PATH event names no pull request; "+
		"run on pull_request events or set the pull request number", ErrConfig)
}

// Client edits the comments of one pull request.
type Client struct {
	hc  *http.Client
	cfg Config
}

// New checks cfg and returns a client. Pass an httpx client: it retries reads and never resends
// a POST after a server error.
func New(hc *http.Client, cfg Config) (*Client, error) {
	owner, name, ok := strings.Cut(cfg.Repo, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return nil, fmt.Errorf("%w: repository %q: want owner/name, or set GITHUB_REPOSITORY", ErrConfig, cfg.Repo)
	}
	if cfg.PullRequest <= 0 {
		return nil, fmt.Errorf("%w: no pull request number", ErrConfig)
	}
	if cfg.Token == "" && !cfg.DryRun {
		return nil, fmt.Errorf("%w: the token is empty", ErrConfig)
	}
	cfg.APIURL = strings.TrimSuffix(cmp.Or(cfg.APIURL, DefaultAPIURL), "/")
	cfg.MaxChars = cmp.Or(cfg.MaxChars, render.DefaultMDMaxChars)
	return &Client{hc: hc, cfg: cfg}, nil
}

// Target names the pull request as owner/name#number.
func (c *Client) Target() string { return fmt.Sprintf("%s#%d", c.cfg.Repo, c.cfg.PullRequest) }

// Post is one report to publish.
type Post struct {
	Kind  string // one comment per kind, such as drift
	Title string // the comment's heading
	Body  string // Markdown under the heading, cut to fit when too long
	Open  bool   // false when the report found nothing: the comment becomes "Nothing to report"
	Hint  string // ends the truncation footer, such as "Run tfcensus drift --format json."
}

// Op is what Publish did, or would do in a dry run.
type Op string

// Operations.
const (
	OpCreate    Op = "create"
	OpUpdate    Op = "update"
	OpResolve   Op = "resolve"   // an existing comment now says "Nothing to report"
	OpUnchanged Op = "unchanged" // the hash matches, so nothing was written
	OpSkipped   Op = "skipped"   // nothing to report and no comment yet
)

// Result says what Publish did.
type Result struct {
	Op     Op     `json:"op"`
	Kind   string `json:"kind"`
	Target string `json:"target"`
	URL    string `json:"url,omitempty"`
	DryRun bool   `json:"dry_run"`
}

// Marker is the hidden first line of a comment. Kind and Scope find the comment again; Hash says
// whether its content changed.
type Marker struct {
	Kind  string `json:"kind"`
	Scope string `json:"scope"`
	Hash  string `json:"hash"`
}

const markerPrefix = "<!-- tfcensus:v1 "

var markerRE = regexp.MustCompile(`<!-- tfcensus:v1 (\{[^\n]*?\}) -->`)

// String renders the marker as an HTML comment, which GitHub doesn't show.
func (m Marker) String() string {
	b, _ := json.Marshal(m) //nolint:errchkjson // three strings always encode
	// A value with "--" could end the comment early; JSON reads \u002d back as "-".
	return markerPrefix + strings.ReplaceAll(string(b), "--", `-\u002d`) + " -->"
}

// ParseMarker finds the marker in a comment body.
func ParseMarker(body string) (Marker, bool) {
	sub := markerRE.FindStringSubmatch(body)
	if sub == nil {
		return Marker{}, false
	}
	var m Marker
	if err := json.Unmarshal([]byte(sub[1]), &m); err != nil || m.Kind == "" || m.Scope == "" {
		return Marker{}, false
	}
	return m, true
}

// Render returns the comment body for p and its marker. The hash covers the heading and the
// Markdown, not the footer.
func (c *Client) Render(p Post) (string, Marker) {
	text := "## " + p.Title + "\n\n"
	if p.Open {
		text += render.TruncateMarkdown(p.Body, c.cfg.MaxChars-room-len([]rune(text)), p.Hint)
	} else {
		text += "Nothing to report.\n"
	}
	sum := sha256.Sum256([]byte(text))
	m := Marker{Kind: p.Kind, Scope: Scope, Hash: "sha256:" + hex.EncodeToString(sum[:12])}
	return m.String() + "\n\n" + text + footer, m
}

type comment struct {
	ID      int64  `json:"id"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
}

// Publish creates, edits or leaves the comment for p.Kind. A report with nothing open turns an
// existing comment into "Nothing to report" and creates none.
func (c *Client) Publish(ctx context.Context, p Post) (*Result, error) {
	res := &Result{Kind: p.Kind, Target: c.Target(), DryRun: c.cfg.DryRun}
	cur, curMarker, err := c.find(ctx, p.Kind)
	if err != nil {
		return res, err
	}
	body, m := c.Render(p)
	switch {
	case cur != nil && curMarker.Hash == m.Hash:
		res.Op, res.URL = OpUnchanged, cur.HTMLURL
		return res, nil
	case cur != nil:
		res.Op, res.URL = OpUpdate, cur.HTMLURL
		if !p.Open {
			res.Op = OpResolve
		}
		if !c.cfg.DryRun {
			err = c.do(ctx, http.MethodPatch, c.repoURL()+"/issues/comments/"+strconv.FormatInt(cur.ID, 10),
				map[string]string{"body": body}, nil)
		}
	case !p.Open:
		res.Op = OpSkipped
		return res, nil
	default:
		res.Op = OpCreate
		if !c.cfg.DryRun {
			var created comment
			err = c.do(ctx, http.MethodPost, c.commentsURL(), map[string]string{"body": body}, &created)
			res.URL = created.HTMLURL
		}
	}
	if err != nil {
		return res, fmt.Errorf("%s comment on %s: %w", res.Op, c.Target(), err)
	}
	return res, nil
}

func (c *Client) repoURL() string {
	owner, name, _ := strings.Cut(c.cfg.Repo, "/")
	return c.cfg.APIURL + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)
}

func (c *Client) commentsURL() string {
	return c.repoURL() + "/issues/" + strconv.Itoa(c.cfg.PullRequest) + "/comments"
}

// find returns the oldest comment whose marker names kind, following the Link header.
func (c *Client) find(ctx context.Context, kind string) (*comment, Marker, error) {
	next := c.commentsURL() + "?per_page=100"
	for next != "" {
		var page []comment
		h, err := c.get(ctx, next, &page)
		if err != nil {
			return nil, Marker{}, fmt.Errorf("list comments on %s: %w", c.Target(), err)
		}
		for i := range page {
			if m, ok := ParseMarker(page[i].Body); ok && m.Kind == kind && m.Scope == Scope {
				return &page[i], m, nil
			}
		}
		next = nextLink(h.Get("Link"))
	}
	return nil, Marker{}, nil
}

var nextRE = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

func nextLink(link string) string {
	if m := nextRE.FindStringSubmatch(link); m != nil {
		return m[1]
	}
	return ""
}

func (c *Client) get(ctx context.Context, u string, out any) (http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	return c.send(req, out)
}

func (c *Client) do(ctx context.Context, method, u string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	_, err = c.send(req, out)
	return err
}

func (c *Client) send(req *http.Request, out any) (http.Header, error) {
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-Github-Api-Version", "2022-11-28")
	if c.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", req.Method, req.URL.Path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		err = fmt.Errorf("%s %s: HTTP %d: %s", req.Method, req.URL.Path, resp.StatusCode, strings.TrimSpace(string(b)))
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			err = fmt.Errorf("%w: %w", ErrAuth, err)
		}
		return resp.Header, err
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.Header, nil
	}
	if err = json.NewDecoder(resp.Body).Decode(out); err != nil {
		return resp.Header, fmt.Errorf("%s %s: decode response: %w", req.Method, req.URL.Path, err)
	}
	return resp.Header, nil
}
