package inventory

import (
	"context"
	"fmt"
	"maps"
	"sync"

	"github.com/aws/smithy-go/middleware"
	"golang.org/x/time/rate"
)

// Service names that rate budgets are keyed by.
const (
	ServiceConfig  = "config"
	ServiceRE      = "resource-explorer-2"
	ServiceEC2     = "ec2"
	ServiceIAM     = "iam"
	ServiceRoute53 = "route53"
	ServiceS3      = "s3"
	ServiceCC      = "cloudcontrolapi"
)

// DefaultRates returns the client-side budget per service in requests per second. Each
// (account, region, service) gets its own budget, because AWS meters most APIs that way; IAM and
// Route 53 are global, so their budget is per account. Resource Explorer allows 5 ListResources
// calls per second and Route 53 5 requests per second per account; the others stay well under
// their documented limits.
func DefaultRates() map[string]rate.Limit {
	return map[string]rate.Limit{
		ServiceConfig:  rateConfig,
		ServiceRE:      rateRE,
		ServiceEC2:     rateEC2,
		ServiceIAM:     rateIAM,
		ServiceRoute53: rateRoute53,
		ServiceS3:      rateS3,
		ServiceCC:      rateCC,
	}
}

// Requests per second per budget.
const (
	rateConfig  rate.Limit = 2
	rateRE      rate.Limit = 5
	rateEC2     rate.Limit = 10
	rateIAM     rate.Limit = 5
	rateRoute53 rate.Limit = 5
	rateS3      rate.Limit = 10
	rateCC      rate.Limit = 2
)

func globalService(service string) bool { return service == ServiceIAM || service == ServiceRoute53 }

// Limiters hands out one token bucket per account, region and service. It is safe for
// concurrent use.
type Limiters struct {
	rates map[string]rate.Limit

	mu      sync.Mutex
	buckets map[string]*rate.Limiter
}

// NewLimiters returns Limiters with rates, which override [DefaultRates] per service.
func NewLimiters(rates map[string]rate.Limit) *Limiters {
	r := DefaultRates()
	maps.Copy(r, rates)
	return &Limiters{rates: r, buckets: map[string]*rate.Limiter{}}
}

// For returns the bucket of service in account and region. Global services ignore region.
func (l *Limiters) For(account, region, service string) *rate.Limiter {
	if globalService(service) {
		region = ""
	}
	key := account + "\x00" + region + "\x00" + service
	l.mu.Lock()
	defer l.mu.Unlock()
	if b, ok := l.buckets[key]; ok {
		return b
	}
	limit, ok := l.rates[service]
	if !ok {
		limit = rate.Inf
	}
	burst := max(1, int(limit))
	b := rate.NewLimiter(limit, burst)
	l.buckets[key] = b
	return b
}

// APIOption returns an SDK client option that waits for the bucket before each operation call,
// each page of a paginator included. Retries go through the SDK's own adaptive rate limiter.
func (l *Limiters) APIOption(account, region, service string) func(*middleware.Stack) error {
	b := l.For(account, region, service)
	return func(s *middleware.Stack) error {
		return s.Initialize.Add(middleware.InitializeMiddlewareFunc("tfcensus.RateLimit",
			func(ctx context.Context, in middleware.InitializeInput, next middleware.InitializeHandler) (
				middleware.InitializeOutput, middleware.Metadata, error,
			) {
				if err := b.Wait(ctx); err != nil {
					return middleware.InitializeOutput{}, middleware.Metadata{}, fmt.Errorf(
						"wait for %s rate budget: %w",
						service,
						err,
					)
				}
				return next.HandleInitialize(ctx, in)
			}), middleware.Before)
	}
}
