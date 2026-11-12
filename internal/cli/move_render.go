package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/buildinfo"
	"github.com/sassdavid/tfcensus/internal/migrate"
	"github.com/sassdavid/tfcensus/internal/render"
	"github.com/sassdavid/tfcensus/internal/sarif"
)

func writeMove(d *app.Deps, format render.Format, rep *migrate.Report) error {
	switch format { //nolint:exhaustive // the command checks the format first
	case render.FormatJSON:
		return render.JSON(d.Stdout, render.NewEnvelope(buildinfo.Get().Version, d.Clock(), rep))
	case render.FormatSARIF:
		return sarif.Write(d.Stdout, moveSARIF(rep))
	default:
		return writeMoveText(d.Stdout, rep)
	}
}

type moveRule struct{ id, title, help string }

func moveRules() []moveRule {
	return []moveRule{
		{
			migrate.RuleVerify, "A plan against the moved state changes a resource",
			"Make the unit's configuration match the moved resources, or fix the provider so it targets the " +
				"account the resources are in, then plan again.",
		},
		{
			migrate.RulePlan, "A plan against the moved state failed",
			"Run terragrunt plan in the unit to see the error.",
		},
		{
			migrate.RuleAccount, "A moved resource is in another account than the destination deploys to",
			"Move the resource to a unit in its own account, or check the destination's provider and backend.",
		},
		{
			migrate.RuleVersioning, "The state bucket has no versioning, so a push can't be rolled back",
			"Enable versioning on the bucket (backend audit check S3-VER) before applying.",
		},
	}
}

func moveSARIF(rep *migrate.Report) *sarif.Log {
	l := sarif.New(render.ToolName, buildinfo.Get().Version, infoURI)
	run := &l.Runs[0]
	index := map[string]int{}
	for i, r := range moveRules() {
		index[r.id] = i
		run.Tool.Driver.Rules = append(run.Tool.Driver.Rules, sarif.Rule{
			ID:                   r.id,
			ShortDescription:     sarif.Message{Text: r.title},
			Help:                 sarif.Message{Text: r.help},
			DefaultConfiguration: sarif.Configuration{Level: sarif.LevelError},
		})
	}
	for _, b := range rep.Blocks {
		for _, f := range b.Findings {
			msg := f.Reason
			if f.Address != "" {
				msg = f.Address + ": " + msg
			}
			run.Results = append(run.Results, sarif.Result{
				RuleID:    f.Rule,
				RuleIndex: index[f.Rule],
				Level:     sarif.LevelError,
				Message:   sarif.Message{Text: "block " + b.Name + ": " + msg},
				Locations: []sarif.Location{
					{PhysicalLocation: sarif.PhysicalLocation{ArtifactLocation: sarif.ArtifactLocation{URI: f.Unit}}},
				},
				PartialFingerprints: map[string]string{
					fingerprintV1: f.Rule + "|" + rep.File + "|" + b.Name + "|" + f.Unit + "|" + f.Address,
				},
			})
		}
	}
	return l
}

func moveChange(u *migrate.UnitReport) string {
	n := len(u.Moved)
	switch u.Role {
	case migrate.RoleSource:
		return "-" + strconv.Itoa(n)
	case migrate.RoleDestination:
		return "+" + strconv.Itoa(n)
	default:
		return strconv.Itoa(n) + " action(s)"
	}
}

func moveSerial(u *migrate.UnitReport) string {
	switch {
	case u.File != "":
		return u.File
	case u.Serial == nil:
		return ""
	case u.NewState:
		return fmt.Sprintf("%d -> %d (new state)", u.Serial.Before, u.Serial.After)
	default:
		return fmt.Sprintf("%d -> %d", u.Serial.Before, u.Serial.After)
	}
}

func moveVersions(v *migrate.VersionIDs) string {
	if v == nil {
		return ""
	}
	before := v.Before
	if before == "" {
		before = "(none)"
	}
	return "version " + before + " -> " + v.After
}

func writeMoveText(w io.Writer, rep *migrate.Report) error {
	var b strings.Builder
	fmt.Fprintf(&b, "move %s %s\n", rep.Command, rep.File)
	for i := range rep.Blocks {
		writeMoveBlock(&b, rep.Command, &rep.Blocks[i])
	}
	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("write move report: %w", err)
	}
	return nil
}

func writeMoveBlock(b *strings.Builder, command string, br *migrate.BlockReport) {
	head := br.Status
	if br.Result != "" {
		head += ", " + br.Result
	}
	fmt.Fprintf(b, "\nBlock %s (%s): %s\n", br.Name, br.Strategy, head)
	tw := tabwriter.NewWriter(b, 0, 0, 2, ' ', 0) //nolint:mnd // two spaces between columns
	for i := range br.Units {
		u := &br.Units[i]
		cols := []string{u.Path, u.Role}
		switch moveCommand(command) {
		case moveStatus:
			cols = append(cols, u.Status, moveVersions(u.VersionIDs))
		case moveRollback, moveMarkApplied:
			cols = append(cols, moveVersions(u.VersionIDs))
		case movePlan, moveApply:
			cols = append(cols, moveChange(u), moveSerial(u), moveVersions(u.VersionIDs))
		}
		fmt.Fprintf(tw, "  %s\n", strings.TrimRight(strings.Join(cols, "\t"), "\t"))
	}
	_ = tw.Flush()
	moved, skipped := movedLines(br)
	section(b, "Moved", moved)
	section(b, "Data sources left in the source", skipped)
	var findings []string
	for _, f := range br.Findings {
		line := f.Rule + " " + f.Unit
		if f.Address != "" {
			line += " " + f.Address
		}
		findings = append(findings, line+": "+f.Reason)
	}
	section(b, "Findings", findings)
	section(b, "Notes", br.Notes)
}

// movedLines lists each moved address with its destination, and the data sources left behind.
func movedLines(br *migrate.BlockReport) ([]string, []string) {
	var moved, skipped []string
	for _, u := range br.Units {
		switch u.Role {
		case migrate.RoleSource:
			skipped = append(skipped, u.Skipped...)
		case migrate.RoleDestination:
			for _, m := range u.Moved {
				moved = append(moved, m+" -> "+u.Path)
			}
		default:
			moved = append(moved, u.Moved...)
		}
	}
	return moved, skipped
}

func section(b *strings.Builder, title string, lines []string) {
	if len(lines) == 0 {
		return
	}
	fmt.Fprintf(b, "  %s\n", title)
	for _, l := range lines {
		fmt.Fprintf(b, "    %s\n", l)
	}
}
