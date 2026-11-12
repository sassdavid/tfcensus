package drift

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	pathpkg "path"
	"path/filepath"
	"slices"
	"strings"

	tfjson "github.com/hashicorp/terraform-json"
)

// PlanFile is the name terragrunt gives each unit's plan under --json-out-dir.
const PlanFile = "tfplan.json"

// Terragrunt run report results. The JSON report format is covered by terragrunt's 1.x
// compatibility promise; fields it adds later are ignored.
const (
	resultSucceeded = "succeeded"
	resultFailed    = "failed"
	resultEarlyExit = "early exit"
	resultExcluded  = "excluded"
)

// run is one entry of terragrunt's --report-format json.
type run struct {
	Name   string  `json:"Name"`
	Result string  `json:"Result"`
	Reason *string `json:"Reason"`
	Cause  *string `json:"Cause"`
}

// Source is a plan directory and the run report that goes with it.
type Source struct {
	PlanDir string // --json-out-dir: <unit>/tfplan.json for each unit
	Report  string // terragrunt --report-file in JSON; empty when there is none
	Prefix  string // prepended to every unit path, such as the repo in run mode
}

// Input is one unit and its plan. Plan is nil unless the unit succeeded.
type Input struct {
	Unit Unit
	Plan *tfjson.Plan
}

// ErrSource is wrapped by errors about the plan directory or the report as a whole.
var ErrSource = errors.New("invalid drift input")

// Load reads every plan under src.PlanDir and the report. A unit whose plan is missing or can't
// be parsed is failed. It returns the units sorted by path, and warnings.
func Load(src Source) ([]Input, []string, error) {
	plans, err := planFiles(src.PlanDir)
	if err != nil {
		return nil, nil, err
	}
	var warnings []string
	runs := map[string]run{}
	if src.Report != "" {
		if runs, err = readReport(src.Report); err != nil {
			return nil, nil, err
		}
	} else {
		warnings = append(warnings, "no run report: units that failed before writing a plan aren't listed")
	}
	names := slices.Collect(maps.Keys(runs))
	for unit := range plans {
		if _, ok := runs[unit]; !ok {
			names = append(names, unit)
			if src.Report != "" {
				warnings = append(warnings, "unit "+unit+" has a plan but isn't in the run report")
			}
		}
	}
	slices.Sort(names)
	names = slices.Compact(names)
	inputs := make([]Input, 0, len(names))
	for _, name := range names {
		r, inReport := runs[name]
		if !inReport {
			r = run{Name: name, Result: resultSucceeded}
		}
		inputs = append(inputs, input(joinUnit(src.Prefix, name), r, plans[name]))
	}
	slices.Sort(warnings)
	return inputs, warnings, nil
}

func joinUnit(prefix, unit string) string {
	if prefix == "" {
		return unit
	}
	return pathpkg.Join(prefix, unit)
}

func input(unit string, r run, planFile string) Input {
	in := Input{Unit: Unit{Path: unit, Outputs: []string{}}}
	reason := strings.TrimSpace(deref(r.Reason) + " " + deref(r.Cause))
	switch r.Result {
	case resultFailed:
		in.Unit.Status, in.Unit.Reason = StatusFailed, cmp.Or(reason, "plan failed")
	case resultEarlyExit:
		in.Unit.Status, in.Unit.Reason = StatusEarlyExit, cmp.Or(reason, "a dependency failed")
	case resultExcluded:
		in.Unit.Status, in.Unit.Reason = StatusExcluded, cmp.Or(reason, "excluded")
	case resultSucceeded:
		if planFile == "" {
			in.Unit.Status, in.Unit.Reason = StatusFailed, "succeeded but wrote no "+PlanFile
			break
		}
		p, err := readPlan(planFile)
		if err != nil {
			in.Unit.Status, in.Unit.Reason = StatusFailed, err.Error()
			break
		}
		in.Unit.Status, in.Plan = StatusClean, p
	default:
		in.Unit.Status, in.Unit.Reason = StatusFailed, fmt.Sprintf("unknown run result %q", r.Result)
	}
	return in
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// planFiles maps each unit path (slash-separated, relative to dir) to its plan file.
func planFiles(dir string) (map[string]string, error) {
	fi, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("%w: plan directory: %w", ErrSource, err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("%w: plan directory %s is not a directory", ErrSource, dir)
	}
	out := map[string]string{}
	err = filepath.WalkDir(dir, func(p string, e fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if e.IsDir() || e.Name() != PlanFile {
			return nil
		}
		rel, relErr := filepath.Rel(dir, filepath.Dir(p))
		if relErr != nil {
			return fmt.Errorf("relativize %s: %w", p, relErr)
		}
		out[filepath.ToSlash(rel)] = p
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("%w: read plan directory: %w", ErrSource, err)
	}
	return out, nil
}

// readReport reads terragrunt's JSON run report, keyed by unit path.
func readReport(file string) (map[string]run, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("%w: run report: %w", ErrSource, err)
	}
	var runs []run
	if err = json.Unmarshal(b, &runs); err != nil {
		return nil, fmt.Errorf("%w: run report %s: %w", ErrSource, file, err)
	}
	out := make(map[string]run, len(runs))
	for _, r := range runs {
		if r.Name == "" {
			return nil, fmt.Errorf("%w: run report %s: an entry has no Name", ErrSource, file)
		}
		out[filepath.ToSlash(r.Name)] = r
	}
	return out, nil
}
