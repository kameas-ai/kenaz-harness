# Implementation Plan: Artifacts storage

**Branch**: `artifacts-storage-01KQ5K4T` (lane allocated at WP-implement time)
**Date**: 2026-04-26
**Spec**: `kitty-specs/artifacts-storage-01KQ5K4T/spec.md`

## Summary

Add a first-class "artifacts" concept: things the model produced inside the harness, captured automatically, browsable later. Three sources: fenced code blocks with filename hints, tool outputs that return inline file-shaped content, manual "save as artifact" pin. Storage rides on the multimodal-io CAS at `<DataDir>/media/<sha256>` with a refcount that respects both attachments and artifacts. UI: per-session "Artifacts" tab + per-project rollup + global `/artifacts` view + per-message inline chip. Filesystem MCP writes are explicitly NOT artifacts (those are real user files).

## Technical Context

- **Language/Version**: Go 1.22+; TypeScript 5.x.
- **Primary Dependencies**: stdlib only on the Go side. Frontend reuses existing markdown engine (`marked` + `DOMPurify` already vendored) for code/markdown previews. No new third-party deps.
- **Storage**: shared CAS at `<DataDir>/media/<sha256>` (laid down by multimodal-io mission); `artifacts` table; refcount across `artifacts` + `attachments`.
- **Testing**: Go `-race -count=1 -short`; vitest. Code-block detector tests use golden samples. Tool-output detector tests use stub `PostToolUseEvent` payloads.
- **Performance**: NFR-003 / NFR-004 — detector ≤ 50 ms per assistant turn / per tool result. NFR-006 — list query ≤ 100 ms for 500 artifacts.
- **Constraints**: NFR-005 — no artifact bytes outside `<DataDir>`. The HTML preview iframe is sandboxed by default (no `allow-scripts`).

## Charter Check

- DIRECTIVE_001 (no cyclic imports): `core/artifacts/` depends on `core/storage`, `core/attachments` (for the shared MediaStore), `core/session` (for cascade hooks). No reverse deps. Pass.
- C-001 (no third-party SDK in `core/`): stdlib only. Pass.
- Privacy CI invariant: rpc returns metadata + opt-in body bytes via `Get`; no payload leak. Pass.
- Privacy CI #4 (no raw color literals): zero net-new color literals — preview/list use existing token CSS variables.

## Project Structure

```
core/artifacts/
├── artifact.go              # Artifact, ArtifactSourceRef, ArtifactFilter, sentinel errors
├── manager.go               # Manager: CRUD, capture orchestration, refcount sweeps
├── detect_code_block.go     # Fenced-block parser + title-hint heuristics
├── detect_tool_output.go    # Tool-output file-shape detector
├── store.go                 # Store interface
├── store_sql.go             # SQL-backed impl
├── store_mem.go             # In-memory impl for tests
├── manager_test.go
├── detect_code_block_test.go
├── detect_tool_output_test.go
└── store_sql_test.go

core/session/
├── migrations.go            # MODIFIED: migration 0303 (artifacts table) registered
├── migrations_artifacts.go  # NEW: migration body
└── migrations_test.go       # MODIFIED: ledger row 303

core/toolloop/
└── hooks.go                 # MODIFIED minor: artifact detector subscribes via existing post-tool-use hook

core/rpc/views/llm/
└── impl.go                  # MODIFIED minor: post-stream finalize fires code-block detector

core/rpc/views/artifacts/
├── api.go                   # ArtifactsAPI interface
├── impl.go                  # concrete API
└── impl_test.go

core/rpc/views/sessions/
└── impl.go                  # MODIFIED: SaveAsArtifact passthrough; cascade prompt extension

core/rpc/api.go              # MODIFIED: Artifacts() accessor; wire detectors into llm + toolloop hooks
core/rpc/bindings.go         # MODIFIED: Artifacts_* bindings + Sessions_SaveAsArtifact

frontend/src/lib/types.ts                              # MODIFIED: Artifact, ArtifactFilter, ArtifactSourceRef
frontend/src/lib/harnessClient.ts                      # MODIFIED: artifacts namespace

frontend/src/views/sessions/
├── SessionsView.vue                                   # MODIFIED: Artifacts tab
└── __tests__/SessionsView.test.ts                     # MODIFIED

frontend/src/components/chat/
├── MessageBubble.vue                                  # MODIFIED: per-message artifact chip + right-click "Save as artifact"
└── ArtifactChip.vue                                   # NEW

frontend/src/views/projects/ProjectLandingPage.vue     # MODIFIED: Project artifacts section
frontend/src/views/artifacts/
├── ArtifactsView.vue                                  # NEW: /artifacts global view
├── ArtifactPreview.vue                                # NEW
└── __tests__/

frontend/src/shell/LeftRail.vue                        # MODIFIED: Artifacts rail entry under Memory
frontend/src/main.ts                                   # MODIFIED: /artifacts route

docs/artifacts.md                                       # NEW
```

