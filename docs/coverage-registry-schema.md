# coverage_registry.yaml — schema reference

The file `core/llm/coverage_registry.yaml` is a YAML document that maps every
`(struct, field, adapter)` triple in the wire-shape contract layer to either a
real test function reference or an explicit `unsupported:` reason.

## Why this exists

Silent-drop bugs — where an adapter silently omits a `GenerationRequest` field
from the wire body, or discards a streamed `tool_call` — pass all existing unit
tests because those tests mock the LLM boundary. The registry enforces that a
deliberate coverage decision was made for every field on every adapter before a
PR can merge.

**Friction is the feature.** Adding a new field to `GenerationRequest`, `Response`,
or `StreamEvent` without updating the registry fails `TestRegistryCompleteness`
with a message naming the exact missing triple.

## Top-level fields

```yaml
version: 1          # Schema version. Currently always 1.
entries:            # Ordered list of RegistryEntry objects.
  - ...
```

## RegistryEntry fields

```yaml
- struct: GenerationRequest   # Go type name. One of:
                              #   GenerationRequest | Response | StreamEvent
  field: Tools                # Exported Go field name (case-sensitive).
  adapter: openrouter         # Provider kind. One of:
                              #   anthropic | openai | openrouter | bedrock
                              # Use "*" only for fields universally rejected by
                              # all adapters (the reflection check treats "*" as
                              # covering all in-scope adapters simultaneously).
  tests:                      # List of Go test function names that verify this
    - TestOpenRouter_Tools_ToolsSerialized   # triple. At least one is required
                              # when unsupported is empty. Function names must
                              # exist in the adapter's *_wireshape_test.go file;
                              # the reflection check parses the AST to verify.
  unsupported: ""             # Human-readable reason string when the adapter
                              # intentionally omits this field. Non-empty value
                              # satisfies coverage even if tests: is empty.
```

## unsupported: policy

An `unsupported:` reason is **permanent** when the field truly cannot be sent
on the wire (e.g. `ProfileID` is an internal routing key) and **temporary**
when the adapter just hasn't implemented support yet.

Temporary placeholders use the convention:

```yaml
unsupported: "WP01 placeholder — WP02 adds TestAnthropicAdapter_Tools_ToolsSerialized"
```

This makes it easy to search for entries that need upgrading.

Permanent `unsupported:` examples:

```yaml
# Field consumed by middleware before reaching the adapter:
unsupported: "consumed by registry retry middleware before reaching the adapter"

# Field populated server-side, not by the adapter:
unsupported: "populated by registry CostReducer middleware after adapter returns"

# Field not supported by provider:
unsupported: "OpenAI does not expose prompt-caching breakpoints via API"
```

## Naming convention for test functions

Use the pattern `Test<Adapter>_<Scenario>_<Field><Direction>`:

- `<Adapter>` — capitalised adapter name: `AnthropicAdapter`, `OpenAIAdapter`,
  `OpenRouterAdapter`, `BedrockAdapter`
- `<Scenario>` — what the test covers: `Tools`, `ChatDefault`, `StreamedToolCalls`,
  `History`, `SystemPrompt`, etc.
- `<Field>` — the field being asserted: `ToolsSerialized`, `ResponseParsed`,
  `ToolCallsParsed`, etc.
- `Serialized` suffix — request body assertion (what we send on the wire)
- `Parsed` suffix — streaming response assertion (what we receive from the wire)

Examples:

```
TestAnthropicAdapter_Tools_ToolsSerialized
TestOpenAIAdapter_StreamedToolCalls_ToolCallsParsed
TestOpenRouterAdapter_ChatDefault_ResponseParsed
TestBedrockAdapter_Params_ParamsSerialized
```

## Adding a new struct field

1. Add the field to the Go struct in `core/llm/llm.go`.
2. Run `go test ./core/llm/wirecheck/... -run TestRegistryCompleteness` — it
   will fail naming the missing triple for each in-scope adapter.
3. For each adapter, either:
   - Write the wire-shape test (≤ 30 LOC per field, NFR-002) and add a `tests:`
     entry to the registry.
   - Decide the adapter doesn't support the field and add an `unsupported:` reason.
4. Re-run `TestRegistryCompleteness` — must be green before the PR merges.

## Adding a new adapter

1. Create the adapter package at `core/llm/<kind>/`.
2. Add `<kind>` to the `inScopeAdapters` slice in
   `core/llm/wirecheck/registry_completeness_test.go`.
3. The completeness check will fail for every (struct, field) pair.
4. Add entries for the new adapter to `coverage_registry.yaml`.
