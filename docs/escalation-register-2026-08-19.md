# Escalation register — 2026-08-19

One sitting. Every open product question the eight in-flight missions and the
three audit records cannot answer from code, collapsed by **theme** so the same
underlying question gets one consistent answer instead of eight.

**Nothing here is resolved by this document.** It records questions, evidence,
and — where the tree supports one — a recommended default grounded in
CLAUDE.md's *"Disposition: delete vs. finish"* rubric. Where the tree cannot
tell you what the product wants, the entry says so and is marked
**NEEDS OWNER INPUT** rather than guessing.

---

## Tally

| | Count |
|---|---|
| Raw escalation-shaped items found | **~117** |
| — formal `E-NNN` in mission specs (7 missions × 4–8) | 37 |
| — unnumbered escalations in `harness-self-attach-01PMHS01` §10 | 6 |
| — `escalate` / `justify-without-owner` dispositions in `dead-code-audit-2026-08-18.md` sweeps 1–2 | 31 |
| — `escalate` dispositions + §C.6 questions in the closing sweep (2026-08-19) | 13 |
| — `Escalate` dispositions in `dead-code-audit-2026-08-16.md` | 7 |
| — open ledger entries whose stated exits are a product call | 6 |
| — **`delete` dispositions pulled in by the ruling test** (justification is absence-of-callers only) | **17** |
| **Distinct decisions after consolidation** | **42** |
| — Part 0 contradictions (X-1…X-7) | 7 |
| — Part 1 delete-vs-finish (A-0…A-14) | 15 |
| — Part 2 unattended execution (B-1…B-6) | 6 |
| — Part 3 trust / consent / audit (C-1…C-5) | 5 |
| — Part 4 per-variant coverage (D-1…D-4) | 4 |
| — Part 5 served-mode parity (E-1…E-3) | 3 |
| — Part 6 ownerless justifications (F-1…F-2) | 2 |
| **Contradictions** (the tree currently holds two answers) | **7** |

Roughly a **2.8× collapse**. The biggest single collapses: A-1 disposes of ~16
findings with one answer; A-0 governs 17; B-1/B-2 together unblock two whole
missions; F-1 clears ~9 parked justifications administratively.

Two of the 43 mission escalations are already **resolved** and are recorded here
only so nobody re-opens them: `model-authored-graphs-01PMGA01` E-002 (owner
ruled 2026-08-18, *"yes, the model may schedule jobs"*,
`kitty-specs/model-authored-graphs-01PMGA01/spec.md:861`) and
`harness-self-attach-01PMHS01`'s inherited harness-self-attach question
(`kitty-specs/harness-self-attach-01PMHS01/spec.md:619`).

### The ruling test, applied

CLAUDE.md: a **delete** needs a **named live substitute**, a **documented
product retirement**, **rival infrastructure**, or a **subsystem with no
producer AND no product intent**. *"No importers" is not a reason.*

Seventeen findings dispositioned `delete` fail that test and appear below as
escalations. The largest single cluster is the closing sweep's **C-ii bucket**,
headed *"a named live substitute exists (delete)"*
(`docs/dead-code-audit-2026-08-18.md:1407`) — it lists **13 items and names a
substitute for 4 of them**. The other nine rest on the zero-caller proof alone.

---

# Part 0 — CONTRADICTIONS

The tree currently holds **two answers** to each of these. They are first
because acting on either answer while the other is on record is how an
irreversible delete lands.

---

### X-1 — Four stub RPC domains: flat delete, or escalate-then-delete? *(the known instance)*

> ## ✅ RESOLVED 2026-08-19 by **A-0** (delete-lane freeze). No decision needed here.
>
> The 08-16 ruling ("escalate, then delete") stands, and A-0 goes further: **none
> of the four is deleted this campaign** — not even `workflowAPI`, whose removal
> the 08-16 carve-out would have permitted, since `Workflows_*` (plural) is a live
> substitute. All four become `justify(owner, blocker, date)`.
>
> `AN-11` must not ship as filed. The audit already carries the flag *"The 08-16
> ruling stands"* at `:1428` while still listing `AN-11` in the delete bucket at
> `:1420` — **fix that internal inconsistency when the finding is scoped.**

**Sources.** `AN-11` (`docs/dead-code-audit-2026-08-18.md:1420`) vs. `B12`
(`docs/dead-code-audit-2026-08-16.md:461-469`).

**Question.** May `a2aAPI` / `workflowAPI` / `trustAPI` / `contextAPI` and their
13 Wails bindings be deleted outright, or must agent-cards (a2a) and
secret-references (trust) be escalated as products first?

**Evidence.**
- `core/rpc/api.go:1259` — `a2aAPI:         &stubA2A{},`
- `docs/dead-code-audit-2026-08-16.md:469` — *"Escalate, then delete."*, warning
  *"a2a (agent cards) and trust (secret references) are plausibly wanted"*
- `docs/dead-code-audit-2026-08-18.md:1420` lists `AN-11` inside the delete
  bucket; **the same document** at `:1427` says *"The 08-16 ruling stands."*
- Calibration auditor, `:1664`: **WRONG-SCOPE** — *"overturns that recorded
  ruling without arguing against it"*

**Status quo.** Thirteen bindings exist, `harnessClient.ts` wires four runtime
surfaces on top, every call returns `errNotWired`. No user reaches them.

**Recommended default.** **Escalate — the 08-16 ruling stands.** Per the rubric,
a2a and trust are *"the only surface for a real capability"*; deleting them
removes the capability, not just the tree. **One carve-out needs no escalation**
and can ship now: the singular `Workflow_*` trio, because
`docs/dead-code-audit-2026-08-16.md:469` names the live substitute — *"`Workflows_*`
is the live substitute, a clean delete class."*

**Blast radius.** Delete = ~13 bindings + 4 client surfaces + `stubs.go` +
`types.ts` entries; a future a2a/trust feature restarts from zero. Keep =
`check-serve-dispatch-drift.sh`'s gap stays 4 entries wider and the stubs keep
lying to anyone reading `bindings.go`.

**Reversibility.** Delete is a **one-way door** in practice.

---

### X-2 — `cedar.CheckTool`: allowlisted DELETE, or the seam WP11 is about to build?

> ## ✅ RESOLVED 2026-08-19 by **A-0** + the `trust-surfaces-that-fire-01PMZ202` scope.
>
> **Do not delete.** The allowlist entry
> (`scripts/ci/allowlists/i10-unwired-gates.txt:221`) records *"Verdict: DELETE
> (superseded)"* justified by *"Tool dispatch is NOT ungated"* — **a false
> premise.** `default_tool_policy.cedar:38` and `sites-recommended.cedar:29,35`
> both key on `Action::"use_tool"`, which has no evaluator, so there is no
> per-call tool authorization in the harness at all.
>
> `01PMZ202` WP11 builds that evaluator. **Acting on the allowlist would have
> deleted exactly what the mission is building.**
>
> **Action owed:** correct the i10 allowlist entry's justification in the same
> commit as WP11, per the monotonic-shrink rule.

**Sources.** `scripts/ci/allowlists/i10-unwired-gates.txt:220-231` vs.
`kitty-specs/trust-surfaces-that-fire-01PMZ202/spec.md:761-769` (E-005) vs.
`docs/dead-code-audit-2026-08-18.md:1257-1260`.

**Question.** Does the product want per-call tool authorization at all — and if
so, is `cedar.CheckTool` the seam, or does WP11 build a new one over a symbol
CI has already been told to delete?

**Evidence.**
- `scripts/ci/allowlists/i10-unwired-gates.txt:221` — *"Verdict: DELETE
  (superseded)"*, justified by *"Tool dispatch is NOT ungated"*
- `docs/dead-code-audit-2026-08-18.md:1259` — the shipped policies target
  `use_tool`, *"an action with **no evaluator either**"*
- `core/policy/cedar/types.go:54` — `ActionUseTool         = "use_tool"`
- `core/policy/cedar/policies/sites-recommended.cedar:29` —
  `action == Action::"use_tool",` (a shipped policy keyed on it)
- `kitty-specs/trust-surfaces-that-fire-01PMZ202/spec.md:765` — *"WP11
  introduces the first per-call gate."*

**Status quo.** A user who installs `sites-recommended.cedar` gets four
`use_tool` rules that never evaluate. The allowlist tells CI the gap is closed
by per-family gates; it is not.

**Recommended default.** **Wire, and correct the allowlist entry in the same
commit** (gate-extension rule). The allowlist's DELETE verdict rests on a
premise the 08-18 audit disproved; leaving it uncorrected while WP11 lands means
a future sweep deletes the gate WP11 just built.

**Blast radius.** Wire = a Cedar evaluation on every tool call (cost), plus
user-authored `forbid` rules start denying. Delete = the eight `tool.*`
families in docstrings must be struck or marked reserved.

**Reversibility.** Wiring is reversible; the allowlist correction is free.

---

### X-3 — `narrative.SetSettingsGate`: DRAINED, or live?

> ## ✅ RESOLVED 2026-08-19 by **A-4** (memory-narrative retirement). Moot.
>
> The ledger said DRAINED; the audit (`:1248`) said *"It is not."* Retirement
> ends the disagreement: the gate and its three unreached downstream consumers —
> the promoter, the citation detector, and `LongTermEnabled`→`LoadPrelude` — are
> removed with the subsystem.
>
> **Action owed:** correct the ledger's DRAINED claim as part of the retirement
> commit, so the record does not outlive the code.

**Sources.** `docs/unwired-ledger.md` "Drained" (2026-08-13, 01PMGX01 WP17) vs.
`docs/dead-code-audit-2026-08-18.md:1248-1252` and `:733`.

**Question.** Is the memory-narrative feature flag a working runtime toggle, or
a dial with three unreachable consumers?

**Evidence.**
- `docs/dead-code-audit-2026-08-18.md:1248` — *"is recorded as DRAINED … **It is
  not.**"*
- `:733` — `api.go:1437-1460` carries a comment asserting *"a runtime toggle
  takes effect on the next turn without a restart"*

**Status quo.** The ledger says the finding is closed. It is not; flipping the
dial changes no observable behaviour, and a 24-line comment says otherwise.

**Recommended default.** **Re-date the ledger entry and correct the comment this
release regardless of the subsystem ruling** (see A-4). A stale DRAINED row is
worse than an open one — it stops the next sweep looking.

**Blast radius.** None beyond documentation until A-4 is answered.
**Reversibility.** Fully reversible.

---

### X-4 — `corpus`: retired, or on the live kernel path?

> ## ✅ RULED 2026-08-19 — owner: alec. **LIVE CAPABILITY — wire the embedder.**
>
> The verifier's refutation stands (`:1367` — *"Verifier refuted the 'corpus is
> retired' escalation"*, reclassified user-visible / P2 / wire). `corpus_read`
> ships in the node catalog and `exec_state.go:106` calls `env.Corpus.Search` on
> the **live kernel path**. The gap is that `SetEmbedder` is never called.
>
> **Deleting the client surface would leave the graph kernel calling into
> nothing** — the audit's own phrasing at `:1884`: *"Deleting one side leaves the
> graph kernel lying."*
>
> **Action owed:** the §C.6 escalation-2 entry at `:1879-1884` still lists corpus
> as an open retirement question. Strike it — this ruling supersedes it, and the
> corpora→contexts retirement note in the ledger applies to the *corpora* UI, not
> to the kernel's corpus seam. Correct the ledger note so the next sweep does not
> re-litigate this.

**Sources.** `docs/dead-code-audit-2026-08-18.md:1367` (`AN-12`) vs. `:1879-1884`
(§C.6 escalation 2), against the ledger's corpora→contexts retirement note.

**Question.** Is `corpus` a retired subsystem whose nine-method client surface
should go, or a live capability with a missing embedder wire?

**Evidence.**
- `:1367` — *"**Verifier refuted the 'corpus is retired' escalation**"*;
  reclassified *"user-visible, P2, wire"*
- `:1884` — still listed as an open escalation: *"Deleting one side leaves the
  graph kernel lying."*

**Status quo.** `corpus_read` ships in the node catalog and `exec_state.go:106`
calls `env.Corpus.Search` on the live kernel path, while `SetEmbedder` is never
called — so ingest and search return `ErrEmbedderUnavailable` for the process
lifetime after the user configures a provider.

**Recommended default.** **Wire the embedder; do not delete.** The verifier's
refutation is evidence-backed and the C.6 entry predates it. Per the rubric this
is *"backend live and only the UI is missing"* on one half and a live kernel
consumer on the other — the delete branch is off the table.

**Blast radius.** Wire = corpus nodes start returning results; retiring later
gets harder. Delete = the graph kernel's `corpus_read` node must go too.
**Reversibility.** Wiring reversible; delete one-way.

---

### X-5 — `MO-10` `RegisteredClient.SecretExpired`: delete class, or blocked?

> ## ✅ RESOLVED 2026-08-19 by **D-4** (ship confidential-client DCR support).
>
> `MO-10` is no longer a delete candidate at all — under A-0 nothing is, and D-4
> gives `SecretExpired` a **live consumer** rather than a dated justification.
> DCR will support `client_secret_*`, so the `SecretSaver`/`SecretLoader`/
> `HasSecret`/`ErrDCRExpired` half stops guarding a value nothing can produce.
>
> The contradiction is dissolved rather than adjudicated: the entry filed it in
> the delete bucket while simultaneously saying the substitute was *"loose"*, the
> delete rested *"on the no-caller proof alone"*, and it *"must wait for MO-03's
> escalation."* All three objections are answered by building the thing.

**Source.** `docs/dead-code-audit-2026-08-18.md:1565-1568`.

**Question.** Same entry files it under "orphan symbols … delete" and then says
it may not be deleted yet. Which is it?

**Evidence.** `:1566-1568` — *"'live substitute' is loose; the delete rests on
the no-caller proof alone, and must wait for `MO-03`'s escalation"*.

**Status quo.** Nothing. It is an unreferenced method.

**Recommended default.** **Hold behind D-4 (`MO-03`).** The finding fails the
ruling test by its own admission — the "substitute" is a different receiver
type. This is the clearest self-declared instance of the pattern the ruling test
exists to catch.

**Reversibility.** One-way; cheap to defer.

---

### X-6 — `parallel_dispatch`: delete a graph attr four shipped graphs set?

> ## ✅ RESOLVED 2026-08-19 by **A-0**. **WIRE, not delete.**
>
> Verified 2026-08-19: **three** shipped YAMLs set the attr —
> `core/agentgraph/graphs/chat_default_classic.yaml`,
> `core/rpc/views/agentgraph/library/toolloop_default.yaml`,
> `core/rpc/views/agentgraph/library/chat_default.yaml` — while Go carries it
> only as a generated struct field with no non-test reader
> (`core/agentgraph/attrs_gen.go:1687`).
>
> Deleting it would have meant **editing shipped graphs to satisfy a linter**.
> The honest fix is to make tool dispatch actually respect the attribute, so the
> three graphs that set it get the behaviour they declare.
>
> *(The register recorded "four shipped graphs"; the verified count is three.
> The direction of the finding is unaffected.)*

**Source.** `docs/dead-code-audit-2026-08-18.md:734`.

**Question.** May an inert node attribute be deleted when shipped and library
graphs author it?

**Evidence.** `:734` — *"No non-test reader of `a.ParallelDispatch` exists, yet
four shipped/library graphs set `parallel_dispatch: true`"*; disposition
*"delete (live substitute `max_concurrent: 1`) or wire"*.

**Status quo.** A graph author sets `parallel_dispatch: true`, the manifest says
*"set false for serial dispatch"*, and dispatch keys off `MaxConcurrent` instead.

**Recommended default.** **Wire, or reject the key at load with an error.**
Deleting an attr four shipped graphs already set is a silent behaviour change
for authored content, and `max_concurrent` is not a substitute for a boolean —
it is a different control. Deleting also drags the codegen chain
(`go generate ./core/agentgraph/...`, `spec_test.go`, `catalog_test.go`).

**Reversibility.** Deleting the attr from the manifest is a **one-way door for
user-authored graphs** — they start failing validation or silently changing
behaviour on upgrade.

---

### X-7 — `compaction-overhead-row-writerless`: justify-without-owner, or wire?

> ## ✅ RULED 2026-08-19 — owner: alec. **ONE FINDING. WIRE IT.**
>
> `:758` and `CK-08` (`:1405`) are the consumer and producer halves of the same
> thing, and *"neither half had been traced to the other."* Merge them.
>
> `compactionLLM` and `compactionAudit` are **already stored** — wiring is
> reading them and unhiding the `SessionsView` header row that is permanently
> hidden today.
>
> **Why it matters beyond tidiness:** compaction silently spends the user's
> tokens and money. Surfacing that overhead is a spend-transparency fix, and it
> pairs with the token/cost footer work in task #37 — both are the product being
> honest about what a turn actually cost.
>
> **Fixes the record's real defect:** `:758` carried `OWNER: unassigned`, which
> fails the ritual's rule outright. It now has an owner and a disposition.

**Sources.** `docs/dead-code-audit-2026-08-18.md:758` vs. `:1405` (`CK-08`).

**Question.** Is the compaction-overhead readout an owned feature or a dead row?

**Evidence.**
- `:758` — *"labelled 'justify' with `OWNER: unassigned`, which does not satisfy
  the rule"*; disposition *"assign an owner or downgrade to escalate"*
- `:1405` — `CK-08` is *"the **producer half**"*, filed **wire**: *"neither half
  had been traced to the other"*

**Status quo.** `compactionLLM`/`compactionAudit` are stored and never read; the
`SessionsView` header row is permanently hidden.

**Recommended default.** **Treat as one finding and wire it**, per the rubric's
*"backend live and only the UI is missing"*. Both halves exist; the only thing
missing is the join.

**Reversibility.** Reversible.

---

# Part 1 — Delete-vs-finish for half-built subsystems

**15 decisions · ~52 raw escalations.** The single largest theme, and the one
where a wrong answer is irreversible.

---

### A-0 — What standard of proof does a `delete` need, and who signs it? *(unblocks all of Part 1)*

> ## ✅ RULED 2026-08-19 — owner: alec. **THE DELETE LANE IS FROZEN FOR THIS CAMPAIGN.**
>
> No deletion lands. Every ruling-test failure resolves as **wire** or as
> **justify(blocker, owner, date)** instead. A dated justification ends the lie
> just as well as a delete — which is the campaign's actual goal — without the
> irreversibility.
>
> **Applies to** all 17 ruling-test failures, the C-ii bucket's nine
> zero-caller deletes (including `sessions.getAutonomy`,
> `getMonthlyCostNotifyUSD` and the `Tasks_*BySession` pair), and contradictions
> `X-1`, `X-5`, `X-6`.
>
> **`X-1` consequence:** none of the four stub RPC domains is deleted this
> campaign — not even `workflowAPI`, whose deletion the 08-16 ruling would have
> permitted. All four become dated justifications. Revisit at the next ritual.
>
> **`X-6` consequence:** `parallel_dispatch` is **wire**, not delete. Three
> shipped graph YAMLs set the attr (`chat_default_classic.yaml`,
> `toolloop_default.yaml`, `chat_default.yaml`) while Go carries it only as a
> generated struct field with no reader — deleting it would have edited shipped
> graphs to match a linter.
>
> **Implementer rule:** a commit whose body's only justification for a removal is
> absence of callers is rejected at review. There is no delete lane to appeal to.



**Instances.** All 17 ruling-test failures; the C-ii bucket
(`docs/dead-code-audit-2026-08-18.md:1407-1420`); `X-1`, `X-5`, `X-6`.

**Question.** Does an implementer resolve "no callers found" by deleting, or
does every delete need a named class (substitute / retirement / rival infra /
no-producer-and-no-intent) signed by the owner?

**Evidence.** `docs/dead-code-audit-2026-08-18.md:1407` heads the bucket *"a
named live substitute exists (delete)"* and names one for `C2V-21`, `C2V-23`,
`C2V-24` and `C2V-29` only. `C2V-32`'s stated justification is *"the binding's
own comment concedes no consumer exists"* (`:1417-1418`) — absence of callers,
verbatim. The calibration auditor's own warning, `:1746-1747`: *"the delete
lane's true error rate is probably higher than my headline 1-in-13"*.

