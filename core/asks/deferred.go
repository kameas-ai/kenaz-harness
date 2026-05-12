// Package asks provides the deferred-ask registry for async elicitation
// (ask-user-question-interactive-01KZNP3G WP06).
//
// Deferred asks allow the model to fire-and-forget a question while
// continuing its turn. The user answers in their own time; the answer is
// delivered as a system_reminder message on the session's next turn.
//
// Key invariants:
//   - A session may have at most MaxConcurrentDeferred (5) in-flight
//     deferred asks at once.
//   - Deferred asks expire (default 24 h) if not answered; on expiry the
//     ask is marked declined with reason "expired".
//   - The registry is in-memory; process restart clears it. Persistence is
//     left for a follow-up (requires the SQLite asks table from the full
//     core/asks/ registry).
package asks

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// MaxConcurrentDeferred is the maximum number of in-flight deferred asks
// per session (spec FR-027).
const MaxConcurrentDeferred = 5

// DefaultExpiry is the default TTL for a deferred ask (spec FR-026).
const DefaultExpiry = 24 * time.Hour

// Status values for a DeferredAsk.
const (
	StatusPending  = "pending"
	StatusAnswered = "answered"
	StatusDeclined = "declined"
	StatusExpired  = "expired"
)

// ErrTooManyPending is returned when a session already has
// MaxConcurrentDeferred in-flight asks.
var ErrTooManyPending = errors.New("asks: too many concurrent pending deferred asks")

// ErrUnknownAsk is returned when the asked-for AskID is not registered.
var ErrUnknownAsk = errors.New("asks: unknown or already-resolved ask id")

// DeferredAsk is a single in-flight deferred elicitation.
type DeferredAsk struct {
	AskID      string
	SessionID  string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	Status     string
	Answer     any    // non-nil when Status == StatusAnswered
	DeclineReason string // non-empty when Status == StatusDeclined
}

// DeferredRegistry tracks deferred asks in memory.
// Safe for concurrent use.
type DeferredRegistry struct {
	mu     sync.Mutex
	asks   map[string]*DeferredAsk // keyed by AskID
	expiry time.Duration
}

// NewDeferredRegistry returns a registry with the given expiry TTL.
// Pass DefaultExpiry for the standard 24-hour behaviour.
func NewDeferredRegistry(expiry time.Duration) *DeferredRegistry {
	if expiry <= 0 {
		expiry = DefaultExpiry
	}
	return &DeferredRegistry{
		asks:   make(map[string]*DeferredAsk),
		expiry: expiry,
	}
}

// Register creates a new deferred ask for a session.
// Returns ErrTooManyPending when the session already has
// MaxConcurrentDeferred asks in StatusPending.
func (r *DeferredRegistry) Register(sessionID, askID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Count pending asks for this session.
	pending := 0
	for _, a := range r.asks {
		if a.SessionID == sessionID && a.Status == StatusPending {
			pending++
		}
	}
	if pending >= MaxConcurrentDeferred {
		return ErrTooManyPending
	}

	now := time.Now()
	r.asks[askID] = &DeferredAsk{
		AskID:     askID,
		SessionID: sessionID,
		CreatedAt: now,
		ExpiresAt: now.Add(r.expiry),
		Status:    StatusPending,
	}
	return nil
}

// Answer records the user's answer for a pending deferred ask.
// Returns ErrUnknownAsk when the ask is not found or already resolved.
func (r *DeferredRegistry) Answer(askID string, answer any) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	a, ok := r.asks[askID]
	if !ok || a.Status != StatusPending {
		return ErrUnknownAsk
	}
	a.Status = StatusAnswered
	a.Answer = answer
	return nil
}

// Decline marks a pending ask as declined with the given reason.
// Returns ErrUnknownAsk when the ask is not found or already resolved.
func (r *DeferredRegistry) Decline(askID, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	a, ok := r.asks[askID]
	if !ok || a.Status != StatusPending {
		return ErrUnknownAsk
	}
	a.Status = StatusDeclined
	a.DeclineReason = reason
	return nil
}

// Get retrieves a DeferredAsk by ID. Returns ErrUnknownAsk when not found.
func (r *DeferredRegistry) Get(askID string) (DeferredAsk, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	a, ok := r.asks[askID]
	if !ok {
		return DeferredAsk{}, ErrUnknownAsk
	}
	return *a, nil
}

// ListPending returns all pending asks for a session.
func (r *DeferredRegistry) ListPending(sessionID string) []DeferredAsk {
	r.mu.Lock()
	defer r.mu.Unlock()

	var out []DeferredAsk
	for _, a := range r.asks {
		if a.SessionID == sessionID && a.Status == StatusPending {
			out = append(out, *a)
		}
	}
	return out
}

// SweepExpired marks all pending asks past their ExpiresAt as expired.
// Returns the number of asks swept.
func (r *DeferredRegistry) SweepExpired() int {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()

	n := 0
	for _, a := range r.asks {
		if a.Status == StatusPending && now.After(a.ExpiresAt) {
			a.Status = StatusExpired
			a.DeclineReason = "expired"
			n++
		}
	}
	return n
}

// SystemReminderText returns the system_reminder text that the harness
// injects into the next LLM turn for an answered deferred ask (spec FR-025).
func SystemReminderText(askID string, answer any) string {
	return fmt.Sprintf("Pending elicitation %q answered: %v", askID, answer)
}
