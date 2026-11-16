package accounts_test

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/cloudcontrol"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/accounts"
	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/testutil/awstest"
)

const cassettes = "../../testdata/cassettes/aws"

func TestReadProfiles(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	awstest.WriteConfig(t, home, []awstest.Profile{
		{Name: "acme-nonprod", Region: "eu-west-1"},
		{Name: "acme-prod", Extra: "sso_session = acme\nsso_account_id = 222222222222"},
		{Name: "acme-legacy", Extra: "sso_start_url = https://acme.awsapps.com/start"},
	}, "[default]\nregion = eu-west-1\n\n[sso-session acme]\nsso_region = eu-west-1\n\n[services s]\n")
	creds := filepath.Join(home, ".aws", "credentials")
	require.NoError(t, os.WriteFile(creds, []byte("# keys\n[acme-ci]\naws_access_key_id = x\n[acme-nonprod]\n"), 0o600))

	got, err := accounts.ReadProfiles(accounts.FilesFromEnv(app.Env{"HOME=" + home}.Lookup))
	require.NoError(t, err)
	assert.Equal(t, []accounts.Profile{
		{Name: "acme-ci"},
		{Name: "acme-legacy", SSO: true},
		{Name: "acme-nonprod"},
		{Name: "acme-prod", SSO: true, SSOSession: "acme"},
		{Name: "default"},
	}, got)

	missing, err := accounts.ReadProfiles(
		accounts.Files{Config: filepath.Join(home, "none"), Credentials: filepath.Join(home, "none")},
	)
	require.NoError(t, err)
	assert.Empty(t, missing)
}

func TestFilesFromEnv(t *testing.T) {
	t.Parallel()
	f := accounts.FilesFromEnv(app.Env{"HOME=/h", "AWS_CONFIG_FILE=/c/config"}.Lookup)
	assert.Equal(t, accounts.Files{Config: "/c/config", Credentials: filepath.Join("/h", ".aws", "credentials")}, f)
}

func TestMatchProfiles(t *testing.T) {
	t.Parallel()
	all := []accounts.Profile{{Name: "acme-dev"}, {Name: "acme-prod"}, {Name: "globex"}}
	tests := []struct {
		name    string
		globs   []string
		want    []string
		wantErr string
	}{
		{name: "glob", globs: []string{"acme-*"}, want: []string{"acme-dev", "acme-prod"}},
		{
			name:  "overlapping globs keep one of each",
			globs: []string{"globex", "*"},
			want:  []string{"acme-dev", "acme-prod", "globex"},
		},
		{
			name:    "no match",
			globs:   []string{"acme-*", "initech"},
			wantErr: `no such profile: no profile matches "initech"`,
		},
		{name: "bad glob", globs: []string{"acme-["}, wantErr: `profile glob "acme-["`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := accounts.MatchProfiles(tt.globs, all)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			names := make([]string, len(got))
			for i, p := range got {
				names[i] = p.Name
			}
			assert.Equal(t, tt.want, names)
		})
	}
}

func TestAuthError(t *testing.T) {
	t.Parallel()
	denied := &smithy.GenericAPIError{Code: "AccessDenied", Message: "not authorized"}
	tests := []struct {
		name string
		err  *accounts.AuthError
		want string
	}{
		{
			name: "sso session",
			err:  &accounts.AuthError{Source: "p", Profile: "p", SSO: true, SSOSession: "acme", Err: errors.New("x")},
			want: "SSO token for profile p is expired or missing. Run `aws sso login --sso-session acme`",
		},
		{
			name: "legacy sso",
			err:  &accounts.AuthError{Source: "p", Profile: "p", SSO: true, Err: errors.New("x")},
			want: "SSO token for profile p is expired or missing. Run `aws sso login --profile p`",
		},
		{
			name: "static keys",
			err:  &accounts.AuthError{Source: "p", Profile: "p", Err: errors.New("no keys")},
			want: "no usable credentials for p: no keys",
		},
		{
			name: "role denied",
			err: &accounts.AuthError{
				Source: "p",
				Role:   "arn:aws:iam::222222222222:role/r",
				Err:    fmt.Errorf("op: %w", denied),
			},
			want: "cannot assume arn:aws:iam::222222222222:role/r from p: access denied",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.err.Error())
			require.ErrorIs(t, tt.err, accounts.ErrAuth)
		})
	}
	assert.True(t, accounts.IsAccessDenied(fmt.Errorf("wrapped: %w", denied)))
	assert.False(t, accounts.IsAccessDenied(&smithy.GenericAPIError{Code: "ThrottlingException"}))
}

