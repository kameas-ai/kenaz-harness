# Implementation Plan — Policy Engine

**Mission**: `policy-engine-01KQ1A3N`
**Spec**: `kitty-specs/policy-engine-01KQ1A3N/spec.md`
**Research**: `kitty-specs/policy-engine-01KQ1A3N/research.md`, `data-model.md`
**Status**: Draft (HOW)
**Created**: 2026-04-25

---

## 1. Overview

The policy engine is the embedded, in-process control plane that turns
kaneaz-harness from "configurable agent runtime" into "enterprise-deployable
agent runtime." It evaluates every policy-relevant action — provider activation,
MCP server install, A2A peer dial, bundle source fetch, scheduler dispatch,
LLM call, network exposure — against a layered (`org → team → personal`),
strictly-narrowing policy composition, with SOC 2-grade auditable decision
events.

**Engine pick (research D1)**: OPA + Rego, embedded in-process via
`github.com/open-policy-agent/opa/v1/rego`. No sidecar, no remote evaluator
in v1.

**Authoring surface (research D3)**: declarative YAML clauses (one clause per
control kind) that compile to Rego internally. Operators do not touch Rego
unless they want to; an escape hatch for raw Rego exists for advanced
authors.

**Distribution (research D5 / spec FR-002)**: policies are bundle artifacts
of kind `policy`, distributed and signed through the existing
`bundle-format-resolver` and `a2a-signed-cards-trust` machinery — never a
parallel channel.

**Audit (research D6 / spec FR-009, NFR-003)**: every evaluation emits a
typed `PolicyEvent` into the append-only `event-log`, hash-chained per that
mission's invariants. An optional, opt-in external mirror sink (file /
syslog / OTLP) is the v1.x story.

**Default posture (spec FR-007, NFR-005)**: fail-closed. Unknown control
kinds, unavailable inputs, missing signatures, broken layer-narrowing — all
deny by default.

---

## 2. Architectural placement

Per DIRECTIVE_001 (architectural integrity) and constraint C-001, all policy
logic lives under `core/policy/`. No `core/` package outside `core/policy/`
and the per-kind handler packages contains policy-evaluation logic.
Consumers (`core/llm`, `core/mcp`, `core/bundle`, `core/scheduler`, etc.)
call the public API only.

```
core/policy/                          # public API (PolicyEngine, types)
  engine/                             # OPA wrapper, evaluation API, hot path
    opa/                              # OPA-specific glue (the only package
                                      #   that imports github.com/open-policy-agent/opa)
  lower/                              # YAML-clause → Rego compiler
    schema/                           # JSON Schema for clause params
    rego_emitters/                    # per-kind Rego emission helpers
  clauses/                            # one subpackage per control kind
    provider_allowlist/
    model_allowlist/
    mcp_server_allowlist/
    mcp_capability_allowlist/
    a2a_peer_allowlist/
    network_tier/
    signature_required/
    redaction_strictness/
    cost_ceiling/
    sandbox_required/
    scheduler_permission/
  layer/                              # org→team→personal merge + validator
    validator/                        # strict-narrowing enforcement
  explain/                            # `harness policy explain` surface
  decisionlog/                        # mirror sink (file / syslog / OTLP) - v1.x
  override/                           # OverrideToken issuance + audit (v1.x)
  cache/                              # EffectivePolicy snapshot cache
  bundle/                             # `policy` artifact-kind handler
                                      #   registers with bundle-format-resolver
```

**Boundary rules**:

- `core/policy/engine/opa/` is the only package allowed to import OPA.
  Everything else uses the `engine.PolicyEngine` interface, so a future
  Cedar backend (research D2) is a drop-in.
- Clause packages declare their own param schema, lowering function, and
  consumer-facing evaluator. They register with the engine at startup;
  adding a new kind never touches another kind's package (NFR-006, SC-006).
- The bundle handler in `core/policy/bundle/` is the only place that
  imports `core/bundle` for kind-registration. It does not contain
  evaluation logic.
