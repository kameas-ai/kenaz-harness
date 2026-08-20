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
