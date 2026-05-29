# Fleet integration — spec breakdown

**Status**: draft · **Owner**: alecfeeman · **Created**: 2026-05-16
**Source material**:
- `~/PycharmProjects/kameas-ai/product-architecture.md` — product taxonomy + data sovereignty matrix
- `~/PycharmProjects/kameas-ai/orgs-tiers-billing-design.md` — tier/capability matrix + billing model
- `kitty-specs/fleet-integration-01KX5R8D/spec.md` — original XL umbrella spec (this doc supersedes its mission breakdown; the spec stays for cross-reference)

## Why break it up

The original `fleet-integration-01KX5R8D` mission is one XL spec covering auth + identity + 8 cloud features + OTel + audit archival + lockdown + settings sync + share workflows + Cedar policy distribution + fork recipe. Trying to ship that as a single mission produces:

- Hundreds of WPs that never close consistently
- No natural minor-version cuts — what's v0.18.0 vs v0.19.0?
- Hard to schedule against the proprietary `kenaz-fleet` repo's API readiness — different cloud features will be ready at different times
- Risk of merge conflicts on the harness side (every cloud feature touches the same `core/fleet/` package)

The seams below mirror the **dependency order**: auth + capabilities first; each subsequent mission gates behind those two; cloud features (share/sync) ship as the fleet repo's API surface comes online.

## The 8 sub-missions

```
              fleet-auth-foundation        (v0.18.0 critical path)
                       │
                       ▼
            fleet-capability-surface       (v0.18.0 critical path)
                       │
       ┌──────────────┬┴───────────┬──────────────┬──────────────┬──────────────┐
       ▼              ▼            ▼              ▼              ▼              ▼
  config-pull   otel-archival   emergency-     audit-         share-and-     context-
  (v0.19.0)     (v0.19.0)       lockdown       archival       sync           sync
                                (v0.19.0)      (v0.20.0)      (v0.20.0)      (v0.21.0)
```

Each fans out independently after the foundation lands. Suggested release slotting:
- **v0.18.0** = `fleet-auth-foundation` + `fleet-capability-surface`. Smallest viable fleet integration — a logged-in user can read the capability matrix and the harness gates features against it. No actual cloud-side functionality yet.
- **v0.19.0** = `fleet-config-pull` + `fleet-otel-archival` + `fleet-emergency-lockdown`. Pull-config (OPA bundle distribution), one outbound telemetry path, one inbound lockdown path. These are operationally critical and depend only on auth + capabilities.
- **v0.20.0** = `fleet-audit-archival` + `fleet-share-and-sync`. The highest-value Team-tier features — they depend on the fleet repo's catalog + immudb APIs being live. Share-and-sync covers workflow/pack/bundle catalog, settings sync (provider profiles, model prefs, MCP recipes, installed MCP server registry, UI theme), and team Cedar policy distribution.
- **v0.21.0** = `fleet-context-sync`. Conversation/session and project-context sync with E2E AEAD encryption + team handoff. Encryption-heavy and privacy-sensitive; deliberately isolated from the smaller-payload settings sync so the threat model + recovery UX get focused review.

If the fleet repo lags on a particular API, the corresponding harness mission slips to the next minor without blocking the others.

## Sub-mission summaries

### A. `fleet-auth-foundation-01NDFSEX08` (v0.18.0, **M**, ~12h)

`core/fleet/` package skeleton + Zitadel PKCE+loopback auth + credstore-backed token + JWT claim extraction (`org_id`, `user_id`, role) + `HARNESS_FLEET_DISABLED=1` kill switch + fork-recipe docs.

Key surfaces:
- `core/fleet/auth.go` — `DeviceCodeFlow` (open browser → poll token endpoint), `RefreshToken`, token-stored-in-credstore semantics under locator `fleet:default:token`
- `core/fleet/identity.go` — `Identity{UserID, OrgID, Tier, Roles[]}`, fetched from `GET /api/v1/me` on first login, refreshed every 24h
- `core/fleet/client.go` — HTTP client that injects `Authorization: Bearer …` from credstore, retries with backoff
- `core/fleet/flags.go` — `Disabled() bool` checks `HARNESS_FLEET_DISABLED` env, parallel to sentry's pattern
- `frontend/src/views/settings/AccountPanel.vue` — sign-in button + signed-in state
- `docs/missions/fleet-auth-foundation.md` — fork-recipe (4 wire-up points to remove for a clean local-only fork)

Acceptance: harness can sign in to fleet via PKCE, token is stored in credstore, `Identity` populated, kill switch short-circuits all fleet calls, fork-recipe verified by `go build ./...` with `core/fleet/` removed.

### B. `fleet-capability-surface-01NDFSEX09` (v0.18.0, **M**, ~10h)

Capability matrix fetcher + cache + per-feature gate accessor + frontend feature-flag wiring.

