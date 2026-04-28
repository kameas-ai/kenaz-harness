# Research Decision Log — Policy Engine

## Summary

- **Feature**: `policy-engine-01KQ1A3N` — embedded policy-as-code engine, layered org → team → personal narrowing, SOC 2-friendly audit and decision-log story.
- **Date**: 2026-04-25
- **Researchers**: alecfeeman, Claude (assisting), background research subagent
- **Open Questions** (after research):
  - Do we adopt OPA's bundle service protocol verbatim or define our own bundle subset that lives inside our existing bundle artifact format?
  - Decision-log sink shape: local hash-chained store mirrored to operator-controlled external sink — what does "external sink" look like for v1 (file? syslog? OTLP?)?
  - Is auditor recognition of OPA worth the Rego learning curve for early enterprise pilots, or do we ship a YAML clause shape on top of a CEL expression layer for ergonomics? (Default: OPA is the engine; YAML clauses are a high-level wrapper that compile to Rego internally.)

---

## Landscape snapshot (April 2026)

### Engines

| Engine | Language | Embedding | 2026 status | Verdict |
|---|---|---|---|---|
| **OPA + Rego** | Datalog-style | First-class Go embed via `opa/v1/rego` (in-process, lowest overhead) and `opa/v1/sdk` (with management hooks); WASM also supported | CNCF graduated; active 2026 releases; bundle service protocol + signed bundles built in | **Recommended.** Auditor-recognizable; layered policy native; bundle distribution + signing built in. |
| Cedar (AWS) | Cedar (purpose-built) | `cedar-policy/cedar-go` v1.6.0 (Mar 2026); pure Go (no CGo) | **Missing policy templates, partial evaluation, formatter, full schema validator.** Excellent ergonomics; 28–80× faster than Rego on ABAC microbenchmarks. | Re-evaluate in 12 months. Auditor recognition AWS-shop-skewed. |
| CEL (Google) | Expression language | `google/cel-go`; ns-µs evaluation; embedding bloats Go binaries (mitigated via `cel.ClearMacros()`) | Designed for embedding; what Kubernetes ValidatingAdmissionPolicy uses | Use **inside** an OPA bundle for fast field-level matchers. Not a full-policy engine — no compose / inherit / decision logs. |
| Topaz / Aserto | OPA + Zanzibar wrapper | Microservice (sidecar) | Wrong shape for desktop. | Skip. |
| Bespoke YAML + matchers (Tailscale, Vault, IAM) | n/a | n/a | Reinvention; no auditor credit; every adopter has shipped layering bugs. | Skip as primary, but legitimate as a clause-level UX layer over Rego. |

### SOC 2 expectations

- **Trust Services Criteria are engine-agnostic.** CC6.1–CC6.3 describe controls; auditors accept any documented engine with audit logs through the audit period.
- **Decision-log requirements (effectively):** append-only / WORM, cryptographically sealed (hash-chain or signed entries), trustworthy timestamps, retention ≥ 1 year (longer for finance/HIPAA).
- **Engine itself does not need SOC 2 attestation.** The harness using it does.
- A named open-source engine (OPA) shortens auditor conversations even though it's not strictly required.

### Layered-policy prior art

- **AWS SCPs** (closest analog to org → team → personal narrowing): permission requires explicit Allow at every level; deny anywhere wins. **Lesson**: surprises everyone the first time; tooling for "why is this denied?" matters more than the policy language itself.
- **Tailscale ACLs**: single-org HuJSON with deny-by-default. Tailscale itself is migrating to "Grants" because original syntax doesn't compose well across application-layer concerns. **Lesson**: bake compose semantics in from day one — retrofitting is painful.
- **Kubernetes RBAC + ValidatingAdmissionPolicy**: two-tier — RBAC gates *who can call*, admission gates *what is allowed in*. **Lesson**: keep authentication separate from semantic policy.
- **Vault policies**: path-based, longest-prefix wins, **no accumulation** across path tree — opposite of SCPs. **Lesson**: pick one accumulation model and stay there. Our SCP-style strict-narrowing is correct; document it loudly.

### Embedding decisions

- OPA Go library evaluates inline in the harness process — **no sidecar required**. The "OPA needs a sidecar" reflex is about its server mode, not the embeddable `rego` package.
- OPA can compile Rego to **WASM**; `golang-opa-wasm` SDK evaluates in-process — useful if we ever want operators to author policies in tools that emit WASM bundles.
- CEL embedding footprint manageable with `ClearMacros()` and dead-code elimination.
- Cedar embedding ergonomics good; gap is feature completeness, not embeddability.

---

## Decisions & Rationale

