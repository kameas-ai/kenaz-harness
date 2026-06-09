// MCP-backed implementation of corellm.ToolDiscoverer. Lives in
// views/llm because it imports both core/mcp (for the pool) and
// core/toolloop (for the permission resolver) — neither dependency is
// allowed inside core/llm itself (DIRECTIVE_001 keeps the connector
// free of orchestration imports).
package llm

import (
	"context"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/mcp"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// ToolNameSeparator is the delimiter used to namespace MCP tool names
// when they are projected onto the model's tool catalog. The pool
// surfaces tools as (Server, Name) pairs but provider tool surfaces
// (Anthropic, OpenAI) take a single string. Joining with "__" keeps
// the result inside Anthropic's `^[a-zA-Z0-9_-]{1,64}$` constraint
// while staying obvious to humans skimming logs and resilient to
// underscores already present in either side.
const ToolNameSeparator = "__"

// mcpToolDiscoverer is the production ToolDiscoverer wired by the rpc
// layer. It reads the live tool list from the pool, drops any tool the
// permission resolver denies for this session, and namespaces the
// remainder so the toolloop can split them back into (server, tool)
// pairs at dispatch time.
//
// When a non-nil BuiltinLookup is provided, the discoverer ALSO appends
// every enabled built-in tool (gated by the lookup's filter, e.g.
// Settings.WebSearchEnabled). Built-ins surface to the model namespaced
// as "kaneaz__<tool>" — the toolloop's BuiltinPool dispatches them
// without going through MCP.
type mcpToolDiscoverer struct {
	pool     mcp.Pool
	perms    toolloop.PermissionResolver
	builtins toolloop.BuiltinLookup
}

// NewMCPToolDiscoverer wraps an mcp.Pool + an optional permission
// resolver into a ToolDiscoverer. A nil pool collapses to a no-op
// (returns an empty list); a nil resolver disables filtering so every
// pool-listed tool reaches the model.
func NewMCPToolDiscoverer(pool mcp.Pool, perms toolloop.PermissionResolver) corellm.ToolDiscoverer {
	return &mcpToolDiscoverer{pool: pool, perms: perms}
}

// NewMCPToolDiscovererWithBuiltins is the production constructor that
// also threads in-binary tools (websearch, bash) through the same
// discovery surface. builtins.Empty() at boot — the registry is filled
// in when the user toggles them on; no rebind needed.
func NewMCPToolDiscovererWithBuiltins(
	pool mcp.Pool,
	perms toolloop.PermissionResolver,
	builtins toolloop.BuiltinLookup,
) corellm.ToolDiscoverer {
	return &mcpToolDiscoverer{pool: pool, perms: perms, builtins: builtins}
}

// Tools satisfies corellm.ToolDiscoverer.
func (d *mcpToolDiscoverer) Tools(ctx context.Context, sessionID string) ([]corellm.ToolSpec, error) {
	if d == nil {
		return nil, nil
	}
	// Preserve the legacy "nil pool ⇒ nil list" contract for callers
	// that don't wire builtins. The empty-list fall-through below
	// only kicks in when at least one source produced something.
	if d.pool == nil && (d.builtins == nil || d.builtins.Empty()) {
		return nil, nil
	}
	var out []corellm.ToolSpec
	if d.pool != nil {
		raw, err := d.pool.Tools(ctx)
		if err != nil {
			return nil, err
		}
		for _, t := range raw {
			if d.perms != nil {
				res, perr := d.perms.Resolve(ctx, sessionID, t.Server, t.Name)
				if perr == nil && res.Policy == toolloop.PolicyDeny {
					continue
				}
			}
			out = append(out, corellm.ToolSpec{
				Name:        t.Server + ToolNameSeparator + t.Name,
				Description: t.Description,
				InputSchema: t.InputSchema,
			})
		}
	}
	if d.builtins != nil && !d.builtins.Empty() {
		for _, b := range d.builtins.List() {
			// Built-ins use the reserved "kaneaz" server prefix so the
			// toolloop's namespaced-name split sends Call back to
			// BuiltinPool. The Name() value already includes the
			// "kaneaz__" prefix in production tools (websearch.Name,
			// bash.Name); the discoverer publishes that name verbatim.
			out = append(out, corellm.ToolSpec{
				Name:        b.Name(),
				Description: b.Description(),
				InputSchema: b.InputSchema(),
			})
		}
	}
	return out, nil
}
