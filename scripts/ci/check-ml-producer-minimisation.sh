#!/usr/bin/env bash
# check-ml-producer-minimisation.sh — ml-producer-01MLPRD01 WP04 (spec §7,
# §12 A-10): fail the build when the harness ML producer could ship
# anything the contract does not allow.
#
# Delegates to scripts/ci/cmd/checkmlminimisation, which runs two halves:
#
#   static   mlproducer.KindTable must be EXACTLY the six A-10 kinds
#            (file, terminal, commit, phase_change, agent.tool, agent.turn)
#            with exactly their payload keys, and no string literal in an
#            event-kind position under core/mlproducer may name any other
#            kind (a daemon-only kind such as `hyprland`, or an invented one).
#   dynamic  the REAL recorder, over a temp sqlite database with a fixed
#            hash key, is fed a fixture corpus of tool calls (deep paths,
#            long commands with secrets/env/URLs, a custom MCP server, git
#            commit, tests, refused/denied calls) and every outbox record is
#            checked: kind and payload keys against the contract, no raw
#            path, cmd <= 2 tokens, no credential-looking token, no fixture
#            canary verbatim, task files keys h+ext, repo_root hashed.
#
# Planted-violation proofs: scripts/ci/gates_can_fail_test.go
# (TestMLMinimisationGate_*). See the tool's package doc for exit codes.
set -euo pipefail
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

ci_require_dir core/mlproducer "[ml-minimisation]"
ci_require_file core/mlproducer/record.go "[ml-minimisation]"
ci_require_file core/mlproducer/recorder.go "[ml-minimisation]"

go run ./scripts/ci/cmd/checkmlminimisation
