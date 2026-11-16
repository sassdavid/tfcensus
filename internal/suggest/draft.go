package suggest

import (
	"cmp"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

const draftHeader = `# Drafted by tfcensus move suggest from plans. Review every block, then check the file with
# tfcensus move plan <file> before tfcensus move apply.
`

// Draft returns a migration file for moves, or nil when there are none. Moves between units come
// first, one block per source unit; renames follow, one in-place block per unit. A resource moved
// to another unit keeps its address there until its unit's rename block runs.
func Draft(moves []Move) []byte {
	if len(moves) == 0 {
		return nil
	}
	f := hclwrite.NewEmptyFile()
	body := f.Body()
	n := 0
	name := func() string {
		n++
		return "suggest_" + strconv.Itoa(n)
	}

	between := map[string]map[string][]string{}
	renames := map[string][]Move{}
	for _, m := range moves {
		if m.FromUnit != m.ToUnit {
			if between[m.FromUnit] == nil {
				between[m.FromUnit] = map[string][]string{}
			}
			between[m.FromUnit][m.ToUnit] = append(between[m.FromUnit][m.ToUnit], m.FromAddress)
		}
		if m.FromAddress != m.ToAddress {
			renames[m.ToUnit] = append(renames[m.ToUnit], m)
		}
	}
	for _, from := range slices.Sorted(maps.Keys(between)) {
		var to []hclwrite.ObjectAttrTokens
		for _, unit := range slices.Sorted(maps.Keys(between[from])) {
			addrs := between[from][unit]
			slices.Sort(addrs)
			to = append(to, hclwrite.ObjectAttrTokens{
				Name: hclwrite.TokensForValue(cty.StringVal(unit)), Value: lines(addrs),
			})
		}
		blk := body.AppendNewBlock("migration", []string{"units", name()}).Body()
		blk.SetAttributeValue("from", cty.StringVal(from))
		blk.SetAttributeRaw("to", hclwrite.TokensForObject(to))
		body.AppendNewline()
	}
	for _, unit := range slices.Sorted(maps.Keys(renames)) {
		ms := renames[unit]
		slices.SortFunc(ms, func(a, b Move) int { return cmp.Compare(a.FromAddress, b.FromAddress) })
		actions := make([]string, 0, len(ms))
		for _, m := range ms {
			actions = append(actions, "mv "+quote(m.FromAddress)+" "+quote(m.ToAddress))
		}
		blk := body.AppendNewBlock("migration", []string{"units", name()}).Body()
		blk.SetAttributeValue("from", cty.StringVal(unit))
		blk.SetAttributeRaw("actions", lines(actions))
		body.AppendNewline()
	}
	out := hclwrite.Format(f.Bytes())
	return append([]byte(draftHeader+"\n"), strings.TrimRight(string(out), "\n")+"\n"...)
}

// lines writes a list of strings with one element per line, which reads and diffs better than
// hclwrite's one-line lists.
func lines(ss []string) hclwrite.Tokens {
	nl := &hclwrite.Token{Type: hclsyntax.TokenNewline, Bytes: []byte("\n")}
	toks := hclwrite.Tokens{{Type: hclsyntax.TokenOBrack, Bytes: []byte("[")}, nl}
	for _, s := range ss {
		toks = append(toks, hclwrite.TokensForValue(cty.StringVal(s))...)
		toks = append(toks, &hclwrite.Token{Type: hclsyntax.TokenComma, Bytes: []byte(",")}, nl)
	}
	return append(toks, &hclwrite.Token{Type: hclsyntax.TokenCBrack, Bytes: []byte("]")})
}

// quote makes an address one shell word for a tfmigrate action. Instance keys hold double quotes,
// so those addresses go in single quotes, or in double quotes with escapes when they hold one.
func quote(addr string) string {
	if !strings.ContainsAny(addr, " \t\n'\"\\") {
		return addr
	}
	if !strings.Contains(addr, "'") {
		return "'" + addr + "'"
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(addr) + `"`
}
