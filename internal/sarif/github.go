package sarif

import (
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
)

// WriteGitHub writes each unsuppressed result of l as a GitHub Actions workflow command, which
// GitHub shows as an annotation: error results as errors, warnings as warnings, the rest as notices.
func WriteGitHub(w io.Writer, l *Log) error {
	var b strings.Builder
	for _, run := range l.Runs {
		for _, r := range run.Results {
			if len(r.Suppressions) > 0 {
				continue
			}
			b.WriteString(githubCommand(run.Tool.Driver.Name, &r))
		}
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("write annotations: %w", err)
	}
	return nil
}

func githubCommand(tool string, r *Result) string {
	level := "notice"
	switch r.Level {
	case "error", "warning":
		level = r.Level
	}
	var props []string
	if len(r.Locations) > 0 {
		loc := r.Locations[0].PhysicalLocation
		if file := annotationFile(loc.ArtifactLocation.URI); file != "" {
			props = append(props, "file="+githubProperty(file))
			if loc.Region != nil && loc.Region.StartLine > 0 {
				props = append(props, "line="+strconv.Itoa(loc.Region.StartLine))
			}
		}
	}
	props = append(props, "title="+githubProperty(tool+" "+r.RuleID))
	return "::" + level + " " + strings.Join(props, ",") + "::" + githubEscape(r.Message.Text) + "\n"
}

// annotationFile returns the repository path of a relative artifact URI. GitHub can't place an
// annotation on a file URI, so those get none.
func annotationFile(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return ""
	}
	return u.Path
}

func githubEscape(s string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A").Replace(s)
}

func githubProperty(s string) string {
	return strings.NewReplacer(":", "%3A", ",", "%2C").Replace(githubEscape(s))
}
