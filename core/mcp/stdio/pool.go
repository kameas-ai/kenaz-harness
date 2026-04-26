package stdio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	coremcp "github.com/sigil-tech/kaneaz-harness/core/mcp"
	"golang.org/x/sync/errgroup"
)

// PoolOptions configures a *Pool. Sampler / Roots / Broker are the
// WP03 hooks; nil values are accepted and cause the pool to no-op
// or reply with -32601 as appropriate. Logger defaults to
// slog.Default.
type PoolOptions struct {
	Sampler SamplingHandler
	Roots   RootsHandler
	Broker  EventPublisher
	Logger  *slog.Logger

	// FirstByteTimeout overrides defaultFirstByteTimeout for every
	// spawn the pool initiates. Recipes can still override per-
	// instance via SpawnSpec.
	FirstByteTimeout time.Duration
	// InitTimeout overrides defaultInitTimeout for every spawn.
	InitTimeout time.Duration
}

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

// NewPool returns an empty pool. Open populates it. Logger
// defaults to slog.Default when nil.
func NewPool(opts PoolOptions) *Pool {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
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
	inst := newServerInstance(spec.Name, p.opts.Logger, p.opts.Sampler, p.opts.Roots, p.opts.Broker)
	sspec := SpawnSpec{
		ID:               spec.Name,
		Command:          spec.Command,
		Env:              spec.Env,
		FirstByteTimeout: p.opts.FirstByteTimeout,
		InitTimeout:      p.opts.InitTimeout,
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
