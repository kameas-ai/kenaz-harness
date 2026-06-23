# Session handoff — 2026-04-29

## Stopping point

`main` is clean and green: 2032 Go tests pass under `-race -count=1 -short` across 138 packages; full `go build ./...` succeeds. No uncommitted changes.

```
6564f8c chore(wailsjs): regen for transport package move + merged catalog
129fac0 feat(mcp/recipes): WP06 curated registry + extended catalog
3ffa215 WP01: extract core/mcp/transport/ from core/mcp/stdio/
7149c04 feat(mcp/recipes): WP05 user recipe loader + merged catalog
e1cff3a feat: ship save-artifact-builtin mission (save-artifact-builtin-01KQCZ4C)
d2aa7f5 spec: flesh out save-artifact-builtin mission
```

## Shipped this session

### Mission: `save-artifact-builtin-01KQCZ4C` — COMPLETE

Third in-binary tool (`kenaz__save_artifact`) sitting alongside `kenaz__web_search` and `kenaz__bash`. Models can save deliverables straight to the Artifacts tab in one call: `(title, content[, mime_type])` → `artifact_id`. No MCP filesystem recipe required, default ON.

- `core/tools/saveartifact/` — new package; `toolloop.BuiltinTool` impl with full validation matrix (oversize, invalid args, no-session, capture failure, disabled-short-circuit).
- `core/toolloop/context.go` — session-id context plumbing (`WithSessionID` / `SessionIDFromContext`) so future built-ins can pull session id without growing the `MCPPool.Call` signature.
- `core/rpc/views/agentgraph/chat/kernel_tool_adapter.go` — stuffs session id into ctx just before `pool.Call`; tools that don't read it pay nothing.
- Settings: `SaveArtifactDisabled` (inverted, default ON, mirrors `AutoCaptureCodeBlocksDisabled`); full `Load*`/`Save*` chain on both `FileStore` and `memoryStore`; `Settings_Get/SetSaveArtifactEnabled` bindings; `harnessClient.ts` typed methods.
- Frontend: third toggle row in `KenazToolsPanel.vue` (default ON state).
- Spec at `kitty-specs/save-artifact-builtin-01KQCZ4C/spec.md` (13 FRs, 3 NFRs, 6 constraints, 4 locked Qs).

### Mission: `mcp-server-install-01KQ8TDP` — PARTIAL (3 of 10 WPs)

| WP | Title | Status | Commit |
|---|---|---|---|
| WP01 | Transport refactor (`core/mcp/stdio/` → `core/mcp/transport/`) | ✅ on main | `3ffa215` |
| WP02 | Stdio re-host under new `transport.Connection` | ⛔ in-flight when stopped | worktree-agent-a9b815dd (uncommitted) |
| WP03 | HTTP transport implementation | ❌ not started | — |
| WP04 | SSE transport implementation | ❌ not started | — |
| WP05 | User recipe loader (`UserStore` + `MergedCatalog`) | ✅ on main | `7149c04` |
| WP06 | Curated registry + 10 extended recipes | ✅ on main | `129fac0` |
| WP07 | Test Connection RPC | ❌ blocked on WP02 + (WP03 or WP04) | — |
| WP08 | Clipboard import translator | ❌ stopped at start | — |
| WP09 | Frontend "Add MCP Server" modal | ❌ blocked on WP05/06/07/08 | — |
| WP10 | Cedar gate + audit + migration | ❌ blocked on WP05 + WP09 | — |

WP01's report flagged two follow-ups:
1. **No pre-refactor benchmark baseline** — WP01 added a new microbench suite (`framer_bench_test.go`: Framer.Write 595 ns/op, Framer.Read 4029 ns/op, RingBuffer.Write 6.9 ns/op, Router 71.7 ns/op) as the WP02 reference point; original "delta < 5%" acceptance criterion is N/A.
2. **`MapLogLevel` exported** — minor surface widening; document if a future cleanup wants to re-hide it behind `transport/internal/...`.

