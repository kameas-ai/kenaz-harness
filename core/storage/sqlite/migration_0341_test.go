package sqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/logging"

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
// databases a PREVIOUS RELEASE produced — the NEWEST committed snapshot
// that predates 0341 (newestSnapshotBefore0341; v0.86.0 when re-pointed by
// units-debt-01UNITD01 WP03, was a hard-coded v0.85.2 — adversarial review
// F4; move-era schema: turn_span_id exists and anchors the
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
	// Installed before the parallel subtests start, restored after they
	// all finish (parent cleanups run last).
	captureDedupeLogs(t)
	for _, tag := range []string{newestSnapshotBefore0341(t), "v0.63.0"} {
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

	// ---- GUARD SHAPES (review M1): one session per skip-guard, each a
	// text-only doubled pair (A at seq 0, B at seq 1, 3 ms apart) that
	// 0341 would dedupe were it not for the guard. Session ids carry the
	// tag so the two parallel subtests' log lines never mix.
	gsid := func(name string) string { return "gg-" + name + "-" + tag }
	guardPair := func(name, aID, bID string, aJSON, bJSON any) {
		t.Helper()
		sid := gsid(name)
		exec(`INSERT INTO sessions (id, name, created_at, updated_at, last_active_at, position)
		      VALUES (?, ?, 1, 1, 1, 60)`, sid, "guard "+name)
		msg(aID, sid, 0, "user", "guarded duplicate "+name, ms(100_000), aJSON)
		msg(bID, sid, 1, "user", "guarded duplicate "+name, ms(100_003), bJSON)
	}
	// continuation_of: a later row continues A.
	guardPair("cont", "gg-cont-a", "gg-cont-b", nil, nil)
	msg("gg-cont-c", gsid("cont"), 2, "assistant", "continued", ms(100_500), nil)
	exec(`UPDATE session_messages SET continuation_of = 'gg-cont-a' WHERE id = 'gg-cont-c'`)
	// compacted_into_id: a row was compacted into A.
	guardPair("comp", "gg-comp-a", "gg-comp-b", nil, nil)
	msg("gg-comp-c", gsid("comp"), 2, "assistant", "compacted", ms(100_500), nil)
	exec(`UPDATE session_messages SET compacted_into_id = 'gg-comp-a' WHERE id = 'gg-comp-c'`)
	// branch_message_refs.parent_msg_id = A, and .child_msg_id = A.
	guardPair("brp", "gg-brp-a", "gg-brp-b", nil, nil)
	exec(`INSERT INTO branch_message_refs (branch_id, seq, parent_msg_id, child_msg_id) VALUES ('g-branch', 0, 'gg-brp-a', '')`)
	guardPair("brc", "gg-brc-a", "gg-brc-b", nil, nil)
	exec(`INSERT INTO branch_message_refs (branch_id, seq, parent_msg_id, child_msg_id) VALUES ('g-branch', 1, 'other-parent', 'gg-brc-a')`)
	// artifact source_ref_json naming A exactly (the quoted instr match)...
	guardPair("art", "gg-art-a", "gg-art-b", nil, nil)
	exec(`INSERT INTO artifacts (id, session_id, title, mime_type, content_hash, byte_size, source, source_ref_json, created_at)
	      VALUES ('gg-artifact-1', ?, 't', 'text/plain', 'h1', 1, 'user_pin', '{"message_id":"gg-art-a"}', 1)`, gsid("art"))
	// ...and the NEGATIVE: an artifact naming a DIFFERENT id that merely
	// starts with A's id. The quotes in the instr probe are what keep this
	// from matching; the pair must be deduped normally.
	guardPair("artneg", "gg-artneg-a", "gg-artneg-b", nil, nil)
	exec(`INSERT INTO artifacts (id, session_id, title, mime_type, content_hash, byte_size, source, source_ref_json, created_at)
	      VALUES ('gg-artifact-2', ?, 't', 'text/plain', 'h2', 1, 'user_pin', '{"message_id":"gg-artneg-a-copy"}', 1)`, gsid("artneg"))
	// archived_at disagreement: A was compacted away, B was not.
	guardPair("arch", "gg-arch-a", "gg-arch-b", nil, nil)
	exec(`UPDATE session_messages SET archived_at = ? WHERE id = 'gg-arch-a'`, ms(100_100))
	// both rows carry a non-text block — a shape the bug never produced.
	guardPair("rich", "gg-rich-a", "gg-rich-b", g0341ImgJS, g0341ImgJS)
	// text-only pair whose A is a span target (move era only: the column
	// does not exist on the v0.63.0 schema until 0333 runs).
	if moveEra {
		guardPair("spana", "gg-spana-a", "gg-spana-b", nil, nil)
		msg("gg-spana-c", gsid("spana"), 2, "assistant", "anchored on A", ms(100_500), nil)
		move("gg-spana-c", "final", "gg-spana-a")
	}

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

	// ---- guard shapes: skipped AND counted (review M1). ----
	type guard struct{ name, a, b, reason string }
	guards := []guard{
		{"cont", "gg-cont-a", "gg-cont-b", "victim_referenced"},
		{"comp", "gg-comp-a", "gg-comp-b", "victim_referenced"},
		{"brp", "gg-brp-a", "gg-brp-b", "victim_referenced"},
		{"brc", "gg-brc-a", "gg-brc-b", "victim_referenced"},
		{"art", "gg-art-a", "gg-art-b", "victim_referenced"},
		{"arch", "gg-arch-a", "gg-arch-b", "archived_at_disagrees"},
		{"rich", "gg-rich-a", "gg-rich-b", "block_shape_not_produced_by_bug"},
	}
	if moveEra {
		guards = append(guards, guard{"spana", "gg-spana-a", "gg-spana-b", "earlier_row_is_span_target"})
	}
	for _, g := range guards {
		kept(g.a)
		kept(g.b)
		assertDedupeSessionCounts(t, gsid(g.name), map[string]string{"pairs_found": "1", "skipped": "1", "deleted": "0"})
		assertDedupeSkipReason(t, gsid(g.name), g.reason)
	}
	// The negative artifact shape is NOT a reference: deduped normally.
	gone("gg-artneg-a")
	kept("gg-artneg-b")
	assertDedupeSessionCounts(t, gsid("artneg"), map[string]string{"pairs_found": "1", "skipped": "0", "deleted": "1"})

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

// ---- 0341 log capture -----------------------------------------------------
//
// 0341's counts are observable only through its log lines (production
// logs them; nothing else reads them). The tests assert the per-session
// summary ("sessions.dedupe_user_turns.session") and per-skip lines
// ("sessions.dedupe_user_turns.skip") — both keyed by session_id, so
// parallel tests' lines cannot be mistaken for this test's.

type dedupeLogCapture struct {
	next slog.Handler
	mu   sync.Mutex
	recs []map[string]string
}

var activeDedupeCapture *dedupeLogCapture

func (c *dedupeLogCapture) Enabled(context.Context, slog.Level) bool { return true }
func (c *dedupeLogCapture) Handle(ctx context.Context, r slog.Record) error {
	if strings.HasPrefix(r.Message, "sessions.dedupe_user_turns.") {
		m := map[string]string{"msg": r.Message}
		r.Attrs(func(a slog.Attr) bool { m[a.Key] = a.Value.String(); return true })
		c.mu.Lock()
		c.recs = append(c.recs, m)
		c.mu.Unlock()
	}
	if c.next != nil && c.next.Enabled(ctx, r.Level) {
		return c.next.Handle(ctx, r)
	}
	return nil
}
func (c *dedupeLogCapture) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *dedupeLogCapture) WithGroup(string) slog.Handler      { return c }

