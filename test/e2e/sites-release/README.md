# sites-release-e2e — runbook (prepped, awaiting creds)

Everything here is staged so the campaign is a near-one-command run once the **fleet admin CLI**
provisions an **Enterprise-tier (`CapSitesHosting`) test org + token** on dev.

## What the admin CLI needs to hand me
- `FLEET_API` — API base, from `https://dev.fleet.kameas.ai/config.json` → `api_base_url`
  (currently `https://api.dev.fleet.kameas.ai`).
- `TOKEN` — a valid access token for the **Enterprise** test org (else every `/sites/*` = 403 `capability_not_in_tier`).
- `ORG_ID` — org UUID (for the ungated `PUT /api/v1/orgs/{id}/slug`).

> Note: the self-serve `dev.fleet.kameas.ai/signup` flow only yields a **Pro** account (403 on sites),
> and I'm not permitted to complete account-creation / card entry myself — hence the admin-CLI path.

## Two phases (split per the fleet agent's handoff)

### Phase A — control-plane HTTP contract (runnable as soon as creds land; no hosting infra needed)
```bash
FLEET_API=https://api.dev.fleet.kameas.ai TOKEN=<token> ORG_ID=<uuid> \
  bash test/e2e/sites-release/run-phase-a-contract.sh
```
Verifies: capability gate + auth (WP00), ungated slug set, site CRUD + error codes
(`invalid_slug` 400, `slug_conflict` 409, `site not found` 404), and the env-var API round-trip
shape (WP05 API half). Creates+deletes a throwaway `e2e-contract-*` site. Writes evidence rows to
`results/.phase-a-rows.md`; I fold PASS/FAIL into `results/WP00.md`, `WP02.md`, `WP05.md`.

### Phase B — true end-to-end hosting (gated on kameas-infra sites-gateway apply + `kameas-fleet-sites-dev` S3 bucket)
Needs a working **dev harness build** (the `lleNativeClientID`/`lleAPIAudience` ldflags, or the installed
`Kenaz Harness.app` binary) to drive the sites MCP for real bundling/deploy, PLUS the gateway/Fargate infra.
- **WP01** static: deploy `fixtures/static-site/` → `sites_deploy` → status `live` → `curl` 200 + SPA fallback.
- **WP02** org-auth: unauth → 302 OIDC, member → 200, non-member → branded 403 (Chrome MCP for the OIDC dance).
- **WP03** Streamlit: `sites_init --framework streamlit` → deploy → `/_stcore/health` 200 → browser UI →
  `/_stcore/stream` WS upgrade + widget round-trip → **>2 min soak** (idle-kill regression).
- **WP04** Dash: `sites_init --framework dash` → deploy → `/` 200 → browser render.
- **WP05** runtime half: set `ECHO_VAR` via API → deploy/restart → app echoes on `/env-check`;
  `tar -tzf <bundle>` has no `.env*`; `strings <bundle> | grep ECHO_VAR` absent.
- **WP06** skill delivery: `ApplyMandatedSkills` installs `fleet-sites` skill for the entitled org; shows in `/skills`.
- **WP07** external agent: `claude mcp add fleet-sites -- <harness> mcp sites` (app not running) → fresh
  Claude Code session invokes `sites_list` + `sites_deploy`.
- **WP08** Tailnet DNS: `dig <site>--<org>.sites.dev.kameas.ai` from this (tailnet-connected) Mac → resolves + reachable.

## Merge gate (per tasks.md)
All 8 `results/WP0N.md` exist and read **PASS**; the `feature/fleet-sites → main` PR links them.
(Note: `feature/fleet-sites` already merged as v0.22.0, so this gate is now a post-merge validation —
a failing WP files a defect against the relevant foundation/MCP/UI mission rather than blocking a merge.)

## Assets staged
- `fixtures/static-site/` — WP01 static site (index.html + app.css + app.js; marker `kenaz-sites-e2e-static-ok`).
- `harness/run-phase-a-contract.sh` — parameterized Phase-A contract runner.
- `results/WP00.md … WP08.md` — scaffolds pre-loaded with each WP's checks.

## Environment facts confirmed (2026-07-06)
- This Mac is on the tailnet (`macbook-pro 100.68.253.104`) → Phase B HTTP/WS + WP08 dig are reachable from here.
- `dev.fleet.kameas.ai` = dashboard SPA (returns 200 HTML for any path — do NOT probe the API here);
  real API = `api.dev.fleet.kameas.ai` (returns 401 without a valid token — verified).
- Contract (from `core/fleet/sites.go`): `POST/GET/DELETE /api/v1/sites`, `…/{id}/deployments` (bundle upload),
  `…/{id}/env` (PUT `{vars}` / GET), `…/{id}/logs?tail_lines=N`, `PUT /api/v1/orgs/{id}/slug` (ungated).
  Deployment status lifecycle: `uploading → building → deploying → live | failed`.