**Structure Decision**: existing harness layout (Go backend + Vue frontend). Adds one new core package (`core/artifacts/`) and one new rpc view (`artifacts/`) plus three new Vue components. Shared CAS layer is reused from multimodal-io.

## Phase 0 — Research summary

- **Code-block title-hint syntax**: prevailing markdown extension uses `title="filename.ext"` after the language tag (Pandoc, mkdocs-material). First-line comment fallbacks (`// file: ...`, `# file: ...`, `<!-- file: ... -->`) are common in Anthropic's own example outputs.
- **MCP file-shaped tool result convention**: per `https://spec.modelcontextprotocol.io/specification/server/tools/`, tool results can return `{type:"image", data, mimeType}` or `{type:"resource", resource:{uri, mimeType, blob}}`. Detect both shapes.
- **HTML iframe sandboxing**: `<iframe sandbox>` with no permissions blocks scripts, plugins, top-level navigation, forms, and pointer-lock. Adding `allow-scripts` enables JS but in a separate origin (no parent-page DOM access). Adding `allow-same-origin` is what enables cookies / localStorage access — never set together with `allow-scripts` for untrusted content. Default: no allow-* flags.
- **CAS refcount**: existing `core/attachments` will need a small extension to expose `RefcountFor(contentHash) int` so artifacts can participate in the same sweep. Multimodal-io's WP02 owns the CAS — coordinate with that mission's media.go author.

## Phase 1 — Schema + storage

**Targets**: `core/artifacts/{artifact.go, store.go, store_sql.go, store_mem.go, store_sql_test.go}`, `core/session/migrations_artifacts.go`, `core/session/migrations.go`.

- Define `Artifact`, `ArtifactSourceRef`, `ArtifactFilter`, sentinel errors (`ErrArtifactNotFound`, `ErrUnsupportedSource`).
- `Store` interface: `Insert`, `Get`, `List(filter)`, `UpdateScope(id, kind, scopeID)`, `Delete(id) (contentHashesToCheck []string)`.
- `NewSQLStore` against the unified `storage.DB`.
- DDL (migration 0303):
  ```sql
  CREATE TABLE IF NOT EXISTS artifacts (
      id              TEXT PRIMARY KEY,
      session_id      TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
      project_id      TEXT NULL REFERENCES projects(id) ON DELETE SET NULL,
      title           TEXT NOT NULL DEFAULT '',
      mime_type       TEXT NOT NULL,
      content_hash    TEXT NOT NULL,
      byte_size       INTEGER NOT NULL,
      source          TEXT NOT NULL CHECK (source IN ('code_block','tool_output','user_pin')),
      source_ref_json TEXT NOT NULL DEFAULT '{}',
      scope_kind      TEXT NOT NULL DEFAULT 'session' CHECK (scope_kind IN ('session','project')),
      created_at      INTEGER NOT NULL
  );
  CREATE INDEX IF NOT EXISTS idx_artifacts_session ON artifacts (session_id, created_at DESC);
  CREATE INDEX IF NOT EXISTS idx_artifacts_project ON artifacts (project_id, created_at DESC) WHERE project_id IS NOT NULL;
  CREATE INDEX IF NOT EXISTS idx_artifacts_content_hash ON artifacts (content_hash);
  ```
- Tests cover: insert, list by filter, scope-update, delete (returns content_hashes for the caller to refcount-sweep), session-cascade behavior.

**Dependencies**: multimodal-io WP01 + WP02 (Message.Content shape + media CAS).

## Phase 2 — Capture detectors

**Targets**: `core/artifacts/{detect_code_block.go, detect_tool_output.go}` + tests, `core/artifacts/manager.go`.

- **Code-block detector**:
  - Input: full assistant message text + message ID.
  - Walk the markdown AST (use the `marked`-equivalent if available on the Go side; otherwise hand-rolled fenced-block scanner — markdown fences are simple enough for a 60-line scanner).
  - For each fenced block: extract title hint (regex order from FR-004), check thresholds (10 lines OR 200 bytes), produce a candidate.
  - Returns `[]CaptureCandidate{ Title, MimeType (inferred from extension), Content, SourceRef }`.
  - Mime inference: `.html → text/html`, `.go → text/x-go`, `.py → text/x-python`, `.md → text/markdown`, default `text/plain`.
