#!/usr/bin/env bash
# check-bsd-gnu-escape-divergence.sh — Finding #96 (CI-gate-hardening,
# 2026-09-14): a META-GATE that fails when any scripts/ci/check-*.sh
# passes a BSD/GNU-divergent whitespace escape (`\t`, `\n`, `\r`) to
# grep or sed in a PATTERN or REPLACEMENT position, spelled as the
# ordinary two-character backslash-letter sequence inside a plain `'...'`
# or `"..."` shell string instead of a real byte via ANSI-C quoting
# ($'...').
#
# THE DEFECT CLASS
# -----------------
# Gates in this directory are written and tested on macOS (BSD
# userland) and enforced on Linux self-hosted CI runners (GNU
# userland). Two confirmed instances, in OPPOSITE failure directions:
#
#   1. check-transport-parity.sh:92 (ALREADY FIXED, the reference case):
#      `grep -oE '^\tcase "[a-z]+":'` — BSD grep matches a real TAB
#      inside a single-quoted `\t`; GNU grep treats it as the literal
#      two characters (an escaped ordinary "t"), so `^\tcase` becomes
#      `^tcase` and matches NOTHING on CI. The gate passed locally and
#      hit its own discovery floor on CI — never silently vacuous, but
#      only because the floor caught it. Fixed with `$'^\tcase ...'`.
#
#   2. check-cedar-gate-arguments.sh's clause-4 nested-call-permit sed
#      script used to read
#      `sed -E "s#.*\(([A-Za-z0-9_.]+)\($#\1#"` — investigated as part
#      of this same finding. That turned out NOT to be a BSD-vs-GNU
#      divergence at all: the `\($#\1#` sequence sits inside a
#      DOUBLE-quoted bash string with no `${var}` in it, and bash's
#      OWN special parameter `$#` (positional-argument count, 0 in
#      this script) silently mangled the sed script before either sed
#      flavor ever parsed it — a bash quoting bug that fails
#      IDENTICALLY on both platforms, with different diagnostic text.
#      Fixed by single-quoting the (variable-free) script, which also
#      forecloses the whole class of accidental $#/$@/$?/$!/$$/$0-9/$*
#      collisions for any future edit. See that script's own inline
#      comment at the fix site for the full account, including a THIRD,
#      independent bug this investigation found in the same clause (an
#      awk pattern using an incorrect single-backslash escape for a
#      literal paren, silently rejected by this platform's awk and
#      masked by `2>/dev/null` — not in this meta-gate's charter, since
#      it is an awk-string-escape defect, not a grep/sed BSD/GNU
#      divergence, but fixed alongside for the same underlying reason).
#
# A point-in-time sweep for instance-shaped bugs decays the moment
# someone writes a new gate. This meta-gate makes the CLASS impossible
# to reintroduce silently: it does not itself know which escapes
# diverge on which platform (that would require running both grep
# flavors, which CI cannot do — the whole reason this class is hard to
# catch locally) — it enforces the STRUCTURAL rule that fixed both
# confirmed instances: a grep/sed pattern or replacement containing a
# whitespace escape must carry a REAL byte (ANSI-C `$'...'` quoting),
# never the two-character backslash-letter spelling, because that
# spelling's meaning is not portably defined.
#
# SCOPE, DELIBERATELY NARROW
# ---------------------------
# Only `\t` / `\n` / `\r` — the whitespace-representing escapes, which
# is exactly instance 1's shape and the general class it belongs to.
# NOT `\s` (a character-CLASS shorthand, not a literal-byte escape):
# six existing gates use `\s` in a grep -E pattern
# (check-input-kind-coverage.sh:115, check-no-credential-in-ui.sh:
# 134,144,152, check-output-ports.sh:74, check-test-only-symbols.sh:31)
# and were individually verified, ahead of this gate's introduction, to
# agree between BSD and GNU grep in this environment (both treat `\s`
# as the documented GNU extension for a whitespace class). Flagging
# `\s` here would force six spurious allowlist entries for a construct
# that is not actually divergent — the exact "allowlist entries that
# don't correspond to a real defect" corrosion Finding #92 names.
#
# WHAT THIS GATE CANNOT SEE
# ----------------------------
#  1. Scoped to `scripts/ci/check-*.sh` only, one PHYSICAL LINE at a
#     time (via a `|`-pipeline-segment split, so a `grep`/`sed`
#     invocation's own segment is checked in isolation from an
#     unrelated `printf '...\n'` elsewhere on the same line). A
#     multi-line sed/awk script (single quotes spanning several
#     physical lines) is invisible to this per-line scan — no gate in
#     this directory currently uses that shape, so this is documented
#     as a known hole rather than fixed speculatively.
#  2. Cannot see a grep/sed invocation built entirely from shell
#     variables (`grep -E "$pattern"` where $pattern is assigned
#     elsewhere) — the escape sequence must appear as a literal in the
#     invocation's own line to be visible to a textual scan. This
#     mirrors the "shell gate cannot do full parsing" caveat every
#     sibling gate in this directory already carries.
#  3. Other backslash-letter escapes that MIGHT diverge (`\b`, `\w`,
#     `\d`, `\<`, `\>`, `\+`, `\|`, `\?`) are deliberately NOT flagged —
#     unlike `\t`/`\n`/`\r`, this repository has no CONFIRMED instance
#     of any of them actually diverging in practice, and guessing at a
#     wider set risks the same false-positive corrosion `\s` would have
#     caused. Widening this set is legitimate future work IF a real
#     instance is found — see Finding #96's own report for the
#     precedent this gate should follow.
#
# Violations must appear in
# scripts/ci/allowlists/bsd-gnu-escape-divergence.txt with a DATED
# justification naming the blocker and owner. Keyed by file + the
# offending pipeline segment's own trimmed text (NOT file:line — see
# Finding #92 and i16-config-nil-coverage.txt's header for why a line
# number self-invalidates on an unrelated edit above the entry).
# Allowlists shrink monotonically.
#
# Exit codes:
#   0 — no check-*.sh passes \t/\n/\r to grep/sed outside ANSI-C quoting,
#       or every occurrence is allowlisted
#   1 — no scripts/ci/check-*.sh files found at all (discovery floor)
#   2 — at least one unlisted occurrence, or the allowlist is stale
#
# Usage: bash scripts/ci/check-bsd-gnu-escape-divergence.sh (from anywhere)

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[bsd-gnu-escape-divergence]"
SELF="check-bsd-gnu-escape-divergence.sh"
SCAN_DIR="scripts/ci"
ALLOW_FILE="scripts/ci/allowlists/bsd-gnu-escape-divergence.txt"