- `core/policy/layer/` knows about layers and merge semantics but does not
  know about Rego. It produces an `EffectivePolicy` from a list of
  `PolicyArtifact`s; the engine lowers and loads it.

---

## 3. Public API (illustrative — subject to refinement at WP time)

```go
// core/policy/policy.go — public surface

// PolicyEngine is the single API every consumer uses.
type PolicyEngine interface {
    // Evaluate returns a Decision in under 1ms p99 (NFR-001) on the hot path.
    Evaluate(ctx context.Context, action Action) (Decision, error)

    // Explain returns clauses that matched and clauses that would have
    // matched, for a given action — supports `harness policy explain`.
    Explain(ctx context.Context, action Action) (Explanation, error)

    // EffectivePolicy returns the current cached merged policy snapshot.
    EffectivePolicy() EffectivePolicy

    // Reload triggers a recompose + revalidate; called by the bundle
    // resolver when a policy artifact changes (FR-001 layer changes,
    // NFR-007 hot-reload).
    Reload(ctx context.Context) error
}

// Action is the typed input every consumer constructs at the action
// boundary. One Action shape per policy-relevant operation; concrete
// shapes live in the consuming package and implement Action.
type Action interface {
    // Kind names the consumer-side operation (e.g. "llm.call", "mcp.install",
    // "a2a.dial", "bundle.fetch", "scheduler.dispatch").
    Kind() string

    // Inputs returns a redaction-aware, JSON-shaped map of fields the
    // engine evaluates (provider id, model id, server id, peer id,
    // network tier, cost-so-far, etc.).
    Inputs() map[string]any
}

// Decision is the typed result of one Evaluate call.
type Decision struct {
    Outcome      Outcome    // allow | deny
    ReasonCode   ReasonCode // taxonomy from FR-008
    PolicyID     string     // source PolicyArtifact
    ClauseID     string     // matched clause
    InputSummary map[string]any
    EvaluatedAt  time.Time
    DecisionID   string     // ULID
}

type Outcome int
const (
    Allow Outcome = iota
    Deny
)

// ReasonCode is the closed denial taxonomy (FR-008). Consumers pattern
// match on this; the message is for humans.
type ReasonCode string
const (
    ReasonNotInAllowlist          ReasonCode = "not_in_allowlist"
    ReasonExceedsCeiling          ReasonCode = "exceeds_ceiling"
    ReasonMissingSignature        ReasonCode = "missing_signature"
    ReasonWrongSigner             ReasonCode = "wrong_signer"
    ReasonNetworkTierNotPermitted ReasonCode = "network_tier_not_permitted"
    ReasonCapabilityNotPermitted  ReasonCode = "capability_not_permitted"
    ReasonSandboxRequired         ReasonCode = "sandbox_required"
    ReasonScheduleNotPermitted    ReasonCode = "schedule_not_permitted"
    ReasonPolicyUnavailable       ReasonCode = "policy_unavailable"
)

// PolicyArtifact, Clause, ControlKind, EffectivePolicy, OverrideToken,
// Validator, Explainer — see §5 (data model) for full shapes.
```

The `clauses/<kind>` packages each implement:

```go
type ControlKind interface {
    Kind() string
    ParamSchema() jsonschema.Schema
    FailurePostureDefault() FailurePosture
    LowerToRego(clause Clause) (string, error)        // YAML → Rego module
    NarrowingMerge(parent, child Clause) (Clause, error) // for §4 layer step
}
```

---

## 4. Internal layering (the lifecycle of a policy)

