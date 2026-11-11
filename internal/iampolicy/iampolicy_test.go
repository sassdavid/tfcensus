package iampolicy_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/iampolicy"
)

func TestParseErrors(t *testing.T) {
	t.Parallel()

	for name, doc := range map[string]string{
		"not json":         `{`,
		"no statement":     `{"Version":"2012-10-17"}`,
		"bad effect":       `{"Statement":[{"Effect":"Maybe","Action":"*","Resource":"*"}]}`,
		"principal string": `{"Statement":[{"Effect":"Allow","Principal":"me","Action":"*","Resource":"*"}]}`,
		"nested action":    `{"Statement":[{"Effect":"Allow","Action":[["s3:*"]],"Resource":"*"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := iampolicy.Parse([]byte(doc))
			assert.Error(t, err)
		})
	}
}

func TestGlob(t *testing.T) {
	t.Parallel()

	tests := []struct {
		pattern, value string
		fold, want     bool
	}{
		{"s3:Get*", "s3:GetObject", false, true},
		{"s3:get*", "s3:GetObject", true, true},
		{"s3:get*", "s3:GetObject", false, false},
		{"arn:aws:s3:::b/*", "arn:aws:s3:::b/env:/x/terraform.tfstate", false, true},
		{"arn:aws:s3:::b?", "arn:aws:s3:::bx", false, true},
		{"arn:aws:s3:::b.c", "arn:aws:s3:::bxc", false, false},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, iampolicy.Glob(tt.pattern, tt.value, tt.fold), "%s ~ %s", tt.pattern, tt.value)
	}
}

func TestPrincipalsContains(t *testing.T) {
	t.Parallel()

	role := iampolicy.Principal{Account: "123456789012", ARN: "arn:aws:iam::123456789012:role/ci"}
	anon := iampolicy.Principal{}
	tests := []struct {
		name string
		p    iampolicy.Principals
		pr   iampolicy.Principal
		want bool
	}{
		{"star", iampolicy.Principals{"*": {"*"}}, anon, true},
		{"aws star", iampolicy.Principals{"AWS": {"*"}}, anon, true},
		{"account id", iampolicy.Principals{"AWS": {"123456789012"}}, role, true},
		{
			"root arn in another partition",
			iampolicy.Principals{"AWS": {"arn:aws-us-gov:iam::123456789012:root"}},
			role,
			true,
		},
		{"exact role", iampolicy.Principals{"AWS": {"arn:aws:iam::123456789012:role/ci"}}, role, true},
		{"other role", iampolicy.Principals{"AWS": {"arn:aws:iam::123456789012:role/admin"}}, role, false},
		{"account never names anonymous", iampolicy.Principals{"AWS": {"123456789012"}}, anon, false},
		{"service", iampolicy.Principals{"Service": {"cloudtrail.amazonaws.com"}}, role, false},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, tt.p.Contains(tt.pr), tt.name)
	}
}

func TestEvaluateConditions(t *testing.T) {
	t.Parallel()

	base := `{"Statement":[{"Sid":"S","Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"*",` +
		`"Condition":%s}]}`
	ctx := map[string]string{"aws:sourcevpce": "vpce-1", "aws:securetransport": "true"}
	tests := []struct {
		cond string
		want iampolicy.Decision
		open bool // the statement is undetermined
	}{
		{`{"StringEquals":{"aws:SourceVpce":"vpce-1"}}`, iampolicy.Allowed, false},
		{`{"StringNotEquals":{"aws:SourceVpce":"vpce-1"}}`, iampolicy.ImplicitDeny, false},
		{`{"StringLike":{"aws:SourceVpce":"vpce-*"}}`, iampolicy.Allowed, false},
		{`{"ForAnyValue:StringEquals":{"aws:SourceVpce":["vpce-2","vpce-1"]}}`, iampolicy.Allowed, false},
		{`{"Null":{"aws:SourceVpce":"false"}}`, iampolicy.Allowed, false},
		{`{"Null":{"aws:SourceVpce":"true"}}`, iampolicy.ImplicitDeny, false},
		{`{"StringEquals":{"aws:PrincipalOrgID":"o-1"}}`, iampolicy.ImplicitDeny, true},
		{`{"NumericLessThan":{"aws:SecureTransport":"1"}}`, iampolicy.ImplicitDeny, true},
	}
	for _, tt := range tests {
		doc, err := iampolicy.Parse([]byte(fmt.Sprintf(base, tt.cond)))
		require.NoError(t, err)
		r := doc.Evaluate(iampolicy.Request{Action: "s3:GetObject", Resource: "arn:aws:s3:::b/k", Context: ctx})
		assert.Equal(t, tt.want, r.Decision, tt.cond)
		assert.Equal(t, tt.open, len(r.Undetermined) > 0, tt.cond)
	}
}
