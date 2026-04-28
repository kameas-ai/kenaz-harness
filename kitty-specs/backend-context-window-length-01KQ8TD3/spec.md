# Spec: Backend-derived per-model context-window length

**Status**: draft · **Owner**: alecfeeman

## 1. Why

The session footer's context-window meter (`SessionsView.vue` after commit `7e60a2a`) uses a regex-matched fallback table to guess context length per model id. It's a heuristic — wrong for new releases, anything outside the lookup, and entirely manual to maintain. The connector's `/models` endpoints already know the right value per model.

## 2. Goals

- Surface `Provider.ModelInfo.contextLength` end-to-end so the frontend reads the authoritative number.
- Wails-bound type adds the field; default to 0 (caller falls back).
- Frontend prefers backend value; the regex table stays only for unwired providers.

## 3. Functional requirements

| ID | Requirement | Status |
|---|---|---|
| FR-001 | `core/llm.ModelInfo` already carries `ContextLength`; add `MaxOutputTokens` for the same /models payloads that surface it. | proposed |
| FR-002 | Provider stack persists model context-length in the providers store on add/edit. | proposed |
| FR-003 | RPC types expose `ModelInfo` per provider via the existing `LLM.ListProviderModels` (or new method if absent). | proposed |
| FR-004 | Frontend `Provider` type gains `models: ModelInfo[]` with `{id, contextLength, maxOutputTokens}`. | proposed |
| FR-005 | `SessionsView.modelContextWindow` reads from the resolved ModelInfo first; falls back to the regex table only if 0. | proposed |
| FR-006 | The Anthropic / OpenAI / OpenRouter / Bedrock `ListModels` calls populate ContextLength when the upstream payload provides it. | proposed |

## 4. Success criteria

- Switching a session to a model the regex table doesn't recognise still shows an accurate %.
- Adding a new model via OpenRouter persists its context length on first list-fetch.