func (c *dedupeLogCapture) find(msg, sessionID string) []map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []map[string]string
	for _, m := range c.recs {
		if m["msg"] == msg && m["session_id"] == sessionID {
			out = append(out, m)
		}
	}
	return out
}

func captureDedupeLogs(t *testing.T) {
	t.Helper()
	prev := logging.Handler()
	c := &dedupeLogCapture{next: prev}
	activeDedupeCapture = c
	logging.Replace(c)
	t.Cleanup(func() { logging.Replace(prev); activeDedupeCapture = nil })
}

func assertDedupeSessionCounts(t *testing.T, sessionID string, want map[string]string) {
	t.Helper()
	got := activeDedupeCapture.find("sessions.dedupe_user_turns.session", sessionID)
	if len(got) != 1 {
		t.Errorf("%s: %d per-session 0341 summary lines, want exactly 1 (the pair was not counted)", sessionID, len(got))
		return
	}
	for k, v := range want {
		if got[0][k] != v {
			t.Errorf("%s: 0341 counted %s=%s, want %s (line %v)", sessionID, k, got[0][k], v, got[0])
		}
	}
}

func assertDedupeSkipReason(t *testing.T, sessionID, reason string) {
	t.Helper()
	for _, m := range activeDedupeCapture.find("sessions.dedupe_user_turns.skip", sessionID) {
		if m["reason"] == reason {
			return
		}
	}
	t.Errorf("%s: no 0341 skip line with reason %q", sessionID, reason)
}

