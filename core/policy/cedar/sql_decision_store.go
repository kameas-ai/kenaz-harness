package cedar

import (
	"context"
	"database/sql"
	"log/slog"
	"sync"
	"time"
)

// DefaultSQLDecisionRetention bounds SQLDecisionStore to the most
// recent N decisions, both in the durable table and in the in-memory
// hot cache. 5,000 is a deliberate choice, not the prior 256-entry
// transient default carried forward: Evaluate fires on every gated
// action (tool calls, bash commands, filesystem ops, credential
// access), so a single busy session can produce hundreds of decisions.
// 5,000 rows covers many sessions of audit history while keeping the
// worst-case table size small (each row is a handful of short strings
// plus an int64 timestamp — a few hundred bytes; 5,000 rows is on the
// order of 1-2MB). It is NOT unbounded: retention is enforced on every
// write (see trimLocked) so the table can never grow past this cap
// regardless of run duration (finding #61 — an unbounded store shipped
// once already because nothing pruned it).
const DefaultSQLDecisionRetention = 5000

// decisionQueueCapacity bounds the async write queue. Sized generously
// relative to expected burst rates; a full queue means the writer
// goroutine cannot keep up, and Append drops the record from the
// durable path rather than block the caller (see Append's doc comment
// — this mirrors the drop-on-full-channel posture the streamBroker and
// audit.API.Push already use elsewhere in this codebase).
const decisionQueueCapacity = 512

// SQLDecisionStore is the production DecisionStore implementation:
// genuinely persistent (survives process restart), bounded (retention
// enforced on every write), and non-blocking on the Evaluate hot path
// (writes are queued and applied by a single background goroutine).
//
// decisions.go's own doc comment on DecisionStore anticipated exactly
// this: "production wiring (telemetry span store or a dedicated SQLite
// policy_log table) implements the same interface." This is that
// table — migrations.go creates policy_decisions (cedar-policy/1300).
//
// Safe for concurrent use. Construct via NewSQLDecisionStore; call
// Close when the owning Engine/process shuts down so the background
// writer flushes any queued decisions and exits cleanly.
type SQLDecisionStore struct {
	db  *sql.DB
	cap int

	mu   sync.Mutex
	ring []Decision // oldest-first, bounded to cap — mirrors MemoryDecisionStore's shape.

	queue    chan Decision
	stopOnce sync.Once
	done     chan struct{}
}

// SQLDecisionStoreOption configures NewSQLDecisionStore.
type SQLDecisionStoreOption func(*SQLDecisionStore)

// WithSQLDecisionRetention overrides DefaultSQLDecisionRetention.
// capacity <= 0 falls back to the default.
func WithSQLDecisionRetention(capacity int) SQLDecisionStoreOption {
	return func(s *SQLDecisionStore) {
		if capacity > 0 {
			s.cap = capacity
		}
	}
}

// NewSQLDecisionStore constructs a persistent DecisionStore backed by
// the policy_decisions table on db (the SAME unified storage.DB handle
// every other harness store uses — this does not open a second sqlite
// connection). db must already have migration cedar-policy/1300
// applied (core/storage/sqlite.Open registers RegisterMigrations before
// this is constructed).
//
// The in-memory hot cache is hydrated from the most recent rows on
// disk so Recent() reflects history from before this process started —
// the whole point of this type existing (the prior MemoryDecisionStore
// fallback loses that on every restart).
func NewSQLDecisionStore(db *sql.DB, opts ...SQLDecisionStoreOption) (*SQLDecisionStore, error) {
	s := &SQLDecisionStore{
		db:    db,
		cap:   DefaultSQLDecisionRetention,
		queue: make(chan Decision, decisionQueueCapacity),
		done:  make(chan struct{}),
	}
	for _, opt := range opts {
		opt(s)
	}

	if err := s.hydrate(); err != nil {
		return nil, err
	}

	go s.run()
	return s, nil
}

