# Provider Adapter Wire-Shape Matrix

**Mission**: provider-implementation-uniformity-01KQ8V4F WP00  
**Audited**: 2026-05-15  
**Scope**: `corellm.GenerationRequest` and `corellm.Response` fields × four adapters.

---

## Legend

| Symbol | Meaning |
|---|---|
| **S** | Supported — field is serialized to / deserialized from the wire |
| **D** | Silently dropped — field is accepted by the connector but not forwarded |
| **N** | Deliberate non-support — explicit capability-gate rejection before the wire |
| **I** | Internal — consumed by middleware before reaching the adapter |

---

## GenerationRequest fields

| Field | Anthropic | OpenAI | OpenRouter | Bedrock |
|---|---|---|---|---|
| `ProfileID` | I (routing key) | I (routing key) | I (routing key) | I (routing key) |
| `Model` | S (body `model`) | S (body `model`) | S (body `model`) | S (URL path) |
| `System` | S (body `system[]`) | S (system message) | S (system message) | S (body `system[]`) |
| `Messages` | S (body `messages`) | S (body `messages`) | S (body `messages`) | S (body `messages`) |
| `Tools` | S (`tools[]`) | S (`tools[]`) | S (`tools[]`) | S (`toolConfig`) |
| `Attachments` | S (image base64) | S (image_url) | S (image_url) | S (image base64) |
| `JSONMode` | S (tool-call workaround) | S (`response_format`) | S (`response_format`) | D (no structured-output in Converse) |
| `Caching` | S (`cache_control`) | D (no breakpoints API) | D (no breakpoints API) | D (not in Converse API) |
| `Reasoning` | S (`thinking` block) | D (model-native; no flag) | D (passthrough) | D (model-native) |
| `ResponseFormat` | S (json_schema via tool WA) | S (`response_format`) | S (`response_format`) | D (no generic RF in Converse) |
| `ImageOutput` | N (gate: CapImageOutput false) | D (routes separate endpoint) | D (passthrough) | D (routes separate endpoint) |
| `Params` | S (temperature, top_k, top_p, max_tokens) | S (temperature, top_p, max_tokens, presence_penalty, frequency_penalty) | S (same as OpenAI) | S (inferenceConfig subset) |
| `RetryOverride` | I (middleware) | I (middleware) | I (middleware) | I (middleware) |
| `SessionID` | I (internal) | I (internal) | I (internal) | I (internal) |

---

## Response fields

| Field | Anthropic | OpenAI | OpenRouter | Bedrock |
|---|---|---|---|---|
| `Content` | S (text blocks) | S (choices[0].delta.content) | S (choices[0].delta.content) | S (output.message.content) |
| `ToolCalls` | S (tool_use blocks) | S (tool_calls deltas) | S (tool_calls deltas) | S (toolUse blocks + embedded extraction) |
| `Reasoning` | S (thinking blocks) | D (none in response) | D (none in response) | D (none in response) |
| `FinishReason` | S (stop_reason) | S (finish_reason) | S (finish_reason) | S (stopReason) |
| `Usage` | S (input/output/cache tokens) | S (prompt/completion/total tokens) | S (same as OpenAI) | S (inputTokens/outputTokens) |
| `Cost` | S (derived by registry cost reducer) | S (derived) | S (derived) | S (derived) |
| `Attempts` | I (middleware counter) | I (middleware counter) | I (middleware counter) | I (middleware counter) |
| `SnapshotID` | I (audit) | I (audit) | I (audit) | I (audit) |

---

## Provider-specific fields (wire-only, no GenerationRequest mapping)

### Anthropic-specific
- `cache_control` — attached to message blocks from `CachingSpec.Breakpoints`; no silent drop; gate rejects on non-Anthropic profiles
- `system[]` — array of text objects with optional cache_control
- `top_k` — forwarded from `Params["top_k"]`
- `metadata.user_id` — not wired; no harness mapping

### Bedrock-specific
- `inferenceConfig` — subtranslation of Params (temperature, topP, maxTokens)
- `system []text` — array of text objects (mirrors Anthropic)
- `additionalModelRequestFields` — not wired from harness today
- `guardrailConfig` — not wired from harness today

### OpenAI-specific
- `parallel_tool_calls` — **silently dropped** today; not in GenerationRequest
- `seed` — **silently dropped** today; not in GenerationRequest
- `response_format` — wired from JSONMode + ResponseFormat
- `logprobs` / `top_logprobs` — **silently dropped**; not in GenerationRequest
- `service_tier` — **silently dropped**; not in GenerationRequest

---

## Silent-drop patches applied in WP00

No regression-breaking silent drops found that required immediate patching before WP01. The notable silently-dropped OpenAI fields (`parallel_tool_calls`, `seed`, `logprobs`, `service_tier`) are listed above and are the target of WP02's `RequestKnobs` surface.

The `Bedrock` non-support of `JSONMode` is guarded by `CapStructuredOutput=false` in `bedrock.yaml` — the gate rejects before the adapter is called, so this is deliberate non-support rather than a silent drop.

**Wire-shape regression tests**: existing adapter test suites (located in `core/llm/{anthropic,openai,openrouter,bedrock}/*_test.go`) cover the S-marked fields. The D-marked fields are confirmed dropped by reading the `buildRequestBody` implementations. No new regression tests were added in WP00 per spec ("Scoped to offending adapter; no util lifting yet") — no qualifying silent drops were found that broke correctness.
