# Dependency security audit — v0.17.0 prep

**Date**: 2026-05-15
**Scope**: frontend npm + Go modules
**Auditor**: automated scan (npm audit v2 + manual CVE cross-reference; govulncheck not installed)

---

## Summary

- **Frontend**: 1 critical / 0 high / 6 moderate / 0 low advisories (7 total across 5 distinct packages — note: npm counts chain entries, so 7 advisory references map to 5 root packages)
- **Go**: 2 known-vulnerable / 2 at-risk-but-not-yet-patched (golang.org/x/crypto v0.50.0, golang.org/x/net v0.53.0 are the primary concerns)
- **Supply-chain hygiene**: largely clean — 1 commented-out local replace directive in go.mod, no git+URL npm deps, all packages from mainstream registries

---

## Frontend (npm audit)

Versions installed are from `package-lock.json`. The root `node_modules/vite` (6.4.2) and `node_modules/esbuild` (0.25.12) are **not** vulnerable; the flagged entries are older copies nested under `node_modules/vitest/node_modules/` and `node_modules/vite-node/node_modules/` pinned by vitest@2.1.9's transitive closure.

### Critical

| Package | Installed | Advisory | Description | Fix |
|---|---|---|---|---|
| `happy-dom` | 15.11.7 | [GHSA-37j7-fg3j-429f](https://github.com/advisories/GHSA-37j7-fg3j-429f) | VM context escape → Remote Code Execution (CWE-94). Attacker-controlled HTML can break out of the VM sandbox used by the test runner. | Bump to `^20.9.0` in `package.json` (major bump; check vitest compat). |

### Moderate

| Package | Installed | Advisory | Description | Fix |
|---|---|---|---|---|
| `happy-dom` | 15.11.7 | [GHSA-w4gp-fjgq-3q4g](https://github.com/advisories/GHSA-w4gp-fjgq-3q4g) | Fetch credentials use page-origin cookies instead of target-origin (CWE-201, CVSS 7.5). Informational leakage in test-runner fetch mocks. | Same fix as above (>=20.8.9). |
| `happy-dom` | 15.11.7 | [GHSA-6q6h-j7hj-3r64](https://github.com/advisories/GHSA-6q6h-j7hj-3r64) | ECMAScriptModuleCompiler: unsanitized export names interpolated as executable code (CWE-94, CVSS 8.8). | Same fix (>=20.8.9, note: this is the HIGH sub-advisory within the critical root). |
| `mermaid` | 11.14.0 | [GHSA-6m6c-36f7-fhxh](https://github.com/advisories/GHSA-6m6c-36f7-fhxh) | Gantt chart input causes infinite loop DoS (CWE-835). Affects rendered diagrams in the UI. | Upgrade to `>=11.15.0` (non-major). |
| `mermaid` | 11.14.0 | [GHSA-xcj9-5m2h-648r](https://github.com/advisories/GHSA-xcj9-5m2h-648r) | `classDefs` in diagrams not sanitized → CSS injection (CWE-94). | Upgrade to `>=11.15.0`. |
| `mermaid` | 11.14.0 | [GHSA-87f9-hvmw-gh4p](https://github.com/advisories/GHSA-87f9-hvmw-gh4p) | Configuration not sanitized → CSS injection (CWE-94). | Upgrade to `>=11.15.0`. |
| `mermaid` | 11.14.0 | [GHSA-ghcm-xqfw-q4vr](https://github.com/advisories/GHSA-ghcm-xqfw-q4vr) | `classDef` in state diagrams → HTML injection (CWE-94). | Upgrade to `>=11.15.0`. |
| `esbuild` (nested under vitest/vite-node) | 0.21.5 | [GHSA-67mh-4wv8-2f99](https://github.com/advisories/GHSA-67mh-4wv8-2f99) | Dev server accepts cross-origin requests (CWE-346, CVSS 5.3). Only exploitable when `vite dev` / vitest watch is running; not a prod build issue. | Bump `vitest` to `^3` or `^4` (major) to pull in fixed vite-node; or add `overrides.esbuild ">=0.25.0"` to `package.json`. |
| `vite` (nested under vitest/vite-node) | 5.4.21 | [GHSA-4w7w-66w2-5vf9](https://github.com/advisories/GHSA-4w7w-66w2-5vf9) | Path traversal in optimized deps `.map` handling (CWE-22, CWE-200). Only in dev server context. | Same as esbuild fix above — upgrading vitest pulls a fixed vite-node. |
| `@vitest/mocker` (nested under vitest) | 2.1.9 | (indirect via vite chain) | Cascading advisory from vite<6.4.2 in the nested tree. | Upgrade vitest to >=3. |
| `vite-node` (nested under vitest) | 2.1.9 | (indirect via vite chain) | Same cascade. | Upgrade vitest to >=3. |

**Context on the nested vitest chain**: `vitest@2.1.9` brings its own copies of `vite@5.4.21`, `vite-node@2.1.9`, `esbuild@0.21.5`, and `@vitest/mocker@2.1.9` that are older than the hoisted `vite@6.4.2` / `esbuild@0.25.12`. All of these advisories are **dev-only** (test runner) and are **not reachable in production builds**. The critical `happy-dom` advisory and the mermaid CSS injection advisories carry more production-surface risk.

---

## Go modules

`govulncheck` was not found on this machine; cross-reference below is based on known published CVEs and the Go vulnerability database entries as of the audit date. **Flag**: this audit cannot substitute for a govulncheck run — install it (`go install golang.org/x/vuln/cmd/govulncheck@latest`) and run `govulncheck ./...` before release.

| Package | Version | Status | Known issue | Recommended action |
|---|---|---|---|---|
| `golang.org/x/crypto` | v0.50.0 | **Vulnerable** | [GO-2024-3321](https://pkg.go.dev/vuln/GO-2024-3321) — `crypto/ssh`: Mishandled handshake-sequence results in HostKey check bypass (fixed in v0.31.0). Also [GO-2025-3447] trivial auth bypass in ssh server fixed in v0.35.0. | Bump to `>=v0.35.0` (run `go get golang.org/x/crypto@latest`). |
| `golang.org/x/net` | v0.53.0 | **Vulnerable** | [GO-2025-3499](https://pkg.go.dev/vuln/GO-2025-3499) — `net/http` CONNECT proxy tunnel host header bypass (fixed in v0.36.0). | Bump to `>=v0.36.0`. |
| `golang.org/x/sys` | v0.43.0 | At risk | No confirmed CVE known at this version, but v0.43 is behind the current stable and is a transitive dep of wails/gopsutil. Low exposure — OS syscall wrappers only. | Bump opportunistically with crypto/net. |
| `gopkg.in/yaml.v3` | v3.0.1 | OK | The memory-exhaustion CVE (CVE-2022-28948) was fixed in v3.0.0. v3.0.1 is patched. No open known issues. | No action required. |
| `modernc.org/sqlite` | v1.50.0 | At risk (low) | No CVE filed against this pure-Go SQLite port itself; the underlying SQLite C amalgamation (3.47.x era at 1.50.0) carries upstream SQLite advisories. Most are exploitable only via attacker-controlled SQL. Exposure depends on whether user data reaches raw SQL. | Run `go get modernc.org/sqlite@latest` to stay current with amalgamation bumps. |
| `github.com/aws/aws-sdk-go-v2` | v1.41.6 | OK | AWS SDK v2 (not v1) — v1 deprecation CVEs do not apply. v1.41.6 is recent. No known high-severity issues. | No action required; monitor AWS SDK releases. |
| `github.com/getsentry/sentry-go` | v0.46.2 | OK | No known CVEs. Recent release. | No action required. |
| `github.com/wailsapp/wails/v2` | v2.12.0 | At risk (low) | No active CVE. Wails embeds Webview2 (Windows) and WebKit (macOS/Linux); OS-level WebView patches are independent. Go-side surface is small. | Keep updated; watch upstream for advisories. |
| `github.com/labstack/echo/v4` | v4.13.3 | OK | No known active CVEs against v4.13.3. Previous path-traversal issues were fixed in v4.10.0. | No action required. |
| `github.com/jung-kurt/gofpdf/v2` | v2.17.3 | At risk (low) | This package is **archived / no longer maintained upstream**. No active CVE filed, but no future patches will be issued. Any future PDF parsing vulnerability would not receive an upstream fix. | Evaluate migration to an actively maintained PDF library (e.g. `github.com/go-pdf/fpdf` or `github.com/pdfcpu/pdfcpu`) before v1.0. |
| `github.com/cedar-policy/cedar-go` | v1.6.0 | OK | Library is under active development; no CVEs filed. Policy evaluation is sandboxed. | No action required; monitor for 2.x releases. |
| `cloud.google.com/go/compute/metadata` | v0.9.0 | OK | Metadata-only client; no JWT validation in this package. No known issues. | No action required. |
| `google.golang.org/grpc` | v1.80.0 | OK | Recent release; no active CVEs. | No action required. |
| `golang.org/x/exp` | v0.0.0-20220921023135 | At risk (low) | This is a 2022 snapshot, pre-dating many upstream changes. The package is experimental and not security-critical in this repo's usage, but very stale. | Bump to a recent commit or remove if no longer needed. |

---

## Pinning + hygiene notes

### npm version specifiers (package.json)

All production and dev dependencies use `^` (caret) ranges. This means npm resolves the highest compatible minor/patch within the declared major. Notable implications:

- `"happy-dom": "^15.11.7"` — the `^` allows any 15.x patch, but the fix requires v20.x (major). **The caret does not protect against this vulnerability; an explicit bump to `"^20.9.0"` is required.**
- `"mermaid": "^11.4.1"` — allows 11.x updates. The fixes land in 11.15.x+, so `npm update mermaid` within the current constraint will pick them up once published and semver-compatible.
- `"vitest": "^2.1.8"` — fix requires vitest 3.x or 4.x (major), so the caret constraint blocks automatic remediation. A conscious major bump is needed.
- `"vite": "^6.0.7"` in devDependencies — top-level vite is already at 6.4.2 (patched). The nested vite under vitest's own closure cannot be overridden by this pin; it requires upgrading vitest itself.
- No `~` (tilde) specifiers found. No exact-pinned (`"1.2.3"` without prefix) prod deps.

### Supply-chain hygiene

| Check | Result |
|---|---|
| git+URL deps in package.json / package-lock.json | None found |
| Non-npmjs.org resolved URLs in package-lock.json | None found |
| `replace` directives in go.mod | 1 directive present but **commented out**: `// replace github.com/wailsapp/wails/v2 v2.12.0 => /Users/alecfeeman/go/pkg/mod`. This is a local dev leftover — safe, but should be removed before release to avoid confusion. |
| Non-mainstream Go module proxies | None — all modules resolve through the standard proxy.golang.org chain |
| `golang.org/x/exp` staleness | Pinned to a 2022 snapshot (v0.0.0-20220921023135). Indirect dep via wails; unusual to have such a stale experimental snapshot. |

---

## Recommended actions (prioritized)

1. **[Critical — do before v0.17.0 ships]** Bump `happy-dom` to `^20.9.0` in `frontend/package.json`. This is the VM escape / RCE advisory and affects the test runner. Though tests don't run in production, the sandbox escape risk is severe enough that it should not remain in the dependency tree. Verify vitest compat with happy-dom 20.x before merging.

2. **[High — do before v0.17.0 ships]** Bump `golang.org/x/crypto` to `>=v0.35.0` and `golang.org/x/net` to `>=v0.36.0` in `go.mod`. These are confirmed published CVEs in the Go vulnerability database. Run `go get golang.org/x/crypto@latest golang.org/x/net@latest && go mod tidy`. The crypto SSH HostKey bypass and net/http CONNECT bypass are the two highest-confidence Go findings here.

3. **[Moderate — do before v0.17.0 ships]** Upgrade `mermaid` to `>=11.15.0`. The CSS/HTML injection advisories affect diagram rendering in the UI (production surface). This is a non-major bump and should be low risk.

4. **[Moderate — schedule for v0.17.x patch lane]** Upgrade `vitest` from `^2.1.8` to `^3.x` (or `^4.x`) to eliminate the nested esbuild/vite/vite-node GHSA chain. These are dev-only (not reachable in production builds), but the esbuild dev-server cross-origin request issue is exploitable during local development. Given that vitest 3→4 is a major bump, target the patch lane rather than blocking v0.17.0.

5. **[Low — clean up]** Remove the commented-out `replace` directive for wails/v2 from `go.mod`. It references a local absolute path (`/Users/alecfeeman/...`) which has no effect while commented but would be confusing to other contributors and CI environments.

6. **[Low — monitor]** `gofpdf/v2` is archived with no future security patches. Plan migration to an actively maintained PDF library as part of the v0.18.x–v0.19.x cycle before a vulnerability appears with no upstream fix path.

7. **[Low — opportunistic]** Bump `golang.org/x/sys`, `golang.org/x/exp`, and `modernc.org/sqlite` while doing the crypto/net bumps (they often update together via `go mod tidy`).

8. **[Tooling gap]** Install `govulncheck` in CI (`go install golang.org/x/vuln/cmd/govulncheck@latest`) and add a step to `pr.yml` that runs `govulncheck ./...`. The current audit is based on memory-resident CVE knowledge; automated scanning will catch regressions continuously. This is the highest-leverage process improvement available.
