package event

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/event/chain"
	"github.com/sigil-tech/kaneaz-harness/core/event/log"
	"github.com/sigil-tech/kaneaz-harness/core/event/redact"
)

// emitter is the concrete implementation of the Emitter interface.
//
// Pipeline (FR-001 / FR-005 / FR-016):
//
//  1. Validate kind grammar via the kind registry. Forward-compat:
//     well-formed but unregistered kinds are accepted (NFR-006); the
//     grammar check still runs.
//  2. Validate emitter id against the namespace allowlist (FR-017).
//  3. Run the redaction pipeline against the caller's payload. Per
//     C-004 the pipeline is non-bypassable; a nil/panicking pipeline
//     returns ErrRedactionBypassed and aborts before any persistence.
//  4. Canonicalize the redacted payload bytes for hashing (chain
//     package handles RFC-8785-shaped JCS encoding).
//  5. Compute payload hash via chain.PayloadHash (currently SHA-256;
//     the chain package documents the BLAKE3 deviation).
//  6. Look up the per-session chain head (Store.Head) and use it as
//     prev_hash. Empty session id keys against "" so headless events
//     form their own degenerate chain.
//  7. Mint a process-monotonic ULID via the public ParseULID/NewULID
//     pathway so ordering is stable even under same-millisecond bursts
//     (FR-016).
//  8. Persist via Store.Append under the backend's per-session lock
//     (single atomic write; the in-memory backend serializes via a
//     sync.Mutex per session id, the libSQL adapter will use a SQL
//     transaction with chain-head check). Emitter holds a process-
//     local emitMu to make the *whole* read-head/append cycle atomic
//     against concurrent goroutines that share the same emitter.
//
// emitMu vs the backend's per-session lock:
//
//	The backend already serializes appends per session via expectedHead
//	optimistic concurrency. We additionally take emitMu around the
//	read-head/compute-id/append sequence so that two goroutines on this
//	emitter cannot both read the same head and race to call Append —
//	which would surface as ErrChainHeadMismatch on one of them. This is
//	a single-writer tightening on top of the storage-level guard, not a
//	replacement for it.
type emitter struct {
	store      *log.Store
	pipeline   *redact.Pipeline
	defaultEID EmitterID
	clock      func() time.Time
	emitMu     sync.Mutex

	// observersMu guards observers. Observers are invoked synchronously
	// after a successful Append; failures inside an observer are
	// swallowed (the emitter never lets a downstream consumer fail an
	// append). Used by core/rpc/views/audit to mirror events onto the
	// Wails `audit:event` topic.
	observersMu sync.RWMutex
	observers   []func(Event)
}

// EmitterOption configures an emitter at construction time.
type EmitterOption func(*emitter)

// WithEmitterClock overrides the wall-clock source used to populate
// EmittedAt when AppendInput.EmittedAt is zero. Tests use this to pin
// time; production callers leave it at the default (time.Now).
func WithEmitterClock(clock func() time.Time) EmitterOption {
	return func(e *emitter) { e.clock = clock }
}

// WithDefaultEmitterID sets a fallback EmitterID. If AppendInput.EmitterID
// is empty the fallback is used; otherwise the per-call value wins. The
// fallback is also validated against the allowlist at construction.
func WithDefaultEmitterID(eid EmitterID) EmitterOption {
	return func(e *emitter) { e.defaultEID = eid }
}

// WithObserver registers a side-effect-only callback invoked
// synchronously after every successful Append. Observers are NOT in
// the persistence path: a panicking or slow observer cannot fail a
// write (the emitter recovers from observer panics; per-call latency
// is the observer's responsibility). Used by core/rpc/views/audit to
// mirror events onto the Wails `audit:event` topic without coupling
// core/event to the rpc layer (DIRECTIVE_001).
func WithObserver(fn func(Event)) EmitterOption {
	return func(e *emitter) {
		if fn == nil {
			return
		}
		e.observersMu.Lock()
		e.observers = append(e.observers, fn)
		e.observersMu.Unlock()
	}
}

// notifyObservers fires every registered observer with the published
// event. Panics inside an observer are recovered so a misbehaving
// downstream subscriber cannot poison the emitter. Synchronous by
// design: the streamBroker fans events out to its async pump, so
// observers should themselves never block.
func (e *emitter) notifyObservers(ev Event) {
	e.observersMu.RLock()
	obs := e.observers
	e.observersMu.RUnlock()
	for _, fn := range obs {
		func(fn func(Event)) {
			defer func() { _ = recover() }()
			fn(ev)
		}(fn)
	}
}

// NewEmitter constructs the production Emitter.
//
// Required dependencies:
//
//   - store: the persistence adapter (core/event/log.Store). Tests pass
//     log.NewStore(log.NewMemoryBackend()); production wires the libSQL
//     backend.
//   - pipeline: the redaction pipeline. Per C-004 a nil pipeline is
//     refused at construction; loaders MUST resolve the salt and build
//     the pipeline before instantiating the emitter.
//
// Returns an error if dependencies are missing or if a configured
// default emitter id is not allowlisted.
func NewEmitter(store *log.Store, pipeline *redact.Pipeline, opts ...EmitterOption) (Emitter, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: nil store", ErrInvalidArgument)
	}
	if pipeline == nil {
		return nil, fmt.Errorf("%w: nil pipeline", ErrRedactionBypassed)
	}
	e := &emitter{
		store:    store,
		pipeline: pipeline,
		clock:    time.Now,
	}
	for _, opt := range opts {
		opt(e)
	}
	if e.defaultEID != "" {
		if err := ValidateEmitterID(e.defaultEID); err != nil {
			return nil, err
		}
	}
	return e, nil
}

