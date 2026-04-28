# Data Model (Discovery Draft) — Policy Engine

## Entities

### Entity: PolicyArtifact
- **Description**: a versioned, signed bundle artifact of kind `policy` declaring a layer (`org`, `team`, `personal`), a control set, and metadata. Distributed through the existing bundle resolver.
- **Attributes**:
  - `policy_id` (string, ULID) — globally unique.
  - `name` (string) — human-readable.
  - `version` (semver) — operator-facing version.
  - `layer` (enum: `org`, `team`, `personal`)
  - `clauses` (list of Clause) — declarative, YAML-authored.
  - `compiled_rego` (blob, optional) — internal lowering for OPA evaluation.
  - `signature_envelope` (signed by `a2a-signed-cards-trust`)
  - `not_before` / `not_after` (timestamps, optional) — validity window.
  - `content_hash` (string) — SHA-256 over the canonical clause set.
- **Identifiers**: `policy_id`, plus `(layer, name, version)` tuple.
- **Lifecycle**: authored externally (or via UI follow-up), packaged as a bundle artifact, signed, distributed, fetched, verified, loaded into the engine. Updates supersede prior versions.

### Entity: Clause
- **Description**: one parameterized control instance within a PolicyArtifact.
- **Attributes**:
  - `clause_id` (string) — stable id within the artifact.
  - `kind` (string) — registered control kind (`provider_allowlist`, `cost_ceiling`, `network_tier`, `mcp_server_allowlist`, `mcp_capability_allowlist`, `a2a_peer_allowlist`, `signature_required`, `redaction_strictness`, `sandbox_required`, `scheduler_permission`, …).
  - `params` (kind-specific structure)
  - `failure_posture` (enum: `fail_closed`, `fail_open`) — default `fail_closed`.
- **Identifiers**: `(policy_id, clause_id)`.
- **Lifecycle**: immutable within an artifact version. Schema validated by the control-kind handler at load.

### Entity: ControlKind (handler)
- **Description**: registered handler for a control kind. Implements parsing, validation, lowering-to-Rego, and consumer-facing evaluate hooks.
- **Attributes**:
  - `kind` (string)
  - `param_schema` (JSON Schema)
  - `failure_posture_default` (enum)
  - `consumer_hooks` (list) — which consumer hooks fire for this kind.
- **Lifecycle**: registered at startup. Lives in its own package per DIRECTIVE_001.

### Entity: EffectivePolicy
- **Description**: merged result of org + team + personal layers, computed at load and cached. Carries provenance per clause.
- **Attributes**:
  - `effective_id` (ULID) — identifies this resolved snapshot.
  - `clauses` (list of Clause + `source_layer` + `source_policy_id`)
  - `validator_findings` (list) — broadening violations, schema mismatches, wildcard warnings, unreachable clauses.
  - `computed_at` (timestamp)
- **Lifecycle**: recomputed when any contributing PolicyArtifact changes. Stale results invalidated; consumers always evaluate against the current EffectivePolicy.

### Entity: Decision
- **Description**: the result of one `policy.Evaluate(ctx, action)` call.
- **Attributes**:
  - `decision_id` (ULID)
  - `outcome` (enum: `allow`, `deny`)
  - `reason_code` (enum) — one of: `not_in_allowlist`, `exceeds_ceiling`, `missing_signature`, `wrong_signer`, `network_tier_not_permitted`, `capability_not_permitted`, `sandbox_required`, `schedule_not_permitted`, `policy_unavailable`.
  - `policy_id` (string) — which artifact applied.
  - `clause_id` (string) — which clause matched.
  - `inputs_summary` (redacted)
  - `evaluated_at` (timestamp)
- **Lifecycle**: emitted per evaluation; persisted as a PolicyEvent.

### Entity: OverrideToken
- **Description**: a one-shot operator override that further narrows (or, for explicitly opt-in operator-controlled clauses, locally accepts) a denial. Cannot loosen org or team layers.
- **Attributes**:
  - `token_id` (ULID)
  - `decision_id` (string) — the denial being overridden.
  - `operator_id` (string)
  - `justification` (string)
  - `expires_at` (timestamp) — short-lived.
- **Lifecycle**: created via constrained surface; consumed once; emits PolicyEvent of kind `policy_override_used`.

### Entity: PolicyEvent
- **Description**: append-only event log entry. Lands in the shared event log per `event-log-01KQ1A3M`.
- **Kinds**: `policy_loaded`, `policy_layer_transitioned`, `policy_evaluation_allowed`, `policy_evaluation_denied`, `policy_validator_finding`, `policy_unavailable_fail_closed`, `policy_unavailable_fail_open`, `policy_override_used`.

## Relationships

| Source | Relation | Target | Cardinality | Notes |
|---|---|---|---|---|
| PolicyArtifact | declares | Clause | 1:N | Authored together. |
| Clause | dispatched to | ControlKind | N:1 | Routed by `kind`. |
| PolicyArtifact | layered into | EffectivePolicy | N:1 | Org + team + personal merge. |
| EffectivePolicy | consulted by | Consumer (LLM connector, MCP, A2A, scheduler, etc.) | 1:N | Single shared cache. |
| Consumer call | produces | Decision | 1:1 | One decision per `Evaluate` call. |
| Decision | emits | PolicyEvent | 1:1 | Always logged. |
| Operator | issues | OverrideToken | 1:N | Constrained, audited. |
| Bundle resolver | activates | PolicyArtifact | 1:N | Through standard bundle distribution + signing. |
| `a2a-signed-cards-trust` | verifies | PolicyArtifact signature | 1:N | Required by default per spec FR-003. |

## Validation & Governance

- **Data quality**:
  - PolicyArtifacts MUST be signed and verified before activation; unknown / wrong-signer artifacts are rejected.
  - Team and personal layer artifacts MUST NOT broaden their parent layer; the validator rejects broadening at load.
  - Unknown control kinds MUST fail load — never silent no-op.
  - Time-bound `not_before` / `not_after` honored with skew tolerance per spec FR-016.
- **Compliance**:
  - 100 % of evaluations emit PolicyEvents with policy id, clause id, inputs summary, outcome, reason.
  - Decision-log immutability + hash-chain + retention all delegated to `event-log-01KQ1A3M`.
  - Default fail-closed; fail-open is per-clause opt-in and *louder* in the event log.
- **Source of truth**:
  - Active PolicyArtifacts (org, team, personal) are authoritative for which controls apply.
  - EffectivePolicy is the cached snapshot consumers evaluate against.
  - The event log is authoritative for decision history.
