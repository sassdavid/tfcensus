package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/sassdavid/tfcensus/internal/complete"
)

// unpublished are the paths under docs/ that the site leaves out (srcExclude in config.mts) or
// that hold no pages.
func unpublished() []string {
	return []string{
		"plan", "engineering", "validation", "dogfood.md", "README.md", "node_modules", ".vitepress", "public",
	}
}

// configKeyPages are the folders and pages where a dotted name in backticks must be a config key.
func configKeyPages() []string { return []string{"reference/config.md", "guides/", "concepts/"} }

var (
	fenceLine  = regexp.MustCompile("^\\s*(```+|~~~+)\\s*([A-Za-z]*)")
	codeSpan   = regexp.MustCompile("`([^`\n]+)`")
	dottedName = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z0-9_*<>-]+)+$`)
)

// shellLangs are the fence languages whose lines are checked as commands.
func shellLangs() []string { return []string{"sh", "bash", "shell", "console", "zsh"} }

// maxLine is the longest Markdown line the snippet check reads.
const maxLine = 1 << 20

// checkSnippets returns a "file:line: problem" line for each command line in the published
// Markdown that names an unknown command, flag or flag value, and for each unknown config key.
func checkSnippets(docs string, root *cobra.Command) ([]string, error) {
	keys, err := schemaKeys()
	if err != nil {
		return nil, err
	}
	fsys := os.DirFS(docs)
	var problems []string
	err = fs.WalkDir(fsys, ".", func(rel string, e fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case slices.Contains(unpublished(), rel) && e.IsDir():
			return fs.SkipDir
		case slices.Contains(unpublished(), rel), e.IsDir(), !strings.HasSuffix(rel, ".md"):
			return nil
		}
		b, rerr := fs.ReadFile(fsys, rel)
		if rerr != nil {
			return fmt.Errorf("read %s: %w", rel, rerr)
		}
		c := &snippetFile{root: root, keys: keys}
		c.checkKeys = slices.ContainsFunc(configKeyPages(), func(k string) bool { return strings.HasPrefix(rel, k) })
		for _, pr := range c.check(b) {
			problems = append(problems, filepath.Join(docs, rel)+":"+pr)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", docs, err)
	}
	return problems, nil
}

// snippetFile checks one Markdown file line by line.
type snippetFile struct {
	root      *cobra.Command
	keys      map[string]bool
	checkKeys bool
	fence     string // the open fence marker, or ""
	lang      string // the open fence's language
	cont      string // a command line continued with a backslash
}

func (c *snippetFile) check(b []byte) []string {
	var problems []string
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(nil, maxLine)
	for line := 1; sc.Scan(); line++ {
		for _, pr := range c.line(sc.Text()) {
			problems = append(problems, fmt.Sprintf("%d: %s", line, pr))
		}
	}
	return problems
}

func (c *snippetFile) line(text string) []string {
	if m := fenceLine.FindStringSubmatch(text); m != nil {
		switch {
		case c.fence == "":
			c.fence, c.lang = m[1], m[2]
		case strings.HasPrefix(m[1], c.fence[:1]) && len(m[1]) >= len(c.fence):
			c.fence, c.lang = "", ""
		}
		return nil
	}
	if c.fence != "" {
		if !slices.Contains(shellLangs(), c.lang) {
			return nil
		}
		text = c.cont + text
		if before, ok := strings.CutSuffix(text, `\`); ok {
			c.cont = before + " "
			return nil
		}
		c.cont = ""
		return checkCommand(c.root, text)
	}
	var problems []string
	for _, m := range codeSpan.FindAllStringSubmatch(text, -1) {
		problems = append(problems, checkCommand(c.root, m[1])...)
		// Table cells hold values, such as a default of github.com, as well as keys.
		table := strings.HasPrefix(strings.TrimSpace(text), "|")
		if c.checkKeys && !table && dottedName.MatchString(m[1]) && !knownKey(c.keys, m[1]) {
			problems = append(problems, "unknown config key "+m[1])
		}
	}
	return problems
}

// knownKey reports whether name is a config key. Keys whose first part is not a top-level key,
// such as file names, are not config keys and pass.
func knownKey(keys map[string]bool, name string) bool {
	parts := strings.Split(name, ".")
	if !keys[parts[0]] {
		return true
	}
	for i := len(parts); i > 0; i-- {
		prefix := strings.Join(parts[:i], ".")
		if keys[prefix] {
			return i == len(parts) || keys[prefix+".*"]
		}
	}
	return false
}

// schemaKeys returns every config key path. A map or list of objects adds "key.*" so that the
// names below it pass.
func schemaKeys() (map[string]bool, error) {
	root, err := loadConfigSchema()
	if err != nil {
		return nil, err
	}
	keys := map[string]bool{}
	var walk func(n *schemaNode, prefix string)
	walk = func(n *schemaNode, prefix string) {
		for _, name := range n.Properties.names {
			child := n.Properties.nodes[name]
			key := strings.TrimPrefix(prefix+"."+name, ".")
			keys[key] = true
			if child.isObject() {
				walk(child, key)
			}
			if child.AdditionalProperties != nil || (child.Items != nil && child.Items.isObject()) {
				keys[key+".*"] = true
			}
		}
	}
	walk(root, "")
	return keys, nil
}

// checkCommand checks one shell line that starts the tool, after an optional prompt and
// variable assignments. Other lines pass.
func checkCommand(root *cobra.Command, line string) []string {
	words := shellWords(line)
	for len(words) > 0 && (words[0] == "$" || isAssignment(words[0])) {
		words = words[1:]
	}
	if len(words) == 0 || words[0] != root.Name() {
		return nil
	}
	cmd := root
	words = words[1:]
	for len(words) > 0 && !strings.HasPrefix(words[0], "-") {
		next := subcommand(cmd, words[0])
		if next == nil {
			break
		}
		cmd, words = next, words[1:]
	}
	if len(words) > 0 && cmd == root &&
		slices.Contains([]string{cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd}, words[0]) {
		return nil // cobra's hidden completion command takes any arguments
	}
	var problems []string
	if len(words) > 0 && !strings.HasPrefix(words[0], "-") && !cmd.Runnable() && !placeholder(words[0]) {
		problems = append(problems, fmt.Sprintf("%s: unknown command %q", cmd.CommandPath(), words[0]))
	}
	return append(problems, checkFlags(cmd, words)...)
}

// checkFlags checks the flags and flag values in the words after the command name.
func checkFlags(cmd *cobra.Command, words []string) []string {
	var problems []string
	for i := 0; i < len(words) && words[i] != "--"; i++ {
		w := words[i]
		if !strings.HasPrefix(w, "-") || w == "-" {
			continue
		}
		name, value, hasValue := strings.Cut(strings.TrimLeft(w, "-"), "=")
		for _, f := range wordFlags(cmd, w, name) {
			if f == nil {
				problems = append(problems, fmt.Sprintf("%s: unknown flag %s", cmd.CommandPath(), w))
				continue
			}
			if !takesValue(f) {
				continue
			}
			if !hasValue && i+1 < len(words) {
				i++
				value = words[i]
			}
			if pr := checkValue(cmd, f, value); pr != "" {
				problems = append(problems, pr)
			}
		}
	}
	return problems
}

// takesValue reports whether f needs a value, unlike a bool, a count or --help.
func takesValue(f *pflag.Flag) bool { return f.NoOptDefVal == "" && f.Name != helpFlag }

// wordFlags returns the flags a word names: one long flag, or each letter of a short flag group.
// An unknown flag is nil.
func wordFlags(cmd *cobra.Command, word, name string) []*pflag.Flag {
	if strings.HasPrefix(word, "--") {
		return []*pflag.Flag{lookupFlag(cmd, name)}
	}
	var flags []*pflag.Flag
	for _, r := range name {
		flags = append(flags, shorthand(cmd, string(r)))
	}
	return flags
}

func subcommand(c *cobra.Command, name string) *cobra.Command {
	for _, sub := range c.Commands() {
		if sub.Name() == name || slices.Contains(sub.Aliases, name) {
			return sub
		}
	}
	return nil
}

func lookupFlag(c *cobra.Command, name string) *pflag.Flag {
	if name == helpFlag {
		return &pflag.Flag{Name: helpFlag, NoOptDefVal: trueValue}
	}
	if f := c.Flags().Lookup(name); f != nil {
		return f
	}
	return c.InheritedFlags().Lookup(name)
}

func shorthand(c *cobra.Command, s string) *pflag.Flag {
	if s == "h" {
		return &pflag.Flag{Name: helpFlag, NoOptDefVal: trueValue}
	}
	if f := c.Flags().ShorthandLookup(s); f != nil {
		return f
	}
	return c.InheritedFlags().ShorthandLookup(s)
}

// checkValue checks the value of a flag that has a fixed list of values.
func checkValue(c *cobra.Command, f *pflag.Flag, value string) string {
	fn, ok := c.GetFlagCompletionFunc(f.Name)
	if !ok || placeholder(value) {
		return ""
	}
	allowed := complete.Values(complete.StaticValues(c, fn))
	if len(allowed) == 0 {
		return ""
	}
	parts := []string{value}
	if f.Value.Type() == "stringSlice" || listFlag(c, fn, allowed) {
		parts = strings.Split(value, ",")
	}
	for _, p := range parts {
		if key, _, isAssign := strings.Cut(p, "="); isAssign {
			p = key
		}
		if !slices.Contains(allowed, p) {
			return fmt.Sprintf("%s: --%s %q: want one of %s", c.CommandPath(), f.Name, p, strings.Join(allowed, ", "))
		}
	}
	return ""
}

// listFlag reports whether fn completes a comma-separated list, as [complete.List] does: after
// "a," it offers the other values with "a," in front.
func listFlag(c *cobra.Command, fn cobra.CompletionFunc, allowed []string) bool {
	done := allowed[0] + ","
	comps, _ := fn(c, nil, done)
	return len(comps) > 0 && strings.HasPrefix(comps[0], done)
}

// placeholder reports whether a word stands for a value the reader fills in, or is built by
// the shell.
func placeholder(w string) bool {
	return w == "" || strings.ContainsAny(w, "<>${}[]*`")
}

func isAssignment(w string) bool {
	name, _, ok := strings.Cut(w, "=")
	return ok && name != "" && strings.ToUpper(name) == name && !strings.HasPrefix(name, "-")
}

// shellWords splits a shell line into words with quotes removed. It stops at a comment, a pipe,
// a redirection or a command separator.
func shellWords(line string) []string {
	var words []string
	var cur strings.Builder
	inWord := false
	quote := rune(0)
	flush := func() {
		if inWord {
			words = append(words, cur.String())
		}
		cur.Reset()
		inWord = false
	}
	for _, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote, inWord = r, true
		case r == ' ' || r == '\t':
			flush()
		case strings.ContainsRune("|;&>", r) || (r == '#' && !inWord):
			flush()
			return words
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	flush()
	return words
}
