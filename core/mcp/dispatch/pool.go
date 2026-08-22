// Package dispatch provides a transport-routing MCP pool that fans out
// Open/OpenOne/Call/Tools/Close/CloseOne operations to the correct
// sub-pool based on ServerSpec.Transport:
//
//	""/"stdio"  → stdioPool      (typically *stdio.Pool)
//	"http"      → httpPool       (typically *mcphttp.Pool)
//	"sse"       → ssePool        (typically *sse.Pool)
//	"inprocess" → inprocessPool  (*InProcessSubPool) — Go-native,
//	              in-process MCP servers registered directly via
//	              Pool.RegisterInProcess, not spawned from a spec
//	              (harness-self-attach-01PMHS01 UNIT-5).
//
// The Pool type satisfies both the narrow PoolController interface used by
// the tools view (OpenOne/CloseOne/RecipeStatus/ServerTools) and the wider
// mcp.Pool interface used by the tool-loop dispatch path
// (Open/Close/Tools/Call).
//
// SECURITY: the Pool never surfaces credential material. Authorization
// headers flow through each transport's internal Connection and are
// redacted at the slog diagnostic level; they are not returned by any
// method on this type.
package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
	"github.com/kameas-ai/kenaz-harness/core/mcp/transport"
	mcphttp "github.com/kameas-ai/kenaz-harness/core/mcp/transport/http"
	"github.com/kameas-ai/kenaz-harness/core/mcp/transport/sse"
	"github.com/kameas-ai/kenaz-harness/core/mcp/transport/stdio"
)

// StdioSubPool is the subset of *stdio.Pool the dispatch pool requires
// over-and-above the base coremcp.Pool interface. It is defined as an
// interface so tests can inject fakes without constructing a live
// *stdio.Pool.
type StdioSubPool interface {
	coremcp.Pool
	// OpenOne adds a single server and returns ErrServerExists when a
	// server with that name is already in the pool.
	OpenOne(ctx context.Context, spec coremcp.ServerSpec) error
	// CloseOne removes a single server. Returns stdio.ErrServerNotFound
	// when the server is not in the pool.
	CloseOne(ctx context.Context, id string) error
	// RecipeStatus returns the live snapshot for a server.
	RecipeStatus(id string) (stdio.RecipeStatus, bool)
	// ServerTools returns the cached tool list for a server.
	ServerTools(id string) []coremcp.Tool
	// AllRecipeStatuses returns status snapshots for every server the
	// pool currently tracks.
	AllRecipeStatuses() []stdio.RecipeStatus
}

// Compile-time witness: *stdio.Pool satisfies StdioSubPool.
var _ StdioSubPool = (*stdio.Pool)(nil)

// RemoteSubPool is the subset of *http.Pool / *sse.Pool the dispatch
// pool requires over-and-above the base coremcp.Pool interface,
// mirroring StdioSubPool. Defined as an interface so tests can inject
// fakes without constructing a live *http.Pool / *sse.Pool.
//
// Before connector-lifecycle-truth-01PMZ303 UNIT-6, httpPool and
// ssePool were typed as bare coremcp.Pool, which has no per-server
// close. dispatch.Pool.CloseOne's http/sse arm was comment-only and
// silently returned nil, so uninstalling (or re-installing, via the
// evict-before-spawn step of InstallRecipe) a remote recipe left its
// health-probe goroutine running forever against the "uninstalled"
// server on the credential that was injected at Open time, and left
// its tools reachable through dispatch.Pool.Tools/Call. See
// docs/unwired-ledger.md and the mission spec's §1.3 for the full
// trace.
type RemoteSubPool interface {
	coremcp.Pool
	// CloseOne removes a single server: stops any per-server health
	// probe (blocking until its goroutine has exited) and closes the
	// connection. Returns an error naming the server when it is not
	// present in the sub-pool.
	CloseOne(ctx context.Context, id string) error
}

