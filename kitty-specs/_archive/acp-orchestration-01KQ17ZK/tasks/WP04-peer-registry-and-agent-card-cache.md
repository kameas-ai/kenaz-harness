---
work_package_id: "WP04"
title: "Peer registry, AgentCard fetch, and cache"
dependencies:
  - "WP01"
  - "WP02"
  - "WP03"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
  - "T006"
phase: "Phase 4 - Peer registry + AgentCard cache"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP04 – Peer registry, AgentCard fetch, and cache

## Goal

Implement `core/acp/peers/registry.go` — an in-memory `peer_id →
PeerProfile` map populated from WP03 activation — and the AgentCard
cache that fetches peer cards on first contact, caches them by
`peer_id` with a configurable TTL (default 300 s), invalidates on
version mismatch, and emits `card_fetched` / `card_cache_hit` /
`card_cache_miss` events. Provide `PreflightAll` that resolves every
peer's `auth_ref` at bundle load.

## Spec references

- FR-004 — Peer Profile bundle artifact (registry materializes the
  parsed peers).
- FR-008 — Agent Card discovery and caching.
- FR-018 — Pre-flight peer resolution.
- FR-020 — Verification API consumer slot (cache calls `Verifier`
  before serving a fetched card).
- US1 Acceptance Scenario 2 — same peer invoked within TTL,
  `card_cache_hit`, no re-fetch.
- US1 Acceptance Scenario 3 — credential reference missing,
  actionable startup error.
- Edge case: card version mismatch → `card_cache_miss` and refetch.
- NFR-007 — Pre-flight resolution success rate.

## Plan references

- §4 Internal Layering — `PeerRegistry`, `AgentCardCache`,
  `PreflightAll`.
- §5.3 Event-log kinds — `card_fetched`, `card_cache_hit`,
  `card_cache_miss`, `peer_auth_attempted`, `peer_auth_failed`.
- §6.1 secrets-keychain integration — `core/secrets.PreflightAll`
  consumed here.
- §9 Q3 — default TTL 300 s.

## Subtasks

- T001 — Implement `PeerRegistry` interface (WP01) backed by a
  thread-safe map: `Load(profiles)` populates from WP03 activation
  output; `Lookup`, `All`, and `PreflightAll` round it out.
- T002 — Implement `AgentCardCache` keyed by `peer_id` with TTL
  expiry, version-mismatch invalidation, and `Get(ctx, peer)`
  semantics: cache hit → emit `card_cache_hit`, return cached card;
  miss → call envelope `FetchCard`, run `Verifier.Verify`, emit
  `card_fetched`, store, return.
- T003 — Implement `PreflightAll(ctx)`: iterate every peer, call
  `secrets.Backend.Resolve(profile.AuthRef)` if non-nil, emit
  `peer_auth_attempted` and on failure `peer_auth_failed` keyed by
  `peer_id`. Aggregate results into `[]PreflightResult`. Failures do
  NOT mutate registry; the resolver decides whether to block bundle
  activation.
- T004 — Plug the WP01 `Verifier` interface into the cache; v1 ships
  the `verify/UnsignedAcceptVerifier` default in `core/acp/verify/`.
  Cache calls `Verifier.Verify` on every freshly-fetched card.
- T005 — Define cache configuration knobs: default TTL = 300 s,
  per-peer override via `PeerProfile.CardCacheTTL`, max cache size
  guard.
- T006 — Tests: a fake envelope returns versioned cards; assert
  cache-hit on second call within TTL with `card_cache_hit` emitted;
  assert version bump triggers invalidation, `card_cache_miss`, and
  re-fetch; assert `PreflightAll` against a fake secrets backend
  emits `peer_auth_attempted` per success and `peer_auth_failed` per
  failure with the correct `peer_id`.

## Acceptance criteria

- `go test ./core/acp/peers/...` passes; coverage ≥ 80%.
- US1 Acceptance Scenario 2 reproduced as a black-box test: two
  consecutive `Get(peer)` calls within TTL produce one `card_fetched`
  event and one `card_cache_hit` event.
- A version-bumped card triggers `card_cache_miss` and a refetched
  card (edge case in spec §Edge Cases).
- `PreflightAll` reports 100% of resolvable references as success and
  100% of unresolvable references as failure with a stable `peer_id`
  field (NFR-007; SC criteria).

## Files to create / modify

- `core/acp/peers/registry.go`
- `core/acp/peers/cache.go`
- `core/acp/peers/preflight.go`
- `core/acp/peers/registry_test.go`
- `core/acp/peers/cache_test.go`
- `core/acp/verify/unsigned.go` — default `UnsignedAcceptVerifier`.
- `core/acp/verify/verify.go` — interface (consumed seam for the
  signed-cards-trust mission).

## Definition of done

- All subtasks complete; tests green; lint clean.
- Cross-mission dependency on `secrets-keychain` PreflightAll surface
  documented in PR.
- Verifier seam (FR-020) compiles unchanged when an alternative
  `Verifier` is bound (asserted by a build-tag test).
- PR merged.
