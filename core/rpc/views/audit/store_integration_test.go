package audit

// UNIT-4 of audit-that-tells-the-truth-01PMZA10 — "the honesty
// threshold". These tests drive audit.API against a REAL sqlite
// database via core/storage/sqlite.Open (never eventlog.NewMemoryBackend()
// for the subject under test — CLAUDE.md blind spot #2, spec AC-004's
// explicit warning). AC-005's write-failure test is the one exception:
// it deliberately injects a Backend whose AppendRow always errors, which
// is the point of that test, not a violation of the real-sqlite rule.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	eventlog "github.com/kameas-ai/kenaz-harness/core/event/log"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

func openStoreAt(t *testing.T, dir string) (storage.DB, *eventlog.Store) {
	t.Helper()
	cfg := storage.Config{DataDir: dir, EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return db, eventlog.NewStore(eventlog.NewSQLBackend(db))
}

// TestPush_SurvivesRelaunch is AC-004: push an entry through the
// production Push path, close the store, reopen from the SAME
// directory with a brand-new audit.API, and assert BOTH ListEntries
// and Filter return it. Asserting on the same in-process API would let
// the ring satisfy this while nothing was persisted — the whole point
// is the reopen.
func TestPush_SurvivesRelaunch(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	db1, store1 := openStoreAt(t, dir)
	api1 := NewAPI(WithStore(store1))

	entry := Entry{
		ID:        "relaunch-evt-1",
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Category:  "LLM",
		Subject:   "llm.request.started",
		Trailing:  "payload_bytes=42",
	}
	api1.Push(entry)

	if err := db1.Close(ctx); err != nil {
		t.Fatalf("close: %v", err)
	}

	db2, store2 := openStoreAt(t, dir)
	defer func() { _ = db2.Close(ctx) }()
	api2 := NewAPI(WithStore(store2))

	entries, err := api2.ListEntries(ctx, Filter{})
	if err != nil {
		t.Fatalf("ListEntries after reopen: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("ListEntries after reopen returned %d entries, want 1: %+v", len(entries), entries)
	}
	if entries[0].ID != entry.ID || entries[0].Subject != entry.Subject || entries[0].Trailing != entry.Trailing {
		t.Errorf("ListEntries after reopen = %+v, want id/subject/trailing matching %+v", entries[0], entry)
	}

	filtered, err := api2.Filter(ctx, eventlog.FilterQuery{Kinds: []string{entry.Subject}})
	if err != nil {
		t.Fatalf("Filter after reopen: %v", err)
	}
	if len(filtered) != 1 || filtered[0].ID != entry.ID {
		t.Fatalf("Filter after reopen = %+v, want exactly the reopened entry", filtered)
	}

	ok, err := api2.VerifyEntry(ctx, entry.ID)
	if err != nil {
		t.Fatalf("VerifyEntry after reopen: %v", err)
	}
	if !ok {
		t.Error("VerifyEntry after reopen = false, want true")
	}
}

// alwaysErrorBackend implements eventlog.Backend with AppendRow always
// failing — every other method delegates to an embedded MemoryBackend
// so the fixture doesn't have to hand-implement all ten methods to
// exercise the one that matters for AC-005.
type alwaysErrorBackend struct {
	*eventlog.MemoryBackend
}

var errInjectedWriteFailure = errors.New("injected: store is unavailable")

func (b *alwaysErrorBackend) AppendRow(ctx context.Context, row eventlog.Row, expectedHead [32]byte) error {
	return errInjectedWriteFailure
}

// TestPush_StoreWriteFailureDoesNotFailCaller is AC-005: a backend
// whose AppendRow always errors must not panic Push, and the ring
// (the "audited operation" as far as Push's caller is concerned — Push
// itself has no error return, so there is nothing for a caller to see
// fail) must still reflect the push. This is also the concrete
// evidence for spec D-5 / R-5: Push's signature has NO error return,
// by design, across all ten production call sites.
func TestPush_StoreWriteFailureDoesNotFailCaller(t *testing.T) {
	backend := &alwaysErrorBackend{MemoryBackend: eventlog.NewMemoryBackend()}
	store := eventlog.NewStore(backend)
	api := NewAPI(WithStore(store))

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Push panicked on a failing store write: %v", r)
		}
	}()

	entry := Entry{
		ID:        "write-fail-evt",
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Category:  "LLM",
		Subject:   "llm.request.started",
	}
	api.Push(entry) // must not panic

	// The ring path is unaffected by the store failure — this is what
	// "an audit write failure never fails the action being audited"
	// means at this layer: Push completed normally either way.
	got, err := api.VerifyEntry(context.Background(), entry.ID)
	if err != nil {
		t.Fatalf("VerifyEntry: %v", err)
	}
	// VerifyEntry reads the STORE when one is configured (impl.go) —
	// and the store never got the row (AppendRow always errors), so
	// membership correctly reports false here. What matters for AC-005
	// is that Push did not panic and did not block — proven above.
	if got {
		t.Error("VerifyEntry unexpectedly true against a backend whose AppendRow always fails")
	}
}

