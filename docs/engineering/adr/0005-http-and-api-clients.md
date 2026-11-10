# ADR-0005: Resilient HTTP and API clients

- Status: accepted (2026-11-09)

## Decision
**Generic HTTP** (notifier APIs: Slack webhooks, GitHub and Bitbucket issues). One `internal/httpx` client:
- `hashicorp/go-retryablehttp` v0.7.8: retries 5xx, 429 and connection resets, with exponential backoff + jitter, and honours `Retry-After`.
- Per-host `x/time/rate` limiter and per-host concurrency semaphore (default 4 per host).
- A rate-limit-aware middleware that reads `x-ratelimit-remaining/reset` where the API sends it. It slows down below 10% remaining and fails with a clear message instead of hammering.
- Proxies via `http.ProxyFromEnvironment`. `--ca-bundle` / `SSL_CERT_FILE` (also on macOS and Windows) adds corporate CAs.
- User-Agent: `tfcensus/<version> (+https://github.com/sassdavid/tfcensus)`. Never personal data.
- Notifier tokens come from env only (see [notifications spec](../../plan/specs/notifications-and-reports.md)).

**AWS.**
- One `aws.Config` per profile or assumed role, loaded once (so SSO doesn't prompt N times). Per-region `cfg.Copy()`.
- `RetryModeAdaptive` for Cloud Control, Config and Resource Explorer. Standard retry with `MaxAttempts=8` elsewhere. Add `retry.AddWithErrorCodes` for known throttle codes.
- Nested errgroup limits: accounts (default 4) → regions (default 4) → per-API pages.
- An expired SSO token is detected once per profile. The message tells the user to run `aws sso login --profile X`, and the profile is skipped, not retried.

relgap's copy of this ADR adds GitHub content fetching and an on-disk ETag cache, which tfcensus doesn't need.

## Consequences
- Every client sits behind a small interface, so tests replay recorded fixtures (go-vcr cassettes / httptest at the HTTP layer for notifier APIs **and** AWS, so SDK deserializers really run; emulators too, see the [testing plan](../../plan/testing.md)).
- `-vvv` dumps requests and responses with `Authorization` and `x-amz-security-token` redacted.
