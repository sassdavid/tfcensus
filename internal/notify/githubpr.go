package notify

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/sassdavid/tfcensus/internal/ghcomment"
)

// GitHubPR keeps one comment per report kind on a GitHub pull request.
type GitHubPR struct {
	c      *ghcomment.Client
	dryRun bool
}

// NewGitHubPR returns a GitHub pull request comment notifier. Fields of cfg left empty come from
// the GitHub Actions variables that lookup reads.
func NewGitHubPR(hc *http.Client, cfg ghcomment.Config, lookup func(string) (string, bool)) (*GitHubPR, error) {
	cfg, err := ghcomment.FromEnv(cfg, lookup)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", NameGitHubPR, wrapComment(err))
	}
	c, err := ghcomment.New(hc, cfg)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", NameGitHubPR, wrapComment(err))
	}
	return &GitHubPR{c: c, dryRun: cfg.DryRun}, nil
}

// Name returns github-pr-comment.
func (g *GitHubPR) Name() string { return NameGitHubPR }

// Publish writes or edits the report's comment. A report with nothing open turns an existing
// comment into "Nothing to report" and creates none.
func (g *GitHubPR) Publish(ctx context.Context, r *Report) (*Result, error) {
	res := &Result{Notifier: NameGitHubPR, Target: g.c.Target(), DryRun: g.dryRun, Actions: []Action{}}
	open := r.OpenScopes()
	title := "tfcensus " + string(r.Kind)
	body := strings.TrimPrefix(ReportMarkdown(r, math.MaxInt32), "## "+title+"\n\n")
	out, err := g.c.Publish(ctx, ghcomment.Post{
		Kind:  string(r.Kind),
		Title: title,
		Body:  body,
		Open:  len(open) > 0,
		Hint:  "The saved JSON report has the full list.",
	})
	if err != nil {
		return res, wrapComment(err)
	}
	ops := map[ghcomment.Op]Op{
		ghcomment.OpCreate:    OpCreate,
		ghcomment.OpUpdate:    OpUpdate,
		ghcomment.OpResolve:   OpClose,
		ghcomment.OpUnchanged: OpUnchanged,
		ghcomment.OpSkipped:   OpSkipped,
	}
	a := Action{
		Op: ops[out.Op], Kind: r.Kind, Scope: ghcomment.Scope, Title: title, URL: out.URL,
		Detail: fmt.Sprintf("%d open scope(s)", len(open)),
	}
	if out.Op == ghcomment.OpSkipped {
		a.Detail = "nothing to report"
	}
	res.add(a)
	return res, nil
}

// wrapComment adds the notifier error that matches a comment error, so the exit codes match.
func wrapComment(err error) error {
	switch {
	case errors.Is(err, ghcomment.ErrAuth):
		return &alsoError{err: err, also: ErrAuth}
	case errors.Is(err, ghcomment.ErrConfig):
		return &alsoError{err: err, also: ErrConfig}
	}
	return err
}

// alsoError is err that also matches another sentinel, without repeating its text.
type alsoError struct{ err, also error }

func (e *alsoError) Error() string   { return e.err.Error() }
func (e *alsoError) Unwrap() []error { return []error{e.err, e.also} }
