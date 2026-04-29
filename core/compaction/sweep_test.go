package compaction

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/context/audit"
)

// sweep_test.go drives RunSweep through a small in-memory SweepStore
// fake that mirrors the SQL filter rules: only delete rows where
// archived_at IS NOT NULL AND archived_at < cutoff AND
// compacted_into_id IS NOT NULL. The fake also enforces the pageLimit
// contract so the pagination test is honest.

// fakeSweepRow is a single row in the in-memory fake. The fields mirror
// the SQL columns the sweep predicate touches.
type fakeSweepRow struct {
	id              string
	archivedAt      time.Time // zero means NOT archived (NULL)
	compactedIntoID string    // empty means NULL — i.e. summary row
}

// fakeSweepStore implements SweepStore over an in-memory []fakeSweepRow.
// It records every DeleteArchivedBefore call so tests can assert on the
// page count.
type fakeSweepStore struct {
	rows []fakeSweepRow

	// returnErr lets a test simulate a storage failure.
	returnErr error

	// calls is the per-page record (one entry per loop iteration inside
	// DeleteArchivedBefore).
	calls []sweepCall
}

type sweepCall struct {
	cutoff    time.Time
	pageLimit int
	deleted   int
}

func (s *fakeSweepStore) DeleteArchivedBefore(_ context.Context, cutoff time.Time, pageLimit int) (int, time.Time, time.Time, error) {
	if s.returnErr != nil {
		return 0, time.Time{}, time.Time{}, s.returnErr
	}

	totalDeleted := 0
	var oldest, newest time.Time

	// Loop in pages until a page yields zero deletions, mirroring the
	// real implementation's "loop until affected-row count is zero"
	// contract.
	for {
		// Find candidate row indices in deterministic order (oldest
		// first) so the test fixture's expected oldest/newest tracks
		// match.
		var candidates []int
		for i, r := range s.rows {
			if r.archivedAt.IsZero() {
				continue // NOT archived
			}
			if r.compactedIntoID == "" {
				continue // summary row — never delete
			}
			if !r.archivedAt.Before(cutoff) {
				continue // archived AT-OR-AFTER cutoff
			}
			candidates = append(candidates, i)
		}
		sort.Slice(candidates, func(a, b int) bool {
			return s.rows[candidates[a]].archivedAt.Before(s.rows[candidates[b]].archivedAt)
		})

		if len(candidates) == 0 {
			s.calls = append(s.calls, sweepCall{cutoff: cutoff, pageLimit: pageLimit, deleted: 0})
			break
		}

		take := len(candidates)
		if take > pageLimit {
			take = pageLimit
		}

		// Track oldest/newest archived_at across the full sweep.
		for _, idx := range candidates[:take] {
			ts := s.rows[idx].archivedAt
			if oldest.IsZero() || ts.Before(oldest) {
				oldest = ts
			}
			if newest.IsZero() || ts.After(newest) {
				newest = ts
			}
		}

		// Build the survivor slice (the easy way: mark deletes by id,
		// rebuild). Order doesn't matter to the test — only the
		// remaining set membership.
		dead := make(map[string]struct{}, take)
		for _, idx := range candidates[:take] {
			dead[s.rows[idx].id] = struct{}{}
		}
		survivors := s.rows[:0]
		for _, r := range s.rows {
			if _, ok := dead[r.id]; ok {
				continue
			}
			survivors = append(survivors, r)
		}
		// Important: copy to a fresh slice so the underlying array
		// isn't aliased after the rebuild.
		s.rows = append([]fakeSweepRow(nil), survivors...)

		totalDeleted += take
		s.calls = append(s.calls, sweepCall{cutoff: cutoff, pageLimit: pageLimit, deleted: take})

		if take < pageLimit {
			// Final partial page — one more empty call would be a
			// no-op; mirror the real loop by exiting now.
			break
		}
	}

	return totalDeleted, oldest, newest, nil
}

// helper: build a fixed `now` closure.
func fixedNow(t time.Time) func() time.Time { return func() time.Time { return t } }

