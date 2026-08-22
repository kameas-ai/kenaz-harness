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
| I14 | `check-broker-topic-consumers.sh` | `i14-unconsumed-broker-topics.txt` | A const under `core/**` whose **identifier contains `Topic`** and whose value is a broker topic, with no frontend subscriber, no Go subscriber, and no `passthroughTopics` entry — **added 2026-08-18; one entry (`mcp:progress`)**. Multi-pass (Go + frontend), same discipline as `check-output-ports.sh`. Wired into `pr.yml`. See "Declined gate" below for why this shipped instead of the gate `docs/dead-code-audit-2026-08-16.md` §5 originally asked for, and the allowlist header for why "contains" rather than "starts with" is the whole gate. |
| I15 | `check-cedar-engine-singleton.sh` | *(none — no allowlist by design)* | more than one Cedar engine construction (`buildCedarGate`/`buildCedarEngineOrNil` call, or a direct `cedar.NewEngine` call) reachable from `rpc.New` — **added 2026-08-18 (consent-surfaces-truth-01PMTR01 WP05)**. I13 checks the *argument* at a call site; it has no vocabulary for *instance count*, which is why thirteen independent engine constructions (nine `buildCedarGate` + four `buildCedarEngineOrNil`) all passed it clean before the WP05 hoist. Wired into `pr.yml`. |

Non-allowlist gates that also protect against unwired code:
`check-output-ports.sh` (output port with no reader),
`check-knob-coverage.sh` (registered config field with no consumer),
`check-seam-implementers.sh`, `check-node-dispatch.sh`,
`check-serve-dispatch-drift.sh`, `core/serve/wsstream_topics_parity_test.go`
(desktop `passthroughTopics` ↔ `SERVED_STREAM_TOPICS`),
`core/rpc/builtins_wiring_test.go` (registered tool ↔ predicate case).
`scripts/ci/gates_can_fail_test.go` is the meta-gate: it plants a violation
per gate and asserts the gate rejects it — for the gates it covers. As of
`entry-points-and-crash-reporting-01PMZD13` (2026-08-20): **22 of 36**
`scripts/ci/check-*.sh` scripts have a planted-violation proof (up from 18
of 34 at that mission's start — it added `check-entrypoint-coverage.sh` and
`check-installer-payload.sh`, both new, and gave `check-seam-implementers.sh`
and `check-csp.sh` each their first-ever proof). The remaining fourteen do
not (RAN: `comm -23` between the script directory listing and the test
table's `gate:` fields — one more than the spec's own count of "thirteen",
which did not name `check-no-model-family-literals.sh`); see that
mission's spec §1.4 and its `research/escalations.md` E-004 for the
corrected list and for why "per gate" overstated the
coverage this sentence used to claim unconditionally.

### The draft tool promises a review path served mode does not have

**Found**: 2026-08-21, by the independent review of PR #304 (finding F3).
**Owner**: alec. **Ungated** — no existing gate can see a contradiction
between a tool description and a route's availability.

`harness_write_draft_agent_graph` tells the model the draft stays inert
*"until a human opens it in the graph editor and saves it."*
`cmd/harness-served/main.go:204` calls `rpc.New`, so the harness-self server
is constructed in served mode and the tool is reachable during a served
chat turn. In the **same PR**, Z707 WP03 boundary-panels `/agentgraph`,
`/agentgraph/edit/:id` and `/agentgraph/run/:runId` under served mode, and
all twelve `Graph_*` bindings sit in the forward gap allowlist
(`scripts/ci/allowlists/i15-serve-dispatch-gap.txt`).
`NotAvailableInServedMode.vue`'s own copy says the served user "often has
no desktop harness at all."

So in served mode the model can produce drafts nobody in that deployment
can open, review or run, while being told a review path exists. Two
missions landing together, each correct alone, contradicting each other at
the seam — which is the class of defect only integration finds.

**Owed**: either an `isServedMode()`-aware refusal in the draft handler, or
a tool description that does not promise the editor. Not fixed in #304
because the right answer is a product call: served mode may well want graph
authoring with a different review surface, and silently refusing would
remove a capability rather than stop a lie.


### Declined gate — 2026-08-18 · "An RPC's async contract vs. its caller's await sequence" — NOT BUILDABLE

`docs/dead-code-audit-2026-08-16.md` §5 owed a gate for the class that
produced finding A7 (`frontend/src/lib/updateClient.ts`'s `installLatest`
racing `Update_Apply` against a fire-and-forget `Update_StartDownload` —
lost by construction, not by timing; see the self-update-repair-01PMUP01
spec §1.1 for the full mechanics). `self-update-repair-01PMUP01` closes
this row as **not buildable**, for three reasons (spec §6):

1. **The contract is not in the type.** `StartDownload(ctx) error` and
   `Apply(ctx) error` have identical signatures — nothing distinguishes
   "done when it returns" from "spawned a goroutine and returns
   immediately". A gate would need a hand-written annotation of which
   methods complete asynchronously, and a method whose author forgot to
   annotate it produces a **pass** — precisely the "gate whose clean
   verdict is indistinguishable from did not look" class
   `scripts/ci/gates_can_fail_test.go` exists to prevent.
2. **The dependency is semantic.** Even given the annotation, "`Apply`
   requires `StartDownload`'s completion" is not derivable from either
   side — it would have to be declared too, at which point the gate
   checks one hand-written claim against another and asserts nothing
   about the actual code.
3. **The syntactic form is trivially evaded.** A matcher for
   `await A(); await B();` is defeated by `const p = A(); await p; await
   B();`, by a helper function, by `.then`, or by `Promise.all` — it
   would catch the literal historical text and nothing else, creating
   false confidence that the class is covered.

**Replacement:** WP02's regression test
(`frontend/src/components/updates/__tests__/updateClient.spec.ts`,
`installLatest (WP02 — polls to a terminal state) > does not call Apply,
and does not settle, while downloading is outstanding`) pins the one call
site that mattered. It is a pin, not a gate — it protects `installLatest`
specifically, not the class. The class this row was really pointing at
(registration without a real consumer) is what I14
(`check-broker-topic-consumers.sh`, above) covers instead: it would have
caught A8 (the topics `installLatest`'s fix needed accelerator events
from) in the same audit, plus B9/B16/B17 — four registration-vs-
consumption misses, one gate, none of them requiring an await-sequence
annotation.

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

### 2026-08-19 · `settings.Settings.SchemaVersion` gates no migration (`SD-13` settings)

`controls-and-readouts-that-tell-the-truth-01PMZ808` WP06 (FR-007). Three
production reads exist (`core/rpc/views/settings/impl.go:84-85`, `:308-309`,
`:1580-1581`), and all three are default-backfill only —
`if got.SchemaVersion == 0 { … = 1 }`. No code compares `SchemaVersion`
against any other value; there is no migration table and no dispatcher. The
doc comment at `core/rpc/views/settings/api.go:15` (formerly *"schemaVersion
gates migrations"*) and `docs/ci-invariants.md`'s #5 invariant (formerly
future-tense *"when WP13 lands"*) were both narrowed to state this plainly in
the same commit. The field and its Settings → About display stay — a number
that is always `1` is a fact, not a lie — and `check-knob-coverage.sh`
(UNIT-17) will register the field clean, because it *has* three real readers;
the gate cannot see that the readers never branch on a non-zero value.

**Owner:** alec. **Blocker:** no settings-migration dispatcher exists;
building one needs the `settings.json` upgrade fixture
`controls-and-readouts-that-tell-the-truth-01PMZ808` WP-PI adds under
`core/storage/sqlite/testdata/upgrade/` (there was none before this mission —
every settings trace in the 2026-08-18 closing sweep ran against the current
struct shape only). **Date:** 2026-08-19.

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

**Owner:** **CLOSED 2026-08-19 — the change this row names is implemented.**
See Part 8 §8.3-P1 of `docs/escalation-register-2026-08-19.md`:
`core/sessions/export/redact.go:528-534` now scans argument KEYS via
`RedactValue(k)` and walks values via `redactStructured(v, 1, …)`, bounded by
`MaxRedactDepth = 24` (`:297`) and cycle-guarded (`:409`). This is a record
correction, not a delete (A-0). What remains open is enumerated with reasons in
the Drained entry below, and those parks are escalated as **G-3**.
*(Original: the closing change is a recursive walk in `redactMessages` over
`map[string]any` / `[]any` / keys; it stayed out of WP05 because WP05's fix was
structural and widening a credential scanner mid-mission is its own review.)*

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

**Owner:** **escalated 2026-08-19 as G-2** (`docs/escalation-register-2026-08-19.md`
Part 8), per ruling F-1 — nothing parks by default. It needs a producer, which
is feature work with a spec (multimodal tool results); re-verified 2026-08-19
that `core/agentgraph/seams.go:322-325` still carries no blocks field. G-2's
recommended default is `justify(blocker, owner, date)` rather than a delete,
since A-0 freezes the delete lane. Note in `core/session/moves.go` where the
field used to be.

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

**Owner:** **escalated 2026-08-19 as G-2** (`docs/escalation-register-2026-08-19.md`
Part 8), per ruling F-1. The fix is backend: deliver the gate's revised
text as a move boundary + delta on the chat stream so the surface can
replace the draft bubble, rather than only writing it to the store.
Re-verified 2026-08-19 and narrowed: the boundary **already fires** —
`core/rpc/views/agentgraph/chat/moves.go:413` allocates a fresh position on the
revised path and `allocate` (`:154-169`) emits `StreamEventMoveStart` — so only
the text deltas are missing.

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

**Owner:** **escalated 2026-08-19 as G-1** (`docs/escalation-register-2026-08-19.md`
Part 8), per ruling F-1. Two honest exits: add the predicate and accept
that compacted content stops being findable, or add an explicit
"include compacted history" filter to the search UI and wire it. Not a
third: the two implementations should not keep disagreeing silently.
G-1's recommended default is **both** — the predicate plus the filter — so the
capability is not removed along with the defect. Re-verified 2026-08-19:
`core/rpc/views/search/impl.go` still has zero occurrences of `archived_at`.

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

