// Package fleet — audit_retention.go
//
// AuditRetentionSweeper runs hourly, deleting locally-retained audit rows
// that are BOTH:
//   (a) ACK'd by fleet (their event_id ≤ last ACK'd cursor), AND
//   (b) older than the configured retention window (default 90 days).
//
// The conjunctive condition prevents deleting un-archived rows even if
// they are old (FR-006). The sweep deletes at most 1000 rows per pass to
// bound I/O spikes (NFR-005).
//
// The retention window is configurable via fleet.audit_local_retention_days
// (delivered by the fleet config bundle; falls back to the default 90d).
//
// (fleet-audit-archival-01NDFSEX13 WP04)
package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
)

const (
	// DefaultAuditRetentionDays is the default local retention window.
	DefaultAuditRetentionDays = 90
	// retentionSweepInterval is how often the sweeper runs.
	retentionSweepInterval = 1 * time.Hour
	// retentionMaxRowsPerPass is the maximum number of rows deleted per sweep.
	retentionMaxRowsPerPass = 1000
)

// AuditRetentionBackend is the storage interface the sweeper needs.
// Both conditions — row.id ≤ cursor AND row.ts < cutoff — are checked
// by the sweeper after fetching rows from the backend.
type AuditRetentionBackend interface {
	// SelectBefore returns audit rows older than cutoff, ordered by
	// event_id ascending, up to limit rows.
	SelectBefore(ctx context.Context, cutoff time.Time, limit int) ([]AuditRetentionRow, error)
	// DeleteRows removes rows by event_id.
	DeleteRows(ctx context.Context, eventIDs []string) error
}

// AuditRetentionRow carries the fields the sweeper needs to decide
// whether to delete a row.
type AuditRetentionRow struct {
	EventID   string
	EmittedAt time.Time
}

// AuditRetentionSweeper runs the hourly retention sweep.
type AuditRetentionSweeper struct {
	mu            sync.RWMutex
	retentionDays int
	cursor        func() string // returns the last ACK'd event_id
	backend       AuditRetentionBackend
	emitter       contextaudit.Emitter
	cancel        context.CancelFunc
	done          chan struct{}
}

// AuditRetentionConfig configures the sweeper.
type AuditRetentionConfig struct {
	// Backend is the audit store backend.
	Backend AuditRetentionBackend
	// Cursor returns the last ACK'd event_id. The sweeper only deletes
	// rows with event_id ≤ cursor.
	Cursor func() string
	// Emitter emits KindFleetAuditRetentionSwept on each pass.
	Emitter contextaudit.Emitter
	// RetentionDays is the initial retention window. 0 = DefaultAuditRetentionDays.
	RetentionDays int
}

// NewAuditRetentionSweeper constructs a sweeper.
func NewAuditRetentionSweeper(cfg AuditRetentionConfig) *AuditRetentionSweeper {
	days := cfg.RetentionDays
	if days <= 0 {
		days = DefaultAuditRetentionDays
	}
	return &AuditRetentionSweeper{
		retentionDays: days,
		cursor:        cfg.Cursor,
		backend:       cfg.Backend,
		emitter:       cfg.Emitter,
	}
}

// Start launches the background sweep loop.
func (s *AuditRetentionSweeper) Start(ctx context.Context) {
	loopCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.done = make(chan struct{})
	go func() {
		defer close(s.done)
		s.loop(loopCtx)
	}()
}

// Stop signals the loop to exit and waits.
func (s *AuditRetentionSweeper) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.done != nil {
		<-s.done
	}
}

// SetRetentionDays updates the retention window. Thread-safe.
func (s *AuditRetentionSweeper) SetRetentionDays(days int) {
	if days <= 0 {
		days = DefaultAuditRetentionDays
	}
	s.mu.Lock()
	s.retentionDays = days
	s.mu.Unlock()
}

// RetentionDays returns the current retention window.
func (s *AuditRetentionSweeper) RetentionDays() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.retentionDays
}

