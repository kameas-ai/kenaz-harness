#!/usr/bin/env bash
# check-shipped-policy-matchable.sh — the G-4 gate from
# kitty-specs/trust-surfaces-that-fire-01PMZ202 (spec.md §G-4, WP18):
# "a shipped policy must be able to match."
#
# THE DEFECT CLASS THIS GATE CLOSES (F24)
# ------------------------------------------
# core/policy/cedar/policies/filesystem-full-recommended.cedar shipped,
# was offered by name in the recipe-install UI, and told the user "the
# model CANNOT touch your secrets, credentials, or .git internals
# through this policy" — while every forbid rule in it was structurally
# unmatchable, for THREE INDEPENDENT reasons at once: the action names
# it used (file_read/file_write) are evaluated by a different code path
# than the filesystem tooling the recipe is actually about
# (read_filesystem/write_filesystem); the resource type it declared
# (FilesystemOp) does not match the entity type that path's evaluator
# actually builds (Filesystem); and the context attribute every `when`
# clause read (canonical_path) is never populated for that path's
# action at all. Sensitive-path restrictions (SSH keys, AWS creds,
# .git internals, the harness's own keychain) silently never fired.
# Fixed by WP12 (retargeted the template onto read_filesystem/
# write_filesystem + FilesystemOp + canonical_path, the pair that
# actually carries that context). This gate is what keeps it fixed, and
# what catches the next one before it ships.
#
# For every `.cedar` file under core/policy/cedar/policies/, for every
# rule's action X, this gate asserts against the Go side:
#   (a) X has an evaluator (a non-test call site that both references
#       the Action* constant and reaches a .Evaluate(...) call).
#   (b) a production UID builder reachable from that evaluator emits
#       the rule's declared resource entity type.
#   (c) that evaluator populates every context key the rule's
#       when/unless clause reads.
# Each leg independently would have caught F24. See
# scripts/ci/cmd/checkpolicymatch/main.go's header for the full design,
# including why this needs function-level (not file-level) granularity
# — core/policy/cedar/hooks.go is one file containing two dozen
# unrelated action/resource/context triples, and a file-level check
# cannot tell "this action's OWN evaluator builds this resource type"
# from "some unrelated evaluator elsewhere in the same large file
# happens to" — and the one-hop delegation widening needed for the
# core/rpc/views/acp EngineAdapter.Check indirection (a real, correctly-
# wired shape this gate's own negative-control run surfaced and had to
# be widened to not misreport).
#
# DESIGN CHOICE: constants vs. call sites. types.go's Action*/
# EntityType* constants are the CHEAP, COMPLETE universe for leg (a)'s
# "does this action string exist at all" question — there is no dynamic
# action-string construction anywhere in this codebase. Legs (b)/(c) are
# fundamentally about what a specific evaluator CALL SITE does (builds
# which resource type, populates which context keys), which constants
# alone cannot answer — F24's own action strings (file_read/file_write)
# were both valid, existing constants; the defect was that the policy
# named the wrong one for its tooling. This gate therefore checks the
# action universe against constants and checks legs (b)/(c) against
# real call-site behaviour, parsed via go/parser (no type-checking
# needed — this is a textual co-occurrence question within a known
# function body).
#
# Backed by scripts/ci/cmd/checkpolicymatch, using the REAL cedar-go
# parser (github.com/cedar-policy/cedar-go, already a module dependency
# — the same library core/policy/cedar/engine.go uses to load these
# exact files in production) to decode each policy into Cedar's
# documented JSON policy format, rather than a hand-rolled regex parser
# for the .cedar text itself. Self-hosted-runner-safe: `go build` is the
# only tool needed, no apt-get.
#
# ALLOWLIST: scripts/ci/allowlists/i-shipped-policy-matchable.txt,
# deliberately kept minimal — see that file's header and main.go's "A
# DELIBERATELY TINY ALLOWLIST" section for why an unmatchable SHIPPED
# policy does not get the same casual allowlist treatment as, say, an
# orphaned functional option. It is seeded with exactly one
# already-investigated, real, pre-existing gap this gate's own
# introduction surfaced (default_acp_policy.cedar's acp_receive rules —
# no ACP_Receive Cedar gate call exists in production at all), not a
# general license to defer future violations.
#
# Exit codes:
#   0 — every shipped policy rule's action/resource/context triple is matchable, or allowlisted
#   1 — the checker itself failed to build, parse a .cedar/.go file, or read the allowlist
#       (this includes the discovery-floor guard: zero .cedar files, zero Action*/
#       EntityType* constants, zero *UID builders, or zero rule statements all fail
#       loudly here rather than reporting a vacuous "clean" — main.go's run())
#   2 — at least one unmatchable rule is unlisted, or the allowlist is stale
#
# Usage: bash scripts/ci/check-shipped-policy-matchable.sh (from anywhere).

set -euo pipefail

WORKTREE_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$WORKTREE_ROOT"

BIN="$(mktemp -d)/checkpolicymatch"
trap 'rm -rf "$(dirname "$BIN")"' EXIT

echo "[shipped-policy-matchable] building checker..."
if ! go build -o "$BIN" ./scripts/ci/cmd/checkpolicymatch/; then
  echo "[shipped-policy-matchable] FAIL: checkpolicymatch failed to build." >&2
  exit 1
fi

echo "[shipped-policy-matchable] checking core/policy/cedar/policies/*.cedar against the engine's real action/entity/context surface..."
"$BIN"
