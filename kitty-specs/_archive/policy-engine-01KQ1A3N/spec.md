# Feature Specification: Policy Engine — Enterprise Pre-Configured Posture

**Feature Branch**: `feat/policy-engine-01KQ1A3N`
**Created**: 2026-04-25
**Status**: Draft
**Input**: Foundation mission. The policy engine is what turns kaneaz-harness from "configurable agent runtime" into "enterprise-deployable agent runtime." It enforces operator-, team-, and org-declared policies that constrain which providers, MCP servers, bundles, packs, workflows, and external endpoints a harness instance is permitted to use. Without it, "enterprise-ready" is marketing.

## Why this mission exists

Enterprise customers buy "configurability with rails" — they want to give their employees a powerful tool while preventing it from connecting to unapproved providers, leaking data to unsanctioned endpoints, running unreviewed code, or exceeding cost budgets. Several drafted specs (`llm-connector`, `acp-orchestration`, `bundle-format-resolver`, `shared-context-distribution`, `a2a-signed-cards-trust`) reference policy decisions in passing. This mission centralizes the policy primitive so each consuming layer enforces a *common* model rather than reinventing one.

## Dependencies and relationships

- **Depends on**: `bundle-format-resolver` (policies are themselves bundle artifacts — the primary distribution path), `event-log` (every policy decision is auditable), `secrets-keychain` (credential references in policies), `a2a-signed-cards-trust` (verifies policy author identity), `shared-context-distribution` (org-level policies travel through the same layered distribution).
- **Consumed by**: every layer that takes an action a policy can constrain — LLM connector (allowed providers, allowed models, cost ceilings), MCP client/server (allowed servers and capabilities), A2A (allowed peers, allowed transports), workflow engine (allowed nodes, sandbox boundaries), bundle resolver (allowed bundle sources), context resolver (required pack tiers), scheduler (allowed cron expressions, allowed tasks).
- **Does not cover**: the actual UX for writing policies (a follow-up authoring-UI mission); cloud-managed policy distribution (a follow-up enterprise-management mission).

## User Scenarios & Testing *(mandatory)*

### User Story 1 — An org publishes a baseline policy that all employee harnesses enforce (Priority: P1)

The org's security/IT team authors a policy bundle pinning the approved set of LLM providers, the approved set of MCP servers, the approved A2A peers, and operator-level controls (no public WAN exposure, signature required, redaction strict). Employee harnesses follow the org's distribution channel, verify the policy bundle's signature, and enforce the policy from that point forward. An attempt to reach a non-approved provider, install a non-approved MCP server, or expose A2A on a public interface is blocked with a typed denial.

**Why this priority**: This is the deployment story that turns "interesting tool" into "approved tool." Without it, IT cannot say yes.

**Independent Test**: An org policy permits only Anthropic and OpenRouter as providers. An employee bundle declares an OpenAI provider. The harness loads, the policy denies the OpenAI declaration, and the operator sees a clear "blocked by org policy" message at startup, not a runtime mystery.

**Acceptance Scenarios**:

1. **Given** an org policy permits providers A and B, **When** an employee bundle declares provider C, **Then** the harness refuses to activate provider C and surfaces a typed "policy denied: provider not in allowlist" event at config-load time.
2. **Given** the policy bundle is signed under an org trust anchor and the operator's harness trusts that anchor, **When** the policy is fetched, **Then** signature verification gates activation; an unsigned or wrong-signer policy is rejected.
3. **Given** the org updates the policy, **When** the harness's next resolution pass runs, **Then** the new policy supersedes the old, with the transition logged.

---

### User Story 2 — A team layers tighter policy on top of org policy (Priority: P1)

A team within the org publishes a more restrictive policy: their workflows must run only against the org's procurement-team Anthropic account, and may only call MCP servers from the org's approved set *plus* a team-specific server. The team layer can never *broaden* the org policy — only narrow it. Conflicts where the team would broaden the org are denied as policy violations at policy-author time.

**Why this priority**: Real organizations have multiple security postures. Without layering, every team is forced into either the strictest or loosest org-wide setting.

**Independent Test**: An org policy permits providers A, B, C. A team policy adds D. Resolution refuses the team policy with a typed "team layer cannot broaden org" violation, not a silent acceptance.