// Compile-time witnesses: the concrete http/sse pools satisfy
// RemoteSubPool.
var _ RemoteSubPool = (*mcphttp.Pool)(nil)
var _ RemoteSubPool = (*sse.Pool)(nil)

// Pool fans out recipe-open/call operations to the correct transport
// sub-pool based on ServerSpec.Transport.
//
// Each sub-pool is optional — pass nil for transports the caller does not
// need. An OpenOne call for a nil sub-pool's transport returns an error.
//
// Pool is safe for concurrent use.
type Pool struct {
	stdioPool     StdioSubPool
	httpPool      RemoteSubPool
	ssePool       RemoteSubPool
	inprocessPool *InProcessSubPool

	// ownership maps a server id to the transport tag that opened it.
	// Guarded by mu.
	mu        sync.RWMutex
	ownership map[string]string // id → "stdio" | "http" | "sse" | "inprocess"

	// callObserver, when set, is invoked with (server, tool) on every
	// Call dispatch. Metadata only — arguments are never passed. Used by
	// the served connector supervisor for FR-014 connector.tool_call
	// ledger events. Guarded by mu.
	callObserver func(server, tool string)
}

// Options bundles the three sub-pools. Nil entries are tolerated;
// the caller is responsible for wiring at least the stdio pool so existing
// recipes keep working.
//
// HTTP and SSE are expressed as concrete types so callers do not need to
// import the interface type; the dispatch package converts them to the
// internal RemoteSubPool interface internally.
type Options struct {
	Stdio StdioSubPool
	HTTP  *mcphttp.Pool
	SSE   *sse.Pool
	// InProcess wires the "inprocess" transport arm (UNIT-5). Nil is
	// tolerated — the tag then behaves like an unwired transport
	// (OpenOne/RegisterInProcess return an explicit error), matching
	// how a nil HTTP/SSE sub-pool already behaves.
	InProcess *InProcessSubPool
}

// New returns a Pool wired with the given sub-pools.
func New(opts Options) *Pool {
	p := &Pool{
		stdioPool: opts.Stdio,
		ownership: make(map[string]string),
	}
	if opts.HTTP != nil {
		p.httpPool = opts.HTTP
	}
	if opts.SSE != nil {
		p.ssePool = opts.SSE
	}
	if opts.InProcess != nil {
		p.inprocessPool = opts.InProcess
	}
	return p
}

// Compile-time witnesses that *Pool satisfies the required interface
// surfaces.
var _ coremcp.Pool = (*Pool)(nil)

// transportFor returns the canonical transport tag for a spec. Empty
// string and "stdio" both map to "stdio" for backwards compatibility
// with the shipped catalog (which predates the transport field).
func transportFor(spec coremcp.ServerSpec) string {
	switch spec.Transport {
	case "", "stdio":
		return "stdio"
	case "http":
		return "http"
	case "sse":
		return "sse"
	case "inprocess":
		return "inprocess"
	default:
		return spec.Transport
	}
}

// subPoolFor returns the sub-pool for a transport tag, or nil if the
// tag names an unrecognised or unwired transport.
func (d *Pool) subPoolFor(tag string) coremcp.Pool {
	switch tag {
	case "stdio":
		if d.stdioPool != nil {
			return d.stdioPool
		}
	case "http":
		return d.httpPool // may be nil
	case "sse":
		return d.ssePool // may be nil
	case "inprocess":
		if d.inprocessPool != nil {
			return d.inprocessPool
		}
	}
	return nil
}