// TestFilter_StoreAndRingAgree drives the SAME sequence of Push calls
// into two API instances — one backed by a real store, one ring-only —
// and asserts Filter/ListEntries return identical results for the
// verbose, kind and free-text cases. This is spec §5.4 item 4's
// required parity check: "Filter's semantics must be reproduced
// exactly in SQL... a silent change in filter meaning is a defect
// nobody notices for a release."
func TestFilter_StoreAndRingAgree(t *testing.T) {
	ctx := context.Background()
	_, store := openStoreAt(t, t.TempDir())
	storeAPI := NewAPI(WithStore(store))
	ringAPI := NewAPI()

	entries := []Entry{
		{ID: "p-1", Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Category: "LLM", Subject: "llm.request.started"},
		{ID: "p-2", Timestamp: time.Now().UTC().Add(time.Second).Format(time.RFC3339Nano), Category: "MCP", Subject: "verbose.mcp.token.stream"},
		{ID: "p-3", Timestamp: time.Now().UTC().Add(2 * time.Second).Format(time.RFC3339Nano), Category: "POLICY", Subject: "policy.decision.denied"},
		{ID: "p-4", Timestamp: time.Now().UTC().Add(3 * time.Second).Format(time.RFC3339Nano), Category: "LLM", Subject: "llm.response.completed"},
	}
	for _, e := range entries {
		storeAPI.Push(e)
		ringAPI.Push(e)
	}

	cases := []eventlog.FilterQuery{
		{},                                       // verbose hidden by default
		{Verbose: true},                          // verbose included
		{Kinds: []string{"llm.request.started"}}, // exact kind match
		{FreeText: "policy"},                     // free-text substring on Subject
		{Kinds: []string{"llm.request.started", "llm.response.completed"}},
	}
	for i, q := range cases {
		sr, err := storeAPI.Filter(ctx, q)
		if err != nil {
			t.Fatalf("case %d: store Filter: %v", i, err)
		}
		rr, err := ringAPI.Filter(ctx, q)
		if err != nil {
			t.Fatalf("case %d: ring Filter: %v", i, err)
		}
		if len(sr) != len(rr) {
			t.Fatalf("case %d (%+v): store returned %d entries, ring returned %d\nstore=%+v\nring=%+v", i, q, len(sr), len(rr), sr, rr)
		}
		for j := range sr {
			if sr[j].ID != rr[j].ID || sr[j].Subject != rr[j].Subject {
				t.Errorf("case %d (%+v) idx %d: store=%+v ring=%+v", i, q, j, sr[j], rr[j])
			}
		}
	}

	// ListEntries category filter parity too.
	sle, err := storeAPI.ListEntries(ctx, Filter{Categories: []string{"LLM"}})
	if err != nil {
		t.Fatalf("store ListEntries: %v", err)
	}
	rle, err := ringAPI.ListEntries(ctx, Filter{Categories: []string{"LLM"}})
	if err != nil {
		t.Fatalf("ring ListEntries: %v", err)
	}
	if len(sle) != len(rle) || len(sle) != 2 {
		t.Fatalf("category-filtered ListEntries: store=%d ring=%d, want 2 each", len(sle), len(rle))
	}
}

// TestVerifyChain_NoStore_NeverReportsVerifiedTrue is AC-014 / G-2's
// headline assertion: with no store configured, VerifyChain must NOT
// report Verified: true. Before UNIT-7, impl.go's VerifyChain returned
// `VerifyChainResult{Verified: true, RowsChecked: checked}` — a
// literal, computed from a loop that only COUNTED ring entries in the
// id range and never touched a hash. This is the mutation G-2 names:
// restoring that literal must turn this test red.
//
// NOT a dedicated Available flag (see VerifyChainResult's doc comment,
// api.go, for why) — RowsChecked: 0 is the no-store signal at the
// current wire shape.
func TestVerifyChain_NoStore_NeverReportsVerifiedTrue(t *testing.T) {
	api := NewAPI() // no WithStore — the ring-only, pre-UNIT-4 posture.
	res, err := api.VerifyChain(context.Background(), "", "")
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if res.Verified {
		t.Error("Verified = true with no store configured — this is EXACTLY the manufactured-success defect " +
			"(spec D-6 / Rule 7 / G-2): a tamper-evidence surface must never report a verification it did not perform")
	}
	if res.RowsChecked != 0 {
		t.Errorf("RowsChecked = %d with no store configured, want 0", res.RowsChecked)
	}
}

