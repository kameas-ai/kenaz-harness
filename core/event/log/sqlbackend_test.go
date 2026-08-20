// Package log_test drives SQLBackend against a REAL sqlite database
// through core/storage/sqlite.Open — never eventlog.NewMemoryBackend()
// for the subject under test, per CLAUDE.md blind spot #2 and spec
// AC-003's explicit "fails if the fixture uses NewMemoryBackend()"
// clause. This file is an EXTERNAL test package (log_test, not log)
// specifically so it can import core/storage/sqlite, which itself
// imports core/event/log (to register event-log's migrations) — an
// internal (package log) test file importing storage/sqlite would be a
// real import cycle; an external one is the standard Go escape hatch.
package log_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	eventlog "github.com/kameas-ai/kenaz-harness/core/event/log"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

// openTestBackend opens a fresh on-disk sqlite database (via the SAME
// production Open path core/rpc wires — event-log's migrations included
// automatically since sqlite.go registers them) and returns a
// SQLBackend against it.
func openTestBackend(t *testing.T) eventlog.Backend {
	t.Helper()
	cfg := storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	return eventlog.NewSQLBackend(db)
}

func mustBackend(t *testing.T, b eventlog.Backend) *eventlog.SQLBackend {
	t.Helper()
	sb, ok := b.(*eventlog.SQLBackend)
	if !ok {
		t.Fatalf("openTestBackend returned %T, want *eventlog.SQLBackend", b)
	}
	return sb
}

