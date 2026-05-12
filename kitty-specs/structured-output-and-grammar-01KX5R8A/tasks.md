# tasks.md — structured-output-and-grammar-01KX5R8A

Six work packages. WP01 (types + capabilities) is the foundation. WP02 (validator) and WP04 (audit + errors) fan out in parallel. WP03a-d (adapter impls) are parallel after WP01 + WP02. WP05 (capability YAML) is parallel after WP01. WP06 (integration tests) closes out.

```
WP01 ──┬── WP02 ──┬── WP03a-d (adapters, parallel) ──┐
       │          │                                    ├── WP06
       └── WP04 ──┘                                    │
                  └── WP05 ─────────────────────────────┘
```

Each WP ends green: `go build ./core/... && go test -race -count=1 -short ./core/llm/...`.

---

## WP01 — Core types + capability constants + GenerationRequest extension

**Effort**: S
**Dependencies**: none

**Files:**
- `core/llm/llm.go` — ADD `ResponseFormat` struct; ADD `ResponseFormat *ResponseFormat` field to `GenerationRequest`; ADD three new `Capability` constants (`CapStructuredOutput`, `CapGrammar`, `CapRegexGrammar`); EXTEND `AllCapabilities()` to include the three new constants; EXTEND `RequestedCapabilities()` to emit the new caps when `ResponseFormat` is set.
- `core/llm/llm.go` — ADD `StructuredOutputAdapter` optional interface.
- `core/llm/llm_test.go` — ADD table-driven cases: `RequestedCapabilities` emits `CapStructuredOutput` when `Mode="json_schema"`; emits `CapGrammar` when `Mode="grammar"`; nil `ResponseFormat` emits neither.

**Acceptance:**
- `go test -race -count=1 -short ./core/llm/` green.
- Existing call sites that don't set `ResponseFormat` compile unchanged (additive field is nil-safe).
- `AllCapabilities()` returns the three new constants.
- `RequestedCapabilities()` emits correct caps per mode.

---

## WP02 — Lightweight JSON-schema validator + retry wrapper

**Effort**: M
**Dependencies**: WP01

**Files:**
- `core/llm/structured/doc.go` (NEW) — package doc.
- `core/llm/structured/validator.go` (NEW) — `Validate(schema json.RawMessage, data []byte) *ValidationError`. Draft-07 primitive subset: `type`, `properties`, `required`, `enum`, `items`. No external dep; uses `encoding/json`. Unsupported keywords pass through. `ValidationError` carries `Field string` + `Message string`.
- `core/llm/structured/validator_test.go` (NEW) — table-driven: valid JSON against schema; missing required field; wrong type; enum violation; nested object; schema with unknown keyword (must pass).
- `core/llm/structured/retry.go` (NEW) — `WithRetry(ctx context.Context, schema json.RawMessage, call func() ([]byte, error), strict bool, maxRetries int) ([]byte, error)`. On failure with `!strict`: appends tail-prompt `"\n\nYour previous response was invalid: <error>. Please correct it."` and calls `call` again once. Second failure returns `ErrResponseValidationFailed`. Zero retries when `schema` is nil (json/grammar mode).
- `core/llm/structured/retry_test.go` (NEW) — happy path; first-failure retry-and-pass; double-failure strict; double-failure lenient returns error.
- `core/llm/structured/schema.go` (NEW) — `SchemaHash(schema json.RawMessage) string` (SHA-256 hex, "" for nil); `InjectAdditionalProperties(schema json.RawMessage) (json.RawMessage, error)` (adds `"additionalProperties": false` at top level if absent, for OpenAI strict mode).

**Acceptance:**
- `Validate` returns nil for valid JSON matching schema.
- `Validate` returns non-nil `*ValidationError` for missing required field.
- Unknown JSON-schema keywords do NOT cause validation failure.
- `WithRetry` calls `call` at most twice (one original + one retry).
- `SchemaHash` returns consistent 64-char hex for the same input.
- `go test -race -count=1 -short ./core/llm/structured/` green.

