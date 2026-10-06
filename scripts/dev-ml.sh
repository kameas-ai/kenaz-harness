#!/usr/bin/env bash
# dev-ml.sh — make a kenaz-ml engine available to a dev harness.
#
# Finds an engine onedir (a kenaz-ml `make freeze` output) and seeds it into
# the DEV engine root (~/.kenaz/ml/dev) with cmd/mlsidecar-devseed, so the
# harness's own sidecar lifecycle spawns it on the dev lane (base :7785,
# then :7795, … past a foreign listener; recorded in engine.port) the first time an
# advisor asks — the same adopt-or-spawn path a released engine takes.
#
# NEVER fails the caller. Every problem here is a warning: the harness still
# launches, and advice falls back to its client-side heuristics ("rung none"
# in the log), which is exactly what it does on a machine with no engine.
#
# Where the engine comes from, first match wins:
#   1. $KENAZ_ML_ONEDIR           an onedir you point at explicitly
#   2. $KENAZ_ML_REPO/dist/kameas-ml
#                                 a kenaz-ml checkout you have frozen
#                                 (KENAZ_ML_REPO defaults to ../kenaz-ml
#                                 next to this repo, then ~/workspace/kenaz-ml)
#   3. the latest successful CI "Frozen bundle" artifact on kenaz-ml main,
#                                 fetched with `gh` (macOS arm64 only — that
#                                 is the only platform CI freezes), cached
#                                 under the dev root
#   4. KENAZ_ML_BUILD=1           `make freeze` in $KENAZ_ML_REPO (minutes)
#
# Knobs:
#   KENAZ_ML_SEED=0      skip entirely
#   KENAZ_ML_BUILD=1     allow step 4
#   KENAZ_ML_FRESH=1     re-check CI for a newer artifact even if a cached one exists
#
# Standalone: bash scripts/dev-ml.sh      (dev.sh runs it before `wails dev`)
set -u

warn() { printf 'dev-ml: %s\n' "$*" >&2; }
info() { printf 'dev-ml: %s\n' "$*"; }

if [ "${KENAZ_ML_SEED:-1}" = "0" ]; then
  info "KENAZ_ML_SEED=0 — not seeding an engine"
  exit 0
fi

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$HERE" || { warn "cannot cd to $HERE"; exit 0; }

if ! command -v go >/dev/null 2>&1; then
  warn "go is not on PATH; cannot seed an engine (the harness will use its heuristics)"
  exit 0
fi

DEV_ROOT="${HOME}/.kenaz/ml/dev"
CACHE="${DEV_ROOT}/.devseed-cache"

# is_onedir DIR: a complete kameas-ml onedir?
is_onedir() {
  [ -d "$1" ] && [ -f "$1/kameas-ml" ] && [ -d "$1/_internal" ]
}

# seed DIR SOURCE: install DIR into the dev root; prints the result.
seed() {
  local dir="$1" source="$2" out errf
  errf=$(mktemp)
  if out=$(go run ./cmd/mlsidecar-devseed --onedir "$dir" --source "$source" 2>"$errf"); then
    rm -f "$errf"
    info "$out"
    return 0
  fi
  warn "seeding failed: $(tail -3 "$errf")"
  rm -f "$errf"
  return 1
}

# ---- 1. explicit --------------------------------------------------------------
if [ -n "${KENAZ_ML_ONEDIR:-}" ]; then
  if is_onedir "$KENAZ_ML_ONEDIR"; then
    seed "$KENAZ_ML_ONEDIR" "local:${KENAZ_ML_ONEDIR}" && exit 0
  else
    warn "KENAZ_ML_ONEDIR=$KENAZ_ML_ONEDIR is not a kameas-ml onedir (need kameas-ml + _internal/)"
  fi
fi

# ---- 2. a sibling checkout's freeze -------------------------------------------
REPO="${KENAZ_ML_REPO:-}"
if [ -z "$REPO" ]; then
  for candidate in "$HERE/../kenaz-ml" "$HOME/workspace/kenaz-ml"; do
    if [ -d "$candidate" ]; then REPO="$candidate"; break; fi
  done
fi
if [ -n "$REPO" ] && is_onedir "$REPO/dist/kameas-ml"; then
  rev=$(git -C "$REPO" rev-parse --short HEAD 2>/dev/null || echo unknown)
  seed "$REPO/dist/kameas-ml" "local:${REPO}/dist/kameas-ml@${rev}" && exit 0
