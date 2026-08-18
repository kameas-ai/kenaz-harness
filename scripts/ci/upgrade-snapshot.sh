#!/usr/bin/env bash
# upgrade-snapshot.sh <tag> — produce (or regenerate) one entry in the
# per-tag upgrade snapshot chain (upgrade-path-coverage-01PMUG01,
# spec.md §5.3).
#
# WHAT THIS DOES
#
#   snapshot(GENESIS_TAG) is the hand-bootstrapped shape a real upgraded
#   install was in the instant before the v0.63.0 P0's four dormant
#   sessions migrations landed — see
#   core/storage/sqlite/testdata/upgrade/v0.63.0/PROVENANCE.md. It is
#   built from testdata/upgrade/seed.sql against a FRESH database at
#   the CURRENT tree (this script's own checkout), then rewound; it is
#   not replayed from anything earlier because there is nothing
#   earlier to replay from.
#
#   snapshot(N) for every tag after the genesis is
#   replay(snapshot(N-1), N): a throwaway `git worktree add` at tag N,
#   the previous snapshot's dump.sql materialised into a fresh data.db,
#   Open() run under tag N's code (applying whatever became newly
#   pending), and the result dumped back out.
#
# USAGE
#
#   bash scripts/ci/upgrade-snapshot.sh <tag>
#
#   <tag> must be an existing git tag (a real release) OR the literal
#   string "HEAD", which snapshots the current working tree without a
#   worktree checkout (useful for testing an unreleased tree before
#   tagging — the result is NOT committed automatically; review it like
#   any other generated file).
#
# DETERMINISM
#
#   Re-running this script for an ALREADY-COMMITTED tag must reproduce
#   byte-identical output (spec §4, "Snapshots for released tags are
#   immutable"). check-upgrade-snapshots-locked.sh enforces that
#   nobody edits a committed snapshot directory; this script is what
#   proves a regeneration attempt agrees with it.
#
# SELF-HOSTED RUNNER NOTE
#
#   No `sqlite3(1)` dependency anywhere in this path — the generator
#   (scripts/ci/upgrade-snapshot/generator_main.go) is pure Go over
#   database/sql + modernc.org/sqlite (spec §4, "self-hosted runner
#   rules bind").

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"

GENESIS_TAG="v0.63.0"
TESTDATA_DIR="core/storage/sqlite/testdata/upgrade"
SEED_FILE="$TESTDATA_DIR/seed.sql"
GENERATOR_SRC="scripts/ci/upgrade-snapshot/generator_main.go"

TAG="${1:-}"
if [[ -z "$TAG" ]]; then
  echo "usage: $0 <tag>" >&2
  exit 2
fi

OUT_DIR="$TESTDATA_DIR/$TAG"
mkdir -p "$OUT_DIR"
OUT_FILE="$OUT_DIR/dump.sql"

build_generator() {
  local src_dir="$1" out_bin="$2"
  ( cd "$src_dir" && go build -o "$out_bin" ./scripts/ci/upgrade-snapshot/ )
}

if [[ "$TAG" == "$GENESIS_TAG" ]]; then
  echo "[upgrade-snapshot] genesis mode: $GENESIS_TAG"
  if [[ ! -f "$SEED_FILE" ]]; then
    echo "missing $SEED_FILE" >&2
    exit 1
  fi
  WORK="$(mktemp -d)"
  trap 'rm -rf "$WORK"' EXIT
  build_generator "$REPO_ROOT" "$WORK/gen"
  "$WORK/gen" -mode=genesis -seed "$SEED_FILE" -out "$OUT_FILE"
  echo "[upgrade-snapshot] wrote $OUT_FILE"
  exit 0
fi

# Replay mode: find the immediately-previous committed snapshot by
# semver order among the directories already under testdata/upgrade/.
PREV_TAG=""
for d in "$TESTDATA_DIR"/v*/; do
  [[ -d "$d" ]] || continue
  name="$(basename "$d")"
  [[ -f "$d/dump.sql" ]] || continue
  if [[ "$name" == "$TAG" ]]; then
    continue
  fi
  # Numeric semver compare via sort -V; keep the highest tag that is
  # still less than TAG.
  if [[ "$(printf '%s\n%s\n' "$name" "$TAG" | sort -V | tail -1)" == "$TAG" ]]; then
    if [[ -z "$PREV_TAG" ]] || [[ "$(printf '%s\n%s\n' "$PREV_TAG" "$name" | sort -V | tail -1)" == "$name" ]]; then
      PREV_TAG="$name"
    fi
  fi
done

if [[ -z "$PREV_TAG" ]]; then
  echo "[upgrade-snapshot] no committed snapshot older than $TAG found under $TESTDATA_DIR" >&2
  echo "[upgrade-snapshot] the chain bootstraps at $GENESIS_TAG — build that first, or pass it explicitly." >&2
  exit 1
fi
PREV_DUMP="$REPO_ROOT/$TESTDATA_DIR/$PREV_TAG/dump.sql"
echo "[upgrade-snapshot] replay mode: $PREV_TAG -> $TAG"

if [[ "$TAG" == "HEAD" ]]; then
  WORK="$(mktemp -d)"
  trap 'rm -rf "$WORK"' EXIT
  build_generator "$REPO_ROOT" "$WORK/gen"
  "$WORK/gen" -mode=replay -prev "$PREV_DUMP" -out "$OUT_FILE"
  echo "[upgrade-snapshot] wrote $OUT_FILE (HEAD, not a committed tag)"
  exit 0
fi

if ! git rev-parse -q --verify "refs/tags/$TAG" >/dev/null; then
  echo "[upgrade-snapshot] no such tag: $TAG" >&2
  exit 1
fi

WORKTREE="$(mktemp -d)"
cleanup() {
  git -C "$REPO_ROOT" worktree remove --force "$WORKTREE" >/dev/null 2>&1 || true
  rm -rf "$WORKTREE"
}
trap cleanup EXIT

git -C "$REPO_ROOT" worktree add --detach "$WORKTREE" "$TAG" >&2
mkdir -p "$WORKTREE/frontend/dist"
touch "$WORKTREE/frontend/dist/.gitkeep"

if [[ ! -f "$WORKTREE/$GENERATOR_SRC" ]]; then
  # Old tags don't contain the generator — inject today's copy. It is
  # self-contained by design (see the file's own header) precisely so
  # this works.
  mkdir -p "$WORKTREE/_upgrade_snapshot_gen"
  cp "$REPO_ROOT/$GENERATOR_SRC" "$WORKTREE/_upgrade_snapshot_gen/main.go"
  GEN_PKG="./_upgrade_snapshot_gen/"
else
  GEN_PKG="./scripts/ci/upgrade-snapshot/"
fi

GEN_BIN="$WORKTREE/_gen_bin"
( cd "$WORKTREE" && go build -o "$GEN_BIN" "$GEN_PKG" )

"$GEN_BIN" -mode=replay -prev "$PREV_DUMP" -out "$OUT_FILE"
echo "[upgrade-snapshot] wrote $OUT_FILE"
