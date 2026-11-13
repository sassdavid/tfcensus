package complete_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"

	"github.com/sassdavid/tfcensus/internal/complete"
)

func choices() []complete.Choice {
	return []complete.Choice{{"auto", "pick one"}, {"always", ""}, {"never", "no"}}
}

func TestJoinAndUsage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		cs   []complete.Choice
		want string
	}{
		{nil, ""},
		{choices()[:1], "auto"},
		{choices()[:2], "auto or always"},
		{choices(), "auto, always or never"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, complete.Join(tt.cs))
	}
	assert.Equal(t, "mode: auto, always or never (config x)", complete.Usage("mode: {values} (config x)", choices()))
}

func TestCompletionFuncs(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{}
	tests := []struct {
		name       string
		f          cobra.CompletionFunc
		toComplete string
		want       []string
		directive  cobra.ShellCompDirective
	}{
		{
			"enum",
			complete.Enum(choices()),
			"a",
			[]string{"auto\tpick one", "always"},
			cobra.ShellCompDirectiveNoFileComp,
		},
		{"enum none", complete.Enum(choices()), "x", nil, cobra.ShellCompDirectiveNoFileComp},
		{
			"list first", complete.List(choices()), "",
			[]string{"auto\tpick one", "always", "never\tno"},
			cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace,
		},
		{
			"list rest", complete.List(choices()), "auto,",
			[]string{"auto,always", "auto,never\tno"},
			cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace,
		},
		{
			"list prefix", complete.List(choices()), "never,a",
			[]string{"never,auto\tpick one", "never,always"},
			cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace,
		},
		{
			"files ext",
			complete.Files("yaml", "yml"),
			"",
			[]string{"yaml", "yml"},
			cobra.ShellCompDirectiveFilterFileExt,
		},
		{"files any", complete.Files(), "", nil, cobra.ShellCompDirectiveDefault},
		{"dirs", complete.Dirs(), "", nil, cobra.ShellCompDirectiveFilterDirs},
		{"no files", complete.NoFiles, "", nil, cobra.ShellCompDirectiveNoFileComp},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, directive := tt.f(cmd, nil, tt.toComplete)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.directive, directive)
		})
	}
}

func TestDynamic(t *testing.T) {
	t.Parallel()
	list := func(context.Context, *cobra.Command, string) ([]complete.Choice, error) { return choices(), nil }
	fail := func(context.Context, *cobra.Command, string) ([]complete.Choice, error) {
		return nil, errors.New("no such file")
	}
	hang := func(ctx context.Context, _ *cobra.Command, _ string) ([]complete.Choice, error) {
		<-ctx.Done()
		return choices(), nil
	}
	tests := []struct {
		name       string
		f          cobra.CompletionFunc
		toComplete string
		want       []string
		directive  cobra.ShellCompDirective
	}{
		{"prefix", complete.Dynamic(time.Minute, list), "n", []string{"never\tno"}, cobra.ShellCompDirectiveNoFileComp},
		{
			"list", complete.DynamicList(time.Minute, list), "never,al",
			[]string{"never,always"},
			cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace,
		},
		{"error", complete.Dynamic(time.Minute, fail), "", nil, cobra.ShellCompDirectiveError},
		{"timeout", complete.Dynamic(time.Millisecond, hang), "", nil, cobra.ShellCompDirectiveError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cmd := &cobra.Command{}
			cmd.SetContext(t.Context())
			got, directive := tt.f(cmd, nil, tt.toComplete)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.directive, directive)
		})
	}
}

func TestIsRequest(t *testing.T) {
	t.Parallel()
	root := &cobra.Command{Use: "tool"}
	run := &cobra.Command{Use: "run"}
	comp := &cobra.Command{Use: cobra.ShellCompRequestCmd}
	noDesc := &cobra.Command{Use: cobra.ShellCompNoDescRequestCmd}
	carapace := &cobra.Command{Use: "_carapace"}
	spec := &cobra.Command{Use: "spec"}
	carapace.AddCommand(spec)
	root.AddCommand(run, comp, noDesc, carapace)
	assert.False(t, complete.IsRequest(root))
	assert.False(t, complete.IsRequest(run))
	assert.True(t, complete.IsRequest(comp))
	assert.True(t, complete.IsRequest(noDesc))
	assert.True(t, complete.IsRequest(carapace))
	assert.True(t, complete.IsRequest(spec))
}
