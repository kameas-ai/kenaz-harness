---
work_package_id: "WP09"
title: "AWS Bedrock provider adapter"
dependencies:
  - "WP01"
  - "WP02"
  - "WP03"
  - "WP04"
  - "WP05"
  - "secrets-keychain:WP-aws-profile-resolution"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
  - "T006"
  - "T007"
phase: "Phase 3 - Provider adapters"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP09 – AWS Bedrock provider adapter

## Goal

Implement `core/llm/bedrock` — a `ProviderAdapter` for AWS Bedrock
Runtime supporting streaming chat, multi-turn, tool calling on
tool-capable models, vision (Claude / Nova families), prompt caching
(Anthropic-on-Bedrock), reasoning blocks, usage reporting, and
mid-stream cancellation. AWS SDK code is contained ONLY in this
package; AWS profile and region resolution flow through the upstream
secrets backend.

## Spec references

- FR-001 — Day-one coverage (bedrock).
- FR-003 — Indirect cred ref (`aws_profile`).
- FR-004 / FR-005 — Streaming + multi-turn (Converse API).
- FR-006 — Tool calling on tool-capable Bedrock models.
- FR-007 — Vision on multimodal Bedrock models.
- FR-009 — Prompt caching (Anthropic-on-Bedrock).
- FR-010 — Reasoning blocks (Bedrock-reasoning view).
- FR-011 — Usage reporting.
- FR-012 — Cancellation.
- C-001 / C-005 — SDK isolation (no AWS SDK outside this package).
- Edge case "Bedrock aws_profile + missing region" (R7).

## Plan references

- §2 Architectural Placement — `core/llm/bedrock/`.
- §6.1 secrets-keychain integration — `aws_profile` reference flow;
  Bedrock adapter constructs SDK config via the upstream backend's
  resolver.
- §8 R7 — region presence validated in adapter `Validate()` and at
  preflight.

## Subtasks

- T001 — Add AWS SDK Go v2 modules (`bedrockruntime`, `config`,
  `credentials`) to `go.mod`; assert containment to
  `core/llm/bedrock/`.
- T002 — Implement `Adapter.Kind() = "bedrock"`;
  `Capabilities(model)` from `capabilities/data/bedrock.yaml`,
  honoring per-model variation (Claude family vs. Nova vs. Llama).
- T003 — Implement `aws_profile` resolution: call `core/secrets`
  `Reference.Resolve` for the `aws_profile` reference, build
  `aws.Config` with explicit region from `ProviderProfile.Region`;
  fail typed-error if region is empty (R7).
- T004 — Translate `GenerationRequest` → Bedrock Converse /
  ConverseStream input (system, messages, toolConfig, image content
  blocks, cachePoint markers for Anthropic-on-Bedrock prompt
  caching, additionalModelRequestFields for reasoning).
- T005 — Translate streaming response chunks to `StreamEvent`
  (contentBlockDelta, toolUse, reasoning, metadata for usage).
- T006 — Cancellation: cancel underlying SDK request via context;
  ensure socket close ≤ 1 s p99 against a slow-stream fake (R5).
- T007 — Tests: VCR fixtures (recorded against
  `bedrock-runtime` mock) for streaming hello, tool-call,
  vision, prompt-cache hit on Anthropic-on-Bedrock, reasoning
  blocks, cancellation, missing-region rejection. Live API gated
  by AWS profile + region env vars.

## Acceptance criteria

- `go test ./core/llm/bedrock/...` passes; coverage ≥ 80 %.
- Bedrock profile with empty `region` rejected at preflight with
  typed "region not configured" error before any AWS call (R7
  test).
- Streaming hello-world VCR test produces ordered events through
  the audit emitter.
- Tool-call test returns `ToolUse` matching fixture.
- Prompt-cache test on Anthropic-on-Bedrock model reports
  cached-token counts > 0 on second identical-prefix request.
- Cancellation test: socket close ≤ 1 s p99.
- `core/llm/bedrock/` is the sole AWS SDK importer (verified by
  `go list -deps`).

## Files to create / modify

- `core/llm/bedrock/adapter.go`
- `core/llm/bedrock/translate_request.go`
- `core/llm/bedrock/translate_stream.go`
- `core/llm/bedrock/awsconfig.go` (profile + region wiring)
- `core/llm/bedrock/errors.go`
- `core/llm/bedrock/adapter_test.go`
- `core/llm/bedrock/testdata/*.json`
- `go.mod`, `go.sum`

## Definition of done

- All subtasks complete; tests green; lint clean.
- Adapter self-registers via `init()`.
- Cross-mission dependency on
  `secrets-keychain:WP-aws-profile-resolution` documented in PR.
- PR merged.
