# AWS SDK for Go v2

These rules cover every call through `github.com/aws/aws-sdk-go-v2`. They come from the AWS SDK for
Go v2 developer guide (`using.html`, `configure-retries-timeouts.html`, `handle-errors.html` under
`docs.aws.amazon.com/sdk-for-go/v2/developer-guide/`) and engineering ADR-0005. Before you add a client or an operation, read the operation's API reference
and those pages through the AWS MCP server (`aws___search_documentation`, then
`aws___read_documentation` with a URL it returned). Say in the PR which pages you checked.

## Before the first line

- Read the operation's API reference: required parameters, the pagination token, the error codes
  and any published quota.
- Look up the protocol in the module's `api_client.go` (query, JSON or REST). Cassettes compare
  request bodies byte for byte, so you need to know what the body looks like.
- Add a new service module at the version whose go.mod requires the repo's current
  `aws-sdk-go-v2` core, so no shared module version moves. Keep the version the same in both
  sibling repos when both use the module.

## Clients

- Build every client with `<service>.NewFromConfig(cfg, optFns...)` from the `aws.Config` that the
  repo's one loader returns (CLAUDE.md names it). Don't call `config.LoadDefaultConfig` anywhere
  else.
- Set the region, retryer and API options in the options function. Don't edit a shared
  `aws.Config` in place.
- A client is safe for concurrent use. Build one per account and region and share it between
  goroutines instead of building one per call.

## Calls

- Pass the caller's `context.Context`. Use `context.WithTimeout` for a deadline. The SDK stops
  retrying once the context is done.
- Page with `<service>.New<Operation>Paginator`, `HasMorePages` and `NextPage`. Don't loop on
  `NextToken` by hand. Set the page size where the API has one.
- Filter on the server when the API can (status filters, query expressions). Spell enum lists out
  in the code instead of calling `Values()`, so the request body doesn't change with an SDK bump.
- Don't send map-typed parameters from code a cassette test covers. The SDK writes map keys in
  random order and replay fails at random.
- Close every `io.ReadCloser` in an output, such as the `Body` of S3 `GetObject`, even when you
  don't read it.
- Check the error before you read the output.

## Retries and throttling

- Use the retry settings in engineering ADR-0005 where it has an AWS section (standard retry with
  `MaxAttempts=8`, adaptive retry for list APIs with low rate limits). Without one, keep the SDK's
  standard retryer.
- A retryer passed through `config.WithRetryer` or an options function must be a new instance per
  client. A shared instance gives every client one retry token bucket.
- Never call `retry.AddWithMaxAttempts(r, 0)`: zero means retry forever.
- `retry.DefaultThrottleErrorCodes` and unmodeled 429s already count as throttling. Add
  `retry.AddWithErrorCodes` only for a code outside that set, and name the code in a comment.
- Where the repo paces requests with a client-side budget, the budget waits before the first
  attempt only. The retryer spaces the retries.

## Errors

- Match errors with `errors.AsType` (or `errors.As`) on `smithy.APIError` and its `ErrorCode()`,
  or on the modeled type from `<service>/types`. Never match the message text.
- Wrap with the operation: `fmt.Errorf("cloudformation:ListStacks: %w", err)`.
- Tell an authorization failure (`AccessDenied`, `AccessDeniedException`,
  `UnauthorizedOperation`) apart from an outage. Report it for that target and go on with the
  others.

## Data and logs

- Read the identifiers you need and nothing else. Don't log, print or cache response fields that
  can hold secrets (state, parameters, template bodies, outputs, model prompts).
- SDK request logging goes through the injected logger with `Authorization` and
  `x-amz-security-token` redacted.

## Tests and permissions

- Never call real AWS unless the prompt names a profile and says read-only is allowed. Write
  cassettes from the API reference shapes and replay them.
- Cover pagination, a throttled request that succeeds on retry where it matters, and a denied
  call.
- When the repo ships an IAM policy, add every new action to it in the same change.
