# Acceptance — tool-context budget (`tool-context-budget-01TCBUD01`), 2026-10-09

Release-gate run of spec §4 on the integrated `release/v0.94.0` tree
(WP01–WP07 plus WP08). Hermetic where it can be: every number below comes
from a Go test that drives the production wiring (`newLLMStack`'s chat
runner, discoverer, dispatch pool and a real provider adapter against an
`httptest` provider) over real sqlite, unless the row says otherwise. The
"before" numbers are the 2026-10-08 dogfood
(`docs/dogfood/2026-10-08-round2.md`).

Catalog shape in every hermetic run, matching the dogfood: Outlook 94,
Filesystem 14, harness-self 7, Fetch 1 (synthetic servers, ~1,500
estimated tokens per tool — the dogfood's per-tool average), plus every
built-in with its Settings dial on.

## Results

| # | Criterion | Result | Evidence |
|---|---|---|---|
| AC1 | First-turn prompt ≤ 15,000 tokens | **PASS (estimator)** — whole first-turn prompt ≈ **3,889** estimated tokens (was 224,798 provider tokens). Tool schemas: 131 tools ≈ 180,509 → 13 hot-set tools ≈ 2,878; digest 891 chars. Corrected by AC5's measured under-count (×1/0.632) the provider would see ≈ 6.2k — still under 15k. Live manual run: **pending (owner alec)**. | `core/rpc` `TestToolExposureWiring_FirstTurnToolTokens` (anthropic adapter over `httptest`) |
| AC2 | "Send an email" in two model calls | **PASS** — call 1 `kenaz__load_tools(servers:["outlook"])`, call 2 carries and calls `outlook__send-mail`, the permission resolver sees it, one `tools.activated` audit row. | `chat` `TestToolLoop_LoadThenCallWithinTwoModelCalls` |
| AC3 | A 131k-window model completes a scheduled chat using harness-self | **PASS** — `ChatRunDispatcher` → llm view `StartStream` → chat runner → OpenRouter adapter (window 131,072 from its live `/models` list) → provider that refuses any request over 131,072 tokens counted at **2.5 bytes/token** (AC5's measured density, harsher than the estimator). Calls: 14,311 B (12 tools), 58,419 B (19 tools: harness-self loaded), 58,707 B → ≈ 5.7k / 23.4k / 23.5k provider tokens; 0 refused; every call's `llm.request.composition` budget = **19,660** (15 % of 131,072, from the live `/models` entry — not the 24k setting); `harness_read_list_sessions` ran once; run `completed`. The whole catalog (≈ 180,609 estimated tokens) cannot fit the window. | `core/rpc` `TestToolExposureAcceptance_131kWindowScheduledChatUsesHarnessSelf` (new in WP08) |
| AC4 | Byte-identical prefix; `cached_tokens ≥ 0.9 × prefix` live | **Hermetic half PASS** — two consecutive calls serialise byte-identical system + hot/pinned segments, through the chat request builder and both adapters. **Live half PENDING (owner alec)**: OpenRouter-Anthropic turn-2 `cached_tokens` ≥ 90 % of the prefix needs a live call; it also settles the ledger bullet "OpenRouter tool-object `cache_control` is unverified live". | `chat` `TestGenerate_CacheablePrefixStableAcrossCalls`; `TestAnthropicAdapter_PromptCache_GoldenPrefixStable`, `…_ThreeSegmentGolden`; `TestOpenRouterAdapter_PromptCache_GoldenPrefixStable` |
| AC5 | Composition within ±10 % of provider `prompt_tokens` on three recorded sessions | **FAIL — recorded gap, not tuned.** On the dogfood's first three recorded calls (session `b0c22dc5…`, persisted `prompt_tokens` 224,798 / 225,104 / 225,296) the composition estimate is **142,097 / 142,312 / 142,442 = 63.2 %** of the provider's count (off by 36.8 %). Parts for frame 1: system 577 + MCP tools 134,958 + built-ins 6,545 + history 17. The MCP part uses the servers' **real** `tools/list` output (captured 2026-10-09 from the same installs: ms-365-mcp-server 0.159.1, server-filesystem 2026.8.31, mcp-server-fetch) — 540,051 bytes the estimator counts at 4.0 bytes/token while the provider tokenized the request's ~566 KB of tool text at ≈ 2.5. The spec's own `ceil(bytes/3.5)` rule would reach ≈ 72 %, also outside 10 %. Consequence: the 24k budget and the 15 %-of-window cap are in estimator tokens, ≈ 1.6× fewer than billed. Owner decision needed on the estimator (per-kind density for schema text, or a real tokenizer); open in `docs/unwired-ledger.md`. | `core/rpc` `TestComposition_RecordedDogfoodFrames_FRH3Gap` (new; pins the measured 0.60–0.72 band so any estimator change re-opens this record) |
| AC6 | Every FR-K1 control has a request-level test; the gate exists with a planted proof | **PASS** — see the two tables below. | |
| AC7 | Unwired sweep | **PASS with dated opens** — see "Sweep". | `docs/unwired-ledger.md` |

### AC6 — FR-K1 / K2 / K4 controls → request-level tests

Each test writes the control the way its binding writes it and asserts the
**next** chat turn's wire request (tool schemas sent, digest contents), not
the stored setting. All in `core/rpc/tool_exposure_dials_test.go`, real
chat runner + real sqlite, pass under `-race`.

| Control | Binding | Test | Observed in the next request |
|---|---|---|---|
| Schema budget | `Settings_SetToolExposure` | `TestToolExposureDial_SchemaBudgetFitsTheNextRequest` | outlook loaded: default 24,000 → 13 outlook tools, `tools_tokens_est` 22,703; budget 8,000 → 3 outlook tools, 7,453; evicted tools back in the digest |
| Activation TTL | `Settings_SetToolExposure` | `TestToolExposureDial_ActivationTTLExpiresInTheNextTurn` | default TTL: fetch still sent on turn 2; TTL 1: gone on turn 2, "fetch (1 tool)" back in the digest |
| Per-server tier (user) | `Settings_SetToolExposure` | `TestToolExposureDial_UserServerAndToolTiersReachTheRequest` | filesystem=full → 14 sent without a load; fetch=off → neither sent nor in the digest |
| Per-tool override (user) | `Settings_SetToolExposure` | same | outlook `tool-03`=full → that one tool sent; digest "outlook (93 tools)" |
| Project tier ("Pin for project") | `Projects_SetToolExposure` | `TestToolExposureDial_ProjectTierReachesItsSessionsOnly` | fetch=full in the project → sent in its session, not in a loose one |
| Session Load / Unload | `Sessions_LoadTools` / `Sessions_SetToolExposure` | `TestToolExposureDial_SessionLoadThenUnload` | Load → 14 filesystem sent; Unload (session off) → 0 sent, not in the digest, activations kept for Undo |
| Org pins (tier + budget) | signed bundle → `fleet/tool_exposure_applied.json` | `TestToolExposureDial_OrgPinsReachTheRequest` | pinned fetch=full sent with no load; pinned filesystem=off not in the digest and every load refused; pinned budget 8,000 caps the next request |

Observed while writing them (spec-conformant, worth knowing): a user
setting a 14-tool server to **full** under the default 24k budget sees one
of its tools evicted (hot ≈ 2.9k + 14 × ~1.5k > 24k) with the composer's
"pinned tools exceed the schema budget" warning — §2.3's pinned-eviction
rule, not a defect.

### AC6 — the gate

`scripts/ci/check-tool-exposure-gate.sh` (wired in `pr.yml`, lint-go job):

- **Structural** (`scripts/ci/cmd/checktoolexposure`, go/parser AST scan of
  every non-test file under `core/`): a writer of
  `llm.GenerationRequest.Tools` is a `Tools:` key in a `GenerationRequest`
  literal (or an unkeyed one), any `SetTools` call, or `x.Tools = …` on an
  `x` the same function visibly declares as a `GenerationRequest`. Each is
  keyed `file|function|kind` and must be in
  `scripts/ci/allowlists/tool-exposure-writers.txt`. Today: 7 literals, 1
  `SetTools` call, 2 writers — the chat request builder
  (`(*LLMProviderAdapter).generate|settools`, the partition path) and the
  workflow `model_turn` adapter (dated open gap). Discovery floor: no
  literal or no `SetTools` call found → fail. Stale entry → fail.
- **Runtime**: `TestRequestBuilder_NeverSendsSummaryToolUnlessActivated`
  (FR-E1 seed) and `TestToolExposureWiring_FirstTurnToolTokens` (production
  wiring sends only the hot set; `newLLMStack` wires the load core, so the
  nil-exposure "send everything" branch is not the production path) must
  report `--- PASS`.
- **Planted proofs** (`scripts/ci/gates_can_fail_test.go`):
  `TestToolExposureGate_PlantedDirectToolsWriteFires` (a `gen.Tools =
  catalog` assignment and a `&GenerationRequest{Tools: catalog}` literal
  planted in the chat package each fail the gate, naming the writer),
  `TestToolExposureGate_PlantedStaleAllowlistEntryFires`,
  `TestToolExposureGate_StructuralVerdictIsCWDIndependent`.

## Sweep (AC7), scoped to `git diff v0.93.3..HEAD`

| Pass | Find | Disposition |
|---|---|---|
| Registration ↔ consumption (bindings) | `Settings_{Get,Set}ToolExposure`, `Projects_{Get,Set}ToolExposure`, `Sessions_{Get,Set}ToolExposure`, `Sessions_LoadTools`, `Tools_SchemaCosts`, `ScheduledChat_DefaultModel` | all wired: typed client method + a `.vue` caller in a mounted component |
| Registration ↔ consumption (audit) | `tools.activated`, `tools.evicted` | emit sites in `core/tools/loadtools`; reader: the audit view's LLM category — now pinned in `TestObserveEvent_KindToCategory` |
| Settings field ↔ branch | `ToolExposure`, `ToolSchemaBudgetTokens`, `ToolActivationTTLTurns` | knob-coverage registered; each now has a request-level test (AC6) |
| Zero-caller exports | `llm.OrderToolsFlat` | **deleted** — live substitute `llm.OrderTools(all, nil, nil)`; its tests moved onto `OrderTools` |
| | `toolexposure.Partition.SendNames` | **unexported** — package test oracle only |
| | `toolexposure.HotSet` | **kept, dated** at its declaration — sole reader is the cross-package anchor test |
| Frontend placeholders | `ContextCompositionPopover.vue` "no schema-budget line until WP04" | **wired** — renders `schemaBudget` / `toolsEvicted` (spec §2.5), with tests |
| | `ScheduledChatFormModal.vue` custom servers TODO | open, dated (schedule tool-set follow-up) |
| In-code deferrals | `llm_provider_adapter.go` "FR-H3 deferred to WP08"; `tool_exposure_wiring.go` "WP08 adds never-started recipes" | rewritten to the recorded state (FR-H3 gap; FR-E2 open with blocker) |

Still open in `docs/unwired-ledger.md`, owner alec: workflow steps not
tiered (gated), FR-E2 never-started recipes, schedule tool set, Tools-menu
meter before budget, FR-H3 estimator gap, compaction's window lookup keyed
by profile id, bundle apply state surviving sign-out (cross-org), the WP05
cache deviations (live OpenRouter tool marker → AC4 live).

## Owner questions — defaults applied (spec §5)

| # | Default applied | Where |
|---|---|---|
| Q-A | Hot set as listed (15 names incl. `kenaz__load_tools`); `bash` stays | `toolexposure.hotSet` |
| Q-B | TTL 6 turns; user loads from the Tools menu are sticky for the session | `DefaultActivationTTLTurns`; `ToolsMenu.vue` `loadTools(…, true)` |
| Q-C | Org pins may set any tier, including full (cost-forcing) | `TestToolExposureDial_OrgPinsReachTheRequest` |
| Q-D | harness-self kept, summary tier | harness default; AC3 loads it |
| Q-E | 24,000 with the 15 % window cap | `DefaultSchemaBudgetTokens`, `EffectiveBudget` (AC3: 131k → 19,660) |
| Q-F | Activation survives fork (every `conversation.Manager` fork) | WP04 ruling, `InheritToolExposure` |

## Pending live checks (owner alec)

1. AC1 manual: first turn with Outlook + Filesystem + Fetch installed —
   record `prompt_tokens` from the usage frame.
2. AC4 live: two consecutive calls on an OpenRouter Anthropic-family
   model — turn 2 `cached_tokens ≥ 0.9 × prefix`; also whether OpenRouter
   accepts `cache_control` on the tool object.
3. Re-measure AC5 on three live sessions of the new build (expect the same
   ~63 % until the estimator decision).