```
[1] Bundle resolver fetches a bundle declaring a kind=policy artifact.
[2] core/policy/bundle handler activates: signature verified by
    a2a-signed-cards-trust against the operator's trust anchors.
    Unsigned/wrong-signer → reject (FR-003).
[3] Per-clause schema validation via the matching clauses/<kind>
    package; unknown kind → reject (FR-017, default-deny).
[4] core/policy/lower compiles each YAML clause to a Rego module.
[5] core/policy/layer takes the set of all activated PolicyArtifacts
    (org / team / personal), runs the strict-narrowing validator
    (FR-001, NFR-004, C-006), and emits an EffectivePolicy.
    Validator findings are loud, typed, and emitted as
    policy_validator_finding events.
[6] core/policy/engine loads the merged Rego module set into a single
    OPA instance, prepared for evaluation.
[7] core/policy/cache stores the EffectivePolicy snapshot keyed by the
    composite content hash of contributing artifacts; the previous
    snapshot is retained briefly for in-flight evaluations to drain
    (NFR-007 hot-reload without restart).
[8] At each consumer action boundary, Engine.Evaluate(ctx, action) is
    called. The hot path:
       a) materialize Action.Inputs() into the OPA evaluation input
       b) run the prepared evaluator (zero allocation where feasible)
       c) decode the decision into a typed Decision
       d) emit policy_evaluation_allowed | policy_evaluation_denied
    Target: <1ms p99 (NFR-001, SC-005). The engine pre-prepares
    evaluators per Kind to avoid Rego compilation on the hot path.
[9] If a control's input source is unavailable (e.g., daily cost
    aggregator hasn't loaded), the engine consults the clause's
    FailurePosture: default fail_closed → deny + emit
    policy_unavailable_fail_closed; explicit fail_open → allow +
    emit policy_unavailable_fail_open (loud, alertable).
```

