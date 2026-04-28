# Spec: `__edit_file` artifact sync

**Status**: draft · **Owner**: alecfeeman

## 1. Why

After `1d7679e` the filesystem `__write_file` tool produces an artifact. But `__edit_file` doesn't — its args carry only the diff (oldText/newText), not the full new content. When the agent edits a previously-saved artifact in place, the artifact row's content_hash now lags the on-disk file. Two undocumented states for the same file.

## 2. Goals

- When `__edit_file` succeeds, re-read the file from disk and update the matching artifact row (or create one if absent).
- Match logic: lookup by canonicalised file path against artifact `SourceRef.Filename` (or new `SourceRef.AbsolutePath`).
- Update mechanism reuses the `update-artifact-tool` mission's revision pipeline.

## 3. Functional requirements

| ID | Requirement | Status |
|---|---|---|
| FR-001 | `OnPostToolMessage` recognises `__edit_file` tools (any `<server>__edit_file` suffix). | proposed |
| FR-002 | On match, the sink reads the file from disk and emits an update candidate. | proposed |
| FR-003 | Lookup matches an existing artifact by `SourceRef.AbsolutePath`; new field on `ArtifactSourceRef`. | proposed |
| FR-004 | If no existing artifact matches, a new artifact row is created (parity with `__write_file`). | proposed |
| FR-005 | If the file write itself fails, no artifact mutation occurs (tool result drives the decision). | proposed |
| FR-006 | Path-based matching is case-sensitive; symlinks resolved via `EvalSymlinks` before lookup. | proposed |

## 4. Success criteria

- Agent edits a previously-saved markdown three times → one artifact row with 4 revisions (initial save + 3 edits).
- Edit to a never-saved file produces a fresh artifact row with revision 1.
