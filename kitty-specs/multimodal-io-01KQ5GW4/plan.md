# Implementation Plan: Multimodal I/O

**Branch**: `multimodal-io-01KQ5GW4` (lane allocated at WP-implement time)
**Date**: 2026-04-26
**Spec**: `kitty-specs/multimodal-io-01KQ5GW4/spec.md`

## Summary

Bring images + PDFs into the chat surface across all three provider adapters (Anthropic / OpenAI / Bedrock). The work is provider-level, not MCP — each adapter serializes the per-provider content-block shape (Anthropic `image`/`document`, OpenAI `image_url`, Bedrock `image`/`document`). Storage is content-addressable under `<DataDir>/media/<sha256>`. Capability gating reuses the existing `core/llm/capabilities/` registry. Frontend gets paperclip + drag-drop on `ChatInput.vue` and inline rendering on `MessageBubble.vue`.

## Technical Context

- **Language/Version**: Go 1.22+; TypeScript 5.x.
- **Primary Dependencies**: stdlib `crypto/sha256`, `encoding/base64`. Frontend uses `FileReader` / `createObjectURL`. No new third-party deps.
- **Storage**: `<DataDir>/media/<sha256>` filesystem CAS; SQLite metadata table `media_artifacts`; `attachments.media_id` FK.
- **Testing**: Go `-race -count=1 -short`; vitest for frontend. Provider adapter tests use `httptest.Server` to capture body shapes.
- **Performance**: NFR-003 — 5 MiB image round-trip < 2 s. NFR-004 — stream from disk to request body.
- **Scale/Scope**: per-message attachment cap 20 MiB; per-session no hard cap (the user can bound it via the existing `Settings → Storage` view).

## Charter Check

- DIRECTIVE_001 (no cyclic imports): `core/attachments/media.go` depends on `core/storage`. `core/llm` doesn't import `core/attachments` — adapters receive bytes by value via `MediaSource.Data`. Pass.
- C-001 (no third-party SDK in `core/`): stdlib only. Pass.
- Privacy CI invariant: media bytes are kept on disk under `<DataDir>` and stream into the provider request body; the rpc surface returns metadata only. Pass.
- Privacy CI #4 (no raw color literals): zero net-new color literals — image rendering uses existing token CSS variables.

## Project Structure

```
core/llm/
├── llm.go                              # MODIFIED: ContentBlock, MediaSource, Message.Content
├── llm_test.go                         # MODIFIED: cover ContentBlock helpers
├── capabilities/
│   ├── capabilities.go                 # MODIFIED: Vision/Documents flags
│   └── capabilities_test.go            # MODIFIED
├── anthropic/anthropic.go              # MODIFIED: image/document serialization
├── anthropic/anthropic_test.go         # MODIFIED: capture body, assert blocks
├── openai/openai.go                    # MODIFIED: image_url; document gate
├── openai/openai_test.go               # MODIFIED
├── bedrock/bedrock.go                  # MODIFIED: image/document via SDK + bearer
├── bedrock/bearer.go                   # MODIFIED
└── bedrock/bedrock_test.go             # MODIFIED

core/attachments/
├── attachments.go                      # MODIFIED: optional MediaID
├── media.go                            # NEW: MediaArtifact + MediaStore (sha256 CAS)
└── media_test.go                       # NEW

core/session/
├── migrations.go                       # MODIFIED: migration 0302 (content_json column)
├── migrations_content_json.go          # NEW: the migration body
└── migrations_test.go                  # MODIFIED: ledger row

core/rpc/views/attachments/
├── api.go                              # MODIFIED: AddMedia method
└── impl.go                             # MODIFIED

core/rpc/views/sessions/
├── api.go                              # MODIFIED: SendMessage gains contentBlocks
└── impl.go                             # MODIFIED

core/rpc/bindings.go                    # MODIFIED: Attachments_AddMedia binding

frontend/src/lib/types.ts               # MODIFIED: ContentBlock, MediaSource
frontend/src/lib/harnessClient.ts       # MODIFIED: addMedia, sendMessage(contentBlocks)
frontend/src/components/chat/
├── ChatInput.vue                       # MODIFIED: paperclip + drag-drop + thumbnails
├── AttachmentChip.vue                  # NEW
├── MessageBubble.vue                   # MODIFIED: render image / document blocks
├── ImageLightbox.vue                   # NEW
└── __tests__/                          # NEW + MODIFIED

docs/multimodal.md                      # NEW
```

**Structure Decision**: Single Go module + Vue frontend (existing harness layout). New code clusters under `core/attachments/media.go`, `core/llm/` provider adapters, and `frontend/src/components/chat/`.

## Phase 0 — Research summary

