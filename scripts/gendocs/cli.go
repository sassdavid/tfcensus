package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/sassdavid/tfcensus/internal/cli"
	"github.com/sassdavid/tfcensus/internal/complete"
)

// maxListedValues is the most flag values a table cell lists; longer lists name their size.
const maxListedValues = 12

const (
	completionName  = "completion"
	globalFlagsPage = "/cli/global-flags"
)

// node is one command in docs/cli/commands.json, which sidebar.mts reads.
type node struct {
	Name     string  `json:"name"`
	Path     string  `json:"path"`
	Short    string  `json:"short"`
	Hidden   bool    `json:"hidden"`
	Children []*node `json:"children,omitempty"`
}

// documented returns the subcommands of c that get a page or a row on the site.
func documented(c *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	for _, sub := range c.Commands() {
		if sub.IsAvailableCommand() {
			out = append(out, sub)
		}
	}
	return out
}

// pagePath returns the site path of the page for c, such as /cli/config/show.
func pagePath(c *cobra.Command) string {
	parts := strings.Fields(c.CommandPath())[1:]
	if len(parts) == 0 {
		return "/cli/"
	}
	if len(documented(c)) > 0 && c.Name() != completionName {
		return "/cli/" + strings.Join(parts, "/") + "/"
	}
	return "/cli/" + strings.Join(parts, "/")
}

// pageFile returns the file under docs/ for the page of c.
func pageFile(c *cobra.Command) string {
	p := strings.TrimPrefix(pagePath(c), "/")
	if strings.HasSuffix(p, "/") {
		return p + "index.md"
	}
	return p + ".md"
}

// cliPages writes one page per command, the global flags page and commands.json.
func (g *generator) cliPages(root *cobra.Command) error {
	tree := g.walk(root)
	b, err := json.MarshalIndent(tree, "", "  ")
	if err != nil {
		return fmt.Errorf("encode commands.json: %w", err)
	}
	g.add("cli/commands.json", append(b, '\n'))
	g.add("cli/global-flags.md", []byte(globalFlagsPageText(root)))
	return nil
}

func (g *generator) walk(c *cobra.Command) *node {
	n := &node{Name: c.Name(), Path: pagePath(c), Short: c.Short, Hidden: c.Hidden}
	if c.Name() == completionName && c.HasParent() {
		g.add(pageFile(c), []byte(g.completionPage(c)))
		return n
	}
	g.add(pageFile(c), []byte(g.commandPage(c)))
	for _, sub := range documented(c) {
		n.Children = append(n.Children, g.walk(sub))
	}
	return n
}