Key surfaces:
- `core/fleet/capability.go` — `Capabilities` struct mirroring the orgs-tiers-billing matrix (~25 capability keys), `Has(key) bool` accessor, `Require(key) error` typed gate
- Boot-time pull from `GET /api/v1/me/capabilities`, cache to `<DataDir>/fleet/capabilities.json` with TTL=5min, refresh on backoff if stale
- `frontend/src/lib/featureFlags.ts` — extended with a `capability(key)` resolver; backed by `Client.appInfo().capabilities`
- Per-feature gates: cloud surfaces (share buttons, catalog tabs, analytics links) consult `capability.Has(...)` before rendering
- Fail-soft: when offline / not signed in, all cloud capabilities return `false`; no UI errors

Acceptance: signed-in user with Pro tier sees `hosted_inference: true`, `shared_team_graph: false`, etc. Tier downgrade propagates within 5min. UI elements gated correctly. Logged-out state hides all cloud features.

### C. `fleet-config-pull-01NDFSEX10` (v0.19.0, **M**, ~12h)

Pull-based config distribution. Fleet pushes nothing; harness polls.

Key surfaces:
- `core/fleet/config_pull.go` — `GET /api/v1/configs?machine=<machine_id>&checksum=<cached>` poll at boot + 5min
- Signed bundle verification — fleet signs bundles with its release key; harness has the public key embedded
- Bundle contents: OPA bundle (Cedar policy delta), MCP allow-list, model prefs (default tier, allowlisted providers), weight URLs (for kameas-ml)
- ACK pattern: on apply success, POST `/api/v1/configs/<bundle_id>/ack`
- Survives Fleet outages: cached bundle applied at boot; refresh resumes when fleet reachable

Acceptance: a Cedar policy pushed via fleet propagates within 5min, signature verification rejects tampered bundles, fleet-outage scenario doesn't degrade the harness.

### D. `fleet-otel-archival-01NDFSEX11` (v0.19.0, **M**, ~14h)

OTel `multi_exporter` wrapper sending spans to local + Fleet in parallel.

Key surfaces:
- `core/telemetry/otel/multi_exporter.go` — fans each span to N configured exporters; independent failure semantics
- `core/fleet/otel_exporter.go` — bounded ring buffer (1000 spans) + 30s batched POST to `/otel/v1/traces` and `/otel/v1/metrics`
- `core/telemetry/otel/fleet_redactor.go` — strips `@secret:` attribute values, configurable attribute deny-list (`prompt`, `response`, `tool_input`, `tool_output` defaults), `private.` prefixed attrs dropped entirely
- Opt-in: per-team default (org-admin choice via fleet dashboard) + per-user override (Settings → Privacy → Send my telemetry to fleet)

Acceptance: span attributes survive redaction round-trip, no `prompt` content reaches fleet, opt-out short-circuits the exporter, bounded ring buffer drops oldest on network failure.

### E. `fleet-emergency-lockdown-01NDFSEX12` (v0.19.0, **M**, ~10h)

Long-poll inbound channel from harness to fleet so a fleet admin can fire a near-realtime lockdown signal.

