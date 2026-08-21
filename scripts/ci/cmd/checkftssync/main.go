// Command checkftssync is the Go half of
// scripts/ci/check-fts-sync.sh (G-3, audit-that-tells-the-truth-01PMZA10
// WP12, tasks.md §UNIT-10).
//
// THE DEFECT CLASS. An external-content FTS5 virtual table
// (CREATE VIRTUAL TABLE ... USING fts5(..., content='<table>', ...))
// stores no text of its own — it reads rows out of <table> on demand by
// rowid. If <table> ever DELETEs a row without also telling the FTS5
// index (the special `INSERT INTO <fts>(<fts>, rowid, ...)
// VALUES ('delete', ...)` command, issued either from an
// `AFTER DELETE ON <table>` trigger or directly from application code),
// two things go wrong at once:
//   - the deleted row's terms stay matchable — an existence oracle over
//     data a caller believed was gone;
//   - any subsequent SearchFTS query that matches the now-missing rowid
//     errors: `fts5: missing row N from content table 'main'.'<table>'`.
//
// This shipped for real: `core/event/log/migrations/0001_events.sql`
// created events_fts with only an AFTER INSERT trigger and a comment
// claiming "No update / delete triggers: append-only at storage layer
// (C-002)" — false the day it shipped (SweepableBackend.DeleteRows /
// BulkPurge already existed), and [RAN]-proven false in
// audit-that-tells-the-truth-01PMZA10's spec. Migration 0007
// (event-log/0106) fixed it. `core/session/migrations_search_fts.go`'s
// messages_fts got its delete/update triggers right the first time
// (0312) — this gate is what makes that the enforced default instead of
// a convention two migrations already violated once.
//
// WHY GO, NOT GREP. The DDL for an external-content table's column list
// and its delete-sync command both span multiple lines, sometimes across
// a Go raw-string const boundary (core/session/migrations_search_fts.go)
// and sometimes across a plain .sql file (core/event/log/migrations/).
// A single-line grep cannot express "does this file, taken as a whole,
// contain both a CREATE VIRTUAL TABLE ... content='X' AND a matching
// delete-sync command for the same table" — this needs whole-file
// pattern matching, which is what regexp.MustCompile(`(?s)...`) is for.
//
// SCOPE. Scans every .sql and .go file under the given root, excluding
// _test.go files and anything under a "testdata" directory (dump
// snapshots are materialized DB state, not migration source — scanning
// them would double-count or go stale relative to the source migrations
// that actually define the schema).
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// reCreateVirtualFTS5 matches CREATE VIRTUAL TABLE ... USING fts5(<body>).
// The body itself has no nested parens in any DDL this repo has ever
// shipped (verified against both current tables), so a non-greedy match
// up to the first ')' is sufficient and avoids needing a paren-balancing
// parser for a two-instance problem.
var reCreateVirtualFTS5 = regexp.MustCompile(
	`(?is)CREATE\s+VIRTUAL\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?(\w+)\s+USING\s+fts5\s*\(([^)]*)\)`,
)

// reContentEquals extracts the content='<table>' shadow-table name from
// a CREATE VIRTUAL TABLE body. A missing or empty content value means
// the table is NOT external-content (it stores its own copy) and is out
// of this gate's scope — only a shadow read needs delete-sync
// protection.
var reContentEquals = regexp.MustCompile(`(?is)content\s*=\s*'(\w+)'`)

// reAfterInsertTrigger matches an AFTER INSERT trigger targeting a given
// content table — the signal that something is trying to keep an FTS5
// shadow in sync at all (as opposed to a table nothing populates).
var reAfterInsertTrigger = regexp.MustCompile(
	`(?is)CREATE\s+TRIGGER\s+(?:IF\s+NOT\s+EXISTS\s+)?\w+\s+AFTER\s+INSERT\s+ON\s+(\w+)`,
)

