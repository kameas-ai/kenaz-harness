# Spec: Memory inspection + observability UI

**Status**: draft · **Owner**: alecfeeman · **Created**: 2026-05-04
**Targets**: v0.7.0 (some pieces cherry-pickable to v0.5.x as patches)
**Related**: `memory-narrative-layer-01KQ8TD1` (produces the structured chunks this UI observes), `unified-search-01KX5R8C` (memory search lives there as one of five searchable entity kinds), `_archive/memory-prune-completion-01KQ8TD8` (the prune sweep this UI exposes status for), `_archive/cross-session-search-01KQ8TDQ` (existing FTS5 over messages — different surface)

## 1. Why

The harness already does claude-mem-style greedy memory capture: every kernel boundary fires a `HookPostLLM` / `HookPostTool` write into `memory_hook_journal` + `memory_chunks`, with embeddings populated when an embedder is available. The retriever pulls from this store on every turn. The compactor splices the highest-weight chunks into context when budget tightens. `memory-narrative-layer` (deferred to v0.6.0) layers structured per-prompt narratives on top.

What's missing is **the user's window into all of this**. Today:

- The Memory view (`MemoryView.vue`, 516 lines) lists chunks with pin/forget/promote-scope buttons and a prune-now control.
- The Hook Journal view (`HookJournalView.vue`, 215 lines) tails the raw capture journal.
- That's it.

What users (and we, when debugging) actually want:

- **"Why did the model say that?"** — see which memory chunks the retriever surfaced for the last user turn, with similarity scores, and which of those the kernel actually injected into the LLM context.
- **"What does the model know about X?"** — search across memory by keyword (covered separately by `unified-search-01KX5R8C` Cmd+K palette) AND by semantic similarity (this spec).
- **"Why is this chunk being recalled so often?"** — drill into a chunk and see its retrieval count, citation count, pin signals, and inspect the queries that returned it.
- **"This summary is wrong, regenerate it"** — re-run the narrative summariser against a specific turn or chunk on demand, replacing the existing summary.
- **"How much memory is the harness actually keeping?"** — a health dashboard showing raw chunk counts, narrative chunk counts, pruned-this-week counts, embedded vs. unembedded ratio, average similarity at retrieve time, etc.
- **"Show me what's about to get pruned"** — preview the next prune sweep's targets before it runs (today's `Memory_PrunePreview` is a number; users want to see the actual rows + reasoning).

These are debugging + trust affordances. claude-mem ships several of them in its UI; we have the data layer to do them better but no surface.

## 2. In scope

### 2.1 Active-session memory inspector (the headline feature)

A panel inside the chat surface (toggleable from the LegendBar like the existing dev panels) that, for the current session, shows:

```
Active session memory               (kernel boundary fires → )

Last turn retrieval:
  Query: "summary of Tuesday's meeting"
  ─────────────────────────────────────────────────────────
  ✓ Injected into context (top 3 by score)
    • narrative-2026-05-03-09:14   score 0.84  ★★★ pinned
      "Discussed Q3 forecast revisions; team agreed to..."
      [Open chunk] [View source turn]
    • raw-2026-05-03-09:18         score 0.78
      "<assistant> The forecast adjustments cover three..."
      [Open chunk]
    • narrative-2026-05-02-14:30   score 0.71
      "..."

  Below threshold (not injected):
    • narrative-2026-05-01-10:00   score 0.58
    • raw-2026-04-29-15:22         score 0.51
    [show all 12]
```

Updates live on every kernel boundary fire; persists per-session so closing+reopening the session shows the most recent retrieval.

Wires into the existing retriever via a new `Memory_LastRetrieval(sessionID) RetrievalReport` RPC. The retriever itself records its decisions per-call into a small bounded ring buffer (in-memory + flushed to a `retrieval_history` table on chassis stop for survival across restarts).

### 2.2 Embedding inspector

For any chunk row in the Memory view, a "Why does this match?" button that:
- Embeds the user's typed query inline against the same embedder
- Computes cosine similarity to the chunk's stored vector
- Shows the score + a list of OTHER chunks the same query would retrieve, ranked by score

