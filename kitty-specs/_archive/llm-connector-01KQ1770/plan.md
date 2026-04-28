# Implementation Plan: LLM Connector — Multi-Provider Model Access

**Mission**: `llm-connector-01KQ1770`
**Spec**: `kitty-specs/llm-connector-01KQ1770/spec.md`
**Branch contract (from `setup-plan`)**:
- Feature branch: `feat/llm-connector-01KQ1770`
- Planning base / merge target: `main` (per `meta.json` + charter Branch Strategy)
- All changes ship via PR; squash-merge default; ≥ 1 maintainer review.

> Branch contract restated: feature work lands on `feat/llm-connector-01KQ1770`,
> targets `main`, and ships via PR. No direct push to `main` under any
> circumstance.

---

## 1. Overview

This plan turns the LLM-connector spec into a concrete Go architecture inside
`core/llm/`. The connector is the single in-tree surface that translates the
harness's provider-agnostic generation request into native provider protocols
for Anthropic, OpenAI, OpenRouter, AWS Bedrock, and local Ollama, while
preserving the charter's local-first, security-first, configuration-first, and
SOC 2-ready posture.

Bounding scope (v1):

- Day-one providers (FR-001): Anthropic (direct), OpenAI (direct), OpenRouter,
  AWS Bedrock, Ollama.
- Capabilities (FR-004 — FR-013): streaming chat, multi-turn history, tool
  calling, vision, JSON mode, prompt caching, reasoning blocks, usage/cost,
  cancellation, typed unsupported-capability errors.
- Reliability (FR-016, FR-017): per-provider retry with exponential backoff and
  jitter, transient/non-transient classification.
- Auditability (FR-014, FR-015, NFR-007, NFR-008, NFR-012): every request,
  chunk, retry, error, cancellation emitted to the append-only event log,
  redacted before persistence.
- Extensibility (FR-018, C-001, C-005): pluggable provider adapter contract;
  new providers (in-tree, enterprise, third-party) added in their own packages
  without modifying any other `core/` package.
- Pre-flight (FR-019): every credential reference resolved (or its failure
  reported) before any agent call.

Explicit non-goals for v1 (per spec Assumptions):

- Cross-provider fallback (deferred — `policy-engine` and a follow-up routing
  spec own this).
- OpenAI-compatible HTTP shim fronting the connector.
- Embeddings, fine-tuning, batch APIs, image generation.

---

## 2. Architectural Placement

The connector sits at the `core/llm/` package boundary. The Wails app, the
frontend, and any future hosted backend reach it only through `core/rpc`
(Charter DIRECTIVE_001; spec C-001). Providers live in their own sub-packages
so the registry is the only seam.

```
core/llm/
├── llm.go                # Public types: Request, Stream, Response, Capability,
│                         # Registry interface, ProviderAdapter contract,
│                         # error taxonomy. Re-exports today's adapter.go shape
│                         # extended for capabilities/usage/cancellation.
├── registry/             # In-memory ProviderAdapter registry; loads providers
│                         # from resolved bundle Provider Profiles.
├── capabilities/         # CapabilityDescriptor types + per-(provider,model)
│                         # capability gate (FR-013).
├── retry/                # Provider-agnostic exponential backoff + jitter
│                         # middleware (FR-016, FR-017).
├── audit/                # Event-log adapter: emits llm/* event kinds with
│                         # redaction-aware payload builders (FR-014, FR-015).
├── cost/                 # Cost-table loader + usage→cost reducer (FR-011).
├── credref/              # Thin adapter from spec FR-003 CredentialReference
│                         # to core/secrets resolve calls. Lives here (not in
│                         # secrets) because it's an LLM-connector ergonomics
│                         # layer over the upstream secrets package.
├── anthropic/            # Provider adapter — Anthropic SDK in this package
│                         # ONLY (DIRECTIVE_001 invariant).
├── openai/               # OpenAI SDK
├── openrouter/           # OpenAI-compatible client pointed at openrouter.ai
├── bedrock/              # AWS SDK Bedrock Runtime
├── ollama/               # Local HTTP client → 127.0.0.1:11434
└── internal/             # shared adapter helpers (HTTP retry shim, SSE parser
                          # wrapper) usable only by adapters in this tree.
```