// reDeleteSyncCommand matches FTS5's special delete-sync form:
// INSERT INTO <fts>(<fts>, rowid, ...) VALUES ('delete', ...). This is
// the ONE shape that removes a row from an external-content FTS5 index
// without touching the shadow table — issued either from inside an
// AFTER DELETE trigger body (both shipped triggers use exactly this
// form) or directly from application code (the "DeleteRows issues the
// FTS5 'delete' command in the same transaction" alternative WP10's
// spec names). Both group 1 and group 2 must name the SAME fts table —
// Go's RE2 engine has no backreferences, so this is checked in code
// rather than in the pattern.
var reDeleteSyncCommand = regexp.MustCompile(
	`(?is)INSERT\s+INTO\s+(\w+)\s*\(\s*(\w+)\s*,[^)]*\)\s*VALUES\s*\([^)]*'delete'`,
)

type ftsTable struct {
	name         string // the fts5 virtual table's own name
	contentTable string // the shadow table it reads from
	file         string
	line         int
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: checkftssync <scan-root>")
		os.Exit(2)
	}
	root := os.Args[1]

	var (
		externalContentTables []ftsTable
		insertSyncedTables    = map[string]bool{} // content table -> has AFTER INSERT trigger
		deleteSyncedFTS       = map[string]bool{} // fts table name -> has a 'delete'-command sync
	)

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".sql" && ext != ".go" {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}

		src, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		text := string(src)

		for _, m := range reCreateVirtualFTS5.FindAllStringSubmatchIndex(text, -1) {
			name := text[m[2]:m[3]]
			body := text[m[4]:m[5]]
			cm := reContentEquals.FindStringSubmatch(body)
			if cm == nil || cm[1] == "" {
				continue // contentless / self-contained — not this gate's class
			}
			line := 1 + strings.Count(text[:m[0]], "\n")
			externalContentTables = append(externalContentTables, ftsTable{
				name:         name,
				contentTable: cm[1],
				file:         path,
				line:         line,
			})
		}

		for _, m := range reAfterInsertTrigger.FindAllStringSubmatch(text, -1) {
			insertSyncedTables[m[1]] = true
		}

		for _, m := range reDeleteSyncCommand.FindAllStringSubmatch(text, -1) {
			if m[1] == m[2] {
				deleteSyncedFTS[m[1]] = true
			}
		}
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "checkftssync: walking %s: %v\n", root, err)
		os.Exit(1)
	}

	// De-duplicate: the same CREATE VIRTUAL TABLE can appear once per
	// definition site; report each distinct (name, contentTable) pair once
	// even if matched from more than one file (e.g. a .sql file and a
	// generated/hashed copy).
	seen := map[string]bool{}
	var violations []string
	for _, t := range externalContentTables {
		key := t.name + "|" + t.contentTable
		if seen[key] {
			continue
		}
		seen[key] = true

		if !insertSyncedTables[t.contentTable] {
			// Nothing populates this shadow at all yet — out of scope for
			// this gate (a table nobody keeps in sync going stale is a
			// different defect, and not the one that errors on read).
			continue
		}
		if deleteSyncedFTS[t.name] {
			continue // has an AFTER DELETE trigger or a direct 'delete'-command sync
		}
		violations = append(violations, fmt.Sprintf(
			"%s:%d: external-content FTS5 table %q (content=%q) has an AFTER INSERT sync trigger "+
				"but no AFTER DELETE trigger and no direct 'delete'-command sync anywhere under %s — "+
				"deleting a row from %q will leave its terms matchable in %q AND make any SearchFTS "+
				"query that matches the deleted rowid error with "+
				"\"fts5: missing row N from content table\"",
			t.file, t.line, t.name, t.contentTable, root, t.contentTable, t.name,
		))
	}

	if len(violations) > 0 {
		sort.Strings(violations)
		for _, v := range violations {
			fmt.Fprintln(os.Stderr, v)
		}
		os.Exit(1)
	}

	fmt.Printf("checkftssync: clean — %d external-content FTS5 table(s) checked under %s, all delete-synced.\n",
		len(seen), root)
}
