package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"sort"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// migrationIDDedupeUserTurns identifies migration 0341 — the cleanup for
// dogfood finding F12 (chat-single-writer-01DOGF0G WP05).
//
// THE DEFECT IT REPAIRS. From the graph-chat migration (5fe2fbcf,
// 2026-04-27, first shipped in v0.1.x; v0.63.0's 146d9e54 then made the
// runner's copy the turn's span) until chat-single-writer's WP02, every
// user chat turn was written to session_messages TWICE: once by the RPC the chat surface
// calls (Sessions_AppendMessage / Sessions_SendMessageWithBlocks — row A),
// and again, 0.7–17 ms later, by the chat runner, which read A's flattened
// text back in LLM.StartStream and re-appended it (row B, which became the
// turn's span). Every history read returns both, so the model has been
// sent every user message twice on every turn. (The oldest SURVIVING pair
// on the dev profile is 2026-06-07; that is the profile's age, not the
// bug's.) The forward fix stops new pairs; this migration removes the ones
// already stored.
//
// It also removes the assistant-side twin the same mission attributed
// (WP04): a kind-less assistant row with streaming_failed_at set that
// repeats, byte for byte, an adjacent row of the same turn — the
// backend-error partial path re-persisting text the move journal already
// owned.
//
// THIS MIGRATION IS DESTRUCTIVE (DELETE + UPDATE) AGAINST REAL USER ROWS.
//
// USER PAIRS — rows A, B form a doubled user turn iff ALL of:
//
//  1. same session, both role='user', both classic (kind NULL or empty);
//  2. B.sequence = A.sequence + 1;
//  3. A.content = B.content (the flattened text, byte-equal);
//  4. 0 <= B.created_at - A.created_at <= 2 s. Observed on the dev
//     profile: 40 pairs, 0.72–16.7 ms apart; the closest GENUINE re-send
//     of identical text was 6.6 s apart. (docs/missions/chat-single-writer.md
//     §1-§2 has the distribution.)
//  5. pairs are formed greedily in ascending sequence; a row consumed as
//     one pair's B is never the A of another.
//
// SURVIVOR RULE (pair-type-aware). The runner's copy B was always
// written text-only, from A's FLATTENED text:
//
//   - neither row carries a non-text block -> delete A. B keeps its
//     turn_span_id referrers and is the id fleet sync was told about.
//     Skipped if A is itself a span target (the bug never anchored on A).
//   - the two rows disagree on archived_at (one compacted away, one not)
//     -> skipped and counted.
//   - A carries an image/document block, B does not -> KEEP A (it holds
//     the user's attachment), re-point every turn_span_id = B.id in the
//     session to A.id, then delete B.
//   - any other combination -> skipped and counted.
//
// ASSISTANT TWINS — delete assistant row R iff R is classic (kind NULL or
// empty), R.streaming_failed_at IS NOT NULL, R.continuation_of IS NULL, and
// the row S immediately before or after it (|sequence diff| = 1) is an
// assistant row with identical non-empty content that is NOT itself a
// classic failed row, with |created_at diff| <= 60 s.
//
// REFERRERS (FR-2c). No table has a foreign key TO session_messages(id),
// so no cascade is possible; the hazard is a dangling soft reference. A
// row is never deleted while referenced by: another row's continuation_of
// or compacted_into_id, branches.parent_message_id,
// branch_message_refs.parent_msg_id / child_msg_id, or an artifact's
// source_ref_json — such a pair is skipped and counted. turn_span_id is
// handled by the survivor rule above. The FTS index follows through the
// messages_fts_ad / messages_fts_au triggers. Audit `events` payloads are
// an immutable hash chain and are deliberately not rewritten.
//
// THE BOUND. Like 0337 this is a bounded PER-SESSION scan in Go: one
// query finds the sessions holding a candidate, then each session's rows
// are loaded alone (O(rows in the largest such session) memory) and
// scanned once in sequence order.
//
// CONVERGENT, NOT SINGLE-PASS IDEMPOTENT. Each pass is safe to repeat and
// every deletion it makes is one a later pass would also make, but one
// pass is not always a fixpoint. The interleaved shape A1,A2,B1,B2 (two
// identical sends racing, so both frontend rows land before both runner
// rows) shows it: pass 1 pairs (A1,A2) and deletes A1, consumes A2, then
// skips (B1,B2) because B1 anchors turn 1's moves; pass 2 sees A2,B1
// adjacent, pairs them and deletes A2; pass 3 changes nothing. The
// endpoint — B1 and B2, each anchoring its own turn — is the same either
// way. A second application is reachable (the repair path re-applies late
// sessions migrations, see repair_upgrade_test.go) and is pinned by
// TestMigration0341_SecondApplicationConvergesOnInterleavedPairs.
// Down is a best-effort no-op (0327/0332/0337 precedent:
// Registry.Rollback has no production caller).
//
// Numbering: 0341 — chat-single-writer-01DOGF0G claims the next
// sessions/03xx after 0340; artifacts-as-units-01DOGF0C takes 0342.
const migrationIDDedupeUserTurns = "sessions/0341-dedupe-user-turns"