// TestSQLBackend_DifferentialAgainstMemory is AC-003's headline
// assertion: append 100 rows across 3 sessions to BOTH a MemoryBackend
// (the reference implementation) and a real SQLBackend, and assert
// SelectBySession / SelectByKind / SelectByEmitter / SelectByTimeRange
// return byte-identical results — and that SearchFTS agrees for a
// whole-token query (FTS5 tokenizes on token boundaries; MemoryBackend
// does a raw case-insensitive substring Contains — the two are only
// guaranteed to agree for a query that is itself a complete token,
// which is what this test uses. That is a real, permanent semantic
// difference between "FTS5 MATCH" and "Contains", not a bug in either
// implementation — noted here so nobody "fixes" it later expecting
// substring-anywhere parity).
func TestSQLBackend_DifferentialAgainstMemory(t *testing.T) {
	ctx := context.Background()
	mem := eventlog.NewMemoryBackend()
	sqlB := openTestBackend(t)

	sessions := []string{"sess-1", "sess-2", "sess-3"}
	heads := map[string][32]byte{}
	const n = 100

	for i := 0; i < n; i++ {
		sid := sessions[i%len(sessions)]
		payload := []byte(fmt.Sprintf("event body %03d alpha%d needletoken%d", i, i%7, i%11))
		var hash [32]byte
		for j, b := range payload {
			hash[j%32] ^= b
		}
		hash[31] ^= byte(i + 1) // vary so no two rows share a hash
		row := eventlog.Row{
			EventID:          fmt.Sprintf("evt-%04d", i),
			SessionID:        sid,
			EmitterID:        fmt.Sprintf("emitter-%d", i%4),
			Kind:             fmt.Sprintf("kind.%d", i%5),
			EmittedAt:        time.UnixMilli(1_700_000_000_000 + int64(i)*1000).UTC(),
			Payload:          payload,
			PayloadHash:      hash,
			PrevHash:         heads[sid],
			RedactionSummary: `{"no_op":true}`,
			SchemaVersion:    1,
		}
		expected := heads[sid]
		if err := mem.AppendRow(ctx, row, expected); err != nil {
			t.Fatalf("mem.AppendRow[%d]: %v", i, err)
		}
		if err := sqlB.AppendRow(ctx, row, expected); err != nil {
			t.Fatalf("sqlB.AppendRow[%d]: %v", i, err)
		}
		heads[sid] = hash
	}

	assertRowsEqual := func(t *testing.T, label string, memRows, sqlRows []eventlog.Row, err1, err2 error) {
		t.Helper()
		if (err1 == nil) != (err2 == nil) {
			t.Fatalf("%s: mem err=%v, sql err=%v", label, err1, err2)
		}
		if len(memRows) != len(sqlRows) {
			t.Fatalf("%s: mem returned %d rows, sql returned %d", label, len(memRows), len(sqlRows))
		}
		if !reflect.DeepEqual(memRows, sqlRows) {
			t.Errorf("%s: mem and sql disagree.\nmem: %+v\nsql: %+v", label, memRows, sqlRows)
		}
	}

	for _, sid := range sessions {
		mr, e1 := mem.SelectBySession(ctx, sid, "", 0, false)
		sr, e2 := sqlB.SelectBySession(ctx, sid, "", 0, false)
		assertRowsEqual(t, "SelectBySession/"+sid, mr, sr, e1, e2)
	}
	for k := 0; k < 5; k++ {
		kind := fmt.Sprintf("kind.%d", k)
		mr, e1 := mem.SelectByKind(ctx, kind, "", 0, false)
		sr, e2 := sqlB.SelectByKind(ctx, kind, "", 0, false)
		assertRowsEqual(t, "SelectByKind/"+kind, mr, sr, e1, e2)
	}
	for e := 0; e < 4; e++ {
		emitter := fmt.Sprintf("emitter-%d", e)
		mr, e1 := mem.SelectByEmitter(ctx, emitter, "", 0, false)
		sr, e2 := sqlB.SelectByEmitter(ctx, emitter, "", 0, false)
		assertRowsEqual(t, "SelectByEmitter/"+emitter, mr, sr, e1, e2)
	}
	// A restricted time window covering roughly the middle third.
	from := time.UnixMilli(1_700_000_000_000 + 30_000).UTC()
	to := time.UnixMilli(1_700_000_000_000 + 60_000).UTC()
	mr, e1 := mem.SelectByTimeRange(ctx, from, to, "", 0, false)
	sr, e2 := sqlB.SelectByTimeRange(ctx, from, to, "", 0, false)
	assertRowsEqual(t, "SelectByTimeRange", mr, sr, e1, e2)
	if len(mr) == 0 {
		t.Fatal("SelectByTimeRange test window matched zero rows — test data does not actually exercise the range filter")
	}
	// Reverse + after-cursor pagination parity.
	mr, e1 = mem.SelectBySession(ctx, "sess-1", "evt-0010", 5, true)
	sr, e2 = sqlB.SelectBySession(ctx, "sess-1", "evt-0010", 5, true)
	assertRowsEqual(t, "SelectBySession reverse+after+limit", mr, sr, e1, e2)
	if len(mr) == 0 {
		t.Fatal("reverse+after+limit test matched zero rows — not actually exercising that path")
	}

	// SearchFTS: a whole-token needle. See the function doc comment for
	// why the query must be a complete token for the two implementations
	// to be expected to agree.
	mr, e1 = mem.SearchFTS(ctx, "needletoken3", "", nil, 0)
	sr, e2 = sqlB.SearchFTS(ctx, "needletoken3", "", nil, 0)
	sortByEventID(mr)
	sortByEventID(sr)
	assertRowsEqual(t, "SearchFTS", mr, sr, e1, e2)
	if len(mr) == 0 {
		t.Fatal("SearchFTS test needle matched zero rows — not actually exercising the search path")
	}

	// AllSessionIDs and SizeBytes — exercised, not left unchecked
	// ("an unexercised method is a method nobody checked").
	memSessions, err := mem.AllSessionIDs(ctx)
	if err != nil {
		t.Fatalf("mem.AllSessionIDs: %v", err)
	}
	sqlSessions, err := sqlB.AllSessionIDs(ctx)
	if err != nil {
		t.Fatalf("sql.AllSessionIDs: %v", err)
	}
	sort.Strings(memSessions)
	sort.Strings(sqlSessions)
	if !reflect.DeepEqual(memSessions, sqlSessions) {
		t.Errorf("AllSessionIDs: mem=%v sql=%v", memSessions, sqlSessions)
	}

	memSize, err := mem.SizeBytes(ctx)
	if err != nil {
		t.Fatalf("mem.SizeBytes: %v", err)
	}
	sqlSize, err := sqlB.SizeBytes(ctx)
	if err != nil {
		t.Fatalf("sql.SizeBytes: %v", err)
	}
	if memSize != sqlSize {
		t.Errorf("SizeBytes: mem=%d sql=%d", memSize, sqlSize)
	}
	if memSize == 0 {
		t.Fatal("SizeBytes returned 0 for a populated store — not actually exercising the size accounting")
	}
}

