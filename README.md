# LLM Gateway

A Go gateway for internal inference access, virtual keys, recurring user budgets, and request cost accounting. OpenRouter is the first supported upstream. State lives in local SQLite; run one gateway process per database on Linux. The admin API uses Huma over Go's standard router; inference streams use direct `net/http` handlers.

## Run locally

Requires Go 1.27.3 or later.

```sh
cp config.example.yaml config.yaml
export GATEWAY_ADMIN_TOKEN="$(openssl rand -hex 32)"
export OPENROUTER_API_KEY='your-provider-key'
go run ./cmd/gateway -config config.yaml -check
go run ./cmd/gateway -config config.yaml
```

Configuration uses YAML, with defaults applied to omitted optional settings. See the [configuration reference](docs/configuration.md) for every setting, whether it is required, and its default. Unknown fields (except inside model `openrouter` settings), duplicate keys, multiple documents, and files larger than 1 MiB are rejected. A [configuration schema](config.schema.json) is available for editor autocomplete and validation. Use `-check` to validate configuration, references, and credentials. Keep token pricing values quoted, such as `input_usd_per_million: "0.15"`.

Inference listens on port 8080; administration binds to loopback port 8081. Put inference behind a TLS reverse proxy before exposing it. Both APIs use bearer authentication; only `/health/live` is public. Do not expose the admin listener to untrusted networks. Credentials are resolved from environment variables at startup/request time, never stored in SQLite. Cors is enabled by default on the inference API.

Configure aliases in `models`; clients send the alias as `model`. Each alias maps to one provider model. Configuration changes require a restart. The example model is illustrative: confirm availability and any model-specific parameters with your provider before use. The gateway forwards Chat Completions fields, including tools and structured output, while controlling routing, upstream credentials, user identity, and maximum output tokens. Upstream model names are preserved in responses. Optional `models.<alias>.openrouter` settings enforce OpenRouter request fields such as provider preferences, reasoning, plugins, and sampling parameters. Configured top-level fields replace client values completely. See the [OpenRouter settings reference](docs/configuration.md#openrouter-settings-modelsaliasopenrouter) for reserved fields and examples.

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

Set `model_access_group` when creating a user, or omit it to store the configured default. Changing `default_model_access_group` affects future users only. Update a user's group and budget together through the existing user endpoint:

```sh
curl --fail --silent --show-error -X PATCH \
  http://127.0.0.1:8081/admin/v1/users/your-internal-user-id \
  -H "Authorization: Bearer $GATEWAY_ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"model_access_group":"premium","budget_usd":"100.00"}'
```

The group must be defined in configuration. Empty, null, and unknown assignments are rejected; omitting the field on updates preserves the current assignment. User responses include `model_access_group`. Subscription integrations choose the user's group and budget independently; the gateway does not map billing plans.

All existing keys use the user's current assignment. Group and budget changes commit atomically. Admission checks access within the same transaction as authentication and budget enforcement. Requests admitted after a downgrade commits use the new assignment; already admitted requests may finish and settle. Unknown or inaccessible aliases return `403 model_not_allowed` without an upstream call or accounting request. `GET /v1/models` lists only the user's permitted aliases, independently of budget exhaustion.

Group definitions and routes require a restart to change; user assignments take effect immediately. Startup and readiness validate stored assignments against configured groups, so users must be reassigned before removing or renaming a group. Missing groups never grant fallback access. Assignment changes are audited with the previous and resulting group; each admitted request stores its admission-time group. Retain versioned configuration to reconstruct historical group membership.

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

## Accounting and budgets

- Each user has one weekly or monthly policy. All their keys share the budget. Disabled users and expired/revoked keys cannot admit new inference requests; active requests may finish.
- A UTC anchor defines renewal boundaries. Weekly means seven days. Monthly preserves the original date/time, clamping to month-end without drifting: January 31 → February 28 → March 31. Fractional seconds in anchors are truncated. Requests before the anchor are rejected.
- Periods include their start and exclude their end. Charge a request to its admission period, including late settlements. Renewal is calculated on admission; it does not depend on a reset scheduler. Unused budget does not roll over.
- Admission checks settled spending. There are no fund reservations: concurrent or unresolved requests can exceed the budget. Once known spending reaches the limit, new requests receive 402. Limits default to four active calls and 60 requests/minute per user, with 100 globally active inference requests.
- Budget amount changes take effect immediately. Anchor and cadence cannot be changed in v1. A zero budget blocks inference. Budgets and model access are assigned to users; all their keys share the current policy.
- OpenRouter `usage.cost` is authoritative; interrupted requests are reconciled through `/generation` when a generation ID was captured. Persisted usage can also recover a failed settlement without another upstream call. Provider endpoints, credential environment-variable references, accounting modes, and configured token rates are snapshotted at admission. Keep referenced credentials available until pending requests are resolved. Cancellation does not guarantee that provider billing stops.
- Cost uses integer nano-USD (nine decimal places), rounded once to nearest with ties up. API amounts are decimal strings; original reported cost and usage metadata are retained. Very small charges below half a nano-dollar round to zero.
- Inference lifecycle and accounting status are independent. Accounting is `pending`, `settled`, or `unresolved`. Automatic settlement is idempotent. Manual resolution specifies a replacement **total** and reason; the ledger receives only the difference and the audit log records the action.
- Reconciliation retries with exponential backoff, capped at one hour; after 12 failed attempts a record becomes unresolved. Requests with no recoverable generation ID also eventually become unresolved. Operators resolve these using provider records. Users continue against known spending while costs remain unknown.

Compatible providers can use `accounting: reported` when they return a trustworthy `usage.cost` in USD. `accounting: token_rates` requires `input_usd_per_million`, `output_usd_per_million`, and `pricing_version` on each model route. That mode only supports flat text token pricing; nonzero cache/audio/other detail dimensions remain unresolved instead of being silently mispriced. Rates are snapshotted per request. Test a provider's streaming, token and cost semantics before enabling it. Cloudflare has not yet passed that conformance testing.

The gateway sends no automatic inference retries and implements no route fallback. A retry after an ambiguous failure could create another billable generation. Unknown request parameters are forwarded; clients remain responsible for provider/model support. Native provider APIs, Responses, embeddings, image/audio generation endpoints, user login, and a dashboard are outside v1.

## Checks

```sh
make check
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

The test creates and removes its own subdirectory, leaving other files in the parent untouched. Recorded local results and their limits are in [validation](docs/validation.md).

## License

Licensed under the [MIT License](LICENSE).
