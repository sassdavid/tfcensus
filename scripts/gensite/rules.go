package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/sassdavid/tfcensus/internal/audit"
	"github.com/sassdavid/tfcensus/internal/drift"
	"github.com/sassdavid/tfcensus/internal/match"
	"github.com/sassdavid/tfcensus/internal/migrate"
	"github.com/sassdavid/tfcensus/internal/ownership"
	"github.com/sassdavid/tfcensus/internal/scan"
	"github.com/sassdavid/tfcensus/internal/whois"
	"github.com/sassdavid/tfcensus/schemas"
)

const packDir = "internal/ownership/packs"

// packComment returns the comment lines at the top of a pack file, joined into one paragraph.
func packComment(root, pack string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, packDir, pack+".yaml"))
	if err != nil {
		return "", fmt.Errorf("read pack %s: %w", pack, err)
	}
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line, ok := strings.CutPrefix(sc.Text(), "#")
		if !ok {
			break
		}
		lines = append(lines, strings.TrimSpace(line))
	}
	return strings.Join(lines, " "), nil
}

func rulePacksPage(root string) ([]byte, error) {
	var b strings.Builder
	b.WriteString(frontmatter("Ownership rule packs", "Every rule of the built-in ownership packs and the "+
		"guards: what each matches, the category it sets and why."))
	b.WriteString("# Ownership rule packs\n\n")
	b.WriteString("`scan` runs these packs, in this order, on every resource that no state manages. Then it " +
		"runs your `rules`. The first rule that matches sets the resource's category, owner and reason, " +
		"and the finding carries the rule's ID. Turn a pack off with `scan.packs.<name>: false`. " +
		"The fields and functions a `match` expression can use are on the [CEL reference](/reference/cel).\n\n")
	packs := append(ownership.PackOrder(), ownership.GuardPack)
	var rows [][]string
	for _, p := range packs {
		rules, err := ownership.PackRules(p)
		if err != nil {
			return nil, err //nolint:wrapcheck // the error names the pack
		}
		cat := "guard"
		if p != ownership.GuardPack && len(rules) > 0 {
			cat = string(rules[0].Category)
		}
		rows = append(rows, []string{"[" + p + "](#" + slug(p) + ")", code(cat), strconv.Itoa(len(rules))})
	}
	table(&b, []string{"Pack", "Category", "Rules"}, rows)
	for _, p := range packs {
		if err := writePack(&b, root, p); err != nil {
			return nil, err
		}
	}
	return []byte(b.String()), nil
}

func writePack(b *strings.Builder, root, pack string) error {
	rules, err := ownership.PackRules(pack)
	if err != nil {
		return err //nolint:wrapcheck // the error names the pack
	}
	comment, err := packComment(root, pack)
	if err != nil {
		return err
	}
	fmt.Fprintf(b, "## %s\n\n%s\n\n", pack, cell(comment))
	if pack == ownership.GuardPack {
		b.WriteString("A guard sets no category. A user rule whose category is listed in `scan.never_hide` " +
			"doesn't apply to a guarded resource, so the resource stays unmanaged.\n\n")
	} else {
		fmt.Fprintf(b, "Category `%s`. Config key `scan.packs.%s`, on by default.\n\n", rules[0].Category, pack)
	}
	for _, r := range rules {
		id := packRuleID(pack, r.Name)
		fmt.Fprintf(b, "#### %s {#%s}\n\n", r.Name, slug(id))
		fmt.Fprintf(b, "Rule `%s`", id)
		if r.Owner != "" {
			fmt.Fprintf(b, ", owner `%s`", r.Owner)
		}
		fmt.Fprintf(b, ": %s.\n\n", cell(r.Reason))
		b.WriteString(fenced("text", strings.TrimSpace(r.Match)) + "\n")
	}
	return nil
}

// packRuleID returns the rule ID that findings of a pack rule or guard carry.
func packRuleID(pack, name string) string {
	if pack == ownership.GuardPack {
		return "guard/" + name
	}
	return "pack/" + pack + "/" + name
}