**Strict narrowing (the validator's contract — NFR-004 = 100 %)**:

For every clause in a child-layer artifact, the validator computes whether
the child's permitted set is a (non-strict) subset of the merged parent's
permitted set under the kind's `NarrowingMerge` semantics:

- **Set-based kinds** (provider, model, MCP server, MCP capability, A2A
  peer): child set ⊆ parent set; otherwise reject with typed
  `team_would_broaden_org` / `personal_would_broaden_team` finding.
- **Tier-based kinds** (network_tier, redaction_strictness): child tier
  must be ≥ stricter than parent.
- **Numeric ceilings** (cost_ceiling): child value ≤ parent value
  (smaller is stricter).
- **Boolean stricter-wins** (signature_required, sandbox_required): child
  may turn ON if parent is OFF, may not turn OFF if parent is ON.
- **Silence semantics** (spec acceptance scenario 1.3): if the parent is
  silent on a control and the child declares it, the child applies. The
  validator's "broadening" check is only meaningful when the parent has
  expressed a constraint.

The validator is a pure function on `(parent EffectivePolicy, child
PolicyArtifact) → []Finding`. Property-based and adversarial-fuzz tests
(see §8 risk register) are the design's airtight argument.

---

## 5. Data model (recap from data-model.md)

The data-model file is the source of truth; this plan reuses those
entities verbatim. Recap for navigation:

- `PolicyArtifact` — versioned, signed bundle artifact of kind `policy`.
  Carries layer, clauses, optional `not_before` / `not_after`,
  `compiled_rego` (filled by the lowering step), `content_hash`.
- `Clause` — one parameterized control instance: `(clause_id, kind, params,
  failure_posture)`.
- `ControlKind` — registered handler. Per-kind package implements
  `validate`, `lower_to_rego`, `narrowing_merge`, `failure_posture_default`.
- `EffectivePolicy` — merged + validated snapshot; carries provenance
  (`source_layer`, `source_policy_id`) per clause + validator findings.
- `Decision` — `(outcome, reason_code, policy_id, clause_id,
  input_summary, evaluated_at, decision_id)`.
- `OverrideToken` — short-lived, audited, cannot loosen org/team layers.
  v1.x surface (FR-014 is medium priority).
- `PolicyEvent` (kinds, all append-only via event-log):
  - `policy_loaded`
  - `policy_layer_transitioned`
  - `policy_evaluation_allowed`
  - `policy_evaluation_denied`
  - `policy_validator_finding`
  - `policy_unavailable_fail_closed`
  - `policy_unavailable_fail_open`
  - `policy_override_used`

---

## 6. Integration points

| Upstream / consumer | Surface | Direction |
|---|---|---|
| `bundle-format-resolver-01KQ1A3J` | `core/policy/bundle/` registers `policy` as a kind handler. The resolver dispatches policy artifacts to the engine for ingestion. | Consumes |
| `a2a-signed-cards-trust-01KQ18P9` | Policy artifacts are verified through the same trust-anchor surface as agent cards. Wrong-signer / unsigned → reject before Rego sees them. | Consumes |
| `event-log-01KQ1A3M` | Every PolicyEvent is appended through the standard event-log API. Hash-chain, redaction, retention are inherited; the engine does not implement its own audit log. | Consumes |
| `shared-context-distribution-01KQ18PA` | Org-level policy bundles travel through the same layered distribution; ingestion is identical to context packs at the transport level. | Consumes |
| `secrets-keychain-01KQ1A3M` | Credential references in policy clauses (e.g., signing-anchor key id) resolve via the keychain; no inline secrets. | Consumes |
| `llm-connector-01KQ1770` | Calls Engine.Evaluate at provider activation, model selection, and per-call cost-ceiling boundary. Cost data sourced from connector FR-011 (token / usage). | Consumed by |
| `core/mcp` | Calls Engine.Evaluate at server activation and at capability-call dispatch. Two control kinds: `mcp_server_allowlist`, `mcp_capability_allowlist`. | Consumed by |
| `core/a2a` (acp-orchestration) | Calls Engine.Evaluate at peer dial and at incoming-card acceptance. | Consumed by |
| `core/scheduler` | Calls Engine.Evaluate at cron registration and at task dispatch. | Consumed by |
| `core/bundle` resolver itself | Calls Engine.Evaluate when fetching a bundle (allowed source check) — note this is a re-entry: only after the engine has bootstrapped from the operator's trust-anchor configuration. | Consumed by |
| `core/context` resolver | Calls Engine.Evaluate to enforce required pack tiers (`redaction_strictness`). | Consumed by |
| `core/workflow` | Calls Engine.Evaluate at node execution to enforce `sandbox_required`. | Consumed by |

**Bootstrap order at startup**:

1. Load operator config (trust anchors, redaction salt, currency, etc.)
2. Load event-log + storage foundations
3. Initialize bundle resolver
4. Resolve and verify policy artifacts (engine bootstrap)
5. Load other consumer subsystems — they query the engine before acting
6. Pre-flight pass (FR-011): every loaded provider, MCP server, peer,
   pack, schedule is dry-run-evaluated against the active policy;
   violations surface as startup errors before any session runs.

---

## 7. Phasing

### v1.0 (this mission)
- Embedded OPA + Rego evaluator with prepared per-action evaluators
- YAML clause schema and lowering for the v1 control kinds
- All v1 control kinds in their own packages: provider_allowlist,
  model_allowlist, mcp_server_allowlist, mcp_capability_allowlist,
  a2a_peer_allowlist, network_tier, signature_required,
  redaction_strictness, cost_ceiling, sandbox_required,
  scheduler_permission
- `core/policy/bundle/` artifact-kind handler registered with
  bundle-format-resolver
- Signature verification through a2a-signed-cards-trust at ingestion
- `core/policy/layer/` org → team → personal merge with the strict-
  narrowing validator (NFR-004 = 100 %)
- Pre-flight policy check at startup (FR-011)
- Decision events emitted for every evaluation (FR-009, NFR-003 = 100 %)
- `harness policy explain <action>` minimum-viable surface (FR-015)
- Time-bound `not_before` / `not_after` validity windows with skew
  tolerance (FR-016)
- Hot-reload without restart, propagation under 60 s (NFR-007)
- Default fail-closed posture; explicit per-clause fail-open opt-in
- Default-deny on unknown control kinds (FR-017)
- p99 evaluation under 1 ms (NFR-001)

### v1.x (follow-ups, scoped here so v1 leaves room)
- CEL field-level matchers inside OPA bundles (research D4) — only if
  v1 ergonomics demand it; defer until at least one design partner asks
- OverrideToken surface (FR-014) — short-lived, audited, constrained
- Policy diffing UI and command (FR-018)
- Decision-log external mirror sinks (file / syslog / OTLP) — research's
  v1 default for the file shape, others optional (research D6 follow-up)
- Validator wildcard-warning ergonomics (spec edge case)

### v2 (defer, but design v1 not to block)
- Cedar backend reassessment (research D2; Cedar v1.6.0 still missing
  templates / partial-eval / formatter as of research date)
- OPA WASM bundles for externally-authored policies
  (`golang-opa-wasm`) — relevant if operators author policies in
  third-party tools that emit WASM

---

## 8. Risk register

| Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|
| **R1**: Rego learning-curve user revolt — operators reject the engine because authoring is alien. | Medium | High | YAML clause wrapper (research D3). Operators see structured YAML, never Rego, unless they want to. Follow-up authoring UI mission targets the long tail. |
| **R2**: Layer-narrowing soundness bug — a child layer broadens its parent and the validator misses it. SOC 2 finding territory. | Low | Critical | Per-kind `NarrowingMerge` is a pure function with property-based tests. An adversarial-fuzz suite exercises every kind with random parent/child pairs. Each finding is logged as `policy_validator_finding`. NFR-004 mandates 100 % rejection — this is the test surface that must be airtight. |
| **R3**: Decision-event volume overwhelms event-log retention budget. | Medium | Medium | Decision events go through the event-log redaction + retention pipeline. Coarse sampling is *forbidden* (audit completeness, NFR-003 = 100 %). The mitigation is event-log retention configuration, not engine sampling. |
| **R4**: cel-go binary footprint balloons the harness if D4 is taken. | Low | Medium | Defer D4 until needed; if taken, apply `cel.ClearMacros()` and dead-code-elim per research E10. Re-measure cold-startup against the 2 s charter target. |
| **R5**: Override-token bypass attempts (operator tries to use overrides to loosen org/team policy). | Medium | High | Override tokens are a deliberately constrained surface: they can only further-narrow or, for a clause explicitly opt-in to operator override, locally accept. The token issuer rejects any override that would loosen org/team. Every issuance and use is a `policy_override_used` event. |
| **R6**: Hot-reload causes in-flight evaluations to use mixed policies. | Medium | Medium | Snapshot semantics: each evaluation pins the EffectivePolicy snapshot at call entry. Reloads atomically swap the active snapshot; the previous snapshot survives until its last in-flight evaluation drains. |
| **R7**: OPA decision-log JSON shape leaks redaction-sensitive inputs into the event log. | Medium | High | Engine adapter emits Decisions through the event-log redaction pipeline (per research caveat). The OPA decision-log emitter is *not* connected directly to disk. |
| **R8**: A consumer forgets to call Engine.Evaluate before acting — silent enforcement gap. | Medium | High | Pre-flight check at startup (FR-011) plus a CI lint that flags any consumer-side action edge that does not have an Evaluate call. NFR-006 + SC-002 set the 100 % bar; we enforce with both runtime and review-time gates. |
| **R9**: Policy artifact rescinded but consumers cached EffectivePolicy that still permits a removed control. | Low | High | Reload on artifact-removal event; the cache is keyed by the contributing artifact-set content hash, so removal forces a recompose. If no prior policy exists, fall to fail-closed (spec edge case "policy is removed"). |
| **R10**: Time-skew weaponization — operator clock drift makes a future-dated policy active early or expired late. | Low | Medium | Configurable skew tolerance with a strict default (spec edge case). Beyond tolerance, the future-dated policy is "not yet active" and the prior policy continues to apply. |

---

## 9. Open questions for the user

The spec carries three [NEEDS CLARIFICATION] markers; research and this
plan propose answers, but final operator confirmation is requested before
WP-time:

1. **Policy expression language** (spec OQ1).
   **Resolved by research D3**: declarative YAML clauses (one per
   control kind) compile to Rego internally. Raw Rego is an escape hatch
   for advanced authors.
   **Question for user**: confirm we expose raw Rego as a v1 escape hatch
   (low cost, big optionality), or strictly hide Rego behind YAML in v1
   and revisit (cleanest mental model for operators)?

2. **Cost-ceiling currency model** (spec OQ2).
   **Proposal**: per-policy currency declaration; mixing currencies in a
   layered effective policy is rejected at validation time as a typed
   `currency_mismatch` finding. Operator may declare a single harness-
   level currency in config; conversion is *not* the engine's job (it
   would couple us to a price feed).
   **Question for user**: confirm — or do you want a single harness-
   wide currency with conversion handled inside the engine?

