---
work_package_id: "WP03"
title: "Configuration schema for mcp.server.* and validation"
dependencies:
  - "WP01"
planning_base_branch: "feat/wire-integration"
merge_target_branch: "feat/wire-integration"
branch_strategy: "Planning artifacts were generated on feat/mcp-plans; completed changes merge into feat/wire-integration."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 1 - Core skeleton"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP03 – Configuration schema for mcp.server.* and validation

## Goal

Define the top-level `mcp.server.*` configuration block, parse it from
the harness config store (`core/config`), and validate it. Wire
loopback-bind invariant, bearer-token credential reference shape, and
per-tool enable flags.

## Spec references

- FR-002 — Top-level configuration `mcp.server.{stdio,http}`.
- FR-016 — Origin / allowlist policy on streamable-HTTP.
- FR-017 — Optional bearer-token authentication.
- C-002 — No inline plaintext credentials.
- C-006 — Local-first; HTTP defaults OFF and loopback-only.
- NFR-010 — Refuses non-loopback bind without explicit opt-in.

## Plan references

- §5 Data Model §1 — top-level config shape.
- §5 Data Model §2 — bearer-token cred ref.
- Risk R2 — loopback default protection.

## Subtasks

- T001 — Define `Config` struct in `core/mcp/server/config.go` with
  `Stdio` and `HTTP` sub-structs, `Limits`, `Tools`, `Roots`,
  `Sampling` sub-structs.
- T002 — Implement `Validate(cfg Config) error` in
  `core/mcp/server/config_schema.go`: enforce loopback-only bind by
  default, reject inline plaintext bearer tokens, enforce absolute
  paths for roots, sane numeric ranges on limits.
- T003 — Implement `LoadConfig(store config.Store) (Config, error)` —
  parse the config block from the harness config store; populate
  defaults; run validation.
- T004 — Tests: each validation rule with positive + negative cases;
  default population test (loaded with empty config returns full
  defaults); inline-plaintext-bearer-rejection test.

## Acceptance criteria

- `go test ./core/mcp/server/...` passes; coverage ≥ 80 % on config
  surface.
- Defaults match plan §5 §1: `http.enabled = false`, `bind = 127.0.0.1:0`,
  `allowed_origins = [null, localhost, 127.0.0.1]`, `drain_timeout_ms =
  30000`, `max_concurrent_sessions = 16`, `sampling.max_depth = 4`.
- Negative test: `mcp.server.http.bind = "0.0.0.0:8080"` without
  `allow_non_loopback: true` is rejected.
- Negative test: `mcp.server.http.auth.bearer_token = "Bearer abc"`
  (inline plaintext) is rejected with a clear message citing C-002.

## Files to create / modify

- `core/mcp/server/config.go`
- `core/mcp/server/config_schema.go`
- `core/mcp/server/config_test.go`

## Definition of done

- All subtasks complete; tests green; lint clean.
- PR merged into `feat/wire-integration`.
