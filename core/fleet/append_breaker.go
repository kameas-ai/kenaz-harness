package fleet

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// AppendStatusError is a non-2xx answer to POST /api/v1/context/append.
// Typed so the append breaker can tell "the remote context does not exist"
// (404 — permanent until something changes) from a transient failure.
// Error() keeps the historical text ("fleet: context append status N").
type AppendStatusError struct {
	Status int
	// ProxyPage marks a 404 that arrived as an HTML/XML page (load
	// balancer / CDN / SPA fallback): transient, not "remote missing".
	ProxyPage bool
}

func (e *AppendStatusError) Error() string {
	return fmt.Sprintf("fleet: context append status %d", e.Status)
}

// AppendBreaker bounds the per-message context-sync append
// (fleet-session-truth-01DOGF0A FR-6, dogfood F7).
//
// The append hook fires on every persisted history message. It used to log
// rpc.context_sync.append_event_failed and carry on — so a 404 for a remote
// context that did not exist was re-posted, and re-logged, for every message
// the user typed, while every surface showed the session as "Synced to
// fleet". The breaker:
//
//   - opens the circuit immediately on a permanent answer (404 missing remote
//     context, 401/403, expired session): no further posts for that session
//     until it is reset (sync re-enabled / disabled, sign-in);
//   - backs off exponentially on transient failures (30s → 10m) and opens
//     after appendMaxConsecutiveFailures in a row — a transient-open circuit
//     half-opens: one probe per appendBackoffMax, a success closes it;
//   - ignores caller-context cancellation (not a fleet failure);
//   - logs WARN once per transition (first failure, circuit open), Debug for
//     everything held back;
//   - reports every failing session into the shared SyncLanes board, so the
//     FleetSession snapshot carries it to a visible badge.
//
// Events held back are NOT queued for later: they are counted as Dropped and
// shown — the honest version of what already happened silently.
type AppendBreaker struct {
	mu       sync.Mutex
	lanes    *SyncLanes
	now      func() time.Time
	sessions map[string]*appendState
	anyOK    bool
	// unsupported latches when the server has no event-stream route at all
	// (a plain 404 — ErrEndpointUnsupported). Not a failure: the lane goes
	// Off with reasonEndpointUnsupported, no session accrues breaker state,
	// and nothing is retried (the client short-circuits before any HTTP).
	// Cleared by ResetAll (sign-in / sign-out), alongside the client latch.
	unsupported bool
}

// reasonEndpointUnsupported is the context-sync lane reason when the fleet
// server has no /api/v1/context/append route (verified absent on kenaz-fleet
// main, 2026-10-05). Events stay local.
const reasonEndpointUnsupported = "fleet_endpoint_unsupported"

type appendState struct {
	failures int
	open     bool
	// permanent: the circuit opened on an answer retrying cannot change
	// (404 / 401 / 403 / expired) — latched until Reset. A circuit opened by
	// transient failures half-opens: one probe every appendBackoffMax
	// (review F4).
	permanent bool
	nextRetry time.Time
	reason    string
	lastErr   string
	dropped   int
}

const (
	appendBackoffBase            = 30 * time.Second
	appendBackoffMax             = 10 * time.Minute
	appendMaxConsecutiveFailures = 5
)

// NewAppendBreaker returns a breaker reporting into lanes (may be nil).
func NewAppendBreaker(lanes *SyncLanes) *AppendBreaker {
	return &AppendBreaker{lanes: lanes, now: time.Now, sessions: map[string]*appendState{}}
}

// Do runs one append through the breaker. fn is not called while the
// session's circuit is open or inside its backoff window. ErrFleetDisabled
// is not a failure (fleet off / nop client). Returns fn's error, or nil
// when the attempt was held back.
func (b *AppendBreaker) Do(ctx context.Context, sessionID string, fn func(context.Context) error) error {
	if b == nil {
		return fn(ctx)
	}
	if !b.allow(sessionID) {
		logging.L().Debug("rpc.context_sync.append_held_back", "session_id", shortID(sessionID))
		// Refresh the board's Dropped count. Not a state transition
		// (SyncLanes ignores Dropped for change detection), so no event.
		b.publish()
		return nil
	}
	err := fn(ctx)
	if errors.Is(err, ErrFleetDisabled) {
		return nil
	}
	if errors.Is(err, ErrEndpointUnsupported) {
		b.markUnsupported(sessionID)
		return nil // events stay local; not a breaker failure
	}
	if err != nil && ctx.Err() != nil && errors.Is(err, context.Canceled) {
		// The CALLER cancelled (a user-stopped turn, shutdown): says nothing
		// about fleet. Not a failure — no backoff, no circuit (review F3).
		return err
	}
	b.record(sessionID, err)
	return err
}

// markUnsupported latches the whole lane unsupported (once) and drops any
// per-session breaker state — those failures predate the discovery that the
// route does not exist, and retrying them can never help.
func (b *AppendBreaker) markUnsupported(sessionID string) {
	b.mu.Lock()
	first := !b.unsupported
	b.unsupported = true
	delete(b.sessions, sessionID)
	b.mu.Unlock()
	if first {
		b.publish()
	}
}

// Reset clears a session's breaker state (sync toggled, sign-in).
func (b *AppendBreaker) Reset(sessionID string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	_, had := b.sessions[sessionID]
	delete(b.sessions, sessionID)
	b.mu.Unlock()
	if had {
		b.publish()
	}
}

