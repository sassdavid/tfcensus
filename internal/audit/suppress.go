package audit

import (
	"errors"
	"fmt"
	"time"

	"github.com/sassdavid/tfcensus/internal/config"
	"github.com/sassdavid/tfcensus/internal/iampolicy"
)

const dateLayout = "2006-01-02"

// Suppressions are the compiled audit.suppress entries.
type Suppressions struct {
	entries []suppression
}

type suppression struct {
	config.Suppression

	index   int
	expires time.Time // the first instant after the last day; zero means never
}

// NewSuppressions validates the entries: each needs a reason and at least one of check, bucket,
// unit or key, and expires must be a date.
func NewSuppressions(entries []config.Suppression) (*Suppressions, error) {
	s := &Suppressions{}
	var errs []error
	for i, e := range entries {
		entry := suppression{Suppression: e, index: i}
		if e.Reason == "" {
			errs = append(errs, fmt.Errorf("audit.suppress[%d]: reason is required", i))
		}
		if e.Check == "" && e.Bucket == "" && e.Unit == "" && e.Key == "" {
			errs = append(errs, fmt.Errorf("audit.suppress[%d]: set check, bucket, unit or key", i))
		}
		if e.Expires != "" {
			day, err := time.Parse(dateLayout, e.Expires)
			if err != nil {
				errs = append(errs, fmt.Errorf("audit.suppress[%d]: expires %q is not a YYYY-MM-DD date", i, e.Expires))
			}
			entry.expires = day.AddDate(0, 0, 1)
		}
		s.entries = append(s.entries, entry)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return s, nil
}

// Apply splits findings into active and suppressed at now, and returns them with warnings. An
// expired entry stops matching, like an expired ownership rule, and produces a warning that counts
// the findings it would have hidden.
func (s *Suppressions) Apply(findings []Finding, now time.Time) ([]Finding, []Finding, []string) {
	var active, suppressed []Finding
	var warnings []string
	expiredHits := make([]int, len(s.entries))
	for _, f := range findings {
		hit := false
		for i := range s.entries {
			e := &s.entries[i]
			if !e.matches(&f) {
				continue
			}
			if !e.expires.IsZero() && !now.Before(e.expires) {
				expiredHits[i]++
				continue
			}
			f.Suppressed = &Suppressed{Reason: e.Reason, Expires: e.Expires}
			suppressed = append(suppressed, f)
			hit = true
			break
		}
		if !hit {
			active = append(active, f)
		}
	}
	for i := range s.entries {
		e := &s.entries[i]
		if e.expires.IsZero() || now.Before(e.expires) {
			continue
		}
		warnings = append(warnings, fmt.Sprintf(
			"audit.suppress[%d] (%s) expired on %s and no longer applies: %d finding(s) reported again",
			e.index, e.label(), e.Expires, expiredHits[i]))
	}
	return active, suppressed, warnings
}

func (e *suppression) matches(f *Finding) bool {
	key := f.Key
	if key == "" {
		key = f.Table
	}
	for _, m := range []struct{ pattern, value string }{
		{e.Check, f.Rule}, {e.Bucket, f.Bucket}, {e.Unit, f.Unit}, {e.Key, key},
	} {
		if m.pattern != "" && !iampolicy.Glob(m.pattern, m.value, false) {
			return false
		}
	}
	return true
}

func (e *suppression) label() string {
	for _, v := range []struct{ name, value string }{
		{"check", e.Check}, {"bucket", e.Bucket}, {"unit", e.Unit}, {"key", e.Key},
	} {
		if v.value != "" {
			return v.name + " " + v.value
		}
	}
	return ""
}
