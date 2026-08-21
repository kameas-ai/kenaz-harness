// Command upgrade-snapshot-generator produces one entry in the
// per-tag upgrade snapshot chain (upgrade-path-coverage-01PMUG01,
// spec.md §5.3).
//
// SELF-CONTAINED BY DESIGN. This file is not committed as a permanent
// package — scripts/ci/upgrade-snapshot.sh copies it, verbatim, into a
// throwaway git worktree checked out at a historical release tag and
// runs it there with `go run`. It therefore depends on nothing except
// the standard library, database/sql, modernc.org/sqlite and this
// repo's two oldest-stable, cross-tag-compatible symbols:
// storage.Config and storagesqlite.Open. It duplicates (rather than
// imports) the dump/materialise logic in
// core/storage/sqlite/upgradesnap — that package does not exist at old
// tags, and a generator that imports it would not compile there. Keep
// the two copies in sync; core/storage/sqlite/upgradesnap/upgradesnap_test.go
// round-trips the canonical copy, and check-upgrade-snapshots-locked.sh
// regenerating the latest committed snapshot byte-identically is the
// cheapest signal that the two have not drifted apart.
//
// Two modes:
//
//	-mode=genesis  Build the v0.63.0 BOOTSTRAP snapshot: apply
//	               seed.sql to a fresh, fully-migrated database (every
//	               migration this tag registers), then rewind it to the
//	               exact shape a real upgraded install was in the
//	               instant before the v0.63.0 P0's four dormant
//	               sessions migrations (332-335) landed — see
//	               core/storage/sqlite/testdata/upgrade/v0.63.0/PROVENANCE.md
//	               for why this reproduces real historical state rather
//	               than encoding a belief about it (spec §5.3).
//
//	-mode=replay   The steady-state case: materialise the PREVIOUS
//	               snapshot's dump.sql into a fresh data.db, Open it
//	               under THIS TAG's code (applying whatever migrations
//	               are newly pending), and dump the result. This is
//	               what scripts/ci/upgrade-snapshot.sh runs inside a
//	               `git worktree add <tmp> <tag>` checkout for every
//	               tag after the genesis.
package main

import (
	"context"
	"database/sql"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"

	_ "modernc.org/sqlite"
)

func main() {
	mode := flag.String("mode", "", "genesis | replay")
	seedPath := flag.String("seed", "", "genesis mode: path to seed.sql")
	prevPath := flag.String("prev", "", "replay mode: path to the previous snapshot's dump.sql")
	outPath := flag.String("out", "", "path to write the new dump.sql")
	flag.Parse()

	if *outPath == "" {
		fatal("missing -out")
	}

	dataDir, err := os.MkdirTemp("", "upgrade-snapshot-*")
	if err != nil {
		fatal("mkdtemp: %v", err)
	}
	defer os.RemoveAll(dataDir)

	switch *mode {
	case "genesis":
		if *seedPath == "" {
			fatal("genesis mode requires -seed")
		}
		if err := runGenesis(dataDir, *seedPath, *outPath); err != nil {
			fatal("genesis: %v", err)
		}
	case "replay":
		if *prevPath == "" {
			fatal("replay mode requires -prev")
		}
		if err := runReplay(dataDir, *prevPath, *outPath); err != nil {
			fatal("replay: %v", err)
		}
	default:
		fatal("unknown -mode %q (want genesis|replay)", *mode)
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "upgrade-snapshot-generator: "+format+"\n", args...)
	os.Exit(1)
}

// rewindStatements reproduces the exact pre-v0.63.0-P0-fix shape: the
// four sessions migrations (332-335) unapplied and the columns/schema
// changes they made rolled back by hand. This is the SAME recipe
// core/storage/sqlite/repair_upgrade_test.go and artifacts_rebuild_test.go
// already use and which those tests' own doc comments record as
// "verified equivalently against a copy of the live dev database" —
// this generator's genesis mode is not inventing a new belief, it is
// running that already-validated recipe through the production Open
// path instead of a hand test.
var rewindStatements = []string{
	"DELETE FROM harness_migrations WHERE owning_mission='sessions' AND version >= 332",
	"ALTER TABLE session_messages DROP COLUMN kind",
	"ALTER TABLE session_messages DROP COLUMN move_index",
	"ALTER TABLE session_messages DROP COLUMN turn_span_id",
	"ALTER TABLE session_messages DROP COLUMN model_tool_args",
	"ALTER TABLE sessions DROP COLUMN move_history_mode",
}

