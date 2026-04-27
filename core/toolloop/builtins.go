// Built-in tools for the toolloop. The toolloop's existing dispatch
// path (concurrent.go → MCPPool.Call) hits the MCP stdio pool only;
// in-binary tools (core/tools/websearch, core/tools/bash) used to be
// invisible to the model. This file plugs them in:
//
//   1. BuiltinTool is the narrow interface every in-binary tool
//      implements (Name / Description / InputSchema / Call). Both
//      websearch.Tool and bash.Tool already match this shape — no
//      changes needed there.
//
//   2. BuiltinRegistry is a goroutine-safe map of (name → BuiltinTool)
//      that the chassis populates when the user toggles WebSearch /
//      Bash on in Settings. A nil-safe Lookup + Names contract lets
//      the LLM-side discoverer gate tools by Settings flags.
//
//   3. BuiltinPool wraps an MCPPool and merges the built-in registry
//      into both Tools() and Call(). Built-in tools take precedence
//      over an MCP server that happens to expose the same name (the
//      "kaneaz__" prefix is reserved on the MCP catalog so this
//      conflict never arises in practice).
//
// The BuiltinPool is the chassis-side entry point: wiring code wraps
// the existing mcpPool and hands the result to the toolloop. The loop
// itself is unchanged.
package toolloop

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
)

// BuiltinTool is the narrow interface every in-binary tool implements.
// Production callers (core/tools/websearch.Tool, core/tools/bash.Tool)
// already satisfy it; tests can pass a stub.
//
// IMPORTANT: implementations MUST be safe for concurrent use. The
// toolloop fans out dispatches behind a semaphore and a single tool
// may receive multiple in-flight Call invocations.
type BuiltinTool interface {
	Name() string
	Description() string
	InputSchema() json.RawMessage
	Call(ctx context.Context, args json.RawMessage) (json.RawMessage, error)
}

// BuiltinServerName is the synthetic server name the loop uses for
// the dispatch path. It MUST NOT clash with any real MCP server name;
// users can't add a server called "kaneaz" because the recipe catalog
// validates server names against an allowlist.
const BuiltinServerName = "kaneaz"

// toolNameSeparator is the canonical delimiter between server and
// tool names in the namespaced "<server>__<tool>" form used on the
// model's tool catalog. Keeping it in this package preserves the
// builtin-naming round-trip without re-introducing the dispatch loop.
const toolNameSeparator = "__"

// builtinNamePrefix is the namespace prefix the in-binary tools embed
// in their Name() values (e.g. websearch.Name = "kaneaz__web_search").
// The toolloop's namespaced-name split strips the "kaneaz__" prefix
// before dispatch, so BuiltinPool.Call receives the tool name without
// the prefix. We restore the prefix when looking up the registry so
// the BuiltinTool's own Name() comparison works.
const builtinNamePrefix = "kaneaz" + toolNameSeparator

// fullBuiltinName re-attaches the "kaneaz__" prefix to a stripped
// tool name. The toolloop splits "kaneaz__bash" into server="kaneaz"
// + tool="bash"; BuiltinPool.Call needs to find the tool registered
// under "kaneaz__bash". The helper hides the asymmetry from callers.
func fullBuiltinName(stripped string) string {
	return builtinNamePrefix + stripped
}

// strippedBuiltinName removes the "kaneaz__" prefix when present.
// Used by BuiltinPool.Tools to publish the un-namespaced tool name in
// the (server, name) tuple the loop's catalog membership check
// expects.
func strippedBuiltinName(full string) string {
	if len(full) > len(builtinNamePrefix) && full[:len(builtinNamePrefix)] == builtinNamePrefix {
		return full[len(builtinNamePrefix):]
	}
	return full
}

// BuiltinRegistry is the concurrent-safe registry of built-in tools.
// The chassis registers tools at boot (gated by the Settings toggles)
// and the toolloop / LLM-side discoverer reads them.
type BuiltinRegistry struct {
	mu    sync.RWMutex
	tools map[string]BuiltinTool
}

// NewBuiltinRegistry returns an empty registry.
func NewBuiltinRegistry() *BuiltinRegistry {
	return &BuiltinRegistry{tools: map[string]BuiltinTool{}}
}

// Register adds a tool. Last-write-wins on collision so the chassis
// can re-register a tool with new options without restarting.
func (r *BuiltinRegistry) Register(t BuiltinTool) {
	if r == nil || t == nil {
		return
	}
	r.mu.Lock()
	r.tools[t.Name()] = t
	r.mu.Unlock()
}

// Unregister removes a tool by name. No-op when the name is unknown.
func (r *BuiltinRegistry) Unregister(name string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	delete(r.tools, name)
	r.mu.Unlock()
}

// Lookup returns the tool with the given name and a present bool.
func (r *BuiltinRegistry) Lookup(name string) (BuiltinTool, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// List returns every registered tool, sorted by name. The output is a
// stable slice the caller can range over without holding the lock.
func (r *BuiltinRegistry) List() []BuiltinTool {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for n := range r.tools {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]BuiltinTool, 0, len(names))
	for _, n := range names {
		out = append(out, r.tools[n])
	}
	return out
}