// TestVerifyChain_WithStore_Verified drives real entries through the
// production Push path (real sqlite), then VerifyChain must report
// Verified: true — a genuine chain walk, not a literal.
func TestVerifyChain_WithStore_Verified(t *testing.T) {
	ctx := context.Background()
	_, store := openStoreAt(t, t.TempDir())
	api := NewAPI(WithStore(store))

	var lastID string
	for i := 0; i < 5; i++ {
		e := Entry{
			ID:        fmt.Sprintf("chain-ok-%02d", i),
			Timestamp: time.Now().UTC().Add(time.Duration(i) * time.Second).Format(time.RFC3339Nano),
			Category:  "LLM",
			Subject:   "llm.request.started",
		}
		api.Push(e)
		lastID = e.ID
	}

	res, err := api.VerifyChain(ctx, "chain-ok-00", lastID)
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if !res.Verified {
		t.Errorf("Verified = false for an untampered chain, want true (BrokenAtID=%q)", res.BrokenAtID)
	}
	if res.RowsChecked != 5 {
		t.Errorf("RowsChecked = %d, want 5", res.RowsChecked)
	}
	if res.BrokenAtID != "" {
		t.Errorf("BrokenAtID = %q on a verified chain, want empty", res.BrokenAtID)
	}
}

// TestVerifyChain_WithStore_TamperedRow_Identified is G-2's other half:
// a store holding a deliberately broken prev_hash link must report
// Verified: false AND identify the break, not just fail silently or
// report success.
func TestVerifyChain_WithStore_TamperedRow_Identified(t *testing.T) {
	ctx := context.Background()
	db, store := openStoreAt(t, t.TempDir())
	defer func() { _ = db.Close(ctx) }()
	api := NewAPI(WithStore(store))

	e := Entry{
		ID:        "chain-tamper-01",
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Category:  "LLM",
		Subject:   "llm.request.started",
	}
	api.Push(e)

	// Tamper: overwrite the persisted row's payload_hash directly
	// against the database, bypassing audit.API and eventlog.Store
	// entirely — exactly what a real tamper attempt would do, and
	// exactly what VerifyChain exists to detect (it recomputes the
	// hash from the stored payload/prev_hash/kind/emitted_at and
	// compares against the stored payload_hash — see chain.go).
	if err := db.WriteTx(ctx, func(tx storage.WriteTx) error {
		_, err := tx.Exec(ctx, "UPDATE events SET payload_hash = ? WHERE event_id = ?",
			[]byte{0xDE, 0xAD, 0xBE, 0xEF}, e.ID)
		return err
	}); err != nil {
		t.Fatalf("tamper UPDATE: %v", err)
	}

	res, err := api.VerifyChain(ctx, "", "")
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if res.Verified {
		t.Error("Verified = true for a tampered chain, want false")
	}
	if res.RowsChecked == 0 {
		t.Error("RowsChecked = 0 for a store holding one row — a real check must have examined it " +
			"(distinguishes this from the no-store case, which also reports Verified: false)")
	}
	if res.BrokenAtID == "" {
		t.Error("BrokenAtID is empty for a tampered chain — the break must be identified, not just detected")
	}
}