**Status quo.** Nine client methods are queued for deletion on a zero-caller
proof. Among them are `sessions.getAutonomy` (trust-relevant),
`getMonthlyCostNotifyUSD` (a spend control), and
`Tasks_AbortBySession`/`ListBySession` (the background-task subsystem the ledger
already records as producerless).

**Recommended default.** **Adopt CLAUDE.md's rubric as a gate on the delete
lane:** no delete lands without a named class in the commit body. Run §C.5's
mandatory dedup (`docs/dead-code-audit-2026-08-18.md:1761`) first — the auditor
estimates it *"reclassifies 15-30 findings — disproportionately in the DELETE
lane"*.

**Blast radius.** Costs an afternoon of dedup and slows nine small deletes.
Skipping it risks repeating `X-1` at scale.

**Reversibility.** The gate is reversible; the deletes it prevents are not.

---

### A-1 — Is the bundle / trust subsystem still wanted?

> ## ✅ RULED 2026-08-19 — owner: alec. **KEEP AND FINISH — scoped to "download a bundle and verify it".**
>
> The subsystem stays. The owner scoped it precisely, and the scope maps onto
> exactly the two things that do not work; everything around them already ships.
>
> **What already works** (do not rebuild): manifest parse + schema versioning,
> lockfile, cache, transactional artifact-kind activation, `BundlesView.vue`,
> and the `List`/`Get`/`Install`/`Remove` RPC surface. `core/bundle` is NOT an
> orphan package — `core/core.go`, `core/config` and `core/session/spec.go`
> import it.
>
> **Gap 1 — DOWNLOAD.** `core/rpc/views/bundle/impl.go:245` refuses every
> channel but one: *"install kind %q unsupported (v0.3.0 beta: local_path
> only)"*. `git`, `oci` and `http_mirror` are declared in the channel registry
> (`core/bundle/channels/channel.go:52`) and rejected at the door.
>
> **Gap 2 — VERIFY.** Two layers, and the inner one is a correctness bug rather
> than a wiring gap:
>   - Nothing on the install path calls the verifier at all — no reference to
>     `VerifyManifestSignatures` or `integrity.` in `impl.go` or `core.go`.
>   - `core/trust/bundleadapter.go:96` passes `Signature: nil` into the engine
>     behind the comment *"engine reads sig math from algo registry; bundle
>     adapter only carries refs."* It does not. `EngineVerifier` therefore
>     **cannot return `OK=true` for any input**, even once wired (finding
>     `BT-02`). Wiring alone would ship a verifier that fails every bundle.
>
> **Sequencing:** fix `bundleadapter.go` BEFORE wiring the call site, or the
> install path starts refusing valid bundles. Two signature schemes are declared
> — `sigstore_referrer` and `ed25519_detached` — and which of them ships first
> is a scoping question for the mission, not a blocker.
>
> **Consequence for the ~16 findings:** all resolve to **wire**. None is a
> delete, which is consistent with A-0's freeze. Because this is now a real
> capability with a real correctness bug on a supply-chain path, it warrants its
> own mission rather than absorption into an existing one.
>
> **Invalid justifications struck:** every prior "keep" rationale pointed at an
> archived mission with zero work packages, which fails the ritual's own rule.
> This ruling replaces them.



**Instances.** `BT-03`, `BT-05`, `BT-07`, `BT-08`, `BT-09`, `BT-10`, `BT-11`,
`BT-12` (~16 findings); §C.6 escalation 1.

**Question.** Does the harness ship signed bundles and a content-addressed
artifact store, or does the whole of `core/bundle/**` + `core/trust/**` retire?

**Evidence.**
- `docs/dead-code-audit-2026-08-18.md:1874-1877` — the justifications name
  `_archive/a2a-signed-cards-trust-01KQ18P9` and
  `_archive/bundle-format-resolver-01KQ1A3J`; *"**Both are archived**"*, with
  *"zero recorded work packages"*
- `:1579-1580` — *"nothing has ever written a byte to the CAS the package doc
  calls 'the long-lived store of every artifact byte'"*
- `:1584` — *"`TrustEngine.Sign` can never succeed in a shipped binary"*
- `:1627` — the sweep proved `core/trust` and four sibling packages have
  **zero** cross-package production readers

**Status quo.** ~52 non-test files, 343 exported symbols, no production reader.
Nothing lies to the user because nothing reaches the user — the failure is a
maintenance surface pretending to be a feature.

**Recommended default.** **NEEDS OWNER INPUT.** The tree cannot tell you whether
signed-artifact distribution is a product. Both named missions are archived with
no work packages, which under the ritual's own rule
(*"a justification names the blocker and the owner"*) makes every current
justification invalid. **This one answer disposes of ~16 findings.**

**Blast radius.** Retire = the largest single deletion in the campaign, and
`BT-11`'s fail-closed signing posture (which the audit calls *"deliberate,
documented and fail-closed"*, `:1585`) goes with it. Keep = someone must own
building a CAS writer and a signing backend.

**Reversibility.** **One-way door.** ~52 files of tested work.

---

### A-2 — Does live MCP connector health ship, or does the whole pipeline go?

> ## ✅ RULED 2026-08-19 — owner: alec. **SHIP IT.**
>
> Build the publisher, the desktop consumer and a health pill.
> `mcp:health-changed` currently has **neither publisher nor subscriber**, and
> `MCP_SubscribeHealthChanges` (`core/rpc/bindings.go:545`) is a binding nothing
> calls. Desktop users get no live connector-health signal; served mode has a
> one-shot snapshot only.
>
> **Cheap because the signal is being built anyway:** `01PMZ303` WP05 creates a
> real health signal regardless, so this is a consumer and an affordance on top.
>
> **Under the rubric this was never a delete candidate** — it is the only surface
> for a real capability, and `01PMZ303` spec.md:887 says explicitly *"Do not
> resolve by deleting one half."*
>
> **Follow-on gate:** declaring the topic constant enables
> `check-broker-topic-consumers.sh` to cover it (`spec.md:870-871`), which is the
> gate-extension obligation for this finding class.


**Instances.** `connector-lifecycle-truth-01PMZ303` E-001;
`rpc-mcp-health-subscribe-dead` (`docs/dead-code-audit-2026-08-18.md:700`);
`docs/dead-code-audit-2026-08-16.md:455` (escalate list).

**Question.** Ship a health publisher + desktop consumer + a health pill, or
delete the binding, the two view methods and the audit kind together?

**Evidence.**
- `core/rpc/bindings.go:545` —
  `func (b *Bindings) MCP_SubscribeHealthChanges() (string, error) {`
- `docs/dead-code-audit-2026-08-18.md:700` — *"`mcp:health-changed` has
  **neither publisher nor subscriber**"*
- `kitty-specs/connector-lifecycle-truth-01PMZ303/spec.md:887` — *"**Do not
  resolve by deleting one half**"*

**Status quo.** A user gets no live connector-health signal on desktop; served
mode has a one-shot snapshot only. Nothing visibly lies.

**Recommended default.** **Ship it.** Z303 WP05 creates a real health signal
regardless, which the spec notes makes the decision *"cheaper either way"*
(`spec.md:889`). Under the rubric this is *"the only surface for a real
capability"*.

**Blast radius.** Ship = a new UI affordance + a broker topic with a declared
const (which then enables `check-broker-topic-consumers.sh`, per
`spec.md:870-871`). Delete = served mode keeps its snapshot and desktop
permanently has none.

**Reversibility.** Delete is one-way; the ship branch is additive.

---

### A-3 — Does the harness want a structured-output surface?

> ## ✅ RULED 2026-08-19 — owner: alec. **SHIP THE FULL STRUCTURED-OUTPUT SURFACE.**
>
> Grammar **and** JSON mode **and** JSON schema, across every adapter whose
> capability row supports them — not just enough to carry grammar for `ollama`.
>
> **Makes D-1 coherent:** grammar-constrained decoding is the ollama adapter's
> headline payoff and is now genuinely reachable rather than a capability row
> nothing can hit (`ollama.yaml:17` `grammar: true`).
>
> **Resolves at once:** `grammar-mode-unreachable`,
> `responseformat-jsonmode-no-producer`, and every adapter's dead
> `case "grammar"` arm.
>
> **Likely its own mission** rather than a WP inside
> `model-settings-reach-the-model-01PMZ101` — it is a capability surface across
> seven adapters, and Z101 is already 12 WPs carrying the P0.
>
> **Sequencing:** the P0 (`custom.yaml` + the tool-roundtrip encoders) ships
> first. Structured output is additive and must not delay users who currently
> cannot make a single tool call.


**Instances.** `model-settings-reach-the-model-01PMZ101` E-002;
`responseformat-jsonmode-no-producer` (`docs/dead-code-audit-2026-08-18.md:702`).

**Question.** Is JSON-mode / response-format constrained decoding a product
feature, or four adapters' worth of tested code with no future?

**Evidence.**
- `core/llm/llm.go:431` —
  ``ResponseFormat *ResponseFormat `json:"response_format,omitempty"` ``
- `kitty-specs/model-settings-reach-the-model-01PMZ101/spec.md:996-998` —
  outside `core/llm/**`, `ResponseFormat` *"matches exactly one line and it is a
  comment"*; `JSONMode` matches nothing
- `spec.md:1002-1004` — the audit field built to record it,
  `FormatMode string`, *"has no writer either"*

**Status quo.** Nothing. The capability exists in four adapters and is
unreachable; no user is told it works.

**Recommended default.** **NEEDS OWNER INPUT**, with a strong lean to *finish*.
`spec.md:1005-1006` calls it *"the classic 'backend live, UI missing' shape and
the cheapest possible win *if* it is wanted"* — the rubric's strongest
finish-signal. But no surface in the tree asks for it, so intent is genuinely
undeterminable. **Do not let a WP resolve it by deleting.**

**Blast radius.** Finish = one producer + one settings surface. Delete = four
`ApplyResponseFormat` implementations, `core/llm/structured`, the registry's
validate-and-repair loop, and the `structured_output` capability rows.

**Reversibility.** **One-way door.**

---

### A-4 — Is the memory-narrative subsystem wanted?

> ## ✅ RULED 2026-08-19 — owner: alec. **RETIRE IT — documented product retirement.**
>
> ### This is an explicit CARVE-OUT from A-0's delete-lane freeze
>
> A-0 froze deletions justified by *absence of callers*. **This is not that.**
> "Documented product retirement" is one of the four classes the rubric names,
> and this ruling supplies it: the memory-narrative layer is retired as a product
> decision, recorded here, and the deletion follows from the retirement rather
> than from a grep. No other finding may cite this ruling as precedent for a
> zero-caller delete.
>
> **Scope:** ~1,500 lines across 13 files, four RPCs, the binding surface, and
> the rendered banner. Migrations 821/822 are never registered
> (`narrative.RegisterMigrations` has no caller), so **the tables do not exist on
> any install** — there is no user data to migrate or preserve.
>
> **User-visible lies this ends:** the *"N narratives unrecoverable"* banner that
> can never appear, and `mark-important`, which is a silent no-op today.
>
> **Also resolves X-3** — the ledger said `narrative.SetSettingsGate` was
> DRAINED, the audit said it is not. Retirement moots the disagreement; the gate
> and its three unreached downstream consumers (promoter, citation detector,
> `LongTermEnabled`→`LoadPrelude`) go with it.
>
> **Release note required.** A retirement the user cannot see is not documented.


**Instances.** `rpc-memory-narrative-nil`, `memory-narrative-deps-nil`,
`narrative-layer-never-constructed`, `mark-important-silent-noop`,
`narrative-settings-gate-inert` (`docs/dead-code-audit-2026-08-18.md:289-298`,
`:733`); ledger *"Migrations that can never run"* (2026-08-18); **X-3**.

**Question.** Build a persistent narrative/promotion store, or retire the
subsystem and its UI?

**Evidence.**
- `docs/dead-code-audit-2026-08-18.md:289` — *"**Escalate** — ~1,500 lines
  across 13 files plus four RPCs, a binding surface and a rendered banner"*
- `:284-285` — `narrative.RegisterMigrations` has no caller, so migrations
  821/822 never run and *"the tables do not exist on any install"*
- `:280-281` — the *"N narratives unrecoverable"* banner *"can never appear"*

**Status quo.** The user presses **Important** ("Boost this memory's promotion
score"), the call returns success, and nothing happens — on every install.

**Recommended default.** **NEEDS OWNER INPUT on the subsystem; ship the honesty
fix regardless.** `:292-294` is unambiguous and does not depend on the ruling:
*"`MarkImportant` must stop returning success"*. Do **not** resolve by wiring
`MemMetricsStore` — *"which would lose every promotion on restart"* (`:291-292`).

**Blast radius.** Build = a SQL-backed metrics store + migration registration.
Retire = ~1,500 lines, four RPCs, a binding surface, a rendered banner, and two
never-run migrations whose ledger rows are permanent.

**Reversibility.** **One-way door.**

---

### A-5 — Is a persistent provider-capability cache wanted?

> ## ✅ RULED 2026-08-19 — owner: alec. **WIRE IT as the probe vehicle.**
>
> Confirms what D-2 implied. The `provider_capabilities` table
> (`core/session/migrations_provider_capabilities.go:19`) stops being *"an empty
> table on every install"*, `HARNESS_LLM_CAPABILITY_CACHE`
> (`core/llm/capabilities/cache.go:21`) becomes meaningful, and the capability-cache
> refresher gets a caller.
>
> **Sequencing:** probe → cache → `CapabilityHints` reader (D-2). The static
> `custom.yaml` baseline (the P0 fix) lands FIRST and stays — without it, a probe
> failure leaves a custom endpoint with no capabilities at all rather than a
> conservative set.
>
> **NOT in scope here:** the second orphan table,
> `agent_graph_node_provenance` (migration 0326), whose sole consumer is a
> test-only `CheckManifestDrift` (`docs/dead-code-audit-2026-08-18.md:704`).
> Unrelated subsystem — handle separately, and note the A-0 freeze applies to it.
>
> **WP-PI applies** — this mission adds a live table and a migration.


**Instances.** `model-settings-reach-the-model-01PMZ101` E-003 (+E-004 depends
on it); `capability-cache-refresher-orphan`
(`docs/dead-code-audit-2026-08-18.md:728`);
`provider-capabilities-table-orphan` (`:704`).

**Question.** Retire the cache subsystem and drop its table, or wire it as the
vehicle for live per-endpoint capability probes on `custom-openai`?

**Evidence.**
- `core/llm/capabilities/cache.go:21` —
  `EnvCapabilityCache = "HARNESS_LLM_CAPABILITY_CACHE"`
- `core/session/migrations_provider_capabilities.go:19` —
  `CREATE TABLE IF NOT EXISTS provider_capabilities (`
- `kitty-specs/model-settings-reach-the-model-01PMZ101/spec.md:1022` — *"an
  empty table on every install"*
- `docs/dead-code-audit-2026-08-18.md:704` — a **second** orphan table exists,
  `agent_graph_node_provenance` (migration 0326)

**Status quo.** Two empty tables on every install; an env var documented in a
constant that nothing reads.

**Recommended default.** **Answer together with D-2** (per-profile
`custom-openai` capabilities). If D-2 says "probe live", this is its vehicle and
the answer is *wire*. If D-2 says "static baseline", retire — but
`:704` is correct that *"an *applied* migration cannot simply be deleted"*; the
retirement needs a drop migration written with the scratch-table care the ledger
demands.

**Blast radius.** Retire = two drop migrations on a schema at high-water mark
~335, which is exactly CLAUDE.md blind spot #3 territory (a destructive
migration aimed at real rows for the first time).

**Reversibility.** The subsystem delete is reversible; **the drop migration is
not.**

---

### A-6 — `kind=mcp` hooks: build the seam, or drop the kind?

> ## ✅ RULED 2026-08-19 — owner: alec. **DROP THE KIND FROM THE UI NOW; the seam is a separate decision.**
>
> Remove `'mcp'` from `HOOK_KINDS` (`frontend/src/lib/hooks.ts:119`) so nobody can
> author a hook that cannot run. Today a user picks MCP, is *required* to name an
> MCP tool, saves successfully — and every fire errors.
>
> **Keep the backend kind constant** so any already-saved `kind=mcp` hook does not
> break on load; it simply cannot be created any more.
>
> **The seam is parked, not refused:** `justify(blocker: "MCP hook dispatch is a
> v1 stub whose result the runner discards even when wired", owner: alec,
> date: 2026-08-19)`. If it is ever built, the discarded-result behaviour
> (`core/hooks/runner.go:231` and the dispatcher error path) must be fixed in the
> same change — a hook whose result is dropped is a hook that cannot block, which
> is most of the point.
>
> **Unblocks `01PMZ202` WP09.**


**Instances.** `trust-surfaces-that-fire-01PMZ202` E-001 *(blocks WP09)*;
`docs/dead-code-audit-2026-08-18.md:197` (*"**Escalate** the `mcp` kind"*).

**Question.** Is an MCP-invoked hook a v1 capability?

**Evidence.**
- `core/hooks/runner.go:231` — *"optional — nil means kind=mcp hooks are skipped
  with a warning"*
- `kitty-specs/trust-surfaces-that-fire-01PMZ202/spec.md:724-725` — the
  dispatchers *"instead **error**"*; the UI *"requires* an MCP tool name … for a
  kind that cannot work"*
- `frontend/src/lib/hooks.ts:119` —
  `export const HOOK_KINDS = ['builtin', 'shell', 'mcp'] as const;`

**Status quo.** A user picks **MCP** in the hook editor, is required to name an
MCP tool, saves — and every fire errors.

**Recommended default.** **Drop the kind from the UI now; escalate the
capability.** The lie (an offered kind that cannot work) must end this release
either way. `spec.md:728-729` is explicit: *"**Do not resolve by deleting on the
implementer's judgement.**"* — so removing `'mcp'` from `HOOK_KINDS` and the
editor branch is a UI-honesty change, not a capability retirement, and the
capability question stays open.

**Blast radius.** Drop = users with saved `kind=mcp` hooks need a migration or a
tolerant validator. Build = someone owns `MCPInvoker` and the discarded-result
semantics at `runner.go:489-492`.

**Reversibility.** Reversible both ways.

---

### A-7 — Which of the eight fire-less hook events are wanted?

> ## ✅ RULED 2026-08-19 — owner: alec. **BUILD PRODUCERS FOR ALL EIGHT.** Plus wire elicitation.
>
> `pre_save_session`, `post_assistant_turn_complete`, `user_prompt_submit`,
> `subagent_start`, `notification`, `worktree_create`,
> `background_task_complete`, `file_changed` all get real fire sites. Nothing is
> deleted from `AllEvents` / `ALL_HOOK_EVENTS`.
>
> **Elicitation is the cheap one — do it first.** `01PMZ202` spec.md:776-777:
> *"The runner, merge and shell-dispatch halves are complete and tested; only the
> call from the ask path is missing."* One call site.
>
> ### ⚠️ THREE OF THE EIGHT ARE BLOCKED ON SUBSYSTEMS THAT DO NOT EXIST
>
> - **`background_task_complete`** — the background-task subsystem has **no
>   producer**; `core/rpc/builtins_wiring.go:321` sets `Tasks: nil` inside a block
>   gated on a `subagentSeam` that is itself nil. The ledger already records this
>   as deliberately parked. **This event cannot be built until that producer
>   exists** — either accept it as the last of the eight, or fold the
>   background-task producer into scope (see A-13).
> - **`subagent_start`** — same seam.
> - **`worktree_create`** — needs a worktree lifecycle producer.
>
> **INTERIM HONESTY STILL REQUIRED.** "Build all eight" is the destination, not
> today's state. Until each producer lands, the picker still offers events that
> cannot fire — a user saves a hook against `user_prompt_submit`, it validates,
> it saves, it never runs. **Ship the picker restriction as an interim step and
> grow it back per producer**, so the surface never advertises more than it does.


**Instances.** `trust-surfaces-that-fire-01PMZ202` E-003 *(blocks WP09)*, E-006
(elicitation events); `docs/dead-code-audit-2026-08-18.md:180`.

**Question.** For each of `pre_save_session`, `post_assistant_turn_complete`,
`user_prompt_submit`, `subagent_start`, `notification`, `worktree_create`,
`background_task_complete`, `file_changed` — is the answer "a producer, owned by
X" or "delete from `AllEvents` + `ALL_HOOK_EVENTS`"? Plus: are `elicitation` /
`elicitation_result` wired, wired-but-hidden, or removed?

