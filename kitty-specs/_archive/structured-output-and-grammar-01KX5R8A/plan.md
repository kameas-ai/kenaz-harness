# plan.md — structured-output-and-grammar-01KX5R8A

First-class JSON schema / response_format / GBNF support across all LLM adapters (Anthropic, OpenAI, OpenRouter, Bedrock). Foundation for downstream missions that need typed model output.

## 1. Branch contract

- **Branch**: `worktree-agent-a1f4f6c92210cdb03`
- **Base**: `main` (targeting merge into `release/v0.10.0`)
- **Hard deps**: none (self-contained capability extension)
- **Soft deps**:
  - `provider-implementation-uniformity-01KQ8V4F` — reuses existing `CapabilityDescriptor` and `Capability` constants; this mission adds `CapStructuredOutput`, `CapGrammar`, `CapRegexGrammar` to the existing enum.
  - `local-model-runtimes-01KQ8VMZ` — the consumer for grammar mode; this mission defines the wire shape that local-runtime adapters will implement.
  - `workflows-agentic-01KW2D3X` — `structured_llm` step kind requires this mission's `ResponseFormat` on `GenerationRequest`.
- **Merge gate**: all WPs green, `go build ./core/...` clean, `go test -race -count=1 -short ./core/llm/...` clean.

## 2. Architecture

### 2.1 Core types — `core/llm/llm.go` extension

`GenerationRequest` gains one optional field:

```go
ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
```

`ResponseFormat` is a new exported struct in `core/llm/llm.go`:

```go
type ResponseFormat struct {
    Mode             string          // "json" | "json_schema" | "grammar"
    Schema           json.RawMessage // Mode=="json_schema"
    Grammar          []byte          // Mode=="grammar" (GBNF bytes)
    StrictValidation bool            // true → fail hard; false (default) → retry once
}
```

`RequestedCapabilities()` is extended to emit the new capability constants when `ResponseFormat` is set.

### 2.2 New capabilities

Three constants added to `core/llm/llm.go`:

```go
CapStructuredOutput Capability = "structured_output"  // native json_schema
CapGrammar          Capability = "grammar"             // token-level GBNF
CapRegexGrammar     Capability = "regex_grammar"       // regex shorthand
```

The capability catalog YAML files (`capabilities/data/*.yaml`) add boolean fields:

```yaml
structured_output: false   # override per model
grammar: false
regex_grammar: false
```

### 2.3 Capability gate extension

`capabilities/gate.go` `Check` already gates on `req.RequestedCapabilities()`. No change to the gate itself — it uses the `Supported` map generically. The loader must map the new YAML keys to the new `Capability` constants (added to `modelEntry` struct and `Describe` method in `loader.go`).

For `Mode=="grammar"` the gate returns `ErrUnsupportedFormat` (a new typed error in `errors.go`) when the descriptor lacks `CapGrammar`. This is a hard gate — grammar constraints are never emulated.

### 2.4 Adapter interface extension

`ProviderAdapter` interface in `llm.go` is **not** extended (to preserve backward compat). Instead, adapters that support structured output implement an optional interface:

```go
// StructuredOutputAdapter is the optional extension adapters implement when
// they support ResponseFormat. Adapters that don't implement it trigger the
// fallback cascade (prompt-engineering + post-hoc validate).
type StructuredOutputAdapter interface {
    ApplyResponseFormat(req *GenerationRequest, wireBody map[string]any) error
}
```

The registry's `Stream` dispatch checks `errors.As` + interface assertion against the resolved adapter. Unknown adapters silently fall through to the prompt-engineering fallback.

### 2.5 Validator — `core/llm/structured/`

A new package `core/llm/structured/` contains:

- `validator.go` — `Validate(schema json.RawMessage, data []byte) error`. Implements a lightweight structural JSON Schema validator (draft-07 subset: `type`, `properties`, `required`, `enum`, `items`, `additionalProperties`). No external dependency — the stdlib `encoding/json` is sufficient.
- `validator_test.go` — table-driven cases.
- `retry.go` — `WithRetry(ctx, schema, call, strict, maxRetries) ([]byte, error)`. Wraps the LLM call + validate. On validation failure with `!strict`, appends the error as a tail-prompt and retries once. Second failure returns `ErrResponseValidationFailed`.
- `doc.go` — package doc.

### 2.6 Per-adapter implementation

**Anthropic**: tool-call workaround. Injects a synthetic tool `_structured_output` whose `input_schema` is the caller-supplied JSON schema. Forces `tool_choice: {type: "tool", name: "_structured_output"}`. On response, extracts `tool_use.input` and runs `validator.Validate`. GBNF mode returns `ErrUnsupportedFormat` immediately.

