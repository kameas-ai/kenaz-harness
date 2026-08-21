package upgradesnap_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/storage/sqlite/upgradesnap"

	_ "modernc.org/sqlite"
)

func openRaw(t *testing.T, path string) *sql.DB {
	t.Helper()
	dsn := "file:" + url.PathEscape(path) + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestDumpMaterializeRoundTrip proves the core mechanism the whole
// snapshot chain depends on: a database dumped and then materialised
// into a fresh file produces the SAME dump when dumped again. This is
// the property scripts/ci/upgrade-snapshot.sh's byte-identical
// regeneration and check-upgrade-snapshots-locked.sh both rely on.
func TestDumpMaterializeRoundTrip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	src := openRaw(t, filepath.Join(dir, "src.db"))
	schema := `
		CREATE TABLE parent (id TEXT PRIMARY KEY, name TEXT NOT NULL);
		CREATE TABLE child (id INTEGER PRIMARY KEY AUTOINCREMENT, parent_id TEXT NOT NULL REFERENCES parent(id) ON DELETE CASCADE, note TEXT);
		CREATE INDEX idx_child_parent ON child (parent_id);
	`
	for _, stmt := range splitTestSQL(schema) {
		if _, err := src.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("schema %q: %v", stmt, err)
		}
	}
	if _, err := src.ExecContext(ctx, `INSERT INTO parent (id, name) VALUES ('p2','second'), ('p1','first')`); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	if _, err := src.ExecContext(ctx, `INSERT INTO child (parent_id, note) VALUES ('p1','a'), ('p1','b'), ('p2','c')`); err != nil {
		t.Fatalf("seed child: %v", err)
	}

	dump1, err := upgradesnap.Dump(ctx, src)
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}

	dst := openRaw(t, filepath.Join(dir, "dst.db"))
	if err := upgradesnap.Materialize(ctx, dst, dump1); err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	dump2, err := upgradesnap.Dump(ctx, dst)
	if err != nil {
		t.Fatalf("Dump (round-trip): %v", err)
	}

	if dump1 != dump2 {
		t.Fatalf("round-trip dump mismatch.\n--- original ---\n%s\n--- after materialise+dump ---\n%s", dump1, dump2)
	}

	// Data sorted by PK: parent rows must come out p1 before p2 despite
	// being inserted p2 then p1.
	var firstParent string
	if err := dst.QueryRowContext(ctx, "SELECT id FROM parent ORDER BY id LIMIT 1").Scan(&firstParent); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if firstParent != "p1" {
		t.Fatalf("first parent = %q, want p1", firstParent)
	}

	// FK still enforced post-materialise (defer_foreign_keys is
	// transaction-scoped, not a permanent relaxation).
	if _, err := dst.ExecContext(ctx, `INSERT INTO child (parent_id, note) VALUES ('does-not-exist','x')`); err == nil {
		t.Fatal("expected FK violation inserting a child with a nonexistent parent after materialise")
	}
}

// TestDumpNormalizesInstallIDAndTimestamps proves the normalisation
// rule that makes two runs of the same logical database produce
// byte-identical dumps: harness_meta.install_id and every applied_at
// (any table) are replaced with fixed literals; everything else,
// including content_hash, is verbatim.
func TestDumpNormalizesInstallIDAndTimestamps(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db := openRaw(t, filepath.Join(dir, "meta.db"))

	schema := `
		CREATE TABLE harness_meta (install_id TEXT NOT NULL PRIMARY KEY, created_at TEXT NOT NULL);
		CREATE TABLE harness_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL, content_hash TEXT NOT NULL);
	`
	for _, stmt := range splitTestSQL(schema) {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO harness_meta (install_id, created_at) VALUES ('real-install-abc123', '2026-08-18T10:00:00Z')`); err != nil {
		t.Fatalf("seed meta: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO harness_migrations (version, applied_at, content_hash) VALUES (1, '2026-08-18T10:00:01Z', 'realhash123')`); err != nil {
		t.Fatalf("seed ledger: %v", err)
	}

	dump, err := upgradesnap.Dump(ctx, db)
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}

	if containsAny(dump, "real-install-abc123", "2026-08-18T10:00:00Z", "2026-08-18T10:00:01Z") {
		t.Fatalf("dump leaked a real timestamp/install_id, want fixed literals:\n%s", dump)
	}
	if !containsAny(dump, "realhash123") {
		t.Fatalf("content_hash must be verbatim (load-bearing — VerifyLedger compares it), got:\n%s", dump)
	}
}