- **Anthropic content blocks**: `text`, `image` (`source.{type:"base64", media_type, data}`), `document` (PDF only, base64 source). Cite: https://docs.anthropic.com/en/docs/build-with-claude/vision and https://docs.anthropic.com/en/docs/build-with-claude/pdf-support.
- **OpenAI Chat Completions**: `image_url` content part with `data:` URL or remote URL. PDFs not first-class via Chat Completions (Files API is a separate endpoint). Cite: https://platform.openai.com/docs/guides/vision.
- **Bedrock Converse**: `image` (`format`, `source.bytes`) and `document` (`format`, `source.bytes`, `name`) blocks. Cite: https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_ContentBlock.html.
- **Wails file uploads**: Wails dev mode supports drag-drop natively; in production, the OS file picker via `<input type="file">` is the simplest path.
- **CAS storage**: sha256 hex filename matches our existing `core/contexts/` library hashing convention.

## Phase 1 — `ContentBlock` shape + helpers

**Targets**: `core/llm/llm.go`, `core/llm/llm_test.go`.

- Define `ContentBlock`, `MediaSource`. Replace `Message.Content string` with `Message.Content []ContentBlock`. Provide `Message.Text() string` flattener and `NewTextMessage(role, text) Message` constructor for the common case.
- Migrate every internal call site: `core/session/manager.go` Append paths, `core/rpc/views/llm/impl.go buildMessages`, all adapter request builders. Most callers wrap a single text → `[]ContentBlock{{Type:"text", Text:s}}`.
- Tests cover empty message, text-only, image-only, mixed.

**Dependencies**: none.

## Phase 2 — `core/attachments/media.go` CAS storage

**Targets**: `core/attachments/media.go`, `core/attachments/media_test.go`.

- `MediaArtifact` struct + `MediaStore` interface + `NewSQLMediaStore(storage.DB)` impl.
- DDL: new table `media_artifacts(id, content_hash, media_type, byte_size, original_name, created_at)`. Lands as part of migration 0302 (Phase 4).
- File CAS: `MediaStore.Put(bytes, mediaType, originalName)` computes sha256, writes `<DataDir>/media/<sha256>` if missing (atomic via tmp + rename), inserts metadata row, returns `MediaArtifact`.
- `MediaStore.Get(id) (*MediaArtifact, []byte, error)` returns metadata + bytes.
- `MediaStore.Delete(id)` removes the metadata row; if no other rows reference the same content hash, removes the on-disk file.
- Refcount via `SELECT count(*) FROM attachments WHERE media_id IN (SELECT id FROM media_artifacts WHERE content_hash=?)`.

**Tests**: dedup, refcount, oversize reject, missing-file detect (corrupted DB).

**Dependencies**: Phase 1 (no — independent; can run in parallel).

## Phase 3 — Provider adapter serialization

**Targets**: `core/llm/anthropic/`, `core/llm/openai/`, `core/llm/bedrock/`.

- Each adapter's request-body builder iterates `Message.Content` blocks and emits the per-provider shape per FR-006/FR-007/FR-008.
- OpenAI documents: at validation time, return `UnsupportedModalityError`; do not serialize.
- Bedrock document name: derive from `MediaSource.OriginalName` if present, else `"document"`.
- Tests use `httptest.Server` to capture body bytes; assert image/document blocks land at the right JSON path with correct `media_type`/`format`.
- `Bedrock` SDK path uses `types.ImageBlock` / `types.DocumentBlock`; bearer-auth REST path mirrors the same JSON shape.

**Dependencies**: Phase 1.

## Phase 4 — Capability gating

**Targets**: `core/llm/capabilities/capabilities.go`, capability rows for shipped models, `core/rpc/views/llm/impl.go buildRequest` validation.

- Extend `Capabilities` with `Vision bool`, `Documents bool`.
- Backfill the capability registry per FR-009.
- `UnsupportedModalityError{Modality string, Model string}` with a `Friendly() string` returning the US3 message.
- `buildRequest` walks the outgoing `Content` for `image`/`document` blocks; if found and the active model lacks the flag, return the error before invoking the adapter. Surfaces in chat as a normal-error path.

**Dependencies**: Phase 1.

## Phase 5 — Migration 0302 + persisted message shape

**Targets**: `core/session/migrations.go`, `core/session/migrations_content_json.go` (new), `core/session/store.go` read/write paths.

- Migration 0302 adds `content_json TEXT NULL` to `session_messages` and creates `media_artifacts` table per Phase 2.
- Store load: prefer `content_json` when non-null; fall back to `content` (legacy text). Synthesize `[{type:"text", text:content}]`.
- Store save: serialize `[]ContentBlock` as JSON into `content_json`; for text-only messages, also populate the legacy `content` column for one release as a compat buffer (deprecate next mission).

**Tests**: ledger row 302 present, legacy row read works, mixed-content round-trip, idempotent re-run.

**Dependencies**: Phases 1, 2.

## Phase 6 — RPC surface

**Targets**: `core/rpc/views/attachments/`, `core/rpc/views/sessions/`, `core/rpc/bindings.go`.

- New `Attachments_AddMedia(scopeKind, scopeID, base64, mediaType, originalName) (Attachment, error)` binding.
- `Sessions_SendMessage` gains `contentBlocks []ContentBlock` field; backend converts `text` → single text block when no blocks supplied. Maintain backward-compat with the existing `text`-only callers for one release.