Architectural-integrity invariants:

- No package outside `core/llm/<provider>/` imports any provider SDK
  (DIRECTIVE_001; FR-018; C-001; C-005).
- `core/llm/llm.go` is the only place that defines the public adapter
  contract; provider packages depend on it but it does not depend on them.
- Wails / RPC / UI never import `core/llm/<provider>/` directly — they go via
  `core/llm` registry through `core/rpc`.

---

## 3. Public API (Illustrative Signatures)

These signatures are illustrative — they extend (do not replace) the existing
`core/llm/adapter.go` stub. Implementation details land in tasks.

```go
package llm

// Capability is a capability id (FR-013).
type Capability string

const (
    CapStreaming      Capability = "streaming"
    CapToolCalling    Capability = "tool_calling"
    CapVision         Capability = "vision"
    CapJSONMode       Capability = "json_mode"
    CapPromptCaching  Capability = "prompt_caching"
    CapReasoning      Capability = "reasoning"
    CapCancellation   Capability = "cancellation"
    CapUsageReporting Capability = "usage_reporting"
)

// CapabilityDescriptor describes what a (provider, model) supports (FR-013).
type CapabilityDescriptor struct {
    Provider     string
    Model        string
    Supported    map[Capability]bool
    Notes        map[Capability]string
}

// CredentialReference is the indirect pointer (FR-003, C-002, secrets FR-001).
type CredentialReference struct {
    Kind    string // "env" | "keychain" | "file" | "aws_profile" | "kms"
    Locator string // kind-specific: env name, keychain entry, path, profile, ARN
}

// ProviderProfile is the materialized form of one bundle Provider Profile
// artifact (FR-002, C-004).
type ProviderProfile struct {
    ID            string                // stable id used by agents
    Kind          string                // "anthropic" | "openai" | ...
    Model         string
    Cred          CredentialReference
    Region        string                // bedrock; optional otherwise
    Endpoint      string                // optional (custom OpenAI-compat / Ollama URL)
    CapabilityHints map[Capability]bool // optional pre-flight hints
    Defaults      map[string]any        // model defaults (temperature, etc.)
}

// GenerationRequest is the provider-agnostic shape (FR-004 — FR-010).
type GenerationRequest struct {
    ProfileID     string             // refers to ProviderProfile.ID
    System        string
    Messages      []Message          // ordered history, FR-005
    Tools         []ToolSpec         // FR-006
    Attachments   []Attachment       // FR-007 (vision)
    JSONMode      *JSONModeSpec      // FR-008
    Caching       *CachingSpec       // FR-009 (cacheable prefix marks)
    Reasoning     *ReasoningSpec     // FR-010
    Params        map[string]any     // temperature, max_tokens, etc.
    RetryOverride *RetryPolicy       // FR-016 per-call override
    SessionID     string             // for event-log scoping
}

// Stream is the streaming response surface (FR-004, FR-012).
type Stream interface {
    Events() <-chan StreamEvent  // text/tool/reasoning/usage/finish kinds
    Cancel() error               // FR-012; closes upstream connection ≤ 1s p99
    Final() (Response, error)    // blocks until finish/error; safe after Cancel
}

// Response is the non-streaming-equivalent final summary (FR-011).
type Response struct {
    Content      []ContentPart
    ToolCalls    []ToolUse
    Reasoning    []ReasoningBlock
    FinishReason string
    Usage        Usage             // input/output/cached/reasoning tokens
    Cost         Cost              // derived; FR-011
    Attempts     int               // 1 = first-try; >1 = recovered after retry
    SnapshotID   string            // for replay (FR-020)
}

// ProviderAdapter is the per-provider plug-in contract (FR-018).
type ProviderAdapter interface {
    Kind() string
    Capabilities(model string) CapabilityDescriptor
    Stream(ctx context.Context, req GenerationRequest, prof ProviderProfile) (Stream, error)
}

// Registry is the connector's public façade (extends today's stub).
type Registry interface {
    RegisterAdapter(a ProviderAdapter)               // FR-018, C-005
    LoadProfiles(profs []ProviderProfile) error      // from bundle
    PreflightAll(ctx context.Context) []PreflightResult // FR-019
    Profile(id string) (ProviderProfile, error)
    Stream(ctx context.Context, req GenerationRequest) (Stream, error)
}
```

