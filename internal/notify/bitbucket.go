package notify

import (
	"cmp"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// prScope is the marker scope of a pull request comment, which covers the whole report.
const prScope = "pull-request"

// BitbucketConfig configures [Bitbucket].
type BitbucketConfig struct {
	Workspace   string
	Repo        string // repository slug
	PullRequest int
	APIURL      string // https://api.bitbucket.org/2.0
	Token       string // access token or API token
	Email       string // with an API token: basic auth as this Atlassian account
	DryRun      bool
	WriteGap    time.Duration
	Sleep       func(ctx context.Context, d time.Duration) error
}

// Bitbucket keeps one comment per report kind on a Bitbucket Cloud pull request. Bitbucket
// removed its issue tracker API in 2026, so pull request comments are the only target.
type Bitbucket struct {
	cfg BitbucketConfig
	api *api
}

// NewBitbucket returns a Bitbucket pull request comment notifier.
func NewBitbucket(hc *http.Client, cfg BitbucketConfig) (*Bitbucket, error) {
	if cfg.Workspace == "" || cfg.Repo == "" || cfg.PullRequest <= 0 {
		return nil, fmt.Errorf("%s: %w: set workspace, repo and pull_request, or BITBUCKET_WORKSPACE, "+
			"BITBUCKET_REPO_SLUG and BITBUCKET_PR_ID", NameBitbucket, ErrConfig)
	}
	if cfg.Token == "" && !cfg.DryRun {
		return nil, fmt.Errorf("%s: %w: the token variable is empty", NameBitbucket, ErrConfig)
	}
	cfg.APIURL = strings.TrimSuffix(cmp.Or(cfg.APIURL, "https://api.bitbucket.org/2.0"), "/")
	if cfg.Sleep == nil {
		cfg.Sleep = SleepContext
	}
	a := &api{hc: hc, gap: cfg.WriteGap, sleep: cfg.Sleep}
	switch {
	case cfg.Token == "":
	case cfg.Email != "":
		a.auth = "Basic " + base64.StdEncoding.EncodeToString([]byte(cfg.Email+":"+cfg.Token))
	default:
		a.auth = "Bearer " + cfg.Token
	}
	return &Bitbucket{cfg: cfg, api: a}, nil
}

// Name returns bitbucket-pr-comment.
func (b *Bitbucket) Name() string { return NameBitbucket }

func (b *Bitbucket) target() string {
	return fmt.Sprintf("%s/%s#%d", b.cfg.Workspace, b.cfg.Repo, b.cfg.PullRequest)
}

func (b *Bitbucket) commentsURL() string {
	return fmt.Sprintf("%s/repositories/%s/%s/pullrequests/%d/comments", b.cfg.APIURL,
		url.PathEscape(b.cfg.Workspace), url.PathEscape(b.cfg.Repo), b.cfg.PullRequest)
}

type bbComment struct {
	ID      int  `json:"id"`
	Deleted bool `json:"deleted"`
	Content struct {
		Raw string `json:"raw"`
	} `json:"content"`
	Links struct {
		HTML struct {
			Href string `json:"href"`
		} `json:"html"`
	} `json:"links"`
}

// find returns the comment whose marker names kind, following the next links.
func (b *Bitbucket) find(ctx context.Context, r *Report) (*bbComment, Marker, error) {
	next := b.commentsURL() + "?pagelen=100"
	for next != "" {
		var page struct {
			Values []bbComment `json:"values"`
			Next   string      `json:"next"`
		}
		if _, err := b.api.do(ctx, http.MethodGet, next, nil, &page); err != nil {
			return nil, Marker{}, fmt.Errorf("list comments: %w", err)
		}
		for i := range page.Values {
			c := &page.Values[i]
			if m, ok := ParseMarker(c.Content.Raw); ok && !c.Deleted && m.Kind == r.Kind && m.Scope == prScope {
				return c, m, nil
			}
		}
		next = page.Next
	}
	return nil, Marker{}, nil
}

// Publish writes or edits the report's comment. A report with nothing open turns an existing
// comment into a resolved note and creates none.
func (b *Bitbucket) Publish(ctx context.Context, r *Report) (*Result, error) {
	res := &Result{Notifier: NameBitbucket, Target: b.target(), DryRun: b.cfg.DryRun, Actions: []Action{}}
	cur, curMarker, err := b.find(ctx, r)
	if err != nil {
		return res, err
	}
	open := r.OpenScopes()
	text := ReportMarkdown(r, MaxBody-1000) + //nolint:mnd // room for the marker and footer
		"\n---\nWritten by [tfcensus](https://github.com/sassdavid/tfcensus). The next run edits this comment.\n"
	body, m := withMarker(r.Kind, prScope, text)
	title := fmt.Sprintf("tfcensus %s on pull request %d", r.Kind, b.cfg.PullRequest)
	a := Action{Kind: r.Kind, Scope: prScope, Title: title, Detail: fmt.Sprintf("%d open scope(s)", len(open))}
	switch {
	case cur != nil && curMarker.Hash == m.Hash:
		a.Op, a.URL = OpUnchanged, cur.Links.HTML.Href
	case cur != nil:
		a.Op, a.URL = OpUpdate, cur.Links.HTML.Href
		if len(open) == 0 {
			a.Op = OpClose
		}
		if !b.cfg.DryRun {
			_, err = b.api.do(ctx, http.MethodPut, fmt.Sprintf("%s/%d", b.commentsURL(), cur.ID), bbBody(body), nil)
		}
	case len(open) == 0:
		a.Op, a.Detail = OpSkipped, "nothing to report"
	default:
		a.Op = OpCreate
		if !b.cfg.DryRun {
			var c bbComment
			_, err = b.api.do(ctx, http.MethodPost, b.commentsURL(), bbBody(body), &c)
			a.URL = c.Links.HTML.Href
		}
	}
	if err != nil {
		return res, fmt.Errorf("%s comment: %w", a.Op, err)
	}
	res.add(a)
	return res, nil
}

func bbBody(raw string) map[string]any {
	return map[string]any{"content": map[string]string{"raw": raw}}
}