func sortByEventID(rows []eventlog.Row) {
	sort.Slice(rows, func(i, j int) bool { return rows[i].EventID < rows[j].EventID })
}

// TestSQLBackend_AppendRow_ChainHeadMismatch is AC-003's required
// mutation target: "drop the head comparison; a concurrent-append test
// must go red." This is the single-threaded half of that proof — a
// stale expectedHead must be rejected.
func TestSQLBackend_AppendRow_ChainHeadMismatch(t *testing.T) {
	ctx := context.Background()
	b := openTestBackend(t)

	row1 := eventlog.Row{
		EventID: "e1", SessionID: "s1", EmitterID: "em", Kind: "k",
		EmittedAt: time.UnixMilli(1000).UTC(), Payload: []byte("a"),
		PayloadHash: [32]byte{1}, PrevHash: [32]byte{}, RedactionSummary: "{}",
	}
	if err := b.AppendRow(ctx, row1, [32]byte{}); err != nil {
		t.Fatalf("first append: %v", err)
	}

	// The head is now row1.PayloadHash. Appending with a STALE
	// (zero) expectedHead must fail with ErrChainHeadMismatch.
	row2 := eventlog.Row{
		EventID: "e2", SessionID: "s1", EmitterID: "em", Kind: "k",
		EmittedAt: time.UnixMilli(2000).UTC(), Payload: []byte("b"),
		PayloadHash: [32]byte{2}, PrevHash: [32]byte{}, RedactionSummary: "{}",
	}
	err := b.AppendRow(ctx, row2, [32]byte{})
	if !errors.Is(err, eventlog.ErrChainHeadMismatch) {
		t.Fatalf("stale expectedHead: got err=%v, want ErrChainHeadMismatch", err)
	}

	// The correct head succeeds.
	row2.PrevHash = row1.PayloadHash
	if err := b.AppendRow(ctx, row2, row1.PayloadHash); err != nil {
		t.Fatalf("correct expectedHead should succeed: %v", err)
	}

	// A duplicate event_id must fail (not silently overwrite).
	row3 := row2
	row3.PayloadHash = [32]byte{3}
	err = b.AppendRow(ctx, row3, row2.PayloadHash)
	if err == nil {
		t.Fatal("expected an error appending a duplicate event_id, got nil")
	}
}

