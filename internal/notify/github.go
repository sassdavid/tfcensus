package notify

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sassdavid/tfcensus/internal/report"
)

// ManagedLabel is on every issue tfcensus opens; only issues with it are read.
const ManagedLabel = "tfcensus"

// summaryScope is the marker scope of the summary issue.
const summaryScope = "_summary"

const fieldBody = "body"

// GitHubConfig configures [GitHub].
type GitHubConfig struct {
	Repo          string // owner/name
	APIURL        string // https://api.github.com, or a GitHub Enterprise Server API URL
	Token         string
	Labels        []string
	MaxOpen       int
	CloseResolved bool
	Summary       bool
	DryRun        bool
	WriteGap      time.Duration                                    // pause between writes; 0 in tests
	Sleep         func(ctx context.Context, d time.Duration) error // nil means [SleepContext]
}

// GitHub keeps one issue per report kind and scope in a repository.
type GitHub struct {
	cfg GitHubConfig
	api *api
}

// NewGitHub returns a GitHub issues notifier.
func NewGitHub(hc *http.Client, cfg GitHubConfig) (*GitHub, error) {
	if !strings.Contains(cfg.Repo, "/") {
		return nil, fmt.Errorf("%s: %w: set notify.github_issues.repo or GITHUB_REPOSITORY", NameGitHub, ErrConfig)
	}
	if cfg.Token == "" && !cfg.DryRun {
		return nil, fmt.Errorf("%s: %w: the token variable is empty", NameGitHub, ErrConfig)
	}
	cfg.APIURL = strings.TrimSuffix(cmp.Or(cfg.APIURL, "https://api.github.com"), "/")
	cfg.MaxOpen = cmp.Or(cfg.MaxOpen, 10) //nolint:mnd // the documented default
	if cfg.Sleep == nil {
		cfg.Sleep = SleepContext
	}
	a := &api{
		hc: hc, gap: cfg.WriteGap, sleep: cfg.Sleep,
		extra: map[string]string{"Accept": "application/vnd.github+json", "X-GitHub-Api-Version": "2022-11-28"},
	}
	if cfg.Token != "" {
		a.auth = "Bearer " + cfg.Token
	}
	return &GitHub{cfg: cfg, api: a}, nil
}

// Name returns github-issues.
func (g *GitHub) Name() string { return NameGitHub }

type ghIssue struct {
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	PullRequest *struct{} `json:"pull_request"`
	marker      Marker
}

func (g *GitHub) repoURL(path string) string { return g.cfg.APIURL + "/repos/" + g.cfg.Repo + path }

var nextLinkRE = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

// listOpen returns the open issues with the managed label that carry a marker.
func (g *GitHub) listOpen(ctx context.Context) ([]ghIssue, error) {
	next := g.repoURL("/issues?state=open&per_page=100&labels=" + url.QueryEscape(ManagedLabel))
	var out []ghIssue
	for next != "" {
		var page []ghIssue
		h, err := g.api.do(ctx, http.MethodGet, next, nil, &page)
		if err != nil {
			return nil, fmt.Errorf("list issues: %w", err)
		}
		for _, is := range page {
			if is.PullRequest != nil {
				continue
			}
			if m, ok := ParseMarker(is.Body); ok {
				is.marker = m
				out = append(out, is)
			}
		}
		next = ""
		if m := nextLinkRE.FindStringSubmatch(h.Get("Link")); m != nil {
			next = m[1]
		}
	}
	return out, nil
}