fi

# ---- 3. the latest CI artifact -------------------------------------------------
fetch_ci_artifact() {
  if [ "$(uname -s)-$(uname -m)" != "Darwin-arm64" ]; then
    warn "CI only freezes macOS arm64 engines; on $(uname -s)-$(uname -m) there is nothing to fetch"
    return 1
  fi
  if ! command -v gh >/dev/null 2>&1; then
    warn "gh is not on PATH; cannot fetch the CI engine artifact"
    return 1
  fi
  if ! gh auth status >/dev/null 2>&1; then
    warn "gh is not authenticated (gh auth login); cannot fetch the CI engine artifact"
    return 1
  fi
  local run
  run=$(gh run list -R kameas-ai/kenaz-ml --branch main --workflow CI --status success --limit 1 \
          --json databaseId --jq '.[0].databaseId' 2>/dev/null) || run=""
  if [ -z "$run" ] || [ "$run" = "null" ]; then
    # Offline, or no green run: fall back to whatever we fetched last time.
    local cached
    cached=$(ls -1dt "$CACHE"/*/kameas-ml 2>/dev/null | head -1 || true)
    if [ -n "$cached" ] && is_onedir "$cached"; then
      warn "could not reach GitHub; reusing the cached engine at $cached"
      seed "$cached" "gh-artifact:cached" && return 0
    fi
    warn "no successful kenaz-ml CI run found and nothing cached"
    return 1
  fi
  local dest="$CACHE/$run/kameas-ml"
  if is_onedir "$dest" && [ "${KENAZ_ML_FRESH:-0}" != "1" ]; then
    info "using cached CI engine from run $run"
  else
    info "fetching the kenaz-ml 'Frozen bundle' artifact from CI run $run (this is a ~400 MB download)"
    rm -rf "$dest"
    mkdir -p "$dest"
    if ! gh run download "$run" -R kameas-ai/kenaz-ml -n kenaz-ml-macos-arm64 -D "$dest" >/dev/null 2>&1; then
      warn "artifact download failed (expired after 7 days, or no artifact on that run)"
      rm -rf "$dest"
      return 1
    fi
    # upload-artifact does not preserve the executable bit (CI restores it too).
    chmod +x "$dest/kameas-ml" 2>/dev/null || true
    find "$dest" \( -name '*.dylib' -o -name '*.so' \) -exec chmod +x {} + 2>/dev/null || true
    # Keep only the two most recent downloads.
    ls -1dt "$CACHE"/* 2>/dev/null | tail -n +3 | xargs rm -rf 2>/dev/null || true
  fi
  if ! is_onedir "$dest"; then
    warn "downloaded artifact at $dest is not a kameas-ml onedir"
    return 1
  fi
  seed "$dest" "gh-artifact:kameas-ai/kenaz-ml/actions/runs/$run"
}
fetch_ci_artifact && exit 0

# ---- 4. build it ---------------------------------------------------------------
if [ "${KENAZ_ML_BUILD:-0}" = "1" ]; then
  if [ -z "$REPO" ] || [ ! -f "$REPO/Makefile" ]; then
    warn "KENAZ_ML_BUILD=1 but no kenaz-ml checkout found (set KENAZ_ML_REPO)"
  elif ! command -v uv >/dev/null 2>&1; then
    warn "KENAZ_ML_BUILD=1 but uv is not on PATH (kenaz-ml builds with uv)"
  else
    info "building the engine: make -C $REPO freeze (this takes a few minutes)"
    if make -C "$REPO" freeze >/dev/null 2>&1 && is_onedir "$REPO/dist/kameas-ml"; then
      rev=$(git -C "$REPO" rev-parse --short HEAD 2>/dev/null || echo unknown)
      seed "$REPO/dist/kameas-ml" "local:${REPO}/dist/kameas-ml@${rev}" && exit 0
    else
      warn "make freeze failed in $REPO"
    fi
  fi
fi

warn "no engine seeded — the harness will run with its client-side heuristics only."
warn "to get one: freeze a kenaz-ml checkout (cd ../kenaz-ml && make freeze), set KENAZ_ML_ONEDIR, or KENAZ_ML_BUILD=1"
exit 0
