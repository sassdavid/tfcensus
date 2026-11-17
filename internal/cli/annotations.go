package cli

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/sassdavid/tfcensus/internal/complete"
	"github.com/sassdavid/tfcensus/internal/render"
)

// Command annotations that scripts/gendocs reads. Values are comma-separated lists.
const (
	AnnotationFormats   = "formats"      // --format values the command accepts; empty means it ignores --format
	AnnotationFailOn    = "fail-on"      // --fail-on values
	AnnotationExitCodes = "exit-codes"   // exit codes the command returns
	AnnotationRelated   = "docs.related" // site paths of related guides and concepts
)

// FlagKeys maps each flag that sets a config key to that key.
func FlagKeys() map[string]string { return flagKeys() }

// SplitAnnotation returns the list in annotation key of cmd, or nil when it is unset or empty.
func SplitAnnotation(cmd *cobra.Command, key string) []string {
	v := cmd.Annotations[key]
	if v == "" {
		return nil
	}
	return strings.Split(v, ",")
}

func annotate(cmd *cobra.Command, key string, values []string) {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[key] = strings.Join(values, ",")
}

// setFormats records the --format values cmd accepts. With none, cmd ignores --format.
func setFormats(cmd *cobra.Command, formats ...render.Format) {
	vs := make([]string, len(formats))
	for i, f := range formats {
		vs[i] = string(f)
	}
	annotate(cmd, AnnotationFormats, vs)
}

// textFormats are the formats of a command that prints a table or JSON.
func textFormats() []render.Format {
	return []render.Format{render.FormatTTY, render.FormatPlain, render.FormatJSON}
}

// formatsOf returns the formats recorded by [setFormats], and false when cmd has none recorded.
func formatsOf(cmd *cobra.Command) ([]render.Format, bool) {
	if _, ok := cmd.Annotations[AnnotationFormats]; !ok {
		return nil, false
	}
	var out []render.Format
	for _, v := range SplitAnnotation(cmd, AnnotationFormats) {
		out = append(out, render.Format(v))
	}
	return out, true
}

// setFailOn records the --fail-on values cmd accepts.
func setFailOn(cmd *cobra.Command, cs []complete.Choice) {
	annotate(cmd, AnnotationFailOn, complete.Values(cs))
}

// setExitCodes records the exit codes cmd returns: codes plus the ones every command returns.
func setExitCodes(cmd *cobra.Command, codes ...int) {
	all := append(universalExitCodes(), codes...)
	slices.Sort(all)
	all = slices.Compact(all)
	vs := make([]string, len(all))
	for i, c := range all {
		vs[i] = strconv.Itoa(c)
	}
	annotate(cmd, AnnotationExitCodes, vs)
}

// setRelated records site paths, such as "guides/scan", that the command's page links to.
func setRelated(cmd *cobra.Command, paths ...string) {
	annotate(cmd, AnnotationRelated, paths)
}

// checkAnnotated checks --format, --out and --fail-on against the annotations of cmd. Its
// errors are usage errors.
func (g *globals) checkAnnotated(cmd *cobra.Command) error {
	if formats, ok := formatsOf(cmd); ok && len(formats) > 0 {
		if err := g.checkFormats(formats...); err != nil {
			return usageError(err)
		}
	}
	allowed := SplitAnnotation(cmd, AnnotationFailOn)
	f := cmd.Flags().Lookup(flagFailOn)
	if len(allowed) == 0 || f == nil || !f.Changed {
		return nil
	}
	for _, v := range flagValues(f) {
		if !slices.Contains(allowed, v) {
			return usageError(fmt.Errorf("--%s %s isn't checked by %s: %s checks %s",
				flagFailOn, v, cmd.Name(), cmd.CommandPath(), joinOr(allowed)))
		}
	}
	return nil
}

// joinOr lists vs as "a, b or c".
func joinOr(vs []string) string {
	if len(vs) < 2 { //nolint:mnd // one value needs no "or"
		return strings.Join(vs, "")
	}
	return strings.Join(vs[:len(vs)-1], ", ") + " or " + vs[len(vs)-1]
}

// flagValues returns the values of a list flag, or the one value of any other flag.
func flagValues(f *pflag.Flag) []string {
	if sv, ok := f.Value.(pflag.SliceValue); ok {
		return sv.GetSlice()
	}
	return []string{f.Value.String()}
}
