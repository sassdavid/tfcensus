// Package httpx builds the HTTP client the notifiers use: retries with backoff, a per-host
// concurrency and rate limit, and a guard that stops at an exhausted rate limit.
package httpx