// TestPush_ConcurrentGoroutinesWithStore exercises -race: entries
// pushed from streaming goroutines while a Store is configured (Push's
// doc comment on the write-through calls this out explicitly).
//
// Every Push-sourced row shares ONE chain (Entry carries no SessionID,
// so rowFromEntry always uses ""; see the package doc comment) —
// concurrent pushes therefore genuinely contend for the SAME
// optimistic-concurrency head. Store.AppendComputed retries on
// ErrChainHeadMismatch (core/event/log/store.go) specifically because
// this test, before that retry loop existed, observed up to 15/20
// (75%) of concurrent pushes silently dropped from the store — logged
// and non-fatal per Push's D-5 contract, but "most concurrent audit
// rows vanish under load" is not acceptable for this mission. With the
// retry loop this test expects exact parity between what was pushed
// and what persisted; the RING (asserted via a second, store-less API
// fed the identical Push calls in parallel, since ListEntries reads
// the store when one is configured) is the reference for "every push
// happened at all," independent of the store's retry behaviour.
// TestExport_NoBackend_ErrorsNotEmptyFile is AC-009's second half: with
// no backend configured, Export must return the error, never a
// zero-row file — "an export that succeeds while omitting every row is
// fabricated completeness on a compliance surface."
func TestExport_NoBackend_ErrorsNotEmptyFile(t *testing.T) {
	api := NewAPI() // no WithSweepableBackend/WithBackend
	_, err := api.Export(context.Background(), eventlog.ExportOptions{
		DataDir: t.TempDir(),
		Format:  eventlog.ExportFormatJSONL,
	})
	if err == nil {
		t.Fatal("Export with no backend returned nil error, want one")
	}
}

// TestExport_WithBackend_WritesRealFileWithRows is AC-009's first
// half: real sqlite, real rows, Export must produce a file under
// <DataDir>/audit-exports/ that actually CONTAINS those rows — not
// just "no error returned".
func TestExport_WithBackend_WritesRealFileWithRows(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	cfg := storage.Config{DataDir: dataDir, EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close(ctx) }()
	backend := eventlog.NewSQLBackend(db)
	store := eventlog.NewStore(backend)
	api := NewAPI(WithStore(store), WithSweepableBackend(backend))

	entry := Entry{
		ID:        "export-evt-1",
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Category:  "LLM",
		Subject:   "llm.request.started",
		Trailing:  "payload_bytes=7",
	}
	api.Push(entry)

	path, err := api.Export(ctx, eventlog.ExportOptions{
		DataDir: dataDir,
		Format:  eventlog.ExportFormatJSONL,
	})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if path == "" {
		t.Fatal("Export returned an empty path with a nil error")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	if !strings.Contains(string(data), entry.ID) {
		t.Errorf("exported file does not contain the pushed entry's id %q:\n%s", entry.ID, data)
	}
	if !strings.Contains(string(data), entry.Subject) {
		t.Errorf("exported file does not contain the pushed entry's subject %q:\n%s", entry.Subject, data)
	}
}

// TestSavedQuery_SurvivesRelaunch is AC-010: a saved query with TWO
// kinds and TWO actors survives a load→save round trip AND a process
// relaunch — reading through audit.API's real ListSavedQueries/
// SaveQuery, not eventlog.SavedQueryStore directly (that unit-level
// proof lives in core/event/log/saved_queries_test.go; this proves the
// WithSavedQueryStore wiring specifically).
func TestSavedQuery_SurvivesRelaunch(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cfg := storage.Config{DataDir: dir, EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption}

	db1, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open (1): %v", err)
	}
	sqs1 := eventlog.NewSavedQueryStore(db1)
	api1 := NewAPI(WithSavedQueryStore(sqs1))

	q := eventlog.SavedQuery{
		ID:   "sq-relaunch-1",
		Name: "two kinds two actors",
		Query: eventlog.FilterQuery{
			Kinds:    []string{"llm.request.started", "llm.response.completed"},
			ActorIDs: []string{"actor-x", "actor-y"},
		},
	}
	if err := api1.SaveQuery(ctx, q); err != nil {
		t.Fatalf("SaveQuery: %v", err)
	}
	// Load→save round trip within the SAME process, before the
	// relaunch — AC-010's "first hop".
	listed, err := api1.ListSavedQueries(ctx)
	if err != nil {
		t.Fatalf("ListSavedQueries: %v", err)
	}
	if len(listed) != 1 || len(listed[0].Query.Kinds) != 2 || len(listed[0].Query.ActorIDs) != 2 {
		t.Fatalf("ListSavedQueries before relaunch = %+v, want 1 query with 2 kinds + 2 actors", listed)
	}
	if err := api1.SaveQuery(ctx, listed[0]); err != nil {
		t.Fatalf("re-SaveQuery: %v", err)
	}
	if err := db1.Close(ctx); err != nil {
		t.Fatalf("close db1: %v", err)
	}

	// The relaunch — AC-010's "second hop", the one an in-memory map
	// (a.savedQueries) satisfies wrongly (01PMZ808 AC-040's defect).
	db2, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open (2): %v", err)
	}
	defer func() { _ = db2.Close(ctx) }()
	sqs2 := eventlog.NewSavedQueryStore(db2)
	api2 := NewAPI(WithSavedQueryStore(sqs2))

	afterRelaunch, err := api2.ListSavedQueries(ctx)
	if err != nil {
		t.Fatalf("ListSavedQueries after relaunch: %v", err)
	}
	if len(afterRelaunch) != 1 {
		t.Fatalf("ListSavedQueries after relaunch = %+v, want 1", afterRelaunch)
	}
	got := afterRelaunch[0]
	if len(got.Query.Kinds) != 2 || got.Query.Kinds[0] != "llm.request.started" || got.Query.Kinds[1] != "llm.response.completed" {
		t.Errorf("Kinds after relaunch = %v, want both terms", got.Query.Kinds)
	}
	if len(got.Query.ActorIDs) != 2 || got.Query.ActorIDs[0] != "actor-x" || got.Query.ActorIDs[1] != "actor-y" {
		t.Errorf("ActorIDs after relaunch = %v, want both terms", got.Query.ActorIDs)
	}
}