// TestMigration0341_SecondApplicationConvergesOnInterleavedPairs pins the
// convergence claim on migrationIDDedupeUserTurns (review M2). One pass is
// NOT a fixpoint for the interleaved shape A1,A2,B1,B2 — two identical
// sends racing, both frontend rows (A*) landing before both runner rows
// (B*), each B anchoring its own turn. Pass 1 deletes A1 and skips (B1,B2)
// because B1 is a span target; pass 2 pairs the now-unconsumed A2 with B1
// and deletes A2; pass 3 changes nothing. Double application is reachable
// in production — the repair path re-applies late sessions migrations
// whose ledger rows are missing (repair_upgrade_test.go) — so each state
// is asserted, not just the endpoint. Driven exactly that way: delete the
// 0341 ledger row and reopen through production Open.
func TestMigration0341_SecondApplicationConvergesOnInterleavedPairs(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	rawPath := filepath.Join(dir, "data.db")
	tag := newestSnapshotBefore0341(t)
	dumpText, err := os.ReadFile(filepath.Join("testdata", "upgrade", tag, "dump.sql"))
	if err != nil {
		t.Fatalf("read %s dump.sql: %v", tag, err)
	}
	raw := openRawSQLiteAt(t, rawPath)
	if err := upgradesnap.Materialize(ctx, raw, string(dumpText)); err != nil {
		t.Fatalf("materialise %s: %v", tag, err)
	}
	ms := func(n int64) int64 { return g0341Base + n*1_000_000 }
	stmts := []struct {
		q    string
		args []any
	}{
		{`INSERT INTO sessions (id, name, created_at, updated_at, last_active_at, position) VALUES ('gi', 'interleaved', 1, 1, 1, 70)`, nil},
		{`INSERT INTO session_messages (id, session_id, sequence, role, content, created_at) VALUES ('gi-a1', 'gi', 0, 'user', 'same words', ?)`, []any{ms(0)}},
		{`INSERT INTO session_messages (id, session_id, sequence, role, content, created_at) VALUES ('gi-a2', 'gi', 1, 'user', 'same words', ?)`, []any{ms(3)}},
		{`INSERT INTO session_messages (id, session_id, sequence, role, content, created_at) VALUES ('gi-b1', 'gi', 2, 'user', 'same words', ?)`, []any{ms(6)}},
		{`INSERT INTO session_messages (id, session_id, sequence, role, content, created_at) VALUES ('gi-b2', 'gi', 3, 'user', 'same words', ?)`, []any{ms(9)}},
		{`INSERT INTO session_messages (id, session_id, sequence, role, content, created_at, kind, move_index, turn_span_id) VALUES ('gi-f1', 'gi', 4, 'assistant', 'answer one', ?, 'final', 0, 'gi-b1')`, []any{ms(900)}},
		{`INSERT INTO session_messages (id, session_id, sequence, role, content, created_at, kind, move_index, turn_span_id) VALUES ('gi-f2', 'gi', 5, 'assistant', 'answer two', ?, 'final', 0, 'gi-b2')`, []any{ms(1800)}},
	}
	for _, st := range stmts {
		if _, err := raw.ExecContext(ctx, st.q, st.args...); err != nil {
			t.Fatalf("seed %q: %v", st.q, err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	userRows := func() []string {
		t.Helper()
		db := openRawSQLiteAt(t, rawPath)
		defer func() { _ = db.Close() }()
		rows, err := db.QueryContext(ctx, `SELECT id FROM session_messages WHERE session_id = 'gi' AND role = 'user' ORDER BY sequence`)
		if err != nil {
			t.Fatalf("list gi users: %v", err)
		}
		defer func() { _ = rows.Close() }()
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		return ids
	}
	open := func() {
		t.Helper()
		db, err := storagesqlite.Open(newConfig(dir))
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		var action string
		if err := db.Reader().QueryRow(ctx, "SELECT action FROM harness_migrations WHERE id = 'sessions/0341-dedupe-user-turns'").Scan(&action); err != nil || action != "applied" {
			t.Fatalf("0341 ledger action=%q err=%v, want applied", action, err)
		}
		if err := db.Close(ctx); err != nil {
			t.Fatalf("close: %v", err)
		}
	}
	rewind0341 := func() {
		t.Helper()
		db := openRawSQLiteAt(t, rawPath)
		defer func() { _ = db.Close() }()
		if _, err := db.ExecContext(ctx, `DELETE FROM harness_migrations WHERE id = 'sessions/0341-dedupe-user-turns'`); err != nil {
			t.Fatalf("rewind 0341 ledger row: %v", err)
		}
	}
	want := func(pass string, ids ...string) {
		t.Helper()
		if got := strings.Join(userRows(), ","); got != strings.Join(ids, ",") {
			t.Fatalf("after %s: user rows = %s, want %s", pass, got, strings.Join(ids, ","))
		}
	}

	open()
	want("pass 1", "gi-a2", "gi-b1", "gi-b2")
	rewind0341()
	open()
	want("pass 2 (re-application)", "gi-b1", "gi-b2")
	rewind0341()
	open()
	want("pass 3 (fixpoint)", "gi-b1", "gi-b2")

	// Both turns still anchor on rows that exist.
	db := openRawSQLiteAt(t, rawPath)
	defer func() { _ = db.Close() }()
	var dangling int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM session_messages m WHERE m.session_id = 'gi'
	    AND m.turn_span_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM session_messages u WHERE u.id = m.turn_span_id)`).Scan(&dangling); err != nil {
		t.Fatal(err)
	}
	if dangling != 0 {
		t.Errorf("%d gi rows carry a dangling turn_span_id after convergence", dangling)
	}
}

// TestMigration0341_ReapplyAfterUnitsConversionDoesNotBrick pins the
// C-review cross-branch hazard (finding 2, 2026-10-04): the repair path
// re-applies late sessions migrations, so 0341 can run AGAIN on a
// database units/1104 has already converted (artifacts renamed to
// artifacts_legacy, and since units/1105 dropped). A bare `FROM
// artifacts` would fail Open there.
// End-to-end: pass 1 applies 0341 (pair protected via the REAL
// artifacts-table reference) then 1104 (artifact copied to units,
// table renamed); the rewind-and-reopen repair shape re-runs 0341 on
// the converted database — it must neither brick Open nor delete the
// pair, now resolved through the units path.
func TestMigration0341_ReapplyAfterUnitsConversionDoesNotBrick(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	rawPath := filepath.Join(dir, "data.db")
	tag := newestSnapshotBefore0341(t)
	dumpText, err := os.ReadFile(filepath.Join("testdata", "upgrade", tag, "dump.sql"))
	if err != nil {
		t.Fatalf("read %s dump.sql: %v", tag, err)
	}
	raw := openRawSQLiteAt(t, rawPath)
	if err := upgradesnap.Materialize(ctx, raw, string(dumpText)); err != nil {
		t.Fatalf("materialise %s: %v", tag, err)
	}
	ms := func(n int64) int64 { return g0341Base + n*1_000_000 }
	stmts := []struct {
		q    string
		args []any
	}{
		{`INSERT INTO sessions (id, name, created_at, updated_at, last_active_at, position) VALUES ('zz-re', 'reapply', 1, 1, 1, 71)`, nil},
		{`INSERT INTO session_messages (id, session_id, sequence, role, content, created_at) VALUES ('zr-a', 'zz-re', 0, 'user', 'same text', ?)`, []any{ms(0)}},
		{`INSERT INTO session_messages (id, session_id, sequence, role, content, created_at) VALUES ('zr-a-copy', 'zz-re', 1, 'user', 'same text', ?)`, []any{ms(1)}},
		{`INSERT INTO artifacts (id, session_id, project_id, title, mime_type, content_hash, byte_size, source, source_ref_json, scope_kind, created_at)
		  VALUES ('zz-re-art', 'zz-re', NULL, 'ref', 'text/plain', 'zzrehash', 3, 'user_pin', '{"message_id":"zr-a"}', 'session', 1)`, nil},
	}
	for _, st := range stmts {
		if _, err := raw.ExecContext(ctx, st.q, st.args...); err != nil {
			t.Fatalf("seed %q: %v", st.q, err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	countUsers := func() int {
		t.Helper()
		db := openRawSQLiteAt(t, rawPath)
		defer func() { _ = db.Close() }()
		var n int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM session_messages WHERE session_id = 'zz-re' AND role = 'user'`).Scan(&n); err != nil {
			t.Fatalf("count zz-re users: %v", err)
		}
		return n
	}

	// Pass 1: real Open applies 0341 (artifacts path protects the pair)
	// then 1104 (converts + renames).
	db, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("Open pass 1: %v", err)
	}
	if err := db.Close(ctx); err != nil {
		t.Fatalf("close pass 1: %v", err)
	}
	if n := countUsers(); n != 2 {
		t.Fatalf("pass 1: user rows = %d, want 2 (artifacts-path protection)", n)
	}

	// Repair shape: rewind 0341's ledger row and reopen on the CONVERTED
	// database (artifacts renamed, reference now in units metadata).
	rawDB := openRawSQLiteAt(t, rawPath)
	if _, err := rawDB.ExecContext(ctx, `DELETE FROM harness_migrations WHERE id = 'sessions/0341-dedupe-user-turns'`); err != nil {
		t.Fatalf("rewind 0341: %v", err)
	}
	// Precondition: the database is fully converted — the reference lives
	// only in units metadata (1104 copied it) and no artifacts table of
	// either generation exists (1104 renamed it, units/1105 dropped the
	// rename), the most hostile shape for a re-applied 0341.
	var legacy, artUnit int
	if err := rawDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('artifacts','artifacts_legacy')`).Scan(&legacy); err != nil || legacy != 0 {
		t.Fatalf("precondition: artifacts tables present = %d err=%v, want 0 (1104 renamed, 1105 dropped)", legacy, err)
	}
	if err := rawDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM units WHERE id='zz-re-art' AND kind='artifact'`).Scan(&artUnit); err != nil || artUnit != 1 {
		t.Fatalf("precondition: artifact unit zz-re-art = %d err=%v, want 1 (1104 copied it)", artUnit, err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	db2, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("Open repair pass on a units-converted database: %v", err)
	}
	if err := db2.Close(ctx); err != nil {
		t.Fatalf("close repair pass: %v", err)
	}
	if n := countUsers(); n != 2 {
		t.Fatalf("repair pass: user rows = %d, want 2 (pair kept via the units reference)", n)
	}
}

// newestSnapshotBefore0341 returns the newest committed snapshot whose
// ledger does not yet carry sessions/0341 — the database an upgrading user
// reaching 0341 actually has (adversarial review F4: these tests pinned
// v0.85.2 while 1104's pinned the newest; re-pointed by
// units-debt-01UNITD01 WP03). "Newest overall" would be wrong once a
// post-0341 snapshot (v0.87.0+) lands: Open would not re-run 0341 on it
// and the dedupe assertions would test nothing.
func newestSnapshotBefore0341(t *testing.T) string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join("testdata", "upgrade"))
	if err != nil {
		t.Fatalf("read snapshot dir: %v", err)
	}
	var tags []string
	for _, e := range entries {
		if e.IsDir() && upgradesnap.IsSnapshotTag(e.Name()) {
			tags = append(tags, e.Name())
		}
	}
	tags = upgradesnap.SortedSnapshotTags(tags)
	for i := len(tags) - 1; i >= 0; i-- {
		dump, err := os.ReadFile(filepath.Join("testdata", "upgrade", tags[i], "dump.sql"))
		if err != nil {
			continue // a tag recorded as unreplayable, no dump.sql
		}
		if !strings.Contains(string(dump), "sessions/0341-dedupe-user-turns") {
			return tags[i]
		}
	}
	t.Fatal("no committed snapshot predates sessions/0341")
	return ""
}
