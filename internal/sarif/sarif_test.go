package sarif_test

import (
	"bytes"
	"testing"

	"github.com/sassdavid/tfcensus/internal/sarif"
	"github.com/sassdavid/tfcensus/internal/testutil/sarifcheck"
)

func TestWriteIsValidSARIF(t *testing.T) {
	t.Parallel()

	l := sarif.New("tool", "1.0.0", "https://example.org/tool") // prose:ignore
	run := &l.Runs[0]
	run.Tool.Driver.Rules = []sarif.Rule{{
		ID:                   "rule",
		ShortDescription:     sarif.Message{Text: "short"},
		FullDescription:      sarif.Message{Text: "full"},
		Help:                 sarif.Message{Text: "help"},
		DefaultConfiguration: sarif.Configuration{Level: sarif.LevelWarning},
		Properties:           map[string]any{"tags": []string{"security"}, "security-severity": "7.5"},
	}}
	for _, uri := range []string{"mise.toml", sarif.FileURI("C:/src/repo/mise.toml"), sarif.FileURI("/src/a b")} {
		run.Results = append(run.Results, sarif.Result{
			RuleID:  "rule",
			Level:   sarif.LevelWarning,
			Message: sarif.Message{Text: "message"},
			Locations: []sarif.Location{{PhysicalLocation: sarif.PhysicalLocation{
				ArtifactLocation: sarif.ArtifactLocation{URI: uri},
				Region:           &sarif.Region{StartLine: 3},
			}}},
			PartialFingerprints: map[string]string{"test/v1": uri},
		})
	}
	var buf bytes.Buffer
	if err := sarif.Write(&buf, l); err != nil {
		t.Fatal(err)
	}
	if err := sarifcheck.Validate(buf.Bytes()); err != nil {
		t.Fatalf("invalid SARIF: %v\n%s", err, buf.String())
	}
}

func TestFileURI(t *testing.T) {
	t.Parallel()

	tests := []struct{ path, want string }{
		{"mise.toml", "mise.toml"},
		{"team/repo/.github/workflows/ci.yml", "team/repo/.github/workflows/ci.yml"},
		{"../infra/main.tf", "../infra/main.tf"},
		{"dir with space/a#b.tf", "dir%20with%20space/a%23b.tf"},
		{"/home/me/src/repo/mise.toml", "file:///home/me/src/repo/mise.toml"},
		{"C:/Users/me/src/repo/mise.toml", "file:///C:/Users/me/src/repo/mise.toml"},
		{"d:/x", "file:///d:/x"},
		{"//server/share/repo/main.tf", "file://server/share/repo/main.tf"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()
			if got := sarif.FileURI(tt.path); got != tt.want {
				t.Errorf("FileURI(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}
