package sqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/storage/sqlite/upgradesnap"

	_ "modernc.org/sqlite"
)

// TestMigration0341_DedupesDoubledTurnsAgainstUpgradedDatabase pins the
// row-level contract of sessions/0341-dedupe-user-turns
// (chat-single-writer-01DOGF0G WP05, spec FR-2 / AC-2 / AC-3 / AC-4).
//
// THE HAZARD. 0341 DELETEs and UPDATEs session_messages rows on real,
// upgraded installs. CLAUDE.md blind spot #3: a migration that has never
// run against populated tables has never been tested. So this boots two
// databases a PREVIOUS RELEASE produced — testdata/upgrade/v0.85.2 (the
// newest snapshot, move-era schema: turn_span_id exists and anchors the
// doubled turns exactly as the bug wrote them) and testdata/upgrade/v0.63.0
// (the OLDEST committed snapshot, whose session_messages predates the move
// columns entirely: 0333 adds them during this very Open, so these pairs
// carry no span on either side — the shape 29 of the 40 dev-profile pairs
// have) — seeds every shape below into each, and runs the production Open.
//
// No pre-v0.63.0 snapshot exists (v0.63.0 is the genesis of the chain, see
// its PROVENANCE.md), so v0.63.0 stands in for "pre-bug rows present".
//
// SHAPES (each in session g-dedupe unless noted):
//
//	1  text-only pair, 5 ms apart                 -> A deleted, B survives, span intact
//	2  text+image pair (A has the image, B not)   -> B deleted, A survives WITH image, span re-pointed to A
//	3  identical text re-sent 6.6 s later         -> both survive (outside the 2 s window)
//	4  pair whose A is a branch's parent_message  -> skipped, both survive (FR-2c)
//	5  final answer + kind-less failed twin        -> twin deleted, answer survives
//	6  two kind-less failed equal partials         -> both survive (not decidable)
//	7  lone genuine error partial                  -> survives
//	8  g-prebug: one user row, no twin (AC-4)      -> untouched
//	   seed-session-1 (the snapshot's own rows)    -> byte-identical
//
// FALSIFIABILITY (run during WP05, recorded in the commit): replacing the
// survivor rule with "always delete the earlier row" deletes the
// image-bearing row in shape 2 and this test fails on
// "g-u2a ... (0 rows means it was incorrectly deleted)"; dropping the
// window check deletes shape 3's genuine re-send.
func TestMigration0341_DedupesDoubledTurnsAgainstUpgradedDatabase(t *testing.T) {
	for _, tag := range []string{"v0.85.2", "v0.63.0"} {
		tag := tag
		t.Run(tag, func(t *testing.T) {
			t.Parallel()
			runMigration0341Case(t, tag)
		})
	}
}

const (
	g0341Base  = int64(1_780_000_000_000_000_000) // unix-nanos, like production created_at
	g0341ImgJS = `[{"type":"text","text":"look at this diagram"},{"type":"image","source":{"kind":"base64","media_type":"image/png","data":"iVBORw0KGgo="}}]`
	g0341TxtJS = `[{"type":"text","text":"look at this diagram"}]`
)

