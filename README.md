# LLM Gateway

A Go gateway for internal inference access, virtual keys, recurring user budgets, and request cost accounting. Connects to OpenRouter and configurable OpenAI-compatible Chat Completions upstreams. State lives in local SQLite; run one gateway process per database on Linux. The admin API uses Huma over Go's standard router; inference streams use direct `net/http` handlers.

## Run locally

Requires Go 1.27.3 or later.

```sh
cp config.example.yaml config.yaml
export GATEWAY_ADMIN_TOKEN="$(openssl rand -hex 32)"
export OPENROUTER_API_KEY='your-provider-key'
go run ./cmd/gateway -config config.yaml -check
go run ./cmd/gateway -config config.yaml
```

Configuration uses YAML, with defaults applied to omitted optional settings. See the [configuration reference](docs/configuration.md) for every setting, whether it is required, and its default. Unknown fields (except inside model `request_params` settings), duplicate keys, multiple documents, and files larger than 1 MiB are rejected. A [configuration schema](config.schema.json) is available for editor autocomplete and validation. Use `-check` to validate configuration, references, and credentials. Keep token pricing values quoted, such as `input_usd_per_million: "0.15"`.

Inference listens on port 8080; administration binds to loopback port 8081. Put inference behind a TLS reverse proxy before exposing it. Both APIs use bearer authentication; only `/health/live` is public. Do not expose the admin listener to untrusted networks. Credentials are resolved from environment variables at startup/request time, never stored in SQLite. Cors is enabled by default on the inference API.

