package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	coreag "github.com/sigil-tech/kaneaz-harness/core/agentgraph"
)

// kernelToolAdapter satisfies agentgraph.ToolRegistry by routing
// dispatch through the chat-package-local ToolPool surface (which the
// chassis wires to its real MCP stdio pool + builtin registry). This
// is the kernel-facing companion to ToolRegistryAdapter; the
// difference is that this implementation depends only on the package-
// local ToolPool / ToolPermissionResolver interfaces rather than the
// toolloop types directly. WP07 retires ToolRegistryAdapter in favour
// of this one.
type kernelToolAdapter struct {
	pool      ToolPool
	perms     ToolPermissionResolver
	sessionID string
	catalog   []ToolEntry
}

// newKernelToolAdapter wraps the chassis-side pool + resolver.
func newKernelToolAdapter(pool ToolPool, perms ToolPermissionResolver, sessionID string) *kernelToolAdapter {
	return &kernelToolAdapter{
		pool:      pool,
		perms:     perms,
		sessionID: sessionID,
	}
}

// Has satisfies agentgraph.ToolRegistry. Lazy-loads the catalog on
// first call; a discovery failure surfaces as "no tools registered"
// so the kernel produces a clean "unknown tool" error.
func (a *kernelToolAdapter) Has(name string) bool {
	if a == nil || a.pool == nil {
		return false
	}
	if a.catalog == nil {
		tools, err := a.pool.Tools(context.Background())
		if err != nil {
			return false
		}
		a.catalog = tools
	}
	server, tool, ok := splitName(name)
	if ok {
		for _, t := range a.catalog {
			if t.Server == server && t.Name == tool {
				return true
			}
		}
		return false
	}
	for _, t := range a.catalog {
		if t.Name == name {
			return true
		}
	}
	return false
}

// Call dispatches through the pool, consulting the permission
// resolver first. Returns IsError=true on deny / pool failure so the
// kernel surfaces the failure to the model without crashing the run.
func (a *kernelToolAdapter) Call(ctx context.Context, call coreag.ToolCall) (coreag.ToolResult, error) {
	if a == nil || a.pool == nil {
		return coreag.ToolResult{}, errors.New("chat: nil tool pool adapter")
	}
	server, tool, ok := splitName(call.Name)
	if !ok {
		// Bare name fallback: find the first catalog match.
		if a.catalog == nil {
			tools, err := a.pool.Tools(ctx)
			if err != nil {
				return coreag.ToolResult{}, fmt.Errorf("chat: tool catalog: %w", err)
			}
			a.catalog = tools
		}
		for _, t := range a.catalog {
			if t.Name == call.Name {
				server = t.Server
				tool = t.Name
				ok = true
				break
			}
		}
		if !ok {
			return coreag.ToolResult{}, fmt.Errorf("chat: unknown tool %q", call.Name)
		}
	}
	if a.perms != nil {
		v, err := a.perms.Resolve(ctx, a.sessionID, server, tool)
		if err != nil {
			return coreag.ToolResult{}, fmt.Errorf("chat: permission resolve: %w", err)
		}
		if v.Policy == "deny" {
			reason := v.Reason
			if reason == "" {
				reason = "denied by permission policy"
			}
			return coreag.ToolResult{
				Content: fmt.Sprintf("tool %q denied: %s", call.Name, reason),
				IsError: true,
			}, nil
		}
	}

	argsJSON, err := json.Marshal(call.Args)
	if err != nil {
		return coreag.ToolResult{}, fmt.Errorf("chat: marshal args: %w", err)
	}

	out, err := a.pool.Call(ctx, server, tool, argsJSON)
	if err != nil {
		return coreag.ToolResult{
			Content: err.Error(),
			IsError: true,
		}, nil
	}
	return coreag.ToolResult{
		Content: string(out),
		IsError: false,
	}, nil
}

// splitName parses a "<server>__<tool>" name. Returns ok=false when
// the input has no separator or either side is empty after split.
const toolNameSeparator = "__"

func splitName(name string) (server, tool string, ok bool) {
	for i := 0; i+len(toolNameSeparator) <= len(name); i++ {
		if name[i:i+len(toolNameSeparator)] == toolNameSeparator {
			s := name[:i]
			t := name[i+len(toolNameSeparator):]
			if s == "" || t == "" {
				return "", "", false
			}
			return s, t, true
		}
	}
	return "", "", false
}
