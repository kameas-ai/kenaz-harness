// Package audit declares the context-event kinds emitted to the
// append-only event log (plan §5.4). Per C-005 the event log is the
// canonical record; this package never writes its own store. The
// concrete event-log mission (event-log-01KQ1A3M) provides the
// [Emitter] implementation; until that lands the [Emitter] interface
// here is the only contract surface that crosses the boundary.
package audit

import (
	"context"
	"encoding/json"
	"time"

	pack "github.com/sigil-tech/kaneaz-harness/core/context/pack"
)

// Kind enumerates context-event kinds (FR-011, FR-012). The kinds
// mirror plan §5.4 1:1.
type Kind string

const (
	KindResolutionStarted   Kind = "context.resolution_started"
	KindPackFetched         Kind = "context.pack_fetched"
	KindPackVerified        Kind = "context.pack_verified"
	KindPackRejected        Kind = "context.pack_rejected"
	KindOverrideApplied     Kind = "context.override_applied"
	KindCacheServed         Kind = "context.cache_served"
	KindResolutionCompleted Kind = "context.resolution_completed"
	KindInjectionEmitted    Kind = "context.injection_emitted"
	KindScopeRevoked        Kind = "context.scope_revoked"
	KindUpdateAvailable     Kind = "context.update_available"
)

// Event is the wire shape passed to the event log. The concrete event-log
// mission accepts a richer envelope; this package speaks only the
// payload-and-kind shape and lets the host re-pack it as needed.
type Event struct {
	Kind    Kind            `json:"kind"`
	TS      time.Time       `json:"ts"`
	Payload json.RawMessage `json:"payload"`
}

// Emitter is the contract surface this package consumes. The event-log
// mission's Emitter implements this trivially. Zero-mutation guarantees
// (append-only, redaction pipeline) are owned by the host.
type Emitter interface {
	Emit(ctx context.Context, e Event) error
}

// ResolutionStartedPayload carries the signalling for FR-011.
type ResolutionStartedPayload struct {
	RequestID    string `json:"request_id"`
	LockfileHash string `json:"lockfile_hash"`
	LayerCount   int    `json:"layer_count"`
}

// PackFetchedPayload — channel + content hash + version (FR-011).
type PackFetchedPayload struct {
	Pack    pack.PackRef `json:"pack"`
	Channel string       `json:"channel"`
}

// PackVerifiedPayload carries provenance metadata (FR-003, NFR-003).
type PackVerifiedPayload struct {
	Pack       pack.PackRef `json:"pack"`
	AnchorID   string       `json:"anchor_id"`
	Algorithm  string       `json:"algorithm"`
	CacheState string       `json:"cache_state"`
	GraceState string       `json:"grace_state"`
}

// PackRejectedPayload carries the rejection reason (FR-003 / SC-003).
// Code is the trust mission's typed code (FR-017 taxonomy) — see
// core/context/verify.
type PackRejectedPayload struct {
	Pack    pack.PackRef `json:"pack"`
	Code    string       `json:"code"`
	Message string       `json:"message"`
}

// OverrideAppliedPayload (FR-008).
type OverrideAppliedPayload struct {
	EntryName string       `json:"entry_name"`
	Winner    pack.Layer   `json:"winner"`
	Loser     pack.Layer   `json:"loser"`
	WinnerPack pack.PackRef `json:"winner_pack"`
	LoserPack  pack.PackRef `json:"loser_pack"`
}

// CacheServedPayload — FR-007 / NFR-004.
type CacheServedPayload struct {
	SnapshotID string `json:"snapshot_id"`
	Mode       string `json:"mode"`
}

// ResolutionCompletedPayload — FR-011.
type ResolutionCompletedPayload struct {
	SnapshotID    string        `json:"snapshot_id"`
	Duration      time.Duration `json:"duration"`
	OverrideCount int           `json:"override_count"`
}

// InjectionEmittedPayload — FR-010, FR-012.
type InjectionEmittedPayload struct {
	SessionID  string `json:"session_id"`
	SnapshotID string `json:"snapshot_id"`
	AgentID    string `json:"agent_id,omitempty"`
}

// ScopeRevokedPayload — FR-016.
type ScopeRevokedPayload struct {
	Pack pack.PackRef `json:"pack"`
	Role string       `json:"role"`
}

// UpdateAvailablePayload — FR-017.
type UpdateAvailablePayload struct {
	Pack             pack.PackRef `json:"pack"`
	AvailableVersion string       `json:"available_version"`
	DiffSummary      string       `json:"diff_summary"`
}

// Marshal is a small convenience wrapper that builds an [Event] for any
// payload value with the current timestamp.
func Marshal(kind Kind, payload any, now time.Time) (Event, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return Event{}, err
	}
	return Event{Kind: kind, TS: now, Payload: raw}, nil
}

// Emit is a small convenience wrapper for callers that have a payload
// value but not a pre-built Event. Errors are returned to the caller —
// audit must never panic.
func Emit(ctx context.Context, em Emitter, kind Kind, payload any, now time.Time) error {
	if em == nil {
		return nil // emitter is optional; resolution can be silent in tests.
	}
	e, err := Marshal(kind, payload, now)
	if err != nil {
		return err
	}
	return em.Emit(ctx, e)
}
