package sse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
	"github.com/kameas-ai/kenaz-harness/core/mcp/transport"
)

// PoolOptions is an alias onto the cross-transport options struct so
// the SSE pool consumes the same defaulted set the stdio and HTTP
// pools do.
type PoolOptions = transport.PoolOptions

// Pool is the SSE equivalent of stdio.Pool. It satisfies
// core/mcp.Pool: Open spawns one Connection per spec, Tools fans out
// tools/list across every server, Call routes tools/call to the named
// server. Servers are addressed by ServerSpec.Name.
//
// The Pool wraps the underlying transport.PoolOptions through
// ApplyDefaults so a caller can construct it with the zero value and
// still observe sane fallbacks.
type Pool struct {
	opts PoolOptions

	mu      sync.RWMutex
	servers map[string]*sseEntry
	closed  bool

	idCounter atomic.Int64
}

// sseEntry pairs a Connection with its reconnect loop and
// dispatch-serialisation mutex.
type sseEntry struct {
	id        string
	conn      *Connection
	reconnect *ReconnectLoop
	logger    ConnectionLogger

	// dispatchMu serialises Send/Recv pairs so a Send + its
	// matching Recv are never interleaved with another pair.
	dispatchMu sync.Mutex
}

// Compile-time witness that *Pool satisfies coremcp.Pool.
var _ coremcp.Pool = (*Pool)(nil)

// NewPool returns an empty Pool with cross-transport defaults applied.
// Open populates it.
func NewPool(opts PoolOptions) *Pool {
	opts.ApplyDefaults()
	return &Pool{
		opts:    opts,
		servers: make(map[string]*sseEntry),
	}
}

// Open spawns each spec concurrently. A failure on one spec is
// recorded and surfaced through err but does not poison the others.
// Specs whose transport is not "sse" are rejected — the SSE pool only
// handles SSE recipes.
func (p *Pool) Open(ctx context.Context, specs []coremcp.ServerSpec) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return errors.New("sse: pool closed")
	}
	p.mu.Unlock()

	if len(specs) == 0 {
		return nil
	}

	var (
		mu   sync.Mutex
		errs []string
		wg   sync.WaitGroup
	)
	for _, raw := range specs {
		spec := raw
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := p.openOne(ctx, spec); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Sprintf("%s: %v", spec.Name, err))
				mu.Unlock()
				p.opts.Logger.Warn("sse.open.failed", "server", spec.Name, "err", err.Error())
			}
		}()
	}
	wg.Wait()
	if len(errs) > 0 {
		return fmt.Errorf("sse: %d server(s) failed to open: %s", len(errs), strings.Join(errs, "; "))
	}
	return nil
}

// openOne validates the spec, builds a Connection, opens it, and
// registers it in the pool.
func (p *Pool) openOne(ctx context.Context, spec coremcp.ServerSpec) error {
	if spec.Name == "" {
		return errors.New("sse: spec.Name required")
	}
	if spec.Transport != "sse" {
		return fmt.Errorf("sse: unsupported transport %q", spec.Transport)
	}
	if strings.TrimSpace(spec.URL) == "" {
		return errors.New("sse: spec.URL required")
	}

	loggerAdapter := slogConnectionAdapter{logger: p.opts.Logger}

	connSpec := Spec{
		ID:               spec.Name,
		URL:              spec.URL,
		PostURL:          spec.PostURL,
		Env:              spec.Env,
		FirstByteTimeout: p.opts.FirstByteTimeout,
		InitTimeout:      p.opts.InitTimeout,
		PingPeriod:       p.opts.PingPeriod,
		PingTimeout:      p.opts.PingTimeout,
	}

	factory := &ConnectionFactory{
		Spec:   connSpec,
		Logger: loggerAdapter,
	}

	conn := NewConnection(connSpec, loggerAdapter)
	if err := conn.Open(ctx); err != nil {
		return err
	}

	reconnect := &ReconnectLoop{
		Factory: factory,
		Now:     p.opts.Now,
		Sleep:   p.opts.Sleep,
		Logger:  loggerAdapter,
	}

	entry := &sseEntry{
		id:        spec.Name,
		conn:      conn,
		reconnect: reconnect,
		logger:    loggerAdapter,
	}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		_ = conn.Close()
		return errors.New("sse: pool closed mid-open")
	}
	if existing, ok := p.servers[spec.Name]; ok {
		p.servers[spec.Name] = entry
		p.mu.Unlock()
		_ = existing.conn.Close()
		return nil
	}
	p.servers[spec.Name] = entry
	p.mu.Unlock()
	return nil
}

// ErrServerNotFound is returned by CloseOne when no server with the
// requested id is in the pool. Mirrors http.ErrServerNotFound — see
// that package's doc comment for why this is a distinct value from
// stdio.ErrServerNotFound rather than a shared import.
var ErrServerNotFound = errors.New("sse: server not in pool")