// Publish creates, updates and closes issues so the repository matches the report.
func (g *GitHub) Publish(ctx context.Context, r *Report) (*Result, error) {
	res := &Result{Notifier: NameGitHub, Target: g.cfg.Repo, DryRun: g.cfg.DryRun, Actions: []Action{}}
	issues, err := g.listOpen(ctx)
	if err != nil {
		return res, err
	}
	byScope := map[string][]ghIssue{}
	for _, is := range issues {
		if is.marker.Kind == r.Kind {
			byScope[is.marker.Scope] = append(byScope[is.marker.Scope], is)
		}
	}
	run := &ghRun{g: g, res: res, kind: r.Kind}
	for k := range byScope {
		slices.SortFunc(byScope[k], func(a, b ghIssue) int { return a.Number - b.Number })
		if k != summaryScope {
			run.open += len(byScope[k])
		}
	}
	for i := range r.Scopes {
		if err = run.scope(ctx, &r.Scopes[i], byScope[r.Scopes[i].Key]); err != nil {
			return res, err
		}
	}
	if g.cfg.Summary {
		if err = run.summary(ctx, r, byScope[summaryScope]); err != nil {
			return res, err
		}
	}
	return res, nil
}

// ghRun is one Publish call: the open issue count max_open checks, and the scopes it skipped.
type ghRun struct {
	g           *GitHub
	res         *Result
	kind        report.Kind
	open        int
	skipped     []string
	labelsReady bool
}

// scope brings the issues of one scope in line with it. Issues past the first are duplicates,
// which a retried create can leave, and are closed.
func (run *ghRun) scope(ctx context.Context, sc *Scope, list []ghIssue) error {
	g := run.g
	var cur *ghIssue
	if len(list) > 0 {
		cur = &list[0]
		for _, dup := range list[1:] {
			comment := "Closed as a duplicate of #" + strconv.Itoa(cur.Number) + "."
			if err := g.close(ctx, run.res, run.kind, sc.Key, dup, "duplicate", comment); err != nil {
				return err
			}
			run.open--
		}
	}
	switch {
	case sc.Open() && cur != nil:
		return g.update(ctx, run.res, run.kind, sc.Key, sc.Title, cur, sc)
	case sc.Open() && run.open >= g.cfg.MaxOpen:
		run.skipped = append(run.skipped, sc.Key)
		run.res.add(Action{
			Op: OpSkipped, Kind: run.kind, Scope: sc.Key, Title: sc.Title,
			Detail: fmt.Sprintf("max_open (%d) reached", g.cfg.MaxOpen),
		})
	case sc.Open():
		run.open++
		return run.create(ctx, sc)
	case cur != nil && g.cfg.CloseResolved:
		run.open--
		return g.close(ctx, run.res, run.kind, sc.Key, *cur, "completed",
			"Resolved: the "+string(run.kind)+" run found nothing in this scope.")
	}
	return nil
}

func (run *ghRun) create(ctx context.Context, sc *Scope) error {
	if !run.labelsReady {
		if err := run.g.ensureLabels(ctx, run.kind); err != nil {
			return err
		}
		run.labelsReady = true
	}
	return run.g.create(ctx, run.res, run.kind, sc)
}

func (g *GitHub) labels(kind report.Kind) []string {
	return append([]string{ManagedLabel, ManagedLabel + ":" + string(kind)}, g.cfg.Labels...)
}

// ensureLabels creates the labels new issues get when the repository lacks them.
func (g *GitHub) ensureLabels(ctx context.Context, kind report.Kind) error {
	if g.cfg.DryRun {
		return nil
	}
	for _, l := range g.labels(kind) {
		_, err := g.api.do(ctx, http.MethodGet, g.repoURL("/labels/"+url.PathEscape(l)), nil, nil)
		if se, ok := errors.AsType[*statusError](err); ok && se.status == http.StatusNotFound {
			body := map[string]string{"name": l, "color": "5319e7", "description": "Managed by tfcensus"}
			_, err = g.api.do(ctx, http.MethodPost, g.repoURL("/labels"), body, nil)
			if se2, ok2 := errors.AsType[*statusError](err); ok2 && se2.status == http.StatusUnprocessableEntity {
				err = nil // created by a concurrent run
			}
		}
		if err != nil {
			return fmt.Errorf("label %s: %w", l, err)
		}
	}
	return nil
}

