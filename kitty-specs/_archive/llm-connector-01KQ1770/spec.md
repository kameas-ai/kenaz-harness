# Feature Specification: LLM Connector — Multi-Provider Model Access

**Feature Branch**: `feat/llm-connector-01KQ1770`
**Created**: 2026-04-25
**Status**: Draft
**Input**: User description: "Spec out an LLM connector that I can easily configure to connect to OpenRouter, Bedrock, OpenAI, Anthropic, basically any model provider. Day-one providers: Anthropic, OpenAI, OpenRouter, AWS Bedrock, local Ollama. Enable all capabilities (streaming, tool calling, vision, JSON mode, prompt caching, reasoning blocks, usage/cost, cancellation). Enterprise-ready auth (no plaintext keys, references only). Event log integration in v1. Retry logic in v1."

## User Scenarios & Testing *(mandatory)*

### User Story 1 — Configure a model provider once and use it from any bundle (Priority: P1)

A bundle author declares one or more named model providers in bundle configuration. Each provider points to a kind (Anthropic, OpenAI, OpenRouter, Bedrock, Ollama), a model identifier, and an indirect credential reference (environment variable, OS keychain entry, file path, or AWS profile name). Once declared, any agent definition, skill, or hook in the bundle can request a generation by referring to that provider by name without knowing how the credentials are resolved or what wire protocol the provider speaks.

**Why this priority**: Configuration-first is the harness's core value proposition. Without this, every agent definition would have to hard-code a provider, which defeats the purpose of bundles as durable, swappable artifacts.

**Independent Test**: A bundle author authors a minimal bundle with one Anthropic provider and one OpenAI provider, runs the same agent definition twice — once against each — and gets a successful streaming completion from both without modifying the agent definition.

**Acceptance Scenarios**:

1. **Given** a bundle declares an Anthropic provider with an environment-variable credential reference and the variable is set, **When** an agent in that bundle requests a streaming chat completion against that provider, **Then** the completion streams to the caller token-by-token and the full response is delivered without errors.
2. **Given** a bundle declares the same agent definition usable with two providers (Anthropic and OpenRouter), **When** the operator switches the active provider in configuration, **Then** the agent runs against the new provider without any change to the agent definition or bundle code.
3. **Given** a bundle declares a provider whose credential reference points to a missing source (env var unset, keychain entry absent), **When** the runtime attempts to resolve that provider, **Then** the operator receives a clear, actionable error identifying which provider and which credential reference failed, before any model call is attempted.

---

### User Story 2 — Use advanced model capabilities through a single uniform request shape (Priority: P1)

An agent author writes one generation request describing the conversation, tools, attachments, and options. The connector accepts that request for any configured provider and returns the most capable response that provider supports. When a capability is requested but unsupported by the chosen provider, the connector reports the unsupported capability up front rather than silently dropping or fabricating it. Capabilities in scope for v1: streaming text, multi-turn message history, tool/function calling, image/vision input, structured output (JSON mode), prompt caching, reasoning/thinking blocks, token usage and cost reporting, and mid-stream cancellation.

**Why this priority**: A connector that only supports the lowest common denominator forces every advanced feature back into provider-specific code, breaking the configuration-first promise. Bundle authors must be able to opt into Anthropic prompt caching, Bedrock reasoning, or OpenAI vision through the same surface.

**Independent Test**: A test bundle exercises each capability against each day-one provider that supports it (e.g., prompt caching against Anthropic and Bedrock-Anthropic, vision against OpenAI and Anthropic, reasoning blocks against Anthropic and OpenRouter-routed reasoning models) and the connector either delivers the capability or returns a structured "capability unsupported by provider <id>" response.

**Acceptance Scenarios**:

