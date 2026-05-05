# Roadmap

Mission-to-release mapping. One line per mission; durations are guesses, not commitments. The release line is **0.MINOR.PATCH** — minor bumps for feature missions, patch bumps for fixes & polish.

Source of truth for each mission: `kitty-specs/<mission-id>/spec.md`. Shipped missions are moved to `kitty-specs/_archive/`.

---

## Already shipped (v0.1.0 – v0.5.0)

| Version | Theme | Key missions |
|---|---|---|
| v0.1.0 | Foundation | storage-foundations, secrets-keychain, policy-engine, event-log, llm-connector, frontend-foundations |
| v0.2.0 | Tool execution | mcp-client, mcp-server, mcp-tool-execution, mcp-stdio-pool, agent-kernel-graph, tool-discovery-wiring, telemetry-otel |
| v0.3.0 | Beta | session-auto-titling, cross-session-search, markdown-rendering-polish, keyboard-shortcuts-settings, mcp-server-install, custom-mcp-installer, save-artifact-builtin, artifacts-storage, scheduler, workflow-engine, context-library, storage-consolidation, compaction-strategy-ui (TDI), memory-prune-completion, branch-as-subagent-recommendation, long-turn-resilience, token-cost-telemetry, cedar-credential-policy |
| v0.4.0 | Auto-update + install UX | auto-update-service, harness-self-mcp-onboarding, agent-kernel-graph-node-catalog, .dmg installer (v0.4.5), Windows NSIS + Linux AppImage (v0.4.6) |
| v0.5.0 | Workflows + agent UX + filesystem tools | workflows-agentic, mcp-server-health-ui, branching-ux-polish, backend-context-window-length, builtin-filesystem-tools, edit-file-artifact-sync |

---

## v0.5.x — patch lane

Polish + low-risk follow-ups + closeout of v0.5.0 deferred work. Ranked by ship order.