// hydrate loads the most recent s.cap rows (oldest-first) into the
// in-memory ring so Recent() serves persisted history immediately
// after construction, before any new decision is appended.
func (s *SQLDecisionStore) hydrate() error {
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT outcome, action, principal, resource, matched_policy, reason, evaluated_at
		 FROM policy_decisions ORDER BY id DESC LIMIT ?`, s.cap)
	if err != nil {
		return err
	}
	defer rows.Close()

	var loaded []Decision
	for rows.Next() {
		var d Decision
		var outcomeStr string
		var evaluatedAtMillis int64
		if err := rows.Scan(&outcomeStr, &d.Action, &d.Principal, &d.Resource, &d.MatchedPolicy, &d.Reason, &evaluatedAtMillis); err != nil {
			return err
		}
		d.Outcome = outcomeFromString(outcomeStr)
		d.EvaluatedAt = time.UnixMilli(evaluatedAtMillis).UTC()
		loaded = append(loaded, d)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// loaded is newest-first (id DESC); ring is oldest-first. Reverse.
	s.mu.Lock()
	s.ring = make([]Decision, len(loaded))
	for i, d := range loaded {
		s.ring[len(loaded)-1-i] = d
	}
	s.mu.Unlock()
	return nil
}

// Append records one decision. Safe for concurrent use, and MUST NOT
// block on I/O (Engine.Evaluate's hot-path contract, engine.go): the
// in-memory ring update is a pure in-process append under a mutex, and
// the durable write is hand off to the background writer goroutine via
// a non-blocking channel send. Under sustained write bursts that
// outrun the writer (queue full), the durable copy of that one
// decision is dropped — the in-memory ring (and therefore Recent())
// still reflects it for the lifetime of this process. A logging/audit
// failure must never block an authorization decision (decisions.go's
// own DecisionStore doc comment); this is the same posture applied to
// a store that can genuinely block on disk.
func (s *SQLDecisionStore) Append(d Decision) {
	s.mu.Lock()
	s.ring = append(s.ring, d)
	if len(s.ring) > s.cap {
		drop := len(s.ring) - s.cap
		s.ring = s.ring[drop:]
	}
	s.mu.Unlock()

	select {
	case s.queue <- d:
	default:
		slog.Warn("cedar.decision_store.queue_full_dropped",
			"action", d.Action, "outcome", d.Outcome.String())
	}
}

// Recent returns up to limit most-recent decisions, newest first.
// Served entirely from the in-memory ring — no disk I/O on the read
// path either, matching MemoryDecisionStore's semantics exactly so
// callers (Engine.RecentDecisions, the policy-panel RPC) see no
// behavioural difference beyond decisions surviving a restart.
func (s *SQLDecisionStore) Recent(limit int) []Decision {
	if limit <= 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.ring) == 0 {
		return nil
	}
	if limit > len(s.ring) {
		limit = len(s.ring)
	}
	out := make([]Decision, limit)
	for i := 0; i < limit; i++ {
		out[i] = s.ring[len(s.ring)-1-i]
	}
	return out
}

// run is the single background writer goroutine. Serialising every
// durable write through one goroutine means concurrent Append calls
// never race on the underlying *sql.DB, and retention trimming
// (trimLocked, run after every insert) never races against itself.
func (s *SQLDecisionStore) run() {
	defer close(s.done)
	for d := range s.queue {
		s.writeOne(d)
	}
}

// writeOne inserts one decision and enforces retention. Errors are
// logged, never propagated — a store write failure must never surface
// as an authorization failure (mirrors audit.API.Push's contract).
func (s *SQLDecisionStore) writeOne(d Decision) {
	ctx := context.Background()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO policy_decisions (outcome, action, principal, resource, matched_policy, reason, evaluated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		d.Outcome.String(), d.Action, d.Principal, d.Resource, d.MatchedPolicy, d.Reason, d.EvaluatedAt.UnixMilli(),
	)
	if err != nil {
		slog.Warn("cedar.decision_store.write_failed", "action", d.Action, "err", err.Error())
		return
	}
	// Retention: enforce the cap on every write rather than
	// periodically. policy_decisions is local sqlite with a small row
	// shape, and this keeps the bound exact and trivially testable
	// (finding #61 was an unbounded store nothing pruned — "trim
	// occasionally" reintroduces the same risk class if the trim
	// interval is ever misconfigured or skipped).
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM policy_decisions WHERE id NOT IN (
			SELECT id FROM policy_decisions ORDER BY id DESC LIMIT ?
		)`, s.cap,
	); err != nil {
		slog.Warn("cedar.decision_store.retention_trim_failed", "err", err.Error())
	}
}

// outcomeFromString is the inverse of Outcome.String() (types.go),
// used when hydrating rows back from the outcome TEXT column. Mirrors
// Outcome.UnmarshalJSON's switch without going through the JSON layer.
// An unrecognised value degrades to NotApplicable rather than erroring
// the whole hydrate — a single corrupted row must not prevent every
// other persisted decision from loading.
func outcomeFromString(s string) Outcome {
	switch s {
	case "allow":
		return Allow
	case "deny":
		return Deny
	default:
		return NotApplicable
	}
}

// Close stops the background writer, flushing every decision already
// queued before returning (no data queued before Close is lost — it is
// only ever-full-queue drops during Append that lose a durable write).
// Idempotent. Does NOT close the underlying *sql.DB — that handle is
// owned by the caller (the same unified storage.DB every other harness
// store shares), not by this type.
func (s *SQLDecisionStore) Close() error {
	s.stopOnce.Do(func() {
		close(s.queue)
	})
	<-s.done
	return nil
}
