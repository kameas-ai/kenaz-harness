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

## In-flight (mission 01PMCH01 — model moves in the transcript)

Fields that exist and are not yet read. Listed here so the next sweep does
not re-find them as inert plumbing: each names the WP that consumes it and
the date it was written. A line here that outlives its mission is a
finding, not an exemption.

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

- ~~`MessageList.vue:162-170` + `MessageBubble.vue:549-553` count
  **rows**, not turns, for "Summary of N turns"~~ — **FIXED 2026-08-14
  by WP05.** `MessageList` now calls `transcript.foldedTurnCounts`,
  which attributes each folded row to the turn that opened it
  (`turnSpanId`, or the most recent preceding user row for a classic
  row) and counts distinct turns. WP05 made the call WP04 declined to
  make unilaterally: the two green assertions
  (`CompactionFlow.e2e.spec.ts` "SummaryIndicatorRenders" and
  `CompactedHistory.spec.ts` "renders the Summary of N turns
  indicator") moved from "Summary of 2 turns" to "Summary of 1 turn",
  because both fixtures fold ONE exchange — a user row and the
  assistant row answering it — and both now carry a comment saying so.
  Pinned by `lib/__tests__/foldedTurnCounts.test.ts`; reverting to the
  row count fails four of its five cases.
- **Search corpus**: migration 0312's FTS trigger
  (`core/session/migrations_search_fts.go:47`) fires on every
  `session_messages` insert with no role filter, so every `tool_result`
  row's raw output is now full-text indexed. `SearchModal.vue:347-350`
  offers User/Assistant/System and **no Tool option**;
  `SearchPalette.vue` has no role filter at all. Cmd-F degrades
  materially on a move-bearing session.
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

Also open, and not a projection bug: on the **revised-draft** path the
exit gate's revised text never streams, so the live view shows the draft
as the answer while a reload shows the revision. FR-003's "no post-hoc
mismatch" is not fully true there until the backend delivers the
revision on the stream.

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

### 2026-08-14 · `export.RedactValue` only walks TOP-LEVEL strings (pre-existing)

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

- `core/rpc/views/contextsync/impl.go:151-160` calls
  `Handoff.ShareSession(ctx, sessionID, recipientUserID, nil)` — a
  literal `nil` event slice, with a comment deferring the real payload to
  "the chassis path". There is no chassis-path caller; every other caller
  of `ShareSession` is a test.
- `core/fleet/team_handoff.go:112` then POSTs `"events": []`.
- Even if the `nil` were replaced, the fleet event record could not carry
  moves: `core/rpc/api.go:6198-6206` marshals
  `{"id": …, "role": …}` and nothing else, deliberately — the wiring
  comment at `core/rpc/api.go:2447` states no plaintext content crosses
  that boundary. `core/fleet/session_sync.go:39-45`'s doc claiming the
  record is "usually JSON of session.Message" is stale against that
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
`session.ToolCall` is `core/rpc/api.go:6228`, which sets `{ID, Name,
IsError}` and nothing else — by design: `Arguments` belongs to the
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

### 2026-08-14 · The dial cascade is an inert subsystem

*(Corrected 2026-08-14 by the frontend orphan-deletion sweep — the
consumer claim below was false: see the two bullets after the summary.)*

`core/agentgraph/dials` has exactly one non-test importer
(`core/rpc/views/dials/impl.go`) and `dials.Resolve` exactly one production
call site, whose output feeds only `Dials_GetEffective`. All 13
`DialConfig` fields are **plumbed-only**: they render in a UI readout and
change no behaviour. The kernel's budget comes from `graph.Budget` folded
with the autonomy token-ceiling knob (`chat_runner.go`), never from
`EffectiveDials`.

Two sharp edges inside it:

- **`Dials_GetEffective` has ZERO live consumers, not one.** The previous
  entry claimed its output "feeds only `Dials_GetEffective` →
  `DialsView.vue`" — but `DialsView.vue` has no route in either `main.ts`
  or `main-served.ts` and no non-test importer anywhere in
  `frontend/src`; the only file that references it is its own
  `views/dials/__tests__/DialsView.test.ts`. The subsystem is one notch
  deader than recorded: the RPC's output reaches a view nothing mounts.
- `ManifestDriftMode` is doubly dead — `dials/cascade.go`'s `applyLayer`
  has no branch for it and `EffectiveDials` has no such field, so it cannot
  survive `Resolve()`. Recorded against the I10 line for
  `EnforceManifestDriftPolicy`, whose old justification asserted the
  opposite.
- `Dials_BumpAndResume` mutates the view's in-memory store and then resumes
  the run; the bumped cap never reaches `env.Budget`. The previous entry
  claimed this means "`CapHitToast.vue`'s 'bump and resume' resumes at the
  original cap" — that is also false as evidence: `CapHitToast.vue` is
  never mounted (see the CapHitToast entry below), so the described bug is
  unreachable through the UI. The `Dials_BumpAndResume` no-op itself is
  still real; it just has no live caller to trigger it.

**Owner:** unassigned. Wire the cascade into `env.Budget` or delete the
subsystem; an RPC that reports numbers nothing enforces, feeding a view
nothing mounts, is worse than no UI at all.

### 2026-08-14 · `CapHitToast.vue` was never mounted

`CapHitToast.vue` has zero production importers — the only file besides
its own test that names it is `composables/useEventToasts.ts:17`, which
lists it in a comment as "NOT migrated here (retained as rich-UI
components because they need input fields or sliders)". That claim is
false: `git log -S "CapHitToast.vue" -- frontend/src` shows exactly one
commit, `c760087f` (the commit that created the component), and no commit
since has ever added an import or a mount site for it. It was never
"retained" from anywhere — it was never wired in the first place.

