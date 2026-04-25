#!/usr/bin/env bash
# Privacy CI invariant #2 (plan §4.3): no user-content fields in slog
# calls. Forbidden field names: Subject, SubjectDim, Body, Prompt,
# Response, DraftInput, Path. A `// privacy:never-log` struct comment
# blocks logging of the annotated field.
#
# Implementation: grep for slog.* lines that mention forbidden field
# names as quoted attribute keys.
set -euo pipefail

FORBIDDEN_KEYS=(Subject SubjectDim Body Prompt Response DraftInput Path)

fail=0

for key in "${FORBIDDEN_KEYS[@]}"; do
  # Match e.g. slog.Info("...", "Subject", subject) — quoted key.
  matches=$(grep -rEn "slog\.(Info|Debug|Warn|Error)\([^)]*\"$key\"" --include='*.go' core/ 2>/dev/null || true)
  if [[ -n "$matches" ]]; then
    while IFS= read -r line; do
      if echo "$line" | grep -q 'privacy-allow:'; then
        continue
      fi
      echo "[slog-privacy] FAIL: $line" >&2
      fail=1
    done <<< "$matches"
  fi
done

if [[ $fail -ne 0 ]]; then
  echo "[slog-privacy] privacy CI invariant #2 (plan §4.3) failed." >&2
  exit 1
fi

echo "[slog-privacy] clean — invariant #2 passes."
