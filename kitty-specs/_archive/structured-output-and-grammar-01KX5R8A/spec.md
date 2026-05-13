# Spec: Structured output + grammar-constrained generation

**Status**: draft · **Owner**: alecfeeman · **Created**: 2026-05-04
**Targets**: v0.7.0+ (closely paired with `provider-implementation-uniformity-01KQ8V4F`)
**Related**: `provider-implementation-uniformity-01KQ8V4F` (capability declaration), `local-model-runtimes-01KQ8VMZ` (consumer for grammars), `workflows-agentic-01KW2D3X` (transform-kind nodes already do post-LLM JSON validation)

## 1. Why

The harness today has a consistency hole: every adapter declares whether it `SupportsJSONMode` / `SupportsStructuredOutput` / `SupportsJSONSchema` (per `provider-implementation-uniformity` FR-001), but **there is no API the user, model, or workflow can call that says "please respond with JSON conforming to this schema."** The capability flags exist; the invocation path doesn't.

Today the only ways to get structured data out of the model are:

1. **Prompt engineering** ("Respond with JSON only. Schema: …"). Works most of the time, fails 5–15% on parse, fails worse on schema conformance, and there's no validator in the loop.
2. **Tool-call workaround** (define a single tool whose schema IS the desired output, force the model to call it). Works, but pollutes the tool catalog and adds a tool-call round-trip for what should be a typed response.
3. **Workflow `transform` step that parses model output** (post-hoc). Catches malformed output but burns a turn.

Provider-side, this is solved at three independent layers:

- **JSON mode** — provider guarantees the response body parses as JSON (no schema). Anthropic / OpenAI / OpenRouter expose this.
- **Structured output / response schema** — provider guarantees the response body parses AND conforms to a supplied JSON schema. OpenAI `response_format: {type: "json_schema", ...}`, Gemini `generationConfig.responseSchema`, Bedrock via tool-config workaround.
- **Grammar-constrained sampling** — at the token-sampling level, the next-token distribution is masked against a formal grammar (GBNF / Lark / regex). Local runtimes (llama.cpp, vLLM, Ollama via llama.cpp) expose this; cloud providers don't. This is the strictest guarantee — invalid tokens are mathematically unreachable.

The harness should expose all three uniformly, with a single user-facing API that **picks the strongest backing the active model supports** and **always validates the result against the schema** before returning it to the caller. A user-supplied JSON schema that the model returns invalid output for becomes a typed error the caller can branch on, not a silent garbage string.

This unblocks: workflow nodes that need typed output (no more `transform` parse step), `/wf` slash commands that produce reliable structured replies, agent tool calls that can rely on response shape, and the entire scheduled-chat-runs lane that needs predictable output to drive downstream automation.

## 2. In scope

### 2.1 Public API surface

A single field added to `core/llm.LLMRequest`:

```go
type LLMRequest struct {
    // ... existing fields ...

    // ResponseFormat constrains the model's output. Three modes:
    //
    //   - nil           — free-form text (today's behavior)
    //   - {Mode: "json"}             — guarantee parseable JSON, no schema
    //   - {Mode: "json_schema", Schema: <json-schema-bytes>}
    //                   — guarantee parseable JSON conforming to schema
    //   - {Mode: "grammar", Grammar: <gbnf-bytes>}
    //                   — token-level grammar constraint (local runtimes only)
    //
    // The adapter chooses the strongest backing it supports:
    //   - mode=json_schema, model has SupportsStructuredOutput → native schema
    //   - mode=json_schema, model has SupportsJSONMode but not schema
    //                   → JSON mode + validate-after, retry-on-fail (max 1)
    //   - mode=json_schema, model has neither → prompt-engineering fallback
    //                   + validate-after, retry-on-fail (max 1)
    //   - mode=grammar, model has SupportsGrammar → native grammar
    //   - mode=grammar, model lacks SupportsGrammar → ErrUnsupportedFormat
    //
    // The caller never has to reason about the cascade; they get either a
    // valid response matching the requested format or a typed error.
    ResponseFormat *ResponseFormat
}

type ResponseFormat struct {
    Mode    string          // "json" | "json_schema" | "grammar"
    Schema  json.RawMessage // populated when Mode == "json_schema"
    Grammar []byte          // populated when Mode == "grammar"; GBNF bytes
    // StrictValidation, when true, fails the call with a typed error if
    // the response doesn't validate. When false (default), validation
    // failures fire a single retry with a "your previous response was
    // invalid: <error>" appended; second failure returns the unparsed
    // string with a `validation_error` field. The retry is opt-out via
    // env var `HARNESS_RESPONSE_FORMAT_NO_RETRY=1`.
    StrictValidation bool
}
```