// sqlDedupeUserTurnsUpSource is the content-hash source for 0341. Up is
// procedural Go; this text states the rules so the hash changes iff the
// rules' meaning does. DO NOT reword once shipped (ErrLedgerHashMismatch).
const sqlDedupeUserTurnsUpSource = `
-- sessions/0341-dedupe-user-turns
-- USER PAIRS: A,B same session, role='user', classic kind, B.sequence =
-- A.sequence+1, A.content = B.content, 0 <= B.created_at-A.created_at <= 2s.
--   text-only pair:  DELETE FROM session_messages WHERE id = A.id
--   A has image/document, B text-only:
--     UPDATE session_messages SET turn_span_id = A.id
--      WHERE session_id = ? AND turn_span_id = B.id;
--     DELETE FROM session_messages WHERE id = B.id
-- ASSISTANT TWINS: classic assistant R with streaming_failed_at set and
-- continuation_of NULL, adjacent (|seq diff| = 1) to an assistant S with
-- identical non-empty content, S not itself classic+failed, |dt| <= 60s:
--     DELETE FROM session_messages WHERE id = R.id
-- Never deleted while referenced by continuation_of, compacted_into_id,
-- branches.parent_message_id, branch_message_refs, or artifact
-- source_ref_json (skipped + counted); likewise a text-only pair whose A
-- is a turn_span_id target, a pair disagreeing on archived_at, and any
-- other block shape. Bounded per-session scan in Go;
-- see migrations_dedupe_user_turns.go.
`

const (
	dedupeUserPairWindow      = int64(2 * time.Second)
	dedupeAssistantTwinWindow = int64(60 * time.Second)
)

func migration0341() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDDedupeUserTurns,
		Version:       341,
		OwningMission: OwningMission,
		UpSource:      sqlDedupeUserTurnsUpSource,
		Up:            dedupeDoubledTurns,
		Down: func(ctx context.Context, tx migrations.WriteTx) error {
			// Best-effort no-op — see doc comment above.
			return nil
		},
	}
}

// dedupeCounts is what one 0341 pass found and did. Production logs it;
// the migration's tests read the rows themselves.
type dedupeCounts struct {
	UserPairsFound        int
	UserRowsDeleted       int
	UserSpansRepointed    int
	UserPairsSkipped      int
	AssistantTwinsFound   int
	AssistantRowsDeleted  int
	AssistantTwinsSkipped int
}

// dedupeRow is one session_messages row loaded for a session's scan.
type dedupeRow struct {
	id                string
	sequence          int64
	role              string
	content           string
	contentJSON       sql.NullString
	kind              sql.NullString
	turnSpanID        sql.NullString
	continuationOf    sql.NullString
	compactedIntoID   sql.NullString
	archivedAt        sql.NullInt64
	createdAt         int64
	streamingFailedAt sql.NullInt64
}

func (r dedupeRow) classic() bool { return !r.kind.Valid || r.kind.String == "" }

// hasNonTextBlock reports whether the row's canonical content carries an
// image / document (or any other non-text) block.
func (r dedupeRow) hasNonTextBlock() bool {
	if !r.contentJSON.Valid || r.contentJSON.String == "" {
		return false
	}
	var blocks []struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(r.contentJSON.String), &blocks); err != nil {
		// Unparseable content_json: treat as carrying something we do not
		// understand, which keeps the row (see the survivor rule).
		return true
	}
	for _, b := range blocks {
		if b.Type != "" && b.Type != "text" {
			return true
		}
	}
	return false
}

func (r dedupeRow) classicFailedAssistant() bool {
	return r.role == "assistant" && r.classic() && r.streamingFailedAt.Valid
}

// dedupeDoubledTurns is migration 0341's Up.
func dedupeDoubledTurns(ctx context.Context, tx migrations.WriteTx) error {
	counts, err := runDedupeDoubledTurns(ctx, tx)
	if err != nil {
		return err
	}
	logging.L().Info("sessions.dedupe_user_turns",
		"migration", migrationIDDedupeUserTurns,
		"pairs_found", counts.UserPairsFound,
		"deleted", counts.UserRowsDeleted,
		"repointed", counts.UserSpansRepointed,
		"skipped", counts.UserPairsSkipped,
		"assistant_twins_found", counts.AssistantTwinsFound,
		"assistant_deleted", counts.AssistantRowsDeleted,
		"assistant_skipped", counts.AssistantTwinsSkipped,
	)
	return nil
}

