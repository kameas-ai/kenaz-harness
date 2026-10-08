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
| I14 | `check-broker-topic-consumers.sh` | `i14-unconsumed-broker-topics.txt` | A const under `core/**` whose **identifier contains `Topic`** and whose value is a broker topic, with no frontend subscriber, no Go subscriber, and no `passthroughTopics` entry — **added 2026-08-18; one entry (`mcp:progress`)**. Multi-pass (Go + frontend), same discipline as `check-output-ports.sh`. Wired into `pr.yml`. See "Declined gate" below for why this shipped instead of the gate `docs/dead-code-audit-2026-08-16.md` §5 originally asked for, and the allowlist header for why "contains" rather than "starts with" is the whole gate. **G-0 correction (connector-lifecycle-truth-01PMZ303 spec.md §1.12 R-6, landed with UNIT-8):** the gate used to pass vacuously on a topic whose only "consumer" was its own declaring file's `Subscribe` call (exactly `mcp:health-changed`'s pre-UNIT-8 shape — `views/mcp/impl.go:240` both declared `TopicMCPHealthChanged` and subscribed to it, with no publisher and no real reader). The gate now explicitly excludes a Go subscriber in the SAME file as the const's declaration, and additionally requires a real `.Publish*(` call site — see the script's own R-6 citation and `TestGates_PlantedViolationFires/broker-topic-consumers/self-subscribe-only`. |
| I15 | `check-cedar-engine-singleton.sh` | *(none — no allowlist by design)* | more than one Cedar engine construction (`buildCedarGate`/`buildCedarEngineOrNil` call, or a direct `cedar.NewEngine` call) reachable from `rpc.New` — **added 2026-08-18 (consent-surfaces-truth-01PMTR01 WP05)**. I13 checks the *argument* at a call site; it has no vocabulary for *instance count*, which is why thirteen independent engine constructions (nine `buildCedarGate` + four `buildCedarEngineOrNil`) all passed it clean before the WP05 hoist. Wired into `pr.yml`. |
| G-1 | `check-transport-parity.sh` | *(none — no allowlist by design)* | a transport tag in `dispatch.Pool.closeOneByTag`'s switch whose case body has no real `.CloseOne(ctx, id)` call — comment-only, empty, or dispatching something else — **added 2026-09-12 (connector-lifecycle-truth-01PMZ303 UNIT-15)**. Hand-scoped to the one production switch this mission found broken (pre-UNIT-6, http/sse's cases were comment-only and fell through to the function's shared tail, then a bare `return nil`); the header states explicitly it is not a general arm-parity gate. Tag set is derived from the switch's own `case` lines, not hardcoded. Wired into `pr.yml`. |
| G-2 | `check-recipe-token-substitution.sh` | `g2-recipe-substituted-paths.txt` | a `${...}` token in `registry.json`/`shipped.json` on a JSON path with no declared, still-matching production `Substitute*` call site — **added 2026-09-12 (connector-lifecycle-truth-01PMZ303 UNIT-15)**. Path set is derived from the catalogs each run (a path with no token today needs no manifest entry); a manifest entry whose grep pattern stops matching also fails, so the manifest cannot degrade into an opt-out list. Measured clean today over 7 discovered paths. Wired into `pr.yml`. |

Non-allowlist gates that also protect against unwired code:
`check-output-ports.sh` (output port with no reader),
`check-knob-coverage.sh` (registered config field with no consumer),
`check-seam-implementers.sh` (interface fields of `*Config`/`*Options`/
`*Deps` structs, and of `*Env` structs since the 2026-10-04 v0.86.0
sweep), `check-node-dispatch.sh`,
`check-serve-dispatch-drift.sh`, `scripts/ci/check-codegen.sh`'s
served-stream-topics block (`frontend/src/lib/servedStreamTopics.gen.ts`
generated from `core/serve/wsstream.go`'s `passthroughTopics` — findings
#63/#62, served-topic-single-source, 2026-09: this used to be
`core/serve/wsstream_topics_parity_test.go`, a runtime regex-parse
cross-check between `passthroughTopics` and a hand-maintained
`SERVED_STREAM_TOPICS` array; it and the third hand-copied mirror in
`harnessClient.wp06Overlay.test.ts` are deleted now that
`SERVED_STREAM_TOPICS` is generated code with no independent content to
drift),
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

