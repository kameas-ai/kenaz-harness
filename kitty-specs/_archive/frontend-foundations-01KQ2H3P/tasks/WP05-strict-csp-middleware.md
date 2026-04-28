---
work_package_id: "WP05"
title: "Strict CSP middleware on Wails asset server (connect-src 'none', no CDNs)"
dependencies:
  - "WP01"
  - "WP03"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 5 - Privacy CI invariants"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP05 – Strict CSP middleware (Go side, Wails asset server)

## Goal

Implement Kenaz privacy CI invariant #1: a strict Content-Security-Policy applied via `<meta http-equiv>` in `index.html` AND duplicated as a Wails asset-server response-header policy where supported. Production CSP forbids CDNs, blocks all network egress (`connect-src 'none'`), and pins fonts/scripts/styles to `'self'`. A relaxed dev CSP allows Vite HMR. CI greps the production-built `index.html` and asserts the strict policy is present and not weakened.

## Spec references

- C-005 (local-first; zero outbound network traffic in steady state)
- FR-020 (no credential values in UI — defence in depth)
- C-004 (no credential values in UI)

## Plan references

- §4.3 invariant #1 ("Strict CSP: `default-src 'none'; connect-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'; object-src 'none'`. Set in `index.html` `<meta http-equiv>` AND duplicated as a Wails response-header policy where supported. **No CDNs.**")
- §7 v1.0 item 12 (privacy CI invariants gate the PR)
- §8 R-4 (CSP strictness breaks Vue dev tooling — two CSPs gated by `import.meta.env.DEV`)

## Subtasks

- T001 — Add a Go-side asset-server middleware in `core/rpc/` (e.g., `core/rpc/csp.go`) wrapping the Wails `AssetServer.Handler`. Set `Content-Security-Policy` response header to the strict production policy. Wire it through `main.go` Wails options.
- T002 — In `frontend/index.html` add `<meta http-equiv="Content-Security-Policy" content="...">` with the strict production policy. Use a Vite plugin or build-time substitution so dev mode injects a relaxed CSP (`script-src 'self' 'unsafe-eval'`, `style-src 'self' 'unsafe-inline'`, `connect-src 'self' ws://localhost:*`) and prod injects the strict CSP.
- T003 — Add a CI grep `scripts/ci/check-csp.sh` that runs against the production-built `dist/index.html` and asserts: `script-src` does NOT contain `unsafe-inline` or `unsafe-eval`; `connect-src` is exactly `'none'`; no CDN host appears. Wire into CI as a build-blocking check.
- T004 — Add an integration test that boots the Wails binary in headless/dev mode and asserts the Go middleware sets the header on a fetched static asset.

## Acceptance criteria

- `dist/index.html` ships with the strict CSP meta tag in production builds.
- The Go asset-server middleware sets the same CSP header on every static asset response.
- `npm run dev` works end-to-end — Vite HMR is not broken by the dev-mode CSP.
- `scripts/ci/check-csp.sh` exits 0 on a clean production build; exits non-zero if any `unsafe-*` is added or any non-`'self'` host appears in `connect-src` / `font-src` / `script-src`.
- Loading the production app shows zero CSP violations in DevTools console.
- No font, image, script, or style loads from a CDN — verified by Network tab.

## Files to create/modify

- Create: `core/rpc/csp.go` (asset-server middleware).
- Modify: `main.go` (wire middleware into Wails options).
- Modify: `frontend/index.html` (CSP meta tag).
- Modify: `frontend/vite.config.ts` (dev vs prod CSP injection).
- Create: `scripts/ci/check-csp.sh`.
- Modify: CI workflow to call `check-csp.sh` after `npm run build`.
- Update: `docs/ci-invariants.md` with invariant #1.

## Definition of done

- All acceptance criteria pass.
- Production binary serves a strict CSP; dev binary serves a relaxed CSP that permits HMR.
- Documentation lists invariant #1 alongside invariant #4 from WP04.
- WP14 will land invariants #2, #3, #5 pointing at the same docs page.