| Decision | Rationale | Evidence | Status |
|----------|-----------|----------|--------|
| **D1**: Adopt **OPA + Rego** as the policy engine, embedded in-process via `github.com/open-policy-agent/opa/v1/rego`. | Auditor-recognizable; only engine of the four routinely encountered as a named "logical access control" artifact in SOC 2 / FedRAMP audits. Mature Go embed (no sidecar). Layered policy native via `default` + multi-document compose. Bundle distribution + signing built in. | E1, E2, E3, E4; sources `opa-integration`, `opa-sdk`, `opa-wasm`, `golang-opa-wasm` | final |
| **D2**: Wrap OPA behind a `PolicyEngine` interface so Cedar (or any future engine) can become the v2 backend. | Cedar-go v1.6.0 still lacks templates, partial evaluation, formatter, full schema validator — but the gap is closing. Charter DIRECTIVE_001 (architectural integrity) requires extension without core surgery. | E5, E6, E7 | final |
| **D3**: Author policies as **YAML clause shape** that compiles to Rego internally (one clause per control kind). Operators do not write Rego unless they want to. | Auditors get OPA artifacts (and Rego if they ask); operators get a constrained, reviewable surface that is easy to validate; advanced operators can still drop down to Rego for power moves. Resolves the "Rego learning curve is the engine's biggest knock" finding. | E1, E14 | final |
| **D4**: **Use CEL inside OPA bundles for field-level matchers** where authoring ergonomics matter (e.g., string-pattern checks, numeric ranges). Not a primary engine. | CEL designed for embedding (Kubernetes admission controls), ns-µs evaluation, JSON-friendly. OPA can host CEL via custom builtins if we want; or we lower YAML clauses straight to Rego and skip CEL entirely. | E8, E9, E10 | follow-up (planning-phase) |
| **D5**: Distribute policies as **bundle artifacts of kind `policy`** through the existing bundle resolver and signing machinery. Reuse, do not parallel. | Avoids forking a parallel distribution / signing system. OPA's bundle service protocol is *compatible with* this approach — we can emit Rego bundles in OPA's format inside our own bundle artifact wrapper. | E1, E2 | final |
| **D6**: **Decision log = append-only local hash-chained store + mirror to operator-controlled external sink.** Adopt OPA's decision-log JSON shape; persist into the harness event log; mirror is opt-in per-policy. | SOC 2 expectations: WORM, hash-chain, trustworthy timestamps, ≥ 1 year retention. Local hash-chain comes "for free" from `event-log-01KQ1A3M`; external mirror is the operator's audit-collector integration. | E11, E12 | final |
| **D7**: SOC 2 retention floor: **1 year minimum**, configurable up to operator-required ceiling (e.g., 5–7 years for finance/HIPAA). Default for v1: keep all. | Floor matches industry SOC 2 norm; "keep all" default mirrors `event-log` mission's retention default; explicit retention scheduler is a follow-up. | E12, E13 | final |
| **D8**: **Strict-narrowing semantics** (org → team → personal can only narrow) modeled on AWS SCPs. Document the "why is this denied?" surface up-front (`harness policy explain <action>`). | Every SCP-shop's #1 ticket is "why am I being denied?" — bake the explainer into v1 so we don't replay that mistake. Tailscale's ACL → Grants migration shows the cost of getting compose wrong from day one. | E15, E16, E17 | final |

---

## Evidence Highlights

- **Key insight 1 — OPA's "needs a sidecar" reflex is wrong for our use case.** The Go library evaluates inline; this is the production-blessed embed path. (E1, E2)
- **Key insight 2 — Cedar is close but not there.** v1.6.0 still missing templates + partial eval + formatter — the exact features layered policy benefits from. Re-evaluate in 12 months. (E5, E6)
- **Key insight 3 — SOC 2 doesn't mandate any specific engine, but auditor recognition makes OPA materially easier.** (E11, E12)
- **Key insight 4 — Decision-log immutability + hash-chain + ≥ 1 year retention is the SOC 2 audit-log floor.** All three are already in scope via `event-log-01KQ1A3M`. (E12, E13)
- **Key insight 5 — Tailscale's migration to Grants confirms "bake compose in from day one."** Don't ship a non-composing ACL syntax and migrate later. (E16)
- **Risks / Concerns**:
  - Rego learning curve is real. D3 (YAML wrapper) is the mitigation; we must validate that the wrapper expresses the v1 control catalog without escape-hatching to raw Rego too often.
  - cel-go binary footprint is non-trivial; if D4 takes effect, profile and apply `ClearMacros()`.
  - OPA's decision log is verbose by default; we'll need a redaction layer between OPA's emitter and the event log (consistent with `event-log` redaction pipeline).

---

## Next Actions

1. Resolve `policy-engine` Open Question 1 (engine choice): D1 — OPA + Rego.
2. Plan-phase: design the YAML clause schema (D3) and the lowering to Rego.
3. Plan-phase: choose decision-log mirror sink shape (file / syslog / OTLP) for v1 default.
4. Plan-phase: produce the v1 control catalog as concrete Rego policies + YAML wrappers.
5. Plan-phase: design `harness policy explain` (D8) — minimum viable surface for "why was this denied?"
6. Coordinate with `bundle-format-resolver` mission to register `policy` as a bundle artifact kind.