3. **Network-tier enforcement scope** (spec OQ3).
   **Proposal**: enforce on **both** ingress and egress.
   - Egress: harness-initiated outbound calls to providers, MCP servers,
     A2A peers, bundle sources.
   - Ingress: harness-exposed endpoints (A2A server endpoints, MCP
     server endpoints) — the network tier on the listener side
     determines whether the harness binds to loopback / LAN / WAN.
   Enterprise pre-configured posture wants both: egress prevents data
   exfil; ingress controls inbound blast radius.
   **Question for user**: confirm both, or do you want to scope v1 to
   egress and add ingress as v1.x?

---

## Appendix A — Mapping spec requirements to plan sections

| Spec ID | Where addressed in this plan |
|---|---|
| FR-001 (layered composition, strict narrowing) | §2 (`core/policy/layer/`), §4 step 5, §7 v1.0 |
| FR-002 (policy as bundle artifact kind) | §2 (`core/policy/bundle/`), §6 (bundle resolver row) |
| FR-003 (signature required by default) | §4 step 2, §6 (a2a-signed-cards-trust row) |
| FR-004 (control catalog v1) | §2 (clauses/* listing), §7 v1.0 |
| FR-005 (control-kind extensibility) | §2 boundary rules, §3 ControlKind interface |
| FR-006 (single Evaluate API) | §3 PolicyEngine, §4 step 8 |
| FR-007 (fail-closed default) | §4 step 9, §1 default posture |
| FR-008 (denial taxonomy) | §3 ReasonCode constants |
| FR-009 (decision events) | §5 PolicyEvent kinds, §6 event-log row |
| FR-010 (validator at build + load) | §4 step 5, §2 (`core/policy/layer/validator/`) |
| FR-011 (pre-flight) | §6 bootstrap order step 6 |
| FR-012 (cost ceiling) | §2 (clauses/cost_ceiling/), §6 llm-connector row |
| FR-013 (network tier) | §2 (clauses/network_tier/), §9 OQ3 |
| FR-014 (override surface) | §7 v1.x, §8 R5 |
| FR-015 (explain) | §3 Explainer, §2 (`core/policy/explain/`) |
| FR-016 (validity windows) | §7 v1.0, §8 R10 |
| FR-017 (default-deny on unknown kinds) | §1 default posture, §4 step 3 |
| FR-018 (policy diffing) | §7 v1.x |
| NFR-001 (sub-1ms p99) | §4 step 8 hot-path notes, §7 v1.0 |
| NFR-002 (determinism) | §4 step 6 (single OPA instance, no remote calls in eval), C-004 |
| NFR-003 (audit completeness) | §6 event-log row, §8 R3 |
| NFR-004 (narrowing soundness 100 %) | §4 strict-narrowing validator contract, §8 R2 |
| NFR-005 (fail-closed completeness) | §4 step 9, §1 default posture |
| NFR-006 (control-catalog parity) | §6 consumer rows, §8 R8 |
| NFR-007 (hot-reload under 60 s) | §4 step 7, §8 R6 |
| C-001 (architectural integrity) | §2 boundary rules |
| C-002 (policies distribute as bundles) | §2 bundle/, §6 bundle-resolver row |
| C-003 (append-only event log) | §6 event-log row |
| C-004 (local-first) | §1 engine pick (in-process, no sidecar) |
| C-005 (SOC 2 readiness) | §6 event-log row, §1 audit |
| C-006 (strict narrowing) | §4 strict-narrowing validator |
| C-007 (OSS / enterprise compatibility) | §3 ControlKind contract — same shape for OSS and enterprise kinds |
