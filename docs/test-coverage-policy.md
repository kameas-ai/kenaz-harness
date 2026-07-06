# Test coverage policy — wire-shape contract layer

This document captures the bug classes each test layer catches and the
rules contributors must follow when adding new fields or adapters.

## Background: the silent-drop bug class

In one debugging session three production-impacting bugs were found that all had
passing unit tests:

1. **commit 4185933** — OpenRouter dropped `Tools` on the request: the adapter
   was called with a tool list but the serialised JSON body had no `tools` key.
2. **commit 2c710ae** — OpenRouter dropped streamed `tool_calls` on the response:
   the SSE pump received tool-call deltas but they were never accumulated into
   the final `Response.ToolCalls`.
3. **commit 495fb13** — Model executor ignored history: `LLMProvider.Generate`
   was called but the `Messages` list was empty because the executor never read
   from the `HistoryReader` seam.

All three had passing unit tests because integration tests used stub `corellm.Stream`
impls that bypass the entire `Registry → Adapter → http.Client → wire bytes` path.

## Test layers

### Layer 1 — wire-shape contract sweep (per-adapter)

**Lives at**: `core/llm/<adapter>/<adapter>_wireshape_test.go`

**Catches**: field serialisation bugs (4185933 shape) and field parsing bugs
(2c710ae shape).

**Mechanism**: `httptest.Server` + `wirecheck.AssertSerialized` / `AssertParsedFromWire`.
Each test drives the adapter with a request that sets one field, captures the
HTTP body the adapter sends, and asserts the field appears in the correct JSON
path. Response tests inject an SSE fixture and assert the parsed `StreamEvent`
has the expected values.

**Coverage gate**: `TestRegistryCompleteness` (in `core/llm/wirecheck/`) walks
`reflect.TypeOf(GenerationRequest{})`, `Response{}`, and `StreamEvent{}` and
asserts every exported field has either a real test or an explicit `unsupported:`
reason in `core/llm/coverage_registry.yaml` for each in-scope adapter.

**Rule: a new `GenerationRequest` field requires a wire-shape test before merge.**

### Layer 2 — seam-fanout recorder matrix (per kernel kind)

**Lives at**: `core/agentgraph/seam_fanout_test.go`

**Catches**: seam parameter bugs (495fb13 shape) — where a kernel node fires but
doesn't populate a seam argument correctly.

**Mechanism**: `core/agentgraph/internal/recorders/` recording fakes capture
every parameter of every seam call. Tests assert `RequireHistoryReadCalledWith`,
`RequireGenerateCalledWith`, etc.

**Coverage gate**: every kernel kind must have a fanout test entry. Adding a new
kind without a fanout entry fails CI.

### Layer 3 — end-to-end golden-file tests (locked tier)

**Lives at**: `core/rpc/views/agentgraph/chat/wire_integration_test.go`

**Catches**: integration bugs where individual layers pass but the assembled
pipeline fails — e.g. a request is serialised correctly but the profile lookup
replaces the model before the adapter sees it.

**Mechanism**: full pipeline run — `Registry.Stream → buildRequestBody → httptest.Server
(request diff against hand-authored golden) → SSE replay → Adapter.Final → kernel.run.complete`.

**Fixtures**: `testdata/wire_golden/<adapter>/<scenario>/`
- `request.json` — **hand-authored**. Every field on the wire is a deliberate
  decision; getting them right IS the point of the contract test.
- `response.sse` — **recorded** from real upstream APIs with `-update`, then
  scrubbed for determinism. Never modified by hand.

**Update flag**: `-update` refreshes ONLY `response.sse` for the named scenarios.
Requires `TEST_<ADAPTER>_KEY` env vars. Missing keys cause a clear `t.Skipf`.
Never touches `request.json`.

## The two-tier fixture model

| Tier | Path | Gating | Update path |
|------|------|--------|-------------|
| Locked | `testdata/wire_golden/<adapter>/<scenario>/` | Every commit (`wire-golden-locked` CI job) | Manual `-update` with real API key |
| Live | Nightly cron via `wire-golden-live.yml` | Never blocks commit-time CI | Auto-PR opens on drift |

