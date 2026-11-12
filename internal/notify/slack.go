package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Slack limits for one message.
const (
	slackHeaderMax  = 150
	slackSectionMax = 3000
	slackFieldsMax  = 10
	moreRoom        = 40 // the "...and N more" line
	fieldText       = "text"
	fieldType       = "type"
)

// SlackConfig configures [Slack].
type SlackConfig struct {
	Webhook string // incoming webhook URL; a secret, never logged
	When    string // findings or always
	Link    string // URL of the full report
	Top     int    // items listed
	Version string // tfcensus version, in the message context
	DryRun  bool
}

// Slack posts a summary to an incoming webhook. A webhook can't edit or delete what it posted,
// so a message is sent every run that has something to report, or every run with when: always.
type Slack struct {
	cfg SlackConfig
	hc  *http.Client
}

// NewSlack returns a Slack notifier.
func NewSlack(hc *http.Client, cfg SlackConfig) (*Slack, error) {
	if cfg.Webhook == "" && !cfg.DryRun {
		return nil, fmt.Errorf("%s: %w: the webhook variable is empty", NameSlack, ErrConfig)
	}
	if cfg.Top <= 0 {
		cfg.Top = 10
	}
	return &Slack{cfg: cfg, hc: hc}, nil
}

// Name returns slack.
func (s *Slack) Name() string { return NameSlack }

func mrkdwn(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

func text(kind, t string) map[string]any { return map[string]any{fieldType: kind, fieldText: t} }

// SlackPayload builds the webhook message: a header, counts, the top items and a context line.
func SlackPayload(r *Report, cfg SlackConfig) map[string]any {
	open := r.OpenScopes()
	failed := 0
	for _, sc := range open {
		if sc.Failed {
			failed++
		}
	}
	headline := fmt.Sprintf(
		"tfcensus %s: %d %s in %d of %d scope(s)",
		r.Kind,
		r.Items(),
		kindNoun(r.Kind),
		len(open),
		len(r.Scopes),
	)
	fields := []any{
		text("mrkdwn", fmt.Sprintf("*Scopes*\n%d", len(r.Scopes))),
		text("mrkdwn", fmt.Sprintf("*With findings*\n%d", len(open))),
		text("mrkdwn", fmt.Sprintf("*Items*\n%d", r.Items())),
	}
	if failed > 0 {
		fields = append(fields, text("mrkdwn", fmt.Sprintf("*Failed plans*\n%d", failed)))
	}
	blocks := []any{
		map[string]any{fieldType: "header", "text": text("plain_text", truncate(headline, slackHeaderMax))},
		map[string]any{fieldType: "section", "fields": fields[:min(len(fields), slackFieldsMax)]},
	}
	var lines []string
	for _, sc := range open {
		if sc.Failed {
			lines = append(lines, fmt.Sprintf("• `%s` plan failed", mrkdwn(sc.Key)))
		}
		for _, it := range sc.Items {
			lines = append(
				lines,
				fmt.Sprintf(
					"• `%s` %s `%s` (%s)",
					mrkdwn(sc.Key),
					mrkdwn(it.Type),
					mrkdwn(it.Target),
					mrkdwn(it.Level),
				),
			)
		}
	}
	if len(lines) > 0 {
		var list strings.Builder
		shown := 0
		for _, l := range lines {
			if shown == cfg.Top || list.Len()+len(l)+moreRoom > slackSectionMax {
				break
			}
			list.WriteString(l + "\n")
			shown++
		}
		if more := len(lines) - shown; more > 0 {
			fmt.Fprintf(&list, "...and %d more", more)
		}
		blocks = append(
			blocks,
			map[string]any{fieldType: "section", "text": text("mrkdwn", strings.TrimSuffix(list.String(), "\n"))},
		)
	}
	ctx := "tfcensus " + mrkdwn(cfg.Version)
	if cfg.Link != "" {
		ctx = fmt.Sprintf("<%s|Full report> · %s", cfg.Link, ctx)
	}
	blocks = append(blocks, map[string]any{fieldType: "context", "elements": []any{text("mrkdwn", ctx)}})
	return map[string]any{fieldText: headline, "blocks": blocks}
}

// Publish posts the message, or reports that it would.
func (s *Slack) Publish(ctx context.Context, r *Report) (*Result, error) {
	res := &Result{Notifier: NameSlack, Target: "incoming webhook", DryRun: s.cfg.DryRun, Actions: []Action{}}
	payload := SlackPayload(r, s.cfg)
	blocks, _ := payload["blocks"].([]any)
	a := Action{
		Kind:   r.Kind,
		Scope:  "all",
		Title:  fmt.Sprint(payload[fieldText]),
		Detail: fmt.Sprintf("1 message, %d blocks", len(blocks)),
	}
	if len(r.OpenScopes()) == 0 && s.cfg.When != "always" {
		a.Op, a.Detail = OpSkipped, "nothing to report (when: findings)"
		res.add(a)
		return res, nil
	}
	a.Op = OpSend
	if !s.cfg.DryRun {
		if err := s.post(ctx, payload); err != nil {
			return res, err
		}
	}
	res.add(a)
	return res, nil
}

func (s *Slack) post(ctx context.Context, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode slack message: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.Webhook, bytes.NewReader(b))
	if err != nil {
		return errors.New("post slack webhook: invalid webhook URL")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.hc.Do(req)
	if err != nil {
		// The error text holds the URL, and the URL is the secret.
		return fmt.Errorf(
			"post slack webhook: %s",
			strings.ReplaceAll(err.Error(), s.cfg.Webhook, "[webhook]"),
		) //nolint:err113 // the wrapped error would leak the URL
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	if resp.StatusCode != http.StatusOK {
		err = fmt.Errorf(
			"post slack webhook: HTTP %d: %s",
			resp.StatusCode,
			strings.TrimSpace(string(body)),
		) //nolint:err113 // carries the response
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound {
			err = fmt.Errorf("%w: %w", ErrAuth, err)
		}
		return err
	}
	return nil
}
