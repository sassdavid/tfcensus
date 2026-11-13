package cli

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/buildinfo"
	"github.com/sassdavid/tfcensus/internal/complete"
	"github.com/sassdavid/tfcensus/internal/config"
	"github.com/sassdavid/tfcensus/internal/ghcomment"
	"github.com/sassdavid/tfcensus/internal/httpx"
	"github.com/sassdavid/tfcensus/internal/notify"
	"github.com/sassdavid/tfcensus/internal/render"
	"github.com/sassdavid/tfcensus/internal/report"
)

const (
	flagNotify       = "notify"
	flagDryRunNotify = "dry-run-notify"
	flagRule         = "rule"
)

// githubWriteGap is the pause between GitHub writes that GitHub's REST API best practices ask for.
const githubWriteGap = time.Second

type notifyArgs struct {
	from   []string
	only   []string
	dryRun bool
}

// notifyData is the data of notify --format json.
type notifyData struct {
	DryRun  bool             `json:"dry_run"`
	Results []*notify.Result `json:"results"`
}

func newNotifyCmd(d *app.Deps, g *globals) *cobra.Command {
	var a notifyArgs
	cmd := &cobra.Command{
		Use:   "notify",
		Short: "Publish saved scan, drift and audit reports to Slack, GitHub, Bitbucket or files",
		Long: `notify reads the JSON that scan, drift and audit print with --format json and publishes it.
It never collects anything itself.

  slack                 one message per report to an incoming webhook, with counts and the top
                        findings; sent only when there is something to report (notify.slack.when)
  github-issues         one issue per scope: per unit for drift, per account for scan and audit.
                        A re-run updates the body only when it changed, and closes the issue when
                        the run covered the scope and found nothing. notify.github_issues.max_open
                        caps new issues
  github-pr-comment     one comment per report on a GitHub pull request, edited in place. The
                        pull request comes from GITHUB_EVENT_PATH; a report with nothing open
                        turns the comment into "Nothing to report"
  bitbucket-pr-comment  one comment per report on a Bitbucket Cloud pull request, edited in place
  file                  tfcensus-<kind>.md and .json in notify.file.dir, rewritten only on change

Every issue body and comment starts with a hidden marker that names the report kind, the scope
and a hash of the body, so runs find their own issues and skip unchanged ones. Only issues
with the tfcensus label are read.

Secrets come from the environment only: the variables named by notify.slack.webhook_env,
notify.github_issues.token_env, notify.github_pr_comment.token_env and
notify.bitbucket_pr_comment.token_env. They are never
logged; -vvv shows request headers with Authorization redacted.

--dry-run-notify reads the existing issues and comments with GET requests and prints what
would change, writing nothing.

Exit codes: 0 when every notifier succeeded, 4 when every failure was a refused token, and 1
for any other failure.`,
		Example: `  tfcensus drift --format json > drift.json; tfcensus notify --from drift.json --notify github-issues
  tfcensus notify --from scan.json --from audit.json --dry-run-notify
  tfcensus notify --from drift.json --notify github-pr-comment   # in a pull_request workflow
  GITHUB_TOKEN=... tfcensus notify --from drift.json --notify github-issues,slack`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := g.outFormat.Check(render.FormatTTY, render.FormatPlain, render.FormatJSON); err != nil {
				return usageError(err)
			}
			if len(a.from) == 0 {
				return usageError(fmt.Errorf("pass at least one --%s file", flagFrom))
			}
			res, err := g.loadConfig(cmd, d)
			if err != nil {
				return err
			}
			return runNotify(cmd, d, g.outFormat, &res.Config, a)
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&a.from, flagFrom, nil, "scan, drift or audit JSON file (repeatable)")
	f.StringSliceVar(
		&a.only,
		flagNotify,
		nil,
		complete.Usage("notifiers to run instead of notify.enabled: {values}", notifierChoices()),
	)
	registerCompletions(cmd, map[string]cobra.CompletionFunc{
		flagFrom:   savedFiles(),
		flagNotify: complete.List(notifierChoices()),
	})
	f.BoolVar(&a.dryRun, flagDryRunNotify, false, "print what would change; read with GET only and write nothing")
	return cmd
}

