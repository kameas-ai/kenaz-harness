# CI invariants

The harness mirrors Kenaz's five privacy CI invariants (plan §4.3). Most
gates run on every PR and block merge on failure. **Two do not** — see the
"Enforcement status" table below. That sentence used to read "Each gate runs
on every PR and blocks merge on failure", which was not true of #4 for
months, and the sentence is part of why nobody checked.

## Enforcement status

Audited end-to-end on 2026-08-08 by injecting a violation into each gate and
confirming it fired. Anything not listed as **blocking** is advisory today.

| Gate | Runs on PR | Blocking | Notes |
|---|---|---|---|
| #1 CSP | yes | yes | desktop bundle only — the served bundle's CSP is unchecked |
| #2 slog privacy | yes | yes | widened 2026-08-08 from `slog.` to any receiver |
| #3 test-only symbols | yes | yes | |
| #4 CSS tokens | yes | **no** — `continue-on-error: true` | 6 real violations on main; see below |
| #5 single persistence file | yes | yes | rewritten 2026-08-08 — it could not fail before |
| bundle-size | yes | **no** — `continue-on-error: true` | desktop bundle is over budget; see #279 |
| serve-dispatch drift | yes | no, by design | informational; `SERVE_DRIFT_GATE=1` to gate |
| cred-bytes hygiene | **no** | n/a | documented in CONTRIBUTING.md, wired to nothing |
| manifest version bump | **no** | n/a | script works, no workflow invokes it |

### Gates that could not fail before 2026-08-08

Recorded so the shape stays recognisable:

- **#4 CSS tokens** — resolved `src` against `process.cwd()`, so under pr.yml
  (cwd = repo root) it threw ENOENT on every run, swallowed by
  `continue-on-error: true`. Never inspected a line of CSS in CI.
- **#5 single persistence file** — read `core/rpc/settings.go`, which has not
  existed since the settings view moved; took the "not found — skipping"
  branch. And its count used `sort -u` on a single fixed literal, so
  `count > 1` was arithmetically unreachable either way.
- **Six shell gates** (emitter/wailsjs/test-only-symbols/no-credential-in-UI/
  slog/single-persistence) reported "clean" when invoked from any directory
  other than the repo root, because their relative scan paths resolved to
  nothing. Now anchored via `scripts/ci/lib/ci-gate.sh`.
- **binding-names** matched only the receiver spelling `b`; renaming the
  receiver reduced the method set to zero and a zero-method loop passed.
- **no-fleet-imports** passed when `go list` failed, because the empty
  package list walked past the loop to "clean".
- **codegen (wailsjs half)** downgraded to a WARN and passed when its hash
  baseline file was deleted.
- **release-integrity** printed "✅ Every SemVer tag reconciles" and exited 0
  when the `gh` API call failed and returned zero tags.

`scripts/ci/gates_can_fail_test.go` is the regression test for this class. It
runs in CI as the "gate meta-tests" step in pr.yml's test-go job
(`go test ./scripts/... -count=1`, `pr.yml:389`), separated from the `-race`
suite because it shells out to mutate a scratch tree. (This sentence was
stale until entry-points-and-crash-reporting-01PMZD13 UNIT-1: the meta-tests
step was added in v0.63.2, but this doc still described the pre-v0.63.2
state. pr.yml's test-go step now scopes to `./core/... ./cmd/... .`, not
`./core/... ./cmd/harness-vm/...` — see UNIT-1 for why.)

## #1 — Strict CSP (no CDNs, no outbound traffic)

- **Production CSP**: `default-src 'none'; connect-src 'none'; script-src 'self' 'sha256-S9VfhoaWcxszZps4jluBpniHVTyGsrOIZlLNj5x7ekE='; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'; object-src 'none'`. The `script-src` hash-source allowlists exactly one inline script — the "read `?theme=` before first paint" snippet duplicated in `index.html`/`served.html` (P0 theme fix) — rather than weakening to `'unsafe-inline'`.
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
- **Non-blocking today, with a real number behind it.** Measured on main at
  `a7f3e87` once the gate was pointed at `frontend/src`: **6 violations, all
  in `frontend/src/styles/fonts.css`**, all of the form
  `@font-face { font-family: 'Geist' }`. Zero colour-literal violations —
  no hex, `rgb()`, `hsl()` or `oklch()` anywhere outside `tokens.css`.
- The first run of the repaired gate reported 41, not 6. The other 35 were
  false positives from a backtracking bug in the font-family pattern:
  `/font-family\s*:\s*(?!var\()/i` re-tests its own negative lookahead after
  giving back the whitespace it consumed, so every `font-family: var(--x)`
  and every `font-family: inherit` matched. Fixed in the same change; do not
  reintroduce a backtrackable `\s*` in front of that lookahead.
- **Before this becomes required**, one of:
  1. allowlist `frontend/src/styles/fonts.css` alongside `tokens.css` — it is
     the same kind of file, and `@font-face` cannot name a family via
     `var()`, so those six lines are unfixable by construction; or
  2. teach the rule to skip `font-family` inside an `@font-face` block.
  Either is a one-line change. Do it in its own PR, re-measure, and drop
  `continue-on-error` from the css-tokens step in the same change.

## #5 — Single-file persistence with schema versioning

- All UI state lives in `$USER_CONFIG_DIR/kenaz-harness/settings.json` with a top-level `schemaVersion: 1` integer.
- **CI gate**: `bash scripts/ci/check-single-persistence-file.sh` (asserts a single canonical persistence filename in `core/rpc/views/settings/impl.go` — **not** `core/rpc/settings.go`, which no longer exists; pointing at the old path is what made this gate a no-op).
- The Go-side test (when WP13 lands) covers schemaVersion migrations + corruption recovery.

## Adjacent guardrails

These are not part of the five Kenaz invariants but ride alongside:

- **wailsjs isolation**: only `frontend/src/lib/harnessClient.ts` may import from `wailsjs/*`. Enforced by ESLint `no-restricted-imports` AND `bash scripts/ci/check-wailsjs-isolation.sh`. (FR-007 / C-001 / plan §4.1)
- **emitter isolation**: only `core/rpc/emitter.go` and `core/rpc/stream_broker.go` may call `runtime.EventsEmit`. Enforced by `bash scripts/ci/check-emitter-isolation.sh`. (Plan §4.2 / WP11)
- **binding-name discipline**: Wails-reflected `Bindings` methods use `<View>_<Operation>` with `_` reserved as separator. Enforced by `bash scripts/ci/check-binding-names.sh`. (Plan §8 R-6)
- **no-credential-in-UI**: TS interfaces ending in `Reference|Credential|Secret` MUST NOT declare `value`/`secret`/`password`/`apiKey`/`token` fields. Enforced by ESLint custom rule + `bash scripts/ci/check-no-credential-in-ui.sh`. (FR-020 / C-004)
- **bundle-size budget**: gzipped JS+CSS under `dist/assets` ≤ 1.5 MB. Enforced by `node scripts/ci/check-bundle-size.mjs`. (NFR-006)