func (g *GitHub) create(ctx context.Context, res *Result, kind report.Kind, sc *Scope) error {
	body, _ := IssueBody(kind, sc)
	a := Action{Op: OpCreate, Kind: kind, Scope: sc.Key, Title: sc.Title, Detail: itemsDetail(sc)}
	if !g.cfg.DryRun {
		var is ghIssue
		req := map[string]any{"title": sc.Title, fieldBody: body, "labels": g.labels(kind)}
		if _, err := g.api.do(ctx, http.MethodPost, g.repoURL("/issues"), req, &is); err != nil {
			return fmt.Errorf("create issue for %s: %w", sc.Key, err)
		}
		a.URL = is.HTMLURL
	}
	res.add(a)
	return nil
}

func (g *GitHub) update(
	ctx context.Context,
	res *Result,
	kind report.Kind,
	scope, title string,
	cur *ghIssue,
	sc *Scope,
) error {
	body, m := IssueBody(kind, sc)
	a := Action{Kind: kind, Scope: scope, Title: title, URL: cur.HTMLURL}
	if m.Hash == cur.marker.Hash && title == cur.Title {
		a.Op = OpUnchanged
		res.add(a)
		return nil
	}
	a.Op, a.Detail = OpUpdate, fmt.Sprintf("#%d, %s", cur.Number, itemsDetail(sc))
	if !g.cfg.DryRun {
		req := map[string]any{"title": title, fieldBody: body}
		if _, err := g.api.do(
			ctx,
			http.MethodPatch,
			g.repoURL(fmt.Sprintf("/issues/%d", cur.Number)),
			req,
			nil,
		); err != nil {
			return fmt.Errorf("update issue #%d: %w", cur.Number, err)
		}
	}
	res.add(a)
	return nil
}

func itemsDetail(sc *Scope) string {
	if sc.Failed {
		return fmt.Sprintf("plan failed, %d item(s)", len(sc.Items))
	}
	return fmt.Sprintf("%d item(s)", len(sc.Items))
}

func (g *GitHub) close(
	ctx context.Context,
	res *Result,
	kind report.Kind,
	scope string,
	is ghIssue,
	reason, comment string,
) error {
	detail := fmt.Sprintf("#%d resolved", is.Number)
	if reason == "duplicate" {
		detail = fmt.Sprintf("#%d duplicate", is.Number)
	}
	if !g.cfg.DryRun {
		path := fmt.Sprintf("/issues/%d", is.Number)
		if _, err := g.api.do(
			ctx,
			http.MethodPost,
			g.repoURL(path+"/comments"),
			map[string]string{fieldBody: comment},
			nil,
		); err != nil {
			return fmt.Errorf("comment on issue #%d: %w", is.Number, err)
		}
		req := map[string]string{"state": "closed", "state_reason": reason}
		if _, err := g.api.do(ctx, http.MethodPatch, g.repoURL(path), req, nil); err != nil {
			return fmt.Errorf("close issue #%d: %w", is.Number, err)
		}
	}
	res.add(Action{Op: OpClose, Kind: kind, Scope: scope, Title: is.Title, Detail: detail, URL: is.HTMLURL})
	return nil
}

// summary keeps one issue per kind that lists the open scopes and those over max_open.
func (run *ghRun) summary(ctx context.Context, r *Report, list []ghIssue) error {
	sc := &Scope{Key: summaryScope, Title: "tfcensus: " + string(r.Kind) + " summary", Items: []Item{}}
	for _, s := range r.OpenScopes() {
		detail := itemsDetail(&s)
		if slices.Contains(run.skipped, s.Key) {
			detail += "; no issue (max_open reached)"
		}
		sc.Items = append(sc.Items, Item{Level: "open", Type: string(r.Kind), Target: s.Key, Detail: detail})
	}
	if len(list) > 0 {
		return run.g.update(ctx, run.res, r.Kind, summaryScope, sc.Title, &list[0], sc)
	}
	return run.create(ctx, sc)
}