**Owner:** **escalated 2026-08-19 as G-1** (`docs/escalation-register-2026-08-19.md`
Part 8), per ruling F-1 — same decision as the `archived_at` entry above, as
this row asked. Adopting `core/search` or deleting it were both fine when this
was written; **A-0 (2026-08-19) freezes the delete lane**, so the two live
options are now *adopt* or `justify(blocker, owner, date)`. Leaving a dead copy
holding the right answer is still not one of them. Re-verified 2026-08-19:
`core/search/search_test.go:9` is the only importer in the tree.

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

**Owner:** **escalated 2026-08-19 as G-2** (`docs/escalation-register-2026-08-19.md`
Part 8), per ruling F-1 — for all three parts. The branch-summary filter is
cheap enough to fold into whatever next touches branch summaries; the
gate is the part that keeps the class from coming back, and G-2 treats the
unwritten gate as an **unsatisfied instance of CLAUDE.md's gate-extension
rule**, not as optional polish. **Citation drift corrected 2026-08-19:** the two
live drift sites are `core/rpc/views/branches/impl.go:378` and `:464`, not
`:366`/`:452` as written above; `LastAssistantMsg` is now at `:341`. Claims
unchanged.

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

**Owner:** **escalated 2026-08-19 as G-8** (`docs/escalation-register-2026-08-19.md`
Part 8), per ruling F-1. The cheap closure is a guard that makes the purge
a no-op when the index holds no tool rows, or a comment at the statement
saying explicitly that it may be applied exactly once. G-8 recommends the
guard, on CLAUDE.md blind-spot-#3 grounds — a migration that has never run
against populated tables has never been tested, and this ledger already records
what that class cost on `sessions/0327-source-model-output`.

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

**Owner:** **escalated 2026-08-19 as G-1** (`docs/escalation-register-2026-08-19.md`
Part 8), per ruling F-1 — carried as a rider on the search decision, because it
shares a surface and an owner, not because it is a product question. One line in
`readFromRoute`: drop a `role` that is not in the option set, the same way the
select would.

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

**Owner:** **alec — RULED 2026-08-19 by A-12** (`docs/escalation-register-2026-08-19.md`
Part 1), which names this entry as its instance. **BUILD BOTH LEGS:** add the
`mode` field to `askuserquestion.AskArgs`, wire `OpenWizard`'s missing call
site, and mount `DeferredAskPill.vue` + `DeferredAskPanel.vue`. A-12's stated
reason is that the scheduling rulings (B-1, B-3) require a place for an
unattended run to put a question. Exit (b) below is additionally foreclosed by
**A-0**'s delete-lane freeze.
*(Original framing: two honest exits — (a) add `mode` + `questions` to the tool
schema and render the wizard, or (b) delete both legs down to the single
blocking path that actually runs. Not (c): leaving a half-surface that reads, in
a code review, like a shipped feature.)*

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

**Owner:** **alec — RULED 2026-08-19 by A-13** (`docs/escalation-register-2026-08-19.md`
Part 1), which names this entry as its instance and **deliberately reverses this
row's own recommendation**. Todo: **wire** (a parent must hold the list) —
unchanged. Sub-agent: **build**, not delete — spec `abort`/`steer`/`pause`/
`resume`, give the background-task subsystem a real producer, and mount
`SubagentTab.vue` + `SubagentBudgetMeter.vue`. A-13 claimed the subsystem
because **A-7** ruled that `subagent_start`, `background_task_complete` and
`worktree_create` all get producers, and none can be built without this seam.

⚠️ **Carry Part 7's correction — now resolved by UNIT-6.** A-13's stated
premise that `kenaz__subagent_dispatch` is "already live" was FALSE as of
2026-08-19: `core/rpc/builtins_wiring.go:312-313` read
`var subagentSeam agentgraph.BranchSeam // nil — no child-run spawner yet`
followed by `if subagentSeam != nil`, so the registration inside was
statically unreachable. **`subagent-control-and-background-tasks-01PMZB11`
UNIT-6 built the child-run spawner** the guard was waiting on
(`core/rpc/subagent_run_spawner.go`, threaded through
`agentgraph.BranchSeamAdapter.SetRunSpawner` and armed from `core/rpc/api.go`'s
`New()` once the LLM connector exists) and replaced the dead local variable
with `registerSubagentDispatchTool`, called only when a real, spawner-armed
seam exists. `kenaz__subagent_dispatch` is genuinely live in production as of
this commit. `SubagentTab.vue` / `SubagentBudgetMeter.vue` remain unmounted
(UNIT-10, gated on UNIT-8 + UNIT-9 landing first per the mission's plan.md
Rule 5) — this paragraph covers only the registration half.

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

**Owner:** **escalated 2026-08-19 as G-6** (`docs/escalation-register-2026-08-19.md`
Part 8), per ruling F-1 — needs a mission under `kitty-specs/` if the push
shape is wanted. The change that deletes this entry is the one that gives
`policyAPI` a non-stub production assignment. G-6 additionally records that
**`policyAPI` is a fifth stub RPC domain X-1 did not enumerate** (X-1 covers
`a2aAPI`, `workflowAPI`, `trustAPI`, `contextAPI`), and recommends resolving it
inside X-1's dated-justification set rather than alone. Re-verified 2026-08-19:
`core/rpc/api.go:1264` is still the sole assignment (`&stubPolicy{}`), and
`policy:event` occurs exactly once in the tree — in
`frontend/src/views/policy/PolicyView.vue:72`, a comment saying it does not
exist.

**2026-08-18 amendment (consent-surfaces-truth-01PMTR01 WP05/WP06) — this
entry over-scoped the gap.** The paragraph above is still correct about
`policyAPI` / `policy:event`: neither exists, and this entry's PUSH-based
scope (a broker topic a denial publishes to, live, the moment it happens)
is still unwired and still needs the mission described above if that is
the product's chosen shape.

What the entry did not know: a **separate, already-wired, non-stub PULL
feed** reaches the client boundary and did not need `policyAPI` at all —
`CedarPolicy_RecentDecisions` → `cedarpolicy.API.RecentDecisions` →
`cedar.Engine.RecentDecisions` (backed by the engine's own
`DecisionStore`), consumed by `harnessClient.ts`'s
`cedarPolicy.recentDecisions(limit)`. Before WP05 this path was *worse*
than unwired-but-simple: the cedarpolicy view held a **private**
`*cedar.Engine` that no gate ever called `Evaluate` on (nine gate sites
and four engine-consumer sites each built their own independent engine —
see `check-cedar-engine-singleton.sh`, I15), so `RecentDecisions` was
structurally always empty regardless of what UI sat on top of it. Mounting
a panel over it then would have been the exact lie this ledger's rubric
warns about ("mounting a panel whose Go knob is inert just moves the lie
from the backend to the UI").

WP05 hoisted every gate site to one shared `*cedar.Engine`, so the ring
`RecentDecisions` reads is now fed by real `Evaluate` calls from every one
of those sites. WP06 built the cheapest possible consumer on top of that:
a pull-based panel (`frontend/src/views/policy/PolicyView.vue`'s
"Decisions" tab, reachable at the existing `/policy` route) that fetches
on open and on a manual Refresh — **no push topic, no `policy:event`
contract, `policyAPI` still returns `errNotWired`.** A denial the user
just caused shows up the next time they open or refresh the tab; a denial
that happens with nobody looking is not surfaced proactively. That
distinction — pull vs. push — is the entire remaining scope of this entry.
If the product wants live, push-driven denial toasts, that is still the
mission this entry originally called for; it did not need to gate the
cheap pull-based win, and should not have been read as blocking it.

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

**Owner:** **escalated 2026-08-19 as G-7** (`docs/escalation-register-2026-08-19.md`
Part 8), per ruling F-1. Either populate `Models` in `runtimeInfosToWire`
(needs a per-runtime model probe at list time) or delete the first branch
so the card stops implying a state it cannot reach. Do not "fix" it by
deleting the string alone — the probe is the feature. G-7 recommends the probe
and asks that it be **scoped with A-5/D-2**, which already ruled a probe-driven
capability path for the same class of endpoint — building a second probe would
be the rival-infrastructure shape this ritual keeps finding.

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
(`EffectiveLocalRuntimeRAMBytes` has zero callers) —
**`EffectiveLocalRuntimeRAMBytes` claimed and narrowed** by
`controls-and-readouts-that-tell-the-truth-01PMZ808` UNIT-16 (WP21,
FR-033). **Owner: alec. Date: 2026-08-21.** Its doc no longer names a
WP06 filter or WP07 panel that do not exist. This claims the field
only, not the row — `LocalRuntimeRAMOverrideGB` the raw setting (as
opposed to the Effective helper) and every other field in this row
remain under G-4.

*Consumer lives in an orphan package:* `CedarStrictCredentialMode`,
`CredentialAuditRetentionDays` (both reach `core/credstore`, an I7 orphan).

*Narrative tuning knobs whose code paths use hardcoded defaults:*
`SummarizerProfileID`, `NarrativePromotionWeights`,
`NarrativePromotionThreshold`, `NarrativeRetrievalWeight`,
`NarrativePromoterParallelism`, `NarrativePreludeTopN`.

*Self-documented as reserved (fine, listed for completeness):*
`KeyboardShortcutsPreset`, `BranchAdvisorUseLLM`, `BranchAutoMode`.

**Owner:** **split 2026-08-19.**

- The six **narrative tuning knobs** (`SummarizerProfileID`,
  `NarrativePromotionWeights`, `NarrativePromotionThreshold`,
  `NarrativeRetrievalWeight`, `NarrativePromoterParallelism`,
  `NarrativePreludeTopN`) are **alec — RULED by A-4** (documented product
  retirement of the memory-narrative subsystem). They are removed with it, not
  wired. Verified 2026-08-19 that all six are `core/memory/narrative`-scoped.
- Everything else in this entry is **escalated as G-4**
  (`docs/escalation-register-2026-08-19.md` Part 8), per ruling F-1.

The cheapest structural fix is still to bring `settings.Settings` under
`core/wiring/knobcoverage` — see the next entry — and that is G-4's recommended
default. Two sequencing constraints G-4 records: **`PermissionMode` must be
ruled together with X-2 and B-4**, since its documented "every call prompts"
semantics *is* the per-call tool authorization those two already ruled wire; and
**`MCPAutoRestartDisabled` must get a reader before `MCPHealthSettingsPanel` is
mounted** (see the frontend orphan backlog below). Inertness re-verified
2026-08-19 for `EffectivePermissionMode`, `MCPAutoRestart()`,
`EffectiveLocalRuntimeRAMBytes`.

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

**Owner:** **alec — RULED 2026-08-19 by A-13**, with **A-7** as the reason
(`docs/escalation-register-2026-08-19.md` Part 1). The background-task producer
is **built**, not parked: A-7 ruled that all eight fire-less hook events get
producers, and `background_task_complete`, `subagent_start` and
`worktree_create` cannot be built without this seam. The fix is one
restructuring — allocate the task id before `cmd.Start()`, attach the registry
writers, pass `BackgroundSpawn`/`BackgroundEnd` and the `HookFirer` from
`core/rpc`, then register `kenaz__monitor` with its predicate case.
Re-verified 2026-08-19: `core/rpc/builtins_wiring.go:321` still reads
`Tasks:   nil,`, and `Options.BackgroundSpawn` still has assignments only in
`run_in_background_test.go`.

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
(`core/rpc/builtins_wiring.go:321`). `tasksview.NewAPI(taskReg)` therefore
serves an always-empty registry.

**Disposition: parked, not deleted** — `core/tasks`, the four RPCs and
`TasksPanel.vue` all stay. This is an *escalation*, not a delete: whether
background execution ships at all is a product call, and deleting the
consumer half of a wanted feature destroys tested work. Removing the *link*
is correct under either outcome. If background execution ships, remount the
panel and restore the nav entry in the same PR that wires the producer.
**Owner:** **alec — RULED 2026-08-19 by A-13**, same as the parent entry. Under
that ruling background execution ships, so this row's conditional applies:
remount the panel and restore the nav entry in the same PR that wires the
producer.

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
  ~~`check-serve-dispatch-drift.sh` is informational (exit 0) unless
  `SERVE_DRIFT_GATE=1`, so the gap accumulates quietly.~~ **CLOSED
  2026-08-21 (I15, `served-mode-is-a-real-mode-01PMZ707` WP02).** The gate
  now defaults to `SERVE_DRIFT_GATE=1` (both directions —
  bindings-without-a-dispatch-case and dispatch-case-without-a-binding),
  seeded with `scripts/ci/allowlists/i15-serve-dispatch-{gap,reverse}.txt`
  (419 forward / 5 reverse entries at promotion). `Graph_*` and
  `Workflows_*` still have no serve dispatch case — that routing decision
  is explicitly OUT of this mission's scope (spec.md §2, D-701: routing
  `Graph_*` would be new capability work, not a parity fix) — but the gap
  is now a *named, allowlisted, dated* line per method rather than a
  silent, unenforced one. **Per-method reclassification CLOSED 2026-08-21
  (WP07, same mission):** all 416 remaining forward-gap entries now carry
  one of the five classes (26 `gated`, 187 `boundary-panelled`, 115
  `unrouted`, 13 `desktop-only-by-nature`, 75 `untriaged` — each untriaged
  entry individually dated and owned, not a repeat of this WP02 note).
  `scripts/ci/check-serve-gap-classification.sh` (wired into `pr.yml`)
  now fails a PR that adds a classless entry or an undated/unowned
  `untriaged` one. `Graph_*`/`Workflows_*` themselves landed as
  `boundary-panelled` (WorkflowsView.vue and the four agentgraph views
  are all panelled — WP03/WP05).

