# Chat single writer — decision record (WP01)

Mission `chat-single-writer-01DOGF0G` (dogfood 2026-10-04, finding F12).
Base: `main` @ `b8079d48` (v0.85.2). This file is the WP01 output; the
mission's `kitty-specs/` directory is git-ignored, so the record lives here
(a copy sits at `kitty-specs/chat-single-writer-01DOGF0G/research/decision-record.md`).

Every statement below is tagged **RAN** (executed, output quoted) or **READ**
(code reading only).

---

## 1. Data: what the dev profile actually contains (RAN)

Method: `cp ~/.kenaz/harness/dev/data.db{,-shm,-wal}` into a scratch
directory, then `sqlite3` against the **copy** only. The live file was never
opened. Timestamps are `created_at` in unix-nanoseconds.

| Query | Result |
|---|---|
| user rows / sessions | 80 rows / 9 sessions |
| adjacent (`b.sequence = a.sequence+1`) identical-content user pairs, `0 ≤ Δt ≤ 2 s` | **40 pairs** — every one of the 80 user rows is in exactly one pair |
| Δt distribution inside those pairs | min **0.72 ms**, max **16.7 ms** |
| adjacent identical user rows *outside* 2 s (genuine re-sends) | 9, smallest gap **6.6 s** (largest seen 13.5 h) |
| earlier row (A) referenced by any `turn_span_id` | **0** of 40 |
| later row (B) referenced by any `turn_span_id` | 11 of 40 (the other 29 predate model-moves 0333, so nothing carries a span) |
| first / last pair | 2026-06-07 23:04 / 2026-10-04 23:40 UTC |
| user rows whose `content_json` has an `image` or `document` block | **0** |

### 1.1 Spec §2.3 (blocks path) — NOT data-confirmable on this profile

The dev profile contains **no** multimodal user row at all, so §2.3 can be
neither confirmed nor refuted against data. A copy of the prod profile was
not taken (the request to copy it was refused by the harness permission
layer; only the dev profile was in scope). §2.3 therefore stands on code
reading plus the FR-3 regression harness, which drives
`Sessions_SendMessageWithBlocks` (text+image and image-only) through the
real `LLM.StartStream` on real sqlite and is the permanent proof (FR-5).

Code reading (READ) agrees with the spec: `sqlStore.AppendMessage`
(`core/session/store.go:1381-1393`) flattens the text blocks into `content`,
`llm/impl.go:957` reads that non-empty `content`, and
`chat_runner.go:953-964` writes it back as a text-only row. Only
image/document-only sends (flattened `content == ""`) took the
`LatestUserMessageID` branch — and those turns never reached fleet sync at
all (see §4).

### 1.2 The assistant-side kind-less duplicate (RAN)

Query: adjacent assistant rows, identical non-empty content, at least one
kind-less.

| session (prefix) | seqs | kinds | which row carries `streaming_failed_at` | Δt |
|---|---|---|---|---|
| 49ff55cc | 4 → 5 | – / – | 5 | 7.1 s |
| 49ff55cc | 17 → 18 | – / – | 18 | 2.4 s |
| 500adbdf | 6 → 7 | **final** / – | 7 (`transient`) | 4.8 s |
| 49ff55cc | 13 → 14, 26 → 27, 42 → 43 | – / – | **both** | 3.4 s / 26 ms / 209 ms |
| 500adbdf | 20 → 21 | – / – | **both** (`transient`, `unknown`) | 4.7 s |

The first three are the "3 observed cases" the diagnosis counted: a healthy
answer row followed by a kind-less **failed partial carrying the same
text**. The both-failed rows are a different shape (see §5.3).

---

## 2. The user-pair rule (FR-2a, finalised)

Two rows A, B form a **doubled user turn** iff all of:

1. same `session_id`, both `role = 'user'`, both `kind IS NULL`;
2. `B.sequence = A.sequence + 1`;
3. `A.content = B.content` (the flattened text, byte-equal);
4. `0 ≤ B.created_at − A.created_at ≤ 2 s`. The observed maximum is 16.7 ms
   and the smallest genuine re-send is 6.6 s; 2 s sits two orders of
   magnitude above the former and a factor of three below the latter;
5. A is **not** referenced by any `turn_span_id` (A is the frontend's row and
   the old runner never anchored on it — 0 of 40 in the data). If A is
   referenced the pair is skipped and counted;
6. pairs are formed greedily in ascending `sequence`; a row already consumed
   as some pair's B cannot be the A of another (guards a hypothetical
   triple-write chain; none observed).

The spec's draft condition "exactly one of which is referenced" is
**relaxed**: 29 of the 40 real pairs predate turn spans and have no
references on either side. Requiring a reference would leave 73 % of the
duplicates in place.

### 2.1 Survivor rule (FR-2b)

* **Text-only pair** (neither row has a non-text block): delete **A**. B
  keeps its span references and the id that (if sync was on) fleet already
  saw.
