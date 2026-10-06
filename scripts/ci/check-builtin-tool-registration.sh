#!/usr/bin/env bash
# check-builtin-tool-registration.sh — I11: every implemented builtin tool
# is reachable from the model's catalog, or is allowlisted with a dated
# justification.
#
# THE DEFECT CLASS (unwired sweep, 2026-08-14)
# --------------------------------------------
# `core/tools/monitor` is a complete, tested, 469-line builtin tool
# (`kenaz__monitor`) with its own Cedar action constant, shipped in
# v0.11.0 and registered NOWHERE ever since. Nothing imports the package
# outside its own test file. No model has ever been able to call it.
#
# No existing gate could see this:
#   - I7 (orphan packages) catches it only as one line in a 36-line bulk
#     list of "packages with no non-test importer", most of which are
#     legitimate test-support libraries. A tool the model is supposed to
#     be able to CALL is a different, louder problem than an unimported
#     helper, and it was buried.
#   - The builtin-tools tripwire (core/rpc/builtins_wiring_test.go
#     TestBuiltinEnabledPredicate_AllRegisteredToolsHaveExplicitCase)
#     iterates `Builtins().Names()` — the REGISTERED set. A tool that
#     never registers is invisible to it by construction; it can only
#     catch registered-but-no-predicate-case.
#
# So: a whole tool can be built, tested, gated, documented and shipped
# without one line of CI noticing that no model can reach it.
#
# SCOPE EXTENSION (mcp-connector-lifecycle-01PMMC01 WP05, FR-008/AC-009)
# -----------------------------------------------------------------------
# The original scan pinned TOOLS_ROOT="core/tools" — a single root. The
# harness-self MCP server (core/mcp/builtin/harness, thirteen tools
# registered in register.go) sat in the SAME defect class the whole time
# — a package the model can never reach — and this gate could not see it,
# because it only ever looked under core/tools/. core/mcp/builtin/'s
# packages (sites, harness) don't share core/tools/'s naming convention
# (kenaz__-prefixed Name/ToolName consts) or its single wiring file
# (core/rpc/builtins_wiring.go — neither package is imported there; they
# reach the model through their own transports: sites via a spawned
# cmd/mcpsubcmd subprocess, harness via an in-process MCP transport
# attach that is B10's own open question, tracked separately in
# research/b10-harness-self-decision.md and NOT resolved by this gate).
#
# So core/mcp/builtin/ gets a SEPARATE, coarser check below (§5): every
# immediate subdirectory other than toolserver/ (a shared helper library,
# not a tool-server package) must be imported by at least one non-test
# .go file somewhere outside itself. This is package-level, not
# name-level — it does not care how a package advertises its tools to a
# model, only whether anything outside it ever reaches in. That is
# deliberately coarser than the core/tools/ check (which additionally
# requires the ONE canonical wiring file specifically); core/mcp/builtin/
# has no single canonical wiring file to require, and requiring one would
# force every one of harness's already-registered-but-unattached tool
# names into this allowlist as a side effect of a CI-script change,
# pre-empting the harness-self product decision this check must not make.
#
# WHAT THIS CHECKS (core/tools/, unchanged)
# ------------------------------------------
# A package under core/tools/ that declares a builtin tool name — a
# `Name`/`ToolName`/`<X>ToolName` const whose value starts with `kenaz__`
# — must be imported by core/rpc/builtins_wiring.go, the single wiring
# site for the builtin registry (core/rpc/api.go calls
# registerBuiltinTools / registerFSBuiltinTools / registerFSRequestTool /
# registerReadContextFileTool and nothing else registers).
#
# Import, not registration, is the bar on purpose. Several tools register
# conditionally and SHOULD (`save_artifact` needs an artifacts manager;
# `subagent_dispatch` is deliberately withheld while its BranchSeam
# cannot spawn a child run — crash-recovery-tool-gating-0XQTC4RK FR-007).
# Requiring the wiring file to at least *know about* the package catches
# the "nobody ever hooked this up" case without arguing with every
# deliberate conditional. A package that is imported but whose
# registration is dead code is a code-review question, not a grep one.
#
# Violations from EITHER check land in the same allowlist,
# scripts/ci/allowlists/i11-unregistered-builtin-tools.txt, with a dated
# justification naming the blocker — same contract as the I10 allowlist:
# a line that stays must say why it stays, and lines shrink monotonically.
# An entry naming a package that no longer exists is fine (deletion is a
# valid resolution). An entry naming a package that IS now imported is
# STALE and fails.
#
# SCOPE EXTENSION (model-harness-toolset-01MHTS001 WP03, finding H-2)
# ------------------------------------------------------------------
# §6 below: a harness-self tool whose handler nil-checks a Managers field
# (`if m.X == nil { return nil, errNotConfigured }`) that NOTHING ever
# assigns. harness_read_get_status and harness_write_install_mcp_recipe
# shipped exactly like that — registered, visible to the model (get_status
# in every chat session), and failing "not configured" on every call,
# because core/rpc/harness_wiring.go's buildHarnessManagers never set
# Managers.Status / Managers.RecipesWriter. §5's package-level import check
# cannot see it (the package IS imported), and the onboarding-starter gate
# only checks names. §6 requires every Managers field a handler nil-checks
# to have at least one `m.X = ` assignment in the wiring file. Assignment,
# not unconditional assignment: a field set only when its backing API is
# non-nil is a deliberate degraded-boot posture, not a never-wired one.
# Violations are reported as `harness-self-manager:<Field>` and use the
# same dated allowlist.
#
# Usage: bash scripts/ci/check-builtin-tool-registration.sh (from anywhere).

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[builtin-tool-registration]"
TOOLS_ROOT="core/tools"
# BUILTIN_MCP_ROOT is the second root FR-008 added. Kept as its own named
# variable rather than folded into TOOLS_ROOT — the two roots use
# different discovery + wiring-site logic (see §5), so silently
# repointing TOOLS_ROOT would have quietly dropped core/tools/ coverage.
BUILTIN_MCP_ROOT="core/mcp/builtin"
# toolserver is a shared helper library BOTH sites/ and harness/ import
# to build their tool-server loops — it declares no tools of its own and
# is not itself a "package the model should be able to reach".
BUILTIN_MCP_ROOT_EXCLUDE="toolserver"
WIRING_FILE="core/rpc/builtins_wiring.go"
ALLOW_FILE="scripts/ci/allowlists/i11-unregistered-builtin-tools.txt"
# §6: harness-self handler managers vs. their one assignment site.
HARNESS_SELF_PKG="core/mcp/builtin/harness"
HARNESS_WIRING_FILE="core/rpc/harness_wiring.go"

