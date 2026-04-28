---
work_package_id: "WP02"
title: "Lockfile TOML schema and reader/writer (kaneaz.lock, Cargo-flavored)"
dependencies:
  - "WP01"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
phase: "Phase 2 - Lockfile"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP02 – Lockfile TOML schema and reader/writer

## Goal

Define and implement the `kaneaz.lock` lockfile at the project root: TOML, Cargo-flavored, schema-versioned, byte-deterministic. The lockfile is the source of truth for reproducible resolution — every downstream phase reads or writes it.

## Spec references

- FR-004 Lockfile generation and consumption
- FR-005 Deterministic resolution
- FR-013 Schema versioning
- NFR-002 Determinism rate (100% byte-identical)
- C-005 SOC 2 readiness (lockfile is auditable evidence)
- US2 acceptance scenarios 1 and 3 (identical inputs → identical graph; pinned hash beats new published version)

## Plan references

- Plan §2 stub migration `lockfile.go → core/bundle/lockfile/lockfile.go`
- Plan §5.2 Lockfile schema canonical example (TOML)
- Plan §8 R3 lockfile merge conflicts (resolve-conflicts deferred to v1.x — emit clear pointer)
- Plan Open Question 7 (tie-breaker on identical `(name, version, source)` falls back to `content_hash`)
- Research D6 (Cargo-flavored, deterministic sort, schema_version, universal model reserved)

## Subtasks

- T001 Define `Lockfile`, `LockedBundle`, `LockedArtifact` Go types matching Plan §5.2 (`schema_version`, `[[bundle]]`, nested `[[bundle.artifact]]`, reserved `[universal]`).
- T002 Implement `Read(data []byte) (*Lockfile, error)` validating `schema_version` is in `[1, current]`; reject newer versions with `ErrSchemaUnsupported`.
- T003 Implement `Write(lf *Lockfile) ([]byte, error)` using a canonical TOML serializer: byte-wise sort `[[bundle]]` by `(name, version, source)` (tie-break on `content_hash`), `[[bundle.artifact]]` by `(name, kind)`, fixed key order, fixed indentation, mandatory trailing newline.
- T004 Provide `ContentHash() string` over the canonical bytes; golden-file tests prove round-trip stability across two machines (cross-OS line-ending neutrality).
- T005 Stub `merge.go` exposing `func ResolveConflicts(...) error { return ErrNotImplementedV1x }`; emit a clear error message pointing operators at the deferred `kaneaz lock --resolve-conflicts` UX.
- T006 Migrate the existing stub `core/bundle/lockfile.go` field shapes; preserve any compatible field names.

## Acceptance criteria

- A round-trip `Read → Write` of a canonical lockfile is byte-identical.
- Two unsorted lockfile inputs with identical content produce byte-identical canonical output.
- `schema_version = 999` returns `ErrSchemaUnsupported`.
- Tie-break test: two `[[bundle]]` entries differing only in `content_hash` sort deterministically by hash.
- Trailing newline is always present; no CRLF on any platform.
- `[universal]` table parses but is not populated in v1.

## Files to create/modify

- `core/bundle/lockfile/lockfile.go` (new — types)
- `core/bundle/lockfile/canonical.go` (new — byte-wise canonical sort + serialization)
- `core/bundle/lockfile/schema.go` (new — version constants, migration table)
- `core/bundle/lockfile/merge.go` (new — stub for v1.x resolve-conflicts)
- `core/bundle/errors.go` (extend — `ErrLockfileInvalid`, `ErrNotImplementedV1x`)
- `go.mod` (add a TOML library — e.g., `github.com/pelletier/go-toml/v2`)

## Definition of done

- Round-trip and determinism golden tests pass on Linux, macOS, and Windows runners (or in CI matrix).
- `core/bundle/lockfile/` has zero imports from other `core/bundle/` subpackages besides `errors`.
- Stub `merge.go` returns the deferred error with operator-actionable text.
- Public API matches Plan §3 expectations (used by resolver in WP09).
