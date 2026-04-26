# Spec: Multimodal I/O — images + documents in chat

**Mission ID**: `multimodal-io-01KQ5GW4`
**Status**: draft
**Owner**: alecfeeman
**Planning base**: `main`
**Merge target**: `main`

## 1. Why this mission

`core/llm.Message.Content` is a string. The harness can only send/receive plain text. But every modern provider supports multimodal inputs:

- **Anthropic Messages API** — `content` accepts `text`, `image` (base64 + media_type), and `document` (PDF, base64) blocks.
- **OpenAI Chat Completions** — `content` accepts `text` and `image_url` parts (file uploads via the Files API for documents).
- **AWS Bedrock Converse** — `content` blocks include `image` and `document` (PDF, csv, html, txt, …).

Without this, the user can't paste a screenshot into a chat or hand the model a PDF. Both common, expected affordances. This is provider-level (not MCP) because the request body shape is per-provider; MCP is for tool calls, not raw multimodal context.

## 2. Goals

- **Inbound (user → model)**: `ChatInput.vue` accepts image and document attachments via paperclip button + drag-and-drop. Files are stored under `<DataDir>/media/<sha256>` (CAS) and referenced by attachment id. Each adapter serializes the right per-provider shape.
- **Vision answers**: Models that support vision (Claude 3+, GPT-4o, Bedrock Nova / Claude 3) describe / OCR / reason over the image as part of their response.
- **Document QA**: Models that support document blocks (Anthropic Claude 3.5+, Bedrock Nova) answer questions from a PDF in the same turn.
- **Outbound (model → file)**: Models can return `text`, `image` (when the provider supports image generation in-message), or `tool_use` blocks. Image generation is a non-goal for v1 because no current provider's chat-completion API returns generated images natively in a way that's adapter-uniform (Anthropic doesn't; OpenAI's `gpt-image-1` is a separate endpoint). Track as a follow-on. **In-message text + tool_use coverage is already complete.**

## 3. Non-goals

- Image generation in the chat stream.
- Audio I/O (Realtime API, voice). Separate mission.
- Video. Separate mission.
- Server-side OCR fallback for non-vision models — the user gets a clear "this model doesn't support images" error instead.
- Per-attachment expiration / TTL. Files live as long as the session does; session delete cascades.
- Streaming uploads (multi-part). Single-shot file POST per attachment.

## 4. User stories

- **US1** As a user, I drag a screenshot onto the chat input. A thumbnail appears with a remove button. Hitting send delivers the screenshot to the model alongside my text. The model describes / answers about it.
- **US2** As a user, I click the paperclip → file picker → choose a PDF. A document chip appears in the input. The model answers questions about the PDF (when the model supports documents).
- **US3** As a user, I send an image to a non-vision model. The harness rejects the send with a clear error: "Model `<id>` doesn't support images. Switch to a vision-capable model or remove the attachment."
- **US4** As a user with a previous turn that included an image, scrolling back shows the thumbnail inline with the message bubble. Clicking opens it full-size.
- **US5** As a developer testing a new model, I see the model's declared capabilities (`supports.vision`, `supports.documents`) flow through the existing `core/llm/capabilities/` registry and gate the input.

## 5. Functional requirements

### 5.1 Message shape

- **FR-001** Replace `core/llm.Message.Content string` with `core/llm.Message.Content []ContentBlock`:
  ```go
  type ContentBlock struct {
      Type       string       `json:"type"`        // "text" | "image" | "document" | "tool_use" | "tool_result"
      Text       string       `json:"text,omitempty"`
      Source     *MediaSource `json:"source,omitempty"`
      ToolUse    *ToolUse     `json:"tool_use,omitempty"`
      ToolResult *ToolResult  `json:"tool_result,omitempty"`
  }
  type MediaSource struct {
      Kind      string `json:"kind"`        // "base64" | "uri" (uri reserved)
      MediaType string `json:"media_type"`
      Data      string `json:"data,omitempty"`
      URI       string `json:"uri,omitempty"`
  }
  ```