**Acceptance Scenarios**:

1. **Given** an org policy permits {A, B, C} and a team policy permits {A, B}, **When** layered, **Then** the effective set is {A, B} (intersection — narrowing is allowed).
2. **Given** an org policy permits {A, B} and a team policy attempts {A, B, D}, **When** layered, **Then** the team policy is rejected with a typed "team would broaden org" violation; D is not accepted.
3. **Given** an org policy is silent on a control and a team policy declares it, **When** layered, **Then** the team policy applies (silence is not narrowing).

---

### User Story 3 — Personal policy adds personal restrictions, never reductions of stricter parent layers (Priority: P2)

An individual operator can layer a personal policy: "I personally don't want to use OpenAI even though my team allows it" or "I cap personal cost at $5/day even though the team has no cost cap." Personal-layer policy can only further narrow what's already permitted; it cannot loosen team or org policy.

**Why this priority**: Personal sovereignty matters even within enterprise governance. P2 because most operators will not author personal policy; the v1 default of "no personal layer" works.

**Independent Test**: An operator's personal policy excludes a provider their team allows; the harness denies that provider for that operator while peers in the team continue to use it.

**Acceptance Scenarios**:

1. **Given** team policy permits providers {A, B} and personal policy excludes B, **When** the operator runs an agent, **Then** only A is available to that operator's harness.
2. **Given** team policy denies a provider, **When** an operator's personal policy attempts to permit it, **Then** the personal layer is rejected with a "personal cannot loosen team" violation.

---

### User Story 4 — Every policy decision is auditable and reproducible (Priority: P1)

Every policy evaluation — every "allowed" and every "denied" — is recorded in the append-only event log with: which policy artifact applied, which clause matched, what input was evaluated, what the outcome was. An auditor can answer "at time T, why did this harness allow this call?" or "why did it block this call?"

**Why this priority**: SOC 2 readiness, post-incident forensics, and operator trust. A policy decision you can't reconstruct is a policy decision you can't defend.

**Independent Test**: A session exercises a mix of allowed and denied calls. The event log entries are sufficient to reconstruct each decision offline using the same policy bundle versions recorded.

**Acceptance Scenarios**:

1. **Given** a policy allowed a call, **When** the event log is queried for that call, **Then** the entry identifies the policy artifact id, the clause id, and the inputs evaluated.
2. **Given** a policy denied a call, **When** the event log is queried, **Then** the entry includes the same fields plus the denial reason in the standard taxonomy.

---

### User Story 5 — Policies cover the controls operators actually need (Priority: P1)

The policy engine ships with a catalog of well-defined control kinds covering the high-leverage enterprise concerns: provider allowlist, model allowlist per provider, MCP server allowlist (and capability allowlist within a server), A2A peer allowlist, network exposure (loopback / LAN / WAN tiers), signature requirement on bundles and packs, redaction strictness, cost ceilings (per session, per day), workflow node sandboxing requirements, scheduler permissions (which schedules and tasks are allowed). Adding a new control kind is a clear extension.

**Why this priority**: A policy engine without the right controls is just YAML. The v1 control set must cover the typical IT/security checklist, or the engine is bypassed by operators forced to fork.

**Independent Test**: A representative org-policy bundle exercises every v1 control kind. Each is correctly enforced in the matching consumer.

**Acceptance Scenarios**:

1. **Given** the v1 control catalog, **When** an org authors a policy using each kind, **Then** each control is enforced by the corresponding consumer with the same evaluation surface and the same denial taxonomy.
2. **Given** a policy attempts an unknown control kind, **When** loaded, **Then** the policy is rejected with an actionable "unknown control kind" error rather than silently ignored.

---

### User Story 6 — Policies degrade gracefully when their inputs are unavailable (Priority: P2)

