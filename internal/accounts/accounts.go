package accounts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

const (
	// DefaultRegion is used for global APIs when a profile sets no region.
	DefaultRegion = "us-east-1"
	// SessionName is the role session name tfcensus uses, so CloudTrail shows who read what.
	SessionName = "tfcensus"
	appID       = "tfcensus"
)

// Kind says where an account's credentials come from.
type Kind string

// Account source kinds.
const (
	KindProfile Kind = "profile" // a --profile match
	KindDefault Kind = "default" // the SDK's default chain (AWS_PROFILE, env, instance role)
	KindOrg     Kind = "org"     // an Organizations member, through a role assumed from the management profile
	KindRole    Kind = "role"    // a role assumed from another account, such as a backend role
)

// Account is one AWS account with working credentials.
type Account struct {
	ID        string     `json:"id"`
	Name      string     `json:"name,omitempty"`
	Source    string     `json:"source"` // the profile, or the profile the role was assumed from
	Kind      Kind       `json:"kind"`
	RoleARN   string     `json:"role_arn,omitempty"`
	Partition string     `json:"-"`
	Config    aws.Config `json:"-"`
}

// Role is a role to assume from another account's credentials.
type Role struct {
	ARN        string
	ExternalID string
}

// Options configures a [Loader].
type Options struct {
	Lookup      func(string) (string, bool) // the environment; nil means empty
	Transport   http.RoundTripper           // nil means the network
	Logger      *slog.Logger
	MaxAttempts int // per API call, the first included; 0 means the SDK default
	Concurrency int // accounts loaded in parallel; 0 means 8
}

// Loader loads and caches one [aws.Config] per profile and per assumed role, so an SSO login
// prompt or an expired token is met once per run. It is safe for concurrent use.
type Loader struct {
	opts  Options
	files Files

	mu       sync.Mutex
	profiles map[string]*memo
	roles    map[string]*memo
	meta     *memo
}

type memo struct {
	once     sync.Once
	acct     *Account
	profiles []Profile
	err      error
}

const defaultConcurrency = 8

// New returns a Loader.
func New(opts Options) *Loader {
	if opts.Lookup == nil {
		opts.Lookup = func(string) (string, bool) { return "", false }
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = defaultConcurrency
	}
	return &Loader{
		opts:     opts,
		files:    FilesFromEnv(opts.Lookup),
		profiles: map[string]*memo{},
		roles:    map[string]*memo{},
		meta:     &memo{},
	}
}

// Profiles returns the profiles in the shared config and credentials files.
func (l *Loader) Profiles() ([]Profile, error) {
	l.meta.once.Do(func() { l.meta.profiles, l.meta.err = ReadProfiles(l.files) })
	return l.meta.profiles, l.meta.err
}

func (l *Loader) profileMeta(name string) Profile {
	profiles, _ := l.Profiles()
	for _, p := range profiles {
		if p.Name == name {
			return p
		}
	}
	return Profile{Name: name}
}

func (l *Loader) entry(m map[string]*memo, key string) *memo {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := m[key]
	if !ok {
		e = &memo{}
		m[key] = e
	}
	return e
}

// Profile loads the named profile and looks up its account.
func (l *Loader) Profile(ctx context.Context, name string) (*Account, error) {
	e := l.entry(l.profiles, name)
	e.once.Do(func() { e.acct, e.err = l.load(ctx, name, KindProfile) })
	return e.acct, e.err
}

// Default loads the SDK's default credential chain: AWS_PROFILE when set, else environment
// credentials, the default profile or an instance role.
func (l *Loader) Default(ctx context.Context) (*Account, error) {
	name, _ := l.opts.Lookup("AWS_PROFILE")
	if name != "" {
		return l.Profile(ctx, name)
	}
	e := l.entry(l.profiles, "")
	e.once.Do(func() { e.acct, e.err = l.load(ctx, "", KindDefault) })
	return e.acct, e.err
}

