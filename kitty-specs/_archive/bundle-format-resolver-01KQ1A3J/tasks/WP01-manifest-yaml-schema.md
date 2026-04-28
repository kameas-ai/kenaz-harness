---
work_package_id: "WP01"
title: "Manifest YAML 1.2 schema and parser (kaneaz.yaml)"
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
phase: "Phase 1 - Manifest"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP01 – Manifest YAML 1.2 schema and parser (kaneaz.yaml)

## Goal

Establish the canonical bundle manifest format `kaneaz.yaml` as YAML 1.2, with a JSON Schema that validates every field, and a Go parser/validator that emits typed errors. The manifest is the entry point of the bundle layer — every other phase depends on its shape being stable and unambiguous.

## Spec references

- FR-001 Bundle manifest schema
- FR-013 Schema versioning and forward-compatibility hints
- FR-014 Path-traversal protection (manifest-level rejection of artifact paths escaping bundle root)
- NFR-002 Determinism (canonical serialization is the basis of the manifest content hash)
- C-001 Architectural integrity (manifest logic isolated to `core/bundle/manifest/`)
- Edge cases: duplicate `(kind, name)` rejected at validation; `schema_version` newer than supported rejected with structured error.

## Plan references

- Plan §2 stub migration: `manifest.go → core/bundle/manifest/manifest.go`
- Plan §3.3 Manifest API surface (`Parse`, `ContentHash`, `Validate`)
- Plan §5.1 Manifest schema canonical example
- Plan §8 R2 (YAML 1.2 library decision) and R6 (1.1 vs 1.2 interop)
- Plan Open Question 4 (Go YAML 1.2 library — default `goccy/go-yaml`)
- Research D1 (YAML 1.2 + JSON Schema)

## Subtasks

- T001 Pin a YAML 1.2 Go library in `go.mod` (default `github.com/goccy/go-yaml`); document the decision in `core/bundle/manifest/manifest.go` package doc.
- T002 Author `core/bundle/manifest/schema/kaneaz.yaml.schema.json` (JSON Schema 2020-12) covering `schema_version`, `name`, `version` (semver), `license` (SPDX), `metadata`, `dependencies[]`, `artifacts[]` (each with `name`, `kind`, `path`, `content_hash`), `signatures[]`. Mandate quoted strings for short identifiers (Norway mitigation).
- T003 Embed the schema via `go:embed` in `core/bundle/manifest/schema.go`.
- T004 Implement `Parse(data []byte) (*Manifest, error)` that parses with the YAML 1.2 lib, validates against the embedded JSON Schema, parses `schema_version` first and returns `ErrSchemaUnsupported` for unsupported versions before any deeper parse.
- T005 Implement `Validate(opts ValidateOpts) error` enforcing: unique `(kind, name)` per bundle (`ErrDuplicateArtifact`); each `artifact.path` confined to the bundle directory (`ErrPathTraversal`); semver well-formed; SPDX license non-empty.
- T006 Implement `ContentHash() string` over a canonical (sorted-key, fixed-newline) serialization of the manifest fields. Cover with golden-file tests proving byte-stable output across re-parses.

## Acceptance criteria

- A valid `kaneaz.yaml` matching Plan §5.1 parses, validates, and round-trips to a stable `ContentHash`.
- A manifest with `schema_version` higher than the harness max returns `ErrSchemaUnsupported` before any other parse error.
- A manifest with two artifacts sharing `(kind, name)` returns `ErrDuplicateArtifact`.
- A manifest declaring an `artifact.path` containing `..` or absolute prefix returns `ErrPathTraversal`.
- The Norway-problem case (`country: NO`) is parsed as the string "NO", not boolean false.
- JSON Schema is embedded and accessible at runtime; validation errors cite JSON pointer paths.

## Files to create/modify

- `core/bundle/manifest/manifest.go` (new — Manifest struct, ContentHash)
- `core/bundle/manifest/parser.go` (new — Parse)
- `core/bundle/manifest/validate.go` (new — Validate)
- `core/bundle/manifest/schema.go` (new — go:embed wiring)
- `core/bundle/manifest/schema/kaneaz.yaml.schema.json` (new — canonical schema)
- `core/bundle/errors.go` (new or seeded — `ErrSchemaUnsupported`, `ErrManifestInvalid`, `ErrPathTraversal`, `ErrDuplicateArtifact`)
- `go.mod`, `go.sum` (add YAML 1.2 + JSON Schema validator deps)
- Migrate the stub `core/bundle/manifest.go` field shapes here (Plan §2).

## Definition of done

- All acceptance criteria pass via Go unit tests and golden-file fixtures.
- Package compiles with no dependency on any other `core/bundle/` subpackage (except `errors.go`).
- The schema file embeds at build time and round-trips cleanly through `Parse → Validate → ContentHash`.
- Norway-problem regression test present.
- Public API matches Plan §3.3 signatures exactly.
