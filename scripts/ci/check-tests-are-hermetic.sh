#!/usr/bin/env bash
# check-tests-are-hermetic.sh — upgrade-path-coverage-01PMUG01 WP05,
# spec.md FR-4b.
#
# THE CLASS THIS CATCHES. `rpc.New` (core/rpc/api.go) builds its
# settings store from `settings.NewFileStoreFromEnv()`, which resolves
# `os.UserConfigDir()` — the DEVELOPER'S REAL config directory — unless
# the test redirects HOME/XDG_CONFIG_HOME/AppData first. During the
# v0.63.1 work a test persisted `cedarStrictWorkflowMode: true` into
# that real file, and its `t.Fatal` skipped the restore step, which
# could have left the installed app permanently refusing shell-bearing
# workflows. FR-4a (the rpc.New settings-store injection option) fixes
# the seam; this gate is the RUNTIME check for the class, because "a
# test writes outside t.TempDir()" is not a static, grep-decidable
# question — a `os.Rename` inside `paths.MigrateLegacyConfigDir()`
# (sync.Once-guarded, un-resettable within one test binary) is exactly
# as real a violation as an explicit file write, and no amount of
# reading the test source tells you whether the write actually
# happened for a given input.
#
# MECHANISM. Point HOME / XDG_CONFIG_HOME / AppData at a throwaway
# sentinel directory seeded with a marker file (so "the sentinel
# directory didn't exist yet" and "the sentinel directory was
# genuinely untouched" are distinguishable), snapshot the sentinel
# tree (path + content hash of every file), run the scoped test suite
# with those env vars set, snapshot again, and fail on ANY difference
# — added, modified, renamed, or removed. A clean run proves nothing
# in the scope wrote outside its own t.TempDir() sandbox.
#
# SCOPE: ./core/rpc/... ./core/llm/personal/... ./core/paths/... —
# widening is fine; narrowing needs a dated justification in this
# file's history.
#
# Usage: bash scripts/ci/check-tests-are-hermetic.sh

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[tests-are-hermetic]"
SCOPE="./core/rpc/... ./core/llm/personal/... ./core/paths/..."

ci_require_dir "core/rpc" "$GATE"
ci_require_dir "core/llm/personal" "$GATE"
ci_require_dir "core/paths" "$GATE"

SENTINEL="$(mktemp -d)"
cleanup() {
  # Go's module cache ships read-only (0444) files by design, so a plain
  # `rm -rf` fails with "Permission denied" if anything ever lands under
  # the sentinel's $HOME/go/pkg/mod (see the GOPATH/GOMODCACHE/GOCACHE
  # pinning below for why that should no longer happen in the normal
  # case — this stays as a belt-and-braces cleanup for any other
  # read-only tree a future test might create under the sentinel).
  chmod -R u+w "$SENTINEL" 2>/dev/null || true
  rm -rf "$SENTINEL"
}
trap cleanup EXIT

mkdir -p "$SENTINEL/.config" "$SENTINEL/AppData"
echo "sentinel-marker-do-not-touch" > "$SENTINEL/.marker"
# Also seed a fake pre-existing settings.json under the real resolution
# path (~/Library/Application Support on macOS,
# $XDG_CONFIG_HOME/kenaz-harness on Linux, %AppData%/kenaz-harness on
# Windows) so a test that WOULD write there has something to clobber,
# the same way a real developer's machine does.
mkdir -p "$SENTINEL/.config/kenaz-harness" "$SENTINEL/Library/Application Support/kenaz-harness" "$SENTINEL/AppData/kenaz-harness"
echo '{"marker":true}' > "$SENTINEL/.config/kenaz-harness/settings.json"
echo '{"marker":true}' > "$SENTINEL/Library/Application Support/kenaz-harness/settings.json"
echo '{"marker":true}' > "$SENTINEL/AppData/kenaz-harness/settings.json"

