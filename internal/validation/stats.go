package validation

import (
	"cmp"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
)

// Verdicts a reviewer gives an unmanaged finding.
const (
	VerdictFP     = "fp"
	VerdictTP     = "tp"
	VerdictUnsure = "unsure"
)

// ErrCSV is returned for a CSV that isn't a reviewed scan CSV.
var ErrCSV = errors.New("not a reviewed scan CSV")

// Stats are the counts of one reviewed scan.
type Stats struct {
	Resources  int
	ByCategory map[string]int
	ByVerdict  map[string]int            // unmanaged findings per verdict; "" when not reviewed
	ByType     map[string]map[string]int // unmanaged findings per type and verdict
	FPByGroup  map[string]int            // false positives per group, when the CSV has a group column
}

// Read reads a scan CSV (scan --format csv) with a verdict column and an optional group column.
func Read(r io.Reader) (*Stats, error) {
	cr := csv.NewReader(r)
	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	col := map[string]int{}
	for i, h := range header {
		col[strings.TrimSpace(h)] = i
	}
	for _, need := range []string{"category", "cfn_type", "source_type", "verdict"} {
		if _, ok := col[need]; !ok {
			return nil, fmt.Errorf("%w: no %s column", ErrCSV, need)
		}
	}
	s := &Stats{
		ByCategory: map[string]int{}, ByVerdict: map[string]int{}, ByType: map[string]map[string]int{},
		FPByGroup: map[string]int{},
	}
	for line := 2; ; line++ {
		row, rerr := cr.Read()
		if errors.Is(rerr, io.EOF) {
			return s, nil
		}
		if rerr != nil {
			return nil, fmt.Errorf("read line %d: %w", line, rerr)
		}
		if err = s.add(row, col, line); err != nil {
			return nil, err
		}
	}
}

func (s *Stats) add(row []string, col map[string]int, line int) error {
	field := func(name string) string {
		if i, ok := col[name]; ok && i < len(row) {
			return strings.TrimSpace(row[i])
		}
		return ""
	}
	s.Resources++
	category := field("category")
	s.ByCategory[category]++
	if category != "unmanaged" {
		return nil
	}
	verdict := strings.ToLower(field("verdict"))
	if !slices.Contains([]string{VerdictFP, VerdictTP, VerdictUnsure, ""}, verdict) {
		return fmt.Errorf("%w: line %d: verdict %q: want fp, tp or unsure", ErrCSV, line, verdict)
	}
	s.ByVerdict[verdict]++
	typ := cmp.Or(field("cfn_type"), field("source_type"), "unknown")
	if s.ByType[typ] == nil {
		s.ByType[typ] = map[string]int{}
	}
	s.ByType[typ][verdict]++
	if verdict == VerdictFP {
		s.FPByGroup[cmp.Or(field("group"), "no group")]++
	}
	return nil
}

// FPRate returns the share of unmanaged findings the reviewer marked fp, in percent, and false
// when there is no unmanaged finding.
func (s *Stats) FPRate() (float64, bool) {
	n := s.ByCategory["unmanaged"]
	if n == 0 {
		return 0, false
	}
	const percent = 100
	return float64(s.ByVerdict[VerdictFP]) * percent / float64(n), true
}

// Write prints the row for docs/validation/nonprod-scan.md and the tables behind it, as Markdown.
func (s *Stats) Write(w io.Writer) error {
	rate := "-"
	if r, ok := s.FPRate(); ok {
		rate = fmt.Sprintf("%.1f%%", r)
	}
	var b strings.Builder
	b.WriteString("| resources | unmanaged | fp | tp | unsure | not reviewed | fp rate | undetermined |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- | --- | --- |\n")
	fmt.Fprintf(&b, "| %d | %d | %d | %d | %d | %d | %s | %d |\n", s.Resources, s.ByCategory["unmanaged"],
		s.ByVerdict[VerdictFP], s.ByVerdict[VerdictTP], s.ByVerdict[VerdictUnsure], s.ByVerdict[""], rate,
		s.ByCategory["undetermined"])
	b.WriteString("\n| category | count |\n| --- | --- |\n")
	for _, c := range sortedByCount(s.ByCategory) {
		fmt.Fprintf(&b, "| %s | %d |\n", c, s.ByCategory[c])
	}
	if len(s.ByType) > 0 {
		b.WriteString("\n| unmanaged type | fp | tp | unsure | not reviewed |\n| --- | --- | --- | --- | --- |\n")
		for _, t := range slices.Sorted(maps.Keys(s.ByType)) {
			v := s.ByType[t]
			fmt.Fprintf(&b, "| %s | %d | %d | %d | %d |\n", t, v[VerdictFP], v[VerdictTP], v[VerdictUnsure], v[""])
		}
	}
	if len(s.FPByGroup) > 0 {
		b.WriteString("\n| false-positive group | count |\n| --- | --- |\n")
		for _, g := range sortedByCount(s.FPByGroup) {
			fmt.Fprintf(&b, "| %s | %d |\n", g, s.FPByGroup[g])
		}
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("write stats: %w", err)
	}
	return nil
}

func sortedByCount(m map[string]int) []string {
	keys := slices.Collect(maps.Keys(m))
	slices.SortFunc(keys, func(a, b string) int { return cmp.Or(cmp.Compare(m[b], m[a]), strings.Compare(a, b)) })
	return keys
}