// TestDumpHandlesSecondFTS5TableAndBinaryBlobs is a regression test for
// two compounding defects found while producing the v0.65.0 upgrade
// snapshot for audit-that-tells-the-truth-01PMZA10 (event-log's
// "events_fts" is the SECOND fts5 virtual table this repo has ever had,
// after session search's "messages_fts"):
//
//  1. isFTSShadow used to consult a hardcoded map holding only
//     "messages_fts". A table's fts5 shadow tables (<name>_data, _idx,
//     _docsize, _config) hold SQLite-internal compressed index bytes —
//     not source-of-truth data — and are supposed to be excluded from
//     the dump entirely (Materialize recreates them via the AFTER
//     INSERT trigger replay, not by data-section INSERTs). A second
//     FTS5 table with a different name was invisible to that map, so
//     its shadow tables got dumped as ordinary tables.
//  2. sqlLiteral's []byte case used to wrap raw bytes in a quoted SQL
//     string ('...'). FTS5 shadow-table blob columns (and this
//     repo's own events.payload_hash/prev_hash) hold real binary
//     content, which can and does contain literal NUL/control bytes —
//     invalid inside a naive quoted-string literal and, observed
//     directly: `file(1)` reports the resulting dump.sql as "data",
//     not text.
//
// This test creates a SECOND fts5 table (not "messages_fts") over a
// content column holding enough distinct terms to force multi-block
// FTS5 internal storage, confirms the dump is pure printable ASCII (no
// embedded NUL), and round-trips it through Materialize+Dump exactly
// like TestDumpMaterializeRoundTrip.
func TestDumpHandlesSecondFTS5TableAndBinaryBlobs(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	src := openRaw(t, filepath.Join(dir, "src.db"))
	schema := `
		CREATE TABLE notes (id INTEGER PRIMARY KEY, body TEXT NOT NULL, digest BLOB NOT NULL);
		CREATE VIRTUAL TABLE notes_fts USING fts5(body, content='notes', content_rowid='id');
	`
	for _, stmt := range splitTestSQL(schema) {
		if _, err := src.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("schema %q: %v", stmt, err)
		}
	}
	// Issued as one raw Exec (not through splitTestSQL's naive ';'
	// splitter, which is not BEGIN/END-aware and would cut this body
	// in half at the INSERT's internal semicolon).
	const notesTrigger = `CREATE TRIGGER notes_ai AFTER INSERT ON notes BEGIN
		INSERT INTO notes_fts(rowid, body) VALUES (new.id, new.body);
	END;`
	if _, err := src.ExecContext(ctx, notesTrigger); err != nil {
		t.Fatalf("create trigger: %v", err)
	}
	// Insert enough rows with varied text (and a BLOB column carrying
	// arbitrary bytes, including 0x00) to give fts5 real shadow-table
	// content, not an empty index.
	insert, err := src.PrepareContext(ctx, `INSERT INTO notes (id, body, digest) VALUES (?, ?, ?)`)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	for i := 0; i < 200; i++ {
		digest := make([]byte, 32)
		for b := range digest {
			digest[b] = byte((i*31 + b*7) % 256) // deterministic; guaranteed to hit 0x00 for some i,b
		}
		body := fmt.Sprintf("note number %d covers alpha bravo charlie delta echo foxtrot golf-%d", i, i)
		if _, err := insert.ExecContext(ctx, i, body, digest); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	if err := insert.Close(); err != nil {
		t.Fatalf("close stmt: %v", err)
	}

	dump, err := upgradesnap.Dump(ctx, src)
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}

	// (1) No embedded NUL / non-printable-outside-newline bytes: the
	// dump must be plain text, not "data" the way the broken generator
	// produced.
	for i, c := range []byte(dump) {
		if c == 0 {
			t.Fatalf("dump.sql contains a literal NUL byte at offset %d — the exact defect this test guards against", i)
		}
	}

	// (2) The shadow tables must not appear as ordinary dumped tables.
	if containsAny(dump, `"notes_fts_data"`, `"notes_fts_idx"`, `"notes_fts_docsize"`, `"notes_fts_config"`) {
		t.Fatalf("dump includes an fts5 shadow table by name — isFTSShadow failed to recognise notes_fts:\n%s", firstNLines(dump, 40))
	}

	// (3) The BLOB column round-trips as a hex literal, not a raw string.
	if !containsAny(dump, "X'") {
		t.Fatalf("expected at least one X'...' BLOB literal for the digest column, got none:\n%s", firstNLines(dump, 40))
	}

	// (4) Full round-trip: materialise into a fresh db, dump again,
	// byte-identical — same property TestDumpMaterializeRoundTrip pins,
	// now exercised with a second FTS5 table and real BLOB content.
	dst := openRaw(t, filepath.Join(dir, "dst.db"))
	if err := upgradesnap.Materialize(ctx, dst, dump); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	dump2, err := upgradesnap.Dump(ctx, dst)
	if err != nil {
		t.Fatalf("Dump (round-trip): %v", err)
	}
	if dump != dump2 {
		t.Fatalf("round-trip dump mismatch with a second FTS5 table + BLOB content")
	}

	// (5) The BLOB values themselves survived the round trip (not just
	// the dump text) — read one back and compare against what was
	// inserted.
	var got []byte
	if err := dst.QueryRowContext(ctx, `SELECT digest FROM notes WHERE id = 5`).Scan(&got); err != nil {
		t.Fatalf("read back digest: %v", err)
	}
	want := make([]byte, 32)
	for b := range want {
		want[b] = byte((5*31 + b*7) % 256)
	}
	if len(got) != len(want) {
		t.Fatalf("digest length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("digest[%d] = %#x, want %#x", i, got[i], want[i])
		}
	}

	// (6) FTS5 search still works post-materialise, proving the shadow
	// tables were genuinely rebuilt via trigger replay rather than
	// silently left empty.
	var matchCount int
	if err := dst.QueryRowContext(ctx, `SELECT count(*) FROM notes_fts WHERE notes_fts MATCH 'charlie'`).Scan(&matchCount); err != nil {
		t.Fatalf("fts5 search after materialise: %v", err)
	}
	if matchCount != 200 {
		t.Fatalf("fts5 match count after materialise = %d, want 200 (every row contains \"charlie\")", matchCount)
	}
}

func firstNLines(s string, n int) string {
	lines := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines++
			if lines >= n {
				return s[:i]
			}
		}
	}
	return s
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if !contains(s, sub) {
			continue
		}
		return true
	}
	return false
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func splitTestSQL(script string) []string {
	var out []string
	var cur []byte
	for i := 0; i < len(script); i++ {
		c := script[i]
		if c == ';' {
			s := trimTestSpace(string(cur))
			if s != "" {
				out = append(out, s)
			}
			cur = cur[:0]
			continue
		}
		cur = append(cur, c)
	}
	if s := trimTestSpace(string(cur)); s != "" {
		out = append(out, s)
	}
	return out
}

func trimTestSpace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}
