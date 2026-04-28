# Spec: Memory narrative layer + compactor feed + long-term feedback

**Status**: draft · **Owner**: alecfeeman

## 1. Why

`core/memory` today is a greedy raw-text store. `HookManager.Fire` writes raw turn/tool content into a chromem-backed store keyed by content hash + scope. The only model touching memory is the `Embedder` for vector retrieval. There is no per-turn summarisation, and the compactor (`core/agentgraph/compaction/`) does not consume memory artifacts.

Reference benchmark: `claude-mem` produces structured per-prompt narratives (request / investigated / learned / completed / next_steps). After compact, those narratives surface verbatim, so the post-compact session inherits pre-compact intent + outcomes — not raw text fragments. We want that experience without claude-mem's fragility.

## 2. Goals

A hybrid architecture that augments (not replaces) the greedy raw-text floor:

1. **Async narrative layer**: after `HookPostLLM` / `HookPostTool` fires (and writes raw), schedule a non-blocking narrative-promotion job. Job calls a configurable summariser model, writes a structured narrative chunk with higher retrieval-weight tag.
2. **Configurable summariser model**: independent of the chat model. Default to a cheap model (Haiku 4.5 or OpenRouter free tier). Per-project configurable.
3. **Compactor feed**: when context approaches budget, the compactor splices narrative chunks verbatim where available; falls back to live summarisation only when no narrative exists.
4. **Long-term feedback loop**: track retrieval frequency + dwell signals. Periodically promote frequently-recalled narratives to a `long_term` scope tier that resists pruning and is preferentially loaded into the system prompt at session start.

## 3. Non-goals

- Replacing the chromem backend.
- Reworking the existing prune sweep (separate `memory-prune-completion` mission).
- Building a new vector DB.

## 4. Functional requirements

| ID | Requirement | Status |
|---|---|---|
| FR-001 | Greedy raw-text writes remain synchronous and offline-safe. Summariser outage does not block chat. | proposed |
| FR-002 | New `core/memory/narrative/` package with a `Promoter` worker pool (parallelism configurable, backpressure, retry-with-backoff). | proposed |
| FR-003 | Promoter reads the per-prompt-cycle window from the hook journal and produces one narrative per agent turn (per-turn-cycle granularity, not per-tool). | proposed |
| FR-004 | Summariser model is provider-agnostic via `core/llm.ProviderProfile`. New `Settings.SummarizerProfileID` dial. | proposed |
| FR-005 | Narrative chunks carry an extra metadata field `kind: "narrative"` and a `retrieval_weight: float` (default 1.5×). | proposed |
| FR-006 | `core/memory/retriever.go` queries both layers; narrative scored higher via the metadata multiplier. | proposed |
| FR-007 | `core/agentgraph/compaction/` consumes narratives at the per-call site first; raw fallback only when no narrative covers the messages being compacted. | proposed |
| FR-008 | Long-term promotion: rolling counter of retrieval frequency per chunk; threshold-based promotion to `scope_kind = "long_term"`. | proposed |
| FR-009 | Long-term chunks loaded into system-prompt prelude at session start (top-N by recency × frequency). | proposed |
| FR-010 | Cedar policy gate (FR-026) applies to narrative writes the same as raw writes. | proposed |
| FR-011 | Promotion signals observable via Memory view (per-chunk frequency + last-recall timestamp). | proposed |

## 5. Non-functional requirements

| ID | Requirement | Threshold | Status |
|---|---|---|---|
| NFR-001 | Summariser failure surface area. | Chat continues uninterrupted; failures land in logs + a settings-panel banner if rate > 10/hour. | proposed |
| NFR-002 | Per-turn narrative cost. | p95 ≤ 1 cent per narrative at default summariser. | proposed |
| NFR-003 | Compactor overhead from narrative lookup. | ≤ 50 ms p95 over baseline. | proposed |

## 6. Constraints

| ID | Constraint | Status |
|---|---|---|
| C-001 | Existing FR-027 greedy-write invariant must hold. | accepted |
| C-002 | Promotion is signal-driven, not heuristic — tunable thresholds in settings. | accepted |
| C-003 | Long-term tier is a new scope_kind value; migration adds the enum and an index. | accepted |

## 7. Success criteria

- Post-compact session retains ≥ 80% of the question-answer fidelity of the pre-compact session on a fixed test conversation.
- Summariser outage (kill the configured profile) leaves chat fully functional; narratives stop being produced; raw layer keeps growing.
- Frequently-referenced narratives surface in the system prompt of a NEW session in the same project within 7 days of promotion.