- **Tool-output detector**:
  - Input: `PostToolUseEvent { Tool, Server, Result }`.
  - Try-parse `Result` as JSON; check shape:
    - `{type:"image", data:"<base64>", mimeType:"<mime>"}` → capture.
    - `{type:"resource", resource:{uri, mimeType, blob}}` → capture (blob is base64).
    - `{content:[{type:"image"|"file", ...}]}` → capture each.
  - Returns `[]CaptureCandidate`.
- **Manager.Capture(candidates, sessionID, projectID)**:
  - For each candidate: write bytes to MediaStore (CAS dedup); insert artifact row via Store.
  - Returns inserted artifact IDs.

Tests:
- Code-block detector golden samples: title-hint variants (attr, comment styles), threshold rejection, multiple blocks per message, no-title-hint passthrough.
- Tool-output detector: image-shape, resource-shape, content-array shape, plain-string-result skip.

**Dependencies**: Phase 1.

## Phase 3 — Hook wiring

**Targets**: `core/rpc/views/llm/impl.go`, `core/rpc/api.go`, `core/toolloop/hooks.go`-adjacent integration via the rpc layer.

- `core/rpc/views/llm/impl.go`: at the assistant-message-finalize site (after the streaming pump completes WITHOUT a tool_use finish), invoke `artifacts.Manager.OnAssistantMessage(ctx, sessionID, messageID, text)` which runs the code-block detector and inserts artifacts. Wired through a thin interface `ArtifactSink interface { OnAssistantMessage(...) }` to keep llm impl independent of the artifacts package.
- Toolloop post-hook: the existing `HookRunner.RunPostToolUse` surface ALREADY emits `PostToolUseEvent` to whatever runner is wired. The artifacts package implements a `HookRunner`-compatible adapter or registers a side-effect listener — choose whichever lands cleaner without modifying `core/toolloop/`. Likely path: add a `PostToolUseListener` channel on the rpc-side runner, the artifacts manager subscribes.
- Settings: `Settings.AutoCaptureCodeBlocks`, `CodeBlockMinLines`, `CodeBlockMinBytes`, `AutoCaptureToolOutputs` plumbed through `core/rpc/views/settings/`. The detectors read the live values.

**Dependencies**: Phase 2.

## Phase 4 — RPC view + bindings

**Targets**: `core/rpc/views/artifacts/{api.go, impl.go, impl_test.go}`, `core/rpc/api.go`, `core/rpc/bindings.go`, `core/rpc/views/sessions/impl.go`.

- `ArtifactsAPI` per FR-007. `Get(id)` returns `(Artifact, []byte, error)` — bytes are base64-encoded on the wire.
- `Promote(id, "project", projectID)` validates the artifact's session has that project; updates `scope_kind` + `project_id`. Move semantics (no original is preserved as a copy).
- `Delete(id)` removes the artifact row, then asks `MediaStore.RefcountFor(contentHash)`; if zero, removes the file. Atomic: transaction wrapping the delete + the refcount check.
- `Sessions_SaveAsArtifact(sessionID, messageID, title, rangeStart, rangeEnd)` binding lives on the existing sessions view, calls into the artifacts manager. Returns the inserted artifact.
- Cascade extension on `Sessions.Delete(sessionID, options)`: when `options.deleteArtifacts` is unset, the rpc returns the artifact count for the frontend to prompt. When set explicitly, performs the cascade.

**Dependencies**: Phase 3.

## Phase 5 — Frontend list / preview / chip

**Targets**: `lib/types.ts`, `lib/harnessClient.ts`, `SessionsView.vue` (Artifacts tab), `ArtifactPreview.vue` (new), `MessageBubble.vue` (chip + right-click), `ArtifactChip.vue` (new), `ArtifactsView.vue` + route, `LeftRail.vue` rail entry.

- TypeScript types per FR-007 wire shape.
- `client.artifacts.{list, get, promote, delete, saveFromMessage}`.
- **SessionsView.vue Artifacts tab**: sub-tab next to ResolvedContextPanel. Lists artifacts where `session_id == activeSessionID`. Sort dropdown (created-at desc default). Click → `ArtifactPreview.vue` modal.
- **ArtifactPreview.vue** modal renders by mime per FR-010. HTML preview uses `<iframe sandbox="">` (no allow flags) by default; "Run scripts" toggle adds `allow-scripts`. Footer: Download (Wails `SaveFileDialog`), Copy, Promote-scope dropdown, Delete.
- **MessageBubble.vue chip**: when `message.artifactIDs.length > 0`, render `ArtifactChip.vue` row below the text. Right-click anywhere on the message bubble exposes "Save as artifact" with title prompt.
- **ProjectLandingPage.vue** Project artifacts section.
- **ArtifactsView.vue** at `/artifacts`: filters (scope, mime-type-prefix), table.
- **LeftRail.vue** rail entry: "Artifacts" (icon `Package` or `Archive` from Lucide; add to `icons.ts`) under Memory. Route `/artifacts`.