func TestRunSweep_DeletesOnlyRowsPastTTL(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	thirtyOneDaysAgo := now.AddDate(0, 0, -31)
	tenDaysAgo := now.AddDate(0, 0, -10)

	store := &fakeSweepStore{
		rows: []fakeSweepRow{
			// past TTL — should be deleted
			{id: "old-1", archivedAt: thirtyOneDaysAgo, compactedIntoID: "sum-1"},
			{id: "old-2", archivedAt: thirtyOneDaysAgo.Add(-time.Hour), compactedIntoID: "sum-1"},
			// recent — should NOT be deleted
			{id: "recent-1", archivedAt: tenDaysAgo, compactedIntoID: "sum-2"},
		},
	}
	auditEm := &fakeAudit{}

	deleted, err := RunSweep(context.Background(), store, auditEm, 30, fixedNow(now))
	if err != nil {
		t.Fatalf("RunSweep returned error: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("expected 2 deletions, got %d", deleted)
	}

	// Verify the recent row survives.
	if len(store.rows) != 1 || store.rows[0].id != "recent-1" {
		t.Fatalf("expected only recent-1 to survive, got %+v", store.rows)
	}
}

func TestRunSweep_NeverDeletesSummaryRows(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -90)

	store := &fakeSweepStore{
		rows: []fakeSweepRow{
			// summary row: compacted_into_id IS NULL — must be skipped
			// even though it's archived AND past the cutoff.
			{id: "summary-1", archivedAt: old, compactedIntoID: ""},
			// archived original past cutoff — should be deleted
			{id: "orig-1", archivedAt: old, compactedIntoID: "summary-1"},
		},
	}

	deleted, err := RunSweep(context.Background(), store, &fakeAudit{}, 30, fixedNow(now))
	if err != nil {
		t.Fatalf("RunSweep returned error: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deletion (the original), got %d", deleted)
	}

	// Verify the summary row survives.
	foundSummary := false
	for _, r := range store.rows {
		if r.id == "summary-1" {
			foundSummary = true
		}
		if r.id == "orig-1" {
			t.Fatalf("orig-1 should have been deleted")
		}
	}
	if !foundSummary {
		t.Fatalf("summary-1 must NEVER be deleted by the sweep")
	}
}

func TestRunSweep_DisabledWhenRetentionNonPositive(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	store := &fakeSweepStore{
		rows: []fakeSweepRow{
			{id: "any", archivedAt: now.AddDate(-1, 0, 0), compactedIntoID: "sum"},
		},
	}
	auditEm := &fakeAudit{}

	for _, retention := range []int{0, -1, -365} {
		store.calls = nil
		deleted, err := RunSweep(context.Background(), store, auditEm, retention, fixedNow(now))
		if err != nil {
			t.Fatalf("retention=%d: RunSweep error: %v", retention, err)
		}
		if deleted != 0 {
			t.Fatalf("retention=%d: expected 0 deletions, got %d", retention, deleted)
		}
		if len(store.calls) != 0 {
			t.Fatalf("retention=%d: expected no Delete call, got %d", retention, len(store.calls))
		}
		if len(auditEm.events) != 0 {
			t.Fatalf("retention=%d: expected no audit emit, got %d", retention, len(auditEm.events))
		}
	}
}