* **Multimodal pair** (A has a non-text block, B is text-only): keep **A**,
  `UPDATE session_messages SET turn_span_id = A.id WHERE session_id = ? AND
  turn_span_id = B.id`, then delete **B**. Never delete the row that carries
  the attachment.
* **Both rows carry non-text blocks**: cannot be produced by the bug (B was
  always written text-only) — skip and count.

---

## 3. Referrers of `session_messages.id` (FR-2c)

Enumerated from the dev copy's full schema (`.schema`, 55 tables) plus the
HEAD migration sources (READ + RAN). No table has a foreign key **to**
`session_messages(id)`; there is therefore no `ON DELETE CASCADE` hazard from
deleting a message row (the 0327/0332 class does not apply). The danger is
dangling *soft* references.

| Table.column | FK action | How 0341 handles it |
|---|---|---|
| `session_messages.turn_span_id` | none (soft) | text-only: deleted row A is never referenced (rule 5); multimodal: re-pointed B→A before the delete |
| `session_messages.continuation_of` | none (soft) | pair skipped + counted if either row is a continuation target |
| `session_messages.compacted_into_id` | none (soft) | pair skipped + counted if either row is a compaction summary target |
| `messages_fts` (triggers `messages_fts_ad/au`) | trigger | the delete trigger removes the FTS doc; the span re-point UPDATE re-indexes the touched assistant rows (content unchanged) — no orphaned FTS rows |
| `branches.parent_message_id` | none (soft) | pair skipped + counted if either id is referenced |
| `branch_message_refs.parent_msg_id` / `child_msg_id` | none (soft) | pair skipped + counted if either id is referenced |
| `artifacts.source_ref_json` (`message_id`) | none (JSON) | pair skipped + counted if either id appears in any `source_ref_json` |
| `stream_checkpoints` | keyed by (session, sub_id) | does not reference message ids — not affected |
| `unit_sync_state` | keyed by unit id | does not reference message ids — not affected (empty on dev) |
| `events` (audit hash chain) payloads | immutable | historical facts ("message X was appended"); NOT rewritten — rewriting would break the hash chain. A payload naming a deleted duplicate id is a true record of what happened |
| fleet-synced session events | remote | the text-only survivor is B, the id the old runner synced, so the common case needs nothing. Multimodal pairs: B was synced, A survives — documented residual, see §6 |
| `media_artifacts` / `context_attachments` | n/a | referenced *from* `content_json`, never referencing a message — keeping the multimodal row keeps the attachment link intact |

On the dev copy: `branch_message_refs` 0 rows; the one non-empty
`branches.parent_message_id` points at an assistant row; artifacts reference
`tool:*` pseudo-ids only; no `continuation_of`/`compacted_into_id` on any
user row. Expected dev-profile outcome: 40 pairs found, 40 deleted, 0
re-pointed, 0 skipped.

---

## 4. Sync path (FR-1d) — decision

READ: the old runner's re-append was the **only** user-turn path into
`SessionSyncer.AppendEvent` (`api.go:7525` → `llmHistoryWriter.AppendEntry`
→ `api.go:9756` `syncHook`). `Sessions_AppendMessage` /
`SendMessageWithBlocks` never touch the hook. Image-only sends — which took
the lookup branch — **never synced their user turn at all**, before or after
this mission's bug.

**Decision: emit without writing, through the writer seam.** The runner
resolves the turn's existing user row and asks the history writer to
*announce* it: `llmHistoryWriter` gains `AnnounceUserTurn(ctx, sessionID,
messageID)`, which fires the same `syncHook` with the same `{id, role:"user"}`
payload `AppendEntry` used. The runner discovers it as an optional interface
on `Config.HistoryWriter`, so production wiring needs no new field and every
harness built on the real writer exercises the real path.

Rejected alternative — moving the hook onto `Sessions_AppendMessage`: that
RPC is also the path for non-chat appends, and the scheduler / sub-agent /
resume paths append through `session.Manager` directly, so it would both
widen sync to rows it never carried and miss rows it did.

Exactly-once: the announce happens only on a *fresh* turn — the resolved user
row is the session's last row at `StartStream` time. The keychain redrive
re-runs the same turn and passes `Announce: false`.

## 5. `StartStream` callers (FR-1c) and the new contract

`ChatRunner.StartStream(ctx, profileID, sessionID, modelOverride string,
turn UserTurn)` where `UserTurn{MessageID, Text string; Announce bool}`
describes an **already-persisted** row. The runner has no code path that
writes a user row.

