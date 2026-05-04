package stdio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	coremcp "github.com/sigil-tech/kaneaz-harness/core/mcp"
	"github.com/sigil-tech/kaneaz-harness/core/mcp/transport"
	"golang.org/x/sync/errgroup"
)

// PoolOptions configures a *Pool. WP02 collapses this onto the
// shared transport.PoolOptions so the cross-transport defaults
// (clock, sleep, ticker, ping cadence, init/first-byte deadlines,
// handler hooks) are applied uniformly across stdio, HTTP, and SSE.
//
// The alias keeps source compatibility with the existing stdio
// callers — fields are addressed by the same names — while letting
// transport.PoolOptions.ApplyDefaults stay the single source of
// truth for nil-pointer / zero-value fall-back behaviour.
type PoolOptions = transport.PoolOptions

// Pool is the real, full-featured stdio MCP pool. It implements
// core/mcp.Pool. Servers are addressed by ServerSpec.Name; the
// transport must be "stdio" — any other value is rejected at Open.
type Pool struct {
	opts PoolOptions

	mu      sync.RWMutex
	servers map[string]*ServerInstance
	closed  bool
}

// Compile-time witness that *Pool satisfies coremcp.Pool. WP05
// swaps the fixture-backed adapter in core/rpc/api.go for one
// constructed around this type.
var _ coremcp.Pool = (*Pool)(nil)

// NewPool returns an empty pool with cross-transport defaults
// applied via transport.PoolOptions.ApplyDefaults. Open populates
// the pool.
func NewPool(opts PoolOptions) *Pool {
	opts.ApplyDefaults()
	return newPool(opts)
}

// NewPoolBare returns an empty pool WITHOUT applying transport-level
// defaults. Callers that have already merged their own defaults (e.g.
// the WP07 Test-Connection one-shot path that wires custom hooks)
// can use this to avoid double-application.
//
// Production callers should prefer NewPool. NewPoolBare is the
// escape hatch for callers that have already filled the option
// fields they care about and don't want the transport package's
// defaults to overwrite future zero-value semantics they intend to
// observe.
func NewPoolBare(opts PoolOptions) *Pool {
	return newPool(opts)
}

// newPool is the shared constructor; both NewPool and NewPoolBare
// route through it so the only behaviour difference is whether
// ApplyDefaults ran.
func newPool(opts PoolOptions) *Pool {
	return &Pool{
		opts:    opts,
		servers: make(map[string]*ServerInstance),
	}
}

// Open spawns each spec concurrently. A failure on one spec is
// recorded (and surfaced through err) but does not poison the
// others — callers iterate Tools / Call against the servers that
// did come up. The returned error is the aggregated list of
// per-spec failures; full per-spec status will be exposed through
// WP02's RecipeStatus.
func (p *Pool) Open(ctx context.Context, specs []coremcp.ServerSpec) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return errors.New("stdio: pool closed")
	}
	p.mu.Unlock()

	if len(specs) == 0 {
		return nil
	}

	var (
		mu   sync.Mutex
		errs []string
	)
	// errgroup.WithContext is intentionally NOT used here: its
	// derived ctx cancels when Wait returns, which would kill every
	// spawned process via exec.CommandContext. We want spawns to
	// outlive Open. Cancellation/abort of the parent ctx still
	// reaches each spawn through the ctx parameter we forward in.
	g := new(errgroup.Group)
	for _, raw := range specs {
		spec := raw
		g.Go(func() error {
			if err := p.openOne(ctx, spec); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Sprintf("%s: %v", spec.Name, err))
				mu.Unlock()
				p.opts.Logger.Warn("stdio.open.failed", "server", spec.Name, "err", err.Error())
				return nil // do not abort the errgroup on a per-spec failure
			}
			return nil
		})
	}
	_ = g.Wait()
	if len(errs) > 0 {
		return fmt.Errorf("stdio: %d server(s) failed to open: %s", len(errs), strings.Join(errs, "; "))
	}
	return nil
}

