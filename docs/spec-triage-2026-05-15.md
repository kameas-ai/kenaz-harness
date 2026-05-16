# Spec triage — v0.17.0 final pre-1.0 inventory

**Date**: 2026-05-15
**Auditor**: claude-sonnet-4-6
**Scope**: every `kitty-specs/<mission>/` directory (excluding `_archive/`)

## Summary

- Total active missions surveyed: 15
  - SHIPPED (recommend archive): 11
  - POST-1.0 (mark deferred): 0
  - IN-FLIGHT / NEAR-TERM (leave active): 4

Note: Two missions (`integration-test-harness-01KQ8TD1` and `multimodal-io-extended-01KQ8TD2`) shipped partially — Phase 1 / Wave 1 is in main, but residual scope in each spec has not yet shipped. Both are counted under IN-FLIGHT because the spec itself still describes undelivered work.

---

## SHIPPED — recommend `git mv kitty-specs/<mission>/ kitty-specs/_archive/<mission>/`

| Mission | Shipped in | Evidence |
|---|---|---|
| `a11y-backlog-cleanup-01NDFSEX07` | v0.16.1 | Commit `ef5d46c` "fix(a11y): v0.16.1 — discharge accessibility-audit backlog (D-01..D-10)"; all 10 deferrals resolved or explicitly deferred-post-1.0; `docs/a11y-followups-stubs.md` deleted |
| `accessibility-audit-01KQ8TDA` | v0.16.0 | Commit `9321cdd` "feat: v0.16.0 — maturity hardening (audit + sentry + a11y)"; `eslint-plugin-vuejs-accessibility` v2.5.0 + 10 rules + `vitest-axe` + 6 `.a11y.test.ts` files + audit report `docs/a11y-audit-2026-05-15.md` all present |
| `audit-log-enhancement-01KX5R8F` | v0.16.0 | Commit `9321cdd`; tamper-evident hash chain, schema_version column, saved_audit_queries table (migrations 0005 + 0006), retention policy Settings panel, filter UX upgrade, CSV/JSONL/PDF export (gofpdf), per-event detail drawer, run-id timeline, Cedar `audit.bulk_purge` action — all present in `core/context/audit/` and `core/rpc/views/audit/` |
| `azure-openai-adapter-01KQ8VMZ` | v0.15.0 | Commit `5007004` "feat: v0.15.0 — provider expansion"; `core/llm/azure/` package exists with adapter, auth, deployments, URL builder, integration tests, `HARNESS_AZURE_OPENAI` feature flag |
| `custom-openai-compatible-endpoint-01KQ8VN0` | v0.15.0 | Commit `5007004`; `core/llm/custom/` package exists with 9-template registry, probe protocol, SQLite capability matrix, frontend `CustomEndpointFields.vue`, `HARNESS_CUSTOM_OPENAI` flag |
| `google-vertex-gemini-adapter-01KQ8VMY` | v0.15.0 | Commit `5007004`; `core/llm/gemini/` package exists with API-key + service-account JWT RS256 + ADC auth, `thinkingConfig` for 2.5 models, multimodal vision, cost table, frontend AddProviderForm Vertex branch, `HARNESS_GOOGLE_GEMINI` flag |
| `local-model-runtimes-01KQ8VMZ` | v0.15.0 | Commit `5007004`; `core/llm/localruntime/` package exists with detection for Ollama/llama-server/LM Studio/Jan/GPT4All, system-resources module, per-runtime metadata fetchers, `AutoConfigureLocalRuntime` RPC, `LocalRuntimesSection.vue`, `HARNESS_LOCAL_RUNTIMES` flag |
| `model-fallback-routing-01NDFSEX04` | v0.15.1 | Commit `f847e4d` "fix: v0.15.1 — model fallback routing"; `core/llm/fallback/` package with Chain/ChainEntry/TriggerCondition, connector retry loop, `llm:fallback-attempted` broker event, Cedar `llm_fallback` action, `LLM_ListFallbackChains` + `LLM_SaveChain` + related RPCs, `LLMRoutingPanel.vue`, `FallbackActivePill.vue` |
| `provider-implementation-uniformity-01KQ8V4F` | v0.15.0 | Commit `5007004`; `ProviderCapabilities` struct + `ReasoningStyle` enum, `RequestKnobs` surface, `ErrUnsupportedFeature`, `core/llm/openaiwire/` shared library, `core/llm/synthstream.go` + `toolextract.go` + `classifystatus.go` top-level utilities, SQLite capability cache (migration 0329), curated capability tables across all 4 original adapters, `/effort` slash command |
| `sentry-error-monitoring-01KX5R8G` | v0.16.0 | Commit `9321cdd`; `core/sentry/` package exists with `tier.go`, `redactor.go`, `breadcrumbs.go`, `panic.go`, `client.go`, `slog_handler.go`; 8-class redactor, 50-entry breadcrumb ring buffer, three-tier opt-in, Settings → Privacy → Crash Reporting, `HARNESS_SENTRY_DSN` env var |
| `session-export-01NDFSEX05` | v0.14.0 | Commit `eb0e8cd` "feat: v0.14.0 — session portability"; `core/sessions/export/` package exists, `Sessions_Export` RPC in `core/rpc/views/sessions/`, `ActionExportSession` Cedar action, `KindSessionExport` audit kind, Download icon in `LeftRail.vue`, Export button in `SessionHeader.vue` |