func runGenesis(dataDir, seedPath, outPath string) error {
	ctx := context.Background()

	db, err := storagesqlite.Open(storage.Config{
		DataDir:          dataDir,
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	})
	if err != nil {
		return fmt.Errorf("initial Open: %w", err)
	}

	seedSQL, err := os.ReadFile(seedPath)
	if err != nil {
		_ = db.Close(ctx)
		return fmt.Errorf("read seed.sql: %w", err)
	}
	sqlDB, ok := db.(interface{ SQL() *sql.DB })
	if !ok {
		_ = db.Close(ctx)
		return fmt.Errorf("storage.DB does not expose SQL()")
	}
	raw := sqlDB.SQL()
	if err := materializeData(ctx, raw, string(seedSQL)); err != nil {
		_ = db.Close(ctx)
		return fmt.Errorf("apply seed.sql: %w", err)
	}
	if err := db.Close(ctx); err != nil {
		return fmt.Errorf("close after seed: %w", err)
	}

	// Rewind behind the harness's back, on a raw connection, then close
	// it before handing the directory to Open again — the lockfile is
	// per-Open, not per-process.
	rawDB, err := openRawSQLite(dataDir)
	if err != nil {
		return fmt.Errorf("open raw for rewind: %w", err)
	}
	for _, stmt := range rewindStatements {
		if _, err := rawDB.ExecContext(ctx, stmt); err != nil {
			_ = rawDB.Close()
			return fmt.Errorf("rewind %q: %w", stmt, err)
		}
	}

	dump, err := dumpDB(ctx, rawDB)
	if err != nil {
		_ = rawDB.Close()
		return fmt.Errorf("dump: %w", err)
	}
	if err := rawDB.Close(); err != nil {
		return fmt.Errorf("close raw: %w", err)
	}
	return os.WriteFile(outPath, []byte(dump), 0o644)
}

func runReplay(dataDir, prevPath, outPath string) error {
	ctx := context.Background()

	prevDump, err := os.ReadFile(prevPath)
	if err != nil {
		return fmt.Errorf("read previous dump: %w", err)
	}

	rawDB, err := openRawSQLite(dataDir)
	if err != nil {
		return fmt.Errorf("open raw for materialise: %w", err)
	}
	if err := materializeAll(ctx, rawDB, string(prevDump)); err != nil {
		_ = rawDB.Close()
		return fmt.Errorf("materialise previous snapshot: %w", err)
	}
	if err := rawDB.Close(); err != nil {
		return fmt.Errorf("close raw after materialise: %w", err)
	}

	// Reopen through THIS TAG's production Open path — this is the
	// replay step: whatever migrations this tag registers that are
	// pending against the materialised ledger run for real now.
	db, err := storagesqlite.Open(storage.Config{
		DataDir:          dataDir,
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	})
	if err != nil {
		return fmt.Errorf("replay Open: %w", err)
	}
	sqlDB, ok := db.(interface{ SQL() *sql.DB })
	if !ok {
		_ = db.Close(ctx)
		return fmt.Errorf("storage.DB does not expose SQL()")
	}
	dump, err := dumpDB(ctx, sqlDB.SQL())
	if err != nil {
		_ = db.Close(ctx)
		return fmt.Errorf("dump: %w", err)
	}
	if err := db.Close(ctx); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	return os.WriteFile(outPath, []byte(dump), 0o644)
}

