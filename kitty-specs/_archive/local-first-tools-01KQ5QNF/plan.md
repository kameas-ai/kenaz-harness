# Implementation Plan: Local-first tools

**Branch**: `local-first-tools-01KQ5QNF` (lane allocated at WP-implement time)
**Date**: 2026-04-26
**Spec**: `kitty-specs/local-first-tools-01KQ5QNF/spec.md`

## Summary

Two built-in Kaneaz Tools: web search and bash. Both ship as in-binary Go code, no npx, no MCP server, no cloud service we control. Web search aggregates public search engines + extracts content via `go-readability`, with `<source>`-tag prompt-injection mitigation and optional SOCKS5 proxy. Bash wraps `os/exec` with an allowlist + working-dir sandbox + output cap. Both surface via the existing tool-discovery + Tools-panel toggle pattern.

## Technical Context

- **Language/Version**: Go 1.22+; TypeScript 5.x.
- **Primary Dependencies**: stdlib + `github.com/go-shiori/go-readability` (MIT). No other new third-party deps.
- **Storage**: Settings extended (BuiltinToolsEnabled, WebSearchProxy, WebSearchBackends, BashAllowlist). No new tables.
- **Testing**: Go `-race -count=1 -short`. Search-engine parsers have golden HTML fixtures. Bash tests use a temp dir as the sandbox root.
- **Performance**: NFR-006 (<5 s for 5 web results); NFR-007 (<100 ms bash overhead).
- **Constraints**: NFR-003 privacy invariant (queries don't touch harness-vendor infra); NFR-004 sandbox invariant; NFR-005 allowlist invariant.

## Charter Check

- DIRECTIVE_001 (no cyclic imports): `core/tools/websearch/` + `core/tools/bash/` depend on stdlib + `core/logging`. Nothing imports them except the rpc layer's `core/rpc/views/llm/builtin_tools.go`. Pass.
- C-001 (no third-party SDK in `core/`): `go-readability` is a content-extraction library, not a vendor SDK. Pass.
- Privacy CI invariants: no new color literals; no leaked secrets in audit (allowlist + working-dir + proxy URL audited; bash command audited; web query audited).

## Project Structure

```
core/tools/
├── builtin.go                  # NEW: registry; lists built-in tool names + handlers
├── websearch/
│   ├── websearch.go            # Tool struct + JSON schema + Call() entrypoint
│   ├── search.go               # Backend interface + SearchOpts
│   ├── duckduckgo.go           # DDG HTML parser
│   ├── wikipedia.go            # Wikipedia opensearch API
│   ├── searxng.go              # SearXNG HTTP client
│   ├── brave.go                # Brave Search API (optional, when keyed)
│   ├── aggregator.go           # Multi-backend fan-out + dedupe
│   ├── fetch.go                # net/http with proxy support
│   ├── extract.go              # go-readability wrapper
│   ├── inject.go               # <source>-tag wrapper + system-prompt addition
│   ├── testdata/
│   │   ├── ddg_html_sample.html
│   │   ├── wikipedia_sample.json
│   │   └── readable_page.html
│   └── *_test.go
├── bash/
│   ├── bash.go                 # Tool struct + JSON schema + Call() entrypoint
│   ├── exec.go                 # os/exec wrapper
│   ├── allowlist.go            # Default list + matcher
│   ├── parser.go               # shlex parser
│   └── *_test.go

core/rpc/views/llm/
├── builtin_tools.go            # NEW: discoverer adapter merging built-ins + MCP pool
├── builtin_tools_test.go
├── impl.go                     # MODIFIED: dispatch branches on `kaneaz__` prefix
└── impl_test.go                # MODIFIED: cover builtin dispatch path

core/rpc/views/settings/
├── api.go                      # MODIFIED: BuiltinToolsEnabled, WebSearchProxy, etc.
└── impl.go

core/rpc/api.go                 # MODIFIED: wire built-in tools into the discoverer + dispatcher
core/rpc/bindings.go            # MODIFIED: Tools_ToggleBuiltin, Tools_BuiltinConfig

frontend/src/views/tools/
├── KaneazToolsPanel.vue        # MODIFIED: Web Search + Bash rows below Memory
├── BashAllowlistEditor.vue     # NEW
└── __tests__/

frontend/src/lib/types.ts       # MODIFIED: BuiltinTool, BuiltinToolConfig
frontend/src/lib/harnessClient.ts # MODIFIED: tools.builtin namespace

docs/local-tools.md             # NEW
```

## Phase 0 — Research summary

- **DuckDuckGo HTML**: `https://html.duckduckgo.com/html/?q=<query>` returns server-rendered HTML. Parser walks `.result` nodes; each has `.result__title a` (href + text) and `.result__snippet`. ToS allows reasonable use; rate-limit is the de-facto enforcement.
- **Wikipedia opensearch**: `https://en.wikipedia.org/w/api.php?action=opensearch&search=<q>&limit=5&format=json` returns `[query, [titles], [snippets], [urls]]`. Stable, free, no key.
- **SearXNG JSON API**: `https://<instance>/search?q=<q>&format=json`. Returns `{results: [{title, url, content}]}`. Most public instances disable JSON; user-self-hosted enables it.
- **Brave Search API**: `https://api.search.brave.com/res/v1/web/search?q=<q>` with `X-Subscription-Token: <key>`. Free tier 2000/mo. Same as the existing Brave MCP recipe but called directly here for users who'd rather have it built-in.
- **`go-readability`**: Mozilla Readability port. Best for static / server-rendered pages. SPA-heavy sites yield poor results.
- **shlex in Go**: `github.com/google/shlex` exists but is third-party. Stdlib alternative: hand-rolled split that handles quoted strings + escape sequences. ~80 LOC. Avoids the dep.
- **Bash safety**: argv[0] allowlist + no shell expansion + working-dir restriction + timeout is the same pattern as other agentic harnesses (Claude Code's bash tool, OpenAI's Code Interpreter sandbox).

## Phase 1 — Web search backend interfaces + DDG/Wikipedia parsers

**Targets**: `core/tools/websearch/{search.go, duckduckgo.go, wikipedia.go, testdata/}`.

- `Backend` interface + `SearchHit` struct.
- DDG parser: HTML scrape of `.result` nodes via `golang.org/x/net/html`. Golden fixture in testdata.
- Wikipedia opensearch: trivial JSON shape.
- Tests: parser correctness against golden fixtures, malformed-HTML graceful degrade, empty-result handling, rate-limit (429) → return wrapped error.

**Dependencies**: none.

## Phase 2 — Aggregator + fetch + extract + inject

**Targets**: `core/tools/websearch/{aggregator.go, fetch.go, extract.go, inject.go}`.

- `Aggregator{backends []Backend}` with `Search(ctx, query, opts)` running backends in parallel via errgroup; deduping by URL (case-insensitive host + path); top-K interleave.
- `Fetcher{client *http.Client, proxyURL string}` — `http.Transport` with `Proxy: http.ProxyURL(proxy)`. Per-fetch timeout 8 s; redirect cap 5; UA header.
- `Extractor` wraps `go-readability`. Cap text to 10 KiB.
- `Injector` wraps each result's text in `<source url="..." title="...">{text}</source>` and emits the system-prompt addition string.

**Tests**: dedupe correctness; proxy round-trip via `httptest.Server` proxy; fetch failure (timeout) → result skipped, not whole tool failed; extractor on golden HTML; inject formatting.

**Dependencies**: Phase 1.

## Phase 3 — Web search Tool + JSON schema

**Targets**: `core/tools/websearch/websearch.go`.

- `Tool struct{...}` implementing the Kaneaz Tool contract (Name, Description, InputSchema, Call).
- `Call(ctx, argsJSON) (resultJSON, error)`:
  1. Parse args.
  2. Filter aggregator output by `allowed_domains` / `blocked_domains`.
  3. Fetch + extract + inject in parallel.
  4. Return `{results: [...], system_prompt_addition: "..."}` so the dispatcher can prepend the system-prompt to the next assistant turn (or keep it as a hint for the toolloop).

**Tests**: end-to-end with stub backends + httptest fetcher.

**Dependencies**: Phase 2.

## Phase 4 — Bash sandbox + allowlist + parser

**Targets**: `core/tools/bash/{bash.go, exec.go, allowlist.go, parser.go}`.

- `parser.go` — shlex-style splitter (hand-rolled; no third-party). Handles single + double quotes, escape sequences, no shell expansion.
- `allowlist.go` — `DefaultAllowlist []string` per FR-012. `Matches(allowlist, name)` exact-match.
- `exec.go` — `Run(ctx, argv, cwd, timeout) (stdout, stderr []byte, exitCode int, truncated bool)`. Uses `exec.CommandContext` + 64 KiB ring buffers.
- `bash.go` — Tool struct + `Call(ctx, argsJSON)`:
  1. Parse `command` via shlex. argv[0] must be in allowlist.
  2. Resolve `working_dir` (relative joined to `<DataDir>/agent-workspace`; absolute must be a child of workspace per `path_validation.go`).
  3. Execute via `exec.Run`. Return result.

**Tests**: parser edge cases (quotes, escapes, no shell metacharacters); allowlist exact match; working-dir traversal rejection; timeout fires; output truncation flag; default allowlist coverage.

**Dependencies**: none (parallel with web search).

## Phase 5 — Settings + ConfigOptions

**Targets**: `core/rpc/views/settings/{api.go, impl.go}`.

- Extend `Settings`:
  - `BuiltinToolsEnabled map[string]bool` — key is tool short-name (`web_search`, `bash`); default both false.
  - `WebSearchBackends []string` — default `["duckduckgo", "wikipedia"]`.
  - `WebSearchProxy string` — default empty.
  - `WebSearchSearXNGURL string` — default empty.
  - `WebSearchBraveAPIKey` — stored in keychain via locator `builtin/web_search/brave_api_key`; the settings struct only tracks "key present" boolean.
  - `BashAllowlist []string` — default = `bash.DefaultAllowlist`.
- Tests for round-trip persistence.

**Dependencies**: Phase 1-4 (so the settings have something to point at).

## Phase 6 — Tool discoverer + dispatcher integration

**Targets**: `core/rpc/views/llm/builtin_tools.go`, `core/rpc/views/llm/impl.go`, `core/rpc/api.go`.

- `BuiltinToolDiscoverer` aggregates enabled built-in tools (websearch, bash) into `[]ToolSpec`.
- `MergedDiscoverer{builtin, mcpPool}` returns the union.
- Dispatcher branches on tool name prefix:
  - `kaneaz__*` → built-in dispatch via the relevant `Tool.Call`.
  - else → MCP pool dispatch.
- The toolloop's audit + perms + post-hook chain runs uniformly regardless of branch.

**Tests**: stub builtin tool + stub MCP pool; assert merged Tools() returns both; assert dispatch routes correctly by prefix.

**Dependencies**: Phases 3, 4, 5.

## Phase 7 — Frontend Tools-panel rows

**Targets**: `KaneazToolsPanel.vue`, `BashAllowlistEditor.vue`, `lib/types.ts`, `lib/harnessClient.ts`.

- Two new rows below Memory: "Web search" + "Bash". Same toggle UX as Memory.
- Web Search row config (editable in modal):
  - Backend multi-select (DDG, Wikipedia, SearXNG, Brave).
  - SearXNG URL input (shown when SearXNG selected).
  - Brave API key input (shown when Brave selected).
  - Proxy URL input.
- Bash row config:
  - `BashAllowlistEditor.vue`: chip-list editor; mirror `DirectoryPicker.vue` from filesystem-mcp WP03. Default-fill from `bash.DefaultAllowlist`.

Tests: panel renders rows; toggle on calls `installBuiltin('web_search')`; modal submits config.

**Dependencies**: Phase 6.

## Phase 8 — Polish + docs

**Targets**: `docs/local-tools.md`, `docs/local-tools.md` cross-link from `docs/mcp-recipes.md`.

- Walkthroughs for both tools.
- Privacy comparison table: built-in web_search vs Brave MCP recipe vs Anthropic native web_search.
- Bash safety notes + how to extend the allowlist.
- Manual A1-A10 verification checklist.

**Dependencies**: all earlier.

## Work-package breakdown (proposed)

- **WP01 — Web search backend + aggregator + fetch/extract/inject** (Phases 1, 2). Pure backend additive. Lands the search infrastructure but nothing is wired into the model yet.
- **WP02 — Bash sandbox + allowlist + parser** (Phase 4). Pure backend additive. Independent of WP01.
- **WP03 — Tool registration + dispatcher integration + settings** (Phases 3, 5, 6). Wires both into the toolloop / discoverer; settings + persistence; built-in tools become callable from the model.
- **WP04 — Frontend Tools-panel rows + config modals** (Phase 7).
- **WP05 — Polish + docs + manual checklist** (Phase 8).

DAG: (WP01 ∥ WP02) → WP03 → WP04 → WP05.

## Risk register

| Risk | Phase | Mitigation |
|---|---|---|
| DuckDuckGo HTML changes its DOM | 1 | Golden fixture test; manual update path documented in `docs/local-tools.md`. Aggregator falls back to other backends. |
| Search engine rate-limits | 2 | Aggregator catches 429; continues with remaining backends; surfaces a "rate-limited; results reduced" hint in result metadata. |
| Page is JS-rendered → poor extraction | 2 | Document the limitation; recommend SearXNG-self-host for users who want better SPA support (SearXNG has its own renderer). |
| Bash allowlist bypass via shell metacharacters | 4 | shlex parses without shell expansion; pipes / redirects fail allowlist (the metachar becomes argv[1]). Test covers `bash("echo hi | grep h")` → rejected. |
| Working-dir escape via symlinks | 4 | `filepath.EvalSymlinks` canonicalizes BEFORE the prefix check. Test plants a symlink + asserts rejection. |
| Bash output blow-up | 4 | 64 KiB ring buffer per stream; `truncated: true` flag in result. |
| `go-readability` panics on malformed HTML | 2 | Wrap in `defer recover`; surface a "extract failed" result with raw bytes (capped). |
| Proxy misconfig leaks query to default DNS | 2 | Document: SOCKS5 with DNS resolution must be explicit (`socks5h://` for remote DNS). Default `socks5://` does local DNS. |
| `<source>` wrapping defeated by clever injection | 2 | Defense in depth: domain blocklist + per-tool audit + the system-prompt addition. Not a perfect mitigation; a future "fine-tuned filter" model is the longer-term answer. |
| Built-in tool name collision with future MCP recipes | 6 | `kaneaz__` prefix is reserved for built-ins. MCP recipes can't have that server id. |

## Open questions

(Restated from spec.md §11 + new ones from planning.)

1. DDG ToS — informal, rate-limit enforced. Document.
2. Default allowlist — start with FR-012 list. Iterate.
3. Naming: `kaneaz__` vs `builtin__`. Decision: `kaneaz__`.
4. Future image-gen — slots into `core/tools/imagegen/` later.
5. **NEW — `go-readability` license**: MIT. No license-conflict concerns.
6. **NEW — Custom shlex vs third-party**: hand-rolled to avoid the dep. ~80 LOC. Tests exhaustive.