**A live-tier failure never blocks the commit-time CI.**

When the live tier detects **response-side drift** (provider changed a field,
added a finish-reason vocabulary entry), an auto-PR opens that updates ONLY the
`response.sse` files. When it detects **request-side rejection** (our request
body is rejected by the provider), it opens a `regression`-labelled PR for human
investigation.

## Contributor rules

1. **New `GenerationRequest` field**: add the field, update `coverage_registry.yaml`
   with either a real test or `unsupported:` reason for every in-scope adapter,
   and write the wire-shape test. `TestRegistryCompleteness` must pass before the
   PR merges.

2. **New adapter**: add the kind to `inScopeAdapters` in
   `core/llm/wirecheck/registry_completeness_test.go`, add registry entries, and
   add a `<adapter>_wireshape_test.go` file.

3. **New kernel kind**: add a fanout test entry in `seam_fanout_test.go` and
   update the fixture at `testdata/seam_fanout/<kind>.yaml`.

4. **New scenario**: add `request.json` (hand-authored) and `response.sse`
   (recorded with `-update`) under `testdata/wire_golden/<adapter>/<scenario>/`
   and add a `Test<Adapter>_<Scenario>_WireGolden` function.

## Live-tier nightly run

The `wire-golden-live.yml` workflow runs nightly at 09:00 UTC. It re-runs all
`*_WireGolden` tests against real upstream APIs using provider credentials
stored as Actions secrets (`WIRE_GOLDEN_<ADAPTER>_KEY`).

### Drift outcomes

| Outcome | What happened | Automated action |
|---------|---------------|------------------|
| `no-drift` | Fixtures still match upstream | No PR |
| `response-drift` | Provider changed a response field | Auto-PR on `auto/wire-golden-refresh` updating `response.sse` only |
| `request-regression` | Provider rejected our request body | Regression PR labelled `regression` for human investigation |
| `skipped` | No keys configured for this adapter | Warning in workflow summary |

A live-tier failure **never blocks `pr.yml`** (the commit-time CI gate).

### Auto-PR constraints (response-drift)

The auto-PR's diff is constrained to `testdata/wire_golden/*/response.sse`.
CI on the auto-PR runs the locked tier against the new fixtures. If green and
a maintainer approves, it merges. The auto-PR uses a single rolling branch
`auto/wire-golden-refresh` — force-pushed on each new drift, so there is always
at most one open response-drift PR.

### Key rotation

Provider keys are stored as `WIRE_GOLDEN_ANTHROPIC_KEY`, `WIRE_GOLDEN_OPENAI_KEY`,
`WIRE_GOLDEN_OPENROUTER_KEY`, `WIRE_GOLDEN_BEDROCK_KEY` in the repository's
Actions secrets. Rotate annually or when a key is revoked. Missing keys cause
the affected adapter to skip (not fail) the workflow.

### Drift dashboard

The drift report is written to `docs/wire-golden-drift.md` and pushed to the
`wire-golden-drift` branch on each nightly run. Read it to see the outcome of
the last live-tier run and any open PRs.

[View drift report](wire-golden-drift.md)

## Scrubber hygiene rules

The scrubber (`core/llm/wirecheck/scrub/`) normalises recorded fixtures to be
deterministic. It applies rules in order:

1. JSON id normalisation: `id`, `request_id`, `response_id`, `tool_use_id`,
   `message_id`, `system_fingerprint` → `wire-golden-id-<n>` (counter per fixture).
2. Timestamp zeroing: `created`, `timestamp`, `created_at` → `0` (number) or
   `1970-01-01T00:00:00Z` (string).
3. Header stripping: SSE-frame `data:` JSON has rules applied per-frame.
4. Anti-abuse / provider-trace stripping: `x-request-id`, `cf-ray`,
   `x-amzn-trace-id`, `openai-organization`, `anthropic-organization-id`.
5. Unicode normalisation: NFC.
6. Trailing-newline normalisation: file ends with a single `\n`.

Two consecutive `-update` runs against the same upstream must produce byte-identical
scrubbed fixtures. The CI fixture-size guard rejects scenarios > 4 KiB SSE.