Consequence: the "bump and resume resumes at the original cap" bug
recorded against `Dials_BumpAndResume` above has no UI path to trigger it
today. The bug in the Go code may still be real; it is simply unreachable
until something mounts `CapHitToast.vue` (or another cap-hit surface) and
wires it to `Dials_BumpAndResume`.

**Owner:** unassigned. Either mount `CapHitToast.vue` on the cap-hit event
and fix the resume-cap bug together, or delete the component and record
the resume-cap bug as an open Go-side finding.

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

### 2026-08-14 · knob-coverage tracks 9 fields out of ~98

`core/wiring/knobcoverage` is a general mechanism, and exactly one struct
uses it: `autonomy.ResolvedKnobs` (9 fields, all genuinely live).
`settings.Settings` (76 fields) and `dials.DialConfig` (13) are outside it
entirely — which is why the two entries above were found by hand rather
than by CI. `RegisterDeferred` is also an unbounded escape hatch: no
allowlist file, no dates, no monotonic-shrink rule.

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

**Correction: only the Tasks panel half is true.** `TasksPanel` IS mounted
(`SettingsView.vue:29` imports it, `:1126` renders it) — that part of the
original claim stands. But `components/sessions/SessionCloseDialog.vue`
has **zero importers** anywhere in `frontend/src` — no route, no parent
component, no test. It is exactly as unreached as the Go producer side it
was meant to gate.

Consequences already addressed this sweep: `kenaz__bash` no longer
advertises `run_in_background` while the seam is nil (commit `fix(tools):
stop advertising bash background mode…`), and `kenaz__monitor` is now
tracked by I11 rather than buried in the I7 bulk list.

**Owner:** unassigned. The fix is one restructuring — allocate the task id
before `cmd.Start()`, attach the registry writers, pass
`BackgroundSpawn`/`BackgroundEnd` and the `HookFirer` from `core/rpc`, then
register `kenaz__monitor` with its predicate case.

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

**P2 — wire candidates** (backend confirmed live; frontend surface exists
but is unreached or half-wired):

- `RecoveryCodeFlow` — backend verified live end-to-end; the frontend
  surface just needs a mount point.
- `AgentsView` + `AgentProfileEditor`.
- `CrashReportingOnboardingModal`.
- `RunChainView`.
- `ModelSizeBadge` + `lib/modelFit.ts`.

**P3 — owner-decision clusters** (each needs a wire-or-delete call from
the subsystem owner, not a mechanical fix):

- The dials cascade (see the dedicated entry above).
- **Cedar propose — a four-layer dead subsystem.**
  `harness_write_propose_cedar_policy` is registered and advertised to the
  model, but `NewCedarProposer` has zero non-test call sites, so it
  returns `errNotConfigured` on every call — 100% of the time, not
  intermittently.
- The background-task subsystem (see the dedicated entry above).
- `CedarEditor` vs. `PolicyView`'s inline editor — two policy-editing
  surfaces, unclear which is canonical.
- `MCPHealthSettingsPanel` + `BranchAdvisorSettings` — both blocked on
  inert Go knobs (see "Settings fields that are stored, bound, and
  inert" above).
- `DenialNotice` / `usePolicyDecisions`.
- `ProjectAutonomyPanel`.
- `BranchContextPickModal`.
- `CompactionSettings`.
- `HookJournalView`.
- `lib/rail.ts`.
- `lib/capability-keys.ts`.

**Owner:** unassigned per-item; this entry itself is owned by whoever
picks up the next unwired sweep. Re-verify each item's importer graph
before acting — frontend code churns between sweeps.

---

## Drained

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
