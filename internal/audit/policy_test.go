package audit_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/audit"
	"github.com/sassdavid/tfcensus/internal/iampolicy"
)

const tlsBucket = "acme-tfstate"

// tlsDeny returns a policy with one Deny statement built from the given elements, in the shape
// Prowler issue 13226 uses.
func tlsDeny(principal, action, resource, condition string) string {
	return `{"Version":"2012-10-17","Statement":[{"Sid":"EnforceTLS","Effect":"Deny",` +
		principal + `,"Action":` + action + `,"Resource":` + resource + `,"Condition":` + condition + `}]}`
}

const (
	anyone      = `"Principal":"*"`
	allS3       = `"s3:*"`
	bothARNs    = `["arn:aws:s3:::acme-tfstate","arn:aws:s3:::acme-tfstate/*"]`
	insecure    = `{"Bool":{"aws:SecureTransport":"false"}}`
	ownerAcct   = "123456789012"
	ownerRootPr = `"Principal":{"AWS":"arn:aws:iam::123456789012:root"}`
)

// TestSpec_audit_2: S3-TLS uses real policy evaluation (Bool and BoolIfExists, NotPrincipal,
// several statements) and gets every shape from Prowler issue 13226 right.
func TestSpec_audit_2(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		policy string
		denied bool
		gaps   []string
	}{
		// Prowler 13226, false passes: HTTP stays allowed for someone.
		{
			name:   "13226 deny names one principal",
			policy: tlsDeny(ownerRootPr, allS3, bothARNs, insecure),
			gaps: []string{
				"s3:GetObject by anonymous, another account",
				"s3:PutObject by anonymous, another account",
			},
		},
		{
			name: "13226 NotPrincipal exempts principals",
			policy: tlsDeny(`"NotPrincipal":{"AWS":"arn:aws:iam::123456789012:role/ci"}`,
				allS3, bothARNs, insecure),
			gaps: []string{
				"s3:GetObject by arn:aws:iam::123456789012:role/ci",
				"s3:PutObject by arn:aws:iam::123456789012:role/ci",
			},
		},
		{
			name:   "13226 s3:Get* leaves PutObject",
			policy: tlsDeny(anyone, `"s3:Get*"`, bothARNs, insecure),
			gaps:   []string{"s3:PutObject by anonymous, another account, the bucket's account"},
		},
		{
			name:   "13226 s3:PutObjectTagging is not PutObject",
			policy: tlsDeny(anyone, `"s3:PutObjectTagging"`, bothARNs, insecure),
			gaps: []string{
				"s3:GetObject by anonymous, another account, the bucket's account",
				"s3:PutObject by anonymous, another account, the bucket's account",
			},
		},
		{
			name: "13226 extra condition narrows the deny",
			policy: tlsDeny(anyone, allS3, bothARNs,
				`{"Bool":{"aws:SecureTransport":"false"},"StringNotEquals":{"aws:SourceVpce":"vpce-0123"}}`),
			gaps: []string{
				"s3:GetObject by anonymous, another account, the bucket's account",
				"s3:PutObject by anonymous, another account, the bucket's account",
			},
		},
		// Prowler 13226, false fails: HTTP is denied.
		{
			name:   "13226 condition value in a list",
			policy: tlsDeny(anyone, allS3, bothARNs, `{"Bool":{"aws:SecureTransport":["false"]}}`),
			denied: true,
		},
		{
			name:   "13226 condition value as a JSON boolean",
			policy: tlsDeny(anyone, allS3, bothARNs, `{"Bool":{"aws:SecureTransport":false}}`),
			denied: true,
		},
		{
			name:   "13226 wildcard action in a list",
			policy: tlsDeny(anyone, `["s3:GetObject","s3:Put*"]`, bothARNs, insecure),
			denied: true,
		},
		// Prowler 13226, crash: Statement is one object.
		{
			name: "13226 single statement object",
			policy: `{"Version":"2012-10-17","Statement":{"Effect":"Deny","Principal":"*","Action":"s3:*",` +
				`"Resource":"arn:aws:s3:::acme-tfstate/*","Condition":{"Bool":{"aws:SecureTransport":"false"}}}}`,
			denied: true,
		},
		// Prowler's own check: it passes a deny of s3:PutObject alone, which leaves state readable.
		{
			name:   "prowler passes PutObject only",
			policy: tlsDeny(anyone, `"s3:PutObject"`, bothARNs, insecure),
			gaps:   []string{"s3:GetObject by anonymous, another account, the bucket's account"},
		},
		// Other shapes.
		{
			name:   "terragrunt EnforcedTLS statement",
			policy: tlsDeny(`"Principal":{"*":["*"]}`, allS3, bothARNs, insecure),
			denied: true,
		},
		{
			name: "BoolIfExists",
			policy: tlsDeny(
				`"Principal":{"AWS":"*"}`,
				allS3,
				bothARNs,
				`{"BoolIfExists":{"aws:SecureTransport":"false"}}`,
			),
			denied: true,
		},
		{
			name:   "condition key in another case",
			policy: tlsDeny(anyone, allS3, bothARNs, `{"Bool":{"AWS:SecureTransport":"FALSE"}}`),
			denied: true,
		},
		{
			name:   "Bool true denies TLS, not HTTP",
			policy: tlsDeny(anyone, allS3, bothARNs, `{"Bool":{"aws:SecureTransport":"true"}}`),
			gaps: []string{
				"s3:GetObject by anonymous, another account, the bucket's account",
				"s3:PutObject by anonymous, another account, the bucket's account",
			},
		},
		{
			name:   "bucket ARN only leaves objects",
			policy: tlsDeny(anyone, allS3, `"arn:aws:s3:::acme-tfstate"`, insecure),
			gaps: []string{
				"s3:GetObject by anonymous, another account, the bucket's account",
				"s3:PutObject by anonymous, another account, the bucket's account",
			},
		},
		{
			name: "split across statements",
			policy: `{"Statement":[` +
				`{"Effect":"Allow","Principal":{"AWS":"arn:aws:iam::123456789012:root"},"Action":"s3:*","Resource":"*"},` +
				`{"Effect":"Deny","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::acme-tfstate/*",` +
				`"Condition":{"Bool":{"aws:SecureTransport":"false"}}},` +
				`{"Effect":"Deny","Principal":"*","Action":["s3:PutObject"],"Resource":["arn:aws:s3:::acme-*/*"],` +
				`"Condition":{"Bool":{"aws:SecureTransport":"false"}}}]}`,
			denied: true,
		},
		{
			name: "NotAction with a gap",
			policy: `{"Statement":[{"Effect":"Deny","Principal":"*","NotAction":"s3:GetObject",` +
				`"Resource":"arn:aws:s3:::acme-tfstate/*","Condition":{"Bool":{"aws:SecureTransport":"false"}}}]}`,
			gaps: []string{"s3:GetObject by anonymous, another account, the bucket's account"},
		},
		{
			name: "NotResource covers objects",
			policy: `{"Statement":[{"Effect":"Deny","Principal":"*","Action":"*",` +
				`"NotResource":"arn:aws:s3:::other/*","Condition":{"Bool":{"aws:SecureTransport":"false"}}}]}`,
			denied: true,
		},
		{
			name: "allow only",
			policy: `{"Statement":[{"Sid":"RootAccess","Effect":"Allow",` + ownerRootPr +
				`,"Action":"s3:*","Resource":` + bothARNs + `}]}`,
			gaps: []string{
				"s3:GetObject by anonymous, another account, the bucket's account, arn:aws:iam::123456789012:root",
				"s3:PutObject by anonymous, another account, the bucket's account, arn:aws:iam::123456789012:root",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doc, err := iampolicy.Parse([]byte(tt.policy))
			require.NoError(t, err)
			got := audit.SecureTransportDenied(doc, tlsBucket, ownerAcct)
			assert.Equal(t, tt.denied, got.Denied)
			assert.Equal(t, tt.gaps, got.Gaps)
		})
	}
}

