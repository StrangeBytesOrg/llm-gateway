# Inference responses

The gateway rebuilds both JSON Chat Completions responses and SSE chunks from
explicitly supported fields. Unknown response fields are discarded at every
structured level. Request extension fields continue to be forwarded according to
the existing request settings rules.

## Identity and envelope

Every JSON completion and data chunk includes:

- `id`: `chatcmpl-` followed by the gateway request ID, stable throughout a stream.
- `object`: `chat.completion` or `chat.completion.chunk`.
- `created`: the gateway request admission time, in Unix seconds.
- `model`: the requested gateway model alias.
- `choices`: completion choices or chunk choices, including an empty array for
  usage-only responses.

`X-Request-ID` identifies the same gateway request. Upstream generation IDs remain
internal for accounting and reconciliation. Upstream model names, backend
fingerprints, service tiers, provider/deployment metadata, and raw headers are not
returned. This replaces the previous upstream model name and generation ID
behavior; clients must use gateway aliases and gateway IDs.

## Supported content

Optional fields preserve omission versus explicit `null`, as well as empty
strings, empty arrays, and zero counts. Missing or null choice indices default to zero;
a missing non-streaming message role defaults to `assistant`. Choices require a
message for JSON responses or a delta for chunks. Message content is a string or
`null`; structured-output JSON stays inside that string.

The public field allowlist is:

| Object | Fields |
| --- | --- |
| Completion choice | `index`, `message`, `finish_reason`, `logprobs` |
| Stream choice | `index`, `delta`, `finish_reason`, `logprobs` |
| Message or delta | `role`, `content`, `refusal`, `tool_calls`, `function_call`, `reasoning`, `reasoning_content`, `reasoning_details`, `thinking_blocks`, `annotations` |
| Function tool call | `index`, `id`, `type`, `function` |
| Function | `name`, `arguments` |
| Reasoning detail | `type`, `id`, `format`, `index`, `text`, `summary`, `signature`, `data` |
| Thinking block | `type`, `thinking`, `signature`, `data` |
| Annotation | `type`, `url_citation` |
| URL citation | `start_index`, `end_index`, `url`, `title` |
| Log probabilities | `content`, `refusal` |
| Token log probability | `token`, `logprob`, `bytes`, `top_logprobs` |
| Top token log probability | `token`, `logprob`, `bytes` |
| Usage | `prompt_tokens`, `completion_tokens`, `total_tokens`, `prompt_tokens_details`, `completion_tokens_details` |
| Prompt token details | `cached_tokens`, `audio_tokens` |
| Completion token details | `reasoning_tokens`, `audio_tokens`, `accepted_prediction_tokens`, `rejected_prediction_tokens` |

Tool arguments, model text, reasoning text, signatures, and encrypted reasoning
data are preserved as opaque strings. Their contents are not redacted or parsed;
for example, an answer containing the word `cost` is still returned. The boundary
controls provider metadata, not model-generated content.

Reasoning details follow the [OpenRouter reasoning format](https://openrouter.ai/docs/guides/best-practices/reasoning-tokens),
including signed text and encrypted data needed for subsequent tool turns.
`reasoning_content` and `thinking_blocks` support compatible upstreams using
[LiteLLM-style response fields](https://github.com/BerriAI/litellm/blob/main/litellm/types/utils.py).
Other extensions, including image/audio response payloads and arbitrary
`provider_specific_fields`, are omitted. Supporting another extension requires an
explicit schema change.

## Accounting and usage

Original upstream usage, including `cost` and provider-specific pricing dimensions,
is retained internally before public response validation. Public usage contains
only the token counts and details listed above. Cost, price breakdowns, credits,
and unknown usage dimensions are never copied into the public usage object.

Missing usage is allowed; it leaves accounting pending under the existing
reconciliation policy. Token counts are never fabricated. A cost-only usage
object becomes `{}` publicly. Streaming usage is still forwarded when supplied by
the upstream, including when the gateway requested it for accounting; this change
does not alter `stream_options` behavior.

## Streaming and failures

The gateway buffers one complete SSE event, validates and rebuilds its JSON, then
flushes it. It never forwards partial upstream JSON or raw network buffers. SSE
comments are replaced with a fixed `: keepalive` comment at most once per second
when upstream comments arrive. Upstream `event`, `id`, and `retry` fields are
ignored. Output events use `data: JSON` framing and a gateway-written `[DONE]`.

Invalid JSON, duplicate names, invalid UTF-8, incorrect supported field types,
negative counts/indices, and null structured array members fail validation. Before
a JSON response is sent, failures return a sanitized HTTP 502 gateway error. After
a stream begins, failures emit a sanitized gateway error event and `[DONE]` when
the connection permits. Upstream error bodies are never forwarded. Captured usage
can still settle a failed response.

Existing request timeouts, idle timeouts, client cancellation, and the 2 MiB SSE
event bound remain in effect. `max_response_bytes` measures upstream bytes,
including discarded metadata; normalized output may differ in size. Streams are
not buffered to completion, and missing `[DONE]` is treated as an incomplete stream.
