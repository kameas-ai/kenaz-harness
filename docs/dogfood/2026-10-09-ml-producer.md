# Acceptance — harness ML producer (`ml-producer-01MLPRD01`), 2026-10-09

The spec §8 acceptance run on `feat/ml-producer` (WP01–WP04). §12 A-1 to
A-12 override §1–11, so the event kinds below are A-10's six (`file`,
`terminal`, `commit`, `phase_change`, `agent.tool`, `agent.turn`) and the
wire is OTLP **protobuf** (A-12). Hermetic rows come from Go tests that
drive the production wiring: `newLLMStack`'s chat runner over
`newGraphManagerWithDeps`' kernel, the real fs builtins and `kenaz__bash`,
the real recorder, gate and shipper, and the outbox on real sqlite. The
only fakes are the model (a scripted Anthropic-protocol `httptest` server)
and Fleet (an `httptest` server for `/config.json`, `/api/v1/enroll`,
`/api/v1/me/capabilities`, `/api/v1/me/ml` and `/otlp/v1/logs`). Fleet's
ingest decodes the request with `proto.Unmarshal`, the same way its
receiver does.

## Results

| § | Criterion | Result | Evidence |
|---|---|---|---|
| 8.1 | A session writes 3 files, runs `go test ./...` (exit 1) and `git commit -m x` (exit 0). Fleet receives one task upsert and the A-10 events, with exactly the contract attributes and nothing raw | **PASS.** The scripted model makes five sequential tool calls in one chat turn. The tools really run: the three files exist on disk, `go test ./...` exits 1 in a workspace with no go.mod, and `git commit` exits 0 in a repo seeded with one staged file. The run ends with the app-shutdown drain. Fleet then receives `file`×3, `terminal`×2 (`go test`/1, `git commit`/0), `commit`×1, `phase_change`×2 (coding, then testing), `agent.turn`×1 (tool_calls 5, model_calls 6, completed) and **one** task upsert. The upsert carries test_runs 1, test_fails 1, commit_count 1, three `h+.go` file keys, phase testing, repo_root = h(workspace) and branch "". Each record carries exactly the five `kameas.ml.*` attributes (source `harness`, schema_version 1), and the resource carries the three resource attributes. The request bytes contain none of the following: the workspace path, the file paths or names, either full command, `./...`, the session id, file content, or any daemon or `agent.*`-legacy kind | `core/rpc` `TestMLAcceptance_8_1_ChatSessionShipsContractRecords` |
| 8.2 | Every row of the effective-rule table ships or doesn't; `ml_not_effective` mid-stream stops and purges | **PASS.** The matrix covers the eight contract rows plus hosted_inference off, org paused, a `/me/ml` 503 and a self-contradicting `/me/ml`. It runs through the real `fleet.Client`, and it was already covered by WP03, so it is not duplicated here. Mid-stream: the shipper loop is running and has shipped one accepted batch. The member then withdraws while a batch is in flight: the pre-batch `/me/ml` read still says effective, and ingest answers `403 ml_not_effective`. The loop stops, the unsent outbox is purged, the recording gate closes, a later tool call records nothing, nothing further is posted, and the panel shows a stop reason | `core/rpc` `TestMLWiring_GateMatrixOverContractEffectiveTable` (WP03), `TestMLWiring_ResponseTableThroughRealClient/403_ml_not_effective…` (WP03), `TestMLAcceptance_8_2_NotEffectiveMidStreamStopsAndPurges` (new); unit: `core/mlproducer` `TestConsentGate_ConditionMatrix`, `TestConsentGate_PurgeOncePerWithdrawal` |
| 8.3 | Notice ack posts the current version; a 409 re-shows | **PASS (WP01).** | `core/fleet` `TestAckMLNotice_PostsVersionAndReturnsFreshState`, `TestAckMLNotice_409PolicyChangedIsTyped`; `core/rpc/views/settings` `TestFleetMLAckNotice_PostsShownVersion`, `TestFleetMLAckNotice_409ReReadsAndReShows`, `TestRenderMLNotice_Golden`; `CloudMLPanel.spec.ts` "notice required…", "409 policy_changed…", "409 surfaced as a thrown policy_changed error…" |
| 8.4 | Agent processes carry `KENAZ_ACTOR=agent`; bash foreground and background also carry `KENAZ_SESSION` (A-4); MCP stdio carries `KENAZ_ACTOR` only | **PASS.** In a production chat turn, the foreground `env` and the background `env` (through the real task registry) both see `KENAZ_ACTOR=agent` and `KENAZ_SESSION` = h(session) under the install key. The consent gate is deliberately closed in that test, because the markers do not depend on consent. An MCP server spawned by the **pool** (`Pool.Open` → `Connection.Open` → `childEnv`, behind a real handshake) sees exactly `KENAZ_ACTOR=agent` and no `KENAZ_SESSION`, on both the inherited and the isolated env path, even when the harness itself inherited both variables | `core/rpc` `TestMLAcceptance_8_4_BashForegroundAndBackgroundCarryMarkers` (new), `TestMLWiring_ChatRunnerToolCallReachesOutbox` (WP03, foreground actor); `core/mcp/transport/stdio` `TestPoolOpen_SpawnedServerCarriesAgentActor` (new), `TestChildEnv_AgentActorMarker_SpawnedChildSeesIt` (WP02); `core/tools/bash` `TestEnvProvider_*` (WP02) |
| A-3 | A subagent of an attended session lands on the root's task | **PASS.** The subagent goes through the real `kenaz__subagent_dispatch` tool, the real `NewSubagentRunSpawner` (with `MLParent` = the producer's linker, as `api.go` wires it) and a real chat runner. The child's tool call (`agent.tool`) and its turn (`agent.turn`) land on h(parent)'s task and on no other task. Control: a subagent dispatched from an unattended (scheduled) parent records nothing | `core/rpc` `TestMLAcceptance_SubagentOfAttendedSessionLandsOnRootTask` (new); unit: `TestLinkMLParent_OnlyAttendedChainsLink` (WP02) |
| 7 | Minimisation, enforced in CI | **PASS.** `scripts/ci/check-ml-producer-minimisation.sh` has two halves. Static: `KindTable` is exactly A-10, and no other kind literal appears in `core/mlproducer`. Dynamic: the real recorder runs over a 29-call fixture corpus and produces 72 outbox records, all within §7. The gate is wired into `pr.yml` (lint-go, `ci-medium`). Six planted proofs fail without the gate and pass with it | `scripts/ci` `TestMLMinimisationGate_*` |
| 8.5 | Live: dev app against dev Fleet after consent; Fleet reports `accepted > 0` for producer `harness`; kenaz-ml confirms tenant rows | **PENDING**: the owner's live run (checklist below) | — |

