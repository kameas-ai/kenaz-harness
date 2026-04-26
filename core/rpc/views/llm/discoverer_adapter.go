// MCP-backed implementation of corellm.ToolDiscoverer. Lives in
// views/llm because it imports both core/mcp (for the pool) and
// core/toolloop (for the permission resolver) — neither dependency is
// allowed inside core/llm itself (DIRECTIVE_001 keeps the connector
// free of orchestration imports).
package llm

import (
	"context"

	corellm "github.com/sigil-tech/kaneaz-harness/core/llm"
	"github.com/sigil-tech/kaneaz-harness/core/mcp"
	"github.com/sigil-tech/kaneaz-harness/core/toolloop"
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
type mcpToolDiscoverer struct {
	pool  mcp.Pool
	perms toolloop.PermissionResolver
}

// NewMCPToolDiscoverer wraps an mcp.Pool + an optional permission
// resolver into a ToolDiscoverer. A nil pool collapses to a no-op
// (returns an empty list); a nil resolver disables filtering so every
// pool-listed tool reaches the model.
func NewMCPToolDiscoverer(pool mcp.Pool, perms toolloop.PermissionResolver) corellm.ToolDiscoverer {
	return &mcpToolDiscoverer{pool: pool, perms: perms}
}

// Tools satisfies corellm.ToolDiscoverer.
func (d *mcpToolDiscoverer) Tools(ctx context.Context, sessionID string) ([]corellm.ToolSpec, error) {
	if d == nil || d.pool == nil {
		return nil, nil
	}
	raw, err := d.pool.Tools(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]corellm.ToolSpec, 0, len(raw))
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
	return out, nil
}