// TestBulkPurge_RealBackend_TermUnsearchableNoError is WP07's explicit
// instruction: "the same for BulkPurge: purged event ids are
// unsearchable afterwards and the search does not error." Real
// sqlite — migration 106's FTS triggers apply to ANY delete from
// events, not just the scheduled sweep's, so BulkPurge benefits
// automatically; this is the direct proof.
func TestBulkPurge_RealBackend_TermUnsearchableNoError(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	cfg := storage.Config{DataDir: dataDir, EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close(ctx) }()
	backend := eventlog.NewSQLBackend(db)
	store := eventlog.NewStore(backend)
	api := NewAPI(WithStore(store), WithSweepableBackend(backend))

	if err := store.AppendComputed(ctx, eventlog.Row{
		EventID: "bulkpurge-evt-1", Kind: "k", EmittedAt: time.Now(),
		Payload: []byte(`{"term":"ZORKBULKPURGEQ"}`), RedactionSummary: "none",
	}); err != nil {
		t.Fatalf("append: %v", err)
	}

	before, err := backend.SearchFTS(ctx, "ZORKBULKPURGEQ", "", nil, 10)
	if err != nil {
		t.Fatalf("pre-purge SearchFTS: %v", err)
	}
	if len(before) != 1 {
		t.Fatalf("pre-purge SearchFTS = %d rows, want 1", len(before))
	}

	if err := api.BulkPurge(ctx, []string{"bulkpurge-evt-1"}); err != nil {
		t.Fatalf("BulkPurge: %v", err)
	}

	after, err := backend.SearchFTS(ctx, "ZORKBULKPURGEQ", "", nil, 10)
	if err != nil {
		t.Fatalf("post-purge SearchFTS errored (must succeed with zero rows, not error): %v", err)
	}
	if len(after) != 0 {
		t.Errorf("post-purge SearchFTS = %d rows, want 0", len(after))
	}
	var rawMatchCount int
	if qerr := db.Reader().QueryRow(ctx,
		"SELECT count(*) FROM events_fts WHERE events_fts MATCH ?", "ZORKBULKPURGEQ").Scan(&rawMatchCount); qerr != nil {
		t.Fatalf("raw events_fts MATCH count: %v", qerr)
	}
	if rawMatchCount != 0 {
		t.Errorf("raw events_fts MATCH count after purge = %d, want 0 (existence-oracle check)", rawMatchCount)
	}
}

func TestPush_ConcurrentGoroutinesWithStore(t *testing.T) {
	_, store := openStoreAt(t, t.TempDir())
	storeAPI := NewAPI(WithStore(store))
	ringOnlyAPI := NewAPI()

	const n = 20
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Push panicked: %v", r)
				}
			}()
			e := Entry{
				ID:        fmt.Sprintf("concurrent-%03d", i),
				Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
				Category:  "LLM",
				Subject:   "llm.request.started",
			}
			storeAPI.Push(e)
			ringOnlyAPI.Push(e)
		}()
	}
	wg.Wait()

	ringEntries, err := ringOnlyAPI.ListEntries(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("ring ListEntries: %v", err)
	}
	if len(ringEntries) != n {
		t.Errorf("ring ListEntries returned %d entries, want %d (the ring must never drop a push)", len(ringEntries), n)
	}

	storeEntries, err := storeAPI.ListEntries(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("store ListEntries: %v", err)
	}
	if len(storeEntries) != n {
		t.Errorf("store ListEntries returned %d entries, want %d (AppendComputed's retry loop should resolve chain-head contention for %d concurrent pushes)", len(storeEntries), n, n)
	}
}
