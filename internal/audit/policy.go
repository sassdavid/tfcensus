package audit

import (
	"fmt"
	"slices"
	"strings"

	"github.com/sassdavid/tfcensus/internal/iampolicy"
)

// outsider is an account no policy names, standing for any principal outside the bucket's
// account.
const outsider = "999999999999"

type probe struct {
	label string
	p     iampolicy.Principal
}

// TLSResult is the outcome of evaluating a bucket policy for S3-TLS.
type TLSResult struct {
	Denied bool
	Gaps   []string // the requests over plain HTTP that the policy doesn't deny, such as "s3:GetObject by anonymous"
}

// SecureTransportDenied reports whether policy explicitly denies plain-HTTP reads and writes of
// state objects to every principal. It evaluates s3:GetObject and s3:PutObject on
// bucket/terraform.tfstate with aws:SecureTransport false, for the anonymous caller, a principal
// in another account, a principal in the bucket's account, and every principal the policy names.
// A Deny only counts when it applies to all of them, whatever the request's other condition keys.
func SecureTransportDenied(doc *iampolicy.Document, bucket, account string) TLSResult {
	probes := []probe{
		{"anonymous", iampolicy.Principal{}},
		{"another account", iampolicy.Principal{Account: outsider, ARN: "arn:aws:iam::" + outsider + ":role/any"}},
	}
	if account != "" {
		probes = append(probes, probe{"the bucket's account", iampolicy.Principal{
			Account: account, ARN: "arn:aws:iam::" + account + ":role/any",
		}})
	}
	for _, name := range namedPrincipals(doc) {
		if p, ok := iampolicy.ParsePrincipal(name); ok {
			probes = append(probes, probe{name, p})
		}
	}
	res := TLSResult{Denied: true}
	object := "arn:aws:s3:::" + bucket + "/terraform.tfstate"
	for _, action := range []string{"s3:GetObject", "s3:PutObject"} {
		var gaps []string
		for _, pr := range probes {
			r := doc.Evaluate(iampolicy.Request{
				Principal: pr.p, Action: action, Resource: object,
				Context: map[string]string{"aws:securetransport": "false"},
			})
			if r.Decision != iampolicy.ExplicitDeny {
				gaps = append(gaps, pr.label)
			}
		}
		if len(gaps) > 0 {
			res.Denied = false
			res.Gaps = append(res.Gaps, action+" by "+strings.Join(gaps, ", "))
		}
	}
	return res
}

// namedPrincipals lists the AWS principals that the document's Principal and NotPrincipal
// elements name, so that a Deny which exempts one of them is caught.
func namedPrincipals(doc *iampolicy.Document) []string {
	var out []string
	for _, s := range doc.Statements {
		for _, ps := range []iampolicy.Principals{s.Principal, s.NotPrincipal} {
			for _, v := range ps["AWS"] {
				if v != "*" && !slices.Contains(out, v) {
					out = append(out, v)
				}
			}
		}
	}
	slices.Sort(out)
	return out
}

// DecryptGrantedToAll returns the statements of a KMS key policy that allow kms:Decrypt to a
// principal in another account. A condition on aws:PrincipalAccount, aws:PrincipalOrgID,
// kms:CallerAccount or any other key that could exclude outsiders makes a statement not count.
func DecryptGrantedToAll(doc *iampolicy.Document, region string) []string {
	ctx := map[string]string{
		"kms:viaservice":       fmt.Sprintf("s3.%s.amazonaws.com", region),
		"kms:calleraccount":    outsider,
		"aws:principalaccount": outsider,
		"aws:securetransport":  "true",
	}
	var out []string
	for _, p := range []iampolicy.Principal{{}, {Account: outsider, ARN: "arn:aws:iam::" + outsider + ":role/any"}} {
		r := doc.Evaluate(iampolicy.Request{Principal: p, Action: "kms:Decrypt", Resource: "*", Context: ctx})
		if r.Decision == iampolicy.Allowed && !slices.Contains(out, r.Statement) {
			out = append(out, r.Statement)
		}
	}
	return out
}