**Evidence.**
- `core/hooks/hooks.go:102` — `EventElicitation,` (in `AllEvents`, so
  `isKnownEvent` accepts it; deliberately absent from `ALL_HOOK_EVENTS`)
- `kitty-specs/trust-surfaces-that-fire-01PMZ202/spec.md:749` — *"Each answer is
  either 'a producer, owned by X' or 'delete from `AllEvents` + `ALL_HOOK_EVENTS`'."*
- `spec.md:776-777` — for elicitation, *"The runner, merge and shell-dispatch
  halves are complete and tested; only the call from the ask path is missing."*

**Status quo.** A user saves a hook against `user_prompt_submit`, the validator
accepts it, and it never fires. For elicitation the surface is worse: it works,
but is configurable only by hand-editing JSON.

**Recommended default.** **Wire elicitation and add it to the picker**
(rubric: *backend live, UI missing*, one call site). **Per-event owner input for
the other eight** — this is eight small product calls, not one, and the tree
cannot infer which are wanted.

**Blast radius.** See D-2 in the spec's own §12: *"Nine hook events start firing
for users who already saved hooks against them believing they worked. A saved
`pre_tool_use` hook with `decision:"block"` will begin blocking."*
(`spec.md:788-790`) — a release-notes-level upgrade behaviour change.

**Reversibility.** Wiring is reversible; removing an event name from `AllEvents`
breaks saved user configs.

---

### A-8 — The `approval` node: finish it or delete the kind?

> ## ✅ RULED 2026-08-19 — owner: alec. **FINISH IT.**
>
> Build the halt-and-resume seam, the approval UI, and write the `rejected` port
> that is currently unwritten.
>
> **Coupled to the scheduling ruling, and that coupling is the reason.** The model
> may schedule jobs to run unattended; an `approval` node is the natural
> human-in-the-loop for exactly that. It is also *"the only HITL-gate kind in the
> node catalog, so deleting it removes the capability"*
> (`01PMZ202` spec.md:732-734).
>
> **A mission, not a work package** — neither the halt-and-resume seam nor the
> approval UI exists.
>
> **Independent of this ruling:** `01PMZ202` WP02 removes the fabricated approval
> event either way. Land that first; it is an honesty fix that does not wait on
> the build.
>
> **Note** `scripts/ci/allowlists/i3-unexercised-kinds.txt:137` records that no
> shipped graph contains an `approval` node — that allowlist entry should shrink
> when a shipped graph uses one, per the monotonic-shrink rule.


**Instance.** `trust-surfaces-that-fire-01PMZ202` E-002.

**Question.** Does the product want a human-approval node in agent graphs?

**Evidence.** `kitty-specs/trust-surfaces-that-fire-01PMZ202/spec.md:732-734` —
*"`approval` is the only HITL-gate kind in the node catalog, so deleting it
removes the capability"*; finishing needs *"a halt-and-resume seam and an
approval UI, neither of which exists"*. `scripts/ci/allowlists/i3-unexercised-kinds.txt:137`
records no shipped graph contains one.

**Status quo.** User-authored graphs only. WP02 removes a fabricated event
either way; the `rejected` port stays unwritten.

**Recommended default.** **NEEDS OWNER INPUT.** This couples directly to B-1
(unattended execution): an approval node is the natural human-in-the-loop for a
scheduled graph run, so answering B-1 "yes, unattended" strengthens the case for
finishing it.

**Blast radius.** Delete = removes the only HITL gate from the catalog and drags
the codegen chain. Finish = a halt/resume seam, which is real infrastructure.

**Reversibility.** **One-way door** (node-kind removal breaks user graphs).

---

### A-9 — Hook + slashcmd telemetry: wire whole, or delete whole?