Useful for: debugging "why didn't the model recall X when I asked Y", verifying embedding quality, sanity-checking the embedder model choice.

UI lives in the existing MemoryView.vue — adds a small input + score visualization per chunk.

### 2.3 Re-summarize chunk on demand

Per-chunk action `Re-summarize` that:
- Takes the chunk's `source_turn_id` (set when the chunk came from a HookPostLLM/HookPostTool fire)
- Re-runs the narrative summariser configured per `memory-narrative-layer-01KQ8TD1` against that turn
- Replaces the chunk's summary text with the new one (atomic update)

Wires into the existing `Promoter` that mission ships. Adds a `Memory_ResummarizeChunk(id)` RPC.

For pre-narrative-layer chunks (raw chunks without a source_turn_id), re-summarize uses an extractive fallback (truncate to first/last N sentences) rather than calling the LLM.

### 2.4 Compression / health dashboard

A new `Memory → Health` tab in the Memory view (or under Settings → Memory if SettingsView has space):

```
Memory health                          [ Refresh ]   ────────────

Total chunks         12,840          
  • Raw              8,230  (64%)    
  • Narrative        4,210  (33%)    
  • Long-term promoted   400  (3%)   

Embedded vs. unembedded
  Embedded           12,440 (97%)    
  Unembedded             400 (3%)    [these would not be retrieved]

Last 7 days
  • Captured         +1,340  
  • Pruned           -210    
  • Promoted         +18     

Embedder
  Provider           openrouter (active profile: openrouter-9-models)
  Model              text-embedding-3-small
  Dimensions         1536
  Last call          2.3 s ago — success
  Last call latency  340 ms (p50: 280 / p95: 620)
  
[Test embedder]    [Re-embed unembedded chunks (400)]
```

All from existing data + a new `Memory_HealthSnapshot()` RPC.

### 2.5 Prune preview drill-down

Today `Memory_PrunePreview` returns a count. Extend to return the actual rows that would be pruned + the per-row reason ("low retrieval count", "high duplication ratio", "stale + below threshold", etc.).

UI: existing prune button gets a confirmation modal showing the row list before the user clicks "run":

```
Prune preview                           [ Cancel ]   [ Run ]
─────────────────────────────────────────────────────────
Will remove 47 chunks:

  raw-2026-04-15-08:30  | "..." | reason: 0 retrievals in 45 days
  raw-2026-04-12-14:22  | "..." | reason: duplicate of raw-2026-04-12-14:21
  narrative-2026-04-10  | "..." | reason: superseded by narrative-2026-04-15
  ...
  
[Show all 47]
```

Modify `Memory_PrunePreview` to return the rows + reasons; UI renders a virtualized list.

### 2.6 Per-chunk provenance drawer

Click any chunk row → side drawer with:
- Source turn (link to the chat session at that turn)
- Hook fire that captured it (HookPostLLM / HookPostTool / explicit pin)
- Original raw text (if currently a narrative)
- Summary (if currently a narrative)
- Embedding info (model used, dimensions, byte size)
- Retrieval count (last 7 days, last 30 days, all-time)
- Citation count (per `memory-narrative-layer` FR-008c)
- Pin count
- Promotion eligibility score
- Scope path (session → project → long_term, with promote/demote buttons)

Most fields exist in the data; the drawer is a richer renderer of `Memory_GetChunk(id)`. Add `RetrievalCount` / `CitationCount` / `PromotionScore` fields to the wire shape.

### 2.7 Live capture-rate widget

Small ambient indicator in the LegendBar showing capture velocity:
- 🧠 N chunks/min (last 60s)
- Click → opens the active-session memory inspector

Helps users notice when the greedy capture is or isn't running (silent failures of the embedder shouldn't go unnoticed).

## 3. Out of scope (this mission)

