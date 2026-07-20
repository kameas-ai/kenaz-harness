package http

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
// the HTTP pool consumes the same defaulted set the stdio pool does.
// Per-transport extensions live as additional fields here when they
// arrive — the WP03 baseline rides entirely on the shared subset.
type PoolOptions = transport.PoolOptions

// Pool is the HTTP equivalent of stdio.Pool. It satisfies
// core/mcp.Pool: Open spawns one Connection per spec, Tools fans out
// tools/list across every server, Call routes tools/call to the
// named server. Servers are addressed by ServerSpec.Name.
//
// The Pool wraps the underlying transport.PoolOptions through
// ApplyDefaults so a caller can construct it with the zero value
// and still observe sane fallbacks for ticker / clock / logger.
type Pool struct {
	opts PoolOptions

	mu      sync.RWMutex
	servers map[string]*serverEntry
	closed  bool

	idCounter atomic.Int64
}

// serverEntry pairs a Connection with its in-flight request map and
// health probe. The pool owns the lifetime of every entry.
type serverEntry struct {
	id     string
	conn   *Connection
	probe  *HealthProbe
	logger ConnectionLogger

	// dispatchMu serialises Send/Recv pairs so a probe-issued
	// tools/list does not race a tools/call. The HTTP transport's
	// inbound queue is FIFO; serialisation guarantees the Recv
	// after a Send observes that Send's response.
	dispatchMu sync.Mutex
}

// Compile-time witness that *Pool satisfies coremcp.Pool.
var _ coremcp.Pool = (*Pool)(nil)

// NewPool returns an empty Pool with cross-transport defaults
// applied. Open populates it.
func NewPool(opts PoolOptions) *Pool {
	opts.ApplyDefaults()
	return &Pool{
		opts:    opts,
		servers: make(map[string]*serverEntry),
	}
}

// Open spawns each spec concurrently. A failure on one spec is
// recorded and surfaced through err but does not poison the others.
// Specs whose transport is empty or "stdio" are rejected — the
// HTTP pool only handles HTTP recipes; the harness binds a
// transport-routing pool above this one in WP01.
func (p *Pool) Open(ctx context.Context, specs []coremcp.ServerSpec) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return errors.New("http: pool closed")
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
				p.opts.Logger.Warn("http.open.failed", "server", spec.Name, "err", err.Error())
			}
		}()
	}
	wg.Wait()
	if len(errs) > 0 {
		return fmt.Errorf("http: %d server(s) failed to open: %s", len(errs), strings.Join(errs, "; "))
	}
	return nil
}

// openOne validates the spec, builds a Connection, opens it, and
// starts the health probe. The pool's HTTP path is "lazy": Open
// does NOT issue a probe round-trip on its own — the first probe
// fires when the ticker first ticks. This keeps boot-time fast
// when a Pool spawns dozens of HTTP recipes.
func (p *Pool) openOne(ctx context.Context, spec coremcp.ServerSpec) error {
	if spec.Name == "" {
		return errors.New("http: spec.Name required")
	}
	if spec.Transport != "http" {
		return fmt.Errorf("http: unsupported transport %q", spec.Transport)
	}
	if strings.TrimSpace(spec.URL) == "" {
		return errors.New("http: spec.URL required")
	}

	connSpec := Spec{
		ID:              spec.Name,
		URL:             spec.URL,
		HeadersTemplate: spec.HeadersTemplate,
		Env:             spec.Env,
		FirstByteTimeout: p.opts.FirstByteTimeout,
		InitTimeout:      p.opts.InitTimeout,
		PingPeriod:       p.opts.PingPeriod,
		PingTimeout:      p.opts.PingTimeout,
	}
	// HeadersTemplate is now passed from ServerSpec.HeadersTemplate.
	// Values may contain ${VAR} tokens that Connection.Open substitutes
	// from Env at connection-open time. The Authorization header is
	// redacted in diagnostic logs by the transport layer.

	loggerAdapter := slogConnectionAdapter{logger: p.opts.Logger}

	conn := NewConnection(connSpec, loggerAdapter)
	if err := conn.Open(ctx); err != nil {
		return err
	}

	entry := &serverEntry{
		id:     spec.Name,
		conn:   conn,
		logger: loggerAdapter,
	}
	entry.probe = &HealthProbe{
		Period:    p.opts.PingPeriod,
		Timeout:   p.opts.PingTimeout,
		NewTicker: p.opts.NewTicker,
		Probe: NewToolsListProbe(conn, func() int64 {
			return p.idCounter.Add(1)
		}),
		OnFailure: func(reason string) {
			p.opts.Logger.Warn("http.health.tripped", "server", spec.Name, "reason", reason)
		},
		Logger: loggerAdapter,
	}
	entry.probe.Start()

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		entry.probe.Stop()
		_ = conn.Close()
		return errors.New("http: pool closed mid-open")
	}
	if existing, ok := p.servers[spec.Name]; ok {
		p.servers[spec.Name] = entry
		p.mu.Unlock()
		existing.probe.Stop()
		_ = existing.conn.Close()
		return nil
	}
	p.servers[spec.Name] = entry
	p.mu.Unlock()
	return nil
}

// Close fans out a Close to every entry. After Close returns, all
// dispatch and probe goroutines have exited.
func (p *Pool) Close(ctx context.Context) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	servers := p.servers
	p.servers = make(map[string]*serverEntry)
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
			entry.probe.Stop()
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
// server's tools/list response is fetched on demand — the HTTP
// transport does not cache between calls because the recipe author
// may rotate tools server-side at runtime.
func (p *Pool) Tools(ctx context.Context) ([]coremcp.Tool, error) {
	p.mu.RLock()
	if p.closed {
		p.mu.RUnlock()
		return nil, errors.New("http: pool closed")
	}
	entries := make([]*serverEntry, 0, len(p.servers))
	for _, e := range p.servers {
		entries = append(entries, e)
	}
	p.mu.RUnlock()

	out := make([]coremcp.Tool, 0)
	for _, entry := range entries {
		tools, err := p.toolsForEntry(ctx, entry)
		if err != nil {
			p.opts.Logger.Warn("http.tools.list_failed", "server", entry.id, "err", err.Error())
			continue
		}
		out = append(out, tools...)
	}
	return out, nil
}

// toolsForEntry issues one tools/list against entry and projects
// the response onto coremcp.Tool. dispatchMu serialises against
// concurrent Call traffic + the health probe so the Recv after Send
// observes the right response.
func (p *Pool) toolsForEntry(ctx context.Context, entry *serverEntry) ([]coremcp.Tool, error) {
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
		return nil, errors.New("http: pool closed")
	}
	entry, ok := p.servers[server]
	p.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("http: unknown server %q", server)
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
// observe ctx cancellation within ~1 channel-op latency. Spawns a
// goroutine for the Recv side; on ctx.Done the response (if any)
// is dropped — the next Send will land it back on the queue and
// the dispatcher will surface it as an orphan response, which is
// fine because the HTTP transport doesn't share a queue across
// recipes.
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
// ConnectionLogger interface so the Pool's slog instance flows
// through to the per-Connection diagnostics path.
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