ci_require_dir "$SCAN_DIR" "$GATE"
ci_require_file "$ALLOW_FILE" "$GATE"

load_allowlist() {
  grep -vE '^[[:space:]]*(#|$)' "$1" || true
}

mapfile -t SCAN_FILES < <(find "$SCAN_DIR" -maxdepth 1 -name 'check-*.sh' ! -name "$SELF" | sort)

# Discovery floor: this repo is known to have 50+ check-*.sh gates. If
# the glob finds none, the scan is broken (wrong cwd resolution, a
# renamed directory), not a repository that stopped shipping gates.
if [[ "${#SCAN_FILES[@]}" -eq 0 ]]; then
  echo "${GATE} FAIL: found zero scripts/ci/check-*.sh files (excluding this gate itself)." >&2
  echo "${GATE} This repository is known to ship 50+ such gates — a scan finding none is" >&2
  echo "${GATE} almost certainly a broken SCAN_DIR resolution, not an empty directory." >&2
  exit 1
fi

# ---------------------------------------------------------------------------
# The scan itself: for each file, for each line, drop any ANSI-C quoted
# ($'...') regions first (their \t/\n/\r is a real, portably-interpreted
# byte — safe by construction), then split the remaining line on `|`
# into pipeline segments and flag any segment that (a) invokes grep or
# sed as its own command and (b) still contains a literal `\t`, `\n`,
# or `\r` — i.e. one that survived outside an ANSI-C quoted region.
# `printf`-format segments are excluded (see the header's "WHAT THIS
# GATE CANNOT SEE" note — the false-positive this excludes is a `\n`
# from an unrelated `printf '%s\n'` nested via command substitution
# inside a LATER pipeline segment on the same line, e.g. an `echo`
# summary line that reports a grep -c count).
# ---------------------------------------------------------------------------
violations=""
scanned_lines=0

