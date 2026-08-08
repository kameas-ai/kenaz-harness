#!/usr/bin/env bash
# Privacy CI invariant #2 (plan §4.3): no user-content fields in structured
# log calls. Forbidden attribute keys: Subject, SubjectDim, Body, Prompt,
# Response, DraftInput, Path. A `// privacy-allow: <reason>` comment on the
# line opts it out.
#
# COVERAGE NOTE — READ BEFORE NARROWING THIS AGAIN
# ------------------------------------------------
# The original pattern was `slog\.(Info|Debug|Warn|Error)\(`. That is the
# minority idiom in this codebase. Measured on main at a7f3e87, core/ has
# ~188 structured-log call sites and only 26 of them are spelled `slog.`:
#
#     logger.Warn   45     log.Info    30     log.Warn   25
#     slog.Warn     20     logger.Info 18     logger.Debug 17
#     log.Debug     13     log.Error    6     slog.Info    5
#     logger.Error   4     slog.Error   1     l.{Info,Warn,Debug,Error} 4
#
# So the gate was blind to 86% of the surface it claims to guard: a
# `logger.Info("...", "Prompt", p)` sailed straight through. The receiver is
# now any identifier, because what matters is the forbidden key reaching a
# log sink, not which variable holds the handle.
#
# KNOWN REMAINING GAP (not fixed here, deliberately)
# --------------------------------------------------
# This is a line-oriented grep, so a call split across lines —
#     logger.Info("saved",
#         "Path", p)
# — is still invisible unless the key sits on the same line as the call. The
# typed-attribute form (slog.String("Path", p)) IS matched separately below
# because that is the common multi-line spelling. Closing the general case
# needs an AST pass (go/analysis), which is a bigger change than this sweep.
# Do not read a passing run as proof no multi-line violation exists.
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[slog-privacy]"
ci_require_dir core "$GATE"

FORBIDDEN_KEYS=(Subject SubjectDim Body Prompt Response DraftInput Path)

fail=0

for key in "${FORBIDDEN_KEYS[@]}"; do
  # (a) key-value form on one line, ANY receiver:
  #       logger.Info("saved", "Path", p)   slog.Warn(...)   log.Debug(...)
  #     Level set includes the *Context variants (InfoContext, ErrorContext…).
  # (b) typed-attribute constructors, which is how multi-line calls spell it:
  #       slog.String("Path", p) / slog.Any("Prompt", x) / slog.Group("Body", …)
  matches=$(
    {
      grep -rEn "[A-Za-z_][A-Za-z0-9_]*\.(Info|Debug|Warn|Error)(Context)?\([^)]*\"$key\"" --include='*.go' core/ 2>/dev/null || true
      grep -rEn "slog\.(String|Any|Int|Int64|Uint64|Float64|Bool|Time|Duration|Group|Attr)\(\s*\"$key\"" --include='*.go' core/ 2>/dev/null || true
    } | sort -u
  )
  if [[ -n "$matches" ]]; then
    while IFS= read -r line; do
      [[ -z "$line" ]] && continue
      if echo "$line" | grep -q 'privacy-allow:'; then
        continue
      fi
      echo "$GATE FAIL: $line" >&2
      fail=1
    done <<< "$matches"
  fi
done

if [[ $fail -ne 0 ]]; then
  echo "$GATE privacy CI invariant #2 (plan §4.3) failed." >&2
  echo "$GATE Log an opaque identifier instead of the content, or annotate the line" >&2
  echo "$GATE with '// privacy-allow: <reason>' if the value is genuinely non-content." >&2
  exit 1
fi

echo "$GATE clean — invariant #2 passes."
