package sarif_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/sarif"
)

func TestWriteGitHub(t *testing.T) {
	t.Parallel()
	l := sarif.New("tfcensus", "1.0.0", "")
	loc := func(uri string, line int) []sarif.Location {
		pl := sarif.PhysicalLocation{ArtifactLocation: sarif.ArtifactLocation{URI: uri}}
		if line > 0 {
			pl.Region = &sarif.Region{StartLine: line}
		}
		return []sarif.Location{{PhysicalLocation: pl}}
	}
	l.Runs[0].Results = []sarif.Result{
		{
			RuleID: "eol", Level: "error", Message: sarif.Message{Text: "kubectl 1.30: end of life\n50% done"},
			Locations: loc("mise.toml", 3),
		},
		{RuleID: "drift", Level: "warning", Message: sarif.Message{Text: "a, b: c"}, Locations: loc("a/b,c.hcl", 0)},
		{RuleID: "breaking", Level: "note", Message: sarif.Message{Text: "n"}, Locations: loc("file:///abs/x.toml", 2)},
		{
			RuleID: "hidden", Level: "warning", Message: sarif.Message{Text: "h"},
			Suppressions: []sarif.Suppression{{Kind: "external", Status: "accepted", Justification: "noise"}},
		},
		{RuleID: "failed", Level: "error", Message: sarif.Message{Text: "no location"}},
	}
	var b bytes.Buffer
	require.NoError(t, sarif.WriteGitHub(&b, l))
	assert.Equal(t, "::error file=mise.toml,line=3,title=tfcensus eol::kubectl 1.30: end of life%0A50%25 done\n"+
		"::warning file=a/b%2Cc.hcl,title=tfcensus drift::a, b: c\n"+
		"::notice title=tfcensus breaking::n\n"+
		"::error title=tfcensus failed::no location\n", b.String())
}