snapshot() {
  # Prune */go/telemetry/* — the Go TOOLCHAIN's own opt-in-by-default
  # usage-counter directory (os.UserConfigDir()/go/telemetry, so
  # $SENTINEL/Library/Application Support/go/telemetry on macOS,
  # $SENTINEL/.config/go/telemetry on Linux, $SENTINEL/AppData/go/
  # telemetry on Windows once XDG_CONFIG_HOME/AppData are redirected
  # below). `go test` writes counter files here on every invocation
  # regardless of what the scoped suite's own code does, and neither
  # GOTELEMETRY=off nor GOTELEMETRYDIR as passthrough env vars change
  # that — `go help telemetry` documents both as "non-settable" go env
  # vars (confirmed empirically while writing this gate: pinning
  # GOPATH/GOMODCACHE/GOCACHE/GOENV to their real locations, as done
  # below, stops those from polluting the sentinel; telemetry ignores
  # the same treatment). Counting it as a violation would fail this
  # gate on every run, on every machine, independent of any real
  # hermeticity defect — exactly the "gate that cannot pass" class
  # CLAUDE.md's release-ritual doctrine calls a lie in the other
  # direction. It is the toolchain's own bookkeeping, not
  # kenaz-harness state, so it is out of this gate's concern.
  # .kenaz/harness.log is core/logging's process-lifetime log file:
  # logging.L() opens $HOME/.kenaz/harness.log on first use
  # (core/logging/logger.go initLogger) and there is no env override —
  # only the package-level configuredDir, set by logging.Configure.
  # core/rpc/testmain_test.go calls Configure for package core/rpc; the
  # ~50 core/rpc/views/* packages and core/llm/personal / core/paths do
  # not, so one of them opens it on every run of this gate.
  #
  # EXCLUDED AS A DELIBERATE CARVE-OUT, not because it is harmless.
  # Under the sentinel HOME the write lands inside the sentinel rather
  # than on the real machine, so its presence here is evidence the
  # redirect worked; but the same code writes the developer's real
  # ~/.kenaz/harness.log on an ORDINARY `go test ./core/...`, and this
  # gate does not and will not catch that. The real fix is a TestMain
  # calling logging.Configure(t.TempDir()) in each scoped package, or an
  # env override in core/logging. Until then this gate polices
  # CONFIG-FILE writes (settings.json, providers.json,
  # MigrateLegacyConfigDir's rename) and explicitly not the logger.
  #
  # Without this line the gate fails on EVERY run — it was wired into
  # pr.yml in that state (WP05), which would have turned lint-go red on
  # every PR. Found by running it, 2026-08-18.
  find "$SENTINEL" -type f -not -path '*/go/telemetry/*' -not -path '*/.kenaz/harness.log' -print0 | LC_ALL=C sort -z | while IFS= read -r -d '' f; do
    if command -v sha256sum >/dev/null 2>&1; then
      h=$(sha256sum "$f" | awk '{print $1}')
    else
      h=$(shasum -a 256 "$f" | awk '{print $1}')
    fi
    printf '%s  %s\n' "$h" "${f#"$SENTINEL"/}"
  done
}

# Pin Go's own state directories to their real, pre-existing locations
# BEFORE redirecting HOME below. GOPATH, GOMODCACHE, GOCACHE and GOENV
# all default to paths under $HOME (~/go, ~/go/pkg/mod,
# ~/Library/Caches/go-build, ~/Library/Application Support/go/env on
# macOS) when not set explicitly. Redirecting HOME without pinning these
# makes `go test` treat the empty sentinel as a brand-new GOPATH: it
# re-downloads the entire module graph (multi-minute, defeats the point
# of a CI gate) and, worse, leaves read-only module-cache files under
# the sentinel that the cleanup trap's `rm -rf` cannot remove. Resolving
# them now, while HOME is still real, keeps the test run using the
# developer's/CI's existing cache and keeps the sentinel tree containing
# ONLY what the scoped tests under HOME/XDG_CONFIG_HOME/AppData wrote.
REAL_GOPATH="$(go env GOPATH)"
REAL_GOMODCACHE="$(go env GOMODCACHE)"
REAL_GOCACHE="$(go env GOCACHE)"
REAL_GOENV="$(go env GOENV)"

BEFORE="$(snapshot)"

echo "${GATE} running scoped suite with HOME/XDG_CONFIG_HOME/AppData=${SENTINEL}..."
set +e
# -timeout=180s, well under go test's 10m default: CLAUDE.md's "Known
# flakes" list names views/sites keyring as a documented flake, and on
# a machine with no interactive Keychain/Secret-Service session
# core/fleet.SaveTokens's underlying zalando/go-keyring call blocks on
# a syscall wait that only the default per-binary timeout would end.
# This gate already treats a non-zero TEST_EXIT as "check hermeticity
# anyway, flag the failure separately" (below) — capping the wall time
# just reaches that same verdict in three minutes instead of ten,
# without narrowing SCOPE (every package below still runs; nothing is
# excluded).
HOME="$SENTINEL" XDG_CONFIG_HOME="$SENTINEL/.config" AppData="$SENTINEL/AppData" \
  GOPATH="$REAL_GOPATH" GOMODCACHE="$REAL_GOMODCACHE" GOCACHE="$REAL_GOCACHE" GOENV="$REAL_GOENV" \
  go test $SCOPE -count=1 -short -p 4 -timeout=180s >/tmp/hermetic-test-output.$$ 2>&1
TEST_EXIT=$?
set -e

AFTER="$(snapshot)"

if [[ "$TEST_EXIT" -ne 0 ]]; then
  echo "${GATE} the scoped test suite itself failed (exit ${TEST_EXIT}) — this gate" >&2
  echo "${GATE} still checks hermeticity below, but a real test failure needs its own fix." >&2
  cat /tmp/hermetic-test-output.$$ >&2
fi
rm -f /tmp/hermetic-test-output.$$

if [[ "$BEFORE" != "$AFTER" ]]; then
  echo "" >&2
  echo "${GATE} FAIL: the sentinel HOME/XDG_CONFIG_HOME/AppData tree changed during the test run." >&2
  echo "${GATE} A test in ${SCOPE} wrote outside its own t.TempDir() sandbox." >&2
  echo "${GATE} diff (sentinel-relative path, before -> after):" >&2
  diff <(printf '%s\n' "$BEFORE") <(printf '%s\n' "$AFTER") >&2 || true
  exit 1
fi

if [[ "$TEST_EXIT" -ne 0 ]]; then
  exit "$TEST_EXIT"
fi

echo "${GATE} clean — sentinel tree unchanged across the scoped suite."
