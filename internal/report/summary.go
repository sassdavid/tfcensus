package report

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/sassdavid/tfcensus/internal/audit"
	"github.com/sassdavid/tfcensus/internal/drift"
	"github.com/sassdavid/tfcensus/internal/iampolicy"
	"github.com/sassdavid/tfcensus/internal/inventory"
	"github.com/sassdavid/tfcensus/internal/scan"
	"github.com/sassdavid/tfcensus/internal/tfstate"
)

// Default and largest number of findings a scan summary lists.
const (
	DefaultTop = 50
	MaxTop     = 500
	MaxTypes   = 25
)

// glob matches value against a pattern where * spans slashes; an empty pattern matches everything.
func glob(pattern, value string) bool {
	return pattern == "" || iampolicy.Glob(pattern, value, false)
}

func sortedKeys(m map[string]bool) []string {
	return slices.Sorted(maps.Keys(m))
}

type unitKey struct{ unit, workspace string }

// UnitsFilter selects units. Empty fields match everything.
type UnitsFilter struct {
	Path   string // glob on repo/path, * spans slashes
	Status string // a state status: ok, absent, encrypted, unsupported or error
}

// UnitRow is one state in the index, with the accounts and regions of its resources.
type UnitRow struct {
	Unit      string         `json:"unit"`
	Workspace string         `json:"workspace"`
	Bucket    string         `json:"bucket"`
	Key       string         `json:"key"`
	Status    tfstate.Status `json:"status"`
	Instances int            `json:"instances"`
	Accounts  []string       `json:"accounts"`
	Regions   []string       `json:"regions"`
}

// Units lists the indexed units that match a filter. Total counts every indexed unit.
type Units struct {
	Total int       `json:"total"`
	Units []UnitRow `json:"units"`
}

// SummarizeUnits lists the units of an index.
func SummarizeUnits(ix *tfstate.Index, f UnitsFilter) *Units {
	accts, regions := map[unitKey]map[string]bool{}, map[unitKey]map[string]bool{}
	for i := range ix.Entries {
		e := &ix.Entries[i]
		k := unitKey{e.Unit, e.Workspace}
		add(accts, k, e.Account)
		add(regions, k, e.Region)
	}
	out := &Units{Total: len(ix.Units), Units: []UnitRow{}}
	for _, u := range ix.Units {
		if !glob(f.Path, u.Unit) || (f.Status != "" && string(u.Status) != f.Status) {
			continue
		}
		k := unitKey{u.Unit, u.Workspace}
		out.Units = append(out.Units, UnitRow{
			Unit: u.Unit, Workspace: u.Workspace, Bucket: u.Bucket, Key: u.Key, Status: u.Status,
			Instances: u.Instances, Accounts: sortedKeys(accts[k]), Regions: sortedKeys(regions[k]),
		})
	}
	return out
}

func add(m map[unitKey]map[string]bool, k unitKey, v string) {
	if v == "" {
		return
	}
	if m[k] == nil {
		m[k] = map[string]bool{}
	}
	m[k][v] = true
}

// InventoryFilter selects resource instances. Empty fields match everything.
type InventoryFilter struct {
	Unit    string // glob on repo/path
	Account string
	Region  string
	Type    string // glob on the TF type
}

// TypeCount is the number of instances of one TF type.
type TypeCount struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}

// InventoryRow summarizes the managed instances of one state.
type InventoryRow struct {
	Unit          string      `json:"unit"`
	Workspace     string      `json:"workspace"`
	Instances     int         `json:"instances"`
	DoubleManaged int         `json:"double_managed"` // instances another state also manages
	Accounts      []string    `json:"accounts"`
	Regions       []string    `json:"regions"`
	Types         []TypeCount `json:"types"`
}

// Inventory summarizes an index per unit.
type Inventory struct {
	Instances     int            `json:"instances"`
	DoubleManaged int            `json:"double_managed"`
	Units         []InventoryRow `json:"units"`
}