Tests: each new component gets a vitest file. SessionsView test extended to cover the new tab.

**Dependencies**: Phase 4.

## Phase 6 — Polish + docs

**Targets**: `docs/artifacts.md`, e2e tests, refcount-orphan sweep extension.

- `docs/artifacts.md`: walkthrough (US1 → tic-tac-toe.html flow), capture rules, settings reference, manual pin instructions, security note on HTML preview sandboxing, troubleshooting.
- E2E test gated `-tags=integration`: stub LLM returns ` ```html title="x.html" ... ``` `; assert artifact appears in the rpc list within the session and that `MediaStore` has a CAS file for it.
- Refcount-orphan sweep at boot: walk `<DataDir>/media/`, drop files whose hash isn't referenced by attachments OR artifacts. Multimodal-io shipped the boot-sweep; this mission extends the predicate.
- Tutorial-style "First artifact" notice: the first time a user receives an artifact, an unobtrusive toast points to the new tab.

**Dependencies**: all earlier phases.

## Work-package breakdown (proposed)

- **WP01 — Schema + storage + detectors** (Phases 1, 2). Pure backend additive. Lands `core/artifacts/` package, migration 0303, both detectors with unit tests. No wiring into hooks yet — detectors are library-shaped.
- **WP02 — Hook wiring + RPC view** (Phases 3, 4). Wires detectors into the assistant-finalize and post-tool-use sites. Lands `Artifacts_*` bindings + `Sessions_SaveAsArtifact`. Cascade prompt extension. End-to-end backend after this WP.
- **WP03 — Frontend list / preview / chip** (Phase 5). Artifacts tab + preview modal + per-message chip + global view + rail entry.
- **WP04 — Polish + docs** (Phase 6). E2E test, refcount-sweep extension, tutorial toast, `docs/artifacts.md`.

DAG: WP01 → WP02 → WP03 → WP04.

## Risk register

| Risk | Phase | Mitigation |
|---|---|---|
| Code-block detector firing on unintended content (e.g., model returns a `.css` snippet for explanation; we capture it) | 2 | Threshold-based gate (10 lines OR 200 bytes). Settings toggles for both axes. User can manually delete unwanted artifacts. |
| HTML preview XSS via `allow-scripts` opt-in | 5 | Default no allow-* flags. "Run scripts" requires explicit click + a clear warning banner. Document in `docs/artifacts.md`. |
| Tool-output detector mis-classifying a long string as a "file" | 2 | Strict shape match — only `{type:"image"\|"resource"\|"file"}` JSON shapes or `data:...;base64,...` URLs. Plain text returns are NEVER captured. |
| Capture races streaming finalization | 3 | Detector runs AFTER message persistence; never on partial content. Stream pump emits a single "finalized" event the artifact manager subscribes to. |
| CAS refcount race when two simultaneous deletes target the same hash | 4 | Single transaction wraps row delete + refcount-check. Either both rows present (skip file delete) or both gone (remove file). |
| Title-hint sanitization missed → display includes path separators | 6 | Sanitize in display only (`replace(/[/\\]/g, '_')`). Filename is metadata; never used in filesystem operations. |
| Boot-time orphan sweep deletes artifacts the harness hasn't loaded yet | 6 | Predicate checks `attachments` + `artifacts` tables; no race because the sweep runs AFTER `Storage()` opens (sql sees committed state). |
| Settings live-tuning loop — user toggles capture off mid-stream | 3 | Detector reads `Settings.AutoCaptureCodeBlocks` ON ENTRY of `OnAssistantMessage`. Mid-stream toggles take effect on the next message. |

## Open questions

(Restated from spec.md §11.)

1. Code-block title-hint syntax — `title="filename"` after lang tag + first-line comments. Open: also accept `<file>...</file>` blocks at model discretion?
2. Per-tool capture opt-out — deferred to post-launch.
3. Threshold tuning — start at 10 lines / 200 bytes; revisit.
4. Promote semantics — move (not copy). Defer "copy on promote" until requested.
5. **NEW** — Should artifacts be searchable inside the `/contexts` library tree as read-only entries? Use case: model generates a research doc, user wants to attach it to a future session as context. Workaround for v1: download → re-add via the library import flow. First-class "promote artifact to context library" is a follow-up.