1. **Given** a request that includes tool definitions and a user prompt that should trigger a tool call, **When** the connector routes it to any provider that supports tool calling, **Then** the response surfaces the tool-call request in a form the caller can dispatch and continue the conversation with the tool result.
2. **Given** a request that includes an image attachment and a question about the image, **When** the connector routes it to a provider with vision support, **Then** the response describes the image content correctly; **And given** the same request routed to a provider without vision support, **When** the connector receives the request, **Then** it returns a typed error identifying the unsupported capability before incurring any provider cost.
3. **Given** a streaming request in flight, **When** the caller cancels it, **Then** the upstream provider connection is terminated within one second and the partial response, tokens consumed up to cancellation, and cancellation cause are recorded.
4. **Given** a request that opts into prompt caching with a cacheable prefix, **When** the same prefix is sent in a subsequent request within the provider's cache window, **Then** the response reports cache-hit token counts greater than zero.

---

### User Story 3 — Every model interaction is auditable and replayable (Priority: P1)

Every generation request, every streamed chunk, every final response, and every error becomes an append-only entry in the harness event log with credentials redacted. An operator can later replay a session, branch from a prior step, audit "what context did the model actually see," answer who-called-what-when, and confirm no credential material was ever written to disk in plaintext.

**Why this priority**: This is the harness's audit story and a SOC 2-readiness anchor in the project charter. Without it from v1, downstream features (replay, branching, audit, observability) all break or require retrofitting.

**Independent Test**: An operator runs a multi-turn session, then queries the event log for that session and confirms it can reconstruct the full request/response history with no plaintext credentials anywhere in the log.

**Acceptance Scenarios**:

1. **Given** a generation request completes successfully, **When** the operator reads the event log for the session, **Then** entries exist for: request submitted, each stream chunk, final response, token usage, latency, and any tool-call events, in the order they occurred.
2. **Given** a request with a credential resolved from an environment variable, **When** the operator inspects the event log entry for that request, **Then** the resolved credential value does not appear anywhere in the log; only the credential reference (kind and lookup key) is present.
3. **Given** a known credential pattern (e.g., an API key shape) appears in the request body or response payload, **When** the entry is written to the log, **Then** the matched substring is redacted before the entry is persisted.
4. **Given** a request fails partway through streaming, **When** the operator reads the event log, **Then** the entries up to failure are present, the failure entry includes the provider error, and the log remains internally consistent (append-only, no rewrites).

---

### User Story 4 — Transient provider failures recover automatically (Priority: P2)

When a provider returns a transient error (network blip, rate-limit, 5xx, timeout), the connector retries with exponential backoff and jitter, up to a configurable retry budget per provider, before surfacing the failure to the caller. Non-transient errors (authentication failure, invalid request, content policy refusal) are surfaced immediately without retry. The caller can always observe whether a response was first-try or recovered-after-N-retries via the event log.

**Why this priority**: Without retry, every model call becomes a flaky-network problem for the caller. Retry is a baseline reliability expectation for any production connector. Cross-provider fallback is a higher-order behavior that is deferred to a follow-up spec.

**Independent Test**: A fault-injecting test substitutes a fake provider that returns a 429 once then 200 — the caller observes a single successful response and the event log shows the retry attempt with backoff timing.

**Acceptance Scenarios**:

1. **Given** a provider returns a transient 5xx or 429 within the retry budget, **When** the connector retries, **Then** the eventual successful response is returned to the caller and the event log records each attempt with its outcome and backoff delay.
2. **Given** a provider returns an authentication error (401/403) or an invalid-request error (4xx other than 408/425/429), **When** the connector receives the response, **Then** the error is returned to the caller without retry and the event log records a single failed attempt.
3. **Given** the retry budget is exhausted, **When** the next attempt fails transiently, **Then** the connector returns a structured "retry budget exhausted" error including the list of attempts.

---

### User Story 5 — A new provider is added without modifying core (Priority: P3)

A contributor (in-tree or out-of-tree, including in the commercial enterprise build) adds support for a new provider by implementing the connector adapter contract and registering it. No changes are required to the core packages outside the provider's own adapter package. The same applies to providers added in private enterprise modules.

