#!/usr/bin/env bash
# check-destructive-migration-coverage.sh — I14
# (upgrade-path-coverage-01PMUG01 WP03, spec.md FR-2).
#
# Every migration whose Up() executes DROP TABLE, DROP INDEX,
# DROP TRIGGER, DROP COLUMN, ALTER TABLE ... RENAME, or DELETE FROM
# outside a scratch/backup table must have a populated-table test (a
# _test.go file whose source references the migration's ID string,
# e.g. "sessions/0327-source-model-output") or a dated entry in
# scripts/ci/allowlists/i14-destructive-migration-coverage.txt.
#
# Down() bodies are explicitly OUT OF SCOPE: Registry.Rollback has no
# production caller (only runner.go:153, adapters.go:172,
# storage.go:214 reference it — see spec.md §3 Non-goals). If a
# Rollback caller is ever added, this gate's scope must widen in the
# same change.
#
# The actual scan (deciding which SQL text belongs to which
# migration's Up closure, including SQL held in a referenced
# package-level const/var rather than inline) is a Go syntax question,
# not a grep question — see scripts/ci/cmd/checkdestructivemigrations
# for why and how.
#
# Usage: bash scripts/ci/check-destructive-migration-coverage.sh

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[destructive-migration-coverage]"
SCAN_ROOT="core"
ALLOW_FILE="scripts/ci/allowlists/i14-destructive-migration-coverage.txt"

ci_require_dir "$SCAN_ROOT" "$GATE"
ci_require_file "$ALLOW_FILE" "$GATE"

BIN="$(mktemp -d)/checkdestructivemigrations"
trap 'rm -rf "$(dirname "$BIN")"' EXIT

echo "${GATE} building checker..."
if ! go build -o "$BIN" ./scripts/ci/cmd/checkdestructivemigrations/; then
  echo "${GATE} FAIL: checkdestructivemigrations failed to build." >&2
  exit 1
fi

if ! "$BIN" "$SCAN_ROOT" "$SCAN_ROOT" "$ALLOW_FILE"; then
  echo "" >&2
  echo "${GATE} FAIL — see violations above." >&2
  echo "${GATE} Fix: write a populated-table test that rewinds the migration's own" >&2
  echo "${GATE} ledger row, seeds the target table (and every FK-referencing table)," >&2
  echo "${GATE} reopens, and asserts rows/content survive — see" >&2
  echo "${GATE} core/storage/sqlite/migration_0327_test.go for the house pattern." >&2
  echo "${GATE} Or add a dated, justified entry to ${ALLOW_FILE}." >&2
  exit 1
fi

echo "${GATE} clean."
