# Spec: Built-in `update_artifact` tool

**Status**: draft · **Owner**: alecfeeman

## 1. Why

`save_artifact` is immutable per its mission contract: each call produces a new row. For deliverables the agent iterates on (a draft document growing over multiple turns), the user accumulates N artifact rows for the same conceptual file. We need an explicit "update this artifact" tool so iteration produces a single coherent timeline.

## 2. Goals

- New built-in `kaneaz__update_artifact(artifact_id, content[, mime_type, title])`.
- Behaviour: writes a new content_hash for the existing artifact row, preserving `id` + `created_at`. The previous content stays in CAS (referenced by the artifact's revision history).
- Revision history surfaced in the artifact preview UI (timeline of versions).
- Tool result: `{"artifact_id":"...","revision":N,"size":bytes}`.
- Cedar policy gate applies.

## 3. Functional requirements

| ID | Requirement | Status |
|---|---|---|
| FR-001 | Package `core/builtins/updateartifact/` mirrors `core/builtins/saveartifact/`. | proposed |
| FR-002 | Tool requires `artifact_id`; resolves via `coreart.Manager.Get` and rejects with `IsError: true` on not-found. | proposed |
| FR-003 | New schema: `artifacts_revisions` table (artifact_id, revision_no, content_hash, mime_type, title, byte_size, created_at). | proposed |
| FR-004 | `coreart.Manager.Update(id, candidate)` writes a new revisions row + flips the artifact's `latest_revision` pointer. | proposed |
| FR-005 | The pruner skips both the artifact row and all its revisions for tool-output sources. | proposed |
| FR-006 | ArtifactPreview UI gains a "history" tab listing revisions with diff-on-click. | proposed |
| FR-007 | The save_artifact tool surface is unchanged; choice between save vs update is the model's. | proposed |

## 4. Success criteria

- An iterating chat session that saves+updates a markdown 5 times produces 1 artifact row with 5 revisions.
- Revisions visible in the preview UI with timestamps; rolling back to revision N restores that content.