func TestRunSweep_NoOpWhenNothingPastCutoff(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	recent := now.AddDate(0, 0, -5)

	store := &fakeSweepStore{
		rows: []fakeSweepRow{
			{id: "recent-1", archivedAt: recent, compactedIntoID: "sum-1"},
		},
	}
	auditEm := &fakeAudit{}

	deleted, err := RunSweep(context.Background(), store, auditEm, 30, fixedNow(now))
	if err != nil {
		t.Fatalf("RunSweep error: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("expected 0 deletions, got %d", deleted)
	}
	// Audit only emits on non-zero deletions.
	if len(auditEm.events) != 0 {
		t.Fatalf("expected no audit emit on zero deletions, got %d", len(auditEm.events))
	}
}

func TestRunSweep_AuditEmissionShape(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	oldest := now.AddDate(0, 0, -90)
	middle := now.AddDate(0, 0, -60)
	newest := now.AddDate(0, 0, -45)

	store := &fakeSweepStore{
		rows: []fakeSweepRow{
			{id: "o-1", archivedAt: oldest, compactedIntoID: "s"},
			{id: "o-2", archivedAt: middle, compactedIntoID: "s"},
			{id: "o-3", archivedAt: newest, compactedIntoID: "s"},
		},
	}
	auditEm := &fakeAudit{}

	deleted, err := RunSweep(context.Background(), store, auditEm, 30, fixedNow(now))
	if err != nil {
		t.Fatalf("RunSweep error: %v", err)
	}
	if deleted != 3 {
		t.Fatalf("expected 3 deletions, got %d", deleted)
	}
	if len(auditEm.events) != 1 {
		t.Fatalf("expected exactly 1 audit event, got %d", len(auditEm.events))
	}
	ev := auditEm.events[0]
	if ev.kind != audit.KindCompactedOriginalsDeleted {
		t.Fatalf("expected KindCompactedOriginalsDeleted, got %s", ev.kind)
	}
	payload, ok := ev.payload.(audit.CompactedOriginalsDeletedPayload)
	if !ok {
		t.Fatalf("payload is not CompactedOriginalsDeletedPayload: %T", ev.payload)
	}
	if payload.DeletedCount != 3 {
		t.Fatalf("payload.DeletedCount: want 3, got %d", payload.DeletedCount)
	}
	if !payload.OldestArchivedAt.Equal(oldest) {
		t.Fatalf("payload.OldestArchivedAt: want %v, got %v", oldest, payload.OldestArchivedAt)
	}
	if !payload.NewestArchivedAt.Equal(newest) {
		t.Fatalf("payload.NewestArchivedAt: want %v, got %v", newest, payload.NewestArchivedAt)
	}
}

func TestRunSweep_PaginationHonorsPageLimit(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -90)

	rows := make([]fakeSweepRow, 0, 12000)
	for i := 0; i < 12000; i++ {
		rows = append(rows, fakeSweepRow{
			id:              "r-" + itoa(i),
			archivedAt:      old.Add(time.Duration(i) * time.Second),
			compactedIntoID: "sum",
		})
	}
	store := &fakeSweepStore{rows: rows}

	deleted, err := RunSweep(context.Background(), store, &fakeAudit{}, 30, fixedNow(now))
	if err != nil {
		t.Fatalf("RunSweep error: %v", err)
	}
	if deleted != 12000 {
		t.Fatalf("expected 12000 deletions, got %d", deleted)
	}

	// Expected page sequence at default page limit (5000): 5000, 5000, 2000.
	// The fake exits after a partial page (no trailing zero call). RunSweep
	// itself does not loop pages — the real implementation does. So the
	// fake must page internally; we assert at least 3 paged calls.
	if len(store.calls) < 3 {
		t.Fatalf("expected at least 3 paged calls, got %d (%+v)", len(store.calls), store.calls)
	}
	// First two pages should be exactly 5000.
	if store.calls[0].deleted != 5000 || store.calls[1].deleted != 5000 {
		t.Fatalf("expected 5000+5000 first pages, got %d+%d", store.calls[0].deleted, store.calls[1].deleted)
	}
	if store.calls[2].deleted != 2000 {
		t.Fatalf("expected 2000 on the third page, got %d", store.calls[2].deleted)
	}
	// Page limit must be the documented default (5000).
	for i, c := range store.calls {
		if c.pageLimit != defaultSweepPageLimit {
			t.Fatalf("call[%d].pageLimit: want %d, got %d", i, defaultSweepPageLimit, c.pageLimit)
		}
	}
}

// TestRunSweep_PropagatesStoreError verifies that a store error bubbles
// up unchanged and no audit is emitted.
func TestRunSweep_PropagatesStoreError(t *testing.T) {
	now := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	wantErr := errSentinel("boom")
	store := &fakeSweepStore{returnErr: wantErr}
	auditEm := &fakeAudit{}

	deleted, err := RunSweep(context.Background(), store, auditEm, 30, fixedNow(now))
	if err != wantErr {
		t.Fatalf("expected sentinel error, got %v", err)
	}
	if deleted != 0 {
		t.Fatalf("expected 0 deletions on error, got %d", deleted)
	}
	if len(auditEm.events) != 0 {
		t.Fatalf("expected no audit on error, got %d", len(auditEm.events))
	}
}

// itoa is a tiny stdlib-free int-to-string for the pagination fixture
// id field. Avoids the strconv import bloat in the test file.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// errSentinel is a tiny error type for the propagation test.
type errSentinel string

func (e errSentinel) Error() string { return string(e) }