Key surfaces:
- `core/fleet/lockdown.go` — `GET /api/v1/lockdown/wait` long-poll (60s timeout, immediately reconnects)
- On lockdown signal: stop all in-flight LLM/tool calls, drop to read-only state, post `audit.lockdown_received` event, display banner UI
- Local override: `HARNESS_FLEET_LOCKDOWN_BYPASS=1` env (audit'd on use) for emergency recovery
- `frontend/src/components/shell/LockdownBanner.vue` — red banner across the top, persistent until admin un-locks

Acceptance: lockdown signal from fleet (simulated) freezes harness within 2s, audit event emits, banner shows; second lockdown signal after un-lock works the same.

### F. `fleet-audit-archival-01NDFSEX13` (v0.20.0, **L**, ~16h)

Team+ tier `audit_log_immudb` capability — stream the local hash-chained audit events to fleet's immudb endpoint.

Key surfaces:
- `core/fleet/audit_archival.go` — batch-flush local audit hash chain to `POST /api/v1/audit/archive` with verified prev-hash chain
- End-to-end chain verification after archive (fleet's immudb returns a Merkle proof; harness verifies the chain continues unbroken)
- Retention policy on local rows: after successful archive, optionally delete local rows older than N days (default 90d), preserving the hash chain
- Settings → Audit → Cloud archival toggle (when capability is on)
- Builds on the v0.16.0 `audit-log-enhancement-01KX5R8F` infrastructure (hash chain + retention sweep already shipped)

Acceptance: 10k local audit rows archive cleanly with chain verification, fleet outage doesn't lose rows (they queue locally), retention sweep deletes only post-archive rows.

### G. `fleet-share-and-sync-01NDFSEX14` (v0.20.0, **L**, ~19h)

The highest-value Team-tier cloud features. Workflow catalog, context pack registry, bundle registry, settings sync (including installed-MCP server registry), team Cedar policy distribution.

Sync categories explicitly include the **installed MCP server registry** (the set of MCPs a user has installed + their non-secret per-server config), distinct from MCP recipes (the templates). Credstore bytes for MCP secrets are never synced; recipients are prompted to provide their own.

Key surfaces:
- `core/fleet/catalog.go` — `PublishWorkflow(scope, workflow)`, `ListWorkflows(scope)`, `InstallWorkflow(id)`; same shape for packs + bundles
- `core/fleet/sync.go` + `sync_mcp.go` — push selected settings (provider profiles metadata, model prefs, MCP recipes, **installed MCP server registry**, UI theme) to fleet; fetch on first login on a new device; per-category LWW with collection dedupe
- `core/fleet/policy_publish.go` — admin "Publish to team" flow on Cedar rules; redistributes via existing config-pull pipeline
- Frontend surfaces: Marketplace view; SyncPanel with secret-prompt banner for incoming MCPs; team-policy badge + Publish button in Cedar editor

Acceptance: a workflow published from device A appears in device B's catalog tab; installing it materializes the local YAML; installed-MCP sync re-installs servers on a second device (with secret-prompt for ones requiring credentials); team Cedar policy merge takes precedence over user policy.

### H. `fleet-context-sync-01NDFSEX15` (v0.21.0, **L**, ~24h)

Conversation/session and project-context sync across the same user's devices, plus team handoff. Append-only event-stream pattern with end-to-end AEAD encryption (per-user key from credstore seed; fleet stores ciphertext only).

Why split from share-and-sync: size (sessions can be hundreds of MB), sensitivity (chat content is the most private surface), conflict model (append-only events, not LWW key-value), and resumability semantics make this a distinct beast.

Key surfaces:
- `core/fleet/context_crypto.go` — per-user seed in credstore, HKDF key derivation, XChaCha20-Poly1305 AEAD, recovery-code mint+verify
- `core/fleet/context_sync.go` — event-stream primitive: chunked backfill, debounced stream, replay with `since=<seq>`
- `core/fleet/session_sync.go` + `project_sync.go` — per-session and per-project opt-in toggles; bridge harness session/project events to the encrypted stream
- `core/fleet/team_handoff.go` — Team+ tier: re-encrypt for recipient + route via fleet's KX/identity service + recipient inbox
- Frontend: session header sync toggle, "Synced to fleet" badge, share-with-teammate dialog, "Shared with you" inbox, recovery-code generation/apply flow, per-artifact-class opt-in panel on projects

Acceptance: chat session on device A resumes mid-conversation on device B (with recovery code); shared session lands in teammate's inbox; project metadata + agent memory + text notes follow user across devices; fleet operator never sees plaintext.

## Things deliberately deferred

- **Hosted inference proxy** — was in the original spec under §2.3 feature gates. Re-scoped into the `fleet-capability-surface` mission: `hosted_inference: true` enables routing LLM calls through fleet's inference endpoint, which is just a `core/llm/` adapter pointed at the fleet base URL. The adapter ships in the capability-surface mission as a tier-gated alternative endpoint, not a standalone mission.
- **Attestation aggregation** (TPM measured-boot) — only relevant when the harness runs inside the kenaz-sandbox VM. Out of scope for the harness-only fleet integration; lives in `kenaz-sandbox` repo.
- **Stripe wiring** — fleet-side concern, not harness-side. Lives in the proprietary `kenaz-fleet` repo's `004-billing-stripe` spec.

## Roadmap impact

The roadmap's "Next + 4 Fleet integration" row gets split:
- **v0.18.0** — fleet-auth-foundation + fleet-capability-surface (~M-sized minor; the smallest useful fleet-integration footprint)
- **v0.19.0** — fleet-config-pull + fleet-otel-archival + fleet-emergency-lockdown (3 missions; ~36h combined)
- **v0.20.0** — fleet-audit-archival + fleet-share-and-sync (2 missions; ~32h combined)
- **v0.21.0** — fleet-context-sync (1 mission; ~24h; encryption-heavy)
- **v1.0.0** — GA after the 4 fleet minors burn in

The original `fleet-integration-01KX5R8D` spec stays as a cross-reference document but its "single mission" framing is superseded by this breakdown.

## What still needs decisions

1. **`kameas-native` Zitadel app config** — exact PKCE redirect URI, scopes requested, refresh-token policy. Lives in `kameas-infra`. Block on confirming the Zitadel app definition before `fleet-auth-foundation` ships.
2. **Capability matrix wire shape** — orgs-tiers-billing-design.md §2 has the matrix in markdown. Need a JSON schema for `/api/v1/me/capabilities` response. Probably the fleet repo's `003-orgs-and-tiers` spec ships this.
3. **Fleet bundle signing public key distribution** — embedded in harness binary or fetched at first sign-in? Embedded is simpler; fetched supports key rotation without a harness release. Lean: embedded for v0.18.0, revisit at v1.0.
4. **Per-user telemetry opt-in storage** — fleet stores `(user_id, telemetry_class) → bool`; harness must check before each post. Block on fleet repo defining the `/api/v1/me/telemetry-consent` shape.