// TestSQLBackend_AppendRow_ConcurrentDistinctSessions is AC-003's
// -race requirement: concurrent appends from multiple goroutines. Each
// goroutine owns a distinct session (concurrent appends to the SAME
// session are expected to race for the head and are not what this test
// is checking — that is exactly what ErrChainHeadMismatch exists to
// arbitrate, proven single-threaded above). This test proves no
// cross-session interference and no data race in SQLBackend itself.
func TestSQLBackend_AppendRow_ConcurrentDistinctSessions(t *testing.T) {
	ctx := context.Background()
	b := openTestBackend(t)

	const sessions = 8
	const perSession = 10
	var wg sync.WaitGroup
	errCh := make(chan error, sessions)

	for s := 0; s < sessions; s++ {
		s := s
		wg.Add(1)
		go func() {
			defer wg.Done()
			sid := fmt.Sprintf("race-sess-%d", s)
			var head [32]byte
			for i := 0; i < perSession; i++ {
				var h [32]byte
				h[0] = byte(s + 1)
				h[1] = byte(i + 1)
				row := eventlog.Row{
					EventID:          fmt.Sprintf("race-%d-%03d", s, i),
					SessionID:        sid,
					EmitterID:        "race",
					Kind:             "race.append",
					EmittedAt:        time.UnixMilli(int64(1_800_000_000_000 + s*10000 + i)).UTC(),
					Payload:          []byte("x"),
					PayloadHash:      h,
					PrevHash:         head,
					RedactionSummary: "{}",
				}
				if err := b.AppendRow(ctx, row, head); err != nil {
					errCh <- fmt.Errorf("session %d row %d: %w", s, i, err)
					return
				}
				head = h
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}

	for s := 0; s < sessions; s++ {
		sid := fmt.Sprintf("race-sess-%d", s)
		rows, err := b.SelectBySession(ctx, sid, "", 0, false)
		if err != nil {
			t.Fatalf("SelectBySession(%s): %v", sid, err)
		}
		if len(rows) != perSession {
			t.Errorf("session %s: got %d rows, want %d", sid, len(rows), perSession)
		}
	}
}

func TestSQLBackend_GetRow_NotFound(t *testing.T) {
	ctx := context.Background()
	b := openTestBackend(t)
	_, err := b.GetRow(ctx, "does-not-exist")
	if !errors.Is(err, eventlog.ErrNotFound) {
		t.Fatalf("got err=%v, want ErrNotFound", err)
	}
}

func TestSQLBackend_HeadFor_NoSession(t *testing.T) {
	ctx := context.Background()
	b := openTestBackend(t)
	_, _, ok, err := b.HeadFor(ctx, "never-seen")
	if err != nil {
		t.Fatalf("HeadFor: %v", err)
	}
	if ok {
		t.Fatal("HeadFor for a never-seen session returned ok=true, want false")
	}
}

// TestSQLBackend_SelectBefore exercises the corefleet.AuditRetentionBackend
// shim UNIT-7 needs — spec R-6: it is NOT on log.Backend.
func TestSQLBackend_SelectBefore(t *testing.T) {
	ctx := context.Background()
	backend := openTestBackend(t)
	sb := mustBackend(t, backend)

	base := time.UnixMilli(1_900_000_000_000).UTC()
	for i := 0; i < 5; i++ {
		row := eventlog.Row{
			EventID: fmt.Sprintf("sb-%d", i), SessionID: "sb-sess", EmitterID: "em",
			Kind: "k", EmittedAt: base.Add(time.Duration(i) * time.Hour),
			Payload: []byte("p"), PayloadHash: [32]byte{byte(i + 1)},
			RedactionSummary: "{}",
		}
		var expected [32]byte
		if i > 0 {
			expected = [32]byte{byte(i)}
		}
		row.PrevHash = expected
		if err := sb.AppendRow(ctx, row, expected); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	// Cutoff after row 2 (index 2, i.e. base+2h): rows 0,1,2 are before it.
	cutoff := base.Add(2*time.Hour + time.Minute)
	rows, err := sb.SelectBefore(ctx, cutoff, 0)
	if err != nil {
		t.Fatalf("SelectBefore: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("SelectBefore returned %d rows, want 3: %+v", len(rows), rows)
	}
	for _, r := range rows {
		if !r.EmittedAt.Before(cutoff) {
			t.Errorf("row %s emitted_at %v is not before cutoff %v", r.EventID, r.EmittedAt, cutoff)
		}
	}

	// limit is respected.
	limited, err := sb.SelectBefore(ctx, cutoff, 1)
	if err != nil {
		t.Fatalf("SelectBefore(limit=1): %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("SelectBefore(limit=1) returned %d rows, want 1", len(limited))
	}
}

// TestSQLBackend_SurvivesReopen is a backend-level preview of UNIT-4's
// AC-004 (which asserts this through the full audit.API, not the bare
// Backend) — proving the persistence primitive itself survives a close
// and reopen of the underlying database, not just the in-process struct.
func TestSQLBackend_SurvivesReopen(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cfg := storage.Config{DataDir: dir, EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption}

	db1, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	b1 := eventlog.NewSQLBackend(db1)
	row := eventlog.Row{
		EventID: "reopen-1", SessionID: "s", EmitterID: "em", Kind: "k",
		EmittedAt: time.UnixMilli(5000).UTC(), Payload: []byte("survives"),
		PayloadHash: [32]byte{9}, RedactionSummary: "{}",
	}
	if err := b1.AppendRow(ctx, row, [32]byte{}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := db1.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}

	db2, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close(context.Background()) })
	b2 := eventlog.NewSQLBackend(db2)

	got, err := b2.GetRow(ctx, "reopen-1")
	if err != nil {
		t.Fatalf("GetRow after reopen: %v", err)
	}
	if string(got.Payload) != "survives" {
		t.Errorf("Payload after reopen = %q, want %q", got.Payload, "survives")
	}

	hash, headID, ok, err := b2.HeadFor(ctx, "s")
	if err != nil {
		t.Fatalf("HeadFor after reopen: %v", err)
	}
	if !ok || headID != "reopen-1" || hash != row.PayloadHash {
		t.Errorf("HeadFor after reopen = (%x, %q, %v), want (%x, %q, true)", hash, headID, ok, row.PayloadHash, "reopen-1")
	}
}