// Append implements Emitter.
func (e *emitter) Append(ctx context.Context, in AppendInput) (Event, error) {
	if e == nil {
		return Event{}, fmt.Errorf("%w: nil emitter", ErrInvalidArgument)
	}
	if err := ctx.Err(); err != nil {
		return Event{}, err
	}

	// 1. Resolve emitter id (per-call override > default > error).
	eid := in.EmitterID
	if eid == "" {
		eid = e.defaultEID
	}
	if err := ValidateEmitterID(eid); err != nil {
		return Event{}, err
	}

	// 2. Validate kind grammar. Unknown-but-well-formed kinds are
	//    accepted (NFR-006); only malformed grammar is rejected.
	if err := ValidateKind(in.Kind); err != nil {
		return Event{}, err
	}

	// 3. Run redaction pipeline. Per C-004 this is non-bypassable: a
	//    panic or nil pipeline maps to ErrRedactionBypassed and the
	//    append aborts before any persistence.
	redacted, summary, err := e.pipeline.Apply(in.Payload)
	if err != nil {
		// Wrap as ErrRedactionBypassed so consumers can errors.Is
		// against the public taxonomy without taking a hard
		// dependency on core/event/redact's sentinels.
		return Event{}, fmt.Errorf("%w: %s", ErrRedactionBypassed, err.Error())
	}

	// 4. Canonicalize the redacted bytes. Apply already returned
	//    canonical-shaped JSON (json.Marshal output post normalization)
	//    but we re-feed through chain.Canonical so the hashing input is
	//    byte-stable across struct vs map inputs.
	var anyVal any
	if err := json.Unmarshal(redacted, &anyVal); err != nil {
		return Event{}, fmt.Errorf("%w: post-redact unmarshal: %s", ErrInvalidArgument, err.Error())
	}
	canonical, err := chain.Canonical(anyVal)
	if err != nil {
		return Event{}, fmt.Errorf("%w: canonicalize: %s", ErrInvalidArgument, err.Error())
	}

	// 5. Hash the canonical bytes.
	payloadHash := chain.PayloadHash(canonical)

	// 6. Mint event id and resolve EmittedAt under the emit lock so
	//    concurrent emits on the same emitter get strictly increasing
	//    ids and cannot race on the head read.
	e.emitMu.Lock()
	defer e.emitMu.Unlock()

	// Look up per-session chain head. Headless events use sessionKey "".
	sessionKey := ""
	if in.SessionID != nil {
		sessionKey = string(*in.SessionID)
	}
	prevHash, _, _, err := e.store.Head(ctx, sessionKey)
	if err != nil {
		return Event{}, fmt.Errorf("event: head lookup: %w", err)
	}

	// Mint the event id.
	id, err := NewULID()
	if err != nil {
		return Event{}, fmt.Errorf("event: id: %w", err)
	}

	// Resolve EmittedAt.
	emittedAt := in.EmittedAt
	if emittedAt.IsZero() {
		emittedAt = e.clock()
	}

	// 7. Build the row and persist atomically against the chain head.
	summaryJSON, err := json.Marshal(RedactionSummary{
		MatchersFired:      summary.MatchersFired,
		FieldPathsRedacted: summary.FieldPathsRedacted,
		NoOp:               summary.NoOp,
		SaltFingerprint:    summary.SaltFingerprint,
	})
	if err != nil {
		return Event{}, fmt.Errorf("event: summary marshal: %w", err)
	}

	row := log.Row{
		EventID:          string(id),
		SessionID:        sessionKey,
		EmitterID:        string(eid),
		Kind:             string(in.Kind),
		EmittedAt:        emittedAt,
		Payload:          canonical,
		PayloadHash:      payloadHash,
		PrevHash:         prevHash,
		RedactionSummary: string(summaryJSON),
	}
	if err := e.store.Append(ctx, row, prevHash); err != nil {
		// Surface chain-head mismatch as ErrAppendOnly so consumers
		// classifying the error get a stable taxonomy. Keep the
		// underlying chain-head sentinel reachable via Unwrap.
		if errors.Is(err, log.ErrChainHeadMismatch) {
			return Event{}, fmt.Errorf("%w: %s", ErrAppendOnly, err.Error())
		}
		return Event{}, fmt.Errorf("event: append: %w", err)
	}

	out := Event{
		EventID:          id,
		EmitterID:        eid,
		Kind:             in.Kind,
		EmittedAt:        emittedAt,
		Payload:          json.RawMessage(canonical),
		PayloadHash:      payloadHash,
		PrevHash:         prevHash,
		RedactionSummary: RedactionSummary{
			MatchersFired:      summary.MatchersFired,
			FieldPathsRedacted: summary.FieldPathsRedacted,
			NoOp:               summary.NoOp,
			SaltFingerprint:    summary.SaltFingerprint,
		},
	}
	if in.SessionID != nil {
		// Copy so callers cannot mutate the returned event's session id
		// out from under us. The caller still owns its own *ULID.
		sid := *in.SessionID
		out.SessionID = &sid
	}

	// Side-effect-only fan-out — never on the persistence path.
	e.notifyObservers(out)

	return out, nil
}