// celDocs describes every field a rule can read, keyed by its CEL path.
func celDocs() map[string]string {
	return map[string]string{
		"resource.account":       "Account ID the resource is in.",
		"resource.region":        "Region. Empty for account-wide resources such as IAM roles.",
		"resource.type":          "CloudFormation type, such as `AWS::EC2::VPC`. Empty when the layer's type has no mapping.",
		"resource.source_type":   "The layer's own type name, such as `codedeploy:deploymentconfig`, when the CloudFormation type is unknown.",
		"resource.tf_types":      "Terraform resource types that map to the CloudFormation type.",
		"resource.arn":           "ARN, when the layer gives one.",
		"resource.id":            "The layer's resource ID, such as AWS Config's `resourceId`.",
		"resource.name":          "Name, or the name in the ARN when the layer gives none.",
		"resource.tags":          "Tags. An empty map when the resource has none, so `\"k\" in resource.tags` works.",
		"resource.attrs":         "Identifiers a layer read, such as `is_default` or `role`. Read one with `resource.attrs.?key.orValue(\"\")`.",
		"resource.layer":         "The first layer that reported the resource, such as `config`.",
		"resource.sources":       "Every layer that reported the resource, in precedence order.",
		"resource.parents":       "Parent resources.",
		"resource.relationships": "Every related resource, the parents included.",
		"resource.created":       "Creation time in RFC 3339, when the layer knows it.",
		"related.type":           "CloudFormation type.",
		"related.id":             "Resource ID.",
		"related.arn":            "ARN, from the relationship or the inventory.",
		"related.name":           "Name, from the inventory.",
		"related.relationship":   "The relationship as the layer names it, such as `Is attached to Instance`.",
		"related.tags":           "Tags, from the inventory.",
		"related.attrs":          "Identifiers, from the inventory.",
		"related.found":          "Whether the inventory has the related resource. Name, tags and attrs are empty when it doesn't.",
		"unit.repo":              "Repository of the unit that manages the resource.",
		"unit.path":              "Unit path in the repository.",
		"unit.workspace":         "Workspace.",
		"unit.address":           "Resource address in the state, such as `aws_iam_role.app`.",
	}
}

// celField is one field of a CEL object type.
type celField struct {
	path, typ string
}

// celFields returns the fields of the object types a rule sees, from the cel struct tags.
func celFields() []celField {
	const resourceVar, relatedObj, unitVar = "resource", "related", "unit"
	var out []celField
	for _, t := range []struct {
		prefix string
		typ    reflect.Type
	}{
		{resourceVar, reflect.TypeFor[ownership.Resource]()},
		{relatedObj, reflect.TypeFor[ownership.Related]()},
		{unitVar, reflect.TypeFor[ownership.Unit]()},
	} {
		for f := range t.typ.Fields() {
			if name := f.Tag.Get("cel"); name != "" {
				out = append(out, celField{t.prefix + "." + name, celType(f.Type)})
			}
		}
	}
	return out
}

func celType(t reflect.Type) string {
	if t.Kind() == reflect.Slice {
		return "list(" + celType(t.Elem()) + ")"
	}
	if t.Kind() == reflect.Map {
		return "map(" + celType(t.Key()) + ", " + celType(t.Elem()) + ")"
	}
	if t.Kind() == reflect.Struct {
		return t.Name()
	}
	return t.Kind().String()
}

func celPart() ([]byte, error) {
	docs := celDocs()
	var b strings.Builder
	b.WriteString(partNote)
	group := ""
	var rows [][]string
	flush := func() {
		if len(rows) > 0 {
			table(&b, []string{"Field", colType, "Description"}, rows)
			rows = nil
		}
	}
	for _, f := range celFields() {
		g, _, _ := strings.Cut(f.path, ".")
		if g != group {
			flush()
			group = g
			title := g
			if g == "related" {
				title = "Related (parents and relationships)"
			}
			fmt.Fprintf(&b, "### %s\n\n", title)
		}
		desc, ok := docs[f.path]
		if !ok {
			return nil, fmt.Errorf("gensite: celDocs has no description for %s", f.path)
		}
		rows = append(rows, []string{code(f.path), code(f.typ), cell(desc)})
	}
	flush()
	return []byte(b.String()), nil
}

func auditChecksPage() ([]byte, error) {
	tuned, err := auditPolicyKeys()
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString(frontmatter("Backend audit checks", "Every check of tfcensus audit backend with its severity, "+
		"what passes and how to fix a finding."))
	b.WriteString("# Backend audit checks\n\n")
	fmt.Fprintf(&b, "`tfcensus audit backend` runs %d checks on the state buckets, lock tables and states its "+
		"units use. Every finding carries the check ID as its `rule`, a severity and a reason. "+
		"`--fail-on` takes the lowest severity that fails the run. Keys under `audit.policy` set the "+
		"thresholds, and `audit.suppress` hides findings with a reason and an optional `expires` date "+
		"([configuration](/reference/config#audit)).\n\n", len(audit.Checks()))
	groups := []struct{ prefix, title, text string }{
		{"S3-", "State buckets", "Run once per state bucket."},
		{"LK-", "Locks", "Run once per unit backend and lock table."},
		{"FL-", "Fleet and states", "Run over the states and the units that point at them."},
	}
	seen := 0
	for _, g := range groups {
		fmt.Fprintf(&b, "## %s\n\n%s\n\n", g.title, g.text)
		var rows [][]string
		for _, c := range audit.Checks() {
			if !strings.HasPrefix(c.ID, g.prefix) {
				continue
			}
			seen++
			rows = append(rows, []string{
				code(c.ID), string(c.Severity), cell(c.Title), cell(c.Remediation), tuned[c.ID],
			})
		}
		table(&b, []string{"Check", "Severity", "Passes when", "Fix", "Set by"}, rows)
	}
	if seen != len(audit.Checks()) {
		return nil, fmt.Errorf("gensite: %d audit checks have no group", len(audit.Checks())-seen)
	}
	b.WriteString("## Suppress a finding\n\n")
	b.WriteString(fenced("yaml", `audit:
  suppress:
    - check: S3-MFA
      reason: MFA delete needs the root user's device
    - check: LK-*
      bucket: acme-legacy-tfstate
      reason: the legacy fleet moves to lock files in Q1
      expires: "2027-03-31"`))
	return []byte(b.String()), nil
}