func (g *generator) commandPage(c *cobra.Command) string {
	var b strings.Builder
	b.WriteString(frontmatter(c.CommandPath(), c.Short))
	b.WriteString("# " + c.CommandPath() + "\n\n")
	if stores := cli.SplitAnnotation(c, cli.AnnotationWrites); len(stores) > 0 {
		// WritesState is a theme component of the sites whose tool has writing commands.
		b.WriteString(`<WritesState stores="` + strings.Join(stores, ",") + `" />` + "\n\n")
	}
	b.WriteString(prose(cmpOr(c.Long, c.Short)) + "\n\n")
	b.WriteString("## Usage\n\n```sh\n" + usageLine(c) + "\n```\n\n")
	if len(c.Aliases) > 0 {
		b.WriteString("Aliases: " + joinCode(c.Aliases) + "\n\n")
	}
	if args := arguments(c); len(args) > 0 {
		b.WriteString("## Arguments\n\n" + table([]string{"Argument", "Required", "Repeatable"}, args) + "\n")
	}
	if rows := flagRows(c, commandFlags(c)); len(rows) > 0 {
		b.WriteString("## Flags\n\n" + table(flagHeader(), rows) + "\n")
	}
	b.WriteString("The [global flags](" + globalFlagsPage + ") work with every command.\n\n")
	if c.Runnable() {
		b.WriteString(formatsSection(c))
		b.WriteString(failOnSection(c))
		b.WriteString(exitCodesSection(c))
	}
	if ex := strings.TrimRight(c.Example, " \n"); ex != "" {
		b.WriteString("## Examples\n\n```sh\n" + strings.Join(dedent(strings.Split(ex, "\n")), "\n") + "\n```\n\n")
	}
	if subs := documented(c); len(subs) > 0 {
		var rows [][]string
		for _, s := range subs {
			rows = append(rows, []string{"[" + s.CommandPath() + "](" + pagePath(s) + ")", cell(s.Short)})
		}
		b.WriteString("## Subcommands\n\n" + table([]string{colCommand, colDescription}, rows) + "\n")
	}
	if related := cli.SplitAnnotation(c, cli.AnnotationRelated); len(related) > 0 {
		b.WriteString("## Related\n\n")
		for _, p := range related {
			b.WriteString("- [" + g.pageTitle(p) + "](/" + p + ")\n")
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func cmpOr(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func joinCode(vs []string) string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = code(v)
	}
	return strings.Join(out, ", ")
}

// usageLine is cobra's usage line without a trailing [flags] after an alternative list, where it
// would read as part of the last alternative.
func usageLine(c *cobra.Command) string {
	if !c.Runnable() {
		return c.CommandPath() + " <command>"
	}
	if strings.Contains(c.Use, " | ") {
		return strings.TrimSuffix(c.UseLine(), " [flags]")
	}
	return c.UseLine()
}

// arguments reads the arguments from the Use line: <name> is required, [name] optional, and
// "..." repeatable.
func arguments(c *cobra.Command) [][]string {
	fields := strings.Fields(c.Use)
	var rows [][]string
	for _, f := range fields[1:] {
		if strings.HasPrefix(f, "-") || f == "[flags]" {
			continue
		}
		required := strings.HasPrefix(f, "<")
		if !required && !strings.HasPrefix(f, "[") {
			continue
		}
		name := strings.Trim(f, "<>[]")
		repeat := strings.HasSuffix(name, "...")
		rows = append(rows, []string{code(strings.TrimSuffix(name, "...")), yesNo(required), yesNo(repeat)})
	}
	return rows
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// commandFlags returns the flags of c that are not global: its own and those of its parents
// below the root.
func commandFlags(c *cobra.Command) []*pflag.Flag {
	global := c.Root().PersistentFlags()
	var out []*pflag.Flag
	add := func(f *pflag.Flag) {
		if f.Hidden || f.Deprecated != "" || f.Name == helpFlag || global.Lookup(f.Name) != nil {
			return
		}
		if !slices.ContainsFunc(out, func(o *pflag.Flag) bool { return o.Name == f.Name }) {
			out = append(out, f)
		}
	}
	c.NonInheritedFlags().VisitAll(add)
	c.InheritedFlags().VisitAll(add)
	slices.SortFunc(out, func(a, b *pflag.Flag) int { return strings.Compare(a.Name, b.Name) })
	return out
}

func flagHeader() []string {
	return []string{"Flag", colDescription, colType, "Default", "Values", "Config key", "Notes"}
}

func flagRows(c *cobra.Command, flags []*pflag.Flag) [][]string {
	keys := cli.FlagKeys()
	rows := make([][]string, 0, len(flags))
	for _, f := range flags {
		name := "--" + f.Name
		if f.Shorthand != "" {
			name = "-" + f.Shorthand + ", " + name
		}
		key := ""
		if k, ok := keys[f.Name]; ok {
			key = "[" + code(k) + "](/reference/config#" + slug(k) + ")"
		}
		rows = append(rows, []string{
			code(name), cell(f.Usage), flagType(f), flagDefault(f), flagValues(c, f), key, flagNotes(f),
		})
	}
	return rows
}

func flagType(f *pflag.Flag) string {
	switch t := f.Value.Type(); t {
	case "stringSlice":
		return "list, repeatable"
	case "stringArray":
		return "string, repeatable"
	case "stringToString":
		return "key=value, repeatable"
	case "float64":
		return "number"
	default:
		return t
	}
}

func flagDefault(f *pflag.Flag) string {
	switch f.DefValue {
	case "", "[]", "false":
		return ""
	case "0":
		if f.Value.Type() == "count" {
			return ""
		}
	}
	return code(strings.Trim(f.DefValue, "[]"))
}

func flagValues(c *cobra.Command, f *pflag.Flag) string {
	fn, ok := c.GetFlagCompletionFunc(f.Name)
	if !ok {
		return ""
	}
	cs := complete.StaticValues(c, fn)
	if len(cs) > maxListedValues {
		return strconv.Itoa(len(cs)) + " values; shell completion lists them"
	}
	parts := make([]string, len(cs))
	for i, ch := range cs {
		parts[i] = code(ch.Value)
		if ch.Desc != "" {
			parts[i] += ": " + cell(ch.Desc)
		}
	}
	return strings.Join(parts, "<br>")
}

// flagNotes lists the cobra flag group rules that involve f.
func flagNotes(f *pflag.Flag) string {
	var notes []string
	if v := f.Annotations[cobra.BashCompOneRequiredFlag]; len(v) == 1 && v[0] == trueValue {
		notes = append(notes, "required")
	}
	others := func(group string) string {
		var names []string
		for n := range strings.FieldsSeq(group) {
			if n != f.Name {
				names = append(names, code("--"+n))
			}
		}
		return strings.Join(names, ", ")
	}
	for _, g := range f.Annotations["cobra_annotation_mutually_exclusive"] {
		notes = append(notes, "not with "+others(g))
	}
	for _, g := range f.Annotations["cobra_annotation_required_if_others_set"] {
		notes = append(notes, "needs "+others(g))
	}
	for _, g := range f.Annotations["cobra_annotation_one_required"] {
		notes = append(notes, "this or "+others(g)+" is required")
	}
	return strings.Join(notes, "; ")
}

func formatsSection(c *cobra.Command) string {
	formats := cli.SplitAnnotation(c, cli.AnnotationFormats)
	if len(formats) == 0 {
		return "## Output formats\n\nThis command ignores `--format`.\n\n"
	}
	return "## Output formats\n\n`--format` accepts " + joinCode(formats) +
		". Without it, the format is `tty` when stdout is a terminal and `plain` otherwise. " +
		"Formats are described on the [global flags](" + globalFlagsPage + ") page.\n\n"
}

func failOnSection(c *cobra.Command) string {
	values := cli.SplitAnnotation(c, cli.AnnotationFailOn)
	fn, ok := c.GetFlagCompletionFunc("fail-on")
	if len(values) == 0 || !ok {
		return ""
	}
	desc := map[string]string{}
	for _, ch := range complete.StaticValues(c, fn) {
		desc[ch.Value] = ch.Desc
	}
	rows := make([][]string, len(values))
	for i, v := range values {
		rows[i] = []string{code(v), cell(desc[v])}
	}
	return "## Fail-on values\n\n`--fail-on` makes the command exit 3 when the report has a match:\n\n" +
		table([]string{"Value", "Matches"}, rows) + "\n"
}

func exitCodesSection(c *cobra.Command) string {
	desc := map[string]string{}
	for _, e := range cli.ExitCodes() {
		desc[strconv.Itoa(e.Code)] = e.Desc
	}
	var rows [][]string
	for _, code := range cli.SplitAnnotation(c, cli.AnnotationExitCodes) {
		rows = append(rows, []string{code, cell(desc[code])})
	}
	return "## Exit codes\n\n" + table([]string{"Code", "Meaning"}, rows) +
		"\nAll codes are listed on the [exit codes](/reference/exit-codes) page.\n\n"
}

func globalFlagsPageText(root *cobra.Command) string {
	var flags []*pflag.Flag
	root.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		if !f.Hidden && f.Deprecated == "" {
			flags = append(flags, f)
		}
	})
	slices.SortFunc(flags, func(a, b *pflag.Flag) int { return strings.Compare(a.Name, b.Name) })
	var b strings.Builder
	b.WriteString(frontmatter("Global flags", "Flags that every "+root.Name()+" command accepts."))
	b.WriteString("# Global flags\n\n")
	b.WriteString("Every " + root.Name() + " command accepts these flags, before or after the command name. " +
		"`-h, --help` prints the help of any command.\n\n")
	b.WriteString(table(flagHeader(), flagRows(root, flags)))
	b.WriteString("\nEach command page lists the `--format` values that command accepts.\n")
	return b.String()
}

func (g *generator) completionPage(c *cobra.Command) string {
	var b strings.Builder
	b.WriteString(frontmatter(c.CommandPath(), c.Short))
	b.WriteString("# " + c.CommandPath() + "\n\n")
	b.WriteString(prose(cmpOr(c.Long, c.Short)) + "\n\n")
	b.WriteString("## Usage\n\n```sh\n" + c.CommandPath() + " <shell>\n```\n\n")
	shells := documented(c)
	rows := make([][]string, len(shells))
	for i, s := range shells {
		rows[i] = []string{"[" + s.Name() + "](#" + s.Name() + ")", code(s.CommandPath())}
	}
	b.WriteString("## Shells\n\n" + table([]string{"Shell", colCommand}, rows) + "\n")
	b.WriteString("The [global flags](" + globalFlagsPage + ") work with every command. " +
		"The shell commands ignore `--format`.\n\n")
	for _, s := range shells {
		b.WriteString("### " + s.Name() + "\n\n" + prose(cmpOr(s.Long, s.Short)) + "\n\n")
		if flags := flagRows(s, commandFlags(s)); len(flags) > 0 {
			b.WriteString(table(flagHeader(), flags) + "\n")
		}
	}
	b.WriteString(exitCodesSection(shells[0]))
	if ex := strings.TrimRight(c.Example, " \n"); ex != "" {
		b.WriteString("## Examples\n\n```sh\n" + strings.Join(dedent(strings.Split(ex, "\n")), "\n") + "\n```\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// slug returns the anchor VitePress gives a heading with text s: runs of punctuation and space
// become one dash, and a leading digit gets an underscore.
func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r >= utf8.RuneSelf:
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		default:
			dash = true
		}
	}
	out := b.String()
	if out != "" && out[0] >= '0' && out[0] <= '9' {
		out = "_" + out
	}
	return out
}
