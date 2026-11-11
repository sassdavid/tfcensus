package accounts

import (
	"cmp"
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// BackendRef names the credentials of a state backend.
type BackendRef struct {
	Profile string // the backend's profile, when it names one
	RoleARN string // the backend's role_arn, assumed from the base credentials
	Account string // the account the unit deploys to; the bucket's account when there is no role
	Region  string // the backend's region, else the unit's target region
}

// Backends hands out the credentials that read each backend, and one S3 client per credential
// and region. It is safe for concurrent use.
type Backends struct {
	loader    *Loader
	res       *Result
	mapping   map[string]string
	pathStyle bool

	mu      sync.Mutex
	clients map[string]*s3.Client
}

// NewBackends returns a Backends. mapping names the profile for an account ID (accounts.map);
// res holds the resolved --profile and --org accounts and may be nil. lookup reads the
// environment: a custom AWS endpoint switches S3 to path-style addressing.
func NewBackends(l *Loader, res *Result, mapping map[string]string, lookup func(string) (string, bool)) *Backends {
	// A custom endpoint (an emulator) can't serve virtual-hosted bucket names.
	_, custom := lookup("AWS_ENDPOINT_URL")
	_, customS3 := lookup("AWS_ENDPOINT_URL_S3")
	return &Backends{
		loader: l, res: res, mapping: mapping, pathStyle: custom || customS3,
		clients: map[string]*s3.Client{},
	}
}

// PathStyle reports whether S3 clients must use path-style addressing.
func (b *Backends) PathStyle() bool { return b.pathStyle }

// Credentials returns the account that reads the backend and the region to call it in: the
// backend's profile when it names one, else the profile that owns the bucket's account
// (accounts.map, then the resolved accounts, then the default chain), with role_arn assumed
// from it. The bucket's account is the role's account, or ref.Account without a role.
func (b *Backends) Credentials(ctx context.Context, ref BackendRef) (*Account, string, error) {
	var base *Account
	var err error
	if ref.Profile != "" {
		base, err = b.loader.Profile(ctx, ref.Profile)
	} else {
		owner := ref.Account
		if role, parseErr := arn.Parse(ref.RoleARN); parseErr == nil {
			owner = role.AccountID
		}
		base, err = b.loader.BaseFor(ctx, owner, b.res, b.mapping)
	}
	if err != nil {
		return nil, "", fmt.Errorf("load backend credentials: %w", err)
	}
	acct, err := b.loader.Backend(ctx, base, Role{ARN: ref.RoleARN})
	if err != nil {
		return nil, "", fmt.Errorf("load backend credentials: %w", err)
	}
	return acct, cmp.Or(ref.Region, acct.Config.Region), nil
}

// S3 returns the S3 client for acct in region, creating it once.
func (b *Backends) S3(acct *Account, region string) *s3.Client {
	key := acct.Source + "\x00" + acct.RoleARN + "\x00" + region
	b.mu.Lock()
	defer b.mu.Unlock()
	if c, ok := b.clients[key]; ok {
		return c
	}
	c := s3.NewFromConfig(acct.Config, func(o *s3.Options) {
		o.Region = region
		o.UsePathStyle = b.pathStyle
	})
	b.clients[key] = c
	return c
}