// RegisterInProcess adds a Go-native, in-process MCP server to the
// "inprocess" transport arm and records its ownership so Tools/Call
// route to it exactly like a stdio/http/sse recipe. Unlike Open/
// OpenOne, there is no ServerSpec to construct a connection from —
// conn is already live. Returns an error if the inprocess sub-pool
// was not wired via Options.InProcess, or if name is already
// registered (ErrInProcessServerExists).
func (d *Pool) RegisterInProcess(ctx context.Context, name string, conn InProcessConnection) error {
	if d.inprocessPool == nil {
		return fmt.Errorf("dispatch: inprocess transport not wired (no InProcessSubPool configured)")
	}
	if err := d.inprocessPool.RegisterServer(ctx, name, conn); err != nil {
		return err
	}
	d.mu.Lock()
	d.ownership[name] = "inprocess"
	d.mu.Unlock()
	return nil
}

// Open spawns all specs by routing each to the appropriate sub-pool.
// Failures are aggregated; a spec-level failure does not block others.
func (d *Pool) Open(ctx context.Context, specs []coremcp.ServerSpec) error {
	// Partition by transport.
	buckets := make(map[string][]coremcp.ServerSpec)
	for _, s := range specs {
		tag := transportFor(s)
		buckets[tag] = append(buckets[tag], s)
	}

	var (
		mu   sync.Mutex
		errs []string
		wg   sync.WaitGroup
	)

	for tag, bucket := range buckets {
		tag, bucket := tag, bucket
		wg.Add(1)
		go func() {
			defer wg.Done()
			sp := d.subPoolFor(tag)
			if sp == nil {
				mu.Lock()
				for _, s := range bucket {
					errs = append(errs, fmt.Sprintf("%s: unsupported transport %q (no sub-pool wired)", s.Name, tag))
				}
				mu.Unlock()
				return
			}
			if err := sp.Open(ctx, bucket); err != nil {
				mu.Lock()
				errs = append(errs, err.Error())
				mu.Unlock()
				return
			}
			d.mu.Lock()
			for _, s := range bucket {
				d.ownership[s.Name] = tag
			}
			d.mu.Unlock()
		}()
	}
	wg.Wait()
	if len(errs) > 0 {
		return fmt.Errorf("dispatch: %d error(s): %s", len(errs), joinErrors(errs))
	}
	return nil
}