// SummarizeInventory counts the instances of an index per unit and type.
func SummarizeInventory(ix *tfstate.Index, f InventoryFilter) *Inventory {
	double := map[[3]string]bool{}
	for _, dm := range ix.DoubleManaged {
		for _, r := range dm.Refs {
			double[[3]string{r.Unit, r.Workspace, r.Address}] = true
		}
	}
	byUnit := map[unitKey]*unitTally{}
	var order []unitKey
	for _, u := range ix.Units {
		if glob(f.Unit, u.Unit) {
			k := unitKey{u.Unit, u.Workspace}
			byUnit[k] = newTally(u)
			order = append(order, k)
		}
	}
	for i := range ix.Entries {
		e := &ix.Entries[i]
		if a := byUnit[unitKey{e.Unit, e.Workspace}]; a != nil && f.matches(e) {
			a.add(e, double[[3]string{e.Unit, e.Workspace, e.Address}])
		}
	}
	filtered := f.Account != "" || f.Region != "" || f.Type != ""
	out := &Inventory{Units: []InventoryRow{}}
	for _, k := range order {
		row := byUnit[k].row()
		if filtered && row.Instances == 0 {
			continue
		}
		out.Instances += row.Instances
		out.DoubleManaged += row.DoubleManaged
		out.Units = append(out.Units, row)
	}
	return out
}

func (f *InventoryFilter) matches(e *tfstate.Entry) bool {
	return (f.Account == "" || e.Account == f.Account) && (f.Region == "" || e.Region == f.Region) &&
		glob(f.Type, e.Type)
}

// unitTally counts one unit's instances while the entries are read.
type unitTally struct {
	r                InventoryRow
	types            map[string]int
	accounts, region map[string]bool
}

func newTally(u tfstate.IndexedUnit) *unitTally {
	return &unitTally{
		r:     InventoryRow{Unit: u.Unit, Workspace: u.Workspace},
		types: map[string]int{}, accounts: map[string]bool{}, region: map[string]bool{},
	}
}

func (a *unitTally) add(e *tfstate.Entry, double bool) {
	a.r.Instances++
	a.types[e.Type]++
	if e.Account != "" {
		a.accounts[e.Account] = true
	}
	if e.Region != "" {
		a.region[e.Region] = true
	}
	if double {
		a.r.DoubleManaged++
	}
}

func (a *unitTally) row() InventoryRow {
	r := a.r
	r.Accounts, r.Regions = sortedKeys(a.accounts), sortedKeys(a.region)
	r.Types = []TypeCount{}
	for _, t := range slices.Sorted(maps.Keys(a.types)) {
		r.Types = append(r.Types, TypeCount{Type: t, Count: a.types[t]})
	}
	return r
}

// ScanFilter selects scan findings. Empty fields match everything, except Category, whose
// empty value means unmanaged and double-managed.
type ScanFilter struct {
	Account  string
	Region   string
	Category string
	Type     string // glob on the CloudFormation or layer type
	Top      int    // findings to list, 1 to MaxTop; 0 means DefaultTop
}

// Source says where a scan summary came from.
type Source struct {
	File    string    `json:"file,omitempty"` // base name of the scan JSON
	Cache   bool      `json:"cache"`          // the newest scan cache snapshot, rules applied again
	Created time.Time `json:"created"`
	Stale   bool      `json:"stale"` // older than cache.ttl; run tfcensus scan to collect again
}

// ScanSummary is the counts of a scan and its top findings.
type ScanSummary struct {
	Source   Source         `json:"source"`
	Accounts []scan.Summary `json:"accounts"`
	Matching int            `json:"matching"` // findings that match the filter
	Types    []TypeCount    `json:"types"`    // matching findings per type, most first, at most MaxTypes
	Top      []scan.Finding `json:"top"`      // double-managed first, then unmanaged, then the rest
	Warnings []string       `json:"warnings"`
}

// CheckScanFilter validates the category and top.
func CheckScanFilter(f *ScanFilter) error {
	if f.Category != "" && !slices.Contains(scan.Categories(), scan.Category(f.Category)) {
		names := make([]string, 0, len(scan.Categories()))
		for _, c := range scan.Categories() {
			names = append(names, string(c))
		}
		return fmt.Errorf("unknown category %q: want one of %s", f.Category, strings.Join(names, ", "))
	}
	if f.Top < 0 || f.Top > MaxTop {
		return fmt.Errorf("top must be 1 to %d, got %d", MaxTop, f.Top)
	}
	return nil
}