### v0.5.1 (next patch)
- **`builtin-filesystem-tools-01KR3N4P` WP06** — frontend FSRead/FSWrite toggle UI in Settings → Tools (deferred from v0.5.0 ship)
- **`edit-file-artifact-sync-01KQ8TD5` WP06** — e2e test + OTel instrumentation (deferred from v0.5.0 ship)
- **Migration-doctor UI** (NEW) — Settings → Health panel that detects ledger drift (e.g. tonight's renumbering bug) and offers a guided rename. No CLI surface — UI-only. Triggered on chassis boot via background scan.
- **Restore Windows NSIS installer** — v0.5.0 release.yml regression dropped the `-nsis` step; restore the matching v0.4.6 NSIS pipeline.

### v0.5.2
- **`per-message-token-meter-01KR3PQR`** — frontend overlay on existing `last_usage_json` data (S effort, no schema)
- **`compaction-strategy-ui-01KQ8TD8`** — settings surface for an existing capability (S)

### v0.5.3
- **`artifact-preview-binary-rendering-01KQ8TD5`** — PDF/image/binary inline rendering in the artifact panel (M, frontend-only)

### v0.5.4
- **`update-artifact-tool-01KQ8TD4`** — `__update_artifact` builtin (M); cross-mission dep already partially landed via edit-file-artifact-sync
- **`harness-self-mcp-onboarding-01KQ8TDU` residual WPs** — tail polish on what shipped in v0.4.2

---

## Provider strategy through pre-1.0

**OpenRouter is the primary provider lane until 1.0.** It exposes hundreds of models across Anthropic, OpenAI, Google, Meta, Mistral, etc. through a single adapter that already ships, so users get provider diversity for free while we keep our adapter surface area small. Anthropic-direct also stays supported for low-latency / first-class tool-use paths.

Direct adapters (Azure-OpenAI, Vertex/Gemini, local runtimes, custom OpenAI-compatible) are deferred to **v0.9.0**. We ship UX and product depth on the existing OpenRouter + Anthropic surface first; broaden the adapter list once the product itself is mature.

---

## v0.6.0 — User creation tools + UX maturity

**Theme**: Move from "tool we built" to "tool the user shapes." Power-user surface area + workflow product polish.

| Mission | Why now | Effort |
|---|---|---|
| `user-slash-commands-01KQ8TD9` | User-defined `/commands` — biggest power-user lever | M |
| `cedar-policy-editor-ui-01KQ8TD6` | Policy editing today is YAML-only; UI unblocks non-technical admins | M |
| `memory-narrative-layer-01KQ8TD1` | Memory presentation layer — surfaces what was remembered + why | M |
| `workflow-extensions-01KW2D3Y` (catalog UI + scheduled inbox WPs) | Front-end half of workflows: drag-drop editor, scheduled-runs inbox | L |
| `multimodal-io-01KQ8TDF` | Image input through chat. Vision is the most-requested capability gap and OpenRouter already serves vision-capable models | L |

**Goal**: a v0.6.0 user can author their own slash commands, edit Cedar policies in the UI, drag-drop workflow nodes, and paste an image into chat.

---

## v0.7.0 — Structured generation + scheduled agents + unified search

**Theme**: Three foundational platform gaps that unblock downstream features. Each addresses a "wait, we don't have that?" — see the matching specs for details.

| Mission | Why now | Effort |
|---|---|---|
| `structured-output-and-grammar-01KX5R8A` | First-class JSON schema / response_format / GBNF support across all adapters. Unblocks workflows that need typed output, `/json` slash command, model-driven structured replies | L |
| `scheduled-chat-runs-01KX5R8B` | Cron-fired chat sessions (separate from workflow YAML) — daily-EA-briefing as a saved schedule, not a workflow definition | M |
| `unified-search-01KX5R8C` | Cmd+K palette search across messages + artifacts + memory + corpus + audit. Closes a long-standing data-discovery gap | M |
| `model-secret-references-01KW7M5A` | `@secret:<locator>` references the model can use without seeing plaintext + Cedar gating + per-turn fingerprint sanitizer | L |
| `provider-keychain-rotation-01KQ8TD9` | Production hygiene; quarterly rotation flow without re-onboarding | S |

---

## v0.8.0 — Multimodal extended + Web primitives + Test harness

**Theme**: Push past "chat with image" toward audio/file/binary interop, give workflows live-web data, lock in a real e2e test framework before opening the provider gates.

| Mission | Why now | Effort |
|---|---|---|
| `multimodal-io-extended-01KQ8TD2` | Audio input/output, file attachments, generated-image artifacts | L |
| `workflow-extensions-01KW2D3Y` (web fetch/scrape WPs) | Web fetch/scrape primitives for workflows beyond what v0.5.0 shipped | M |
| `integration-test-harness-01KQ8TD1` | E2E harness that boots full app + drives via UI events; needed for 1.0 confidence | L |
| `autonomy-dial-01KR3M2A` (residual) | Posture controls partially shipped in v0.4.1; finalize remaining dials | M |

---

## v0.9.0 — Provider expansion

**Theme**: Stop being OpenRouter+Anthropic-only. Add direct adapters now that the product is mature enough to justify the snowflake-per-provider surface area.

| Mission | Why now | Effort |
|---|---|---|
| `provider-implementation-uniformity-01KQ8V4F` | Foundation: Capabilities accessor + cache. Must land before direct adapters | M |
| `azure-openai-adapter-01KQ8VMZ` | Enterprise tablestakes (Azure-hosted OpenAI) | M |
| `google-vertex-gemini-adapter-01KQ8VMY` | Gemini 2.x direct (vs. via OpenRouter) | M |
| `local-model-runtimes-01KQ8VMZ` | Ollama / LM Studio / llama.cpp. Enables grammar-constrained sampling from `structured-output-and-grammar-01KX5R8A` | M |
| `custom-openai-compatible-endpoint-01KQ8VN0` | Long tail (Together / Groq / Fireworks / self-hosted vLLM) | S |

**Goal**: a v0.9.0 user can choose direct connections to Azure-OpenAI, Vertex/Gemini, Ollama, or any OpenAI-compatible endpoint instead of routing everything through OpenRouter.

---

## v0.10.0 — 1.0 readiness

**Theme**: Cut the rough edges that block calling this 1.0. No new feature work — just hardening, audits, polish.

| Mission | Why now | Effort |
|---|---|---|
| `accessibility-audit-01KQ8TDA` | WCAG 2.1 AA compliance sweep. Required for any enterprise rollout | L |
| Security audit pass | External pen-test against the released bundle | M |
| Documentation completeness pass | User-facing docs site, API reference, getting-started flows | M |
| Cross-platform burn-in | Windows + Linux + macOS soak across the v0.5.0 → v0.9.0 surface area | M |
| Final spec triage | Move every active spec to either shipped or explicitly-deferred-post-1.0 | S |

---

## v1.0.0 — General Availability

No new feature missions; v1.0.0 is the first version where:
- All v0.5.0–v0.10.0 missions have shipped and burned in for ≥30 days
- v0.10.0 accessibility audit clean
- v0.10.0 external security review clean
- Public documentation complete (`docs/missions/` + user-facing docs site)
- Auto-update path proven through ≥3 consecutive minor bumps without regression

---

## Cross-cutting deferred / unlanded

_None at present — every active spec has been mapped above._

---

## How to update this doc

When a mission ships, move its row out of the upcoming section and into the matching "Already shipped" cell. When a new spec lands in `kitty-specs/`, add it to the most appropriate upcoming version. When priorities flip, edit the version assignment in place — this doc is the single source of truth for "what's slotted where."

This file is ordered chronologically forward, not by priority. Within a single release the missions are roughly in dependency order (foundations first).
