package sse

import (
	"context"
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/mcp/transport"
)

// HealthProbe issues a `tools/list` JSON-RPC request on a recurring
// cadence to verify the SSE-backed MCP server is still alive. It
// mirrors the HTTP transport's HealthProbe (core/mcp/transport/http/
// health.go) field-for-field — connector-lifecycle-truth-01PMZ303
// UNIT-7's "mirror in sse" — with one structural difference: unlike
// http.Connection.Open (which does no network I/O — the first Send
// drives the first POST), sse.Connection.Open ALREADY performs a real
// GET against the stream endpoint (see connection.go's Open doc
// comment) before returning nil. So by the time a Pool starts this
// probe, the server has already answered once for real; Start()
// therefore seeds state as transport.StateRunning rather than
// StateStarting — see Start's doc comment.
//
// Before UNIT-7, the sse package had NO probe at all: CloseOne's doc
// comment said outright "the sse Pool has no per-server health-probe
// goroutine to stop today ... nothing calls Reconnect automatically".
// A stale SSE stream (server still answers the GET's initial headers
// but has stopped sending events, or a request/response round trip
// over the POST channel starts timing out) had no way to be detected
// short of the next real tool call failing. This probe closes that
// gap the same way the HTTP transport's does: an active tools/list
// round trip on a ticker, independent of whatever the passive stream
// scanner observes.
//
// Like http's probe, and unlike stdio's real restart supervisor
// (core/mcp/transport/stdio/supervisor.go:8, attemptRestart :75),
// OnFailure here does NOT restart anything — it records state (see
// the struct's field comments) for a status accessor to read.
type HealthProbe struct {
	// Period is the inter-probe interval. Falls back to
	// transport.DefaultPingPeriod when zero. Negative values disable
	// the probe entirely (used by tests that drive Probe manually).
	Period time.Duration

	// Timeout is the per-probe response deadline. Falls back to
	// transport.DefaultPingTimeout when zero.
	Timeout time.Duration

	// NewTicker is the ticker factory. Falls back to
	// transport.NewRealTicker when nil.
	NewTicker func(d time.Duration) transport.Ticker

	// Probe is the function the goroutine calls every tick. It runs
	// one tools/list round-trip and returns nil on success or the
	// underlying error otherwise. The default implementation is
	// bound by NewToolsListProbe; tests substitute a stub.
	Probe func(ctx context.Context) error

	// OnFailure is called when two consecutive Probe calls fail (or
	// one Probe call returns a non-recoverable error such as a
	// context deadline). It logs and records state — it does NOT
	// restart anything; tests assert the callback fired.
	OnFailure func(reason string)

	// OnStateChange, when set, fires synchronously from the probe
	// goroutine whenever the derived state
	// (transport.StateRunning / transport.StateFailed) differs from
	// the previous tick's. previous is "" before the first probe
	// result — which for sse, unlike http, never actually happens in
	// production: Start seeds state as StateRunning before spawning
	// the goroutine (see the type doc comment), so the first
	// observable transition (if any) is Running -> Failed, not "" ->
	// Running. The Pool wires this to notify its own health observer
	// (connector-lifecycle-truth UNIT-8's mcp:health-changed
	// publisher).
	OnStateChange func(previous, current transport.State)

	// Logger records per-tick diagnostics. Nil silences output.
	Logger ConnectionLogger

	mu      sync.Mutex
	cancel  context.CancelFunc
	doneCh  chan struct{}
	stopped bool

	// state, lastError and lastProbeAt record the probe loop's last
	// observed outcome. Guarded by mu alongside the goroutine
	// lifecycle fields above — mirrors http.HealthProbe's Half 1
	// (UNIT-7). Reads happen from Snapshot(), typically called from
	// the RPC goroutine; writes happen from the probe goroutine on
	// every tick.
	state       transport.State
	lastError   string
	lastProbeAt time.Time
}

// ProbeSnapshot is the probe loop's last recorded outcome, returned
// by Snapshot() as a copy taken under mu so callers never observe a
// partially-written record. Mirrors http.ProbeSnapshot.
type ProbeSnapshot struct {
	State       transport.State
	LastError   string
	LastProbeAt time.Time
}

// Snapshot returns the probe's last recorded outcome. Safe to call
// from any goroutine, including before Start() — State is then "",
// meaning no probe has run yet (never the production case; see the
// type doc comment).
func (p *HealthProbe) Snapshot() ProbeSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	return ProbeSnapshot{
		State:       p.state,
		LastError:   p.lastError,
		LastProbeAt: p.lastProbeAt,
	}
}