---

## WP03 — Adapter implementations (4 adapters, all parallel after WP01+WP02)

**Effort**: M per adapter
**Dependencies**: WP01, WP02

### WP03a — Anthropic adapter

**Files:**
- `core/llm/anthropic/anthropic.go` — ADD `ApplyResponseFormat` method on `*Adapter`; implement `StructuredOutputAdapter` interface. For `Mode=="json_schema"`: inject synthetic tool `_structured_output` + `tool_choice: {type:"tool", name:"_structured_output"}`. For `Mode=="json"`: append `"\nRespond with valid JSON only."` to system prompt. For `Mode=="grammar"`: return `ErrUnsupportedFormat`. EXTEND `buildRequestBody` to call `ApplyResponseFormat` when `req.ResponseFormat != nil`.
- `core/llm/anthropic/anthropic_test.go` — ADD: `TestApplyResponseFormat_JSONSchema_InjectsTool`; `TestApplyResponseFormat_Grammar_ReturnsUnsupported`; `TestApplyResponseFormat_JSONMode_AppendsSysPrompt`.

**Acceptance:**
- `buildRequestBody` with `Mode="json_schema"` emits a `tools` array with one entry named `_structured_output` and `tool_choice` forcing it.
- `buildRequestBody` with `Mode="grammar"` returns `ErrUnsupportedFormat`.
- `buildRequestBody` with `Mode="json"` appends a JSON instruction to the system string.
- Existing tests (no `ResponseFormat`) unaffected.

### WP03b — OpenAI adapter

**Files:**
- `core/llm/openai/openai.go` — ADD `ApplyResponseFormat` method; implement `StructuredOutputAdapter`. For `Mode=="json"`: set `response_format: {type: "json_object"}`. For `Mode=="json_schema"`: call `structured.InjectAdditionalProperties` then set `response_format: {type: "json_schema", json_schema: {name: "response", schema: <schema>, strict: true}}`. For `Mode=="grammar"`: return `ErrUnsupportedFormat`. EXTEND `buildRequestBody` to call `ApplyResponseFormat`.
- `core/llm/openai/openai_test.go` — ADD: `TestApplyResponseFormat_JSONObject`; `TestApplyResponseFormat_JSONSchema_Native`; `TestApplyResponseFormat_Grammar_Unsupported`.

**Acceptance:**
- `Mode="json"` wire body has `response_format.type = "json_object"`.
- `Mode="json_schema"` wire body has `response_format.type = "json_schema"` with `strict: true`.
- `Mode="grammar"` returns `ErrUnsupportedFormat`.

### WP03c — OpenRouter adapter

**Files:**
- `core/llm/openrouter/openrouter.go` — ADD `ApplyResponseFormat` method (same logic as OpenAI: passthrough). For `Mode=="grammar"`: return `ErrUnsupportedFormat`.
- `core/llm/openrouter/openrouter_test.go` — ADD equivalent tests.

**Acceptance:** mirrors OpenAI adapter acceptance.

### WP03d — Bedrock adapter

**Files:**
- `core/llm/bedrock/bedrock.go` — ADD `ApplyResponseFormat` method. For `Mode=="json_schema"` on Claude-family models: inject tool-config workaround via `toolConfig.tools` + `toolChoice.tool`. For `Mode=="json"`: append system prompt. For `Mode=="grammar"`: return `ErrUnsupportedFormat`.
- `core/llm/bedrock/bedrock_test.go` — ADD equivalent tests.

**Acceptance:** `Mode="json_schema"` emits `toolConfig` with synthetic tool; `Mode="grammar"` returns `ErrUnsupportedFormat`.

---

## WP04 — New error types + audit kind

**Effort**: S
**Dependencies**: WP01

