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
#     FleetNamespaceOrg or FleetNamespaceUser — so a new caller is in scope
#     the moment it touches the namespace, with no edit to this script.
# The scan refuses to pass on an empty or implausibly small scope.
#
# WHAT FAILS
# ----------
# Inside any log call — <recv>.{Debug,Info,Warn,Error}[Context](…),
# slog.Log / slog.LogAttrs, log.Print*/Fatal*/Panic*, fmt.Print*/Fprint* —
# matched across lines (balanced parentheses, so a multi-line call is not
# invisible the way it is to the one-line slog-privacy gate):
#   (a) a forbidden attribute key: "value", "plaintext", "secret", "key",
#       "api_key", "apikey", "locator", "cred_locator", "uck", "uck_auth",
#       "uck_auth_hash", "ucek", "kek", "seed", "nonce", "sealed",
#       "na", "nb" (case-insensitive);
#   (b) a raw-secret identifier as an argument: plaintext, secret,
#       credBytes, value, buf, locator, loc, uck, uckAuth, ucek, kek,
#       seed, nonce, sealed;
#   (c) a bytes-to-text conversion or borrowed view: string(…), .Bytes(),
#       .Use(.
# Anywhere in a scoped file: (d) a typed slog attribute with a forbidden
# key — slog.String("value", …) etc. — since that is how a multi-line call
# builds its attrs out of line.
# Opt-out: a `cred-log-allow: <reason>` comment inside the call.
#
# Planted-violation proofs: scripts/ci/gates_can_fail_test.go cases
# "fleet-cred-logs/*" (a one-line value log, a multi-line locator log, a
# typed attribute, a new file that only names FleetCredentialStore).
#
# Usage: bash scripts/ci/check-no-credential-in-fleet-logs.sh (from anywhere).

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[fleet-cred-logs]"
ci_require_dir core "$GATE"

scope=$(
  {
    find core/secrets -maxdepth 1 -type f -name 'fleet_credentials*.go' ! -name '*_test.go' 2>/dev/null || true
    find core/llm/credref -type f -name '*.go' ! -name '*_test.go' 2>/dev/null || true
    find core/fleet -maxdepth 1 -type f -name 'credential*.go' ! -name '*_test.go' 2>/dev/null || true
    if [[ -d core/fleet/credentials ]]; then
      find core/fleet/credentials -type f -name '*.go' ! -name '*_test.go'
    fi
    grep -rlE '\b(FleetCredentialStore|FleetCredentialLocator|FleetNamespaceOrg|FleetNamespaceUser)\b' \
      --include='*.go' --exclude='*_test.go' core 2>/dev/null || true
  } | sort -u
)

nfiles=$(printf '%s\n' "$scope" | grep -c . || true)
if [[ "$nfiles" -lt 2 ]]; then
  echo "$GATE FAIL: scope resolved to $nfiles file(s); expected at least core/secrets/fleet_credentials.go and core/llm/credref/credref.go." >&2
  echo "$GATE A gate with nothing in scope passes vacuously — refusing." >&2
  exit 1
fi

# The scanner: perl, so a call can be matched with balanced parentheses
# across lines. Prints "<file>:<line>: <reason>: <call excerpt>" per hit.
# shellcheck disable=SC2016
scanner='
use strict; use warnings;
my $keys = qr/(?i:value|plaintext|secret|key|api_key|apikey|locator|cred_locator|uck|uck_auth|uck_auth_hash|ucek|kek|seed|nonce|sealed|na|nb)/;
my $idents = qr/\b(?:plaintext|secret|credBytes|value|buf|locator|loc|uck|uckAuth|ucek|kek|seed|nonce|sealed)\b/;
my $call = qr/
  (?: \b[A-Za-z_][A-Za-z0-9_]*\.(?:Debug|Info|Warn|Error)(?:Context)?
    | \bslog\.(?:Log|LogAttrs)
    | \blog\.(?:Print|Printf|Println|Fatal|Fatalf|Fatalln|Panic|Panicf|Panicln)
    | \bfmt\.(?:Print|Printf|Println|Fprint|Fprintf|Fprintln)
  )
  (\((?:[^()"`]++|"(?:[^"\\]|\\.)*+"|`[^`]*+`|(?1))*+\))
/xs;
my $fail = 0;
for my $f (@ARGV) {
  open(my $fh, "<", $f) or die "$f: $!";
  local $/; my $src = <$fh>; close $fh;
  # A call that sits after // on its line is prose, not code.
  while ($src =~ /$call/g) {
    my $text = $&; my $args = $1;
    my $start = $-[0];
    my $line = 1 + (substr($src, 0, $start) =~ tr/\n//);
    next if $text =~ /cred-log-allow:/;
    my $prefix = substr($src, rindex($src, "\n", $start) + 1, $start - rindex($src, "\n", $start) - 1);
    next if $prefix =~ m{//};
    my $inner = substr($args, 1, -1);
    # Drop the first argument when it is the message literal (or ctx then
    # message), so the message text itself is never scanned for keys.
    (my $scan = $inner) =~ s/^\s*(?:ctx\s*,\s*)?(?:[A-Za-z_.]*Level[A-Za-z]*\s*,\s*)?"(?:[^"\\]|\\.)*"//s;
    my @why;
    while ($scan =~ /"($keys)"/g) { push @why, "forbidden attribute key \"$1\""; }
    # Identifiers and conversions are code, so scan with string literals
    # removed: "value" as a key is (a); value as an argument is (b).
    (my $code = $scan) =~ s/"(?:[^"\\]|\\.)*"|`[^`]*`//g;
    while ($code =~ /($idents)/g) { push @why, "raw-secret identifier $1"; }
    push @why, "bytes-to-text conversion string(...)" if $code =~ /\bstring\s*\(/;
    push @why, "borrowed secret view .Bytes()/.Use(" if $code =~ /\.(?:Bytes\s*\(\s*\)|Use\s*\()/;
    next unless @why;
    (my $excerpt = $text) =~ s/\s+/ /g;
    $excerpt = substr($excerpt, 0, 160);
    my %seen; @why = grep { !$seen{$_}++ } @why;
    print "$f:$line: " . join("; ", @why) . ": $excerpt\n";
    $fail = 1;
  }
  while ($src =~ /\bslog\.(?:String|Any|Int|Int64|Uint64|Float64|Bool|Time|Duration|Group|Attr)\(\s*"($keys)"/g) {
    my $line = 1 + (substr($src, 0, $-[0]) =~ tr/\n//);
    my $eol = index($src, "\n", $-[0]);
    my $rest = substr($src, $-[0], ($eol < 0 ? length($src) : $eol) - $-[0]);
    next if $rest =~ /cred-log-allow:/;
    print "$f:$line: typed slog attribute with forbidden key \"$1\"\n";
    $fail = 1;
  }
}
exit $fail;
'

files=()
while IFS= read -r f; do
  [[ -n "$f" ]] && files+=("$f")
done <<< "$scope"
if ! out=$(perl -e "$scanner" "${files[@]}"); then
  while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    echo "$GATE FAIL: $line" >&2
  done <<< "$out"
  echo "$GATE Fleet credential code must not log key material, locators or secret-typed values." >&2
  echo "$GATE Log the credential id only, or annotate the call with 'cred-log-allow: <reason>'." >&2
  exit 1
fi

echo "$GATE clean — $nfiles file(s) in the credential namespaces, no credential reaches a log call."
