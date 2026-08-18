# Unwired ledger

The standing list of code that is **built but not reached** — findings from
the release-start unwired sweep (see CLAUDE.md, "Release ritual: unwired
sweep"). Every release drains it or re-dates it.

Two kinds of entry live here:

- **Gated** findings, whose real home is an allowlist file under
  `scripts/ci/allowlists/`. Those are enumerated below only as an index —
  the justification text lives with the gate that enforces it.
- **Ungated** findings, which have no gate to hold them. Those are written
  out in full here, because otherwise the only record is a commit message
  nobody will grep.

An entry earns removal by being wired, deleted, or re-justified with a new
date and a named owner. "Still true" is not a reason to leave a stale date.

**Not a finding:** `docs/served-mode-boundary.md` documents the six views
that intentionally render `NotAvailableInServedMode.vue` in served builds.
Every sweep before 2026-08-14 re-found this as if it were new; it is a
shipped product boundary, not unwired code. Read that doc before flagging
`NotAvailableInServedMode` usage again.

---

## Gate inventory (the gated half)

| ID | Gate | Allowlist | What it catches |
|---|---|---|---|
| I1 | `core/agentgraph/convergence_gates_test.go` | `i1-missing-executor.txt` | node kind with no registered executor (**empty**) |
| I2 | same | `i2-manifest-executor-symbol.txt` | manifest `executor:` naming drift (**empty**) |
| I3 | `check-agentgraph-convergence.sh` | `i3-unexercised-kinds.txt` | callable kind no shipped graph/activity/kernel-fixture exercises |
| I4 | `check-no-forbidden-compaction-symbols.sh` | `i4-forbidden-compaction-symbols.txt` | second compaction entry point |
| I5 | `check-agentgraph-convergence.sh` | `i5-elicitation-stores.txt` | second elicitation store (**empty, must stay empty**) |
| I6 | `check-agentgraph-convergence.sh` | `i6-template-only-kinds.txt` | kind only reachable from a template |
| I7 | `check-agentgraph-convergence.sh` | `i7-orphan-packages.txt` | non-test package with no non-test importer |
| I10 | `check-no-unwired-gates.sh` | `i10-unwired-gates.txt` | exported control-flow function with zero non-test call sites |
| I11 | `check-builtin-tool-registration.sh` | `i11-unregistered-builtin-tools.txt` | builtin tool package no wiring site imports — **added 2026-08-14** |
| I12 | `check-single-move-writer.sh` | *(none — no allowlist by design)* | second writer of transcript-move metadata, or a seam with 0 / >1 production callers — **added 2026-08-14** |
| I13 | `check-cedar-gate-arguments.sh` | `i13-cedar-gate-arguments.txt` | a `cedar.Gate` argument or view-`Config` field that resolves to an unconditional permit — **added 2026-08-16, empty** |

Non-allowlist gates that also protect against unwired code:
`check-output-ports.sh` (output port with no reader),
`check-knob-coverage.sh` (registered config field with no consumer),
`check-seam-implementers.sh`, `check-node-dispatch.sh`,
`check-serve-dispatch-drift.sh`, `core/serve/wsstream_topics_parity_test.go`
(desktop `passthroughTopics` ↔ `SERVED_STREAM_TOPICS`),
`core/rpc/builtins_wiring_test.go` (registered tool ↔ predicate case).
`scripts/ci/gates_can_fail_test.go` is the meta-gate: it plants a violation
per gate and asserts the gate rejects it.

---

## Mission 01PMCH01 (model moves in the transcript) — CLOSED 2026-08-14

This section was the mission's in-flight list: fields that existed and
were not yet read, each naming the WP that would consume it and the date
it was written, so the next sweep would not re-find them as inert
plumbing. The rule attached to it was that a line outliving its mission
is a finding, not an exemption.

**All six WPs are merged; nothing is in flight.** Every line that sat
here is resolved below with its disposition — wired, deleted, or
graduated to a standing ungated finding. There is no open exemption
left in this section; it is kept as the mission's record, not as a
waiver.

**Release gate CLEARED 2026-08-14 by WP04.** This section previously
carried a sequencing constraint — "01PMCH01 must not reach a release tag
between WP02 and WP04" — because WP02 shipped ~13 rows per turn into a UI
that rendered every one as an unlabelled assistant bubble, tool output
included. WP04 is that consumer: the transcript projection
(`frontend/src/lib/transcript.ts`) reads `kind` / `moveIndex` /
`turnSpanId` off every row and the move boundary off the stream.

The **transcript** is correct from here — narrower than "the mission is
releasable", which this section previously claimed. WP04's review found
three other surfaces that consume move-bearing rows and are wrong, none of
them the transcript, all owned by WP05/WP06:

- ~~`MessageList.vue` + `MessageBubble.vue` count **rows**, not turns,
  for "Summary of N turns"~~ — **FIXED 2026-08-14 by WP05.**
  `MessageList` now calls `transcript.foldedTurnCounts`
  (imported at `MessageList.vue:35`, called at `:178`; the indicator
  itself renders at `MessageBubble.vue:551`),
  which attributes each folded row to the turn that opened it
  (`turnSpanId`, or the most recent preceding user row for a classic
  row) and counts distinct turns. WP05 made the call WP04 declined to
  make unilaterally: the two green assertions
  (`CompactionFlow.e2e.spec.ts` "SummaryIndicatorRenders" and
  `CompactedHistory.spec.ts` "renders the Summary of N turns
  indicator") moved from "Summary of 2 turns" to "Summary of 1 turn",
  because both fixtures fold ONE exchange — a user row and the
  assistant row answering it — and both now carry a comment saying so.
  Pinned by `lib/__tests__/foldedTurnCounts.test.ts` (five cases);
  reverting to the row count fails four of them — every case except
  "counts an orphaned folded row as its own turn", where one row *is*
  one turn. WP06 deliberately stayed out of both files this cycle;
  they were WP05's.
- ~~**Search corpus**: migration 0312's FTS triggers
  (`core/session/migrations_search_fts.go:48` and its update/delete
  siblings) fire on every `session_messages` write with no role filter,
  so every `tool_result` row's raw output and every `tool_call` row's
  synthetic args summary was full-text indexed~~ — **DRAINED 2026-08-14
  by WP06.** Migration 0335
  (`core/session/migrations_search_fts_tool_rows.go`) replaces 0312's
  unguarded triggers with role-guarded ones and evicts the tool rows
  already in the index. See the Drained section for the contract and
  why "index them and filter in the UI" was rejected. The two search
  components were left untouched, deliberately: `SearchModal.vue`'s
  User/Assistant/System role filter (`:347-350`) is now exactly the
  corpus, and adding a Tool option would have been a control that
  returns nothing. `SearchPalette.vue` still has no role filter at all,
  which is unchanged and not a finding. One residual defect that came
  out of that decision is recorded as its own standing finding below
  (`?role=` is read from the URL unvalidated).
- ~~**`Sessions_Export`** (Go, markdown + JSON) walks tool rows with raw
  output~~ — **FIXED 2026-08-14 by WP05** (FR-006). The export is now
  an explicit DISPLAY consumer, contract written at the head of
  `core/sessions/export/moves.go`: the document is turns (move rows
  never take a `## Turn N` heading), tool output is capped at
  `ToolOutputCap` = 4000 runes on a rune boundary, and argument VALUES
  are never printed in either format — the markdown `**Arguments:**`
  raw-JSON block and the JSON `tool_calls[].arguments` map are both
  gone, replaced by a names-and-types summary. That is a structural
  rule, not a redaction one, because `RedactValue` only walks top-level
  strings: a secret in a nested argument object or inside an array
  sailed past it (see the standalone finding below — that leak is
  PRE-EXISTING, not something WP05 introduced).

  Three corrections to WP05's first write-up of this row, from its
  adversarial review, because each was stated more strongly than the
  code supports:

  - *"Nothing in the package can reach
    `session.Message.ModelLayerToolArgs`"* — **false as written.** The
    package imports `core/session` and the accessor is exported, so the
    call compiles from here (verified by compiling one). What is true
    is that no line calls it, the helpers are named apart so the wrong
    one is not an autocomplete away, and **no gate enforces it** — a
    future edit that added the read would pass CI. Convention, not
    fence; the comment in `moves.go` now says so.
  - *"A classic session's export is unchanged in both formats"* —
    **true only for a classic session with no tool calls.** Verified
    byte-for-byte against the base commit for that case. A classic
    session WITH tool calls changes in both formats, deliberately: that
    IS the security fix. Now pinned separately by
    `TestExport_ClassicToolCallsLoseTheirRawArguments` so the
    deliberate break cannot be misread as an accident.
  - The removal of `tool_calls[].arguments` is **not additive**, and
    `ExportFormatVersion` was left at 1 while its own doc comment says
    to increment on a breaking shape change. Bumped to **2**. The rest
    of WP05's JSON additions (`kind`, `turn_span_id`, `moves`,
    `trajectory_only`) genuinely are additive and `omitempty`.

  One hole the review found and closed: argument **names** are printed
  by design, and `redactMessages` walks only argument VALUES, so a
  credential sitting in an argument KEY went into both documents
  verbatim. `argsSummaryFromValues` now runs `RedactValue` over the
  name; pinned by `TestExport_RedactsArgumentNames`.
- ~~**`session.TranscriptEntry.ContentBlocks` write half**~~ —
  **DELETED 2026-08-14 by WP05**, which is the other half of the
  delete-vs-finish deadline this list recorded against it. Its own
  dated entry is immediately below, and the expressiveness hole the
  deletion re-opens is a standing ungated finding. WP06 did not touch
  the field.

All three of the surfaces WP04's review named are therefore resolved,
and so is the field: two fixed by WP05, one drained by WP06, one
deleted by WP05. None of them is an inherited exemption — the deadline
text each carried has been discharged, not extended.

**Moved out of the mission's list, because no WP owned them and the
mission has closed** — see "Open — ungated findings" for the full text
of both:

- the revised-draft stream gap (FR-003's "no post-hoc mismatch" is not
  true on the exit-gate-revision path);
- `views/search/impl.go` returning soft-archived rows, found by WP06
  while working next door.

Whoever mounts `SubagentTab.vue` (dead today) must route through
`projectTranscript` or it reinherits the 13-bubble regression.

### 2026-08-14 · `session.TranscriptEntry.ContentBlocks` — DELETED by WP05

**Disposition: deleted.** Class: *the whole producer chain has no
producer.* WP02 added the field so the transcript seam could express as
much as `Manager.AppendMessage` — without it an author needing a
multimodal entry had to leave the seam, and the off-seam path cannot
stamp move metadata, so the move degraded silently to a classic entry
with neither the compiler nor `check-single-move-writer.sh` objecting.
WP03 drained the READ half (`core/rpc/model_history.go` prefers
`session.Message.ContentBlocks` over the flattened column, pinned by
`TestModelHistory_ContentBlocksAreNotReflattened` — that reads the
DURABLE field, which stays and is still written by
`SendMessageWithBlocks`). The WRITE half was WP05's, with a deadline:
ship a writer or delete the field.

WP05 deleted it. There is no producer to wire it to and none is one
change away: `agentgraph.ToolResult` is `{Content string; IsError
bool}`, so a tool result cannot carry an image, and
`agentgraph.HistoryEntry` has no blocks field either. Writing the
writer would have meant synthesising content for it — moving the lie
one layer up instead of ending it, which "Disposition: delete vs.
finish" names explicitly. `TestAppendTranscriptEntry_CarriesContentBlocks`,
the field's only reader, was deleted with it.

**The expressiveness hole this re-opens is recorded below** under
*Open — ungated findings*, and the reasoning is repeated in a comment
where the field used to be so an author who needs it finds it there
rather than rediscovering it.

Not on this list, because they are already load-bearing:
`session.Message.{moveKind,moveIndex,moveTurnSpanID}` +
`Manager.AppendTranscriptEntry` (the live chat write path runs through
them on every append and every read — `moveColumnValues` binds them and
`applyMoveColumns` rehydrates them, even though today every production
write leaves them zero) and `session.MoveKinds()`, whose production
reader is `MoveKind.known()` — the validation `AppendTranscriptEntry`
runs on every entry. The wire and the frontend mirror the vocabulary in
prose and in a TS union; they do not call `MoveKinds()`.

---

## Open — ungated findings

### 2026-08-14 · `export.RedactValue` only walks TOP-LEVEL strings (pre-existing) — **CLOSED 2026-08-16, see below**

> **Superseded.** The reproduction below stands, but re-verifying it on
> 146d9e54 found the finding was both narrower and wider than written:
> narrower because v0.63.0's structural rule really had closed the
> argument path, wider because four surfaces the scanner never touched
> at all were still leaking. See
> "2026-08-16 · what the export scanner covered BEFORE, and what leaked"
> under **Drained**.


**This predates the mission.** Recorded plainly rather than left inside a
WP report, because it is a data-leak finding and a WP report is not where
those go.

`core/sessions/export/redact.go:127` `redactMessages` is the export's only
credential scanner. For a tool call it does:

```go
for k, v := range tc.Arguments {
    if sv, ok := v.(string); ok { redacted[k] = rv } else { redacted[k] = v }
}
```

So a `map[string]any` or a `[]any` argument value is copied through
UNSCANNED, and argument KEYS are never scanned at all. Before WP05 the
export then printed those arguments verbatim — `formatToolArgs` as a
markdown JSON block, `jsonToolCall.Arguments` as a JSON map.

Reproduced against the base commit `8c8b63a9` with a throwaway probe:
a secret at `arguments.headers.authorization`, one inside
`arguments.body[1]`, and one used as a map KEY all reached both the
`.md` and the `.json` file. Only the top-level string matching a
credential pattern was replaced. Any session exported from a build
before this fix may contain live credentials on disk.

**Mitigated, not fixed.** WP05's structural rule — the export never
prints an argument value — removes the reachable path, and
`argsSummaryFromValues` now scans the NAME too. `RedactValue` itself is
unchanged and is still a top-level-strings-only scanner; it is also
still the only thing standing between a credential in a tool RESULT (or
in message content) and the exported file, and a credential that matches
no pattern in `builtinMatchers` is not caught anywhere. Verified: a
credential-shaped secret in `ToolCall.Result` IS redacted; an
arbitrary-looking secret in the same field is not.

**Owner:** unassigned. The change that closes it is a recursive walk in
`redactMessages` over `map[string]any` / `[]any` / keys, which is cheap;
it stayed out of WP05 because WP05's fix was structural and widening a
credential scanner mid-mission is its own review.

### 2026-08-14 · FR-006's SHARE half is unimplemented (`Handoff_Share` sends nothing)

WP05 reported that fleet share carries moves "by construction" because
`Handoff_Share` transports EventLog records rather than `session_messages`
rows. **That claim is false, and the export half of FR-006 is the only
half that shipped.**

- `core/rpc/views/contextsync/impl.go:151` (`Handoff_Share`) calls
  `Handoff.ShareSession(ctx, sessionID, recipientUserID, nil)` at `:159`
  — a literal `nil` event slice, with a comment at `:155-158` deferring
  the real payload to "the chassis path". There is no chassis-path
  caller. The only other non-test mention of `ShareSession` outside the
  fleet package is `core/rpc/context_sync_wiring.go:123-128`, the
  adapter that forwards this same call; every remaining call site is a
  test.
- `core/fleet/team_handoff.go:133` builds `wireEvents` as
  `make([]wireEvent, 0, len(plainEvents))` and `:151` posts it as
  `"events"` — so the request body carries a literal `[]`, not `null`.
- Even if the `nil` were replaced, the fleet event record could not carry
  moves: `core/rpc/api.go:6180-6184` marshals
  `{"id": …, "role": …}` and nothing else, deliberately — the wiring
  comment at `core/rpc/api.go:2426-2428` states no plaintext content
  crosses that boundary. `core/fleet/session_sync.go:42`'s doc claiming
  the record is "usually JSON of session.Message" is stale against that
  producer.
- `scripts/ci/check-fleet-log-export-fence.sh` constrains the OTel/slog
  lane, not this one, so no gate sees the gap either way.

So: `Handoff_Share` is a plumbed, contentless surface. Sharing a session
transmits an encrypted envelope around an empty list — for classic
sessions as much as move-bearing ones. FR-006's second sentence is not
satisfied and cannot be satisfied by anything in the mission's diff.

**Owner:** 01PMCH01 spec amendment or a fleet mission. Closing it needs a
product decision first (what may cross the fleet boundary in plaintext),
then a payload builder; it is not a WP-sized fix. **Do not** mark FR-006
done on the strength of the export half.

### 2026-08-14 · A move cannot be multimodal (the hole WP05's deletion re-opened)

Recorded as required by the deleted `TranscriptEntry.ContentBlocks`
entry above. This is an **expressiveness hole**, not unwired code —
listed here because deleting the field is what makes it invisible, and
the sweep that finds it next should find this line first.

`Manager.AppendTranscriptEntry` is the only seam that may stamp move
metadata, and it can now express only a TEXT entry. So an author who
needs a move carrying an image has exactly two options, and both are
wrong:

1. `Manager.AppendMessage`, which accepts `ContentBlocks` but cannot
   stamp `kind` / `move_index` / `turn_span_id`. The move silently
   degrades to a classic entry. **No gate sees this**:
   `check-single-move-writer.sh` clause 3 counts CALLERS of
   `AppendTranscriptEntry`, not writers that avoid it, and the compiler
   is satisfied because `AppendMessage` is a legitimate API (the user
   turn uses it, via `SendMessageWithBlocks`).
2. Flatten the image out of the move, which loses it.

**Nothing needs this today** — the producer chain cannot make a
multimodal move: `agentgraph.ToolResult` is `{Content string; IsError
bool}` and `agentgraph.HistoryEntry` carries no blocks. It becomes real
the first time a tool returns an image, or thinking/vision output is
captured per-move.

**The change that closes it** (one commit, all three parts, or none):
give `agentgraph.ToolResult` a blocks field, add `ContentBlocks` to
`agentgraph.HistoryEntry`, restore `TranscriptEntry.ContentBlocks` and
its assignment in `AppendTranscriptEntry`. Doing only the last part
recreates the field with no writer, which is what WP05 deleted.

**Owner:** unassigned — it needs a producer, which is feature work with
a spec (multimodal tool results). Note in `core/session/moves.go` where
the field used to be.

### 2026-08-14 · `session.ToolCall.{Arguments,Result}` have no production writer

Found by WP05 while rewriting the export. The only production writer of
`session.ToolCall` is `moveToolCalls` (`core/rpc/api.go:6202`, the
composite literal at `:6208` — the sole non-test `session.ToolCall{`
in the tree), which sets `{ID, Name, IsError}` and nothing else — by
design, and the comment at `:6195-6201` says so: `Arguments` belongs to the
DISPLAY layer and the display layer never sees values (see the contract
on `session.Message.ModelLayerToolArgs`), and a tool result is its own
`tool_result` row now, not a field hanging off the call.

**Kept, not deleted, and not because "we'll get to it".** These are
fields of a struct serialised into `session_messages.tool_calls`. Rows
written before the mission may hold both, and the export is a live
reader of both: `Arguments` feeds `argsSummaryFromValues` (names and
types, never values) and `Result` is rendered capped. Deleting them
would silently drop data from old sessions' exports. They are a
**read-compat surface with a live reader**, which is a different thing
from unwired code — logged here so the next sweep does not re-find them
as an inert pair.

The **gap the gates cannot see** is the one worth writing down: nothing
prevents a future writer from populating `Arguments` with real values,
at which point the display layer starts carrying them. The export is
structurally safe (it never prints a value), but no gate enforces that
`Arguments` stays empty at the write site.

### 2026-08-14 · The exit gate's revised answer never reaches the stream

Inherited from 01PMCH01's in-flight list at mission close; no WP claimed
it, so it graduates to a standing finding rather than expiring with the
section.

On the **revised-draft** path the exit gate (or the escalation ladder)
returns text different from the draft the model streamed. The revision is
persisted — `turnJournal.AppendEntry` flushes the parked draft as its own
`assistant_move` and stamps the revision as the turn's `final`, which is
the honest record — but nothing puts the revised text ON the stream. The
live view therefore shows the draft as the answer and a reload shows the
revision.

Spec FR-003 asks for "no post-hoc mismatch between what you watched and
what's stored", and on this path there is one. The transcript is right
and the live view is wrong, which is the better of the two failure
directions but still a lie to the user who was watching.

**Owner:** unassigned. The fix is backend: deliver the gate's revised
text as a move boundary + delta on the chat stream so the surface can
replace the draft bubble, rather than only writing it to the store.

### 2026-08-14 · `views/search/impl.go` searches soft-archived rows

Found by 01PMCH01 WP06 while re-guarding the FTS triggers next door; not
caused by the moves mission and not fixed by it.

There are two message-search implementations and they disagree about
compacted history. `core/search/search.go` filters `sm.archived_at IS
NULL` on both of its query shapes. `core/rpc/views/search/impl.go` — the
one the Wails binding and served mode actually call — filters project,
session and role, and never archived_at, in either `Search` or the
`UnifiedSearch` messages adapter.

So a row that compaction soft-archived is gone from the transcript, gone
from the model's history, and still a search hit that navigates the user
to a message the session no longer renders. The moves mission makes this
more visible (compaction now archives many more rows per turn) without
having introduced it.

**Owner:** unassigned. Two honest exits: add the predicate and accept
that compacted content stops being findable, or add an explicit
"include compacted history" filter to the search UI and wire it. Not a
third: the two implementations should not keep disagreeing silently.

### 2026-08-14 · `core/search/search.go` is a dead second search engine

Adjunct to the `archived_at` entry above, found while verifying it.
`core/search` — `doc.go`, `query.go`, `search.go` and its own test — has
**zero non-test importers**. The correct `archived_at IS NULL` predicate
lives only there; the implementation the app actually calls,
`core/rpc/views/search/impl.go`, lacks it.

So the honest framing of the entry above is not "two implementations
disagree" but "the correct implementation is dead and the live one is
missing the predicate". Under this file's own doctrine that is
**rival infrastructure**, and the disposition is a delete-or-adopt
decision, not a bug.

**Owner:** unassigned. Whoever resolves the `archived_at` entry should
resolve this in the same change — adopting `core/search` or deleting it
are both fine; leaving a dead copy holding the right answer is not.

### 2026-08-14 · `Role == RoleAssistant` is a staleness class, and no gate sees it

This entry merges three findings that are the same finding: the class
itself (recorded by WP06 against the Drained line below), the one live
consumer still drifting on it, and the gate that was owed for it and not
written.

**The class.** Since 01PMCH01 WP02, `Role` no longer identifies what a
row *is*. A `tool_call` move persists with `Role = RoleTool`, and
`RoleAssistant` now covers both the turn's `final` answer and every
interim `assistant_move` narration. So two idioms that were true for
every release before WP02 are now silently wrong on a move-bearing
session, and neither fails to compile:

- `Role == RoleAssistant && len(ToolCalls) > 0` to mean "this row
  opened a tool call" — the defect WP06 fixed in
  `core/agentgraph/compaction/wiring/store.go`; `toolUseID` (`:131`)
  and its mirror (`:149`) now switch on `m.MoveKind()` (`:135`, `:155`)
  instead. See the Drained entry.
- `Role == RoleAssistant` to mean "an assistant answer".

**The live drift.** `core/rpc/views/branches/impl.go:366` (tail-5 branch
summary) and `:452` (last-8 turns for `ReintegrationProposal`) still use
the second idiom, so both now sample the model's thinking-out-loud
alongside its answers. `impl.go:329-330` (`LastAssistantMsg`) is
unaffected — the last row of a completed turn is the `final` move. This
is mild: nothing is mis-paired, no request is malformed, the summaries
are just noisier than they read. Filtering to
`MoveKind() == MoveKindFinal || MoveKind() == ""` is the one-line fix.

**The gate that is owed.** The sweep's gate-extension rule says a find
representing a class the existing gates cannot see must extend a gate in
the same commit, with a planted-violation proof in
`scripts/ci/gates_can_fail_test.go`. WP06 found exactly such a class and
did **not** extend a gate: the merge-base→WP06 diff touches no file under
`scripts/ci/`. The rule is not satisfied, and the usual excuse — that any
gate for it would be vacuous or unboundedly noisy — does not hold here.
The candidate set is small and enumerable: eleven non-test `.go` files
under `core/` mention both `RoleAssistant` and `ToolCalls`
(`core/agentgraph/compaction/wiring/store.go`, `core/llm/bedrock/bearer.go`,
`core/llm/bedrock/bedrock.go`, `core/llm/gemini/wire.go`, `core/llm/llm.go`,
`core/llm/registry/registry.go`, `core/rpc/api.go`,
`core/rpc/views/sessions/impl.go`, `core/session/moves.go`,
`core/session/types.go`, `core/sessions/export/moves.go`), and only three
non-test files outside that set name `RoleAssistant` at all. A gate that
requires each `RoleAssistant` test over a `session.Message` to sit beside
a `MoveKind()` discriminator, or to be listed with a dated justification,
is constructible against a list that size.

**Owner:** unassigned, for all three parts. The branch-summary filter is
cheap enough to fold into whatever next touches branch summaries; the
gate is the part that keeps the class from coming back.

### 2026-08-14 · Migration 0335's tool-row purge is not idempotent

`sqlPurgeToolRowsFromFTS`
(`core/session/migrations_search_fts_tool_rows.go:155-156`) issues
FTS5's `'delete'` command for every `role = 'tool'` row in
`session_messages`, with no guard against having been issued before. By
the migration's own documented reasoning (`:104-110`), a `'delete'` for
terms the index does not hold drives the term counts negative and SQLite
then fails the statement with "database disk image is malformed".

This is latent, not live: migrations run once, keyed by the ledger, and
0335's `Down` backfills the same rows, so the only supported Down→Up
cycle is balanced — `TestMigration0335_EvictsRowsIndexedBeforeIt`
exercises exactly that path. What is unguarded is any future path that
re-applies `Up` without the matching `Down`: a repair routine, a manual
re-run, or a second migration that copies this statement. Recorded
because the statement reads like a plain backfill and is not one.

**Not independently reproduced** — the corruption claim above is the
migration's own comment plus the shape of the SQL, not a probe this
sweep ran. Treat it as a hazard to design against, not a measured
failure.

**Owner:** unassigned. The cheap closure is a guard that makes the purge
a no-op when the index holds no tool rows, or a comment at the statement
saying explicitly that it may be applied exactly once.

### 2026-08-14 · `SearchModal` takes `?role=` from the URL unvalidated

`SearchModal.vue:51-63` (`readFromRoute`) copies the `role` query
parameter straight into `roleFilter` (`:60`) with no membership check,
and `:118` forwards whatever it holds as `filters.roleFilter`. The
`<select>` that owns the control offers exactly four values —
`""`, `user`, `assistant`, `system` (`:347-350`).

Since WP06 took tool rows out of the FTS corpus (see Drained), a
deep link carrying `?role=tool` puts the modal into a state its own UI
cannot express and cannot leave by any control: the select renders blank
because no option matches, and every query returns zero hits forever.
Before WP06 the same link returned tool rows, so this is a defect the
corpus change made permanent rather than one it introduced.

The same hole exists for any other unknown value (`?role=banana`), and
for `project`, `from` and `to`, which are equally unvalidated — but only
`role` has a closed vocabulary the UI enforces everywhere else, which is
what makes it a lie rather than an empty result set.

**Owner:** unassigned. One line in `readFromRoute`: drop a `role` that is
not in the option set, the same way the select would.

### 2026-08-14 · Deferred asks have no producer, and wizards have no caller

The elicitation surface ships three modes; only one of them is reachable.

**Deferred.** `elicitview.API.{RegisterDeferred,AnswerDeferred,ListDeferred}`
exist, the `elicit:deferred` / `elicit:deferred:answered` topics exist, and
`DeferredAskPill.vue` + `DeferredAskPanel.vue` subscribe to them. Nothing
ever registers a deferred ask: the only entry points are the Wails bindings
`Elicit_RegisterDeferred` / `Elicit_AnswerDeferred`, which only the frontend
can call, and no frontend code calls them. It cannot come from the model
either — `askuserquestion.AskArgs` has no `mode` field, so the tool's own
schema gives the model no way to ask for deferred delivery. The topic has a
subscriber shape and no publisher.

The two components were therefore **left unmounted** by the 2026-08-14
elicitation-mount fix, deliberately. Mounting a subscriber to a topic that
nothing emits would have made the release ritual's own report ("the deferred
surface is wired") false.

**Wizard.** `elicitview.API.OpenWizard` has zero non-test callers, and
nothing constructs an `elicitation.Question` with a non-empty `Batch`. The
whole downstream chain — `Elicit_SubmitWizardStep`,
`ElicitClient.submitWizardStep`, `WizardQuestion` / `WizardDependsOn` /
`WizardAnswer` in `types.ts`, and the `questions[]` branch of the wire shape
— is reachable only from `api_test.go`. `AskUserQuestion.vue` has no wizard
renderer, so even a batch that did arrive would render as an unknown kind.

**Owner:** unassigned. Two honest exits: (a) add `mode` + `questions` to the
tool schema and render the wizard, which is feature work with a spec, or
(b) delete both legs — deferred and wizard — down to the single blocking
path that actually runs. Not (c): leaving a half-surface that reads, in a
code review, like a shipped feature.

### 2026-08-14 · Live tools whose only UI is unmounted (todo, sub-agent)

Found while tracing the elicitation gap; same shape, lower severity — these
park nothing, so the failure is a missing display rather than a stalled turn.

`kenaz__todo_write` is registered (default-OFF, user opt-in from the Tools
panel) and writes to `GlobalTodoStore`. `TodoChip.vue` and
`TodoSidePanel.vue` are its display surface and **no component imports
either**. A user who turns the toggle on gets the model's task list rendered
as raw tool-result text, and the chip that was built to summarise it never
appears. Both components are purely presentational (props in, `open`/`close`
out), so mounting them needs a parent that owns the todo state — most
plausibly `MessageBubble`, which is owned by another worktree this cycle.

`SubagentTab.vue` + `SubagentBudgetMeter.vue` are worse off: no importer,
**and** no backend to import them for. `SubagentBranch.subagentStatus` is
read by `SubagentTab.vue` alone, and the tab's four control emits
(`abort` / `steer` / `pause` / `resume`) have no counterpart anywhere in
`harnessClient.ts` — there is no pause, resume, abort or steer RPC to call.
`kenaz__subagent_dispatch` itself is live (registered when the BranchSeam is
non-nil) and its branches do surface in the mounted `BranchSidebar`, so
nothing is invisible; what is missing is the dedicated live-worker view.

**Owner:** unassigned. Todo: wire (a parent must hold the list). Sub-agent:
delete, or spec the control RPCs first — a tab whose buttons cannot be
implemented from the current backend is not "not yet mounted", it is
unbuilt.

### 2026-08-14 · The denial UX gap (opened by deleting `DenialNotice`)

`DenialNotice.vue` + `usePolicyDecisions()` + `_emitDenialForTest` were
deleted this sweep (see Drained). They were the *intended* surface for
policy denials and they were completely inert, so deleting them was
right — but it leaves a real product gap that must not be lost with them.

**The gap:** the harness has no denial-aware UI at all. `policyAPI`
(declared at `core/rpc/api.go:423`) is assigned exactly once, at
`core/rpc/api.go:1094`, to `&stubPolicy{}`;
every method returns `errNotWired`. No `policy:event` broker topic exists
anywhere in the tree — the string appeared only in the deleted
`usePolicyDecisions` docstring. So when a Cedar policy denies an action,
the user sees whatever raw error string happens to reach the calling
surface, with no reason, no policy name, and no remediation affordance.

This is trust-relevant by the sweep's own rubric ("**trust- or
compliance-relevant** (consent, permissions, denials, audit)"), which is
exactly why the fix is a mission and not a re-mount: mounting a component
fed by a stub that returns `errNotWired` would move the lie from the
backend to the UI. Wire `policyAPI` to a real implementation and define
the denial event contract **first**; the component is the cheap half.

**Owner:** unassigned — needs a mission under `kitty-specs/`. The change
that deletes this entry is the one that gives `policyAPI` a non-stub
production assignment.

### 2026-08-14 · `LocalRuntimesSection` has a branch it can never render

`LocalRuntimesSection.vue` renders three mutually-exclusive states per
runtime card. The first one is unreachable in production:

```
v-if="rt.running && rt.models && rt.models.length > 0"   → "N models available"
v-else-if="rt.running"                                   → "No models detected (runtime is running)"
v-else                                                   → "Installed but not running …"
```

`LocalRuntimeInfo.Models` (`core/rpc/views/llm/api.go:153`) is **never
populated on the listing path**: `runtimeInfosToWire`
(`core/rpc/views/llm/impl_local_runtime.go:181`) — the sole converter,
called from both listing sites (`:33` and `:126`) — copies `Kind`,
`Name`, `Running`, `Installed`, `DefaultBaseURL` and `Port`, and never
sets `Models`. The upstream `localruntime.RuntimeInfo` has no `Models`
field to copy from in the first place.

So a running runtime **always** falls through to "No models detected
(runtime is running)", even when the user has models installed. This is
not an orphan — `LocalRuntimesSection.vue` is a **shipping** surface, so
this is a live cosmetic lie rather than dead code, which is why it is
recorded rather than deleted.

Note the neighbouring `runtimeModelsToWire` (`:197`) does populate a
`Models` field, but on `LocalRuntimeModels` — a different wire type on a
different RPC. The listing path never calls it.

**Owner:** unassigned. Either populate `Models` in `runtimeInfosToWire`
(needs a per-runtime model probe at list time) or delete the first branch
so the card stops implying a state it cannot reach. Do not "fix" it by
deleting the string alone — the probe is the feature.

### 2026-08-14 · Settings fields that are stored, bound, and inert

Each has a persisted field and (usually) a Wails binding, and changes
nothing. Grouped by why:

*No implementation at all:*
`PermissionMode` (the documented "every call prompts" / "all non-dangerous
permitted" semantics are unimplemented — `EffectivePermissionMode()`'s only
non-test callers are the two store accessors `FileStore.LoadPermissionMode`
/ `memoryStore.LoadPermissionMode`, whose only caller in turn is the
`Settings_GetPermissionMode` binding, so the value round-trips to
`PermissionDialsPanel.vue` and nothing ever branches on it),
`MCPAutoRestartDisabled` (the stdio supervisor calls
`attemptRestart()` unconditionally; nothing in `core/mcp` reads any settings
gate), `SkippedUpdateVersions` (the doc claims the updater filters these
out; no filter exists), `LocalRuntimeRAMOverrideGB`
(`EffectiveLocalRuntimeRAMBytes` has zero callers).

*Consumer lives in an orphan package:* `CedarStrictCredentialMode`,
`CredentialAuditRetentionDays` (both reach `core/credstore`, an I7 orphan).

*Narrative tuning knobs whose code paths use hardcoded defaults:*
`SummarizerProfileID`, `NarrativePromotionWeights`,
`NarrativePromotionThreshold`, `NarrativeRetrievalWeight`,
`NarrativePromoterParallelism`, `NarrativePreludeTopN`.

*Self-documented as reserved (fine, listed for completeness):*
`KeyboardShortcutsPreset`, `BranchAdvisorUseLLM`, `BranchAutoMode`.

**Owner:** unassigned. The cheapest structural fix is to bring
`settings.Settings` under `core/wiring/knobcoverage` — see the next entry.

### 2026-08-14 · knob-coverage tracks one struct out of the several that need it

`core/wiring/knobcoverage` is a general mechanism, and exactly one struct
uses it: `autonomy.ResolvedKnobs` (9 fields, all genuinely live).
`settings.Settings` — the largest knob surface in the tree, ~78 exported
fields — is outside it entirely, which is why the entry above was found
by hand rather than by CI. `RegisterDeferred` is also an unbounded escape
hatch: no allowlist file, no dates, no monotonic-shrink rule.

*(Updated 2026-08-14: this entry previously counted `dials.DialConfig`'s
13 fields alongside `settings.Settings`, for "9 out of ~98". The dials
cascade was deleted by orphan-deletion sweep wave 2 — see Drained — so
that arm is gone and the ratio is smaller than recorded. The
`settings.Settings` gap is unchanged and is the whole of it now.)*

The vacuous-pass hole was closed this sweep (the gate now requires a real
guard test outside the mechanism's own package). The coverage gap is not.

### 2026-08-14 · The background-task subsystem has no producer

*(Corrected 2026-08-14 by the frontend orphan-deletion sweep — "a
session-close dialog" below is only half true; see the correction after
the summary.)*

`core/tasks` ships a SQLite store, ring buffers, boot-time orphan recovery,
four live RPCs, a mounted Settings → Tasks panel, a session-close dialog and
a registered `background_task_complete` hook event — and nothing ever calls
`Registry.Register`, because `bash.Options.BackgroundSpawn` has no non-test
assignment. `Registry.StdoutWriter` / `StderrWriter` likewise have no
callers: `spawnBackground` calls `cmd.Start()` before it has a task id, so a
background task could not capture output even if the seam were passed.

**Correction: the Tasks panel half was true when written, and is no
longer.** `TasksPanel` was mounted in `SettingsView.vue` behind
`?tab=tasks` when this entry was first written; the follow-up below
removed that mount along with the nav entry, so as of 2026-08-14 the
panel is **retained but unmounted** — the only remaining reference in
`frontend/src` is the comment at `SettingsView.vue:131-133` and the
component's own `__tests__/TasksPanel.spec.ts`. Re-verified on the
merged tree. `components/sessions/SessionCloseDialog.vue` likewise has
**zero importers** anywhere in `frontend/src` — no route, no parent
component, no test. Both are now exactly as unreached as the Go producer
side they were meant to gate.

Consequences already addressed this sweep: `kenaz__bash` no longer
advertises `run_in_background` while the seam is nil (commit `fix(tools):
stop advertising bash background mode…`), and `kenaz__monitor` is now
tracked by I11 rather than buried in the I7 bulk list.

**Owner:** unassigned. The fix is one restructuring — allocate the task id
before `cmd.Start()`, attach the registry writers, pass
`BackgroundSpawn`/`BackgroundEnd` and the `HookFirer` from `core/rpc`, then
register `kenaz__monitor` with its predicate case.

**2026-08-14 · Follow-up — the Settings → Tasks *nav entry* is removed.**
The producer gap above is unchanged, but it was reachable: `SettingsTabs.vue`
rendered a visible "Tasks" link under the Runtime group, and `SettingsView.vue`
mounted `TasksPanel` behind `?tab=tasks`. A user could click it and get a
permanently empty panel — the lie this ritual exists to end. Removed: the
nav entry + its `CheckSquare` import, the `showTasksTab` computed, the
`tasks` `SECTION_HEADS` row and the template branch. Pinned by a new spec in
`SettingsTabsNav.spec.ts` ("does not offer a Tasks entry"); the rail count
moved 24 → 23.

Producer-absence proof re-confirmed at removal time, both arms:
`core/tools/bash/Options.BackgroundSpawn` has assignments only in
`run_in_background_test.go`; the sole `Register` call into a tasks registry
is `core/tools/subagentdispatch/tool.go:240`, guarded by `opts.Tasks != nil`,
and that field's only production assignment is `Tasks: nil`
(`core/rpc/builtins_wiring.go:317`). `tasksview.NewAPI(taskReg)` therefore
serves an always-empty registry.

**Disposition: parked, not deleted** — `core/tasks`, the four RPCs and
`TasksPanel.vue` all stay. This is an *escalation*, not a delete: whether
background execution ships at all is a product call, and deleting the
consumer half of a wanted feature destroys tested work. Removing the *link*
is correct under either outcome. If background execution ships, remount the
panel and restore the nav entry in the same PR that wires the producer.
**Owner:** unassigned — same owner as the parent entry.

### 2026-08-14 · `cedar.CheckLLMFallback` — the LLM fallback chain is ungated

The highest-priority wire on this list. `core/llm/fallback`'s Runner is on
the live chat path, constructed as `fallback.NewRunner(a.reg,
&fallback.StoreResolver{})` with no options — so `r.checkPolicy` stays nil
and every fallback hop issues unevaluated, while `runner.go`'s own doc says
"WithPolicyCheck wires the Cedar gate. fn should call
cedar.CheckLLMFallback" and `cedar/types.go` says "the loop calls
CheckLLMFallback before issuing each hop". Neither happens. The Runner's
`WithBlockedHook` audit path is unfired for the same reason.

Tracked on `i10-unwired-gates.txt` with seven siblings surfaced by the same
vocabulary widening — three more WIRE verdicts (`CheckSQLiteVersion`,
`CheckManifestDrift`), three DELETE verdicts (`cedar.CheckTool`,
`cedar.CheckModel`, `cedar.CheckRecipeAdd`, `fleet.VerifySignature`) and one
blocked on I7. Read those entries before touching any of them; each carries
its evidence.

### 2026-08-16 · The workflow strictness dial has no UI, only a settings key

Closing audit finding A2 needed a producer for the `mode` context attribute
`default_workflows_policy.cedar` branches on. That bundle is **embedded in
every engine** (`engine.go`'s `defaultWorkflowsPolicySource`, not a
user-installed template as the audit's correction paragraph states), and its
strict arm forbids saving a shell-bearing workflow. Nothing outside tests
ever set the attribute, so the arm shipped to every user and could not fire.

The producer now exists end to end: `settings.CedarStrictWorkflowMode` →
`workflowCedarModeFn` → `workflowsview.Config.CedarModeFn` → the gate, read
live on every run/save. It is settable by editing `cedarStrictWorkflowMode`
in the harness settings file, and covered by
`TestCedarWiring_WorkflowStrictMode_IsReachableFromSettings`.

**What is still missing: the UI dial.** This deliberately follows the
`CedarStrictCredentialMode` precedent (`views/settings/api.go`), whose own
comment says "the UI dial for this setting is a follow-up; the binding is
wired now". Here even the Wails binding is deferred — adding one requires
regenerating `frontend/wailsjs/` with the Wails toolchain plus a
`wailsjs-bindings.sha256` bump, which is a mission, not a drive-by mount.

- **Blocker:** whether the harness should surface a fail-closed workflow
  posture at all, and where, is the same product question as
  `Options.DefaultDeny` (deliberately `false`, `api.go`'s `buildCedarGate`).
  Both dials should be designed together or not at all.
- **Owner / deleting change:** a Settings → Workflows panel mission that
  surfaces both strictness dials, adds
  `Settings_{Get,Set}CedarStrictWorkflowMode`, and deletes this entry.

Until then the policy file's header says exactly this, and no longer claims
a "Settings → Workflows panel" that does not exist.

### 2026-08-16 · Each Cedar gate builds its own Engine, so the audit panel sees a fraction of decisions

Surfaced while wiring A1/A2, not fixed here. `buildCedarGate` constructs a
**fresh** `cedar.Engine` per call — nine call sites reachable from `rpc.New`
(`grep -c 'buildCedarGate(' core/rpc/api.go` minus the definition), plus four
more Engines from `buildCedarEngineOrNil` — and each
Engine owns a private `MemoryDecisionStore`. `views/cedarpolicy`'s
`RecentDecisions` reads one engine, built separately via
`buildCedarEngineOrNil`. So the decisions the user can actually review are
only those from the engine the policy view happens to hold; memory-write,
model-select, workflow and scheduled-chat denials are recorded into stores
nothing reads. A `Reload` triggered from the policy editor likewise refreshes
only that one engine — the other gates keep their boot-time PolicySet until
the app restarts.

This was pre-existing (four `buildCedarGate` sites before this change) and
wiring the remaining sites made it worse rather than introducing it. Left
alone deliberately: sharing one Engine across every gate is the right fix but
it changes reload semantics for live gates, which deserves its own change
rather than riding a wiring fix.

**Reproduced 2026-08-16 (review):** boot `rpc.New` over an empty DataDir,
save `forbid memory_write` through `cedarpolicy.SavePolicy` (the editor's own
entry point), call `ReloadPolicies`, confirm `ListPolicies` reports the file
as loaded — then `memStoreRef.Add` still succeeds. So the sentence "a user
could author a policy … and nothing consulted it" is only fixed for policy
that exists **before the process starts**. The in-session editor flow still
tells the user their rule is live when it is not. That is the same lie class
this sweep exists to end, and it is the reason the entry below is a blocker
and not a nice-to-have.

- **Blocker:** none technical; needs a deliberate decision that a policy
  reload should take effect on live gates mid-session.
- **Owner / deleting change:** hoist a single `a.cedarGate` in `rpc.New`,
  pass it to all nine sites and to the cedarpolicy view, and delete this
  entry. Add a regression test for the in-session flow above at the same
  time — today nothing pins it.

### 2026-08-14 · Known gate holes (not yet closed)

- **`check-output-ports.sh` pass 1 is a bare substring count.** Roughly 15
  of 47 ports are "covered" by literal collision rather than by a reader —
  `Outputs["true"]` passes because 34 unrelated `"true"` literals exist.
  A real regression on `out` / `result` / `next` / `block` would be silent.
- **I7 cannot see orphan clusters.** The imported-set is computed over all
  listed packages including the orphans themselves, so a package whose only
  importer is an allowlisted orphan is invisible. Six such today
  (`core/context/merge`, `core/context/verify`, `core/bundle/kinds`,
  `core/bundle/channels`, `core/policy/engine`, `core/trust/backends`) —
  the allowlist says 36, the true closure is 42. Fix is a fixpoint
  iteration over live packages only.
- **I13 clause 2 cannot do reachability or scope analysis.** A *dead*
  replacement (`if false { g = engine }`) and a replacement to a same-named
  variable in a **different function** later in the same file both satisfy
  "the placeholder is replaced". Found by planting them, 2026-08-16; both
  need a Go AST tool rather than awk, and both require someone to write the
  replacement deliberately — unlike the omission shapes clause 3 covers,
  which happen by accident. The five accidental evasions found in the same
  session (gofmt-wrapped argument, slice-literal element, trailing comment
  or struct tag on the field declaration, a comment standing in for the
  assignment, and an explicit `Field: nil`) **were** closed, each with a
  planted-violation fixture in `gates_can_fail_test.go`.
- **I10's `has_real_callsite` is package-blind.** `grep "Symbol("` across
  all of `core/` with no package qualification: two same-named functions in
  different packages cover for each other.
- **The builtin-tools tripwire asserts on a log line, not the switch.**
  Renaming `rpc.builtins.predicate.unknown_tool` makes it pass with every
  tool denied. It also silently skips unparseable log lines and never
  asserts that any line parsed.
- **`Graph_*` / `Workflows_*` RPCs are not routed in served mode.**
  `check-serve-dispatch-drift.sh` is informational (exit 0) unless
  `SERVE_DRIFT_GATE=1`, so the gap accumulates quietly.

### 2026-08-14 · Tooling footgun: `rtk proxy grep` truncates on a double pipe

New manifestation of the known rtk truncation bug (CLAUDE.md's "Tooling
footguns" documents the plain-wrapper case): **piping one `rtk proxy grep`
into another `rtk proxy grep` also truncates output**, even though each
individually is the documented safe form. During this sweep's import-graph
verification it silently dropped `views/audit/AuditView.vue:16` from a
piped-proxy search for `EventStreamRow` importers, which nearly produced a
false orphan verdict — `EventStreamList.vue` (truly dead) would have taken
`EventStreamRow.vue` and its test down with it had the missing importer
not been caught by a second, unpiped pass. Correct form: pipe into
`/usr/bin/grep`, not into a second `rtk proxy` invocation. Mirrored in
CLAUDE.md's "Tooling footguns" bullet list.

### 2026-08-14 · Frontend orphan backlog (post-deletion-sweep handoff)

The 2026-08-14 frontend orphan-deletion sweep deleted the zero-consumer,
zero-ambiguity items (see the deletion commits on
`fix/frontend-orphan-deletions`). What follows is the remainder — findings
that are real but need an owner decision, not a delete — so the next sweep
inherits this list instead of re-deriving it from scratch.

*(Updated 2026-08-14 by orphan-deletion sweep wave 2. Items resolved by
deletion moved to Drained. One claim below was **false** and is corrected
in place — see the Cedar-propose note.)*

**P1 — slated to be finished** (owner decision: these are real features;
the backend is live and only the UI is missing, or they are the only
surface for a real capability). Do **not** re-find these as orphans:

- `RecoveryCodeFlow` — backend assigned unconditionally at
  `core/rpc/api.go:2426`; keychain-only, works offline. Only recovery
  surface in the product.
- `ProjectAutonomyPanel` — the project rung is engine-consumed at
  `core/rpc/api.go:4304`.
- `HookJournalView` — rows **are** being written to SQL in production;
  the read path is what is missing.
- `MCPHealthSettingsPanel` + `BranchAdvisorSettings` — both blocked on
  inert Go knobs (see "Settings fields that are stored, bound, and
  inert" above). Wire the consumer first, in the same PR.
- `CrashReportingOnboardingModal`.
- `CedarEditor` — retained pending the mission that ports its fleet
  features into `PolicyView`. It is **not** an orphan to delete, even
  though `lib/cedar/permissionCatalog.contribution.ts` (deleted this
  sweep) turned out not to be reachable from it.

**P1b — parked pending a named mission.** Owner wants each of these
capabilities on the roadmap; the components are kept deliberately. The
next sweep should skip them, not re-derive them:

- **Delegated sub-agent execution** — `AgentsView.vue`,
  `AgentProfileEditor.vue`, `SubagentTab.vue`, `SubagentBudgetMeter.vue`.
  Parked 2026-08-14 pending that mission.
- **Background execution** — `TaskOutputViewer.vue`,
  `SessionCloseDialog.vue`, `BackgroundTaskChip.vue`. Parked 2026-08-14
  pending that mission (see the background-task entry above).

**P3 — remaining owner-decision items:**

- `lib/capability-keys.ts` — do **not** delete the `.ts`: it is
  generated, invoked by `//go:generate` at `core/fleet/capability_gen.go:13`
  and enforced by `scripts/ci/check-codegen.sh`, which runs in `pr.yml`.
  Being wired as a typed import instead.

**CORRECTION 2026-08-14 — the Cedar-propose "advertised to the model"
claim was FALSE.** The previous revision of this entry said
`harness_write_propose_cedar_policy` "is registered and advertised to the
model". It was registered (`core/mcp/builtin/harness/register.go:138`)
but **never advertised**, because the harness-self MCP server that owns
it was constructed at `core/rpc/api.go:2604`, logged for its tool count
at `:2608`, and then never attached to any session pool —
`harnessServer.Server()` (`core/rpc/harness_wiring.go:290`) had zero
callers, and the comment at `api.go:2590` conceded the in-process
transport wiring (WP09) had not landed. So the model never saw the tool
and `errNotConfigured` fired on exactly zero calls: it was **dead code,
not a live lie**. The stack was deleted this sweep (see Drained). Recorded
because the retracted report that raised it asserted the opposite, and the
difference is the difference between "urgent" and "housekeeping".

**Owner:** unassigned per-item; this entry itself is owned by whoever
picks up the next unwired sweep. Re-verify each item's importer graph
before acting — frontend code churns between sweeps.

### 2026-08-18 · Custom-recipe authoring (A5) is flagged off, dated interim state

`mcp-connector-lifecycle-01PMMC01` WP02 closed the A5 lie (a row Edit button
and a Custom-recipe tab that both opened a form whose Save unconditionally
threw) by gating both doors behind one flag —
`CUSTOM_RECIPE_AUTHORING_ENABLED` in
`frontend/src/lib/customRecipeAuthoring.ts`, read by both
`KenazToolsPanel.vue`'s per-row Edit button and
`AddMCPServerModal.vue`'s Custom tab (same injected
`CustomRecipeAuthoringKey`, so the two doors cannot drift apart). The flag
ships **`false`** — there is still no `MCP_SaveCustomRecipe` RPC for the
form to call.

This is not a permanent default-off per CLAUDE.md's flag rule: it is a
dated interim state with a named retirement condition.

- **Blocker:** `MCP_SaveCustomRecipe` does not exist. Landing it — view
  method → `core/rpc/bindings.go` → `harnessClient.ts` wiring, persisting
  through the already-implemented `recipes.UserStore.Save`
  (`core/mcp/recipes/user.go:487`) — is `mcp-connector-lifecycle-01PMMC01`
  WP06, which is conditional on WP01's B10 decision record and was **not**
  in this dispatch's scope (WP02/WP03/WP04/WP05 only).
- **Retirement:** flip `CUSTOM_RECIPE_AUTHORING_ENABLED` to `true` (or
  delete it and the `v-if`s that read it, if the Custom tab is redesigned
  away instead) in the same commit that lands WP06. See
  `kitty-specs/mcp-connector-lifecycle-01PMMC01/spec.md` FR-006 / AC-007.
- **Owner:** whoever picks up WP06 — unassigned as of this entry; escalate
  to the mission owner if WP06 has not been dispatched by the next sweep.

### 2026-08-18 · `recipes.UserStore.StartWatch` deleted — a live substitute made it redundant

`mcp-connector-lifecycle-01PMMC01` WP04 deleted `UserStore.StartWatch`,
`watchLoop`, the watcher arm of `Close`, and `ErrAlreadyWatching`
(`core/mcp/recipes/user.go`), plus their four `user_test.go` regression
tests — the method's only readers.

This is **not** the "no producer and no product intent" delete class:
the producer (paste-config import) is real and shipping. The
justification is the **live-substitute** class instead —
`mcp-connector-lifecycle-01PMMC01` WP03, landed in the same mission
immediately before this WP, wired every merged-recipe-catalog consumer
(`core/rpc/api.go`'s chassis catalog, the import-collision reader, the
boot-time recipe bootstrap, and `tools.Config.Catalog` — what
`Tools_ListRecipes` reads) to reload `UserStore` from disk on **every
call**, matching `core/mcp/connectors.CatalogWithUserRecipes`'s existing
served-mode contract. Once every consumer already re-reads live, there is
no cached state left for `StartWatch`'s debounced `onChange` callback to
invalidate — wiring it would have started a real fsnotify goroutine (with
its own idempotency and shutdown-lifecycle burden — `(*rpc.API).Shutdown`
turned out to have the same never-called gap `docs/dead-code-audit-2026-
08-16.md` found elsewhere) that pushed updates nothing was polling off
of.

**Positive no-consumer proof:** `grep -rn 'UserStore.Close\|\.StartWatch(' core/ --include=*.go`
(pre-deletion) showed the only callers of both were the four deleted
tests; no production code called either.

**Blocker/owner:** none — this is a closed, dated justification, not a
parked item. If a future need for a genuine background push (e.g. an
external process editing recipe files that this process must react to
mid-request rather than on its next catalog read) resurfaces, re-derive
the watcher fresh against whatever the freshness contract looks like at
that time rather than restoring this deleted code verbatim — the
`onChange`-to-cache-invalidation shape assumed a cache that no longer
exists.

---

## Drained

### 2026-08-16 · what the export scanner covered BEFORE, and what leaked

Closes the 2026-08-14 finding above. Recorded in full because
**exports taken from any build before this change may contain live
credentials on disk**, and that is a user-facing fact, not a code note.

**What the scanner covered BEFORE (146d9e54).** `redactMessages` walked
exactly three things per message and nothing else in the export:

1. `Message.Content`
2. each `ToolCall.Result`
3. each TOP-LEVEL string in `ToolCall.Arguments` (a `map[string]any` or a
   `[]any` value was copied through unscanned; keys were never scanned)

Against a catalog of ten patterns: AWS `AKIA` ids, a 40-char AWS secret
in `key=value` form, JWTs, `Bearer`/`Basic`, PEM private-key blocks,
`ghp_`-style GitHub tokens, `sk-`, `sk-ant-`, and a
`(?:password|secret|apikey|api_key|api-key|token)\s*[:=]\s*…` generic.

**What actually leaked**, reproduced on 146d9e54 with a throwaway probe
that rendered both formats and searched the resulting bytes:

| shape | markdown | json |
|---|---|---|
| credential in the session **title** | LEAKED | LEAKED |
| credential in the **system prompt** | n/a | LEAKED |
| credential in an attachment's **`original_name`** | n/a | LEAKED |
| credential in an attachment's **`uri`** (presigned URL) | LEAKED | n/a |
| credential in the **tool name** | LEAKED | LEAKED |
| `{"aws_secret_access_key": "wJalr…"}` in a tool **result** | LEAKED | LEAKED |
| the same key in `key=value` form | redacted | redacted |
| provider-shaped key in content / result | redacted | redacted |
| secret nested in an argument object / array / used as a key | redacted | redacted |

Two corrections to the 2026-08-14 entry, both from running the probe
rather than reading the code:

- **The argument leak it describes is no longer reachable.** v0.63.0
  (`ExportFormatVersion` 1 → 2) stopped printing argument VALUES, and
  `argsSummaryFromValues` already scanned the NAMES. The nested/array/key
  shapes it reproduces against `8c8b63a9` do not reach either file today.
  The shallow walk was real; its consequence was not.
- **The session ROW was never scanned at all**, by anything. That is the
  bigger half of the finding and it is not in the 2026-08-14 entry:
  `redactMessages` is named for what it walks, and `Render` called
  nothing else. `Record.Name` reaches the markdown H1, the JSON
  `session.name`, **and the filename offered to the OS save dialog** via
  `DefaultFilename`, which the only production caller feeds the raw name.

**What the scanner covers now.** The message walk is recursive over
nested objects, arrays and map KEYS (bounded at `MaxRedactDepth = 24`,
cycle-guarded, failing CLOSED at the bound); the session row, attachment
`original_name` / `uri` / block text, and tool names are scanned; and a
key that NAMES a secret (`authorization`, `cookie`, `set-cookie`,
`x-api-key`, `api_key`, `*_token`, `*_secret`, `password`, `passphrase`,
`private_key`) forces its value to be redacted whether or not the value
matches a pattern. The catalog gained: the full AWS unique-id prefix set,
GitHub fine-grained PATs, `sk-proj-`/`sk-svcacct-`/`sk-admin-`, Google
`AIza`, Slack `xox[abposr]-`, Stripe `sk_live_`/`rk_test_`, and inline
passwords in connection strings.

The single highest-value change is the smallest: `["']?` before the
separator. The old key-name matcher required `name<colon>value` with
nothing between, so `{"password": "hunter2"}` never matched — the key's
CLOSING QUOTE was in the way. Every structured tool result in this app is
JSON, so in practice that matcher only ever fired on shell, env-file and
query-string text.

**Still open, deliberately, each with the reason:**

- **`\b` finds no boundary after `_`.** A credential glued to a prefix
  with an underscore (`myprefix_sk-ant-…`) is not matched by the
  provider patterns. Real credentials are preceded by `"`, `=`, `:` or
  whitespace in every shape observed; widening the boundary costs
  precision everywhere for a case nobody has produced. Pinned by the
  fixture comment in `redact_leak_test.go` so the next reader knows it is
  a decision.
- **A TRUNCATED PEM block is not matched.** The pattern requires the
  `-----END … PRIVATE KEY-----` marker, and `capToolOutput` truncates a
  tool result at 4000 runes. Making the END optional would let the word
  "BEGIN RSA PRIVATE KEY" in prose redact the rest of the document.
  Owner: unassigned; the fix is a length-bounded variant, not an
  open-ended one.
- **`core/event/redact.defaultMatchers` has NOT been widened.** The
  export catalog began as a copy of it and has now diverged; that one
  still has all ten original patterns including the JSON-blind generic.
  It feeds the audit log's HMAC pipeline, which is a different contract,
  and widening a live audit pipeline from inside an export fix is the
  wrong blast radius. Owner: unassigned. **If you are auditing what the
  event log redacts, do not read the export catalog and assume parity.**
- **`core/eval/capture.go:137` `redactString` is much weaker than any of
  the four catalogs** — it handles `sk-` and `Bearer ` and nothing else,
  no GitHub token, no AWS key, no JWT, no password, no cookie — and it
  writes LLM messages to disk. Its own comment calls it "defense-in-depth"
  behind the event log, which is true of the event-log path and not of
  the capture FILE. Owner: unassigned. Gated behind eval capture being
  enabled, which is why it is recorded rather than fixed here.
- **`Handoff_Share` does not run through any scanner.** Unchanged and
  correct for now: it is E2E-encrypted to a recipient the user picks, and
  per the 2026-08-14 entry above it currently transmits a literal empty
  event list. If a payload builder ever lands, redaction is a product
  decision (what may cross the fleet boundary), not an automatic yes.

**Cost.** Markdown export of a 400-message, ~3.8 MB session, darwin/arm64,
`-benchtime=20x`: 556 ms/op before → **485 ms/op** now for a transcript
with no key-name anchor words, **973 ms/op** for one containing all seven.
The typical case is faster than the code it replaces despite scanning for
eleven more shapes, because `RedactValue` gained a literal prefilter
(`credMatcher.anchor`) and a `FindStringIndex` guard that stops
`ReplaceAllStringFunc` from copying the whole string once per matcher when
nothing matched. The 20-way case-insensitive alternation the key-name rule
started as measured 203 ms/MB on its own — twenty times every other
matcher — which is why it is seven anchored matchers instead of one.

- **2026-08-14** (orphan-deletion sweep wave 2) — eleven surfaces
  deleted, each with positive no-consumer proof and a named rubric
  class. Every premise was re-verified from the tree first: the report
  that originally nominated these was retracted by its author for
  fabricated citations, so nothing here rests on it.
  - **The dials cascade** — `core/agentgraph/dials`,
    `core/rpc/views/dials`, the four `Dials_*` bindings, the
    `DialsClient` surface, `DialScope`/`DialConfig`/`DialEffectiveDials`/
    `DialDelta`, `views/dials/DialsView.vue` and
    `components/chat/CapHitToast.vue` (+ both tests). Class: **rival
    infrastructure** — `core/autonomy` is a second cascade of identical
    shape with three mounted panels. Proof: `coredials.Resolve` had
    exactly one caller (`views/dials/impl.go:116`, inside the view
    package it fed); `core/rpc/api.go:1726` constructed the view as
    `dialsview.New(dialsview.Config{})`, an empty Config, so the resumer
    and persister were both nil (`BumpAndResume` never resumed,
    `SetDials` never persisted across restart); `env.Budget`'s only
    production assignment is `chat_runner.go:899`
    (`applyTokenCeilingKnob(graph.Budget, resolvedKnobs)`), which never
    reads `EffectiveDials`; `CapHitToast.vue` had no mount site, and the
    kernel's `emitCapHit` (`kernel.go:1577`) only appends to the internal
    EventLog with no broker publish, so it had no possible trigger.
    `ManifestDriftMode` had no home to begin with — the I10 entry for
    `EnforceManifestDriftPolicy` was rewritten to say so.
  - **`views/audit/RunChainView.vue`** — class: **live substitute**
    (`AuditView.vue` is routed at `/audit` in *both* entry points,
    `main.ts:41` and `main-served.ts:50`) **plus dead subsystem**
    (`audit.Entry` and `audit.Filter` carry no session or run id, so a
    per-run chain view could not filter even if mounted; it would have
    rendered the global audit ring under a per-session title). Zero
    references repo-wide, and no test existed — confirmed.
  - **`views/providers/ModelSizeBadge.vue` + `lib/modelFit.ts`** (+ both
    tests, + the two cases in `local_runtime_e2e.test.ts` that were
    `modelFit`'s only other readers). Class: **dead subsystem** — all
    three inputs unreachable: `Models` is never populated on the listing
    path (`runtimeInfosToWire`), `core/system/resources` has zero Go
    importers (its only mention is a *comment* at
    `views/settings/api.go:1586`), and `EffectiveLocalRuntimeRAMBytes`
    (`views/settings/api.go:1591`) has zero callers. The component
    rendered nothing even if mounted — `v-if="displayText"` over empty
    inputs. The deleted e2e case was itself a small lie: it was titled
    "ModelSizeBadge integration smoke … the badge is rendered inline"
    but never mounted the badge, only called `modelFitsInRAM` directly.
    The live gap this leaves is recorded above as the
    `LocalRuntimesSection` entry.
  - **`components/sessions/TraceView.vue`** + its 5-case spec. Class:
    **dead subsystem** — `session.Message.{ActualProvider,ActualModel}`
    (`core/session/types.go:229,232`) have zero writers repo-wide *and*
    are absent from the sessions wire shape, so the data could never
    cross even if something produced it. `lib/types.ts` declared a
    frontend-only phantom.
  - **`components/ui/DenialNotice.vue`** + its test + the ~20 dead lines
    inside the live file `lib/useHarnessAPI.ts` (`policyDeniedHandlers`,
    `usePolicyDecisions`, `_emitDenialForTest`, `UsePolicyDecisionsResult`
    — an orphan-*file* scan would never have surfaced these). Class:
    **dead subsystem** — `policyAPI` is assigned exactly once, to
    `&stubPolicy{}` at `core/rpc/api.go:1109`, every method returning
    `errNotWired`; no `policy:event` topic exists; the handler Set was
    fed only by `_emitDenialForTest`, which itself had zero callers. The
    product gap this leaves is recorded above as its own finding.
  - **`components/chat/BranchContextPickModal.vue`** — class: **live
    substitute**. The banner's "Branch it off" action is fully wired to
    `CreateBranchModal` (`ChatInput.vue:243` `handleBannerBranchOff`,
    bound at `:1206`, rendered at `:1279`); only a stale comment in
    `BranchSuggestionBanner.vue` still named the dead modal, and it has
    been corrected to describe what actually happens. Independently, the
    modal's two headline fields (`contextItems`, `toolGrantMode`) do not
    exist in Go at all (`core/rpc/views/branches/api.go`) — they
    unmarshalled into nothing.
  - **`views/compaction/CompactionSettings.vue`** + its test — class:
    **live substitute**, nothing more. `views/settings/compaction/
    CompactionStrategyPanel.vue` is mounted at `SettingsView.vue:1086`
    and is a strict superset. Note for the record: this is **not** an
    inert-settings finding — `Compaction_SetConfig` writes into the live
    pipeline resolver. The ledger never claimed otherwise; do not let a
    future sweep introduce that framing.
  - **`lib/rail.ts`** — class: **rival infrastructure**.
    `registerRailEntry` / `listRailEntries` had zero call sites
    repo-wide; `LeftRail.vue` inlines its nav via a `RailEntry.vue`
    component (a different, live thing). The file's own docstring
    conceded it: "LeftRail currently inlines defaults; this registry is
    the v1.x extension point."
  - **`lib/cedar/permissionCatalog.contribution.ts`** + its test —
    orphaned from everything, including `CedarEditor.vue`. `CedarEditor`
    itself was **kept** (see P1 above).
  - **The Cedar propose stack**, dead at all four layers: the tool
    registration + `ToolProposeCedarPolicy` const
    (`core/mcp/builtin/harness/register.go`), the `CedarProposer`
    interface + `Managers.CedarProposer` field +
    `handleProposeCedarPolicy` (`handlers.go`), the whole
    `cedar_proposer.go` impl + its test, the RPC layer
    (`CedarProposeResolver`, `ErrCedarProposeNotWired`,
    `CedarProposeResolve`, `SetCedarProposeResolver`,
    `CedarPolicy_ResolvePropose`), and
    `components/permissions/CedarProposeModal.vue` +
    `CedarProposalPayload`. Class: **dead subsystem, no producer**.
    Proof: `NewCedarProposer` had zero non-test call sites, so
    `Managers.CedarProposer` was always nil and the handler could only
    return `errNotConfigured`; `SetCedarProposeResolver` had zero
    callers, so `CedarProposeResolve` could only return
    `ErrCedarProposeNotWired`; the sole publisher of the
    `cedar:propose-pending` topic lived inside the deleted
    `cedar_proposer.go`; and the modal had no mount site. **No live lie
    existed** — see the correction above; the tool was never advertised
    to the model. `TestRegisterAll_HappyPath`'s canonical tool count
    moved 14 → 13.
- **2026-08-14** (01PMCH01 WP06) — the **search corpus**, and with it
  the last non-WP05 line on the mission's in-flight list. Migration
  0312's FTS triggers had no role predicate, so every `tool_call` row's
  synthetic args-summary string and every `tool_result` row's raw output
  had been full-text indexed since WP02 — BM25 ranking against a corpus
  that had become mostly machine output, and a cross-session grep over
  every tool payload the harness ever received. Migration 0335
  (`core/session/migrations_search_fts_tool_rows.go`) re-guards all
  three triggers on `role <> 'tool'` and evicts the rows already
  ingested — the eviction is the half a trigger-only migration would
  have missed, and `'rebuild'` is not a substitute for it because
  rebuild re-reads the content table and would put the tool rows
  straight back.
  The update trigger is **one trigger carrying two independently guarded
  statements** (`:135-141`), not two triggers. WP06's first version used
  two — `messages_fts_au_del` and `messages_fts_au_ins` — and its
  adversarial review (commit `44d7649f`) found that shape is the one
  shape that cannot work: SQLite leaves the firing order of two triggers
  on one event undefined and in practice fires them in reverse creation
  order, so the insert ran first and the delete second, and every term
  the two row versions share netted to zero. Because `session_messages`
  is UPDATEd on paths that never touch content — `core/usage/usage.go`
  writing token counts onto the assistant row of every turn that reports
  usage, `ApplyCompaction` flipping `archived_at`,
  `MarkStreamingFailure` setting a flag — that silently removed every
  assistant turn from the index. The two halves genuinely do need
  independent guards (`old.role` for the delete, `new.role` for the
  insert), which is why they are expressed as `INSERT … SELECT … WHERE`
  inside one body: statements in a trigger body run in written order.
  Pinned by `TestMigration0335_UpdatingANonToolRowKeepsItIndexed`.
  Contract chosen, and why not the other one: tool OUTPUT never reached
  `session_messages` before WP02, so removing it from the index restores
  the corpus every prior release shipped rather than deleting a
  capability. Narrowly (commit `6a409d27`): `role='tool'` rows are not
  brand new — since v0.21.7 the interrupt path has written one synthetic
  "cancelled: interrupted by user" row per cancelled call and 0312
  indexed those, so the purge does remove rows earlier releases carried.
  They hold no user language and the sibling assistant row keeps its
  `[interrupted by user]` marker, so the product judgement is unchanged;
  the claim is "the corpus that shipped", not "every row that shipped".
  Indexing tool rows with a UI opt-out was rejected because the ranking
  damage happens with the filter OFF, which is the default, and because
  a `tool_call` row's content is `displayArgsSummary` output — a
  synthetic display string with no user language in it and no query for
  which it is the right answer. `SearchModal.vue`'s existing
  User/Assistant/System filter is now exactly the corpus; no Tool option
  was added, because it would be a control that can only return nothing.
  (The deep-link consequence of that decision is an open finding above:
  `?role=tool` is still accepted from the URL.) Pinned by
  `TestMigration0335_EvictsRowsIndexedBeforeIt`, which manufactures a
  dirty pre-migration index through the migration's own Down and asserts
  the state is dirty before asserting it is clean.
- **2026-08-14** (01PMCH01 WP06) —
  `core/agentgraph/compaction/wiring/store.go`'s `toolUseID` (`:131`),
  the one shipped consumer of the `Role == RoleAssistant` idiom for
  "this row opened a tool call". It reported `""` for every move-borne
  call, emptied `snapBoundaryForToolPairs`'s openers map, and turned the
  whole tool-pair boundary clamp into a no-op on both the threshold and
  rolling flows. It and its mirror (`:149`) now switch on `m.MoveKind()`
  (`:135`, `:155`). Drained as a defect fix, **not** as a class fix: the
  idiom is still legal everywhere else in the tree and no gate sees it.
  The class, the one live surface still drifting on it, and the gate
  that is owed for it are recorded together above under
  "`Role == RoleAssistant` is a staleness class".

- **2026-08-14** (01PMCH01 WP04) — `sessions.Message.{Kind,MoveIndex,
  TurnSpanID}` and their `frontend/src/lib/types.ts` mirror (populated by
  `messageToView` on every row since WP01, read by nothing — and, after
  WP02 started filling the columns, actively rendering wrong: ~13
  unlabelled assistant bubbles per turn with raw tool output flattened
  into the conversation). Now the input to `projectTranscript`, which
  folds a turn's moves into a trail of steps + tool chips and leaves the
  answer as the full bubble. Also drained: `llm.StreamMoveStart` +
  `llm.MoveBoundary` + `llm.StreamEvent.Move` and their kernel mirrors
  `agentgraph.StreamEventMoveStart` + `StreamEvent.{MoveIndex,MoveKind}`
  — emitted on every chat turn by the move journal and ignored by the
  surface; `useSession` now branches on the kind, opening a fresh bubble
  per boundary. That deleted the `existing.content + delta` glue that
  produced the run-on paragraph in spec §1; there is no flag that brings
  it back. The sequencing constraint recorded against these lines (no
  release tag between WP02 and WP04) is cleared.
- **2026-08-14** — the whole `kenaz__ask_user_question` return leg. Three
  independent breaks, each of which alone made the tool useless:
  (1) `core/rpc/api.go` passed `a.elicitAPI` to `newLLMStack` ~500 lines
  BEFORE it assigned it, so the tool was registered default-on with a nil
  Delegate and answered every call `"not_wired … will return once WP04
  lands"` — construction order fixed, pinned by
  `TestAskUserQuestionDelegateIsWired`;
  (2) the eleven components under
  `frontend/src/components/dialogs/AskUserQuestion/` had **never** been
  imported by anything (`git log -S` over `frontend/src` finds no import,
  ever) — `AskUserQuestion.vue` is now mounted in `App.vue` beside
  `ConfirmToolModal`, pinned by `AppElicitationMount.spec.ts`, which mounts
  `App.vue` rather than the dialog so "the dialog renders" can never again
  be mistaken for "the user can answer";
  (3) served mode dispatched `Elicit_ListPending` but not
  `Elicit_SubmitAnswer`, so a workbench could see the question and not
  reply — added, pinned by
  `TestRPC_ElicitSubmitAnswer_ReleasesTheParkedCall`, which asserts the
  blocked `OpenDialog` returns with the answer rather than that the RPC
  returned 200.
  Also fixed en route: the frontend pre-`JSON.stringify`'d the answer into a
  `json.RawMessage` parameter, double-encoding every value (the model's
  schema promises `["a","b"]` for a checkbox and would have received the
  string `"[\"a\",\"b\"]"`), and the question-input subtree was unkeyed, so
  a second question of the same kind reused the first question's child and
  submitted a stale answer. Neither had ever run in the app.
  Still open from the same surface: deferred + wizard modes, and the
  todo / sub-agent UI — see the ungated findings above.
- **2026-08-14** (01PMCH01 WP02) — `agentgraph.HistoryEntry.{MoveKind,
  MoveIndex,TurnSpanID}` (carried end-to-end by the seam while every call
  site passed it zero) now stamped on every chat turn by the runner's
  move journal; `session.TranscriptEntry.ToolCalls` now has its
  production writer — `agentgraph.HistoryEntry` gained the counterpart
  field WP01 said it lacked, and `llmHistoryWriter.AppendEntry` projects
  it through `moveToolCalls`. Also drained, though it predates the
  mission: `ModelAttrs.StreamToChat`, a manifest attr declared since the
  chat migration with no reader anywhere — it is now the discriminator
  that tells the chat's assistant turn apart from the six other
  executors that call `env.LLM.Generate`.
- **2026-08-14** (01PMCH01 WP01) — `llm.SessionMessageWriter` +
  `llm.Config.HistoryWriter` + `llmImpl.historyW` (an interface declared,
  a field populated at boot from `core/rpc/api.go`, and never read by any
  code path — a seam that reported it persisted the assistant turn and
  persisted nothing); `(*sessionHistoryReader).AppendMessage` (the
  toolloop `SessionHistoryRW` shape, whose interface no longer exists in
  the tree; zero call sites). Both deleted, not gated — they were rival
  transcript writers on the adapter the one-writer seam wraps.
- **2026-08-14** — `NodeAttributeEditor`'s four `*Options` props (dead
  props that made every `model_ref`/`tool_ref`/`corpus_ref`/
  `attachment_ref` attribute unsettable); `CanvasAdapter.persistsLayout`
  (set by both adapters, read by nothing — now load-bearing);
  `CanvasAdapter.paletteItems` + `PaletteItem` + `CANVAS_CATEGORIES`
  (deleted, zero consumers); `Settings.PermissionCacheDangerousOps`
  (never passed to the bash gate — wired, and converted to a live lookup).
- **2026-08-13** (01PMGX01 WP17) — `EvaluateToolGate`,
  `oauth.AuthorizeDevice`, `event.AuthorizeRawReplay` deleted;
  `narrative.SetSettingsGate` wired; eight I3 kinds drained by the
  kernel-run marker mechanism.
