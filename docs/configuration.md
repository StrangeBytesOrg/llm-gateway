# Configuration reference

The gateway reads one YAML configuration file at startup. Start from
[config.example.yaml](../config.example.yaml), and use
[config.schema.json](../config.schema.json) for editor autocomplete and validation.
The tables below list every supported YAML field, its required status, and its
default **when omitted**. Example provider names, model aliases, groups, and
credentials are not built-in defaults.

```sh
go run ./cmd/gateway -config config.yaml -check
go run ./cmd/gateway -config config.yaml
```

`-check` validates the YAML, references, limits, and presence of credentials without
opening the database or contacting providers. It does not check whether credentials
work, upstream models exist, or listener addresses can bind. Normal startup also
validates that stored users reference defined model access groups.

Unknown fields, duplicate keys, multiple YAML documents, and files larger than
1 MiB are rejected. Defaults apply to omitted optional settings; explicitly
setting a number to zero fails validation except for `max_output_tokens`, where
zero explicitly disables the gateway output cap.
Relative file paths resolve from the process working directory, not the directory
containing the configuration file. Configuration changes require a restart.

## Listeners, storage, and administration

| Field | Type | Required in YAML? | Default | Description and constraints |
| --- | --- | --- | --- | --- |
| `listen` | string | No | `":8080"` | Inference API TCP listen address. Must be nonempty and different from `admin_listen`. |
| `admin_listen` | string | No | `"127.0.0.1:8081"` | Admin API TCP listen address. Must be nonempty and different from `listen`. Keep administration on loopback or a private network. |
| `database` | string | No | `"data/gateway.db"` | SQLite database path; must be nonempty. Run one gateway process per database. |
| `backup_directory` | string | No | `"data/backups"` | Directory for automatic and manual database backups; must be nonempty. |
| `backup_interval_seconds` | integer | No | `86400` (24 hours) | Interval between automatic backups, from `1` to `86400` seconds inclusive. |
| `admin_token_env` | string | No | `"GATEWAY_ADMIN_TOKEN"` | Name of the environment variable containing the admin bearer credential. Its value is required even when this field is omitted. |

## Providers, routes, and model access

| Field | Type | Required in YAML? | Default | Description and constraints |
| --- | --- | --- | --- | --- |
| `providers` | map of provider objects | Yes | None | At least one named upstream provider. Names must be nonempty. See provider fields below. |
| `models` | map of route objects | Yes | None | At least one gateway model alias mapped to a provider and upstream model. Alias names must be nonempty and contain no carriage returns or line breaks. See route fields below. |
| `model_access_groups` | map of group objects | Yes | None | Explicit sets of allowed gateway aliases. At least one group must exist so the default can reference it. |
| `default_model_access_group` | string | Yes | None | Must name a defined group. Assigned to newly created users when their API request omits `model_access_group`; changing it does not reassign existing users. |

### Provider fields: `providers.<name>`

Each provider object supports exactly these fields:

| Field | Type | Required? | Default | Description and constraints |
| --- | --- | --- | --- | --- |
| `base_url` | string | Yes | None | HTTPS API base URL with a host, without userinfo, query, or fragment, such as `https://openrouter.ai/api/v1`. |
| `api_key_env` | string | Yes | None | Name of the environment variable containing this provider's API key. The variable must have a nonempty value. |
| `accounting` | string | Yes | None | One of `openrouter`, `reported`, or `token_rates`. No accounting mode is selected implicitly. |

Accounting modes:

- `openrouter`: uses reported `usage.cost` in USD and can reconcile interrupted
  requests through OpenRouter's `/generation` endpoint when a generation ID is available.
- `reported`: uses a compatible provider's reported `usage.cost` in USD.
- `token_rates`: calculates cost from input/output token counts and the route's
  configured flat rates. Requires all three pricing fields below. Nonzero
  cache/audio/other token detail dimensions remain unresolved rather than being
  priced as ordinary text tokens.

### Route fields: `models.<alias>`

Clients send the gateway alias as `model`; the gateway forwards the configured
upstream model identifier. The alias `default` has no implicit route or access grant.

| Field | Type | Required? | Default | Description and constraints |
| --- | --- | --- | --- | --- |
| `provider` | string | Yes | None | Name of an entry in `providers`. |
| `model` | string | Yes | None | Nonempty upstream model identifier. |
| `input_usd_per_million` | string | For `token_rates` | None (unused in other modes) | USD price per million input tokens. Use a quoted nonnegative decimal string, such as `"0.15"`; `"0"` explicitly sets a zero rate. |
| `output_usd_per_million` | string | For `token_rates` | None (unused in other modes) | USD price per million output tokens. Same format as the input rate. |
| `pricing_version` | string | For `token_rates` | None (unused in other modes) | Nonempty operator-chosen pricing version, snapshotted with each request for accounting. |

Token rates must pass the gateway's USD parser: at most 100 characters, no sign or
whitespace, digits before any decimal point, and digits after it if present.
Scientific notation is accepted with a one- or two-digit exponent between `-30`
and `30`, for example `"1.5e-1"`. Values must fit the supported signed 64-bit
nano-USD range after rounding. Amounts round to nine decimal places, nearest with
ties up. A missing rate is not treated as zero.

