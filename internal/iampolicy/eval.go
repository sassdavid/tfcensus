package iampolicy

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Principal is the caller of a request. An anonymous caller has no Account and no ARN.
type Principal struct {
	Account string
	ARN     string
}

// Request is the request to evaluate.
type Request struct {
	Principal Principal
	Action    string // such as s3:GetObject
	Resource  string // an ARN
	// Context holds the condition keys the caller knows, in lowercase. A condition on any other
	// key makes its statement undetermined, because the key could take any value.
	Context map[string]string
}

// Decision is the outcome of evaluating a request.
type Decision string

// Decisions, in IAM's terms.
const (
	ImplicitDeny Decision = "implicit-deny"
	Allowed      Decision = "allow"
	ExplicitDeny Decision = "explicit-deny"
)

// Result is a [Decision] with the statements that produced it.
type Result struct {
	Decision Decision
	// Statement is the Sid (or #index) of the statement that decided: the first Deny that
	// applies, else the first Allow. Empty for an implicit deny.
	Statement string
	// Undetermined lists the statements that might apply, depending on condition keys outside
	// the request context or on operators this package doesn't evaluate.
	Undetermined []string
}

type match int

const (
	matchNo match = iota
	matchYes
	matchUnknown
)

// Evaluate decides r against the document. An undetermined statement counts as not applying,
// for Allow and Deny alike, and is listed in the result.
func (d *Document) Evaluate(r Request) Result {
	var res Result
	allow := ""
	for i := range d.Statements {
		s := &d.Statements[i]
		switch s.applies(r) {
		case matchNo:
			continue
		case matchUnknown:
			res.Undetermined = append(res.Undetermined, s.label(i))
			continue
		case matchYes:
		}
		if s.Effect == Deny {
			res.Decision, res.Statement = ExplicitDeny, s.label(i)
			return res
		}
		if allow == "" {
			allow = s.label(i)
		}
	}
	res.Decision = ImplicitDeny
	if allow != "" {
		res.Decision, res.Statement = Allowed, allow
	}
	return res
}

func (s *Statement) label(i int) string {
	if s.Sid != "" {
		return s.Sid
	}
	return "#" + strconv.Itoa(i)
}

func (s *Statement) applies(r Request) match {
	if s.Principal != nil && !s.Principal.Contains(r.Principal) {
		return matchNo
	}
	if s.NotPrincipal != nil && s.NotPrincipal.Contains(r.Principal) {
		return matchNo
	}
	if !listMatches(s.Action, s.NotAction, r.Action, true) {
		return matchNo
	}
	if !listMatches(s.Resource, s.NotResource, r.Resource, false) {
		return matchNo
	}
	result := matchYes
	for _, c := range s.Condition {
		switch c.holds(r.Context) {
		case matchNo:
			return matchNo
		case matchUnknown:
			result = matchUnknown
		case matchYes:
		}
	}
	return result
}

// listMatches applies an element and its Not form: value must match a pattern in in (when in is
// set) and none in notIn.
func listMatches(in, notIn []string, value string, fold bool) bool {
	if in == nil && notIn == nil {
		return false
	}
	if in != nil && !anyGlob(in, value, fold) {
		return false
	}
	return !anyGlob(notIn, value, fold)
}

func anyGlob(patterns []string, value string, fold bool) bool {
	for _, p := range patterns {
		if Glob(p, value, fold) {
			return true
		}
	}
	return false
}

// Glob reports whether value matches an IAM pattern, where * matches any run of characters
// (slashes included) and ? matches one. fold compares without regard to case, as IAM does for
// action names.
func Glob(pattern, value string, fold bool) bool {
	var b strings.Builder
	if fold {
		b.WriteString("(?is)^")
	} else {
		b.WriteString("(?s)^")
	}
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	return err == nil && re.MatchString(value)
}

// Contains reports whether p names the principal. "*" names everyone, the anonymous caller
// included. An account ID or its root ARN names every principal in that account.
func (p Principals) Contains(pr Principal) bool {
	for typ, vals := range p {
		for _, v := range vals {
			if v == "*" && (typ == "*" || typ == "AWS") {
				return true
			}
			if typ != "AWS" || pr.Account == "" {
				continue
			}
			if v == pr.Account || v == pr.ARN || isRootOf(v, pr.Account) {
				return true
			}
		}
	}
	return false
}

func isRootOf(arn, account string) bool {
	rest, ok := strings.CutPrefix(arn, "arn:")
	if !ok {
		return false
	}
	_, rest, ok = strings.Cut(rest, ":") // the partition
	return ok && rest == "iam::"+account+":root"
}

// ParsePrincipal turns a Principal value (an account ID or an ARN) into a [Principal]. It
// returns false for values that don't name an AWS account, such as "*".
func ParsePrincipal(v string) (Principal, bool) {
	if isAccountID(v) {
		return Principal{Account: v, ARN: "arn:aws:iam::" + v + ":root"}, true
	}
	parts := strings.SplitN(v, ":", 6) //nolint:mnd // arn:partition:service:region:account:resource
	if len(parts) == 6 && parts[0] == "arn" && isAccountID(parts[4]) {
		return Principal{Account: parts[4], ARN: v}, true
	}
	return Principal{}, false
}

func isAccountID(s string) bool {
	const accountIDLen = 12
	if len(s) != accountIDLen {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (c Condition) holds(ctx map[string]string) match {
	op, _ := strings.CutPrefix(c.Operator, "ForAnyValue:")
	op, _ = strings.CutPrefix(op, "ForAllValues:")
	op, _ = strings.CutSuffix(op, "IfExists")
	v, ok := ctx[c.Key]
	if !ok {
		return matchUnknown
	}
	anyOf := func(f func(string) bool) match {
		if slices.ContainsFunc(c.Values, f) {
			return matchYes
		}
		return matchNo
	}
	noneOf := func(f func(string) bool) match {
		if anyOf(f) == matchYes {
			return matchNo
		}
		return matchYes
	}
	eq := func(w string) bool { return w == v }
	fold := func(w string) bool { return strings.EqualFold(w, v) }
	like := func(w string) bool { return Glob(w, v, false) }
	switch op {
	case "Bool", "StringEqualsIgnoreCase":
		return anyOf(fold)
	case "StringEquals":
		return anyOf(eq)
	case "StringNotEquals":
		return noneOf(eq)
	case "StringNotEqualsIgnoreCase":
		return noneOf(fold)
	case "StringLike", "ArnLike", "ArnEquals":
		return anyOf(like)
	case "StringNotLike", "ArnNotLike", "ArnNotEquals":
		return noneOf(like)
	case "Null":
		// The key is in the context, so it isn't null.
		return anyOf(func(w string) bool { return strings.EqualFold(w, "false") })
	default:
		return matchUnknown
	}
}