func (l *Loader) loadOptions() []func(*config.LoadOptions) error {
	opts := []func(*config.LoadOptions) error{
		config.WithSharedConfigFiles([]string{l.files.Config}),
		config.WithSharedCredentialsFiles([]string{l.files.Credentials}),
		config.WithDefaultRegion(DefaultRegion),
		config.WithRetryMode(aws.RetryModeAdaptive),
		config.WithAppID(appID),
	}
	if l.opts.MaxAttempts > 0 {
		opts = append(opts, config.WithRetryMaxAttempts(l.opts.MaxAttempts))
	}
	if l.opts.Transport != nil {
		opts = append(opts, config.WithHTTPClient(&http.Client{Transport: l.opts.Transport}))
	}
	return opts
}

func (l *Loader) load(ctx context.Context, profile string, kind Kind) (*Account, error) {
	source := profile
	opts := l.loadOptions()
	if profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(profile))
	} else {
		source = string(KindDefault)
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		if _, ok := errors.AsType[config.SharedConfigProfileNotExistError](err); ok {
			return nil, fmt.Errorf("%w: %s is not in %s", ErrNoProfile, profile, l.files.Config)
		}
		return nil, l.credentialError(ctx, source, profile, err)
	}
	if err = l.retrieve(ctx, cfg, source, profile); err != nil {
		return nil, err
	}
	acct, err := l.identify(ctx, cfg, source)
	if err != nil {
		return nil, err
	}
	acct.Kind = kind
	acct.Name = profile
	return acct, nil
}

// retrieve fetches credentials before any API call, so that a failure here is known to be about
// credentials and not about one action.
func (l *Loader) retrieve(ctx context.Context, cfg aws.Config, source, profile string) error {
	if cfg.Credentials == nil {
		return &AuthError{Source: source, Profile: profile, Err: errors.New("no credentials found")}
	}
	if _, err := cfg.Credentials.Retrieve(ctx); err != nil {
		return l.credentialError(ctx, source, profile, err)
	}
	return nil
}

func (l *Loader) credentialError(ctx context.Context, source, profile string, err error) error {
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	ae := &AuthError{Source: source, Profile: profile, Err: err}
	if profile != "" && !isNetwork(err) {
		meta := l.profileMeta(profile)
		ae.SSO, ae.SSOSession = meta.SSO, meta.SSOSession
	}
	return ae
}

func (l *Loader) identify(ctx context.Context, cfg aws.Config, source string) (*Account, error) {
	out, err := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		if isExpiredCredentials(err) {
			return nil, &AuthError{Source: source, Err: err}
		}
		return nil, fmt.Errorf("get caller identity for %s: %w", source, err)
	}
	acct := &Account{ID: aws.ToString(out.Account), Source: source, Config: cfg, Partition: "aws"}
	if a, parseErr := arn.Parse(aws.ToString(out.Arn)); parseErr == nil {
		acct.Partition = a.Partition
	}
	return acct, nil
}

// AssumeRole assumes role with base's credentials and looks up the role's account.
func (l *Loader) AssumeRole(ctx context.Context, base *Account, role Role) (*Account, error) {
	e := l.entry(l.roles, base.Source+"\x00"+base.RoleARN+"\x00"+role.ARN+"\x00"+role.ExternalID)
	e.once.Do(func() { e.acct, e.err = l.assume(ctx, base, role) })
	return e.acct, e.err
}

func (l *Loader) assume(ctx context.Context, base *Account, role Role) (*Account, error) {
	cfg := base.Config.Copy()
	provider := stscreds.NewAssumeRoleProvider(sts.NewFromConfig(base.Config), role.ARN,
		func(o *stscreds.AssumeRoleOptions) {
			o.RoleSessionName = SessionName
			if role.ExternalID != "" {
				o.ExternalID = aws.String(role.ExternalID)
			}
		})
	cfg.Credentials = aws.NewCredentialsCache(provider)
	if _, err := cfg.Credentials.Retrieve(ctx); err != nil {
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		return nil, &AuthError{Source: base.Source, Role: role.ARN, Err: err}
	}
	acct, err := l.identify(ctx, cfg, base.Source)
	if err != nil {
		return nil, err
	}
	acct.Kind, acct.RoleARN = KindRole, role.ARN
	return acct, nil
}

// RoleARN returns the ARN of role name in account id, in the partition of from.
func RoleARN(from *Account, id, name string) string {
	return arn.ARN{Partition: from.Partition, Service: "iam", AccountID: id, Resource: "role/" + name}.String()
}
