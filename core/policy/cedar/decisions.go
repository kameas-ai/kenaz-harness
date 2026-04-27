package cedar

import (
	"sync"
	"time"

	cedar "github.com/cedar-policy/cedar-go"
)

// Decision is the harness-shaped record returned from Engine.Evaluate
// and surfaced by the policy RPC view's RecentDecisions endpoint. The
// shape is JSON-friendly so the frontend can render it without
// custom marshalling.
type Decision struct {
	// Outcome is allow / deny / not_applicable.
	Outcome Outcome `json:"outcome"`

	// Action is the action name (one of the Action* constants).
	Action string `json:"action"`

	// Principal / Resource are stringified EntityUIDs for audit
	// readability. Original Cedar UIDs are not retained; the strings
	// are sufficient for the audit panel and avoid coupling consumers
	// to cedar-go types.
	Principal string `json:"principal"`
	Resource  string `json:"resource"`

	// MatchedPolicy is the Cedar policy ID (filename + index) that
	// produced the decision. Empty for not_applicable outcomes.
	MatchedPolicy string `json:"matched_policy,omitempty"`

	// Reason is a human-readable explanation. For deny: the matching
	// forbid policy's id. For allow: the matching permit's id. For
	// not_applicable: the literal "no policy matched".
	Reason string `json:"reason,omitempty"`

	// EvaluatedAt is the wall-clock time of the decision, UTC.
	EvaluatedAt time.Time `json:"evaluated_at"`
}

// DecisionStore is the seam Engine writes decisions through. The
// in-memory implementation is the default; production wiring (telemetry
// span store or a dedicated SQLite policy_log table) implements the
// same interface. Engine treats Store errors as non-fatal — a logging
// failure must NEVER block an authorization decision.
type DecisionStore interface {
	// Append records one decision. MUST be safe for concurrent use.
	Append(d Decision)

	// Recent returns up to limit most-recent decisions, newest first.
	// The limit ceiling is implementation-defined; callers requesting
	// 0 or negative values get an empty slice.
	Recent(limit int) []Decision
}

// MemoryDecisionStore is the default in-memory store. It keeps the
// most-recent N decisions in a ring buffer; older entries fall off
// the back. The buffer size is a constructor argument so the harness
// can tune memory pressure without touching this file.
type MemoryDecisionStore struct {
	mu       sync.Mutex
	cap      int
	decisions []Decision
}

// NewMemoryDecisionStore returns a store retaining up to capacity
// decisions. capacity ≤ 0 falls back to 256 — enough to surface
// recent gate decisions in the audit panel without unbounded growth.
func NewMemoryDecisionStore(capacity int) *MemoryDecisionStore {
	if capacity <= 0 {
		capacity = 256
	}
	return &MemoryDecisionStore{cap: capacity}
}

// Append records one decision. When the store is at capacity the
// oldest entry is dropped. Concurrency-safe.
func (s *MemoryDecisionStore) Append(d Decision) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.decisions = append(s.decisions, d)
	if len(s.decisions) > s.cap {
		// Drop the oldest 25% in one shot rather than every-call
		// shifting; reduces per-call work in the hot path.
		drop := len(s.decisions) - s.cap
		s.decisions = s.decisions[drop:]
	}
}

// Recent returns up to limit most-recent decisions, newest first.
func (s *MemoryDecisionStore) Recent(limit int) []Decision {
	if limit <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.decisions) == 0 {
		return nil
	}
	if limit > len(s.decisions) {
		limit = len(s.decisions)
	}
	out := make([]Decision, limit)
	// Copy newest-first.
	for i := 0; i < limit; i++ {
		out[i] = s.decisions[len(s.decisions)-1-i]
	}
	return out
}

// fromCedar converts a cedar-go authorization result + the request
// context the engine constructed into a harness-shaped Decision. The
// helper is package-private — Engine is the only legitimate caller.
func fromCedar(
	action string,
	principal cedar.EntityUID,
	resource cedar.EntityUID,
	dec cedar.Decision,
	diag cedar.Diagnostic,
	defaultDeny bool,
) Decision {
	out := Decision{
		Action:      action,
		Principal:   principal.String(),
		Resource:    resource.String(),
		EvaluatedAt: time.Now().UTC(),
	}
	switch {
	case dec == cedar.Allow:
		out.Outcome = Allow
		if len(diag.Reasons) > 0 {
			out.MatchedPolicy = string(diag.Reasons[0].PolicyID)
			out.Reason = "permit policy matched"
		}
	case len(diag.Reasons) > 0:
		// A forbid policy explicitly matched.
		out.Outcome = Deny
		out.MatchedPolicy = string(diag.Reasons[0].PolicyID)
		out.Reason = "forbid policy matched"
	default:
		// No policy matched. Cedar reports Deny by default; the
		// harness layer reinterprets that as Allow (default-allow)
		// unless DefaultDeny is set.
		if defaultDeny {
			out.Outcome = Deny
			out.Reason = "no policy matched (fail-closed)"
		} else {
			out.Outcome = NotApplicable
			out.Reason = "no policy matched"
		}
	}
	return out
}