func TestDefaultUsesAWSProfile(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	awstest.WriteConfig(t, home, []awstest.Profile{{Name: "acme-live", Region: "eu-west-1"}}, "")
	l := accounts.New(accounts.Options{
		Lookup:    app.Env{"HOME=" + home, "AWS_PROFILE=acme-live"}.Lookup,
		Transport: awstest.Replay(t, filepath.Join(cassettes, "units-account-id")),
	})
	acct, err := l.Default(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "111111111111", acct.ID)
	assert.Equal(t, "acme-live", acct.Source)
	again, err := l.Default(t.Context())
	require.NoError(t, err)
	assert.Same(t, acct, again, "a profile is loaded once")
}

func TestUnknownProfile(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	awstest.WriteConfig(t, home, nil, "")
	l := accounts.New(accounts.Options{Lookup: app.Env{"HOME=" + home}.Lookup})
	_, err := l.Profile(t.Context(), "nope")
	require.ErrorIs(t, err, accounts.ErrNoProfile)
}

// TestBackendRoleChain assumes a backend role from the profile that owns the bucket's account.
func TestBackendRoleChain(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	awstest.WriteConfig(t, home, []awstest.Profile{{Name: "acme-mgmt", Region: "eu-west-1"}}, "")
	l := accounts.New(accounts.Options{
		Lookup:    app.Env{"HOME=" + home}.Lookup,
		Transport: awstest.Replay(t, filepath.Join(cassettes, "backend-role")),
	})
	base, err := l.BaseFor(t.Context(), "111111111111", nil, map[string]string{"111111111111": "acme-mgmt"})
	require.NoError(t, err)
	assert.Equal(t, "111111111111", base.ID)

	same, err := l.Backend(t.Context(), base, accounts.Role{})
	require.NoError(t, err)
	assert.Same(t, base, same, "no role means the base credentials")

	role := accounts.Role{ARN: "arn:aws:iam::333333333333:role/tfstate-reader", ExternalID: "acme-tfstate"}
	acct, err := l.Backend(t.Context(), base, role)
	require.NoError(t, err)
	assert.Equal(t, "333333333333", acct.ID)
	assert.Equal(t, accounts.KindRole, acct.Kind)
	assert.Equal(t, role.ARN, acct.RoleARN)

	_, err = l.Backend(t.Context(), base, accounts.Role{ARN: "arn:aws:iam::333333333333:role/missing"})
	require.ErrorIs(t, err, accounts.ErrAuth)
	assert.EqualError(t, err, "cannot assume arn:aws:iam::333333333333:role/missing from acme-mgmt: access denied")
}

func TestRoleARN(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "arn:aws-cn:iam::222222222222:role/r",
		accounts.RoleARN(&accounts.Account{Partition: "aws-cn"}, "222222222222", "r"))
}

// TestOURoles assumes the role configured for an account's OU, and the default role elsewhere.
func TestOURoles(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	awstest.WriteConfig(t, home, []awstest.Profile{{Name: "acme-mgmt", Region: "eu-west-1"}}, "")
	l := accounts.New(accounts.Options{
		Lookup:    app.Env{"HOME=" + home}.Lookup,
		Transport: awstest.Replay(t, filepath.Join(cassettes, "org-ou-roles")),
	})
	res, err := l.Resolve(t.Context(), accounts.Selection{
		Org:               true,
		ManagementProfile: "acme-mgmt",
		OURoles:           map[string]string{"ou-a1b2-sandbox1": "SandboxAdmin"},
		Names:             map[string]string{"111111111111": "Acme management"},
	})
	require.NoError(t, err)
	require.Empty(t, res.Errors)
	require.Len(t, res.Accounts, 2)
	assert.Equal(t, "Acme management", res.Accounts[0].Name, "the config map names accounts")
	assert.Empty(t, res.Accounts[0].RoleARN, "the management account uses its own credentials")
	assert.Equal(t, "arn:aws:iam::222222222222:role/SandboxAdmin", res.Accounts[1].RoleARN)
	assert.Equal(t, accounts.KindOrg, res.Accounts[1].Kind)
	assert.Equal(t, "acme-sandbox", res.Accounts[1].Name)
}