**Files:**
- `core/llm/errors.go` — ADD `ErrUnsupportedFormat` struct + `Error()` method; ADD `ErrResponseValidationFailed` struct + `Error()` method.
- `core/llm/errors.go` — ADD `IsUnsupportedFormat(err error) bool` helper.
- `core/context/audit/audit.go` — ADD `KindLLMStructuredResponse Kind = "llm.structured.response"`; ADD `LLMStructuredResponsePayload` struct.
- `core/context/audit/audit_test.go` — ADD JSON round-trip test for `LLMStructuredResponsePayload`.

**Acceptance:**
- `ErrUnsupportedFormat.Error()` includes provider, model, and mode.
- `ErrResponseValidationFailed.Error()` includes mode and schema error.
- `IsUnsupportedFormat(nil)` returns false; `IsUnsupportedFormat(&ErrUnsupportedFormat{})` returns true.
- `LLMStructuredResponsePayload` round-trips JSON cleanly.
- No raw schema or response payload fields in `LLMStructuredResponsePayload`.

---

## WP05 — Capability YAML updates

**Effort**: S
**Dependencies**: WP01

**Files:**
- `core/llm/capabilities/loader.go` — ADD `StructuredOutput bool`, `Grammar bool`, `RegexGrammar bool` to `modelEntry`; ADD `"structured_output"`, `"grammar"`, `"regex_grammar"` to `applyDefaults` keymap; EXTEND `Describe` to set `CapStructuredOutput`, `CapGrammar`, `CapRegexGrammar` from the model entry.
- `core/llm/capabilities/data/anthropic.yaml` — ADD `structured_output: false` to defaults (tool-call workaround is not native schema); ADD `structured_output: true` to claude-sonnet-* and claude-opus-* (they have reliable tool-call workaround); keep grammar/regex_grammar false for all.
- `core/llm/capabilities/data/openai.yaml` — ADD `structured_output: true` to gpt-4o* entries; `structured_output: false` for gpt-3.5-turbo* (no native support); grammar false for all.
- `core/llm/capabilities/data/openrouter.yaml` — ADD `structured_output: true` for claude-sonnet*, claude-opus*, gpt-4o* pass-throughs; false for others; grammar false for all.
- `core/llm/capabilities/data/bedrock.yaml` — ADD `structured_output: true` for anthropic.claude-3-5-sonnet*; false for others; grammar false for all.
- `core/llm/capabilities/data/ollama.yaml` — ADD `structured_output: false`; `grammar: true` (GBNF is the local path, deferred to `local-model-runtimes-01KQ8VMZ`).
- `core/llm/capabilities/capabilities_test.go` — ADD test that `Describe("openai", "gpt-4o")` has `CapStructuredOutput=true`; `Describe("anthropic", "claude-haiku-*")` has `CapStructuredOutput=false`; `Describe("ollama", "*")` has `CapGrammar=true`.

**Acceptance:**
- `go test -race -count=1 -short ./core/llm/capabilities/` green.
- Known structured-output models advertise `CapStructuredOutput=true`.
- Grammar mode is false for all cloud providers.
- Ollama grammar stub is true (future use).

---

## WP06 — Integration tests + build verification

**Effort**: S
**Dependencies**: WP01–WP05

**Files:**
- `core/llm/structured/integration_test.go` (NEW) — table-driven tests using fake adapters that implement `StructuredOutputAdapter`:
  1. Happy path: `Mode="json_schema"` passes validation.
  2. First-fail retry: first call returns invalid JSON, second returns valid.
  3. Double-fail strict: both calls fail, `ErrResponseValidationFailed` returned with `StrictValidation=true`.
  4. Grammar gate: adapter lacks `CapGrammar`, `ErrUnsupportedFormat` returned.
  5. Nil `ResponseFormat`: adapter not called with any format logic.
- `core/llm/llm_test.go` — EXTEND `TestAllCapabilities` to include the three new cap constants.

**Acceptance:**
- All five integration scenarios pass with `-race`.
- `go build ./core/...` clean.
- `go test -race -count=1 -short ./core/llm/...` all green.
- No new linter warnings under `.golangci.yml`.