Errors form a typed taxonomy (FR-013, FR-017): `ErrCapabilityUnsupported`,
`ErrCredentialResolution`, `ErrTransient`, `ErrRetryBudgetExhausted`,
`ErrAuth`, `ErrInvalidRequest`, `ErrPolicyDenied`, `ErrCancelled`. Adapters
classify; middleware acts.

---

## 4. Internal Layering

Request pipeline (left = entry; right = wire):

```
Caller (rpc/session)
  └─→ Registry.Stream
        ├─ Profile lookup (FR-002)
        ├─ CapabilityGate.check(req, profile)        (FR-013)
        ├─ PolicyGuard.allow(req, profile)           (policy-engine hook)
        ├─ CredentialResolver.resolve(profile.Cred)  (FR-003 → secrets)
        ├─ AuditEmitter.requestSubmitted(...)        (FR-014, FR-015)
        ├─ RetryMiddleware.run(attempt → adapter)    (FR-016, FR-017)
        │     └─ ProviderAdapter.Stream(...)         (provider-specific)
        ├─ AuditEmitter.streamChunk(...) per event   (FR-014)
        │ AuditEmitter.retryAttempted(...) per retry
        ├─ CostReducer.derive(usage, costTable)      (FR-011)
        └─ AuditEmitter.responseFinal(...)           (NFR-012)
```

Layers in detail:

- **Adapter base**: `core/llm/internal/adapterbase` provides shared helpers —
  SSE/JSONL stream parsing, HTTP retry/backoff classification (provider-side),
  and typed-error mapping. Each adapter sub-package owns its SDK; the base
  package does not import any provider SDK.

- **CapabilityGate** (FR-013): every request is checked against the resolved
  `CapabilityDescriptor` before any wire call. Unsupported requests emit
  `capability_rejected` to the event log and return `ErrCapabilityUnsupported`
  without incurring provider cost (Acceptance Scenario US2.2).

- **RetryMiddleware** (FR-016, FR-017): adapter returns typed errors with a
  `Transient bool`. Middleware applies exponential backoff with full jitter,
  bounded by per-provider `RetryPolicy`. Retries emit `retry_attempted` events
  with attempt number and backoff delay (US4 Acceptance 1). On exhausted
  budget: `ErrRetryBudgetExhausted` with the attempt list (US4 Acceptance 3).
  Streaming retry only re-issues if **no** chunks have been delivered to the
  caller (otherwise: surface the partial-stream drop as a transient failure
  visible in the log, never silently double-bill).

- **AuditEmitter** (FR-014, FR-015, C-003): single chokepoint to event-log.
  Every payload passes through redaction (`core/event` redaction pipeline per
  upstream spec FR-005) before persistence. The connector itself never logs
  resolved credential bytes; the redaction layer is defense-in-depth, not the
  primary guarantee (NFR-007, NFR-008).

- **CostReducer** (FR-011): consumes the cost table; adapters report raw
  token counts; the reducer derives `Cost` and attaches it to the final
  `Response` and the `response_final` event payload. Operator-overridable
  table (default starter table per spec Open Question 2).

