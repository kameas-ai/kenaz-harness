---
work_package_id: "WP03"
title: "Server-initiated requests — roots, sampling, log, progress"
dependencies:
  - "WP01"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Branch wp03-stdio-server-initiated off main; merge back when WP03 acceptance gate passes."
subtasks:
  - "T013"
  - "T014"
  - "T015"
  - "T016"
  - "T017"
phase: "Phase 4 — Server-initiated requests"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-26T16:35:00Z"
    agent: "system"
    action: "WP prompt authored manually after Plan agent could not write files."
---

# Work Package Prompt: WP03 — Server-initiated requests

## Goal

Wire the four server→client surfaces: `roots/list` (always answer), `sampling/createMessage` (gated per-recipe, default off, translates onto `core/llm`), `notifications/message` (logging → slog), `notifications/progress` (broker → `mcp:progress` topic). After this WP, an MCP server can request a sampling completion or list our roots, and the result reaches the model via the existing chassis surfaces.

## Spec / plan references

- Spec: §FR-015, FR-016, FR-017.
- Plan: Phase 4.
- Research: §"sampling/createMessage pass-through", §"roots/list response", §"notifications/message → slog", §"notifications/progress → chassis stream".

## Prerequisites

WP01 merged (framer, protocol shapes, response router, server lifecycle).

## Subtasks

- **T013 — `core/mcp/stdio/roots.go`** —
  ```go
  type Root struct { URI string; Name string }
  type RootsHandler interface { Roots(ctx context.Context) []Root }
  func DefaultRoots(dataDir string, projectRoot func() string) RootsHandler
  ```
  Default impl returns `file://<dataDir>` plus `file://<projectRoot()>` when projectRoot is non-nil and returns a non-empty path. Reader loop in `server.go` (added in WP01) routes incoming `roots/list` requests through this handler and writes the response back through the framer.

- **T014 — `core/mcp/stdio/sampling.go`** —
  ```go
  type SamplingRequest struct { Messages []SamplingMessage; SystemPrompt string; MaxTokens int; Temperature float64; StopSequences []string; ... }
  type SamplingResponse struct { Role string; Content SamplingContent; Model string; StopReason string }
  type SamplingHandler interface { CreateMessage(ctx context.Context, req SamplingRequest) (SamplingResponse, error) }
  ```
  Provide `LLMSamplingHandler` that adapts onto `core/llm.Registry`:
  - Selects the active provider+model (the same one the user is chatting with). For now, accept a `func() (provider, model string)` injection so the handler doesn't reach into rpc-state.
  - Builds a `corellm.GenerationRequest` with the server's `messages`, `systemPrompt`, `maxTokens`, `temperature`, `stopSequences`. Drains streaming events synchronously into a single response.
  - Returns the response shaped as `SamplingResponse`.
  - **Per-server gate**: `(*ServerInstance).samplingOn` flag (default false) wraps the handler. When `false`, the pool's request-dispatch path responds with `RPCError{Code: -32601, Message: "sampling disabled for this server"}` without invoking the handler.
  - `(*Pool).SetSamplingEnabled(serverID string, on bool)` — exposed for the rpc surface in WP05.

- **T015 — `core/mcp/stdio/log.go`** — `LogSink` handles incoming `notifications/message`:
  - Maps the spec's level strings (`debug|info|notice|warning|error|critical|alert|emergency`) onto slog levels (debug → Debug; info/notice → Info; warning → Warn; error/critical/alert/emergency → Error).
  - Calls `logger.Log(ctx, level, "mcp."+recipeID+".message", "mcp.recipe", recipeID, "mcp.level", originalString, "mcp.logger", payload.logger, "mcp.data", payload.data)`.
  - Wired in WP01's reader-loop notification dispatch.

- **T016 — `core/mcp/stdio/progress.go`** — `ProgressForwarder` handles `notifications/progress`:
  - Maintains `map[int64]string` (request id → progressToken) on the `*ServerInstance`.
  - On incoming progress notification, looks up the request id by matching token, then publishes onto the broker:
    ```go
    type ProgressEvent struct { Server string; Tool string; RequestID int64; Progress float64; Total *float64; Message string }
    ```
  - Broker is `EventPublisher interface { Publish(topic string, payload any) error }` — wired in WP01's PoolOptions. Topic: `mcp:progress`.
  - Pool-level: when a `Call` registers a request, also register the progressToken (caller-supplied via `_meta.progressToken` field — accept it through `Call`'s envelope shape; if not present, no progress correlation).

- **T017 — Tests** —
  - `roots_test.go`: stub `RootsHandler`; fake server emits `roots/list` request; assert response includes `file://<dataDir>` and project root.
  - `sampling_test.go`:
    - Stub `corellm.Registry` returning a deterministic completion. Fake server emits `sampling/createMessage`; assert request shape is mapped (messages, system prompt, maxTokens). Assert response shape returned to the server.
    - Gate test: `samplingOn=false` → server gets `-32601`; handler is NOT invoked (use a counter on the stub).
  - `log_test.go`: capture slog (testlog handler); fake server emits `notifications/message{level:"warning", data:{message:"x"}}`; assert slog record has the right level, attrs, and message key.
  - `progress_test.go`: stub broker; fake server emits a tool/call response that includes a `notifications/progress` mid-flight; assert broker received a `ProgressEvent` keyed correctly.

## Acceptance

- `go test -race -count=1 -short ./core/mcp/stdio/...` passes including WP01 + WP02 + new tests.
- A9 from spec: server-initiated `sampling/createMessage` (with the gate ON) returns an LLM completion via the harness's active provider.
- A10 from spec: `roots/list` returns `file://<DataDir>` plus the active project root if one is set.

## Constraints

- Sampling default is **off per recipe**. Document the cost-amplification risk in any inline comment if necessary; never ship samplingOn=true as the default.
- Don't bind the sampling handler directly to a specific provider. Use `core/llm.Registry`'s active-provider selector via a function injection.
- LogSink and ProgressForwarder are best-effort — drop with a debug log if a notification can't be parsed; never crash the reader.
- Independent of WP02 (resilience). Can land in either order after WP01.

## Branch strategy

Branch `wp03-stdio-server-initiated` off `main`.