func runMigration0341Case(t *testing.T, tag string) {
	ctx := context.Background()
	dir := t.TempDir()
	rawPath := filepath.Join(dir, "data.db")

	dumpText, err := os.ReadFile(filepath.Join("testdata", "upgrade", tag, "dump.sql"))
	if err != nil {
		t.Fatalf("read %s dump.sql: %v", tag, err)
	}
	raw := openRawSQLiteAt(t, rawPath)
	if err := upgradesnap.Materialize(ctx, raw, string(dumpText)); err != nil {
		t.Fatalf("materialise %s: %v", tag, err)
	}
	moveEra := columnExists(t, raw, "session_messages", "turn_span_id")

	ms := func(n int64) int64 { return g0341Base + n*1_000_000 }
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := raw.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	exec(`INSERT INTO sessions (id, name, created_at, updated_at, last_active_at, position)
	      VALUES ('g-dedupe', 'F12 doubled turns', 1, 1, 1, 50), ('g-prebug', 'pre-bug single turn', 1, 1, 1, 51)`)
	msg := func(id, sid string, seq int64, role, content string, at int64, contentJSON any) {
		t.Helper()
		exec(`INSERT INTO session_messages (id, session_id, sequence, role, content, created_at, content_json)
		      VALUES (?, ?, ?, ?, ?, ?, ?)`, id, sid, seq, role, content, at, contentJSON)
	}
	failed := func(id string, at int64, kind string) {
		t.Helper()
		exec(`UPDATE session_messages SET streaming_failed_at = ?, streaming_failure_kind = ?, streaming_recoverable = 1 WHERE id = ?`, at, kind, id)
	}
	move := func(id, kind, span string) {
		t.Helper()
		if moveEra {
			exec(`UPDATE session_messages SET kind = ?, move_index = 0, turn_span_id = ? WHERE id = ?`, kind, span, id)
		}
	}

	// 1 text-only pair (the frontend's row A, the runner's row B; B anchors the turn).
	msg("g-u1a", "g-dedupe", 0, "user", "what is a zebracorn", ms(0), `[{"type":"text","text":"what is a zebracorn"}]`)
	msg("g-u1b", "g-dedupe", 1, "user", "what is a zebracorn", ms(5), `[{"type":"text","text":"what is a zebracorn"}]`)
	msg("g-a1", "g-dedupe", 2, "assistant", "a horse with ideas", ms(900), nil)
	move("g-a1", "final", "g-u1b")
	// 2 multimodal pair: A carries the image, the runner's B is text-only and anchors the turn.
	msg("g-u2a", "g-dedupe", 3, "user", "look at this diagram", ms(2000), g0341ImgJS)
	msg("g-u2b", "g-dedupe", 4, "user", "look at this diagram", ms(2005), g0341TxtJS)
	msg("g-a2", "g-dedupe", 5, "assistant", "it is a state machine", ms(3000), nil)
	move("g-a2", "final", "g-u2b")
	// 3 a genuine re-send, 6.6 s apart.
	msg("g-u3a", "g-dedupe", 6, "user", "again?", ms(10_000), nil)
	msg("g-u3b", "g-dedupe", 7, "user", "again?", ms(16_600), nil)
	// 4 a pair whose A a branch points at — must be skipped, not dangled.
	msg("g-u4a", "g-dedupe", 8, "user", "branch from here", ms(20_000), nil)
	msg("g-u4b", "g-dedupe", 9, "user", "branch from here", ms(20_004), nil)
	exec(`INSERT INTO branches (id, parent_session_id, child_session_id, created_at, updated_at, parent_message_id)
	      VALUES ('g-branch', 'g-dedupe', 'seed-session-2', 1, 1, 'g-u4a')`)
	// 5 the answer, then its kind-less failed twin 4.8 s later.
	msg("g-a5", "g-dedupe", 10, "assistant", "the answer is 42", ms(30_000), nil)
	move("g-a5", "final", "g-u4b")
	msg("g-r5", "g-dedupe", 11, "assistant", "the answer is 42", ms(34_800), nil)
	failed("g-r5", ms(34_800), "transient")
	// 6 two equal failed partials — neither is "the answer".
	msg("g-p6a", "g-dedupe", 12, "assistant", "partial words", ms(40_000), nil)
	failed("g-p6a", ms(40_000), "transient")
	msg("g-p6b", "g-dedupe", 13, "assistant", "partial words", ms(40_026), nil)
	failed("g-p6b", ms(40_026), "transient")
	// 7 a lone genuine error partial.
	msg("g-p7", "g-dedupe", 14, "assistant", "I was cut off mid", ms(50_000), nil)
	failed("g-p7", ms(50_000), "transient")
	// 8 AC-4: a pre-bug session with a single user row.
	msg("g-pre-u", "g-prebug", 0, "user", "hello from before the bug", ms(0), nil)
	msg("g-pre-a", "g-prebug", 1, "assistant", "hello back", ms(500), nil)

	seedDigestBefore := sessionRowsDigest(t, ctx, raw, "seed-session-1")
	preDigestBefore := sessionRowsDigest(t, ctx, raw, "g-prebug")
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	// ---- production Open: applies 0341 (and everything else above the snapshot). ----
	db, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("Open on the seeded %s snapshot failed: %v", tag, err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	r := db.Reader()

	var action string
	if err := r.QueryRow(ctx, "SELECT action FROM harness_migrations WHERE id = ? AND owning_mission = 'sessions'",
		"sessions/0341-dedupe-user-turns").Scan(&action); err != nil || action != "applied" {
		t.Fatalf("ledger row for sessions/0341-dedupe-user-turns: action=%q err=%v, want applied", action, err)
	}

	gone := func(id string) {
		t.Helper()
		var n int
		if err := r.QueryRow(ctx, "SELECT COUNT(*) FROM session_messages WHERE id = ?", id).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", id, err)
		}
		if n != 0 {
			t.Errorf("%s survived 0341, want deleted", id)
		}
	}
	kept := func(id string) (content string, contentJSON sql.NullString) {
		t.Helper()
		if err := r.QueryRow(ctx, "SELECT content, content_json FROM session_messages WHERE id = ?", id).
			Scan(&content, &contentJSON); err != nil {
			t.Fatalf("read %s after Open: %v (0 rows means it was incorrectly deleted)", id, err)
		}
		return content, contentJSON
	}
	span := func(id string) string {
		t.Helper()
		var s sql.NullString
		if err := r.QueryRow(ctx, "SELECT turn_span_id FROM session_messages WHERE id = ?", id).Scan(&s); err != nil {
			t.Fatalf("read span of %s: %v", id, err)
		}
		return s.String
	}

	// 1
	gone("g-u1a")
	kept("g-u1b")
	if moveEra && span("g-a1") != "g-u1b" {
		t.Errorf("g-a1 span = %q, want g-u1b (the survivor)", span("g-a1"))
	}
	// 2
	gone("g-u2b")
	if _, js := kept("g-u2a"); !strings.Contains(js.String, `"type":"image"`) {
		t.Errorf("multimodal survivor g-u2a lost its image block: %q", js.String)
	}
	if moveEra && span("g-a2") != "g-u2a" {
		t.Errorf("g-a2 span = %q, want re-pointed to the image-bearing survivor g-u2a", span("g-a2"))
	}
	// 3
	kept("g-u3a")
	kept("g-u3b")
	// 4
	kept("g-u4a")
	kept("g-u4b")
	// 5
	gone("g-r5")
	kept("g-a5")
	// 6, 7
	kept("g-p6a")
	kept("g-p6b")
	kept("g-p7")

	// AC-2: every turn_span_id resolves to an existing row.
	var dangling int
	if err := r.QueryRow(ctx, `SELECT COUNT(*) FROM session_messages m
	    WHERE m.turn_span_id IS NOT NULL AND m.turn_span_id <> ''
	      AND NOT EXISTS (SELECT 1 FROM session_messages u WHERE u.id = m.turn_span_id)`).Scan(&dangling); err != nil {
		t.Fatalf("dangling span query: %v", err)
	}
	if dangling != 0 {
		t.Errorf("%d row(s) carry a turn_span_id that no longer resolves", dangling)
	}

	// The FTS index followed the deletes (messages_fts_ad trigger): the
	// doubled turn's text is indexed exactly once.
	var hits int
	if err := r.QueryRow(ctx, "SELECT COUNT(*) FROM messages_fts WHERE messages_fts MATCH 'zebracorn'").Scan(&hits); err != nil {
		t.Fatalf("fts query: %v", err)
	}
	if hits != 1 {
		t.Errorf("messages_fts hits for the deduped turn = %d, want 1", hits)
	}

	// AC-2 / AC-4: unrelated sessions are byte-identical.
	postRaw := openRawSQLiteAt(t, rawPath)
	defer func() { _ = postRaw.Close() }()
	if got := sessionRowsDigest(t, ctx, postRaw, "seed-session-1"); got != seedDigestBefore {
		t.Errorf("seed-session-1 rows changed under 0341:\nbefore %s\nafter  %s", seedDigestBefore, got)
	}
	if got := sessionRowsDigest(t, ctx, postRaw, "g-prebug"); got != preDigestBefore {
		t.Errorf("pre-bug single-row session changed under 0341 (AC-4):\nbefore %s\nafter  %s", preDigestBefore, got)
	}
}