// openOne is the per-spec helper. Validates the spec, builds a
// ServerInstance, performs the handshake, and registers it on
// success.
func (p *Pool) openOne(ctx context.Context, spec coremcp.ServerSpec) error {
	if spec.Name == "" {
		return errors.New("stdio: spec.Name required")
	}
	if spec.Transport != "" && spec.Transport != "stdio" {
		return fmt.Errorf("stdio: unsupported transport %q", spec.Transport)
	}
	if len(spec.Command) == 0 {
		return errors.New("stdio: spec.Command required")
	}
	inst := newServerInstance(
		spec.Name,
		p.opts.Logger,
		p.opts.Sampler,
		p.opts.Roots,
		p.opts.Broker,
		instanceOptions{
			Now:       p.opts.Now,
			Sleep:     p.opts.Sleep,
			NewTicker: p.opts.NewTicker,
		},
	)
	sspec := SpawnSpec{
		ID:               spec.Name,
		Command:          spec.Command,
		Env:              spec.Env,
		FirstByteTimeout: p.opts.FirstByteTimeout,
		InitTimeout:      p.opts.InitTimeout,
		PingPeriod:       p.opts.PingPeriod,
		PingTimeout:      p.opts.PingTimeout,
		// SamplingEnabled is recipe-level state owned by WP03+; the
		// fixture/test path leaves it false. Pool callers that need
		// sampling at WP03+ time will use the recipe-aware Open path.
		SamplingEnabled: false,
	}
	if err := inst.Spawn(ctx, sspec); err != nil {
		return err
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		_ = inst.Close(ctx)
		return errors.New("stdio: pool closed mid-open")
	}
	if existing, ok := p.servers[spec.Name]; ok {
		// Race against a concurrent re-open of the same name —
		// shouldn't happen with the spec set Open is given, but be
		// defensive.
		p.servers[spec.Name] = inst
		p.mu.Unlock()
		_ = existing.Close(ctx)
		return nil
	}
	p.servers[spec.Name] = inst
	p.mu.Unlock()
	return nil
}

// Close fans out a Close to every instance. The first error is
// returned but every instance is given a chance to exit. After
// Close returns, all reader/stderr-pump goroutines have exited.
func (p *Pool) Close(ctx context.Context) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	servers := p.servers
	p.servers = make(map[string]*ServerInstance)
	p.mu.Unlock()

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		first error
	)
	for _, inst := range servers {
		inst := inst
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := inst.Close(ctx); err != nil && !isExpectedExit(err) {
				mu.Lock()
				if first == nil {
					first = err
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return first
}

// Tools aggregates tool lists across every running instance.
// Closed-pool returns an error so callers don't silently get an
// empty list.
func (p *Pool) Tools(ctx context.Context) ([]coremcp.Tool, error) {
	p.mu.RLock()
	if p.closed {
		p.mu.RUnlock()
		return nil, errors.New("stdio: pool closed")
	}
	insts := make([]*ServerInstance, 0, len(p.servers))
	for _, inst := range p.servers {
		insts = append(insts, inst)
	}
	p.mu.RUnlock()
	out := make([]coremcp.Tool, 0)
	for _, inst := range insts {
		out = append(out, inst.Tools()...)
	}
	return out, nil
}

// Call dispatches tools/call against the named server. Returns an
// error when the server is unknown or the pool is closed.
func (p *Pool) Call(ctx context.Context, server, tool string, args json.RawMessage) (json.RawMessage, error) {
	p.mu.RLock()
	if p.closed {
		p.mu.RUnlock()
		return nil, errors.New("stdio: pool closed")
	}
	inst, ok := p.servers[server]
	p.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("stdio: unknown server %q", server)
	}
	return inst.CallTool(ctx, tool, args)
}

// Server returns the named instance for callers that need to read
// status (Tools, Negotiated, StderrTail) without going through the
// public Pool surface. nil if absent.
func (p *Pool) Server(name string) *ServerInstance {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.servers[name]
}

// ErrServerExists is returned by OpenOne when a server with the
// requested ID is already present in the pool. Callers (the rpc
// view) should CloseOne first if they want to replace the instance.
var ErrServerExists = errors.New("stdio: server already in pool")

// ErrServerNotFound is returned by CloseOne when no server with the
// requested ID exists in the pool. The rpc view treats this as a
// non-fatal "already gone" outcome.
var ErrServerNotFound = errors.New("stdio: server not in pool")

// OpenOne adds a single server to a running pool. It mirrors Open's
// per-spec spawn path but operates on one spec at a time, surfacing
// the spawn error directly instead of folding it into the aggregate
// "N server(s) failed to open" envelope.
//
// Restart-history semantics: a fresh ServerInstance is allocated on
// every OpenOne call, so its restartHistory starts empty even when
// CloseOne was called minutes ago for the same id. This matches the
// user's mental model — "I just toggled this recipe back on, give it
// the full backoff budget."
//
// Returns ErrServerExists when the pool already has a server with
// spec.Name. The caller must CloseOne first to replace it; this is
// the contract WP05's UninstallRecipe + InstallRecipe round-trips
// rely on.
func (p *Pool) OpenOne(ctx context.Context, spec coremcp.ServerSpec) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return errors.New("stdio: pool closed")
	}
	if _, exists := p.servers[spec.Name]; exists {
		p.mu.Unlock()
		return fmt.Errorf("%w: %q", ErrServerExists, spec.Name)
	}
	p.mu.Unlock()
	return p.openOne(ctx, spec)
}

