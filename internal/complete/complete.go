package complete

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// Timeout is how long a dynamic completion may take before it gives up.
const Timeout = time.Second

// carapaceCmd is the hidden command that carapace.Gen adds.
const carapaceCmd = "_carapace"

// Choice is one allowed value of an enum flag.
type Choice struct {
	Value string
	Desc  string
}

// Values returns the values of cs in order.
func Values(cs []Choice) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Value
	}
	return out
}

// Join lists the values of cs for help text, as in "a, b or c".
func Join(cs []Choice) string {
	vs := Values(cs)
	switch len(vs) {
	case 0:
		return ""
	case 1:
		return vs[0]
	default:
		return strings.Join(vs[:len(vs)-1], ", ") + " or " + vs[len(vs)-1]
	}
}

// Usage returns text with every "{values}" replaced by [Join] of cs.
func Usage(text string, cs []Choice) string {
	return strings.ReplaceAll(text, "{values}", Join(cs))
}

// Enum returns a completion function that offers cs with descriptions and no file names.
func Enum(cs []Choice) cobra.CompletionFunc {
	return func(_ *cobra.Command, _ []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		return withPrefix(cs, "", toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}

// List returns a completion function for a comma-separated list of cs values. It completes the
// last element, keeps the ones before it and leaves out values already in the list.
func List(cs []Choice) cobra.CompletionFunc {
	return func(_ *cobra.Command, _ []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		done, last := "", toComplete
		if i := strings.LastIndex(toComplete, ","); i >= 0 {
			done, last = toComplete[:i+1], toComplete[i+1:]
		}
		used := strings.Split(strings.TrimSuffix(done, ","), ",")
		var rest []Choice
		for _, c := range cs {
			if !slices.Contains(used, c.Value) {
				rest = append(rest, c)
			}
		}
		return withPrefix(rest, done, last), cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveNoSpace
	}
}

// Files returns a completion function for file names with one of exts (without the dot), or
// any file when exts is empty. fish and PowerShell ignore the filter and list every file.
func Files(exts ...string) cobra.CompletionFunc {
	return func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(exts) == 0 {
			return nil, cobra.ShellCompDirectiveDefault
		}
		return slices.Clone(exts), cobra.ShellCompDirectiveFilterFileExt
	}
}

// Dirs returns a completion function for directory names.
func Dirs() cobra.CompletionFunc {
	return func(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveFilterDirs
	}
}

// NoFiles is a completion function for free-form values: it offers nothing, not even file names.
func NoFiles(*cobra.Command, []string, string) ([]cobra.Completion, cobra.ShellCompDirective) {
	return nil, cobra.ShellCompDirectiveNoFileComp
}

// Source lists choices from local data. cmd gives access to the flags parsed so far.
type Source func(ctx context.Context, cmd *cobra.Command, toComplete string) ([]Choice, error)

// Dynamic returns a completion function that offers what src lists, filtered by prefix. When src
// fails or takes longer than timeout, it offers nothing and returns ShellCompDirectiveError.
func Dynamic(timeout time.Duration, src Source) cobra.CompletionFunc {
	return dynamic(timeout, src, false)
}

// DynamicList is [Dynamic] for a comma-separated list, completing the last element like [List].
func DynamicList(timeout time.Duration, src Source) cobra.CompletionFunc {
	return dynamic(timeout, src, true)
}

func dynamic(timeout time.Duration, src Source, list bool) cobra.CompletionFunc {
	return func(cmd *cobra.Command, _ []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
		done, last := "", toComplete
		if i := strings.LastIndex(toComplete, ","); list && i >= 0 {
			done, last = toComplete[:i+1], toComplete[i+1:]
		}
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		type result struct {
			cs  []Choice
			err error
		}
		ch := make(chan result, 1)
		go func() {
			cs, err := src(ctx, cmd, last)
			ch <- result{cs, err}
		}()
		select {
		case r := <-ch:
			if r.err != nil {
				cobra.CompDebugln(r.err.Error(), true)
				return nil, cobra.ShellCompDirectiveError
			}
			directive := cobra.ShellCompDirectiveNoFileComp
			if list {
				directive |= cobra.ShellCompDirectiveNoSpace
			}
			return withPrefix(r.cs, done, last), directive
		case <-ctx.Done():
			cobra.CompDebugln("completion stopped: "+ctx.Err().Error(), true)
			return nil, cobra.ShellCompDirectiveError
		}
	}
}

// IsRequest reports whether cmd answers a completion request (cobra's __complete or carapace's
// _carapace) instead of running a command.
func IsRequest(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		switch c.Name() {
		case cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd, carapaceCmd:
			return true
		}
	}
	return false
}

func withPrefix(cs []Choice, done, prefix string) []cobra.Completion {
	var out []cobra.Completion
	for _, c := range cs {
		switch {
		case !strings.HasPrefix(c.Value, prefix):
		case c.Desc == "":
			out = append(out, done+c.Value)
		default:
			out = append(out, cobra.CompletionWithDesc(done+c.Value, c.Desc))
		}
	}
	return out
}
