package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/sassdavid/tfcensus/internal/typemap"
)

const (
	tallySource = "testdata/script/spec_scan_1.txtar"
	tallyFile   = "golden/scan.json"
	maxMarks    = 20 // marks in the longest tally row
)

// categories returns the scan categories the home page tally shows, in the order of the
// wireframe in docs/plan/specs/docs-design.md.
func categories() []string {
	return []string{"managed", "aws-managed", "other-iac", "derived", "unmanaged", "undetermined"}
}

func main() {
	os.Exit(run())
}

func run() int {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	files, err := generate(*root)
	var stale []string
	if err == nil {
		stale, err = writeAll(*root, files)
	}
	switch {
	case err != nil:
		_, _ = fmt.Fprintf(os.Stderr, "gensite: %v\n", err)
		return 1
	case len(stale) > 0:
		for _, f := range stale {
			_, _ = fmt.Fprintf(os.Stderr, "%s was stale and has been regenerated; review and commit it\n", f)
		}
		return 1
	default:
		return 0
	}
}

// generate returns every file gensite writes, keyed by its path under the repository root.
func generate(root string) (map[string][]byte, error) {
	files := map[string][]byte{}
	tally, err := tallyJSON(root)
	if err != nil {
		return nil, err
	}
	files["docs/.vitepress/theme/data/home-tally.json"] = tally
	m, err := typemap.Load()
	if err != nil {
		return nil, err //nolint:wrapcheck // the error says what failed
	}
	if files["docs/.vitepress/theme/data/type-coverage.json"], err = typeCoverage(m); err != nil {
		return nil, err
	}
	if files["docs/parts/inventory-layers.md"], err = layersPart(m); err != nil {
		return nil, err
	}
	if files["docs/reference/rule-packs.md"], err = rulePacksPage(root); err != nil {
		return nil, err
	}
	if files["docs/parts/cel-fields.md"], err = celPart(); err != nil {
		return nil, err
	}
	if files["docs/reference/audit-checks.md"], err = auditChecksPage(); err != nil {
		return nil, err
	}
	if files["docs/parts/output-formats.md"], err = formatsPart(); err != nil {
		return nil, err
	}
	files["docs/reference/drift-noise.md"] = driftNoisePage()
	files["docs/reference/rule-ids.md"] = ruleIDsPage()
	files["docs/parts/move-rules.md"] = moveRulesPart()
	if files["docs/parts/supported-versions.md"], err = versionsPart(root); err != nil {
		return nil, err
	}
	for _, g := range goldenParts() {
		if files[g.out], err = archiveFile(root, g.archive, g.file); err != nil {
			return nil, err
		}
	}
	policy, err := os.ReadFile(filepath.Join(root, iamPolicy))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", iamPolicy, err)
	}
	files["docs/public/iam/tfcensus-readonly.json"] = policy
	return files, nil
}

// iamPolicy is the read-only policy, which the site serves for download and embeds on /reference/iam.
const iamPolicy = "docs/iam/tfcensus-readonly.json"

// writeAll writes files and returns the paths it changed, sorted.
func writeAll(root string, files map[string][]byte) ([]string, error) {
	var stale []string
	for _, rel := range slices.Sorted(maps.Keys(files)) {
		changed, err := writeIfChanged(root, rel, files[rel])
		if err != nil {
			return nil, err
		}
		stale = append(stale, changed...)
	}
	return stale, nil
}

// tallyJSON returns the home page tally from the scan golden.
func tallyJSON(root string) ([]byte, error) {
	golden, err := archiveFile(root, tallySource, tallyFile)
	if err != nil {
		return nil, err
	}
	t, err := buildTally(golden)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", tallySource, tallyFile, err)
	}
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode tally: %w", err)
	}
	return append(b, '\n'), nil
}

type tally struct {
	Source  string     `json:"source"`
	Caption string     `json:"caption"`
	Unit    int        `json:"unit"`
	Rows    []tallyRow `json:"rows"`
}

type tallyRow struct {
	Category string `json:"category"`
	Count    int    `json:"count"`
}

type scanReport struct {
	Data struct {
		Accounts []struct {
			Counts map[string]int `json:"counts"`
		} `json:"accounts"`
		Regions []struct {
			Region string `json:"region"`
		} `json:"regions"`
	} `json:"data"`
}

// buildTally sums the category counts over the accounts of a scan report.
func buildTally(report []byte) (tally, error) {
	var r scanReport
	if err := json.Unmarshal(report, &r); err != nil {
		return tally{}, fmt.Errorf("decode scan report: %w", err)
	}
	if len(r.Data.Accounts) == 0 {
		return tally{}, errors.New("scan report has no accounts")
	}
	t := tally{Source: tallySource + " (" + tallyFile + ")"}
	most := 0
	for _, c := range categories() {
		n := 0
		for _, a := range r.Data.Accounts {
			n += a.Counts[c]
		}
		t.Rows = append(t.Rows, tallyRow{Category: c, Count: n})
		most = max(most, n)
	}
	t.Unit = unitFor(most)
	t.Caption = fmt.Sprintf("Sample scan: %s, %s", plural(len(r.Data.Accounts), "account"),
		plural(len(r.Data.Regions), "region"))
	return t, nil
}

// unitFor returns the smallest of 1, 5, 10, 25, 50 and their powers of ten that keeps the
// longest row at maxMarks marks or fewer.
func unitFor(most int) int {
	for scale := 1; ; scale *= 10 {
		for _, step := range []int{1, 5, 10, 25, 50} {
			if u := step * scale; (most+u-1)/u <= maxMarks {
				return u
			}
		}
	}
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func writeIfChanged(root, rel string, data []byte) ([]string, error) {
	path := filepath.Join(root, filepath.FromSlash(rel))
	have, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", rel, err)
	}
	if bytes.Equal(have, data) {
		return nil, nil
	}
	if err = os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create %s: %w", filepath.Dir(rel), err)
	}
	if err = os.WriteFile(path, data, 0o600); err != nil {
		return nil, fmt.Errorf("write %s: %w", rel, err)
	}
	return []string{rel}, nil
}