**Tests**: stub Pool + adapters; assert AddMedia → adapter receives the right bytes; assert capability-gated reject path.

**Dependencies**: Phases 1, 2, 5.

## Phase 7 — Frontend

**Targets**: `ChatInput.vue`, `AttachmentChip.vue` (new), `MessageBubble.vue`, `ImageLightbox.vue` (new), `lib/types.ts`, `lib/harnessClient.ts`.

- `ChatInput.vue`:
  - Paperclip button → `<input type="file" accept="image/*, application/pdf" multiple>`.
  - Drag-drop on the textarea container with the existing `useDropZone` pattern.
  - Per-file 20 MiB cap; render thumbnail (image) or AttachmentChip (PDF) in a row above the textarea.
  - On send: for each pending attachment, call `client.attachments.addMedia(...)` to upload, collect returned `attachment.id` + `mediaId`. Build `contentBlocks` array: `[{type:"image"|"document", source:{kind:"base64", media_type, data}}, {type:"text", text:userText}]` ordering. Submit via `sendMessage(sessionID, {contentBlocks})`.
  - Cancel path: removing an unsent attachment never calls `addMedia`.
- `MessageBubble.vue`:
  - Switch on each `ContentBlock.Type`: text → `StreamingText`; image → `<img>` thumbnail with click → `ImageLightbox.vue`; document → chip with name + size + download link.
- `ImageLightbox.vue` — full-screen overlay; ESC + click-outside close.
- Type updates in `types.ts`.
- Tests for `ChatInput.vue` (drag-drop, capability-gate warning, attachment removal), `MessageBubble.vue` (block rendering), `ImageLightbox.vue` (open/close/escape).

**Dependencies**: Phase 6.

## Phase 8 — Polish + docs

**Targets**: `docs/multimodal.md`, end-to-end smoke tests.

- `docs/multimodal.md`: per-provider feature matrix, supported file types, size caps, troubleshooting.
- E2E smoke test (gated `-tags=integration`): boot core against temp DataDir, attach a real test fixture image, send via Anthropic adapter wired to a httptest stub, assert capture shape; same for Bedrock.
- Refcount-orphan sweep at boot: walk `<DataDir>/media/`, drop files whose hash isn't referenced. Prevents disk leak after crashes.

**Dependencies**: all earlier phases.

## Work-package breakdown (proposed)

- **WP01 — `ContentBlock` shape + adapter serialization** (Phases 1, 3). Lands the type changes and serializes for all three providers. Big bang refactor — every call site of `Message.Content` updated in one go.
- **WP02 — Media CAS + migration** (Phases 2, 5). Storage layer: `media.go`, migration 0302, persisted-shape changes.
- **WP03 — Capability gating + RPC surface** (Phases 4, 6). `Capabilities.Vision/Documents`, validation in `buildRequest`, `Attachments_AddMedia`, `Sessions_SendMessage(contentBlocks)`.
- **WP04 — Frontend** (Phase 7). `ChatInput.vue` paperclip + drag-drop, `MessageBubble.vue` block rendering, `ImageLightbox.vue`.
- **WP05 — Polish + docs** (Phase 8). `docs/multimodal.md`, refcount sweep, e2e smoke.

DAG: WP01 → (WP02 ∥ WP03) → WP04 → WP05.

## Risk register

| Risk | Phase | Mitigation |
|---|---|---|
| ContentBlock refactor touches every adapter + every test | 1, 3 | Land WP01 as one big-bang refactor, include the `Message.Text()` flattener so legacy tests don't break. |
| OpenAI rejects very large data: URLs (~25 MiB practical limit) | 3 | Cap at 20 MiB on the frontend; surface clear error past it. Document in `docs/multimodal.md`. |
| Bedrock document `name` validation — Converse may require non-empty | 3 | Default to `"document"` if `OriginalName` is missing; verify in adapter test. |
| sha256 collision (theoretical) | 2 | Accepted; collisions on cryptographic hashes are out-of-scope to defend against. |
| Disk leak if a session crashes mid-upload | 2, 8 | Boot-time orphan sweep walks `<DataDir>/media/` and removes hash files with zero refcount. |
| Provider rejects unsupported `media_type` | 3 | Validate against an allow-list per provider before send; surface friendly error. |
| Old `content` column / new `content_json` column drift | 5 | Reader prefers content_json; writer fills both for one release; ledger row 302 is a hard checkpoint. |
| Frontend memory blow-up with N large images in input | 7 | `URL.createObjectURL` (revokes on remove); never load full-resolution into Vue reactive state — use object URLs for previews. |
| Wails desktop drag-drop differs from browser dev | 7 | Test both; if Wails surface differs, fall back to button-only with a clear note in `docs/multimodal.md`. |

## Open questions

(Restated from spec.md §11.)

1. OpenAI document support: gated for v1; Files API integration is its own mission.
2. Bedrock document `name` field: verify non-empty constraint at WP03 implement time.
3. Refcount sweep timing: on-delete + boot-time orphan sweep (defense in depth). Documented.
