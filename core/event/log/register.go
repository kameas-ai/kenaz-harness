package log

import (
	"context"
	"strings"

	_ "embed"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// OwningMission is the canonical migrations.Migration.OwningMission
// value for every migration this package registers. It MUST match a
// key in migrations.CanonicalBlocks (core/storage/migrations/blocks.go)
// or Register returns ErrUnknownOwningMission and Open fails.
//
// This used to be "core/event" (register.go:59, pre-audit-that-tells-
// the-truth-01PMZA10). The reserved block is keyed "event-log"
// (blocks.go:22, {Min:100,Max:199}); "core/event" is not in
// CanonicalBlocks at all, so a rewrite that preserved the old string
// would fail at boot with ErrUnknownOwningMission the moment this
// package's migrations were actually registered from sqlite.go. Fixed
// here, not carried forward — see
// kitty-specs/audit-that-tells-the-truth-01PMZA10/research/corrections.md.
const OwningMission = "event-log"

//go:embed migrations/0001_events.sql
var migration0100Source string

//go:embed migrations/0002_event_chain_heads.sql
var migration0101Source string

//go:embed migrations/0003_redaction_rules.sql
var migration0102Source string

//go:embed migrations/0004_retention_config.sql
var migration0103Source string

//go:embed migrations/0005_schema_version.up.sql
var migration0104UpSource string

//go:embed migrations/0005_schema_version.down.sql
var migration0104DownSource string

//go:embed migrations/0006_saved_audit_queries.up.sql
var migration0105UpSource string

//go:embed migrations/0006_saved_audit_queries.down.sql
var migration0105DownSource string

//go:embed migrations/0007_events_fts_sync.up.sql
var migration0106UpSource string

//go:embed migrations/0007_events_fts_sync.down.sql
var migration0106DownSource string

// Migrations returns the event-log migration set in version order,
// shaped for the production storage-foundations migration framework
// (core/storage/migrations). Versions occupy 100-105 inside the
// "event-log" reserved block (100-199); core/storage/sqlite/sqlite.go
// registers them alongside session / slashcmd / units.
//
// These are the first migrations in this repo's history to land
// NUMERICALLY BELOW an existing install's high-water mark — every
// shipped install already carries sessions/03xx and units/11xx ledger
// rows well above 100-105. They apply correctly on an upgraded install
// ONLY because Registry.Pending() selects by set membership, not by a
// max-based high-water mark (core/storage/migrations/registry.go:131-163,
// and the v0.63.0 P0 the comment there documents). Do not "optimise"
// that selection, and do not reason about these migrations as if a
// fresh/empty database is representative — see
// core/storage/sqlite/upgrade_path_test.go (TestUpgradePath), which
// boots these against a database a previous release actually produced.
func Migrations() []migrations.Migration {
	return []migrations.Migration{
		{
			ID:            "event-log/0100-events",
			Version:       100,
			OwningMission: OwningMission,
			UpSource:      migration0100Source,
			Up:            migration0100Up,
			Down:          migration0100Down,
		},
		{
			ID:            "event-log/0101-event-chain-heads",
			Version:       101,
			OwningMission: OwningMission,
			UpSource:      migration0101Source,
			Up: func(ctx context.Context, tx migrations.WriteTx) error {
				return execAll(ctx, tx, splitSQL(migration0101Source))
			},
			Down: func(ctx context.Context, tx migrations.WriteTx) error {
				return execAll(ctx, tx, []string{"DROP TABLE IF EXISTS event_chain_heads"})
			},
		},
		{
			ID:            "event-log/0102-redaction-rules",
			Version:       102,
			OwningMission: OwningMission,
			UpSource:      migration0102Source,
			Up: func(ctx context.Context, tx migrations.WriteTx) error {
				return execAll(ctx, tx, splitSQL(migration0102Source))
			},
			Down: func(ctx context.Context, tx migrations.WriteTx) error {
				return execAll(ctx, tx, []string{"DROP TABLE IF EXISTS redaction_rules"})
			},
		},
		{
			// The seeded policy ('{"kind":"keep_all"}') is not a valid
			// RetentionStrategy value (retention.go:25-32 only defines
			// keep_forever / delete_after_window / archive_after_window).
			// UNIT-8 fixes the seed when it wires retention_config as the
			// real read path (spec §1.7d, §5.6 item 3/4) — not this unit,
			// which lands inert and must not change migration behaviour
			// beyond registering it correctly.
			ID:            "event-log/0103-retention-config",
			Version:       103,
			OwningMission: OwningMission,
			UpSource:      migration0103Source,
			Up: func(ctx context.Context, tx migrations.WriteTx) error {
				return execAll(ctx, tx, splitSQL(migration0103Source))
			},
			Down: func(ctx context.Context, tx migrations.WriteTx) error {
				return execAll(ctx, tx, []string{"DROP TABLE IF EXISTS retention_config"})
			},
		},
		{
			// ALTER TABLE events ADD COLUMN, over the table 0100 creates.
			// Correct only in one ascending pass after 0100 — the reason
			// these are six distinct versions and not one concatenated
			// blob (spec §5.2 trap 1).
			ID:            "event-log/0104-schema-version",
			Version:       104,
			OwningMission: OwningMission,
			UpSource:      migration0104UpSource,
			Up: func(ctx context.Context, tx migrations.WriteTx) error {
				return execAll(ctx, tx, splitSQL(migration0104UpSource))
			},
			Down: func(ctx context.Context, tx migrations.WriteTx) error {
				return execAll(ctx, tx, splitSQL(migration0104DownSource))
			},
		},
		{
			ID:            "event-log/0105-saved-audit-queries",
			Version:       105,
			OwningMission: OwningMission,
			UpSource:      migration0105UpSource,
			Up: func(ctx context.Context, tx migrations.WriteTx) error {
				return execAll(ctx, tx, splitSQL(migration0105UpSource))
			},
			Down: func(ctx context.Context, tx migrations.WriteTx) error {
				return execAll(ctx, tx, splitSQL(migration0105DownSource))
			},
		},
		{
			// FTS5 delete/update sync triggers for events_fts (fixes the
			// data-loss-of-privacy + search-breakage defect at
			// migrations/0001_events.sql:33 — see 0007's own header for
			// the full account of why that comment could not be edited
			// in place) plus a one-time correction of migration 103's
			// invalid seed value. audit-that-tells-the-truth-01PMZA10
			// UNIT-8. Optional per the campaign allocation
			// (docs/v0.65.0-merge-order.md §4: "100-105, optionally
			// 106") — included because UNIT-8 (retention deletes) does
			// not ship without it: plan.md Rule 5.
			ID:            "event-log/0106-events-fts-sync",
			Version:       106,
			OwningMission: OwningMission,
			UpSource:      migration0106UpSource,
			Up:            migration0106Up,
			Down: func(ctx context.Context, tx migrations.WriteTx) error {
				return execAll(ctx, tx, splitSQL(migration0106DownSource))
			},
		},
	}
}

// migration0106Up executes migrations/0007_events_fts_sync.up.sql: one
// plain UPDATE statement followed by TWO CREATE TRIGGER ... BEGIN ...
// END; blocks. splitTrailingTrigger (singular, used by migration0100Up)
// only isolates ONE trailing trigger and silently discards anything
// after it — insufficient here. splitTrailingTriggers (plural) below
// generalises it.
func migration0106Up(ctx context.Context, tx migrations.WriteTx) error {
	head, triggers := splitTrailingTriggers(migration0106UpSource)
	if err := execAll(ctx, tx, head); err != nil {
		return err
	}
	for _, trig := range triggers {
		if _, err := tx.Exec(ctx, trig); err != nil {
			return err
		}
	}
	return nil
}

// migration0100Up executes migrations/0001_events.sql. It cannot use
// the naive splitSQL below over the whole file: the file's CREATE
// TRIGGER ... BEGIN ... END; statement contains an internal semicolon
// (the INSERT inside the trigger body) that a plain ';'-split would
// cut in half. This mirrors the pattern established by
// core/session/migrations_search_fts.go (migration0312), which
// executes its CREATE TRIGGER statements as individually-held Go
// string constants rather than through its own splitSQL for the same
// reason.
func migration0100Up(ctx context.Context, tx migrations.WriteTx) error {
	head, trigger := splitTrailingTrigger(migration0100Source)
	if err := execAll(ctx, tx, splitSQL(head)); err != nil {
		return err
	}
	if trigger == "" {
		return nil
	}
	_, err := tx.Exec(ctx, trigger)
	return err
}

func migration0100Down(ctx context.Context, tx migrations.WriteTx) error {
	return execAll(ctx, tx, []string{
		"DROP TRIGGER IF EXISTS events_ai",
		"DROP TABLE IF EXISTS events_fts",
		"DROP INDEX IF EXISTS idx_events_emitted_at",
		"DROP INDEX IF EXISTS idx_events_emitter_event",
		"DROP INDEX IF EXISTS idx_events_kind_event",
		"DROP INDEX IF EXISTS idx_events_session_event",
		"DROP TABLE IF EXISTS events",
	})
}

// RegisterMigrations registers every migration returned by Migrations()
// with the production registry, in order, aborting on the first error.
// Callers MUST register before storage.Open applies pending migrations —
// core/storage/sqlite/sqlite.go does so alongside session / slashcmd /
// units.
func RegisterMigrations(reg *migrations.Registry) error {
	for _, m := range Migrations() {
		if err := reg.Register(m); err != nil {
			return err
		}
	}
	return nil
}

func execAll(ctx context.Context, tx migrations.WriteTx, stmts []string) error {
	for _, stmt := range stmts {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

// splitTrailingTrigger separates a SQL script into everything before
// its first "CREATE TRIGGER" statement (safe to split naively — no
// statement in that portion contains an embedded semicolon) and the
// trigger statement itself, taken verbatim through its closing "END;"
// (which DOES contain embedded semicolons and must execute atomically).
// If the script has no CREATE TRIGGER, the whole script is returned as
// head and trigger is "".
func splitTrailingTrigger(src string) (head string, trigger string) {
	upper := strings.ToUpper(src)
	idx := strings.Index(upper, "CREATE TRIGGER")
	if idx < 0 {
		return src, ""
	}
	head = src[:idx]
	tail := src[idx:]
	tailUpper := upper[idx:]
	endIdx := strings.Index(tailUpper, "END;")
	if endIdx < 0 {
		return head, strings.TrimSpace(tail)
	}
	return head, strings.TrimSpace(tail[:endIdx+len("END;")])
}

// splitTrailingTriggers separates a SQL script into every "plain"
// statement before the first CREATE TRIGGER (split via splitSQL —
// safe, because that portion contains no embedded semicolons) and
// EACH INDIVIDUAL CREATE TRIGGER ... END; block after it, taken
// verbatim through its own closing "END;" (trigger bodies contain
// their own internal semicolons and must not be split). Generalises
// splitTrailingTrigger (singular), which only isolates ONE trailing
// trigger and silently drops anything after it — migration 0106 needs
// two (events_fts_au, events_fts_ad) after one plain UPDATE statement.
func splitTrailingTriggers(src string) (head []string, triggers []string) {
	upper := strings.ToUpper(src)
	idx := strings.Index(upper, "CREATE TRIGGER")
	if idx < 0 {
		return splitSQL(src), nil
	}
	head = splitSQL(src[:idx])
	rest := src[idx:]
	for {
		restUpper := strings.ToUpper(rest)
		ti := strings.Index(restUpper, "CREATE TRIGGER")
		if ti < 0 {
			break
		}
		tail := rest[ti:]
		tailUpper := restUpper[ti:]
		endIdx := strings.Index(tailUpper, "END;")
		if endIdx < 0 {
			triggers = append(triggers, strings.TrimSpace(tail))
			break
		}
		triggers = append(triggers, strings.TrimSpace(tail[:endIdx+len("END;")]))
		rest = tail[endIdx+len("END;"):]
	}
	return head, triggers
}

// splitSQL is a tiny semicolon splitter for statement lists that
// contain no embedded semicolons OUTSIDE of "--" line comments (i.e. no
// trigger/BEGIN...END bodies — those need splitTrailingTrigger instead).
// It IS comment-aware: a ';' inside a "-- ..." comment does not split a
// statement. This is not a hypothetical — migrations/0003_redaction_rules.sql's
// header comment reads "append-only; \"disable\" is recorded by", and a
// naive byte-scan split (core/session/migrations.go's splitSQL, which
// this package's Up functions do NOT use for exactly this reason) cuts
// that comment in half at the semicolon, corrupting the next chunk
// with a stray `"disable" is recorded by\n...` prefix that is not valid
// SQL. Caught by TestMigration0102Up_ExecutesAgainstRealSQLite driving
// this against a real sqlite connection, not a hand-rolled fixture.
func splitSQL(src string) []string {
	out := make([]string, 0, 8)
	cur := make([]byte, 0, len(src))
	inComment := false
	for i := 0; i < len(src); i++ {
		c := src[i]
		if inComment {
			cur = append(cur, c)
			if c == '\n' {
				inComment = false
			}
			continue
		}
		if c == '-' && i+1 < len(src) && src[i+1] == '-' {
			inComment = true
			cur = append(cur, c)
			continue
		}
		if c == ';' {
			if stripLineComments(string(cur)) != "" {
				out = append(out, strings.TrimSpace(string(cur)))
			}
			cur = cur[:0]
			continue
		}
		cur = append(cur, c)
	}
	// Trailing chunk after the last ';' (or the whole input, if it has
	// no ';' at all). Dropped when it is pure commentary — e.g. the
	// "-- Keep FTS in sync." line between 0001_events.sql's virtual
	// table statement and its CREATE TRIGGER, which splitTrailingTrigger
	// leaves in `head`'s tail.
	if stripLineComments(string(cur)) != "" {
		out = append(out, strings.TrimSpace(string(cur)))
	}
	return out
}

// stripLineComments removes every "-- ..." line comment from s and
// returns what remains, trimmed. Used only to decide whether a chunk
// is "real SQL" or pure commentary — callers append the ORIGINAL
// (comment-preserving) text, not this function's return value.
func stripLineComments(s string) string {
	lines := strings.Split(s, "\n")
	var b strings.Builder
	for _, line := range lines {
		if idx := strings.Index(line, "--"); idx >= 0 {
			line = line[:idx]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
}
