# Wiring audit

**Mission**: `wiring-integrity-01PMAG04`. This document is the durable record FR-004 requires:
a re-runnable methodology plus the disposition of every surface the 2026-07-27 multi-model
audit and its 2026-08-12 follow-up confirmed. It is not a changelog — when a disposition
changes (an item gets wired, or a deferred item's reason stops applying), edit the row in
place and note the commit that changed it.

## The `//wiring:deferred` directive

A surface (a Go interface, a struct field, an `Outputs["k"]` assignment, an event kind, a
node kind) that has no consumer *on purpose* carries a directive comment on the line
immediately above the declaration or write site:

```go
// wiring:deferred(<reason>)
type PromptTemplateSource interface { ... }
```

Grammar (enforced by `core/agentgraph/wiring_directive_test.go` and consulted by the
`check-output-ports.sh` / `check-seam-implementers.sh` guards):

```
^[[:space:]]*//[[:space:]]*wiring:deferred\(.+\)[[:space:]]*$
```

(POSIX character classes, not `\s` — BSD `grep` on macOS dev boxes doesn't support `\s` in
`-E` mode. The reason group is greedy (`.+`, not `[^)]+`) so a reason that itself contains
parentheses — e.g. "...deduped (below) is the only field..." — still matches through to the
line's final closing paren instead of stopping at the first one.)

- Exactly one directive line, immediately preceding the declaration it covers (no blank
  line between the two — a script parses "the next non-comment line" as the covered
  symbol).
- The parenthesised reason must be non-empty free text. Prefer citing the mission that owns
  the eventual wiring (`wiring:deferred(needs versioned-model-profile-01PMDL04 WP02+)`) or
  the reason it's a deliberately-unused capability
  (`wiring:deferred(capability library — see spec §2 non-goals)`).
- The directive is documentation + a machine-checkable marker. It does not change runtime
  behaviour and is not itself enforcement — the guard scripts are what turn "declared" into
  "CI-checked."

This is the mechanism spec §3.1 calls for: it makes deliberate non-wiring *declarable*, so
the `sleep` / `subagent_dispatch` shape (a node kind that is correctly unreachable through
`newExecutorRegistry()` because it dispatches as a builtin tool, not a bug) stops being
textually identical to an oversight.

## Re-run methodology (read this before trusting a guard's "clean" output)

The 2026-07-27 audit's rawest pass — a Go-only grep for `Outputs["k"] = ...` writes with no
matching `Outputs["k"]` read — returned **18** hits. Only **6** were real. The other 12 were
read through one of two channels no literal-string grep sees:

1. **YAML `condition:` expressions.** `evalBranchExpr` does a dynamic key lookup against the
   port-values map at runtime — a graph author can write `condition: "should_replan == true"`
   in `chat_default.yaml` and that *is* a read of `Outputs["should_replan"]`, but no Go
   symbol references the string `"should_replan"` anywhere, so a naive grep misses it.
2. **The frontend.** Several output ports are consumed only by `frontend/src/**` (TypeScript/
   Vue), not by any Go code path — a Go-repo grep is blind to this by construction.

**Any re-run of this audit, and specifically `check-output-ports.sh`, must reproduce all
three passes**: Go reads, YAML `condition:`/attr string literals across every shipped graph
in `core/rpc/views/agentgraph/library/*.yaml` and `testdata/`, and a frontend grep. Skipping
the YAML or frontend pass reintroduces the 3x false-positive rate — the auditor "fixes" the
noise by deleting live code, which is exactly the risk FR-005 exists to prevent.

To re-run the full audit by hand:

```bash
# 1. Node kinds with no registered executor (should be exactly the builtin_tool-
#    dispatched kinds — cross-check against manifests' dispatch: field, WP01).
comm -23 \
  <(grep -oh 'NodeKind[A-Za-z]*' core/agentgraph/spec_test.go | sort -u) \
  <(grep -oh '&[a-zA-Z]*Executor{}' core/agentgraph/executor.go | sort -u)

# 2. Env fields with no reader outside their own definition site.
for f in $(grep -oP '(?<=\t)[A-Z][A-Za-z]*(?=\s+\*?[A-Za-z.]+$)' core/agentgraph/executor.go); do
  n=$(grep -rn "\.$f\b" core/ --include='*.go' | grep -v '_test.go' | wc -l)
  echo "$n  $f"
done | sort -n | head -20

# 3. Output ports written but never read (the 3-pass version — see
#    scripts/ci/check-output-ports.sh, which automates exactly this).
bash scripts/ci/check-output-ports.sh --report   # prints the raw+adjusted counts

# 4. autonomy.ResolvedKnobs fields with a registered runtime consumer
#    (generalised in WP07 — see scripts/ci/check-knob-coverage.sh).
go test ./core/agentgraph/... -run TestKnobCoverage -v
```

## Confirmed inventory + disposition

Items 1, 2, 9, 10 are owned by sibling missions (`autonomy-knobs-live-01PMAG02`,
`agentic-turn-routing-01PMAG01`, `turn-context-runway-01PMAG03`) — this mission tracks them to
closure, it does not re-implement them. This mission (`01PMAG04`) owns 3–8, 11, 12. Item 12
(escalate/ladder system prompts) shipped as WP00 in PR #283, ahead of this mission's other
WPs.

| # | Surface | Disposition | Notes / evidence |
|---|---|---|---|
| 1 | 6 of 7 autonomy knobs (`AskProceed`/`AskNever`/`ErrorAdapt`/`RecapBrief`/`DestructiveConfirm`, `AutoApproveFamilies`) | **shipped — 01PMAG02 + 01PMAG05 (2026-08-13)** | All seven knobs have live consumers registered in `core/wiring/knobcoverage` and enforced by `check-knob-coverage.sh`. The confirm-each gap closed with the real modal flow (ConfirmBus + skip-set); the characterization tests were rewritten as real assertions. (Row previously said "owned elsewhere / pending its own modal-flow mission"; refreshed by the adversarial review.) |
| 2 | `should_replan`, `doom_loop_hits` | **shipped — 01PMGX01 WP11c (2026-08-13)** | `exec_dispatch.go` writes; `chat_default.yaml`'s `replan_check` decision + router replan route now read the signal and drive the `recover` escalation ladder. (Row previously said "no reader yet"; refreshed by the adversarial review.) |
| 3a | `child_session_id` (`exec_control.go:915`, branch fork) | **deferred** | The frontend's `BranchSidebar.vue` reads `childSessionId` from the `branches` DB table (`core/conversation/store.go`) via RPC, and the same value is *also* emitted on `EventBranchFork`'s `child_session` field (`exec_control.go:901`). The graph `Outputs["child_session_id"]` port itself has no Go, YAML, or frontend reader — it is redundant with two channels that already carry the value, not missing a channel. Marked `wiring:deferred` rather than deleted (no positive reason to delete a port a future YAML `condition:` might want). |
| 3b | `summary_msg_id` (`exec_control.go:1015`, merge) | **deferred** | Same shape as 3a: `EventBranchMerge`'s `summary_msg_id` field (`exec_control.go:1003`) already carries the value to any event consumer. No reader of the `Outputs["summary_msg_id"]` port anywhere. Marked `wiring:deferred`. |
| 3c | `model_used` (`exec_compute.go:1223` escalate; `exec_escalation_ladder.go:166` ladder-escalate rung) | **wired (deleted the redundant port)** | Both call sites already emit `EventEscalateTriggered` with `target_model: a.TargetModel` — the *same* value under a different key, one line below the `Outputs["model_used"]` write. The output port was never read anywhere outside its own characterization tests. Positive no-consumer proof: repo-wide grep for `Outputs["model_used"]` reads returns zero non-test hits. Deleted both writes; the two tests that asserted on the port now assert on the event payload's `target_model` field instead (same signal, correct channel). |
| 3d | `plan_text` (`exec_compute.go:1105`, planner) | **deferred (capability library)** | No shipped graph (`chat_default.yaml` or any other library graph) includes a `planner` node — this is the §2 non-goal carve-out: an available-but-unused *node kind*, not a forgotten wire. `plan` (the typed `Message` output) is the port a future planner-using graph would read from; `plan_text` is a plain-string convenience alongside it. Marked `wiring:deferred`. |
| 4 | Manifest `executor:` field never validated | **fixed — WP01** | Superseded by a `dispatch: graph \| builtin_tool` discriminator, validated in both directions against `newExecutorRegistry()`. The `executor:` field itself is *kept for one release cycle* as an unvalidated human-readable hint (`nodes/manifest.go` documents this), not deleted — "replaced" here means the discriminator is the load-bearing field, not that `executor:` is gone. See §"WP01" below. |
| 5 | `PromptTemplateSource` has no production implementer | **deferred (self-declared)** | `prompt_render.go:16-19` already explained why; now carries `wiring:deferred(needs versioned-model-profile-01PMDL04 WP02+ for a live ModelProfile lookup)`. `env.PromptTemplates` stays nil until that mission lands an adapter. |
| 6 | `env.MergeSuggester` never read | **deferred** | `branch_merge_suggester.go` is fully implemented (terminal-token + idle-timeout heuristics) and unit-tested, but no production wiring site (`core/rpc/views/agentgraph/env_deps.go` or elsewhere) ever constructs one and assigns it to `Env.MergeSuggester`. Wiring it means a real feature — an RPC event stream + a frontend "merge?" toast — not a one-line fix, so it does not belong in a triage WP. Marked `wiring:deferred(needs a merge-suggestion RPC + frontend toast; heuristic is ready, no consumer wiring exists yet)`. |
| 7 | `EventDialOverridden` emitted, no Go reader | **deleted — 01PMGX01 WP17** | Correction on the correction: it was never emitted at all. The 2026-07-27 audit read the declaration as a wire; re-checking found **zero** `AppendKind(..., EventDialOverridden, ...)` call sites anywhere in `core/` and no frontend reader (`TraceView.vue` renders `Message.actualProvider`/`actualModel`, a typed field populated by a different mechanism, not a generic event-kind reader). It was marked `wiring:deferred` in the first pass and Phase 8 came back to it with a deadline. **Deleted** on 2026-08-13 rather than deferred again. Wiring was considered and rejected: dials really are overridden (`applyMaxTurnsDial` / `applyReasoningBudgetDial` rewrite the graph spec every turn) but both run in the CHASSIS, before `Kernel.Run`, mutating the spec rather than acting inside a run — there is no `EventBatch` at that point — and no consumer exists on the other end either. An event kind with no emitter and no reader is a promise the EventLog is not keeping. The rationale is preserved at the deletion site in `core/agentgraph/events.go` so the next person to want dial auditing adds the kind back together with its emit site and its consumer. |
| 8 | `reasoning_budget_tokens` plumbed, never set | **shipped — WP08 (PR #283)** | `feat(settings): wire the extended-thinking dial that was plumbed but unreachable`. Deliberately wired without turning it on (every default resolves to 0 — off). |
| 9 | In-loop compaction hardcoded off | **wire → `turn-context-runway-01PMAG03`** | Owned elsewhere. |
| 10 | `decision.next_true` / `next_false` unread | **wire → `agentic-turn-routing-01PMAG01` WP02** | Owned elsewhere. |
| 11 | `escalation_ladder` implemented, in zero graphs | **shipped — 01PMGX01 WP11c (2026-08-13)** | The deferral resolved the way its directive predicted: `chat_default.yaml`'s `recover` node is an `escalation_ladder`, driven by the `replan_check` decision (row 2). The `wiring:deferred` marker was removed from the manifest in the same change. (Row previously said "in zero graphs — do not delete"; refreshed by the adversarial review.) |
| 12 | `escalate` / ladder LLM calls sent no system prompt | **shipped — WP00 (PR #283)** | `fix(agentgraph): ground the escalate + escalation-ladder LLM calls`. Bug, not a wiring gap; landed ahead of this mission. |

## WP05 — surfaced four more output ports the original inventory missed

Building `check-output-ports.sh` and running it against `HEAD` (after WP01–WP03) turned up
four write-only ports the 2026-07-27 audit's manual pass didn't catch — proof the mechanism
half of this mission earns its keep independent of the triage half. Per plan.md's "every
guard ships green" principle, each got a `wiring:deferred` directive rather than a fix,
since none of them is a one-line wire and the guard's introduction shouldn't block on new
feature work:

| Port | Write site | Disposition |
|---|---|---|
| `chunk_id` (memory write) | `exec_state.go` | **deferred** — the written chunk's id has no reader; `deduped` (written the line below) is the only sibling field anything currently keys off. Plausible future consumer: a memory-write drilldown UI. |
| `escalated` (review cap-hit → escalate) | `exec_compute.go` | **deferred** — `should_retry=false` (written the line above) is read by the kernel promotion path; the `EventEscalateTriggered` event fired just above already carries the "cap hit, routed to escalate" signal to the EventLog. `escalated` itself has no reader. |
| `should_replan` / `doom_loop_hits` (tool-dispatch doom-loop detector) | `exec_dispatch.go` | **deferred → `agentic-turn-routing-01PMAG01` WP08** | This is spec §1.2 item 2, already tracked as owned by a sibling mission (see the main inventory table above) — the directive here is what lets the guard land green today without WP08 having shipped. |

None of these were deleted (FR-005) — the guard's job at introduction is to make the current
state legible, not to force fixes that belong to other missions or unshipped features.

## Node-catalog boundary (do not cross this in a future audit)

Per spec §2 non-goals and the `01PMGX01` amendment recorded there: this mission does not
delete unused *node kinds*. Roughly 25 of 34 kind manifests are unreachable from the two
shipped graphs (`chat_default.yaml` and its siblings) — that is the extensibility surface for
user-authored graphs, not dead code. "Unused by `chat_default`" is not "unwired." The durable
rule for what makes a kind *legitimate* going forward is `01PMGX01` invariant I3: a kind must
be executable and exercised end-to-end by at least one shipped graph, activity, or golden
fixture — extensibility is preserved by exercising the surface, not by exempting it from
audit. `sleep` and `subagent_dispatch` (dispatched as builtin tools, not graph executors) are
the standing proof that "no registered `Executor`" and "unreachable" are different claims;
WP01's `dispatch:` discriminator makes the difference declarable instead of relying on tribal
knowledge.

## WP01 — manifest `dispatch:` discriminator

The nominal `executor:` string (33 distinct Go-symbol strings across 34 kind manifests, all
of them resolving to nothing — `newExecutorRegistry()` keys purely off `Executor.Kind()`, a
Go method, never off the manifest string) is replaced by:

```yaml
dispatch: graph            # a registered Executor must exist for this kind
# or
dispatch: builtin_tool     # no Executor; reached via the tool catalog
tool_name: kenaz__sleep    # required when dispatch: builtin_tool
```

`core/agentgraph/nodes/dispatch.go` validates: every `dispatch: graph` kind must have a
matching entry in `newExecutorRegistry()`; every registry entry's kind must declare
`dispatch: graph` (not `builtin_tool`, and not absent).
`scripts/ci/check-node-dispatch.sh` runs this both-directions check in CI.

> **Integration update (2026-08-12, 01PMGX01 Phase 2):** `sleep` and
> `subagent_dispatch` — originally this WP's canonical `dispatch: builtin_tool`
> examples — gained real archetype-derived graph executors and now declare
> `dispatch: graph` (inherited from `_archetype.tool.yaml`). The
> `builtin_tool` mode therefore has **zero shipped instances**.
>
> **Phase 8 decision (2026-08-13, 01PMGX01 WP17): KEEP.** Phase 6 shipped
> without needing the mode, and a mode with zero instances is normally what
> this mission deletes — but this one is not an unreachable code path, it is
> the *negative pole of a discriminator*. Delete it and `dispatch:` has one
> legal value, at which point `scripts/ci/check-node-dispatch.sh`'s
> both-directions check degrades into "every callable kind has an executor",
> which is invariant **I1** and is already enforced by a Go test with an empty
> allow-list. What would actually be lost is the ability to *say* that a kind
> has no executor on purpose — the exact expressiveness this WP added when it
> replaced the unvalidated `executor:` string. The validator branches stay
> exercised by `nodes/dispatch_test.go` (clean builtin_tool, missing
> `tool_name`, stray `tool_name` on `graph`).
>
> The decision carries an **expiry condition**, recorded on `DispatchMode` in
> `core/agentgraph/nodes/manifest.go`: if a later reader still finds zero
> instances and wants the surface gone, the honest change deletes the mode
> **and the field together** — every manifest's `dispatch:` line,
> `GraphDispatchKindIDs`/`BuiltinToolDispatchKindIDs`, `ValidateDispatch`,
> `dispatch_registry_test.go` and the CI script — and leans on I1 alone.
> Deleting only the constant is the one option strictly worse than either
> keeping or removing the whole thing.

The `executor:` field itself is retained on `Manifest` (still parsed, still recorded in
`Provenance`) for one release cycle as a human-readable hint of "what Go type backs this," but
it is no longer load-bearing — `dispatch:` is what the loader and the guard validate.