// SweepOnce executes a single retention pass. Returns the number of rows
// deleted and any error. Called by the loop; also exposed for tests.
func (s *AuditRetentionSweeper) SweepOnce(ctx context.Context) (int, error) {
	if s.backend == nil {
		return 0, nil
	}

	s.mu.RLock()
	retDays := s.retentionDays
	s.mu.RUnlock()

	cutoff := time.Now().Add(-time.Duration(retDays) * 24 * time.Hour)

	cursor := ""
	if s.cursor != nil {
		cursor = s.cursor()
	}

	// Fetch rows older than cutoff, up to the per-pass cap.
	rows, err := s.backend.SelectBefore(ctx, cutoff, retentionMaxRowsPerPass)
	if err != nil {
		return 0, fmt.Errorf("fleet/audit_retention: select: %w", err)
	}

	// Apply conjunctive condition: event_id ≤ cursor.
	var toDelete []string
	var oldestID string
	for _, r := range rows {
		if cursor == "" || r.EventID > cursor {
			continue // not ACK'd yet; never delete
		}
		toDelete = append(toDelete, r.EventID)
		if oldestID == "" {
			oldestID = r.EventID
		}
	}

	if len(toDelete) == 0 {
		return 0, nil
	}

	if err := s.backend.DeleteRows(ctx, toDelete); err != nil {
		return 0, fmt.Errorf("fleet/audit_retention: delete: %w", err)
	}

	_ = emitAudit(ctx, s.emitter, contextaudit.KindFleetAuditRetentionSwept,
		contextaudit.FleetAuditRetentionSweptPayload{
			DeletedCount:    len(toDelete),
			OldestDeletedID: oldestID,
			RetentionDays:   retDays,
		})

	slog.Info("fleet/audit_retention: sweep pass complete",
		"deleted", len(toDelete), "retention_days", retDays)
	return len(toDelete), nil
}

func (s *AuditRetentionSweeper) loop(ctx context.Context) {
	ticker := time.NewTicker(retentionSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.SweepOnce(ctx); err != nil {
				slog.Warn("fleet/audit_retention: sweep error", "err", err)
			}
		}
	}
}

// ── MemoryRetentionBackend ────────────────────────────────────────────────────

// MemoryRetentionBackend is an in-memory AuditRetentionBackend for tests.
type MemoryRetentionBackend struct {
	mu   sync.Mutex
	rows []AuditRetentionRow
}

// AddRow inserts a row into the backend.
func (m *MemoryRetentionBackend) AddRow(r AuditRetentionRow) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows = append(m.rows, r)
}

// SelectBefore implements AuditRetentionBackend.
func (m *MemoryRetentionBackend) SelectBefore(_ context.Context, cutoff time.Time, limit int) ([]AuditRetentionRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AuditRetentionRow
	for _, r := range m.rows {
		if r.EmittedAt.Before(cutoff) {
			out = append(out, r)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

// DeleteRows implements AuditRetentionBackend.
func (m *MemoryRetentionBackend) DeleteRows(_ context.Context, ids []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	var kept []AuditRetentionRow
	for _, r := range m.rows {
		if !set[r.EventID] {
			kept = append(kept, r)
		}
	}
	m.rows = kept
	return nil
}

// Rows returns a snapshot of the current rows.
func (m *MemoryRetentionBackend) Rows() []AuditRetentionRow {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]AuditRetentionRow, len(m.rows))
	copy(out, m.rows)
	return out
}

// applyRetentionConfig applies a fleet config bundle section
// "audit_local_retention_days" key to the sweeper. Used by the
// composite ConfigApplier in settings/fleet.go.
func applyRetentionConfig(sweeper *AuditRetentionSweeper, raw json.RawMessage) error {
	if sweeper == nil || len(raw) == 0 {
		return nil
	}
	var days int
	if err := json.Unmarshal(raw, &days); err != nil {
		return fmt.Errorf("fleet/audit_retention: parse audit_local_retention_days: %w", err)
	}
	if days > 0 {
		sweeper.SetRetentionDays(days)
	}
	return nil
}
