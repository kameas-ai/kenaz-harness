# CI invariants

The harness mirrors Kenaz's five privacy CI invariants (plan §4.3). Each
gate runs on every PR and blocks merge on failure.

## #1 — Strict CSP (no CDNs, no outbound traffic)

- **Production CSP**: `default-src 'none'; connect-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'; object-src 'none'`.
- **Set in two places**: `<meta http-equiv>` in `frontend/index.html` (substituted by `frontend/vite.config.ts`) AND a Wails asset-server middleware (`core/rpc/csp.go`). The TS-side test (`main_test.go`) covers the middleware.
- **CI gate**: `bash scripts/ci/check-csp.sh frontend/dist/index.html` greps the production-built HTML and asserts the policy is not weakened.
- **Dev CSP**: relaxed to permit Vite HMR (`unsafe-eval`, `ws://localhost:*`) — guarded by `command === 'build'` in `vite.config.ts`.

## #2 — No user content in slog calls

- Forbidden field names: `Subject`, `SubjectDim`, `Body`, `Prompt`, `Response`, `DraftInput`, `Path`. Per-field `// privacy:never-log` struct comments add to the list.
- **CI gate**: `bash scripts/ci/check-no-user-content-in-slog.sh`.
- Allowlist: `// privacy-allow: <reason+ticket>` end-of-line marker.

## #3 — Test-only hooks stay in `_test.go`

- Forbidden in non-`_test.go` files under `core/rpc/` and `core/rpc/views/*`: exported identifiers prefixed `Test`, `Fake`, `Stub`, `Fixture`.
- **CI gate**: `bash scripts/ci/check-test-only-symbols.sh`.

## #4 — CSS token discipline

- Forbidden outside `frontend/src/styles/tokens.css`: raw hex literal, `rgb()`, `rgba()`, `hsl()`, `hsla()`, `oklch()`, hard-coded font-family stack.
- **CI gate**: `node scripts/ci/check-css-tokens.mjs` (npm: `npm run check:css-tokens`).
- Allowlist: `// css-tokens-allow: <reason+ticket>` end-of-line marker.

## #5 — Single-file persistence with schema versioning

- All UI state lives in `$USER_CONFIG_DIR/kenaz-harness/settings.json` with a top-level `schemaVersion: 1` integer.
- **CI gate**: `bash scripts/ci/check-single-persistence-file.sh` (asserts a single canonical filename in `core/rpc/settings.go`).
- The Go-side test (when WP13 lands) covers schemaVersion migrations + corruption recovery.

## Adjacent guardrails

These are not part of the five Kenaz invariants but ride alongside:

- **wailsjs isolation**: only `frontend/src/lib/harnessClient.ts` may import from `wailsjs/*`. Enforced by ESLint `no-restricted-imports` AND `bash scripts/ci/check-wailsjs-isolation.sh`. (FR-007 / C-001 / plan §4.1)
- **emitter isolation**: only `core/rpc/emitter.go` and `core/rpc/stream_broker.go` may call `runtime.EventsEmit`. Enforced by `bash scripts/ci/check-emitter-isolation.sh`. (Plan §4.2 / WP11)
- **binding-name discipline**: Wails-reflected `Bindings` methods use `<View>_<Operation>` with `_` reserved as separator. Enforced by `bash scripts/ci/check-binding-names.sh`. (Plan §8 R-6)
- **no-credential-in-UI**: TS interfaces ending in `Reference|Credential|Secret` MUST NOT declare `value`/`secret`/`password`/`apiKey`/`token` fields. Enforced by ESLint custom rule + `bash scripts/ci/check-no-credential-in-ui.sh`. (FR-020 / C-004)
- **bundle-size budget**: gzipped JS+CSS under `dist/assets` ≤ 1.5 MB. Enforced by `node scripts/ci/check-bundle-size.mjs`. (NFR-006)
