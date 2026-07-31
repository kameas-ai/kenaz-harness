package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// AutonomyKnobsProvider is the narrow surface the kernel tool adapter
// needs to apply the autonomy posture before each tool call. The
// production wiring binds this to a closure over the session's resolved
// autonomy knobs (autonomy-dial WP04). nil disables posture-aware
// short-circuiting — the adapter falls through to the permission
// resolver on every call (v0.3.0 baseline behaviour).
type AutonomyKnobsProvider func() autonomy.ResolvedKnobs

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

	// autonomy is the optional knobs provider for autonomy-dial WP04.
	// When non-nil the adapter reads AutoApproveFamilies before each
	// tool call to determine whether the permission-resolver prompt path
	// can be bypassed for the "tool" family.
	autonomy AutonomyKnobsProvider
}

// newKernelToolAdapter wraps the chassis-side pool + resolver.
func newKernelToolAdapter(pool ToolPool, perms ToolPermissionResolver, sessionID string) *kernelToolAdapter {
	return &kernelToolAdapter{
		pool:      pool,
		perms:     perms,
		sessionID: sessionID,
	}
}

// withAutonomy attaches an AutonomyKnobsProvider to the adapter.
// Returns the same pointer so callers can chain at construction time.
func (a *kernelToolAdapter) withAutonomy(provider AutonomyKnobsProvider) *kernelToolAdapter {
	a.autonomy = provider
	return a
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
			// Surface the discovery failure: otherwise Has() returns false
			// and the kernel reports a misleading "unknown tool" with no
			// trace of the real cause (network / permission / pool error).
			logging.L().Warn("chat.tool_adapter.catalog_load_failed",
				"at", "Has", "err", err.Error())
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
		logging.L().Error("chat.tool_adapter.nil_pool", "tool", call.Name)
		return coreag.ToolResult{}, errors.New("chat: nil tool pool adapter")
	}
	server, tool, ok := splitName(call.Name)
	if !ok {
		// Bare name fallback: find the first catalog match.
		if a.catalog == nil {
			tools, err := a.pool.Tools(ctx)
			if err != nil {
				logging.L().Warn("chat.tool_adapter.catalog_load_failed",
					"at", "Call", "tool", call.Name, "err", err.Error())
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
	// WP04 — autonomy-dial posture gate.
	//
	// Before consulting the permission resolver, check whether the
	// session's resolved autonomy knobs auto-approve the "tool" family.
	// AutoApproveFamilies is set by the tier presets:
	//   - strict / cautious: empty or read-only → resolver always runs
	//   - default / bold / autonomous: includes "tool" (via FamilyShellSafe
	//     or FamilyNetwork in the autonomous preset) — BUT the autonomy
	//     package maps its preset families (read/write/shell-safe/network)
	//     onto the cedar FamilyTool via the "tool" family name directly.
	//
	// The canonical family for any MCP / external tool call is
	// autonomy.FamilyShellSafe when the tool is a general tool, and we
	// treat any non-empty AutoApproveFamilies that includes at least one
	// of the four families as a signal to skip the interactive-prompt
	// path. We check for the explicit "tool" sentinel first, then fall
	// back to "shell-safe" (the lowest rung that covers tool dispatch in
	// the presets). Cedar deny (explicit forbid) is NOT bypassed —
	// callers gate through the engine before reaching the permission
	// resolver; this path only skips the NotApplicable→interactive-prompt
	// branch by short-circuiting Resolve to "auto_allow".
	skipPrompt := false
	if a.autonomy != nil {
		knobs := a.autonomy()
		fs := knobs.AutoApproveFamilies
		if fs.Has(autonomy.FamilyShellSafe) || fs.Has(autonomy.FamilyNetwork) {
			skipPrompt = true
		}
	}

	if a.perms != nil && !skipPrompt {
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
	} else if a.perms != nil && skipPrompt {
		// Still run the resolver when it returns "deny" (explicit Cedar
		// deny must remain the floor), but skip interactive prompts by
		// checking only the policy string — NotApplicable maps to
		// "confirm_each" in the resolver, which we bypass here.
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
		// "confirm_each" path: autonomy posture says auto-allow — proceed.
	}

	argsJSON, err := json.Marshal(call.Args)
	if err != nil {
		return coreag.ToolResult{}, fmt.Errorf("chat: marshal args: %w", err)
	}

	// Stuff the session ID into ctx so built-in tools that need it
	// (kenaz__save_artifact) can pull it out without a parameter.
	// Tools that don't read it pay nothing.
	out, err := a.pool.Call(toolloop.WithSessionID(ctx, a.sessionID), server, tool, argsJSON)
	if err != nil {
		// WP02 (tool-error-legibility-01PMDL02): append a conservative
		// environment-drift hint when the raw error signature-matches a
		// well-known case. Never rewrites err.Error().
		return coreag.ToolResult{
			Content: coreag.AppendEnvironmentDriftHint(err.Error()),
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