func loadProfile(t *testing.T, opts accounts.Options) *accounts.Account {
	t.Helper()
	home := t.TempDir()
	awstest.WriteConfig(t, home, []awstest.Profile{{Name: "acme-live", Region: "eu-west-1"}}, "")
	opts.Lookup = app.Env{"HOME=" + home}.Lookup
	opts.Transport = awstest.Replay(t, filepath.Join(cassettes, "units-account-id"))
	acct, err := accounts.New(opts).Profile(t.Context(), "acme-live")
	require.NoError(t, err)
	return acct
}

// TestRetrySettings checks ADR-0005: standard retry with 8 attempts for every client, adaptive
// retry where a client asks for it, and Options.MaxAttempts overriding the limit of both.
func TestRetrySettings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		maxAttempts int
		want        int
	}{
		{name: "default", want: accounts.DefaultMaxAttempts},
		{name: "override", maxAttempts: 2, want: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := loadProfile(t, accounts.Options{MaxAttempts: tt.maxAttempts}).Config

			standard := s3.NewFromConfig(cfg).Options()
			assert.Equal(t, aws.RetryModeStandard, standard.RetryMode)
			assert.Equal(t, tt.want, standard.Retryer.MaxAttempts())

			adaptive := accounts.AdaptiveRetryer(cfg)
			assert.IsType(t, &retry.AdaptiveMode{}, adaptive)
			assert.Equal(t, tt.want, adaptive.MaxAttempts())
			cc := cloudcontrol.NewFromConfig(cfg, func(o *cloudcontrol.Options) { o.Retryer = adaptive }).Options()
			assert.Equal(t, tt.want, cc.Retryer.MaxAttempts())
		})
	}
	custom := aws.Config{Retryer: func() aws.Retryer { return aws.NopRetryer{} }}
	assert.Equal(t, aws.NopRetryer{}, accounts.AdaptiveRetryer(custom), "a Retryer on the config wins")
}

// TestSDKLogger sends SDK log lines to the loader's logger: warnings at warn level, the rest at
// debug level.
func TestSDKLogger(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	}))
	cfg := loadProfile(t, accounts.Options{Logger: logger}).Config
	buf.Reset()
	cfg.Logger.Logf(logging.Warn, "falling back to %s", "IMDSv1")
	cfg.Logger.Logf(logging.Debug, "request %d", 1)
	assert.Equal(t, "level=WARN msg=\"falling back to IMDSv1\" source=aws-sdk\n"+
		"level=DEBUG msg=\"request 1\" source=aws-sdk\n", buf.String())
}

// TestOrgAccountsState keeps the accounts whose State is ACTIVE and ignores the retired Status
// field.
func TestOrgAccountsState(t *testing.T) {
	t.Parallel()
	rt := awstest.NewRouter(t).On("organizations", "ListAccounts", "", awstest.Stub{Body: `{"Accounts":[` +
		`{"Id":"222222222222","Name":"acme-dev","State":"ACTIVE","Status":"ACTIVE"},` +
		`{"Id":"333333333333","Name":"acme-old","State":"SUSPENDED","Status":"ACTIVE"},` +
		`{"Id":"444444444444","Name":"acme-closing","State":"PENDING_CLOSURE"},` +
		`{"Id":"555555555555","Name":"acme-status-only","Status":"ACTIVE"}]}`})
	mgmt := &accounts.Account{ID: "111111111111", Partition: "aws", Config: aws.Config{
		Region:           accounts.DefaultRegion,
		Credentials:      credentials.NewStaticCredentialsProvider("AKIAFAKE", "fake-secret", ""),
		HTTPClient:       &http.Client{Transport: rt},
		RetryMaxAttempts: 1,
	}}
	got, err := accounts.New(accounts.Options{}).OrgAccounts(t.Context(), mgmt, false)
	require.NoError(t, err)
	assert.Equal(t, []accounts.OrgAccount{{ID: "222222222222", Name: "acme-dev"}}, got)
}
