package activity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ddlDocPath is the kenaz-ml handoff copy of the schema (spec §12 A-16,
// tasks.md WP11 "Send kenaz-ml the final DDL for its fixture test").
const ddlDocPath = "../../../docs/telemetry/ml-activity-db.sql"

const ddlDocHeader = `-- Local ML activity store: <dataDir>/ml/activity.db (ml-producer-01MLPRD01 WP11).
-- GENERATED from core/mlproducer/activity.DDL(); do not edit by hand.
-- Regenerate: UPDATE_ACTIVITY_DDL=1 go test ./core/mlproducer/activity -run TestDDLDoc
--
-- Source shape: kenaz-fleet service/mltenant/mltenant.go @ d53c8c9 (:160-237),
-- SQLite-mapped (BIGSERIAL -> INTEGER PRIMARY KEY AUTOINCREMENT, UUID -> TEXT,
-- JSONB -> TEXT, received_at -> INTEGER unix ms). The harness creates every
-- table below; the engine creates and owns ml_cursor. Local rows carry
-- user_id = '' and node_id = '', producer = 'harness', source = 'harness.agent',
-- device_event_id = id; ts / started_at / last_active / completed_at are unix ms.
-- Connections: journal_mode=WAL, busy_timeout=5000. The engine is always handed the
-- absolute path in KENAZ_ML_ACTIVITY_DB (a non-standard data dir uses
-- <dataDir>/ml-activity/activity.db); it never derives one. KENAZ_ML_ACTIVITY_RETENTION_DAYS
-- is read once at spawn: a decimal integer 1..3650 (whitespace trimmed) prunes
-- ml_predictions (created_at) and ml_events (ts) older than that many days; "hold"
-- (case-insensitive) means do not prune this run; missing, empty or any other value
-- means do not prune, with one warning. Never clamped; no 90-day default engine-side.
-- The engine never prunes events / tasks. The harness sends the effective local
-- retention (the last value it read successfully when a read fails) and "hold" when it
-- has none; a change applies at the engine's next start.
`

func renderDDLDoc() string {
	var b strings.Builder
	b.WriteString(ddlDocHeader)
	for _, stmt := range DDL() {
		b.WriteString("\n")
		lines := strings.Split(stmt, "\n")
		for i, l := range lines {
			if i > 0 {
				l = "  " + strings.TrimLeft(l, "\t")
				if strings.HasPrefix(strings.TrimSpace(l), ")") {
					l = strings.TrimSpace(l)
				}
			}
			b.WriteString(l)
			b.WriteString("\n")
		}
		// Replace the trailing newline with ";\n".
		s := strings.TrimSuffix(b.String(), "\n") + ";\n"
		b.Reset()
		b.WriteString(s)
	}
	return b.String()
}

func TestDDLDoc_MatchesSchema(t *testing.T) {
	want := renderDDLDoc()
	if os.Getenv("UPDATE_ACTIVITY_DDL") == "1" {
		if err := os.WriteFile(filepath.FromSlash(ddlDocPath), []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(filepath.FromSlash(ddlDocPath))
	if err != nil {
		t.Fatalf("read %s: %v (regenerate with UPDATE_ACTIVITY_DDL=1)", ddlDocPath, err)
	}
	if string(got) != want {
		t.Fatalf("%s is stale; regenerate with UPDATE_ACTIVITY_DDL=1 go test ./core/mlproducer/activity -run TestDDLDoc", ddlDocPath)
	}
}
