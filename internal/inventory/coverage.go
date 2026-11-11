package inventory

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/sassdavid/tfcensus/internal/probe"
	"github.com/sassdavid/tfcensus/internal/typemap"
)

// GlobalColumn is the region shown for account-wide types such as IAM roles.
const GlobalColumn = "global"

// Coverage says, per type and per account and region, which layers listed the type. An empty
// cell is a blind spot.
type Coverage struct {
	Columns []Column      `json:"columns"`
	Rows    []CoverageRow `json:"rows"`
}

// Column is one account and region, or the account's [GlobalColumn].
type Column struct {
	Account string `json:"account"`
	Region  string `json:"region"`
}

// CoverageRow is one CloudFormation type.
type CoverageRow struct {
	CFNType string `json:"cfn_type"`
	Global  bool   `json:"global"`
	// Cells has one entry per column: the layers that listed the type there. A layer that was
	// denied or failed shows as "<layer>!".
	Cells [][]string `json:"cells"`
}

// coverage builds the matrix from the runs. A layer covers a regional type in a region when it
// ran there and can return the type. For a global type, Config covers it when some region's
// recorder records global types, Resource Explorer when it ran in the partition's global region,
// and a global native layer when it ran.
func coverage(targets []Target, tasks []*task, layers []Layer, env *Env, types []string) *Coverage {
	rows := coverageTypes(layers, env, types)
	hasGlobal := slices.ContainsFunc(rows, func(r CoverageRow) bool { return r.Global })
	cov := &Coverage{Columns: columns(targets, hasGlobal), Rows: rows}
	index := map[Column]int{}
	for i, c := range cov.Columns {
		index[c] = i
	}
	for ri := range rows {
		rows[ri].Cells = make([][]string, len(cov.Columns))
	}
	for _, t := range tasks {
		if t.run.Status == RunUnsupported || t.run.Status == RunSkipped {
			continue
		}
		layerTypes := t.layer.Types()
		if isDependent(t.layer) {
			layerTypes = t.types
		}
		for ri := range rows {
			col, ok := cellOf(t, &rows[ri], index)
			if !ok || !slices.Contains(layerTypes, rows[ri].CFNType) {
				continue
			}
			rows[ri].Cells[col] = appendMissing(
				rows[ri].Cells[col],
				[]string{cellMark(t, rows[ri].CFNType, layers, env)},
			)
		}
	}
	for ri := range rows {
		for _, marks := range rows[ri].Cells {
			slices.SortFunc(marks, func(a, b string) int { return strings.Compare(markRank(a), markRank(b)) })
		}
	}
	return cov
}

// cellMark is what a task's listing shows in the cell of type cfn.
func cellMark(t *task, cfn string, layers []Layer, env *Env) string {
	mark := short(t.layer.Name())
	if isDependent(t.layer) && env != nil && env.Map != nil && !ParentCovered(env.Map, cfn, layers) {
		mark += markNoParent
	}
	if t.run.Status == RunDenied || t.run.Status == RunError {
		mark += "!"
	}
	return mark
}

// columns returns each account's global column (when a row needs it) and its regions, sorted.
func columns(targets []Target, hasGlobal bool) []Column {
	var out []Column
	for i := range targets {
		acct := targets[i].Account.ID
		if hasGlobal {
			out = append(out, Column{acct, GlobalColumn})
		}
		for _, reg := range sortedRegions(targets[i].Report) {
			out = append(out, Column{acct, reg})
		}
	}
	return out
}

// cellOf returns the column a task's listing counts for in row: its region, or the account's
// global column for a global type when the task covers global types.
func cellOf(t *task, row *CoverageRow, index map[Column]int) (int, bool) {
	if !row.Global {
		col, ok := index[Column{t.run.Account, t.region}]
		return col, ok
	}
	col, ok := index[Column{t.run.Account, GlobalColumn}]
	return col, ok && coversGlobal(t)
}

func coversGlobal(t *task) bool {
	switch {
	case t.layer.Global():
		return true
	case t.layer.Name() == LayerConfig:
		return t.caps.ConfigGlobal
	case t.layer.Name() == LayerRE:
		return t.region == GlobalRegion(t.target.Account.Partition)
	}
	return false
}

func sortedRegions(r *probe.Report) []string {
	out := make([]string, 0, len(r.Regions))
	for i := range r.Regions {
		out = append(out, r.Regions[i].Region)
	}
	slices.Sort(out)
	return out
}

// coverageTypes returns the rows: every type a selected layer can return, limited to types.
func coverageTypes(layers []Layer, env *Env, types []string) []CoverageRow {
	var names []string
	for _, l := range layers {
		for _, c := range l.Types() {
			if wanted(types, c) && !slices.Contains(names, c) {
				names = append(names, c)
			}
		}
	}
	slices.Sort(names)
	rows := make([]CoverageRow, 0, len(names))
	for _, c := range names {
		global := false
		if env != nil && env.Map != nil {
			tfs := env.Map.ForCFN(c)
			global = len(tfs) > 0 && !slices.ContainsFunc(tfs, func(t *typemap.Type) bool { return !t.Global })
		}
		rows = append(rows, CoverageRow{CFNType: c, Global: global})
	}
	return rows
}

// markNoParent follows "cc" in a cell when no layer lists the parent type that Cloud Control
// needs to list the type, so the cell is still a blind spot.
const markNoParent = ":no-parent"

// short returns the layer name shown in a cell.
func short(layer string) string {
	switch layer {
	case LayerRE:
		return "re"
	case LayerConfig:
		return LayerConfig
	case LayerCC:
		return "cc"
	}
	return LayersNative
}

// markRank orders the marks in a cell like the layers: config, re, native, cc.
func markRank(m string) string {
	base := strings.TrimSuffix(strings.TrimSuffix(m, "!"), markNoParent)
	return fmt.Sprintf("%d%s", slices.Index([]string{LayerConfig, "re", LayersNative, "cc"}, base), m)
}

// WriteCoverage prints the matrix as an aligned table: one row per type, one column per account
// and region. "-" marks a blind spot.
func WriteCoverage(w io.Writer, c *Coverage) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0) //nolint:mnd // two spaces between columns
	header := []string{"TYPE"}
	for _, col := range c.Columns {
		header = append(header, col.Account+"/"+col.Region)
	}
	_, _ = fmt.Fprintln(tw, strings.Join(header, "\t"))
	for _, r := range c.Rows {
		line := []string{r.CFNType}
		for _, marks := range r.Cells {
			if len(marks) == 0 {
				line = append(line, "-")
				continue
			}
			line = append(line, strings.Join(marks, "+"))
		}
		_, _ = fmt.Fprintln(tw, strings.Join(line, "\t"))
	}
	if err := tw.Flush(); err != nil {
		return fmt.Errorf("write coverage: %w", err)
	}
	return nil
}
