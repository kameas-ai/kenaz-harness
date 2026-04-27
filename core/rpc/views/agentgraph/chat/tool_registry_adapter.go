package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	coreag "github.com/sigil-tech/kaneaz-harness/core/agentgraph"
	"github.com/sigil-tech/kaneaz-harness/core/toolloop"
)

// ToolRegistryAdapter satisfies agentgraph.ToolRegistry by routing
// dispatch through the existing toolloop.MCPPool surface (which the
// chassis wires to its real MCP stdio pool + builtin registry). The
// adapter wraps both the pool and the toolloop's permission resolver
// so behaviour parity with the legacy chat path is mechanical:
// permissions, audit, and the BuiltinPool's kaneaz__* tools all keep
// working through the same path.
//
// WP07 will simplify this adapter when toolloop is fully retired —
// the kernel-side ToolRegistry can take ownership of the catalog and
// the permission resolver becomes a Cedar gate. For now the adapter
// is the cleanest cutover surface.
type ToolRegistryAdapter struct {
	pool      toolloop.MCPPool
	perms     toolloop.PermissionResolver
	sessionID string
	// catalog caches the (server, tool) pairs from pool.Tools so Has()
	// can answer without a round-trip per call. Populated on first
	// access; the runner refreshes it implicitly on each StartStream
	// because a fresh adapter is constructed per chat run.
	catalog []toolloop.Tool
}

// NewToolRegistryAdapter wraps the chassis-side MCP pool + permission
// resolver into the kernel's ToolRegistry shape. The sessionID is
// threaded through so per-session permission overrides apply.
func NewToolRegistryAdapter(pool toolloop.MCPPool, perms toolloop.PermissionResolver, sessionID string) *ToolRegistryAdapter {
	if perms == nil {
		// Use the toolloop's static-empty resolver so the adapter
		// behaves like auto_allow when the chassis didn't wire one.
		perms, _ = toolloop.NewStaticResolver(nil)
	}
	return &ToolRegistryAdapter{
		pool:      pool,
		perms:     perms,
		sessionID: sessionID,
	}
}

// Has reports whether the model-emitted tool name is registered. The
// kernel's seam expects a synchronous bool; we lazy-load the catalog
// once on first call (cheap — pool.Tools is a single MCP roundtrip).
// The kernel calls Has before each tool dispatch so a discovery
// failure surfaces as a clean "unknown tool" error rather than a
// blocked dispatch.
func (a *ToolRegistryAdapter) Has(name string) bool {
	if a == nil || a.pool == nil {
		return false
	}
	if a.catalog == nil {
		// Best-effort: catalog load failure means we report no tools.
		// The kernel will surface "unknown tool" which is the right
		// user-visible error.
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
	// Fallback: the model emitted a non-namespaced name. Match against
	// the bare tool name; the catalog's server-side disambiguation
	// kicks in only on dispatch.
	for _, t := range a.catalog {
		if t.Name == name {
			return true
		}
	}
	return false
}

// Call dispatches the tool through the pool, consulting the
// permission resolver first. Returns ErrToolDenied wrapped in a
// ToolResult{IsError: true} so the kernel surfaces a synthetic
// error result without crashing the run.
//
// The implementation is intentionally close to toolloop.dispatchOne:
// resolve perms, route to pool.Call, surface IsError on JSON-RPC
// failure. Hooks (pre/post-tool-use) are NOT fired here — the kernel
// HookManager fires the canonical pre/post-tool boundaries from the
// ToolNode executor (WP06). This keeps the adapter narrow.
func (a *ToolRegistryAdapter) Call(ctx context.Context, call coreag.ToolCall) (coreag.ToolResult, error) {
	if a == nil || a.pool == nil {
		return coreag.ToolResult{}, errors.New("chat: nil tool pool adapter")
	}
	server, tool, ok := splitName(call.Name)
	if !ok {
		// Best-effort: the model may have emitted a bare tool name.
		// Find the first catalog match and use its server.
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
	res, err := a.perms.Resolve(ctx, a.sessionID, server, tool)
	if err != nil {
		return coreag.ToolResult{}, fmt.Errorf("chat: permission resolve: %w", err)
	}
	if res.Policy == toolloop.PolicyDeny {
		reason := res.Reason
		if reason == "" {
			reason = "denied by permission policy"
		}
		return coreag.ToolResult{
			Content: fmt.Sprintf("tool %q denied: %s", call.Name, reason),
			IsError: true,
		}, nil
	}
	// confirm-each: the brief defers Cedar PolicyGate routing to WP06's
	// chassis wiring. For now treat confirm_each as auto_allow at the
	// adapter boundary — this matches toolloop's behaviour when
	// ConfirmEachEnabled is false. WP06 lifts confirm-each into the
	// kernel's ToolNode + Cedar PolicyGate.

	// Marshal the kernel's args map → JSON for the pool.
	argsJSON, err := json.Marshal(call.Args)
	if err != nil {
		return coreag.ToolResult{}, fmt.Errorf("chat: marshal args: %w", err)
	}

	out, err := a.pool.Call(ctx, server, tool, argsJSON)
	if err != nil {
		// Surface as IsError result so the model sees the failure and
		// can re-plan. The kernel records the canonical EventNodeError
		// alongside.
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
