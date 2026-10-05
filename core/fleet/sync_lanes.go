package fleet

import (
	"reflect"
	"sync"
	"time"
)

// SyncLanes is the shared, in-memory status board for the fleet background
// lanes whose failures used to be log-only (fleet-session-truth-01DOGF0A
// FR-6 / FR-7): context-sync append (dogfood F7 — a 404 that repeated per
// message, silently), the unit read-down poll (B3b) and OTLP telemetry
// reconcile (B3a — "no resource-owner claim", logged every tick, never
// shown).
//
// Producers report into it; the FleetSession snapshot reads it; a change in
// a lane's *state* (not its timestamps) fires OnChange so the snapshot can
// be pushed to the UI. Deliberately NOT persisted: a lane's health is a fact
// about this process's recent attempts, and a restart re-derives it within
// one attempt. (WP-PI: no persistence surface.)
type SyncLanes struct {
	mu       sync.Mutex
	lanes    map[LaneName]LaneSnapshot
	onChange []func()
}

// LaneName identifies one background sync lane.
type LaneName string

// The lanes FleetSession.sync reports.
const (
	LaneContextSync LaneName = "context_sync"
	LaneUnitPoll    LaneName = "unit_poll"
	LaneTelemetry   LaneName = "telemetry"
)

// LaneStatus is a lane's coarse health.
type LaneStatus string

const (
	// LaneUnknown — the lane has not attempted anything this process.
	LaneUnknown LaneStatus = "unknown"
	// LaneOK — the last attempt succeeded.
	LaneOK LaneStatus = "ok"
	// LaneDegraded — attempts are failing; Reason names why.
	LaneDegraded LaneStatus = "degraded"
	// LaneOff — the lane is deliberately not running (consent none, not
	// entitled, signed out). Not a failure; Reason names why.
	LaneOff LaneStatus = "off"
)

// LaneSnapshot is one lane's state.
type LaneSnapshot struct {
	Status              LaneStatus
	Reason              string
	LastError           string
	ConsecutiveFailures int
	LastSuccessAt       time.Time
	NextRetryAt         time.Time
	// Sessions carries per-session breaker state for the context-sync lane
	// (only sessions that are currently failing or circuit-open). Empty for
	// the other lanes.
	Sessions []LaneSessionSnapshot
}

// LaneSessionSnapshot is the per-session context-sync breaker state.
type LaneSessionSnapshot struct {
	SessionID           string
	Reason              string
	LastError           string
	ConsecutiveFailures int
	// Open is true when the circuit is open: no further appends are
	// attempted for this session until it is reset (sign-in, re-enable).
	Open        bool
	NextRetryAt time.Time
	// Dropped counts history events that were NOT sent because the breaker
	// held them back. They are lost for sync — reported, not hidden.
	Dropped int
}

// NewSyncLanes returns an empty board (every lane "unknown").
func NewSyncLanes() *SyncLanes {
	return &SyncLanes{lanes: map[LaneName]LaneSnapshot{}}
}

// Snapshot returns a copy of one lane's state.
func (l *SyncLanes) Snapshot(name LaneName) LaneSnapshot {
	if l == nil {
		return LaneSnapshot{Status: LaneUnknown}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return copyLane(l.lanes[name])
}

// OnChange registers fn to run after a lane's state transitions. fn runs on
// the reporting goroutine, outside the board's lock.
func (l *SyncLanes) OnChange(fn func()) {
	if l == nil || fn == nil {
		return
	}
	l.mu.Lock()
	l.onChange = append(l.onChange, fn)
	l.mu.Unlock()
}

// Set replaces a lane's state, firing OnChange when the state (everything
// but the timestamps) changed.
func (l *SyncLanes) Set(name LaneName, snap LaneSnapshot) {
	if l == nil {
		return
	}
	if snap.Status == "" {
		snap.Status = LaneUnknown
	}
	l.mu.Lock()
	prev, had := l.lanes[name]
	l.lanes[name] = copyLane(snap)
	changed := !had || !sameLaneState(prev, snap)
	fns := append([]func(){}, l.onChange...)
	l.mu.Unlock()
	if changed {
		for _, fn := range fns {
			fn()
		}
	}
}

// RecordSuccess marks a lane healthy.
func (l *SyncLanes) RecordSuccess(name LaneName) {
	if l == nil {
		return
	}
	cur := l.Snapshot(name)
	l.Set(name, LaneSnapshot{Status: LaneOK, LastSuccessAt: time.Now(), Sessions: cur.Sessions})
}

// RecordFailure marks a lane degraded with a named reason and the running
// consecutive-failure count. nextRetry may be zero.
func (l *SyncLanes) RecordFailure(name LaneName, reason string, err error, consecutive int, nextRetry time.Time) {
	if l == nil {
		return
	}
	cur := l.Snapshot(name)
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	l.Set(name, LaneSnapshot{
		Status:              LaneDegraded,
		Reason:              reason,
		LastError:           msg,
		ConsecutiveFailures: consecutive,
		LastSuccessAt:       cur.LastSuccessAt,
		NextRetryAt:         nextRetry,
		Sessions:            cur.Sessions,
	})
}

// RecordOff marks a lane deliberately off (not a failure).
func (l *SyncLanes) RecordOff(name LaneName, reason string) {
	if l == nil {
		return
	}
	cur := l.Snapshot(name)
	l.Set(name, LaneSnapshot{Status: LaneOff, Reason: reason, LastSuccessAt: cur.LastSuccessAt, Sessions: cur.Sessions})
}

// Reset clears every lane back to "unknown" (sign-out: nothing this process
// learned about the previous session's lanes is true of the next one).
func (l *SyncLanes) Reset() {
	if l == nil {
		return
	}
	l.mu.Lock()
	changed := len(l.lanes) > 0
	l.lanes = map[LaneName]LaneSnapshot{}
	fns := append([]func(){}, l.onChange...)
	l.mu.Unlock()
	if changed {
		for _, fn := range fns {
			fn()
		}
	}
}

func copyLane(s LaneSnapshot) LaneSnapshot {
	if s.Status == "" {
		s.Status = LaneUnknown
	}
	if len(s.Sessions) > 0 {
		s.Sessions = append([]LaneSessionSnapshot(nil), s.Sessions...)
	} else {
		s.Sessions = nil
	}
	return s
}

// sameLaneState compares the transition-relevant fields: timestamps move on
// every attempt and are not, by themselves, a change worth pushing.
func sameLaneState(a, b LaneSnapshot) bool {
	if a.Status != b.Status || a.Reason != b.Reason || a.LastError != b.LastError ||
		a.ConsecutiveFailures != b.ConsecutiveFailures || len(a.Sessions) != len(b.Sessions) {
		return false
	}
	strip := func(in []LaneSessionSnapshot) []LaneSessionSnapshot {
		out := make([]LaneSessionSnapshot, len(in))
		for i, s := range in {
			s.NextRetryAt = time.Time{}
			out[i] = s
		}
		return out
	}
	return reflect.DeepEqual(strip(a.Sessions), strip(b.Sessions))
}