// CategoryRank orders findings for listing: double-managed (0), then unmanaged (1), then the rest.
func CategoryRank(c scan.Category) int {
	if c == scan.CategoryDoubleManaged {
		return 0
	}
	if c == scan.CategoryUnmanaged {
		return 1
	}
	return 2 //nolint:mnd // everything else after the two that need action
}

// SummarizeScan counts a scan report and lists its top findings. Tags are left out of the
// findings; the owner tag stays in owner.
func SummarizeScan(rep *scan.Report, src Source, f ScanFilter) *ScanSummary {
	top := cmp.Or(f.Top, DefaultTop)
	out := &ScanSummary{Source: src, Accounts: []scan.Summary{}, Top: []scan.Finding{}, Warnings: rep.Warnings}
	if out.Warnings == nil {
		out.Warnings = []string{}
	}
	var picked []scan.Finding
	for i := range rep.Findings {
		if fd := rep.Findings[i]; f.matches(&fd) {
			fd.Tags = nil
			picked = append(picked, fd)
		}
	}
	slices.SortStableFunc(picked, func(a, b scan.Finding) int {
		return cmp.Compare(CategoryRank(a.Category), CategoryRank(b.Category))
	})
	out.Matching = len(picked)
	out.Types = typeCounts(picked)
	out.Top = append(out.Top, picked[:min(top, len(picked))]...)
	summaries := rep.Accounts
	if f.Region != "" {
		summaries = rep.Regions
	}
	for _, s := range summaries {
		if (f.Account == "" || s.Account == f.Account) && (f.Region == "" || s.Region == f.Region) {
			out.Accounts = append(out.Accounts, s)
		}
	}
	return out
}

// typeCounts counts findings per type, most first, then by name, keeping MaxTypes.
func typeCounts(fs []scan.Finding) []TypeCount {
	n := map[string]int{}
	for i := range fs {
		n[fs[i].Type()]++
	}
	out := make([]TypeCount, 0, len(n))
	for t, c := range n {
		out = append(out, TypeCount{Type: t, Count: c})
	}
	slices.SortFunc(out, func(a, b TypeCount) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), strings.Compare(a.Type, b.Type))
	})
	return out[:min(len(out), MaxTypes)]
}

func (f *ScanFilter) matches(fd *scan.Finding) bool {
	region := cmp.Or(fd.Region, inventory.GlobalColumn)
	if (f.Account != "" && fd.Account != f.Account) || (f.Region != "" && region != f.Region) ||
		!glob(f.Type, fd.Type()) {
		return false
	}
	if f.Category == "" {
		return fd.Category == scan.CategoryUnmanaged || fd.Category == scan.CategoryDoubleManaged
	}
	return string(fd.Category) == f.Category
}

// AuditFilter selects audit findings. Empty fields match everything.
type AuditFilter struct {
	Rule              string // glob on the check ID, such as S3-*
	Severity          string // the lowest severity listed: info, low, medium or high
	Account           string
	IncludeSuppressed bool
}

// AuditFindings is the backend audit's findings, highest severity first.
type AuditFindings struct {
	Counts     map[audit.Severity]int `json:"counts"` // unsuppressed findings per severity, before filtering
	Suppressed int                    `json:"suppressed"`
	Findings   []audit.Finding        `json:"findings"`
	Incomplete []audit.Problem        `json:"incomplete"`
	Warnings   []string               `json:"warnings"`
}

// SeverityRank orders severities from high (0) to info (3); unknown ones come last.
func SeverityRank(s audit.Severity) int {
	switch s {
	case audit.SeverityHigh:
		return 0
	case audit.SeverityMedium:
		return 1
	case audit.SeverityLow:
		return 2 //nolint:mnd // the order of the four severities
	case audit.SeverityInfo:
		return 3 //nolint:mnd // the order of the four severities
	}
	return 4 //nolint:mnd // after every known severity
}

// CheckAuditFilter validates the severity.
func CheckAuditFilter(f *AuditFilter) error {
	if f.Severity != "" && SeverityRank(audit.Severity(f.Severity)) > SeverityRank(audit.SeverityInfo) {
		return fmt.Errorf("unknown severity %q: want info, low, medium or high", f.Severity)
	}
	return nil
}

