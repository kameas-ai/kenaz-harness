// Package dispatch provides a transport-routing MCP pool that fans out
// Open/OpenOne/Call/Tools/Close/CloseOne operations to the correct
// sub-pool based on ServerSpec.Transport:
//
//	""/"stdio"  → stdioPool (typically *stdio.Pool)
//	"http"      → httpPool  (typically *mcphttp.Pool)
//	"sse"       → ssePool   (typically *sse.Pool)
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

// Pool fans out recipe-open/call operations to the correct transport
// sub-pool based on ServerSpec.Transport.
//
// Each sub-pool is optional — pass nil for transports the caller does not
// need. An OpenOne call for a nil sub-pool's transport returns an error.
//
// Pool is safe for concurrent use.
type Pool struct {
	stdioPool StdioSubPool
	httpPool  coremcp.Pool
	ssePool   coremcp.Pool

	// ownership maps a server id to the transport tag that opened it.
	// Guarded by mu.
	mu        sync.RWMutex
	ownership map[string]string // id → "stdio" | "http" | "sse"
}

// Options bundles the three sub-pools. Nil entries are tolerated;
// the caller is responsible for wiring at least the stdio pool so existing
// recipes keep working.
//
// HTTP and SSE are expressed as concrete types so callers do not need to
// import the interface type; the dispatch package converts them to the
// internal coremcp.Pool interface internally.
type Options struct {
	Stdio StdioSubPool
	HTTP  *mcphttp.Pool
	SSE   *sse.Pool
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
	}
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

// Call dispatches tools/call to the sub-pool that opened server.
func (d *Pool) Call(ctx context.Context, server, tool string, args json.RawMessage) (json.RawMessage, error) {
	d.mu.RLock()
	tag, ok := d.ownership[server]
	d.mu.RUnlock()
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

	switch tag {
	case "stdio":
		if d.stdioPool != nil {
			return d.stdioPool.CloseOne(ctx, id)
		}
	default:
		// http and sse pools do not yet expose a per-server CloseOne method.
		// We drop the ownership entry (done above) and return nil; the
		// connection is torn down on sub-pool Close at process shutdown.
		// A future revision should add CloseOne to the http/sse pool surface.
	}
	return nil
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
	case "http", "sse":
		// Synthesised: the ownership entry proves the server was opened
		// successfully. The http/sse pools provide no richer per-server
		// status today.
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
// Only stdio servers have a per-server tool cache today; http/sse
// servers return nil (tools are fetched live via Tools()).
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

	// Append synthesised statuses for http/sse owned servers.
	d.mu.RLock()
	type entry struct{ id, tag string }
	var remote []entry
	for id, tag := range d.ownership {
		if tag == "http" || tag == "sse" {
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