// CloseOne removes a single server from a running pool. The instance
// is closed (SIGTERM grace per ServerInstance.Close, then SIGKILL on
// the closeGrace deadline), every reader / supervisor / health-pinger
// goroutine exits before this returns, and the instance is dropped
// from the pool's server map so its restartHistory is released along
// with it.
//
// Returns ErrServerNotFound when no server with id exists. The rpc
// view treats this as a non-fatal "already gone" — UninstallRecipe
// is allowed to be called twice.
func (p *Pool) CloseOne(ctx context.Context, id string) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return errors.New("stdio: pool closed")
	}
	inst, ok := p.servers[id]
	if !ok {
		p.mu.Unlock()
		return fmt.Errorf("%w: %q", ErrServerNotFound, id)
	}
	delete(p.servers, id)
	p.mu.Unlock()
	if err := inst.Close(ctx); err != nil && !isExpectedExit(err) {
		return err
	}
	return nil
}

// isExpectedExit treats common "process exited with status N"
// shapes as non-errors during Close. exec.ExitError on a SIGTERM/
// SIGKILL exit is expected when the server doesn't gracefully
// honor stdin EOF.
func isExpectedExit(err error) bool {
	if err == nil {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "signal: killed") ||
		strings.Contains(msg, "signal: terminated") ||
		strings.Contains(msg, "exit status")
}

// ServerTools returns the cached tool list for the named server. It
// returns nil when the server is not in the pool (including when the
// pool is closed). This is the per-recipe counterpart to the
// aggregate Tools() method; it is used by the recipe install path to
// discover which tools need Cedar permit snippets.
func (p *Pool) ServerTools(id string) []coremcp.Tool {
	p.mu.RLock()
	inst, ok := p.servers[id]
	p.mu.RUnlock()
	if !ok {
		return nil
	}
	return inst.Tools()
}

// SetSamplingEnabled flips the per-server sampling gate. When off,
// the reader-loop dispatch path returns -32601 to the server
// without invoking the SamplingHandler. This is the user's consent
// boundary — see the cost-amplification risk note in
// transport/sampling.go.
func (p *Pool) SetSamplingEnabled(serverID string, on bool) {
	p.mu.RLock()
	inst, ok := p.servers[serverID]
	p.mu.RUnlock()
	if !ok {
		return
	}
	inst.setSamplingEnabled(on)
}

// setSamplingEnabled mutates the per-instance sampling gate under
// the instance's lock so the reader loop's read of samplingOn
// observes a consistent value.
func (s *ServerInstance) setSamplingEnabled(on bool) {
	s.mu.Lock()
	s.samplingOn = on
	s.mu.Unlock()
}

// AllRecipeStatuses returns a snapshot of every server's RecipeStatus in
// the pool. Used by the health-snapshot RPC (mcp-server-health-ui WP01) to
// return the full picture in a single round-trip. Closed pool returns nil.
func (p *Pool) AllRecipeStatuses() []RecipeStatus {
	p.mu.RLock()
	if p.closed {
		p.mu.RUnlock()
		return nil
	}
	ids := make([]string, 0, len(p.servers))
	for id := range p.servers {
		ids = append(ids, id)
	}
	p.mu.RUnlock()

	out := make([]RecipeStatus, 0, len(ids))
	for _, id := range ids {
		if s, ok := p.RecipeStatus(id); ok {
			out = append(out, s)
		}
	}
	return out
}

// SamplingEnabled reads the per-server gate. Test-only convenience.
func (s *ServerInstance) SamplingEnabled() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.samplingOn
}
