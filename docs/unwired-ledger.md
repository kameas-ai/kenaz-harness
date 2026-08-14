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

### 2026-08-14 · Move metadata reaches the wire ahead of its consumers

*(Updated 2026-08-14 by WP02 — two of WP01's three entries are drained;
see the Drained section. What remains:)*

`sessions.Message.{Kind,MoveIndex,TurnSpanID}` (core/rpc/views/sessions/api.go)
and their `frontend/src/lib/types.ts` mirror are populated by
`messageToView` and read by nothing today. WP02 fills the columns behind
them on every chat turn, so the wire now carries real values.

**THIS ONE IS NOT INERT — IT IS USER-VISIBLE, AND WRONG UNTIL WP04**
(corrected 2026-08-14 by the adversarial review of WP02; the sentence
here previously claimed "the chat surface still renders one bubble per
turn until WP04 reads them", which is false). `MessageList.vue` renders
one `MessageBubble` per persisted row with **no filter on role or
kind** — `visibleMessages` (SessionsView.vue) only concatenates
transient slash results. A five-iteration tool-using turn that used to
be 2 rows is now ~13, so on **reload** today's UI shows:

- each `assistant_move` as a full, unlabelled assistant bubble
  (indistinguishable from the answer, "branch from this turn" included);
- each `tool_call` / `tool_result` as a bare monospace line with no
  `whitespace-pre-wrap` — i.e. the **raw, untruncated tool output**
  flattened into one run-on line in the conversation;
- `useLongSessionNudge` counting rows/2 as "turns", so the long-session
  banner trips roughly 3x early.

(Not during the turn: there is no live-append path — `Sessions_StartStream`
is a stub and nothing publishes `message_appended` — so the new rows
appear only on reload / refresh / `/clear`.)

**Sequencing constraint, not just a consumer note: 01PMCH01 must not
reach a release tag between WP02 and WP04.** On this repo's flow a
`feat:` merge to main cuts a prod tag immediately, so landing WP02
alone ships a visibly broken transcript. WP02 is safe on the mission
branch; the release gate is WP04 (+ WP05 collapse).

This is WP01 landing the schema + the single writer seam ahead of the
code that emits and renders moves, which is the sequencing plan.md fixes
deliberately (the entry shape is the contract WP02–WP05 build on).

**Consumers:** WP04 (move bubbles + tool chips), WP05 (collapse +
export). **Owner:** 01PMCH01.

**Deadline:** these lines are drained when 01PMCH01 ships. If the mission
is abandoned, the fields and migration 0333 go with it — a nullable
column nothing writes is the same lie as a toggle nothing reads. If the
mission stalls after WP02, the columns must stop being WRITTEN, not just
left unread.

### 2026-08-14 · The stream move-boundary marker has no reader yet (WP02)

`llm.StreamMoveStart` + `llm.MoveBoundary` + `llm.StreamEvent.Move`, and
their kernel-side mirrors `agentgraph.StreamEventMoveStart` +
`StreamEvent.{MoveIndex,MoveKind}`, are EMITTED on every chat turn by the
move journal (`core/rpc/views/agentgraph/chat/moves.go`) and translated
onto the `llm:stream-chunk` topic by `translateAGStreamEvent`. Nothing on
the frontend branches on the kind yet, so today the marker is an event
the chat surface receives and ignores.

That is the WP02/WP04 split plan.md specifies: WP02 defines the contract
(one boundary per persisted move, same count, same order, same index —
the doc comment on `llm.MoveBoundary` is the contract text) and WP04
consumes it to split the run-on paragraph into bubbles. The contract is
pinned from the producing side today by
`TestMoves_StreamBoundariesMatchPersistedMoves`, so WP04 inherits a
guarantee rather than a hope.

**Consumer:** WP04 (live view: move bubbles + tool chips).
**Owner:** 01PMCH01.

### 2026-08-14 · `session.TranscriptEntry.ContentBlocks` has no production writer (WP02)

Added by WP02 to close a WP01 review finding: without it the transcript
seam could express strictly LESS than `Manager.AppendMessage`, so an
author needing a multimodal entry had to leave the seam — and the
off-seam path cannot stamp move metadata, so the move degraded to a
classic entry with neither the compiler nor
`check-single-move-writer.sh` objecting. The field removes the reason to
leave the seam.

Precision (adversarial review of WP02): that makes the degradation
**unmotivated, not unreachable.** `Manager.AppendMessage` is exported and
still accepts an assistant row — `SendMessageWithBlocks` uses it, legally,
for the user turn. What the compiler forbids is minting move METADATA off
the seam; it does not forbid writing a row that should have been a move
and silently is not. The gate cannot see that class either: clause 3
counts callers of `AppendTranscriptEntry`, not writers that avoid it.
The residual guard is that there is no longer any expressiveness reason
to take the off-seam path.

Read on every append (`AppendTranscriptEntry` → `canonicalBlocks`) and
round-tripped by `TestAppendTranscriptEntry_CarriesContentBlocks`, but
every production caller leaves it empty today: the moves WP02 writes are
text. It becomes load-bearing the first time a move carries an image.

**Consumers:** WP03 (per-family rendering must not re-flatten a
multimodal move on its way to the provider), WP05 (export/share
fidelity). **Owner:** 01PMCH01.

**Deadline:** if 01PMCH01 ships without either consumer setting it, the
right disposition is to delete the field and record the expressiveness
hole as an open finding — not to leave it here for a fourth release.

**READ HALF DRAINED 2026-08-14 by WP03. WRITE HALF STILL OPEN.** Be
precise about which half moved, because the deadline above is written
against the write half ("without either consumer *setting* it"):

- *Drained.* WP03's named obligation here was "per-family rendering must
  not re-flatten a multimodal move on its way to the provider", and it is
  now discharged. The model-visible composition
  (`core/rpc/model_history.go` → `textMessage`) prefers `ContentBlocks`
  over the flattened `Content` column and carries the canonical
  polymorphic body onward to the family renderers. Before WP03 the
  projection was `Role`+`Content` only — a blocks-bearing row reached the
  model with whatever text happened to sit beside them, or with nothing.
  Pinned by `TestModelHistory_ContentBlocksAreNotReflattened`, which
  fails if the branch is deleted.
- *Still open.* No production code SETS `TranscriptEntry.ContentBlocks`.
  The write half needs `coreag.HistoryEntry` to carry blocks too (it does
  not today), which is the shape a multimodal tool result or a captured
  generated image would travel in. That is WP05's, and the deadline above
  stands unchanged against it: if 01PMCH01 ships with no writer, delete
  the field rather than carry it a fourth release.

**Remaining consumer:** WP05 (export/share fidelity, and the writer).

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

### 2026-08-14 · The dial cascade is an inert subsystem

`core/agentgraph/dials` has exactly one non-test importer
(`core/rpc/views/dials/impl.go`) and `dials.Resolve` exactly one production
call site, whose output feeds only `Dials_GetEffective` → `DialsView.vue`.
All 13 `DialConfig` fields are **plumbed-only**: they render in a UI readout
and change no behaviour. The kernel's budget comes from `graph.Budget`
folded with the autonomy token-ceiling knob (`chat_runner.go`), never from
`EffectiveDials`.

Two sharp edges inside it:

- `ManifestDriftMode` is doubly dead — `dials/cascade.go`'s `applyLayer`
  has no branch for it and `EffectiveDials` has no such field, so it cannot
  survive `Resolve()`. Recorded against the I10 line for
  `EnforceManifestDriftPolicy`, whose old justification asserted the
  opposite.
- `Dials_BumpAndResume` mutates the view's in-memory store and then resumes
  the run; the bumped cap never reaches `env.Budget`, so
  `CapHitToast.vue`'s "bump and resume" resumes at the original cap.

**Owner:** unassigned. Wire the cascade into `env.Budget` or delete the
subsystem; a UI that reports numbers nothing enforces is worse than no UI.

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

`core/tasks` ships a SQLite store, ring buffers, boot-time orphan recovery,
four live RPCs, a mounted Settings → Tasks panel, a session-close dialog and
a registered `background_task_complete` hook event — and nothing ever calls
`Registry.Register`, because `bash.Options.BackgroundSpawn` has no non-test
assignment. `Registry.StdoutWriter` / `StderrWriter` likewise have no
callers: `spawnBackground` calls `cmd.Start()` before it has a task id, so a
background task could not capture output even if the seam were passed.

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

---

## Drained

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
