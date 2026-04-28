# Spec: Provider/node integration test harness

**Status**: draft · **Owner**: alecfeeman

## 1. Why

In one debugging session today we found three production-impacting bugs of the same shape, all hidden behind passing unit tests:
1. OpenRouter dropped `Tools` on the request (commit `4185933`).
2. OpenRouter dropped streamed `tool_calls` on the response (commit `2c710ae`).
3. Model executor ignored history (commit `495fb13`).

All three had passing unit tests at every layer because the integration tests in `chat_runner_integration_test.go` use stub `corellm.Stream` impls that bypass the entire `Registry → Adapter → http.Client → wire bytes` path.

## 2. Goals

Three layered test investments:

1. **Wire-shape contract sweep**: every field of `corellm.GenerationRequest` (Tools, System, Messages, Stop, Temperature, MaxTokens, ResponseFormat, etc.) AND every field of `corellm.Response` / streamed `StreamEvent` gets a "Serialized"/"ParsedFromWire" test for each adapter (anthropic, openai, openrouter, bedrock).
2. **Seam-fanout recorder matrix**: for each `core/agentgraph/seams.go` interface, recording fakes assert each parameter of each seam call is populated correctly (not just "called once").
3. **End-to-end chat-graph integration**: one golden-file test per provider running `chat_default.yaml` end-to-end through `Registry.Stream → buildRequestBody → fakeServer → SSE injection → Adapter.Final → kernel.run.complete`. Captures wire request bytes for diff against checked-in goldens.

## 3. Non-goals

- Property-based / fuzz testing (separate mission).
- Mutation testing of executors.
- Performance / load tests.

## 4. Functional requirements

| ID | Requirement | Status |
|---|---|---|
| FR-001 | Wire-shape tests live alongside each adapter (`core/llm/<adapter>/<adapter>_test.go`); shared `wirecheck` helper so adding a new field is one struct entry, not 30 lines. | proposed |
| FR-002 | Reflection-based field-coverage table iterates `GenerationRequest`/`Response` fields; CI fails if a new field is added without a test. | proposed |
| FR-003 | Seam recorder fakes live in `core/agentgraph/internal/recorders/`; assertions walk every field of every recorded call. | proposed |
| FR-004 | End-to-end tests at `core/rpc/views/agentgraph/chat/wire_integration_test.go`; one func per adapter; httptest.Server + SSE fixtures; goldens at `testdata/wire_golden/<adapter>/<scenario>.json`. | proposed |
| FR-005 | Goldens regenerated via a `-update` flag (existing convention). | proposed |
| FR-006 | CI job runs wire-golden tests separately so failures point at the exact boundary. | proposed |
| FR-007 | `docs/test-coverage-policy.md` captures the bug classes each layer catches and the rule "new GenerationRequest field requires a wire-shape test before merge." | proposed |

## 5. Non-functional requirements

| ID | Requirement | Threshold | Status |
|---|---|---|---|
| NFR-001 | Total new test time. | ≤ 60 s under `-race -count=1 -short`. | proposed |
| NFR-002 | Adapter-test add cost for a new field. | ≤ 30 LOC per adapter. | proposed |

## 6. Success criteria

- All three of today's bug fixes (4185933, 2c710ae, 495fb13) would have been caught by these tests on the day they shipped.
- Adding a new `GenerationRequest` field without a test fails CI.
- Adding a new kernel kind without seam-fanout coverage fails CI.