**Why this priority**: This protects the architectural integrity invariant (charter DIRECTIVE_001) and the open-source / enterprise distribution split. Without it, every new provider becomes a core change, every enterprise provider forks core, and the configuration-first promise rots.

**Independent Test**: A throwaway "echo" provider is implemented in a separate package and registered. It is then declared in a bundle, and an agent successfully invokes it — all without any commit touching the existing `core/llm/...` interface package or any other `core/` package.

**Acceptance Scenarios**:

1. **Given** a new provider implements the adapter contract and registers itself, **When** a bundle declares it by kind, **Then** it can be resolved and invoked through the same configuration shape as the day-one providers.
2. **Given** an attempt to add a new provider that requires modifying the shared `core/llm` interface or any other `core/` package, **When** the change is reviewed, **Then** the review flags the architectural-integrity violation before merge.

---

### Edge Cases

- A provider stops streaming midway with no terminal event (network drop): connector treats this as a transient failure subject to retry, and the event log records the partial stream and the drop.
- A request asks for a capability the chosen provider supports but the specific configured model does not (e.g., vision on a text-only OpenAI model): the connector returns a typed "capability unsupported by model" error before incurring cost, distinguishing model-level from provider-level support.
- Two providers in the same bundle reference the same credential source (e.g., the same `OPENROUTER_API_KEY`): the connector resolves the credential once per request, never duplicates it in the event log, and never logs the resolved value.
- A bundle declares a Bedrock provider with an `aws_profile` reference but no AWS region in scope: the connector fails resolution before any model call with an actionable "region not configured" message.
- A request opts into prompt caching against a provider that does not support it: the connector returns the response with caching disabled and surfaces a non-fatal warning entry in the event log; the request is not failed.
- The harness machine goes offline (laptop closed) during a long-running streaming response: the in-flight request is cancelled cleanly on resume, event log is consistent, and the scheduler may retry per the session's policy (out of scope for this spec, but the connector must not block resume).
- A tool call response from the model references a tool the bundle does not export: the connector returns the tool-call to the caller (it is the caller's responsibility to decide whether to execute it); the event log records the unknown-tool reference for audit.

## Requirements *(mandatory)*

### Functional Requirements

| ID | Title | User Story | Priority | Status |
|----|-------|------------|----------|--------|
| FR-001 | Day-one provider coverage | As a bundle author, I want first-class support for Anthropic (direct), OpenAI (direct), OpenRouter, AWS Bedrock, and local Ollama so that I can target any common deployment without writing provider-specific code. | High | Open |
| FR-002 | Named provider profiles in bundle config | As a bundle author, I want to declare named provider profiles (kind, model, capability hints, credential reference) in bundle configuration so that agents reference providers symbolically rather than by raw connection details. | High | Open |
| FR-003 | Indirect credential resolution only | As an operator, I want credentials supplied only via indirect references (environment variable, OS keychain, encrypted file, AWS profile) so that no plaintext key ever lives in bundle source, the event log, or process arguments. | High | Open |
| FR-004 | Unified streaming chat completion | As an agent author, I want a single request shape that streams completions from any configured provider so that I do not branch on provider in agent code. | High | Open |
| FR-005 | Multi-turn conversation history | As an agent author, I want to pass an ordered message history (system, user, assistant, tool) and have it correctly translated into each provider's native conversation format. | High | Open |
| FR-006 | Tool / function calling | As an agent author, I want to declare callable tools and receive structured tool-call requests from the model, then continue the conversation with tool results, against any provider that supports tool calling. | High | Open |
| FR-007 | Vision / image input | As an agent author, I want to attach images to messages and have them delivered to providers that support vision input. | High | Open |
| FR-008 | Structured output (JSON mode) | As an agent author, I want to request structured JSON output (with a schema where the provider supports schema-bound output) so that downstream code does not have to parse free-form text. | High | Open |
| FR-009 | Prompt caching opt-in | As an agent author, I want to mark cacheable message prefixes and have the connector pass that intent through to providers that support prompt caching, so that repeated context is not re-billed and not re-sent unnecessarily. | High | Open |
| FR-010 | Reasoning / thinking blocks | As an agent author, I want to opt into extended reasoning on providers that expose it (Claude extended thinking, reasoning-capable models via OpenRouter or Bedrock) and receive reasoning blocks distinct from final answer content. | Medium | Open |
| FR-011 | Token usage and cost reporting | As an operator, I want every completed request to report input tokens, output tokens, cached tokens (where applicable), reasoning tokens (where applicable), and a derived cost in operator's reporting currency, so that I can attribute spend per session, per agent, and per provider. | High | Open |
| FR-012 | Mid-stream cancellation | As an agent author, I want to cancel an in-flight streaming request and have the upstream connection closed promptly, so that runaway generations do not consume budget or block the runtime. | High | Open |
| FR-013 | Unsupported-capability errors are first-class | As an agent author, I want a typed "capability unsupported by provider/model" error returned before any model call when I request a capability the target cannot fulfill, so that I never pay for a request that was guaranteed to fail. | High | Open |
| FR-014 | Append-only event log integration | As an operator, I want every request, every stream chunk, every response, every retry, every error, and every cancellation written to the harness event log as immutable append-only entries, so that any session can be replayed, audited, or branched. | High | Open |
| FR-015 | Credential redaction in event log | As an operator, I want credentials and known-credential-shaped substrings redacted from event log entries before they are persisted, so that the log is safe to share, archive, and audit. | High | Open |
| FR-016 | Per-provider retry with exponential backoff and jitter | As an operator, I want transient provider failures retried automatically with exponential backoff and jitter up to a configurable budget, so that single network blips do not surface to bundle authors as failures. | High | Open |
| FR-017 | Distinguish transient from non-transient errors | As an operator, I want the connector to retry transient errors (network, 408, 425, 429, 5xx) and not retry non-transient errors (auth failure, invalid request, content policy refusal), so that retries cannot cause cost amplification on broken requests. | High | Open |
| FR-018 | Provider adapter extensibility | As a contributor (open source or enterprise), I want to add a new provider by implementing a stable adapter contract and registering it, without modifying any other core package, so that the architectural-integrity invariant in the charter is preserved. | High | Open |
| FR-019 | Pre-flight credential resolution | As an operator, I want every configured provider's credential reference resolved (or its resolution failure reported) before any agent call, so that misconfiguration surfaces as a clear startup error and not as an inconsistent runtime failure. | Medium | Open |
| FR-020 | Replay reproducibility | As an operator, I want a recorded request entry to be sufficient to re-issue an equivalent call later (modulo provider non-determinism), so that replay and branching from event log positions are well-defined. | Medium | Open |

### Non-Functional Requirements

| ID | Title | Requirement | Category | Priority | Status |
|----|-------|-------------|----------|----------|--------|
| NFR-001 | Connector overhead per call | Connector-introduced latency overhead (excluding provider time) for a non-streaming call: under 20 ms p95 on a developer laptop. | Performance | High | Open |
| NFR-002 | First-token streaming overhead | Connector-introduced delay between provider first byte and first chunk delivered to caller: under 10 ms p95. | Performance | High | Open |
| NFR-003 | Cancellation responsiveness | Time from caller-issued cancel to upstream connection closed: under 1 second p99. | Performance | High | Open |
| NFR-004 | Event log append latency | Time from event emission to append-acknowledged on local disk: under 5 ms p99 (matches charter performance target). | Performance | High | Open |
| NFR-005 | Pre-flight resolution success rate | Configured providers whose credential reference is resolvable at runtime startup are successfully resolved 100 % of the time; unresolved references are reported with the failing provider id and reference kind 100 % of the time. | Reliability | High | Open |
| NFR-006 | Retry budget effectiveness | A single transient failure followed by success completes within the retry budget at least 99 % of the time for budgets of 3 attempts or more. | Reliability | High | Open |
| NFR-007 | Plaintext credential leakage | Plaintext credential material appearing anywhere in the persisted event log: zero occurrences across the audit suite, including process arguments, request bodies, response bodies, and error messages. | Security | High | Open |
| NFR-008 | Credential-pattern redaction recall | Known credential-shaped patterns appearing in user-supplied request or provider-returned response content: redacted at recall ≥ 99 % across the maintained pattern catalog. | Security | High | Open |
| NFR-009 | Local-first guarantee | Connector emits zero outbound network traffic when no provider is invoked; provider invocation is the only network egress path. | Security | High | Open |
| NFR-010 | Day-one provider parity for streaming text | Streaming text completion is supported on all five day-one providers. | Functional Coverage | High | Open |
| NFR-011 | Day-one provider parity for tool calling | Tool calling is supported against every day-one provider whose API exposes it (Anthropic, OpenAI, OpenRouter, Bedrock for tool-capable models, Ollama for tool-capable local models). | Functional Coverage | High | Open |
| NFR-012 | Audit completeness | For every successfully completed generation request, the event log contains at minimum: request submitted, final response, token usage, latency. Coverage: 100 %. | Auditability | High | Open |

### Constraints

| ID | Title | Constraint | Category | Priority | Status |
|----|-------|------------|----------|----------|--------|
| C-001 | Architectural-integrity boundary | The connector lives behind the existing `core/llm` registry interface. The Wails app, the frontend, and any future hosted backend access the connector only via the existing `core/rpc` surface. New providers must not require modifications to packages outside their own adapter package. | Technical | High | Open |
| C-002 | No inline plaintext credentials | Plaintext credentials are not accepted in any configuration source (bundle YAML/TOML, lockfile, RPC payload, environment-loaded config). Only indirect credential references are accepted. | Security | High | Open |
| C-003 | Append-only event log immutability | Connector-emitted event log entries are never edited or deleted in place. Corrections (e.g., post-hoc redaction expansion) are new entries that reference the prior entry. | Security | High | Open |
| C-004 | Bundle-format compatibility | Provider profile declarations live within the existing bundle configuration format (YAML/TOML with lockfile pinning) and do not introduce a new top-level configuration surface. | Technical | High | Open |
| C-005 | Open-source / enterprise distribution split | Provider adapters that are part of the commercial enterprise build are added via the same adapter contract used by open-source providers, behind build tags or as separate packages — never by forking or shadowing the open-source `core/`. | Business | High | Open |
| C-006 | SOC 2-readiness | Audit, retry, redaction, and configuration behaviors meet the testing and review bar set by the project charter (no direct pushes to main, ≥ 1 review approval, signed/attributed commits, decision documentation for material trade-offs). | Regulatory | High | Open |

### Key Entities

- **Provider Profile**: a bundle author's declaration that names a provider connection. Carries: a stable id used by agents, a kind (anthropic, openai, openrouter, bedrock, ollama, …), a model identifier, an indirect credential reference, optional region/endpoint hints, and optional capability hints used by pre-flight validation.
- **Credential Reference**: an indirect pointer to where a credential lives. One of: environment variable, OS keychain entry, file path (encrypted at rest), or AWS profile name. The connector resolves a reference at request time; the resolved value never enters configuration, the event log, or process arguments.
- **Generation Request**: a provider-agnostic description of one model invocation. Includes the message history, declared tools, attachments, capability opt-ins (caching, JSON mode, reasoning), retry policy override, and a cancellation handle. The same request shape is valid for every provider; the connector translates it to each provider's native protocol.
- **Generation Response / Stream**: the provider-agnostic result of a generation. For non-streaming: final content, tool calls, reasoning blocks, token usage, finish reason. For streaming: an ordered sequence of chunks (text delta, tool-call delta, reasoning delta, usage update, finish event) plus a final summary.
- **Capability Descriptor**: a per-(provider, model) record of which capabilities are supported (streaming, tools, vision, json_mode, prompt_caching, reasoning, cancellation). Used to fail fast on unsupported requests before any provider call.
- **LLM Event**: a typed entry in the harness event log emitted by the connector. Kinds include: request submitted, stream chunk, response final, retry attempted, error, cancelled, capability rejected. Each carries a session id, provider id, model id, and a redacted payload reference.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A bundle author can configure a new model provider (any of the five day-one kinds) and run a working agent against it in under 5 minutes from a clean clone, given valid credentials.
- **SC-002**: A bundle author can switch the active provider for an existing agent definition with zero edits to the agent definition and at most one edit to bundle configuration.
- **SC-003**: 100 % of completed generation requests produce a complete, replayable event-log trail covering request, response, usage, and latency, verified by an audit harness that runs in CI.
- **SC-004**: Zero plaintext credentials appear in the event log, in bundle source, or in process arguments across the full provider matrix, verified by an automated scanner that runs in CI.
- **SC-005**: A single transient provider failure is invisible to the bundle author at default retry settings (success without surfaced error) at least 99 % of the time across the day-one provider matrix.
- **SC-006**: A new provider can be added end-to-end (adapter package, registration, bundle declaration, working agent run) with no commits touching `core/` packages outside the new adapter's own package, verified by a structural review check.
- **SC-007**: Connector overhead does not regress the harness's local performance budget: cold first-call overhead under 50 ms p95, warm steady-state overhead under 20 ms p95.
- **SC-008**: Bundle authors can opt into prompt caching, vision, tool calling, structured output, and reasoning blocks against every day-one provider whose model and API support each capability, with no agent-side branching on provider kind.

## Assumptions

- The bundle format and lockfile mechanism described in the charter are the source of truth for provider profile declarations; this spec does not redefine them, only extends what they carry.
- The harness already provides an append-only event log surface usable by core packages; the connector emits into it but does not own its storage.
- Token-to-cost conversion uses an operator-configurable price table maintained outside this spec; the connector consumes the table to derive the cost field on usage entries but is not responsible for keeping the table accurate.
- Cross-provider fallback (e.g., automatic failover from Anthropic to OpenRouter) is out of scope for v1 and will be addressed in a follow-up spec; per-provider retry is in scope.
- Embeddings, fine-tuning, batch APIs, and image generation are out of scope for v1 and will be addressed in follow-up specs.
- A non-Go HTTP surface (e.g., an OpenAI-compatible HTTP server fronting the connector) is out of scope for v1; in-tree callers reach the connector via the existing `core/rpc` surface.
- The Wails frontend interacts with the connector exclusively through the existing `core/rpc` surface; no new frontend-only transport is introduced by this feature.

## Open Questions

The following decisions materially shape the implementation contract and should be resolved before planning. Each has a working default; flagging them so the resolution is explicit and recorded.

1. **[NEEDS CLARIFICATION]** Tool-call response shape — should the connector normalize tool-call and tool-result blocks into a single unified envelope across providers, or pass them through in provider-native shapes with a thin wrapper that identifies which provider's shape is in use? Default if unresolved: unified normalized envelope, because it preserves the configuration-first promise that an agent definition does not branch on provider; provider-native shapes remain accessible as a raw payload field for advanced cases.
2. **[NEEDS CLARIFICATION]** Cost reporting source — does the connector ship with a built-in starter price table (refreshed manually per release) for the day-one providers' common models, or does it require operators to supply the table from day one? Default if unresolved: ship a starter table for day-one provider common models, with explicit documentation that it is best-effort and operator-overridable.
3. **[NEEDS CLARIFICATION]** Default retry budget — what is the default retry budget when a bundle does not specify one? Default if unresolved: 3 attempts total (initial + 2 retries) with base delay 250 ms, max delay 5 s, full jitter; configurable per provider in bundle config.