### 2.2 Capability flags

The `provider-implementation-uniformity-01KQ8V4F` `ProviderCapabilities` struct already has `SupportsJSONMode`, `SupportsStructuredOutput`, `SupportsJSONSchema`. This mission **adds**:

```go
SupportsGrammar      bool  // GBNF / token-level constraint
SupportsRegexGrammar bool  // some runtimes accept regex shorthand
GrammarFormat        string // "gbnf" | "lark" | "regex" — for adapter dispatch
```

### 2.3 Adapter responsibilities

Each adapter implements:

```go
ApplyResponseFormat(ctx context.Context, req *providerWireRequest, fmt *ResponseFormat) error
ValidateResponse(ctx context.Context, body []byte, fmt *ResponseFormat) (validated []byte, err error)
```

- `ApplyResponseFormat` translates `*ResponseFormat` into the provider's wire shape (Anthropic `tool_use` workaround, OpenAI `response_format`, Gemini `generationConfig.responseSchema`, llama.cpp `grammar`/`json_schema` field).
- `ValidateResponse` runs after the response arrives. For `json_schema` mode, validates against the schema using `xeipuuv/gojsonschema` (already an indirect dep). For `grammar` mode, validation is a no-op (the grammar already guaranteed shape at sample time).

### 2.4 Workflow integration

`core/workflows/types.go` `StepKindTransform` gets a sibling `StepKindStructuredLLM`:

```yaml
- name: extract_pr_summary
  kind: structured_llm
  prompt_template: "Summarize this PR diff: {{.diff}}"
  response_format:
    mode: json_schema
    schema:
      type: object
      properties:
        summary: { type: string }
        risk: { type: string, enum: [low, medium, high] }
        files_touched: { type: integer }
      required: [summary, risk]
  outputs: [summary, risk, files_touched]
```

The runner sets `LLMRequest.ResponseFormat`, awaits the response, and unmarshals into the per-output map. Validation failures fail the step (not the workflow) so retry policy applies normally.

### 2.5 Slash command surface

`/json` slash command in chat: user supplies an inline schema, model responds with JSON validated against it. Response renders as a foldable JSON tree in the chat UI.

```
> /json
schema: { "type": "object", "properties": { "name": {"type":"string"} } }
prompt: my name is Alec

{
  "name": "Alec"
}
```

### 2.6 Model-facing tool

A new builtin `respond_with_json(schema)` registered in tool catalogs where the model is producing structured output for a downstream consumer. When called, the next message from the model is constrained to the given schema. This is for cases where the model itself decides "I need to give the user typed data."

## 3. Out of scope (this mission)

- **Streaming-aware grammar constraint** — for Mode=grammar, this mission does NOT support streamed responses. Grammar-constrained outputs come back as a complete token sequence. Streaming + grammar is a v2 problem (requires partial-validation logic across stream chunks).
- **Schema synthesis from natural language** — "describe what shape you want and I'll generate the schema." Future tooling-on-top.
- **Cross-call schema state** — every call carries its own schema. No reuse of "last schema" across calls.
- **Function-calling via response_format** — even though OpenAI's `response_format` can accept tool-call shapes, this mission does NOT collapse the existing tool-use path into response_format. Tool calls stay tool calls; this mission is specifically for typed-DATA responses.
- **Pydantic-style Python integration.** This is a Go-side concern; Python tool authors who want Pydantic models supply JSON schemas exported from Pydantic.

## 4. UX shape

### 4.1 Workflow YAML
See §2.4.

### 4.2 Settings panel
```
Settings → Models → Response format defaults

Default JSON validation behaviour for response_format calls:
  ⦿ Retry once on schema-validation failure (recommended)
  ○ Strict — fail immediately on validation error
  ○ Best-effort — return unvalidated response if schema fails

Per-provider grammar support:
  • OpenRouter         not supported
  • Anthropic          not supported
  • Local llama-server supported (GBNF)
  • Ollama             supported (GBNF, requires llama.cpp backend)
```

### 4.3 Audit row
```
2026-05-04T14:22:11Z  kind=llm.structured.response
  provider=openrouter  model=anthropic/claude-sonnet-4.6
  format_mode=json_schema  schema_hash=abc123
  validation_outcome=passed   tokens={prompt:1200, completion:280}
```

## 5. Functional requirements