### 2026-08-21 · WP07's own caller-site pass found two live served-mode bugs neither the closing sweep nor WP03/04/05's per-view scans could see

Both are shell-chrome / cross-affordance findings — the class of bug a
per-VIEW audit structurally cannot catch, which is the exact gap
`served-mode-is-a-real-mode-01PMZ707` §1.7 named. Both **CLOSED
2026-08-21** in the same WP07 commit that found them:

- **`shell/MemoryBadge.vue` rendered "Loading memory count…" PERMANENTLY
  in served mode.** `Memory_HealthSnapshot`/`Memory_ListChunks` are
  unrouted; `chunkCount` starts `null` and the `fetchCount()` catch
  leaves it there ("keep the previous value" — never true on the FIRST
  call). The badge is mounted from `shell/LeftRail.vue`, which is not a
  "view" any per-view scan (including this mission's own WP03) would
  have enumerated. Fixed: gated `v-if="!served"` in `LeftRail.vue`.
- **WP04's `/` slash-menu gate covered only the dropdown's autocomplete
  fetch, not the independent send-time branch.** `ChatInput.vue`'s
  `send()` has a SEPARATE `if (text.startsWith('/'))` branch (typing
  "/foo" and pressing Enter, with no dropdown ever opened) that emits
  `slashCommand`, reaching `SessionsView.vue`'s unguarded
  `client.slash.execute()`. WP04's gate on the dropdown fetch alone left
  this fully reachable. Fixed: `send()`'s slash branch now also checks
  `!served`.

### 2026-08-21 · Open findings from WP07's per-method triage — chat-surface affordances needing a WP04-style port/gate call

`served-mode-is-a-real-mode-01PMZ707` WP04 scoped six chat affordances
(paperclip, `/`, autonomy chip, title suggestion, Branches, feature
flags). WP07's own caller-site pass found several MORE affordances
reachable from the live, routed chat surface (`SessionsView.vue`,
`MessageBubble.vue`, `CodeBlock.vue`/`MarkdownBlock.vue`) with no
`isServedMode()` guard of their own — none is an active data-fabrication
lie (each fails via an honest `ServedUnsupportedError` today), but none
has had the WP04-shape port-or-gate review either. Full detail and
per-binding reasoning: the `untriaged` class in
`scripts/ci/allowlists/i15-serve-dispatch-gap.txt`. Summary, owner alec
for all:

- **Artifact save/view from chat** (`Artifacts_Delete/Get/List/Promote`,
  `Sessions_SaveAsArtifact`) — MarkdownBlock.vue/CodeBlock.vue's "save as
  artifact" flow. `CodeBlock.vue`'s `saveAsArtifact()` also calls
  `createHarnessClient()` directly instead of the injected
  `useHarnessClient()`, bypassing the served/desktop transport switch
  AND fake-client test injection — a second, architecture-level bug
  independent of served mode, found as a side effect.
- **Context-attachment management outside the paperclip**
  (`Attachments_Add/ListResolved/Refresh/Remove`,
  `Contexts_AttachModule/CreateFolder/Get/List`) — reachable via
  `ResolvedContextPanel.vue` (mounted in `SessionsView.vue`), a separate
  path from the paperclip WP04 gated in `ChatInput.vue`.
- **`Sessions_ResumeMessage`, `Sessions_ClearTitle`** — live in
  `MessageBubble.vue`/`SessionHeader.vue`. `ClearTitle` is the undo half
  of `Sessions_SuggestTitle`, which WP04 ported — porting one without the
  other is itself arguably a half-flow WP04's own bar would reject.
- **`Bash_Exec`** (the inline `!command` chat affordance) — already
  fails honestly (inline error text, not fake output) but the port/gate
  question is a genuine security escalation, not a mechanical one:
  unlike the Cedar-gated `kenaz__bash` MODEL tool, this is a direct
  human-to-shell bypass with no gate at all today.
- **`Slashcmd_Get`/`Slashcmd_Run`** — the user-authored slash-command
  EXECUTION path from `SessionsView.vue`, distinct from the
  already-boundary-panelled `Slashcmd_List/Save/Delete` settings UI.
- **`Memory_RememberMessage`, `Handoff_Inbox/ListTeam/Share`,
  `SessionSync_Toggle`, `Search_Sessions/Search_Unified`,
  `Settings_GetArtifactPreview`** — each degrades safely today (empty
  list / disabled default, not fake data) but carries no
  `isServedMode()` gate.

### 2026-08-21 · Two more orphan Wails bindings found alongside A-14's nine (not part of A-14)

`escalation-register:1139`'s A-14 already rules on nine zero-caller
bindings. WP07's caller-site pass found two more with the identical
shape (zero TS callers anywhere, desktop or served) that are NOT among
A-14's nine: `MCP_HealthSnapshot`, `MCP_SubscribeHealthChanges`,
`LLM_UpdateProviderCredential`, `Settings_GetLocalRuntimeRAMOverrideGB`,
`Settings_SetLocalRuntimeRAMOverrideGB`. Recorded here rather than
silently dropped; owner alec, needs the same per-binding A-0-style
ruling A-14 got (wire, delete, or keep as a dev tool) — not resolved by
this mission, which only classifies served-mode reachability and these
have none to classify.

### 2026-08-21 · An "absorbed" finding that was never actually fixed — `dead-code-audit-2026-08-18.md:1794`'s SD-01/SD-02 (serve) claim

`docs/dead-code-audit-2026-08-18.md:1794`'s mission-assignment table
credits `trust-surfaces-that-fire-01PMZ202` with absorbing "`SD-01`/`SD-02`
serve (fabricated permission posture; fabricated empty audit trail)" — the
same two findings `served-mode-is-a-real-mode-01PMZ707` §8 and plan.md's
out-of-band check #2 name as a **required pre-check** ("if it has already
edited `AuditView.vue` or `PermissionDialsPanel.vue`, WP05 shrinks to the
served-specific half"). **The pre-check found the absorption claim false.**
`01PMZ202` did touch `AuditView.vue` (v0.66.0, `4d34cf4a`) — but only to
migrate the seeded fetch from `listEntries()` to the richer `filter()`
(UNIT-6/WP07's multi-term query work). The catch that turns a rejected
served-mode call into a fabricated result was untouched in both files, and
verified still present at HEAD before this WP's fix:
`AuditView.vue`'s `catch { seeded.value = []; }` and
`PermissionDialsPanel.vue`'s `catch { permissionMode.value = 'normal'; }`.
**CLOSED 2026-08-21** by `served-mode-is-a-real-mode-01PMZ707` WP05 — see
`frontend/src/views/audit/AuditView.vue` (boundary-panelled; all eleven
`Audit_*` RPCs are unrouted) and
`frontend/src/components/settings/PermissionDialsPanel.vue` (per-panel fix,
D-710: the view itself is NOT panelled because `Permissions_ListPending`/
`_Resolve` genuinely work in served mode — only the dial that cannot read
its own posture is hidden, replaced with an explicit unavailable state).
**The lesson, per `feedback_verify_agent_citations`:** a mission-assignment
table entry is a claim about intent, not a verified fix — the next agent
that reads "absorbed by X" should still grep the actual file before
treating a finding as closed, exactly as this mission's own plan.md
insisted on doing.

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
- `MCPHealthSettingsPanel` — blocked on an inert Go knob (see "Settings
  fields that are stored, bound, and inert" above). Wire the consumer
  first, in the same PR.
- ~~`BranchAdvisorSettings`~~ — **drained 2026-08-18** by
  engineer-truth-pass-01PMTP01 WP02/WP03. This entry previously pointed
  at "Settings fields that are stored, bound, and inert" above, but
  that section only ever named `BranchAdvisorUseLLM` and
  `BranchAutoMode` (both correctly self-documented as reserved) — it
  never named the two fields that actually blocked the mount,
  `BranchAdvisorEnabled` and `BranchReintegrationMaxTokens` (verified:
  `BranchAdvisorEnabled` had zero occurrences anywhere in this ledger).
  Anyone following the old pointer would have wired the wrong two
  fields, mounted the panel, and shipped an inert toggle. WP02 gave
  `BranchAdvisorEnabled` a reader (`ChatInput.vue`'s
  `runAdvisorDetector`) and `BranchReintegrationMaxTokens` a caller
  (`ProposeReintegrationSummary` via `EffectiveBranchReintegration-
  MaxTokens`); WP03 mounted `BranchAdvisorSettings.vue` at
  `SettingsView.vue`'s `?tab=branch-advisor` pane, linked from
  `SettingsTabs.vue`. `BranchAdvisorUseLLM` / `BranchAutoMode` remain
  correctly reserved and stay in the "stored, bound, and inert" list
  above — they were never this entry's blocker.
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

**Owner:** **split 2026-08-19.**

- The **P1b** block above (delegated sub-agent execution; background execution)
  is **alec — RULED by A-13**, which is the "named mission" it was parked
  pending.
- The **P1** list above is **escalated as G-5**
  (`docs/escalation-register-2026-08-19.md` Part 8), per ruling F-1. G-5's
  recommended default: mount `RecoveryCodeFlow`, `ProjectAutonomyPanel` and
  `CrashReportingOnboardingModal` now; hold `HookJournalView` until its read
  path exists; **do not mount `MCPHealthSettingsPanel` until G-4 wires
  `MCPAutoRestartDisabled`**; hold `CedarEditor` for the `PolicyView` port.
- The **P3** item (`lib/capability-keys.ts`) is not ownerless — it carries a
  live disposition ("Being wired as a typed import").

Re-verify each item's importer graph before acting — frontend code churns
between sweeps. Verified 2026-08-19: all six P1 components still have zero
non-test, non-self importers.

⚠️ **CITATION DRIFT CORRECTED 2026-08-19 (Part 8 §8.3-P3).** Two of this
entry's load-bearing line numbers are stale, and following them would wire the
wrong thing — the same failure the `BranchAdvisorSettings` correction above
records. `RecoveryCodeFlow`'s backend is assigned at **`core/rpc/api.go:2695`**
(`Recovery: &recoveryBackendAdapter{},`), inside the *bare* block opened at
`:2405`, so "unconditional" holds — `api.go:2426` is now the catalog view. The
project rung is engine-consumed at **`core/rpc/api.go:4370-4373`** →
`autonomy.Resolve` (`:4703`), not at `:4304`, which is now a headless-confirm
log line. Both underlying claims hold. **Cite the symbol, not the line, for
`api.go`** — it is ~7,000 lines and churns every release.

**AMENDMENT 2026-08-18 (`mcp-connector-lifecycle-01PMMC01` WP01) — this
entry no longer names no owner and no mission.** The harness-self MCP
server (B10 in `docs/dead-code-audit-2026-08-16.md`, this section's
subject) is no longer parked as "housekeeping": the owner ruled
**attach** on 2026-08-18. See
`kitty-specs/harness-self-attach-01PMHS01/research/attach-decision.md`
for the decision record (the original pointer,
`kitty-specs/mcp-connector-lifecycle-01PMMC01/research/b10-harness-self-decision.md`,
is dangling — that mission is archived and has no `research/` directory
at all; the ruling it held is reproduced in the file cited above).
Execution (the session-scoped tool-visibility seam, the fourth
dispatch-pool arm, installing `EmbeddedCedar`, making
`IsHarnessSelfMCPDisabled` real, emitting-or-deleting the dead event
kinds, and the Cedar-gating that makes attaching safe rather than merely
attached) is now **assigned to the dedicated follow-on mission**,
`harness-self-attach-01PMHS01`, and not executed in
`mcp-connector-lifecycle-01PMMC01` (that mission's own WP07 is
explicitly out of scope for the attach — see its spec).

**AMENDMENT 2026-08-19 (`harness-self-attach-01PMHS01` UNIT-1) — owner
named.** `docs/escalation-register-2026-08-19.md` Part 8 **G-9 was RULED**
(not merely escalated): *"✅ RULED 2026-08-19 — owner: alec. Owner: alec.
Dated 2026-08-19."* **Owner of the attach execution: alec, dated
2026-08-19, executing as `harness-self-attach-01PMHS01`.** No capability
question remains open — the attach decision was made 2026-08-18; G-9 only
named who dispatches the follow-on mission, and it is now dispatched.
This row was previously missed by F-1's count of sixteen because it
anchored on the bold `**Owner:** unassigned` form rather than this row's
prose — see Part 8 §8.3-P2. **The blocker is load-bearing and survives
the owner assignment** (G-9's own ruling text: *"Naming an owner does not
unblock the work; it names who decides when it unblocks"*): the
session-scoped visibility seam and `EmbeddedCedar` wiring do not exist on
`main` as of this amendment — they are `harness-self-attach-01PMHS01`
UNIT-2/UNIT-3/UNIT-4, not yet landed. Attaching before they land would
hand every session write access to provider credentials and settings,
which is why the mission's own sequencing rule (see its `tasks.md`) is
non-negotiable: no commit may make `harnessServer.Server()` reachable
from a session until AC-002 passes.
attached) is **deferred to a dedicated follow-on mission**, not executed
in `mcp-connector-lifecycle-01PMMC01` (that mission's own WP07 is
explicitly out of scope for the attach — see its spec). **Owner of the
attach execution:** **escalated 2026-08-19 as G-9**
(`docs/escalation-register-2026-08-19.md` Part 8), per ruling F-1 — this row
names a real blocker but no person, which is exactly what F-1 forbids. G-9's
recommended default is to name alec and date it: the product decision (attach)
was already ruled 2026-08-18, so no capability question is open. **This row was
missed by F-1's count of sixteen**, which anchored on the bold
`**Owner:** unassigned` form — see Part 8 §8.3-P2. **Blocker:** the visibility seam
and `EmbeddedCedar` wiring do not exist yet (spec §6 option A cost items
2 and 3) — attaching without them would hand every session write access
to provider credentials and settings, which is why this is not a
same-commit fix.

Two small pieces of this finding were resolved immediately, regardless
of the attach mission's timeline, because they were unambiguous under
every branch (attach, retire, or park):

- The three never-emitted `KindHarnessSelfPolicy{Proposed,Written,Rejected}`
  event kinds are **deleted** (`core/event/kind/registry.go`) —
  `harness_write_propose_cedar_policy`, the tool that would have emitted
  them, was itself deleted by the 2026-08-14 sweep, so no emit site for
  any of the three ever existed under any name. Positive no-consumer
  proof: `grep -rn "KindHarnessSelfPolicy" --include='*.go' .` (pre-
  deletion) found exactly one reader, `integration_test.go`'s
  `TestIntegration_AuditKindsRegistered`, which asserted only
  `kind.IsRegistered` (the string is a registry-map key) — not that
  anything emits it. That test's docstring also claimed (falsely) that
  the kinds "fire on the propose/accept round-trip"; corrected in the
  same commit. `KindHarnessSelfToolCalled` — which `audit.go`'s
  `WithAudit` genuinely emits on every harness-self tool dispatch —
  survives.
- Escalation #3 from the audit ("do the dead kinds have waiting
  consumers — an audit view filter, a fleet exporter?") is answered: no.
  `grep -rn "KindHarnessSelfPolicy"` across the frontend and
  `core/rpc/views/audit` found no filter, no exporter, no reader of any
  kind besides the one test above.

`IsHarnessSelfMCPDisabled` (`core/rpc/onboarding_wiring.go:194-196`,
hardcoded `false`) is **left as-is** and assigned to the attach mission
rather than fixed here: the dial only means something once there is a
live server to disable, and building its settings-store persistence now
would front-run the attach mission's own design of what scope the dial
applies at (global vs. per-project) — see spec §6 option A cost item 5,
which already scopes this to the attach execution.

### 2026-08-18 · Custom-recipe authoring (A5) flagged off, then CLOSED same day by WP06

`mcp-connector-lifecycle-01PMMC01` WP02 closed the A5 lie (a row Edit button
and a Custom-recipe tab that both opened a form whose Save unconditionally
threw) by gating both doors behind one interim flag,
`CUSTOM_RECIPE_AUTHORING_ENABLED` (`frontend/src/lib/customRecipeAuthoring.ts`),
shipped `false` with a named retirement condition: land `MCP_SaveCustomRecipe`
and retire the flag in the same commit.

**CLOSED 2026-08-18, same mission, WP06.** The owner unblocked WP06
mid-dispatch (originally conditional on the B10 decision, which landed via
WP01 the same day). `MCP_SaveCustomRecipe` is live end-to-end (view method
`core/rpc/views/mcp/custom_recipe.go` → `core/rpc/bindings.go` →
`harnessClient.ts` → `CustomRecipeTab.vue`'s `save()`), persisting through
the already-implemented `recipes.UserStore.Save`. The flag was **deleted
outright** (`frontend/src/lib/customRecipeAuthoring.ts` removed, both
`v-if`s reverted to unconditional) rather than left flipped to `true` —
per the flag's own retirement note, "a flag left permanently true is a new
dead knob." `KenazToolsPanel.vue`'s row Edit button and
`AddMCPServerModal.vue`'s Custom tab are unconditionally reachable again,
now backed by a real save path.

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

### 2026-08-18 · `sessions/0327-source-model-output` already destroyed data, and it is unrecoverable

Found and fixed forward (not recovered) in `upgrade-path-coverage-01PMUG01`
WP03. `sessions/0327-source-model-output`
(`core/session/migrations_source_model_output.go`) carried the identical
`DROP TABLE artifacts` + `RENAME` recipe as
`sessions/0332-artifacts-global-scope`, with **no scratch-table
protection**, at version 327 — above 324, which is the migration that
creates `artifact_versions` with `artifact_id ... ON DELETE CASCADE`. The
production DSN always sets `_pragma=foreign_keys(1)`, so `DROP TABLE
artifacts` cascade-deletes every `artifact_versions` row.

Unlike 0332 (invisible to the runner on every upgraded install until the
v0.63.1 `Pending()` selection fix, so it never actually ran on populated
tables before this mission), **0327 shipped before the units block
existed**, when the max-based selection bug had no cross-block condition to
trigger it — it ran, cascade and all, on **every install that had
`artifact_versions` rows at the moment it upgraded through 327.**

- **Historical (unfixable):** any `artifact_versions` rows an install had
  before it first upgraded through 327 are gone. There is no backup and no
  recovery path. Recorded here so nobody rediscovers it as new.
- **Live forward risk (fixed):** any database whose ledger still stops at
  <=326 had 0327 pending, and the v0.63.1 selection repair reaches it
  again. WP03 applied 0332's exact scratch-table pattern; `UpSource` is
  untouched (it is the migration's content hash). See
  `core/storage/sqlite/migration_0327_test.go` — written first, watched
  fail (`artifact_versions = 0 after the 0327 rebuild, want 2`), then fixed.

### 2026-08-18 · `UpSource` edits are not caught at boot — `migrations.Registry.VerifyLedger` has zero non-test callers

Found while proving WP03's "editing `UpSource` fails every snapshot"
mutation criterion (`upgrade-path-coverage-01PMUG01` spec.md §4). Spec's
design-constraints section states that an edited `UpSource` "fails every
snapshot at once" via `ErrLedgerHashMismatch`. **Performed the mutation to
check:** edited `sqlSourceModelOutputUp`'s CHECK-constraint value order (a
real content change, not just whitespace — `HashSQL` canonicalises
whitespace) and reran `TestUpgradePath`. It still passed.

`migrations.Registry.VerifyLedger` (`core/storage/migrations/runner.go`) is
the function that compares a ledger row's stored `content_hash` against the
registered migration's computed hash and returns `ErrLedgerHashMismatch` on
mismatch — exactly the check the spec's claim depends on. **It has zero
non-test callers anywhere in the module** (`grep -rn "VerifyLedger(" --include="*.go" core | grep -v _test.go` returns only its own definition).
`storagesqlite.Open` calls `EnsureLedger` → `Apply` →
`verifyFullyApplied` (a *different*, similarly-named function that only
checks whether an applied ledger row exists per registered migration — it
does not compare `content_hash` at all) and never calls `VerifyLedger`.
Set-membership `Pending()` also does not consult `content_hash` — an
already-applied migration whose `UpSource` was edited after the fact is
simply skipped, hash mismatch and all, with no error anywhere in the boot
path.

**Why this was not fixed in WP03 by wiring `VerifyLedger` into `Open`:**
`VerifyLedger`'s per-mission contiguity check (`ErrSchemaGap`) is the same
mechanism that would flag a `ledger_only` entry — an applied ledger row
with no matching registered migration, the normal state of a downgrade or a
removed mission. Wiring it into `Open` would make a healthy downgrade
refuse to boot, which is the exact behaviour spec's FR-3 explicitly
rejects ("Making drift fatal at `Open` — explicitly rejected", see this
mission's WP04). A narrower fix — call only the hash-comparison half of
`VerifyLedger`'s logic, skip the contiguity half — is plausible but was not
attempted in WP03; it changes `Open`'s error surface and needs its own
review, which is why this is recorded rather than silently patched. Owner:
whoever picks up migration-ledger integrity next; the fix shape to
evaluate is a hash-only variant of `VerifyLedger` callable from `Open`
without also enforcing schema-gap contiguity.

### 2026-08-18 · Migrations that can never run: `memory-rag`, `event-log`, `tasks`

Surveyed while grounding `upgrade-path-coverage-01PMUG01` (not that
mission's fix — recorded per its spec §8 for visibility):

- `core/memory/narrative/migrations.go` defines migrations 821 and 822.
  `narrative.RegisterMigrations` has no caller anywhere in the module —
  these two migrations can never run, on any install, ever.
- `core/event/log/register.go` and `core/tasks/store_sql.go` each declare a
  `RegisterMigrations` function against a *stub* registry interface (not
  `*migrations.Registry`), with no caller. `core/event/log` carries six
  embedded `.sql` files the migration framework never sees.
- Of the 14 blocks declared in `core/storage/migrations/blocks.go`'s
  `CanonicalBlocks`, 10 have zero registered migrations: `event-log`,
  `secrets-keychain`, `scheduler`, `mcp`, `a2a`, `signed-cards-trust`,
  `bundle`, `shared-context-distribution`, `memory-rag`, `app-layer` (block
  reservations made ahead of the feature landing, some now orphaned).

Not itself a bug — a reserved-but-unused block is inert, not lying — but
recorded because dormancy at this scale is exactly the shape that let the
v0.63.0 P0 hide: nothing exercises these migrations at all, so nothing
would notice if `RegisterMigrations` were ever wired up against a populated
table without the same care WP03 gave 0327/0332.

### 2026-08-18 · The boot drift goroutine's unsynchronised reads are TOCTOU-shaped, not racy — `a.updatePollCancel` is the real unsynchronised write pair

Found while implementing `upgrade-path-coverage-01PMUG01` WP04 (FR-3g).
Spec §2 FR-3g flagged `core/rpc/api.go`'s boot-time migration-drift
goroutine (spawned from `SetContext`) for reading `a.storageAPI` (once at
the nil-guard on the caller's goroutine, again inside the spawned
goroutine after `runMigrationDriftCheck` was extracted) and `a.auditImpl`
with no mutex.

**Precise finding, stated carefully so it isn't over-claimed:** this is
TOCTOU-shaped — a check on one goroutine and a use on another — but it is
**not a race `-race` will ever catch**, because both fields are written
exactly once, inside `New()` (`core/rpc/api.go`, around the `storageAPI:
storageview.NewAPI(db, dataDir)` and `a.auditImpl = audit.NewAPI(...)`
assignments), before the `*API` value is ever handed to a caller.
`SetContext` — the only place that spawns goroutines reading these fields
— never writes either one. `main.go` calls `api.SetContext(ctx)` on an
`api` returned from a prior `rpc.New(...)` call in the same function body,
at both its call sites (`main.go` around lines 241 and 405/416). `New()`
happens-before `SetContext` at both, by ordinary single-goroutine sequencing
within `main()` — there is no second goroutine that could write
`storageAPI`/`auditImpl` concurrently with the drift goroutine's read.

**The actual unsynchronised write pair in this file is
`a.updatePollCancel`**, written in `SetContext` (guarding a prior poller
before replacing it, then storing the new `context.CancelFunc`) and again
in `Shutdown` (cancelling and nilling it). Unlike `storageAPI`/`auditImpl`,
both of *these* writes happen after construction, from call sites that
main.go does not guarantee run on the same goroutine relative to each
other (`OnShutdown` is a Wails-invoked callback, not necessarily
sequenced against a concurrent `SetContext` re-init in the test harness
path that calls it more than once). **Fixed in the same WP**: added
`updatePollMu sync.Mutex` guarding every read and write of
`updatePollCancel` in both `SetContext` and `Shutdown`. Cheap — two lock/
unlock pairs around code that was already there — so there was no reason
to leave it recorded-but-unfixed the way FR-3g's spec text allowed for.

Point of this entry: do not re-flag `a.storageAPI` / `a.auditImpl` in a
future sweep as racy without also re-deriving this happens-before
argument — the fields were never the live risk, `a.updatePollCancel` was
and now is guarded.

### 2026-08-19 (model-scheduled-jobs-01PMSJ01 WP03) · `scheduler.Scheduler`, `scheduler.Store`, `Job.OnMissed`, `Job.MissedPolicy` — justified, not implemented, not deleted

`core/scheduler/scheduler.go` declares `Scheduler` (`Start/Stop/Upsert/
Delete/Get/List/RunNow/ReconcileMissed`, keyed on the generic `Job` type)
and `Store` (`Upsert/Delete/Get/List`, also `Job`-keyed) with **zero
implementations anywhere in the tree** (spec.md §1.2 for this mission).
`Job.OnMissed` / `Job.MissedPolicy` (`core/scheduler/job.go:46-62`) have no
reader.

Per spec.md §8 D-2, as amended by owner ruling A-0 (the delete lane is
frozen): WP03 was to implement `scheduler.Scheduler` "if the engine can
honestly satisfy it, minus `ReconcileMissed`" or else justify each symbol
here instead of deleting it. **It cannot be honestly satisfied**, and this
entry is that justification, named per-symbol:

- **`scheduler.Store`** is `Job`-keyed generic persistence. The only
  concrete persistence for chat-run schedules is `ScheduledChatStore`
  (`core/scheduler/chat_store.go`), which is `ChatRunRecord`-keyed and has
  no `on_missed` column in `scheduled_chat_runs` at all — there is no
  honest `Store` implementation to write without inventing a second,
  parallel persistence path for the same rows.
- **`scheduler.Scheduler`** is the `Store`-backed engine surface built on
  top of the above. WP03 built `ChatCronEngine`
  (`core/scheduler/chat_cron_engine.go`) instead — a purpose-built engine
  keyed directly on `ScheduledChatStore` / `ChatRunRecord.ID`, exposing
  `Sync`/`Unregister`/`Start`/`Stop`/`SetDispatcher`/`Registered`/`Started`.
  It is wired into production (`core/rpc/api.go`: constructed when a DB is
  available, `Start`ed from `SetContext`, `Stop`ped from `Shutdown`,
  reacting to `scheduledchat.API`'s Create/Update/Delete/SetEnabled via the
  `Registrar` interface) and has full unit + wiring test coverage
  (`core/scheduler/chat_cron_engine_test.go`,
  `core/rpc/api_chat_cron_engine_test.go`). It does not implement
  `scheduler.Scheduler`'s literal Go interface, but it closes the same gap
  that interface's zero implementations left open (spec.md §1.2).
- **`Job.OnMissed` / `Job.MissedPolicy`** describe a reconcile-on-resume
  policy for missed fires. `ChatCronEngine` has no missed-fire tracking —
  a schedule that was disabled while the process was down and re-enabled
  later simply resumes ticking from `Sync`'s next call, with no attempt to
  "catch up" a skipped fire. This is the same posture the workflow-side
  `CronScheduler` already ships (`core/workflows/scheduler/cron_scheduler.go`
  has no `ReconcileMissed` caller either).

**Blocker:** a generic `Job`/`Store`-based scheduler abstraction that
actually fits both the legacy session-kind jobs and the chat-run kind
without a schema rework of `scheduled_chat_runs` to carry `on_missed`.
**Owner:** the wave lead for a future missed-fire / reconciliation mission
(none scheduled as of this date — this mission's WP08 adds one-shot
`trigger_kind`/`run_at` schedules but does not add reconciliation).
**Date:** 2026-08-19.

Do not re-delete these four symbols on a future "no callers" grep without
re-reading this entry — `Job` and `Scheduler`/`Store` are still declared
because `JobKindSession` / `JobKindChatRun` and `ChatRunSpec` live on
`Job`, which `ChatCronEngine.fireSync` constructs and passes to
`ChatRunDispatcher.DispatchChatRun`. Only the `Scheduler`/`Store`
interfaces and the `OnMissed`/`MissedPolicy` fields are the unimplemented
part; `Job` itself is very much wired.

---

### 2026-08-19 · `check-codegen.sh` attests the binding *source*, not the emitted bindings

Found while verifying a sub-agent's work on `release/01PMZ909-bundle-verify`.
The agent hand-wrote `frontend/wailsjs/go/models.ts` because it believed no
Wails toolchain was available, then ran
`check-codegen.sh --update-wailsjs-hash`. The gate went green.

The toolchain **was** available (`$HOME/go/bin/wails`). Running
`wails generate module` produced a file differing from the hand-mirror in 88
lines: the `trustanchor` namespace block was byte-identical (84 lines) but
placed near line 1085 instead of 7662, plus trailing-whitespace drift. A
sorted-line compare of the two files is empty, so this particular instance
was harmless — TypeScript does not care where a namespace sits in the module.

The gate hole is the point, not this instance. `check-codegen.sh` hashes the
Go **binding source** and compares it against a committed hash that any
author can restamp with `--update-wailsjs-hash`. So a green result asserts
*"the bindings were stamped for this source"*, never *"the bindings are what
Wails would emit."* Both the hand-mirror and the regenerated file pass it.
Nothing in CI regenerates the bindings and diffs the result, which means any
hand-edit of `frontend/wailsjs/**` — including a semantically wrong one —
passes as long as the hash is restamped.

This is the vacuous-pass shape: the gate cannot fail for the defect class a
reader assumes it covers. It belongs with the gate-falsifiability finding
(19 of 34 gates have a planted-violation proof; this one has none that
exercises output drift).

**Owed:** either regenerate-and-diff in CI (needs the Wails toolchain on the
runner — the same missing-toolchain constraint that produced the hand-mirror),
or a planted-violation proof in `scripts/ci/gates_can_fail_test.go` that
mutates a committed binding file and asserts the gate fails. It currently
would not. **Owner: unassigned. Not fixed here — recorded only.**

### 2026-08-20 · `maxVisibleBranchDepth`'s depth-overflow affordance — narrowed, not built

`controls-and-readouts-that-tell-the-truth-01PMZ808` WP02 (FR-002, SD-03 part
two). Three surfaces — `SettingsView.vue`'s help text,
`core/rpc/views/settings/api.go`'s field doc, and
`frontend/src/lib/types.ts`'s field doc — all promised that sessions nested
past the configured depth cap are hidden behind a click-to-expand
depth-overflow control. No such control exists: `LeftRail.vue` →
`SessionTreeRow.vue`'s `indentPx` only clamps how far a row indents; every
row still renders regardless of depth.

WP01 (same commit) wires `maxVisibleBranchDepth` from settings into
`LeftRail.vue` for the first time — the dial did nothing at all before this.
Landing WP01 without also correcting the three claims would have made the
dial *reachable* while still describing a hiding/expand behaviour it does
not have, which is the class this mission exists to end (spec D-1: "wiring
the value while the help text still describes a hiding behaviour moves the
lie, it does not end it").

- **Blocker:** building the affordance (hide rows past the cap, render a
  clickable depth-overflow control that reveals one more level) is a product
  call — register `E-002` — not a technical one; nobody has asked for it and
  no design exists for what the control should look like.
- **Owner / deleting change:** alec. Deletes when either a mission builds the
  affordance and re-widens the three docs, or the product decides depth
  clamping alone is the intended behaviour and this entry is closed as
  "decided, not deferred."

### 2026-08-20 · `registry.ts`'s "components never hard-code binding strings" claim — narrowed for the native menu only

`controls-and-readouts-that-tell-the-truth-01PMZ808` WP09 (FR-011,
register `E-003`). `Shell.vue`'s two global bindings (search, cheat sheet)
and `useCommandPalette.ts`'s ⌘K now resolve through
`shortcuts/registry.ts`'s `resolveBinding` against the persisted
`keyboardShortcuts` overrides — landed in this commit. The native OS menu
accelerators (`core/menu/menu.go`'s `keys.CmdOrCtrl(...)` literals for
Command Palette / Search / etc.) still do not: `core/menu/state.go`'s
`MenuState` carries no shortcut field, and no topic fires a menu rebuild
on a shortcut save.

Spec R-15 confirms the *mechanism* exists — `rebuildMenuLocked` calls
`wailsruntime.MenuSetApplicationMenu` at runtime, debounced and already
fired from three live subscriptions — the *payload* does not. Wiring it
requires: a shortcut field on `MenuState`, a broker topic (or reuse of an
existing one) firing on `Settings_Set` when `keyboardShortcuts` changes,
and `menu.go`'s `keys.CmdOrCtrl(...)` calls becoming dynamic per-binding
lookups instead of literals — a real, if small, feature, not a
one-line wire.

- **Blocker:** whether native-menu rebinding ships in this mission's scope
  at all is a product call (register `E-003`), not a technical one — no
  owner decision was available this session.
- **Owner / deleting change:** whoever answers `E-003` either lands the
  MenuState + topic + dynamic-accelerator wiring (deletes this entry), or
  decides native-menu accelerators are intentionally fixed regardless of
  the in-app override and narrows `registry.ts:5-6`'s claim to say so
  explicitly (also deletes this entry, the other direction).

### 2026-08-20 · `branchAdvisorDefaultModel` — narrowed, chain's second link is a stub

`controls-and-readouts-that-tell-the-truth-01PMZ808` WP05 (FR-005, SD-10).
`core/rpc/views/settings/api.go`'s field doc promised the field "Defaults to
CompactionModel when empty, which itself defaults to the session's active
model." The field has neither a reader nor a writer anywhere in production,
and `EffectiveBranchAdvisorDefaultModel` has zero callers. Per spec R-6 this
mission does **not** wire it: the chain's second link,
`core/rpc/views/branches/impl.go`'s `parentModel`, is a stub that discards
both its parameters and returns `("", "")` — wiring link one over a broken
link two would produce a dial that appears to work and silently resolves to
nothing, which is worse than the current honest inertness.

- **Blocker:** `parentModel` needs `Settings.CompactionModel` wired, which is
  owned by `model-settings-reach-the-model-01PMZ101`, not this mission.
- **Owner / deleting change:** whoever lands `01PMZ101`'s `CompactionModel`
  wiring should re-open `parentModel` and, once it resolves a real model,
  wire `branchAdvisorDefaultModel` and delete this entry. This ruling
  re-affirms (does not overturn) `docs/dead-code-audit-2026-08-16.md:330`'s
  "wire, and wire before mounting" — nothing is being mounted here.

### 2026-08-20 · Two pairs of missions share a migration block

`core/storage/migrations/blocks.go` reserves a numeric range per owning
mission so two missions cannot collide. Two pairs share one anyway:

```
"a2a":                         {Min: 600, Max: 699}
"signed-cards-trust":          {Min: 600, Max: 699}
"bundle":                      {Min: 700, Max: 799}
"shared-context-distribution": {Min: 700, Max: 799}
```

Found by the independent review of PR #299 and verified here. Pre-existing —
`git diff main...release/v0.65.0 -- core/storage/migrations/blocks.go` is
empty, so v0.65.0 neither caused nor touched it.

**Severity: latent, and loud rather than silent.** `Registry.Register`
returns `ErrVersionCollision` when a version is already registered, so a
real clash cannot corrupt a ledger — it fails at registration. But
registration happens inside `storagesqlite.Open`, so the failure mode is
**an install that will not start**, which is the exact shape of the v0.63.0
P0. It is one allocation away: `bundle/700` is already taken by
`trust_anchors_init` (bundle-download-and-verify-01PMZ909 UNIT-3), so the
first `shared-context-distribution` migration that picks 700 turns every
boot into a hard failure.

The block table's whole purpose is to make allocation decidable without
cross-mission coordination, and for these four missions it does not.

**Owed:** either give each mission a distinct block, or — if the pairing is
deliberate because the missions are two halves of one subsystem — say so in
a comment naming which mission owns which half of the range, so the next
allocator does not have to guess. Nothing currently records the intent.
**Owner: unassigned.**
### 2026-08-20 (vm-execution-surface-truth-01PMZD14 WP03) · the nil-optional-dependency gate does not exist yet — G-2 ships nothing

R-3 in this mission's spec. HV-03 (`cmd/harness-vm/agentexec.go`'s
`registry.Options` literal left `Policy` unset, silently substituting
`llm.AllowAllGuard{}`) is the **sixth** confirmed instance of the
nil-optional-dependency class in this campaign, which makes a gate the
obvious recurrence prevention. Three other v0.65.0-era missions
(`model-scheduled-jobs-01PMSJ01` UNIT-9/WP11, `model-settings-reach-the-model-01PMZ101`
UNIT-11/G-2, and a Z505 mission's G-1) were each independently designing that
gate. **Verified at this mission's dispatch (`ls scripts/ci/ | grep -iE
'nil|optional|dep'` → no matches):** none of the three had landed on this
merge base. Building a fourth gate here — after three other missions already
proposed one for the same class — would be rival infrastructure for a class
this repo already knows it wants exactly one instrument for.

This WP therefore ships only G-1 (widening `check-cedar-engine-singleton.sh`'s
Check 2 scan root to `core/`+`cmd/`, closing the specific evasion HV-03's own
fix could have taken) and does not build the nil-optional-dependency gate.

- **Blocker:** none of the three claimant missions (SJ01 UNIT-9/WP11, Z101
  G-2, a Z505 mission's G-1) had landed a nil-optional-dependency gate as of
  this mission's dispatch (2026-08-20).
- **Owner / deleting change:** whichever of the three lands its gate first
  should extend its package scan to cover `./cmd/...` (HV-03's own class is
  the concrete instance to plant as that gate's `cmd/`-scoped
  planted-violation proof) and delete this entry. Until then, HV-03's fix
  (WP02, same mission) is verified only by this mission's own
  `TestNewLLMExecutorPolicyGuardCanDeny`/`...AllowsWhenNotApplicable` tests,
  not by a standing gate that would catch a *future* nil-optional-dependency
  regression on this exact field.

### 2026-08-20 (vm-execution-surface-truth-01PMZD14) · UNIT-2 cut this run — the approval capability grant still overclaims (HV-01, HV-02, HV-05, HV-08)

This mission's floor (UNIT-0 + UNIT-1 + UNIT-PI) landed; UNIT-2 through
UNIT-6 were cut for scope in the run that produced UNIT-1, per the mission's
own cut-order rule (`tasks.md` "Cut order", item 5: *"If UNIT-2 is cut,
UNIT-0's record must say so explicitly ... an undated known lie is what this
ritual exists to end"*). Recording that explicitly here, since UNIT-2's own
WP05 was the unit that would have filed the dated justifications for
`approvalGateFrom` and `runStatus` directly.

Current state, unchanged by this run: `cmd/harness-vm/main.go` grants the
`approval` capability whenever `readservice.go`'s `promptRegistry()` is
non-nil — which is true on every boot where the read-service bootstrap
succeeded, since `core/rpc/api.go`'s `rpc.New` assigns the prompt registry
unconditionally. **No gate site in this process can raise an approval**:
`approvalGateFrom` (`cmd/harness-vm/approvalgate.go`) and
`approvalBridge.runStatus()` (same file) each have zero non-test callers
(verified by this mission's WP01 observation, re-confirmed identical to the
spec's own RAN ledger). `contracts/vm-rpc.md:474-477` still asserts, in the
present tense, a call site that does not exist and contradicts itself three
lines later.

- **Blocker (HV-01 / `approvalGateFrom`):** no `cedar.PromptSurface` variant
  exists for a model call — `core/policy/cedar/prompt.go`'s `PromptSurface`
  is a closed four-variant union (`Bash`/`FS`/`Cred`/`Tool`) enforced by
  `Family()`. Adding a fifth variant is a product feature (a new host modal,
  a wire payload change) with a product owner, not a wiring fix
  (`vm-execution-surface-truth-01PMZD14/spec.md` R-2).
- **Blocker (HV-05 / `runStatus`):** the wire has no status field and
  `contracts/vm-rpc.md:436-438` explicitly forbids adding one (*"Run status
  is DERIVED, not a wire field"*); the named future consumer is the deferred
  `agent_feed.*` push stream (`contracts/vm-rpc.md:482-483`).
- **Blocker (HV-02, the grant itself / HV-08, the contract's stale smoke
  probe):** a genuine product call — whether the `approval` capability
  should be granted at all when nothing in the process can raise one (escalation
  E-002 in the mission spec) — was not answered before this run's scope cut.
  The spec's default disposition is (c): make the grant self-describing
  ("this process is listening") rather than narrowing it to never-granted.
- **Owner / deleting change:** land `vm-execution-surface-truth-01PMZD14`
  UNIT-2 (WP04 + WP05, `contracts/vm-rpc.md`'s two corrections plus
  `main.go:145-155`'s comment and the approval-grant self-description),
  which deletes this entry and files HV-01/HV-05's dated justifications
  directly. Owner: alecfeeman. Filed 2026-08-20 by the same mission's WP01
  scope-cut record.

### 2026-08-20 (vm-execution-surface-truth-01PMZD14) · UNIT-4 cut this run — `cmd/harness-vm` boots `core.New → Start → rpc.New`, the reverse of both shipped entry points (HV-N1)

Cut per the mission's own cut-order rule (`tasks.md` item 2: *"If UNIT-4 is
cut, HV-N1 gets a dated entry ... blocker: arming two never-run bootstraps
needs a soak this release has no room for"*).

`cmd/harness-vm/readservice.go`'s `newReadService` calls `core.New` → then
`c.Start(ctx)` → then `rpc.New(c)`. **Both shipped entry points invert
that order**: `main.go` and `cmd/harness-served/main.go` both call
`core.New → rpc.New → Start`. The order matters because `rpc.New` is what
installs `Core`'s Start hooks (`c.SetMCPRecipeBootstrap(...)` in
`core/rpc/api.go`'s `New`); `Core.Start` invokes them. In
`cmd/harness-vm`, both hook fields are still nil when `Start` runs, so
**neither hook ever fires**: the MCP recipe spawn bootstrap (also the only
boot-time Cedar gate site in this process — a second, independent reason the
UNIT-2 finding above is a lie even on its boot-time path) and the first-boot
bash-allowlist migration both silently never run in this process.

This is a persistence-adjacent finding, not a cosmetic one: reordering arms
a bootstrap (`BashAllowlistMigrated`) that writes persisted state and has
**never run in this process against any database** — `cmd/harness-vm`'s
`readservice.go` opens the harness's real data directory
(`HARNESS_READ_DATADIR` or `paths.DataDir()`), not a scratch one.

- **Blocker:** arming two never-run bootstraps in the same process that opens
  the user's real data directory needs its own soak — a populated-table test
  booting from a committed `core/storage/sqlite/testdata/upgrade/` snapshot,
  per `CLAUDE.md` blind spot #3's corollary — which this run's floor-only
  scope had no room for. (Separately and pre-existing: this tree's newest
  committed snapshot is `v0.64.1` while the newest release tag is `v0.65.0` —
  `scripts/ci/check-upgrade-snapshot-present.sh` reports this red already,
  independent of this mission. See `AC-PI-1`'s notes in this mission's report
  — WP07 would need that gap closed, or would need to boot from `v0.64.1`
  and say so, before its own AC-PI-1 falsification is meaningful.)
- **Owner / deleting change:** land `vm-execution-surface-truth-01PMZD14`
  UNIT-4 (WP07 reorders `core.New → rpc.New → Start` to match the two
  shipped entry points, with `AC-013`/`AC-014`'s hook-fires / degrades-clean
  proofs and `AC-PI-1`'s populated-table boot; WP08 adds the
  `KENAZ_HARNESS_WORKSPACE` read the same six lines are missing). Owner:
  alecfeeman. Filed 2026-08-20 by the same mission's WP01 scope-cut record.

### 2026-08-20 · The snapshot dumper emitted raw BLOB bytes as a quoted SQL string

Found independently by `audit-that-tells-the-truth-01PMZA10` while
generating the `v0.65.0` snapshot, in parallel with the FTS5 shadow-table
fix in PR #300. The two are separate bugs in the same dumper.

`upgradesnap` (and its duplicate in `scripts/ci/upgrade-snapshot/`) wrapped
`[]byte` column values in `quoteStr` — a quoted SQL string literal —
instead of emitting a `X'...'` hex literal. `events_fts_data`'s shadow
columns hold real compressed FTS5 blobs, so the regenerated dump came out
as `data` rather than text under `file(1)`.

**Why the committed `v0.65.0` dump is nevertheless clean** (verified, not
assumed): PR #300's fix excludes FTS5 shadow tables from the dump
entirely, and those are the only BLOB-bearing tables in the schema today.
With them gone there is nothing left to mis-encode — the committed file is
ASCII with zero non-printable bytes. The encoder was still wrong, and would
have produced a binary, unreplayable snapshot for **any** future table with
a BLOB column. Both fixes are now in.

**The standing hazard is the duplication, not either bug.** The dump logic
exists twice on purpose (`upgradesnap` does not exist at old tags, so the
generator must be self-contained), and **nothing compares the two sources**.
`TestDumpMaterializeRoundTrip` round-trips only `upgradesnap`'s copy. Both
of these bugs existed in both copies, and both had to be fixed twice. The
header comment claiming a test keeps them in sync describes a test that
does not exist.

**Owed:** a real source-comparison test, or a single shared implementation
with a build-tag shim. **Owner: unassigned.**

### 2026-08-20 · `branch.created` is audited on one path and not the other, and the audited path has no test

The adversarial review of `release/v0.66.0` flagged that
`audit-that-tells-the-truth-01PMZA10` WP06's commit overstated its own
evidence: it claimed each view's existing package tests "already cover what
the emit site does once a non-nil emitter reaches it", and for
`core/rpc/views/branches` and `core/rpc/views/tools` that is false — no test
in either package ever sets `Config.Audit`, and `audit.MustEmit` is nil-safe,
so those suites pass whether the field is wired or not.

Verified here, and it is worse than an evidence gap. `core/rpc/views/update`
has no audit test wiring either (three packages, not two). And when a
spy-emitter test was actually written against `branches`, it failed:

    no branch.created event reached the configured audit emitter; got []

`CreateBranch` (`core/rpc/views/branches/impl.go:139`) has two paths:

- **explicit fork** (`opts.ParentMessageID != ""`) → delegates to
  `CreateBranchAtMessage` and emits `KindBranchCreated` (`impl.go:159`).
- **legacy** (no `ParentMessageID`) → creates the branch and emits
  **nothing**.

So the audit log records only some branch creations. Whether that is
intended is a WP06 question — the emit site's own comment says "for explicit
path", which reads deliberate — but an audit trail that silently covers a
subset is exactly the class this mission exists to close, and nothing states
the intent.

Compounding it: **no test in the package passes `ParentMessageID` at all**,
so the only path that emits has no coverage whatsoever. The explicit path
needs a real persisted parent message, which the current fixture
(`newTestStack`) does not build.

**Sharpened by the approving review of PR #301 (2026-08-21).** The
unaudited path is not an edge case — it is the *ordinary* one.
`frontend/src/components/chat/CreateBranchModal.vue`, opened by the "+ Fork"
button and reached from `ChatInput.vue` / `BranchSidebar.vue` /
`BranchSuggestionBanner.vue`, sends **no** `ParentMessageID`. So a user
creating a branch the normal way produces **zero** audit trail, while only
the fork-at-a-specific-message path is recorded. A trust surface that audits
the rare path and not the common one is worse than one that audits neither,
because the log looks populated.

**Owed:** decide whether the legacy path should audit — it almost certainly
should, given the above; then a spy-emitter test on whichever paths are
meant to emit, for `branches`, `tools` and `update` (all three lack one).
The test written during this investigation was removed rather than left red
or weakened to pass — a green test over the non-emitting path would have
enshrined the gap.

**Owner: alec. Date: 2026-08-21. Belongs with ZA10 WP06.** Recorded with an
owner because CLAUDE.md's own rule is that a justification names the blocker
*and* the owner — the first version of this entry said "unassigned", which
the PR #301 reviewer correctly flagged as failing that bar. It approved the
release anyway on the grounds that the gap is honestly disclosed, is a net
improvement over zero branch auditing, and matches how equivalent findings
are carried elsewhere in the same PR. That reasoning is sound and the
release shipped; the owner gap is fixed here rather than left as a second
lie about the first.

**RESOLVED 2026-08-21, ZA10 WP06.** `CreateBranch`'s legacy path
(`core/rpc/views/branches/impl.go`) now calls `audit.MustEmit(...,
audit.KindBranchCreated, ...)` immediately before `publishBranchCreated`,
using `br.CreationPath` (which `conversation.Manager.CreateBranch` already
resolves to `"unknown"` when the caller specifies nothing — the ordinary
"+ Fork" case today). The legacy path's `ForkOptions` construction was also
silently dropping `opts.CreationPath`, so a caller-supplied
`"edit_resend"` never reached storage either; both are now threaded
through. `TestAPI_CreateBranch_LegacyPathEmitsAudit` and
`TestAPI_CreateBranch_LegacyPathThreadsCreationPath` were written first and
confirmed to fail against the pre-fix code with "no branch.created event
reached the configured audit emitter; got []"; `TestAPI_CreateBranch_
ExplicitPathStillEmitsAudit` pins the already-correct explicit path so a
regression on the shared emit call is caught in the same file.

Spy-emitter coverage was also added for `tools` (`TestInstallRecipe_
EmitsAudit`, `TestUninstallRecipe_EmitsAudit`, `TestForgetRecipeKey_
EmitsAudit` in `core/rpc/views/tools/impl_test.go`) — unlike `branches`,
these three `a.emit(...)` call sites (`impl.go:401,649,669`) were already
correctly wired; this closes the evidence gap without a functional
change.

`update` needed no fix and no new test: `core/rpc/views/update` (the RPC
view) has no `Config.Audit` field at all — it wraps an already-constructed
`coreupdate.Service`, injected in by `core/rpc/api.go`, and does not itself
own any emit call. The actual audit owner is `core/update/audit.go`'s six
`audit.MustEmit` sites in the `core/update` package (not
`core/rpc/views/update`), which already have full spy-emitter coverage via
`core/update/integration_test.go` (kind-ordering assertions across all six
`Kind*` values) and a live, non-nil emitter at the production construction
site (`core/rpc/api.go:2645-2654`, itself a ZA10 UNIT-5 fix, already
shipped). The ledger's "three packages" framing conflated the RPC-view
wrapper with its underlying service; once that boundary is drawn, `update`
was never actually missing coverage.

## Drained

### 2026-08-19 · CLOSED — the missing-upgrade-snapshot hole is now gated

Three consecutive releases shipped without their snapshot, and each one's
PROVENANCE.md named the missing gate and then left it missing:

- **v0.63.2** — *"the lock gate catches modification but nothing catches
  absence."*
- **v0.64.0** — *"This is now twice. A convention that depends on a human
  remembering it at release time has failed on two consecutive releases."*
  This was the release whose PR title claims *"CI can finally see an
  upgrade."*
- **v0.64.1** — found open while writing this entry: latest tag `v0.64.1`,
  latest snapshot `v0.64.0`.

Writing the diagnosis into a document that is only read while performing the
ritual that keeps being skipped is not a fix. The gate now exists:

- `scripts/ci/check-upgrade-snapshot-present.sh` — fails when
  `max(testdata/upgrade/v*)` is behind `max(git tag v*)`. Stable tags only;
  `-rc` tags are soak builds and owe no snapshot. With **no** tags reachable
  it FAILS rather than passing, matching the lock gate's rule that a gate
  which cannot look at anything is not clean.
- Wired into `pr.yml` beside the lock gate, whose tag-fetch step is now
  load-bearing for both.
- Planted-violation proof:
  `upgrade-snapshot-present/chain-behind-newest-tag`. Verified falsifiable in
  **both** directions — against a neutered gate (`exit 0`) it fails with *"the
  gate cannot fail"*, and against a gate that exits non-zero for an unrelated
  reason it fails the `wantOutput` check. That second direction matters here:
  `check-tests-are-hermetic.sh` once shipped permanently broken and this
  table passed anyway.
- `v0.64.1`'s snapshot was backfilled in the same change, so the chain is
  current. Its `dump.sql` is byte-identical to `v0.64.0`'s — correct, since
  that release registered no migration.

**A harness limitation was removed to make this possible.**
`TestGates_PlantedViolationFires` called `plant()` unconditionally, so it
could only express violations you create by *adding a file*. An absence gate's
violation cannot be planted that way — you create it by moving the tag
forward, not by writing anything. The runner now allows a case with no `file`,
and requires such a case to name a `wantOutput` so the proof stays about the
specific violation rather than about a non-zero exit. Any future
absence-shaped gate can now be proven the same way.

Note the ledger entry dated 2026-08-19 above — `check-codegen.sh` attesting
binding *source* rather than emitted bindings — is the same vacuous-pass
class and remains **open**.


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
  Owner: **escalated 2026-08-19 as G-3** (`docs/escalation-register-2026-08-19.md`
  Part 8), per ruling F-1. The fix is a length-bounded variant, not an
  open-ended one. **Missed by F-1's count of sixteen** — this row uses the
  unbolded `Owner:` form; see Part 8 §8.3-P2.
- **`core/event/redact.defaultMatchers` has NOT been widened.** The
  export catalog began as a copy of it and has now diverged; that one
  still has all ten original patterns including the JSON-blind generic.
  It feeds the audit log's HMAC pipeline, which is a different contract,
  and widening a live audit pipeline from inside an export fix is the
  wrong blast radius. Owner: **escalated 2026-08-19 as G-3**
  (`docs/escalation-register-2026-08-19.md` Part 8), per ruling F-1 — G-3's
  recommended default for this one is a compliant
  `justify(blocker, owner, date)` rather than work, since the blast-radius
  reason above is a real blocker that only lacks a name. **Missed by F-1's
  count of sixteen** — unbolded form; see Part 8 §8.3-P2.
  **If you are auditing what the
  event log redacts, do not read the export catalog and assume parity.**
- **`core/eval/capture.go:137` `redactString` is much weaker than any of
  the four catalogs** — it handles `sk-` and `Bearer ` and nothing else,
  no GitHub token, no AWS key, no JWT, no password, no cookie — and it
  writes LLM messages to disk. Its own comment calls it "defense-in-depth"
  behind the event log, which is true of the event-log path and not of
  the capture FILE. Owner: **escalated 2026-08-19 as G-3**
  (`docs/escalation-register-2026-08-19.md` Part 8), per ruling F-1 — and G-3
  singles this one out: it is the only redaction park whose failure mode is a
  credential at rest on the user's disk, so "gated behind eval capture being
  enabled" is a mitigation, not a blocker, and the rubric's *trust-relevant*
  class does not park without an explicit ruling. **Missed by F-1's count of
  sixteen** — unbolded form; see Part 8 §8.3-P2, which notes the phrasing
  accident hid the highest-severity item in the set.
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