// CloseOne removes a single server and closes its connection. Unlike
// the http transport, the sse Pool has no per-server health-probe
// goroutine to stop today (ReconnectLoop is constructed per entry but
// is not itself goroutine-driven — nothing calls Reconnect
// automatically), so closing the connection is the whole of the
// teardown. If a probe/reconnect goroutine is added to this transport
// later, it must be stopped here too, mirroring http.Pool.CloseOne.
//
// Locking mirrors stdio.Pool.CloseOne and http.Pool.CloseOne: the map
// mutation happens under the pool lock and completes before the
// (potentially blocking) conn.Close() runs unlocked, so a concurrent
// CloseOne(id) for the same id observes ErrServerNotFound rather than
// racing this one into a double close.
//
// Returns ErrServerNotFound when id is not present.
func (p *Pool) CloseOne(ctx context.Context, id string) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return errors.New("sse: pool closed")
	}
	entry, ok := p.servers[id]
	if !ok {
		p.mu.Unlock()
		return fmt.Errorf("%w: %q", ErrServerNotFound, id)
	}
	delete(p.servers, id)
	p.mu.Unlock()

	return entry.conn.Close()
}

// Close fans out a Close to every entry.
func (p *Pool) Close(ctx context.Context) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	servers := p.servers
	p.servers = make(map[string]*sseEntry)
	p.mu.Unlock()

	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		first error
	)
	for _, entry := range servers {
		entry := entry
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := entry.conn.Close(); err != nil {
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

// Tools aggregates tool lists across every running entry. Each
// server's tools/list response is fetched on demand.
func (p *Pool) Tools(ctx context.Context) ([]coremcp.Tool, error) {
	p.mu.RLock()
	if p.closed {
		p.mu.RUnlock()
		return nil, errors.New("sse: pool closed")
	}
	entries := make([]*sseEntry, 0, len(p.servers))
	for _, e := range p.servers {
		entries = append(entries, e)
	}
	p.mu.RUnlock()

	out := make([]coremcp.Tool, 0)
	for _, entry := range entries {
		tools, err := p.toolsForEntry(ctx, entry)
		if err != nil {
			p.opts.Logger.Warn("sse.tools.list_failed", "server", entry.id, "err", err.Error())
			continue
		}
		out = append(out, tools...)
	}
	return out, nil
}

// toolsForEntry issues one tools/list against entry. dispatchMu
// serialises against concurrent Call traffic.
func (p *Pool) toolsForEntry(ctx context.Context, entry *sseEntry) ([]coremcp.Tool, error) {
	entry.dispatchMu.Lock()
	defer entry.dispatchMu.Unlock()

	id := p.idCounter.Add(1)
	req := transport.RequestEnvelope{
		JSONRPC: transport.JSONRPCVersion,
		ID:      id,
		Method:  transport.MethodToolsList,
	}
	if err := entry.conn.Send(req); err != nil {
		return nil, err
	}
	msg, err := recvWithCtx(ctx, entry.conn)
	if err != nil {
		return nil, err
	}
	if msg.Error != nil {
		return nil, msg.Error
	}
	var result transport.ToolsListResult
	if err := json.Unmarshal(msg.Result, &result); err != nil {
		return nil, fmt.Errorf("decode tools/list: %w", err)
	}
	out := make([]coremcp.Tool, 0, len(result.Tools))
	for _, td := range result.Tools {
		out = append(out, coremcp.Tool{
			Server:      entry.id,
			Name:        td.Name,
			Description: td.Description,
			InputSchema: td.InputSchema,
		})
	}
	return out, nil
}

// Call dispatches tools/call against the named server.
func (p *Pool) Call(ctx context.Context, server, tool string, args json.RawMessage) (json.RawMessage, error) {
	p.mu.RLock()
	if p.closed {
		p.mu.RUnlock()
		return nil, errors.New("sse: pool closed")
	}
	entry, ok := p.servers[server]
	p.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("sse: unknown server %q", server)
	}

	entry.dispatchMu.Lock()
	defer entry.dispatchMu.Unlock()

	id := p.idCounter.Add(1)
	req := transport.RequestEnvelope{
		JSONRPC: transport.JSONRPCVersion,
		ID:      id,
		Method:  transport.MethodToolsCall,
		Params: transport.ToolsCallParams{
			Name:      tool,
			Arguments: args,
		},
	}
	if err := entry.conn.Send(req); err != nil {
		return nil, err
	}
	msg, err := recvWithCtx(ctx, entry.conn)
	if err != nil {
		return nil, err
	}
	if msg.Error != nil {
		return nil, msg.Error
	}
	return json.RawMessage(msg.Result), nil
}

// recvWithCtx wraps Connection.Recv with a context guard so callers
// observe ctx cancellation within ~1 channel-op latency.
func recvWithCtx(ctx context.Context, conn *Connection) (transport.RawMessage, error) {
	type result struct {
		msg transport.RawMessage
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		msg, err := conn.Recv()
		resCh <- result{msg: msg, err: err}
	}()
	select {
	case r := <-resCh:
		return r.msg, r.err
	case <-ctx.Done():
		return transport.RawMessage{}, ctx.Err()
	}
}

// slogConnectionAdapter projects a *slog.Logger onto the
// ConnectionLogger interface.
type slogConnectionAdapter struct {
	logger interface {
		Warn(msg string, args ...any)
		Debug(msg string, args ...any)
	}
}

func (a slogConnectionAdapter) Warn(msg string, args ...any) {
	if a.logger == nil {
		return
	}
	a.logger.Warn(msg, args...)
}

func (a slogConnectionAdapter) Debug(msg string, args ...any) {
	if a.logger == nil {
		return
	}
	a.logger.Debug(msg, args...)
}
