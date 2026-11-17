package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sassdavid/tfcensus/internal/cli"
	"github.com/sassdavid/tfcensus/internal/complete"
	"github.com/sassdavid/tfcensus/internal/config"
	"github.com/sassdavid/tfcensus/internal/execx"
	"github.com/sassdavid/tfcensus/schemas"
)

// schemaNode is the part of a JSON Schema the config reference reads.
type schemaNode struct {
	Type                 any                        `json:"type"`
	Description          string                     `json:"description"`
	Enum                 []any                      `json:"enum"`
	Pattern              string                     `json:"pattern"`
	Minimum              *float64                   `json:"minimum"`
	Maximum              *float64                   `json:"maximum"`
	Properties           orderedProps               `json:"properties"`
	Items                *schemaNode                `json:"items"`
	AdditionalProperties *schemaNode                `json:"-"`
	Raw                  map[string]json.RawMessage `json:"-"`
}

// orderedProps keeps the properties in schema order, which follows the Go struct.
type orderedProps struct {
	names []string
	nodes map[string]*schemaNode
}

func (p *orderedProps) UnmarshalJSON(b []byte) error {
	dec := json.NewDecoder(strings.NewReader(string(b)))
	if _, err := dec.Token(); err != nil {
		return fmt.Errorf("read properties: %w", err)
	}
	p.nodes = map[string]*schemaNode{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("read properties: %w", err)
		}
		name, _ := tok.(string)
		var n schemaNode
		if err = dec.Decode(&n); err != nil {
			return fmt.Errorf("read property %s: %w", name, err)
		}
		p.names = append(p.names, name)
		p.nodes[name] = &n
	}
	return nil
}

func (n *schemaNode) UnmarshalJSON(b []byte) error {
	type plain schemaNode
	if err := json.Unmarshal(b, (*plain)(n)); err != nil {
		return fmt.Errorf("decode schema: %w", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return fmt.Errorf("decode schema: %w", err)
	}
	n.Raw = raw
	if ap, ok := raw["additionalProperties"]; ok && strings.HasPrefix(strings.TrimSpace(string(ap)), "{") {
		n.AdditionalProperties = &schemaNode{}
		if err := json.Unmarshal(ap, n.AdditionalProperties); err != nil {
			return err
		}
	}
	return nil
}

func (n *schemaNode) typeName() string {
	switch t := n.Type.(type) {
	case string:
		if t == "array" && n.Items != nil {
			return "list of " + n.Items.typeName()
		}
		if t == "object" && n.AdditionalProperties != nil {
			return "map of " + n.AdditionalProperties.typeName()
		}
		return t
	case []any:
		var parts []string
		for _, p := range t {
			parts = append(parts, fmt.Sprint(p))
		}
		return strings.Join(parts, " or ")
	}
	return "any"
}

func (n *schemaNode) isObject() bool { return n.Type == "object" && len(n.Properties.names) > 0 }

// configLeaf is one key of the config reference.
type configLeaf struct {
	key  string
	node *schemaNode
}

func loadConfigSchema() (*schemaNode, error) {
	b, err := schemas.Read(schemas.ConfigV1)
	if err != nil {
		return nil, err //nolint:wrapcheck // the error names the schema
	}
	var root schemaNode
	if err = json.Unmarshal(b, &root); err != nil {
		return nil, fmt.Errorf("%s: %w", schemas.ConfigV1, err)
	}
	return &root, nil
}

// configLeaves returns every config key below n that is not an object with properties, in
// schema order.
func configLeaves(n *schemaNode, prefix string) []configLeaf {
	var out []configLeaf
	for _, name := range n.Properties.names {
		child := n.Properties.nodes[name]
		key := strings.TrimPrefix(prefix+"."+name, ".")
		if child.isObject() {
			out = append(out, configLeaves(child, key)...)
			continue
		}
		out = append(out, configLeaf{key, child})
	}
	return out
}

// defaults returns config.Default as nested maps keyed by JSON name.
func defaults() (map[string]any, error) {
	b, err := json.Marshal(config.Default())
	if err != nil {
		return nil, fmt.Errorf("encode defaults: %w", err)
	}
	var m map[string]any
	if err = json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("decode defaults: %w", err)
	}
	return m, nil
}

func lookupKey(m map[string]any, key string) (any, bool) {
	var cur any = m
	for part := range strings.SplitSeq(key, ".") {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = mm[part]; !ok {
			return nil, false
		}
	}
	return cur, true
}

func showValue(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		if t == "" {
			return code(`""`)
		}
		return code(t)
	default:
		b, _ := json.Marshal(t)
		return code(string(b))
	}
}

