#!/usr/bin/env bash
# check-no-credential-in-fleet-logs.sh — provider-credentials-client-01PCCLI01
# B1 / WP04: code in the Fleet credential namespaces never logs a credential
# value, a keychain locator, or a raw secret-typed variable.
#
# THE DEFECT CLASS
# ----------------
# Org/team keys and synced user keys are plaintext provider keys held in
# the OS keychain (kenaz-harness/fleet/<env>/{org,user}/<credential_id>).
# The spec allows exactly one fingerprint anywhere outside the keychain:
# the audit row the Fleet contract defines (id, sha256: prefix, node_id).
# A log line is not that row. One `log.Debug("stored", "value", string(b))`
# — or a locator, a UCK, a uck_auth (spec §10.4: never logged on either
# side), a pairing nonce — ships the secret into ~/.kenaz/harness.log, the
# in-app Logs ring and, once log export is opted into, Fleet.
#
# SCOPE (the "credential namespaces")
# -----------------------------------
# Every non-test .go file under core/ that is either
#   - core/secrets/fleet_credentials*.go          (the namespace store)
#   - core/llm/credref/*.go                       (resolution by Source)
#   - core/fleet/credential*.go, core/fleet/credentials/**  (WP05/WP06)
#   - or ANY file that names FleetCredentialStore, FleetCredentialLocator,
#     FleetNamespaceOrg, FleetNamespaceUser, FleetPrincipal or the
#     principal purge/reconcile functions — so a new caller is in scope
#     the moment it touches the namespace, with no edit to this script.
# The scan refuses to pass on an empty or implausibly small scope.
#
# WHAT FAILS
# ----------
# The checker is an AST pass, scripts/ci/cmd/checkfleetcredlogs — see its
# header for the full sink and finding list. In short: any log sink
# (<recv>.{Debug,Info,Warn,Error}[Context], including the repo's dominant
# logging.L().Info(…) chain; logger.With(…); slog.*; log.Print*; fmt.Print*;
# a fmt.Sprintf result that reaches one) whose arguments carry
#   (a) a forbidden attribute key ("value", "plaintext", "key", "token",
#       "uck_auth", "nonce", …);
#   (b) a KEY-SHAPED literal — matched with the regexes the checker reads
#       out of core/fleet/secretshape.go (secretValuePatterns +
#       apiKeyShapeRegex), so this gate and the sync backstop share one
#       definition of "looks like a key";
#   (c) a secret-named identifier or field (plaintext, pt, secret, value,
#       key, apiKey, token, locator, …, or *Key/*Token/*Secret/*Password);
#   (d) string(…), .Bytes() or .Use(.
# Opt-out: a `cred-log-allow: <reason>` comment on a line the call spans.
#
# HISTORY: the first version (2026-10-15) was a perl regex over
# `ident.Info(`. Review planted ten violations — logging.L().Info, apiKey /
# key / pt / token identifiers, a key-shaped message, Sprintf-built
# messages, logger.With — and it caught none. The AST pass replaced it.
#
# Planted-violation proofs: scripts/ci/gates_can_fail_test.go cases
# "fleet-cred-logs/*" (one per shape) and
# TestFleetCredLogsGate_BenignLogsPass (negative controls).
#
# Usage: bash scripts/ci/check-no-credential-in-fleet-logs.sh (from anywhere).

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[fleet-cred-logs]"
ci_require_dir core "$GATE"

BIN="$(mktemp -d)/checkfleetcredlogs"
trap 'rm -rf "$(dirname "$BIN")"' EXIT
if ! go build -o "$BIN" ./scripts/ci/cmd/checkfleetcredlogs/; then
  echo "$GATE FAIL: checkfleetcredlogs failed to build." >&2
  exit 1
fi
"$BIN"