// runDedupeDoubledTurns runs 0341's repair inside tx and returns what it did.
// Split from dedupeDoubledTurns so the counts have one owner.
func runDedupeDoubledTurns(ctx context.Context, tx migrations.WriteTx) (dedupeCounts, error) {
	var total dedupeCounts
	sessionIDs, err := dedupeCandidateSessions(ctx, tx)
	if err != nil {
		return total, err
	}
	for _, sid := range sessionIDs {
		c, err := dedupeSession(ctx, tx, sid)
		if err != nil {
			return total, err
		}
		total.UserPairsFound += c.UserPairsFound
		total.UserRowsDeleted += c.UserRowsDeleted
		total.UserSpansRepointed += c.UserSpansRepointed
		total.UserPairsSkipped += c.UserPairsSkipped
		total.AssistantTwinsFound += c.AssistantTwinsFound
		total.AssistantRowsDeleted += c.AssistantRowsDeleted
		total.AssistantTwinsSkipped += c.AssistantTwinsSkipped
	}
	return total, nil
}

// dedupeCandidateSessions is the one cross-session query: every session
// holding an adjacent identical user pair inside the window, or a classic
// failed assistant row (the assistant-twin precondition).
func dedupeCandidateSessions(ctx context.Context, tx migrations.WriteTx) ([]string, error) {
	rows, err := tx.Query(ctx, `
        SELECT DISTINCT a.session_id
          FROM session_messages a
          JOIN session_messages b
            ON b.session_id = a.session_id AND b.sequence = a.sequence + 1
         WHERE a.role = 'user' AND b.role = 'user'
           AND a.content = b.content
           AND b.created_at - a.created_at BETWEEN 0 AND ?
        UNION
        SELECT DISTINCT session_id
          FROM session_messages
         WHERE role = 'assistant'
           AND (kind IS NULL OR kind = '')
           AND streaming_failed_at IS NOT NULL
         ORDER BY 1`, dedupeUserPairWindow)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var sid string
		if err := rows.Scan(&sid); err != nil {
			return nil, err
		}
		out = append(out, sid)
	}
	return out, rows.Err()
}

