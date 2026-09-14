// hooks_mcp_invoker.go bridges the harness's production MCP
// tool-dispatch pool (coremcp.Pool — concretely the process-singleton
// *dispatch.Pool newLLMStack constructs) onto hooks.MCPInvoker, the
// seam core/hooks.Runner uses to dispatch kind=mcp lifecycle hooks.
//
// Finding #71: hooks.Config.MCP was never set at the only production
// construction site (newHooksStack), so r.mcp was always nil and every
// kind=mcp hook — which registers cleanly (Hook.Validate has no
// opinion on kind=mcp beyond "mcp_tool required") — failed on every
// dispatch with "mcp invoker not configured (v1 stub)". This file
// closes that gap.
package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
)

// mcpHookInvokerAdapter implements hooks.MCPInvoker over a coremcp.Pool.
//
// Construction ordering forces a backfill: hooks.Runner (and the
// hooks.Config.MCP it is built with) is constructed by newHooksStack
// early in api.New, but the *dispatch.Pool that backs every other MCP
// call path (mcp_call workflow steps, the chat toolloop,
// context-bootstrap) is not constructed until newLLMStack runs
// afterward (api.go's newLLMStack, mcpPool/dispatchPool ~line 5476) —
// newLLMStack itself takes the hooks runner adapter as a parameter, so
// the dependency cannot run the other way around without a much larger
// restructuring. Rather than reorder pool construction, this adapter
// is constructed empty and handed to newHooksStack; once newLLMStack
// returns, api.New calls setPool with the real pool. This mirrors the
// backfill api.New already performs for
// a.permissionHookAdapter.Runner = a.hookRunner immediately after the
// same newHooksStack call (see that assignment's comment) — both exist
// because a dependency the hooks runner needs is only available later
// in the same constructor. There is no window in production where a
// hook can dispatch before the backfill: api.New is fully synchronous
// and the API is not reachable by any caller (RPC or Wails binding)
// until it returns.
type mcpHookInvokerAdapter struct {
	mu   sync.RWMutex
	pool coremcp.Pool
}

// setPool backfills the live pool once newLLMStack has constructed it.
// Called exactly once, from api.New's construction goroutine, before
// the API is reachable by anything else — see the type doc for why no
// lock would be strictly required, but one is kept anyway (cheap, and
// it makes the type's own concurrency contract self-evident rather
// than depending on a caller-side ordering guarantee holding forever).
func (a *mcpHookInvokerAdapter) setPool(pool coremcp.Pool) {
	a.mu.Lock()
	a.pool = pool
	a.mu.Unlock()
}

// InvokeTool implements hooks.MCPInvoker. tool must be the namespaced
// "<server>__<tool>" identifier — the same convention
// core/rpc/views/llm.ToolNameSeparator uses to fold an MCP (server,
// tool) pair into the single string a corellm.ToolSpec.Name (and, here,
// a persisted Hook.MCPTool) carries. splitToolName (wf_adapters.go,
// same package) is the canonical splitter already used by the
// workflow and slash-command tool dispatchers; reused here rather than
// duplicated so there is exactly one namespacing implementation. A
// tool value with no "__" separator falls back to server="" (matching
// splitToolName's existing contract), which coremcp.Pool.Call rejects
// with an explicit "unknown server" error — a visible dispatch
// failure, not a silent no-op.
//
// Shutdown safety (finding #61 / #71 interaction — see
// core/hooks/fire.go's asyncShutdownDrainTimeout doc and
// core/core.go's Shutdown): a post_send or FireAsync kind=mcp
// dispatch runs on hooks.Runner's detached async worker pool.
// hooks.Runner.Shutdown abandons any dispatch still running past its
// 3s drain deadline — the goroutine is not killed, only stopped being
// waited on — so InvokeTool can still be executing pool.Call after
// api.Shutdown() returns. main.go's OnShutdown (and the served-mode
// equivalents) call api.Shutdown() and THEN core.Core.Shutdown(ctx),
// which closes c.MCP — and c.MCP is set, via c.SetMCP(a.dispatchPool)
// in api.New, to this EXACT *dispatch.Pool. So an abandoned kind=mcp
// dispatch really can race a live Close on the same pool.
//
// This is safe, not merely unguarded, because the pool stack was
// already built to tolerate it: dispatch.Pool.Call (core/mcp/dispatch/
// pool.go) snapshots the sub-pool reference under a read-lock and does
// not hold any lock across the call itself, so a racing Call either
// (a) loses the ownership-map race and returns "unknown server" once
// Close has cleared d.ownership, or (b) reaches the underlying
// *stdio.Pool / ServerInstance while Close is concurrently tearing it
// down — which is itself race-safe by construction:
// ServerInstance.Close (core/mcp/transport/stdio/server.go) closes
// doneCh and cancels the router BEFORE killing the process, and every
// in-flight callRawWithProgress selects on doneCh/ctx.Done/the
// response channel, so a racing call observes a clean "stdio: server
// closed" (or a broken-pipe write) error — never a panic, never a
// write through a freed resource. This is pre-existing behaviour the
// dispatch/stdio layer already had to support: a live LLM tool call
// racing a user-initiated quit hits the identical path today. Wiring
// kind=mcp hooks onto the same pool adds one more caller of an
// already race-hardened surface, not a new category of risk.
//
// No Cedar / confirm-each gate is applied here, matching kind=shell
// hooks (core/hooks/runner.go's execShell execs the configured command
// directly, ungated) rather than kind=builtin's toolloop path
// (wfMCPCallerAdapter, wf_adapters.go) which does gate. A hook is
// user-authored local configuration, not model-initiated action — the
// harness already trusts a kind=shell hook with unrestricted exec, so
// gating kind=mcp (a strict subset: it can only reach tools already
// configured on an installed MCP server) more tightly would be
// inconsistent, not more secure.
func (a *mcpHookInvokerAdapter) InvokeTool(ctx context.Context, tool string, payload []byte) ([]byte, error) {
	a.mu.RLock()
	pool := a.pool
	a.mu.RUnlock()
	if pool == nil {
		return nil, fmt.Errorf("mcp invoker not configured — MCP pool not yet initialized")
	}
	server, name := splitToolName(tool)
	raw, err := pool.Call(ctx, server, name, json.RawMessage(payload))
	if err != nil {
		return nil, err
	}
	return []byte(raw), nil
}
