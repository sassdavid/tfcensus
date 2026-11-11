package gen_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/testutil"
	"github.com/sassdavid/tfcensus/internal/typemap"
	"github.com/sassdavid/tfcensus/internal/typemap/gen"
)

const (
	fixture   = "../../../testdata/typemap"
	awsCommit = "1111111111111111111111111111111111111111"
	ccCommit  = "2222222222222222222222222222222222222222"
)

// checkout copies a fixture provider into a temp dir and gives it a .git directory, which git
// can't track inside this repo. packed puts the branch in packed-refs instead of a loose ref.
func checkout(t *testing.T, name, commit string, packed bool) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "some", "machine", "path", name)
	require.NoError(t, os.CopyFS(dir, os.DirFS(filepath.Join(fixture, name))))
	git := filepath.Join(dir, ".git")
	require.NoError(t, os.MkdirAll(filepath.Join(git, "refs", "heads"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(git, "HEAD"), []byte("ref: refs/heads/main\n"), 0o600))
	if packed {
		body := "# pack-refs with: peeled fully-peeled sorted\n" + commit + " refs/heads/main\n"
		require.NoError(t, os.WriteFile(filepath.Join(git, "packed-refs"), []byte(body), 0o600))
	} else {
		require.NoError(t, os.WriteFile(filepath.Join(git, "refs", "heads", "main"), []byte(commit+"\n"), 0o600))
	}
	return dir
}

func TestGenerate(t *testing.T) {
	t.Parallel()

	first, err := gen.Generate(checkout(t, "aws", awsCommit, false), checkout(t, "awscc", ccCommit, true))
	require.NoError(t, err)
	second, err := gen.Generate(checkout(t, "aws", awsCommit, true), checkout(t, "awscc", ccCommit, false))
	require.NoError(t, err)

	assert.Equal(t, string(first.YAML), string(second.YAML), "same input, different output")
	assert.Equal(t, string(first.License), string(second.License))
	for _, out := range [][]byte{first.YAML, first.License} {
		assert.NotContains(t, string(out), "machine", "output contains a checkout path")
	}
	testutil.Golden(t, filepath.Join(fixture, "generated.golden.yaml"), first.YAML)
	testutil.Golden(t, filepath.Join(fixture, "generated.golden.LICENSE"), first.License)
}

func TestGenerateErrors(t *testing.T) {
	t.Parallel()

	aws := checkout(t, "aws", awsCommit, false)
	cc := checkout(t, "awscc", ccCommit, false)
	_, err := gen.Generate(filepath.Join(t.TempDir(), "missing"), cc)
	require.ErrorIs(t, err, gen.ErrSource)

	require.NoError(t, os.WriteFile(filepath.Join(cc, "LICENSE"), []byte("Copyright X\n\nOther text\n"), 0o600))
	_, err = gen.Generate(aws, cc)
	require.ErrorIs(t, err, gen.ErrSource)
}

var commitRE = regexp.MustCompile(`^[0-9a-f]{40}$`)

// TestCommittedData checks the generated data in the repo: it loads with the curated entries,
// records 40-hex commits, and holds no machine path.
func TestCommittedData(t *testing.T) {
	t.Parallel()

	b, err := os.ReadFile("../data/generated.yaml")
	require.NoError(t, err)
	header, _, _ := strings.Cut(string(b), "\nsources:")
	assert.Contains(t, header, "DO NOT EDIT")
	for _, bad := range []string{"/Users/", "/home/", `C:\`, "/_reference/"} {
		if strings.Contains(string(b), bad) {
			t.Errorf("generated.yaml contains %q", bad)
		}
	}
	_, err = os.Stat("../data/generated.yaml.LICENSE")
	require.NoError(t, err, "generated.yaml needs its MPL-2.0 license next to it")

	m, err := typemap.Load()
	require.NoError(t, err)
	require.Len(t, m.Sources, 2)
	for _, s := range m.Sources {
		assert.Regexp(t, commitRE, s.Commit, s.Repo)
		assert.NotEmpty(t, s.Version, s.Repo)
	}
	assert.Greater(t, m.Generated(), 1500)
}

func TestDefaultDir(t *testing.T) {
	t.Parallel()

	got := gen.DefaultDir(filepath.FromSlash("/home/me"), gen.RepoAWS)
	assert.Equal(t, filepath.FromSlash("/home/me/src/github/_reference/hashicorp/terraform-provider-aws"), got)
}