// Names returns every registered tool's name, sorted.
func (r *BuiltinRegistry) Names() []string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.tools))
	for n := range r.tools {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Empty reports whether the registry holds zero tools. Useful for the
// chassis-side wiring path: when no built-in is enabled, the
// BuiltinPool can short-circuit straight to the underlying MCP pool.
func (r *BuiltinRegistry) Empty() bool {
	if r == nil {
		return true
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools) == 0
}

// EnabledFilter is the read-side gate the chassis composes onto a
// concrete BuiltinRegistry. Every Lookup / List / Names is filtered
// through Enabled(name) — when a setting is toggled OFF mid-session,
// the next Tools() call returns the smaller list and the model never
// sees the disabled tool again.
//
// This is a thin wrapper rather than a mutator on BuiltinRegistry so
// the chassis can keep one registry instance for the process and toggle
// visibility per-tool without re-registering.
type EnabledFilter struct {
	inner   *BuiltinRegistry
	enabled func(name string) bool
}

// NewEnabledFilter wraps the registry with the supplied predicate. A
// nil predicate is treated as "always enabled".
func NewEnabledFilter(inner *BuiltinRegistry, enabled func(name string) bool) *EnabledFilter {
	return &EnabledFilter{inner: inner, enabled: enabled}
}

// Lookup returns the tool only when the predicate allows it.
func (f *EnabledFilter) Lookup(name string) (BuiltinTool, bool) {
	if f == nil || f.inner == nil {
		return nil, false
	}
	t, ok := f.inner.Lookup(name)
	if !ok {
		return nil, false
	}
	if f.enabled != nil && !f.enabled(name) {
		return nil, false
	}
	return t, true
}

// List returns the enabled subset of registered tools.
func (f *EnabledFilter) List() []BuiltinTool {
	if f == nil || f.inner == nil {
		return nil
	}
	all := f.inner.List()
	if f.enabled == nil {
		return all
	}
	out := make([]BuiltinTool, 0, len(all))
	for _, t := range all {
		if f.enabled(t.Name()) {
			out = append(out, t)
		}
	}
	return out
}

// Names returns the enabled subset of names, sorted.
func (f *EnabledFilter) Names() []string {
	tools := f.List()
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		out = append(out, t.Name())
	}
	return out
}

// Empty reports whether zero tools are currently enabled.
func (f *EnabledFilter) Empty() bool {
	return len(f.List()) == 0
}

// BuiltinLookup is the narrow interface BuiltinPool consumes. Both
// *BuiltinRegistry (raw) and *EnabledFilter (gated) satisfy it.
type BuiltinLookup interface {
	Lookup(name string) (BuiltinTool, bool)
	List() []BuiltinTool
	Empty() bool
}

// BuiltinPool wraps an MCPPool and merges the built-in registry into
// both Tools() and Call(). When an inbound Call's tool name matches a
// built-in, the pool dispatches to the built-in's Call method
// directly; otherwise it falls through to the underlying MCP pool.
//
// The Server field on a built-in's Tool entry is BuiltinServerName so
// the toolloop's namespacing path ("server__tool" → split for
// dispatch) round-trips cleanly. The loop's catalog membership check
// (catalogContains) sees the built-in alongside MCP tools.
type BuiltinPool struct {
	inner    MCPPool
	builtins BuiltinLookup
}

// NewBuiltinPool wraps base with the supplied registry. nil base
// collapses to a builtins-only pool — useful for tests that don't want
// to spin up the MCP stdio pool. nil registry is a passthrough wrapper
// (the inner pool's behaviour is preserved).
func NewBuiltinPool(base MCPPool, registry BuiltinLookup) *BuiltinPool {
	return &BuiltinPool{inner: base, builtins: registry}
}

// Tools returns the union of MCP-discovered tools and registered
// built-ins. Built-ins are appended after MCP tools so the existing
// audit / progress topics surface them in the second half of any UI
// list — the dispatch path doesn't care about ordering.
//
// Each built-in's (Server, Name) is (BuiltinServerName, stripped) —
// the "kaneaz__" prefix is removed so the loop's namespaced-name split
// produces the same stripped name on dispatch. Lookup compensates by
// re-attaching the prefix.
func (p *BuiltinPool) Tools(ctx context.Context) ([]Tool, error) {
	if p == nil {
		return nil, nil
	}
	var inner []Tool
	if p.inner != nil {
		i, err := p.inner.Tools(ctx)
		if err != nil {
			return nil, err
		}
		inner = i
	}
	if p.builtins == nil || p.builtins.Empty() {
		return inner, nil
	}
	for _, b := range p.builtins.List() {
		inner = append(inner, Tool{Server: BuiltinServerName, Name: strippedBuiltinName(b.Name())})
	}
	return inner, nil
}

// Call dispatches to a built-in when (server == BuiltinServerName)
// AND the tool name (or its prefixed equivalent) matches a registered
// built-in. Otherwise it falls through to the underlying MCP pool.
func (p *BuiltinPool) Call(ctx context.Context, server, tool string, args json.RawMessage) (json.RawMessage, error) {
	if p == nil {
		return nil, errors.New("toolloop: nil builtin pool")
	}
	if server == BuiltinServerName && p.builtins != nil {
		// Try the name as-passed, then the prefixed form. Built-ins
		// register under their full Name() (e.g. "kaneaz__bash"); the
		// loop strips the prefix on dispatch so we look up under both.
		if t, ok := p.builtins.Lookup(tool); ok {
			return t.Call(ctx, args)
		}
		if t, ok := p.builtins.Lookup(fullBuiltinName(tool)); ok {
			return t.Call(ctx, args)
		}
	}
	if p.inner == nil {
		return nil, errors.New("toolloop: built-in not found and no fallback pool")
	}
	return p.inner.Call(ctx, server, tool, args)
}

// Compile-time witness.
var _ MCPPool = (*BuiltinPool)(nil)