- **PreflightCoordinator** (FR-019, NFR-005): on `LoadProfiles`, validate
  that every `CredentialReference` resolves successfully. Failures surface as
  startup errors keyed by `ProfileID` and never trigger a model call (US1
  Acceptance 3, edge case "Bedrock aws_profile without region").

---

## 5. Data Model

### 5.1 Bundle artifact: Provider Profile (FR-002, C-004)

A new artifact kind registered with the bundle resolver, stored as YAML
inside the bundle. No new top-level configuration surface.

```yaml
# In a bundle's manifest declares one or more artifacts of kind: llm_provider
artifacts:
  - kind: llm_provider
    name: anthropic-default
    path: providers/anthropic-default.yaml
    content_hash: sha256:...
    kind_metadata: { schema_version: 1 }

# providers/anthropic-default.yaml
schema_version: 1
id: anthropic-default
kind: anthropic
model: claude-sonnet-4-7-20260420
auth:
  keychain: kaneaz/anthropic-api-key   # FR-003 indirect ref
region: ""                              # not used by anthropic-direct
capabilities:                           # optional pre-flight hints
  vision: true
  prompt_caching: true
defaults:
  temperature: 0.7
  max_tokens: 4096
retry:                                  # per-provider override (FR-016)
  max_attempts: 3
  base_ms: 250
  max_ms: 5000
  jitter: full
```

Validation rules (`core/llm/profile_schema.go`):

- `kind` ∈ {anthropic, openai, openrouter, bedrock, ollama, …registered}.
- `auth` MUST be exactly one of `env|keychain|file|aws_profile|kms` per
  upstream secrets FR-001 (no inline plaintext — C-002, secrets FR-015).