ci_require_dir "$TOOLS_ROOT" "$GATE"
ci_require_dir "$BUILTIN_MCP_ROOT" "$GATE"
ci_require_file "$WIRING_FILE" "$GATE"
ci_require_file "$ALLOW_FILE" "$GATE"
ci_require_dir "$HARNESS_SELF_PKG" "$GATE"
ci_require_file "$HARNESS_WIRING_FILE" "$GATE"

fail=0

load_allowlist() {
  grep -vE '^[[:space:]]*(#|$)' "$1" || true
}

# ---- 1. discover tool-declaring packages under core/tools/ ----
# A declaration looks like `Name = "kenaz__x"` or `ToolName = "kenaz__x"`
# or `EnterToolName = "kenaz__x"`, with or without a leading `const`.
decls=$(grep -rnE '^[[:space:]]*(const[[:space:]]+)?[A-Za-z0-9_]*(Name|ToolName)[[:space:]]*=[[:space:]]*"kenaz__' \
  --include='*.go' "$TOOLS_ROOT" 2>/dev/null | grep -v '_test\.go' || true)

if [[ -z "$decls" ]]; then
  echo "${GATE} FAIL: found no kenaz__ tool-name declarations under ${TOOLS_ROOT}." >&2
  echo "${GATE} A gate cannot pass by having nothing to look at — the naming convention" >&2
  echo "${GATE} this scan depends on has changed. Update the pattern in the same commit." >&2
  exit 1
fi

pkgs=$(printf '%s\n' "$decls" | cut -d: -f1 | xargs -n1 dirname | sort -u)

# ---- 2. which of them the wiring file imports ----
violations=""
while IFS= read -r pkg; do
  [[ -z "$pkg" ]] && continue
  if ! grep -qE "\"github\.com/kameas-ai/kenaz-harness/${pkg}\"" "$WIRING_FILE"; then
    violations="${violations}${pkg}"$'\n'
  fi