- **FR-002** Backward-compat: `Message.Text() string` flattens text blocks for legacy callers / tests; `NewTextMessage(role, text)` constructs the common case. Existing call sites migrate progressively.

### 5.2 Storage

- **FR-003** New `core/attachments/media.go` for binary artifacts. Files at `<DataDir>/media/<sha256-hex>` (CAS).
  ```go
  type MediaArtifact struct {
      ID           string    // ULID
      ContentHash  string    // sha256 hex; matches filename on disk
      MediaType    string
      ByteSize     int64
      OriginalName string
      CreatedAt    time.Time
  }
  ```
- **FR-004** `Attachment` (existing context-library shape) gains optional `MediaID *string`. When present, the attachment refers to a binary, not a text snippet.
- **FR-005** `Attachments_AddMedia(scopeKind, scopeID, mediaBytesBase64, mediaType, originalName) → Attachment`. Performs sha256 dedup — if the artifact already exists on disk, only the metadata row is added.

### 5.3 Provider adapters

- **FR-006** `core/llm/anthropic/`:
  - text → `{type:"text", text:"..."}`
  - image → `{type:"image", source:{type:"base64", media_type:..., data:...}}`
  - document → `{type:"document", source:{type:"base64", media_type:"application/pdf", data:...}}`
- **FR-007** `core/llm/openai/`:
  - text → `{type:"text", text:"..."}`
  - image → `{type:"image_url", image_url:{url:"data:<media_type>;base64,<data>"}}`
  - document → not supported via Chat Completions inline; gate at FR-010 with friendly error pointing to a future Files API mission.
- **FR-008** `core/llm/bedrock/`:
  - text → `{text:"..."}`
  - image → `{image:{format:"png|jpeg|gif|webp", source:{bytes:...}}}`
  - document → `{document:{format:"pdf|csv|html|txt|md", source:{bytes:...}, name:"..."}}`

### 5.4 Capability gating

- **FR-009** Extend `core/llm/capabilities/` with `Vision bool` + `Documents bool`. Backfill known models:
  - Vision: claude-3-* / claude-3-5-* / claude-sonnet-4-*, gpt-4o, gpt-4o-mini, gpt-4-turbo (vision builds), bedrock-anthropic-claude-3-* / bedrock-amazon-nova-*.
  - Documents: claude-3-5-sonnet+ / claude-sonnet-4+, bedrock-amazon-nova-*.
- **FR-010** `buildRequest` validates: any inbound `image` block requires `supports.vision`; any `document` block requires `supports.documents`. Validation failure surfaces as `corellm.UnsupportedModalityError` and renders in the chat UI as US3's error.

### 5.5 Frontend

- **FR-011** `ChatInput.vue`:
  - Paperclip button → file picker (accept: `image/png, image/jpeg, image/gif, image/webp, application/pdf`).
  - Drag-and-drop on the input area.
  - Per-file 20 MiB cap; harder error past 30 MiB.
  - Each pending attachment renders as a thumbnail (image) or document-chip (PDF) with a remove button.
  - On send: upload via `Attachments_AddMedia` (with `scope=session`), then attach `MediaID` to the outbound `ContentBlock` array.
- **FR-012** `MessageBubble.vue`:
  - Renders `text` blocks via `StreamingText.vue` (markdown).
  - Renders `image` blocks inline as a thumbnail (lightbox on click).
  - Renders `document` blocks as a chip with filename + size + a "view" link that opens the file.
  - `tool_use` / `tool_result` rendering is unchanged.

### 5.6 Wire shape