- `kind: bedrock` MUST have `region` non-empty (edge case #4).
- `id` unique within the harness's resolved profile set.

Registration flow:

1. `bundle-format-resolver` discovers artifacts of `kind: llm_provider`.
2. Calls into the connector's `ArtifactKindHandler` (registered at startup).
3. Handler validates, parses, and registers the resulting `ProviderProfile`
   with `core/llm` Registry.
4. Resolution + validation events emitted to event log.

### 5.2 Credential Reference shape (FR-003)

Identical wire shape to upstream `secrets-keychain` FR-001. The connector
**does not** re-define it; it references the upstream `core/secrets.Reference`
type via `credref/`. Defense-in-depth: a config-load validator in the
connector also rejects inline plaintext (US1 Acceptance 3, secrets FR-015).

### 5.3 Event-log kinds emitted by the connector

Namespaced `llm/` per upstream `event-log` FR-017. Each carries `session_id`,
`emitter_id="llm/<provider-kind>"`, `profile_id`, `model`, `event_id` (ULID),
and a redacted payload.

| Kind | Purpose | FR coverage |
|---|---|---|
| `llm/request_submitted` | request reached connector after gates | FR-014, NFR-012 |
| `llm/preflight_resolved` | credential ref resolved (no value!) | FR-019, secrets FR-012 |
| `llm/preflight_failed` | credential ref failed at startup | FR-019 |
| `llm/capability_rejected` | gate fired before wire call | FR-013 |
| `llm/retry_attempted` | attempt N failed transiently, backoff | FR-016 |
| `llm/stream_chunk` | one streamed delta (text/tool/reason/usage) | FR-014 |
| `llm/response_final` | final response with usage + cost | FR-011, NFR-012 |
| `llm/cancelled` | caller cancellation reached upstream | FR-012 |
| `llm/error` | non-transient or budget-exhausted error | FR-017 |
| `llm/policy_denied` | policy-engine refusal pre-call | policy-engine integration |

Per US3 Acceptance 1: a successful request produces at minimum
`request_submitted` → N × `stream_chunk` → `response_final`. Per Acceptance 4
a failed stream produces `request_submitted` → partial chunks → `error` and
the chain remains internally consistent.

### 5.4 Cost-table format (FR-011)

Default starter table (spec Open Question 2 default) ships in
`core/llm/cost/starter_table.yaml`:

```yaml
schema_version: 1
currency: USD
entries:
  - kind: anthropic
    model: claude-sonnet-4-7-*       # glob match
    per_million_tokens:
      input: 3.00
      output: 15.00
      cached_input_read: 0.30
      cached_input_write: 3.75
  - kind: openai
    model: gpt-4o-*
    per_million_tokens: { input: 2.50, output: 10.00 }
```

Operator override path: `~/.config/kaneaz/cost-table.yaml` (or platform
equivalent), merged on top of the starter table. Missing entries produce a
`Cost.Indeterminate=true` flag rather than failing the request (US3
Acceptance 1 must still pass without a cost number).

---

## 6. Integration Points

### 6.1 secrets-keychain-01KQ1A3M

- The connector calls `core/secrets.Backend.Resolve(ref)` at request time
  (FR-003, secrets FR-002, secrets FR-013).
- Resolved bytes live in a `core/secrets.Secret` (`[]byte`-typed; never
  `string`; per upstream research D7) and are zeroized when the adapter
  finishes building the wire request (secrets FR-013).
- Pre-flight (FR-019) calls `core/secrets.PreflightAll` for every loaded
  profile — failures map to `llm/preflight_failed` events.
- TTL cache: the connector relies on the upstream cache (secrets FR-010,
  FR-011); the connector does not add its own credential cache.
- AWS Bedrock specifically uses `aws_profile` references (secrets FR-006);
  the Bedrock adapter constructs an SDK config via the upstream backend's
  resolver — **no AWS SDK code outside `core/llm/bedrock/`**.

### 6.2 event-log-01KQ1A3M

- All emit goes through `core/event.Log.Append` (event-log FR-001).
- Event kinds registered under `llm/` namespace (event-log FR-017).
- Redaction is **not** the connector's responsibility to implement — it is
  the event-log pipeline's (event-log FR-005, FR-006). The connector's
  contract: never put resolved credentials into the payload in the first
  place (defense-in-depth alignment per secrets-keychain assumption).
- Replay determinism (event-log FR-009; FR-020 here): each
  `request_submitted` payload includes the `ResolvedGraph.snapshot_id` it
  was issued under, so replay can recreate the exact provider profile state.

### 6.3 bundle-format-resolver-01KQ1A3J

- Connector registers an `ArtifactKindHandler` for `kind: llm_provider`
  (bundle FR-002, FR-005) at process start.
- Handler signature follows the upstream contract:
  `Parse(bytes)→Profile`, `Validate(Profile, ManifestCtx)→errors`,
  `Activate(Profile, ResolverCtx)→registration in core/llm.Registry`.
- Activation order is deterministic per `ResolvedGraph.activation_order`
  (bundle FR-005). Profile-id collisions are surfaced through the
  resolver's `FR-009` conflict-detection path (team + personal overlay).

### 6.4 policy-engine-01KQ1A3N

The connector is one of the policy engine's most-constrained consumers.
Integration touches three points:

- **Profile registration** — the policy engine's `LLM.Allowlist(provider,
  model)` is consulted at `Registry.LoadProfiles` time. Disallowed profiles
  emit `llm/policy_denied` and are not registered (policy spec US1
  Acceptance 1).
- **Per-request gate** — before retry middleware, `PolicyGuard.allow(req)`
  enforces cost-ceiling, model-family limits, and bundle-source allowlists.
- **Cost ceilings** — the `CostReducer` reports against the policy
  engine's running budget; a request that would breach ceiling is denied
  (`ErrPolicyDenied`) before the wire call.

The policy engine itself is a separate mission; this plan only nails down
the **call sites** and the typed-denial taxonomy. The connector ships with a
no-op `PolicyGuard` until that mission lands.

### 6.5 core/rpc

The Wails frontend reaches the connector exclusively via the existing
`core/rpc` surface (charter: same RPC surface a future hosted backend would
expose; spec C-001). RPC introduces neither new types nor a parallel adapter
seam. v1 RPC methods envisioned (defined in tasks, not here):

- `LLM.ListProfiles() → [ProfileSummary]`
- `LLM.Stream(req) → server-stream of StreamEvent`
- `LLM.Cancel(streamID)`
- `LLM.Preflight() → [PreflightResult]`

---

## 7. Phasing

### v1.0 — this mission (all 5 day-one providers)

Scope:

- Adapter scaffolding + `Registry` + `ProviderAdapter` contract.
- All five day-one providers: Anthropic, OpenAI, OpenRouter, Bedrock, Ollama.
- Capabilities: streaming text, multi-turn, tool calling, vision, JSON mode,
  prompt caching, reasoning, usage/cost, cancellation, capability gate.
- Retry middleware + transient/non-transient classification.
- Pre-flight credential resolution + actionable startup errors.
- Audit emit for all kinds in §5.3.
- Cost-table starter + operator override.
- Provider-Profile artifact-kind handler registered with bundle resolver.
- Test coverage: ≥ 80 % `core/llm/**` line (charter target); black-box
  integration tests per provider against either real APIs (gated by
  credentials at PR-time) or recorded VCR fixtures (default).

### v1.x — fast-follows (separate missions)

- **Cross-provider fallback** (spec Assumption #4 deferred): falls under
  the policy engine routing layer — the connector exposes the typed denial
  / transient-budget-exhausted signals it needs.
- **OpenAI-compatible HTTP shim** (spec Assumption #6 deferred): wraps the
  connector behind an HTTP server so external tools can drive it as if it
  were OpenAI.
- **Additional cloud KMS for AWS Bedrock auth** (secrets-keychain D4 v2
  scope): Azure / GCP backends. The Bedrock adapter is unchanged.

### v2 — out of scope this spec

- Embeddings, batch APIs, fine-tuning APIs, image generation (spec
  Assumption #5).
- Hardware-token (`go-piv`) credential refs in the LLM context
  (secrets-keychain D5 → per-provider integration).
- HSM (PKCS#11) credential refs (secrets-keychain D5 deferred).

---

## 8. Risk Register

Premortem-driven (Charter Tactic `premortem-risk-identification`). Top
failure modes and mitigations:

| # | Risk | Impact | Likelihood | Mitigation |
|---|---|---|---|---|
| R1 | A provider SDK leaks types into the registry contract (e.g., adapter returns an `anthropic.Message`), violating DIRECTIVE_001 / FR-018. | High — collapses the extensibility story; every future provider becomes a core change. | Medium without guardrails. | CI lint rule: any non-test file under `core/` (excluding `core/llm/<kind>/`) that imports a provider SDK fails the build. Adapter contract returns only `core/llm` types. |
| R2 | A resolved credential leaks into a payload via `params: map[string]any` round-tripping the wire body. | Critical — NFR-007 violation, breaks SOC 2 posture. | Medium — provider SDKs sometimes round-trip request bodies on retry. | The audit emitter reconstructs a redacted view from the typed `GenerationRequest` shape, not from the wire body. Adapters MUST NOT log raw HTTP request bodies. Tests assert zero plaintext credential bytes across the full provider matrix (SC-004). |
| R3 | Streaming retry double-bills the operator. Adapter declares "transient" mid-stream; middleware retries; both attempts deliver overlapping content. | High — operator surprise; cost amplification (FR-017 violation). | Low if explicitly designed; otherwise high. | Streaming retry only re-issues if **zero** chunks have been delivered. Once a chunk is forwarded, mid-stream drops are surfaced as terminal `error` events; the upstream session retry is the operator's choice, not the connector's. |
| R4 | Capability descriptor drifts from real provider behavior — the gate rejects something the provider actually supports, or admits something it doesn't, leading to silent failures. | Medium — degrades the configuration-first promise. | Medium — providers ship new model families monthly. | Capability descriptors live as YAML data, not code, in `core/llm/capabilities/data/`. Per-provider integration tests assert descriptor parity against a recorded matrix per release. Unknown model defaults to "streaming-only safe baseline" with a warning. |
| R5 | Cancellation doesn't propagate to the underlying HTTP/2 stream; provider keeps generating and billing. | High — NFR-003 violation, FR-012 violation. | Medium for SDK-wrapped clients that own connection lifecycle. | Adapters MUST honor a `context.Context` deadline AND expose a `Cancel()` that closes the response body. Cancellation test: assert socket close within 1s p99 against a slow-stream fake. |
| R6 | OS keychain UX (macOS prompts) makes pre-flight pop a dialog at every harness start. Operator perceives harness as "broken." | Medium — usability. | High on macOS without care. | Pre-flight uses the upstream secrets cache (TTL configurable). First-prompt UX hint surfaced via RPC to frontend; documentation calls it out. Coordinated with secrets-keychain Open Question 1. |
| R7 | Bedrock + AWS profile + missing region produces a generic AWS SDK error rather than a typed "region not configured" message (edge case #4). | Low — operator confusion only, no security impact. | Medium. | Bedrock adapter validates region presence in `Validate()`; pre-flight catches it before any AWS call. |
| R8 | Cost table goes stale; operators see incorrect cost reporting and trust the audit log less. | Low security; medium product. | High — model pricing churns monthly. | Starter table marked `best_effort: true`; operator override is the supported path. CI has a freshness check that warns (does not block) on tables > 90 days old. |
| R9 | Event-log redaction recall < 99 % on novel credential shapes a provider returns in error messages (e.g., "key ABCDEF... is invalid"). | Medium — NFR-008 boundary. | Medium. | Pattern catalog co-evolves with the event-log mission; the connector contributes provider-specific patterns (Anthropic `sk-ant-*`, OpenAI `sk-*`, Bedrock signature substrings, etc.) and tests them in the connector's audit suite. |
| R10 | Policy-engine call site is missing or stubbed too aggressively, so an enterprise build ships without enforced provider allowlists (spec C-005, policy-engine US1). | High enterprise impact. | Low if explicitly tracked. | Even pre-policy-engine, the integration call sites in §6.4 ship as `PolicyGuard` interface calls returning `Allow` from a no-op impl. Replacing the no-op with the real engine is a one-binding change. |

---

## 9. Open Questions for the User

These remain unresolved after research; resolving each materially shapes
implementation. Defaults from the spec are noted; the listed questions are
the ones the user explicitly flagged as `[NEEDS CLARIFICATION]` plus a
small handful surfaced during planning.

### Unresolved from the spec

1. **Tool-call response shape (spec OQ-1, FR-006)** — unified normalized
   envelope across providers, or provider-native passthrough with a thin
   wrapper? Plan default (matches spec default): **unified normalized
   envelope** with the raw provider-native body accessible via a `Raw
   json.RawMessage` field for advanced cases.

2. **Cost-table source (spec OQ-2, FR-011)** — ship a starter table or
   require operators to supply one from day one? Plan default (matches
   spec default): **ship a starter table** marked `best_effort: true`,
   with a clear operator-override path documented in `quickstart.md`.

3. **Default retry budget (spec OQ-3, FR-016)** — what numeric defaults
   when bundles do not specify? Plan default (matches spec default):
   **3 attempts total** (initial + 2 retries), base delay 250 ms, max
   delay 5 s, full jitter; per-provider overridable in profile YAML.

### Surfaced during planning

4. **OpenRouter capability descriptor source** — OpenRouter routes to many
   underlying models with different capabilities. Do we (a) ship a static
   per-routed-model capability table, (b) query OpenRouter's `/models`
   endpoint at startup and cache, or (c) treat OpenRouter as
   "capabilities-from-hint" and trust bundle authors? Plan default if
   unresolved: **(b) query at startup, cache in event-log-pinned snapshot
   for replay determinism**.

5. **Reasoning-block taxonomy across providers** — Anthropic's
   `thinking` blocks, OpenRouter-routed reasoning models, and Bedrock's
   reasoning view all differ. The plan currently defines a unified
   `ReasoningBlock` type with `Type, Content, Summary` fields; should the
   provider's raw reasoning frame be preserved verbatim under `Raw`?
   Plan default if unresolved: **yes, preserve verbatim** for replay /
   audit (consistent with the tool-call default in OQ-1).

6. **Pre-flight prompt-caching warm-up** — do we issue a tiny "ping" call
   per profile at pre-flight to warm prompt-caching counters and confirm
   capability hints, or skip and rely on first real call? Plan default if
   unresolved: **skip — pre-flight is auth-only**, and capability
   descriptors are static data. Avoids surprise billing.

---

## Charter Check

Per `spec-kitty charter context --action plan` (loaded above):

- **DIRECTIVE_001 (Architectural Integrity)**: PASS by construction —
  every provider lives in its own sub-package; the registry contract is
  the single seam; CI guard rule blocks cross-package SDK imports
  (§2, R1).
- **DIRECTIVE_003 (Decision Documentation)**: PASS — every material
  trade-off (provider isolation, retry semantics, cost-table policy,
  reasoning taxonomy) is recorded in this plan; an ADR will accompany
  the Provider-Profile artifact-kind contract (one ADR, drafted during
  tasks).
- **DIRECTIVE_010 (Specification Fidelity)**: PASS — every FR/NFR/C is
  cited in the corresponding section. Deviations (none material in v1)
  would be called out in §9 or in the ADR.
- **DIRECTIVE_024 (Locality of Change)**: PASS — provider-by-provider
  decomposition keeps blast radius inside each adapter package.
- **DIRECTIVE_028 (Efficient Local Tooling)**: PASS — black-box tests via
  recorded VCR fixtures are the default; live-API integration tests are
  opt-in behind credentials, matching upstream practice.
- **DIRECTIVE_029 (Agent Commit Signing)**: applies at implementation
  time, not planning.
- **DIRECTIVE_030 (Test and Typecheck Quality Gate)**: tasks must enforce
  `go test ./... -race`, `go vet`, `golangci-lint`, and ≥ 80 % coverage on
  `core/llm/**`.
- **DIRECTIVE_033 (Explicit Staging)**: applies at commit time.
- **DIRECTIVE_036 (Black-Box Testing)**: PASS — connector tests drive the
  registry through its public API; provider-internal helpers are not
  asserted directly.

No charter conflicts to escalate.

---

## Phase 0 / Phase 1 artifact status

- **Phase 0 (`research.md`)**: NOT generated by this plan. Provider APIs
  are well-known; the parent agent explicitly noted no research artifacts
  are needed for this mission. The two upstream research docs cited in
  this plan (`secrets-keychain/research.md`, `bundle-format-resolver/
  research.md`) supply every external library decision the connector
  inherits.
- **Phase 1 (`data-model.md`, `contracts/`, `quickstart.md`)**: not
  generated as separate files; the data model is captured in §5 and the
  contracts in §3 of this plan. If the tasks phase prefers separate
  files, they will be promoted from this plan verbatim.

---

## Branch contract — restated for hand-off

Feature branch: `feat/llm-connector-01KQ1770`. Planning base / merge
target: `main`. All work ships via PR with ≥ 1 maintainer review and
squash-merge default. No direct push to `main`. Suggested next command
for the user: `/spec-kitty.tasks --mission llm-connector-01KQ1770`.
