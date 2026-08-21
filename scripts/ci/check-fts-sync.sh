#!/usr/bin/env bash
# check-fts-sync.sh — G-3, the FTS-sync gate (new class)
# (audit-that-tells-the-truth-01PMZA10 WP12, tasks.md §UNIT-10).
#
# THE DEFECT CLASS. Fails if any migration (Go or .sql, under the scan
# root) creates an external-content FTS5 virtual table
# (USING fts5(..., content='<table>', ...)) with an AFTER INSERT sync
# trigger and no AFTER DELETE trigger — unless the same corpus also
# contains a direct `INSERT INTO <fts>(<fts>, rowid, ...)
# VALUES ('delete', ...)` command (the alternative WP10's spec names:
# "DeleteRows issues the FTS5 'delete' command in the same
# transaction"). Either shape keeps a shadow-read FTS5 index in sync
# with DELETEs against the table it reads from; having neither means a
# delete leaves the term matchable AND makes a later SearchFTS on that
# rowid error with "fts5: missing row N from content table".
#
# THIS CLASS HAS SHIPPED TWICE. `core/event/log/migrations/0001_events.sql`
# created events_fts with only an AFTER INSERT trigger and a comment
# ("No update / delete triggers: append-only at storage layer (C-002)")
# that was false the day it shipped — SweepableBackend.DeleteRows /
# BulkPurge already existed in the same package. `sessions/0335` exists
# because the exact same sync drifted for messages_fts's tool-row
# rewrite. Migration 0007 (event-log/0106) is the fix for the first
# instance; this gate is the standing guard against a third.
#
# The real parsing (DDL and trigger bodies spanning multiple lines,
# sometimes across a Go raw-string boundary) is a whole-file pattern
# question, not a single-line grep question — see
# scripts/ci/cmd/checkftssync for why and how.
#
# Planted-violation proof: scripts/ci/gates_can_fail_test.go
# "fts-sync/external-content-table-no-delete-sync".
#
# Usage: bash scripts/ci/check-fts-sync.sh

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[fts-sync]"
SCAN_ROOT="core"

ci_require_dir "$SCAN_ROOT" "$GATE"

BIN="$(mktemp -d)/checkftssync"
trap 'rm -rf "$(dirname "$BIN")"' EXIT

echo "${GATE} building checker..."
if ! go build -o "$BIN" ./scripts/ci/cmd/checkftssync/; then
  echo "${GATE} FAIL: checkftssync failed to build." >&2
  exit 1
fi

if ! OUT="$("$BIN" "$SCAN_ROOT" 2>&1)"; then
  echo "$OUT" | sed "s/^/${GATE} /" >&2
  echo "${GATE} FAIL — see violation(s) above." >&2
  echo "${GATE} Fix: add an AFTER DELETE (and AFTER UPDATE) trigger for the table, mirroring" >&2
  echo "${GATE} core/event/log/migrations/0007_events_fts_sync.up.sql or" >&2
  echo "${GATE} core/session/migrations_search_fts.go's messages_fts_ad, in the SAME commit" >&2
  echo "${GATE} that adds the AFTER INSERT trigger." >&2
  exit 1
fi

echo "${GATE} ${OUT}"