done <<< "$pkgs"

# ---- 5. core/mcp/builtin/: package-level import check (FR-008) ----
# Every immediate subdirectory other than BUILTIN_MCP_ROOT_EXCLUDE must be
# imported by at least one non-test .go file outside itself, anywhere in
# the module. "Anywhere", not "the wiring file" — see the header comment
# for why core/tools/'s single-wiring-file bar does not transfer here.
mcp_builtin_pkgs=$(find "$BUILTIN_MCP_ROOT" -mindepth 1 -maxdepth 1 -type d \
  ! -name "$BUILTIN_MCP_ROOT_EXCLUDE" | sort)

# Discovery floor (Finding #74, CI-gate-hardening, 2026-09-14): unlike
# the `decls` scan above (already floored at line 119), this candidate
# set had no check that it found anything before feeding the
# import-wiring loop below. A renamed BUILTIN_MCP_ROOT, a moved
# exclude-directory convention, or every subpackage happening to share
# the excluded name would make mcp_builtin_pkgs empty — and a `while
# read` over an empty string silently produces zero FR-008 violations,
# indistinguishable from a healthy, fully-imported tree.
if [[ -z "$mcp_builtin_pkgs" ]]; then
  echo "${GATE} FAIL: found zero subdirectories under ${BUILTIN_MCP_ROOT} (excluding ${BUILTIN_MCP_ROOT_EXCLUDE})." >&2
  echo "${GATE} FR-008's package-level import check has nothing to inspect, which is" >&2
  echo "${GATE} indistinguishable from passing. Either BUILTIN_MCP_ROOT moved or the" >&2
  echo "${GATE} exclude-directory convention changed — update this script in the same commit." >&2
  exit 1
fi

while IFS= read -r pkg; do
  [[ -z "$pkg" ]] && continue
  import_path="github.com/kameas-ai/kenaz-harness/${pkg}"
  # -l lists matching filenames only; grep against every .go file in the
  # module, then drop matches that are (a) inside the package itself —
  # its own files legitimately reference their own import path in
  # nothing, this is belt-and-suspenders — or (b) test files, which
  # importing a package under test does not count as production wiring.
  importers=$(grep -rlE "\"${import_path}\"" --include='*.go' . 2>/dev/null \
    | grep -v '_test\.go$' \
    | grep -v "^\./${pkg}/" \
    || true)
  if [[ -z "$importers" ]]; then
    violations="${violations}${pkg}"$'\n'
  fi
done <<< "$mcp_builtin_pkgs"

# ---- 6. harness-self: every nil-checked Managers field is assigned ----
# See the WP03 scope-extension note in the header. Discovery floor: the
# harness-self handlers nil-check a dozen managers today; finding none
# means the `if m.X == nil` convention changed, not that all is wired.
# Comment lines are stripped first (a commented-out check is not a check),
# and BOTH operand orders are matched anywhere on a line — so a compound
# `if m.A == nil || m.B == nil` yields A and B, and `nil == m.X` counts
# (security review L5: the first version matched only `if m.X == nil`, one
# field per line).
# strip_go_comments removes Go comments — // line comments (whole-line AND
# trailing) and /* */ blocks (single- or multi-line) — and blanks the
# contents of string, raw-string and rune literals, so neither commented
# nor quoted text is ever read as code (WP02 re-review: `var _ = 0 //
# m.X = later` and a block-commented assignment both used to count as
# wiring). A small per-character state machine: a naive strip got
# "cedar/*.cedar" inside a string wrong and swallowed the rest of the
# package, which the discovery floor below caught.
strip_go_comments() {
  awk '
  BEGIN { st = 0 }   # 0 code, 1 "string", 2 `raw`, 3 /* block */, 4 rune
  {
    line = $0; out = ""; n = length(line)
    for (i = 1; i <= n; i++) {
      c = substr(line, i, 1); nx = substr(line, i + 1, 1)
      if (st == 0) {
        if (c == "/" && nx == "/") break
        if (c == "/" && nx == "*") { st = 3; i++; continue }
        if (c == "\"") { st = 1; out = out c; continue }
        if (c == "`")  { st = 2; out = out c; continue }
        if (c == "\047") { st = 4; out = out c; continue }
        out = out c
      } else if (st == 1) {
        if (c == "\\") { i++; continue }
        if (c == "\"") { st = 0; out = out c }
      } else if (st == 2) {
        if (c == "`") { st = 0; out = out c }
      } else if (st == 3) {
        if (c == "*" && nx == "/") { st = 0; i++ }
      } else if (st == 4) {
        if (c == "\\") { i++; continue }
        if (c == "\047") { st = 0; out = out c }
      }
    }
    if (st == 1 || st == 4) st = 0
    print out
  }'
}
nil_checked=$(find "$HARNESS_SELF_PKG" -maxdepth 1 -name '*.go' ! -name '*_test.go' -exec cat {} + 2>/dev/null \
  | strip_go_comments \
  | grep -oE '(m\.[A-Za-z0-9_]+[[:space:]]*==[[:space:]]*nil|nil[[:space:]]*==[[:space:]]*m\.[A-Za-z0-9_]+)' \
  | grep -oE 'm\.[A-Za-z0-9_]+' | sed 's/^m\.//' | sort -u || true)
