---
work_package_id: "WP01"
title: "Pack format parser and validator (YAML metadata + Markdown entries)"
dependencies: []
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
phase: "Phase 1 - Pack format parser"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP01 – Pack format parser and validator

## Goal

Establish `core/context/pack/` as the on-disk pack ingester. Parse the spec's working-default pack layout (YAML 1.2 manifest + Markdown entries with YAML frontmatter), produce typed `ContextPack` / `ContextEntry` values, and run a validator that enforces schema, naming uniqueness, size budgets, and required signature references — all *before* any provenance call. This is the foundation every downstream WP consumes.

## Spec references

- FR-002 (Named context entries with stable names for override matching)
- FR-013 (Pack authoring surface — YAML + Markdown with frontmatter)
- FR-014 (Pack validation: schema, naming, signing, size budgets)
- FR-015 (Workflow-scoped entry frontmatter parsing prerequisite)
- NFR-002 (Per-layer size budget; default 256 KB)
- C-001 (Logic confined to `core/context/`)
- Key Entities: Context Pack, Context Entry

## Plan references

- §2 (Architectural placement, `core/context/pack/`)
- §3 (Public API illustrative `ContextPack`, `ContextEntry`)
- §4.1 (Pack ingester responsibilities)
- §5.1 (On-disk layout: `pack.yaml`, `entries/<kind>/*.md`, `signatures/`)
- §5.2 (Bundle artifact-kind registration prerequisite — pack handler shape)
- Risk R6 (frontmatter ambiguity → validator must catch)

## Subtasks

- T001 Scaffold `core/context/pack/` package and define `ContextPack`, `ContextEntry`, `EntryKind`, `PackRef`, `Layer` types per plan §3.
- T002 Implement YAML 1.2 manifest parser reusing the parser pin from `core/bundle/`; load `pack.yaml` into a typed manifest.
- T003 Walk `entries/<kind>/*.md`, parse YAML frontmatter (name, kind, scope, tags) + Markdown body; compute per-entry content hash.
- T004 Build the pack validator: required fields, name uniqueness within pack, size-per-layer ceiling (NFR-002), required signature reference field, path-traversal containment (inherited from `core/bundle/` FR-014).
- T005 Table-driven tests with golden pack fixtures: valid org pack, valid team pack with workflow-scoped entries, invalid (duplicate name), invalid (oversize), invalid (missing signature ref).

## Acceptance criteria

- A well-formed pack directory parses to a `ContextPack` whose entries appear in deterministic name order with content hashes computed.
- All validator failure modes return typed errors distinguishable by code (not string match).
- Size-budget overflow yields a structured warning record (do not silently trim here — trimming is policy, WP07).
- Tests run under `go test ./core/context/pack/... -race` and meet ≥80 % coverage per charter testing standards.

## Files to create/modify

- `core/context/pack/pack.go`
- `core/context/pack/parser.go`
- `core/context/pack/validator.go`
- `core/context/pack/types.go`
- `core/context/pack/testdata/...` (golden pack fixtures)
- `core/context/pack/parser_test.go`, `validator_test.go`

## Definition of done

- All subtasks complete and tests passing under `-race`.
- `gofmt`, `goimports`, `go vet`, `golangci-lint run` clean for the package.
- Public types match the contract intent in plan §3 (names may refine).
- No imports outside `core/context/pack/` other than `core/bundle/` YAML primitives and stdlib.
- WP merged to main via squash-merge PR referencing this mission.
