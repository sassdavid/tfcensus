package mcpserver

import (
	"fmt"
	"strings"

	"github.com/sassdavid/tfcensus/internal/mcpkit"
)

// Prompts lists the prompt names, in registration order.
func Prompts() []string {
	return []string{"triage_drift", "explain_unmanaged"}
}

// noValues ends every prompt. State and plans hold secrets, so the agent works from the
// identifiers the tools return and never asks for more.
const noValues = `Never ask the user for attribute values, and never ask them to paste state, plan output, ` +
	`terraform show output or AWS describe output. tfcensus returns identifiers and attribute paths ` +
	`only, because state holds secrets. When a decision needs a value, name the unit and address or ` +
	`the ARN to check, and leave that check to the user.`

func addPrompts(s *mcpkit.Server) {
	mcpkit.AddPrompt(s, mcpkit.Prompt{
		Name:  "triage_drift",
		Title: "Triage drift",
		Description: "Sorts the drifted and failed units of the saved drift report into revert, adopt and " +
			"investigate, citing unit paths, resource addresses and attribute paths.",
		Arguments: []mcpkit.PromptArgument{
			{Name: "unit", Description: "Unit path glob to triage; * also matches /. Default: every unit."},
		},
	}, triageDrift)
	mcpkit.AddPrompt(s, mcpkit.Prompt{
		Name:  "explain_unmanaged",
		Title: "Explain unmanaged resources",
		Description: "Explains the unmanaged resources of the last scan by rule and proposes import, an " +
			"ownership rule or a deletion review for each group, citing rule IDs and ARNs.",
		Arguments: []mcpkit.PromptArgument{
			{Name: "account", Description: "AWS account ID. Default: every account."},
			{Name: "type", Description: "CloudFormation or layer type glob, such as AWS::EC2::*. Default: every type."},
		},
	}, explainUnmanaged)
}

func triageDrift(args map[string]string) (string, error) {
	var filter string
	if u, ok := args["unit"]; ok {
		filter = fmt.Sprintf(" with unit %q", u)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Triage the drift in the saved tfcensus drift report.\n\n")
	fmt.Fprintf(&b, "1. Call drift_summary%s. Follow page.next_cursor until you have every page. "+
		"Repeat its warnings and errors.\n", filter)
	b.WriteString("2. List the failed units first, with each path and reason, and say what has to be " +
		"fixed before tfcensus can measure drift there.\n")
	b.WriteString("3. For each drifted unit, group its changes by resource address. Give the TF type, " +
		"the action and the attribute paths. Propose revert (apply the unit's code again), adopt " +
		"(change the code to match AWS) or investigate (the change looks unplanned or touches IAM, " +
		"security groups, KMS or networking). Base each proposal on the type, action and attribute " +
		"paths alone.\n")
	b.WriteString("4. A unit's hidden count says how many changes noise rules hid. When the same " +
		"harmless attribute path drifts in many units, suggest a drift.noise rule with a reason for " +
		".tfcensus.yaml. Name noise rules by their IDs, preset/<name> or user/<name>.\n")
	b.WriteString("5. When a change might come from a second state, call whois with the resource's " +
		"ARN or ID to see which units manage it.\n\n")
	b.WriteString("Cite every claim with the unit path and resource address, and the attribute path " +
		"where it applies, exactly as drift_summary returns them. Don't invent addresses.\n")
	b.WriteString(noValues)
	return b.String(), nil
}

func explainUnmanaged(args map[string]string) (string, error) {
	filter := ` with category "unmanaged"`
	if a, ok := args["account"]; ok {
		filter += fmt.Sprintf(", account %q", a)
	}
	if t, ok := args["type"]; ok {
		filter += fmt.Sprintf(", type %q", t)
	}
	var b strings.Builder
	b.WriteString("Explain the unmanaged resources in the last tfcensus scan.\n\n")
	fmt.Fprintf(&b, "1. Call scan_summary%s. Follow page.next_cursor until you have every page. "+
		"Say when the scan ran (source.created), and repeat its warnings and coverage gaps.\n", filter)
	b.WriteString("2. Group the findings by account, region and type. For each group give the count " +
		"and up to 10 ARNs or IDs.\n")
	b.WriteString("3. Explain why tfcensus counts each group unmanaged from the findings' rule and " +
		"reason. rules/no-match means no state manages the resource and no ownership rule matches it.\n")
	b.WriteString("4. Call whois with a few ARNs or IDs of each group. It confirms that no state " +
		"manages them, or names the rule that explains them.\n")
	b.WriteString("5. Propose one action per group: import into a unit (name a likely unit from units " +
		"or inventory by account, region and TF type), an ownership rule with a reason in " +
		".tfcensus.yaml when another team or tool owns the resources, or a deletion review. Say which " +
		"group to check first and why.\n")
	b.WriteString(`6. Call scan_summary with category "undetermined" under the same filters and give ` +
		"its count. Those types have no mapping, so tfcensus can't tell whether a state manages " +
		"them. Never count them as unmanaged.\n\n")
	b.WriteString("Cite every claim with the rule ID and the ARN or ID, exactly as scan_summary and " +
		"whois return them. Use only the names and tags those tools return.\n")
	b.WriteString(noValues)
	return b.String(), nil
}
