# Spec: Model fallback routing

**Status**: draft · **Owner**: alecfeeman

## 1. Why

The escalation primitive in the graph kernel routes **up** to a stronger model on demand. There is no construct for routing **sideways** when the chosen provider is degraded: an Anthropic 529 today aborts the turn; the user retries manually or switches provider in Settings.

For a tool people rely on for real work, this is a reliability gap. The fix isn't blind retry — it's an explicit **fallback chain** the user (or workflow author) declares: "this turn wants `claude-opus-4-7`; on 5xx or rate-limit, try `gpt-4o` via OpenRouter; on continued failure, surface to the user." Cedar policy gates which fallback chains a session can use.

## 2. Goals

- A `FallbackChain` is a declarative ordered list of `(provider, model, [param overrides])` entries with per-entry trigger conditions.
- Chains are first-class settings: app-level default, per-session override, per-graph-node override.
- The LLM connector consults the chain when a primary call fails; transitions are visible in the trace (`fallback_attempted` event with reason).
- Cedar gates "which chains is this session allowed to use" so a locked-down sub-agent can't silently swap to a model outside its budget.
- User-visible: a small status pill in the chat surface flips to "using fallback: gpt-4o (Anthropic rate-limited)" when a fallback is active.

## 3. Non-goals

- Automatic chain construction. The user authors or selects chains; we don't infer them.
- Cost-based routing. Cost-vs-capability optimization is a deeper product play; this mission is reliability-only.
- Streaming continuation across providers. If a fallback kicks in mid-stream, the turn restarts from the user's last message; we don't try to glue partial streams together.
- Cross-provider tool-call schema translation. If `claude-opus-4-7` fails mid-tool-call and the fallback is `gpt-4o`, the turn restarts (per above) — we don't translate in-flight tool definitions.

## 4. Functional requirements

### Data model

| ID | Requirement | Status |
|---|---|---|
| FR-001 | New `core/llm/fallback/` package: `Chain{ID, Name, Description, Entries []ChainEntry}`, `ChainEntry{ProviderID, Model, ParamOverrides, Triggers []TriggerCondition, MaxAttempts}`. | proposed |
| FR-002 | `TriggerCondition` enum: `error_5xx`, `error_429`, `error_auth_failed`, `error_context_overflow`, `error_safety_block`, `error_any`. | proposed |
| FR-003 | Chains persisted at `<DataDir>/llm/fallback_chains/*.yaml`; bundled defaults at `core/llm/fallback/bundled/*.yaml` (e.g. `anthropic-with-openrouter-fallback.yaml`). | proposed |
| FR-004 | New RPCs `LLM_ListFallbackChains`, `LLM_LoadChain(id)`, `LLM_SaveChain(chain)`, `LLM_DeleteChain(id)`. | proposed |

### Runtime integration

| ID | Requirement | Status |
|---|---|---|
| FR-005 | `core/llm/connector.go`'s `Generate()` wraps the primary call; on failure, walks the active chain and retries. Emits a `llm:fallback-attempted` broker event per attempt with `{reason, from, to, attempt}`. | proposed |
| FR-006 | Cedar adds `Action::"llm_fallback"` resource type with `chain_id` attribute; sessions in lockdown postures fail closed (no fallback unless explicitly allowed). | proposed |
| FR-007 | Per-graph-node attr `fallback_chain_id` on the `model` and `escalate` node kinds. Empty means "use session default". | proposed |
| FR-008 | Audit event per fallback attempt with redaction-safe payload (provider name + model + trigger reason; no request bytes). | proposed |

### UI

| ID | Requirement | Status |
|---|---|---|
| FR-009 | Settings → LLM Routing panel: list chains, edit entries, set default. | proposed |
| FR-010 | Chat surface shows a transient "fallback active" pill while a non-primary entry is serving the current turn. Tooltip explains which trigger fired. | proposed |
| FR-011 | Session inspector trace view renders fallback events inline so a user can see the route a turn took. | proposed |

## 5. Open questions

- **Composition with `escalate`.** Escalation is an upgrade-to-stronger-model construct in the graph. Fallback is a sideways-on-failure construct in the connector. They compose poorly if both fire on the same turn. Proposal: escalate runs first (it's intentional); fallback runs only on failures of the escalated call.
- **Per-tool-call vs per-turn.** Should a single failed tool call within a turn trigger a chain hop, or only an overall turn failure? Proposal: per-turn for now. Per-tool-call is fragile and we don't have a clean restart point mid-turn.
- **User-visible attribution.** If a fallback writes the assistant message, should the message metadata reflect the actual model that produced it (not the requested one)? Yes; persist `actual_provider` + `actual_model` separately from the requested fields.

## 6. Acceptance criteria

- A chain `anthropic-then-openrouter` declared in YAML and selected as session default produces a successful turn when the Anthropic adapter is forced to error with a synthetic 529.
- The `llm:fallback-attempted` broker event fires exactly once for that turn with reason `error_5xx`.
- The chat surface shows the fallback pill during streaming and the assistant message metadata reports the OpenRouter model.
- A Cedar policy denying `llm_fallback` on this session causes the turn to fail closed (no fallback attempted).