func (g *generator) configPage() error {
	root, err := loadConfigSchema()
	if err != nil {
		return err
	}
	defs, err := defaults()
	if err != nil {
		return err
	}
	envKeys, err := config.EnvKeys()
	if err != nil {
		return err //nolint:wrapcheck // the error names the schema
	}
	flagsByKey := map[string][]string{}
	for flag, key := range cli.FlagKeys() {
		flagsByKey[key] = append(flagsByKey[key], "--"+flag)
	}
	var b strings.Builder
	b.WriteString(frontmatter("Configuration", "Every "+config.AppName+" config key with its type, default, "+
		"environment variable and flag."))
	b.WriteString("# Configuration\n\n")
	fmt.Fprintf(&b, "%s reads its settings in layers, and a later layer wins: the defaults, the user file "+
		"(`$XDG_CONFIG_HOME/%s/%s`), the project file (`%s`, found by walking up from the working "+
		"directory), environment variables and flags. `%s config show --sources` prints each value and its "+
		"layer, and `%s config init` writes the commented template at the end of this page. "+
		"Files are checked against [%s](%s).\n\n",
		config.AppName, config.AppName, config.UserFileName, config.ProjectFileName,
		config.AppName, config.AppName, schemas.ConfigV1, "/schemas/"+schemas.ConfigV1)
	fmt.Fprintf(&b, "A key's environment variable is `%s` followed by the key in upper case, with dots and "+
		"dashes as underscores. Only scalars and string lists (comma-separated) can be set this way. "+
		"See [environment variables](/reference/environment).\n\n", config.EnvPrefix)
	for _, name := range root.Properties.names {
		node := root.Properties.nodes[name]
		b.WriteString("## " + name + "\n\n")
		if !node.isObject() {
			g.configKey(&b, configLeaf{name, node}, defs, envKeys, flagsByKey, false)
			continue
		}
		if node.Description != "" {
			b.WriteString(cell(node.Description) + "\n\n")
		}
		for _, leaf := range configLeaves(node, name) {
			g.configKey(&b, leaf, defs, envKeys, flagsByKey, true)
		}
	}
	b.WriteString("## Template\n\n`" + config.AppName + " config init` writes this file:\n\n```yaml\n")
	b.Write(config.Template())
	b.WriteString("```\n")
	g.add("reference/config.md", []byte(b.String()))
	return nil
}

func (g *generator) configKey(b *strings.Builder, leaf configLeaf, defs map[string]any, envKeys []string,
	flagsByKey map[string][]string, heading bool,
) {
	if heading {
		b.WriteString("### " + leaf.key + "\n\n")
	}
	n := leaf.node
	typ := code(n.typeName())
	var values []string
	for _, e := range n.Enum {
		values = append(values, code(fmt.Sprint(e)))
	}
	if n.Items != nil {
		for _, e := range n.Items.Enum {
			values = append(values, code(fmt.Sprint(e)))
		}
	}
	allowed := strings.Join(values, ", ")
	if n.Pattern != "" {
		allowed = strings.TrimPrefix(allowed+", pattern "+code(n.Pattern), ", ")
	}
	if n.Minimum != nil {
		allowed = strings.TrimPrefix(allowed+", at least "+strconv.FormatFloat(*n.Minimum, 'f', -1, 64), ", ")
	}
	def, _ := lookupKey(defs, leaf.key)
	env := ""
	if slices.Contains(envKeys, leaf.key) {
		env = code(config.EnvVarName(leaf.key))
	}
	flags := flagsByKey[leaf.key]
	slices.Sort(flags)
	b.WriteString(table([]string{colType, "Default", "Allowed", "Environment", "Flag"},
		[][]string{{typ, showValue(def), allowed, env, joinCode(flags)}}))
	b.WriteString("\n")
	if n.Description != "" {
		b.WriteString(cell(n.Description) + "\n\n")
	}
	item := n.Items
	if n.AdditionalProperties != nil {
		item = n.AdditionalProperties
	}
	if item != nil && item.isObject() {
		var rows [][]string
		for _, name := range item.Properties.names {
			p := item.Properties.nodes[name]
			rows = append(rows, []string{code(name), code(p.typeName()), cell(p.Description)})
		}
		b.WriteString(
			"Each entry has these fields:\n\n" + table([]string{"Field", colType, colDescription}, rows) + "\n",
		)
	}
}

