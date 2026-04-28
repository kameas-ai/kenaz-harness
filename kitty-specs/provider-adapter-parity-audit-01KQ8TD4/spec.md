# Spec: Provider adapter wire-shape parity audit

**Status**: draft · **Owner**: alecfeeman

## 1. Why

Today's debugging surfaced two silent drops in `core/llm/openrouter` (Tools on request 4185933; tool_calls on response 2c710ae). Same audit hasn't been run for `core/llm/anthropic`, `core/llm/openai`, or `core/llm/bedrock`. This mission is the **one-time sweep** to ensure parity across all four adapters before the integration-test-harness mission codifies enforcement.

## 2. Goals

- Audit each adapter's `buildRequestBody` for every `corellm.GenerationRequest` field; identify silent drops.
- Audit each adapter's SSE/streaming parser for every `corellm.Response` field; identify silent drops.
- Audit the Final/terminal response builder for fan-in completeness (tool_calls, finish_reason, usage, cost).
- Patch any drops found.
- Document the per-adapter feature matrix.

## 3. Functional requirements

| ID | Requirement | Status |
|---|---|---|
| FR-001 | Audit table in `docs/provider-adapter-matrix.md` listing every (adapter, GenerationRequest field, supported?) tuple. | proposed |
| FR-002 | Same table for Response fields. | proposed |
| FR-003 | Each silent drop fixed with an accompanying wire-shape test. | proposed |
| FR-004 | Anthropic adapter audited for `cache_control`, `system`, `top_k`, `metadata`, `stream` payload shapes. | proposed |
| FR-005 | Bedrock adapter audited for `inferenceConfig`, `system` (array shape), `additionalModelRequestFields`, `guardrailConfig`. | proposed |
| FR-006 | OpenAI adapter audited for `parallel_tool_calls`, `seed`, `response_format`, `logprobs`, `service_tier`. | proposed |

## 4. Success criteria

- All four adapters score 100% on the new wire-shape contract sweep.
- The provider-adapter-matrix.md doc reflects current state and lists deliberate non-support with reasons.