- **Memory chunk EDITING** (changing the text of a stored chunk by hand). Pin/forget/promote-scope are state-only operations; full edit is a future affordance.
- **Memory IMPORT** (drop a markdown file in to populate memory). Lives with `_archive/context-library` patterns.
- **Cross-user / team-shared memory**. That's `fleet-integration-01KX5R8D` territory.
- **Memory diff between two sessions / branches**. Possible follow-on, not now.
- **Embedding re-projection** (changing the embedder model for the entire store). Different mission — needs a careful re-embed pipeline.
- **Per-tool memory views** (different layouts for memories-from-bash vs. memories-from-web_fetch). The provenance drawer covers the inspection use case; specialized views are speculative.
- **Privacy redaction** in the inspector. Inherits from `model-secret-references` sanitizer; that mission's redactor already filters outbound surfaces; the inspector renders post-redaction content.

## 4. UX shape

See sketches in §2.1 / §2.4 / §2.5.

### 4.1 Toggleable inspector panel
Lives in the LegendBar as `🧠 Memory inspector`. Toggling opens a right-side dock that follows the active session. Closes per-session preference (don't force the panel on every new session).

### 4.2 Health tab routing
`Memory → Health` is a top-level tab inside MemoryView.vue. Switching tabs is keyboard-accessible (Tab to switch, ↑↓ within rows).

### 4.3 Capture-rate widget
A badge next to the existing fleet/build pills in the LegendBar. Color-coded: green (capturing), amber (capture queue backed up), red (embedder errors > 3 in last 5 min).

## 5. Functional requirements

- **FR-001** Active-session inspector reads the last retrieval decisions via a new `Memory_LastRetrieval(sessionID)` RPC.
- **FR-002** Retriever records each call's `(query, top_k_chunks, scores, injected_set)` into a bounded ring buffer + a `retrieval_history` table for survival across restarts.
- **FR-003** Embedding inspector embeds an inline query, computes cosine similarity to a target chunk + shows ranked others — `Memory_EmbeddingProbe(query) []ScoredChunk` RPC.
- **FR-004** `Memory_ResummarizeChunk(id) Chunk` re-runs the narrative summariser against the chunk's source turn (if narrative) or returns extractive-fallback summary (if raw).
- **FR-005** `Memory_HealthSnapshot()` returns counts (raw / narrative / long-term / embedded / unembedded), 7-day deltas, embedder metadata + recent latency stats, and the active embedder profile id + model.
- **FR-006** `Memory_PrunePreview(scope)` (existing) returns a count AND a per-row list with reasons; UI renders both in the prune confirmation modal.
- **FR-007** Per-chunk provenance drawer renders source turn link, hook fire, retrieval/citation/pin counts, promotion score, scope path. New fields on `Memory_GetChunk` wire shape.
- **FR-008** LegendBar capture-rate widget shows chunks-per-minute (last 60s) + color-coded status; click opens inspector.
- **FR-009** "Re-embed unembedded chunks" button on the health dashboard kicks off a backfill against the active embedder; surfaces progress (chunks remaining + ETA) inline.
- **FR-010** Inspector + health + provenance views render correctly on a fresh install with zero memory chunks (empty states with explanatory copy).
- **FR-011** No backend Cedar policy changes — all RPCs are read-mostly and gated by the existing memory-view Cedar action set. The "re-summarize" RPC reuses `ActionUseModel` for the LLM call inside the Promoter.

## 6. Non-functional requirements

- **NFR-001 (Performance)** `Memory_HealthSnapshot` returns in ≤ 200 ms p95 against a 100k-chunk store (use indexed COUNT queries, not full table scans).
- **NFR-002 (Storage)** `retrieval_history` table is bounded — last 1000 retrievals per session, soft-pruned on each insert. Total row cap 50k across all sessions.
- **NFR-003 (Capture-rate widget overhead)** Widget polls a counter that's incremented on every hook fire; no SQL per render. ≤ 0.5 ms per second of polling overhead.
- **NFR-004 (Inspector reactivity)** New retrieval events update the inspector within 200 ms of the kernel boundary firing.
- **NFR-005 (No content leakage)** Inspector renders post-sanitization content (the per-turn fingerprint sanitizer from `model-secret-references-01KW7M5A`); secret values never appear in inspector views.

## 7. Threats considered

| Threat | Mitigation |
|---|---|
| Inspector becomes a "show me everything" surface that leaks secrets via memory chunks | All renderers route through the same sanitizer the chat UI uses; inspector adds no new bypass |
| `retrieval_history` table grows unbounded under heavy use | NFR-002 caps at 50k rows total + per-session soft-prune |
| Re-summarize RPC abused to burn LLM tokens on stale chunks | Cedar `ActionUseModel` already gates; rate-limit 1 re-summarize per chunk per minute |
| Capture-rate widget polling adds UI jank | NFR-003 caps polling overhead; renders only when LegendBar is visible |
| Embedding-inspector exposes which chunks the user has stored — privacy concern with shared screen | Inspector lives behind the same Cedar gate as MemoryView; users can disable from Settings → Privacy |

## 8. Open questions

1. **Should the active-session inspector be SHOWN BY DEFAULT for power users?** Lean: hidden by default, surfaced via a one-time tooltip after the user's 5th session. Power users opt in; new users aren't overwhelmed.
2. **Should `Memory_ResummarizeChunk` write the new summary atomically (replace) or version-append (keep history)?** Lean: replace. Versioning explodes storage and the user can re-run if dissatisfied.
3. **Does the capture-rate widget need a per-project filter?** Lean: no for v1. Single global rate is fine.
4. **Should the prune preview support partial-apply (uncheck rows you want to keep)?** Lean: yes — render with checkboxes, default all-checked, "run on selected".
5. **For raw chunks without a source_turn_id (legacy)**, should re-summarize do nothing or run extractive-fallback? Lean: extractive-fallback, with a one-time toast explaining "this chunk predates the new summariser".

## 9. Spec mapping

| Existing | This mission |
|---|---|
| `memory-narrative-layer-01KQ8TD1` | Produces the narrative chunks this UI observes; reuses its `Promoter` for re-summarize |
| `unified-search-01KX5R8C` | Memory search across keywords lives there; this mission adds SEMANTIC inspection (similarity scores) |
| `_archive/memory-prune-completion-01KQ8TD8` | The prune sweep this UI exposes a richer preview for |
| `_archive/cross-session-search-01KQ8TDQ` | Existing message FTS5 — orthogonal |
| `model-secret-references-01KW7M5A` | Inspector renders post-sanitization content; reuses the per-turn fingerprint sanitizer |

## 10. Success looks like

A user can:

1. Toggle on the LegendBar `🧠 Memory inspector` and see the last retrieval's top chunks for their active session, with similarity scores + which were actually injected.
2. Click `Why does this match?` on a chunk in MemoryView, type a query, see the cosine similarity + ranked alternatives.
3. Click `Re-summarize` on a narrative chunk and watch its summary text update in place within 5 seconds.
4. Open the Memory → Health tab and see at a glance: 12,840 chunks (97% embedded), embedder = openrouter / text-embedding-3-small, last call 2.3s ago.
5. Click "Re-embed unembedded chunks" and watch the 400 unembedded rows backfill with progress.
6. Click "Run prune now" and review the actual 47 rows + their reasons before confirming.
7. Open a chunk's provenance drawer and see: source turn link, hook fire id, retrieval count 23, citation count 4, pin count 1, promotion score 47, scope path: session → project → long_term (eligible).
8. Notice the capture-rate widget turn amber when their embedder profile fails (key revoked) — debug + recover before discovering memory is silently broken hours later.

That's the bar.

## 11. Cherry-pick candidates for v0.5.x

Some pieces don't depend on `memory-narrative-layer` shipping first and could land as standalone v0.5.x patches:

- **§2.4 Compression / health dashboard** — only reads existing data. v0.5.3 candidate.
- **§2.5 Prune preview drill-down** — extends existing RPC. v0.5.3 candidate.
- **§2.7 Capture-rate widget** — single counter increment. v0.5.4 candidate.

The pieces that DO need narrative-layer first (because they observe narrative-specific concepts):
- §2.3 Re-summarize (needs the Promoter)
- §2.1 Active-session inspector (better with narrative scoring; raw-only version possible but less useful)

Recommend shipping the cherry-picks in v0.5.x; full inspector capstone in v0.7.0 alongside `memory-narrative-layer`'s arrival.
