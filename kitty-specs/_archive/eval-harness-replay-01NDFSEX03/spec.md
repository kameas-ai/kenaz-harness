# Spec: Eval harness (replay-based, no scoring)

**Status**: draft · **Owner**: alecfeeman

## 1. Why

The platform's thesis is **explicit graphs > opaque loops** — that giving users control over compaction, branching, memory, and routing produces *better* outputs than letting a single agent loop reason about everything. Today there is no mechanism to **measure** that claim. We choose strategies by feel.

This is the project's biggest **methodology gap**. It's not a feature; it's the missing harness for every future feature ("should the default compaction tier be balanced or aggressive? we don't know"). The existing `integration-test-harness-01KQ8TD1` covers e2e UI confidence — clicking through a chat works, the binary boots — not output quality.

The minimum plumbing: capture sessions, replay them deterministically against alternative strategies, diff the outputs. Scoring (preference labelling, ELO, automated graders) is intentionally out of scope for this mission — get the replay path in first; scoring can land on top.

## 2. Goals

- A captured session can be replayed end-to-end against a different `Settings`/`Strategy` profile without re-hitting live LLM APIs (replay reads cached LLM responses from the original capture).
- A captured session can be replayed against a *real* LLM with a different strategy (compaction, memory, branching) and the outputs diffed against the original.
- Replays are deterministic given the same seed + cached responses.
- A `core/eval/` package owns the replay machinery; no UI surface in this mission (CLI / Go test driver only).

## 3. Non-goals

- Automated scoring of outputs. No LLM-as-judge, no preference UI, no ELO. Just replay + diff.
- A web UI for browsing eval runs. Read JSON output from disk for now.
- Anything that requires distributing captured sessions externally. Captures stay on-device unless the user explicitly exports.
- Replacing the existing integration test harness. This is a parallel construct optimized for output quality vs. flow correctness.

## 4. Functional requirements

### Capture

| ID | Requirement | Status |
|---|---|---|
| FR-001 | New session-level flag `capture_for_eval: bool` and `Sessions_StartCapture(sessionID)` / `Sessions_StopCapture(sessionID)` RPCs that opt a session into recording. | proposed |
| FR-002 | Captured data: full message history, every tool call + tool result, every LLM request/response pair (with provider, model, params), every compaction/branching/memory decision with input state + chosen output. Serialized to `<DataDir>/eval-captures/<session_id>.jsonl`. | proposed |
| FR-003 | Captures redact credentials and any byte ranges flagged by the credential analyzer (reuse existing redaction pipeline). | proposed |

### Replay

| ID | Requirement | Status |
|---|---|---|
| FR-004 | `core/eval/replay.go` reads a capture file and reconstructs the session state machine deterministically. | proposed |
| FR-005 | LLM calls during replay are served from the capture's cached responses (by request fingerprint) when `--cached-only` is set; fall through to live API on miss when `--allow-live` is set. | proposed |
| FR-006 | Replay accepts a `--strategy-override <key=value>` flag (`compaction.tier=aggressive`, `memory.recall_top_k=5`, `branching.default_model=…`) so the user can vary one or more inputs while holding everything else constant. | proposed |
| FR-007 | Replay writes `<DataDir>/eval-runs/<run_id>/{trace.jsonl, summary.json, diff_vs_baseline.json}`. | proposed |

### Diff

| ID | Requirement | Status |
|---|---|---|
| FR-008 | `diff_vs_baseline.json` includes per-turn divergence: token counts, tool calls made, final assistant message similarity (cosine similarity over embeddings; embeddings drawn from the existing memory embedder), wall-clock time. | proposed |
| FR-009 | A `core/eval` Go test helper `RunMatrix(captures, strategies)` runs a matrix and emits a markdown summary table. | proposed |

## 5. Open questions

- **Response cache key.** LLM request fingerprints need to span temperature, system prompt, tool definitions, message history. Reuse `llm/cachekey.go` if compatible.
- **Capture size.** A turn-rich session could produce multi-MB captures. Defer compression until size becomes a problem.
- **Tool side effects.** Real file edits during a captured session can't be replayed. Either flag captures with mutating tools as "non-replayable" or stub the FS at replay time. Start with the flag.
- **Embedding similarity is a weak proxy for "better output".** Acknowledged; this is the foundation for later scoring, not the answer.

## 6. Acceptance criteria

- A captured 5-turn chat session can be replayed in `--cached-only` mode and produces byte-identical assistant outputs.
- The same capture replayed with `--strategy-override compaction.tier=aggressive` produces a non-identical assistant output and a `diff_vs_baseline.json` that reports the divergence.
- The `RunMatrix` helper produces a 3x3 (3 captures × 3 strategies) markdown table in under 30 seconds against cached responses.
