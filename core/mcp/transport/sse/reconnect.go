package sse

import (
	"context"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/mcp/transport"
)

// ReconnectPool wraps a ConnectionFactory and re-establishes the SSE
// stream on EOF, using the same restart-history backoff machinery as
// the stdio supervisor (transport.BackoffSchedule /
// transport.RestartWindow).
//
// The Reconnect loop is driven by the Pool: when Recv returns io.EOF
// (stream closed by server), the pool calls Reconnect to get a fresh
// Connection. The restart history limits how often reconnects may
// happen within a rolling window; beyond the schedule the server is
// marked failed.
type ReconnectLoop struct {
	// Factory builds a fresh Connection per reconnect attempt.
	Factory *ConnectionFactory

	// Now is the clock-injection hook. Nil → time.Now.
	Now func() time.Time

	// Sleep is the reconnect backoff sleep. Nil → time.Sleep.
	Sleep func(d time.Duration)

	// OnFailed is called when the restart history is exhausted and
	// the reconnect loop gives up. Optional.
	OnFailed func(reason string)

	// Logger for diagnostics. Nil silences output.
	Logger ConnectionLogger

	// history records the timestamps of recent reconnect attempts in
	// the rolling RestartWindow.
	history []time.Time
}

// Reconnect attempts to open a fresh SSE Connection using the
// restart-history backoff. It prunes the history window, applies the
// appropriate backoff delay, then opens a new connection.
//
// Returns the open connection on success, or an error when the
// restart schedule is exhausted (three attempts in RestartWindow).
func (r *ReconnectLoop) Reconnect(ctx context.Context) (*Connection, error) {
	now := r.now()
	r.history = transport.PruneHistory(r.history, now, transport.RestartWindow)

	n := len(r.history)
	if n >= len(transport.BackoffSchedule) {
		reason := "sse: too many reconnect attempts within restart window"
		if r.OnFailed != nil {
			r.OnFailed(reason)
		}
		if r.Logger != nil {
			r.Logger.Warn("sse.reconnect.exhausted",
				"server", r.Factory.Spec.ID,
				"attempts_in_window", n,
			)
		}
		return nil, errReconnectExhausted
	}

	delay := transport.BackoffSchedule[n]
	if r.Logger != nil {
		r.Logger.Debug("sse.reconnect.backoff",
			"server", r.Factory.Spec.ID,
			"attempt", n+1,
			"delay", delay.String(),
		)
	}

	r.sleep(delay)
	r.history = append(r.history, r.now())

	conn, err := r.Factory.NewConnection(r.Factory.Spec.ID)
	if err != nil {
		return nil, err
	}
	if err := conn.Open(ctx); err != nil {
		return nil, err
	}
	return conn.(*Connection), nil
}

// ResetHistory clears the restart history. Called by the pool when a
// connection has been stable long enough that past failures should not
// count against future reconnect budget.
func (r *ReconnectLoop) ResetHistory() {
	r.history = r.history[:0]
}

// now returns the current time via the injected Now hook or time.Now.
func (r *ReconnectLoop) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// sleep sleeps for d via the injected Sleep hook or time.Sleep.
func (r *ReconnectLoop) sleep(d time.Duration) {
	if r.Sleep != nil {
		r.Sleep(d)
		return
	}
	time.Sleep(d)
}

// errReconnectExhausted is the sentinel returned when the backoff
// schedule is exhausted.
var errReconnectExhausted = errReconnectExhaustedType{}

type errReconnectExhaustedType struct{}

func (errReconnectExhaustedType) Error() string {
	return "sse: reconnect schedule exhausted"
}