func runNotify(cmd *cobra.Command, d *app.Deps, format render.Format, cfg *config.Config, a notifyArgs) error {
	names := a.only
	if len(names) == 0 {
		names = cfg.Notify.Enabled
	}
	if len(names) == 0 {
		return usageError(fmt.Errorf("no notifier: pass --%s or set notify.enabled", flagNotify))
	}
	for _, n := range names {
		if !slices.Contains(notify.Names(), n) {
			return usageError(fmt.Errorf("unknown notifier %q: want %s", n, strings.Join(notify.Names(), ", ")))
		}
	}
	reports, err := notifyReports(cfg, a.from)
	if err != nil {
		return err
	}
	data := notifyData{DryRun: a.dryRun, Results: []*notify.Result{}}
	env := render.NewEnvelope(buildinfo.Get().Version, d.Clock(), &data)
	var failures []error
	for _, name := range names {
		failures = append(failures, publishAll(cmd, d, cfg, name, reports, a.dryRun, &data, &env)...)
	}
	if format == render.FormatJSON {
		err = render.JSON(d.Stdout, env)
	} else {
		err = writeNotifyText(d.Stdout, &data, env.Errors)
	}
	if err != nil {
		return err
	}
	return notifyExit(failures)
}

// notifyReports reads the saved reports and splits them into scopes.
func notifyReports(cfg *config.Config, from []string) ([]*notify.Report, error) {
	inputs, err := report.ReadAll(from)
	if err != nil {
		return nil, usageError(err)
	}
	opts := notify.Options{Categories: cfg.Notify.Categories, MinSeverity: cfg.Notify.MinSeverity}
	reports := make([]*notify.Report, 0, len(inputs))
	for _, in := range inputs {
		r, rerr := notify.FromSaved(in, opts)
		if rerr != nil {
			return nil, usageError(rerr)
		}
		reports = append(reports, r)
	}
	return reports, nil
}

// publishAll runs one notifier over every report, adding results and errors to the envelope.
func publishAll(
	cmd *cobra.Command, d *app.Deps, cfg *config.Config, name string, reports []*notify.Report, dryRun bool,
	data *notifyData, env *render.Envelope,
) []error {
	ctx := cmd.Context()
	n, err := newNotifier(d, cfg, name, dryRun)
	if err != nil {
		env.Errors = append(env.Errors, render.Error{Target: name, Message: err.Error()})
		return []error{err}
	}
	var failures []error
	for _, r := range reports {
		res, perr := n.Publish(ctx, r)
		if res != nil && len(res.Actions) > 0 {
			data.Results = append(data.Results, res)
		}
		if perr != nil {
			failures = append(failures, perr)
			env.Errors = append(env.Errors, render.Error{Target: name, Message: perr.Error()})
			d.Logger.ErrorContext(ctx, "notifier failed", "notifier", name, "kind", r.Kind, "error", perr)
			continue
		}
		d.Logger.InfoContext(ctx, "notifier done", "notifier", name, "kind", r.Kind, "changes", res.Changed())
	}
	return failures
}

func notifyExit(failures []error) error {
	if len(failures) == 0 {
		return nil
	}
	allAuth := true
	for _, f := range failures {
		allAuth = allAuth && errors.Is(f, notify.ErrAuth)
	}
	err := fmt.Errorf("%d notifier failure(s)", len(failures))
	if allAuth {
		return &exitError{code: ExitAuth, err: err}
	}
	if slices.ContainsFunc(failures, func(f error) bool { return errors.Is(f, notify.ErrConfig) }) {
		return usageError(errors.Join(failures...))
	}
	return err
}

