#!/usr/bin/env bash
# check-genai-span-no-content.sh — llm-call-telemetry-01LLMTEL01 WP01: the
# gen_ai.chat / kenaz.chat.turn spans emitted for every model provider
# call NEVER carry content (prompts, completions, tool arguments, tool
# results, system prompt text). There is no TRACE_CONTENT equivalent, not
# even behind a flag.
#
# THE DEFECT CLASS
# ----------------
# OpenLLMetry-style instrumentation makes content one attribute away:
# gen_ai.prompt.0.content, gen_ai.completion.0.content, the semconv
# gen_ai.input.messages / gen_ai.output.messages / gen_ai.system_instructions
# keys, a tool call's arguments. Any of them on a span ships the user's
# conversation into the local store and, once WP02's opt-in exists, to
# Fleet. So does a "harmless" free-text attribute (an error message, a
# provider's explanation) — which is why every string attribute must be
# bounded, not just named innocently.
#
# WHAT THIS CHECKS
# ----------------
# (a) STRUCTURAL (scripts/ci/cmd/checkgenaispan):
#     1. the allowlist core/llm/genaitrace/attrs.go SpanAttributes: keys in
#        gen_ai.* / kenaz.* / error.type only; no content key
#        (gen_ai.prompt*, gen_ai.completion*, *.content, *.message*,
#        *.arguments, *.result, *.instructions, *.text, *.body); every
#        string / string[] key has a bounded class (id, model, token, enum,
#        hash) — there is no free-text class;
#     2. every gen_ai.* / kenaz.* literal in the instrumentation package is
#        a declared key or span name;
#     3. no gen_ai.* / kenaz.{llm,provider,session,chat}.* literal anywhere
#        else under core/ or cmd/ — one writer, the allowlist-enforcing one;
#     4. no semconv GenAI*{Messages,Instructions,Prompt,Completion,
#        Arguments,Result,Content}* selector anywhere under core/ or cmd/;
#     5. no AddEvent / RecordError / AddLink, and code-only SetStatus, in
#        the instrumentation package;
#     6. (WP02) the local model-call store: every model_calls column any
#        file under core/ or cmd/ declares (CREATE TABLE / ALTER TABLE ADD
#        COLUMN) is structural (genaitrace.RecordStructuralColumns) or
#        bound to an allowlisted key (genaitrace.RecordColumns); no column
#        names content; no mapping is stale.
# (b) RUNTIME: the per-adapter table tests drive a recorded, content-
#     bearing request/response through the real choke point
#     (genaitrace.Instrument) and assert the exact attribute set, that
#     every key is allowlisted and that no content string appears; the
#     chat-runner test does the same for a whole turn (retry + tool call);
#     the attribute doc test fails when the Fleet-declarable table drifts.
#     Each must report `--- PASS: <name>`; "no tests to run" fails.
#
# Planted-violation proofs: scripts/ci/gates_can_fail_test.go
# TestGenAISpanGate_* (content key in the allowlist, free-text class,
# a second emitter, an undeclared key in the package, a semconv content
# key, a span event; WP02: a content column added to the store, an
# unmapped store column, a store column mapped to a non-allowlisted key).
#
# GENAI_SPAN_GATE_STRUCTURAL_ONLY=1 skips (b) — used only by the planted-
# violation meta-tests. CI runs both halves.
#
# Usage: bash scripts/ci/check-genai-span-no-content.sh (from anywhere).

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[genai-span-no-content]"

ci_require_dir core "$GATE"
ci_require_dir cmd "$GATE"
ci_require_file core/llm/genaitrace/attrs.go "$GATE"

# ---- (a) structural ----
BIN_DIR="$(mktemp -d)"
trap 'rm -rf "$BIN_DIR"' EXIT
if ! go build -o "$BIN_DIR/checkgenaispan" ./scripts/ci/cmd/checkgenaispan/; then
  echo "${GATE} FAIL: checkgenaispan failed to build." >&2
  exit 1
fi
set +e
"$BIN_DIR/checkgenaispan"
rc=$?
set -e
if [[ $rc -ne 0 ]]; then
  echo "${GATE} FAIL — structural check (exit ${rc})." >&2
  exit 1
fi

if [[ "${GENAI_SPAN_GATE_STRUCTURAL_ONLY:-0}" == "1" ]]; then
  echo "${GATE} structural half clean; runtime half skipped (GENAI_SPAN_GATE_STRUCTURAL_ONLY=1)."
  exit 0
fi

# ---- (b) runtime ----
run_named_test() {
  local pkg="$1" name="$2" out
  set +e
  out=$(CGO_ENABLED="${CGO_ENABLED:-1}" go test -count=1 -short -run "^${name}\$" -v "./${pkg}/" 2>&1)
  local code=$?
  set -e
  if [[ $code -ne 0 ]]; then
    printf '%s\n' "$out" | grep -v '^{' | tail -40 >&2
    echo "${GATE} FAIL: ${pkg} ${name} failed (exit ${code})." >&2
    exit 1
  fi
  if ! grep -q -- "--- PASS: ${name} " <<< "$out"; then
    printf '%s\n' "$out" | grep -v '^{' | tail -20 >&2
    echo "${GATE} FAIL: ${pkg} ${name} did not run (renamed or deleted?). Update this gate in the same commit." >&2
    exit 1
  fi
  echo "${GATE} ${pkg} ${name}: PASS"
}

run_named_test core/llm/anthropic TestAnthropicAdapter_GenAISpan_ExactAttributes
run_named_test core/llm/openai TestOpenAIAdapter_GenAISpan_ExactAttributes
run_named_test core/llm/openrouter TestOpenRouterAdapter_GenAISpan_ExactAttributes
run_named_test core/llm/azure TestAzureAdapter_GenAISpan_ExactAttributes
run_named_test core/llm/custom TestCustomAdapter_GenAISpan_ExactAttributes
run_named_test core/llm/gemini TestGeminiAdapter_GenAISpan_ExactAttributes
run_named_test core/llm/ollama TestOllamaAdapter_GenAISpan_ExactAttributes
run_named_test core/llm/genaitrace TestAttrSet_RefusesUndeclaredAndUnbounded
run_named_test core/llm/genaitrace TestSpanAttributeDocInSync
run_named_test core/rpc/views/agentgraph/chat TestChatTurn_GenAISpans_NestedWithRetryAndToolCall
run_named_test core/fleet TestOTLPPipeline_ModelCallSpansStayLocal
# WP02: the local store, the stats pull and the opt-in Fleet export.
run_named_test core/llm/genaitrace TestAttrSet_ScopeRefusesKeysOffTheirSpan
run_named_test core/modelcalls TestRecorder_SpanToStore_NoContent
run_named_test core/modelcalls TestStatsPuller_RetriesThenMapsAllowlistedFields
run_named_test core/fleet TestOTLPPipeline_ModelCallSharing_OffOnOff

echo "${GATE} clean — model-call spans and the local model_calls store carry only allowlisted, bounded, content-free attributes, from one emitter."
