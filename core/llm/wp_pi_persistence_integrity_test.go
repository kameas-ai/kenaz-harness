package llm_test

// WP-PI — persistence integrity for structured-output-is-reachable-01PMZE14.
//
// Per kitty-specs/_templates/WP-persistence-integrity.md (mandatory in
// every mission, no per-mission judgement call) and this mission's
// tasks.md UNIT-PI (P0 merge gate — "the release PR cannot open
// without it"). This file is the AC-PI-4 enumeration the template
// requires when a mission has no persistence surface: "'Not
// applicable' asserted without enumeration is the same failure mode
// as a green test over an empty database."
//
// # AC-PI-4 — per-WP enumeration (table, no table/migration/setting/FTS)
//
// This is a re-verification at time of landing, not a copy of the
// spec's own table (tasks.md:650-661) — each row was re-checked against
// the actual diff, not assumed from the plan.
//
//   - WP02 (fix(agentgraph): the model node's json_schema attr reaches
//     the wire) — none. ModelAttrs.JsonSchema persists as verbatim YAML
//     TEXT in a file (core/rpc/views/agentgraph/manager.go:358's
//     os.WriteFile), not a database row. The only agent_graph* sqlite
//     tables (agent_graph_events, agent_graph_node_provenance) are
//     events/provenance, not graph definitions. WP02 adds a field to
//     two in-memory structs (agentgraph.LLMRequest,
//     llm.GenerationRequest) and a translation — no table, migration,
//     persisted setting, or FTS index touched.
//   - WP03 (fix(ci): knobcoverage sees agentgraph.ModelAttrs) — none.
//     core/wiring/knobcoverage's registry is an in-process map, reset on
//     every process start; nothing here is durable by design.
//   - WP04 (feat(llm): gemini emits a real structured-output
//     constraint) — none. Adapter wire-shape code and a struct field on
//     an outbound HTTP request body.
//   - WP05 (fix(llm): gemini's structured-output rows match its
//     adapter) — none. Embedded YAML capability data
//     (core/llm/capabilities/data/gemini.yaml, compiled into the binary
//     via embed.FS) and a completeness-test scope list — neither is a
//     database artifact.
//   - WP07 (fix(llm): stop claiming body.go applies the response_format
//     knob) — none. A doc comment and an in-process knobcoverage
//     registration.
//   - WP08 (fix(llm): the structured-output interface stops promising a
//     fallback) — none. A doc comment and a compile-time interface
//     assertion test.
//   - WP-PI (this WP) — none, plus one PROOF that a related surface
//     (graph YAML file persistence, not sqlite) round-trips correctly:
//     see AC-PI-2 below.
//
// Not landed in this pass, cut per plan.md's own cut-line table (each
// already stated as leaving the tree honest when cut, except where
// noted) — recorded here so a future WP-PI does not have to re-derive
// which units this mission's first landing covered:
//
//   - WP06 (audit emitter for llm.structured.response) — cut. Would
//     have touched no table/migration/persisted setting either (spec
//     section 1.6/D-6: it emits into core/event/log's in-memory-only ring
//     buffer, MemoryBackend — RegisterMigrations has zero callers and
//     no libSQL/sqlite Backend exists in the tree today). Its absence
//     means audit.KindLLMStructuredResponse stays undeclared-but-unused
//     as before; plan.md's cut-line table: "No audit trail for
//     structured calls... yes [safe] — nothing claims the trail exists
//     today."
//   - WP09 (adapter<->capability-row parity gate, G-3) — cut, not in
//     plan.md's cut-line table (that table only covers WP02-WP10's
//     production-code units); recorded as an open gap rather than
//     silently dropped. No persistence surface either way — it is a
//     Go test.
//   - WP10 (review gate + router request a real constraint) — cut per
//     plan.md's own instruction: "Deliberately last. Cutting it blocks
//     nothing." No persistence surface.
//
// # AC-PI-1 — tests boot from a previous-release database
//
// N/A by the enumeration above: this landing adds no table, migration,
// or persisted setting. Verified rather than assumed —
// `go test ./core/storage/sqlite/... -run TestUpgradePath -v` was RUN
// (not read) after every WP in this landing and passed
// (TestUpgradePath/v0.63.0, v0.63.1, v0.63.2 all PASS, 0.51s total),
// confirming nothing in this landing regressed migration selection or
// schema evolution.
//
// # AC-PI-2 — this mission's own fixtures, audited for the SQL/file bypass
//
// Examined and NOT changed, with reasons:
//   - core/agentgraph/exec_compute_test.go's stubLLM (pre-existing) and
//     this mission's new tests built on it
//     (TestModelExecutor_JsonSchemaReachesLLMRequest etc.) —
//     legitimately test in-memory kernel behaviour (the LLMRequest seam
//     a real provider adapter would receive), not persistence. No
//     sqlite or file I/O is in scope for what these tests assert.
//   - core/rpc/views/agentgraph/chat's capturingRegistry/schemaAwareRegistry
//     (this mission's new fakes) — same reasoning: they assert what
//     reaches corellm.GenerationRequest at the seam boundary, not what
//     survives a database round trip. Nothing here claims persistence.
//
// Changed: none of the pre-existing `session.NewMemoryStore()` fixtures
// under core/ were touched — this mission's WPs do not read or write
// session state at all.
//
// One real bypass WAS found and closed, per the template's "second
// bypass specific to this mission" clause (tasks.md AC-PI-2): manager.go's
// saveGraph stores the author's YAML verbatim, so a fixture built by
// constructing a coreag.Graph directly in Go (as every WP02 test in
// core/agentgraph does) never proves an authored json_schema: attr
// survives manager.saveGraph -> file -> manager.loadGraph ->
// coreag.LoadYAML -> decodeAttrs. Closed by
// core/rpc/views/agentgraph/json_schema_roundtrip_test.go's
// TestJsonSchemaAttr_SurvivesSaveLoadRoundTrip, which drives the real
// file-persistence layer (t.TempDir()-backed Manager, not an in-memory
// fixture) and asserts the schema's nested shape (properties, required)
// survives byte-for-byte through the save/reload/decode round trip.
//
// # AC-PI-3 — destructive migrations
//
// None. This landing adds and repairs no migration.
//
// # AC-PI-5 — release-ritual hook
//
// This mission is NOT the last to land before the v0.65.0 tag (many
// other missions in the campaign are still in flight per
// docs/v0.65.0-merge-order.md). bash scripts/ci/upgrade-snapshot.sh
// v0.65.0 was NOT run and no core/storage/sqlite/testdata/upgrade/v0.65.0/
// directory was committed by this WP — that is the responsibility of
// whichever mission lands last on the release branch, per CLAUDE.md's
// release-ritual corollary.
//
// # ADDENDUM (release branch now release/v0.78.2) — WP06 and WP10 land
//
// This mission was triaged and finished against release/v0.78.2, ~13
// releases after the enumeration above was written. UNIT-1/UNIT-2/
// UNIT-4/UNIT-5 (WP02/WP03/WP04/WP05/WP07/WP08) had already landed and
// squashed into main via PR #299 (tag v0.65.0), verified on the tree
// rather than assumed. UNIT-6 (WP09, the adapter<->capability-row
// parity gate) had also since landed (PR #323, commit e17b67ad) —
// verified live: core/llm/registry/wp09_g3_capability_row_parity_test.go
// and core/llm/bedrock/wp09_g3_row_parity_test.go both exist and pass.
// This addendum covers the two units still open at that point: WP06
// and WP10.
//
//   - WP06 (feat(llm): emit llm.structured.response with the real
//     outcome) — the enumeration line above ("emits into ... MemoryBackend
//     — no libSQL/sqlite Backend exists in the tree today") is NOW FALSE
//     and is left uncorrected above only because it was true when written;
//     do not copy it forward. audit-that-tells-the-truth-01PMZA10 landed
//     on this same release branch since (commits c6f40bb4 WP02 "register
//     the event-log schema with the production migration framework"
//     through 7b0a95b2 WP10 "the retention sweep runs"), and
//     core/rpc/api.go's newLLMStack construction site now wires a REAL
//     sqlite-backed store: `eventlog.NewSQLBackend(db)` +
//     `eventlog.NewStore(auditBackend)` are passed via `audit.WithStore`
//     into `a.auditImpl` whenever a real storage.DB is available
//     (api.go, immediately above the newLLMStack() call site) — see that
//     block's own comment: "the honesty threshold — before this, the
//     Audit view was an in-memory ring that did not survive a relaunch."
//     WP06 adds NO NEW table or migration of its own — it is a pure
//     consumer of the table 01PMZA10 already migrated — but for the
//     first time a KindLLMStructuredResponse event this mission emits
//     (via `&acpAuditBridge{impl: a.auditImpl}`, wired into
//     llmregistry.Options.Audit) genuinely reaches disk in a production
//     build, through audit.API.Push -> store.AppendComputed, not a
//     ring buffer that evaporates on process exit. Verified by reading
//     core/rpc/views/audit/impl.go's Push implementation and
//     core/rpc/api.go's auditOpts construction, not assumed from the
//     original spec's (now-stale) §1.6.
//   - WP10 (feat(agentgraph): ask for a schema instead of parsing JSON
//     out of prose) — none. The review gate and router standalone call
//     each gained a ResponseSchema literal (an in-memory json.RawMessage
//     built at call time from a Go map or a package-level const string)
//     and a degrade-on-ErrCapabilityUnsupported branch. No table,
//     migration, persisted setting, or FTS index touched — the schema
//     never reaches any store; it is a wire-request shape only.
//
// AC-PI-1 rerun on this landing: `go test ./core/storage/sqlite/...
// -run TestUpgradePath -count=1 -short -p 4` — RAN, exit 0, all
// snapshots pass (this landing touches no migration, consistent with
// the enumeration above).
//
// AC-PI-2 for this addendum's own new fixtures:
//   - core/llm/registry/wp06_structured_audit_test.go's
//     recordingAuditEmitter — a pure in-memory contextaudit.Emitter fake
//     (mutex + snapshot per CLAUDE.md's race-safe-fake pattern). It
//     deliberately does NOT drive the real audit.API/eventlog.SQLBackend
//     path — that would require a real storage.DB and duplicate what
//     01PMZA10's own test suite already covers for Push/AppendComputed.
//     This mission's job is "does structuredStream.Final() call the
//     emitter with the right payload," not "does the store persist
//     correctly," which is 01PMZA10's tested property, not this
//     mission's. Examined and NOT changed to drive real sqlite for that
//     reason.
//   - core/agentgraph/wp10_structured_output_degrade_test.go reuses the
//     pre-existing stubLLM/countingLLM fakes (exec_compute_test.go,
//     exec_router_test.go) — legitimate in-memory kernel-seam testing,
//     the same class already audited above for WP02's tests; no new
//     persistence-bypass risk introduced.
//
// AC-PI-5 unchanged: still not the last mission landing before a tag on
// this branch; the release-ritual upgrade-snapshot step remains the
// responsibility of whichever mission ships last.