WP06's report flagged one follow-up:
- Registry recipes for `sqlite` and `git` use new substitution tokens (`${DB_PATH}`, `${REPO_PATH}`) that aren't yet in `core/mcp/recipes/substitution.go`. `Recipe.Validate()` passes today, but the install flow will need new substitutors before those recipes go live. WP09's install path is the natural place to wire them.

## What's pending (priority order)

### Mission queue snapshot

35 missions total in `kitty-specs/`. 2 are now fully shipped (compaction-strategy-ui from prior session, save-artifact-builtin from this session). 33 remain spec-only. The mcp-server-install mission is partially landed (3 of 10 WPs).

### Recommended next steps (when resuming)

**Continue mcp-server-install** (largest blast radius — unlocks user-installable MCP servers, the #1 missing capability for an alpha-quality harness):

1. **Resume WP02** (stdio re-host) — the worktree at `.claude/worktrees/agent-a9b815dd` has uncommitted progress. Either pick up where it left off or start fresh; either way, this is the next critical path step.
2. **Launch WP03 (HTTP) + WP04 (SSE) in parallel** once WP02 lands. Both depend only on WP01 + WP02's pool shape. They live in non-overlapping sub-packages (`transport/http/`, `transport/sse/`) so worktree-isolated agents won't conflict.
3. **WP07 (Test Connection RPC)** unblocks once WP02 + (WP03 or WP04) are in.
4. **WP08 (clipboard import translator)** can run in parallel with WP07 (deps WP05 + WP06 only — already met). Note: HTTP/SSE entries from `claude_desktop_config.json` will need WP03/WP04's `Recipe.URL/HeadersTemplate/PostURL` fields; until those land, WP08 marks them "unsupported" with a clear reason.
5. **WP09 (frontend Add MCP Server modal)** is the integration WP — depends on all of WP05/WP06/WP07/WP08.
6. **WP10 (Cedar gate + audit + migration)** is the polish WP — depends on WP05 + WP09.

**Other high-impact missions ready for implementation** (all spec+plan+tasks already drafted):

| Mission | Priority signal |
|---|---|
| `cedar-credential-policy-01KQ8TDE` | Universal interactive permission system (4 resource families: Credential, BashCommand, FilesystemOp, Tool). Replaces hardcoded bash allowlist. Big UX win. |
| `harness-self-mcp-onboarding-01KQ8TDU` | First-launch onboarding via in-process MCP server + two-phase FSM. Bridges fresh-install gap with a real conversational flow (paired with save-artifact already on main, this becomes Day-1-usable). |
| `cross-session-search-01KQ8TDQ` | FTS5 + Cmd+F modal. Pure-additive; small surface; unlocks much better long-session navigation. |
| `keyboard-shortcuts-settings-01KQ8TDR` | Settings panel for shortcut overrides + cheat-sheet overlay. |
| `session-auto-titling-01KQ8TDS` | One-shot auto-title after first user-assistant turn. Cheap, high-frequency UX win. |
| `markdown-rendering-polish-01KQ8TDT` | KaTeX + Mermaid + code-block actions. Visible polish across every chat turn. |

## Worktrees still locked

```
.claude/worktrees/agent-a474d16b  # WP01 (work landed on main as 3ffa215; can be removed)
.claude/worktrees/agent-a1218b8a  # WP05 (work landed on main as 7149c04; can be removed)
.claude/worktrees/agent-a28ad555  # WP06 (work landed on main as 129fac0; can be removed)
.claude/worktrees/agent-a9b815dd  # WP02 (in-flight stopped — preserve until resumed)
```

To clean up the three completed worktrees:

```
git worktree remove --force .claude/worktrees/agent-a474d16b
git worktree remove --force .claude/worktrees/agent-a1218b8a
git worktree remove --force .claude/worktrees/agent-a28ad555
```

(`--force` is required because the harness still holds a lock; the agent processes have terminated.)

## Verification commands when resuming

```
go build ./...
go test -race -count=1 -short ./core/...
cd frontend && npm run typecheck && npx vitest run
```

Last green run: 2032 Go tests across 138 packages; vitest passing for all touched components; typecheck clean for save-artifact-related files (pre-existing TS errors elsewhere are unrelated and were not introduced by this session).
