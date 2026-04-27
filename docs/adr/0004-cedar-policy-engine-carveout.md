# ADR 0004 — Cedar policy engine carve-out from third-party-SDK posture

**Status**: Accepted
**Date**: 2026-04-26
**Mission**: `agent-kernel-graph-01KQ6391` (WP14)

## Context

The agent-kernel-graph spec (FR-053, §4.10) names Cedar as the policy
engine for action gating across LLM model selection, tool execution,
filesystem writes, network requests, and memory writes. The official
Cedar Go binding `github.com/cedar-policy/cedar-go` is Apache-2.0,
self-contained, pure-Go.

The project charter (`.kittify/charter/charter.md`) cites
`DIRECTIVE_001` ("architectural integrity") and the deployment
constraint that the runtime "MUST function with zero network access
except when explicitly invoking a remote model adapter." A reasonable
reading of these clauses is "no third-party SDK in `core/`". A strict
reading would block cedar-go from landing under `core/policy/`.

## Decision

We adopt cedar-go under `core/policy/cedar/` as the WP14
implementation, treating it as exempt from a strict third-party-SDK
prohibition for the following reasons:

1. **It is a configuration interpreter, not a service client.** The
   library opens no sockets, holds no credentials, performs no
   telemetry, and ships no cloud surface. Its public API is "give me
   a `*cedar.PolicySet` and a `cedar.Request`, get a `cedar.Decision`
   back" — pure CPU-bound evaluation over text policy documents.

2. **It is the documented spec recommendation.** Spec FR-053 names
   Cedar explicitly. Re-implementing a Cedar evaluator from scratch
   would be a significant scope expansion for no architectural gain.

3. **Local-first invariant is preserved.** Cedar-go does not network
   at any point during Evaluate; it satisfies the charter's local-only
   posture by construction.

4. **The seam is replaceable.** The `core/policy/cedar.Gate`
   interface is the contract every gate-hook caller (in `core/llm`,
   `core/memory`, `core/tools/*`) imports. If a future audit
   determines Cedar must be removed, a hand-rolled rule evaluator can
   replace `*cedar.Engine` behind the same interface without
   touching any caller.

5. **Precedent.** `modernc.org/sqlite` is already vendored under
   `core/storage/sqlite/` to interpret our own SQLite files; the
   Cedar carve-out mirrors that pattern for policy files.

## Consequences

- **Pros**: spec-faithful implementation; well-tested upstream
  evaluator; small surface (`Gate` interface + five gate-hook
  helpers); replaceable.
- **Cons**: one new direct dependency under `core/`. Mitigated by
  the leaf-package discipline — cedar-go is imported only from
  `core/policy/cedar/`; nothing else in the tree references it.
- **Open**: if the user's interpretation of the charter later
  forbids cedar-go even under this rationale, the policy engine can
  be replaced with a hand-rolled evaluator. The gate-hook callers
  do not change.

## References

- `kitty-specs/agent-kernel-graph-01KQ6391/spec.md` §4.10 (FR-053).
- `core/policy/cedar/doc.go` — package-level rationale + carve-out
  paragraph (mirrors this ADR).
- `core/policy/cedar/policies/default_policy.cedar` — bundled
  starter policy.