| Caller | Who persists the user row | `UserTurn` |
|---|---|---|
| `views/llm` `API.StartStream` ← `Bindings.LLM_StartStream` (desktop), `rpc.StartLLMStream` (served), `LiveChatRunDispatcher` (scheduled chats, appends the rendered prompt first, `chat_run_dispatcher.go:203`), sub-agent spawner (child session seeded before the call) | the caller, via `Sessions_AppendMessage` / `SendMessageWithBlocks` / `session.Manager.AppendMessage` | newest user row's id + flattened text, `Announce` iff it is the session tail |
| `ChatRunner.RedriveLastTurn` (keychain rotation) | already persisted by the original turn | the paused turn's own `UserTurn`, `Announce: false` (previously passed `""`, which also starved `postSendHook`/`fireAdvice` of the text) |
| `buildResumeStarter` (`api.go:8047`, Sessions_ResumeMessage) | **was the runner** (it passed a synthesized continuation prompt as `userMessage`) | the starter now persists the prompt itself via `session.Manager.AppendMessage`, then passes its id + text, `Announce: true` |

Consumers of the text that must keep working: `askBus.Answer` pre-seed,
`postSendHook` (`chat_runner.go:1478`), `fireAdvice` (`:1525`) — all now read
`turn.Text`. `checkModelSwitch` never read the text. Compaction
(`session_compaction.go:47-53`, `:257`) counts the `history_read` slice; with
one physical row the slice holds the user turn once, which is what the
existing comment claimed (it was false while two rows existed). No arithmetic
change is needed; the comment is corrected.

### 5.1 Assistant duplicate attribution (READ, reproduced in WP04)

None of spec §2.6's three candidates is the writer. The failed partial is
written by the **backend-error `PartialPersister.PersistPartial` path**
(`chat_runner.go:2056-2118` → `api.go:7596` writes a classic row then
`MarkStreamingFailure`). It persists `bridge.PartialSegment()`, which is the
text since the last `MoveStart` boundary — but a segment that the journal has
already written (the absorbed `final`) or still holds parked (and will write
in `driveRun`'s deferred `journal.Finish`) is not "un-persisted". Two
shapes, both in the data:

* error **after** `session_write` absorbed the final (500adbdf 6→7):
  segment = the final's text → kind-less failed twin after the final;
* error **after** the last fire completed but **before** `session_write`
  (500adbdf 21→22, 2 ms apart): partial row first, then `Finish` flushes the
  parked text as an `assistant_move`.

Fix (WP04): the journal reports what it owns; the backend-error path
persists only the tail the journal neither wrote nor holds.

### 5.2 Assistant cleanup rule (FR-2e, same migration)

Delete assistant row R iff: `R.kind IS NULL`, `R.streaming_failed_at IS NOT
NULL`, and an adjacent (`|Δsequence| = 1`) assistant row S in the same
session has byte-identical non-empty content, S is **not** itself a kind-less
failed row, `|Δcreated_at| ≤ 60 s`, no `user` row lies between them, and R
is not referenced (`continuation_of`, `compacted_into_id`, `turn_span_id`,
branch refs, artifact source refs). On the dev copy: exactly the 3 cases of
§1.2.

### 5.3 Residuals deliberately left alone

* **Both-failed equal-content assistant pairs** (4 on dev, Aug 2026). Equal
  text, both flagged — the pre-0336 periodic-flush checkpoint P0 writing the
  same text twice. `sessions/0337`'s discriminator requires a *strict* prefix
  and so does not touch them. Neither row is "the answer", so the survivor
  is not decidable by this mission's rule. Owner: a follow-up to the
  chat-turn-integrity repair (0337 family).
* **Whole-turn partials from the pre-#105 era** (500adbdf row 21: 508 bytes
  = move 0 + final). Content differs from the move it duplicates, so an
  equality rule cannot see it. Left in place.

## 6. Migration ID

`sessions/0341-dedupe-user-turns` (G claims the next `sessions/03xx`; C takes
0342). One migration covers both classes (FR-2e "same migration") so this
mission consumes exactly one ID.

Residual: a multimodal pair whose B had already been fleet-synced keeps A;
the remote event stream names B's id. Fleet pull idempotency is
out of scope (spec §5) and handed to `fleet-session-truth-01DOGF0A`.

---

## 7. Release-note text (WP06 — for the shipping PR body)

> **Chat: every message you sent was stored — and sent to the model —
> twice.** Since early June, each user chat turn was written to the
> session twice (once by the chat window, once again by the chat engine),
> so on every request the model received every one of your earlier
> messages duplicated. This affected answer quality and token cost, and
> showed up as repeated message bubbles. The engine no longer writes your
> message; the chat window's copy is the only one. A one-time migration
> (`sessions/0341-dedupe-user-turns`) removes the stored duplicates from
> existing conversations — when a duplicate pair involved an attached
> image, the copy that carries the image is the one kept. It also removes
> extra assistant bubbles that repeated an answer after a late provider
> error. Fleet session sync keeps receiving each of your messages exactly
> once (image-only messages now sync too; previously they did not).

## 8. Records

* Ledger: `docs/unwired-ledger.md` → Drained → 2026-10-04 entry.
* Roadmap: `docs/roadmap.md` is git-ignored and absent from this
  worktree; the row ("chat-single-writer-01DOGF0G — shipped") is to be
  added by whoever merges the release branch, with the spec dir moved to
  `kitty-specs/_archive/` in the same step (CLAUDE.md mission convention).