// auditPolicyKeys returns, for each check ID, the audit.policy keys whose description names it,
// as links to the config reference.
func auditPolicyKeys() (map[string]string, error) {
	raw, err := schemas.Read(schemas.ConfigV1)
	if err != nil {
		return nil, err //nolint:wrapcheck // the error names the schema
	}
	var s struct {
		Properties struct {
			Audit struct {
				Properties struct {
					Policy struct {
						Properties map[string]struct {
							Description string `json:"description"`
						} `json:"properties"`
					} `json:"policy"`
				} `json:"properties"`
			} `json:"audit"`
		} `json:"properties"`
	}
	if err = json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("decode %s: %w", schemas.ConfigV1, err)
	}
	out := map[string]string{}
	props := s.Properties.Audit.Properties.Policy.Properties
	for _, name := range slicesSorted(props) {
		key := "audit.policy." + name
		for _, c := range audit.Checks() {
			if strings.Contains(props[name].Description, c.ID) {
				link := "[`" + key + "`](/reference/config#" + slug(key) + ")"
				out[c.ID] = strings.TrimPrefix(out[c.ID]+", ", ", ") + link
			}
		}
	}
	return out, nil
}

func slicesSorted[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func driftNoisePage() []byte {
	var b strings.Builder
	b.WriteString(frontmatter("Drift noise presets", "The built-in drift noise rules, what each hides and the "+
		"drift.noise entry that turns it on."))
	b.WriteString("# Drift noise presets\n\n")
	b.WriteString("A preset is a built-in `drift.noise` rule. List it in the config to hide the drift it " +
		"matches. A hidden change stays in the JSON report, marked with the preset's rule ID and reason, " +
		"and the summary counts it. No preset hides an action listed in `drift.never_hide` " +
		"(`delete` and `replace` by default).\n\n")
	for _, p := range drift.PresetDocs() {
		fmt.Fprintf(&b, "## %s\n\n", p.Name)
		types := "every resource type"
		if p.Types != "" {
			types = "resource types matching " + code(p.Types)
		}
		fmt.Fprintf(&b, "Rule `%s`: %s.\n\n", p.Rule, cell(p.Reason))
		fmt.Fprintf(&b, "It hides `%s` changes to %s on %s.\n\n", p.Action, codeList(p.Attributes), types)
		b.WriteString(fenced("yaml", "drift:\n  noise:\n    - preset: "+p.Name) + "\n")
	}
	return []byte(b.String())
}

// ruleID is one rule ID or rule ID pattern that a finding can carry.
type ruleID struct {
	id, commands, category, meaning string
}

const (
	scanCmd   = "scan"
	scanWhois = "scan, whois"
)

// ruleIDs lists every rule ID the code sets. A test greps the sources for rule literals that
// are missing here.
func ruleIDs() []ruleID {
	return append(matchRuleIDs(), otherRuleIDs()...)
}

// matchRuleIDs lists the rules of the matching step.
func matchRuleIDs() []ruleID {
	managed, undetermined := string(scan.CategoryManaged), string(scan.CategoryUndetermined)
	return []ruleID{
		{
			match.RuleIdentity,
			scanWhois,
			managed,
			"A state resource has the same resource identity (account, region and the identity attributes).",
		},
		{match.RuleARN, scanWhois, managed, "A state resource has the same ARN."},
		{match.RuleID, scanWhois, managed, "A state resource has the same normalized ID."},
		{
			match.RuleTuple,
			scanCmd,
			managed,
			"An `aws_security_group_rule` has the same protocol, ports, source and direction.",
		},
		{
			match.RuleInline,
			scanCmd,
			managed,
			"An inline `ingress` or `egress` block of an `aws_security_group` has the same tuple.",
		},
		{
			match.RuleParent,
			scanCmd,
			string(scan.CategoryDerived),
			"No state has the resource, but a state manages its parent, which creates it.",
		},
		{
			match.RuleDoubleManage,
			scanWhois,
			string(scan.CategoryDoubleManaged),
			"Instances in more than one state match the resource.",
		},
		{
			match.RuleNoMapping,
			scanWhois,
			undetermined,
			"The type has no mapping to a Terraform type, so tfcensus can't tell whether a state manages it ([type coverage](/reference/type-coverage)).",
		},
		{
			match.RuleAmbiguous,
			"whois",
			undetermined,
			"Instances of different Terraform types have the ARN or ID. Narrow the query with `--type`.",
		},
	}
}

// otherRuleIDs lists the rules of ownership, inheritance, CloudFormation, whois and drift.
func otherRuleIDs() []ruleID {
	const packs = "/reference/rule-packs"
	unmanaged := string(scan.CategoryUnmanaged)
	return []ruleID{
		{
			match.RuleNotInState,
			scanCmd,
			unmanaged,
			"No state manages the resource and no ownership rule matched it. Findings carry `" + ownership.RuleNoMatch + "` once the rules ran.",
		},
		{
			ownership.RuleNoMatch,
			scanCmd,
			unmanaged,
			"No built-in pack and no user rule matched the unmanaged resource.",
		},
		{
			"pack/<pack>/<name>",
			scanWhois,
			"the pack's",
			"A rule of a built-in pack matched ([rule packs](" + packs + ")).",
		},
		{
			"guard/<name>",
			scanCmd,
			unmanaged,
			"A guard kept a user rule from hiding the resource ([guards](" + packs + "#guards)).",
		},
		{
			"user/<name>",
			"scan, whois, drift",
			"the rule's",
			"Your rule in `rules` (scan, whois) or `drift.noise` (drift) matched.",
		},
		{
			scan.RuleInherit + "<category>",
			scanCmd,
			"the parent's",
			"The resource took its parent's category, such as an IAM role policy of an AWS-managed role. `scan --explain` shows the parent chain.",
		},
		{
			scan.RuleCloudFormation,
			scanCmd,
			string(scan.CategoryOtherIaC),
			"A CloudFormation stack lists the resource and no state manages it.",
		},
		{
			whois.RuleNotFound,
			"whois --live",
			unmanaged,
			"A state has the ARN or ID, but the live inventory listed the type and the resource isn't there.",
		},
		{"preset/<name>", "drift", "hidden", "A [drift noise preset](/reference/drift-noise) hid the change."},
	}
}

func ruleIDsPage() []byte {
	var b strings.Builder
	b.WriteString(frontmatter("Matching and rule IDs", "Every rule ID a scan, whois or drift finding can carry, "+
		"with the category it sets and what it means."))
	b.WriteString("# Matching and rule IDs\n\n")
	b.WriteString("Every finding carries a `rule` and a `reason`. The rule says which step decided the " +
		"category. Matching tries the keys in order: identity, then ARN, then normalized ID, and the " +
		"first that finds a state resource decides. Unmapped types are `undetermined`, never managed. " +
		"`<name>` stands for a rule or preset name.\n\n")
	var rows [][]string
	for _, r := range ruleIDs() {
		rows = append(rows, []string{code(r.id), cell(r.commands), cell(r.category), r.meaning})
	}
	table(&b, []string{"Rule", "Commands", "Category", "Meaning"}, rows)
	b.WriteString("Backend audit findings carry the check ID as their rule, such as `S3-VER` " +
		"([audit checks](/reference/audit-checks)).\n")
	return []byte(b.String())
}

// moveRules describes the findings that reject a migration block. A test checks the list against
// the rule constants of internal/migrate.
func moveRules() [][2]string {
	return [][2]string{
		{
			migrate.RuleVersioning,
			"A state bucket of a surgery block has no versioning, so a push couldn't be rolled back (audit check S3-VER).",
		},
		{migrate.RuleAccount, "A moved resource's ARN names another account than the destination unit deploys to."},
		{migrate.RulePlan, "terragrunt plan failed in a unit of the block. The reason has the error."},
		{
			migrate.RuleVerify,
			"The plan of a unit, against the state after the move, would change a moved resource. A surgery block may only leave no-op and read; a native block's destinations may only import and its source may only forget.",
		},
	}
}

func moveRulesPart() []byte {
	var b strings.Builder
	b.WriteString(partNote)
	rows := make([][]string, 0, len(moveRules()))
	for _, r := range moveRules() {
		rows = append(rows, []string{code(r[0]), cell(r[1])})
	}
	table(&b, []string{"Rule", "The block is rejected when"}, rows)
	return []byte(b.String())
}