if [[ -z "$nil_checked" ]]; then
  echo "${GATE} FAIL: found no 'if m.<Field> == nil' manager checks under ${HARNESS_SELF_PKG}." >&2
  echo "${GATE} §6 has nothing to inspect, which is indistinguishable from passing. The" >&2
  echo "${GATE} handler nil-check convention changed — update this script in the same commit." >&2
  exit 1
fi
# Comment lines do not count as assignments (security review L5). Read
# once into a variable rather than piped per field: under pipefail a
# `grep -v | grep -q` pipeline can fail on SIGPIPE after a match.
wiring_code=$(strip_go_comments < "$HARNESS_WIRING_FILE" || true)
while IFS= read -r field; do
  [[ -z "$field" ]] && continue
  if ! grep -qE "(^|[^A-Za-z0-9_])m\.${field}[[:space:]]*=[^=]" <<< "$wiring_code"; then
    violations="${violations}harness-self-manager:${field}"$'\n'
  fi
done <<< "$nil_checked"

violations=$(printf '%s' "$violations" | grep -v '^$' | sort -u || true)

allow=$(load_allowlist "$ALLOW_FILE")

# ---- 3. violations not covered by the allowlist ----
unlisted=$(comm -23 <(printf '%s\n' "$violations" | sort -u) <(printf '%s\n' "$allow" | sort -u) | grep -v '^$' || true)
if [[ -n "$unlisted" ]]; then
  echo "" >&2
  echo "${GATE} FAIL: builtin tool package(s) with no production importer, not in ${ALLOW_FILE}:" >&2
  printf '%s\n' "$unlisted" | sed 's/^/    /' >&2
  echo "" >&2
  echo "${GATE} A tool the model cannot call is not shipped, it is dead. Register it," >&2
  echo "${GATE} delete it, or add a DATED justification naming the blocker." >&2
  fail=1
fi

# ---- 4. stale allowlist entries ----
while IFS= read -r entry; do
  [[ -z "$entry" ]] && continue
  # A line naming a package that no longer exists is fine — deleting the
  # dead tool is a valid resolution and must not require a paired edit.
  # §6 entries (harness-self-manager:<Field>) are not paths: they are
  # stale as soon as the field is no longer a violation (wired, or the
  # handler and its nil-check deleted).
  if [[ "$entry" != harness-self-manager:* ]]; then
    [[ -d "$entry" ]] || continue
  fi
  if ! printf '%s\n' "$violations" | grep -qx "$entry"; then
    echo "" >&2
    echo "${GATE} FAIL: STALE entry in ${ALLOW_FILE} — ${entry} now has a production importer. Delete the line (allow-lists shrink monotonically)." >&2
    fail=1
  fi
done <<< "$allow"

if [[ "$fail" -ne 0 ]]; then
  echo "" >&2
  echo "${GATE} FAIL — see violations above." >&2
  exit 1
fi

echo "${GATE} clean — every builtin tool package under ${TOOLS_ROOT} and ${BUILTIN_MCP_ROOT} is wired or listed in ${ALLOW_FILE}; every harness-self handler manager is assigned in ${HARNESS_WIRING_FILE}."