func openRawSQLite(dataDir string) (*sql.DB, error) {
	dsn := "file:" + dataDir + "/data.db?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

// ---- dump/materialise, duplicated from core/storage/sqlite/upgradesnap.
// See the package doc at the top of this file for why.

const (
	fixedInstallID = "upgrade-snapshot-fixed-install-id"
	fixedTimestamp = "1970-01-01T00:00:00Z"
)

// ftsVirtualTableNames returns the set of fts5 virtual table names
// present in objs, detected from each table's own CREATE VIRTUAL
// TABLE ... USING fts5(...) DDL — NOT a hardcoded list. Kept in sync
// with core/storage/sqlite/upgradesnap.ftsVirtualTableNames; see that
// copy's doc comment for why a hardcoded list drifted silently and
// corrupted a dump (a NUL byte embedded in an un-hex-encoded shadow-
// table blob) the moment a second FTS5 table existed in the schema.
func ftsVirtualTableNames(objs []schemaObj) map[string]bool {
	out := map[string]bool{}
	for _, o := range objs {
		if o.typ != "table" {
			continue
		}
		upper := strings.ToUpper(o.sql)
		if strings.Contains(upper, "VIRTUAL TABLE") && strings.Contains(upper, "FTS5") {
			out[o.name] = true
		}
	}
	return out
}

func isFTSShadow(name string, ftsNames map[string]bool) bool {
	for v := range ftsNames {
		if name != v && strings.HasPrefix(name, v+"_") {
			return true
		}
	}
	return false
}

type schemaObj struct{ typ, name, sql string }

func (o schemaObj) isVirtualTable() bool {
	return o.typ == "table" && strings.Contains(strings.ToUpper(o.sql), "VIRTUAL TABLE")
}

func dumpDB(ctx context.Context, db *sql.DB) (string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT type, name, sql FROM sqlite_master
		WHERE sql IS NOT NULL AND type IN ('table','index','trigger')
		  AND name NOT LIKE 'sqlite_%'
		ORDER BY type, name
	`)
	if err != nil {
		return "", err
	}
	var objs []schemaObj
	for rows.Next() {
		var o schemaObj
		if err := rows.Scan(&o.typ, &o.name, &o.sql); err != nil {
			rows.Close()
			return "", err
		}
		objs = append(objs, o)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", err
	}
	ftsNames := ftsVirtualTableNames(objs)

	var b strings.Builder
	b.WriteString("-- upgrade-snapshot dump. Generated by scripts/ci/upgrade-snapshot.\n")
	b.WriteString("-- Normalised: install_id and every applied_at are fixed literals;\n")
	b.WriteString("-- content_hash is verbatim (load-bearing, see VerifyLedger).\n")
	b.WriteString("-- DO NOT HAND-EDIT. Regenerate with scripts/ci/upgrade-snapshot.sh.\n\n")

	b.WriteString("-- === SCHEMA (sorted by type, name) ===\n\n")
	for _, o := range objs {
		if o.typ == "table" && isFTSShadow(o.name, ftsNames) {
			continue
		}
		stmt := strings.TrimSpace(o.sql)
		if !strings.HasSuffix(stmt, ";") {
			stmt += ";"
		}
		b.WriteString(stmt)
		b.WriteString("\n\n")
	}

	b.WriteString("-- === DATA (sorted by table name, rows sorted by primary key) ===\n\n")
	for _, o := range objs {
		if o.typ != "table" || o.isVirtualTable() || isFTSShadow(o.name, ftsNames) || o.name == "sqlite_sequence" {
			continue
		}
		stmts, err := dumpTable(ctx, db, o.name)
		if err != nil {
			return "", fmt.Errorf("dump table %s: %w", o.name, err)
		}
		for _, s := range stmts {
			b.WriteString(s)
			b.WriteString("\n")
		}
		if len(stmts) > 0 {
			b.WriteString("\n")
		}
	}
	return b.String(), nil
}

func dumpTable(ctx context.Context, db *sql.DB, table string) ([]string, error) {
	cols, err := colsOf(ctx, db, table, false)
	if err != nil {
		return nil, err
	}
	if len(cols) == 0 {
		return nil, nil
	}
	pk, err := colsOf(ctx, db, table, true)
	if err != nil {
		return nil, err
	}
	orderBy := "rowid"
	if len(pk) > 0 {
		orderBy = strings.Join(quoteIdents(pk), ", ")
	}
	q := fmt.Sprintf("SELECT %s FROM %s ORDER BY %s", strings.Join(quoteIdents(cols), ", "), quoteIdent(table), orderBy)
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		for i, c := range cols {
			switch {
			case c == "install_id":
				vals[i] = fixedInstallID
			case c == "applied_at":
				vals[i] = fixedTimestamp
			case table == "harness_meta" && c == "created_at":
				vals[i] = fixedTimestamp
			}
		}
		out = append(out, buildInsert(table, cols, vals))
	}
	return out, rows.Err()
}

func colsOf(ctx context.Context, db *sql.DB, table string, pkOnly bool) ([]string, error) {
	q := "SELECT name FROM pragma_table_info(?)"
	if pkOnly {
		q = "SELECT name FROM pragma_table_info(?) WHERE pk > 0 ORDER BY pk"
	}
	rows, err := db.QueryContext(ctx, q, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

func buildInsert(table string, cols []string, vals []any) string {
	var b strings.Builder
	fmt.Fprintf(&b, "INSERT INTO %s (%s) VALUES (", quoteIdent(table), strings.Join(quoteIdents(cols), ", "))
	for i, v := range vals {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(literal(v))
	}
	b.WriteString(");")
	return b.String()
}

func literal(v any) string {
	if v == nil {
		return "NULL"
	}
	switch t := v.(type) {
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'g', -1, 64)
	case []byte:
		// SQLite BLOB literal syntax (X'...'), not a quoted string — see
		// core/storage/sqlite/upgradesnap.sqlLiteral's doc comment for
		// why wrapping raw bytes in '...' corrupts the dump the moment a
		// BLOB column carries real binary content (e.g. events.payload_hash).
		return "X'" + hex.EncodeToString(t) + "'"
	case string:
		return quoteStr(t)
	case bool:
		if t {
			return "1"
		}
		return "0"
	default:
		return quoteStr(fmt.Sprint(t))
	}
}

func quoteStr(s string) string  { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
func quoteIdents(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = quoteIdent(s)
	}
	return out
}

var beginWordRe = regexp.MustCompile(`(?i)\bBEGIN\b`)
var endWordRe = regexp.MustCompile(`(?i)\bEND\b`)

// splitStatements splits on top-level ';', tracking a BEGIN/END depth
// counter so a CREATE TRIGGER body's own internal statement-ending
// semicolons don't fragment it. See the sibling copy in
// core/storage/sqlite/upgradesnap/upgradesnap.go for the full
// rationale — kept in sync manually (this file must stay self-
// contained, see the package doc at the top of this file).
func splitStatements(script string) []string {
	var out []string
	var cur strings.Builder
	depth := 0
	for _, line := range strings.Split(script, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "--") {
			if depth == 0 {
				continue
			}
		}
		cur.WriteString(line)
		cur.WriteByte('\n')
		depth += len(beginWordRe.FindAllString(trimmed, -1)) - len(endWordRe.FindAllString(trimmed, -1))
		if depth < 0 {
			depth = 0
		}
		if depth == 0 && strings.HasSuffix(trimmed, ";") {
			out = append(out, strings.TrimSpace(cur.String()))
			cur.Reset()
		}
	}
	if strings.TrimSpace(cur.String()) != "" {
		out = append(out, strings.TrimSpace(cur.String()))
	}
	return out
}

// materializeAll replays a FULL dump (schema + data) into a fresh raw
// connection. Used by replay mode to reconstruct the previous
// snapshot's database before handing it to production Open.
func materializeAll(ctx context.Context, db *sql.DB, dumpText string) error {
	return materialize(ctx, db, dumpText, true)
}

// materializeData replays a DATA-ONLY script (seed.sql — no schema
// statements, the schema already exists from a fresh Open) into db.
func materializeData(ctx context.Context, db *sql.DB, script string) error {
	return materialize(ctx, db, script, false)
}

func materialize(ctx context.Context, db *sql.DB, text string, hasSchema bool) error {
	stmts := splitStatements(text)
	var tables, virtualTables, indexes, triggers, data []string
	for _, s := range stmts {
		upper := strings.ToUpper(strings.TrimSpace(s))
		switch {
		case strings.HasPrefix(upper, "CREATE VIRTUAL TABLE"):
			virtualTables = append(virtualTables, s)
		case strings.HasPrefix(upper, "CREATE TABLE"):
			tables = append(tables, s)
		case strings.HasPrefix(upper, "CREATE INDEX"), strings.HasPrefix(upper, "CREATE UNIQUE INDEX"):
			indexes = append(indexes, s)
		case strings.HasPrefix(upper, "CREATE TRIGGER"):
			triggers = append(triggers, s)
		case strings.HasPrefix(upper, "INSERT INTO"):
			data = append(data, s)
		default:
			return fmt.Errorf("materialize: unrecognised statement: %s", firstLine(s))
		}
	}
	if !hasSchema && (len(tables)+len(virtualTables)+len(indexes)+len(triggers) > 0) {
		return fmt.Errorf("materialize: data-only script contained schema statements")
	}
	for _, group := range [][]string{tables, virtualTables, indexes, triggers} {
		for _, s := range group {
			if _, err := db.ExecContext(ctx, s); err != nil {
				return fmt.Errorf("schema %q: %w", firstLine(s), err)
			}
		}
	}
	if len(data) == 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin data tx: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "PRAGMA defer_foreign_keys=ON"); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("defer_foreign_keys: %w", err)
	}
	for _, s := range data {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("data %q: %w", firstLine(s), err)
		}
	}
	return tx.Commit()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		s = s[:120] + "..."
	}
	return s
}