Configure aliases in `models`; clients send the alias as `model`. Each alias maps to one provider model. Configuration changes require a restart. The example model is illustrative: confirm availability and any model-specific parameters with your provider before use. The gateway forwards Chat Completions fields, including tools and structured output, while controlling routing, upstream credentials, user identity, and maximum output tokens.
Responses are rebuilt from an explicit field allowlist and identify the requested gateway model alias and a gateway completion ID. Provider costs and routing metadata remain internal. See the [response contract](docs/responses.md) for supported fields, reasoning, usage, and streaming behavior. Optional `models.<alias>.request_params` settings enforce upstream request fields such as reasoning and sampling parameters.
OpenRouter provider preferences and plugins use the same `request_params` object. Configured top-level fields replace client values completely. See the [OpenRouter request parameters reference](docs/configuration.md#openrouter-request-parameters) for reserved fields and examples.

## Provision a user and key

```sh
curl -sS http://127.0.0.1:8081/admin/v1/users \
  -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"id":"your-internal-user-id","budget_usd":"25.00","cadence":"monthly","anchor":"2026-10-15T00:00:00Z"}'
```

Creating a user does not create any keys. Create their first key using the same user `id` and a key alias:

```sh
curl --fail --silent --show-error -X POST \
  http://127.0.0.1:8081/admin/v1/users/your-internal-user-id/keys \
  -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"alias":"your-key-alias"}'
```

Save `secret` from this response; it cannot be retrieved later. Supply it as the API key in your existing compatible client and set its base URL to `http://localhost:8080/v1`.

Users have a single immutable `id`. Supply it during creation to use your own IDs, or omit it to let the gateway generate a `usr_` ID. Lookup, updates, usage, key management, and stored relationships all use that same ID. User routes use `/admin/v1/users/{user_id}`; URL-encode user IDs and key aliases individually as path segments. Supplied IDs are matched exactly and case-sensitively, must be nonblank and at most 256 bytes, and cannot be changed after creation.

## Model access groups

Define `model_access_groups` alongside `models`. Each group lists gateway model aliases, independently of the upstream providers behind those aliases:

```yaml
model_access_groups:
  free: { models: [fast] }
  basic: { models: [fast, advanced] }
  premium: { models: [fast, advanced, flagship] }
  none: { models: [] }
default_model_access_group: free
```

Every listed alias must exist in `models`. Group IDs are case-sensitive lowercase slugs, and containing up to 64 letters, digits, or hyphens. Unknown aliases, duplicate aliases within a group, missing model arrays, invalid group IDs, and undefined defaults fail configuration validation. Lists are explicit: there is no inheritance, wildcard, or implicit access when a new model route is added. An empty array grants no model access. The `default` model alias, if configured, needs explicit inclusion like any other alias.

Set `model_access_group` when creating a user, or omit it to store the configured default. Changing `default_model_access_group` affects future users only. Change model access and budget together through PATCH without resetting spending:

```sh
curl --fail --silent --show-error -X PATCH \
  http://127.0.0.1:8081/admin/v1/users/your-internal-user-id \
  -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"model_access_group":"premium","budget_usd":"100.00"}'
```

The group must be defined in configuration. Empty, null, and unknown assignments are rejected; omitting the field on updates preserves the current assignment. User responses include `model_access_group`. Subscription integrations choose the user's group and budget independently; the gateway does not map billing plans.

All existing keys use the user's current assignment. PATCH and subscription transitions can change group and budget atomically as described below. Admission checks access within the same transaction as authentication and budget enforcement. Requests admitted after a downgrade commits use the new assignment; already admitted requests may finish and settle. Unknown or inaccessible aliases return `403 model_not_allowed` without an upstream call or accounting request. `GET /v1/models` lists only the user's permitted aliases, independently of budget exhaustion.

Group definitions and routes require a restart to change; user assignments take effect immediately. Startup and readiness validate stored assignments against configured groups, so users must be reassigned before removing or renaming a group. Missing groups never grant fallback access. Subscription transitions audit the previous and resulting group; PATCH changes create no audit history. Each admitted request stores its admission-time group. Retain versioned configuration to reconstruct historical group membership.

## Key aliases

A **key alias** is the caller-supplied string stored in a key's `alias`, unique within its user. A user can have multiple aliases, and different users can reuse the same alias. Each alias identifies one key whose secret can be rotated. The rotate endpoint creates a missing key or atomically revokes its previous credential and activates a replacement. It returns `200` with `{ "key": { ...metadata }, "secret": "..." }` only after commit. Failed rotations preserve the previous credential. A retry after a lost response rotates again and returns a fresh secret; under concurrent rotations only the last committed credential remains valid, so an earlier response can already be superseded when it arrives.

To replace an existing key's secret, use the rotate endpoint. This same call can also provision a key when its alias does not exist yet:

```sh
curl --fail --silent --show-error -X POST \
  http://127.0.0.1:8081/admin/v1/users/your-internal-user-id/keys/your-key-alias/rotate \
  -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN"
```

Save the returned `secret` and update the client using this alias; the previous secret stops working after rotation commits.

Fetch current key metadata by alias:

```sh
curl --fail --silent --show-error \
  http://127.0.0.1:8081/admin/v1/users/your-internal-user-id/keys/your-key-alias \
  -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN"
```

Revoke a key by alias with one idempotent call:

```sh
curl --fail --silent --show-error -X DELETE \
  http://127.0.0.1:8081/admin/v1/users/your-internal-user-id/keys/your-key-alias \
  -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN"
```

DELETE returns `204` even when already revoked or absent, including when the user is absent. GET returns `404` when the user or key alias is absent and returns revoked metadata for an existing revoked key. Successful rotations and revocations, including no-op revocations, are audited. Revocation affects new requests; in-flight requests may finish and settle against their original credential IDs.

GET and list responses contain `alias` (the key alias), `status` (`active`, `revoked`, or `expired`), `created_unix`, and `rotated_unix`, plus credential ID, prefix and expiration metadata. They contain no secrets or hashes. Lists include only the current credential for each alias, including revoked and expired entries. `created_unix` preserves the original creation time for that user's alias; `rotated_unix` records its latest successful rotate operation, including create-on-rotate. Status describes the credential; user enablement and budgets are checked separately.

Rotation accepts an optional JSON body such as `{"expires":"2026-12-01T00:00:00Z"}`. Omitting it creates a non-expiring replacement, clearing any previous expiration. All retired credential rows and request/accounting links remain stored.

`POST /admin/v1/users/{user_id}/keys` also accepts `{"alias":"your-key-alias"}` with optional `expires`, returning `409` for an alias already used by that user, including a revoked alias. Omitting the alias generates one. `DELETE /admin/v1/keys/{key}` revokes a specific credential version by its internal ID; revoking an old version does not revoke its rotated replacement. To revoke the current credential regardless of rotation, use the key-alias endpoint.

The first release initializes the complete SQLite schema at version 1 using `001_init.sql`. Databases from earlier unreleased development schemas are not upgrade targets; use a fresh development database when switching to this baseline.

## API documentation

The generated admin OpenAPI contract describes user management, key expiration/revocation, usage, request history, manual cost resolution, audit history, backups, readiness, and metrics. Huma generates it from registered handlers and Go types and serves the current specification at `/openapi.json` and `/openapi.yaml` on the admin listener, behind admin authentication. Fetch the JSON specification with:

```sh
curl --fail --silent --show-error http://127.0.0.1:8081/openapi.json \
  -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN"
```

Use `/openapi.yaml` instead for YAML. The running admin server also serves an interactive documentation UI at `http://127.0.0.1:8081/docs`. Browser access requires an authenticated local proxy or header-injection setup. Administrative validation errors use Huma's RFC 9457 problem details (`application/problem+json`); domain errors put their stable code in `detail`. Inference retains its [compatible error envelope and contract](docs/inference.openapi.yaml).

List APIs accept `limit` (1–500, default 100) and `after`; follow `next_cursor` until empty. Treat cursors as opaque values. Usage summaries include the latest 100 periods; request history provides pagination across all retained requests. Filter request history with `/admin/v1/requests?user_id=your-internal-user-id` (URL-encode the query value).

## Subscription transitions

Read the user's `budget_policy_id` from creation or GET, then replace the complete policy:

```sh
curl --fail --silent --show-error -X POST \
  http://127.0.0.1:8081/admin/v1/users/your-internal-user-id/budget-transitions \
  -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"transition_id":"subscription-event-123","expected_policy_id":"pol_from_user_response","budget_usd":"100.00","cadence":"monthly","anchor":"2026-10-29T00:00:00Z","model_access_group":"premium","enabled":true}'
```

Every successful transition activates a new policy immediately and resets its spending, even if all policy values match the previous version. Choose an anchor at the subscription start to align renewal. A future anchor immediately blocks inference until that date; this API does not schedule a future policy switch. Downgrades and cancellations can assign a free policy, a zero budget, or disable the user. To cancel at a future billing boundary, the service submits the transition at that time. The gateway does not prorate allowances or interpret billing-provider events.

`transition_id` is unique per user. Persist the ID and complete submitted parameters in your subscription integration. Retrying with the same ID and parameters returns the original successful user snapshot, including its original policy ID, without another reset or audit entry, even after restart or later transitions. JSON whitespace and field order do not matter; preserve decimal strings, timestamp representation, and optional field values. Reusing the ID with different parameters returns `409 transition_conflict`. A replay response is historical; GET the user for current state.

`expected_policy_id` must match the active policy for a new transition; otherwise the gateway returns `409 stale_budget_policy` with no changes. Concurrent transitions based on the same policy permit one winner. The service must serialize or reconcile subscription events against authoritative billing state. This precondition detects stale observations; it cannot infer provider event ordering. Do not automatically replace the expected ID and retry an old event, because that could restore an obsolete plan. Duplicate delivery and response loss should retry the original payload.

Policy creation, user activation, optional model access and enablement changes, the retry result, and the `budget.transition` audit entry commit in one transaction. Omitted optional fields preserve their current values. Existing requests retain their admission period and settle or receive corrections against that original allowance. Old spending never consumes the new allowance. Policy identities, recurrence schedules, and period identities are protected by database constraints and immutability triggers. The current policy amount can be edited in place through PATCH.

`GET /admin/v1/users/{user_id}/budget-policies` lists retained policies with normal `limit`/`after` pagination in ID order. Users and policy definitions expose `budget_allowance_id`. Usage periods expose `id` and `budget_allowance_id`; request history exposes `period_id`, `budget_allowance_id`, and the `budget_policy_id` active at admission. PATCH preserves these identities and edits the current policy amount directly; retained policy data is not a history of administrative budget edits. Period rows are created on successful admission, so a current usage period can have an empty ID before its first admitted request. Usage reads take a consistent database snapshot and resolve the current allowance independently of the 100-period history limit.

## Administrative user updates

Use `PATCH /admin/v1/users/{user_id}` to update any combination of the recurring budget amount, model access group, and enablement in place:

```json
{
  "budget_usd": "9.00",
  "model_access_group": "basic",
  "enabled": true
}
```

Omitted fields preserve their current values. Null, empty, malformed, and unknown group assignments are rejected. Amounts must be nonnegative USD strings. All supplied changes commit atomically. PATCH preserves the anchor, cadence, policy ID, allowance ID, period IDs, and spending. It creates no policy revision, audit entry, or retry record and requires no operation ID, reason, or expected version. It does not check whether the user has changed since the caller last read it; later updates replace the supplied fields.

## Accounting and budgets

- Each user has one active weekly or monthly policy; transitions retain previous policies and create fresh allowances. All their keys share the budget. Disabled users and expired/revoked keys cannot admit new inference requests; active requests may finish.
- A UTC anchor defines renewal boundaries. Weekly means seven days. Monthly preserves the original date/time, clamping to month-end without drifting: January 31 → February 28 → March 31. Fractional seconds in anchors are truncated. Requests before the anchor are rejected.
- Periods include their start and exclude their end. Charge a request to its admission period, including late settlements. Renewal is calculated on admission; it does not depend on a reset scheduler. Unused budget does not roll over.
- Admission checks settled spending. There are no fund reservations: concurrent or unresolved requests can exceed the budget. Once known spending reaches the limit, new requests receive 402. Limits default to four active calls and 60 requests/minute per user, with 100 globally active inference requests.
- Subscription transitions replace the complete policy and start a fresh allowance. PATCH updates the amount, model access, and/or enablement in place while retaining the allowance and its spending. A zero budget blocks inference. All keys share the current policy. Period IDs are permanent; identical timestamps in different allowances never share spending.
- OpenRouter `usage.cost` is authoritative; interrupted requests are reconciled through `/generation` when a generation ID was captured. Persisted usage can also recover a failed settlement without another upstream call. Provider endpoints, credential environment-variable references, accounting modes, and configured token rates are snapshotted at admission. Keep referenced credentials available until pending requests are resolved. Cancellation does not guarantee that provider billing stops.
- Cost uses integer nano-USD (nine decimal places), rounded once to nearest with ties up. API amounts are decimal strings; original reported cost and usage metadata are retained. Very small charges below half a nano-dollar round to zero.
- Inference lifecycle and accounting status are independent. Accounting is `pending`, `settled`, or `unresolved`. Automatic settlement is idempotent. Manual resolution specifies a replacement **total** and reason; the ledger receives only the difference and the audit log records the action.
- Reconciliation retries with exponential backoff, capped at one hour; after 12 failed attempts a record becomes unresolved. Requests with no recoverable generation ID also eventually become unresolved. Operators resolve these using provider records. Users continue against known spending while costs remain unknown.

Generic connections use a configured HTTPS base URL and bearer API key; no separate provider adapter is required. See the [generic connection examples](docs/configuration.md#generic-connections-and-request-settings-modelsaliasrequest_params) for Together AI, Fireworks AI, and DeepInfra. These are configuration targets awaiting live verification; local tests verify the shared connector.

Compatible providers can use `accounting: reported` when they return a trustworthy `usage.cost` in USD. `accounting: token_rates` requires `input_usd_per_million`, `output_usd_per_million`, and `pricing_version` on each model route. That mode only supports flat text token pricing; nonzero cache/audio/other detail dimensions remain unresolved instead of being silently mispriced. Rates are snapshotted per request. Test a provider's streaming, token and cost semantics before enabling it. Cloudflare has not yet passed that conformance testing.

The gateway sends no automatic inference retries and implements no route fallback. A retry after an ambiguous failure could create another billable generation. Unknown request parameters are forwarded; clients remain responsible for provider/model support. Native provider APIs, Responses, embeddings, image/audio generation endpoints, user login, and a dashboard are outside v1.

## Checks

CI runs formatting, vet, race tests, a build, Go vulnerability checks, and a
Docker image build on pushes to `main`, pull requests, and weekly.
Dependabot groups weekly updates into
one PR per ecosystem (Go, Docker, and GitHub Actions).

Pushing a tag beginning with `v` publishes a checked Linux amd64 image to
`ghcr.io/strangebytesorg/llm-gateway` with the same tag.
See [container releases](docs/operations.md#container-releases) for deployment.

```sh
make check
make vuln
go test ./internal/gateway -run '^$' -fuzz '^FuzzSSE$' -fuzztime=10s
go test ./internal/gateway -run '^$' -fuzz '^FuzzMoney$' -fuzztime=10s
go test ./internal/gateway -run '^$' -fuzz '^FuzzUsage$' -fuzztime=10s
make load
```

The opt-in load test seeds 3,000 users and sends 20 requests/second, reaching approximately 100 simultaneous five-second streams. It checks admission p95 below 50 ms, no lost/duplicate settlements, and bounded heap growth against a local TLS fake upstream. This measures gateway behavior, not provider latency or available provider quotas. See [operations](docs/operations.md) for launch gates, deployment, backups, and recovery.

Temporary storage may be memory-backed. To test durable storage, create a scratch directory on the intended volume and run:

```sh
mkdir -p data/load-tests
go test ./internal/gateway -run '^TestLoad$' -count=1 -v -timeout=5m \
  -args -gateway-load -gateway-load-dir="$PWD/data/load-tests"
```

The test creates and removes its own subdirectory, leaving other files in the parent untouched.

## License

Licensed under the [MIT License](LICENSE).