// Close shuts down all sub-pools. Returns the first non-nil error.
func (d *Pool) Close(ctx context.Context) error {
	d.mu.Lock()
	d.ownership = make(map[string]string)
	d.mu.Unlock()

	var first error
	for _, sp := range d.activePools() {
		if err := sp.Close(ctx); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Tools aggregates tool lists from all sub-pools. Per-pool errors are
// swallowed so a pool with no servers yet doesn't fail the aggregate.
func (d *Pool) Tools(ctx context.Context) ([]coremcp.Tool, error) {
	var out []coremcp.Tool
	for _, sp := range d.activePools() {
		tools, err := sp.Tools(ctx)
		if err != nil {
			// Non-fatal: one dead sub-pool should not hide the others.
			continue
		}
		out = append(out, tools...)
	}
	return out, nil
}

// SetCallObserver installs a metadata-only observer invoked with
// (server, tool) on every Call dispatch. Pass nil to clear. The observer
// never sees call arguments or results.
func (d *Pool) SetCallObserver(fn func(server, tool string)) {
	d.mu.Lock()
	d.callObserver = fn
	d.mu.Unlock()
}

// Call dispatches tools/call to the sub-pool that opened server.
func (d *Pool) Call(ctx context.Context, server, tool string, args json.RawMessage) (json.RawMessage, error) {
	d.mu.RLock()
	tag, ok := d.ownership[server]
	observer := d.callObserver
	d.mu.RUnlock()
	if observer != nil {
		observer(server, tool)
	}
	if !ok {
		return nil, fmt.Errorf("%w: %q", stdio.ErrServerNotFound, server)
	}
	sp := d.subPoolFor(tag)
	if sp == nil {
		return nil, fmt.Errorf("dispatch: sub-pool for transport %q no longer wired", tag)
	}
	return sp.Call(ctx, server, tool, args)
}

// ─── PoolController surface ───────────────────────────────────────────────────

// OpenOne adds a single server to the appropriate sub-pool.
// Returns an error when the spec's transport has no sub-pool wired.
func (d *Pool) OpenOne(ctx context.Context, spec coremcp.ServerSpec) error {
	tag := transportFor(spec)
	sp := d.subPoolFor(tag)
	if sp == nil {
		return fmt.Errorf("dispatch: unsupported transport %q for recipe %q (no sub-pool wired)", tag, spec.Name)
	}

	// For stdio we call OpenOne directly — it returns ErrServerExists
	// rather than silently re-spawning. For http/sse we use Open with a
	// single-element slice because those pools expose only the bulk Open surface.
	switch tag {
	case "stdio":
		if err := d.stdioPool.OpenOne(ctx, spec); err != nil {
			return err
		}
	default:
		if err := sp.Open(ctx, []coremcp.ServerSpec{spec}); err != nil {
			return err
		}
	}

	d.mu.Lock()
	d.ownership[spec.Name] = tag
	d.mu.Unlock()
	return nil
}

// CloseOne removes a single server from whichever sub-pool owns it.
// Returns stdio.ErrServerNotFound when no server with id is tracked.
//
// Ownership bookkeeping (connector-lifecycle-truth-01PMZ303 UNIT-6,
// spec.md §1.11 N-4): the ownership entry is deleted up front (under
// the lock, so a concurrent CloseOne(id) for the same id observes
// "not found" instead of racing this one into a double teardown
// dispatch) and RESTORED if the underlying close fails. Before this
// fix the entry was deleted unconditionally with no restore path, so
// a failed close on ANY transport — not just http/sse — silently
// forgot a server the pool had not actually torn down: Call/Tools
// would then treat it as gone even though its process/connection was
// still alive. The net externally-observable effect — present after
// a failed close, absent after a successful one — is the same
// contract the ownership-delete-after-success framing describes; this
// implementation keeps the up-front delete-under-lock so the
// concurrent-double-close guard the original code relied on does not
// regress.
func (d *Pool) CloseOne(ctx context.Context, id string) error {
	d.mu.Lock()
	tag, ok := d.ownership[id]
	if ok {
		delete(d.ownership, id)
	}
	d.mu.Unlock()

	if !ok {
		return fmt.Errorf("%w: %q", stdio.ErrServerNotFound, id)
	}

	if err := d.closeOneByTag(ctx, tag, id); err != nil {
		d.mu.Lock()
		d.ownership[id] = tag
		d.mu.Unlock()
		return err
	}
	return nil
}

// closeOneByTag dispatches CloseOne to the sub-pool tag names.
//
// Before UNIT-6 this switch's http/sse arm was comment-only ("http
// and sse pools do not yet expose a per-server CloseOne method") and
// fell through to the function's shared "return nil" tail — reporting
// success for a close that never happened. Every arm below now either
// really closes the server or returns an explicit error; there is no
// remaining path that reports success without having called a real
// CloseOne.
//
// A tag with no sub-pool wired, or a tag this switch does not
// recognise, returns an explicit error rather than silently
// succeeding. That combination should never occur for an id that made
// it into d.ownership — Open/OpenOne only record ownership after
// subPoolFor(tag) resolved to a non-nil sub-pool — so this is a
// defensive backstop against an internal invariant breaking, not a
// reachable production path.
func (d *Pool) closeOneByTag(ctx context.Context, tag, id string) error {
	switch tag {
	case "stdio":
		if d.stdioPool != nil {
			return d.stdioPool.CloseOne(ctx, id)
		}
	case "http":
		if d.httpPool != nil {
			return d.httpPool.CloseOne(ctx, id)
		}
	case "sse":
		if d.ssePool != nil {
			return d.ssePool.CloseOne(ctx, id)
		}
	case "inprocess":
		// Unlike http/sse, the inprocess sub-pool DOES expose a real
		// per-server CloseOne (InProcessSubPool.CloseOne) — an
		// in-process connection is cheap to tear down (it just closes
		// Go channels, no network/process teardown).
		if d.inprocessPool != nil {
			return d.inprocessPool.CloseOne(ctx, id)
		}
	}
	return fmt.Errorf("dispatch: no sub-pool wired to close %q (transport %q)", id, tag)
}

// RecipeStatus returns the live status snapshot for a server.
// For stdio servers it delegates to the stdioPool.RecipeStatus.
// For http/sse servers it synthesises a "running" status because
// those pools have no per-server status API yet.
func (d *Pool) RecipeStatus(id string) (stdio.RecipeStatus, bool) {
	d.mu.RLock()
	tag, ok := d.ownership[id]
	d.mu.RUnlock()
	if !ok {
		return stdio.RecipeStatus{}, false
	}

	switch tag {
	case "stdio":
		if d.stdioPool != nil {
			return d.stdioPool.RecipeStatus(id)
		}
	case "http", "sse", "inprocess":
		// Synthesised: the ownership entry proves the server was opened
		// (registered, for inprocess) successfully. None of these three
		// pools provide a richer per-server status today.
		return stdio.RecipeStatus{
			ID:        id,
			Enabled:   true,
			State:     string(transport.StateRunning),
			UpdatedAt: time.Now().UTC(),
		}, true
	}
	return stdio.RecipeStatus{}, false
}

// ServerTools returns the cached tool list for a server.
// Only stdio servers have a per-server tool cache today; http/sse/
// inprocess servers return nil (tools are fetched live via Tools()).
// This is a deliberate choice, not an oversight: an in-process
// server's tools/list round-trip is a few Go function calls, cheap
// enough that a cache buys nothing.
func (d *Pool) ServerTools(id string) []coremcp.Tool {
	d.mu.RLock()
	tag, ok := d.ownership[id]
	d.mu.RUnlock()
	if !ok {
		return nil
	}
	if tag == "stdio" && d.stdioPool != nil {
		return d.stdioPool.ServerTools(id)
	}
	return nil
}

// AllRecipeStatuses returns status snapshots from all sub-pools.
// This satisfies the mcp-health view's HealthPool interface.
func (d *Pool) AllRecipeStatuses() []stdio.RecipeStatus {
	var out []stdio.RecipeStatus
	if d.stdioPool != nil {
		if s := d.stdioPool.AllRecipeStatuses(); len(s) > 0 {
			out = append(out, s...)
		}
	}

	// Append synthesised statuses for http/sse/inprocess owned servers.
	d.mu.RLock()
	type entry struct{ id, tag string }
	var remote []entry
	for id, tag := range d.ownership {
		if tag == "http" || tag == "sse" || tag == "inprocess" {
			remote = append(remote, entry{id, tag})
		}
	}
	d.mu.RUnlock()

	for _, e := range remote {
		out = append(out, stdio.RecipeStatus{
			ID:        e.id,
			Enabled:   true,
			State:     string(transport.StateRunning),
			UpdatedAt: time.Now().UTC(),
		})
	}
	return out
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// activePools returns the non-nil sub-pools in a stable order.
func (d *Pool) activePools() []coremcp.Pool {
	var pools []coremcp.Pool
	if d.stdioPool != nil {
		pools = append(pools, d.stdioPool)
	}
	if d.httpPool != nil {
		pools = append(pools, d.httpPool)
	}
	if d.ssePool != nil {
		pools = append(pools, d.ssePool)
	}
	if d.inprocessPool != nil {
		pools = append(pools, d.inprocessPool)
	}
	return pools
}

// joinErrors joins a slice of error strings into a semicolon-separated string.
func joinErrors(errs []string) string {
	out := ""
	for i, e := range errs {
		if i > 0 {
			out += "; "
		}
		out += e
	}
	return out
}