for f in "${SCAN_FILES[@]}"; do
  while IFS= read -r hit; do
    [[ -z "$hit" ]] && continue
    seg="${hit#*:}"
    trimmed="$(printf '%s' "$seg" | sed -E 's/^[[:space:]]+|[[:space:]]+$//g')"
    violations="${violations}${f}: grep/sed segment carries a literal whitespace escape outside ANSI-C quoting: \`${trimmed}\`"$'\n'
  done < <(
    awk '
      {
        line = $0
        gsub(/\$\x27[^\x27]*\x27/, "", line)
        n = split(line, segs, "|")
        for (i = 1; i <= n; i++) {
          seg = segs[i]
          if (seg ~ /^[[:space:]]*(grep|sed)([[:space:]]|$)/) {
            if (seg ~ /\\[tnr]/ && seg !~ /printf/) {
              print FILENAME ":" seg
            }
          }
        }
      }
    ' "$f"
  )
  scanned_lines=$((scanned_lines + $(grep -cE '(grep|sed)' "$f" 2>/dev/null || true)))
done

# Second discovery-floor guard: grep/sed themselves must be a heavily
# used idiom across this directory (every gate uses at least one). If
# the per-file grep/sed line count sums to zero, the scan mechanism
# itself is broken (e.g. find returned files this shell cannot read).
if [[ "$scanned_lines" -eq 0 ]]; then
  echo "${GATE} FAIL: scanned ${#SCAN_FILES[@]} file(s) but found zero lines mentioning grep or sed —" >&2
  echo "${GATE} every gate in this directory uses at least one; this is almost certainly a" >&2
  echo "${GATE} broken scan, not a directory of gates that stopped using either tool." >&2
  exit 1
fi

violations=$(printf '%s' "$violations" | grep -v '^$' | sort -u || true)
allow=$(load_allowlist "$ALLOW_FILE")

unlisted=$(comm -23 <(printf '%s\n' "$violations" | sort -u) <(printf '%s\n' "$allow" | sort -u) | grep -v '^$' || true)
stale=$(comm -13 <(printf '%s\n' "$violations" | sort -u) <(printf '%s\n' "$allow" | sort -u) | grep -v '^$' || true)

fail=0
if [[ -n "$unlisted" ]]; then
  echo "" >&2
  echo "${GATE} FAIL: grep/sed pattern or replacement carries \\t/\\n/\\r outside ANSI-C quoting," >&2
  echo "${GATE} not in ${ALLOW_FILE}:" >&2
  printf '%s\n' "$unlisted" | sed 's/^/    /' >&2
  echo "" >&2
  echo "${GATE} A two-character backslash-letter whitespace escape inside a plain '...' or" >&2
  echo "${GATE} \"...\" string has no portably-defined meaning to grep/sed across BSD and GNU" >&2
  echo "${GATE} userlands. Rewrite using ANSI-C quoting (\$'...') so the pattern/replacement" >&2
  echo "${GATE} carries a REAL byte instead — see check-transport-parity.sh:92's fix for the" >&2
  echo "${GATE} canonical form — or add a DATED justification to ${ALLOW_FILE} naming the" >&2
  echo "${GATE} blocker and owner." >&2
  fail=1
fi
if [[ -n "$stale" ]]; then
  echo "" >&2
  echo "${GATE} FAIL: STALE entries in ${ALLOW_FILE} — no longer a violation:" >&2
  printf '%s\n' "$stale" | sed 's/^/    /' >&2
  echo "${GATE} Delete the line(s) — allowlists shrink monotonically." >&2
  fail=1
fi

if [[ "$fail" -ne 0 ]]; then
  exit 2
fi

echo "${GATE} clean — scanned ${#SCAN_FILES[@]} check-*.sh file(s), ${scanned_lines} grep/sed-bearing line(s): no unquoted whitespace escapes found in a grep/sed pattern or replacement position."
