# CHANGELOG

> **Note**: Detailed per-commit history lives in git log + GitHub Releases.
> This file records significant behaviour changes, new provider kinds, and
> SQLite migration milestones for operators upgrading production deployments.

---

## Unreleased (v0.15.0)

### New: Custom OpenAI-compatible provider kind

kenaz-harness can now connect to any server that speaks the OpenAI
chat-completion wire format — locally-hosted models (vLLM, llama.cpp,
LiteLLM) and hosted inference services (Together AI, Groq, Fireworks AI,
Anyscale, DeepSeek, Mistral AI).

Shipped as the `custom-openai` provider kind behind the `HARNESS_CUSTOM_OPENAI`
feature flag (enabled by default; set `HARNESS_CUSTOM_OPENAI=0` to disable).

**New UI surface**:
- "Custom OpenAI-compatible" option in Add Provider form
- Template chip picker (9 built-in templates with auto-recognition by URL glob)
- Auth scheme selector: bearer / api-key-header / custom / none
- Three-step capability probe (streaming, tool calling, streaming usage)
- Capability matrix display in the form and on the provider list row

**New RPC methods** (Wails-bound):
- `LLM_ListCustomTemplates` — returns the 9 shipped templates
- `LLM_RecognizeTemplate(rawURL)` — URL-to-template matching
- `LLM_ProbeCustomEndpoint(in)` — runs the capability probe, returns the matrix

**SQLite migration**: `0331` — adds `llm_custom_capability_matrix` table
storing per-endpoint streaming/tool-calling/streaming-usage results. The
migration runs automatically at next boot.

**Docs**: `docs/providers/custom-openai.md`