- **FR-013** `Sessions_SendMessage` request gains `contentBlocks []ContentBlock` (existing `text` field stays for the simple path). Frontend uses `contentBlocks` whenever any attachment exists; backend converts `text` → single `ContentBlock{Type:"text"}` when only text is present.
- **FR-014** Persisted message shape mirrors `[]ContentBlock`. Migration 0302 adds `content_json TEXT NULL` to `session_messages`; existing rows have `content` (string) intact and the loader prefers `content_json` when non-null. Old rows synthesize `[{type:"text", text:content}]` on read.

## 6. Non-functional requirements

- **NFR-001** `go test -race -count=1 -short ./core/...` ≥ baseline + new tests.
- **NFR-002** Frontend tests + build clean.
- **NFR-003** Image upload of a 5 MiB PNG round-trips end-to-end in < 2 s.
- **NFR-004** No unbounded memory growth — attachments stream from disk to the request body, not held twice in memory.
- **NFR-005** CAS dedup: re-uploading the same image yields the same filename and a fresh metadata row keyed to a new attachment id, but no duplicate disk usage.

## 7. Acceptance criteria

- **A1** US1 round-trip against Anthropic: image input → vision response.
- **A2** US2 against Bedrock Nova or Claude 3.5: PDF input → answer.
- **A3** US3 enforces capability gate: image to a text-only model returns the friendly error in chat.
- **A4** US4 — historical messages with images render thumbnails on session reload.
- **A5** 5 MiB image dedup: re-upload yields one file on disk, two attachment rows.
- **A6** Migration 0302 upgrades a v1 DB cleanly.
- **A7** Removing an attachment from input before send does not write it to disk.
- **A8** Deleting a session prunes session-scope attachments AND media artifacts no longer referenced (refcount-driven).

## 8. Architecture

```
core/llm/
├── llm.go                  # MODIFIED: ContentBlock, MediaSource, Message.Content shape
├── capabilities/           # MODIFIED: Vision/Documents flags
├── anthropic/              # MODIFIED: serialize image/document blocks
├── openai/                 # MODIFIED: serialize image_url; gate documents
└── bedrock/                # MODIFIED: serialize image/document content blocks

core/attachments/
├── attachments.go          # MODIFIED: optional MediaID
├── media.go                # NEW: MediaArtifact + MediaStore (CAS under <DataDir>/media/)
└── media_test.go

core/session/migrations.go  # MODIFIED: migration 0302 adds content_json to session_messages

core/rpc/views/attachments/ # MODIFIED: AddMedia method
core/rpc/views/sessions/    # MODIFIED: SendMessage accepts contentBlocks

frontend/src/lib/types.ts                      # MODIFIED: ContentBlock, MediaSource
frontend/src/components/chat/
├── ChatInput.vue                               # MODIFIED: paperclip + drag-drop + thumbnails
├── AttachmentChip.vue                          # NEW
├── MessageBubble.vue                           # MODIFIED: render image / document blocks
└── ImageLightbox.vue                           # NEW

docs/multimodal.md                              # NEW
```

## 9. Edge cases

1. User attaches an image then switches to a text-only model → soft warning before send.
2. Provider rejects oversize at wire level → render verbatim error in chat.
3. Two files with identical bytes → same media artifact, two attachment rows, refcount = 2.
4. Missing `supports.vision` flag → assume false; surface as US3.
5. PDF with embedded JS / forms → trust provider sandbox.
6. Drag-drop in Wails desktop AND browser dev — both must work.

## 10. Out of scope (explicit)

- Image generation in the chat stream.
- Audio / voice / video.
- OpenAI Files API (assistant uploads).
- Inline editing of attached images.
- OCR fallback for non-vision models.
- Streaming chunked uploads.

## 11. Open questions

1. **OpenAI document support**: gated for v1; revisit when we add Files API routing.
2. **Bedrock document `name` field**: does Converse require non-empty `name`? Verify before merging adapter changes.
3. **Refcount sweep timing**: on-delete (simpler) vs. periodic. Default: on-delete with a boot-time orphan sweep as a safety net.
