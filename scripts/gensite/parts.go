package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/rogpeppe/go-internal/txtar"
	"github.com/spf13/cobra"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/cli"
	"github.com/sassdavid/tfcensus/internal/toolcheck"
	"github.com/sassdavid/tfcensus/schemas"
)

// colType is the header of a type column.
const colType = "Type"

// e2eTool reads a tool version from mise.e2e.toml, the versions the end-to-end tests run.
var e2eTool = regexp.MustCompile(`(?m)^(terragrunt|terraform|opentofu) = "([^"]+)"$`)

func versionsPart(root string) ([]byte, error) {
	const e2eFile = "mise.e2e.toml"
	raw, err := os.ReadFile(filepath.Join(root, e2eFile))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", e2eFile, err)
	}
	tested := map[string]string{}
	for _, m := range e2eTool.FindAllStringSubmatch(string(raw), -1) {
		tested[m[1]] = m[2]
	}
	var b strings.Builder
	b.WriteString(partNote)
	tools := []struct {
		kind       toolcheck.Kind
		name, mise string
	}{
		{toolcheck.KindTerragrunt, "terragrunt", "terragrunt"},
		{toolcheck.KindTerraform, "terraform", "terraform"},
		{toolcheck.KindTofu, "OpenTofu (`tofu`)", "opentofu"},
	}
	var rows [][]string
	for _, t := range tools {
		var features []string
		for _, f := range toolcheck.Gated(t.kind) {
			needs := "never"
			if f.Needs != "" {
				needs = f.Needs
			}
			features = append(features, f.Name+" ("+needs+")")
		}
		if len(features) == 0 {
			features = []string{"none"}
		}
		if tested[t.mise] == "" {
			return nil, fmt.Errorf("%s has no %s version", e2eFile, t.mise)
		}
		rows = append(rows, []string{
			t.name, toolcheck.Minimum(t.kind), tested[t.mise], cell(strings.Join(features, ", ")),
		})
	}
	table(&b, []string{"Tool", "Oldest version", "End-to-end tests run", "Features that need a newer version"}, rows)
	return []byte(b.String()), nil
}

// goldenPart names a file inside a test script archive that a page shows as output.
type goldenPart struct {
	archive, file, out string
}

// goldenParts lists the outputs that live in test script archives. Outputs that are plain golden
// files are included from testdata/golden directly.
func goldenParts() []goldenPart {
	return []goldenPart{
		{"testdata/script/spec_accounts_2.txtar", "want-doctor.txt", "docs/snippets/doctor.txt"},
		{"testdata/script/spec_scan_1.txtar", "golden/scan.txt", "docs/snippets/scan.txt"},
	}
}

// archiveFile returns the content of name in the txtar archive at path.
func archiveFile(root, path, name string) ([]byte, error) {
	a, err := txtar.ParseFile(filepath.Join(root, path))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	for _, f := range a.Files {
		if f.Name == name {
			return f.Data, nil
		}
	}
	return nil, fmt.Errorf("%s has no %s", path, name)
}

func newRoot() *cobra.Command {
	return cli.NewRootCmd(&app.Deps{
		Stdin:  strings.NewReader(""),
		Stdout: io.Discard,
		Stderr: io.Discard,
		Clock:  time.Now,
		Getwd:  os.Getwd,
	})
}

// schemaCommands maps a command path such as "audit backend" to the schema of its JSON output,
// from the schema titles ("tfcensus audit backend --format json").
func schemaCommands() (map[string]string, error) {
	out := map[string]string{}
	for _, n := range schemas.Names() {
		b, err := schemas.Read(n)
		if err != nil {
			return nil, err //nolint:wrapcheck // the error names the schema
		}
		var s struct {
			Title string `json:"title"`
		}
		if err = json.Unmarshal(b, &s); err != nil {
			return nil, fmt.Errorf("decode %s: %w", n, err)
		}
		rest, ok := strings.CutPrefix(s.Title, "tfcensus ")
		if cmd, found := strings.CutSuffix(rest, " --format json"); ok && found {
			out[cmd] = n
		}
	}
	return out, nil
}

func formatsPart() ([]byte, error) {
	bySchema, err := schemaCommands()
	if err != nil {
		return nil, err
	}
	var rows [][]string
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if row := formatsRow(c, bySchema); row != nil {
			rows = append(rows, row)
		}
		for _, sub := range c.Commands() {
			if sub.IsAvailableCommand() {
				walk(sub)
			}
		}
	}
	walk(newRoot())
	var b strings.Builder
	b.WriteString(partNote)
	table(&b, []string{"Command", "Formats", "`--out`", "JSON schema"}, rows)
	return []byte(b.String()), nil
}

// formatsRow returns the formats table row of c, or nil when c runs nothing or ignores --format.
func formatsRow(c *cobra.Command, bySchema map[string]string) []string {
	formats := cli.SplitAnnotation(c, cli.AnnotationFormats)
	if !c.Runnable() || len(formats) == 0 {
		return nil
	}
	path := strings.TrimPrefix(c.CommandPath(), c.Root().Name()+" ")
	schema := bySchema[path]
	if schema != "" {
		schema = "[`" + schema + "`](/schemas/" + schema + ")"
	}
	link := "[`" + path + "`](/cli/" + strings.ReplaceAll(path, " ", "/") + ")"
	out := ""
	if c.Flags().Lookup("out") != nil {
		out = "yes"
	}
	return []string{link, codeList(formats), out, schema}
}