---

## POST-1.0 — recommend mark deferred

No missions triage cleanly into this bucket based on available evidence. The two candidates examined:

- `fleet-integration-01KX5R8D` is explicitly scoped for v0.18.0 (pre-1.0, per roadmap "Next +4") — it is a planned pre-1.0 deliverable, not a post-1.0 deferral.
- `acp-orchestration-integration-01NDFSEX06` is similarly slotted for v0.18.0 (same roadmap section as fleet).

Neither mission's spec uses language like "after GA" or "post-1.0." Both are on the pre-1.0 critical path per the roadmap's v1.0.0 definition. They remain IN-FLIGHT / NEAR-TERM.

---

## IN-FLIGHT / NEAR-TERM — leave active

| Mission | Slotted for | Notes |
|---|---|---|
| `fleet-integration-01KX5R8D` | v0.18.0 (roadmap "Next +4" — Fleet integration) | `core/fleet/` package does NOT exist yet. The spec describes the complete integration surface (device-code OAuth, `RequireActiveAccount`, share-workflow/pack/bundle, OTel-to-fleet sink, audit archival, team Cedar policy distribution). The underlying `core/acp/` wire layer is present but the fleet-side contract is still "WIP" per spec §1. Full spec still describes undelivered work. Leave active; coordinate with fleet repo GA. |
| `acp-orchestration-integration-01NDFSEX06` | v0.18.0 (roadmap "Next +4" — alongside fleet) | `core/acp/` exists (envelope, peers, transports, verify sub-packages) but NO RPC verbs (`ACP_ListPeers`, `ACP_TrustPeer`, `ACP_Dispatch`, `ACP_GetTrace`) are present in `core/rpc/bindings.go`. No Cedar `ACP::Envelope` resource type. No Settings → Peers panel in frontend. The wire-layer bytes-in/bytes-out is done (archived `acp-orchestration-01KQ17ZK`); this mission wires it to the harness surface. Fully unstarted. Leave active. |
| `integration-test-harness-01KQ8TD1` | Phase 2 still pending (no version yet) | Phase 1 shipped in v0.13.0 (`core/llm/wirecheck/`, coverage_registry.yaml, locked-tier wire goldens, seam recorders). Phase 2 per the spec is the live-tier nightly cron drift detection + auto-PR on response-fixture drift (FR-006b/c/d). No nightly cron workflow exists in `.github/workflows/`. The spec's FR-006b/c/d deliverables are unshipped. The spec as written describes both phases; leave active until Phase 2 ships. |
| `multimodal-io-extended-01KQ8TD2` | v0.17.0 or later (audio/video/office/archive wave) | Wave 1 (outbound image generation, JSON-mode, `GeneratedImageBlock`, capability YAML for image-gen models) shipped in v0.13.0. The spec's remaining deliverables — audio I/O (`ContentBlock` types `audio`/`video`), office-document extractors (`core/extractors/` package), archive unpacking, video support via Vertex GCS, `document_extracted` content blocks — are completely absent from the codebase (`ContentBlock` struct has no `audio`/`video` types; `core/extractors/` does not exist). The spec title matches what shipped only partially; the larger audio/file/binary interop scope is still open. Leave active. |

---

## Triage notes

### Contested calls

**`integration-test-harness-01KQ8TD1`** — the v0.13.0 commit message explicitly says "Phase 2 live-tier cron drift detection deferred." The spec's FR-006b/c/d (nightly CI job + auto-PR for response-fixture drift) are unambiguously unshipped. However, the mission's core value proposition (wire-shape contract tests as a CI gate) is fully operational. If the team decides the live-tier is genuinely non-critical pre-1.0, this could be archived now and FR-006b/c/d tracked as a separate small spec. The call here is conservative: leave active until explicitly closed.

**`multimodal-io-extended-01KQ8TD2`** — the mission directory name and the v0.13.0 commit message both use the ULID `01KQ8TD2`, confirming this is the spec that partially shipped. The delivered scope (outbound image gen + JSON-mode) is substantial and fully operational. The remaining scope (audio, video, office docs, source-code archives) was never deferred to a separate spec; it just wasn't dispatched in v0.13.0. Options: (a) leave active and dispatch the audio/video/office/archive WPs in v0.17.0+, or (b) split into a new `multimodal-io-phase2` spec and archive this one. Leaving active is the lowest-friction path if the remaining scope is genuinely planned for the near term.

**Roadmap staleness** — the `docs/roadmap.md` file (gitignored) reflects v0.13.1 as the latest tag and has not been updated for v0.14.0 through v0.16.1. The "Next minor / Next +1 / ..." section labels are therefore stale (they predate v0.15.0–v0.16.1 shipping). The actual current state is: latest tag = v0.16.1; the roadmap's "Next +3" (Security + bug-bash + Final spec triage) is v0.17.0 in progress; "Next +4" (Fleet + ACP) maps to v0.18.0. The roadmap should be updated to reflect the shipped minors and the correct "Next" labels before v0.17.0 closes.

### Confirmation that nothing is genuinely POST-1.0 in the active set

All 15 active missions are either already shipped or on the documented pre-1.0 roadmap path. None of the specs use post-1.0 deferral language. The roadmap's "Cross-cutting deferred / unlanded" section reads "None at present." No missions need a POST-1.0 marker based on this audit.
