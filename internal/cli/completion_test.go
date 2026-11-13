package cli_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/cli"
)

// completionAllowlist names flags, as "command path --flag", that may have no completion, each
// with the reason.
func completionAllowlist() map[string]string {
	return map[string]string{}
}

// TestCompletionCoverage walks the command tree: every flag that takes a value has a completion
// function, a file or directory annotation, or an allowlist entry, and every command completes
// its arguments.
func TestCompletionCoverage(t *testing.T) {
	t.Parallel()
	d, _, _ := newDeps(t)
	root := cli.NewRootCmd(d)
	allow := completionAllowlist()
	used := map[string]bool{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		check := func(f *pflag.Flag) {
			if f.NoOptDefVal != "" { // bool and count flags take no value
				return
			}
			key := c.CommandPath() + " --" + f.Name
			_, hasFunc := c.GetFlagCompletionFunc(f.Name)
			_, hasExt := f.Annotations[cobra.BashCompFilenameExt]
			_, hasDir := f.Annotations[cobra.BashCompSubdirsInDir]
			if reason, ok := allow[key]; ok {
				used[key] = true
				assert.NotEmpty(t, reason, "%s: allowlist entry needs a reason", key)
				return
			}
			assert.True(t, hasFunc || hasExt || hasDir, "%s has no completion", key)
		}
		c.LocalNonPersistentFlags().VisitAll(check)
		if !c.HasParent() {
			c.PersistentFlags().VisitAll(check)
		}
		if c.Runnable() && !c.HasSubCommands() {
			assert.True(t, c.ValidArgsFunction != nil || len(c.ValidArgs) > 0,
				"%s has no argument completion", c.CommandPath())
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
	for key := range allow {
		assert.True(t, used[key], "allowlist entry %s matches no flag", key)
	}
}

// TestCompleteIsFast runs __complete in-process: a warm run stays under 100 ms and writes only
// results to stdout.
func TestCompleteIsFast(t *testing.T) {
	t.Parallel()
	const limit = 100 * time.Millisecond
	best := time.Hour
	for range 5 {
		d, stdout, stderr := newDeps(t)
		start := time.Now()
		code := cli.Execute(t.Context(), d, []string{cobra.ShellCompRequestCmd, "--color", ""})
		best = min(best, time.Since(start))
		require.Equal(t, cli.ExitOK, code, stderr.String())
		lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
		assert.Equal(t, []string{
			"auto\tcolor on a terminal unless NO_COLOR is set",
			"always\talways color",
			"never\tnever color",
			":4",
		}, lines)
	}
	assert.Less(t, best, limit)
}

// TestCompleteSkipsSetup checks that a completion request opens no log file: the root's
// PersistentPreRunE does no I/O for __complete.
func TestCompleteSkipsSetup(t *testing.T) {
	t.Parallel()
	d, stdout, _ := newDeps(t)
	dir := t.TempDir()
	code := cli.Execute(
		t.Context(),
		d,
		[]string{
			cobra.ShellCompRequestCmd,
			"--log-file",
			filepath.Join(dir, "no", "such", "dir", "log"),
			"--color",
			"",
		},
	)
	require.Equal(t, cli.ExitOK, code)
	assert.Contains(t, stdout.String(), "always")
}
