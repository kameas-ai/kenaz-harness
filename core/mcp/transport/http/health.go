package http

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/mcp/transport"
)

// HealthProbe issues a `tools/list` JSON-RPC request on a recurring
// cadence to verify the HTTP-backed MCP server is still alive. It is
// the HTTP transport's analogue of the stdio ping pump — same
// contract (two consecutive failures trip an OnFailure callback the
// Pool wires to the supervisor's restart machinery), different
// payload (the MCP spec does not promise that HTTP servers respond
// to `ping`, so we use `tools/list` whose result is well-defined for
// any compliant server).
//
// The probe is goroutine-driven: Start spawns one goroutine that
// loops on a Ticker until Stop fires. A mutex guards the
// stopped-flag so Stop is idempotent — calling Stop twice is the
// same as calling it once.
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
	// a context deadline). The Pool wires this to a supervisor-
	// style restart; tests assert the callback fired.
	OnFailure func(reason string)

	// Logger records per-tick diagnostics. Nil silences output.
	Logger ConnectionLogger

	mu      sync.Mutex
	cancel  context.CancelFunc
	doneCh  chan struct{}
	stopped bool
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

	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.doneCh = make(chan struct{})

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
				if err == nil {
					if consecutive > 0 {
						logger.Debug("http.health.recovered")
					}
					consecutive = 0
					continue
				}
				consecutive++
				logger.Debug("http.health.failed",
					"err", err.Error(),
					"consecutive", consecutive,
				)
				if consecutive >= 2 {
					reason := "two consecutive tools/list probe failures"
					if onFailure != nil {
						onFailure(reason)
					}
					// Reset so we don't re-trip every subsequent
					// tick while the supervisor is still tearing
					// down the previous generation.
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
