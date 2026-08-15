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

- `MessageList.vue:162-170` + `MessageBubble.vue:549-553` count **rows**,
  not turns, for "Summary of N turns" — the same arithmetic error WP04
  fixed in `useLongSessionNudge`, 100 lines above the fix. Three
  move-bearing turns compact to "Summary of 39 turns". Pre-existing (it
  was already 2x wrong), and correcting it moves two green e2e
  assertions, so it was deliberately not fixed unilaterally.
- **Search corpus**: migration 0312's FTS trigger
  (`core/session/migrations_search_fts.go:47`) fires on every
  `session_messages` insert with no role filter, so every `tool_result`
  row's raw output is now full-text indexed. `SearchModal.vue:347-350`
  offers User/Assistant/System and **no Tool option**;
  `SearchPalette.vue` has no role filter at all. Cmd-F degrades
  materially on a move-bearing session.
- **`Sessions_Export`** (Go, markdown + JSON) walks tool rows with raw
  output — FR-006 territory.

Also open, and not a projection bug: on the **revised-draft** path the
exit gate's revised text never streams, so the live view shows the draft
as the answer while a reload shows the revision. FR-003's "no post-hoc
mismatch" is not fully true there until the backend delivers the
revision on the stream.

Whoever mounts `SubagentTab.vue` (dead today) must route through
`projectTranscript` or it reinherits the 13-bubble regression.

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

**The gap:** the harness has no denial-aware UI at all. `policyAPI` is
assigned exactly once, at `core/rpc/api.go:1109`, to `&stubPolicy{}`;
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

---

## Drained

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