func (g *generator) environmentPage() {
	var b strings.Builder
	b.WriteString(frontmatter("Environment variables", "The environment variables "+config.AppName+
		" reads, and the ones it passes to child processes."))
	b.WriteString("# Environment variables\n\n")
	rows := [][]string{
		{
			code(config.EnvConfigFile),
			cell("Config file to read instead of the discovered project file, as --config does."),
		},
		{code(config.EnvPrefix + "<KEY>"), "Sets a config key; the [configuration](/reference/config) page " +
			"names the variable of each key. An unknown `" + config.EnvPrefix + "` variable is ignored with a warning."},
	}
	for _, v := range config.EnvVars() {
		rows = append(rows, []string{code(v.Name), cell(v.Desc)})
	}
	b.WriteString(table([]string{"Variable", "Effect"}, rows))
	b.WriteString("\n## Child processes\n\n")
	b.WriteString("A child process, such as terraform, starts with only these variables from " + config.AppName +
		"'s environment, plus the ones the command sets for it. A `*` matches any suffix.\n\n")
	b.WriteString(joinCode(execx.DefaultEnvAllow()) + "\n")
	g.add("reference/environment.md", []byte(b.String()))
}

func (g *generator) exitCodesPage(root *cobra.Command) {
	var b strings.Builder
	b.WriteString(frontmatter("Exit codes", "What each "+config.AppName+" exit code means, and which "+
		"commands return it."))
	b.WriteString("# Exit codes\n\n")
	rows := make([][]string, 0, len(cli.ExitCodes()))
	for _, e := range cli.ExitCodes() {
		rows = append(rows, []string{strconv.Itoa(e.Code), code(e.Name), cell(e.Desc)})
	}
	b.WriteString(table([]string{"Code", "Name", "Meaning"}, rows))
	b.WriteString("\nWhen more than one applies, the first error decides the code. A `--fail-on` match " +
		"(3) is reported after the report is written.\n\n## Per command\n\n")
	var perCmd [][]string
	forEachRunnable(root, func(c *cobra.Command) {
		perCmd = append(perCmd, []string{
			"[" + c.CommandPath() + "](" + pagePath(c) + ")",
			strings.Join(cli.SplitAnnotation(c, cli.AnnotationExitCodes), ", "),
		})
	})
	b.WriteString(table([]string{colCommand, "Exit codes"}, perCmd))
	g.add("reference/exit-codes.md", []byte(b.String()))
}

func (g *generator) failOnPage(root *cobra.Command) {
	var b strings.Builder
	b.WriteString(frontmatter("Fail-on values", "The --fail-on values of each "+config.AppName+
		" command, which make it exit 3."))
	b.WriteString("# Fail-on values\n\n`--fail-on` makes a command exit 3 when its report has a match. " +
		"The report is still written. A list flag takes several values, comma-separated or repeated.\n\n")
	var rows [][]string
	forEachRunnable(root, func(c *cobra.Command) {
		fn, ok := c.GetFlagCompletionFunc("fail-on")
		if !ok {
			return
		}
		desc := map[string]string{}
		for _, ch := range complete.StaticValues(c, fn) {
			desc[ch.Value] = ch.Desc
		}
		for _, v := range cli.SplitAnnotation(c, cli.AnnotationFailOn) {
			rows = append(rows, []string{"[" + c.CommandPath() + "](" + pagePath(c) + ")", code(v), cell(desc[v])})
		}
	})
	b.WriteString(table([]string{colCommand, "Value", "Matches"}, rows))
	g.add("reference/fail-on.md", []byte(b.String()))
}

// forEachRunnable calls fn for each command with its own page that runs something. The shell
// commands share the completion page and are left out.
func forEachRunnable(c *cobra.Command, fn func(*cobra.Command)) {
	if c.Runnable() {
		fn(c)
	}
	if c.Name() == completionName && c.HasParent() {
		return
	}
	for _, sub := range documented(c) {
		forEachRunnable(sub, fn)
	}
}

func (g *generator) schemasPage() error {
	var b strings.Builder
	b.WriteString(frontmatter("JSON schemas", "The JSON schemas of "+config.AppName+"'s config file and "+
		"JSON output, with the URL each is served at."))
	b.WriteString("# JSON schemas\n\nEach schema's `$id` is the URL it is served at, so an editor or a " +
		"validator can fetch it from there.\n\n")
	var rows [][]string
	for _, name := range schemas.Names() {
		raw, err := schemas.Read(name)
		if err != nil {
			return err //nolint:wrapcheck // the error names the schema
		}
		var s struct {
			ID          string `json:"$id"`
			Title       string `json:"title"`
			Description string `json:"description"`
		}
		if err = json.Unmarshal(raw, &s); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		g.add("public/schemas/"+name, raw)
		rows = append(rows, []string{"[" + code(name) + "](/schemas/" + name + ")", cell(s.Title), cell(s.Description)})
	}
	b.WriteString(table([]string{"Schema", "Title", colDescription}, rows))
	g.add("reference/schemas.md", []byte(b.String()))
	return nil
}
