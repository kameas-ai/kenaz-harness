package llm_test

// WP-PI — persistence integrity for model-settings-reach-the-model-01PMZ101,
// scoped to THIS landing: WP04 (Bedrock tool round-trip), WP05 (cost
// reducer construction), and WP15 (LLMRequest.Model reaches the
// registry). WP01/WP02/WP03/WP08 landed in an earlier pass and already
// carry their own coverage; this file does not re-audit them.
//
// Per kitty-specs/_templates/WP-persistence-integrity.md (mandatory in
// every mission, no per-mission judgement call) and this mission's
// tasks.md UNIT-PI ("P0 · gate ... merge gate. Lands with or before the
// last of UNIT-4 / UNIT-6 / UNIT-9. The release PR cannot open without
// it."). This landing does not include UNIT-4/6/9 (WP06, WP10+WP11,
// WP13+WP14) — those remain unlanded and are named below — so this file
// is an interim per-landing WP-PI, not the mission-closing one.
//
// # AC-PI-4 — per-WP enumeration (this landing touches no
// table/migration/setting/FTS index)
//
// Re-verified against the actual diffs (`git show --stat <sha>`), not
// assumed from the plan:
//
//   - WP04 (fix(llm): WP04 — stop dropping every tool result on the
//     Bedrock SDK and bearer paths) — none. Touches only
//     core/llm/bedrock/bedrock.go, bearer.go, and a new
//     tool_roundtrip_test.go. toBedrockMessages/toConverseJSON convert
//     in-memory llm.Message/llm.ContentBlock values into AWS SDK request
//     structs (types.Message) or a JSON-marshalled HTTP body sent
//     directly to the Bedrock Converse endpoint. Nothing here reads or
//     writes a database row, a migration, a persisted setting, or an FTS
//     index — the conversation history it reads was already in memory
//     (KernelMessagesToWire's output), not loaded from storage by this
//     code.
//   - WP05 (fix(llm): WP05 — construct the cost reducer so cost stops
//     being indeterminate everywhere but OpenRouter) — none. Touches
//     core/rpc/api.go (one field assignment building an in-memory
//     llmregistry.Options literal), core/llm/registry/audited_stream.go
//     (an in-memory llm.Cost computation on a in-flight llm.Response
//     value), and two new test files. The downstream write this cost
//     value eventually feeds — core/rpc/api.go's usageHookFn calling
//     usageMgr.Add, which persists session_usage_turns — is pre-existing
//     code this WP did not touch; WP05 only makes sure a real (non-
//     Indeterminate) Cost value reaches that pre-existing call, it does
//     not change how or whether persistence happens.
//   - WP15 (fix(chat): WP15 — stop dropping LLMRequest.Model so the
//     escalation ladder escalates) — none. Touches only
//     core/rpc/views/agentgraph/chat/llm_provider_adapter.go (an
//     in-memory struct-field selection, req.Model vs a.modelOverride,
//     when building a corellm.GenerationRequest passed straight to
//     Registry.Stream) and its test file. No table, migration, setting,
//     or FTS index.
//   - WP-PI (this file) — none; adds only this enumeration and re-runs
//     the falsifiability check below.
//
// Not landed in this pass — recorded so a future WP-PI does not have to
// re-derive which units this session covered:
//
//   - WP06 (last_usage_json read-back) — persistence-bearing (UNIT-4,
//     AC-PI-1 applies directly: must boot from
//     core/storage/sqlite/testdata/upgrade/v0.64.0/, never an empty
//     dir). Not attempted this session; still open.
//   - WP09+WP16 (Bedrock/Gemini reasoning output + frontend render,
//     UNIT-5, coupled — must not split) — not attempted.
//   - WP10+WP11 (session knobs persistence + /effort + tune panel,
//     UNIT-6, coupled, persistence-bearing) — not attempted.
//   - WP07 (auto-title/compaction overhead surfaced, UNIT-8) — unblocked
//     now that WP05 landed (spec D-7), but not attempted this session.
//   - WP13+WP14 (ollama adapter kind + capability probe/cache,
//     UNIT-9, persistence-bearing — WP14 must write a real sqlite
//     CapabilityCache backend per the spec's re-derivation finding 2)
//     — blocked on E-005 (an open product ruling: does `ollama` migrate
//     existing custom-openai profiles?) per tasks.md's own header; not
//     attempted.
//   - WP17 (branch recommender, UNIT-10) — not attempted.
//   - WP12 (G-1/G-2/G-3 gates, UNIT-11) — not attempted.
//
// # AC-PI-1 — tests boot from a previous-release database
//
// N/A by the enumeration above: this landing adds no table, migration,
// or persisted setting. Verified rather than assumed —
// `go test ./core/storage/sqlite/... -run TestUpgradePath -race -count=1 -v`
// was RUN (not read) after WP04/WP05/WP15 and PASSED against all seven
// committed snapshots (v0.63.0, v0.63.1, v0.63.2, v0.64.0, v0.64.1,
// v0.65.0, v0.65.1), confirming nothing in this landing regressed
// migration selection or schema evolution.
//
// A real gap was found while checking this, NOT caused by this landing
// (none of WP04/WP05/WP15 touch migrations or storage): `bash
// scripts/ci/check-upgrade-snapshot-present.sh`, RUN directly, FAILS on
// this tree —
//
//	newest release tag: v0.66.0
//	newest snapshot:    v0.65.1
//
// i.e. the newest committed upgrade snapshot is one release behind the
// newest git tag. This is exactly the class of gap
// check-upgrade-snapshot-present.sh exists to catch (CLAUDE.md's blind
// spot #3 corollary): TestUpgradePath keeps passing, it just does not
// cover anything a user upgrading from v0.66.0 would traverse. Producing
// the v0.66.0 snapshot requires `bash scripts/ci/upgrade-snapshot.sh
// v0.66.0` (not a check-*.sh script, so out of this WP's read-only
// scope) plus a hand-written PROVENANCE.md, and per CLAUDE.md's
// release-ritual corollary is "the responsibility of whichever mission
// lands last on the release branch" — recorded here, not silently
// left for the next sweep to re-discover.
//
// # AC-PI-2 — this mission's own fixtures, audited for the SQL/file bypass
//
// None of WP04/WP05/WP15's new or relied-upon fixtures touch
// session.NewMemoryStore() or any sqlite path at all — they are pure
// in-memory seam-boundary fakes:
//
//   - core/llm/bedrock/tool_roundtrip_test.go (new): calls
//     toBedrockMessages/toConverseJSON directly, and drives the real
//     adapter over an httptest server via the pre-existing
//     capturedBody/rewritingClient harness this package already uses for
//     every other *_Serialized test. No storage layer in scope.
//   - core/llm/registry/registry_test.go's zzCostReducer +
//     newRegWithCostReducer (new): an in-memory CostReducer fake and a
//     Registry constructed with New(Options{...}) directly — asserts
//     what Registry.Stream returns, not what gets persisted.
//   - core/rpc/cost_reducer_wiring_test.go (new): drives the actual
//     production newLLMStack construction path (via the pre-existing
//     llmRegistryOverDataDir helper in api_cedar_gate_wiring_test.go)
//     specifically so the test CANNOT be satisfied by a hand-built
//     registry.New(Options{Cost:...}) — this is the opposite of the SQL-
//     bypass failure mode: it exists to make sure the test cannot avoid
//     seeing the real (missing, pre-fix) production wiring.
//   - core/rpc/views/agentgraph/chat's capturingRegistry (pre-existing,
//     relied upon by the new TestGenerate_ModelFieldWinsOverOverride) —
//     asserts what reaches corellm.GenerationRequest at the seam
//     boundary the LLMProviderAdapter owns, not what survives a database
//     round trip.
//
// Examined and NOT changed, with reason: none of the above needed
// changing to avoid a bypass, because none of them assert anything that
// is supposed to survive a storage round trip in the first place — the
// three WPs landed this session do not read or write session/sqlite
// state.
//
// # AC-PI-3 — destructive migrations
//
// None. This landing adds and repairs no migration.
//
// # AC-PI-5 — release-ritual hook
//
// This session did not land the mission's remaining persistence-bearing
// units (WP06, WP10+WP11, WP13+WP14) or the gates (WP12), so it is not
// positioned to know whether it is "the last mission to land before a
// release tag." The v0.66.0 snapshot gap recorded under AC-PI-1 above is
// real regardless of that question and is flagged for whoever cuts the
// next release.