func writeNotifyText(w io.Writer, data *notifyData, errs []render.Error) error {
	var b strings.Builder
	for _, r := range data.Results {
		fmt.Fprintf(&b, "%s  %s\n", r.Notifier, r.Target)
		rows := make([][]string, 0, len(r.Actions))
		for _, a := range r.Actions {
			rows = append(rows, []string{"  " + string(a.Op), string(a.Kind), a.Scope, cmp.Or(a.Detail, a.Title)})
		}
		if err := writeRows(&b, rows); err != nil {
			return err
		}
	}
	for _, e := range errs {
		fmt.Fprintf(&b, "error: %s: %s\n", e.Target, e.Message)
	}
	if data.DryRun {
		fmt.Fprintf(&b, "\nno changes made (--%s)\n", flagDryRunNotify)
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	return nil
}

// newNotifier builds one notifier from the config, with secrets from the environment.
func newNotifier(d *app.Deps, cfg *config.Config, name string, dryRun bool) (notify.Notifier, error) {
	nc := &cfg.Notify
	env := func(key string) string {
		v, _ := d.Env.Lookup(key)
		return v
	}
	switch name {
	case notify.NameFile:
		return notify.NewFile(notify.FileConfig{Dir: nc.File.Dir, Formats: nc.File.Formats, DryRun: dryRun}), nil
	case notify.NameSlack:
		hook := env(nc.Slack.WebhookEnv)
		var secret []string
		if u, err := url.Parse(hook); err == nil && u.Hostname() != "" {
			secret = []string{u.Hostname()}
		}
		hc, err := notifyClient(d, secret)
		if err != nil {
			return nil, err
		}
		return notify.NewSlack(hc, notify.SlackConfig{ //nolint:wrapcheck // the error names the notifier
			Webhook: hook,
			When:    nc.Slack.When,
			Link:    nc.Slack.Link,
			Top:     nc.Top,
			Version: buildinfo.Get().Version,
			DryRun:  dryRun,
		})
	case notify.NameGitHub:
		hc, err := notifyClient(d, nil)
		if err != nil {
			return nil, err
		}
		gh := nc.GitHubIssues
		return notify.NewGitHub(hc, notify.GitHubConfig{ //nolint:wrapcheck // the error names the notifier
			Repo: cmp.Or(gh.Repo, env("GITHUB_REPOSITORY")), APIURL: cmp.Or(gh.APIURL, env("GITHUB_API_URL")),
			Token: env(gh.TokenEnv), Labels: gh.Labels, MaxOpen: gh.MaxOpen, CloseResolved: gh.CloseResolved,
			Summary: gh.Summary, DryRun: dryRun, WriteGap: githubWriteGap,
		})
	case notify.NameGitHubPR:
		hc, err := notifyClient(d, nil)
		if err != nil {
			return nil, err
		}
		pr := nc.GitHubPRComment
		return notify.NewGitHubPR(hc, ghcomment.Config{ //nolint:wrapcheck // the error names the notifier
			Repo: pr.Repo, APIURL: pr.APIURL, PullRequest: pr.PullRequest, Token: env(pr.TokenEnv), DryRun: dryRun,
		}, d.Env.Lookup)
	case notify.NameBitbucket:
		hc, err := notifyClient(d, nil)
		if err != nil {
			return nil, err
		}
		bb := nc.BitbucketPRComment
		pr := bb.PullRequest
		if pr == 0 {
			pr, _ = strconv.Atoi(env("BITBUCKET_PR_ID"))
		}
		email := ""
		if bb.EmailEnv != "" {
			email = env(bb.EmailEnv)
		}
		return notify.NewBitbucket(hc, notify.BitbucketConfig{ //nolint:wrapcheck // the error names the notifier
			Workspace: cmp.Or(
				bb.Workspace,
				env("BITBUCKET_WORKSPACE"),
			),
			Repo:        cmp.Or(bb.Repo, env("BITBUCKET_REPO_SLUG")),
			PullRequest: pr,
			APIURL:      bb.APIURL,
			Token:       env(bb.TokenEnv),
			Email:       email,
			DryRun:      dryRun,
		})
	}
	return nil, usageError(fmt.Errorf("unknown notifier %q", name))
}

func notifyClient(d *app.Deps, secretHosts []string) (*http.Client, error) {
	rt := d.HTTPTransport
	if ca, _ := d.Env.Lookup("SSL_CERT_FILE"); rt == nil && ca != "" {
		var err error
		if rt, err = httpx.CATransport(ca); err != nil {
			return nil, fmt.Errorf("SSL_CERT_FILE: %w", err)
		}
	}
	return httpx.New(httpx.Options{
		Logger:          d.Logger,
		Transport:       rt,
		UserAgent:       fmt.Sprintf("tfcensus/%s (+https://github.com/sassdavid/tfcensus)", buildinfo.Get().Version),
		SecretPathHosts: secretHosts,
	}), nil
}

func notifierChoices() []complete.Choice {
	desc := map[string]string{
		notify.NameSlack:     "one message per report to an incoming webhook",
		notify.NameGitHub:    "one GitHub issue per scope",
		notify.NameGitHubPR:  "one comment per report on a GitHub pull request",
		notify.NameBitbucket: "one comment per report on one pull request",
		notify.NameFile:      "tfcensus-<kind>.md and .json files",
	}
	var out []complete.Choice
	for _, n := range notify.Names() {
		out = append(out, complete.Choice{Value: n, Desc: desc[n]})
	}
	return out
}