> ## ✅ RULED 2026-08-19 — owner: alec. **WIRE BOTH.**
>
> `core/hooks/telemetry.go`'s entire exported surface is test-only and the
> `fires_total` counter its own header promises *"does not exist"*;
> `core/slashcmd/telemetry.go:27` instructs callers to use `TraceRun` while the
> sole production caller calls `Run`. Both files assert consumers that do not
> exist.
>
> **This is now load-bearing rather than cosmetic.** A-7 rules that all eight
> fire-less hook events get producers. Tracing is **how anyone verifies they
> actually fire** — which is precisely the class of proof this entire campaign
> exists to establish. Shipping eight new producers with no observability would
> repeat the pattern that produced the findings.
>
> **Both or neither, never one** (`01PMZ202` spec.md:759 — *"a half-deleted
> [surface]"* is the worst outcome). Wiring both satisfies that.

**Instance.** `trust-surfaces-that-fire-01PMZ202` E-004 *(WP08 defaults to wire)*.

**Question.** Is hook/slashcmd tracing wanted?

**Evidence.** `kitty-specs/trust-surfaces-that-fire-01PMZ202/spec.md:752-757` —
`core/hooks/telemetry.go`'s entire exported surface is test-only and the
`fires_total` counter its header promises *"does not exist"*;
`core/slashcmd/telemetry.go:27` instructs callers to use `TraceRun` while the
sole production caller calls `Run`.

**Status quo.** No traces. Two files assert consumers that do not exist.

**Recommended default.** **Wire** (the spec's own default). Cheap, and the
alternative must delete *both* files together — `spec.md:759`: *"a half-deleted
telemetry file leaves the same lie."*

**Blast radius.** Small either way. **Reversibility.** Reversible.

---

### A-10 — Should workflow run caching ship?

> ## ✅ RULED 2026-08-19 — owner: alec. **NARROW THE SCHEMA — stop advertising a dial that does nothing.**
>
> `rerun_policy` currently **rejects invalid values**, telling the author the
> field is real, while all six accepted values behave identically
> (`core/workflows/runtime.go:157` — the cache branch is dead because
> `Engine.Cache` is nil). A user writing `rerun_policy: skip` re-runs and re-bills
> every time.
>
> **This is an honesty fix, not a delete-lane action.** The A-0 freeze governs
> removing *code*; this removes a false *advertisement*. `Engine.Cache` and the
> runtime branch stay — they are the seam if caching is ever built.
>
> ### ⚠️ The re-billing does not go away — say so
>
> Narrowing the schema stops the product lying about the dial; it does **not**
> stop the cost. Every workflow run re-executes and re-bills, and **B-6 makes
> workflow schedules real**, which multiplies how often that bill lands. If
> scheduled workflows become common, revisit caching as a spend issue rather than
> a correctness one.
>
> `justify(blocker: "no cache implementation; dial removed rather than faked",
> owner: alec, date: 2026-08-19)`.

**Instances.** `automation-actually-runs-01PMZ404` E-002;
`engine-cache-nil-rerun-inert` (`docs/dead-code-audit-2026-08-18.md:726`).

**Question.** Wire `Engine.Cache` and ship caching, or narrow the schema so
authors are not told a dial is real?

**Evidence.**
- `core/workflows/runtime.go:157` —
  `if e.Cache != nil && wf.RerunPolicy != "" && !opts.SkipCache {`
- `docs/dead-code-audit-2026-08-18.md:726` — the schema *"**rejects** an invalid
  value, telling the author the field is real, while all six accepted values
  behave identically"*
- `kitty-specs/automation-actually-runs-01PMZ404/spec.md:1019-1020` — *"a user
  writing `rerun_policy: skip` today re-runs and re-bills every time"*

**Status quo.** Every workflow re-runs and re-bills, silently, whatever the
author wrote.

**Recommended default.** **Owner call on shipping the cache; narrow the schema
either way** so `rerun_policy` stops accepting values that do nothing. The
spec is explicit: *"**Do not delete the tested subsystem to resolve this.**"*
(`spec.md:1023-1024`).

**Blast radius.** Ship = **billing and freshness semantics change** for every
existing workflow. Narrow = authored workflows using the dead values fail
validation on upgrade.

**Reversibility.** Shipping the cache is reversible; the billing change is
observable immediately.

---

### A-11 — Four picker widgets, or a smaller accepted workflow input set?

> ## ✅ RULED 2026-08-19 — owner: alec. **BUILD THE FOUR PICKERS.**
>
> `enum`, `file`, `artifact_ref`, `project_ref` get real widgets.
> `WorkflowsView.vue:659` branches only on `'multiline'` and its `v-else` is a
> plain text box, while `schema.go:88` **requires** options for `enum` and
> nothing reads them.
>
> **Not merely missing polish:** a free-text box where an `enum` was declared
> lets a user submit a value the workflow cannot handle, so the author's
> constraint is validated at authoring time and discarded at run time.
>
> **Unblocks gate G-2** in `automation-actually-runs-01PMZ404`.

**Instances.** `automation-actually-runs-01PMZ404` E-003 *(blocks gate G-2)*;
`workflow-input-kind-variant-gap` (`docs/dead-code-audit-2026-08-18.md:508`).

**Question.** Build enum/file/artifact_ref/project_ref pickers, or trim the
schema to what the run form supports and migrate existing workflows?

**Evidence.** `docs/dead-code-audit-2026-08-18.md:508` — *"`WorkflowsView.vue:659`
branches only on `'multiline'`; the `v-else` is a plain text box"*, while
`schema.go:88` **requires** options for `enum` and nothing reads them.

**Status quo.** An author declares `kind: enum` with options; the user gets a
free-text box.

**Recommended default.** **NEEDS OWNER INPUT** — it is a mission-sized build vs.
a migration. `spec.md:1033` forbids the middle path: *"**Do not resolve by
widening the `v-if`**"*.

**Blast radius.** Trim = existing workflows using the four kinds must migrate,
and gate G-2's input set changes. Build = a mission.

**Reversibility.** Trimming the schema is a **one-way door for authored
workflows**.

---

### A-12 — Deferred asks and elicitation wizards: build, or delete both legs?

> ## ✅ RULED 2026-08-19 — owner: alec. **BUILD BOTH LEGS.**
>
> Add the `mode` field to `askuserquestion.AskArgs` so the tool's schema can
> express deferred delivery, wire `OpenWizard`'s missing call site, and mount
> `DeferredAskPill.vue` + `DeferredAskPanel.vue`.
>
> **Directly required by the scheduling rulings.** A scheduled or unattended run
> that needs user input has **nowhere to put the question today** — it can only
> block. B-1 (unattended cron) and B-3 (model-created schedules execute) both
> assume a run can raise something for a human without holding the turn open.
> Deferred asks are that mechanism.
>
> The UI half is already built; this is a schema field and a call site.

**Instance.** `docs/unwired-ledger.md:592-623`.

**Question.** Do deferred asks and multi-step wizards ship?

**Evidence.** `docs/unwired-ledger.md:602-604` — *"`askuserquestion.AskArgs` has
no `mode` field, so the tool's own schema gives the model no way to ask for
deferred delivery"*; `:611` — `OpenWizard` *"has zero non-test callers"*.

**Status quo.** `DeferredAskPill.vue` and `DeferredAskPanel.vue` were
deliberately left unmounted, so nothing lies — but two components and a whole
wizard wire-shape are inert.

**Recommended default.** **NEEDS OWNER INPUT.** The ledger states the two honest
exits and forbids the third (`:622-623`): *"Not (c): leaving a half-surface that
reads, in a code review, like a shipped feature."*

**Blast radius.** Build = a tool-schema change (`mode` + `questions`) plus a
wizard renderer. Delete = both legs plus `types.ts` shapes and the wire branch.

**Reversibility.** **One-way door.**

---

### A-13 — The sub-agent live-worker view: delete, or spec the control RPCs?

> ## ✅ RULED 2026-08-19 — owner: alec. **BUILD THE CONTROL RPCs + the background-task producer.**
>
> Spec `abort` / `steer` / `pause` / `resume`, give the background-task subsystem
> a real producer, and mount `SubagentTab.vue` + `SubagentBudgetMeter.vue`.
>
> **This is now load-bearing, not optional.** A-7 ruled all eight hook events get
> producers, and three of them — `subagent_start`, `background_task_complete`,
> `worktree_create` — cannot be built without this seam.
> `core/rpc/builtins_wiring.go:321` sets `Tasks: nil` inside a block gated on a
> `subagentSeam` that is itself nil.
>
> **Not a green-field capability:** `kenaz__subagent_dispatch` is already live and
> its branches already surface in `BranchSidebar`. This makes a shipped
> capability observable and controllable rather than inventing one.
>
> **Reverses the ledger's own recommended default** (delete under
> no-producer-no-intent, `docs/unwired-ledger.md:649-651`) — deliberately, and
> because A-7 claimed the subsystem. Update the ledger entry to point here.


**Instance.** `docs/unwired-ledger.md:625-651`.

**Question.** Should `SubagentTab.vue` + `SubagentBudgetMeter.vue` be deleted,
or do abort/steer/pause/resume RPCs get built?

**Evidence.** `docs/unwired-ledger.md:641-643` — the tab's four control emits
*"have no counterpart anywhere in `harnessClient.ts` — there is no pause,
resume, abort or steer RPC to call"*.

**Status quo.** No importer, and no backend to import them for.
`kenaz__subagent_dispatch` is live and its branches surface in `BranchSidebar`,
so nothing is invisible; the dedicated live-worker view is missing.

**Recommended default.** **Delete under the rubric's fourth class** — *"the whole
subsystem has no producer and no product intent"* — **unless someone claims
sub-agent steering.** The ledger's own framing (`:649-651`) supports this: *"a
tab whose buttons cannot be implemented from the current backend is not 'not yet
mounted', it is unbuilt."* Note the sibling `todo` case is the opposite call:
**wire it** (`TodoChip.vue` has a live producer and only needs a parent).

**Blast radius.** Small. **Reversibility.** One-way but cheap to rebuild.

---

### A-14 — Nine orphan Wails bindings: per-binding rulings

> ## ✅ RULED 2026-08-19 — owner: alec. **WIRE ALL NINE.**
>
> Verified 2026-08-19: all nine have **zero** frontend callers and **zero**
> served-mode dispatch entries. Each gets a real caller.
>
> **Tier 1 — backend already live, cheap:**
> `Diag_LogPath` (a one-liner returning `logging.PathOrError()` — an "open log
> file" affordance), `Contexts_ContextSearch`, `Contexts_ContextExport`,
> `Sessions_DeleteWithOptions`.
>
> **Tier 2 — pairs with an existing ruling:**
> `CedarPolicy_ListPlanModeActions` should land with **B-3**'s plan-mode fix,
> since that ruling already requires adding `ActionScheduledRunExecute` to
> `PlanModeDeniedActions`. One surface, one change.
>
> **Tier 3 — ⚠️ wiring these means INVENTING UX that nobody has specified:**
> `Sessions_StartCapture` / `Sessions_StopCapture` (what is session capture *for*
> — debugging, sharing, replay?) and `Unit_PromoteAsMergeRequest` /
> `Unit_ResolveLoadable` (the units merge-request flow has no described user
> journey). **Treat the UX question for each as an escalation raised during the
> mission, not as a detail the implementer resolves alone.** Wiring a binding to
> a UI nobody designed is how the next sweep finds a panel that lies.
>
> **Also served-mode relevant:** under **E-1** (full parity) each newly-wired
> binding needs a served-mode answer too — dispatch it, or boundary-panel it.

**Instances.** `rpc-orphan-binding-inventory`
(`docs/dead-code-audit-2026-08-18.md:709`); `eval-replay-half-unwired` (`:699`);
`unit-promote-merge-request-no-caller` (`:708`);
`docs/dead-code-audit-2026-08-16.md:455`.

**Question.** For each of `CedarPolicy_ListPlanModeActions`, `Diag_LogPath`,
`Sessions_StartCapture`, `Sessions_StopCapture`, `Sessions_DeleteWithOptions`,
`Contexts_ContextSearch`, `Contexts_ContextExport`, `Unit_PromoteAsMergeRequest`,
`Unit_ResolveLoadable` — wire, delete, or keep as a dev tool?

**Evidence.**
- `docs/dead-code-audit-2026-08-18.md:709` — *"escalate per-binding; do not
  batch-delete"*; *"Two carry docstrings naming a UI that does not exist."*
- `core/eval/replay.go:112` — `func NewReplayer(captureDir, runsDir string) *Replayer {`
  (`:699`: *"Captures can never be started from the app"*)
- `:708` — `Unit_PromoteAsMergeRequest`'s docstring *"frames it as the safe
  alternative to writing the higher layer directly — i.e. the direct-write path
  is the one that ships"* (a **governance** question)

**Status quo.** Nine bindings no UI reaches; two docstrings assert a consumer.

**Recommended default.** **Per-binding, not batch.** Two carry distinct product
questions that must not be answered by a sweep: eval capture/replay is
*dev-tool-vs-product*, and `Unit_PromoteAsMergeRequest` is *governance*. The
rest are candidates for delete **only** with a named class per A-0.

**Reversibility.** Batch-deleting is a one-way door across nine unrelated
questions.

---

# Part 2 — Unattended, deferred and model-initiated execution

**6 decisions · ~14 raw escalations.** One root question with several
independent sub-rulings. Answering B-1 unblocks the most downstream work in the
campaign after A-0.

---

### B-1 — Should the harness run chat prompts unattended on a cron?

> ## ✅ RESOLVED 2026-08-19 — already ruled by the owner earlier in this campaign. **YES.**
>
> The owner ruled that the model should be able to schedule jobs to run; the
> capability was scoped to the **scheduled-chat** surface (mission
> `model-scheduled-jobs-01PMSJ01`), not to graph runs. Recorded at
> `model-authored-graphs-01PMGA01` §10 E-002 as RESOLVED.
>
> **Carried constraints:** unattended writes are **deny + queue for approval**
> (owner decision, same sitting); `confirm_each` must not park forever
> (`01PMSJ01` WP05 lands the unattended posture BEFORE WP04's dispatcher); and
> `B-2`'s per-run tool containment gates only the *model-creates-the-schedule*
> half (WP10), not the user-creates-it half (WP01–WP09).

**Instances.** `automation-actually-runs-01PMZ404` E-001 (task #33);
`model-scheduled-jobs-01PMSJ01` (the whole mission);
`docs/dead-code-audit-2026-08-18.md:542`;
`model-authored-graphs-01PMGA01` E-002 **(RESOLVED 2026-08-18: yes, for
scheduled *chat*; scheduled *graph* runs remain out of scope)**.

**Question.** Does unattended, deferred execution of a stored prompt ship?

**Evidence.**
- `docs/dead-code-audit-2026-08-18.md:539-540` — *"Clicking 'Run now' returns a
  successful run summary and writes a permanent `completed` row into run history
  for work that never happened."*
- `kitty-specs/model-authored-graphs-01PMGA01/spec.md:861` — *"**E-002 — RESOLVED
  2026-08-18 by the owner: yes, the model may schedule jobs.**"*
- `kitty-specs/automation-actually-runs-01PMZ404/spec.md:1010-1011` — *"Under
  *either* outcome the panel cannot stay as-is"*

**Status quo.** Settings → Runtime → Scheduled Chats is mounted and
discoverable. A user authors a prompt, model and cron, enables the row, sees
Pause/Resume — and it never fires, ever, with no error.

**Recommended default.** **The capability question is already answered "yes" for
scheduled chats.** What remains is scope and containment (B-2..B-4). **Ship the
honesty fix unconditionally this release**, per `:544-547`: *"`RunNow` must
return a typed 'not wired' error, and `NoopChatRunDispatcher.Status` must not be
`"completed"` while it is reachable from production. That is fabricated evidence
in a persisted table."*

**Blast radius.** Building the engine is `model-scheduled-jobs-01PMSJ01`. Not
building it means the panel and its schema come out.

**Reversibility.** The honesty fix is reversible. Removing the panel and schema
loses user-authored schedules.

---

### B-2 — Does per-run tool containment get built, and who owns it? *(blocks SJ01 WP10)*

> ## ✅ RULED 2026-08-19 — owner: alec. **BUILD IT — assigned to `harness-self-attach-01PMHS01` WP04.**
>
> One wire, two missions unblocked. WP04 instantiates `NewMergedResolver`
> (`core/toolloop/perms.go:290`) at `core/rpc/api.go:4065-4074`, replacing the
> static resolver whose `Resolve` discards the session id by construction
> (`perms.go:185`).
>
> **Unblocks:** `model-scheduled-jobs-01PMSJ01` WP10 (the model may create
> schedules) and `model-authored-graphs-01PMGA01` E-004.
>
> **Sequencing:** `01PMHS01` WP04 must land BEFORE SJ01 WP10. SJ01's spec
> explicitly refuses to ship WP10 without it, and refuses "ship with no
> containment" as a third option.
>
> **Blast radius, accepted:** the merged resolver goes live for EVERY session,
> not only scheduled ones. The consumers are already correct and already consume
> a session id at both points — `discoverer_adapter.go:80-85` (deny → invisible)
> and `kernel_tool_adapter.go:320-329` (deny → floor) — so this changes which
> resolver answers, not what the answer means.


**Instances.** `model-scheduled-jobs-01PMSJ01` E-002 *(blocks WP10)*;
`model-authored-graphs-01PMGA01` E-004; `harness-self-attach-01PMHS01` WP04
(the same wiring).

**Question.** Instantiate the containment that already exists, or ship the
schedule surface without letting the model create schedules?

**Evidence.**
- `core/agentgraph/branch_seam.go:69` — `ToolAllowlist      []string`
- `kitty-specs/model-scheduled-jobs-01PMSJ01/spec.md:1069-1073` — it *"has two
  producers and no reader"*, and the LLM seam *"discards"* the model-attr form
- `spec.md:1082-1083` — *"This is the same wiring `harness-self-attach-01PMHS01`
  WP04 performs … the two missions should not do it twice."*
- `spec.md:1089-1091` — the third option is refused: it *"grants the model
  deferred, unattended execution of an arbitrary prompt against the full tool
  catalog, chosen by the model, at a moment chosen by the model."*

**Status quo.** No per-run tool containment exists. Two producers write an
allowlist nothing reads.

**Recommended default.** **Assign the wiring to `harness-self-attach-01PMHS01`
WP04 and gate SJ01 WP10 behind it.** Both missions need the same
`SessionOverrideReader` + merged-resolver assignment; the spec says the
remaining work is *"much smaller than T-1..T-3 alone imply"*
(`spec.md:1083-1084`). **This decision unblocks two missions with one wire.**

**Blast radius.** Wire = the merged resolver goes live for every session, not
just scheduled ones. Defer = SJ01 ships WP01–WP09 only; a user may schedule, the
model may not.

**Reversibility.** Reversible.

---

### B-3 — What should the shipped Cedar policy say about a model-created schedule executing?

> ## ✅ RULED 2026-08-19 — owner: alec. **PERMIT ONLY WITHIN A TOOL ALLOWLIST.**
>
> A model-created schedule may fire, but only with a tool set declared at creation
> time and enforced per run.
>
> ### ⚠️ THIS REMOVES THE HUMAN REVIEW MOMENT. Containment is now the only boundary.
>
> The alternative shape — forbid execute until a human opens the entry — was the
> design the scheduling and graph-authoring specs leaned on, and it is **not**
> what ships. Consequences, stated plainly:
>
> - **B-2's per-run containment becomes safety-critical, not a nice-to-have.**
>   `harness-self-attach-01PMHS01` WP04 is now the load-bearing security control
>   for model-initiated unattended execution. It must be correct, not merely
>   present.
> - **`model-scheduled-jobs-01PMSJ01` WP10 is hard-blocked until WP04 lands.**
>   Shipping the model-facing tool before containment would grant deferred,
>   unattended execution of a model-chosen prompt against the full tool catalog
>   at a model-chosen time — which SJ01's spec explicitly refuses.
> - **Fail-safe direction must be verified.** A schedule whose allowlist cannot be
>   resolved must NOT run. Do not let a missing or empty allowlist read as
>   "unrestricted" — that is the `enforce`-treats-NotApplicable-as-allow shape
>   (`core/policy/cedar/hooks.go:733-742`) that this campaign has already found
>   four times.
>
> **Close the plan-mode gap regardless:** `PlanModeDeniedActions` denies
> `ScheduledRunCreate` and `ScheduledRunDelete` but **not**
> `ActionScheduledRunExecute`. Add it — plan mode should not fire schedules.
>
> **Unblocks WP09's policy file.** The shipped `.cedar` must key on
> `tool.scheduled_run.*`, which no policy file mentions today.

**Instance.** `model-scheduled-jobs-01PMSJ01` E-003 *(blocks WP09's policy file)*.

**Question.** Forbid execute for `created_by == "model"` until a human opens the
entry, permit with an audit record, or permit only within a tool allowlist?

**Evidence.**
- `core/rpc/views/scheduledchat/impl.go:64` —
  `// Cedar gate — default-allow; permissive posture.`
- `kitty-specs/model-scheduled-jobs-01PMSJ01/spec.md:1096-1101` — no `.cedar`
  file mentions `tool.scheduled_run.*`; `PlanModeDeniedActions` denies Create and
  Delete in plan mode *"but **not** `ActionScheduledRunExecute`"*

**Status quo.** Both create and execute are default-allow.

**Recommended default.** **NEEDS OWNER INPUT** — this is a security posture, not
a code question. Note the plan-mode asymmetry should be resolved in the same
ruling; leaving Execute permitted while Create is denied reads as an oversight
and will get "fixed" by someone later.

**Blast radius.** Forbid = a review step before any model-created schedule runs.
Permit = the model can schedule work that runs with nobody watching.

**Reversibility.** Policy files are reversible; a schedule that already ran is
not.

---

### B-4 — Should `kenaz__write_file` prompt a human interactively?

> ## ✅ RULED 2026-08-19 — owner: alec. **WIRE THE PROMPTER THAT ALREADY EXISTS.**
>
> Connect `fs.Prompter` to `cedar.Registry.RequestInteractive`. The interactive
> permission system is built, wired to the frontend, and its own package doc
> names the four families it serves — Credential, BashCommand, **FilesystemOp**,
> Tool. Bash calls it (`core/tools/bash/bash.go:531`); credstore calls it; the
> recipe-dir flow calls it. The FilesystemOp family, named in its own
> documentation, was never connected.
>
> **Today's behaviour, which this ends:** `core/rpc/builtins_wiring.go:502-504`
> builds the gate with no `Prompter`, so `NoOpPrompter` denies; no shipped policy
> permits `write_filesystem` (the only occurrence in the policies dir is a
> comment); `enforce` sees the deny. **`kenaz__write_file` refuses every
> unmatched write, silently, for every user who enables it.**
>
> **Unattended safety is preserved, not weakened.** `NoOpPrompter` remains the
> fallback when no channel is attached and is documented verbatim *"so unattended
> runs are safe"* — a scheduled run still fail-safe denies. The 5-minute
> `PromptTimeout` also resolves to deny.
>
> **Closes task #35.** Also reconciles the split flagged in
> `reference-node-vs-tool-path-gate-asymmetry`: the tool path gets its prompt;
> the graph-node path still resolves NotApplicable to *allow* and is tracked
> separately under `model-scheduled-jobs` WP05.


**Instances.** `model-scheduled-jobs-01PMSJ01` E-001;
`model-authored-graphs-01PMGA01` E-002 amendment (*"deny + queue for approval"*);
`AN-13` (`docs/dead-code-audit-2026-08-18.md:1368`).

**Question.** Wire `corefs.Gate.Prompter`, ship a default permit, or leave the
write tools opt-in-by-policy?

**Evidence.**
- `core/rpc/builtins_wiring.go:502` — `gate := corefs.NewGate(corefs.GateOptions{`
  (no `Prompter`)
- `kitty-specs/model-scheduled-jobs-01PMSJ01/spec.md:1056-1058` — the fs gate
  *"denies every unmatched write today, on every path, silently"*
- `kitty-specs/model-authored-graphs-01PMGA01/spec.md:879-884` — the node path
  resolves `NotApplicable` to **allow**; the tool path to a fail-safe **deny**
  *"so unattended runs are safe"* — **two answers to one question**
- `spec.md:888-890` — the owner's chosen posture is *"deny + queue for
  approval"*

**Status quo.** Every unmatched `kenaz__write_file` is denied silently, while
the Tools panel's filesystem-write toggle claims otherwise.

**Recommended default.** **Reconcile the two gates to the owner's stated posture
(deny + queue for approval), and ship the stop-being-silent fix regardless.**
The owner has already ruled the direction at `01PMGA01:888-890`; what is
unresolved is which of the three options implements it.

**Blast radius.** Prompter = the fs family gets the modal `bash` already has.
Default permit = containment falls entirely to the dangerous-path table.

**Reversibility.** Reversible.

---

### B-5 — Should memory prune run automatically without a consent surface?

> ## ✅ RULED 2026-08-19 — owner: alec. **NO AUTO-PRUNE. The scheduler does not start at boot.**
>
> Prune deletes the user's memories. Starting a scheduler at boot is silent data
> deletion nobody agreed to — the trust-relevant class in the rubric.
>
> **Instead, make the growth visible:** a memory-size indicator and a nudge when
> it crosses a threshold. Deletion stays user-initiated, but the status quo
> ("memory grows without bound for everyone who never finds that button") ends.
>
> **`core/memory/prune/scheduler.go` is NOT deleted** — consistent with the A-0
> freeze, and it is the vehicle if an opt-in setting is ever wanted. Record it as
> `justify(blocker: "no consent surface; auto-deletion requires explicit opt-in",
> owner: alec, date: 2026-08-19)`.
>
> The pruner itself stays wired to the existing user-initiated button.


**Instance.** `prune-scheduler-never-started`
(`docs/dead-code-audit-2026-08-18.md:690`).

**Question.** Does the prune scheduler start at boot?

**Evidence.**
- `core/memory/prune/scheduler.go:76` —
  `func NewScheduler(p *Pruner, opts ...SchedulerOption) *Scheduler {`
- `docs/dead-code-audit-2026-08-18.md:690` — *"Memory grows without bound for
  everyone who never finds that button"*; disposition: *"prune deletes user
  memories; whether it should run automatically without a consent surface is a
  product call."*

**Status quo.** The only prune path is a user pressing "prune now" in the Memory
view. Memory grows unbounded for everyone else.

**Recommended default.** **NEEDS OWNER INPUT.** This is data deletion without
consent — the rubric's *trust-relevant* class. Do not start a scheduler that
deletes user memories as a wiring fix.

**Blast radius.** Start = user memories begin being deleted on a schedule.
**Reversibility.** **Deleted memories are one-way.**

---

### B-6 — Who owns workflow dispatch?

> ## ✅ RULED 2026-08-19 — owner: alec. **BUILD `wfsched.Dispatcher` — workflow schedules become real.**
>
> The runtime already exists: the manual Run button calls the live engine
> directly (`RunWithOptions`, `core/rpc/views/workflows/impl.go:310`). What is
> missing is only the connection from the cron fire to it. Users keep the
> schedules they authored.
>
> **The honesty fix ships FIRST, in its own commit.** Today
> `core/workflows/scheduler/cron_scheduler.go:282-314` takes the nil-dispatcher
> branch, synthesises a run id, leaves `err` nil, therefore records
> `status="completed"`, and calls `SetLastFired`. Every tick persists a completed
> run and advances `last_fired_at` for work that never ran. Land
> failed/skipped + no `SetLastFired` before the dispatcher, so the fix is
> verifiable independently of the build.
>
> **Closes task #34.** Belongs to `automation-actually-runs-01PMZ404`.


**Instance.** `model-scheduled-jobs-01PMSJ01` E-005.

**Question.** Build `wfsched.Dispatcher`, or publicly retire workflow schedules?

**Evidence.** `kitty-specs/model-scheduled-jobs-01PMSJ01/spec.md:1116-1119` —
*"D-3 makes every enabled workflow schedule start visibly failing"*; no non-test
implementation of `wfsched.Dispatcher` exists.

**Status quo.** Workflow schedules complete successfully and do nothing.

**Recommended default.** **Retire or own — do not leave the completed rows.**
Same class as B-1; the honesty fix ships regardless.

**Reversibility.** Retiring loses user-authored schedules.

---

# Part 3 — Trust, consent and audit surfaces that are advertised but absent

**5 decisions · ~16 raw escalations.** Every entry here is
*trust-/compliance-relevant* under the rubric, which is why none is a delete
candidate without an explicit owner ruling.

---

### C-1 — Is there meant to be a persistent local audit log?

> ## ✅ RULED 2026-08-19 — owner: alec. **BUILD THE PERSISTENT AUDIT STORE.** New mission.
>
> The audit log is meant to be real. Scope: register `core/event/log`'s six
> embedded migrations (written, never registered — already in the ledger as
> "migrations that can never run"), wire `eventlog.NewStore` (zero production
> callers today), then retention sweep and export.
>
> **Sequencing, load-bearing:** the store lands FIRST. The calibration audit
> established that any plan saying "schedule `RetentionSweep`" is not
> implementable as written, because there is no production store to sweep —
> `fleet-enforcement-truth-01PMZ505` C-1/WP05 was written to ship the honesty
> change *instead*. That WP now becomes an interim step, not the endpoint.
>
> **Unblocks:** fleet retention enforcement (`01PMZ505`), audit Export
> (CSV/JSONL/PDF — never worked in a shipped build), BulkPurge, and the
> retention window that currently bounds nothing.
>
> **Ship the honesty change anyway, in the interim.** The store is a mission;
> `AuditSettingsPanel.vue:67-68` tells users events are "permanently deleted
> during the nightly sweep" TODAY. That copy must stop lying before the store
> ships, not after.
>
> **WP-PI is mandatory here** — this mission is the persistence mission.


**Instances.** `fleet-enforcement-truth-01PMZ505` E-004;
`audit-retention-backend-nil` (`docs/dead-code-audit-2026-08-18.md:310`);
`AN-03` (`:1363`); `SD-01` settings (`:1470`); ledger *"Migrations that can
never run: … event-log"*.

**Question.** Build a persistent audit store, scope the audit log explicitly as
session-lifetime and change the copy everywhere, or retire the compliance
surfaces?

**Evidence.**
- `kitty-specs/fleet-enforcement-truth-01PMZ505/spec.md:1115-1120` — the audit
  view is *"a bounded in-memory ring"*, `eventlog.NewStore` has no production
  caller, and `core/event/log`'s six migrations are never registered
- `docs/dead-code-audit-2026-08-18.md:1470` — `AuditSettingsPanel.vue:67-68`
  tells the user events are *"permanently deleted during the nightly sweep"*
- `docs/dead-code-audit-2026-08-18.md:220` — *"**Audit export (CSV/JSONL/PDF) has
  never worked in a shipped build**"*

**Status quo.** The audit log does not survive a relaunch. Export and BulkPurge
return errors. The retention window bounds nothing. Three compliance-adjacent
surfaces sit on a store nobody built.

**Recommended default.** **NEEDS OWNER INPUT (product + compliance) on building
it; ship the honesty change now.** `spec.md:1125-1126` — *"WP05 ships the
honesty change without it."* Note the calibration auditor's correction: *"there
is no production audit store to sweep"* (`:1780`), so any plan that says
"schedule `RetentionSweep`" is not implementable as written.

**Blast radius.** Build = a mission (store + migrations + retention). Retire =
the Compliance panel, retention settings, export and BulkPurge all come out.

**Reversibility.** **One-way door** for the compliance surfaces.

---

### C-2 — Where does a per-device catalog signing key come from?

> ## ✅ RULED 2026-08-19 — owner: alec. **SHIP THE HONESTY CHANGE; the key source is a separate, later decision.**
>
> Both fleet install paths — catalog **and** skills — currently skip ed25519
> verification because `core/rpc/api.go:2653` hardcodes
> `PubKeyBase64: ""  // empty = skip verify`, and *"there is no pubkey source
> anywhere in the repo to wire from"*.
>
> **Now:** the UI and docs state plainly that fleet catalog and skill installs are
> **unverified**. The empty-pubkey path must stop reading as a configured setting
> that happens to be blank.
>
> **Later, when a key source exists**, either option remains open: per-device keys
> published by the control plane (correct trust model — rotatable, revocable;
> blocked on the same fleet endpoints as F-2), or reuse of the build-embedded
> bundle keys (immediately implementable; cannot rotate or revoke per device).
>
> **`WithPubKey` is NOT deleted** — it is the seam the eventual fix uses.
> `justify(blocker: "no key source exists in or out of the repo", owner: alec,
> date: 2026-08-19)`.
>
> **Interacts with A-1:** the bundle-verify mission builds real signature
> verification. Check whether its key handling can serve catalog items before
> inventing a second scheme.


**Instances.** `fleet-enforcement-truth-01PMZ505` E-001 **(blocks any real
fix)**; `catalog-withpubkey-never-called` (`docs/dead-code-audit-2026-08-18.md:311`).

**Question.** Does the control plane publish per-device catalog keys, are
catalog items signed with the build-embedded bundle keys, or does verification
stay off and the product says so?

**Evidence.**
- `core/rpc/views/catalog/impl.go:45` —
  `func (a *API) WithPubKey(pubKeyBase64 string) *API {`
- `docs/dead-code-audit-2026-08-18.md:311` — `core/rpc/api.go:2653` hardcodes
  `PubKeyBase64: "", // fleet-level pub key; empty = skip verify`
- `:311` — *"there is no pubkey source anywhere in the repo to wire from"*

**Status quo.** Both fleet install paths — catalog **and** skills — skip ed25519
verification entirely.

**Recommended default.** **NEEDS OWNER INPUT (fleet control-plane).** The tree
cannot supply a key source. **Ship the honesty change regardless**
(`spec.md:1091-1092`).

**Blast radius.** (a) is a control-plane change; (b) is smaller and a weaker
guarantee; (c) is a documentation change with a security consequence.

**Reversibility.** Reversible.

---

### C-3 — May an individual publisher unpublish an org catalog item?

> ## ✅ RULED 2026-08-19 — owner: alec. **YES — a publisher may withdraw their own items. Admins may withdraw anything.**
>
> Wire `core/fleet/catalog.go:213` `Unpublish` to a UI, with an authorization rule
> of *own items for publishers, anything for admins*.
>
> **This is a trust surface, not a convenience gap** (`01PMZ505`
> spec.md:1099-1102). A user who published something sensitive by mistake
> currently has **no in-app withdrawal path at all** — the correct latency for
> withdrawing a leaked secret is minutes, not however long an admin takes to
> respond to a request.
>
> **The spec's instruction is honoured:** *"Do not delete the client method to
> resolve the ambiguity."* The method was never the problem; the missing UI and
> the undecided authorization rule were.


**Instance.** `fleet-enforcement-truth-01PMZ505` E-002;
`catalog-unpublish-orphan` (`docs/dead-code-audit-2026-08-18.md:703`).

**Question.** Is withdrawal a publisher right or an admin-console action?

**Evidence.**
- `core/fleet/catalog.go:213` —
  `func (c *Client) Unpublish(ctx context.Context, catalogID string) error {`
- `kitty-specs/fleet-enforcement-truth-01PMZ505/spec.md:1099-1102` — *"**Do not
  delete the client method to resolve the ambiguity**"*; a user who published
  something sensitive *"currently has no in-app withdrawal path at all, which is
  a trust surface, not a convenience gap."*

**Status quo.** No in-app withdrawal path exists.

**Recommended default.** **Wire it if the answer is "publisher"** — the spec
calls it *"a cheap wire"*. **NEEDS OWNER INPUT** on the permissions question
itself.

**Reversibility.** Reversible.

---

### C-4 — Does the harness let MCP servers call back into the model (sampling)?

> ## ✅ RULED 2026-08-19 — owner: alec. **LEAVE THE POSTURE — parked with a dated justification.**
>
> `justify(blocker: "no consent surface exists", owner: alec, date: 2026-08-19)`.
>
> Not built, not deleted. The stack is constructed and unreachable
> (`core/mcp/transport/stdio/pool.go:160` `SamplingEnabled: false`; all 117
> catalog recipes declare `sampling_policy.allowed = false`), so no user is
> affected today and nothing lies.
>
> **Hard constraint carried forward** (`01PMZ303` spec.md:901): **do not flip the
> default.** Any future work here builds the consent surface first.
>
> Consistent with A-0's delete-lane freeze: deleting the handler, the recipe
> policy field and the pool flag would be a one-way door on an MCP-spec
> capability.


**Instances.** `connector-lifecycle-truth-01PMZ303` E-002;
`stdio-sampling-hardcoded-off` (`docs/dead-code-audit-2026-08-18.md:729`).

**Question.** Finish the recipe-aware Open path plus a consent UI, or delete the
whole sampling stack?

**Evidence.**
- `core/mcp/transport/stdio/pool.go:160` — `SamplingEnabled: false,`
- `core/rpc/api.go:4104` — the handler is constructed in production:
  `Sampler: stdio.LLMSamplingHandler(reg, func() (string, string) {`
- `kitty-specs/connector-lifecycle-truth-01PMZ303/spec.md:896-897` — *"All 117
  catalog recipes declare `sampling_policy.allowed = false`."*

**Status quo.** The stack is constructed and unreachable. No user or connector
is affected.

**Recommended default.** **NEEDS OWNER INPUT.** The spec adds a hard constraint
that survives either answer (`spec.md:901`): *"**do not flip the default.** The
current posture is the safe one."*

**Blast radius.** Finish = an MCP server can drive the user's model, which needs
a consent surface. Delete = the sampling handler, the recipe policy field and
the pool flag go together.

**Reversibility.** **One-way door** on delete; flipping the default is a
security change.

---

### C-5 — Should the model be able to set a site's env vars (secrets)?

> ## ✅ RULED 2026-08-19 — owner: alec. **YES — a `sites_env_set` tool, behind an explicit gate.**
>
> This overrides the register's recommended default (which was "never, document
> the asymmetry"). Recorded as an owner decision, not an oversight — which was
> the point of asking.
>
> ### Gate requirements are load-bearing, not advisory
>
> A leaked secret is not reversible. The tool ships ONLY with both:
> 1. **An explicit Cedar permit.** No default-allow, and no reliance on
>    `NotApplicable`, which `enforce` maps to nil (`hooks.go:733-742`).
> 2. **An interactive confirmation per call**, showing the site and the key name
>    — never the value.
>
> ### ⚠️ SEQUENCING — do not ship this tool before these land
>
> - **B-4** (`fs.Prompter` → `RequestInteractive`) — the interactive-confirm
>   machinery this depends on.
> - **E-1's authorization fix.** ⚠️ **CORRECTED 2026-08-19** — the leak is
>   SERVER-side, not in the Vue component. `ConfirmToolModal`'s cross-session
>   queue is a **documented product decision** (`:43-53`: *"The queue is
>   CROSS-SESSION on purpose… The fix is attribution, not filtering"*), and
>   `activeSessionId` is already passed and used at `:198` to mark foreign rows.
>   **Filtering there would regress desktop.** The real defect: `handleWS`
>   (`core/serve/server.go:952-969`) never reads its `Params` although the client
>   sends `params: { id }` (`harnessClient.ts:4282-4284`), plus a global
>   `SubscribeTracked` (`wsstream.go:265`) and a verbatim `frameFor`. Two
>   snapshots leak independently, and **the elicit leg cannot be filtered at all
>   today** — `elicit.ElicitRequest` carries no `SessionID` and `publish` drops
>   it, so a producer change plus Wails codegen is required.
>   **Shipping a secret-writing tool before the WS fan-out is session-scoped
>   means one served client could approve another session's secret write.**
> - Credential hygiene: `scripts/ci/check-no-cred-bytes-in-rpc.sh` must cover the
>   new path; values must never enter an RPC payload, a log line or an audit
>   record.
>
> **Context:** `sites.go:315`'s `SiteEnvSet` is *"the ONLY way secrets reach a
> site"*, and `sites_*` tools already exist for six other operations.


**Instance.** `fleet-enforcement-truth-01PMZ505` E-005.

**Question.** Is a secret-setting site tool ever in scope, and under what gate?

**Evidence.** `kitty-specs/fleet-enforcement-truth-01PMZ505/spec.md:1129-1133` —
`sites.go:315` calls `SiteEnvSet` *"the ONLY way secrets reach a site"*, while
`sites_*` tools already exist for six other operations, *"so the asymmetry will
look like an oversight and get 'fixed' by someone later."*

**Status quo.** No MCP tool sets site secrets. The asymmetry is undocumented.

**Recommended default.** **Decide in writing now, either way.** The value here
is not the answer — it is preventing a future drive-by. Cost of recording it:
one paragraph. Cost of not: a model writing secrets.

**Reversibility.** Adding the tool later is easy; a leaked secret is not.

---

# Part 4 — Per-variant and per-provider coverage

**4 decisions · ~11 raw escalations.** The shape is always the same: one variant
works and the rest silently do not.

---

### D-1 — Is `ollama` meant to be a real adapter kind?

> ## ✅ RULED 2026-08-19 — owner: alec. **SHIP A REAL `ollama` ADAPTER KIND**, with grammar-constrained decoding real.
>
> `ollama.yaml:17` (`grammar: true`) becomes reachable, and every adapter's
> currently-dead `case "grammar"` arm gets a live path.
>
> ### ⚠️ THIS DOES NOT FIX THE P0 ON ITS OWN — read before scoping
>
> `core/llm/localruntime/templates_local.go:16` — **"Kind is always
> `custom-openai` for local runtimes"** — covers Ollama, LM Studio, Jan AND
> GPT4All. Moving Ollama to its own kind leaves the other three persisted as
> `custom-openai`, which still has no capability-catalog entry, so
> `Catalog.Describe` still returns the streaming-only baseline that never sets
> `CapToolCalling` and `Gate.Check` still refuses their every tool-bearing turn.
>
> **`custom.yaml` is therefore still required**, and it is the P0 fix. The
> `ollama` adapter is the quality upgrade on top. Scope both; ship `custom.yaml`
> first, because it is what stops LM Studio / Jan / GPT4All users being unable
> to make a single tool call.
>
> **Coupling:** grammar is meaningless if **A-3** (structured-output surface)
> retires. This ruling implies A-3 ships at least far enough to carry grammar —
> confirm when A-3 is taken.
>
> **Still coupled:** `model-settings-reach-the-model-01PMZ101` notes that fixing
> the catalog alone exposes `azure-custom-tool-roundtrip-dropped` — the private
> encoders never emit `tool_calls`/`tool_call_id`. That pair must land together.


**Instances.** `model-settings-reach-the-model-01PMZ101` E-001;
`grammar-mode-unreachable` (`docs/dead-code-audit-2026-08-18.md:701`).

**Question.** Ship an `ollama` adapter and make grammar-constrained decoding
real, map `custom-openai` profiles onto the `ollama` catalog row, or retire the
row and the grammar capability?

**Evidence.**
- `core/llm/capabilities/data/ollama.yaml:17` — `grammar: true`
- `kitty-specs/model-settings-reach-the-model-01PMZ101/spec.md:980-983` —
  grammar is *"true only for the unreachable kind `ollama` and false for all
  seven registered kinds"*, so `ResponseFormat.Mode="grammar"` *"can never
  succeed"*
- `docs/dead-code-audit-2026-08-18.md:701` — `llm.go:34` claims *"local runtimes
  (llama.cpp via Ollama) return true"*

**Status quo.** Local Ollama installs persist as `custom-openai`; the grammar
capability is unreachable and every adapter's `case "grammar"` arm is dead code.

**Recommended default.** **NEEDS OWNER INPUT**, coupled to A-3 (structured
output) — grammar-constrained decoding is the headline payoff of the
local-runtime feature and is meaningless if A-3 retires.

**Blast radius.** Ship = a new adapter kind + registry entry. Retire =
`ollama.yaml` and the grammar capability go.

**Reversibility.** Reversible; the catalog row delete is cheap to restore.

---

### D-2 — Should `custom-openai` capabilities be per-profile?

> ## ✅ RULED 2026-08-19 — owner: alec. **WIRE `CapabilityHints`, PROBE-DRIVEN.**
>
> The field is shaped exactly for this override and has no reader. It gets one,
> populated by probing the endpoint rather than by asking the user to declare
> capabilities — self-correcting, and it does not push a technical question onto
> someone pointing the harness at a vLLM deployment.
>
> **This implies A-5 ships.** The register notes the two are one decision: if the
> answer is "wire, but probe-driven", **A-5's provider-capability cache is its
> vehicle**. Treat A-5 as ruled ship-it by implication and confirm when reached.
>
> **Does not replace the P0 fix.** `custom.yaml` still provides the static
> baseline; hints refine it per profile. Without the baseline, a probe failure
> leaves a custom endpoint with no capabilities at all.
>
> **Fixes:** a user pointing `custom-openai` at a vision-capable endpoint
> currently has images refused (`01PMZ101` spec.md:1030-1035).


**Instance.** `model-settings-reach-the-model-01PMZ101` E-004 *(WP11 must not
resolve it by deleting)*.

**Question.** Does `ProviderProfile.CapabilityHints` get a reader — and if so,
is it user-set or probe-driven?

**Evidence.** `kitty-specs/model-settings-reach-the-model-01PMZ101/spec.md:1030-1035`
— *"A user pointing `custom-openai` at a vision-capable vLLM deployment will
still have images refused"*; the field *"is shaped exactly for this override and
has no reader."*

**Status quo.** A static baseline refuses capabilities the user's endpoint
actually has.

**Recommended default.** **Answer with A-5.** If the answer is *"wire, but
driven by a live probe"*, A-5's capability cache is its vehicle and the two are
one decision.

**Reversibility.** Reversible.

---

### D-3 — Nobody will register 14 OAuth apps — does Kameas, or do those recipes ship "bring your own"?

> ## ✅ RULED 2026-08-19 — owner: alec. **BRING YOUR OWN OAUTH APP, STATED PLAINLY.**
>
> The fourteen placeholder-client-id recipes — Atlassian, Salesforce, HubSpot,
> Box, Zoom, Discord, Vercel, Webex, Front, RingCentral, Smartsheet, Wrike,
> Tableau — ship with an explicit "advanced: register your own OAuth app"
> posture. Kameas registers nothing and owns no rotation burden.
>
> **Deliverable is copy + posture, not plumbing:** the Connect button must
> explain the BYO setup and link to each provider's app-registration page. FR-003
> (the placeholder-plus-env-key seam) still lands — it is what makes BYO work at
> all.
>
> **The constraint that holds regardless** (`01PMZ303` spec.md:929-930):
> *"whatever the answer, the button must not lie in the meantime."* FR-002 is
> unaffected by this ruling and is the priority — today those buttons are
> enabled and cannot succeed.
>
> **Separate and NOT covered by this ruling:** the 36 recipes whose sign-in path
> hard-rejects them, 30 of which `oauth.SignInWithDCR` would fix — that
> implementation is written, tested and has zero production callers. Different
> defect, same mission.


**Instance.** `connector-lifecycle-truth-01PMZ303` E-005.

**Question.** Does Kameas register OAuth apps with Atlassian, Salesforce,
HubSpot, Box, Zoom, Discord, Vercel, Webex, Front, RingCentral, Smartsheet,
Wrike and Tableau — or do those 14 recipes ship with an explicit
"advanced: bring your own OAuth app" posture?

**Evidence.** `kitty-specs/connector-lifecycle-truth-01PMZ303/spec.md:922-928` —
FR-003 *"makes the placeholder-plus-env-key seam work. It does not make those 14
connectors work for a user who has not registered an app"*.

**Status quo.** Fourteen shipped recipes carry placeholder client IDs. Their
Connect button cannot succeed.

**Recommended default.** **NEEDS OWNER INPUT (business decision).** The spec's
constraint holds either way (`:929-930`): *"**This is why FR-002 exists** —
whatever the answer, the button must not lie in the meantime."*

**Blast radius.** Register = 14 OAuth app registrations to own and rotate. BYO =
a modal-copy and posture change on 14 recipes.

**Reversibility.** Reversible.

---

### D-4 — Does DCR client-secret support ship, or does the secret half come out?

> ## ✅ RULED 2026-08-19 — owner: alec. **SHIP CONFIDENTIAL-CLIENT SUPPORT.**
>
> DCR stops hardcoding `TokenEndpointAuthMethod: "none"` and supports
> `client_secret_*`. The existing `SecretSaver` / `SecretLoader` / `credstoreKey`
> / `HasSecret` / `ErrDCRExpired` half of `DCRStore` becomes meaningful instead of
> guarding a value nothing can produce.
>
> ### ⚠️ MUST LAND WITH MO-02 AND MO-04
>
> `docs/dead-code-audit-2026-08-18.md:1795` — wiring `SignInWithDCR` without the
> registration-recovery pieces *"ships a flow RFC-conformant providers reject,
> with no recovery from a revoked registration."* Shipping the secret half alone
> makes that worse, not better: a confidential client whose registration is
> revoked has a persisted secret it cannot use and no path to re-register.
>
> **Unblocks X-5** (`MO-10` `RegisteredClient.SecretExpired`) — which under the
> A-0 freeze is no longer a delete candidate anyway, but now has a live consumer
> rather than a dated justification.
>
> **Reach:** DCR is what would fix 30 of the 36 recipes whose sign-in path
> currently hard-rejects them. Distinct from D-3's fourteen placeholder-client-id
> recipes — different defect, same mission.


**Instances.** `MO-03` (`docs/dead-code-audit-2026-08-18.md:1379`); blocks
**X-5** (`MO-10`); interacts with `MO-02`/`MO-04` as *"blocking sub-items of
◆B4"* (`:1795`).

**Question.** Does dynamic client registration support confidential clients?

**Evidence.** `:1379` — `TokenEndpointAuthMethod: "none"` is hardcoded, *"so no
conforming server issues a secret"*, and *"The whole `SecretSaver`/`SecretLoader`/
`credstoreKey`/`HasSecret`/`ErrDCRExpired` half of `DCRStore` guards a value that
cannot be produced."*

**Status quo.** A whole persisted-secret code path guards a value nothing can
produce.

**Recommended default.** **NEEDS OWNER INPUT**, and it must be answered before
`MO-10` is deleted (X-5). Note `docs/dead-code-audit-2026-08-18.md:1795` warns
that wiring `SignInWithDCR` without `MO-02`/`MO-04` *"ships a flow RFC-conformant
providers reject, with no recovery from a revoked registration"* — so the DCR
lane is one decision, not four.

**Reversibility.** Delete of the secret half is **one-way**.

---

# Part 5 — Served-mode parity

**3 decisions · ~8 raw escalations.** Served mode is a deployment *variant*, so
its questions cut across every other theme; the audit proposes a dedicated
mission (`served-mode-is-a-real-mode-01PMZ707`).

---

### E-1 — Is served mode a supported mode, or a documented subset?

> ## ✅ RULED 2026-08-19 — owner: alec. **FULL PARITY — served mode is a real mode.**
>
> Spec `served-mode-is-a-real-mode-01PMZ707` in full, ~10 WPs. Every routed
> served surface either works or refuses honestly, per the dispatch docstring's
> own bar (`core/serve/server.go:537`).
>
> **The authorization leak is sequenced FIRST and does not wait on this ruling.**
> `ConfirmToolModal.vue:109` subscribes to the global `tool:confirm-pending`
> topic with only a `call_id` dedup and no session filter, while
> `Confirm_Resolve`, `Confirm_ResolveAlways`, `Confirm_ApproveBatch` and
> `Elicit_SubmitAnswer` are all served (`core/serve/methods.go:40-47`). So a
> served client renders — and can resolve — another session's tool
> confirmations. **Fix by session-scoping the fan-out, NOT by unserving the
> methods:** they are served for a real reason (`server.go:750-756` — without
> them "every confirm_each tool call in a workbench would hang forever").
>
> **In scope:** the paperclip, `/`, the autonomy chip, title suggestion and all
> ten `Branches_*` calls; `/audit` rendering a clean empty compliance trail when
> the backend refused the query; `/permissions` painting a `'normal'` posture it
> never read.
>
> **Follow-on:** E-2 (promoting `check-serve-dispatch-drift.sh` from
> informational) becomes reachable once parity is the standard — it is currently
> `GATE="${SERVE_DRIFT_GATE:-0}"`.


**Instances.** `SD-03` and `SD-04` serve
(`docs/dead-code-audit-2026-08-18.md:1382-1383`);
`model-authored-graphs-01PMGA01` E-006; `harness-self-attach-01PMHS01` §10
(served-mode assumption).

**Question.** Does every routed served surface either work or refuse honestly —
or does the product declare a supported subset and boundary-panel the rest?

**Evidence.**
- `docs/served-mode-boundary.md:19` — *"Six live views render
  `components/ui/NotAvailableInServedMode.vue` instead"*
- `docs/dead-code-audit-2026-08-18.md:1382` — *"**ten routed served surfaces have
  neither**"* of the doc's two boundary mechanisms
- `core/serve/server.go:537` — the dispatch docstring's own bar: *"than an
  honest refusal, because the user only finds out at the point"*
- `:1383` — the chat surface, *"the sole reason served mode exists"*, violates it

**Status quo.** In a served build the paperclip, `/`, the autonomy chip, title
suggestion and all ten `Branches_*` calls are reachable and unrouted. `/audit`
renders *"a clean, empty, non-erroring compliance trail when the backend refused
the query"* and `/permissions` *"paints a safe-looking `'normal'` posture it
never read"* (`:1854-1856`).

**Recommended default.** **Spec `served-mode-is-a-real-mode-01PMZ707` and
sequence the three authorization leaks first** — `:1858-1861` records that
`tool:confirm-pending` and `elicit:pending` *"ride a global fan-out with no
session filter at either end"* while `Confirm_Resolve`, `Confirm_ApproveBatch`
and `Elicit_SubmitAnswer` are all served, *"so the approval lands."* That is a
cross-session authorization defect, not a parity gap, and it does not wait on
the product ruling.

**Blast radius.** Full parity = ~10 WPs. Documented subset = boundary panels on
ten routes plus a doc rewrite.

**Reversibility.** Reversible; **the authorization leak is live today.**

---

### E-2 — Can `check-serve-dispatch-drift.sh` ever be promoted from informational?

> ## ✅ RULED 2026-08-19 — owner: alec. **GIVE IT AN ALLOWLIST AND PROMOTE IT.**
>
> Seed an allowlist with the 417 current entries, add the reverse direction, and
> flip `SERVE_DRIFT_GATE=1`. The allowlist then shrinks monotonically like every
> other allowlist in `scripts/ci/allowlists/`.
>
> **A gate that runs on every PR and can never fail is decoration** — the exact
> class this campaign exists to eliminate. Today it *"uses `comm -23`, one
> direction only, and has **no allowlist**, so `SERVE_DRIFT_GATE=0` can only be
> flipped by triaging all 417 entries at once"* (`:1455-1457`). The allowlist is
> what makes promotion possible without that triage.
>
> **Gate-extension rule applies:** needs a planted-violation proof in
> `scripts/ci/gates_can_fail_test.go`, per `CLAUDE.md`.
>
> **Sequencing note:** `served-mode-is-a-real-mode-01PMZ707` will churn the entry
> list substantially. Promote the gate FIRST so the mission's own work shrinks a
> live allowlist rather than editing a dormant one.

**Instance.** `SD-12` (`docs/dead-code-audit-2026-08-18.md:1455-1461`);
`connector-lifecycle-truth-01PMZ303` §13 (*"promoting it now would create work
that a product decision might erase"*, `spec.md:1023-1025`).

**Question.** Does the drift gate get an allowlist and a reverse direction, or
stay advisory forever?

**Evidence.** `:1455-1457` — it *"uses `comm -23`, one direction only, and has
**no allowlist**, so `SERVE_DRIFT_GATE=0` can only be flipped by triaging all
417 entries at once."*

**Status quo.** A CI gate runs on every PR and can never fail.

**Recommended default.** **Fold into E-1's mission** — the allowlist is the
artifact that closes the per-method triage, and per the gate-extension rule it
must ship with a planted-violation proof in
`scripts/ci/gates_can_fail_test.go`. Promoting it *before* E-1 creates work a
product decision may erase.

**Reversibility.** Reversible.

---

### E-3 — Should harness-self and graph surfaces be reachable in served mode at all?

> ## ✅ RULED 2026-08-19 — owner: alec. **VERIFY FIRST, THEN RULE.**
>
> Both missions currently exclude served mode **by assumption, not by proof**, and
> both specs say so about themselves. Before either ships, prove or disprove:
> (a) whether any `Graph_*` RPC is reachable in a served build by any route, and
> (b) whether a served session can reach the harness-self MCP pool by any route
> other than the one already checked.
>
> **Prior expectation, to be tested not trusted:**
> `01PMGA01` spec.md:928-929 — *"`core/serve/methods.go` dispatches no `Graph_*`
> RPC, so served mode cannot reach this surface **by the route I checked**."*
> The hedge is the point.
>
> **Given E-1 (served mode is a real mode), the likely outcome is "route them
> properly" rather than "assume excluded"** — which would widen both
> `01PMHS01` and `01PMGA01`, since both were scoped assuming exclusion. Budget
> for that.
>
> **This is an action item with a ruling attached, not a pure product call.**
> Assign the verification before either mission's WP01 closes.
>
> ### ✅ VERIFICATION DONE 2026-08-19 — the assumption HOLDS, for both routes
>
> **(a) `Graph_*` is genuinely unreachable in served mode, not merely unrouted.**
> `core/serve/methods.go` contains **zero** `Graph_` entries (`grep -c '"Graph_'`
> → 0), while `LLM_StartStream` IS served (`:49`) — so chat streams and graph
> authoring does not. Decisively, the dispatch `default:` is a **hard refusal**,
> not a passthrough: `core/serve/server.go:915` returns `unknownMethodError`
> under the comment *"FR-005: no fake success — the caller gets a clear error,
> never silent fake data."* The WS path refuses identically at `:966`
> (*"unknown stream method"*). And the list is not decorative: it is pinned by
> `TestServedMethodsMatchDispatchSwitch`, which parses `server.go` and fails on
> divergence — so the method list IS the served surface, enforced by a test.
>
> **(b) The served entry point never constructs the harness-self server or pool.**
> `cmd/harness-served/main.go` contains no reference to `harnessServer`,
> `NewTransport`, `mcpPool` or `Pool`.
>
> **What this does and does not prove.** It proves no *RPC or WS* route reaches
> either surface. It does not prove reachability is impossible by some third
> mechanism nobody has thought of — but both documented routes hard-refuse, and
> the hedge in `01PMGA01` spec.md:928-929 (*"by the route I checked"*) is now
> discharged for the routes that exist.
>
> **Consequence for E-1 (served = a real mode):** routing graph authoring into
> served mode would be **new capability work**, not the closing of a leak. Scope
> it deliberately in `served-mode-is-a-real-mode-01PMZ707` or exclude it there
> explicitly — but `01PMHS01` and `01PMGA01` may keep their current scope, since
> their exclusion assumption is now proven rather than assumed.


**Instances.** `harness-self-attach-01PMHS01` §10 (*"nobody has confirmed a
served-mode session cannot reach the pool by another route"*, `spec.md:646-650`);
`model-authored-graphs-01PMGA01` E-006 (`spec.md:927-932`).

**Question.** Are these desktop-only by design, or merely unrouted today?

**Evidence.** `kitty-specs/model-authored-graphs-01PMGA01/spec.md:928-929` —
*"`core/serve/methods.go` dispatches no `Graph_*` RPC, so served mode cannot
reach this surface by the route I checked."*

**Status quo.** Both missions exclude served mode by assumption, not by proof.

**Recommended default.** **Verify before shipping, or state the assumption in
the mission report** — both specs say exactly this. This is a verification task
with a product ruling attached, not a pure product call.

**Reversibility.** Reversible.

---

# Part 6 — Justifications with no owner

**2 decisions · ~9 raw escalations.** CLAUDE.md: *"A justification names the
blocker and the owner — the change that will delete the line. 'We'll get to it'
is not a reason."* These findings are parked on justifications that name neither.

---

### F-1 — Which parked justifications get an owner, and which convert to escalations?

> ## ✅ RULED 2026-08-19 — owner: alec. **CONVERT ALL SIXTEEN TO ESCALATIONS.**
>
> Nothing parks by default. The sixteen `**Owner:** unassigned` rows in
> `docs/unwired-ledger.md` (plus one in the audit) each become an escalation
> requiring a decision, rather than being assigned an owner and left standing.
>
> **The rigorous reading of the ritual's own rule.** A justification must name a
> blocker AND an owner; a row that names neither is not a justification, it is
> unexplained code with a label on it. Assigning a name would have satisfied the
> letter of the rule while preserving the thing it exists to prevent.
>
> ### ⚠️ This creates SIXTEEN NEW DECISIONS
>
> They are not in this register — it covered escalations that already existed.
> **A second sitting is required**, and it should follow the same method: index
> them, collapse duplicates by theme, attach evidence and a recommended default
> to each, and rule in bulk. Expect meaningful collapse; several of the sixteen
> concern the same subsystems this register already ruled on (bundle/trust,
> narrative — now retired, compaction overhead — now wired), so some will resolve
> by reference rather than needing a fresh decision.
>
> **Exceptions already ruled, do NOT re-open:** `F-2`
> (`fleet-org-config-inheritance`) was explicitly assigned owner + blocker in this
> sitting; `C-2` (`WithPubKey`), `C-4` (MCP sampling), `A-6` (the MCP hook seam),
> `A-10` (`Engine.Cache`) and `B-5` (the prune scheduler) all received dated
> justifications with named blockers here and are compliant as they stand.

**Instances.** `synckind-org-scope-declared-only`
(`docs/dead-code-audit-2026-08-18.md:707`) and
`fleet-enforcement-truth-01PMZ505` E-003;
`bootstrap-consent-checker-nil` (`:322`);
`narrative-settings-gate-inert` (`:733`, see X-3);
`compaction-overhead-row-writerless` (`:758`, see X-7);
`event-family-orphan-const` (`:759`); the ~16 bundle/trust justifications naming
archived missions (A-1); the ledger's 15+ `**Owner:** unassigned` rows.

**Question.** For each parked line: name the person and the change that deletes
it, or convert it to an escalation?

**Evidence.**
- `core/fleet/synckind.go:130` — `func (k SyncKind) HasScope(s Scope) bool {`
- `kitty-specs/fleet-enforcement-truth-01PMZ505/spec.md:1109-1110` — *"A
  justification that names a mission but not a person is the 'we'll get to it'
  the ritual forbids."*
- `docs/dead-code-audit-2026-08-18.md:322` — *"**escalate, not justify** — the
  blocker … is named … but there is **no owner and no date**"*
- `:1874-1877` — the bundle/trust justifications name **archived** missions with
  *"zero recorded work packages"*

**Status quo.** Allowlists and the ledger hold entries that will never be
revisited because nobody is on the hook.

**Recommended default.** **Convert every ownerless justification to an
escalation this release.** That is the ritual's own rule, and `spec.md:1112`
notes it *"**Blocks WP10's ledger entry**, not its code"* — cheap to do, and it
is what keeps the allowlists shrinking monotonically.

**Blast radius.** Administrative. It moves ~9 lines from "justified" to
"undecided", which is honest.

**Reversibility.** Fully reversible.

---

### F-2 — Is `fleet-org-config-inheritance-01NORGX01` wanted, and who owns it?

> ## ✅ RULED 2026-08-19 — owner: alec. **WANTED. Owner: alec.**
>
> `justify(owner: alec, blocker: "kenaz-fleet org endpoints not yet available",
> date: 2026-08-19)`.
>
> `SyncKind`'s org-scope fields and `HasScope` stay; the four `ScopeOrg`
> declarations stay. The org layer is real product direction for a fleet-managed
> harness and the declarations are already shaped for it.
>
> **What this fixes:** the mission was *"on disk, unstarted, with no named
> owner"* (`01PMZ505` spec.md:1105-1112), which fails the ritual's own rule that a
> justification must name a blocker AND an owner. It now names both.
>
> **Resolves the concrete instance of F-1.** Apply the same treatment to the
> remaining parked justifications when F-1 is taken: an owner and a blocker, or
> convert to an escalation.


**Instance.** `fleet-enforcement-truth-01PMZ505` E-003.

**Question.** If nobody claims the org-config-inheritance mission, do
`SyncKind`'s org-scope fields and `HasScope` get deleted and the four `ScopeOrg`
declarations reduced to `ScopeUser`?

**Evidence.** `kitty-specs/fleet-enforcement-truth-01PMZ505/spec.md:1105-1112` —
the mission is *"on disk, unstarted, with **no named owner**"*.

**Status quo.** Four sync kinds advertise an org layer that does not exist.

**Recommended default.** **NEEDS OWNER INPUT — assign or delete.** This is the
concrete instance of F-1 and can be decided in the same sitting.

**Reversibility.** Reversible.

---

# Appendix A — What could NOT be determined from the tree

Marked here rather than given a recommendation, because guessing product intent
is the failure mode this register exists to prevent:

- **A-1** bundle/trust — whether signed-artifact distribution is a product.
- **A-3 / D-1** structured output + grammar decoding — no surface asks for them.
- **A-8** the `approval` node — no shipped graph uses it.
- **A-11** workflow input pickers — build-vs-migrate is a scoping call.
- **A-12** deferred asks + wizards — no producer, no stated intent.
- **B-3** the scheduled-run Cedar policy — a security posture.
- **B-5** automatic memory prune — data deletion without consent.
- **C-1** persistent audit log — needs compliance input, not code.
- **C-2** per-device catalog signing key — the source is out of repo.
- **C-3** unpublish rights — a permissions question.
- **C-4** MCP sampling — a consent-surface question.
- **D-3** 14 OAuth app registrations — a business decision.
- **D-4** DCR confidential clients — a protocol-scope decision.
- **F-2** org-config inheritance — needs an owner, not an analysis.

---

# Appendix B — Verification ledger

**What was read**, all in the main checkout on branch `release/v0.59.0`, with
`.claude/worktrees/` and `.worktrees/` excluded from every search:

- `kitty-specs/{model-settings-reach-the-model-01PMZ101, trust-surfaces-that-fire-01PMZ202,
  connector-lifecycle-truth-01PMZ303, automation-actually-runs-01PMZ404,
  fleet-enforcement-truth-01PMZ505, model-scheduled-jobs-01PMSJ01,
  model-authored-graphs-01PMGA01, harness-self-attach-01PMHS01}/spec.md` —
  escalation sections read in full (§10/§11/§12) plus surrounding context.
- `docs/dead-code-audit-2026-08-18.md` — §2 class tables, §6 verifier extras, §7
  ledger cross-reference, and the entire closing sweep (`:1286-1936`) including
  the calibration audit reproduced at `:1646-1752`.
- `docs/dead-code-audit-2026-08-16.md` — all `####` finding headers and
  `**Disposition**` lines; `B12` read in full at `:461-471`.
- `docs/unwired-ledger.md` — section index plus `:592-714` read in full.
- `scripts/ci/allowlists/i10-unwired-gates.txt:215-270`.

**Citation spot-checks.** Every `file:line` in a code file cited above was
re-read with `sed -n '<line>p'` before being written here. Thirty-five code and
allowlist citations were checked; **thirty-four matched exactly**. One near-miss is
recorded rather than silently corrected: the audit's
`telemetry-otlp-no-producer` row (`:732`) cites `core/core.go:124` for
`core.Options.Telemetry`; line 124 is `type TelemetryOptions struct {` — the
type, not the field. The finding is unaffected; the citation is one line off its
subject and is **not** used as evidence in this register.

**Counting method.** "Raw escalations" = distinct `E-NNN` identifiers per mission
(`grep -oh 'E-0[0-9][0-9]' <mission>/*.md | sort -u`, giving
4/6/5/4/5/5/8 across the seven numbered missions) plus six unnumbered blockquote
escalations in `harness-self-attach-01PMHS01` §10; plus disposition-column and
prose `escalate` / `justify`-without-owner values extracted from the two audit
documents; plus the delete-dispositioned findings that fail the ruling test.
The total is stated as **~117** because the audits mix table rows, prose
dispositions and grouped clusters (e.g. the C-ii bucket is one row listing
thirteen client methods), so an exact integer would be false precision.

**Not done.** No escalation was resolved. No spec, audit, ledger or allowlist was
modified. No test was run and no code was compiled. This document is the only
file written.

---

# Part 7 — PREMISE CORRECTIONS (2026-08-19, from spec re-derivation)

Twelve spec agents re-derived every prescription before building on it. **Four
rulings above rest on premises that turned out to be false.** Every ruling's
OUTCOME stands; only the stated reason was wrong. Recorded here rather than
edited inline, so the record shows what was believed and what was found.

This is the calibration audit's warning landing exactly where it predicted:
*"Trust the defects; do not trust the fix plans."*

### C-1 — "register the six event-log migrations" DOES NOT COMPILE

The two types are unrelated. `eventlog.Migration` is `{ID string; SQL string}`
(`core/event/log/register.go:11-14`) — a raw SQL blob. `migrations.Migration`
(`core/storage/migrations/types.go:21-26`) requires `ID`, `Version`,
`OwningMission`, and `Up`/`Down` **functions**. The registries differ too:
`migrations.Registry.Register(m Migration) error` takes one argument; the
eventlog one takes `(owner, []Migration)`.

**A REWRITE, not a wire** — it materially enlarges `persistent-audit-store-01PMZA10`.
Doc lie found alongside: `register.go:55` says "four"; `Migrations()` returns six.

**The same shape recurs in `core/tasks`** — `tasks.RegisterMigrations` has **zero**
callers, its `Migration` is `{ID,SQL}` with `Register(owner string, …)`, and no
version block is reserved. **The `tasks` table exists on no install.** Second
instance of the pattern; check for a third before assuming it is only these two.

### A-13 — `kenaz__subagent_dispatch` is NOT live

The ruling said it was *"already live"* and that the mission would *"make a
shipped capability observable, not invent one"*. **False.**
`core/rpc/builtins_wiring.go:312-313`: `var subagentSeam agentgraph.BranchSeam
// nil — no child-run spawner yet`, followed by `if subagentSeam != nil`. The
registration inside is **statically unreachable**.

So the mission must first build the child-run spawner the guard comment names.
Filed as E-001 in `01PMZB11` with a cut line. Ruling (build it) unchanged;
**size and risk go up.**

### A-8 — the halt-and-resume seam and approval UI BOTH EXIST

The ruling said *"neither of which exists"*, quoting `01PMZ202`. **False.** The
`ask` node uses both every chat turn, and the kernel's pause path keys on
`r.Pause` / `ErrPaused` (`core/agentgraph/kernel.go:532,570`), **not on node
kind**.

What is actually missing is narrower and different: an approval-shaped pending
record, a verdict-carrying resume verb (`Graph_Resume` takes free text), the
`rejected` port write, and durable run state — `Manager.runs` is a plain map,
so **AC-PI-1 is unmet today for ANY pause, `ask` included.** That enlargement is
`01PMZC12` E-002.

### F-2 — `01NORGX01` was never ownerless

The escalation said the mission was *"on disk, unstarted, with no named owner"*.
**False** — `kitty-specs/fleet-org-config-inheritance-01NORGX01/spec.md:3` has
read `**Owner**: alecfeeman` since 2026-07-18. Genuinely missing were a
**blocker**, a **date**, and a `meta.json`; all three have been added.

Ruling unchanged. Recorded so the next sweep does not re-discover a defect that
never existed.

---

**Standing lesson.** Three of these four came from documents this campaign
treats as authoritative — the audit and this register. A ruling is a decision
about *what to do*; it is not evidence about *what the code is*. Every mission's
WP01 re-derives, and that requirement is why these were caught before an
implementer budgeted work against them.

---

# Part 8 — Second sitting (ownerless parks converted per F-1)

Owner ruling **F-1** (2026-08-19) converted every ownerless parked justification
into an escalation: *"Nothing parks by default."* This part is the second
sitting F-1 called for.

**Nothing here is resolved by this document.** Same method as the 42: index the
rows, resolve by reference wherever an existing ruling already answers one,
collapse the remainder by theme, and attach evidence plus a recommended default
to each. The owner rules.

---

## Tally

| | Count |
|---|---|
| `**Owner:** unassigned` rows in `docs/unwired-ledger.md` (F-1's sixteen) | **16** |
| Ownerless row in `docs/dead-code-audit-2026-08-18.md` | 1 |
| **Additional ownerless parks F-1's count missed** (see §8.3-P2) | **+4** |
| **Total ownerless parks in scope** | **21** |
| — resolved by reference to an existing ruling | **5** (+2 partial) |
| — closed on verification (not open at all) | **1** |
| — requiring a new decision | **16 instances** |
| **Distinct new decisions (G-1…G-9)** | **9** |

Collapse on the new-decision side is **1.8×** — materially lower than the first
sitting's 2.8×, and that is expected rather than a shortfall. The register's
~117 raw items were drawn from eight specs and three audits that describe the
same defects repeatedly; the ledger's rows are deduplicated by construction, so
there is far less redundancy to collapse. The real leverage here is *inside*
rows: **G-4 is one row covering twelve settings fields** and **G-5 is one row
covering six components**.

**Ordered by instances unblocked:** G-1 (3), G-2 (3), G-3 (4, three of which
F-1's count missed), then six single-row decisions. **The three that unblock
the most work** are G-1, G-2 and G-4 — G-4 by field count rather than row count.

---

## §8.0 — Index of all 21 ownerless parks

Rows are identified by their **dated ledger heading**, which is stable, with the
line number **as it stood before this change** in parentheses. Applying the
pointer edits shifted every line below the first one — which is exactly the
drift §8.3-P3 documents, so it is named here rather than reproduced.

| # | Ledger entry (heading · pre-edit line) | Subsystem | Current justification (gist) | Disposition |
|---|---|---|---|---|
| 1 | `2026-08-14 · export.RedactValue only walks TOP-LEVEL strings` (`:318`) | `export.RedactValue` recursive walk | "the change that closes it is a recursive walk in `redactMessages`" | **CLOSED on verification** → §8.3-P1, pointer in G-3 |
| 2 | `2026-08-14 · A move cannot be multimodal` (`:394`) | multimodal move expressiveness hole | "needs a producer, which is feature work with a spec" | **G-2** |
| 3 | `2026-08-14 · The exit gate's revised answer never reaches the stream` (`:444`) | exit gate's revised answer never streams | "the fix is backend: deliver the gate's revised text" | **G-2** |
| 4 | `2026-08-14 · views/search/impl.go searches soft-archived rows` (`:466`) | `views/search` searches soft-archived rows | "two honest exits: add the predicate … or an explicit filter" | **G-1** |
| 5 | `2026-08-14 · core/search/search.go is a dead second search engine` (`:485`) | `core/search` is a dead second engine | "adopting `core/search` or deleting it are both fine" | **G-1** |
| 6 | `2026-08-14 · Role == RoleAssistant is a staleness class` (`:538`) | `RoleAssistant` staleness class + the owed gate | "unassigned, for all three parts" | **G-2** |
| 7 | `2026-08-14 · Migration 0335's tool-row purge is not idempotent` (`:565`) | migration 0335's non-idempotent FTS purge | "the cheap closure is a guard" | **G-8** |
| 8 | `2026-08-14 · SearchModal takes ?role= from the URL unvalidated` (`:589`) | `SearchModal` `?role=` unvalidated | "one line in `readFromRoute`" | **G-1** (rider) |
| 9 | `2026-08-14 · Deferred asks have no producer, and wizards have no caller` (`:619`) | deferred asks + wizards | "two honest exits: (a) add `mode` + `questions` … or (b) delete both legs" | **RESOLVED → A-12** |
| 10 | `2026-08-14 · Live tools whose only UI is unmounted (todo, sub-agent)` (`:648`) | todo + sub-agent unmounted UI | "Todo: wire … Sub-agent: delete, or spec the control RPCs first" | **RESOLVED → A-13** |
| 11 | `2026-08-14 · The denial UX gap` (`:676`) | the denial UX gap (`policyAPI` stub) | "needs a mission under `kitty-specs/`" | **G-6** |
| 12 | `2026-08-14 · LocalRuntimesSection has a branch it can never render` (`:745`) | `LocalRuntimeInfo.Models` never populated | "either populate `Models` … or delete the first branch" | **G-7** |
| 13 | `2026-08-14 · Settings fields that are stored, bound, and inert` (`:779`) | settings fields stored, bound, inert | "the cheapest structural fix is to bring `settings.Settings` under knobcoverage" | **PARTIAL → A-4** (6 narrative knobs) + **G-4** (6 remaining) |
| 14 | `2026-08-14 · The background-task subsystem has no producer` (`:831`) | background-task subsystem has no producer | "the fix is one restructuring" | **RESOLVED → A-13** (+ A-7) |
| 15 | same entry, follow-up (`:860`) | background-task follow-up | "same owner as the parent entry" | **RESOLVED → A-13** |
| 16 | `2026-08-14 · Frontend orphan backlog` (`:1083`) | frontend orphan backlog | "unassigned per-item; owned by whoever picks up the next sweep" | **PARTIAL → A-13** (P1b) + **G-5** (P1) |
| 17 | `docs/dead-code-audit-2026-08-18.md:758` | compaction-overhead header row | "labelled 'justify' with `OWNER: unassigned`" | **RESOLVED → X-7** |
| 18 | `2026-08-18 amendment (mcp-connector-lifecycle-01PMMC01 WP01)` (`:1101`) | harness-self attach execution owner | "unassigned as of this entry" (blocker IS named) | **G-9** — *missed by F-1's count* |
| 19 | `2026-08-16 · what the export scanner covered BEFORE` (`:1419`) | truncated-PEM redaction gap | "the fix is a length-bounded variant" | **G-3** — *missed by F-1's count* |
| 20 | same entry (`:1426`) | `core/event/redact.defaultMatchers` not widened | "the wrong blast radius" | **G-3** — *missed by F-1's count* |
| 21 | same entry (`:1433`) | `core/eval/capture.go` `redactString` | "gated behind eval capture being enabled" | **G-3** — *missed by F-1's count* |

---

## §8.1 — Resolved by reference (no owner time required)

These cost the owner nothing. Each row is already answered by a 2026-08-19
ruling; the ledger edits in this change point each one at its ruling.

### R-1 · `:619` deferred asks + wizards → **A-12**

A-12 ruled **BUILD BOTH LEGS** and names this exact row as its instance
(*"Instance. `docs/unwired-ledger.md:592-623`"*). Add `mode` to
`askuserquestion.AskArgs`, wire `OpenWizard`'s missing call site, mount
`DeferredAskPill.vue` + `DeferredAskPanel.vue`. The row's option (b) — "delete
both legs" — is additionally foreclosed by **A-0**'s delete-lane freeze.

### R-2 · `:648` todo + sub-agent unmounted UI → **A-13**

A-13 ruled **BUILD THE CONTROL RPCs + the background-task producer**, names
this exact row as its instance (`docs/unwired-ledger.md:625-651`), and
deliberately **reverses this row's own recommended default** ("Sub-agent:
delete"). A-13 also splits the row the way the row itself did: *"the sibling
`todo` case is the opposite call: wire it."*

⚠️ Carry **Part 7's correction** into any work here: A-13's stated premise that
`kenaz__subagent_dispatch` is "already live" is **false**. Re-verified
2026-08-19 — `core/rpc/builtins_wiring.go:312-313` reads
`var subagentSeam agentgraph.BranchSeam // nil — no child-run spawner yet`
followed by `if subagentSeam != nil`, so the registration inside is statically
unreachable. Ruling unchanged; size and risk go up.

### R-3 · `:831` the background-task subsystem has no producer → **A-13** (+ **A-7**)

A-13 requires the background-task producer explicitly, because **A-7** ruled
that all eight fire-less hook events get producers and three of them —
`subagent_start`, `background_task_complete`, `worktree_create` — cannot be
built without this seam. Re-verified 2026-08-19: `builtins_wiring.go:321` still
reads `Tasks:   nil,` and `core/tools/bash/Options.BackgroundSpawn` still has
assignments only in `run_in_background_test.go`.

### R-4 · `:860` background-task follow-up → **A-13**

The row's own text is *"same owner as the parent entry"*, so it follows R-3.
Its instruction stands under A-13: *"If background execution ships, remount the
panel and restore the nav entry in the same PR that wires the producer."*

### R-5 · `docs/dead-code-audit-2026-08-18.md:758` compaction overhead → **X-7**

X-7 ruled **ONE FINDING, WIRE IT**, owner alec, and states the fix explicitly:
*"`:758` carried `OWNER: unassigned`, which fails the ritual's rule outright.
It now has an owner and a disposition."* Verified 2026-08-19:
`frontend/src/views/sessions/SessionsView.vue:948` still reads
`const compactionOverheadUSD = ref<number>(0);` with no other writer.

### R-6 (partial) · `:779` the six narrative tuning knobs → **A-4**

`SummarizerProfileID`, `NarrativePromotionWeights`, `NarrativePromotionThreshold`,
`NarrativeRetrievalWeight`, `NarrativePromoterParallelism` and
`NarrativePreludeTopN` are retired with the subsystem under **A-4**
(documented product retirement). Verified 2026-08-19 that all six are
memory-narrative-scoped: `core/memory/narrative/synthetic.go:37` names
`Settings.SummarizerProfileID`; `core/memory/narrative/prelude.go:28` names
`NarrativePreludeTopN`. The row's remaining six fields are **G-4**.

### R-7 (partial) · `:1083`'s P1b bullets → **A-13**

The P1b block parks two capabilities *"pending a named mission"*: **delegated
sub-agent execution** (`AgentsView.vue`, `AgentProfileEditor.vue`,
`SubagentTab.vue`, `SubagentBudgetMeter.vue`) and **background execution**
(`TaskOutputViewer.vue`, `SessionCloseDialog.vue`, `BackgroundTaskChip.vue`).
A-13 is that named mission for both. The P3 item (`lib/capability-keys.ts`) is
not ownerless — it carries a live disposition (*"Being wired as a typed
import"*). The remaining **P1** list is **G-5**.

---

## §8.2 — New decisions

---

### G-1 — Which search implementation is the product's, and does compacted history stay findable?

> ## ✅ RULED 2026-08-19 — owner: alec. **Recommended default accepted.**
>
> Add the archived-row predicate to the LIVE implementation **and** an explicit
> "include compacted history" filter, so the capability is not silently removed
> along with the defect. Ship the `readFromRoute` membership check as a rider.
>
> `core/search` is **not deleted** — A-0's freeze applies even though the ledger
> correctly classes it as rival infrastructure, which is one of the rubric's four
> delete classes. Either adopt it as the single engine or
> `justify(blocker, owner: alec, date: 2026-08-19)`.

**Instances.** `docs/unwired-ledger.md:448-470` (`views/search` searches
soft-archived rows); `:471-487` (`core/search` is a dead second engine);
`:569-590` (`SearchModal` `?role=` unvalidated — a rider on the same surface,
included because it has no owner, not because it is a product question).

**Question.** Does the live search surface stop returning compaction-archived
rows — and is `core/search` adopted as the single engine or parked with a
blocker?

**Evidence.**
- `core/search/search.go:98` and `:110` — `AND sm.archived_at IS NULL` on both
  query shapes.
- `core/rpc/views/search/impl.go` — `grep -c archived_at` returns **0**. This is
  the implementation the Wails binding and served mode call.
- `core/search/search_test.go:9` is the **only** importer of `core/search`
  anywhere in the tree.
- `frontend/src/components/search/SearchModal.vue:60` —
  `roleFilter.value = get('role');` — no membership check; `:118` forwards it;
  the `<select>` at `:347-350` offers exactly `""`, `user`, `assistant`,
  `system`.

**Status quo.** A row that compaction soft-archived is gone from the transcript
and gone from the model's history, and is still a search hit that navigates the
user to a message the session no longer renders. Separately, a deep link
carrying `?role=tool` puts the modal in a state its own control cannot express
and cannot leave — the select renders blank and every query returns zero hits.

**Recommended default.** **Add the predicate to the live implementation and add
an explicit "include compacted history" filter**, so the capability is not
silently removed along with the defect. Then either **adopt `core/search` as
the single engine** or `justify(blocker, owner, date)` — under **A-0** it cannot
be deleted this campaign, even though the ledger correctly classes it as *rival
infrastructure*, which is one of the rubric's four delete classes. Ship the
`readFromRoute` membership check as a rider in the same change.

**Blast radius.** Adding the predicate alone makes compacted content
unfindable, which users will notice — hence the paired filter. Leaving it
navigates users to invisible messages. Adopting `core/search` swaps the engine
behind a live binding and a served method.

**Reversibility.** Reversible. Note the ledger forbids the third path: *"the two
implementations should not keep disagreeing silently."*

---

### G-2 — Who owns the three transcript-truth findings a closed mission left behind?

> ## ✅ RULED 2026-08-19 — owner: alec. **Wire two, gate one, park one.**
>
> (a) Deliver the exit gate's revised text as a move boundary **plus deltas** —
> the boundary already fires, only the text is missing. (b) The one-line filter
> to `MoveKind() == MoveKindFinal || MoveKind() == ""` at both branch sites.
> (c) The owed gate, with a planted-violation proof in
> `scripts/ci/gates_can_fail_test.go` — the candidate set is small and
> enumerable (eleven non-test files). (d) **Park** the multimodal move:
> `justify(blocker: "no multimodal tool-result producer exists —
> agentgraph.ToolResult carries no blocks", owner: alec, date: 2026-08-19)`.

**Instances.** `docs/unwired-ledger.md:425-446` (the exit gate's revised answer
never reaches the stream); `:489-540` (`Role == RoleAssistant` is a staleness
class, and no gate sees it); `:361-396` (a move cannot be multimodal). All three
are 01PMCH01 residue — the mission CLOSED 2026-08-14 and no WP claimed them.

**Question.** For each of the three: wire it, or park it with a named blocker
and owner — and does the gate CLAUDE.md's gate-extension rule already required
get written this release?

**Evidence.**
- `core/rpc/views/agentgraph/chat/moves.go:409` —
  `absorbed := j.heldLive && strings.TrimSpace(j.held) == strings.TrimSpace(entry.Content)`.
  The non-absorbed (revised) branch calls `j.allocate(moveKindFinal, moveDetail{})`
  at `:413`, and `allocate` (`:154-169`) emits only
  `Kind: coreag.StreamEventMoveStart` — **a boundary carrying no text.** So the
  revised answer is persisted and never streamed.
- `core/rpc/views/branches/impl.go:378` (tail-5 branch summary) and `:464`
  (last-≤8 turns for `ReintegrationProposal`) — both still `if m.Role ==
  session.RoleAssistant {`, so both sample the model's thinking-out-loud
  alongside its answers.
- `core/agentgraph/seams.go:322-325` — `type ToolResult struct { Content string;
  IsError bool }`, with no blocks field; `agentgraph.HistoryEntry` likewise.
- The owed gate: `:520-523` records that the merge-base→WP06 diff *"touches no
  file under `scripts/ci/`"*, so CLAUDE.md's gate-extension rule is **not
  satisfied** for a class WP06 itself identified.

**Status quo.** A user watches a draft stream, the exit gate revises it, and the
live view keeps showing the draft while a reload shows the revision — the
transcript is right and the surface is wrong. Branch summaries and reintegration
proposals quote interim narration as if it were answers. Multimodal moves are
inexpressible, but nothing needs them today.

**Recommended default.** **Wire the first two; write the owed gate; park the
third.** Specifically: (a) deliver the revised text as a move boundary **plus
deltas** so the surface can replace the draft bubble — the boundary already
fires, only the text is missing; (b) the one-line filter to
`MoveKind() == MoveKindFinal || MoveKind() == ""` at both branch sites; (c) the
gate, with a planted-violation proof in `scripts/ci/gates_can_fail_test.go`, per
the rule — the ledger already proves the candidate set is small and enumerable
(eleven non-test files); (d) `justify(blocker: "no multimodal tool-result
producer exists — `agentgraph.ToolResult` carries no blocks", owner: <name>,
date: 2026-08-19)` for the multimodal hole, since **A-0** forecloses deleting it
and it is the rubric's no-producer case.

**Blast radius.** (a) touches the chat stream contract on a path that fires only
when the gate revises. (b) is one line each and makes summaries quieter. (c) is
CI-only. (d) is a documentation line.

**Reversibility.** All reversible. Note (b)'s cited lines in the ledger
(`:366`, `:452`) have **drifted** — see §8.3-P3.

---

### G-3 — Four redaction catalogs have diverged, and the weakest one writes to disk

> ## ✅ RULED 2026-08-19 — owner: alec. **ONE OWNER, ONE SHARED CATALOG.**
>
> The widened export catalog (recursive walk, key scanning, `MaxRedactDepth`)
> becomes the single source; the Sentry Go, Sentry TS and eval-capture catalogs
> **consume it rather than copying it**.
>
> ### The urgent instance
> `core/eval/capture.go:137` `redactString` handles `sk-` and `Bearer ` and
> nothing else — while its own doc comment claims it also redacts the `env:`
> credential-locator format, and there is **no code for that**. It writes full
> LLM `Messages` to `<DataDir>/eval-captures/<session_id>.jsonl`. AWS keys,
> GitHub tokens, JWTs, database URLs and email addresses reach disk in
> plaintext. **Fix the doc-comment lie in the same commit as the behaviour** —
> a reader trusting that comment is the failure mode.
>
> **Gate owed:** a new redaction catalog must CONSUME the shared one, not copy
> it. Needs a planted-violation proof. Without it a fifth catalog appears and
> the next sweep finds this again — this is the third incomplete redactor the
> campaign has found (export, fixed v0.63.1; Sentry, both languages; this one).

**Instances.** `docs/unwired-ledger.md:1419` (truncated PEM not matched);
`:1426` (`core/event/redact.defaultMatchers` not widened); `:1433`
(`core/eval/capture.go` `redactString`); plus `:318`, which is **not open** —
see §8.3-P1 — and whose ledger row should be closed with a pointer rather than
escalated.

**Question.** Who owns keeping the four redaction catalogs from diverging, and
does the eval-capture writer get brought up to the export catalog's standard
before eval capture is ever enabled by default?

**Evidence.**
- `core/eval/capture.go:137` — `func redactString(s string) string {`, whose own
  doc comment lists its entire coverage: *"sk-ant-…, sk-…, Bearer …"* plus the
  `env:` locator format. No GitHub token, no AWS key, no JWT, no password, no
  cookie. The ledger's framing at `:1433`: *"it writes LLM messages to disk"*,
  and its own comment calls it *"defense-in-depth"* behind the event log —
  *"which is true of the event-log path and not of the capture FILE."*
- `docs/unwired-ledger.md:1426` — `core/event/redact.defaultMatchers`
  *"still has all ten original patterns including the JSON-blind generic"*, and
  it feeds the audit log's HMAC pipeline.
- `:1419` — the PEM pattern requires the `-----END … PRIVATE KEY-----` marker
  while `capToolOutput` truncates at 4000 runes.

**Status quo.** The export catalog was widened substantially (recursive walk,
key-name anchoring, eleven new shapes). The other three were not, deliberately
and with stated reasons — but with **no owner on any of them**, so the
divergence has no closing date. The eval-capture one writes model messages to a
file with a two-pattern scanner.

**Recommended default.** **One owner for all four catalogs, and a deadline on
the eval-capture one specifically.** The other two parks are well-reasoned and
should become compliant `justify(blocker, owner, date)` lines rather than work:
`:1426`'s blocker is *"widening a live audit pipeline from inside an export fix
is the wrong blast radius"* — a real blocker that only lacks a name; `:1419`'s
is precision loss. `core/eval/capture.go` is different in kind: it is the only
one whose failure mode is a credential at rest on the user's disk, and *"gated
behind eval capture being enabled"* is a mitigation, not a blocker. Under the
rubric this is **trust-relevant**, which is the class that does not get parked
without an explicit ruling.

**Blast radius.** Widening `capture.go` to reuse the export catalog is small and
local. Widening `core/event/redact` is not — it changes what a live HMAC audit
pipeline emits, which is why it should stay parked with a name on it.

**Reversibility.** The code changes are reversible. **A credential already
written to a capture file is not.**

---

### G-4 — Does `settings.Settings` come under `knobcoverage`, and what happens to the six remaining inert fields?

> ## ✅ RULED 2026-08-19 — owner: alec. **BRING `settings.Settings` UNDER `knobcoverage`.**
>
> The mechanism is already generic (`knobcoverage.Register[T any]`), so this is
> **one registration site plus a guard test**, not a script change — ~78/79
> exported fields tracked.
>
> **Honest coverage:** it reaches 5 of the 8 known settings findings.
> `controls-and-readouts-01PMZ808` named the three it structurally cannot —
> `SchemaVersion` registers clean while its defect survives,
> `MaxGeneratedImageBytes` has a consumer, and `SD-06`/`SD-07` are interface
> **methods**, out of reflection's reach. Wire or narrow those three directly.
>
> **Ships the missing planted proof in the same WP.** `pr.yml:374-383` lists
> *"the check-knob-coverage vacuous-pass fix"* among the proofs;
> `grep -c 'knob' scripts/ci/gates_can_fail_test.go` returns **0**. See task #48.
>
> **Constraints:** `Uncovered[T]` panics on an untracked type, `Register` panics
> on duplicates, the site must live under `./core/...`, and must not be in the
> mechanism's own package.

**Instances.** `docs/unwired-ledger.md:750-781` (settings fields that are
stored, bound and inert). Unblocks `:782-798` (knob-coverage tracks one struct
out of the several that need it) in the same answer. Six of the row's fields are
already resolved by **A-4** — see R-6.

**Question.** Does the largest knob surface in the tree (~78 exported fields)
come under `core/wiring/knobcoverage`, and for each remaining inert field: wire
a consumer, or justify with a blocker and an owner?

**Evidence** — each re-verified 2026-08-19:
- `EffectivePermissionMode()` (`core/rpc/views/settings/api.go:887`) has exactly
  three callers, all store accessors: `impl.go:558`, `:560`, `:1714`. Nothing
  branches on the value.
- `MCPAutoRestart()` (`api.go:967`) has no reader anywhere in `core/mcp`;
  `core/mcp/transport/stdio/supervisor.go:56` calls `s.attemptRestart()`
  unconditionally. The field's own comment at `api.go:333` concedes it:
  *"MCPAutoRestart() accessor; never read directly."*
- `EffectiveLocalRuntimeRAMBytes` (`api.go:1639`) has **zero** callers.
- `SkippedUpdateVersions` (`api.go:317`) — load/save only.
- `CedarStrictCredentialMode`, `CredentialAuditRetentionDays` — consumers live
  in `core/credstore`, an I7 orphan (`core/credstore/prune.go:64` describes the
  wiring that does not exist: *"Wire a func() int that returns
  Settings.CredentialAuditRetentionDays."*).

**Status quo.** A user turns MCP auto-restart **off** and the supervisor
restarts anyway. A user picks a permission posture and nothing branches on it. A
user sets a local-runtime RAM override and nothing reads it. Each round-trips
through a binding to the panel, so each reports itself as applied.

**Recommended default.** **Bring `settings.Settings` under `knobcoverage`** —
the structural fix the row itself names, and the reason this class was found by
hand rather than by CI. Then rule each remaining field wire-or-justify. Two
sequencing notes:
- **`PermissionMode` should be ruled together with X-2 and B-4, not separately.**
  Its documented semantics — *"every call prompts"* / *"all non-dangerous
  permitted"* — is the per-call tool authorization **X-2** already ruled
  **wire** (via `01PMZ202` WP11) and the interactive prompt **B-4** already
  ruled **wire**. Ruling it here independently is how one question gets two
  answers, which is the shape Part 0 exists to catch.
- **`MCPAutoRestartDisabled` gates G-5's `MCPHealthSettingsPanel`.** Wire the
  knob before mounting the panel, in the same PR, per CLAUDE.md.

**Blast radius.** Registering ~78 fields with `knobcoverage` will surface more
inert fields immediately — budget for a second, larger list rather than treating
it as a one-commit fix. `RegisterDeferred` is also an unbounded escape hatch
today (no allowlist file, no dates, no monotonic-shrink rule); closing that is
part of the same change or the mechanism launders the next gap.

**Reversibility.** Reversible. Not doing it means the next sweep re-derives this
row by hand, which is exactly how it was produced.

---

### G-5 — The frontend P1 mount backlog: six components the ledger already says are wanted

**Instance.** `docs/unwired-ledger.md:1000-1085` (the P1 block; P1b resolves to
A-13 per R-7, and the P3 item is not ownerless).

**Question.** Which of `RecoveryCodeFlow`, `ProjectAutonomyPanel`,
`HookJournalView`, `MCPHealthSettingsPanel`, `CrashReportingOnboardingModal` and
`CedarEditor` get mounted this release, and in what order relative to their
backends?

**Evidence** — importer graph re-verified 2026-08-19; **all six still have zero
non-test, non-self-referential importers** under `frontend/src`.
- `RecoveryCodeFlow` — backend assignment is **unconditional**:
  `core/rpc/api.go:2695` `Recovery: &recoveryBackendAdapter{},` inside the bare
  `{` block opened at `core/rpc/api.go:2405` (not an `if`). ⚠️ The ledger cites
  `api.go:2426` — **stale**, see §8.3-P3.
- `ProjectAutonomyPanel` — the project rung **is** engine-consumed:
  `core/rpc/api.go:4370-4373` reads `pm.GetAutonomyProfile(ctx, *rec.ProjectID)`
  into the `project` layer, fed to
  `resolveAutonomyKnobsWithSettingsFallback(...)` → `autonomy.Resolve` at
  `api.go:4703`. Bindings exist at `core/rpc/bindings.go:2758` / `:2764`.
  ⚠️ The ledger cites `api.go:4304` — **stale**, see §8.3-P3.
- `MCPHealthSettingsPanel` — the ledger's own note holds: *"blocked on an inert
  Go knob … Wire the consumer first, in the same PR."* That knob is
  `MCPAutoRestartDisabled` in **G-4**.

**Status quo.** Six built, tested components ship in the bundle and no route or
parent reaches any of them. `RecoveryCodeFlow` is the notable one — CLAUDE.md
names it as the canonical "backend is live and only the UI is missing" case and
it is *"the only recovery surface in the product"*.

**Recommended default.** **Mount the ones whose backends are verified live, and
hold the two that are not.** Mount now: `RecoveryCodeFlow`,
`ProjectAutonomyPanel`, `CrashReportingOnboardingModal`. Hold
`HookJournalView` until its read path exists (rows are written to SQL; the read
path is what is missing). **Do not mount `MCPHealthSettingsPanel` until G-4
wires `MCPAutoRestartDisabled`** — mounting it first moves the lie from the
backend to the UI, which the ritual names explicitly. Hold `CedarEditor` for the
mission that ports its fleet features into `PolicyView`.

**Blast radius.** Each mount is additive and small. The risk is entirely in
mounting the wrong one — which the row's own drifted citations nearly caused
once before (the `BranchAdvisorSettings` correction recorded at `:1024-1042`:
following the old pointer *"would have wired the wrong two fields, mounted the
panel, and shipped an inert toggle"*).

**Reversibility.** Reversible.

---

### G-6 — Denial UX: is the pull panel the product's answer, or does a push feed ship?

> ## ✅ RULED 2026-08-19 — owner: alec. **Pull panel sufficient; push feed parked; `policyAPI` folded into X-1.**
>
> `PolicyView`'s Decisions tab is the shipped answer for now.
> `justify(blocker: "no push path exists — the `policy:event` topic is emitted
> nowhere and subscribed nowhere", owner: alec, date: 2026-08-19)`.
>
> **`policyAPI` is a FIFTH stub RPC domain X-1 did not enumerate** (X-1 covers
> `a2aAPI`, `workflowAPI`, `trustAPI`, `contextAPI`); sole assignment
> `&stubPolicy{}` at `core/rpc/api.go:1264`. Same lie, same shape, and under A-0
> not deletable — so it takes the **same dated justification** as the other
> four, inside X-1's set. Answering it there costs nothing and stops a sixth
> sweep re-finding it alone.

**Instance.** `docs/unwired-ledger.md:653-714`.

**Question.** Is `PolicyView`'s pull-based Decisions tab the shipped answer for
policy denials, or does the product want live, push-driven denial surfacing —
and does `policyAPI` get a real backend or a dated justification?

**Evidence.**
- `core/rpc/api.go:434` declares `policyAPI policy.PolicyAPI`; its **only**
  assignment is `core/rpc/api.go:1264` — `policyAPI:      &stubPolicy{},`. Every
  method returns `errNotWired`. Served at `api.go:6996`.
- `frontend/src/views/policy/PolicyView.vue:72` — *"Pull-based: no push topic,
  no policy:event contract (spec §4 non-goal)."* That comment is the **only**
  occurrence of `policy:event` in the entire tree.
- The row's 2026-08-18 amendment records what actually shipped: WP05 hoisted
  every gate site to one shared `*cedar.Engine`, and WP06 built the pull panel
  on `CedarPolicy_RecentDecisions`.

**Status quo.** A denial the user just caused appears the next time they open or
refresh `/policy`. A denial that happens with nobody looking is never surfaced
proactively. That pull-vs-push distinction is the entire remaining scope.

**Recommended default.** **Rule the pull panel sufficient for now and park the
push feed with a dated blocker — but resolve `policyAPI` with X-1, not
separately.** `policyAPI` is a **fifth stub RPC domain that X-1 did not
enumerate** (X-1 covers `a2aAPI`, `workflowAPI`, `trustAPI`, `contextAPI`). It
is the same lie in the same shape, and under **A-0** it is not deletable this
campaign, so the honest outcome is the same dated justification the other four
get. Answering it inside X-1's set costs nothing extra and stops a sixth sweep
re-finding it alone.

**Blast radius.** Push = a broker topic contract, a publisher at every gate
site, and a toast surface. Pull-only = the current behaviour, stated plainly.
Trust-relevant per the rubric (*consent, permissions, denials, audit*), which is
why it needs an explicit ruling rather than silence.

**Reversibility.** Reversible.

---

### G-7 — `LocalRuntimeInfo.Models`: probe at list time, or drop the branch?

> ## ✅ RULED 2026-08-19 — owner: alec. **Probe at list time — scoped with A-5/D-2, not standalone.**
>
> D-2 ruled `CapabilityHints` probe-driven and A-5 ruled the provider-capability
> cache is the probe vehicle. **This is the same probe against the same class of
> endpoint**; building a second one is the rival-infrastructure shape this
> campaign keeps finding. Scope it into `model-settings-reach-the-model-01PMZ101`
> WP14 rather than as a separate fix.
>
> The ledger's warning holds and is now a ruling: *"Do not 'fix' it by deleting
> the string alone — the probe is the feature."* Today a running local runtime
> with models installed **always** renders "No models detected".

**Instance.** `docs/unwired-ledger.md:716-748`.

**Question.** Does the local-runtimes card learn which models a running runtime
has, or does the branch that claims to show them come out?

**Evidence.** `core/rpc/views/llm/impl_local_runtime.go:181-194` —
`runtimeInfosToWire`, the sole converter for both listing sites, copies `Kind`,
`Name`, `Running`, `Installed`, `DefaultBaseURL` and `Port`, and **never sets
`Models`**. The neighbouring `runtimeModelsToWire` (`:197`) does populate a
`Models` field, but on a different wire type reached by a different RPC.

**Status quo.** A running local runtime with models installed **always** renders
*"No models detected (runtime is running)"*. `LocalRuntimesSection.vue` is a
shipping surface, so this is a live cosmetic lie, not dead code.

**Recommended default.** **Populate `Models` with a per-runtime probe at list
time, and scope it with A-5/D-2 rather than as a standalone fix.** **D-2** ruled
`CapabilityHints` **probe-driven** and **A-5** ruled the provider-capability
cache **wire it as the probe vehicle** — this is the same probe against the same
class of endpoint, and building a second one would be the rival-infrastructure
shape this campaign keeps finding. The ledger's warning holds: *"Do not 'fix' it
by deleting the string alone — the probe is the feature."*

**Blast radius.** A probe at list time costs a round-trip per installed runtime
on a settings-panel render. Deleting the branch removes the affordance
permanently and leaves `LocalRuntimeInfo.Models` with no writer *and* no reader.

**Reversibility.** Reversible.

---

### G-8 — Migration 0335's FTS purge: add the guard, or document it as once-only?

> ## ✅ RULED 2026-08-19 — owner: alec. **ADD THE GUARD.**
>
> Make the purge a no-op when the index holds no tool rows. Latent today —
> migrations run once by ledger key — but this sits squarely in CLAUDE.md blind
> spot #3: *"a migration that has never run against populated tables has never
> been tested."*
>
> The ledger records what this class costs: `sessions/0327-source-model-output`
> *"already destroyed data, and it is unrecoverable"*, and repairing migration
> **selection** is what turned `sessions/0332-artifacts-global-scope` into a
> live cascade hazard the instant the v0.63.1 fix shipped. **A guard is cheap; a
> corrupted FTS index on a user's database is not.**
>
> Pairs with `audit-that-tells-the-truth-01PMZA10` UNIT-8, which found by
> EXECUTION that a purge against an external-content FTS table leaves it
> permanently unreadable, not merely stale.

**Instance.** `docs/unwired-ledger.md:542-567`.

**Question.** Does the tool-row FTS purge become idempotent, or does it get an
explicit "may be applied exactly once" contract at the statement?

**Evidence.** `core/session/migrations_search_fts_tool_rows.go:155-156` —
```
const sqlPurgeToolRowsFromFTS = `INSERT INTO messages_fts(messages_fts, rowid, content)
    SELECT 'delete', rowid, content FROM session_messages WHERE role = 'tool'`
```
issued unguarded at `:190`. By the migration's own documented reasoning
(`:104-110`), a `'delete'` for terms the index does not hold drives term counts
negative and SQLite then fails with *"database disk image is malformed"*.

**Status quo.** Latent, not live. Migrations run once by ledger key and 0335's
`Down` backfills the same rows, so the supported Down→Up cycle is balanced. What
is unguarded is any future path that re-applies `Up` without the matching
`Down`: a repair routine, a manual re-run, or a second migration that copies the
statement — and it reads like a plain backfill, which is why it will be copied.

**Recommended default.** **Add the guard.** This sits squarely in CLAUDE.md's
blind spot #3 — *"a migration that has never run against populated tables has
never been tested"* — and the ledger already records what that class costs:
`sessions/0327-source-model-output` *"already destroyed data, and it is
unrecoverable"*, and repairing migration **selection** is what turned
`sessions/0332-artifacts-global-scope` into a live cascade hazard. A guard that
makes the purge a no-op when the index holds no tool rows is cheap; a corrupted
FTS index on a user's database is not.

**Honesty note carried from the row:** the corruption claim is *"not
independently reproduced"* — it is the migration's own comment plus the shape of
the SQL. Treat it as a hazard to design against, not a measured failure.

**Reversibility.** The guard is reversible. **The corruption is not.**

---

### G-9 — Name the owner for the harness-self attach follow-on mission

> ## ✅ RULED 2026-08-19 — owner: alec. **Owner: alec. Dated 2026-08-19.**
>
> No capability question is open — the attach ruling was already made. This was
> the purest instance of what F-1 forbids: a row naming a blocker but not a
> person, which the ritual calls *"not a justification, it is unexplained code
> with a label on it."*
>
> ⚠️ **The blocker survives the assignment and is load-bearing.** Attaching
> before the visibility seam and `EmbeddedCedar` land *"would hand every session
> write access to provider credentials and settings."* Naming an owner does not
> unblock the work; it names who decides when it unblocks.

**Instance.** `docs/unwired-ledger.md:1086-1138` (the 2026-08-18
`mcp-connector-lifecycle-01PMMC01` WP01 amendment). **Missed by F-1's count** —
see §8.3-P2.

**Question.** Who dispatches the harness-self attach follow-on mission, and by
when?

**Evidence.** `docs/unwired-ledger.md:1099-1104` — *"**Owner of the attach
execution:** the mission owner who dispatches the follow-on mission; unassigned
as of this entry. **Blocker:** the visibility seam and `EmbeddedCedar` wiring do
not exist yet."*

**Status quo.** The product decision is **already made** — the owner ruled
**attach** on 2026-08-18, recorded at
`kitty-specs/mcp-connector-lifecycle-01PMMC01/research/b10-harness-self-decision.md`.
The blocker is named and real. What is missing is a person and a date.

**Recommended default.** **Name alec and date it. No capability question is
open.** This is the cheapest of the nine and the purest instance of what F-1
actually forbids: a row that names a blocker but not a person, which the ritual
says *"is not a justification, it is unexplained code with a label on it."*
Note the blocker is load-bearing and must survive the assignment: attaching
before the visibility seam and `EmbeddedCedar` land *"would hand every session
write access to provider credentials and settings"*.

**Reversibility.** Fully reversible — it is a name and a date.

---

## §8.3 — Premise findings

Four things this sitting found by verifying rather than trusting. Per the
standing lesson in Part 7: *a ruling is a decision about what to do; it is not
evidence about what the code is.* The same now applies to ledger rows.

### P1 — `:318` is not open. Its stated closing change is already implemented.

The row reads *"**Owner:** unassigned. The change that closes it is a recursive
walk in `redactMessages` over `map[string]any` / `[]any` / keys."* That walk
**exists**: `core/sessions/export/redact.go:528-534` calls
`RedactValue(k)` on every argument key and `redactStructured(v, 1,
secretNamingKeyRe.MatchString(k), …)` on every value, bounded by
`MaxRedactDepth = 24` (`:297`) and cycle-guarded (`:409`). The entry's own
heading already says *"**CLOSED 2026-08-16, see below**"* and the Drained entry
below it documents the widened scanner in detail.

So one of F-1's sixteen needs **no decision at all** — the `**Owner:**
unassigned` line is residue inside an entry that was superseded and never had
its footer trimmed. **Disposition: close the row with a pointer to the Drained
entry.** This is *not* a delete under A-0; it is a record correction, the same
class as X-3's instruction to *"correct the ledger's DRAINED claim … so the
record does not outlive the code."*

### P2 — F-1's count of sixteen anchored on the bold form and missed four more.

`grep '\*\*Owner:\*\* unassigned'` returns exactly 16 — which is where the
figure came from. But the ledger holds **four more ownerless parks in other
phrasings**:

| Line | Form | Now |
|---|---|---|
| `:1101` | `…mission; unassigned as of this entry.` | **G-9** |
| `:1419` | `Owner: unassigned;` (unbolded, list item) | **G-3** |
| `:1426` | `Owner: unassigned.` (unbolded, list item) | **G-3** |
| `:1433` | `Owner: unassigned.` (unbolded, list item) | **G-3** |

The true total is **20 in the ledger, 21 including the audit**. The three
unbolded ones are all redaction parks, and one of them
(`core/eval/capture.go:137`) writes model messages to disk behind a two-pattern
scanner — so the phrasing accident hid the single highest-severity item in the
set. **A mechanical sweep must match on `[Oo]wner:.*unassigned`, not on the bold
form.** Recommend this as the gate-extension obligation for this finding class,
per CLAUDE.md's rule.

### P3 — Two rows' load-bearing citations have drifted. Both claims still hold.

Anyone acting on the cited lines would have wired the wrong thing — the exact
failure the ledger's own `BranchAdvisorSettings` correction records.

| Row | Cited | Actually | Claim |
|---|---|---|---|
| `:1013` (`RecoveryCodeFlow`) | `core/rpc/api.go:2426` | `:2695` (`Recovery: &recoveryBackendAdapter{},`), inside the **bare** block opened at `:2405` | **holds** — unconditional |
| `:1015` (`ProjectAutonomyPanel`) | `core/rpc/api.go:4304` | `:4370-4373` → `:4703` `autonomy.Resolve` | **holds** — engine-consumed |
| `:508-510` (branch drift) | `branches/impl.go:366`, `:452` | `:378`, `:464` | **holds** — both still `Role == RoleAssistant` |

`core/rpc/api.go:2426` today is `a.catalogAPI = catalogview.NewAPI(nil, nil,
"")` and `:4304` is a headless-confirm log line. Neither is related to its
citing row. **Recommend the ledger stop citing bare line numbers for `api.go`**,
which is ~7,000 lines and churns every release; cite the symbol.

### P4 — Confirms Part 7 rather than adding to it.

`:648` and `:831` both rest on the sub-agent seam. Re-verified 2026-08-19 at
`core/rpc/builtins_wiring.go:312-313`: `var subagentSeam agentgraph.BranchSeam
// nil — no child-run spawner yet` followed immediately by `if subagentSeam !=
nil`. This **confirms Part 7's A-13 correction** (`kenaz__subagent_dispatch` is
NOT live) independently. No new finding; recorded so R-2 and R-3 carry the
corrected size estimate rather than the ruling's original one.

---

## Appendix C — Verification ledger (Part 8)

**Method.** Every row was located mechanically
(`grep -n '[Oo]wner:.*unassigned' docs/unwired-ledger.md
docs/dead-code-audit-2026-08-18.md docs/dead-code-audit-2026-08-16.md
scripts/ci/allowlists/`), not from a supplied list — which is how the four
unbolded parks in §8.3-P2 were found. `.claude/worktrees/` and `.worktrees/`
were excluded from every search. All work on branch `release/v0.59.0`.

**Code citations re-read before being written here** (`sed -n '<line>p'` or a
surrounding-range read, per citation):

| Claim | Location | Result |
|---|---|---|
| export walk is recursive + key-scanning | `core/sessions/export/redact.go:297,409,514-537` | ✅ matched |
| `views/search` lacks the predicate | `core/rpc/views/search/impl.go` (`grep -c archived_at` → 0) | ✅ matched |
| `core/search` has the predicate | `core/search/search.go:98,110` | ✅ matched |
| `core/search` has one importer, its own test | `core/search/search_test.go:9` | ✅ matched |
| `?role=` unvalidated; four options | `SearchModal.vue:60,118,347-350` | ✅ matched |
| revised final allocates a new move | `chat/moves.go:409,413` | ✅ matched |
| `allocate` emits a boundary only | `chat/moves.go:154-169` | ✅ matched |
| branch drift sites | `branches/impl.go:378,464` | ⚠️ ledger cites `:366`,`:452` — drifted, claim holds |
| `ToolResult` has no blocks | `core/agentgraph/seams.go:322-325` | ✅ matched |
| FTS purge unguarded | `core/session/migrations_search_fts_tool_rows.go:155-156,190` | ✅ matched |
| `Models` never set on the listing path | `core/rpc/views/llm/impl_local_runtime.go:181-194` | ✅ matched |
| `EffectivePermissionMode` has 3 store-only callers | `settings/api.go:887`; `impl.go:558,560,1714` | ✅ matched |
| `MCPAutoRestart()` unread by `core/mcp` | `settings/api.go:333,967`; `stdio/supervisor.go:56` | ✅ matched |
| `EffectiveLocalRuntimeRAMBytes` zero callers | `settings/api.go:1639` | ✅ matched |
| `policyAPI` sole assignment is the stub | `core/rpc/api.go:434,1264,6996` | ✅ matched |
| `policy:event` exists only in a comment | `PolicyView.vue:72` | ✅ matched |
| `Recovery` adapter assignment is unconditional | `core/rpc/api.go:2405,2695` | ⚠️ ledger cites `:2426` — drifted, claim holds |
| project rung is engine-consumed | `core/rpc/api.go:4370-4373,4703`; `bindings.go:2758,2764` | ⚠️ ledger cites `:4304` — drifted, claim holds |
| six P1 components still unmounted | `frontend/src` importer graph, all six | ✅ matched |
| `subagentSeam` nil; `Tasks: nil` | `core/rpc/builtins_wiring.go:312-313,321` | ✅ matched |
| `BackgroundSpawn` test-only | `core/tools/bash/run_in_background_test.go:67` | ✅ matched |
| compaction-overhead ref has no writer | `SessionsView.vue:948` | ✅ matched |
| narrative knobs are narrative-scoped | `core/memory/narrative/synthetic.go:37`, `prelude.go:28` | ✅ matched |
| `redactString` covers three shapes | `core/eval/capture.go:133-142` | ✅ matched |
| register C-2's own citation (spot-check) | `core/rpc/api.go:2653` | ✅ matched exactly |

**Twenty-five checks; twenty-two matched exactly, three found line drift with
the underlying claim intact** — all three recorded in §8.3-P3 rather than
silently corrected.

**Not done.** No escalation was resolved. No code, test, spec, allowlist or
audit document was modified. No test was run and nothing was compiled. The only
files written are this Part 8 and the pointer edits in `docs/unwired-ledger.md`.
