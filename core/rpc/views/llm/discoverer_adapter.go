// MCP-backed implementation of corellm.ToolDiscoverer. Lives in
// views/llm because it imports both core/mcp (for the pool) and
// core/toolloop (for the permission resolver) — neither dependency is
// allowed inside core/llm itself (DIRECTIVE_001 keeps the connector
// free of orchestration imports).
package llm

import (
	"context"
	"strings"
	"sync"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/logging"
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
// as "kenaz__<tool>" — the toolloop's BuiltinPool dispatches them
// without going through MCP.
type mcpToolDiscoverer struct {
	pool     mcp.Pool
	perms    toolloop.PermissionResolver
	builtins toolloop.BuiltinLookup

	// sizes holds each server's summed schema estimate as of its last
	// discovery, so a tools/list refresh that moves it by more than
	// schemaSizeChangeRatio is logged once, at the discovery that sees it.
	sizesMu sync.Mutex
	sizes   map[string]int
}

// schemaSizeChangeRatio is the relative change in a server's summed
// schema estimate, between two discoveries, that logs
// tools.schema_size_changed.
const schemaSizeChangeRatio = 0.20

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
	// out is this session's catalog; listedAll is every listed tool before
	// the per-session permission filter, which is what a server's schema
	// size is measured over (a session's allowlist is not a size change).
	var out, listedAll []corellm.ToolSpec
	if d.pool != nil {
		raw, err := d.pool.Tools(ctx)
		if err != nil {
			return nil, err
		}
		// Listing is a visibility probe, not a dispatch: a resolver that
		// records denials (the scheduled-run allowlist arm) returns the
		// same verdict but must not record one per listed tool.
		probeCtx := toolloop.WithVisibilityProbe(ctx)
		for _, t := range raw {
			spec := corellm.ToolSpec{
				Name:        t.Server + ToolNameSeparator + t.Name,
				Description: t.Description,
				InputSchema: t.InputSchema,
				Server:      t.Server,
				// mcp.Pool.Tools lists only tools whose server can answer
				// a call (dispatch.Pool.canServe), so a listed tool is a
				// running one.
				Running: true,
			}
			spec.TokenEst = corellm.EstimateToolSpecTokens(spec)
			listedAll = append(listedAll, spec)
			if d.perms != nil {
				res, perr := d.perms.Resolve(probeCtx, sessionID, t.Server, t.Name)
				// harness-self-attach-01PMHS01 UNIT-4, AC-017: a
				// resolver error used to leave the tool listed
				// (perr == nil was required to even consider denying).
				// AC-016's dispatch-time floor (kernel_tool_adapter.go's
				// default: arm) still refuses the CALL when the
				// resolver errors, so this was never a reachability
				// hole — but it was a visibility lie: the model would
				// see a tool advertised that any subsequent call to it
				// was guaranteed to refuse. Omit on error too, so
				// visibility matches reachability (FR-005) even on the
				// error path, not just the happy path.
				if perr != nil || res.Policy == toolloop.PolicyDeny {
					continue
				}
			}
			out = append(out, spec)
		}
	}
	if d.builtins != nil && !d.builtins.Empty() {
		probeCtx := toolloop.WithVisibilityProbe(ctx)
		listed := d.builtins.List()
		var enabledBuiltins []string
		// One summary line per discovery; the enabled predicate itself
		// does not log per tool.
		defer func() {
			logging.L().Info("llm.builtins.enabled",
				"session_id", sessionID, "listed", len(listed),
				"enabled", len(enabledBuiltins), "tools", enabledBuiltins)
		}()
		for _, b := range listed {
			server, _ := splitBuiltinName(b.Name())
			spec := corellm.ToolSpec{
				Name:        b.Name(),
				Description: b.Description(),
				InputSchema: b.InputSchema(),
				Server:      server,
				Running:     true,
			}
			spec.TokenEst = corellm.EstimateToolSpecTokens(spec)
			listedAll = append(listedAll, spec)
			// Visibility matches reachability for builtins too
			// (model-harness-toolset-01MHTS001 WP02 security review, M2):
			// a builtin the resolver denies for this session — e.g. one
			// off a scheduled run's allowlist — is not advertised, since
			// every call to it would be refused (and recorded). Same
			// probe-marked path as the pool tools above, same "omit on
			// error" rule. Builtins publish "kenaz__<tool>"; the resolver
			// sees (server, tool) exactly as the kernel adapter's split
			// hands it at dispatch.
			if d.perms != nil {
				_, tool := splitBuiltinName(b.Name())
				res, perr := d.perms.Resolve(probeCtx, sessionID, server, tool)
				if perr != nil || res.Policy == toolloop.PolicyDeny {
					continue
				}
			}
			// Built-ins use the reserved "kenaz" server prefix so the
			// toolloop's namespaced-name split sends Call back to
			// BuiltinPool. The Name() value already includes the
			// "kenaz__" prefix in production tools (websearch.Name,
			// bash.Name); the discoverer publishes that name verbatim.
			enabledBuiltins = append(enabledBuiltins, b.Name())
			out = append(out, spec)
		}
	}
	d.noteSchemaSizes(listedAll)
	return out, nil
}

// noteSchemaSizes sums the catalog's estimates per server and logs
// tools.schema_size_changed for every server whose sum moved by more
// than schemaSizeChangeRatio since the previous discovery. A server
// seen for the first time records its baseline without logging; a
// server absent from this discovery keeps its last baseline, so a
// stop/start cycle compares against the size it had before stopping.
func (d *mcpToolDiscoverer) noteSchemaSizes(catalog []corellm.ToolSpec) {
	cur := map[string]int{}
	count := map[string]int{}
	for _, t := range catalog {
		cur[t.Server] += t.TokenEst
		count[t.Server]++
	}
	d.sizesMu.Lock()
	defer d.sizesMu.Unlock()
	if d.sizes == nil {
		d.sizes = map[string]int{}
	}
	for server, now := range cur {
		prev, seen := d.sizes[server]
		d.sizes[server] = now
		if !seen || prev == now {
			continue
		}
		delta := now - prev
		if delta < 0 {
			delta = -delta
		}
		if prev > 0 && float64(delta)/float64(prev) <= schemaSizeChangeRatio {
			continue
		}
		logging.L().Info("tools.schema_size_changed",
			"server", server, "tools", count[server],
			"tokens_est_before", prev, "tokens_est_after", now)
	}
}

// splitBuiltinName splits a published builtin name ("kenaz__web_fetch")
// into the (server, tool) pair the dispatch-time permission check sees.
// A name without the separator resolves as (name, "") — never matched by
// an allowlist entry, so it fails closed under containment.
func splitBuiltinName(name string) (string, string) {
	if i := strings.Index(name, ToolNameSeparator); i > 0 {
		return name[:i], name[i+len(ToolNameSeparator):]
	}
	return name, ""
}