// sessionRowsDigest renders every column of a session's message rows
// that exists in every schema era, in sequence order, as one string.
func sessionRowsDigest(t *testing.T, ctx context.Context, db *sql.DB, sessionID string) string {
	t.Helper()
	rows, err := db.QueryContext(ctx, `SELECT id, sequence, role, content, IFNULL(content_json,''), created_at,
	        IFNULL(streaming_failed_at,0), IFNULL(continuation_of,'')
	      FROM session_messages WHERE session_id = ? ORDER BY sequence, id`, sessionID)
	if err != nil {
		t.Fatalf("digest %s: %v", sessionID, err)
	}
	defer func() { _ = rows.Close() }()
	var b strings.Builder
	n := 0
	for rows.Next() {
		var id, role, content, js, cont string
		var seq, at, failedAt int64
		if err := rows.Scan(&id, &seq, &role, &content, &js, &at, &failedAt, &cont); err != nil {
			t.Fatalf("digest scan: %v", err)
		}
		n++
		fmt.Fprintf(&b, "%s|%d|%s|%s|%s|%d|%d|%s\n", id, seq, role, content, js, at, failedAt, cont)
	}
	if n == 0 {
		t.Fatalf("digest %s: no rows — the comparison would be vacuous", sessionID)
	}
	return b.String()
}