- **FR-001** `LLMRequest.ResponseFormat` field is optional and additive; existing call sites unaffected when nil.
- **FR-002** Adapters expose `Capabilities()` flags `SupportsJSONMode`, `SupportsStructuredOutput`, `SupportsJSONSchema`, `SupportsGrammar`, `SupportsRegexGrammar`, `GrammarFormat`.
- **FR-003** Each adapter implements `ApplyResponseFormat` + `ValidateResponse` per §2.3.
- **FR-004** Mode=json_schema with adapter SupportsStructuredOutput → native pass-through. Without → JSON mode + post-hoc validate. Without either → prompt-engineering fallback + post-hoc validate.
- **FR-005** Mode=grammar with adapter SupportsGrammar → native pass-through. Without → returns `ErrUnsupportedFormat`.
- **FR-006** Validation failures with `StrictValidation=false` trigger ONE auto-retry with a "your previous response was invalid: <error>" tail-prompt; second failure returns unvalidated string + `validation_error` field.
- **FR-007** Workflow `structured_llm` step kind accepts a `response_format` block matching §2.4.
- **FR-008** `/json` slash command parses inline schema + prompt, renders the validated JSON tree in chat.
- **FR-009** `respond_with_json(schema)` builtin tool, when called, constrains the model's next message to the supplied schema.
- **FR-010** Audit kind `KindLLMStructuredResponse` emitted per call with `{provider, model, format_mode, schema_hash, validation_outcome, tokens}`. No raw schema or response payload.
- **FR-011** Settings panel exposes the validation-on-fail behaviour (retry / strict / best-effort).

## 6. Non-functional requirements

- **NFR-001 (Compatibility)** Existing call sites without `ResponseFormat` set behave identically to today.
- **NFR-002 (Determinism)** Same `(prompt, schema)` input MUST produce semantically equivalent output across retries (same fields, same types — actual values can vary).
- **NFR-003 (Performance)** Validation overhead ≤ 5 ms p50 / ≤ 15 ms p99 for schemas under 50 properties. Larger schemas are best-effort.
- **NFR-004 (Audit-completeness)** Every structured-format call logs an audit event regardless of validation outcome.
- **NFR-005 (Capability-honesty)** A model reporting `SupportsStructuredOutput=true` MUST actually receive native pass-through; if the adapter falls back to JSON-mode-and-validate, that's a capability-table bug, not a runtime decision.

## 7. Threats considered

| Threat | Mitigation |
|---|---|
| Model returns valid-JSON-but-doesn't-match-schema | `ValidateResponse` catches and triggers retry; second failure surfaces typed error |
| Schema is ambiguous and validates either of two response shapes | Schema authoring is the user's responsibility; the harness exposes the validator outcome but doesn't try to reason about schema quality |
| Grammar contains a token sequence that's never satisfiable by the model's vocabulary | Local runtime returns its own error; we surface as `ErrUnsatisfiableGrammar` |
| User supplies massive 500-property schema that blows validation latency | NFR-003 is best-effort above 50 properties; document the soft limit |
| Schema leaks user-private structure into the audit log | Audit logs `schema_hash` only, not the schema itself |
| Adapter falsely claims `SupportsGrammar=true` | Integration test per adapter that round-trips a known GBNF and asserts the response matches |

## 8. Open questions

1. **Should `Mode: grammar` accept a JSON-schema and auto-translate to GBNF?** Convenience win for local runtimes; risks dialect mismatches. Lean: no for v1, surface as a separate `Mode: json_schema` with adapter dispatch.
2. **Is the auto-retry on validation failure configurable per-call?** Currently global setting. Lean: per-call wins (`StrictValidation` on the request) plus the global default for unset cases.
3. **Should `/json` save the schema-prompt pair as a reusable `/jsonNAME` slash command?** Out of scope here, lives with `user-slash-commands-01KQ8TD9`.

## 9. Spec mapping

| Existing | This mission |
|---|---|
| `provider-implementation-uniformity-01KQ8V4F` | Reuses capability flags + extends struct with grammar fields |
| `local-model-runtimes-01KQ8VMZ` | Provides the `SupportsGrammar=true` adapters this mission targets |
| `workflows-agentic-01KW2D3X` | Adds `structured_llm` step kind alongside existing `tool` / `transform` |
| `user-slash-commands-01KQ8TD9` | The `/json` command pattern this mission introduces |
| `_archive/llm-connector-01KQ1770` | The `LLMRequest` struct this mission extends |

## 10. Success looks like

A user can:

1. Author a workflow with a `structured_llm` step + JSON schema and trust the output's shape without a manual validation step.
2. Type `/json` in chat with an inline schema and get back a validated JSON tree — even against models that don't natively support response_format.
3. In Settings, see at-a-glance which providers offer native structured-output vs. fallback emulation.
4. Switch from `openrouter/claude-opus-4.5` to local `llama-server` for a structured-output workload and have the harness automatically pick GBNF over JSON-mode-and-validate.
5. Audit every structured call — when, which provider, which schema-hash, validation outcome — without secrets or full payloads in the log.

That's the bar.
