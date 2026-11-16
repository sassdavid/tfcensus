package inventory

import (
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
)

// ExternalDNS is the TXT registry naming of external-dns: --txt-prefix and --txt-suffix, either of
// which may hold %{record_type}.
type ExternalDNS struct {
	Prefix string
	Suffix string
}

// TXT ownership values, the txt_owner attribute of a TXT record. The value itself isn't kept.
const (
	txtHeritage = "external-dns" // the value starts with heritage=external-dns
	txtOpaque   = "opaque"       // one base64 token, as --txt-encrypt-enabled writes
)

const recordTypeTemplate = "%{record_type}"

// attrExternalDNS marks a record that external-dns owns, and the TXT record that says so.
const attrExternalDNS = "external_dns"

// minOpaque is the shortest base64 value taken for an encrypted one: a nonce and a cipher text.
const minOpaque = 24

// opaque reports whether v is one base64 token, as --txt-encrypt-enabled writes.
func opaque(v string) bool {
	body := strings.TrimRight(v, "=")
	if len(body) < minOpaque || len(v)-len(body) > 2 {
		return false
	}
	return strings.IndexFunc(body, func(c rune) bool {
		return (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '+' && c != '/'
	}) < 0
}

// txtOwner reads the values of a TXT record and returns txtHeritage, txtOpaque or "".
func txtOwner(rrs []r53types.ResourceRecord) string {
	owner := ""
	for _, rr := range rrs {
		v := strings.Trim(strings.TrimSpace(aws.ToString(rr.Value)), `"`)
		switch {
		case strings.HasPrefix(v, "heritage=external-dns"):
			return txtHeritage
		case opaque(v):
			owner = txtOpaque
		default:
			return ""
		}
	}
	return owner
}

// recordTokens are the record types external-dns writes into a TXT record's name.
func recordTokens() []string {
	return []string{"a", "aaaa", "cname", "ns", "mx", "ptr", "srv", "naptr", "txt"}
}

// affixes returns the prefix and suffix pairs to try: the configured ones, else no prefix and
// then external-dns-.
func (e ExternalDNS) affixes() [][2]string {
	if e.Prefix != "" || e.Suffix != "" {
		return [][2]string{{e.Prefix, e.Suffix}}
	}
	return [][2]string{{"", ""}, {"external-dns-", ""}}
}

// ownedKey identifies a record in a zone: name, type and set identifier.
type ownedKey struct{ name, typ, set string }

// owned returns the records a TXT ownership record can belong to: its name with the prefix,
// suffix and record-type token stripped from the first label. A cname token also names an A
// record, because external-dns used it for ALIAS records before v0.22.0.
func (e ExternalDNS) owned(txtName, set string) []ownedKey {
	first, rest, hasRest := strings.Cut(txtName, ".")
	var out []ownedKey
	for _, a := range e.affixes() {
		for _, tok := range recordTokens() {
			core, ok := stripAffixes(first, a[0], a[1], tok)
			if !ok {
				continue
			}
			name := core
			if hasRest {
				name += "." + rest
			}
			out = append(out, ownedKey{name, strings.ToUpper(tok), set})
			if tok == "cname" {
				out = append(out, ownedKey{name, "A", set})
			}
		}
	}
	return out
}

// stripAffixes removes prefix, suffix and the record-type token tok from a TXT name's first
// label, and reports whether all of them were there. When an affix holds %{record_type}, the
// token is part of the affix instead of a tok- prefix of the name.
func stripAffixes(label, prefix, suffix, tok string) (string, bool) {
	inAffix := strings.Contains(prefix, recordTypeTemplate) || strings.Contains(suffix, recordTypeTemplate)
	core, ok := strings.CutPrefix(label, strings.ReplaceAll(prefix, recordTypeTemplate, tok))
	if !ok {
		return "", false
	}
	if core, ok = strings.CutSuffix(core, strings.ReplaceAll(suffix, recordTypeTemplate, tok)); !ok {
		return "", false
	}
	if !inAffix {
		if core, ok = strings.CutPrefix(core, tok+"-"); !ok {
			return "", false
		}
	}
	return core, core != ""
}

// markExternalDNS sets external_dns on the records of one zone that a TXT ownership record owns,
// and on that TXT record. A TXT record whose value can't be read counts only when a record it
// names exists.
func (e ExternalDNS) markExternalDNS(zone []CloudResource) {
	index := make(map[ownedKey]int, len(zone))
	for i := range zone {
		a := zone[i].Attrs
		index[ownedKey{a[attrName], a["type"], a["set_identifier"]}] = i
	}
	for i := range zone {
		txt := &zone[i]
		owner := txt.Attrs["txt_owner"]
		if txt.Attrs["type"] != "TXT" || owner == "" {
			continue
		}
		found := false
		for _, k := range e.owned(txt.Attrs[attrName], txt.Attrs["set_identifier"]) {
			if j, ok := index[k]; ok && j != i {
				zone[j].setAttr(attrExternalDNS, "true")
				found = true
			}
		}
		if owner == txtHeritage || found {
			txt.setAttr(attrExternalDNS, "true")
		}
	}
}
