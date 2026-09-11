package http

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/mcp/transport"
)

// HealthProbe issues a `tools/list` JSON-RPC request on a recurring
// cadence to verify the HTTP-backed MCP server is still alive. It is
// the HTTP transport's analogue of the stdio ping pump — similar
// cadence (two consecutive failures trip an OnFailure callback),
// different payload (the MCP spec does not promise that HTTP servers
// respond to `ping`, so we use `tools/list` whose result is
// well-defined for any compliant server) — and, unlike stdio, NO
// restart: OnFailure logs and records state (below); nothing in this
// package or http/pool.go respawns the connection. stdio's real
// restart supervisor lives at
// core/mcp/transport/stdio/supervisor.go:8 (attemptRestart :75); http
// and sse have no equivalent today. (Corrected
// connector-lifecycle-truth-01PMZ303 UNIT-7/N-7 — this comment
// previously claimed the callback was wired "to the supervisor's
// restart machinery", which was never true; see spec.md §1.4.)
//
// The probe is goroutine-driven: Start spawns one goroutine that
// loops on a Ticker until Stop fires. A mutex guards the
// stopped-flag so Stop is idempotent — calling Stop twice is the
// same as calling it once. The SAME mutex (mu, below) also guards the
// probe's last-observed outcome (state/lastError/lastProbeAt) —
// UNIT-7's Half 1: before this, the probe recorded nothing
// (spec.md §1.4 — "not one of them records an outcome"), so
// http.Pool's status accessor had nothing real to read and
// dispatch.Pool synthesised a permanent "running" instead. Reads
// happen from Snapshot(), typically called from the RPC goroutine;
// writes happen from the probe goroutine on every tick. -race with
// CGO_ENABLED=1 catches an unguarded read/write pair, so every touch
// goes through mu.
//
// Tests inject a fake Ticker (transport.Ticker) so the cadence does
// not depend on wall-clock progress. The default Ticker comes from
// transport.NewRealTicker.
type HealthProbe struct {
	// Period is the inter-probe interval. Falls back to
	// transport.DefaultPingPeriod when zero. Negative values
	// disable the probe entirely (used by tests that drive Probe
	// manually).
	Period time.Duration

	// Timeout is the per-probe response deadline. Falls back to
	// transport.DefaultPingTimeout when zero.
	Timeout time.Duration

	// NewTicker is the ticker factory. Falls back to
	// transport.NewRealTicker when nil.
	NewTicker func(d time.Duration) transport.Ticker

	// Probe is the function the goroutine calls every tick. It
	// runs one tools/list round-trip and returns nil on success
	// or the underlying error otherwise. The default
	// implementation is bound by NewToolsListProbe; tests
	// substitute a stub.
	Probe func(ctx context.Context) error

	// OnFailure is called when two consecutive Probe calls fail
	// (or one Probe call returns a non-recoverable error such as
	// a context deadline). It logs and records state (see the type
	// doc comment's N-7 correction) — it does NOT restart anything;
	// tests assert the callback fired.
	OnFailure func(reason string)

	// OnStateChange, when set, fires synchronously from the probe
	// goroutine whenever the derived state (transport.StateRunning /
	// transport.StateFailed) differs from the previous tick's —
	// never on a tick that leaves the state unchanged, so a healthy
	// server ticking away does not fire this on every tick. previous
	// is "" before the first probe result. The Pool wires this to
	// notify its own health observer (connector-lifecycle-truth
	// UNIT-8's mcp:health-changed publisher); it is called with mu
	// already released, so it is safe for the callback to call back
	// into Snapshot().
	OnStateChange func(previous, current transport.State)

	// Logger records per-tick diagnostics. Nil silences output.
	Logger ConnectionLogger

	mu      sync.Mutex
	cancel  context.CancelFunc
	doneCh  chan struct{}
	stopped bool

	// state, lastError and lastProbeAt record the probe loop's last
	// observed outcome. Guarded by mu alongside the goroutine
	// lifecycle fields above (UNIT-7 Half 1 — see the type doc
	// comment). state is the zero value ("") until the first tick;
	// Snapshot() callers treat that as "no probe result yet", and
	// http.Pool's RecipeStatus maps it to transport.StateStarting —
	// Open() does no network I/O (see http/connection.go's doc
	// comment), so there is no earlier basis to claim "running".
	state       transport.State
	lastError   string
	lastProbeAt time.Time
}

// ProbeSnapshot is the probe loop's last recorded outcome, returned
// by Snapshot() as a copy taken under mu so callers never observe a
// partially-written record.
type ProbeSnapshot struct {
	State       transport.State
	LastError   string
	LastProbeAt time.Time
}

// Snapshot returns the probe's last recorded outcome. Safe to call
// from any goroutine, including before Start() — State is then "",
// meaning no probe has run yet.
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
	// Starting, not Running: Open() does no network I/O for this
	// transport (see http/connection.go's doc comment), so there is
	// no basis yet to claim the server answered anything. The first
	// tick below is what earns "running".
	p.state = transport.StateStarting

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
						logger.Debug("http.health.recovered")
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

				logger.Debug("http.health.failed",
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
					// tick while the state stays Failed — the
					// consecutive counter is about detecting the
					// NEXT trip, not re-announcing this one; p.state
					// (unlike this local) is not reset, so
					// Snapshot() keeps reporting Failed/LastError
					// until a probe actually succeeds again.
					consecutive = 0
				}
			}
		}
	}()
}

// Stop cancels the probe goroutine and waits for it to exit. Safe
// to call multiple times — the second and further calls return
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

// NewToolsListProbe binds a probe function that issues one
// tools/list JSON-RPC request through the given Connection. The
// supplied id source generates request ids so concurrent probes do
// not collide on the same envelope id.
//
// The probe is intentionally minimal: it builds a Request envelope,
// sends it, then drains exactly one response off the connection's
// inbound queue. This works because the Connection's dispatch
// goroutine pushes one response per Send. If the connection is
// already busy with an in-flight tools/call (the toolloop's main
// path), the probe-driven Recv may grab the call's response — the
// caller is expected to serialise probe runs against application
// traffic, which the Pool does by routing every response through a
// shared Router (WP01's transport.Router).
//
// In test-mode the probe runs against an isolated Connection
// instance so the response-snatching concern does not apply; the
// production wiring routes through Router.
func NewToolsListProbe(conn *Connection, idSource func() int64) func(ctx context.Context) error {
	if idSource == nil {
		// Default: a per-probe atomic counter — every probe gets a
		// unique id even across goroutines.
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
		// Wait for the response or the probe deadline.
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

// MarshalProbeRequest builds the canonical tools/list request body
// the probe writes on the wire. Exposed so tests can assert the
// exact envelope shape without reaching into the Connection's
// dispatch goroutine.
func MarshalProbeRequest(id int64) ([]byte, error) {
	return json.Marshal(transport.RequestEnvelope{
		JSONRPC: transport.JSONRPCVersion,
		ID:      id,
		Method:  transport.MethodToolsList,
	})
}