**OpenAI**: native `response_format`. For `Mode=="json"`, sets `response_format: {type: "json_object"}`. For `Mode=="json_schema"`, sets `response_format: {type: "json_schema", json_schema: {name: "response", schema: <schema>, strict: true}}`. GBNF mode returns `ErrUnsupportedFormat`.

**OpenRouter**: passthrough — mirrors the OpenAI wire shape. Models that support structured output natively receive it; others fall back to prompt-engineering + validate.

**Bedrock**: tool-config workaround (same pattern as Anthropic; Bedrock Converse API uses `toolConfig`). GBNF mode returns `ErrUnsupportedFormat`.

**Ollama / local** (stub for future use): would set `format: <gbnf>` on the Ollama wire shape. Deferred to `local-model-runtimes-01KQ8VMZ`; this mission wires `CapGrammar: false` for the Ollama YAML entry, ensuring the gate rejects grammar requests cleanly.

### 2.7 New error types (`errors.go`)

```go
// ErrUnsupportedFormat is returned when the caller requests a ResponseFormat
// mode the (provider, model) does not support AND no fallback is possible.
// Specifically: Mode="grammar" when CapGrammar is false — grammar cannot
// be emulated via prompt engineering (FR-005).
type ErrUnsupportedFormat struct {
    Provider string
    Model    string
    Mode     string
}

// ErrResponseValidationFailed is returned when the model's output failed
// schema validation after all retries (FR-006 second-failure path).
type ErrResponseValidationFailed struct {
    Mode        string
    SchemaError string // human-readable validation error
    Raw         string // the first 500 chars of the invalid response
}
```

### 2.8 Audit

`core/context/audit/audit.go` gains:

```go
KindLLMStructuredResponse Kind = "llm.structured.response"

type LLMStructuredResponsePayload struct {
    Provider          string `json:"provider"`
    Model             string `json:"model"`
    FormatMode        string `json:"format_mode"`
    SchemaHash        string `json:"schema_hash"` // SHA-256 hex of Schema bytes; "" for grammar/json mode
    ValidationOutcome string `json:"validation_outcome"` // "passed" | "retry_passed" | "failed" | "skipped"
    Attempts          int    `json:"attempts"`
    InputTokens       int    `json:"input_tokens"`
    OutputTokens      int    `json:"output_tokens"`
}
```

No raw schema or response payload in the audit log (spec §4.3 / FR-010).

## 3. WP dependency graph

```
WP01 ──┬── WP02 ──┬── WP03a (anthropic) ──┐
       │          ├── WP03b (openai)    ──┤
       │          ├── WP03c (openrouter) ──┤── WP05 ── WP06
       │          └── WP03d (bedrock)   ──┘
       └── WP04 (audit + errors)
```

WP01 (types + capabilities) must land first. WP02 (validator + retry) and WP04 (audit) can run in parallel after WP01. WP03a-d (adapter impls) depend on WP01 + WP02. WP05 (YAML capability flags) can run anytime after WP01. WP06 (integration tests) closes out.

## 4. Rollout + smoke

### 4.1 Rollout
- `ResponseFormat` field is `*ResponseFormat` — nil = today's behavior unchanged (FR-001 / NFR-001).
- New capability constants and YAML keys are additive; existing `Describe` callers see no change.
- New adapter methods are optional-interface — existing adapter code is untouched.

### 4.2 Smoke
1. Direct Anthropic call with `Mode="json_schema"` → tool-call workaround triggers → extracted JSON validates against schema.
2. Direct OpenAI `gpt-4o` call with `Mode="json_schema"` → native `response_format` → model returns conforming JSON.
3. Grammar mode against Anthropic → `ErrUnsupportedFormat` returned (not silent fallback).
4. `Mode="json_schema"` with model that lacks `CapStructuredOutput` → JSON-mode fallback → post-hoc validate passes.
5. Schema-validation failure with `StrictValidation=false` → one retry fires → second attempt validates.

## 5. Risk register

| Risk | Likelihood | Mitigation |
|---|---|---|
| Anthropic tool-call workaround conflicts with caller-supplied tools | M | Synthetic tool name `_structured_output` is injected last and `tool_choice` overrides; real tools are still present in the catalog but unreachable for this call |
| OpenAI `strict: true` rejects schemas with `additionalProperties` absent | M | Adapter auto-injects `"additionalProperties": false` when mode is `json_schema` and provider is OpenAI |
| Validator false-positives on `$ref`, `allOf`, etc. | L | Validator documents supported subset (draft-07 primitive keywords); unsupported keywords are accepted (pass-through) — validates best-effort, not exhaustively |
| Race on retry shared state | L | `retry.go` is stateless; each call allocates its own state |
| Schema hash collision in audit | negligible | SHA-256 hex is 64 chars; collision probability negligible in audit context |