// SummarizeAudit filters and orders the findings of an audit report.
func SummarizeAudit(rep *audit.Report, f AuditFilter) *AuditFindings {
	out := &AuditFindings{
		Counts: make(map[audit.Severity]int), Suppressed: len(rep.Suppressed), Findings: []audit.Finding{},
		Incomplete: rep.Incomplete, Warnings: rep.Warnings,
	}
	if out.Incomplete == nil {
		out.Incomplete = []audit.Problem{}
	}
	if out.Warnings == nil {
		out.Warnings = []string{}
	}
	all := slices.Clone(rep.Findings)
	for _, fd := range rep.Findings {
		out.Counts[fd.Severity]++
	}
	if f.IncludeSuppressed {
		all = append(all, rep.Suppressed...)
	}
	for _, fd := range all {
		if !glob(f.Rule, fd.Rule) || (f.Account != "" && fd.Account != f.Account) {
			continue
		}
		if f.Severity != "" && SeverityRank(fd.Severity) > SeverityRank(audit.Severity(f.Severity)) {
			continue
		}
		out.Findings = append(out.Findings, fd)
	}
	slices.SortStableFunc(out.Findings, func(a, b audit.Finding) int {
		return cmp.Compare(SeverityRank(a.Severity), SeverityRank(b.Severity))
	})
	return out
}

// DriftFilter selects drift units and changes. Empty fields match everything; an empty status
// lists the drifted and failed units.
type DriftFilter struct {
	Unit   string // glob on the unit path
	Status string // drifted, failed, clean, early-exit or excluded
}

// DriftChange is one visible drifted resource, with attribute paths but never values.
type DriftChange struct {
	Unit       string       `json:"unit"`
	Address    string       `json:"address"`
	Type       string       `json:"type"`
	Action     drift.Action `json:"action"`
	Revert     drift.Action `json:"revert,omitempty"`
	Attributes []string     `json:"attributes"`
}

// DriftSummary is a drift report reduced to unit statuses and visible changes.
type DriftSummary struct {
	Mode     drift.Mode    `json:"mode"`
	Summary  drift.Summary `json:"summary"`
	Units    []drift.Unit  `json:"units"`
	Changes  []DriftChange `json:"changes"`
	Warnings []string      `json:"warnings"`
}

// CheckDriftFilter validates the status.
func CheckDriftFilter(f *DriftFilter) error {
	switch drift.Status(f.Status) {
	case "", drift.StatusDrifted, drift.StatusFailed, drift.StatusClean, drift.StatusEarlyExit, drift.StatusExcluded:
		return nil
	}
	return fmt.Errorf("unknown status %q: want drifted, failed, clean, early-exit or excluded", f.Status)
}

// SummarizeDrift lists unit statuses and the changes no noise rule hides.
func SummarizeDrift(rep *drift.Report, f DriftFilter) *DriftSummary {
	out := &DriftSummary{
		Mode: rep.Mode, Summary: rep.Summary, Units: []drift.Unit{}, Changes: []DriftChange{}, Warnings: rep.Warnings,
	}
	if out.Warnings == nil {
		out.Warnings = []string{}
	}
	listed := map[string]bool{}
	for _, u := range rep.Units {
		if !glob(f.Unit, u.Path) {
			continue
		}
		if f.Status == "" && u.Status != drift.StatusDrifted && u.Status != drift.StatusFailed {
			continue
		}
		if f.Status != "" && string(u.Status) != f.Status {
			continue
		}
		listed[u.Path] = true
		out.Units = append(out.Units, u)
	}
	for _, c := range rep.VisibleChanges() {
		if !listed[c.Unit] {
			continue
		}
		dc := DriftChange{
			Unit:       c.Unit,
			Address:    c.Address,
			Type:       c.Type,
			Action:     c.Action,
			Revert:     c.Revert,
			Attributes: []string{},
		}
		for _, a := range c.Visible() {
			dc.Attributes = append(dc.Attributes, a.Path)
		}
		out.Changes = append(out.Changes, dc)
	}
	return out
}