## Defects found and fixed by WP04

- **Final task counters were not shipped until completion.** The recorder
  upserted a task on its first activity, and after that only when a call
  landed at least 60 s after the previous upsert. Activity that stopped
  inside that window left Fleet with the first-activity state (one file,
  zero tests, no commit) until the session was deleted or the 7-day idle
  sweep ran. §8.1 caught it through the real stack: with the fix
  reverted, the shipped task reads `files` ×1, `test_runs` 0,
  `commit_count` 0, phase coding. Fix:
  - a trailing upsert on a `UpsertEvery` ticker for tasks whose state
    changed since their last upsert (`Recorder.flushStale`, with
    `mlstore.StaleTasks` plus an in-memory dirty set for activity in the
    same millisecond);
  - a forced trailing upsert on the shutdown drain (`Recorder.FlushTasks`,
    called from `mlProducerWiring.shutdown` before the final ship).

  This keeps the "at most once per 60 s while active" bound. Tests:
  `core/mlproducer` `TestRecorder_TrailingUpsertCarriesFinalCounters` and
  §8.1.
- **An inherited `KENAZ_SESSION` passed through to MCP stdio servers** on
  the inherited-env path. `withAgentActor` stripped only `KENAZ_ACTOR`, so
  a harness launched with `KENAZ_SESSION` set would have attributed a
  global MCP server to that session. It now drops both variables, and
  `TestPoolOpen_SpawnedServerCarriesAgentActor` pins it.

## Records

- `docs/unwired-ledger.md` (2026-10-09):
  - The agent-process markers (`KENAZ_ACTOR`, `KENAZ_SESSION`,
    `agent_pids`) have no reader until sigil ships its side. Owner: the
    sigil daemon change.
  - The dev-org-only guard is not re-ledgered here: WP05 (on-device
    exclusions) removes the guard and its WP03 ledger entry.
  - The Fleet OTLP-JSON decode bug is CLOSED by kenaz-fleet #218. The
    harness keeps sending protobuf.
- Context from kenaz-ml: the dev tenant
  `tenant_fa81ec53d3744c488b72dd6b8584d968` exists, offload is on
  (`member_choice`), and the dev worker accepts `producer=harness`.

## §8.5 live-run checklist (owner)

1. Run the dev app (`bash scripts/dev.sh` or `wails dev`) and sign in to the
   **dev** Fleet with the `fa81ec53` org account.
2. Go to Settings → Sync → **Cloud ML** and turn on the `workflow_events`
   opt-in. The toggle is shown because offload is on with `member_choice`.
3. Read the notice and press **Acknowledge**. The panel should then show
   effective = yes and the notice as acknowledged.
4. Run a chat session in which the agent writes at least one file, runs a
   test command (for example `go test ./...`) and makes a `git commit`.
   Wait about 60 s, or quit the app to force the drain.
5. Check the panel's shipping status: a last batch time, and **accepted > 0**
   with rejected = 0.
6. Ask kenaz-ml to confirm rows for producer `harness` in
   `tenant_fa81ec53d3744c488b72dd6b8584d968`: one `agent-…` task per
   session, plus `file` / `terminal` / `commit` / `phase_change` / `agent.turn`
   events, and the task's `test_runs` and `commit_count` above zero.
7. Record the result here and flip §8.5 to PASS or FAIL.