// Start spawns the probe goroutine. Subsequent Start calls are a
// noop until Stop has fired. Returns immediately — the probe runs
// asynchronously.
func (p *HealthProbe) Start() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return
	}
	if p.cancel != nil {
		return
	}

	period := p.Period
	if period == 0 {
		period = transport.DefaultPingPeriod
	}
	if period < 0 {
		// Probe disabled; mark stopped so Start is a noop.
		p.stopped = true
		return
	}
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = transport.DefaultPingTimeout
	}
	newTicker := p.NewTicker
	if newTicker == nil {
		newTicker = transport.NewRealTicker
	}
	probe := p.Probe
	if probe == nil {
		// No probe function wired — refuse to start. The Pool is
		// responsible for binding NewToolsListProbe before Start.
		p.stopped = true
		return
	}
	logger := p.Logger
	if logger == nil {
		logger = noopLogger{}
	}
	onFailure := p.OnFailure
	onStateChange := p.OnStateChange

	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.doneCh = make(chan struct{})
	// Running, not Starting: unlike http, sse.Connection.Open already
	// performed a real GET against the stream endpoint before the
	// Pool got this far (see the type doc comment) — there IS a basis
	// to claim the server answered.
	p.state = transport.StateRunning

	go func() {
		defer close(p.doneCh)
		ticker := newTicker(period)
		defer ticker.Stop()

		consecutive := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C():
				probeCtx, probeCancel := context.WithTimeout(ctx, timeout)
				err := probe(probeCtx)
				probeCancel()
				now := time.Now()

				if err == nil {
					if consecutive > 0 {
						logger.Debug("sse.health.recovered")
					}
					consecutive = 0

					p.mu.Lock()
					prev := p.state
					p.state = transport.StateRunning
					p.lastError = ""
					p.lastProbeAt = now
					p.mu.Unlock()

					if onStateChange != nil && prev != transport.StateRunning {
						onStateChange(prev, transport.StateRunning)
					}
					continue
				}
				consecutive++

				p.mu.Lock()
				p.lastError = err.Error()
				p.lastProbeAt = now
				p.mu.Unlock()

				logger.Debug("sse.health.failed",
					"err", err.Error(),
					"consecutive", consecutive,
				)
				if consecutive >= 2 {
					reason := "two consecutive tools/list probe failures"

					p.mu.Lock()
					prev := p.state
					p.state = transport.StateFailed
					p.mu.Unlock()

					if onFailure != nil {
						onFailure(reason)
					}
					if onStateChange != nil && prev != transport.StateFailed {
						onStateChange(prev, transport.StateFailed)
					}
					// Reset so we don't re-trip every subsequent
					// tick while the state stays Failed — mirrors
					// http.HealthProbe's identical comment.
					consecutive = 0
				}
			}
		}
	}()
}

// Stop cancels the probe goroutine and waits for it to exit. Safe to
// call multiple times — the second and further calls return
// immediately.
func (p *HealthProbe) Stop() {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	p.stopped = true
	cancel := p.cancel
	doneCh := p.doneCh
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if doneCh != nil {
		<-doneCh
	}
}

// NewToolsListProbe binds a probe function that issues one tools/list
// JSON-RPC request through the given Connection. The supplied id
// source generates request ids so concurrent probes do not collide on
// the same envelope id. Mirrors the http package's function of the
// same name — see its doc comment for the response-snatching caveat,
// which applies identically here (Send POSTs and returns once the
// ACK lands; the actual JSON-RPC response arrives asynchronously on
// the SSE stream and is drained via Recv on the same inboundCh
// application traffic uses).
func NewToolsListProbe(conn *Connection, idSource func() int64) func(ctx context.Context) error {
	if idSource == nil {
		var counter int64
		idSource = func() int64 {
			counter++
			return counter
		}
	}
	return func(ctx context.Context) error {
		id := idSource()
		req := transport.RequestEnvelope{
			JSONRPC: transport.JSONRPCVersion,
			ID:      id,
			Method:  transport.MethodToolsList,
		}
		if err := conn.Send(req); err != nil {
			return err
		}
		respCh := make(chan probeResult, 1)
		go func() {
			msg, err := conn.Recv()
			respCh <- probeResult{msg: msg, err: err}
		}()
		select {
		case res := <-respCh:
			if res.err != nil {
				return res.err
			}
			if res.msg.Error != nil {
				return res.msg.Error
			}
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

type probeResult struct {
	msg transport.RawMessage
	err error
}
