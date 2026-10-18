package activity

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// CLAUDE.md blind spot #3: an empty database cannot show an upgrade
// defect. testdata/upgrade/v0.98.0/dump.sql is a populated activity.db
// written by the RELEASED v0.98.0 store (schema v1; see PROVENANCE.md);
// HEAD must open it, keep every row and add v2 without touching them.
func TestUpgrade_FromV0980(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dump, err := os.ReadFile(filepath.FromSlash("testdata/upgrade/v0.98.0/dump.sql"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), DirName, FileName)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(string(dump)); err != nil {
		t.Fatalf("materialise dump: %v", err)
	}
	_ = raw.Close()

	want := map[string]int64{"events": 8, "tasks": 4, "ml_predictions": 4, "ml_events": 4}
	check := func(s *Store) {
		t.Helper()
		for tbl, n := range want {
			if got := count(t, s.db, `SELECT COUNT(*) FROM `+tbl); got != n {
				t.Fatalf("%s rows = %d, want %d (upgrade lost data)", tbl, got, n)
			}
		}
		if v, err := s.SchemaVersion(ctx); err != nil || v != 2 {
			t.Fatalf("schema version = %d, %v; want 2", v, err)
		}
		if got := count(t, s.db, `SELECT COUNT(*) FROM harness_activity_schema`); got != 2 {
			t.Fatalf("ledger rows = %d, want 2", got)
		}
	}
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open the v0.98.0 file under HEAD: %v", err)
	}
	check(s)
	if got := count(t, s.db, `SELECT COUNT(*) FROM harness_retention_choice`); got != 0 {
		t.Fatalf("harness_retention_choice rows = %d, want an empty table", got)
	}
	if _, ok, err := s.RetentionChoice(ctx); err != nil || ok {
		t.Fatalf("RetentionChoice on an upgraded file = %v, %v; want no choice and no error", ok, err)
	}
	_ = s.Close()

	s2, err := Open(path) // idempotent reopen
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	check(s2)
	if err := s2.SetRetentionChoice(ctx, 365); err != nil {
		t.Fatal(err)
	}
	if d, ok, err := s2.RetentionChoice(ctx); err != nil || !ok || d != 365 {
		t.Fatalf("choice round trip = %d/%v/%v", d, ok, err)
	}
	if st, err := s2.Stats(ctx); err != nil || st.Events != 8 || st.Tasks != 4 {
		t.Fatalf("stats after upgrade = %+v, %v", st, err)
	}
}