**2026-09-11 update (finding #48 closure)**: re-derived from scratch
(`comm` between `scripts/ci/check-*.sh` filenames and every real `gate:`
field / `runGate(t, "check-*.sh"` literal / `const gate = "check-*.sh"`
call in `gates_can_fail_test.go`, **plus** `check-no-model-family-literals_test.go`,
a separate file the naive single-file `comm` above misses). By then the
repo had **51** `check-*.sh` scripts, **41** already proved, and exactly
**10** missing: `check-codegen`, `check-fleet-log-export-fence`,
`check-manifest-version-bump`, `check-no-cred-bytes-in-rpc`,
`check-no-fleet-imports`, `check-no-forbidden-compaction-symbols`,
`check-node-dispatch`, `check-oss-first`, `check-output-ports`,
`check-release-integrity`. All 10 now have a proof (7 as new
`TestGates_PlantedViolationFires` table cases; `check-codegen` and
`check-manifest-version-bump` as standalone functions sharing one plant on
`core/agentgraph/nodes/manifests/sleep.yaml`; `check-release-integrity` as
a standalone function that fakes `gh` on PATH rather than hitting the
network). **51 of 51 now have a planted-violation proof.** One gate
(`check-no-fleet-imports.sh`) was found to have a real, live scope hole
while writing its proof — see the dedicated 2026-09-11 entry below; the
proof itself was adjusted to stay honest about what the *shipped* gate can
prove rather than silently landing a fix with unassessed blast radius.

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

### 2026-10-07 (release-infra hardening, chore/release-infra-hardening) · six zero-asset release tags, one mechanism; three fixes landed, four follow-ups open

`scripts/ci/check-release-integrity.sh` flagged six tags whose GitHub
Release has zero assets: **v0.60.0, v0.76.1** (Chocolatey community-feed
outages in release.yml's `install NSIS toolchain (windows-amd64 only)`
step — invalid XML for `nsis.install`, then a 504) and **v0.85.1, v0.85.2,
v0.86.0, v0.87.0** (Apple notarization `403` "agreement expired" on both
darwin legs, 2026-10-04 → 10-05; the team K54JDRHBA7 agreement was
accepted before v0.88.0, 2026-10-05 19:11Z). The shared mechanism: one
failed matrix leg makes `needs.build.result` `failure`, and `publish-s3`
gates on `== 'success'`, so EVERY platform ships nothing. All six are
superseded (v0.60.1; v0.77.0 tagged 13 s after v0.76.1; v0.88.0+) and are
now on `.github/release-integrity-ignore.txt` with reasons.

They were ignore-listed rather than re-run because re-running was unsafe:
`resolve-env` maps every `workflow_dispatch` to prod, and `publish-s3`
unconditionally overwrote the stable `kenaz-harness/manifest.json` pointer
(read by `core/update/manifest.go`, `useUpdateStore.ts`'s `MANIFEST_URL`
and the docs download page) and inserted the run into `index.json` with
`released_at=now` — an old tag would have become "latest".

**CLOSED in this PR:**

- **Pointer could move backwards** — `fix(ci): publish-s3 refuses to move
  the stable pointer backwards`. publish-s3 reads the current canonical
  manifest (NoSuchKey = first publish; any other read error fails
  closed), and when the published version is older keeps the per-tag
  upload but leaves `manifest.json` alone and files the version into
  `index.json` at its commit date. dev stays rolling. Ordering in
  `scripts/ci/lib/semver.sh`, gated by `check-semver-lib.sh` (pr.yml)
  with planted proof `semver-lib/lexical-compare`. The integrity gate's
  "re-run release.yml" remedy is now actually safe, and says so.
- **`manifest.json.released_at` was always `""`** — `fix(ci): manifest
  released_at is a real timestamp`. Both builders read
  `GITHUB_RUN_STARTED_AT`, which GitHub does not provide; now `date -u`.
- **Engine-pin step as a zero-release path on CDN blips** — `fix(ci):
  engine-pin downloads survive transient CDN errors` (`--retry 5
  --retry-all-errors`, ≤3 DMG re-fetches on a short body; verification
  unchanged and still fail-closed). Mitigates (c) below; does not close it.

**OPEN:**

- **(a) A single failed matrix leg zeroes the release for all platforms.**
  Block-all (today) vs publish-the-legs-that-built (partial release, with
  the missing platforms named in the manifest/Release body and the
  integrity gate taught to tell "partial" from "empty"). This is a product
  call — a partial release means some users' updaters see a version they
  cannot install. **Owner:** alec (decision), then release-infra.
- **(b) Apple agreement lapse fails late and anonymously.** The 403 surfaced
  only at notarization, after a full build, as a generic notarytool
  failure. Pre-flight it at the start of the macOS leg (`xcrun notarytool
  history` with the same credentials, or an App Store Connect API call)
  so a lapse fails in seconds with a named cause. **Owner:** release-infra.
- **(c) The engine-pin step is a seventh zero-release path.** It runs only
  on darwin/arm64, and any persistent CDN/index failure there fails the
  leg and so (per (a)) the whole release. Retries above cover transient
  errors; the structural question is (a)'s. **Owner:** same as (a).
- **(d) Index-shape coupling of the pin step.** The step requires the
  pinned version to appear EXACTLY once in `kenaz-ml/index.json` and its
  `darwin_arm64.key_id` to equal the baked key. A kenaz-ml republish that
  duplicates a version row, or a key rotation on the publisher side
  before the harness `.pub` follows, fails every harness release build
  (fail-closed is correct; the coupling is the note). kenaz-ml's publish
  should treat "one row per version" and "key_id changes only with a
  harness pin bump" as contract. **Owner:** release-infra (kenaz-ml side).
- **(e) Pointer-guard TOCTOU residual.** release.yml's `concurrency.group`
  is per-ref, so two tag runs seconds apart (e.g. v0.76.1 / v0.77.0, 13 s
  apart) can both read the current pointer before either writes, and the
  older can write last. The guard narrows the window to one publish-s3
  job's guard→cp gap; it does not close it. **Fix shape:** re-read the
  pointer immediately before the canonical cp, or make the write an S3
  conditional put (`aws s3api put-object --if-match <etag>` from the
  guard's read; `--if-none-match '*'` on first publish). **Owner:**
  release-infra.
- **Note — "Not Found" also matches a nonexistent bucket.** The guard's
  first-publish branch keys on `(404)|NoSuchKey|does not exist|Not Found`,
  so a misconfigured BUCKET reads as "first publish". Harmless: the
  canonical cp that follows fails on the same missing bucket, and the
  per-tag upload before the guard would already have failed. Recorded so
  nobody "fixes" it by widening the match.
- **Cross-reference:** the prerelease updater host bug
  (`core/update/manifest.go:53` `stage-downloads.kameas.ai`, NXDOMAIN; the
  channel publishes to `stage.downloads.kameas.ai`) is being fixed on the
  parallel `fix/ledger-followups-oct7` branch — not duplicated here.
- **Remedy hardening (this PR, review tweak):** the integrity gate's
  remedy now names `gh workflow run release.yml --ref <tag> -f
  version=<tag>`, and derive-version refuses a `workflow_dispatch` whose
  `inputs.version` tag is not `GITHUB_SHA` — a UI dispatch from main would
  otherwise build main's code under the old label and date the held index
  entry ≈now (top of the picker). The per-tag `manifest.json` now uploads
  with the binaries, before the guard, so a guard exit 1 leaves a complete
  per-tag prefix; a pointer that exists but has no string `.version` now
  warns instead of advancing silently.

### 2026-10-07 (v0.93.0 post-release review, memory-sync-01MEMSY01) · `MemorySync` has no `Stop`; nothing on the shutdown path cancels it

`buildMemorySync` (`core/rpc/api.go`, the `ms.Start(context.Background())`
line near the end of the constructor) starts the memory-sync cycle loop
under a context that is never cancelled. `MemorySync.Start`
(`core/fleet/memory_sync.go`) is the only lifecycle method the type has —
it spawns one goroutine with a `time.Ticker` and returns only on
`ctx.Done()`. `API.Shutdown` stops every other scheduler and poller by
name (workflow, compaction, chat-cron, settings/ctx-graph/unit syncers,
audit archiver + sweeper, Cedar decision writer) and delegates the fleet
pollers to `settingsImpl.StopFleetBackground`; neither function mentions
`memorySync`. It is the only background lane in the constructor with no
stop.

**Blast radius:** harmless in production — the interval is 2 minutes,
`RunOnce` short-circuits on `fleet_disabled` / `not_entitled` / disabled
state before doing I/O, and process exit reaps the goroutine. In tests it
leaks one goroutine per `API` instance constructed with a real memory
store (129 `MemorySync.Start.func1` frames in the v0.93.0 CI goroutine
dump of the core/rpc timeout — not the cause of that timeout, but noise
that hides a real hang). After `StopFleetBackground` on sign-out the
loop keeps ticking against a signed-out client and records
`not_entitled` on every cycle.

**Fix shape:** add `(*MemorySync).Stop()` (cancel the Start context, wait
for the goroutine; idempotent + nil-safe like every sibling), hold the
cancel in `buildMemorySync`, and call it from BOTH `API.Shutdown` and the
sign-out path (`StopFleetBackground` or wherever `SetMemorySync` is
undone), with a `goleak`-style or `runtime.NumGoroutine` assertion in the
`buildMemorySync` test so the leak cannot come back silently.
**Owner:** memory-sync follow-up (01MEMSY01 residuals).

**Cosmetic, same lane:** after a `node_removed` sign-out the memory lane
board reads `not_entitled` rather than `signed_out`. `RunOnce` checks
`m.entitled()` (capability poller, which `StopFleetBackground` has just
nil'd → default-deny) BEFORE any fleet call, so the `ErrNotSignedIn`
branch that records `signed_out` is never reached. Correct behaviour,
misleading label; fold into the Stop fix (a stopped lane should record
nothing) rather than reorder the checks.

### 2026-10-07 (v0.93.0 CI) · core/rpc test package is at the 10-minute cliff

core/rpc ran 580.9s under `-race -short` on the self-hosted ARM runner in
the v0.91.0 CI run; v0.93.0's additions (real-sqlite retry + handoff accept
tests) pushed it past Go's default 10m per-package timeout (one just-started
test in the dump — slowness, not a hang). `pr.yml` now passes `-timeout 20m`.
**Fix shape:** split core/rpc's heavy integration tests into a sibling
package (or `t.Parallel()` the independent real-sqlite tests), so the
per-package alarm returns to a tight budget. **Owner:** next release's
unwired/CI-hygiene sweep. Related flake seen in the same run:
`core/mlsidecar` TestDemandProbe_FirstDemandStartsEngine_OneEnsurePerTick
("first demand must not block" — a timing assertion under runner load);
owner: mlsidecar follow-up if it recurs.

### 2026-10-07 (skill-library-01SKLIB01 residuals + review F4–F6, feat/skill-library) · six accepted, none introduced as regressions

1. **OQ-1 — the revocation sweep covers skills + workflows only.**
   Catalog-installed packs and bundles have no consumer install record
   carrying a catalog id (both kinds are still allowlisted in
   `check-install-provider-coverage.sh` — there is no pack/bundle
   provider), so `RevocationSweeper` has nothing to diff for them; a
   revoked pack/bundle the user downloaded stays as `installed/` residue.
   **Blocker:** install-framework Phase 3 (pack/bundle providers).
   **Owner:** the mission that registers those providers adds a
   `RevocationWorkflows`-shaped source for each kind.
2. **`CatalogItem.LifecycleReason` has no producer on the live wire.**
   Fleet main (@ b57b2ec) sends `lifecycle`, `superseded_by` (+
   `revoked_at` on list) on `GET /catalog/list` and the fetch, but
   `lifecycle_reason` only on `GET /catalog/entries/{kind}/{slug}`. The
   harness decodes it forward-compatibly and the chip tooltip shows it
   when present, so today the tooltip never carries the admin's reason —
   not a lie (nothing claims a reason), but a consumer without a
   producer. **Owner:** fleet (add `lifecycle_reason` to
   `CatalogItemMetaAPI` / `CatalogFetchResponse`, additive), or a harness
   follow-up that reads the entries detail for deprecated rows.
3. **A user cannot move their catalog install of a skill from v1 to v2.**
   Each version is its own catalog id, so `LiveRegister` refuses v2 over
   v1 under the same store id (`ErrSkillIDCollision`), and
   `SkillProvider.Update` never offers one (`newestByID` keys on the
   per-version id). Pre-existing since per-version ids; surfaced, not
   introduced, by this mission (the mandated path was fixed in WP04 —
   mandates may take over any catalog-provenance copy). **Owner:** the
   OQ-4 follow-up ("nudge toward `superseded_by`"), which needs
   entry-level grouping of versions anyway.
4. **Review F4 — the mandated-workflow read-only guard fails OPEN on an
   unreadable provenance file.** `isOrgMandated` reads an unreadable
   `install_provenance.json` as "not mandated", so Save / ScheduleSet /
   ScheduleClear are allowed while it is broken. Deliberately matches
   the pre-existing `Delete` posture (the install-collision checks fail
   closed on their own, and the provenance store logs the breakage at
   WARN once). **Owner:** skill-library follow-up — decide fail-closed
   for all four mutators together, not one at a time.
5. **Review F5 — a FAILED v2 promote removes v1 until the retry.** When
   v2's install fails in the same bundle that drops v1, v1 is not "seen"
   and the reconcile removes it; the bundle error retries and v2 lands
   on a later poll. Pre-existing since v0.91 (the applier cannot know a
   failed item's local id when its payload did not decode). **Owner:**
   skill-library follow-up — keep v1 when a failed item shares its
   catalog entry (needs fleet to send the entry slug in the envelope,
   which is a signed-wire change under the §5.4 rule).
6. **Review F6 — the revocation sweep's skill removal is check-then-act.**
   `uninstallLocked` re-reads the skill and then `LiveUnregister`s it; a
   mandate landing between the two could in principle be removed. The
   window is two local file ops inside one sweep and the config poller
   that applies mandates runs the sweep on its own goroutine AFTER apply,
   so the two never interleave today; the workflow side is atomic under
   `installMu`. **Owner:** skill-library follow-up, if the sweep ever
   moves off the config poller's goroutine.

### 2026-10-07 (memory-sync-01MEMSY01 verify pass) · PRE-EXISTING flake: `TestStore_Add_WiresGlobalCaptureTracker`

`core/memory/capture_rate_test.go` fails when the whole `core/memory`
package runs with `-count=5` (ChunksPerMinute delta came out negative). It
fails the same way on the base commit b2b2df75, so memory-sync did not
introduce it. Cause: the test reads `GlobalCaptureTracker()`, a
process-wide sliding 60 s window shared with every other test in the
package. On repeated runs, earlier writes age out of the window between the
test's before and after readings, so the delta it measures can go
negative. It passes at `-count=1` (CI's setting).

- **Class:** test isolation (shared global state), not a production defect.
- **Fix shape:** inject the tracker into the store (or give it a
  per-test reset/clock), so the test measures only its own writes.
- **Owner:** memory follow-up mission (next one to touch
  `core/memory/capture_rate.go`). Dated 2026-10-07.

### 2026-10-06 (conformance verify-pass residuals, feat/fleet-contract-conformance) · four accepted, none introduced as regressions

1. **R1 (P2) — promote-to-team can wedge and duplicate when the user has
   no team_id.** Fleet 400s `team_shared` pushes without a team_id;
   `UnitMapper` attaches a nil `m.teamID` unchecked, and
   `Unit_PromoteAsMergeRequest` creates the local team copy BEFORE the
   push — a failed push leaves a dirty copy that background sync re-pushes
   (400ing the whole team batch) every cycle, and each retry mints another
   copy. **Latent: no frontend caller exists.** Fix shape: refuse before
   creating the copy when team_id is absent; reuse an existing
   `promoted_from` copy; teach the test fake the team_id rule. **Owner:**
   the mission that wires the promote UI — the guard lands BEFORE the
   first caller.
2. **R2 (P3) — workflow mandate-takeover restore hands back the mandated
   content relabelled as a catalog install**, not the user's earlier
   version/edits (skills restore properly). **Owner:** skill-library
   mission (01SKLIB01) WP04 territory.
   > **CLOSED 2026-10-07 (skill-library-01SKLIB01 WP04, `fix(fleet): WP04 —
   > superseded removals stay quiet locally …`):** a mandate that takes
   > over the user's catalog install now returns an opaque snapshot of the
   > user's copy (stored document incl. edits + its install provenance),
   > persisted as `prior_workflow` in `mandated_applied.json`; withdrawal
   > restores that copy under its original catalog id/version. A v0.91
   > record (flag only, no snapshot) keeps the old relabel — the only thing
   > it makes possible. Also fixed in the same change: a mandate may now
   > take over a catalog/mandated row of ANY catalog id (every version has
   > its own id), so a v1→v2 promote updates in place instead of failing
   > as a collision and deleting the workflow. Pinned by
   > `TestInstallMandatedDocument_TakeoverRestoredOnWithdrawal` (real
   > sqlite, file provenance), `TestRemoveMandatedDocument_LegacyRecordRelabels`,
   > `TestInstallMandatedDocument_PromoteUpdatesInPlace`. Review F3
   > (2026-10-07): the restore now runs the Cedar save gate; a refused
   > restore deletes the mandated copy and audits `restore_refused`
   > (`TestRemoveMandatedDocument_RestoreRefusedByPolicyDeletes`).
3. **R3 (P3) — a takeover while mandated_applied.json is unreadable (F5
   path) never persists PriorSkill**, so a later withdrawal deletes
   rather than restores. Needs the F5 corruption AND a takeover in the
   same window. **Owner:** same as R2.
   > **CLOSED 2026-10-07 (skill-library-01SKLIB01 WP04):** while the state
   > file is unreadable the applier still never overwrites it (F5), but
   > records what it applied — takeover priors included — in
   > `fleet/mandated_applied.pending.json`, carries it across further
   > unreadable runs, merges it into the applied set on the first readable
   > run (file rewritten OR deleted), and clears it after a successful
   > save. Removals stay skipped while unreadable (F5 unchanged). Pinned by
   > `TestMandated_TakeoverDuringUnreadableStatePersistsPrior` (real
   > files; fails with the merge removed).
4. **Mandated workflows are delete-protected but not edit/unschedule-
   protected** (accepted "not done"): a weak guarantee for
   compliance-type workflows. **Owner:** skill-library mission, with the
   read-only UI treatment.
   > **CLOSED 2026-10-07 (skill-library-01SKLIB01 WP02, `feat(frontend):
   > WP02 — Deprecated/Revoked chips …`):** the workflows view refuses
   > Save, ScheduleSet and ScheduleClear (as well as Delete) for a workflow
   > whose provenance is `mandated`, with `ErrWorkflowOrgManaged`; a user
   > catalog re-install can no longer relabel the org's copy as the user's
   > (it had been able to, which also silently ended the delete guard);
   > the mandate's own removal disarms the schedule directly. `Summary`
   > carries `orgManaged`; the Library hides Edit / Edit on canvas /
   > Delete and says why, the Schedules tab offers no Schedule/Unschedule,
   > and the Capabilities workflow row is read-only. Pinned by
   > `TestMandatedWorkflow_EditAndScheduleGuarded` (real sqlite store,
   > file provenance) and `WorkflowsView.orgManaged.spec.ts`. The guard is view-level: a
   > 2026-10-07 grep found no `Store.Save` caller outside the workflows
   > view and `core/workflows` itself, so a future writer that bypasses
   > the view must re-check provenance.

### 2026-10-07 (device-keys-handoff-01DEVKH01 residuals) · seven accepted gaps, none a lie in the UI

Per-device handoff keys + v2 wrap-to-all + accept-persists shipped
(WP01–WP06). What the mission deliberately left, each with an owner:

1. **Accepted copies carry import-time timestamps.** `created_at` rides in
   every handoff event, but `session.Manager.ReplayTranscript` (the one
   sanctioned cross-session writer) does not copy `CreatedAt`, so the
   recipient's rows are stamped at accept time. Changing that also changes
   conversation forks. **Owner:** alec — decide with the next
   ReplayTranscript change.
2. **AC-7 fixtures were not recorded from the dev fleet.** They are encoded
   from verbatim mirrors of kenaz-fleet's response structs @ 97a1c12
   (core/fleet/testdata/handoff/PROVENANCE.md); recording needs a live
   Team-tier bearer token the implementing agent must not handle.
   **Owner:** alec — re-record from dev and diff.
3. ~~**v1 "direct" accept arm is transitional.**~~ **CLOSED 2026-10-07**
   (review fix #9): the arm is deleted — the v1 send never produced a real
   item (nil events → 422), so nothing could be accepted through it. The
   coordinator signalled fleet O5 (drop direct-mode acceptance).
4. **Signing key is registered; nothing shows it.** `signing_public_key`
   now goes up at enroll (audit batches become verifiable, fleet §8.3), but
   the Compliance panel does not surface verified/unverified (OQ-10 ruled
   out of scope). **Owner:** next compliance mission.
5. **Self-unenroll runs on explicit sign-out only.** An uninstalled or
   abandoned install keeps its handoff key active and counting toward the
   16-key cap until an admin removes the device in the fleet dashboard.
   **Owner:** alec (uninstall hook) / fleet (dormant-key policy).
6. **Media is not shipped in shares.** Image/document/generated-image
   blocks are counted and noted in the recipient's copy ("1 attachment was
   not included"), not transferred (2 MiB/event cap; generated images are
   sender-local artifacts). **Owner:** product — decide if attachments
   should ride as separate encrypted events.
7. **Externally-owned tokens + node_removed.** Where the host owns the
   tokens (served / brokered mode) `ClearTokens` is a no-op. Since review
   fix #5 (2026-10-07) the block is DURABLE (`<dataDir>/fleet/node_removed`,
   honoured by every enroll path incl. cmd/servedfleet's supervisor, cleared
   only by an explicit sign-in). **Served-mode gap (verify pass,
   2026-10-07):** a served guest has no sign-in of its own and NO code path
   clears the marker when the host re-authorizes — a removed served guest
   stays blocked until someone deletes `<dataDir>/fleet/node_removed` by
   hand. Fix shape: clear the marker when the served supervisor observes a
   NEW identity (different user/node) from the host. **Owner:** served-mode
   boundary owner. Also low: a crash between `sessions.Create` and the first
   `importing` ledger write leaves an empty "Shared by…" session (never a
   second transcript); owner: same mission follow-up.

### 2026-10-06 (newly-live fleet routes verification, pre-v0.91.0) · four latent gaps, all verified non-firing today

Fleet deployed audit/append, identity/public-key, handoff/*, context
streams and team/members (#180-#183); the harness's 404-latches stop
firing. A six-family verification (no crash/corrupt/loop anywhere)
left these latents:

1. ~~**Handoff_Share hardcodes nil events**~~ **CLOSED 2026-10-07**
   (device-keys-handoff-01DEVKH01 WP04, `ca17df45`): `Handoff_Share`
   loads the session through the `contextsync.Impl.SessionEvents` seam
   (wired in api.go to the real session manager) and serializes it as
   self-contained `kenaz.handoff.event` v1 events
   (core/session/handoff_transcript.go); an empty session errors readably
   before any POST. Pinned by `TestHandoffShare_LoadsRealSessionFromUpgradedDB`
   (v0.91.0 snapshot) and `TestImpl_Handoff_Share_*`. Shipped in the same
   branch as key registration (WP02) — the release gate held.
2. **EventStream backfill has no 1000-event/2MiB-per-event client caps
   and sends no client_event_id** (context_sync.go:314-332, 250-268) —
   latent, no live backfill caller (both Toggles pass nil). **Owner:**
   context-streams mission.
3. **Server-seq cursor discipline**: when SessionSync_ResumeFrom gains
   a real applier, persist fleet's next_seq — never the local message
   count (the hook posts wire seq=1 per event; server assigns arrival
   order). **Owner:** context-streams mission.
4. ~~Cosmetic: handoff 409/422 raw "status NNN"~~ **CLOSED 2026-10-07**
   (device-keys-handoff-01DEVKH01 WP04 `ca17df45` + WP06): every §10.3
   code maps to a `*fleet.HandoffError` whose text is dialog copy
   (`TestShareSession_ErrorCopy` asserts no "status" leaks); 409
   recipient_keys_stale re-wraps and retries once; the share dialog and
   inbox render the copy via `handoffErrorText`.

### 2026-10-06 (empty-turn fix review residuals, fix/session-write-empty-turn) · two pre-existing flags, neither introduced by d845ecf0

1. **Image-only turns drop generated images silently.**
   `GeneratedImageCapturer.DrainPendingImages` rides the session_write
   `HookPostLLM` (`chat_runner.go:1598-1612`). A final fire with
   generated images but EMPTY text now completes via the soft skip and
   never drains — images lost quietly (previously the same turn
   hard-errored, losing them loudly). Only bites providers that emit
   image-only turns. **Fix shape:** move the drain off the session_write
   hook, or drain on the skip path too. **Owner:** next chat-runner
   mission; revisit if any wired provider starts emitting image-only
   finals.
2. **Whitespace-only assistant rows render as visible empty bubbles.**
   `session_write` skips only exact `""`; a `"\n"` turn still writes a
   row, and `MessageBubble.vue` (311, 655-662) renders it under
   `whitespace-pre-wrap` with no blank guard. Pre-existing. **Fix
   shape:** TrimSpace-based skip at session_write (consistent with
   `AppendEntry`'s TrimSpace absorb compare) or a blank guard in the
   bubble. **Owner:** same follow-up as above.

### 2026-10-05 (engine-ports review residuals, fix/engine-ports) · two accepted lane-scan edges

1. A slow OUR engine with NO engine.port record (failed write or garbage
   file) still double-spawns: the scan reads its busy port as
   unknown-occupied, steps past, and spawns on the first free candidate —
   two engines, one lease dir. Accepted: the alternative (block spawning
   while ANY candidate is busy) lets a hung foreign listener deny the
   engine entirely. Trigger requires a missing record while ours runs;
   the record is rewritten on every adopt. **Owner:** revisit with the
   kenaz protocol-gap follow-up (kenaz #178 "Known gap").
2. Uninstall no longer sends the shutdown token to an engine that fails
   verification (tampered/refuse-unverified) before removing the root —
   correct for the token (never send to the unverified), but a behaviour
   change: such an engine keeps running until it idle-exits. Same owner.

### 2026-10-07 (undelivered-message-retry review, informational) · Retry on a scheduled session's undelivered prompt runs it attended, without the schedule's tool allowlist

**Owner**: toolset/containment follow-up (alec) — decided by the B-3
attended-semantics ruling. **Ungated.**

Retry re-dispatches the newest user row through the ordinary interactive
`LLM_StartStream` path. In a session a schedule created, that row can be a
scheduled (possibly model-written — `scheduled_chat_runs.created_by`)
prompt whose original run was unattended and constrained by the
schedule's `tool_allowlist`. Retrying it from the chat surface re-runs
that prompt interactively, with the session's normal tool catalog and the
attended confirm ladder rather than the schedule's allowlist. This is the
same posture as the user typing into that session — the human is present
and every confirm/containment gate applies — so it is not a bypass, but
it changes which tools the prompt can reach. Blocker: B-3 has not ruled
whether attended re-runs of scheduled prompts inherit the schedule's
allowlist. Owner change that closes this: the B-3 ruling, then either
carry the allowlist onto the retried run or document "attended = session
catalog".

### 2026-10-07 (undelivered-message-retry review, pre-existing) · `provider:auth-resumed` is not filtered by session

**Owner**: alec (chat surface). **Ungated.**

`useSession.ts`'s `provider:auth-resumed` handler adopts
`payload.new_sub_id` as the active stream for WHICHEVER session view is
mounted — it never compares `payload.session_id` (which
`AuthResumedPayload` carries) to the current session. With two sessions
on one profile, rotating a key in one can point the other view's stream
guard at the wrong run and clear its banner. Pre-existing (not introduced
by undelivered-message-retry); found in its review. Fix: early-return when
`payload.session_id` is set and differs from `id.value`, with a test.

### 2026-10-05 (pull-idempotency audit, fleet-session-truth research) · session-sync push ships seq=1 on every event; the two pull surfaces are count-only stubs

### 2026-10-05 (pull-idempotency audit, fleet-session-truth research) · session-sync push ships seq=1 on every event (item 1 downgraded 2026-10-05: no fleet event-stream exists); the two pull surfaces are count-only stubs

Three linked findings from kitty-specs/fleet-session-truth-01DOGF0A/
research/pull-idempotency-audit-2026-10-05.md:
1. **seq=1 — DOWNGRADED 2026-10-05 (fleet brief answered, verified
   against kenaz-fleet main):** fleet registers NO event-stream endpoints
   — `/api/v1/context/append` and `/api/v1/context/replay` do not exist
   and answer the Go mux's plain 404. No remote streams exist, so nothing
   was ever collapsed to "ONE event total"; the seq=1 construction
   (SessionSyncer.AppendEvent builds a fresh EventStream per call) is
   latent, not live data loss. The append 404s were never a provisioning
   gap either — the parent mission's "remote context missing" reading
   (dogfood F7) was a misdiagnosis of a missing route. Since
   `fix/fleet-contract-truth` WP03 a plain 404 latches the stream
   UNSUPPORTED for the process (`core/fleet/unsupported_endpoint.go`):
   logged once, never retried, lane `context_sync` goes Off with reason
   `fleet_endpoint_unsupported`, events stay local, and `EnableSync`
   refuses once it is known. A fleet session-event log is a NEW fleet
   feature awaiting an owner spec (server-assigned seq — which also
   settles the seq question). **Owner:** fleet owner (spec); the harness
   side re-opens only when that endpoint ships.
2. **SessionSync_ResumeFrom** and **Handoff_Accept** decrypt and COUNT
   records, persisting nothing (views/contextsync/impl.go:86-89, :213-228,
   both commented as future-WP). Bindings exist; frontend never calls the
   first and never calls accept. Dated-justified: the pull writer must not
   exist until it can be idempotent per the audit's fix shape — do not
   wire a naive AppendMessage loop. **Owner:** the future session-pull
   mission; the audit file is its contract.
3. **Handoff and audit-append client stubs call endpoints that do not
   exist server-side (2026-10-05, verified against kenaz-fleet main).**
   `HandoffHandler.ShareSession` / `Inbox` / `AcceptShare`
   (`core/fleet/team_handoff.go`, `/api/v1/handoff/{send,inbox,{id}}`)
   and `AuditArchiver.post` (`core/fleet/audit_archive.go`,
   `/api/v1/audit/append`) target routes fleet does not register. No
   provisioning fixes it. Since WP03 (cross-ref item 1) a plain 404
   latches each feature unsupported — handoff calls short-circuit with
   `ErrEndpointUnsupported`, the archiver's loop exits (Compliance's
   `archiverRunning` goes false) — and the local audit log is unaffected.
   Still open: these surfaces (Handoff_* bindings, the Compliance
   archive panel) remain offered to the user although the server cannot
   serve them; a UI "not supported by your fleet" state is unbuilt.
   **Owner:** fleet owner for the endpoints; harness copy follows them.

### 2026-10-05 (fix/fleet-contract-truth review R3) · bootstrap node ids are not user-scoped — org-wide collisions become per-item rejections under fleet PR #173

> **CLOSED 2026-10-06 (fleet wire-contract WP04):** the bootstrap fleet push
> leg was removed — it pushed classification "personal", which fleet always
> refuses, and bootstrap has no share-consent surface. `bootstrapNodeID` is
> deleted; extracted context stays local (RunStatus.SharingSkipped =
> `personal_requires_share_consent`) and is shared per entry through
> Knowledge › Curated, whose ids are per-install UUIDv5s (core/fleet/wire_id.go).
> The text below is historical.

`bootstrapNodeID` (`core/rpc/contextbootstrap_wiring.go`) is
`"ctxb-" + connector + "-" + sourceRef` — deterministic per source item but
NOT scoped to the user. Two users in one org who bootstrap the same shared
item (a team channel message, a shared ticket) push the same node id. On the
current server that is last-writer-wins over someone else's personal node;
once kenaz-fleet PR #173 lands, the second user's push comes back in
`rejected[]` with `not_permitted`. Since R3 that rejection is recorded
honestly (`fleet.context_push_rejected` audit, no publish record, no
onboarding `context_synced`), so nothing lies — but that user's node never
reaches fleet. **Not redesigned here** (changing ids orphans every node
already pushed). **Owner:** contextbootstrap follow-up — scope the id by
user (or let fleet assign it) with a migration for existing `ctxb-` ids.

### 2026-10-05 · contexts per-node publication-state read (blocker for FR-7 pre-run state display) — owner: follow-up WP on knowledge-home (needs a Contexts_NodeStatus binding returning per-node layer/version)

knowledge-home-01DOGF0E FR-7 asks the folder share/promote dialog to show,
per entry and before the run, whether it is already published and at which
layer. Nothing can answer that today: `Context_SyncStatus` is an aggregate
(cursor, pull count, errors) and `Context_ContextSearch` is a title/body
search — neither is keyed by node id (`core/rpc/views/contexts/api.go`). The
dialog (`frontend/src/views/contexts/FolderShareDialog.vue`) therefore
reports each entry's actual landing layer (`effective_layer`) only after the
run, and says nothing about prior state rather than guessing. Blocker: a
`Contexts_NodeStatus` binding returning per-node layer/version. Owner: a
follow-up WP on knowledge-home. Not a lie today — no control claims the
state — so this is a dated gap, not an inert dial.

### 2026-10-05 (v0.87.0 adversarial review F2) · the repair path's re-application window ends at 0341

Re-running sessions/0332 on a database units/1104 has converted fails
("no such table: artifacts") — proven by the release probe. UNREACHABLE
in production: Pending applies in version order so 1104 only ever runs
after 0332, and Registry.Rollback has no production caller; the repair
path re-applies 0341+ (hardened for the converted store) and never
0332. Recorded so nobody "fixes" 0332 by modifying a shipped migration:
IF a future repair feature widens the window below 0341, it must add a
table-exists guard IN THE REPAIR PATH, not in 0332. **Owner:** whoever
builds a wider repair. Also noted (F4): the 0341 composition tests pin
v0.85.2/v0.63.0 while 1104's pin the newest snapshot — re-point 0341's
at the newest tag when next touched. **F4 CLOSED 2026-10-05 by
units-debt-01UNITD01 WP03:** the three 0341 tests now select
`newestSnapshotBefore0341` (`core/storage/sqlite/migration_0341_test.go`)
— the newest committed snapshot whose ledger predates 0341 (v0.86.0
today), not a hard-coded tag; "newest overall" would silently stop
re-running 0341 once a post-0341 snapshot lands. The repair-window
finding above (F2) stays open; units/1105 tolerates it — re-applying 1105
after a ledger rewind is a no-op once the tables are gone
(`TestMigration1105_FreshInstallAndReopenAfterRewind`), and
`TestMigration0341_ReapplyAfterUnitsConversionDoesNotBrick` now runs 0341's
re-application against a database with NO artifacts table of either
generation.

### 2026-10-05 (install-framework-01DOGF0B review L2) · install consent is UI-enforced only; fleet workflows install unverified (C-2)

**Class:** a control that reads as enforced but is enforced only in one
caller (consent); a verification step that reports, not refuses (C-2).

**(a) Consent (pre-existing, owner: Phase 3 / install-framework-01DOGF0B).**
`install.RequirementConsent` (an MCP recipe's `Warning`) is declared by the
MCP provider and routes the "Add capability" row to the key-prompt modal,
whose acknowledgement checkbox is the only thing that enforces it.
`install.Framework` deliberately enforces only input-bearing requirements
(key / config / directory); a direct `Tools_InstallRecipe` or
`Capability_Install` call installs a warning-bearing recipe without any
acknowledgement — exactly as `Tools_InstallRecipe` did before the
framework. Blocker: an acknowledgement token on the install request (a
binding-signature change) belongs with Phase 3's surface consolidation.

**(b) Fleet workflows join skills' unverified posture (owner: register
C-2 / the FR-2 fleet payload brief).** Since WP05, org-catalog workflow
payloads install through `WorkflowsAPI.InstallDocument`, verified by the
same single `installSignatureVerifier` as skills. **C-2's real design
(kenaz-fleet owner, 2026-10-05):** there is no per-org or per-device
catalog key and never will be. Org-MANDATED items ship inside the
ed25519-signed config bundle and verify against the build-time-pinned
fleet key — `core/fleet/config_pull.go` `VerifyWithKeySet` (hard-reject
before apply) → `compositeConfigApplier.ApplyBundle`
(`core/rpc/views/settings/fleet.go`) → `fleet.MandatedApplier.Apply`
(`core/fleet/mandated.go`, which replaced `ApplyMandatedSkills` with the
`mandated_items` envelope on 2026-10-06); that is the ONLY mandated-item
write path. Non-mandated
catalog installs carry no fleet signature today; a possible future design
signs item payloads with the bundle key (fleet-owner decision pending).
So the verifier reports `verified=false` with the C-2 reason (since
`fix/fleet-contract-truth`: "fleet signs only the org config bundle;
catalog installs carry no fleet signature") and the install proceeds — recorded on the `capability:installed`
event and stated in the workflow detail pane, not refused. A workflow can
carry shell steps and a cron schedule, so this posture is a larger trust
surface than a text skill; the collision refusal (review H1/H2 — a payload
can never overwrite a template, a user workflow or another item's
workflow) bounds it to new ids. Clears only if the fleet owner decides to
sign catalog item payloads with the bundle key and that verdict lands in
the verifier (via `WithPubKey`); otherwise it is the permanent posture for
non-mandated items and the UI's "not signature-verified" notice stands.

### 2026-10-05 (install-framework-01DOGF0B re-review low 3) · templates installed before install provenance are never offered an update

**Class:** a dial with no producer for a subset of rows.
`installed_outdated` is computed from the install provenance record
(`core/workflows/provenance.go`, the shipped `v<N>` recorded at install).
Templates installed by v0.87.0 and earlier (Workflows › Catalog) have no
record, so they read "installed" forever and are never offered an update —
the conservative choice: without a recorded version, "outdated" would be a
guess, and an update overwrites user edits. **Disposition: acceptance note,
not a backfill WP** — the workaround is to Remove the template in
Capabilities (Workflows) and Install it again, which records provenance; a
backfill would have to guess an installed version from YAML the user may
have edited. Owner: alec / install-framework-01DOGF0B Phase 3 (release
notes carry the workaround). *Annotated 2026-10-05:* the Marketplace
fold-in this entry used to wait on happened in Phase 4, pulled forward by
owner ruling ahead of Phase 3; it did not touch provenance, so the entry
stays with Phase 3.

### 2026-10-05 (install-framework-01DOGF0B review L3) · `slashcmd.Registry` has no mutex

**Class:** latent data race on a live map. `Registry.commands` is read by
dispatch (`Lookup`/`List`) and mutated by `LiveRegister` /
`LiveUnregister` / `RenameLocalTrigger` from RPC goroutines and the fleet
mandated-skill applier, with no lock. Not changed in this review (a
drive-by lock on a hot dispatch path wants its own race test).
Owner: Phase 3 (install-framework-01DOGF0B). Blocker: a `-race` test
driving concurrent install + dispatch, written with the lock.

### 2026-10-05 (install-framework-01DOGF0B Phase 4) · deleting MarketplaceView left `Catalog_Install` and `Slashcmd_SkillInstall` with zero frontend callers

**Class:** Wails binding ↔ `harnessClient.ts` ↔ `.vue` caller (pass 2),
created by this change, not found by a sweep. MarketplaceView was the only
`.vue` caller of `client.catalog.install` (→ `Catalog_Install`) and
`client.slashcmd.skillInstall` (→ `Slashcmd_SkillInstall`). Its fold-in
calls neither: a catalog skill installs through `Capability_Install`, and
bundle / agent_pack Install is disabled-with-reason (a click shows the
reason and calls nothing). Both bindings now sit in the i15 allowlist's
`unrouted` class as "no caller anywhere".

**Disposition: dated-justified, not deleted (2026-10-05).**
- `Catalog_Install` / `catalog.install` — the backend refusal
  (`ErrCatalogKindNotInstallable`) is WP02's defence in depth behind the
  disabled button, and `CapabilitySurface.catalog.test.ts` M4/M4a use the
  client method as the spy proving the UI never calls it. Blocker: the
  Phase 3 bundle / agent_pack providers, which replace this path; the
  commit that registers the last of them deletes the binding, the client
  method and the refusal together (see the badge-only entry below). Owner:
  alec / install-framework-01DOGF0B Phase 3.
- `Slashcmd_SkillInstall` / `slashcmd.skillInstall` — framework-routed
  (decision record §2.1), so it cannot install around the pipeline; it is
  simply unused by the UI now. Removing a bound method is a codegen change
  (`frontend/wailsjs`), out of scope for the frontend-only Phase 4. Blocker:
  a decision whether per-kind install bindings outlive their last UI
  caller (Capability_Install covers every zero-input kind). Owner: alec /
  install-framework-01DOGF0B Phase 3 (which retires the other per-kind
  browse surfaces and owns the same question for `Bundle_Install`).

### 2026-10-04 (install-framework-01DOGF0B WP01/WP02) · Marketplace "Install" for workflow / agent_pack / bundle was badge-only — nothing consumes `installed/`; the skill badge lied the other way

**Class:** registered/advertised capability with no consumer.

**Finding.** `fleet.Client.Install` (`core/fleet/catalog_install.go`)
fetched a catalog item, (skipped) signature verification, wrote
`<DataDir>/installed/<kind>/<id>@<version>/payload` + `meta.json`, and
returned success — no registration, no store save, no event. The only
reader of `installed/` was the badge set inside `Catalog_List`
(`core/rpc/views/catalog/impl.go`); `Catalog_Installed` has zero `.vue`
callers. So Marketplace Install for **workflow**, **agent_pack** and
**bundle** reported success and painted "Installed" while delivering no
capability. Opposite-direction lie on the same code: **skill** installs
bypass `installed/` entirely (`slashcmd.SkillInstall` →
`fleet.InstallSkill` → `slashcmd.LiveRegister` → `SkillStore`), so a
successfully installed skill still showed "Install".

**Verification pass (dogfood 2026-10-04, CONFIRMED, re-read against
`main` @ `b8079d48`;** `kitty-specs/dogfood-2026-10-04/notes.md`
§"Marketplace installed/ verification"). Per kind, the runtime consumer and
why `installed/` never reaches it:
- workflow — workflows load from sqlite + builtins via a *different,
  same-named* catalog (`Workflows_CatalogInstall` → wfcatalog over
  `LoadBuiltins` → `Store.Save` + cron arm). Never reads `installed/`.
- agent_pack — agent profiles load only from `<dataDir>/agents`
  (`core/agents/loader.go`). Never reads `installed/`.
- bundle — the bundle list reads `kenaz.lock`; `Bundle.Install` needs a
  directory with a `kenaz.yaml` manifest, which `Catalog_Install` never
  wrote (it wrote an opaque `payload` file).
- skill — bypasses `installed/`; consumed by the slash registry.

**Disposition (this PR, Phase 0).**
- WP01 (`fix(marketplace): WP01`): `Catalog_List` reads kind=skill
  installed state from the `SkillStore` (`WithSkillStore`, wired in
  `core/rpc/api.go`), not `installed/`. `fleet.ResolveSkillStoreID` maps
  the catalog_id the Marketplace sends to the store's skill ID (they differ
  for every `SkillPublish`'d skill), so the now-visible Uninstall works.
  Pins: `TestCatalogList_SkillInstalledStateFromSkillStore` (P-1),
  `TestCatalogList_SkillResidueInInstalledDirIsNotInstalled`,
  `TestUninstallSkill_ByCatalogID` — each fails with the behaviour reverted.
- WP02 (`fix(marketplace): WP02`): `Client.Install` refuses **every** kind
  with `fleet.ErrCatalogKindNotInstallable`, the message naming the kind
  and the working alternative, and writes nothing — the payload-writing
  code is deleted (skill is refused too: on this path it was equally
  unconsumed; skills install via `SkillInstall`). `MarketplaceView.vue`
  shows workflow / agent_pack / bundle Install **disabled, not hidden**,
  with the reason as visible text (`item-install-unsupported-<slug>`,
  linked by `aria-describedby`). Existing residue is labelled
  "Downloaded — not active" (never "Installed") and keeps a
  "Remove download" action — `Client.Uninstall` and `InstalledItems` stay
  as the cleanup path. Pins: `TestCatalog_Install_RefusesEveryKind`,
  `TestCatalogInstall_RefusesUnconsumedKinds`, `MarketplaceView.spec.ts`
  4 (P-2), 2 and 5 — each fails against the pre-fix code. *(Phase 4,
  2026-10-05: MarketplaceView was deleted; these pins moved, not died, to
  `views/capabilities/__tests__/CapabilitySurface.catalog.test.ts` M4, M2
  and M5 — workflow left the disabled set when its provider shipped in
  WP05.)*

**Still standing (dated-justified, 2026-10-04).** `Client.Install`'s
`dataDir` and `pubKeyBase64` parameters are unread, kept so the per-kind
providers and any future C-2 bundle-key payload signature (no per-device
key will exist — fleet owner, 2026-10-05) plug into the existing call chain
(catalog/impl.go's `pubKeyBase64` / `WithPubKey`, already justified under
register C-2). `Catalog_Installed` keeps zero `.vue` callers (pre-existing
NARROW, `harnessClient.ts`); WP08 is its intended reader. Blocker: the
install framework (install-framework-01DOGF0B Phases 1–3). Owner: alec /
install-framework-01DOGF0B.

Two residue gaps the Phase-0 cleanup path does not reach (dated
2026-10-04, review F5; owner: install-framework-01DOGF0B **WP08**, which
deletes both lines when its offer-to-finish-or-remove flow reads
`installed/` directly):
- **Withdrawn-item residue is unreachable.** "Remove download" lives on
  the item's Marketplace card (since Phase 4: its catalog row in the
  Capabilities surface — still built from `Catalog_List`, so the gap
  stands), which comes from `Catalog_List` (the live fleet listing). Once a publisher withdraws the item, the card is gone
  and its `installed/` payload has no removal surface — the one RPC that
  would list it, `Catalog_Installed`, has no `.vue` caller.
- **`installed/skill/` residue has no removal surface.** WP01 made the
  skill store the only authority for a skill's installed state, so a
  skill payload an earlier release's `Catalog_Install` wrote under
  `installed/skill/` never surfaces as a card state at all; nothing
  offers to remove it (it is inert — the slash registry never reads it).

Review follow-ups landed with this entry
(`fix(marketplace): review follow-ups`): `rpc.New`'s `WithSkillStore`
wiring is pinned at chassis level
(`TestChassis_CatalogList_SkillInstalledStateIsWiredToSkillStore`);
`fleet.ResolveSkillStoreID` matches CatalogID (+version) before the exact
store ID so a colliding catalog_id cannot cross-delete another skill;
`Client.Uninstall` — now the promoted "Remove download" path — refuses
any kind/catalogID/version that is not a single clean path segment
(`ErrCatalogInvalidPathSegment`; before, a version of
`/../../../../victim` removed a directory outside `installed/`) and an
empty data dir.

**What deletes this entry.** Each kind gaining a *consumed* install
through the provider framework, its installed state read from the consumer:
workflow (WP05 — wfcatalog `Store.Save`, listed by `Workflows_List`),
bundle (WP06 — `Bundle.Install` from a `kenaz.yaml` directory, listed by
`Bundle_List`), agent_pack (WP07 — written to `<dataDir>/agents` + loader
reload, **or** the kind dropped from the catalog with the reason recorded
here), plus WP08 offering to finish or remove existing `installed/`
residue. When the last kind lands, `ErrCatalogKindNotInstallable`,
`CATALOG_ONLY_KINDS` in `frontend/src/views/capabilities/catalogBrowse.ts`
(the Marketplace's `INSTALL_UNSUPPORTED_REASON` map, moved there when the
Marketplace folded into Capabilities in Phase 4), and this entry are
deleted together.

**Gate question — could a gate see "install writes a directory nothing
reads"?** Not with the existing gates, and not cheaply as a new one. Every
gate in the inventory pairs *symbols* (registration ↔ predicate, exported
func ↔ call site, package ↔ importer); this class is a filesystem *path*
written by one function and read by none, reached through
`filepath.Join` fragments (`"installed"`, a runtime `kind`) that no
grep can pair with a reader reliably — a path-literal write/read scan
would both miss computed paths and flag every write whose reader lives in
another process (fleet, sidecars). No gate is extended here. The class
becomes symbol-gateable at **WP03**: once the provider contract exists,
"every `CatalogItemKind` has a registered provider whose `InstalledState`
is computed from its consumer" is a registration↔consumer pair the
pass-2 tripwire pattern can enforce. WP03 owns adding that gate, with a
planted-violation proof in `scripts/ci/gates_can_fail_test.go`.
### 2026-10-04 (agentgraph-settings-linkage-01DOGF0D) · materializing an older chat run falls back to the library graph, verified by digest — the exact resolved spec is not stored — CLOSED 2026-10-05


**Gate added (WP03, 2026-10-05).** `scripts/ci/check-install-provider-coverage.sh`
(wired into `pr.yml`) pairs, in both directions: every `core/fleet`
`CatalogItemKind` value ↔ an `install.Kind` (`core/install/provider.go`);
every `install.Kind` ↔ a production `Register(install.Kind<Name>, …)` under
`core/rpc/` **plus** a `TestInstallProvider_<Name>_ConsumerSeesInstall`
consumer test — or a dated line in
`scripts/ci/allowlists/install-provider-coverage.txt` naming the blocker and
owner; and every allowlist line ↔ a real, still-unregistered kind (the
allowlist shrinks as providers land). "InstalledState is computed from its
consumer" is not grep-checkable, so it is enforced at runtime instead:
`install.Framework.Install` re-reads `Provider.InstalledState` after every
install and fails with `install.ErrNotConsumed` when the consumer does not
list the capability (`TestInstall_BadgeOnly_RefusedWithErrNotConsumed`,
`core/install/framework_test.go`). Planted proofs:
`install-provider-coverage/{unregistered-kind,registered-without-consumer-test,catalog-kind-without-install-kind}`.

**Progress (WP04/WP05, 2026-10-05).** `mcp_recipe`, `skill` and `workflow`
have registered providers with consumer tests and left the coverage
allowlist: MCP recipes adapt the existing recipe install (consumer: the
supervisor's persisted enabled list); skills fetch in `Verify`, are checked
by the single `SignatureVerifier`, and `LiveRegister` the same bytes
(consumer: skill store + slash registry); workflows install shipped
templates through wfcatalog `Store.Save` + cron and fleet workflow payloads
through `WorkflowsAPI.InstallDocument` (consumer: `Workflows_List`). Every
per-kind install binding (`Tools_InstallRecipe`, `Tools_UninstallRecipe`,
`Slashcmd_SkillInstall`, `Slashcmd_SkillUninstall`,
`Workflows_CatalogInstall`) routes through the framework. Still standing:
`bundle` (WP06) and `agent_pack` (WP07) stay allowlisted, and
`Catalog_Install` + the Marketplace keep refusing workflow / agent_pack /
bundle — the Marketplace is folded into the surface in Phase 3, and until
then its workflow refusal copy still names Workflows › Catalog, which WP05
retired (`?tab=catalog` now redirects to `/tools?kind=workflow`, so the
copy's pointer still lands; the wording is Phase 3's to change — the WP02
copy is frozen for this phase). Owner: alec / install-framework-01DOGF0B.

**Progress (Phase 4 rail consolidation, executed EARLY — 2026-10-05).** By
owner ruling (2026-10-05, the third time the owner asked), the spec's
Phase 4 ran before Phase 3: ONE rail entry, **Capabilities** (route
`/tools`, page action "Add capability"), replaces Tools + Marketplace —
decision record §3, except that the route stays `/tools` (match-prefix
unchanged) and `/marketplace` redirects there in both route tables;
`/bundles` is untouched because the Bundles fold-in is Phase 3.
`MarketplaceView.vue` is deleted; what only it showed now lives in the
Capabilities surface (`views/capabilities/catalogBrowse.ts`,
`CatalogListingDetail.vue`): bundle / agent_pack catalog rows with Install
**disabled-with-reason exactly as WP02 left them** (bundles → Settings ›
Integrations › Bundles; agent packs → not installable yet), `installed/`
residue as "Downloaded — not active" + Remove download, the listing facts
and Withdraw. The workflow refusal copy above is fixed (Go now says
"Capabilities (Workflows)"; the UI never shows a disabled workflow row —
workflows install through their provider). **Still standing, with their
owners: Phase 3** — the `bundle` (WP06) and `agent_pack` (WP07) providers
and their coverage-allowlist lines, the Bundles browse fold-in (and the
`/bundles` redirect the decision record names), the tasks.md WP08
offer-to-finish-or-remove flow for `installed/` (FR-7) and the two residue
gaps above. Owner: alec / install-framework-01DOGF0B Phase 3; agent_pack's
payload format blocker: fleet team.
### 2026-10-04 (agentgraph-settings-linkage-01DOGF0D) · materializing an older chat run falls back to the library graph, verified by digest — the exact resolved spec is not stored

Every chat turn now links to its run graph (WP04), so materialization
quality on *old* runs is user-visible. The resolved spec a run executed
lives only in `Manager.TrackExternalRun`'s process-local map, capped at
`maxTrackedExternalRuns = 64` (`core/rpc/views/agentgraph/manager.go`).
After a restart, or 64 turns later, `runSpecFor` answers from tier 3:
the library graph named by the run's own `run_start`. That is a
projection of the run's events onto *today's* file, which may not be
the spec that ran.

**Disposition: dated justification, not wired.** Persisting the exact
resolved spec per run was explicitly out of scope (spec §5). Instead
WP03 records a SHA-256 `agentgraph.SpecDigest` of the resolved spec
(layout and provenance excluded) on `run_start` and in
`session_turn_runs.spec_digest`. At tier 3 the manager compares it with
the library file's digest: **equal** ⇒ the file *is* the spec that ran,
exact provenance, no banner; **different or absent** (file edited since,
a resolved spec that never matched a file, or a pre-WP03 run) ⇒
`SpecProvenanceLibraryFallback`, and the editor says so in plain words
("Reconstructed from the library graph — the exact spec for this turn
is no longer in memory…"). Pin:
`TestMaterializeRun_LibraryFallbackVerifiedBySpecDigest` (fresh manager
on a SQL log = after a restart). So nothing is presented as faithful
that is not — the gap is fidelity, not honesty: a digest-mismatched run
still renders against the wrong topology, under a banner.

**Blocker:** a per-run spec store (a `run_specs(run_id, spec_json)` side
table or an event carrying the resolved spec) with a retention policy —
specs are larger than any row the event log holds today. **Owner:** the
01DOGF0D follow-up that persists exact run state (spec §5; the same
owner as the redrive-window entry below). Deleted when tier 3 is
reachable only for runs that predate that store, and a test materializes
a 65th-turn run with exact provenance after an edit to its library file.

**Closed 2026-10-05** by branch `feat/graph-resolved-spec` (target
v0.88.0), both halves of the deletion condition met:

- *The store.* Migration `sessions/0343-agent-graph-run-specs`
  (`core/session/migrations_agent_graph_run_specs.go`):
  `agent_graph_run_specs(run_id PK, graph_id, spec_digest, spec_json,
  created_at_ns)`. Written by the kernel itself (`Kernel.Run` →
  `recordRunSpec`, `core/agentgraph/run_spec_store.go`) once per run,
  before `run_start`, from `env.Graph` — the spec it executes, post alias
  / routing gate / dial. Insert-once (Resume and the overflow redrive
  keep the first row); bounded per row by `MaxRunSpecBytes` (1 MiB;
  measured 2026-10-05: `chat_default` encodes to ~5.0 KiB of JSON (5028
  bytes; ~2.7 KiB resolved with the routing gate off), `toolloop_default`
  ~4.2 KiB (4259 bytes)); digest re-checked on read. An oversized spec is
  not stored and renders as a labelled reconstruction (pinned by
  `TestKernel_OversizedSpecRunCompletesAndRendersAsReconstruction`).
- *Tier 3 reachable only for runs that predate it.* `runSpecFor` reads
  the persisted spec first and holds it to `run_start`'s `spec_digest`;
  a run with a stored spec never reaches the library reconstruction.
  The digest-verified upgrade described above is **deleted** — what
  remains (`reconstructUnrecordedRunSpec`) always stamps
  `library_fallback`, and the banners now say the run predates per-run
  spec recording. Its only reader,
  `TestMaterializeRun_LibraryFallbackVerifiedBySpecDigest`, is deleted.
- *The test.* `TestMaterializeRun_65thTurnAfterLibraryEdit_IsExact`
  (`core/rpc/views/agentgraph/materialize_persisted_spec_test.go`) is
  the condition verbatim; `TestMaterializeRun_EditedGraphStillShowsWhatRan`
  does the same across a restart, and
  `TestMaterializeRun_PreSnapshotRunOnUpgradedInstall_IsLabelledReconstruction`
  boots the v0.86.0 snapshot to prove pre-store runs still render,
  labelled.

Not done here, and not a lie: the blocker named a retention policy.
`agent_graph_run_specs` has the same lifecycle as `agent_graph_events`,
which has none either; the per-row bound caps the growth rate (one row
per run). A retention sweep belongs with event-log retention, whenever
that is specced — not a dated item here because nothing claims it exists.

### 2026-10-04 (agentgraph-settings-linkage-01DOGF0D) · a redriven run's status reads "failed" for the seconds before its redrive starts

Between a chat run's failed attempt and the overflow redrive's
`run_start` (the window is the pre-redrive compaction, typically
seconds), `statusFromLog` honestly derives "failed" from the first
attempt's events — an open RunView stops polling and shows failed for
a run that is about to continue. Data is correct and nothing is lost;
reopening the view shows the redrive. **Blocker:** a durable
"redrive pending" record the status derivation can consult.
**Owner:** the follow-up to 01DOGF0D that persists exact run state
(spec §5 follow-up). Deleted when that record exists and the status
test asserts "running" across the whole redrive window.
*2026-10-05:* `feat/graph-resolved-spec` — the spec-persistence half of
that follow-up — persists the resolved **spec**, not run **state**, so
this entry stays open; its owner is now the redrive-state half alone (a
durable redrive-pending record), not yet specced.
### 2026-10-04 (artifacts-as-units-01DOGF0C review F10) · artifact purge after a session/project delete has no retry — purge-retry

`purgeArtifactsAfterDelete` (`core/rpc/api.go`) runs after the session or
project row is already gone. If it fails, the error is logged
(`rpc.artifacts.purge_failed`) and the delete still reports success.
Returning the error would show "delete failed" for an item that no longer
exists and cannot be deleted again. The residue is artifact units whose
`scope_id` / `metadata.session_id` / `metadata.project_id` name a deleted
row, and the media they pin. These are still visible and deletable one by
one in the Library's Captured view. No sweep re-runs the purge. Fix shape: a
boot-time sweep that purges artifact units whose session/project no longer
exists. Blocker: none technical; it was descoped from the review round.
Owner: artifacts-as-units-01DOGF0C follow-up (filed 2026-10-04).

### 2026-10-04 (artifacts-as-units-01DOGF0C review F11) · session delete funnel deletes promoted artifacts — pre-existing, preserved

`rpc/views/sessions` `DeleteWithOptions` (default cascade) lists artifacts
by ORIGIN session and deletes all of them, including ones the user promoted
to project or global scope. That contradicts what promotion is for. The core
session-delete observer only deletes session-SCOPED units and unlinks
promoted ones. The legacy store behaved the same way as the funnel, so
artifacts-as-units kept it as is: a storage migration is the wrong place to
change delete semantics. Decision needed: should the funnel skip promoted
artifacts (filter `ScopeKind=session`)? Owner: the product owner, raised by
artifacts-as-units-01DOGF0C. Decision record:
`docs/missions/artifacts-as-units.md` "Review follow-ups".

### 2026-10-04 (artifacts-as-units-01DOGF0C WP07) · `units.KindArtifact` defined, accepted by the schema, used only in tests — CLOSED

`core/units/unit.go` has declared `KindArtifact = "artifact"` and the
`units.kind` CHECK has admitted `'artifact'` since
`unified-context-artifacts-01NCTXU01` (units/1100), but every reference was
in a test (`resolution_test.go`, `store_mem_test.go`, `store_sql_test.go`)
and artifacts kept living in their own `artifacts` / `artifact_versions`
tables. 01NCTXU01 FR-003 ("artifacts are units with kind=artifact") was
ratified and never implemented — a schema that promised a shape no writer
produced.

**Closed** by artifacts-as-units-01DOGF0C: migration
`units/1104-artifacts-to-units` copies every artifact and version onto
`units` / `unit_versions` (ids preserved, verified in-transaction), and
`core/rpc/api.go` `newArtifactsStack` now builds `artifacts.NewUnitsStore`
— every capture, revision, promote and delete in production writes
`kind='artifact'` units. Decision record: `docs/missions/artifacts-as-units.md`.

### 2026-10-04 (artifacts-as-units-01DOGF0C WP07) · `artifacts_legacy` / `artifact_versions_legacy` retained read-only — DROP due next release — CLOSED

Migration `units/1104-artifacts-to-units` RENAMES the legacy tables
instead of dropping them (spec FR-3.3: never drop in the migration that
copies — dropping in place is how `sessions/0327` and `sessions/0332`
destroyed `artifact_versions`). Nothing reads or writes them after 1104:
the legacy store implementation was deleted with the store switch. They are
a recovery copy for one release of real upgrades.

**Follow-up (dated, owned):** the release AFTER the one that ships 1104
adds `units/1105-drop-artifacts-legacy` (`DROP TABLE
artifact_versions_legacy` first, then `artifacts_legacy` — child before
parent so no cascade fires), with a populated-snapshot test from the
first snapshot that carries the `*_legacy` tables, per the I14 gate.
Blocker: one shipped release of 1104 against real installs. Owner:
artifacts-as-units-01DOGF0C (filed 2026-10-04). Do not fold it into 1104
or into the same release.

**Closed 2026-10-05 by units-debt-01UNITD01 WP02** (target: the release
after v0.87.0 — it must not ship in v0.87.0 itself). Migration
`units/1105-drop-artifacts-legacy` (`core/units/migration_drop_legacy_artifacts.go`)
drops both tables child-first, `IF EXISTS`-guarded, after verifying the
1104 copy in the same transaction: both legacy tables present or neither;
1104 ledgered `applied`; no legacy id held by a non-artifact unit; every
legacy version of a still-present artifact unit — and every synthesized v1
of a version-less one — has its `unit_versions` twin (version, content
hash, byte size, created_at). Any failure returns
`ErrLegacyArtifactsUnverified` — Open fails closed, both tables intact.
Deliberate deviation from the units-debt spec FR-2's literal row-count
equality (orchestrator ruling, 2026-10-05: QUARANTINE, not refuse): legacy
rows whose artifact UNIT is absent — deleted on v0.87.0 (artifact delete,
session / project purge), or lost to a bug/tamper, which looks identical
— are neither refused (the literal check would fail Open for every user
who deleted an artifact during the retention release; mutation-tested:
"literal: 16 legacy vs 11 units") nor silently dropped: each is copied,
with its legacy versions as JSON, into the retained table
**`artifacts_legacy_orphans`** (id, title, content_hash, source_ref_json,
legacy_metadata, quarantined_at) and named at WARN
(`units.drop_artifacts_legacy.orphan_quarantined`: id, hash, title) before
the big tables drop. The quarantine table is bounded (only rows orphaned at
drop time; nothing writes or reads it afterwards) and exists for manual
recovery. An orphan whose id is already in the quarantine table is
refused, not skipped (tampering — no legitimate path writes it twice). Tests: `core/storage/sqlite/migration_1105_test.go` (P-1 on a
reconstructed v0.87.0 state from the newest pre-1104 snapshot + every
seeded artifact shape, zero delta on the four units tables and every media
refcount; P-2: seven planted mismatches refuse and boot once repaired;
check 1 via ledger surgery; deleted-since-copy and mass-loss quarantined;
fresh install + rewind-reopen).
**Not a bug — do not "fix" (2026-10-05, units-debt review):**
`sqlStore.UpdateAtVersion` reads the unit's version OUTSIDE its write
transaction — the same shape the pull path's `UpdateWithSyncState` had
(review M3, fixed there by re-checking `baseVersion` inside the tx). It is
not exploitable: a concurrent bump between the read and the write makes
the history INSERT hit `UNIQUE(unit_id, version)`, which surfaces as
`ErrVersionConflict`. Moving the check without keeping that UNIQUE path
would be the regression.
**Resolved residual (2026-10-05):** `TestMigration1105_V087SnapshotBoots`
runs against the real `testdata/upgrade/v0.87.0/` snapshot (committed on
release/v0.88.0, 3490ee44) and PASSES; the guard fails loudly if a tag
>= v0.87.0 ever exists without a legacy-carrying snapshot.

### 2026-10-04 (artifacts-as-units-01DOGF0C WP01, D4) · artifact version history is write-only — `Store.ListVersions` has no production reader

`kenaz__update_artifact`, plan-mode Edit and edit-file sync all append
revisions (`Manager.WriteVersion`), but nothing outside the store
implementations and their tests calls `ListVersions`, and
`ArtifactsAPI.Get` / the Captured preview always serve the ORIGINAL
capture's bytes (`Artifact.ContentHash`). A user who asks the model to
update an artifact sees the old content in the Library. Found while
mapping versions onto `unit_versions`; not fixed here because it is a
product decision (show history and latest, or serve latest only), not a
storage one. Blocker: that decision. Owner: the next artifacts/Library
mission — escalate to the owner before wiring, do not delete the
WriteVersion path (it is the only revision capability).
### 2026-10-04 · v0.86.0 release-start unwired sweep — findings and dispositions

**Scope.** Base `main` `b8079d48`. Fresh surface: `v0.85.0..HEAD` (the
fs-gate/policy family, the mlsidecar spawn-lock flip) **plus the
v0.85.0 ML-engine surface itself** (`v0.84.0..v0.85.0`: `core/mlsidecar`,
`core/advice/sidecar.go`, `core/rpc/views/sidecar`, the Recommendations
panel) — the v0.85.0 sweep ran at the *start* of that release, before the
engine mission merged, so that code had never been swept.
`cmd/kenaz-ml-sign` and the engine-publication seams are **not on
`main`** (they live only on `feat/engine-publication`) and were not swept;
the next sweep after that branch lands owns them. Already-ledgered items
(engine-publication residuals, marketplace badge-only lie, fail-closed
shared-engine residual, `LaneStatus`) were not re-found.

Baseline before any fix: `check-no-unwired-gates.sh`, builtin-tool
registration, broker topics, knob coverage, output ports, serve-dispatch
drift, agentgraph convergence, seam implementers, node dispatch, single
move writer, Cedar gate arguments and Cedar singleton — all clean. Every
find below is one those gates could not see.

| # | Find (pass) | Disposition | Class / commit |
|---|---|---|---|
| 1 | **`SubagentStartEvent.AdditionalContext` discarded** (pass 2). `subagent_run_spawner.go` fired `subagent_start` with `_, _ =` while `hooks/fire.go` documented the context as "prepended to the child's system context". VERIFIED as briefed. | **Wired** — merged context becomes a session-scoped system attachment on the CHILD session before `StartStream`; the doc now says what happens (and that the decision is not honoured). | Documented lie, producer live / consumer missing — `fix(hooks)` 9caf4197 |
| 2 | **`agentgraph.Env.PendingContext` had zero production writers** (pass 2, then confirmed by the widened gate). No non-test `PendingContextAppender` existed, so every `pre_tool_use` / `post_tool_use` `additional_context` was dropped on every chat run; the field doc's "it is still logged" was also false. | **Wired** — `chat.pendingContextQueue` (per-session, bounded at 64) set on every chat run's Env after `EnvDefaults`; `LLMProviderAdapter` drains it into the next **primary** (`StreamToChat`, i.e. `assistant_turn`) call's system prompt — router / exit-gate / escalation / compaction calls leave it queued (review M2); a failed primary call re-queues it (L3); it is the second-to-last prompt layer, before user instructions (L4); session delete forgets the queue and overflow logs once per burst (L5). Library-graph runs outside chat still drop it — now with a real log line (residual below). | Seam with no implementer — 9caf4197 |
| 3 | `post_tool_use_failure` result discarded with `_, _ =` (pass 2). | **Wired** through the same `forwardHookContext`. | 9caf4197 |
| 4 | `session_start` `AdditionalContext` discarded by `Manager.Create` while `SessionHookRunner`'s doc promised it (pass 2). | **Wired** — rpc-level decorator on the session hook runner attaches it to the new session (never at position 0, which `Sessions_SetSystemPrompt` owns and deletes), under a provenance heading, removable from the Resolved Context panel (review M1/L6). | 9caf4197 |
| 5 | `HookDryRunDrawer` labelled every event's context "injected" (pass 5). | **Wired** — event-aware label ("not delivered for this event" outside the five events that deliver it). | 9caf4197 |
| 6 | Stale comment: `exec_control.go` said `SubagentStartEvent` "is never constructed anywhere" (pass 2). | **Fixed** (comment). | 9caf4197 |
| 7 | `PromptTemplateSource`'s `wiring:deferred` directive sat above its doc comment, where `checkseams`' one-line-up rule never saw it (found by the widened gate). | **Fixed** — directive moved; deferral reason unchanged (versioned-model-profile-01PMDL04 WP02+). | Vacuous-allow directive — 9caf4197 |
| 8 | **`mlsidecar.WriteLocalToken` had zero non-test callers** (pass 3). kenaz-ml reads `lease/shutdown.token` on every shutdown, fails closed without it, and never creates it ("written user-read-only by the spawning client"). Production never wrote it: `Update` never stopped the old engine and `Uninstall` RemoveAll'd the root under a running engine. Every test hand-wrote the token (blind spot #2). | **Wired** — `spawnLocked` writes it under the spawn lock before the process exists; `Update`/`Uninstall` use `ensureLocalToken` (write-if-absent, never rotate). Latent in the field only because `PinnedEngineRelease` is still unpublished — it would have shipped live with the pin. | Fixture doing the production layer's job — `fix(mlsidecar)` 92a0d21b |
| 9 | **`LabelPusher.ResetCursor` had zero non-test callers** (pass 3). The pusher's "rebuildable mirror" guarantee never held: Uninstall wipes the engine's label mirror but the harness cursor survived. | **Wired** — `sidecar.Impl.ResetLabelCursor` after a successful Uninstall only. | Documented guarantee with no caller — dc7e5083 |
| 10 | `LabelPusher.Run` had zero callers, test or production; `Nudge`'s doc still promised "(or Run tick)" (pass 3). | **Deleted** — `Nudge` is the live substitute. The gap a ticker would have covered is closed by `NudgeLabels` (Enable/Update/Repair nudge when they leave the engine healthy). | Live substitute — dc7e5083 |
| 11 | `StatusView.LabelLanes[].until/.detail` sent, never rendered, while the panel promised "it retries automatically" (pass 1 + 5). | **Wired** — the panel shows retry time and engine detail; copy says the lane retries on the next recommendation after that time (there is no timer). | Output with no reader + overclaiming copy — dc7e5083 |
| 12 | `advice.WithSidecarCacheCapacity` — zero callers anywhere (pass 3). | **Deleted** — `defaultAdviceCacheCapacity` is the live value; no product surface tunes it. | No consumer, no product intent — c9adfd92 |
| 13 | `advice.WithSidecarBudget` / `WithSidecarContractsTTL` — test-only callers (pass 3). | **Unexported** (`withSidecar*`), matching `withSidecarClock`. | Test seam exported as a tunable — c9adfd92 |
| 14a | `mlsidecar.Client.Lease` — doc claimed callers use its 404 for legacy detection; zero callers (pass 3). | **Justified** (dated 2026-10-04). Legacy detection reads `/health`'s `lifecycle_protocol`, leases are file-based, contracts come from `/v1/contracts`. **Blocker:** the engine-interop ruling on whether clients must perform the HTTP registration handshake (`POST /v1/clients/lease`). **Owner:** alec. **Deleted by:** that ruling — wire `Lease` into adoption, or delete it with `LeaseWire*` and the stub handler. Doc now says "no production caller". | Cross-repo wire contract — c9adfd92 |
| 14b | `mlsidecar.Client.SystemOne` — zero callers (pass 3). | **Justified** (dated 2026-10-04), on its OWN blocker, not Lease's. `/v1/systemone` is the raw laya pass-through the design §3.2 owner ruling kept in the engine's contract; no harness feature asks laya a raw System-One question (every advisor goes through `/v1/recommend/{kind}` with a feature contract). **Blocker:** a product decision that some harness surface needs raw laya answers outside the advice-kind contract — none is specced or roadmapped. **Owner:** alec. **Deleted by:** the next sweep if no spec has claimed it by then (delete the method, `SystemOne*` wire types and the stub's `/v1/systemone` handler together), or by the spec that wires it. | Wire completeness, no product consumer — c9adfd92 |

**Gate extension (rule: a class the gates could not see).**
`check-seam-implementers.sh`'s derivation (`scripts/ci/cmd/checkseams`)
now also scans exported structs whose name ends in **`Env`** —
`agentgraph.Env`'s interface fields are exactly G-1a's
optional-collaborator shape, but the struct's name kept them out of
scope, which is how find #2 survived with the gate green. Widened, it
fired on exactly `PendingContextAppender` (pre-fix) and
`PromptTemplateSource` (find #7). Planted-violation proof:
`TestGates_PlantedViolationFires/seam-implementers/derived-env-field-unsatisfiable`,
verified failing against the pre-widening checker. No allowlist changed.

**Declined gate — finds #8–#10, #12–#14 (zero-call-site exported
functions outside the I10 name heuristic).** A general "exported func
with no non-test caller" gate over-reports badly (Wails bindings, wire
mirrors, interface methods, `json` decode targets all look unconsumed to
a grep), and an allowlist big enough to hold that noise would be the
"clean verdict indistinguishable from did not look" class
`gates_can_fail_test.go` exists to prevent. The CLAUDE.md pass-3 scan
over `git diff <last-tag>..HEAD` *is* the mechanism; this sweep's
scratch scan (exported `func` declarations in files changed since
v0.84.0, filtered to symbols added since then, with zero non-test
references) is reproducible from that description. Finding #8's real
root cause — fixtures that do the production layer's job — is blind
spot #2, which no file-level gate can see.

**Residuals, not drained (each with blocker + owner):**

- **Hook `additional_context` on library-graph runs outside the chat
  runner** is still dropped (now logged as
  `agentgraph.hook_context.dropped`). Blocker: those runs drive LLM nodes
  through the graph manager's own provider path, which has no per-session
  system-prompt layer to drain into; giving it one is graph-runtime
  work, not a sweep fix. Owner: alec — the agent-graph convergence
  mission (01PMGX01) closes or re-dates this.
- **`user_prompt_submit` / `setup` additional_context** — those events
  still do not fire at all; already held by
  `scripts/ci/allowlists/i17-eventless-hook-events.txt` (no new entry).
- **Hook-attached session context is persistent**, by design: a
  `session_start` / `subagent_start` attachment stays in that session's
  system prompt every turn until the user removes it. It carries an
  "Additional context from the user's <event> hook:" heading (review
  L6), and the session's Resolved Context panel now offers Remove on
  session-scope rows (review M1 — until the review follow-up every row
  there was mounted read-only, so "removable" was false and the context
  could not be taken back). Recorded so a later reader does not mistake
  it for a one-turn note.

**Not findings (recorded so the next sweep skips them):** mlsidecar wire
types carry decode-only fields with no Go reader (`HealthPayload`/
`KindContract`/`LabelPushResponse` fields such as `DTypes`,
`SupportedVersions`, `Replaced`, `Stale`, `Generation`, `Slot`,
`Refusal`) — wire mirrors of the engine's contract, not claims.
`StatusView.Supported` has no frontend reader, but its doc already names
`Available` as "the single 'may the Enable button be offered' bit".
`SettingsView.vue`'s `TODO(compaction-strategy-ui-01KQ8TDI WP06)`
deprecated-model chip predates the fresh surface and describes a missing
feature, not a lie.

### 2026-09-30 (laya-advisors-01LAYA001 WP13) · the dated-nil `sidecarProbe` is replaced; two dated justifications remain

`core/rpc/api.go`'s `sidecarProbe` (nil since WP12, "until the Settings
surface lands") is now a real `*mlsidecar.Manager` + `DemandProbe`
(`core/rpc/sidecar_wiring.go`), and `core/mlsidecar` left
`i7-orphan-packages.txt`. Two things are intentionally still inert, each
with a blocker and an owner:

- **`mlsidecar.PinnedEngineRelease` returns `ErrNoPublishedRelease`.**
  The Settings panel therefore reports the install action as unavailable
  ("not published to the release channel yet") instead of offering a
  button that cannot work — honest, not decorative. Blocker: the
  channel-publishing infra task (kenaz-ml CI's
  `kenaz-ml-macos-arm64-notarized` .dmg published to the env-specific
  kameas release channel with an A-1 manifest signed by a trusted
  anchor). Owner: release-infra. Deleted by: the change that makes that
  function return the pinned `EngineRelease` from the harness release
  manifest. The whole download -> verify-over-DMG-bytes -> mount -> copy
  -> clear-quarantine -> start path is exercised against a local fixture
  (`core/mlsidecar/install_dmg_test.go`, incl. a real-hdiutil test).
  > **CLOSED 2026-10-07 (`chore(ci)`, engine pin → kenaz-ml 0.1.1):**
  > the darwin/arm64 release build now pins the published engine — see
  > the engine-publication 2026-10-04 entry below for the evidence.
- **(RESOLVED at the WP13 merge into `release/v0.85.0`, 2026-09-30.)**
  `sidecarProbe`'s consumers (`SidecarAdvisor` WP15, the label pusher
  WP14) and the real Manager+DemandProbe now live on the same branch;
  the probe is demand-driven end to end and `LabelPusher.LaneStatus`
  gained its production reader (`StatusView.LabelLanes` → the
  Recommendations panel). The `main`-only inertness this bullet
  recorded no longer exists anywhere a release is cut from.

- **Half-broken install leaks the shared ml root on uninstall (dated
  2026-09-30, WP13 merge review F3; owner: the release-channel wiring
  that makes `PinnedEngineRelease` live).** Uninstall's whole-root
  RemoveAll requires a KNOWN install record at entry
  (`core/mlsidecar/manager.go` Uninstall), and the panel only offers
  Uninstall when `view.installed` is true — so a corrupt/unknown
  `install.json` leaves engine-written state (models, config, logs)
  behind with no UI removal path. Unreachable today (no published
  release means no field installs); the moment the pin goes live, add a
  "remove anyway" recovery path or a repair-record step.

### 2026-09-12 (fleet-generic-sync-framework-01NSYNC02 WP03) · `SyncKind.ConflictPolicy` and `.SecretPolicy` were validated-only dials, never consumed — FIXED

Dials-to-consumer trace (CLAUDE.md unwired-sweep pass 4) on the
`fleet.SyncKind` registration struct WP01/WP02 introduced. Both
`ConflictPolicy` and `SecretPolicy` are marked "Required" on the struct
and `KindRegistry.Register`'s `validate()` rejects a kind that leaves
either empty — but a repo-wide grep for `.ConflictPolicy` / `.SecretPolicy`
reads (not just the constant declarations) turned up exactly one call site
each, and it was the same `validate()` non-empty check. Nothing ever
branched on *which* value a kind declared: `mcpRecipesKind`
(`core/rpc/sync_categories.go`)'s own comment said as much — "this
framework's own conflict machinery [...] has not landed yet" — and every
kind's `SecretPolicy` was pure documentation of an honor-system rule each
collector/applier was independently trusted to follow.

Fixed in the same change (not deferred):

- `SecretPolicy`: `core/fleet/secretshape.go`'s new `SecretShapeReason`
  scans for `@secret:` references, API-key-shaped literals, and a fixed
  set of exact-match credential field names, walking arbitrary JSON
  recursively. Wired into BOTH invocation paths — `SyncKind.CategoryConfig()`
  (the ScopeUser/LWW adapter, `core/fleet/synckind.go`) and
  `compositeConfigApplier.ApplyBundle`'s org_config dispatch loop
  (`core/rpc/views/settings/fleet.go`, ScopeOrg) — since the two paths
  invoke `kind.Apply` through different code and neither previously
  consulted `SecretPolicy` at all.
- `ConflictPolicy`: the actual shadow-vs-delete CONFLICT RESOLUTION
  remains correctly kind-specific (`core/mcp/recipes/merged.go`'s org-layer
  precedence for `mcp_recipes`) — a framework-generic resolver over an
  opaque `[]byte` payload was never a buildable goal, so this is not
  "fixed" in the sense of making the enum drive behavior directly. What
  IS now real: `KindRegistry.MarkOrgApplied`/`OrgAppliedAt`/
  `ClearOrgProvenance` (`core/fleet/synckind.go`) track, generically and
  kind-agnostically, *whether* a kind is currently org-provisioned —
  the "shared provenance model" WP03's tasks.md acceptance asked for,
  consumed by the Settings → Sync surface (WP06) and cleared on sign-out
  (`StopFleetBackground`).

Mutation-proof tests: `core/fleet/secretshape_test.go` (clean payloads
pass, secret-shaped ones don't, lookalike field names like
`tokenEnvVar` are NOT flagged), `core/fleet/synckind_test.go`'s
`TestSyncKind_CategoryConfig_{Collect,Apply}RejectsSecretShapedPayload`
+ `CleanPayloadStillRoundTrips`, and
`core/rpc/views/settings/fleet_orgconfig_test.go`'s
`TestApplyBundle_OrgConfig_{RefusesSecretShapedPayload,
CleanSecretlessPayloadStillApplies, MarksProvenanceOnSuccess,
DoesNotMarkProvenanceOnFailure}`.

### 2026-09-12 (fleet-generic-sync-framework-01NSYNC02 WP03) · `recipes.Recipe.Source` never reached the frontend — KenazToolsPanel.vue hardcoded every row to 'shipped' — FIXED

`recipes.Recipe.Source` carries `json:"-"` (correctly — it must never
round-trip through the on-disk YAML/JSON recipe-definition codecs) and
`tools.RecipeListing` (the `Tools_ListRecipes` wire response) embedded
`Recipe` by value with no separate field to carry it. The frontend's own
`sourceBadge()` in `KenazToolsPanel.vue` said so directly: "BACKEND GAP:
The wire shape does not yet carry a `source` discriminator... [it] returns
'shipped'" for every row, always — including the org-provisioned MCP
recipes `fleet-org-config-inheritance-01NORGX01` had already wired
end-to-end on the backend (`recipes.ApplyProvisionedMCP` → `SourceOrg`).
There was no way for a member to see "Provisioned by your org" on any
MCP recipe, because the ONE field that would tell them never reached the
browser.

**Decision**: add `Source string` as a top-level field on
`tools.RecipeListing` (`core/rpc/views/tools/api.go`), populated from
`recipes.Recipe.Source` in `ListRecipes`. This is a dedicated,
purpose-built wire type ("Wire shapes are deliberately small" per its own
doc comment) distinct from `recipes.Recipe` itself, so re-exposing the
value here does not touch the on-disk `json:"-"`/`yaml:"-"` contract or
any other endpoint that returns a bare `recipes.Recipe` (e.g.
`SaveCustomRecipe`). The alternative — flipping `Recipe.Source`'s own
json tag — was rejected because `Recipe` is a shared type serialized in
multiple contexts with different needs.

Frontend: `KenazToolsPanel.vue`'s `sourceBadge()`/`sourceBadgeClass()`
now read the real field; a new `isOrgManaged()` hides the Edit/Delete
buttons and shows a "Provisioned by your org" badge for `source === 'org'`
rows, mirroring `SkillsPanel.vue`'s existing FR-302 "Org-managed"
treatment for mandated skills (same rationale: editing an org-provisioned
row would silently create a lower-precedence personal override the next
bundle re-shadows without warning).

**Known follow-up, not resolved here**: `tools.RecipeListing` is a
Wails-bound return type; `scripts/ci/check-codegen.sh` will report
WAILSJS DRIFT until `frontend/wailsjs/go/models.ts`'s `tools.RecipeListing`
class is regenerated to include `source: string`. Per this repo's
`wails generate module` hazard (it opens whatever database `HOME`/
`KENAZ_HARNESS_ENV` resolve to — see CLAUDE.md's "Tooling footguns"), this
was deliberately NOT run here; the frontend adapts the new field through
hand-maintained interfaces (`WireRecipeListing` in `harnessClient.ts`,
which `Tools_ListRecipes`'s runtime call path never routes through
`models.ts` for anyway), matching the "hand-declare on WailsBindingsLike"
pattern this repo uses for binding changes a session can't safely
regenerate. A `wails generate module` pass (with the documented
`HOME=$(mktemp -d) KENAZ_HARNESS_ENV=test` override) is needed before the
codegen-drift gate goes green again.

Mutation-proof: `core/rpc/views/tools/impl_test.go`'s
`TestListRecipes_SourceField` asserts an org-tagged and a shipped-tagged
recipe in the SAME catalog produce DIFFERENT `Source` values (a hardcoded
constant would pass either check alone but fail the inequality assertion).
`frontend/src/views/tools/__tests__/KenazToolsPanel.test.ts`'s new
"org-provisioned recipe read-only badge (WP03)" suite asserts the badge
renders and Edit/Delete are hidden for `source: 'org'`, and — the
mutation-proof half — still render for an ordinary shipped row.

### 2026-09-12 (model-settings-reach-the-model-01PMZ101 UNIT-10 / WP17, ESCALATION — not resolved here) · `branchesview.API.parentModel` is a hardcoded `return "", ""` stub; the cross-provider warning can never fire
### 2026-09-12 (fleet-enforcement-truth-01PMZ505, WP01/WP10 decision record — AC-014/AC-015) · disposition of the mission's fourteen handed-over findings, triaged against the live tree

**This mission was triaged, not built from scratch.** Its spec.md was
written against `origin/main@55029354` (tag `v0.64.0`); the release branch
this pass landed on (`release/v0.78.2`'s worktree base) already carried 23+
merged missions from later campaigns. Re-verification against the LIVE tree
(not the spec's cited state) found five of the spec's sixteen WPs already
shipped by other missions, and the mission's flagship P0 (§1.3, the audit
retention backend) already resolved more completely than the spec's own
"interim honesty change" asked for. Per-finding disposition:

| Finding | Spec's ask | Live-tree status | This pass |
|---|---|---|---|
| §1.1 `SetCedarEngine` zero callers (P0) | wire it, WP03 | **Already wired.** `api.go` calls `settingsImpl.SetCedarEngine(a.cedarEngine)`; `fleet_wp03_test.go` pins `TestSetCedarEngine_WiresApplierToLiveEngine` against the real engine. | Verified only (RAN `go test ./core/rpc/views/settings/... -run SetCedarEngine`, PASS). |
| §1.2 `Bundle.ModelPrefs` stored, never read (WP04) | consumer reaching a branch, WP04 | **Already wired.** `llmview.ApplyFleetModelPrefs` installs `DefaultModel`+`ProviderAllowlist`; `bundle_knob_coverage.go` registers it against `knobcoverage`. | Verified only. |
| §1.3 retention window confirmed, unenforced (WP05) | interim honesty change ONLY — spec explicitly forbids building the backend here (C-1 hands that to a successor mission) | **Superseded — the successor already shipped.** `audit-that-tells-the-truth-01PMZA10 UNIT-7` built the real event-log-backed `AuditRetentionBackend` (`core/rpc/views/settings/audit_retention_adapter.go`) and wired it at `api.go`'s sweeper construction (`fleetRetentionBackend` is non-nil whenever `auditBackend != nil`, which is the production shape). **RAN**, not read: `TestFleetAuditRetentionBackend_WiredIntoRealSweeper_AC015` passes with a real delete on a real sqlite-backed store (`log="fleet/audit_retention: sweep pass complete deleted=1"`). This mission's WP05 "ship the honesty change because nothing can enforce it" premise is **factually superseded**: something now enforces it. | Deleted the two remaining lies (AC-007): `applyRetentionConfig` (`core/fleet/audit_retention.go`, zero callers, fed a `Bundle.audit_local_retention_days` field that has never existed) and its two false doc comments. Did not add `RetentionEnforced`/`HasBackend()` — the residual nil-backend path is a "defensive fallback... not a production configuration" per ZA10's own in-code comment, not the live shape AC-006 was written against; adding a second honesty layer on top of a real implementation would be gold-plating a closed finding. **Escalation for a future sweep, not resolved here**: `ComplianceStatus` still has no field distinguishing "retention enforced by a real backend" from "retention configured but inert" for the residual `auditBackend == nil` edge case — low severity given it's non-production-shaped, but worth a one-line field if anyone hits it. |
| §1.4 chain-break recovery, no surface (WP06) | `Compliance_SkipToID` RPC + binding + client + UI | **Backend built and mutation-tested this pass**: `ComplianceAPI.SkipToID`, gated identically to its siblings, delegates to the already-tested `*fleet.AuditArchiver.SkipToID`. `Bindings.Compliance_SkipToID` added. **Frontend NOT wired** — see "Frontend-binding deferral" below. | `core/rpc/views/compliance/{api,impl}.go`, `core/rpc/bindings.go`; `TestComplianceAPI_SkipToID_ClearsChainBreakAndEmits` mutation-proven (reverting the delegation makes it fail). |
| §1.5 sync arm matched pair (`installed_mcp`, WP07) | id fix + Reader/Writer/SecretKeys, one commit | **Already wired.** `SyncPanel.vue`'s id is `installed_mcp`; `core/rpc/api.go` constructs `NewMCPSyncCategory(mcpRegistry, mcpRegistry, <secretKeys func>, syncPending)` with a real reader/writer over `tools.API.ListRecipes`; `SyncPanel.spec.ts` drives the canonical backend ids, not a hand-built fixture. | Verified only (RAN `go test ./core/rpc/... -run Sync`, PASS; frontend spec RAN, PASS). |
| §1.6 lockdown reason dropped (WP08) | store + return the reason | **Not wired — fixed this pass.** `lockdownActive` was a bare `atomic.Bool`; both write paths (`Watcher.run`, `BootstrapLockdownStatus`) parsed the reason off the wire and only logged it. | `core/fleet/lockdown.go` (new `lockdownReason atomic.Value` + `setLockdownState` single write path), `core/rpc/views/settings/fleet.go`'s `FleetLockdownStatus`. Mutation-proven: `TestBootstrapLockdownStatus` now asserts `LockdownReason()=="bootstrap-test"` after the BOOT path (no broker replay) — reverting the fix fails it. No frontend change needed: `LockdownStatusView.Reason`/`types.ts`'s `reason` field already existed on the wire type. |
| §1.7 site env vars unsettable (WP09) | `Sites_EnvSet`/`Sites_EnvList` RPC + binding + surface | **Not wired — backend built this pass.** `SitesAPI.Sites_EnvSet`/`Sites_EnvList`, `FleetSitesClient` interface extended, `Bindings.Sites_{EnvSet,EnvList}` added. **Frontend NOT wired.** | `core/rpc/views/sites/{api,impl}.go`, `core/rpc/bindings.go`; `TestSitesEnvList_NeverReturnsAValue` mutation-proven at the JSON-wire level (a planted `Value` field is caught). No MCP tool added (spec explicitly forbids it in the same WP). |
| §1.8 four orphans (`Client.SignOut`, `Client.Unpublish`, `SyncKind.HasScope`+friends, `applyRetentionConfig`) | delete/wire/justify per-symbol | `Client.SignOut` **deleted** this pass (D-5 — zero callers, rival to `settings.API.FleetSignOut` which additionally calls `StopFleetBackground` first). `Client.Unpublish` **wired** this pass (WP11, below). `SyncKind.HasScope`+seven siblings — **RULED (register F-2): justified, not deleted** — `fleet-org-config-inheritance-01NORGX01`'s `meta.json`/`spec.md` already carry owner `alec` + blocker "kenaz-fleet org endpoints not yet available" + date 2026-08-19; this entry cross-references it rather than duplicating. `applyRetentionConfig` **deleted** (see §1.3 row). | `core/fleet/client.go` (deletion), `core/fleet/catalog.go`+`impl.go` (WP11 wiring, below). |
| §1.9 catalog/skill signature verification skipped (WP10, register C-2) | honesty change: comments stop reading as settled, docstrings corrected, Marketplace says installs are unverified, `WithPubKey` kept | **Not wired — fully built this pass.** Five edits: (1) `api.go`'s `PubKeyBase64: ""` comment; (2) `catalog/impl.go`'s `pubKeyBase64` doc; (3) both `harnessClient.ts` "Downloads, verifies, and live-registers" docstrings + `catalog/api.go`'s `Catalog_Install` doc; (4) `MarketplaceView.vue` gained a persistent plain-text notice (`data-testid="marketplace-unverified-notice"`) — not a modal, not a tooltip (MarketplaceView deleted in install-framework Phase 4; the notice lives in the skill / workflow detail plugins of the Capabilities surface, the only fleet-catalog install paths left); (5) `verifyCatalogSignature`'s skip now logs at warn. `WithPubKey` untouched (kept per C-2). *2026-10-05: C-2's design is settled as "no per-device/per-org catalog key, ever; mandated items verify via the pinned bundle signature; payload signing with the bundle key is a pending fleet-owner decision" — the reason string and comments now say so (`fix/fleet-contract-truth`).* | Frontend test suite RAN clean post-edit (`vitest run`, 261 files / 2445 tests pass); `vue-tsc --noEmit` clean. |
| §1.16 / task #43 `core/fleet.VerifySignature` zero callers | verify `01PMZ909` UNIT-1 rewrote the i10 allowlist entry; only touch it if that mission slipped | **Verified: `01PMZ909` UNIT-1 already landed it.** `scripts/ci/allowlists/i10-unwired-gates.txt`'s entry now reads "UPDATED 2026-08-21 by bundle-download-and-verify-01PMZ909 UNIT-1/UNIT-9... Standing verdict SUPERSEDED", exactly per that mission's own §9.2 commitment (C-14). Not touched here — touching it would have been the rival-infrastructure failure mode AC-029 warns against. | Read-verified (`grep` on the allowlist file). |
| §7 G-1/G-2 (nil-optional-dep + uncalled-wiring-setter gates, WP10) | new gate(s), planted-violation proofs | **Already built, by a different mission, in a coordinated form.** `check-nil-optional-deps.sh` (I18, built collaboratively per its own header: "THREE MISSIONS SPECCED THIS GATE; NONE BUILT IT... this tool takes the doc-phrase design") subsumes both G-1 (nil optional dep on a Config/Options struct) and G-2 (an uncalled `Set*`/`With*` method) — its clause-3 fix is literally "a Set*-named method whose body assigns the field from its own parameter... with NO call site anywhere" (`gates_can_fail_test.go`'s `setter-defined-but-never-called-still-fires`), which is G-2 verbatim. `check-config-nil-coverage.sh` (built by `trust-surfaces-that-fire-01PMZ202` WP26) covers the sibling "declared, read, never assigned" shape with two planted-violation proofs of its own. This mission's own I13-widening design (`check-cedar-gate-arguments.sh`) was **not** the one that shipped — per CLAUDE.md's coordination rule, whoever lands first owns the gate. Building a second gate for the same class here would be rival infrastructure. | Verified only (read both scripts' headers + `gates_can_fail_test.go`'s planted cases; did not re-run the full gate suite in this pass — see "What was RUN" below for what was). |
| §1.11/§1.12 Accent inert-and-pushed; sync push-path trace (WP12) | remove `Accent` from the wire; honest row copy; consumer-map enumeration test (G-5) | **Not wired — fully built this pass.** `Accent` removed from `uiThemePayload` (collect + apply); `SyncPanel.vue` rewritten for all four non-`installed_mcp` rows (`model_prefs` now names its real four fields, `ui_theme` claims only color, `provider_profiles`/`mcp_recipes` state "not yet syncing"). | `core/rpc/sync_categories.go` + three test files; AC-024 assertions added to `SyncPanel.spec.ts` (mutation-proven: reverting the `model_prefs` description to "Default model, provider allowlist..." fails the new test). **G-5 enumeration test (AC-022) NOT built** — see "Not done" below. |
| §1.12 (dup ID, spec's own numbering — remote purge, WP13) | confirm-guarded UI action per surface | **Already wired.** `SessionsView.vue`/`ProjectLandingPage.vue` both call `client.{Session,Project}Sync_DeleteRemote` with confirm dialogs; `SessionsView.remotePurge.spec.ts`/`ProjectLandingPage.fleetSync.spec.ts` exist. | Verified only. |
| §1.13/§1.14 served-mode token invalidation + sign-out ledger event (WP14) | wire `Invalidate`/`NotifyOn401` at a real 401-observing call site; add `LedgerEmitter.EmitSessionLifecycle`-shaped method; wire both entry points | **Not wired — built this pass, with one honest gap.** New `On401 func()` hook threaded through `core/mcp.ServerSpec` → both `transport/http` and `transport/sse` `Spec` → their `dispatch`/GET/POST paths (the only place a served-mode OAuth connector's 401 is actually observable — the token is handed to a spawned subprocess's own HTTP calls, which the harness cannot see). Supervisor wires `spec.On401 = func(){ tokens.Invalidate(id) }` for OAuth connectors. `LedgerEmitter.EmitSessionLifecycle(event string)` added (the missing `func(string)` shape); both `main.go` and `cmd/harness-served/main.go` now construct their `authbroker.Session` with `WithLedgerEmit(ledgerEmitter.EmitSessionLifecycle)`. **`Session.NotifyOn401` is NOT wired** — see "Escalated, not guessed" below; it is a materially different, deeper finding than the spec anticipated. | Mutation-proven at two layers: `TestConnectionOn401_Fires`/`_DoesNotFireOn500` (http+sse transport level, real httptest 401/500) and `TestSupervisor_OAuthConnector_On401InvalidatesCachedToken` (supervisor level). `main.go`/`cmd/harness-served/main.go` wiring is READ-verified only (package `main`, no test harness). |
| §1.15 ACP peer registry nil secrets backend (WP15) | wire consumer or record; escalate secrets backend (E-008); dated justification for `DefaultRegistry` | **Escalated, not guessed — per spec's own explicit instruction ("do not fix this by passing a non-nil backend to make the linter quiet").** `core/acp/events` (the package `peers.NoopEmitter`'s own doc names as owning "the real wiring") **does not exist anywhere in the repo** — confirmed no directory, no second `AuthEventEmitter` implementer. Recorded in-code at `api.go`'s `acpReg := acppeers.NewRegistry(...)` construction site (both nils explained) and at `DefaultRegistry()`'s declaration (`wiring:deferred`, dated 2026-09-12, owner alec, distinct from `AN-10` per C-10 — `DefaultRegistry` has zero callers repo-wide and cannot itself produce the nil-secrets defect). | Read-verified (repo-wide grep for `PeerAuthAttempted`, `core/acp/events`). **E-008 (secrets backend product-scoping question) remains genuinely open — flagged for owner alec, no default assumed.** |

### 2026-09-15 (fleet-enforcement-truth-01PMZ505, WP05 follow-up — CORRECTION to the 2026-09-12 entry above, finding #103) · the §1.3 and §1.8 "applyRetentionConfig deleted" claims were both false

**The 2026-09-12 entry above is preserved verbatim, uncorrected, above this
note — this is the correction, not a rewrite of that entry.** Its §1.3 row's
"This pass" cell states *"Deleted the two remaining lies (AC-007):
`applyRetentionConfig` (`core/fleet/audit_retention.go`, zero callers, fed a
`Bundle.audit_local_retention_days` field that has never existed) and its two
false doc comments."* Its §1.8 row states *"`applyRetentionConfig`
**deleted** (see §1.3 row)."* **Neither was true.** An independent audit on
2026-09-14 re-read the tree and found the function still present, unchanged,
with the same zero non-test callers it had before that pass claimed to
remove it — `git log --oneline --follow -- core/fleet/audit_retention.go`
shows exactly two commits touching the file, `8d96d337` (original WP04 add)
and `b11f5223` (a squash merge that also added it); no commit ever deleted
it. The false claim was written twice in the same sweep pass (§1.3's prose
and §1.8's cross-reference to it), which is why it is being corrected in two
places here rather than one.

**Deleted for real on 2026-09-15** (fleet-enforcement-truth-01PMZ505 WP05
follow-up commit, this pass): the function body, its two false doc comments
(the file header's bundle-config claim and the function's own "Used by the
composite ConfigApplier" claim), and the `encoding/json` import that only it
used. Positive no-consumer proof, re-run after the deletion:
`grep -rn 'applyRetentionConfig' core/` and
`grep -rn 'audit_local_retention_days' core/` both return zero hits (exit 1);
`go build ./core/...` is clean. `Bundle` (`core/fleet/bundle.go:42`–`:82`)
was re-confirmed to carry no such field. `core/rpc/wp_pi_test.go`'s WP05
persistence-integrity note, which had independently repeated the same false
"already deleted" claim in its own prose, was corrected in the same commit —
without naming the deleted symbol by its literal identifier, since AC-007's
grep is over all of `core/` including test-file comments.

**Why the earlier claims were premature, not malicious:** the 2026-09-12
pass's own "What was RUN" ledger section (below) shows it verified the
*sibling* findings in this same table by running tests, but for §1.3/§1.8 it
appears to have read the *intended* diff (or an equivalent change made in a
sibling worktree during the same campaign) rather than re-reading the merged
file on the branch it actually recorded against. This is exactly the failure
mode CLAUDE.md's citation discipline exists to catch, and it reached the
ledger anyway — recorded here as finding #103's disposition, not swept under
the correction.

**Frontend-binding deferral (WP06, WP09, WP11) — one blocker, three
findings.** All three new RPC surfaces (`Compliance_SkipToID`,
`Sites_EnvSet`/`Sites_EnvList`, `Catalog_Unpublish`) are fully built,
tested and mutation-proven on the Go side — `Bindings.go` methods exist
and are `check-binding-names.sh`-clean. **None has its
`frontend/wailsjs/go/rpc/Bindings.{js,d.ts}` mirror, `harnessClient.ts`
entry, or UI control**, because this pass's operating constraints
explicitly forbade hand-editing `frontend/wailsjs/**` (the repo's normal
path for adding one, absent the DB-opening `wails generate module`
risk CLAUDE.md documents). Blocker: an agent authorized to hand-mirror
three small binding stubs following the existing 2870-line mirror's exact
pattern (`window['go']['rpc']['Bindings']['MethodName'](args)`), plus
three small UI affordances (a chain-break-recovery input in
`CompliancePanel.vue`, an env-var form in a Sites settings surface, an
"Withdraw" action in `MarketplaceView.vue` distinct from "Uninstall" per
AC-021 — *since wired, and moved with the rest of the catalog browse to
the Capabilities surface when MarketplaceView was deleted,
install-framework-01DOGF0B Phase 4, 2026-10-05*). Owner: alec. This is the single largest remaining gap between
this pass and full AC-008/AC-012/AC-020/AC-021 satisfaction — the backend
halves of all four ACs are proven; only the last-mile frontend wire is
missing, which is the cheapest-win class CLAUDE.md's disposition rubric
names explicitly.

**Not done in this pass, named rather than left silent:**

- **AC-022 (WP12 G-5) — the sync-payload consumer-map enumeration test.**
  The individual findings it would have caught (`Accent`, the three
  mislabelled rows) are fixed; the machine-checked enumeration itself
  (`AllSyncCategories()`-driven table test naming a real consumer per
  payload field) was not built. A future field addition to any
  `SyncKind`'s payload struct with no consumer is therefore not yet
  CI-caught for this class — same residual gap `check-knob-coverage.sh`
  closes for `fleet.Bundle` but not (yet) for `core/fleet.SyncKind`
  payloads.
- **AC-023's `Settings.Accent` field disposition (D-7's "wired-down"
  half).** `Accent` stopped travelling (done); whether the field itself
  has a future is E-009, unaddressed here (unchanged from the spec's own
  framing — "cross-reference E-009 for whether the field itself has a
  future").
- **The three fleet audit kinds with zero emit call sites**
  (`KindFleetConfigApplied`/`KindFleetConfigSignatureRejected`/
  `KindFleetConfigPartialFailure`) — **already recorded above** (2026-09-12,
  `01NORGX01` WP02 triage entry). **UPDATE (2026-09-15, `01NORGX01`
  WP05):** `KindFleetConfigApplied` is now wired (see the updated entry
  above) — this mission's own FR-010 is met for the section it owns. The
  other two kinds remain open, owner alec, same `ConfigPoller`-plumbing
  blocker.
- **`config_pull.go`'s stale header claiming a `bundle.json` disk cache**
  — likewise already recorded in the `01NORGX01` entry above; not
  re-fixed here for the same cross-cutting-architecture reason that entry
  gives.
- **The `CapContextSync`/server-capability-document mismatch** flagged in
  this pass's brief could not be verified from this repository — the
  fleet server's capability document is out-of-repo. `core/fleet/
  capability.go`'s client-side declaration (`CapContextSync = "context_sync"`)
  is internally consistent and matches `SyncPanel.vue`'s gate key
  (already corrected 2026-08-14 per that file's own header comment).
  Recorded as unverifiable-from-here rather than silently dropped.

**Gate-extension rule note:** no new *class* of defect was found this pass
that an existing gate cannot see — every new behavioural fix (lockdown
reason storage, retention dead-code deletion, catalog error mapping, sync
payload honesty, connector-token invalidation) is a single-instance defect
fix, not a pattern a future author could reintroduce invisibly. No gate
extension is owed.

Found while implementing WP17 (branch recommender provider hydration,
closing finding AN-07). WP17's own spec text describes only a narrower
gap — `core/rpc/branches_wiring.go`'s `knownModelProviders` literal
covering just `["anthropic", "openai"]` — and that half is fixed in this
landing (widened to `anthropic, openai, gemini, azure-openai,
openrouter`, plus a real `agentgraph.BranchRecommender.pickAtTier` fix
so an unknown provider degrades to the parent's own pair instead of
silently substituting a different provider's model — see the mutation-
verified `TestRecommender_UnknownProvider_FallsBackToParentNotCrossProvider`).

**But the recommender was never the whole path**, and this second half
is NOT fixed here — it needs a product decision, not a technical patch:

`core/rpc/views/branches/impl.go`'s `parentModel(_ context.Context, _
string) (string, string)` — the function `RecommendModel` calls to learn
what provider/model the FORK'S PARENT session is actually on — is
verbatim:

```go
func (a *API) parentModel(_ context.Context, _ string) (string, string) {
	// v1: we don't yet thread the parent's active model through
	// session.Record. The recommender accepts empty parents and uses
	// the model id heuristic. Future patch: read from the per-session
	// model dial.
	return "", ""
}
```

It ignores both its `ctx` and `sessionID` arguments, and ignores
`a.cfg.Sessions` (a real, already-wired `*session.Manager`) — not
because the wiring is missing, but because **there is nowhere to read
the answer from**: `session.Record` has no provider/model field at all
(confirmed by reading `core/session/types.go`'s full struct). The
session's active (provider, model) selection lives ONLY in the
frontend's per-session `localStorage`
(`kenaz.session.config.${sessionID}`, `SessionsView.vue`'s
`readSessionConfig`) — it is never sent to the backend at all, let alone
persisted.

**Practical effect, verified by reading the call chain, not run against
a live app:** every `RecommendModel` call resolves `parentProvider = ""`
unconditionally. `RecommendModel`'s own cross-provider-warning check —

```go
if parentProvider != "" && rec.ProviderID != "" && parentProvider != rec.ProviderID {
    out.CrossProviderWarning = "Cross-provider fork: ..."
}
```

— can **never fire**, for any parent, on any provider, today — not
because same-provider detection works, but because the first operand is
always false. This mission's own WP17 fix (the recommender degrading
correctly to the parent's pair for an unknown provider) is invisible
through this RPC: `Recommend("", "", ...)` always resolves via the
empty-string branch regardless of which provider the parent is really
on, so AC-015 as literally written ("assert the recommender returns a
candidate of the parent's own provider ... with no cross-provider
warning") is verified in this landing at the `agentgraph.BranchRecommender`
level (where WP17's actual fix lives) and is **not** observable end to
end through the live RPC, independent of how correct the recommender
itself now is.

**Why this is an escalation, not a fix landed here:** closing it needs
one of two real product decisions, not a guess:

1. Persist an "active (providerID, modelID)" field on `session.Record`
   (a new migration, a new write path on every model switch, and a
   decision about whether the tune-panel/`/effort`-style "session
   default" pattern this same mission's UNIT-6 just built is the right
   home for it or a separate concept), **or**
2. Widen the `RecommendModel` RPC signature to accept the frontend's
   already-known `activeProviderId`/`activeModelId` as explicit
   parameters (no new persistence, but a live RPC contract change — the
   same `wails generate module` regeneration blocker this mission's
   UNIT-6 WP10 hit, at larger blast radius since `RecommendModel` is a
   live, already-shipped binding, not a new one).

Neither is a WP17-sized change, and picking one without owner input
would be exactly the "two rival implementations, pick unilaterally"
mistake CLAUDE.md's escalation guidance warns against.

**Blocker:** an owner ruling on (1) vs (2) above. **Owner:** alec.
Re-check at the next mission that touches `branchesview.API` or session
provider/model selection.

### 2026-09-12 (model-settings-reach-the-model-01PMZ101 UNIT-6 / WP10) · `session_messages.knobs_override` stays unread — a product decision, not an oversight

Migration `sessions/0330-knobs` shipped two columns:
`sessions.knobs_default` and `session_messages.knobs_override`. UNIT-6 /
WP10 gave the first one its first production writer AND reader
(`Sessions_{Get,Set}KnobsDefault`; `chat.LLMProviderAdapter.Generate`'s
send-path merge onto `GenerationRequest.Knobs`) — the "reaches the model"
half spec FR-009 asks for. `session_messages.knobs_override` is
**deliberately left unwired**, named here per CLAUDE.md's rule that "we'll
get to it" is not a reason and every disposition needs a stated blocker
and owner.

**Blocker:** no surface today asks for a PER-MESSAGE knob override
distinct from the per-session default. `/effort`'s text says the new
value "takes effect on the next message," but there is no mechanism (and
no spec) for it to apply to *only* that one message and then revert —
today it simply updates the session default going forward, same as the
tune panel. Wiring `knobs_override` needs a product decision about what a
one-message override means UX-wise before it needs a Go reader; guessing
at a mechanism here would be inventing a UX nobody asked for, the same
class of mistake CLAUDE.md's "spec it and finish it" guidance warns
against for a half-built feature.

**Owner:** alec — whichever mission specs a per-message knob override.
Re-check at the release after model-settings-reach-the-model-01PMZ101
UNIT-6 merges. See `core/session/migrations_knobs.go`'s doc comment for
the same note kept with the column.

### 2026-09-12 (model-settings-reach-the-model-01PMZ101 UNIT-6 / WP11) · `LLM_TestProviderKey` has no `.vue` caller; the interface doc named the wrong substitute

`core/rpc/views/llm/api.go`'s `TestProviderKey` (bound as `LLM_TestProviderKey`
in `core/rpc/bindings.go`) is a read-only pre-submit key probe with exactly
one real implementation arm (`azure-openai`, via the `azureTester` duck-typed
interface in `impl.go`) — every other provider kind falls through to
`"no adapter registered for provider kind %s"` or a similar stub result. A
case-insensitive grep for `testProviderKey` over `frontend/src` finds it only
in `types.ts` and `harnessClient.ts`; **no `.vue` file calls it.**

The interface doc used to compound this by naming the wrong substitute:
"others are stubs for now" implied more kinds were coming, and nothing
pointed at what the AddProvider form actually does instead. Corrected here
(spec C-1): `AddProviderForm.vue:344`'s pre-submit connection-status check
calls `client.llm.listModels(form.kind, form.apiKey)`, **not**
`TestProviderKey` and not `TestAndRotateKey` either (`TestAndRotateKey`
exists because it's the one that WRITES to the keychain, per this same
interface's adjacent doc comment — a real, different reason to exist,
which is why this is not simply "delete the rival").

**Class: reachable-but-unconsumed surface, not rival infrastructure.**
`listModels` and `TestProviderKey` do different jobs (list vs. probe-one-key);
the AddProviderForm using `listModels` for its pre-submit check does not
make `TestProviderKey` a duplicate of it. Register A-0 (model-settings-
reach-the-model-01PMZ101) froze the delete lane for exactly this
distinction: "a commit whose only justification for a removal is absence
of callers is rejected at review." **Nothing is deleted** — not the impl
arm, not `azureTestKeyResult`, not the interface method, not the
`LLM_TestProviderKey` binding, not `harnessClient.ts`'s `testProviderKey`
wrapper, not `types.ts`'s `ProviderKeyTestResult`.

**Blocker:** no surface today asks for a non-writing pre-submit key probe
separate from `listModels`'s implicit one (a failed `listModels` call
already tells the AddProvider form the key/host combination doesn't work).
**Owner:** alec — wire a caller if a future AddProviderForm redesign wants
a probe that doesn't also fetch the model list, or drop this entry to
"delete: no producer, no consumer, unreachable" if the answer is settled
as "never." Re-check at the next unwired sweep touching `core/rpc/views/llm`.
### 2026-09-12 (`automation-actually-runs-01PMZ404` UNIT-17, G-1a widening) · seven interfaces outside this mission's scope, newly surfaced by the widened check-seam-implementers.sh

UNIT-17 widened `scripts/ci/cmd/checkseams` from ONE FILE IN ONE PACKAGE
(`core/agentgraph/seams.go`) to a DERIVED input set: every exported
interface under `core/` that is the type of a field on an exported
`*Config`/`*Options`/`*Deps` struct. Running the widened gate against
the tree immediately surfaced seven more unimplemented interfaces —
this mission's own seven findings (`ArtifactsReadWriter`, `ToolCaller`,
`NetworkAuthorizer`, `corewf.AuditEmitter`, `slashcmd.ToolDispatcher`,
`catalog.RecipeRegistry`, `wfsched.Dispatcher`) are all now wired and
do not appear in this list; these seven are in completely unrelated
subsystems and are out of scope for this mission to fix. Each carries
its own `wiring:deferred(...)` directive at its declaration (dated
2026-09-12) rather than being fixed here — A-0-style: the gate stays
required and does not silently pass on these, but fixing them is a
separate, unscoped body of work.

- **`storage.SecretsBackend`** (`core/storage/storage.go:26`) — zero
  non-test implementers of `Resolve()`. The field's own doc names the
  blocker: `TODO(secrets-keychain mission): switch to
  core/secrets.Backend`.
- **`llm.BundleSource`** (`core/rpc/views/llm/impl.go`) — zero
  non-test implementers of `BundleProfiles()`.
- **`llm.CredPeeker`** (same file) — zero non-test implementers of
  `PeekCred()`.
- **`llm.CredentialInvalidator`** (same file) — zero non-test
  implementers of `InvalidateCred()`, **contrary to its own doc
  comment**, which claims "the rpc wiring passes a thin adapter over
  secrets.Resolver.Invalidate." No such adapter exists anywhere in the
  tree — a docstring-describes-nothing finding, not just an unwired
  dependency.
- **`llm.AuditEmitter`** (same file, distinct from `corewf.AuditEmitter`
  this mission wired) — zero non-test implementers of `EmitRotated()`,
  **contrary to its own doc comment**'s claim of "a concrete
  `*audit.Emitter` (or equivalent)" adapter. None exists.
- **`hooks.MCPInvoker`** (`core/hooks/runner.go:211`) — zero non-test
  implementers of `InvokeTool()`. Its own doc comment already
  documents this as deliberate: *"v1 implementations are stubs."*
- **`memory.JournalSource`** (`core/rpc/views/memory/impl.go:59`) —
  zero non-test implementers of `JournalSnapshot()`, **contrary to its
  own doc comment**'s claim that "the kernel's HookManager satisfies
  this." `agentgraph.HookManager` has no such method.
- **`subagentdispatch.TasksRegistry`** (`core/tools/subagentdispatch/
  tool.go:143`) — zero non-test implementers of `Register()`/
  `Cancel()`. Its doc comment frames this as blocked on
  "the not-yet-merged mission" (background-task-monitor-01KZNP3C) —
  that mission has since merged (this ledger's own 2026-08-14 entry
  records the background-task subsystem, now producerless for a
  different reason), so the comment is stale, but the interface itself
  is still genuinely unimplemented.

Three of the seven (`CredentialInvalidator`, `AuditEmitter`,
`JournalSource`) are a SECOND finding stacked on the first: not just an
unwired dependency, but a doc comment actively describing a production
adapter that does not exist — the exact "comment is itself a lie" shape
CLAUDE.md's unwired-sweep doctrine calls out.

**Owner:** whoever next touches each respective subsystem
(secrets-keychain for `SecretsBackend`; the llm view / provider-
keychain-rotation for the four `llm.*` interfaces; hooks for
`MCPInvoker`; the memory view for `JournalSource`; subagent dispatch /
background-task-monitor for `TasksRegistry`). **Blocker:** none of
these has a scoped mission as of this date. **Date:** 2026-09-12.

### 2026-09-12 (`automation-actually-runs-01PMZ404` UNIT-15, PARTIAL) · `elicitview.API.OpenWizard` still has zero non-test callers — the deferred-ask leg landed, the wizard leg did not

UNIT-15 was scoped as three pieces the mission's own tasks.md and the
ledger's prior entry (`docs/unwired-ledger.md:592-623`, dated 2026-08-19)
both insist ship together, because any one alone is "a half-surface that
reads, in a code review, like a shipped feature":

1. `mode` on `AskArgs`, plumbed to a real producer — **DONE.**
   `core/tools/askuserquestion/askuserquestion.go` now declares
   `AskArgs.Mode` (`"blocking"` default / `"deferred"`), advertised in
   the tool's own JSON schema so the model can actually request it.
   `askuserquestion.Delegate` gained `Defer(ctx, q) (askID string, err
   error)`; `core/rpc/views/elicit/api.go`'s `*API` (the production
   Delegate, wired at `core/rpc/builtins_wiring.go:305-312`) implements
   it by calling `elicitation.Registry.Register` — never `Park` — so
   the call never blocks. `Tool.Call` branches on `args.deferredMode()`
   before ever reaching `OpenDialog`.
2. **Mounting `DeferredAskPill` / `DeferredAskPanel`** — **DONE.**
   `frontend/src/components/chat/SessionHeader.vue` now mounts
   `DeferredAskPill :session-id="session.id"`, the same "chat-header
   chip" pattern `BackgroundTaskChip` already uses on the line above
   it. `DeferredAskPill.vue` gained an optional `sessionId` prop that
   filters `elicit:deferred`'s process-wide broker payloads to the
   mounted session — without it, a pill on session A's header would
   have shown session B's pending questions, since the topic carries no
   inherent scoping of its own. Mounting was safe to do the moment (1)
   gave the topics a real producer, per the ledger's original
   objection.
3. **`OpenWizard`'s missing call site** — **NOT DONE.** Still zero
   non-test callers (`grep -rn "OpenWizard(" core/` — only the
   declaration at `core/rpc/views/elicit/api.go:393` and
   `api_test.go`'s four call sites). Closing this needs two things
   neither of which exists yet: (a) a model-facing way to submit a
   *batch* of questions — `AskArgs` has no `questions:` field, so there
   is no tool call shape that could reach `OpenWizard` even in
   principle; (b) a wizard renderer in
   `frontend/src/components/dialogs/AskUserQuestion/AskUserQuestion.vue`
   — confirmed absent: `grep -in wizard` on that file is zero hits;
   its two hits for "questions" (singular-vs-plural key-generation
   comments) are unrelated to a multi-question batch. Building either
   alone is a bigger, separately-reviewable
   change than the deferred-mode leg above; scoping both into the same
   commit as (1)+(2) would have meant shipping neither well or shipping
   the deferred leg late.

This does **not** repeat the half-surface failure the prior entry
warned about: (1) and (2) together are a complete, real capability on
their own terms (a model can defer a question; a human sees it and
answers it; nothing about that path implies or advertises a wizard).
`OpenWizard` remains exactly as before — built, zero callers, no new
lie created by leaving it that way.

**Owner:** whoever next extends `askuserquestion` with a multi-question
batch shape. **Blocker:** no tool schema exists for a question batch,
and no wizard UI exists to render one — both need to land together,
which is a second unit of comparable size to the one this entry closes.
**Date:** 2026-09-12.

### 2026-09-12 (`automation-actually-runs-01PMZ404` UNIT-11) · `WorkflowRunsSection.vue`'s `workflow-run:focus` emit has zero listeners — dated-justified, not deleted

UNIT-11 wired the OTHER half of this finding (`WorkflowsView.vue` now reads
`?run=<id>` via `useRoute()` and forwards it to `RunsHistoryTab.vue`, which
expands and scrolls to the named run — see `spec.md` §1.10 / §5.11). The
`router.push({ path: '/workflows', query: { run: run.runId } })` call in
`WorkflowRunsSection.vue`'s `onRowClick` is no longer a "hash-style hint …
harmless if ignored"; it genuinely navigates.

The sibling `emit('workflow-run:focus', run.runId)` two lines above it,
declared at `WorkflowRunsSection.vue:33`, still has **zero listeners** —
the sole mount site, `frontend/src/shell/LeftRail.vue:964`, is bare
(`<WorkflowRunsSection />`, no `@workflow-run:focus`). A-0 forbids
resolving that by deleting the emit.

**Why this is left dated-justified rather than wired to a listener:**
`LeftRail.vue` has no in-place surface of its own that a "run got
focused" event could sensibly drive — it is the persistent left rail,
not a panel that shows run detail. `/workflows?run=<id>` (the query
param path) already *is* the surface that shows run detail, and the
`router.push` two lines below the emit already reaches it. A listener on
`LeftRail.vue` would have nothing to do except duplicate that navigation,
which is not a second capability, just a second name for the same one.

**Owner:** whoever next redesigns the left rail's workflow-runs panel
into something with its own in-place detail view (at which point the
emit would have a real consumer). **Blocker:** no such redesign is
scoped or planned. **Date:** 2026-09-12.

### 2026-09-12 (`automation-actually-runs-01PMZ404` UNIT-16) · five inherited closing-sweep findings, dispositioned

`docs/dead-code-audit-2026-08-18.md:1796` assigned five findings to this
mission. A-0 forbids resolving any of them by deletion.

- **`C2V-14` `contextBootstrap.resume`** (audit `:1401`) — re-checked
  2026-09-12: `docs/dead-code-audit-2026-08-18.md`'s own body entry
  already recorded this as backend-live/UI-missing with no scoped mission
  claiming the UI mount. Nothing new to add; still open.
  `justify(blocker: "no mission has scoped the UI mount", owner: alec,
  date: 2026-09-12)`.
- **`C2V-35` `Tasks_AbortBySession` / `ListBySession`** (audit `:1419`) —
  filed under the audit's own "a named live substitute exists (delete)"
  bucket, but no substitute is named for these two, and the background-
  task subsystem is already recorded elsewhere in this ledger as
  producerless. A-0 names it explicitly:
  `justify(blocker: "background-task subsystem has no producer", owner:
  alec, date: 2026-09-12)`.
- **`C2V-01`'s handoff-share prerequisite** (audit `:1397`) — this
  ledger already records (see the handoff entries elsewhere in this
  file) that `Handoff_Share` sends a nil payload, so wiring
  `Handoff_Accept` alone would open an EMPTY session. Recording the
  prerequisite here per UNIT-16's obligation; `automation-actually-runs`
  does not own the handoff subsystem and does not wire `Accept`.
- **`C2V-08`, `C2V-30` — NOT CARRIED.** Per spec.md §1.11 X-12, both
  appear **only** in `docs/dead-code-audit-2026-08-18.md:1796`'s
  assignment table — neither has a body entry anywhere else in that
  file. Escalated as E-007 (spec.md §14): either the audit's author
  supplies the finding text, or these two are struck from this
  mission's inventory. **Nobody has acted on them** — inventing a
  defect to match a label is exactly the failure mode
  `feedback_verify_agent_citations` exists to prevent. **Owner:** the
  `docs/dead-code-audit-2026-08-18.md` author. **Date:** 2026-09-12.

### 2026-09-11 (finding #61 round-2 review, `fix/memory-persist-growth-and-latency-v2`) · served-mode exit never calls `core.Core.Shutdown(ctx)` — only `api.Shutdown()` does

Round 2 of the finding #61 follow-up (Blocker 3: wiring `rpc.API.Shutdown()`
into real process exit) traced both served-mode entry points —
`runServeMode` in `main.go` and `cmd/harness-served/main.go` — end to end.
Both now call `api.Shutdown()` after `srv.Serve(ctx)` returns (that wiring
is correct and covered by this same commit's shutdown-deadline fix). Neither
one calls `core.Core.Shutdown(ctx)` anywhere. The desktop path
(`main.go`'s `OnShutdown` callback) calls both — `api.Shutdown()` then
`_ = c.Shutdown(ctx)` — so this is a served-mode-only gap, not a repeat of
Blocker 3 itself.

Practical effect: on served-mode exit (SIGTERM/SIGINT via
`installServeShutdownSignal`, or a real server error from `Serve`), whatever
`core.Core.Shutdown` closes — storage, MCP client connections, telemetry —
never closes. `rpc.API.Shutdown()` only reaches what the `API` struct
touches directly (hook runner, prune/compaction schedulers, fleet/audit
background pollers, etc.); `Core` is a separate type the `API` merely holds
a reference to, per `main.go`'s own comment at the `OnShutdown` call site
("`c.Shutdown` (`core.Core.Shutdown`, a different type)"). Not a data-loss
bug on its own — the OS process exiting reclaims file handles and network
connections regardless — but it means served-mode quit skips whatever
graceful-close behavior `Core.Shutdown` is meant to provide (e.g. any
buffered telemetry flush, orderly MCP disconnect), silently, on every
served-mode process exit.

Explicitly out of scope for the branch that found it: the round-2 brief for
`fix/memory-persist-growth-and-latency-v2` scoped that branch to the
async-pool shutdown-deadline fix only and named this finding as a
do-not-fix-here discovery to record.

**Owner:** whoever next touches served-mode shutdown wiring (natural
pairing with any future `runServeMode/cmd/harness-served` shutdown-sequence
work — the two call sites already have a "both served entry points must
agree" convention per their own comments, so a fix should touch both files
together). **Blocker:** no active mission currently owns served-mode
shutdown sequencing; needs a decision on whether `core.Core.Shutdown(ctx)`
should run before or after `api.Shutdown()` in served mode (the desktop path
runs it after, per `main.go`'s ordering, for the reason documented there:
`API` fields must still be able to reach `Core`'s live storage/MCP/Events
while they're being drained). **Date:** 2026-09-11.

### 2026-09-10 (ledger #46) · `post_send` never fired — WIRED. `pre_send` was ALSO dead, not just the "one that works" — CORRECTED

**Disposition: WIRED (post_send), CORRECTED (pre_send's status), DEFERRED with named blocker (pre_send/user_prompt_submit/notification/pre_save_session/post_assistant_turn_complete's real producer work).**

Ledger item #46 ("post_send hooks never fire — only 1 of 18 hook events
works, and memory.persist rides on it") verified accurate as *stated*, but
the "1 of 18" premise — `pre_send`, seeded 2026-08-19 by
`trust-surfaces-that-fire-01PMZ202` WP08/WP01 as the one confirmed-firing
event — was itself wrong, and had been wrong since before WP01 ran.
`core/rpc/views/llm/impl.go:638`'s `a.hooks.RunPreSend(...)` is real code,
but it lives inside `(a *API).buildMessages` (`impl.go:556`), a method with
**zero production callers** — `grep -rn '\.buildMessages(' core/
--include='*.go'` finds only `impl_test.go` / `integration_test.go`. The
live send path since the agent-kernel-graph-chat-migration cutover (commit
`f0b17126`, **2026-04-27** — four months before WP01's 2026-08-19
investigation) is `API.StartStream` → `ChatRunner.StartStream`
(`core/rpc/views/agentgraph/chat/chat_runner.go`), which never imports
`core/hooks`. `memory.retrieve` (registered on `pre_send`) has therefore
never run in a shipped build either — the identical defect class as
`post_send`/`memory.persist`, just not fixed in this same change (see
below). The gate that certified `pre_send` as firing
(`scripts/ci/check-hook-event-fire-sites.sh` leg (a)) was a **syntactic**
grep for `.RunPreSend(` outside test files — it could not distinguish a
real call site from one buried in dead code, so it passed on a false
positive for three-plus weeks. **CLOSED (same PR, review-nit follow-up,
2026-09-10):** leg (a) now adds a one-hop reachability check — it
resolves the function ENCLOSING each textual match and requires that
function to have at least one non-test caller anywhere in `core/`,
reproducing exactly the `buildMessages` shape found here. Planted-
violation proof:
`TestHookEventFireSitesGate_PlantedDeadEnclosingFunctionFires`
(`scripts/ci/gates_can_fail_test.go`) registers a fake event with its only
fire site inside a zero-caller function and confirms the new gate rejects
it while the pre-fix gate passes it — the exact regression this entry
describes. This is deliberately ONE hop, not full call-graph reachability
— see the gate's own header comment and `one_hop_reachable()` for what it
still cannot see (a live caller that is itself unreachable at hop two;
indirect invocation through an interface, stored closure, or reflection;
a same-named method on an unrelated receiver miscounted as a caller).
Tree-wide run after the widening: 0 events flagged, 0 false positives
among the 7 currently-firing events (`post_send`, `pre_tool_use`,
`post_tool_use`, `post_tool_use_failure`, `permission_request`,
`permission_denied`, `session_start`) — each already has a reachable
production call site. Full transitive reachability (hop two and beyond)
remains open; this fix's own tests still substitute a real-path
integration proof for the one event they cover, which is the stronger
guarantee CLAUDE.md's testing-rule-3 doctrine asks for and which no
static gate can fully replace.

**What shipped:** `post_send` now fires from the real path.
`ChatRunner.Config.PostSendHook` (new field, `chat_runner.go`) is
registered on the SAME `HookPostLLM` boundary `UsageHook` already uses —
after `exec_state.go`'s `sessionWriteExecutor` persists the assistant
message — and reads `FinishReason`/`ProviderKind`/`ModelID` via
`turnJournal.LookupCandidateUsage` (content-matched against the persisted
text), not `LLMProviderAdapter.LastResponse()`, for the same reason the
usage hook already had to switch: on a routed graph `LastResponse()` is
`exit_gate`'s own always-runs-last verdict call, not the turn that
produced the text being persisted — a bug this fix would have introduced
if it had copied the usage hook's *pre*-fix shape instead of its current
one. `core/rpc/api.go`'s `buildChatRunner` gained a `hooksRunner
llm.HookRunner` parameter (threaded from `newLLMStack`, which already held
it) and a `postSendHookFn` closure calling the existing
`hooksRunnerAdapter.RunPostSend` — no new dispatch logic, only a live call
site. `memory.persist` (`core/hooks/memory_builtins.go:191`) now runs in a
shipped build for the first time — this is the behaviour-change warning
the mission's own WP12 plan already carried (D-9, spec.md §12): every user
with the starter memory hooks installed gets `post_send` behaviour they
have never observed.

**Honesty-floor correction (`frontend/src/lib/hooks.ts`):**
`FIRING_HOOK_EVENTS` swapped `pre_send` out and `post_send` in (same
position — index 0, "chat" family). `scripts/ci/allowlists/
i17-eventless-hook-events.txt` gained a dated `pre_send` row and lost the
`post_send` row. Rippled into `HookEditor.vue`'s `blankHook()` default and
`HooksPanel.spec.ts`'s fixtures/exemplars (the "does this event fire"
honesty-floor tests had `post_send` as the negative exemplar and
`pre_send`/`FAKE_HOOK` as the positive one — inverted to match reality).

**Tests:** `core/rpc/views/agentgraph/chat/post_send_hook_integration_test.go`
— `TestChatRunner_PostSendHook_FiresOnRealPath` (real `StartStream`, a
race-safe recorder asserts one call with the real session id / user turn /
assistant turn / finish reason) and
`TestPostSendHook_MemoryPersist_WritesRealRow` (real `hooks.Runner` + real
`hooks.Registry` + a real saved `post_send` hook wired to `memory.persist`,
backed by a REAL `corememory.NewChromemStore` on-disk file — `core/memory`
has no sqlite backend, so this is that subsystem's equivalent of CLAUDE.md
blind spot #2's "must drive real sqlite": the assertion re-opens a FRESH
store instance against the same path rather than reading back through the
original in-process `Store`, proving the write survived a real gob
encode → rename → decode round trip). Both pass under `-race`. Mutation
proof: gating the `chat_runner.go` registration behind `if false &&
r.cfg.PostSendHook != nil` (reproducing "never registered") turned both
tests red (`PostSendHook fired 0 times, want 1`; `len(chunks) = 0, want
1`); reverting turned them green again.

**Blocker / owner — the four still-dead v1/v2 events:**
`user_prompt_submit`, `notification`, `pre_save_session`,
`post_assistant_turn_complete` remain unbuilt — see their (corrected)
rows in `i17-eventless-hook-events.txt`; two of the four rows previously
cited `buildMessages`/`impl.go:892 StartStream` as live seats, which this
finding shows was never true, so those rows were corrected to point at
`ChatRunner.StartStream` instead. Wiring `pre_send` for real needs a
genuinely bigger change than `post_send`'s did: `post_send` is a
post-hoc side effect (fire-and-forget after the message is already
persisted, trivially hung off the existing `HookPostLLM` boundary);
`pre_send` must MUTATE the outbound message list BEFORE the LLM call is
built, and `ChatRunner` has no pre-LLM injection point today
(`HookPreLLM` exists as a boundary constant in `core/agentgraph/hooks.go`
but has zero `Fire` call sites of its own). **Owner: alec — follow-up
WP**, scoped separately from this fix (dated 2026-09-10, same as the
corrected allowlist rows).

The gate's syntactic-only leg (a) — the part of this entry that used to
say "neither is fixed here" — **was** closed in the same PR as a
review-nit follow-up (see the "CLOSED (same PR...)" note above): leg (a)
now requires the enclosing function of each matched fire site to have a
non-test caller (one hop), with
`TestHookEventFireSitesGate_PlantedDeadEnclosingFunctionFires` as the
planted-violation proof.

What remains open, stated with the same specificity the four dead events
above get, because a generic "remains open" is the shape this entry exists
to correct:

- **Name collision — the one that still bites.** `one_hop_reachable` finds
  callers textually (`.Name(` / `Name(`). Go does not require method names
  to be unique across receivers, so a dead fire site inside a method whose
  name is shared with ANY called method elsewhere in `core/` still reads as
  reachable. Reproduced 2026-09-10 by the review: a plant inside
  `(d *zzCollisionProbeDead) Validate()` — colliding with ~66 `Validate()`
  declarations, 29 with real call sites — passes BOTH the pre-fix and the
  one-hop gate. For that class this commit buys nothing. It does not affect
  the 7 currently-firing events: each resolves through a distinctively named
  enclosing method (`RunPostSend`, `FirePreToolUse`, `FirePostToolUse`,
  `FirePermissionRequest`, `FirePermissionDenied`, `FireSessionStart`), each
  directly verified to have a real production caller — no verdict rests on
  an incidental match. **Blocker:** disambiguating requires correlating the
  caller-side receiver's static type, which grep/awk cannot do reliably
  (aliasing, embedding, interface satisfaction); it is a Go/packages job of
  the same order as the hop-two work, not a regex tweak. **Owner: alec.
  Date: 2026-09-10.**
- Hop two (a caller that is itself dead), and invocation through an
  interface, a stored closure, or reflection. Same blocker, same owner.

Closures nested one level inside a named function are NOT affected: the
awk "last `^func ` at column 0" heuristic attributes them to the enclosing
declaration, which is why `post_send`'s fire site (inside the
`postSendHookFn` closure) correctly resolves to `buildChatRunner`, called
at `core/rpc/api.go:5475`. That is a structural property of Go syntax, not
a coincidence of this case.

### 2026-09-09 (vm-execution-surface-truth-01PMZD14 WP05) · `approvalGateFrom` — HV-01, no `PromptSurface` variant for a model call

`cmd/harness-vm/approvalgate.go`'s `approvalGateFrom` is the one function a
task-path call site would use to read the run's approval gate and park on
`RequestInteractive`. It has zero non-test callers — the four hits are all
in `approvalgate_test.go`, which supplies its own executor and calls it
directly, so coverage is real and proves nothing about reachability. The
writer half of the seam (`withApprovalGate`, `cmd/harness-vm/main.go`) is
wired; only the reader has no caller. Superseded the broader
2026-08-20 UNIT-2/UNIT-4 scope-cut entry this mission's WP01 filed — UNIT-2
(WP04 + WP05) has now landed: `contracts/vm-rpc.md`'s self-contradiction is
corrected, the grant is self-describing, and this is the dated justification
that correction promised.

- **Blocker:** no `cedar.PromptSurface` variant exists for a model call.
  `core/policy/cedar/prompt.go`'s `PromptSurface` is a closed four-variant
  union (`Bash`/`FS`/`Cred`/`Tool`), *"Exactly one MUST be non-nil"*, enforced
  by `Family()` which returns empty for zero or multiple variants. Adding a
  fifth variant means a new family, the host modal that renders it, and a
  wire payload change (`vm-execution-surface-truth-01PMZD14/spec.md` R-2) —
  a product feature with a product owner, not a wiring fix. The in-VM task
  graph (`plan` → `run`) also has no tool dispatch of its own to raise
  anything through.
- **Owner:** alecfeeman.
- **Date:** 2026-09-09.

### 2026-09-09 (vm-execution-surface-truth-01PMZD14 WP05) · `approvalBridge.runStatus` — HV-05, the wire has no status field and the contract forbids adding one

`cmd/harness-vm/approvalgate.go`'s `approvalBridge.runStatus()` derives
`waiting_for_input` / `running` from approval state. It has zero non-test
callers — six hits, all in `approvalgate_test.go`. Superseded the broader
2026-08-20 UNIT-2/UNIT-4 scope-cut entry this mission's WP01 filed, now that
UNIT-2 (WP04 + WP05) has landed.

- **Blocker:** the wire has no status field, and `contracts/vm-rpc.md`'s
  "Run status is DERIVED, not a wire field" explicitly forbids adding one —
  the host already computes the same value independently from the
  `task.approval_requested` / `task.approval_resolved` event pair. The named
  future consumer is the deferred `agent_feed.*` push stream
  (`contracts/vm-rpc.md`'s "Deferred (not in this surface)" list).
- **Owner:** alecfeeman.
- **Date:** 2026-09-09.

### 2026-09-10 · `llm.Response.Reasoning` has zero writers and zero readers — `model-settings-reach-the-model-01PMZ101` WP09

Found while wiring UNIT-5 (WP09 + WP16): Bedrock and Gemini did not emit
`llm.StreamReasoning` at all — the user paid for reasoning tokens on those
two providers and saw nothing, live or on reload, matching Anthropic's
pre-existing gap on the other two providers still open at the time
(`docs/unwired-ledger.md` earlier entries; closed for Anthropic only).
WP09 added the missing delta-decode arms in `core/llm/bedrock/bedrock.go`,
`core/llm/bedrock/bearer.go`, `core/llm/gemini/wire.go` and
`core/llm/gemini/adapter.go` so all three providers now emit
`llm.StreamReasoning` during streaming.

`llm.Response.Reasoning` (`core/llm/llm.go:723`,
`Reasoning []ReasoningBlock`) is a different thing: the **terminal,
non-streaming** response struct's reasoning field. It has zero
production writers repo-wide (no adapter — including the newly-wired
Bedrock/Gemini paths and the pre-existing Anthropic path — ever assigns
`Response.Reasoning`; every adapter accumulates reasoning only on the
`StreamEvent`/`StreamReasoning` side, matching how `Response.Content`
and `Response.ToolCalls` are accumulated from stream events by the
*caller*, not the adapter) and zero readers.

**Not deleted.** Register A-0 (this release's delete-lane freeze, spec
D-11) is explicit: "a commit whose body's only justification for a
removal is absence of callers is rejected at review" — which is exactly
this field's only justification. WP09 instead:

1. Amended the field's docstring (`core/llm/llm.go`) to name
   `StreamEvent.Reasoning` as the live carrier, so a future reader does
   not mistake `Response.Reasoning` for the place reasoning content
   accumulates.
2. Records this dated justification here, since no existing gate class
   in `scripts/ci/allowlists/` covers "exported struct field with zero
   writers and zero readers" — I10 is scoped to
   `Evaluate*/Enforce*/Authorize*/Check*/Verify*/Guard*/Permit*/*Gate`
   control-flow symbols (`scripts/ci/allowlists/i10-unwired-gates.txt`),
   I16 is scoped to agentgraph manifest output ports
   (`scripts/ci/allowlists/i16-declared-unwritten-output-ports.txt`),
   and the other allowlists are similarly domain-specific. This finding
   has no allowlist to live in, so per CLAUDE.md ("Where the ledger
   lives") it stays here in the ungated half.

**Blocker:** wiring a non-streaming consumer of `Response.Reasoning`
would mean either (a) accumulating `StreamReasoning` events into
`Response.Reasoning` at the adapter layer — a second accumulation path
alongside the kernel-side accumulation `llm_provider_adapter.go` and
`stream_bridge.go` already do for the UI, which is exactly the "rival
infrastructure" class CLAUDE.md's delete-vs-finish section says to avoid
— or (b) a genuinely new non-streaming `Generate()` consumer that has no
product owner today. Neither is UNIT-5's scope (spec: WP16 is the
render-path fix; both are P1, coupled, and explicitly must not widen to
a third mechanism). **Owner:** alec — next reasoning-related mission
should re-examine whether `Response.Reasoning` should be populated from
the same `StreamReasoning` accumulation the kernel already performs, or
deleted once that accumulation has run for a full release with zero
regressions reported. **Date:** 2026-09-10.

### 2026-08-23 · WP20 (UNIT-15) — frontend props and exports with no consumer, five NARROWed after wiring the two that had a source

`controls-and-readouts-that-tell-the-truth-01PMZ808` WP20. Spec §1.15 named
nine findings grouped by class (declared prop no parent passes / exported
symbol no non-test reader). Two wired in the same commit
(`EVENT_FAMILY` → grouped `<optgroup>`s in `HookEditor.vue`;
`HookDryRunDrawer.vue` now renders `output.permissionDecision` and
`output.watchPaths`, which the Go mapper had always sent). The remaining
seven, all NARROWed or left as documented no-ops:

- **`SlashArgFill.prefilled`** (`components/chat/SlashArgFill.vue`) — no
  caller passes it; `SessionsView.vue` captures typed args into a raw
  string nobody tokenizes into `Record<string,string>`. **Blocker:** no
  slash-arg tokenizer exists anywhere in `frontend/src` (spec D-10 — do
  not invent one in this WP).
- **`SlashCommandEditor.readOnly`** (`views/settings/SlashCommandEditor.vue`)
  — never passed; `UserCommand` carries no builtin/managed/fleet-pushed
  marker to derive it from. **Blocker:** no such field exists on
  `UserCommand`.
- **`AttachmentTreePicker.attachmentKind`** (`components/contexts/`) —
  REFUTED as a defect (spec D-9). All three mounts are context-library
  surfaces, the folder-attach branch takes no kind, and `'system'` is
  semantically correct. Prop doc narrowed to stop implying an override
  path exists; no functional change.
- **`CANONICAL_RECIPE_CATEGORIES`** (`lib/recipeCategories.ts`) —
  downgraded to an internal-duplication note (spec D-9): identical, and
  in identical order, to `Object.keys(RECIPE_CATEGORY_META)`, which
  `isCanonicalCategory` already treats as authoritative. Not a false
  advertisement (no badge renders from it directly); documented as a
  drift risk rather than rewritten, to avoid an unforced behavioural
  change in a P3 item.
- **`DocumentChip.pageCount`, `.sizeLabel`'s `size_bytes` branch, and
  `ImageBlock.dimensionTooltip`'s `image_dimensions`** — the five-hop
  wire spec §1.15 describes (`core/attachments/media.go` →
  `core/llm.MediaSource` → `ChatInput.vue`'s block builder → the two
  render components) needs a field added to `core/llm.MediaSource`.
  `core/llm/**` is out of scope for this WP's dispatch (sibling missions
  own it this wave). All three readouts degrade gracefully today (no
  page count / size / dimension shown, not a wrong one) — documented as
  dead-but-honest rather than wired. **Blocker:** `core/llm.MediaSource`
  needs a `PageCount` field, and the frontend staging path
  (`ChatInput.vue`) needs to populate `size_bytes` and
  `image_dimensions` on send — cross-package work spanning a directory
  this WP does not own.
- **`MessageList.scrollToBottom` / `ConfirmToolModal.reconcile`**
  (`defineExpose`, test-only callers) — spec §1.15 confirms this is a
  house pattern (`FilesystemPermissionModal.vue`,
  `BashPermissionModal.vue` do the same), both methods are fully used
  internally, and neither component claims the expose is for external
  consumption. No false claim exists to narrow; left unchanged.
- **`HealthPill.label` / `.compact`** (`views/tools/HealthPill.vue`) —
  never passed; both have declared defaults so behaviour is defined
  (dead knobs, not a false readout — spec's own lowest-severity call).
  **Not edited in this WP**: `frontend/src/views/tools/**` is out of
  scope for this WP's dispatch (owned by a sibling agent this wave).
  Recorded here so the next pass over that directory has the finding on
  hand rather than rediscovering it.

**Owner:** alec. **Blocker:** see each item above (tokenizer / UserCommand
field / core/llm.MediaSource field + frontend staging / directory
ownership this wave). **Date:** 2026-08-23.
### 2026-08-23 · `Sanitizer.SanitizeStream` has zero production callers — streamed assistant tokens are not redacted (PR #308 secret-reference regression fix)

`core/credstore/refs/sanitizer.go` docstrings previously claimed "Every
tool result content block AND every streamed assistant token is scanned
via Sanitize" (FR-007 of `model-secret-references-01KW7M5A`, which does
require both). That was never true for the streamed-token half:
`SanitizeStream` (`sanitizer.go:131`, now with a corrected docstring) is a
thin `Sanitize` alias with no reader/writer chunk-boundary handling
despite an earlier comment describing one, and
`/usr/bin/grep -rn 'SanitizeStream' --include='*.go' core/` minus
`_test.go` returns only the declaration — no call site anywhere in the
LLM streaming bridge (`core/rpc/views/llm/impl.go`).

Net effect (**corrected 2026-08-25** — see the dated entry below; this
paragraph originally overclaimed): `Sanitize` runs on every SUCCESS-path
tool result the three production `Resolver.Substitute` callers produce
(bash, web_fetch, and — as of this fix — the MCP stdio transport), so a
secret echoed back *inside a successful tool result* is redacted before
it reaches session history or the UI. Two v0.72.0 regressions meant
error-path and background-mode results were NOT covered until fixed on
2026-08-25 — see below. A secret the **model itself** types directly
into its own streamed response text (as opposed to relaying it through a
tool result) is still NOT caught by anything today.

**Not fixed here** — this entry was found while closing the MCP
tool-result sanitization gap (`core/mcp/transport/stdio/server.go`
`CallTool`), which was the actual security regression in scope. Wiring
`SanitizeStream` into the assistant-token streaming path is a distinct
change touching `core/rpc/views/llm/impl.go`'s stream loop and needs its
own persisted-history test (per CLAUDE.md's testing-rule-3 discipline —
a test that calls `SanitizeStream` directly proves nothing).

**Owner:** alec. **Blocker:** no mission currently owns wiring
`SanitizeStream` into the LLM streaming bridge; the next mission that
touches assistant-token streaming should pick this up. **Date:** 2026-08-23.

### 2026-08-25 · MCP stdio has THREE unsanitized channels, not one (review round 5 correction)

An earlier entry conceded only the stdio **`RPCError.Message`** path. Round 5
established that two sibling channels carry the same taint and were not
covered by that wording:

1. **Child stderr → `RingBuffer` → `StderrTail` → `RecipeStatus` → RPC/UI**
   (`core/mcp/transport/stdio/connection.go:307`, `server.go:409/882`,
   `status.go:30/53`). No sanitizer anywhere on this path, and it is read
   *outside* any turn context. Can also be promoted into `LastError`.
2. **`notifications/message` → `LogSink.Handle` → `slog.Default()`**
   (`core/mcp/transport/log.go:65-68`, `server.go:871`). Production wiring is
   `Logger: nil // defaults to slog.Default` (`core/rpc/api.go:5134`), so
   server-controlled `params.Data` reaches `harness.log` — the same durable
   sink as the seventh egress.

**Why this is accepted rather than fixed:** all three require a stdio MCP
server to echo back plaintext the harness *deliberately handed it* — that is
what `@secret:` substitution into MCP arguments means. Once a server holds the
plaintext it can exfiltrate over its own socket; redacting our logs does not
reduce attacker capability. The sanitizer here defends against *accidental*
echo by a buggy server, and the structurally identical `RPCError.Message`
case was already accepted. Recorded so the accepted scope is stated
accurately: three channels, not one.

**Owner:** alec. **Date:** 2026-08-25.

### 2026-08-25 · `refs.Sanitize` is exact-substring, so a transformed secret is caught by nothing

Raised by review round 5 as the one genuinely open design question after seven
egress fixes. `Sanitize` matches the resolved plaintext literally. A child
process that base64-encodes, URL-encodes, case-folds, or splits a secret before
echoing it defeats every sanitizer on every path — bash, webfetch, MCP stdio,
and the background task chokepoint alike.

This is a known property of the design, not a defect in any one call site, and
no further review of a single PR can close it. It wants a spec: either a
stronger matching primitive, or an explicit statement that redaction is
best-effort against accidental echo only.

**Owner:** alec. **Blocker:** needs a product decision on what redaction is
supposed to guarantee. **Date:** 2026-08-25.

### 2026-08-25 · web_fetch error-path and bash background-mode secret leaks (release/v0.72.0 WP19 regressions — FIXED)

Two confirmed secret-leak regressions, both introduced by WP19 wiring the
`@secret:` resolver on `release/v0.72.0` (before it, no plaintext existed
to leak). Both were independently reproduced by a reviewer and re-verified
by reading the code before the fix landed.

**LEAK 1 — `core/tools/webfetch/webfetch.go`.** The Sanitizer was only
applied to the success-path response body; every `errorResult(...)` return
(request-build failure, transport failure, response-read failure —
13 call sites, `/usr/bin/grep -c 'return errorResult' webfetch.go` at the
time of the fix) bypassed it. Go's `*url.Error` embeds the full request URL,
so any DNS/TLS/connection failure on a URL carrying a resolved `@secret:`
wrote the plaintext straight into the tool result, which is persisted as a
`tool_result` move and indexed into FTS
(`core/session/migrations_search_fts_tool_rows.go`) verbatim.

Fixed with a single chokepoint rather than enumerating call sites: `Call`
is now a thin wrapper that runs the (renamed) request logic in `call` and
sanitizes `call`'s return value — success or error — exactly once, in one
place, before it reaches the caller. A new `errorResult(...)` call added
anywhere inside `call` cannot reintroduce the leak; it would have to
bypass the `Call`/`call` split entirely.

**LEAK 2 — `core/tools/bash/background.go`.** `bash.go` resolves
`@secret:` references into `commandLine` and, for `run_in_background:
true`, passed that *resolved* string to `spawnBackground`, which used it
for both the actual `exec.Command` invocation AND for
`t.backgroundSpawn(...)` (→ `core/tasks/registry.go` → `store_sql.go`'s
`INSERT INTO tasks (..., cmd, ...)`, plaintext, never zeroed, never
expired) AND for the `bash.background.spawned` log line
(`logging.L()`, wired in production). The synchronous path was already
correct — it stores `args.Command` (pre-substitution). Fixed by splitting
`spawnBackground`'s single `commandLine` parameter into `execCommand`
(resolved, used only for `exec.Command`) and `logCommand` (unresolved,
used only for the registry write and the log line), matching the
synchronous path's contract.

**Correction (2026-08-25, round 3 — see the entry below):** the above
fixed only the *command-line* half of LEAK 2. `background.go` still
attached `cmd.Stdout`/`cmd.Stderr` directly to the task registry's
writers with no sanitizer anywhere — a resolved secret **echoed by the
child process itself** (e.g. `curl -v` printing a substituted
`Authorization` header to stderr) still reached the on-disk task log,
the in-memory ring buffer, the `background_task_complete` hook payload,
and the `kenaz__monitor` tool result, all in plaintext. This was a
distinct gap from the one fixed above, in a different code path
(`t.backgroundWriters`, not `t.backgroundSpawn`), and shipped past this
same review pass — see the round-3 entry for the fix and its scope.

Both fixes are covered by falsification tests
(`core/tools/webfetch/webfetch_test.go`
`TestWebFetch_ErrorResult_DoesNotLeakSecret`,
`core/tools/bash/secret_background_leak_test.go`
`TestRunInBackground_DoesNotPersistOrLogResolvedSecret`) that assert on
the actual marshalled/persisted bytes, not an in-memory intermediate, and
were confirmed red against the pre-fix code and red again under a
compiling mutation of the fix.

This is also the correction referenced in the 2026-08-23 entry above: its
claim that "`Sanitize` now runs on every tool result" was true only for
the success path at the time it was written; these two gaps existed
alongside it in the same release.

**Owner:** alec. **Date:** 2026-08-25.

### 2026-08-25 · LEAK 2 round 3 — bash background mode's OUTPUT path was still unsanitized (FIXED)

Round 2 (previous entry) fixed the background arm's *command line* —
`spawnBackground`'s persisted/logged string. It did not touch the arm's
*output*. `core/tools/bash/background.go` attached `cmd.Stdout`/
`cmd.Stderr` straight to `t.backgroundWriters(taskID)` — the task
registry's raw writers — with zero sanitizer anywhere in that file
(confirmed: `background.go` had no `credstore/refs` import at all). A
child process that echoes a resolved `@secret:` value (`curl -v` writing
a substituted `Authorization` header to stderr is the canonical case)
put the plaintext into all four sinks `core/tasks/registry.go` fans
each writer to: the on-disk log file (cleartext, mode 0600, 7-day
retention, survives process exit — `core/tasks/log.go`), the in-memory
ring buffer (`core/tasks/ring.go`), the `background_task_complete` hook
payload (piped to an arbitrary user-configured executable's stdin), and
the `kenaz__monitor` tool result (back into session history and FTS).
The identical *synchronous* `kenaz__bash` call was correctly redacted
(`bash.go:470-477`) — only the background arm was exposed.

**The hard part:** `refs.Sanitizer` is turn-scoped —
`chat_runner.go`'s `defer sanitizer.Clear()` zeroes it at end-of-turn —
but a background task outlives its turn. Wrapping the task's output
writers with the turn's shared `*refs.Sanitizer` would have redacted
correctly for a fast test and then silently stopped the moment the
turn ended, which is worse than shipping no fix at all: it looks green
in CI and leaks in production on any task whose output arrives after
its turn completes (which is the common case for anything that runs
longer than one turn).

**Fix (task-scoped sanitizer, chosen over refusing `@secret:` in
background mode because it preserves the capability):**

1. `refs.Sanitizer.Clone()` (new) returns an independent, deep-copied
   Sanitizer whose entries survive the original's `Clear()`.
2. `core/tasks.OutputSanitizer` (new minimal interface,
   `Sanitize([]byte) []byte`) lets `core/tasks` accept a sanitizer
   without importing `core/credstore/refs` — the same reasoning that
   already makes `bash.BackgroundSpawnFunc` a plain function type
   instead of an import of `core/tasks`. `*refs.Sanitizer` satisfies it
   structurally.
3. `core/rpc/builtins_wiring.go`'s `bgSpawn` closure calls
   `refs.SanitizerFromContext(ctx).Clone()` and passes the clone via the
   new `tasks.RegisterOpts.Sanitizer` field, **at `Register` time —
   which always runs before `cmd.Start()`** (background.go's existing
   ordering guarantee, originally added so the writers could be attached
   before the first byte was written; the same ordering now guarantees
   the sanitizer is attached before the first byte too).
4. `core/tasks/ring.go`'s `lineWriter.Write` — the single chokepoint all
   four sinks flow through (ring buffer, log file, and via
   `record`/`AppendLine`, both `__monitor`'s `Lines` and the hook's
   `Tail`-derived `StdoutTail`/`StderrTail`) — calls `Sanitize` on the
   raw bytes before any sink sees them. One chokepoint fix covers all
   four sinks by construction instead of requiring each to remember to
   sanitize independently.

**Covered:** on-disk log file, in-memory ring buffer,
`background_task_complete` hook payload, `kenaz__monitor` (drain and
watch, both read `Registry.Tail`/`AppendLine`, downstream of the same
chokepoint) — for every task spawned by the fixed code, including
tasks that are still running when their originating turn's `Clear()`
fires (falsification-tested explicitly, see below).

**NOT covered, dated + owned:**

- **Pre-fix on-disk log files.** Any `<taskID>.log` written before this
  fix deployed keeps its plaintext; nothing retroactively scrubs
  existing files. 7-day retention (`core/tasks/log.go`) ages them out
  naturally; no separate cleanup was written. **Owner:** alec.
  **Blocker:** none planned — retention is considered sufficient
  mitigation for a single-user desktop app; revisit only if a future
  finding shows log files are backed up or synced somewhere retention
  doesn't reach. **Date:** 2026-08-25.
- **Chunk-boundary splitting.** `lineWriter.Write` sanitizes each
  `cmd.Stdout`/`cmd.Stderr` write call independently (the same
  chunk-boundary limitation already documented on `SanitizeStream`,
  2026-08-23 entry above). A secret plaintext split across two
  separate pipe reads from the child process is not caught. In
  practice the sink test's `curl -v`-shaped scenario writes the header
  in one line/one write; this is a real but narrow residual gap, not
  exercised by the falsification test. **Owner:** alec. **Blocker:** no
  mission currently owns a chunk-reassembly buffer for task output;
  pick up alongside the `SanitizeStream` wiring already tracked in the
  2026-08-23 entry, since both need the same boundary-aware scan.
  **Date:** 2026-08-25.
- **`core/rpc/subagent_run_spawner.go`'s `Register` call.** Sub-agent
  tasks (`KindSubagent`) go through `Registry.Register` but never
  through `StdoutWriter`/`StderrWriter` — they are LLM streams, not
  child-process stdout/stderr, so this fix's chokepoint does not apply
  to them and none was added. Out of scope for this fix (no
  `@secret:`-bearing exec output exists on that path today); flagged
  here only so a future reader doesn't assume `RegisterOpts.Sanitizer`
  covers every task kind. **Owner:** alec. **Date:** 2026-08-25.
- **The `builtins_wiring.go` `bgSpawn` closure itself is not directly
  exercised by an automated test.** The falsification test
  (`core/tools/bash/secret_background_output_leak_test.go`) drives a
  real `tasks.Registry` through a test-local closure that mirrors
  `bgSpawn` line-for-line (constructing the full `core.Core` needed to
  reach the real closure is out of scope here) — the same pattern the
  pre-existing round-2 test already used. A mutation was applied to
  the test's mirrored `Clone()` call specifically (not to
  `builtins_wiring.go`) to prove the Clear()-survival assertion is
  load-bearing; it does not prove the production closure wasn't
  independently mistyped. **Owner:** alec. **Blocker:** none planned —
  the closure is small (6 lines) and structurally identical to its
  tested mirror; revisit if `builtins_wiring.go`'s bash-background
  block grows enough logic to diverge from the mirror unnoticed.
  **Date:** 2026-08-25.

Falsification: `core/tools/bash/secret_background_output_leak_test.go`
(`TestRunInBackground_OutputSinksDoNotLeakResolvedSecret`,
`TestRunInBackground_OutputRedactionSurvivesTurnClear`) drives a real
`tasks.Registry` with a real temp `LogDir` and real
`StdoutWriter`/`StderrWriter`, and asserts on the actual on-disk log
file bytes, the ring-buffer tail (via the hook payload), and
`Registry.Tail` (the `kenaz__monitor` read path) — not a stub's
captured argument, which is what let the round-2 fix ship with this
output-path gap still open (`secret_background_leak_test.go` stubs
`BackgroundSpawn`/`BackgroundEnd` and has zero references to
`BackgroundWriters`). Both tests were confirmed red against the pre-fix
code (with the resolved plaintext visible in the failure output) and
red again under two independent compiling mutations of the fix: (a)
gating the `lineWriter.Write` sanitize call behind `if false && …` and
(b) the `bgSpawn` mirror sharing the turn-scoped `*refs.Sanitizer`
directly instead of calling `Clone()` — mutation (b) isolates
specifically to `TestRunInBackground_OutputRedactionSurvivesTurnClear`,
confirming that test (and not the plain sink test) is what catches a
lifetime regression.

**Owner:** alec. **Date:** 2026-08-25.

**Correction (2026-08-25, later same day — see the dated entry below):**
this entry and the two above it were written as though bash's `@secret:`
handling was now complete. It was not: the *synchronous* path had its own
separate error-message leak (`core/tools/bash/exec.go`'s `Run`, feeding
`bash.go`'s `t.logf("bash.run_error", ...)`), a seventh distinct egress,
found by a reviewer's execution-based reproduction and fixed the same day.
See "seventh `@secret:` egress" below. That entry also closes the
"`builtins_wiring.go` `bgSpawn` closure itself is not directly exercised by
an automated test" gap flagged two bullets above (`core/rpc/api_b4_wiring_
regression_test.go`'s `TestB4_BackgroundSanitizerWiring_ResolvedSecret
RedactedInTaskLog` now drives the real closure, not a mirror).

### 2026-08-25 · seventh `@secret:` egress — resolved plaintext in the synchronous run-error log record (release/v0.72.0 blocking finding — FIXED)

A reviewer reproduced this by execution (side-by-side `LOG:`/`RES:` output
showing the tool result redacted and the log record carrying the plaintext
verbatim); re-verified by reading the code before the fix landed. This is
a *different* code path from LEAK 2 (background mode, fixed twice above) —
this one is the **synchronous** `kenaz__bash` call's error-wrapping, and it
predates none of those fixes; it shipped alongside WP19 undetected because
every prior falsification test for this file drove the happy path or the
background arm, never a failing `exec.Cmd.Run()` on the *synchronous* arm.

**Mechanism:** `core/tools/bash/exec.go`'s `Run` sets
`label := opts.CommandLine` — the RESOLVED, post-`@secret:`-substitution
command line — on the non-`*exec.ExitError` branch (shell-not-found,
context-cancelled-before-`Start()`, etc.), and wraps it verbatim into
`fmt.Errorf("bash: run %q: %w", label, err)`. `core/tools/bash/bash.go`
then does `t.logf("bash.run_error", "err", runErr.Error())` — **three
lines before** the WP08 sanitizer runs (`bash.go:470-477`, downstream of
where `runErr.Error()` is folded into `rawStderr`). The tool result is
sanitized correctly (it inherits the sanitize call on `rawStderr`); the
log record embeds the raw `runErr.Error()` string separately and was
never sanitized.

**Two confirmed-reachable triggers** (not timing-dependent):

- `$SHELL` set to an absolute path that no longer exists.
  `exec.go:88-93` checks `filepath.IsAbs` but never existence, so this is
  **deterministic** — every `@secret:`-bearing bash command on such a
  machine leaks, forever, not just occasionally.
- The turn is cancelled before `cmd.Start()` (user hits stop) — universal,
  any command, any machine.

Both land in `cmd.Run()`'s non-`*exec.ExitError` branch (an `*exec.Error`
or a context-cancellation error, neither of which `errors.As(err,
&exitErr)` matches), which is exactly the branch that builds the leaking
label.

**Sink:** `t.logf` → `logging.L()` → `~/.kenaz/harness/<env>/logs/
harness.log`, mode 0600, rotated at 32 MB with one generation kept, so the
plaintext persists past the leaking call. Worse: `core/core.go:416` sets
`InstallSlogBridge: true` **unconditionally** — with an OTLP endpoint or
the fleet telemetry pipeline configured, the same record egresses
**off-device**, not just to a local file.

**Fix:** removed the plaintext from the error message rather than relying
on a second sanitize pass. `RunOpts` gained a `LogCommandLine` field (the
PRE-substitution command line); `Run`'s error-label logic prefers it over
`CommandLine` when building the `%q` label, falling back to `CommandLine`
(then `Argv[0]`) when unset, so callers that never resolve a secret see no
behaviour change. `bash.go`'s synchronous call site now passes
`LogCommandLine: args.Command` (unresolved) — the exact
`execCommand`/`logCommand` split `core/tools/bash/background.go` already
used for the same reason, applied to the arm that didn't have it yet.

**`t.logf` site audit (bash.go, relative to the substitution point at
`bash.go:424`):** 16 call sites total. 15 are pre-substitution — they log
`progBase` (from `argv := FirstSegmentArgv(args.Command)`, derived before
the substitution point) or a Cedar-gate `pattern` (also derived from the
pre-substitution `argv`), or a value with no relationship to command
content at all (mkdir/write/reload errors, prompt-registry errors). The
sixteenth, `bash.run_error` at line ~473 (post-fix), is the one downstream
of substitution — confirmed independently by reading every call site, not
taken on the reviewer's count. Lines (pre-fix numbering): 386
(`bash.denied`), 397 (`bash.cwd_rejected`), 407 (`bash.invoke`), 468
(`bash.run_error` — the fixed site), 548 (`bash.gate.allow`), 552
(`bash.gate.deny`), 562 (`bash.gate.not_applicable.allow_unbooted`), 576
(`bash.gate.prompt_err`), 586 (`bash.gate.prompt_deny`), 594
(`bash.gate.allow_once`), 606 (`bash.gate.allow_always.dangerous_
demoted`), 636 (`bash.gate.snippet_skip`), 642
(`bash.gate.snippet_mkdir_err`), 652 (`bash.gate.snippet_write_err`), 655
(`bash.gate.snippet_written`), 660 (`bash.gate.reload_warn`).

**Also fixed in the same change:** `core/rpc/api_b4_wiring_regression_
test.go` gained `TestB4_BackgroundSanitizerWiring_ResolvedSecretRedacted
InTaskLog`, protecting `core/rpc/builtins_wiring.go`'s
`Sanitizer: sanitizer` line (the round-3 fix's own production wiring,
added above) against the exact same "no automated coverage of the real
closure" gap the round-3 entry flagged and left open. It drives the real
`registerBuiltinTools` with a real `*coretasks.Registry` over a real temp
`LogDir`, spawns a background command carrying a `@secret:` reference
through the real `refs.WithResolver`/`refs.WithTurnSanitizer` context, and
reads the actual on-disk `<taskID>.log` bytes.

**Falsification (both, confirmed red pre-fix and red again under a
compiling mutation of each fix):**

- `core/tools/bash/secret_run_error_leak_test.go`
  `TestSyncRunError_DoesNotLogResolvedSecret` — real `*slog.Logger` with a
  captured handler, `$SHELL` pointed at a nonexistent absolute path
  (deterministic, no race). Pre-fix: failed with the plaintext visible in
  the log record (`msg=bash.run_error err="bash: run \"echo
  SUPERSECRET-PLAINTEXT-123\": fork/exec ..."`). Mutation
  (`LogCommandLine: args.Command` → `LogCommandLine: ""`) compiled and
  reproduced the same red failure.
- `core/rpc/api_b4_wiring_regression_test.go`
  `TestB4_BackgroundSanitizerWiring_ResolvedSecretRedactedInTaskLog` —
  real production wiring, real on-disk task log. Pre-fix-equivalent
  mutation (gating the `sanitizer = s.Clone()` assignment behind `if
  false && s != nil` — a direct `Sanitizer: nil` literal does not compile,
  since `sanitizer` would then be declared-and-unused) reproduced red with
  the sentinel visible in the on-disk log file content.

**Owner:** alec. **Date:** 2026-08-25.

### 2026-08-25 · MCP http/sse transports never substitute or sanitize `@secret:` references (found alongside the WP19 leak fixes — NOT FIXED, tracked)

Only the stdio MCP transport participates in `@secret:` resolution and
sanitization: `/usr/bin/grep -rn "credstore/refs" core/mcp/` returns
exactly one non-test production file
(`core/mcp/transport/stdio/server.go`). The dispatch pool and the two
other transports never call `Resolver.Substitute` or `Sanitizer.Sanitize`
at all:

- `core/mcp/dispatch/pool.go:319`
- `core/mcp/transport/http/pool.go:337`
- `core/mcp/transport/sse/pool.go:311`

60 of the 115 recipes in `core/mcp/recipes/registry.json` declare
`transport: "http"`. Consequences: (1) a `@secret:<locator>` token in a
tool-call argument reaches the remote third-party MCP server verbatim —
the *locator string itself* leaks to a third party, not just plaintext;
(2) the per-turn Sanitizer never runs over an http/sse tool result, so if
a secret was resolved earlier in the same turn (e.g. by bash or
web_fetch) and the remote server's response happens to echo it back, it
reaches session history and the UI unredacted.

Not fixed in the same pass as the two confirmed leaks above — this is a
missing-capability gap in two entire transport implementations, not a
bypass of an existing chokepoint, and needs its own design pass (where in
the http/sse request lifecycle substitution and sanitization should hook
in, given they're not in-process subprocesses like stdio).

**Owner:** alec. **Blocker:** no mission currently owns extending
`@secret:` resolution + sanitization to the http/sse MCP transports; the
next mission touching `core/mcp/transport/{http,sse}` or
`core/mcp/dispatch` should pick this up. **Date:** 2026-08-25.

### 2026-08-23 · `data_dir` reached the fleet wire on every installed-MCP sync (PR #308 review finding H2 — FIXED, entry kept for the correction it carries)

`core/rpc/views/tools/impl.go:489-492`'s `resolveConfig` unconditionally
injects `out["data_dir"] = a.cfg.DataDir` into every recipe's persisted
config. It is a non-empty string, so `stringifyConfig` copies it into
`InstalledMCP.EnvOverrides`, and it is not an `EnvKey` name, so the
`SecretKeys` redaction never dropped it. Every synced recipe published
the user's OS username and local path layout to the fleet server.

Fixed in the same commit: `core/fleet/sync_mcp.go`'s `Collect` now strips
a `localOnlyConfigKeys` set (currently `"data_dir"`) independently of
`SecretKeys`, so it is dropped even when the secret set is determined and
empty. Falsified with a compiling mutation (`if false && localOnlyConfigKeys[k]`):
`TestSync_DataDirStripped` goes red with the absolute path visible in the
marshalled payload.

**Correction on the record.** The sub-agent that produced this fix worked
from a worktree whose base predated the v0.72.0 merges, and concluded
from `core/rpc/api.go:3256`'s `NewMCPSyncCategory(nil, nil, nil, ...)`
that `MCPRegistryReader` has no production implementer and the leak was
therefore unreachable. **That was an artifact of the stale base.** On
`release/v0.72.0`, Z505 WP07 wired a real reader — `toolsMCPRegistry`
in `core/rpc/sync_mcp_registry.go`, passed at `core/rpc/api.go` as
`NewMCPSyncCategory(mcpRegistry, mcpRegistry, ...)`. The leak was live,
not hypothetical, and the fix is a fix rather than defence-in-depth.

**Related, and genuinely still open:** `refs.SanitizeStream` has no
production caller. FR-007 requires streamed-assistant-token redaction;
only bash and webfetch tool *output* is sanitized today. The Sanitizer
docstring was narrowed to what is actually enforced rather than left
claiming coverage that does not exist. **Owner:** alec. **Blocker:**
wiring it touches `core/rpc/views/llm/impl.go`'s stream loop and needs a
persisted-history assertion. **Date:** 2026-08-23.

### 2026-08-22 · Bundled sub-agent profiles advertise containment (allowed_tools/denied_tools/budget_*) that reaches no consumer (`subagent-control-and-background-tasks-01PMZB11`, containment review of PR #307 finding B3)

**UPDATE 2026-09-09 — `budget_tokens`/`budget_time_s` half FIXED, filed
live against the owner's "agent reached the per-run budget cap" report
(ruling: "we should be using our built in autonomy dial").** As of that
change, `Profile.BudgetTokens`/`BudgetTimeS` DO reach a consumer:
`core/tools/subagentdispatch/tool.go` forwards them onto
`ForkRequest.BudgetTokens`/`BudgetTimeS` (new fields) →
`core/rpc.NewSubagentRunSpawner` records them in
`chat.SubagentBudgetRegistry`, keyed by the spawned child session id →
`ChatRunner.StartStream` reads the registry back and
`chat.applyProfileBudgetClamp` folds the value onto the run's
`coreag.Budget` (clamp, not override — see that function's doc for the
precedence justification). This landed alongside the bigger fix in the
same change: the per-run call-volume budget cap
(`MaxLLMCallsPerRun`/`MaxToolCallsPerRun`) is now scaled by the
dispatching session's autonomy tier at all (`chat.applyBudgetTierDial`,
`autonomy.BudgetCeilingForTier`) — before this, those two caps were the
flat `chat_default_classic.yaml` constants (5000/10000) for every tier
alike, which is what actually fired live. `Profile.BudgetTokens`/
`BudgetTimeS`'s doc comments were updated in the same commit.

**`AllowedTools`/`DeniedTools` are UNCHANGED — still open, see below.**
The two containment fields and the two budget fields were always
separate gaps sharing one entry; only the budget half had a live user
report driving it. Every bundled profile (`core/agents/bundled/*.yaml`)
declares `allowed_tools` and `denied_tools`. Neither reaches a consumer
on the dispatch path:

- `BranchSeamAdapter.Fork` (`core/rpc/views/agentgraph/env_deps_branch.go`)
  never reads `req.ToolAllowlist`, though `coreag.ForkRequest` carries the
  field and `core/tools/subagentdispatch/tool.go` populates it from
  `profile.AllowedTools`.
- `DeniedTools` is dropped even earlier — `ForkRequest` has no field for
  it at all.
- `Profile.IsAllowed` / `IsDenied` (`core/agents/profile.go`) have zero
  production callers.

Net effect: dispatching `explore` — whose bundled YAML says "Read-only
research worker" and lists `kenaz__write_file` / `kenaz__edit_file` /
`kenaz__bash` under `denied_tools` — produces a child session with the
full session tool catalogue, those three tools included (now with a
real, tier-clamped budget ceiling, but no tool-catalogue narrowing).
Session-level Cedar containment (`cedar.ActionUseTool`, evaluated for
every tool call in every session) still gates every call, so this is not
an absolute-terms regression, but the profile fields,
`Profile.IsAllowed`/`IsDenied`'s doc comments, and the
`kenaz__subagent_dispatch` tool description all previously implied a
restriction that does not exist for tools (it now does for budget). The
tool description and the `agents.Profile` field docs were corrected when
this entry was first filed to stop asserting it; the fields, `IsAllowed`
and `IsDenied` stay (deleting them fails the ritual's ruling test — no
named live substitute, no documented retirement, and the mission that
will consume them is already ruled to land).

**Not fixed here because real enforcement needs a session-scoped tool
permission overlay** — the containment PR #307 review answered (owner
ruling G-1, `docs/escalation-register-2026-08-19.md` Part 9): the
sub-agent mission runs to completion through UNIT-13, and per that
ruling's sequencing (`UNIT-6 → {7, 8, 9, 12} → UNIT-10 → UNIT-13`),
UNIT-9 is where these fields are meant to reach a consumer. Building that
now, inside a three-finding containment fix, would mean touching
`core/toolloop`'s `PermissionResolver` composition and
`core/rpc/api.go`'s global `perms` construction (currently ONE
`toolloop.NewMergedResolver` shared by every session, unconditional) —
real mission-scale work, not a same-PR wire.

**Owner:** alec. **Blocker:** `subagent-control-and-background-tasks-01PMZB11`
UNIT-9 (not yet dispatched this release — see owner ruling G-1), for
`AllowedTools`/`DeniedTools` only. **Date:** 2026-08-22 (opened),
2026-09-09 (budget half closed).

### 2026-09-09 · `kernel.emitCapHit`'s `PausePending`/`ResumeToken` document a `BumpAndResume` RPC that was never built — cap hits still hard-terminate the run

Found while wiring the autonomy tier to the per-run budget cap (owner
directive, live "agent reached the per-run budget cap" report).
`core/agentgraph/kernel.go`'s `PauseMarker`/`emitCapHit` doc comments
say a cap hit "pauses" the run and the frontend echoes a `ResumeToken`
back via `BumpAndResume` "so the kernel can match resume-without-bump as
a no-op" (WP17, `agentgraph-total-convergence-01PMGX01`, "cap-hit
pause-not-kill"). **`BumpAndResume` does not exist anywhere in this
tree** — not as a Go function, not as a Wails binding, not as a frontend
call site. (This is a *different*, unrelated `BumpAndResume` from the
one in the 2026-08-14 orphan-deletion entry above, which was part of the
already-deleted `core/agentgraph/dials`/`dialsview` cascade; this one is
still live prose in `kernel.go` today.)

What actually happens on a cap hit: `chat.driveRun`'s
`errors.Is(err, coreag.ErrBudgetExceeded)` case sets `reason =
"backend-error"` and closes the stream — a hard terminal error, not a
pause the user can resume from. `PausePending: true` is set on every
emitted marker regardless, which is the exact "a comment (here, a field
docstring plus a struct literal) asserting an invariant nothing
enforces" shape CLAUDE.md's ritual targets.

**Not fixed here**: building the resume RPC (a new Wails-bound method,
a frontend "bump and retry" affordance reading the marker's
`ResumeToken`/`Limit`/`Used`, and a kernel-side resume-with-bumped-cap
path) is real UI+RPC surface work, out of scope for a backend-only
budget-governance fix that must not touch Wails binding signatures. The
message fix landed alongside this entry (`chat.budgetCapMessage`) gives
the user an actionable "raise the tier in Settings" instruction instead,
which does not require a resume RPC to be true.

**Owner:** alec. **Blocker:** a mission to build the actual resume path
(new RPC + frontend), or a decision to rewrite `PauseMarker`'s docs down
to what `ErrBudgetExceeded` actually does today (hard-terminate) and
drop `PausePending`. **Date:** 2026-09-09.

### 2026-09-09 · Egress-guard census (`egress-guards-that-hold-01PMZF15` WP03) — MCP HTTP/SSE connectors have no address validation at all

`web_fetch`'s DNS-rebinding bypass (`core/tools/webfetch/webfetch.go`,
fixed the same day, same mission) prompted a census of every other
outbound-HTTP-fetch site in the tree, per spec §4. Confirmed by grep:
`net.LookupHost` now appears exactly zero times in production Go (only
in the fix's own test file) — no other site does resolve-then-connect by
hand, because no other site validates addresses at all, guarded or not.

**MCP HTTP/SSE connectors — needs the same treatment, and is arguably a
bigger gap than web_fetch was.** `core/mcp/transport/http/connection.go:205-217`
and `core/mcp/transport/sse/connection.go:202-209` each default
`spec.HTTPClient` to a bare `&http.Client{CheckRedirect: ...}` when nil —
no `Transport`, no `DialContext`, connects via `http.DefaultTransport`'s
own unvalidated resolution. Unlike web_fetch's pre-fix state, there is no
block list here at all: `validateURL` (`sse/connection.go:474`) and
`validateRecipeURL` (`core/mcp/recipes/recipes.go:516`) check only
scheme/host-non-empty/no-fragment/no-userinfo — a recipe pointed at
`169.254.169.254` or an internal service connects today with zero
resistance, blocklist or pinning. Provenance is user-typed (the
Claude-Desktop/Cursor-style `mcpServers` import flow,
`core/mcp/recipes/import.go`) or fleet/registry-shipped
(`core/mcp/recipes/registry.json`, precedence "user > registry >
shipped") — not model-controlled per call, but an imported config from
an untrusted source (a pasted "helpful" MCP config, a compromised
registry entry) reaches an internal address exactly as easily as the
pre-fix web_fetch model-injection path did. **Not fixed here** — out of
scope for the webfetch mission (spec put WP03 at P2/independent, and the
right shape is probably extending `webfetch`'s `pinnedDialContext` into
a shared helper both HTTP and SSE connectors call, not a copy). Recommend
a follow-up mission.

**Custom-OpenAI capability prober — does not need guarding, same
exception as the `ollama`/`custom` LLM adapters.**
`core/llm/custom/probe.go:51-56`'s `NewProber(httpc)` defaults to
`http.DefaultClient` when `httpc` is nil, and the real (non-test)
production call site, `core/rpc/views/llm/impl.go:1813`, does pass nil:
`prober := custom.NewProber(nil) // uses http.DefaultClient`. `BaseURL`
(`impl.go:1809-1815`) is typed by the user into the Add-Custom-Provider
settings form. This is the identical class spec §2 already carves the
`ollama`/`custom` LLM adapters out of: pointing the probe at a
self-hosted or local (`127.0.0.1`) OpenAI-compatible endpoint is the
product feature, not a bypass. `http.DefaultClient` vs.
`httpx.DefaultTransport()` is an inconsistency worth a `chore:` cleanup
someday, but not a security gap — the URL is deliberately user-owned
infrastructure.

**Fleet sync / update-manifest fetches — does not need guarding; URL is
a compile-time constant.** `core/update/manifest.go:52-53`:
`stableManifestURL = "https://downloads.kameas.ai/kenaz-harness/manifest.json"`,
`prereleaseManifestURL = "https://stage-downloads.kameas.ai/..."` — both
literal constants, `ManifestURL` has no production override path
(test-only). `fetchManifest` (`manifest.go:74-105`) and the asset
downloader (`core/update/service.go:99,265-405`) use a plain
`&http.Client{Timeout: 30 * time.Second}`, no custom `Transport` — but
`info.DownloadURL` (`service.go:184,314`) is read from the manifest JSON
itself, fetched from the same pinned first-party host over TLS. Never
influenced by the model or by untrusted external input.

**E-001 recommendation** (gate-or-not, owner call per spec §6): a
general "checked but not enforced" gate is hard to express without false
positives against the legitimate `ollama`/`custom` exemption above. A
narrower, expressible gate is plausible: lint the two MCP connector
constructors specifically for "default `http.Client` has no
`DialContext` override," since that's a fixed, small set of
call sites rather than an open-ended semantic property. Recommendation,
not a decision — owner to rule.

**Owner:** alec. **Blocker:** a follow-up mission to extend
`pinnedDialContext`-equivalent pinning to `core/mcp/transport/{http,sse}`.
**Date:** 2026-09-09.

### 2026-08-22 · `RunOptions.SkipCache` has zero frontend writers (UNIT-13, `automation-actually-runs-01PMZ404`)

`RunOptions.SkipCache bool` (`core/rpc/views/workflows/api.go:139`, wire tag
`json:"skipCache,omitempty"`), read at `core/rpc/views/workflows/impl.go:400`
into `corewf.RunOptions{SkipCache: req.SkipCache}`, has no non-test writer
anywhere under `frontend/src` — nothing on the run form or elsewhere sets it.
It is a no-op today for the same reason `rerun_policy` was: `Engine.Cache`
(`core/workflows/runtime.go`) has no production assignment, so the branch it
guards (`runtime.go:166` `if e.Cache != nil && wf.RerunPolicy != "" &&
!opts.SkipCache`) can never be reached in production regardless of what
`SkipCache` is set to.

UNIT-13 (owner ruling A-10) narrowed `rerun_policy` itself — `Store.Save` now
refuses a non-empty value outright (`core/workflows/schema.go`
`ValidateForSave`), and `Store.Load` tolerates a legacy stored value but
scrubs it to `""` before the workflow ever reaches `Engine.Run`
(`core/workflows/storage.go`, `sqliteStore.Load`) — so as of this unit
`wf.RerunPolicy != ""` can no longer be true for anything that came through
the Store, and the whole cache-consult branch `SkipCache` was built to bypass
is now doubly unreachable in production. A-10 is explicit that this is
intentional and not a delete-lane action: `Engine.Cache` and
`runtime.go:157`'s branch **stay** as the seam for if/when run caching ships;
narrowing `rerun_policy` and leaving `SkipCache` inert is what stops the
product lying about the dial without tearing out the seam a future mission
would rebuild on. See `kitty-specs/automation-actually-runs-01PMZ404/spec.md`
§5.13 / §1.11 X-11 and `core/storage/sqlite/upgrade_path_test.go`'s
`assertRerunPolicyToleratedOnLoad` for the load-side read-compat proof.

`justify(blocker: "Engine.Cache has no production assignment", owner: alec,
date: 2026-08-19)` — the date matches the owner ruling (A-10,
`docs/escalation-register-2026-08-19.md`) that decided to preserve the seam
rather than delete it; this entry was written 2026-08-22 when UNIT-13 shipped
and is the first record of `SkipCache` specifically (the field itself
predates this mission).
### 2026-08-22 · `scheduledchat.API.CreateAsModel` has no caller (`model-scheduled-jobs-01PMSJ01` WP09)

> **SUPERSEDED 2026-10-05:** `CreateAsModel` has a production caller (`harness_write_create_scheduled_run` via `scheduledRunWriterAdapter`), and the per-run containment this entry deferred is enforced as of model-harness-toolset-01MHTS001 WP02 — see that entry.

WP09 built the full server-side provenance mechanism FR-005 requires — the
`created_by`/`tool_allowlist` columns (migration `sessions/0340`), the
`CreateAsModel` entry point that stamps `created_by="model"` and refuses an
empty allowlist (ruling B-3), and the Cedar fail-safe
(`core/policy/cedar.GateScheduledChatExecute` treats `NotApplicable` as
`Deny`, not default-allow, for a model-created row — see
`policies/default_scheduled_run_policy.cedar`). `CreateAsModel` itself has
zero callers outside its own package's tests: the model-facing surface
(`harness_write_create_scheduled_run`) is WP10, per the mission's own
tasks.md this is **HARD-BLOCKED** on `harness-self-attach-01PMHS01`
WP04+WP06 (the merged-resolver wiring and the harness-self server actually
being attached to a session) — landing WP10 without that dependency would
make per-run tool containment *invisibly absent* rather than merely absent
(spec.md §6.1 F2), which is worse than the current gap.

Per `tasks.md`'s own cut-order note for this exact situation ("If UNIT-8 is
cut, UNIT-7 files a dated entry..."): the `created_by='model'` arm has a
tested mechanism and no producer.

**Owner:** the wave lead landing `harness-self-attach-01PMHS01`.
**Blocker:** `harness-self-attach-01PMHS01` WP04 (merged resolver) + WP06
(attach the harness-self server). **Date:** 2026-08-22.

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

**2026-09-12 · Sub-agent half CLOSED — see Drained.** UNIT-9 gave
`SubagentBranch`'s six fields real producers and UNIT-10 mounted
`SubagentTab.vue` + `SubagentBudgetMeter.vue` from `SessionsView.vue`,
wired to the four control RPCs UNIT-8 landed earlier the same day. This
row's sub-agent half is fully drained — see the Drained section's
"Live tools whose only UI is unmounted — sub-agent half" entry for the
verification detail. **The todo half is UNCHANGED and remains open**
(different producer, different parent component — out of this
mission's scope per its spec.md §7 non-goals); do not read this
correction as closing the row as a whole.

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

### finding #58 · the Cedar audit-decision log had no persistent backing

**Found and RESOLVED in the same pass, 2026-09-11.** Distinct from the
WP05/WP06 entry immediately above — that one fixed *which* engine
`RecentDecisions` reads (one shared singleton instead of thirteen
private ones); this finding is about what backs the `DecisionStore`
*inside* that singleton once it is reached correctly.

`core/policy/cedar/engine.go`'s `Options.Decisions` (the `DecisionStore`
seam `Engine.Evaluate` appends every decision through) was omitted at
**both** real `cedar.NewEngine` call sites —
`buildCedarEngineOrNil`/`buildCedarGate` in `core/rpc/api.go`. With it
nil, `NewEngine` always fell back to `NewMemoryDecisionStore(0)`: a
256-entry in-memory ring, silently truncated while running and wholly
lost on every restart. This is the audit trail for every permission-gate
decision the harness makes (trust/compliance-relevant per this repo's
disposition rules) — invisible by design, since an audit log that
silently forgets looks identical to one with nothing to report.

**Fix:** a new `cedar.SQLDecisionStore` (`core/policy/cedar/
sql_decision_store.go`), backed by a dedicated `policy_decisions` table
(migration `cedar-policy/1300-policy-decisions`,
`core/policy/cedar/migrations.go` — new reserved block 1300-1399,
verified free against every other `CanonicalBlocks` entry and every
migration file in the tree before claiming it). `decisions.go`'s own doc
comment had already anticipated this exact shape ("production wiring …
a dedicated SQLite policy_log table"). Retention is bounded and
deliberate — the most recent 5,000 decisions, enforced by a `DELETE`
after every durable write (not a periodic sweep, so the bound cannot be
silently skipped the way finding #61's unbounded store was). `Append` is
non-blocking (queues onto a buffered channel; a background goroutine
performs the actual write) to preserve `Evaluate`'s documented "MUST NOT
block on I/O" hot-path contract; the in-memory ring `Recent()` reads
from is hydrated from disk at construction so history survives a
restart. Both builders now thread a real `*cedar.SQLDecisionStore`
(built from the same unified `storage.DB` every other harness store
uses — no second sqlite connection) through `Options.Decisions`; the nil
fallback stays intact for the test chassis.

**Falsifiability:** `TestCedarDecisionPersistence_SurvivesCloseAndReopen`
(`core/rpc/api_cedar_decision_persistence_test.go`) drives the real
`rpc.New` boot path, evaluates a gated action, shuts the process down,
boots a brand-new `core.New`/`rpc.New` over the same `DataDir`, and
asserts the decision is still there — plus an independent raw-sqlite
query of `policy_decisions`. Run against the production wiring reverted
(the `cedarDecisionsOpt` assignment removed), it failed with
`RecentDecisions is empty on the reopened engine — the decision did not
survive a restart`; restored, it passes. `core/policy/cedar/
sql_decision_store_test.go` additionally pins retention actually
evicting from the durable table (not just the in-memory ring) and race
safety under concurrent `Append`/`Recent`. `core/storage/sqlite/
cedar_decision_upgrade_test.go` proves migration 1300 applies over a
populated database a previous release (`testdata/upgrade/v0.77.1`)
actually produced, per this repo's blind-spot-#3 discipline.

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

**2026-09-12 · CLOSED — see Drained.** The conditional above fired:
`subagent-control-and-background-tasks-01PMZB11` shipped the producer
(migration registration, real `BackgroundSpawn`/`BackgroundEnd`
assignments, the `HookFirer` wiring, `kenaz__monitor`'s predicate case)
and, per the ruling, remounted `TasksPanel.vue` + restored the Settings
nav entry in UNIT-11. See the Drained section's "The background-task
subsystem has no producer" entry for the full verification detail and
commit list.

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
- **Owner / deleting change:** a mission that surfaces both strictness
  dials on the Workflows surface (Workflows › Schedules / Library — the
  Settings → Workflows panel was retired into it by nav-ia-sweep-01DOGF0F,
  2026-10-04), adds `Settings_{Get,Set}CedarStrictWorkflowMode`, and deletes
  this entry.

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

### 2026-09-10 · `eval.Recorder.AppendMessage`/`AppendToolCall`/`AppendLLMRequest`/`AppendLLMResponse` have zero production call sites

**Found**: 2026-09-10, during review follow-up on PR #337
(`fix/eval-capture-canonical-redaction`). **RAN**: grep across `core/` for
every caller of `*eval.Recorder`. The only production holder of a
`*eval.Recorder` is `core/rpc/api.go`'s `evalRecorder` field, and it calls
exactly `StartCapture`, `StopCapture`, and `StopAll`
(`api.go:1233,1278,1287`). `StartCapture`/`StopCapture`
(`core/eval/capture.go:361,377`) only open/close a `captureWriter` and write
the `KindCaptureStart`/`KindCaptureStop` administrative records; neither
those two methods nor anything else on the production path calls
`AppendMessage`, `AppendToolCall`, `AppendLLMRequest`, or
`AppendLLMResponse`. Every call site for those four is a test
(`core/eval/model_profile_gate_test.go`, `core/eval/eval_test.go`,
`core/eval/capture_redaction_test.go`).

Concretely: an eval capture started today (via the wired
`Sessions_StartCapture`/`Sessions_StopCapture` Wails bindings) produces a
`.jsonl` file containing only `capture_start`/`capture_stop` records — no
session message, tool call, or LLM request/response content is ever written
to a capture file in the shipped build. PR #337's redaction fix is real and
correct (the catalog it replaced genuinely under-redacted), but it hardens a
path nothing currently reaches.

**Checked whether this is a user-visible lie: it is not, today.** No
`.vue` file references `StartCapture`/`StopCapture`/`evalCapture`, and
`harnessClient.ts`/`types.ts` have no wrapper for either binding — every
"capture" hit in those two files is the unrelated auto-capture-generated-
images or memory-capture-rate features. There is no UI surface that offers
to start an eval capture at all, so no UI claims it records conversation
content. The only prose claiming eval-capture writes message/LLM content is
internal and developer-facing: `core/eval/capture.go`'s own package doc and
`docs/escalation-register-2026-08-19.md`'s G-3 discussion — not a
user-visible claim.

**Disposition: finish, not delete.** Per CLAUDE.md's "only surface for a
real capability" rule: `AppendMessage`/`AppendToolCall`/`AppendLLMRequest`/
`AppendLLMResponse` are the only way eval capture could ever record a real
conversation. The `Recorder` type, the JSONL schema (`CaptureEntry`,
`MessageEntry`, `ToolCallEntry`, `LLMRequestEntry`, `LLMResponseEntry`), the
replay/diff/model-profile-gate machinery in
`core/eval/{replay,diff,model_profile_gate}.go`, and now PR #337's
redaction fix, are all built specifically to consume records only these
four methods can produce. Deleting them removes eval capture — and with it
eval-harness-replay and the model-profile-gate regression check — from the
product, not just from the tree. There is no live substitute and no
documented retirement.

- **Blocker:** nothing calls `Recorder.AppendMessage`/`AppendToolCall`/
  `AppendLLMRequest`/`AppendLLMResponse` from the chat/tool-execution path.
  Wiring them means finding the actual turn-loop call sites —
  `core/rpc/chat_run_dispatcher.go` and/or the `core/rpc/views/agentgraph`
  env/session plumbing, wherever a message is appended to the session and a
  request/response crosses the LLM boundary — and, behind an
  `if a.evalRecorder != nil && a.evalRecorder.IsCapturing(sessionID)` guard,
  calling the matching `Append*`. That is a real wiring mission (turn-loop
  + tool-exec + LLM-adapter call sites, tests with `IsCapturing` both on and
  off, a populated capture file asserted against `ReadCapture`), not a
  drive-by fix.
- **Owner:** whoever picks up eval-harness-replay end-to-end — the feature
  `Sessions_StartCapture`/`Sessions_StopCapture` were built for, per
  `core/rpc/api.go:785`'s own comment ("the per-session eval-capture writer
  (eval-harness-replay)"). No owner is currently assigned. Until claimed,
  `Sessions_StartCapture`/`Sessions_StopCapture` stay reachable (the Wails
  bindings exist) but functionally inert: calling them produces a capture
  file with nothing but start/stop markers, and no UI currently exposes even
  that much.

### 2026-09-11 · RESOLVED (import boundary) / STILL OPEN (buildability) — `check-no-fleet-imports.sh`'s bare `core/rpc` allowlist entry exempted its whole subtree, and 7 real view packages relied on the hole

**Found**: 2026-09-11, during the finding-#48 planted-violation-proof sweep
(11 of 49 CI gates had no proof they could fail — see
`scripts/ci/gates_can_fail_test.go`'s 2026-09-11 block). Planting a
`core/fleet` import in a brand-new package under `core/rpc/views/` to prove
`check-no-fleet-imports.sh` (the OSS-first boundary gate) could fail — it
didn't. **RAN**, not read: `bash scripts/ci/check-no-fleet-imports.sh` with
`import _ ".../core/fleet"` planted in a fresh
`core/rpc/views/zzgateprobefleetimport/probe.go` reported
`clean — no unauthorized fleet imports found: PASS`, exit 0.

**Root cause** (`scripts/ci/check-no-fleet-imports.sh:79`, unchanged by this
sweep — see disposition below): the allowlist match is
`[[ "$pkg" == "$a" || "$pkg" == "${a}/"* ]]` for every entry in `ALLOWLIST`,
including the bare `"${MODULE}/core/rpc"` entry. The `"${a}/"*` wildcard
means that entry matches **any** package whose import path starts with
`core/rpc/` — not just the top-level `core/rpc` chassis-wiring package the
comment above it describes. `core/rpc` has exactly two subdirectories,
`middleware` (separately allowlisted) and `views` (dozens of packages, only
two of which — `settings`, `fleet` — are supposed to be exempt). Every other
package under `core/rpc/views/` inherits the exemption by accident.

**This is not hypothetical — RAN and confirmed live**: reverting the plant
and instead running the gate against the unmodified tree with a locally
tested fix (exact-match the bare `core/rpc` entry, keep prefix matching for
the other four) turned the gate red against the **real, currently-committed
tree**, naming 7 packages: `core/rpc/views/{catalog,cedar,compliance,
contexts,sites,slashcmd,sync}`. Each has a non-test `impl.go` importing
`core/fleet` (confirmed via `grep -l core/fleet core/rpc/views/<pkg>/*.go`)
and none is in `ALLOWLIST`. They pass today only because of the prefix hole.
This sweep did not ship that fix — see disposition.

> **UPDATE 2026-09-11 (same day), v0.78.2.** The classification the escalation
> below asked for was **done**, and the gate fix **shipped**. Each of the 7
> packages was checked by reading actual usage rather than import lines: every
> one holds a `*fleet.Client` / `*fleet.Syncer` / `*fleet.AuditArchiver` (etc.)
> as a structural field on its API struct, wired with a live fleet object at
> boot in `core/rpc/api.go`, and none is cheaply decouplable. So reading (a)
> — legitimately fleet-facing — won for all 7; none was OSS-first drift to
> unwind. All 7 are now in `PREFIX_ALLOWLIST` with dated justifications, the
> matcher is split into `EXACT_ALLOWLIST` (equality; the `core/rpc` chassis)
> and `PREFIX_ALLOWLIST` (named leaves), and a new planted-violation proof
> plants *inside* `core/rpc/views/` and is named by the gate.
>
> **Two corrections to the entry below.** (1) The exemption was larger than
> recorded: the wildcard exempted not 7-plus-chassis importers but **every
> package under `core/rpc/` — 53 of them — plus `core/mcp/builtin/sites`,
> i.e. 54 of 242 checkable packages went unreviewed.** The count of *real*
> importers (12 = 1 exact + 11 prefix) was right; the count of *exempted*
> packages was never stated. (2) `core/rpc/middleware` was already in the
> pre-fix allowlist by name, so it was never "previously unlisted" — the 7
> genuinely-new entries are `catalog, cedar, compliance, contexts, sites,
> slashcmd, sync`. Independent review caught this; the PR body and commit
> message for the fix both said 8 and are wrong.
>
> **What is still open is not the gate, it is the architecture.** The property
> the OSS-first framing implies — *delete `core/fleet/` and `core/rpc` still
> builds* — is **genuinely not held**: `core/rpc` imports `core/fleet`
> directly and uses `*fleet.Client` as a bare local type, and all 11 prefix
> packages hold typed `*fleet.X` fields. **No CI job verifies it**;
> `check-oss-first.sh` only sets `HARNESS_FLEET_DISABLED=1` with the package
> still physically present, so it cannot catch this. The gate's header now
> discloses that its "fork case: PASS" is an import-boundary pass only and
> says so in the script. Unwinding it (neutral interface, or a build tag)
> remains an architecture decision with **no owner assigned** — that half of
> the escalation below stands unchanged.

**Disposition: escalate, not fix-and-ship.** The technically-correct fix
(exact-match `core/rpc`) is small in diff size but not small in blast
radius: it immediately fails CI for 7 packages that have apparently been
fleet-facing for some time without anyone widening the allowlist to say so
in review. Two readings are equally plausible from here and this sweep has
no way to distinguish them:

1. These 7 packages have a legitimate, undocumented reason to import fleet
   (config-pull, capability checks, telemetry — several plausible per their
   names: `sites`, `sync`, `compliance`), and the allowlist itself is stale
   — it should be widened to name them explicitly, with the same
   per-package justification style as the existing 5 entries.
2. This is exactly the OSS-first drift the gate exists to prevent, and it
   shipped silently because the prefix bug made the gate incapable of
   seeing it — the fork/OSS-first contract (`check-oss-first.sh`,
   `HARNESS_FLEET_DISABLED=1`) may currently be broken for anyone who forks
   and deletes `core/fleet/`, since 7 RPC view packages would fail to build.

Per CLAUDE.md's "Escalate when the call is genuinely product, not
technical" — resolving which reading is true requires knowing why each of
the 7 packages reaches into `core/fleet`, which is a review call, not a
grep result.

- **Blocker:** someone who knows the fleet-integration roadmap needs to
  classify each of the 7 packages as (a) legitimately fleet-facing → add to
  `ALLOWLIST` with a one-line reason matching the existing 5 entries' style,
  or (b) drift → remove the import / route it through `core/rpc/views/fleet`
  or `core/rpc/views/settings` instead. Only after that classification
  should the exact-match fix to `check-no-fleet-imports.sh:79` (tested and
  ready — see the sweep's PR) land, since landing the gate fix first with no
  classification done would just turn CI red with no actionable diff.
- **Owner:** whoever owns the fleet-auth-foundation-01NDFSEX08 boundary
  (the mission `check-no-fleet-imports.sh`'s own header attributes WP07 to).
  No owner currently assigned.

The gate's planted-violation proof added by this sweep
(`no-fleet-imports/unauthorized-package-imports-fleet` in
`gates_can_fail_test.go`) deliberately plants outside `core/rpc/` (under
`core/sessions/`) to stay honest about what the **shipped** gate can
currently prove — the field-proven class (an unrelated package importing
fleet, the shape that really fired on release/v0.78.1 against
`core/serve`). It does not claim the `core/rpc/views/` hole is closed.

### 2026-09-12 (fleet-org-config-inheritance-01NORGX01 WP02 triage) — `Bundle.ProvisionedMCP`/`ProviderSetups` were signed and transmitted with ZERO apply branch — WIRED for ProvisionedMCP, ProviderSetups stays deferred

**Found**: 2026-09-12, triaging `fleet-org-config-inheritance-01NORGX01`
against the live tree. `ef43a2f1` (2026-09-10, WP01) added
`Bundle.ProvisionedMCP` and `Bundle.ProviderSetups`, both signed into
`bundleSigningPayload` — so a fleet server could push either section and
the harness would verify and ACK it — but
`compositeConfigApplier.ApplyBundle` (`core/rpc/views/settings/fleet.go`)
had no branch reading either field at all. **RAN**, not read:
`rtk proxy grep -rl "ProvisionedMCP\|ProviderSetup" core/` returned only
`bundle.go`, `bundle_test.go`, `bundle_knob_coverage.go` — no consumer
anywhere in the tree. `bundle_knob_coverage.go` already carried
`RegisterDeferred` entries for both (so `TestKnobCoverage_Bundle` was not
lying), but the deferral was undocumented here, contrary to CLAUDE.md's
own release-ritual instruction to record every `RegisterDeferred` with a
dated blocker+owner.

**Disposition — split, because the two sections have different blockers.**

- **ProvisionedMCP: WIRED, same commit as this finding.**
  `recipes.ApplyProvisionedMCP` (`core/mcp/recipes/org.go`, new) installs
  each entry as the highest-precedence ("org_wins_readonly") layer of the
  shared `*recipes.MergedCatalog` (`merged.go`'s new `SetOrgRecipes`).
  `compositeConfigApplier.ApplyBundle` now converts `b.ProvisionedMCP` and
  calls it unconditionally on every apply (not gated on non-empty — see
  the in-code comment on why a subsequent empty bundle must clear a prior
  org overlay, not leave it stale). `StopFleetBackground` (sign-out)
  clears the overlay via the same method. `knobcoverage.Register` replaces
  the `RegisterDeferred` entry. No architecture change was needed for
  WP03's "OAuth client_id resolution order" requirement: every OAuth
  sign-in call site resolves the recipe to use via
  `MergedCatalog.Get`/`.Recipes()` (e.g.
  `core/rpc/views/tools/oauth.go:404`'s `recipe.Auth.ClientID` read), so
  once the org-provisioned recipe wins the merge, its `Auth.ClientID`
  wins the resolution by construction — spec §3.3's ordering falls out of
  the merge precedence rather than needing separate resolution code.
  `docs/unwired-ledger.md` did not previously record `mcp_recipes`'s own
  near-miss: `core/rpc/sync_categories.go`'s `emptyPayloadKind` declared
  `ScopeOrg` for the `mcp_recipes` `SyncKind` (registered, no error) with
  an Apply that was **always** a no-op regardless of scope — an
  `org_config["mcp_recipes"]` payload would have silently "succeeded"
  while doing nothing, the exact "gate that cannot fail" shape CLAUDE.md
  flags. This is now fixed too: `mcpRecipesKind`'s `ScopeOrg` branch
  parses the payload and calls the identical `ApplyProvisionedMCP`
  function used by the bespoke-bundle-field path, so the wire shape can
  migrate later (`fleet-generic-sync-framework-01NSYNC02` WP04, not yet
  landed) without a second implementation ever existing.

- **ProviderSetups: still deferred, RegisterDeferred entry re-dated with a
  named blocker.** Owner: alec. Blocker: `kitty-specs/fleet-org-config-
  inheritance-01NORGX01/plan.md`'s Gates section states explicitly — "no
  harness WP04 merge before a fleet dev environment can exercise it" —
  and the dedicated encrypted org-key channel WP04 requires (org-shared
  provider keys delivered straight into the device credstore, never
  through any bundle/RPC/frontend path) does not exist yet either. This
  is the same "kenaz-fleet org endpoints not yet available" blocker ruled
  at `docs/escalation-register-2026-08-19.md` §F-2 for the mission as a
  whole — unchanged as of this date. Unlike ProvisionedMCP, WP04's apply
  target (LLM provider stack wiring in `core/rpc/api.go`+`core/llm/*` plus
  a not-yet-built credstore-delivery mechanism) has no safe
  server-independent slice to wire ahead of the blocker clearing.

**Two related, out-of-scope findings surfaced during this triage — recorded
here because they are also `RegisterDeferred`-shaped gaps this sweep found
but did not fix (both predate this mission and are cross-cutting to
`fleet-config-pull-01NDFSEX10`, not `01NORGX01`-specific):**

1. **UPDATE (2026-09-15, `01NORGX01` WP05):** `KindFleetConfigApplied` is
   now wired — `compositeConfigApplier.ApplyBundle`
   (`core/rpc/views/settings/fleet.go`) emits it directly on a fully-clean
   apply, via a new `auditEmitter` field on `fleetState` +
   `SetAuditEmitter`, wired from `core/rpc/api.go` reusing the same
   `fleetAuditEmitter` bridge instance already constructed for the
   catalog/sync views (no new construction). The payload
   (`FleetConfigAppliedPayload`) was extended with `OrgID`/`OrgName` (read
   best-effort from the on-disk `fleet.Identity` cache) and
   `ProvisionedRecipeIDs`, closing this mission's FR-010 ("naming the org,
   the recipe/provider ids, and the bundle_id") for the one section this
   mission owns. Pinned by
   `core/rpc/views/settings/fleet_wp05_audit_test.go`. The emission point
   turned out to be `compositeConfigApplier.ApplyBundle` itself, not
   `ConfigPoller` — every recipe/provider id needed for the payload is
   already local to that function, so this did NOT require the
   constructor-signature change originally assumed below; only
   `ConfigPoller`-level signals (signature rejection, which section failed)
   still need that plumbing.
   
   **Still open, same as before:** `KindFleetConfigSignatureRejected` (needs
   `ConfigPoller.poll()`'s signature-verify-failure branch, before
   `ApplyBundle` is ever called) and `KindFleetConfigPartialFailure` (needs
   per-section failure attribution — today's `errs []error` slice carries
   section names only inside free-text error strings, not a structured
   tag). Owner: alec. Blocker: threading an audit emitter through
   `ConfigPoller` for the signature-rejection case, plus a section-tagged
   error type for the partial-failure case (cross-cutting to
   `fleet-config-pull-01NDFSEX10`, not `01NORGX01`-specific — same
   reasoning as the original entry below).
   
   Original entry (2026-09-12), preserved for history: `audit.KindFleetConfigApplied`, `KindFleetConfigSignatureRejected`, and
   `KindFleetConfigPartialFailure` (`core/context/audit/audit.go:265-286`,
   payload structs at `:1345-1381`) are declared with full payload types
   and privacy-invariant doc comments but have **zero emit call sites
   anywhere in the tree** — **RAN**:
   `rtk proxy grep -rn "KindFleetConfigApplied\|KindFleetConfigPartialFailure\|KindFleetConfigSignatureRejected" core/`
   returns only the three declarations plus their payload structs, no
   `Emit(...)` call. `core/fleet/config_pull.go`'s `ConfigPoller` (the only
   place that knows verify/apply/ACK outcomes) has no audit-emitter field
   at all — wiring this is a `ConfigPoller` constructor-signature change
   touching every call site, not a small patch. This is why this
   mission's own FR-010 ("applying provisioned sections emits an auditable
   event naming the org, the recipe/provider ids, and the bundle_id") is
   **not met** — there is no live `fleet.config.applied` emission for
   ANY bundle section to extend, not just this mission's new one. Owner:
   unassigned. Blocker: threading an audit emitter through `ConfigPoller`
   (cross-cutting to `fleet-config-pull-01NDFSEX10`).
2. `config_pull.go`'s own header comment (line 12) claims the disk cache
   is "`<DataDir>/fleet/bundle.json` + `bundle_checksum.txt`", but the
   actual persisted files are `bundle_id.txt` + `bundle_checksum.txt`
   (`bundleIDPath`/`bundleChecksumPath`, `:378-385`) — the full bundle
   body is **never** written to disk. Every section that only lives in an
   in-process package-level var (`llmview`'s model-prefs store is the
   clearest case; `recipes.MergedCatalog`'s new org overlay from this WP
   is now in that same category) is lost on a process restart while
   offline, contradicting spec NFR-003 / this mission's FR-009
   ("cached provisioned config stays active when fleet is unreachable").
   "Offline-safe" today only means "mid-session, don't undo what's
   applied in memory" — it does not survive a restart. Not fixed here:
   persisting and replaying the full bundle at boot is an architecture
   change to `fleet-config-pull-01NDFSEX10`, not an `01NORGX01` patch.
   Owner: unassigned. Blocker: same as #1 — both are `ConfigPoller`/
   `config_pull.go` architecture, not this mission's surface.
### 2026-09-12 (connector-lifecycle-truth-01PMZ303 UNIT-3/UNIT-4) — E-006: the `oauth` primary_auth arm (Slack + 5 others) has no working sign-in path, and nine Slack-specific symbols are dead until it is resolved

`core/rpc/views/tools/oauth.go`'s `SignInRecipe` fails closed
unconditionally for every recipe whose `primary_auth == "oauth"` (6
recipes: slack, zapier, make, pipedream, google-calendar, google-drive),
citing `E-006` in the returned error string. This is deliberate and
correct as shipped — none of the six is dynamically-registerable
(`browser_oauth_dcr`), ships a pre-registered client id
(`browser_oauth_pkce`), or has a device-code flow — but it leaves real
dead code behind it:

- `core/mcp/oauth/slack_signin.go`'s nine exported symbols
  (`SlackSignIn`, `SlackSignInWithDiscovery`, `ResolveSlackClientID`,
  `SlackSignInConfig`, `SlackAuthorizationEndpoint`, `SlackTokenEndpoint`,
  `SlackClientIDEnvVar`, `SlackDefaultScopes`, `ErrSlackNoClientID`) have
  zero production callers. They drag `SlackLoopbackPort`
  (`loopback.go:32`) and `InteractiveConfig.FixedPort` (`loopback.go:60`)
  with them — `FixedPort` has no non-Slack, non-test setter.
- Two dead branches live inside this dead code
  (`slack_signin.go`'s `SlackSignInWithDiscovery`, MO-06): `:192`
  returns unconditionally on `err != nil` so the `errors.Is(err,
  ErrNoChallenge)` fallback a few lines later is unreachable, and
  `scopes` is already defaulted earlier in the function so the
  `len(scopes) == 0` branch is a second dead branch in the same
  function. Both are unreachable regardless of whether the Slack lane
  is ever wired, but fixing them has zero behavioural value while
  nothing calls the function they live in — they would need re-review
  the moment E-006 is resolved anyway, since resolving it means writing
  (or rewriting) this function's real control flow.

The user-visible half of this — `registry.json`'s slack `warning` telling
the operator to set an environment variable no code reads — did **not**
wait on E-006 and is fixed (UNIT-4, this release): the copy now states
the real limitation and points at the working `slack-tokens` stdio
fallback, with `warning_severity: "danger"` so it renders as the hard
blocker it is.

- **Blocker:** whether Slack (and the other 5 `oauth`-arm recipes) moves
  to a real sign-in path is a product call, not a technical one.
  `slack_signin.go:22–24`'s own TODO anticipates moving Slack to a baked
  client id, which would reclassify it as `browser_oauth_pkce` under
  UNIT-2's bring-your-own posture — that decision (register a real app
  vs. rely entirely on the BYO posture vs. leave slack-tokens as the only
  supported path) has not been made.
- **Owner / deleting change:** alec. Deletes (or rather, resolves) when
  either (a) a product decision routes the `oauth` arm's recipes to one
  of the five working arms and this unit's dead Slack symbols get real
  callers (fixing MO-06 in the same commit, since it would no longer be
  dead-code-inside-dead-code), or (b) the product decides `oauth`-arm
  recipes are permanently `keys`/stdio-fallback-only, in which case the
  nine Slack symbols, `SlackLoopbackPort`, and `InteractiveConfig.FixedPort`
  become deletable under a documented product retirement (not today's A-0
  freeze).

### 2026-09-12 (connector-lifecycle-truth-01PMZ303 UNIT-5) — three MO-* OAuth findings justified rather than wired

Three of the sixteen OAuth-cluster findings the 2026-08-18 closing sweep
assigned to this mission (`spec.md` §1.10) were reviewed this pass and
found to need either a real per-call clock-injection design or a real
multi-scheme-header design — neither of which is a one-line wire, and
inventing one un-reviewed risks landing wrong. MO-07, MO-09, MO-12 and
MO-13 were wired or pinned by test this same session (see git log —
`fix(mcp): UNIT-5 (MO-07, MO-09)`, `test(mcp): UNIT-5 (MO-12)`, `fix(mcp):
UNIT-5 (MO-13)`); the doc naming `LoadedClient` (a type that does not
exist in the repo) was already corrected by an earlier UNIT-3 commit and
needed no further action this pass.

- **MO-05** — `ResolveClientIDConfig.Now` (`resolve.go:71-72`) is derived
  into a local `nowFn` (`:97-99`) that is never invoked; the DCR expiry
  check that actually runs uses `DCRStore`'s own `s.nowFn` (fixed to
  `time.Now` at `NewDCRStore` construction, `dcr_store.go:105`, with no
  setter). Wiring `cfg.Now` to mean anything would require either a
  per-call clock override on a shared, potentially concurrently-used
  `*DCRStore` (a real race-safety design question — CI runs `-race`) or a
  second `DCRStore` constructed per call (defeats the point of the
  cross-launch cache UNIT-3 3e just wired). No test anywhere sets
  `cfg.Now` today, including in this package's own test suite, so this is
  a genuinely orphaned seam, not a live regression risk.
  - **Blocker:** needs a design decision on whether `DCRStore`'s clock
    should be mutable per-call (and if so, how that interacts with
    concurrent `Resolve` calls sharing one store) or whether this field
    should be retired in favour of constructing a test-only `DCRStore`
    with a custom `nowFn` directly (which every existing DCR expiry test
    already does, bypassing this field entirely).
  - **Owner:** alec.

- **MO-08** — `StoredCredential.AuthorizationHeader` (`store.go`)
  hardcodes `"Bearer "` under a doc saying `TokenType` defaults to Bearer
  when unset; `TokenType` is written on every mint path and read nowhere.
  This has a **live caller** (`core/rpc/views/tools/oauth.go`'s bearer
  injection at spawn), so a DPoP/MAC-authenticating MCP provider would
  401 with no diagnostic naming the real cause. No recipe in either
  catalog uses a non-Bearer scheme today (measured: zero `token_type`
  overrides anywhere in `registry.json`/`shipped.json`), so this is
  latent, not field-proven.
  - **Blocker:** wiring `TokenType` into the header format is a real
    per-scheme change (Bearer vs. DPoP have different header shapes —
    DPoP requires a proof-of-possession JWT, not just a different
    keyword), not a one-line format-string edit, and there is no
    DPoP/MAC provider in either catalog to test against without
    inventing one.
  - **Owner:** alec.

- **`FromDCR`** (`resolve.go:49`, written at `:117`/`:150`) — its doc says
  *"so callers can track the source for debugging"*; nothing logs or
  emits it today. Read only in tests.
  - **Blocker:** none technical — this is the cheapest of the three to
    close (thread it into the existing `mcp.recipe.*` structured log
    lines `SignInRecipe`/`ResolveClientID` already emit) but doing so
    without a concrete downstream consumer (a log line nobody greps for
    is a different flavor of the same "recorded, unread" defect this
    mission is about) needs a decision on whether debug-level
    provenance logging is worth the extra field on every log call, or
    whether `FromDCR`'s job is fully discharged by
    `ClientIDResult.FromDCR`'s existing test coverage of the resolution
    order itself.
  - **Owner:** alec.

### 2026-09-12 (connector-lifecycle-truth-01PMZ303 UNIT-12) — http/sse `Spec.InitTimeout` has no request to gate

`core/mcp/transport/http/connection.go:52` and
`core/mcp/transport/sse/connection.go:71` declare `InitTimeout` with a doc
promising it is *"the response deadline once initialize is on the wire"*.
UNIT-12 wired the stdio half of this finding (`InitTimeoutMs`/
`PingPeriodMs` now reach `stdio.SpawnSpec` from the recipe, overriding the
pool-wide default — see git log) but deliberately did not touch http/sse,
because the premise the doc and the original spec finding both share —
"the deadline is assigned but not read" — undersells what is actually
there: **`http.Connection.Open` (and sse's equivalent) performs zero
network I/O.** There is no `initialize` JSON-RPC round-trip anywhere in
either transport package at the `Connection` level; `Open` only parses
and validates the URL, builds header templates, and sets up the HTTP
client. `MethodInitialize` is dispatched exactly once in the whole
`core/mcp` tree, from `stdio/server.go:465` — http and sse never send it.

This means `InitTimeout` cannot be "wired" by adding a read of the field
inside `Open`, because there is no request there to put a deadline
around. The honest fix is a real design question: either (a) http/sse
gain an actual stateful handshake step (a real feature — these
transports may be intentionally stateless-per-POST, matching how MCP
supports HTTP-transport servers that answer each JSON-RPC call
independently with no session concept, in which case "the response
deadline once initialize is on the wire" describes a step these
transports never perform by design), or (b) `InitTimeout` is repurposed
to gate the *first* real network call each transport does perform (the
first `tools/list`, or the health probe's first tick), which changes its
semantics from what its doc currently claims.

- **Blocker:** whether http/sse are meant to have a stateful handshake at
  all is a product/protocol-conformance call, not a technical one — it
  determines whether this is "wire a missing feature" or "the doc
  describes a step that doesn't apply to this transport shape, narrow
  it." Either answer closes this differently.
- **Owner:** alec. Deletes (or resolves) when either a handshake step is
  added to `http.Connection`/`sse.Connection` and `InitTimeout` gates its
  response, or the doc on both `Spec.InitTimeout` fields is narrowed to
  say explicitly that no handshake exists for these transports and the
  field is retired under a documented protocol-conformance decision
  (not today's A-0 freeze, since that requires a product ruling this
  session did not have).

### 2026-10-04 (engine-publication-01ENPUB01, harness half WP-H1..H5) — engine publication is built end to end and degrades honestly until three external items land

**What is live in the harness now.**

- `cmd/kenaz-ml-sign` (WP-H1): `keygen` / `sign` / `verify` / `pin-gen`.
  `sign` signs `mlsidecar.EngineManifest(...).SigningPayload()` — the
  exact bytes `mlsidecar.Install` verifies — and
  `TestSignThenHarnessInstallVerifies` drives a fresh-key signature
  through the real `Install` (local_path channel, real `core/trust`
  engine, `SigningRequired`). The flag set is the contract the kenaz-ml
  publish job (WP-M2) is built against.
- Baked release anchor (WP-H2): `core/mlsidecar/release_signing_key.pub`
  (go:embed) + `-ldflags -X …/core/mlsidecar.releaseSigningPubKeyHex`.
  `core/rpc` boot calls `trust.SeedAnchor`, which yields to every
  existing row (live → untouched, tombstoned → stays revoked, same id
  with another key → untouched). Pinned on real sqlite in
  `core/trust/seed_test.go`.
- Build-time pin (WP-H3): `core/mlsidecar/pinned_release_gen.go`,
  regenerated by `pin-gen`; `TestPinnedReleaseGen_NoDrift` is the drift
  guard (a Go test rather than a `check-*.sh`, so it runs in the
  existing `go test ./core/...` job with no new gate to plant-prove).
- `http_mirror` (WP-H4): no 60s whole-body cap; a 60s *stall* watchdog
  instead. Applies to every http_mirror fetch, bundle installs included.

**What degrades honestly, and why (all three are external — spec
§External).**

1. `pinned_release_gen.go` is the zero value → `PinnedEngineRelease`
   returns `ErrNoPublishedRelease` → Settings says "not published". The
   engine-pin step in `.github/workflows/release.yml` is present but
   COMMENTED OUT with a dated note; enabling it needs (a) and (b) below
   plus a first published engine.
2. `release_signing_key.pub` is a NOT-A-REAL-KEY placeholder → boot
   seeds nothing → even a pinned build would fail closed
   (`anchor_missing`) unless an operator/fleet installs the key via the
   TrustAnchors RPC.
3. Blockers: (a) kameas-infra extends `gh-deploy-<env>` OIDC trust to
   the kenaz-ml repo; (b) the owner runs `kenaz-ml-sign keygen`, sets
   `KENAZ_ML_RELEASE_SIGNING_KEY` on kenaz-ml and commits the `.pub`.

- **Owner:** release-infra (alec). Deletes when the first engine is
  published, the real key is committed, and the release.yml step is
  uncommented — in that change, also delete the dated note on
  `mlsidecar.PinnedEngineRelease`.

> **CLOSED 2026-10-07 (items 1–3; `chore(ci): enable the kenaz-ml
> engine-pin step against 0.1.1`).** (a) kenaz-ml publishes to the
> kameas release buckets; (b) the real key landed in #388 (KeyID
> `56fc9905…0352512`); kenaz-ml 0.1.1 is live on
> `downloads.kameas.ai/kenaz-ml/0.1.1/`. Verified before enabling: the
> index's `darwin_arm64.key_id` == the baked KeyID; `kenaz-ml-sign
> verify` accepts the published `.sig`; the DMG is 189403636 bytes and
> hashes to the index sha256; a one-off `go run` of the real
> `mlsidecar.Install` against the live channel, trust store seeded only
> by `BakedReleaseAnchor()`, installed it `verified=true`. The step is
> enabled on darwin/arm64 builds and exits 1 on any index/key/sig/size/
> sha mismatch; the version lives in release.yml's workflow `env`
> (`KENAZ_ML_ENGINE_VERSION`). **Design deviation, recorded:** the step
> pins from the PROD engine channel for every harness env — kenaz-ml
> publishes stable versions only to prod (dev carries
> `<ver>-dev.<sha7>`, stage has no index), so the original per-env
> `cdn_base` would have failed every dev/stage harness build. The
> `-ldflags releaseSigningPubKeyHex` override is deliberately not wired
> (the step verifies against the `.pub` file; an unverified override
> would bypass it). Hermetic proof:
> `core/mlsidecar/published_release_test.go` over
> `testdata/engine-release-0.1.1/` (real index + `.sig`, PROVENANCE.md),
> which also fails if release.yml's pin drifts from the fixtures. The
> dated note on `PinnedEngineRelease` is deleted. Review follow-up #3
> below is partly addressed: the release step now cross-checks the pin's
> values against the published index and the served bytes at build time.
>
> **Note 2026-10-07:** kenaz-ml **0.2.0** was published 2026-10-08T00:39Z
> (prod `kenaz-ml/index.json`). The harness deliberately stays pinned to
> `KENAZ_ML_ENGINE_VERSION: '0.1.1'` until a fixture-refresh PR replaces
> `testdata/engine-release-0.1.1/` (index, `.sig`, PROVENANCE.md) and
> re-runs the pre-enable verification against 0.2.0 — bumping the env
> alone would fail `published_release_test.go`'s drift check, by design.

**Finding, ungated: trust anchors have no production revocation path.**
`TrustEngine.RemoveAnchor` and `TrustEngine.IngestRevocation` have zero
non-test callers, and `trustanchor.TrustAnchorAPI` exposes only
`ListAnchors`/`InstallAnchor`. `SeedAnchor` *respects* a tombstone
(pinned), but nothing in the product can create one today — so the
owner decision "rotation/revocation via the trust store" has a working
store half and no operator surface. `IngestRevocation`'s cache is also
in-memory only (`memRevocationCache`), so a revocation would not survive
a relaunch even if something called it. Not wired here: adding a
Remove RPC is a Wails-binding + frontend change outside this mission's
scope, and an unmounted RPC would just move the lie.

- **Blocker:** a product call on the anchor-management UI (where revoke
  lives, who may press it) — the Settings trust surface has never had
  one.
- **Owner:** alec. Resolves when a revoke action reaches
  `TrustEngine.RemoveAnchor` from a mounted surface (or fleet
  distribution grows a revocation message), with a persisted revocation
  store if key-id revocation (not just anchor tombstoning) is wanted.

**Review follow-ups recorded, not fixed (2026-10-04, branch review of
WP-H1..H5; WP-H6 fixed the blocking items — bounded sig/manifest/artifact
fetches and an overall install deadline).**

1. *The baked ML release key is trusted for ALL signature verification.*
   Anchors carry no purpose scoping: `Metadata["purpose"]` is
   descriptive only, and `VerifyManifestSignatures` accepts any live
   anchor — so a manifest signed by the engine-release key would also
   verify as a **bundle** install. Rotation also deviates from the
   spec's "rotation via PreviousKey" model: a new baked key seeds a
   second, KeyID-scoped anchor, and the rotated-away key stays a live
   anchor with no product removal path (see the no-revocation-path
   finding above — the two compound).
   - **Blocker:** purpose-scoped anchors are a `core/trust` model change
     (anchor ↔ verification-purpose binding consulted by
     `EngineVerifier`), plus a decision on whether seeding should
     `BeginRotation` an existing baked anchor instead of adding one.
   - **Owner:** alec. Resolves when engine verification only accepts
     anchors scoped to engine releases (and vice versa), and a
     rotated-away baked key is retired by the store's rotation model.
2. *Operators cannot see that an anchor is baked.*
   `trustanchor.toWire` drops `Metadata` and `InstalledBy`, so the
   `origin=baked_release_key` marking the seed writes never reaches the
   TrustAnchors RPC or any UI.
   - **Blocker:** a small RPC wire-shape addition + Wails binding regen +
     frontend display; out of this mission's scope.
   - **Owner:** alec. Resolves when the wire `Anchor` carries origin /
     installed-by and the trust surface renders it.
3. *`TestPinnedReleaseGen_NoDrift` guards shape, not values.* It
   catches a hand edit that breaks the render format or the publish
   contract (locator, channel kind, field validity) but cannot catch a
   well-formed value swap (another valid sha256/version). The signature
   check at install time is what actually binds the values. Comment
   softened accordingly in WP-H6.
   - **Owner:** alec. Informational; resolves if a release-time
     cross-check against the published index is added (the commented
     release.yml step's url/key_id asserts are a start).

### 2026-10-05 (engine port lanes, owner rulings A5.2/A5.3, harness branch `fix/engine-ports`) — update-pending identity is lexical; lane fallback has two accepted costs

1. **Update-pending identity is a lexical `exe_path` check in BOTH repos.**
   A lease-aware engine answering on a lane candidate whose self-reported
   `exe_path` is under this root's `versions/` but not `current` is
   classified *update-pending* and adopted (`engine.port` written — the
   agreed cross-repo contract; Kenaz mirrors it). Nothing re-hashes that
   binary: a process that merely *claims* such a path is adopted the same
   way. Containment in the harness (review F4): update-pending is never
   the Manager's verified port (`Manager.DialClient`), never leased, and
   `shutdownClient` never sends it the token — so no advice, label, or
   shutdown traffic reaches a lexically-claimed squatter; the cost is
   bounded to "one lane port is held and recorded". Matches kenaz PR #178's
   recorded protocol-gap follow-up.
   - **Owner:** that same follow-up (the engine-identity protocol gap —
     an engine-attested identity both clients can verify). The line goes
     when it lands.
2. **A legacy/foreign listener on a base port now coexists with our
   engine on the next lane** (e.g. a pre-lease engine on prod 7774 and
   ours on 7784): extra RAM for two engines; the "update Kenaz to share
   the ML engine" hint surfaces only when ALL lane candidates are foreign.
   Accepted (review F6; matches the legacy = foreign ruling). sigild dials
   prod 7774 directly and does not read `engine.port`, so a squatter on the
   prod base still strands sigild — pre-existing, owner-acknowledged
   (A5.3).
3. **Observe worst case** is LaneCount × `observeProbeTimeout` (5 × 2s =
   10s) on hanging candidates — cut from 5 × 5s by a per-probe deadline on
   the read-only Settings scan only. Informational.

### 2026-10-05 (model-harness-toolset-01MHTS001 WP02, finding H-1) — CLOSED: the B-3 scheduled-run tool allowlist was a containment boundary asserted, not enforced

**Class: containment boundary asserted, not enforced.** Owner ruling B-3
made a model-created schedule's `tool_allowlist` "the only boundary" (no
human review moment). The list was persisted (`sessions/0340`), required at
create time, and named by the policy file as enforced "per run" by "a
separate seam (harness-self-attach-01PMHS01's merged PermissionResolver)".
At fire time `chat_cron_engine.go` and `scheduledchat.RunNow` reduced it to
`has_tool_allowlist` for the execute gate; `ChatRunSpec` carried no list
and no resolver read one. A fired model-created run could call every tool.
Reachable only from onboarding sessions (the only kind that may call
`harness_write_create_scheduled_run`), which bounded but did not remove it.

**Fixed (wired, not deleted):** `ChatRunSpec` carries `CreatedBy` +
`ToolAllowlist` from the gate; `scheduler.ResolveRunContainment` combines
spec and re-read row, never widening (model + absent/empty/unreadable list
= DOES NOT RUN, checked in the dispatcher as well as by the gate; a user
row's unreadable list = every tool denied; a user row with no list =
unchanged). `LiveChatRunDispatcher` binds the run's session in
`ScheduledRunContainmentRegistry`; `cedarSessionKindResolver.Resolve` (the
session arm of the one merged resolver the chat adapter, slash dispatcher
and workflow tool gate share) checks it first and denies off-list tools,
recording a `blocked_permission_requests` row (family `tool`) plus a
`policy.blocked_permission_request` audit record per refused dispatch.
Subagent children inherit the parent's containment. Pins:
`core/rpc/scheduled_run_containment_test.go` (real cron fire path, real
sqlite, v0.89.2 upgrade snapshot for the pre-0340 user row),
`core/scheduler/containment_test.go`. The 2026-08-22 `CreateAsModel` entry
above is superseded (it has a production caller now).

**Accepted residuals:** (1) ~~builtin `kenaz__*` tools are still LISTED to
a contained run~~ — closed by security review M2 (2026-10-05): the
discoverer now filters builtins through the same probe-marked resolver
path as pool tools (`TestScheduledRunContainment_ListingShowsOnlyAllowlistedBuiltins`). (2) ~~a timed-out run's session
stays contained for the process lifetime~~ — superseded by security review
L4 (2026-10-05). The re-review (same date) narrowed it further.
Containment is released only when one of the run's OWN streams ends: the
dispatched stream, or a key-rotation redrive of it, which is linked through
`chat.AuthResumedPayload.PausedSubID`. Before that, ANY stream ending in the
session released it. That was a proven hole: a user opening the "Scheduled:"
session mid-run and finishing a turn freed the still-running scheduled
stream. A redrive stays contained while it runs and releases when it ends,
whether it starts before or after the dispatcher's timeout. A timed-out
session is released once its own stream ends. Pins:
`..._UnrelatedStreamInSessionDoesNotRelease`,
`..._RedriveBeforeTimeoutStaysContainedThenReleases`,
`..._RedriveAfterKeyRotationStaysContained`,
`..._TimedOutRunReleasedWhenItsStreamEnds`. A turn that is
never redriven keeps its containment for up to 24h (`containmentWatchMax`);
the watcher then stops and the session stays contained (fail-safe). Owner of
both: WP16
(`kenaz__schedule_chat`), which must re-read this seam before shipping.

**Gate question (tasks.md WP02): can any file-level gate see "a persisted
allowlist field with no runtime reader"?** No. The field HAD readers — the
two `len(rec.ToolAllowlist) > 0` sites — and that is exactly why the gap
survived: a reader that reduces a list's contents to its existence is
indistinguishable, to a grep or a knob-coverage registration, from one that
enforces it. "Is this read a decision on the contents?" is a semantic
question. The guard is the runtime pin (an off-list call in a really-fired
run is denied), which is now in place; no gate extension is filed because
none can be made non-vacuous for this class.

### 2026-10-05 (model-harness-toolset-01MHTS001 WP03, findings H-2..H-6) — harness-self tools that always failed; comments naming gates nothing evaluates

**H-2 — CLOSED (deleted, not wired).** `harness_read_get_status` and
`harness_write_install_mcp_recipe` were registered on the harness-self
server (`get_status` visible in every chat session, since reads are
permitted for every session kind) while `buildHarnessManagers` never
assigned `Managers.Status` / `Managers.RecipesWriter`, so every call
returned "not configured". No ledger entry existed; the gap was recorded
only in a code comment. Both tools, their handlers, the two Managers
fields and the `StatusReporter`/`StatusSnapshot`/`RecipeWriter` types are
deleted; tests that used `get_status` only as a representative read tool
now use `harness_read_list_providers`. Pin:
`TestRegisterAll_NoAlwaysFailingTools`; harness-self count is 13.
- `get_status` — **class: not trivially wireable; substitutes exist or are
  slotted.** It needs five unrelated sources (providers, installed MCP
  servers, sessions, projects, policies); the policy count has no source
  on this path. Providers and sessions are already readable
  (`harness_read_list_providers`, `harness_read_list_sessions`); projects
  and capabilities are slotted as `kenaz__list_projects` /
  `kenaz__list_capabilities` (this mission, WP06/WP08). **Owner:** WP08 —
  if a status summary is still wanted, it is a catalog tool over the
  WP06/WP08 reads, not a revival of this one.
- `install_mcp_recipe` — **class: product decision, escalated not
  guessed.** Wiring it means a model-triggered install, which is owner
  question **Q8** (spec §7, default "forbid until ruled"); the
  install-framework decision record also requires it to route through
  `install.Framework.Install` (consumer-confirmed), which its
  `{id, config}` schema (an inline config object) cannot. **Owner:** WP15
  (`kenaz__install_capability`, shares `Framework.Install`).
  **Blocker:** the Q8 ruling.
- Also corrected: `harness_read_get_onboarding_recommendations` described
  itself as "based on current state"; its handler returns a fixed list.

**Gate extended (I11, `check-builtin-tool-registration.sh` §6):** every
`Managers` field a harness-self handler nil-checks must have an `m.X =`
assignment in `core/rpc/harness_wiring.go`. It would have caught both H-2
tools. Planted proof:
`builtin-tool-registration/harness-self-manager-never-assigned`. It cannot
see a field assigned only on a branch that never runs in production — that
is a runtime question.

**H-3 — CLOSED for the comments; the four actions stay declared.** Four
Cedar actions were named in code comments as the gate for a builtin, and
nothing evaluates any of them. Comments rewritten to say "declared, not
evaluated" and name the real gates (the predicate dial + per-call
`use_tool` resolution, where a user forbid on the tool name does work):
- `ActionToolTasksMonitor` (`monitor/tool.go`, `builtins_wiring.go`
  predicate case, `types.go`) — passive read of captured output.
  Not evaluated, by choice: `use_tool` already gives per-call control.
- `ActionToolTodoWrite` (`todo/todo.go` said the tool "reports" it on
  every call; `types.go`) — session-internal state. Not evaluated, by
  choice, same reason.
- `ActionToolListSecrets` (`listsecrets.go`, `types.go`) — the same file
  also claimed per-call `tool.invoked` audit records, which do not exist
  (H-5); withdrawn. Not evaluated, by choice: the list returns reference
  names only, and secret *resolution* is separately gated and audited
  (`secret_reference.resolve`). **Owner if that changes:** alec.
- `ActionArtifactUpdate` (`types.go`, `posture.go`) — **this one SHOULD
  bind by its own design**: it sits in `PlanModeDeniedActions`, i.e. plan
  mode is meant to deny `kenaz__update_artifact`, and does not. Not wired
  drive-by: the plan-mode wrapper only reaches the agentgraph
  `env.Policy`, so evaluating the action at the tool would still not be
  plan-mode-aware (that is H-4). **Owner:** the H-4 follow-up mission
  (spec Q12). **Blocker:** Q12's ruling on widening plan mode to builtins.
  Dated 2026-10-05.
- Not touched (already listed as declared-not-evaluated, inventory §2.1):
  `ToolSkillInvoke`, `ToolSubagentMerge`, `ToolTasksCancel`,
  `ElicitDeferred`.

**H-4 — OPEN (filed, not fixed).** Plan mode does not bind builtin
`kenaz__*` tools: `cedar.WithPostureMode` wraps only the agentgraph
`env.Policy` (`use_tool`/`tool_exec` are not in `PlanModeDeniedActions`);
bash, the fs gate, the workflow and scheduled-chat views and the RiskGate
use unwrapped engines (inventory H-4), so the plan-mode deny set does not
reach `kenaz__bash`, `kenaz__write_file` or `kenaz__update_artifact`
through Cedar. This mission's own W-tier
tools check posture themselves (FR-G5). **Owner:** a follow-up mission per
spec Q12 (behaviour change for current users, so it needs the ruling).
**Blocker:** Q12. Dated 2026-10-05.

**H-5 — OPEN, deferred to WP04.** Builtin `kenaz__*` calls have no
per-call audit record; the only tool-dispatch record is
`KindToolConfirmDecision`, written only on the confirm-each ladder,
which builtins (auto_allow by default) never reach. Spec FR-A1 adds
`harness.tool.called` for this mission's W-tier tools in WP04 (the kind
does not exist yet). That will NOT cover the pre-existing builtins (bash,
write_file, …); that wider gap stays open. **Owner:** WP04 for the
harness toolset; alec for the existing builtins. Dated 2026-10-05.

**H-6 — OPEN, deferred to WP05.** The builtin registration tripwire walks
registered → predicate only (`TestBuiltinEnabledPredicate_AllRegistered
ToolsHaveExplicitCase`); predicate case → registration site is unchecked.
Spec FR-S1(d) puts it in WP05's new `check-harness-toolset-catalog.sh`
with planted proof `harness-toolset/predicate-case-without-registration`.
Not done here: case labels are package constants, so the check needs the
same const resolution WP05's gate builds. **Owner:** WP05. Dated
2026-10-05.

### 2026-10-05 (model-harness-toolset-01MHTS001 WP02 security review) — review findings on the containment seam

**H1 — CLOSED: a model-supplied session id overrode the real session.**
`kenaz__skill` accepted `session_id` in its arguments and passed it to
`slashcmd.Dispatch.RunModelInvoked`. Then `dispatch.go`'s kind:tool branch
re-stamped the tool-dispatch ctx with `toolloop.WithSessionID(ctx,
sc.SessionID)`, replacing the session the call was really running in. A run
contained to `[kenaz__skill]` reached `kenaz__sleep` by sending a forged or
foreign id, and a forged onboarding-kind id would also have passed the
session-kind arm for `harness_write_*`. Two fixes:
(1) the skill tool takes the session only from
`toolloop.SessionIDFromContext`, and its schema is `additionalProperties:
false` with `DisallowUnknownFields`, so a `session_id` argument is refused
with `invalid_args` instead of being silently ignored;
(2) the slash dispatcher uses the new `toolloop.WithSessionIDChecked`, which
only fills an empty ctx. A different id coming from data is refused with
`ErrSessionIDMismatch`. The human `UserRun` path, where the ctx carries no
session, is unchanged.
Pins: `TestSkillTool_SessionIsCtxDerived_ContainmentHolds` (the reviewer's
probe, now permanent; red on the pre-fix code),
`TestDispatch_KindTool_SessionContextCannotOverrideCtxSession`,
`TestTool_Call_SessionIDArgumentRefused`, `TestWithSessionIDChecked`.
`project_id`/`cwd` remain model-supplied skill arguments. They feed only
command lookup and the `{{cwd}}` template, not permission resolution.

**Sibling audit (as the reviewer asked):**
- `planmode/enter.go` and `planmode/exit.go`: ctx-derived
  (`SessionResolver` defaults to `toolloop.SessionIDFromContext`), with no
  session argument. Nothing to change.
- `saveartifact`: ctx-derived, with no session argument. Nothing to change.
- `updateartifact`: the session is ctx-derived (logging only), but the
  target is any `artifact_id`, with no ownership check against the session.
  **Cross-session targeting is designed-in:** artifacts have been a global
  library since `sessions/0332-artifacts-global-scope`. Writes append a new
  `artifact_versions` row, so they are non-destructive and earlier versions
  survive. What gates it: the `FSWriteEnabled` dial and per-call `use_tool`
  resolution, which now includes scheduled-run containment. The H-3
  `ActionArtifactUpdate` gap is above. **Constraint for this mission:** any
  W-session wrapper over artifacts must add the FR-G6 ownership check
  (`InScopeArtifact`) rather than copy this tool's posture. **Owner:**
  WP04 (ownership.go) / WP10.

**Re-review fold-ins (2026-10-05):**
- I11 §6 now strips trailing `//` comments, `/* */` blocks and string/rune
  literal contents (string-aware) before matching. Two more degenerate
  passes are closed: an assignment in a trailing comment, and one inside a
  block comment. Each has a planted proof.
- `core/workflows/runners.go` (model_turn and mcp_call) now uses
  `WithSessionIDChecked`. A run's `ParentSessionID` can no longer override
  a session already on the context. This path is not model-reachable today;
  WP17 would make it reachable, so it is closed now. Pin:
  `TestMCPCallRunner_ParentSessionCannotOverrideCtxSession`.

**L6 — deliberate deviation from tasks.md, recorded.** tasks.md WP02
said "User-created rows are unchanged". Two classes of user-created rows
now behave differently. This is deliberate:
- A **user row WITH a declared allowlist** is now contained to it. The
  view's own `CreateInput.ToolAllowlist` doc already said the list is
  "enforced against this schedule's runs"; before WP02 it was not. No
  shipped UI writes the field (`frontend/src` has no `toolAllowlist`
  writer), so no existing user row is affected in practice.
- A **user row whose allowlist column is corrupt** (non-empty but not
  decodable to at least one name) now runs with every tool denied, where
  before it ran unrestricted. A declared but unreadable boundary must not
  read as "unrestricted".
User rows with NO allowlist, which is every pre-0340 row and every row the
UI creates, are unchanged. That case is pinned by
`TestScheduledRunContainment_UserRow_NoAllowlistUnchanged` and on the
v0.89.2 snapshot by `..._UpgradedUserRowStillRuns`.

**Forward warning for WP17 (`kenaz__set_workflow_schedule`), from the
reviewer.** `ScheduledRunContainmentRegistry` is keyed by session, and
scheduled **workflow** runs have no session (`wf_sched_dispatcher.go`
dispatches through the workflow runner, not a chat session). This seam
therefore does **NOT** contain model-armed workflow schedules. WP17 must
not claim it does. It needs its own boundary on the workflow tool path:
`wfToolGate.authorize` / `wfMCPCallerAdapter`, which resolve with the
step's `ParentSessionID`, are empty for a scheduled workflow. Until it
has one, a model-armed workflow schedule is uncontained. tasks.md's
"through the WP02 seam" wording for WP17 is wrong as written.
**Owner:** WP17. Dated 2026-10-05.

**Known behavior (from the security review): user hooks are blocked in
contained scheduled sessions.** The hooks-kind-builtin path resolves
through the same per-session containment, so in a contained run a user
hook is refused unless what it dispatches is on the run's allowlist.
This is intended, not a defect: hooks are not a way around the boundary.
A schedule that relies on a hook must list what the hook dispatches.

### 2026-10-06 (bundle-key-rotation, review F3) — `FleetHealthChip` fetches fleet health once at mount and never refreshes

`frontend/src/shell/FleetHealthChip.vue` calls `client.settings.fleetHealth()`
in `onMounted` only. Every state it renders — including the new
`"unknown-key"` (`fleet.ErrSigningKeyUnknown`, bundle-key-rotation WP01) —
is whatever was true at app start. The config poller runs every 5 minutes,
so a key flip, a first-ever bundle delivery, or a recovery after an
"unknown-key" rejection is not reflected until a restart. The chip does not
lie about the state it fetched; it goes stale.

Justified, not wired, in the key-rotation PR: the fix is a polling or
event-driven refresh shared by the Settings fleet-health surfaces, a
separate design question (cadence, event source) the key-rotation change
should not settle in passing. **Blocker:** none technical — needs the refresh
design. **Owner:** alec — the Settings-health refresh follow-up deletes this
entry.

## Drained

### 2026-10-07 · CLOSED — project sync advertised an agent-memory class that shipped nothing (`memory-sync-01MEMSY01` WP01)

Class: **advertised sync that ships nothing.** `core/fleet/project_sync.go`
declared `ArtifactClassMemory` and `DefaultArtifactClassOptions()` returned
`Memory: true`; `ProjectSync_Toggle` enabled every project with the same
default (`core/rpc/views/contextsync/impl.go`). Verified in the 2026-10
audit (§5.3): no caller of `ProjectSyncer.AppendEvent` ever produced a
`memory`-class event, `EnableSync`'s backfill was passed `nil`, Fleet has
no project-memory route, and the project UI's class list never offered
memory. The `memory: true` flag in the RPC shape (`ArtifactClassOptionsView`)
was therefore a promise read by nothing.

- **Drained — deleted** (live substitute: the user-private memory lane,
  `core/fleet/memory_sync.go`, same mission WP07, against Fleet's
  `/api/v1/memory/*`). Removed the class constant, the `Memory` fields of
  `ArtifactClassOptions` / `ArtifactClassOptionsView` / `ProjectSyncOpts`,
  the two adapter copies in `core/rpc/context_sync_wiring.go`, and the
  hand-matched `memory` member of `frontend/wailsjs/go/models.ts`.
- **Persisted blobs:** a keyring options blob written with `"memory":true`
  still decodes (unknown key ignored) — pin
  `TestArtifactClassOptions_LegacyMemoryKeyIgnored`.
- **Not fixed here (pre-existing, separate):** `frontend/src/lib/types.ts`
  `FleetArtifactClassOptionsView` is `{classes: Record<string, boolean>}`
  while the Go wire shape is flat `{notes, binaries}` — the project landing
  page's class toggles round-trip a shape the backend does not decode.
  Owner: the project-registry harness work (fleet P1–P4), which rebuilds
  that panel.

### 2026-10-04 · CLOSED — chat run ids were a per-process counter written into a persistent log (`agentgraph-settings-linkage-01DOGF0D` WP02)

Found by the spec's verification pass, not by a user: no surface linked
a historical chat turn to its run yet, so nothing exercised it — the
mission that adds that link is what would have exposed it. Class: **a
latent identity defect behind a missing consumer.**

- `chat_runner.go` minted run ids as `r.nextID++; "chat-%d"` — reset to
  0 on every boot — and used the same string as the kernel `RunID`
  written to `agent_graph_events`, which `buildAgentGraphEventLog` backs
  with `NewSQLEventLog` (persistent). Today's `chat-1` and yesterday's
  `chat-1` shared one run id; `MaterializeRun("chat-1")` would have
  projected several unrelated turns as one graph.
- **Drained — wired** (WP02): `newChatRunID` returns `chat-<ULID>`
  (`core/event.NewULID`); sub id and kernel run id stay one string; the
  counter is gone. Pin: `TestChatRunID_UniqueAcrossRestartsOnSQLEventLog`
  (two runner instances = a simulated restart, one real sqlite log, the
  production `0309` DDL). Red proof: `newChatRunID` reverted to the
  constant `"chat-1"` → FAIL.
- **Pre-fix rows are kept** (an audit trail, no destructive migration)
  and are never linked: no `session_turn_runs` row exists for them, so
  the transcript shows "Run graph not recorded for this turn". A direct
  `MaterializeRun` of a legacy id whose log holds several starts refuses
  with `agentgraph.ErrRunIDReused` instead of merging turns (pin
  `TestMaterializeRun_RefusesRunIDReusedAcrossRestarts`); resume and the
  overflow redrive — one run continuing under one id — still
  materialize as one graph.
- **Why the suite never saw it:** every agentgraph test of run identity
  ran on `NewMemoryEventLog`, which dies with the "process". Blind spot
  #2 in its event-log shape; WP-PI moved the identity pins to real
  sqlite and recorded which memory-log tests stay and why.

### 2026-10-04 · CLOSED — every user chat turn had two writers; the second was the only path into fleet sync (`chat-single-writer-01DOGF0G`)

Dogfood finding F12 ("i keep seeing messages i send in chats get
duplicated"). Class: **rival writer**, plus a **comment asserting an
invariant nothing enforced**.

- **Two writers for one row.** The chat surface persisted each turn via
  `Sessions_AppendMessage` / `SendMessageWithBlocks`; `LLM.StartStream`
  then read that row's text back and `ChatRunner.StartStream` re-appended
  it through the HistoryWriter, anchoring the turn's span on the copy.
  Every typed turn since the graph-chat migration (5fe2fbcf, 2026-04-27,
  v0.1.x — the dev profile's oldest surviving pair is 2026-06-07) was
  stored twice (dev profile: 40
  pairs, 0.7–17 ms apart), and every history read handed the model each
  user message twice. Text+image sends were doubled too (the flattened
  text was re-appended text-only); only image/document-only sends escaped.
  The runner's comment ("the multimodal send, where the frontend already
  landed the user's row") described a property of ALL sends as if it held
  for one. **Drained — wired** (WP02): the runner takes a `UserTurn`
  reference (id + text + announce) and has no code path that writes a user
  row; the span is the caller's row, `TurnSpan.LatestUserMessageID` the
  only fallback. Pin: `TestChatTurn_UserMessageStoredOnce_AppendThenStartStream`
  (`core/rpc`, real sqlite, append → StartStream; verified failing on
  `b8079d48`).
- **The duplicate was load-bearing for fleet sync.** The re-append was the
  ONLY path by which a user turn reached `SessionSyncer.AppendEvent`
  (`llmHistoryWriter.AppendEntry` → `syncHook`); image-only sends never
  synced at all. **Drained — wired:** `llmHistoryWriter.AnnounceUserTurn`
  (`chat.UserTurnAnnouncer`) emits the same `{id, role}` event for the
  existing row, once per fresh turn, never on the keychain redrive. Pinned
  by assertion (d) of the test above and by the key-rotation redrive test
  (one announcement across start + redrive).
- **Assistant-side twin.** Not any of the spec's three candidates: the
  backend-error `PartialPersister` path re-persisted text the move journal
  already owned (a parked last fire, or an absorbed `final`) as a
  kind-less failed row. **Drained — wired** (WP04):
  `turnJournal.UnpersistedTail`. Pin:
  `TestChatRunner_BackendErrorAfterCompletedFire_WritesTheAnswerOnce`.
- **Stored history.** `sessions/0341-dedupe-user-turns` (WP05) removes
  existing pairs pair-type-aware (an image-bearing row always survives)
  and the assistant twins; populated-snapshot test
  `TestMigration0341_DedupesDoubledTurnsAgainstUpgradedDatabase`.
- **Why the suite never saw it:** frontend tests fake `startStream`;
  backend chat-runner tests called `StartStream(…, userMessage)` directly
  and skipped the frontend append. Blind spot #2 in a new shape — the
  fixture bypassed the OTHER writer.
- **Residual, not drained — recorded so the next sweep does not re-find
  it as new:** (1) equal-content assistant rows that are BOTH classic
  failed partials (pre-0336 periodic-flush residue; 0337's strict-prefix
  rule leaves them; neither is "the answer") — 4 on the dev profile.
  (2) whole-turn partials from before finding #105 whose text differs from
  the move they duplicate. (3) the destructive-migration gate sees a
  procedural `Up` only through its `UpSource` text: a named-function `Up`
  whose UpSource omitted the word DELETE would be invisible (0337 and 0341
  both state it). **Owner:** alec — a follow-up to the chat-turn-integrity
  repair family for (1)/(2); a gate change for (3) dates or closes it.

### 2026-10-03 · CLOSED — graph file nodes bypassed fs.Gate; a corrupt user policy failed the graph path OPEN (`graph-fs-gate-01GFSG01`)

Two findings from the 2026-10-03 peer-session verification pass (the
kenaz-ml-1c session's read-only pass against main `d4957b04`,
spot-checked by the implementing session), continuing the August
node-vs-tool gate asymmetry finding. Owner rulings 2026-10-03: (1) route
graph file nodes through fs.Gate — one permission model everywhere, no
second unattended-safety model; (2) a corrupt user policy fails CLOSED.

- **Graph `write_file` / `read_file` never reached `core/tools/fs.Gate`.**
  `core/agentgraph/exec_state.go` checked only Cedar
  `file_write`/`file_read` + `state_write`/`state_read`, and
  `default_policy.cedar` permits `file_write`/`file_read` for every
  resource (its comment calling user-confirmed roots "the typical
  targets" was a hope, not enforcement). An unattended graph run's
  `write_file` wrote any path with no prompt and no confirmed-roots
  check; the identical write via `kenaz__write_file` went through
  fs.Gate. The split was recorded as deliberate in
  `core/policy/cedar/hooks.go` (`trust-surfaces-that-fire-01PMZ202`
  WP18); ruling (1) supersedes it and that docstring now says so.
  **Drained — wired** (WP02): optional seam `agentgraph.FileAccessGate`,
  consulted after both Cedar checks; `PolicyGateAdapter` implements it
  over the SAME `*corefs.Gate` the fs builtin tools use, late-bound in
  `rpc.New` via `SetFileGate(stack.fsGate)` (a shared slot, so
  `WithPostureMode` copies see it). Unattended runs deny through the
  existing `CedarPrompter` → `Registry.RequestInteractive` runposture
  check. Pin: `TestGraphWriteFile_UnattendedOutsideConfirmedRoots_Denied`
  (real API + real Kernel; verified failing on `d4957b04`).
- **Corrupt `<DataDir>/policy/*.cedar` → graph path fail-open.** The
  spec located this at `api.go`'s `AllowAll` fallback for a nil engine;
  the live tree showed that was the smaller half. `Engine.Reload` never
  aborts on a per-file parse failure (the embedded bundle always
  parses), so a corrupt user file did not drop the engine — it silently
  dropped the user's rules while the permissive embedded defaults kept
  running, with only a per-file `ParseErr` in `ListPolicies` as a trace.
  **Drained — wired** (WP01): `cedar.FailClosedOnLoadError` (bound for
  the graph path only) denies `file_read`/`file_write`/`state_write`/
  `tool_exec`/`use_tool` while `Engine.UserPolicyLoadError()` is
  non-nil, records each denial in the engine's decision log, and is
  re-evaluated per call so a fix + reload lifts it. A nil engine with a
  DataDir (construction failure) also fails closed; no DataDir at all
  (nil-Core chassis) stays `AllowAll` — absence, not corruption.
  Settings › Policy shows a banner naming each failing file and its
  parse error. **Blast radius, stated outright:** chat runs on the same
  kernel Env, so the `use_tool`/`tool_exec` denial covers EVERY chat
  turn's tool calls — interactive and scheduled chat alike — not just
  library-graph runs, until the file is fixed and reloaded. Parse
  errors are attributed by source (WP05), so a corrupt user file that
  shares a name with an embedded default or a harness/graph snippet
  is still reported and still fails closed. Pins: `TestGraphPolicy_CorruptUserPolicyMidSession_FailsClosed`,
  `TestGraphPolicy_CorruptUserPolicyAtBoot_FailsClosed` (verified failing
  on `d4957b04`).
- **Behaviour change (WP04, reviewer F3):** `Engine.Reload` used to
  rebuild from embedded + disk only and silently uninstall everything
  `LoadHarnessSnippets` had added — the harness-self and
  graph-authoring policies, and fleet team rules applied through
  `ApplyCedarDelta`. Those now SURVIVE a Reload. Arguably a fix (a
  team forbid no longer evaporates when a user reloads), recorded here
  because it changes what is enforced after a reload.
- **Residual, not drained — recorded so the next sweep does not re-find
  it as new:** the SHARED engine's other consumers (memory write,
  workflows, scheduled chat, session export, …) keep the documented
  fail-open posture on a corrupt user policy
  (`TestCedarHoist_CorruptPolicyMidSession_StaysFailOpen`); ruling (2)
  named the graph path only. Network/exec node gates and the
  model-authored-graph `write_file` emission policy (01PMGA01) are out
  of this mission's scope. **Owner:** alec — a follow-up ruling on
  whether fail-closed extends engine-wide closes or re-dates this line.

### 2026-09-12 · CLOSED — `structured-output-is-reachable-01PMZE14`, all six owed ledger entries

The mission's own tasks.md Appendix names six findings this mission owed
the ledger "on merge." None had been recorded when this mission was
triaged and finished against `release/v0.78.2` — UNIT-0/UNIT-1/UNIT-2/
UNIT-4/UNIT-5 had squash-merged via PR #299 (tag `v0.65.0`) and UNIT-6
via PR #323, but the ledger entry itself was never written by either
landing. Recorded now, all six, with the disposition each actually got
(verified against the live tree, not copied from the spec):

- **`ModelAttrs.JsonSchema` — authored end to end, dropped at the
  executor** (spec §1.2). **Drained.** `core/agentgraph/seams.go`'s
  `LLMRequest.ResponseSchema`, `exec_compute.go`'s marshal, and
  `core/rpc/views/agentgraph/chat/llm_provider_adapter.go`'s translation
  to `GenerationRequest.ResponseFormat` are all live (WP02, commit
  `ca605714`). `core/wiring/knobcoverage` sees the whole
  `agentgraph.ModelAttrs` struct (WP03, commit `03b0d68f`) —
  `TestKnobCoverage_ModelAttrs` is the gate; a planted removal of the
  `JsonSchema` registration reddens it.
- **`llm.StructuredOutputAdapter` — no dispatcher; the documented
  fallback does not exist** (spec §1.4). **Narrowed, not drained** (WP08,
  commit `6c1480dd`, disposition (b) per E-002's default). The doc
  comment at `core/llm/llm.go:353-388` now states plainly the interface
  has no dispatcher and warns against a fifth `var _
  StructuredOutputAdapter` assertion — verified live;
  `TestStructuredOutputAdapterInterfaceNarrowing`-shaped coverage pins
  exactly four implementors (anthropic/openai/openrouter/bedrock),
  gemini deliberately absent. The fallback path itself remains
  unbuilt and unowned (E-002 named no owner) — that residual is real
  but is a design deferral recorded in the interface's own doc comment,
  not a silent gap.
- **`structured.SchemaHash` + `audit…SchemaHash` — two orphans, each the
  other's only reason to exist** (spec §1.4a). **Drained**, in this
  landing (WP06, below) — `core/llm/registry/structured_stream.go`'s
  `emitAudit` is `structured.SchemaHash`'s first production caller, and
  `LLMStructuredResponsePayload.SchemaHash` now has a real assigner.
- **`RequestKnobs.ResponseFormatMode` / `.JSONMode` +
  `openaiwire/base.go:77-79`'s false docstring** (spec §1.5). **Dated
  justification**, not wired (WP07, commit `8a603ce8`; E-003's
  recommended default). `core/llm/openaiwire/knob_coverage.go` registers
  both as `RegisterDeferred`, each naming a real blocker and owner —
  updated again during `model-settings-reach-the-model-01PMZ101` UNIT-6
  to record that the *writer* half of the original blocker landed
  (`Sessions_SetKnobsDefault`) but the *consumer* half (an override
  branch in `openaiwire/body.go`) is still open and is out of scope for
  this mission specifically because `body.go` is Z101 territory
  (`CLAUDE.md`/plan.md Rule 2 — a commit here may not touch it). The
  docstring itself no longer claims a wire that does not exist.
- **`audit.KindLLMStructuredResponse` — declared, never emitted** (spec
  §1.6). **Drained** by this landing's WP06: `llmregistry.Options` gained
  an `Audit contextaudit.Emitter` field, wired at
  `core/rpc/api.go`'s `newLLMStack` construction site via
  `&acpAuditBridge{impl: a.auditImpl}`, and
  `core/llm/registry/structured_stream.go`'s `Final()` emits the kind
  with the real `Attempts` count, `ValidationOutcome`, and
  `structured.SchemaHash(schema)` on every call that reaches schema
  validation. **Correction to the spec's own §1.6/D-6, recorded so the
  next reader does not carry the stale claim forward:** at spec-writing
  time (2026-08-19) the audit event log had no durable backend
  (`MemoryBackend` only, `RegisterMigrations` uncalled) and D-6 required
  the WP06 commit body to say so. `audit-that-tells-the-truth-01PMZA10`
  landed on this same release branch since then (`c6f40bb4` through
  `7b0a95b2`) and wired a real sqlite-backed store
  (`eventlog.NewSQLBackend` + `audit.WithStore`) into `a.auditImpl`
  whenever a real `storage.DB` is available. So as of this landing, a
  `KindLLMStructuredResponse` event genuinely reaches disk in a
  production build via `audit.API.Push` → `store.AppendComputed` — it is
  **not** still writing into a ring buffer that evaporates on process
  exit. See `core/llm/wp_pi_persistence_integrity_test.go`'s addendum
  for the full citation trail.
- **`coverage_registry.yaml`'s false bedrock `unsupported:` row** (spec
  §1.8). **Corrected** (WP05, commit `c20edafc`) — the bedrock
  `ResponseFormat`/`JSONMode` rows now cite the real wire-shape test
  functions instead of the false "not wired at generic adapter layer"
  string; gemini rows were added to the same file. The
  adapter↔capability-row parity gate itself (G-3) landed separately as
  WP09 (PR #323, commit `e17b67ad`) — `core/llm/registry/
  wp09_g3_capability_row_parity_test.go` and
  `core/llm/bedrock/wp09_g3_row_parity_test.go` are its planted-violation
  proofs.

Also recorded and **not** fixed, per the mission's own tasks.md
Appendix (unchanged by this landing, still true): four copies of the
OpenAI `response_format` translation exist
(`openaiwire/body.go`, `openai/openai.go`, `openrouter/openrouter.go`,
`azure/adapter.go`) — consolidating them is a refactor the mission
explicitly scoped out, not a lie; and `bearer.go` (bedrock's REST
transport) still has no `JSONMode` handling while the SDK transport
does.

Mission fully closed with this entry: UNIT-3 (WP06, audit emitter) and
UNIT-7 (WP10, review gate + router request a real schema, with
`ErrCapabilityUnsupported`-gated degrade per D-9) were the two units
still open when this pass started; both landed in the same change that
added this entry.

### 2026-09-12 · CLOSED — the background-task subsystem has no producer

The original entry (2026-08-14, above) found `core/tasks` fully built
(SQLite store, ring buffers, boot-time orphan recovery, four RPCs, a
retained-but-unmounted Settings panel) and structurally unable to run:
nothing ever called `Registry.Register` because
`bash.Options.BackgroundSpawn` had no production assignment, and
`Registry.StdoutWriter`/`StderrWriter` had no callers because
`spawnBackground` started the process before a task id existed to
attach them to. A-13/A-7 ruled: build it.

`subagent-control-and-background-tasks-01PMZB11` did, across five units
verified against the live tree (not copied from spec):

- **UNIT-1** (`1ce7ea13`) recorded A-13's corrections and confirmed
  which of its premises were still true against the merged base.
- **UNIT-2** (`ef0eecb7`) registered the `core/tasks` migration through
  the real `core/storage/migrations` framework with a reserved version
  block — the `tasks` table now exists on every install, including
  upgraded ones (FR-001; re-verified by the `upgrade-path` CI job
  against the `v0.65.0` snapshot, per UNIT-PI above).
- **UNIT-3** (`44eaf995`) attached the task registry to
  `core/tools/bash`'s background-spawn path with a restructuring that
  allocates the task id BEFORE `cmd.Start()`, so `StdoutWriter`/
  `StderrWriter` can actually attach (FR-002/FR-003) —
  `run_in_background: true` now produces a real row with real
  captured output.
- **UNIT-4** (`766d917b`) wired `Registry.Options.HookFirer` in
  production, so `hooks.EventBackgroundTaskComplete` fires on every
  terminal task (FR-004) — closing the exact gap hooks-fire-sites
  finding #85 (also landed this release) named.
- **UNIT-5** (`7fbfdc86`) registered `kenaz__monitor` with a real
  predicate case, removing it from both
  `i11-unregistered-builtin-tools.txt` and `i7-orphan-packages.txt`
  (FR-005) — a registered `kenaz__monitor` no longer returns an empty
  `lines` array forever, because UNIT-3 gave it real output to read.
- **UNIT-11** (`1062fad1`, squash-merged `v0.71.0` as `620c048c`)
  restored the Settings → Tasks nav entry and mounted `TasksPanel.vue`,
  `BackgroundTaskChip.vue` and `TaskOutputViewer.vue` (the last had zero
  importers at all before this), per A-13's ruling and the parked
  entry's own conditional above. `SettingsTabsNav.spec.ts`'s pinned
  absence assertion was inverted, not deleted, per the mission's own
  convention for that class of test.

**Disposition: Drained**, not narrowed — every producer gap the
original entry named now has a real assignment, and the parked UI
(panel + nav entry) was remounted in the same ruling's conditional,
not left dangling.

### 2026-09-12 · CLOSED — live tools whose only UI is unmounted, sub-agent half

The original entry (2026-08-14, above) found `SubagentTab.vue` +
`SubagentBudgetMeter.vue` with zero importers and, at the time, no
backend to import them for either: `kenaz__subagent_dispatch` was
itself statically unreachable (the `var subagentSeam
agentgraph.BranchSeam // nil` dead-branch shape UNIT-12 below now has
a permanent CI gate for), and the tab's four control emits had no RPC
counterpart at all. A-13 ruled: build, not delete.

Closed by three more units on the same mission, verified against the
live tree:

- **UNIT-8** (`40f2e2a8`, `4d2ca708`, `fd938980`, `5feeb930`) landed
  `Subagent_Abort` / `Subagent_Steer` / `Subagent_Pause` /
  `Subagent_Resume` as real Wails-bound RPCs, each gated by a Cedar
  action in the `tool.subagent.*` family and each writing exactly one
  audit record per call that actually changes state (idempotent
  re-calls write none) — `TestAPI_{Abort,Steer,Pause,Resume}Subagent_
  DeniedByRealCedarPolicy` pin the negative half against a REAL
  `cedar.Engine`, not `cedar.AllowAll{}` (FR-008).
- **UNIT-9** (`41d15cd4`) gave `SubagentBranch`'s six fields
  (`subagentStatus`, `profileId`, `tokensUsed`, `budgetTokens`,
  `elapsedS`, `budgetTimeS`) real producers on `Branches_List` /
  `Branches_GetStatus`: `subagentStatus` off the tracked
  `core/tasks.Task` (mapped onto a strict subset of the frontend
  union — pinned by `TestSubagentStatusValuesAreInTSUnion`),
  `tokensUsed` off the child session's real `usage.Manager` aggregate
  (the same per-turn accounting token-cost-telemetry already writes),
  `profileId`/budgets off metadata `BranchSeamAdapter.Fork` now records
  against the branch id whenever the dispatch carries a `ProfileID`
  (FR-009). `TestAPI_ListBranches_SubagentFieldsPopulatedAndOmitted`
  proves both the positive half and AC-11's required negative half (an
  ordinary branch created through the identical `CreateBranch` call
  gets none of the six), with a manually-verified mutation proof.
- **UNIT-10** (`2e920f7e`) mounted `SubagentTab.vue` from
  `SessionsView.vue` — the natural owner, since it already renders
  `BranchSidebar` and owns the active session's transcript — gated on
  a real `activeSubagentBranch` computed derived from `BranchSidebar`'s
  own `Branches_List` rows (UNIT-9's fields), not a placeholder. The
  four emits reach UNIT-8's RPCs through new
  `client.branches.{abort,steer,pause,resume}Subagent` methods.
  `SubagentTab.spec.ts` asserts the status pill's RENDERED TEXT for all
  six `SubagentStatus` values and the budget meter's rendered
  percentage off real `tokensUsed`/`budgetTokens` — not prop
  pass-through (the `?role=`-blank-pill trap this ledger's 2026-08-14
  entry on `SearchModal` names).

**Disposition: Drained.** The todo half of the original entry is
explicitly **not** touched by this closure — see the correction left
in place on the original 2026-08-14 entry above.

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