Policy evaluation may need data that is temporarily unavailable (a remote signing service is down, a network policy depends on environment metadata that hasn't loaded yet, a cost-ceiling check needs the daily total which takes time to compute). Each control declares its fail-closed or fail-open posture. Default is fail-closed; explicit opt-out is required.

**Why this priority**: Without explicit failure semantics, a temporarily unhealthy backend silently disables enforcement — exactly the SOC 2 finding to avoid.

**Independent Test**: With a controlled-policy backend offline, controls that depend on it fail closed by default; the operator sees a clear "policy backend unavailable, denying" event.

**Acceptance Scenarios**:

1. **Given** a control's input source is unavailable, **When** evaluated, **Then** the default fail-closed posture denies the action and emits a "policy unavailable" event.
2. **Given** a control is explicitly marked fail-open in the operator's policy, **When** evaluated with unavailable input, **Then** the action is allowed and a "policy degraded, fail-open" event is emitted (loud enough to alert on).

---

### User Story 7 — Policy-violation reports include enough context to fix the violation (Priority: P3)

When a policy denies an action, the operator sees the policy artifact, the specific clause, the specific input value that violated, and a suggested remediation (e.g., "ask your team admin to add provider X to the team allowlist," or "run `harness policy explain`"). Operators can move from "blocked, mysterious" to "blocked, here's why and what to do" in one step.

**Why this priority**: The difference between an enterprise tool people respect and one they resent is whether it explains itself.

**Independent Test**: Run an action that a known policy denies. The error message contains the policy artifact, clause id, violating input, and a remediation hint.

**Acceptance Scenarios**:

1. **Given** any policy denial, **When** the operator inspects the error, **Then** they see policy id, clause id, evaluated input, and a remediation hint.

---

### Edge Cases

- A policy bundle is signed under a trust anchor not yet trusted by the operator: the policy is rejected (not silently absent); operator must explicitly trust the anchor before enforcement applies.
- An org policy and a team policy disagree on a control with no clean intersection (e.g., conflicting cost ceilings): the stricter (smaller, more restrictive) value wins; tie-break is policy-version-tuple ordering, recorded in the event log.
- A policy uses a control kind from a future schema version: rejected at load with "schema version unsupported" — the harness never silently treats unknown clauses as no-ops.
- A policy is removed (org rescinds the bundle): the harness reverts to its prior state from the lockfile; if no prior state exists, the harness's default is fail-closed (deny everything that was previously gated).
- An operator's local time clock skews and a policy's validity window is in the future: a configurable skew tolerance applies; beyond it, the policy is treated as "not yet active" and the prior policy continues to apply.
- A policy includes an excessively broad allowlist (e.g., `*`): allowed but flagged in the policy validator as a low-severity warning ("wildcard allowlist") so operators see it during review.
- Conflict between a per-session override (operator clicks "allow this once") and an org policy that denies: the org policy wins; the override mechanism cannot be used to bypass org policy.

## Requirements *(mandatory)*

### Functional Requirements

| ID | Title | User Story | Priority | Status |
|----|-------|------------|----------|--------|
| FR-001 | Layered policy composition | As an operator, I want org → team → personal policy layering with strict narrowing semantics (later layers can only narrow, never broaden, parent layers). | High | Open |
| FR-002 | Policy as a bundle artifact kind | As an author, I want policies declared as a registered artifact kind in bundles, so they distribute through the existing bundle resolver. | High | Open |
| FR-003 | Required signing for policy artifacts | As an operator, I want policies to require signature verification by default, with the signing trust anchor declared in operator config. | High | Open |
| FR-004 | Control catalog v1 | As an operator, I want v1 to cover: provider allowlist, model allowlist per provider, MCP server + capability allowlist, A2A peer allowlist, network exposure tiers, signature requirements, redaction strictness, cost ceilings (per-session, per-day), workflow sandbox requirements, scheduler permissions. | High | Open |
| FR-005 | Control-kind extensibility | As a contributor, I want a stable control-kind contract so new controls are addable in their own packages without modifying existing ones. | High | Open |
| FR-006 | Policy decision API | As a consumer, I want a single `policy.Evaluate(ctx, action)` API returning `allow` or `deny(reason)` so every consumer enforces uniformly. | High | Open |
| FR-007 | Fail-closed default | As an operator, I want each control to declare a fail-closed or fail-open posture; default is fail-closed. | High | Open |
| FR-008 | Per-action denial taxonomy | As an operator, I want a stable denial-reason taxonomy: not-in-allowlist, exceeds-ceiling, missing-signature, wrong-signer, network-tier-not-permitted, capability-not-permitted, sandbox-required, schedule-not-permitted, policy-unavailable. | High | Open |
| FR-009 | Policy-decision events | As an operator, I want every evaluation recorded in the append-only event log with policy id, clause id, inputs, outcome, and reason code. | High | Open |
| FR-010 | Policy validator | As an author, I want a validator that runs at policy build time (and at load time) to catch broadening violations, schema mismatches, wildcard warnings, and unreachable clauses. | High | Open |
| FR-011 | Pre-flight policy check | As an operator, I want my harness's loaded bundles, MCP servers, providers, peers, and packs validated against the active policy at startup; violations surface as startup errors before any session runs. | High | Open |
| FR-012 | Cost-ceiling enforcement | As an operator, I want per-session and per-day cost ceilings denominated in the operator's currency, enforced at the LLM-call boundary, with usage data sourced from `llm-connector` FR-011. | High | Open |
| FR-013 | Network-exposure tiering | As an operator, I want the policy engine to enforce network tier (`loopback`, `lan`, `wan`) for A2A and MCP server exposures and outbound calls. | High | Open |
| FR-014 | Operator-override surface (constrained) | As an operator, I want a documented surface for one-shot overrides ("allow this once") that *cannot* loosen org or team policy and is itself audited. | Medium | Open |
| FR-015 | Policy explanation surface | As an operator, I want `harness policy explain <action>` to show which clauses matched and why an action was allowed or denied. | Medium | Open |
| FR-016 | Time-bound policy validity windows | As an org admin, I want policies with optional validity windows (`not_before`, `not_after`) so a planned posture change applies at the right moment. | Medium | Open |
| FR-017 | Default-deny posture for unknown control kinds | As an operator, I want unknown control kinds in a loaded policy to fail load — never to silently no-op. | High | Open |
| FR-018 | Policy diffing | As an operator, I want a diff view showing how a new policy version differs from the current, before accepting an update. | Medium | Open |

### Non-Functional Requirements

| ID | Title | Requirement | Category | Priority | Status |
|----|-------|-------------|----------|----------|--------|
| NFR-001 | Evaluation latency | Single `policy.Evaluate` call adds under 1 ms p99 to a consumer call on a developer laptop. | Performance | High | Open |
| NFR-002 | Decision determinism | Identical inputs and identical policy state produce identical decisions across machines, in 100 % of the test matrix. | Reliability | High | Open |
| NFR-003 | Audit completeness | 100 % of policy evaluations emit append-only event-log entries with policy id, clause id, inputs, outcome, reason. | Auditability | High | Open |
| NFR-004 | Layer-narrowing soundness | A team or personal layer that broadens its parent layer is rejected 100 % of the time across the validator's test matrix. | Security | High | Open |
| NFR-005 | Fail-closed completeness | When a control's input source is unavailable and posture is default, the evaluation denies 100 % of the time. | Security | High | Open |
| NFR-006 | Control-catalog parity | Each v1 control kind has at least one consumer that enforces it; each consumer that takes a policy-relevant action queries the policy engine before acting. | Functional Coverage | High | Open |
| NFR-007 | Hot-reload without restart | A policy update applies within 60 s of arriving at the harness, without requiring restart. | Reliability | Medium | Open |

### Constraints

| ID | Title | Constraint | Category | Priority | Status |
|----|-------|------------|----------|----------|--------|
| C-001 | Architectural integrity | Policy engine lives in `core/policy/`. Consumers query through the public API; control-kind handlers live in their own packages. No `core/` package outside `core/policy/` and the handler packages contains policy-evaluation logic. | Technical | High | Open |
| C-002 | Policies distribute as bundles | Policies use the existing bundle distribution and signing machinery; no parallel distribution path. | Technical | High | Open |
| C-003 | Append-only event log | Policy decisions, layer transitions, validator outputs, and override usages are append-only. | Security | High | Open |
| C-004 | Local-first | The policy engine evaluates locally against locally-cached policy artifacts; remote KMS / OPA-style remote evaluation is opt-in only. | Technical | High | Open |
| C-005 | SOC 2 readiness | Decisions, layer changes, validator outputs, override usage, and unavailable-input fallbacks produce audit evidence sufficient for SOC 2 audit per the charter. | Regulatory | High | Open |
| C-006 | Strict narrowing | Team and personal layers cannot broaden parent layers under any circumstance; the validator enforces this. | Security | High | Open |
| C-007 | OSS / enterprise compatibility | Enterprise-only control kinds (e.g., DLP scanners, integrated SIEM exports) implement the same control-kind contract as OSS controls. They never fork the policy engine. | Business | High | Open |

### Key Entities

- **Policy Artifact**: a versioned, signed bundle artifact of kind `policy` declaring a layer (`org`, `team`, `personal`), a control set, and metadata.
- **Layer**: one of `org`, `team`, `personal`. Strict narrowing semantics: each layer can only narrow its parent.
- **Control Kind**: a registered handler for a specific control (e.g., `provider_allowlist`, `cost_ceiling`, `network_tier`). Contracts: `validate(clause)`, `evaluate(inputs) -> decision`, `failure_posture()`.
- **Clause**: one parameterized control instance within a policy artifact (e.g., `kind: provider_allowlist, allow: [anthropic, openrouter]`).
- **Decision**: typed result of an evaluation: `allow` or `deny(reason_code, message, policy_id, clause_id, inputs_summary)`.
- **Effective Policy**: the merged result of org + team + personal layers, computed at load and cached. Carries provenance for every clause.
- **Override Token**: a one-shot, audited operator override that further narrows (or, for explicitly opt-in operator-controlled clauses, locally accepts) a denial. Cannot loosen org or team layers.
- **Policy Event**: append-only log entry kinds: `policy_loaded`, `policy_layer_transitioned`, `policy_evaluation_allowed`, `policy_evaluation_denied`, `policy_validator_finding`, `policy_unavailable_fail_closed`, `policy_unavailable_fail_open`, `policy_override_used`.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An org admin can publish a policy that constrains providers, MCP servers, and network exposure, distribute it through an existing bundle channel, and have employee harnesses enforce it within 30 minutes from a clean clone.
- **SC-002**: 100 % of policy-relevant actions across the consuming layers query the policy engine before acting.
- **SC-003**: 100 % of attempted layer broadenings are rejected by the validator.
- **SC-004**: 100 % of policy evaluations emit append-only event-log entries sufficient to reproduce decisions offline.
- **SC-005**: With a v1 representative policy, evaluation overhead stays under 1 ms p99 in the hot path.
- **SC-006**: Adding a new control kind is end-to-end possible without modifying any other control or any consumer outside the new control's own package and the consumer that newly queries it.
- **SC-007**: A new operator can install the harness, follow an org policy bundle, and reach a working "blocked but explained" experience for any disallowed action — never a silent failure.

## Assumptions

- The bundle resolver, event log, and signed-cards trust missions have landed before this one ships; this mission consumes those surfaces.
- The v1 control catalog is sufficient for first enterprise design partners; new control kinds will be added in follow-up missions as use cases emerge.
- Operator-side policy authoring is text-based (YAML) for v1; a UI-based authoring tool is a follow-up.
- Cost-ceiling enforcement uses token / usage data exposed by `llm-connector` FR-011; pricing tables are operator-configurable.
- The policy engine is purely deterministic given inputs; non-deterministic remote evaluation (e.g., a cloud OPA service) is out of scope for v1 — but the control-kind contract leaves room for it as an optional backend later.

## Open Questions

1. **[NEEDS CLARIFICATION]** Policy expression language — declarative YAML clauses (one clause per control kind, simple structure, easy to validate) versus a richer expression language (Rego / CEL) for complex predicates? Default if unresolved: declarative YAML in v1 with one clause per control kind. Rego/CEL is a follow-up if expressivity demands it; the cost of richer language is harder validation, harder audit, and harder mental model for operators.
2. **[NEEDS CLARIFICATION]** Cost-ceiling currency model — single currency per harness, with conversion done by the policy engine, or per-policy currency declarations? Default if unresolved: per-policy currency declaration; mixing currencies in a layered effective policy is rejected at validation.
3. **[NEEDS CLARIFICATION]** Network-tier enforcement scope — does the policy engine enforce only on harness-initiated egress, or also on harness-exposed ingress (A2A server endpoints, MCP server endpoints)? Default if unresolved: both. Egress is the higher-impact tier (data exfil); ingress is the higher-blast-radius tier (compromise via inbound). Enterprise pre-configured posture wants both.