### Group fields: `model_access_groups.<group>`

Group IDs must match `^[a-z0-9][a-z0-9-]{0,63}$`: 1–64 lowercase ASCII letters,
digits, or hyphens, starting with a letter or digit.

| Field | Type | Required? | Default | Description and constraints |
| --- | --- | --- | --- | --- |
| `models` | array of strings | Yes | None | Unique gateway aliases defined in the top-level `models` map. An explicit empty array (`[]`) grants no model access. Omitting the array or setting it to null is invalid. |

Groups have no inheritance or wildcards. Adding a route does not grant access
until its alias is included in a group. Reassign stored users before removing or
renaming their group. See [model access groups](../README.md#model-access-groups)
for user assignment and admission behavior.

## Request limits and timeouts

Every field in this table is optional in YAML. Byte limits are bytes, token and
concurrency limits are counts, and timeouts are seconds. Values must be positive
except for `max_output_tokens`, where zero disables the gateway output cap.

| Field | Type | Required? | Default | Description and constraints |
| --- | --- | --- | --- | --- |
| `max_body_bytes` | integer | No | `4194304` (4 MiB) | Maximum inference request body size. Range: `1`–`67108864` (64 MiB). |
| `max_response_bytes` | integer | No | `16777216` (16 MiB) | Maximum upstream response body size, including cumulative bytes for streaming responses. Range: `1`–`67108864` (64 MiB). |
| `max_output_tokens` | integer | No | `0` (no gateway cap) | Must be nonnegative. Zero leaves client output limits unchanged and inserts no limit when omitted. Positive values cap supplied `max_tokens` and `max_completion_tokens`; when neither is supplied, the gateway inserts `max_tokens` with this value. Client-supplied limits must always be positive integers. Upstream defaults and limits still apply. |
| `global_concurrency` | integer | No | `100` | Maximum active inference requests across all users. Must be at least `1`. |
| `user_concurrency` | integer | No | `4` | Maximum active inference requests per user, shared by all their keys. Must be at least `1`. |
| `user_requests_per_minute` | integer | No | `60` | Per-user token-bucket rate limit, shared by all their keys. Bucket capacity is this value and refill rate is this value per minute. Must be at least `1`. |
| `request_timeout_seconds` | integer | No | `300` | Overall inference request timeout, including upstream work. Range: `1`–`86400`. |
| `header_timeout_seconds` | integer | No | `60` | Timeout waiting for upstream response headers. Range: `1`–`86400`. |
| `stream_idle_seconds` | integer | No | `60` | Idle timeout between upstream stream reads; also bounds downstream stream writes. Range: `1`–`86400`. |
| `shutdown_seconds` | integer | No | `30` | Time allowed to drain HTTP requests during shutdown before remaining connections close. Range: `1`–`86400`. Accounting finalization can take up to 12 additional seconds. |
| `reconcile_max_attempts` | integer | No | `12` | Maximum automatic accounting reconciliation attempts before marking a request unresolved. Range: `1`–`86400`. |

## Credential environment variables

Environment variables supply credentials; they do not override YAML settings.
The gateway does not load a `.env` file or interpolate `${VARIABLE}` expressions
in YAML. Export variables in the process environment or supply them through your
service manager/container configuration.

| Variable | Required? | Default value | Constraints |
| --- | --- | --- | --- |
| Variable named by `admin_token_env` (normally `GATEWAY_ADMIN_TOKEN`) | Yes | None | Credential must contain 32–256 characters with no spaces, tabs, carriage returns, or line breaks. |
| Each variable named by `providers.<name>.api_key_env` | Yes, for every configured provider | None | Must be nonempty, including for providers not referenced by a route. `OPENROUTER_API_KEY` is an example variable name, not a built-in default. |

Credentials are required for normal startup and `-check`. The admin credential is
captured at startup, so changing it requires a restart. Provider keys are read
from the process environment when making upstream requests. Provider configuration
and credential variable names are snapshotted for admitted requests; keep referenced
credentials available until pending accounting is resolved.

## Command-line options

| Option | Type | Required? | Default | Behavior |
| --- | --- | --- | --- | --- |
| `-config` | string | No | `config.yaml` | Configuration file path. The file must exist for normal startup and `-check`. |
| `-check` | boolean | No | `false` | Validate configuration and credentials, then exit without opening the database or starting listeners. |
| `-verify-backup` | string | No | `""` (disabled) | Verify the specified backup database and exit. Requires no configuration or credentials. |
| `-export-admin-openapi` | boolean | No | `false` | Print the generated admin OpenAPI JSON and exit. Requires no configuration or credentials. |

If modes are combined, `-export-admin-openapi` takes precedence over
`-verify-backup`, which takes precedence over configuration loading and `-check`.
The standard `-h`/`-help` flag prints usage.

See [operations](operations.md) for deployment, backups, and shutdown behavior.