func loadDedupeRows(ctx context.Context, tx migrations.WriteTx, sessionID string) ([]dedupeRow, error) {
	rows, err := tx.Query(ctx, `
        SELECT id, sequence, role, content, content_json, kind, turn_span_id,
               continuation_of, compacted_into_id, archived_at, created_at,
               streaming_failed_at
          FROM session_messages
         WHERE session_id = ?
         ORDER BY sequence, id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []dedupeRow
	for rows.Next() {
		var r dedupeRow
		if err := rows.Scan(&r.id, &r.sequence, &r.role, &r.content, &r.contentJSON,
			&r.kind, &r.turnSpanID, &r.continuationOf, &r.compactedIntoID,
			&r.archivedAt, &r.createdAt, &r.streamingFailedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].sequence < out[j].sequence })
	return out, nil
}

// externallyReferenced reports whether any table outside this session's
// own pointer columns names id — branches, branch message refs, or an
// artifact's source ref.
func externallyReferenced(ctx context.Context, tx migrations.WriteTx, id string) (bool, error) {
	var n int
	err := tx.QueryRow(ctx, `
        SELECT
          (SELECT COUNT(*) FROM branches WHERE parent_message_id = ?) +
          (SELECT COUNT(*) FROM branch_message_refs WHERE parent_msg_id = ? OR child_msg_id = ?) +
          (SELECT COUNT(*) FROM artifacts WHERE instr(source_ref_json, ?) > 0)`,
		id, id, id, `"`+id+`"`).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// dedupeSession applies both rules to one session.
func dedupeSession(ctx context.Context, tx migrations.WriteTx, sessionID string) (c dedupeCounts, err error) {
	all, err := loadDedupeRows(ctx, tx, sessionID)
	if err != nil {
		return c, err
	}
	spanRefs := map[string]int{}
	pointerTargets := map[string]bool{} // continuation_of / compacted_into_id targets
	for _, r := range all {
		if r.turnSpanID.Valid && r.turnSpanID.String != "" {
			spanRefs[r.turnSpanID.String]++
		}
		if r.continuationOf.Valid && r.continuationOf.String != "" {
			pointerTargets[r.continuationOf.String] = true
		}
		if r.compactedIntoID.Valid && r.compactedIntoID.String != "" {
			pointerTargets[r.compactedIntoID.String] = true
		}
	}
	deleted := map[string]bool{}
	// skip records why a candidate was left in place — ids and a reason
	// only, never content (the same privacy line syncHook draws).
	skip := func(class, id, reason string) {
		logging.L().Info("sessions.dedupe_user_turns.skip",
			"session_id", sessionID, "class", class, "message_id", id, "reason", reason)
	}
	defer func() {
		if c != (dedupeCounts{}) {
			logging.L().Info("sessions.dedupe_user_turns.session",
				"session_id", sessionID,
				"pairs_found", c.UserPairsFound,
				"deleted", c.UserRowsDeleted,
				"repointed", c.UserSpansRepointed,
				"skipped", c.UserPairsSkipped,
				"assistant_twins_found", c.AssistantTwinsFound,
				"assistant_deleted", c.AssistantRowsDeleted,
				"assistant_skipped", c.AssistantTwinsSkipped,
			)
		}
	}()
	referenced := func(id string) (bool, error) {
		if pointerTargets[id] {
			return true, nil
		}
		return externallyReferenced(ctx, tx, id)
	}
	del := func(id string) error {
		if _, err := tx.Exec(ctx, "DELETE FROM session_messages WHERE id = ?", id); err != nil {
			return err
		}
		deleted[id] = true
		return nil
	}

	// ---- user pairs ----
	consumed := map[string]bool{}
	for i := 0; i+1 < len(all); i++ {
		a, b := all[i], all[i+1]
		if consumed[a.id] || a.role != "user" || b.role != "user" || !a.classic() || !b.classic() {
			continue
		}
		if b.sequence != a.sequence+1 || a.content != b.content {
			continue
		}
		dt := b.createdAt - a.createdAt
		if dt < 0 || dt > dedupeUserPairWindow {
			continue
		}
		c.UserPairsFound++
		consumed[b.id] = true

		aRich, bRich := a.hasNonTextBlock(), b.hasNonTextBlock()
		var survivor, victim dedupeRow
		repoint := false
		switch {
		case !aRich && !bRich:
			survivor, victim = b, a
			if spanRefs[a.id] > 0 {
				c.UserPairsSkipped++
				skip("user", a.id, "earlier_row_is_span_target")
				continue
			}
		case aRich && !bRich:
			survivor, victim, repoint = a, b, true
		default:
			c.UserPairsSkipped++
			skip("user", a.id, "block_shape_not_produced_by_bug")
			continue
		}
		if a.archivedAt.Valid != b.archivedAt.Valid {
			c.UserPairsSkipped++
			skip("user", a.id, "archived_at_disagrees")
			continue
		}
		if ref, rerr := referenced(victim.id); rerr != nil {
			return c, rerr
		} else if ref {
			c.UserPairsSkipped++
			skip("user", victim.id, "victim_referenced")
			continue
		}
		if repoint && spanRefs[victim.id] > 0 {
			if _, err := tx.Exec(ctx,
				"UPDATE session_messages SET turn_span_id = ? WHERE session_id = ? AND turn_span_id = ?",
				survivor.id, sessionID, victim.id); err != nil {
				return c, err
			}
			spanRefs[survivor.id] += spanRefs[victim.id]
			delete(spanRefs, victim.id)
			c.UserSpansRepointed++
		}
		if err := del(victim.id); err != nil {
			return c, err
		}
		c.UserRowsDeleted++
	}

	// ---- assistant twins ----
	for i := 0; i < len(all); i++ {
		r := all[i]
		if deleted[r.id] || !r.classicFailedAssistant() || r.content == "" || r.continuationOf.Valid {
			continue
		}
		var twin *dedupeRow
		for _, j := range []int{i - 1, i + 1} {
			if j < 0 || j >= len(all) {
				continue
			}
			s := all[j]
			if deleted[s.id] || s.role != "assistant" || s.content != r.content {
				continue
			}
			if d := s.sequence - r.sequence; d != 1 && d != -1 {
				continue
			}
			if d := s.createdAt - r.createdAt; d > dedupeAssistantTwinWindow || d < -dedupeAssistantTwinWindow {
				continue
			}
			found := s
			twin = &found
			break
		}
		if twin == nil {
			continue
		}
		if twin.classicFailedAssistant() && twin.sequence < r.sequence {
			// Already counted (and skipped) from the earlier row's visit.
			continue
		}
		c.AssistantTwinsFound++
		if twin.classicFailedAssistant() {
			// Both copies are classic failed rows — neither is "the
			// answer", so the survivor is not decidable here (the pre-0336
			// periodic-flush residue 0337's strict-prefix rule leaves).
			c.AssistantTwinsSkipped++
			skip("assistant", r.id, "both_rows_failed_partials")
			continue
		}
		if spanRefs[r.id] > 0 {
			c.AssistantTwinsSkipped++
			skip("assistant", r.id, "span_target")
			continue
		}
		if ref, rerr := referenced(r.id); rerr != nil {
			return c, rerr
		} else if ref {
			c.AssistantTwinsSkipped++
			skip("assistant", r.id, "victim_referenced")
			continue
		}
		if err := del(r.id); err != nil {
			return c, err
		}
		c.AssistantRowsDeleted++
	}
	return c, nil
}