// ResetAll clears every session (sign-out / sign-in).
func (b *AppendBreaker) ResetAll() {
	if b == nil {
		return
	}
	b.mu.Lock()
	had := len(b.sessions) > 0
	wasUnsupported := b.unsupported
	b.sessions = map[string]*appendState{}
	b.unsupported = false
	b.mu.Unlock()
	if wasUnsupported && b.lanes != nil {
		// Back to "not yet attempted": the next append re-probes the route.
		b.lanes.Set(LaneContextSync, LaneSnapshot{Status: LaneUnknown})
		return
	}
	if had {
		b.publish()
	}
}

func (b *AppendBreaker) allow(sessionID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.sessions[sessionID]
	if !ok {
		return true
	}
	if (st.open && st.permanent) || b.now().Before(st.nextRetry) {
		st.dropped++
		return false
	}
	// Backoff elapsed, or a transient-open circuit's half-open probe.
	return true
}

func (b *AppendBreaker) record(sessionID string, err error) {
	b.mu.Lock()
	if err == nil {
		_, had := b.sessions[sessionID]
		delete(b.sessions, sessionID)
		first := !b.anyOK
		b.anyOK = true
		b.mu.Unlock()
		if had || first {
			b.publish()
		}
		return
	}
	st, ok := b.sessions[sessionID]
	if !ok {
		st = &appendState{}
		b.sessions[sessionID] = st
	}
	st.failures++
	st.lastErr = err.Error()
	reason, permanent := classifyAppendError(err)
	st.reason = reason
	wasOpen := st.open
	if permanent {
		st.open = true
		st.permanent = true
		st.nextRetry = time.Time{}
	} else if st.failures >= appendMaxConsecutiveFailures {
		st.open = true
		st.nextRetry = b.now().Add(appendBackoffMax) // half-open probe
	} else {
		d := appendBackoffBase << (st.failures - 1)
		if d > appendBackoffMax || d <= 0 {
			d = appendBackoffMax
		}
		st.nextRetry = b.now().Add(d)
	}
	logWarn := st.failures == 1 || (st.open && !wasOpen)
	failures, open := st.failures, st.open
	b.mu.Unlock()

	if logWarn {
		logging.L().Warn("rpc.context_sync.append_event_failed",
			"session_id", shortID(sessionID),
			"err", err.Error(),
			"reason", reason,
			"consecutive", failures,
			"circuit_open", open,
		)
	} else {
		logging.L().Debug("rpc.context_sync.append_event_failed",
			"session_id", shortID(sessionID), "reason", reason, "consecutive", failures)
	}
	b.publish()
}

// classifyAppendError names a failure and says whether retrying the same
// post can ever help.
func classifyAppendError(err error) (reason string, permanent bool) {
	var se *AppendStatusError
	if errors.As(err, &se) {
		switch {
		case se.Status == http.StatusNotFound && se.ProxyPage:
			return "fleet_api_not_routed", false
		case se.Status == http.StatusNotFound:
			// Only an enveloped (JSON {code}) 404 reaches here; a plain
			// route-level 404 is ErrEndpointUnsupported, handled in Do.
			return "remote_context_missing", true
		case se.Status == http.StatusUnauthorized || se.Status == http.StatusForbidden:
			return "not_authorized", true
		case se.Status >= 500:
			return "server_error", false
		case se.Status == http.StatusTooManyRequests:
			// A rate limit says "later", not "never" (delta review #2).
			return "rate_limited", false
		case se.Status == http.StatusRequestTimeout:
			return "network", false
		case se.Status == http.StatusRequestEntityTooLarge:
			// Fleet contract (2026-10-06): content problems come back as
			// 200 + a per-event report; a 413 means THIS body can never be
			// accepted, so re-posting it is pointless. Permanent.
			return "payload_too_large", true
		default:
			return fmt.Sprintf("status_%d", se.Status), true
		}
	}
	switch {
	case errors.Is(err, ErrTokenExpired), errors.Is(err, ErrNotSignedIn):
		return "session_expired", true
	case errors.Is(err, context.DeadlineExceeded):
		return "network", false
	}
	return "network", false
}

// publish projects every failing session into the context-sync lane.
func (b *AppendBreaker) publish() {
	if b.lanes == nil {
		return
	}
	b.mu.Lock()
	if b.unsupported {
		b.mu.Unlock()
		b.lanes.Set(LaneContextSync, LaneSnapshot{Status: LaneOff, Reason: reasonEndpointUnsupported})
		return
	}
	ids := make([]string, 0, len(b.sessions))
	for id := range b.sessions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var sessions []LaneSessionSnapshot
	maxFail := 0
	reason, lastErr := "", ""
	for _, id := range ids {
		st := b.sessions[id]
		sessions = append(sessions, LaneSessionSnapshot{
			SessionID:           id,
			Reason:              st.reason,
			LastError:           st.lastErr,
			ConsecutiveFailures: st.failures,
			Open:                st.open,
			NextRetryAt:         st.nextRetry,
			Dropped:             st.dropped,
		})
		if st.failures > maxFail {
			maxFail, reason, lastErr = st.failures, st.reason, st.lastErr
		}
	}
	b.mu.Unlock()
	cur := b.lanes.Snapshot(LaneContextSync)
	if len(sessions) == 0 {
		b.lanes.Set(LaneContextSync, LaneSnapshot{Status: LaneOK, LastSuccessAt: b.now()})
		return
	}
	b.lanes.Set(LaneContextSync, LaneSnapshot{
		Status:              LaneDegraded,
		Reason:              reason,
		LastError:           lastErr,
		ConsecutiveFailures: maxFail,
		LastSuccessAt:       cur.LastSuccessAt,
		Sessions:            sessions,
	})
}