func TestDecryptGrantedToAll(t *testing.T) {
	t.Parallel()

	const admin = `{"Sid":"Admin","Effect":"Allow","Principal":{"AWS":"arn:aws:iam::123456789012:root"},` +
		`"Action":"kms:*","Resource":"*"}`
	tests := []struct {
		name string
		stmt string
		want []string
	}{
		{"owner only", admin, nil},
		{
			"everyone may decrypt",
			`{"Sid":"Open","Effect":"Allow","Principal":"*","Action":["kms:Decrypt","kms:GenerateDataKey*"],"Resource":"*"}`,
			[]string{"Open"},
		},
		{
			"everyone through S3 only",
			`{"Sid":"ViaS3","Effect":"Allow","Principal":{"AWS":"*"},"Action":"kms:De*","Resource":"*",` +
				`"Condition":{"StringLike":{"kms:ViaService":"s3.*.amazonaws.com"}}}`,
			[]string{"ViaS3"},
		},
		{
			"restricted to the org",
			`{"Sid":"Org","Effect":"Allow","Principal":"*","Action":"kms:Decrypt","Resource":"*",` +
				`"Condition":{"StringEquals":{"aws:PrincipalOrgID":"o-abc123"}}}`,
			nil,
		},
		{
			"restricted to the caller account",
			`{"Sid":"Caller","Effect":"Allow","Principal":"*","Action":"kms:Decrypt","Resource":"*",` +
				`"Condition":{"StringEquals":{"kms:CallerAccount":"123456789012"}}}`,
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			doc, err := iampolicy.Parse([]byte(`{"Statement":[` + admin + `,` + tt.stmt + `]}`))
			require.NoError(t, err)
			assert.Equal(t, tt.want, audit.DecryptGrantedToAll(doc, "eu-west-1"))
		})
	}
}
