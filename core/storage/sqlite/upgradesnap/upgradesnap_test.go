package upgradesnap_test

import (
	"context"
	"database/sql"
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
