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

- **Inbound (user → model) — multimodal-native types**: `ChatInput.vue` accepts image (PNG/JPEG/GIF/WebP) and PDF attachments via paperclip button + drag-and-drop. Files are stored under `<DataDir>/media/<sha256>` (CAS) and referenced by attachment id. Each adapter serializes the right per-provider shape.
- **Inbound (user → model) — any other file type**: drop a `.go` / `.json` / `.txt` / `.html` / `.md` / etc. and it's inlined as a **session-scope context attachment** via the existing `Attachments_Add` (context-library WP03) path. The file content lands in the resolved-system-prompt order alongside library attachments. NOT a multimodal content block — providers reject those as inputs.
- **Inbound — `@filepath` shortcut**: typing `@~/path/to/file` (or `@/abs/path` or `@./relative/path`) in the chat input inlines the file the same way drag-drop does (session-scope context attachment for non-multimodal types; multimodal block for image/PDF). Supports tab-complete from the input.
- **Vision answers**: Models that support vision (Claude 3+, GPT-4o, Bedrock Nova / Claude 3) describe / OCR / reason over the image as part of their response.
- **Document QA**: Models that support document blocks (Anthropic Claude 3.5+, Bedrock Nova) answer questions from a PDF in the same turn.
- **Outbound (model → file)**: Out of scope for this mission. The model's response stream stays text + tool_use. **Capturing files the model produces** (code blocks with filename hints, tool outputs that return file-shaped content, manual user pin) is the **artifacts-storage mission's** job, riding on the same CAS this mission lays down. Filesystem MCP writes (model mutating the user's actual disk) are the **filesystem-mcp-recipe mission's** job and are explicitly NOT artifacts.

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
- **US6** As a user, I drag a `.go` file onto the chat input. A chip appears with the filename. The file's contents are inlined as a session-scope context attachment so the model sees them in the resolved-system-prompt. The chip persists in the session's resolved-context panel.
- **US7** As a user, I type `@~/Code/foo.go` in the chat input. After a brief pause, the harness reads the file and converts the token into the same chip US6 produces. Tab autocompletes the path against the local filesystem (within the current project root if any, else `~`).

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
  - Paperclip button → file picker (accept: **all files** — type-detection happens in the upload handler).
  - Drag-and-drop on the input area accepts any file type.
  - Per-file 20 MiB cap for multimodal types (images/PDF); 1 MiB cap for inlined-text types matching the existing context-library library cap; harder error past 30 MiB total.
  - **Type branch on drop**:
    - MIME type starts with `image/` AND in `[image/png, image/jpeg, image/gif, image/webp]` → multimodal image block. Render thumbnail.
    - MIME type is `application/pdf` → multimodal document block. Render document chip.
    - Anything else → text-snapshot path. Read as UTF-8; if the file isn't valid UTF-8 (binary), reject with "binary files are only supported as images or PDFs". Inlined via `Attachments_Add` (existing context-library binding) at session scope. Render as a context-attachment chip with filename.
  - On send: for multimodal blocks, upload via `Attachments_AddMedia` and attach `MediaID` to the `ContentBlock` array. For text-snapshot attachments, no upload step (already added on drop); the resolved-system-prompt picks them up at send time.
- **FR-011b** `@filepath` token in the input box:
  - When the user types `@<token>` followed by a space (or hits Enter), if `<token>` parses as a filesystem path that the harness can read, treat it like a drop event for that path.
  - Path forms accepted: `~/...`, `/abs/...`, `./relative/...`. Paths must be inside the user's home directory OR (when the active session has a project) inside the project's allowed roots — same deny-list as the filesystem-mcp-recipe path-validator (canonicalize + check).
  - Tab autocomplete: when the user types `@<partial>` and hits Tab, the harness suggests path completions via a new `Shell_PathComplete(partial string) ([]string, error)` binding. Limited to 32 results.
  - On accept (Tab-completion or Enter), the `@<token>` text is replaced with a chip in the input pre-send queue (same chip rendering as drop).
  - Path-validator REUSED from filesystem-mcp-recipe's `core/rpc/views/tools/path_validation.go` (Mission B WP01). If Mission B hasn't landed yet, this mission ships a slim local copy and Mission B refactors to share when it lands.
- **FR-012** `MessageBubble.vue`:
  - Renders `text` blocks via `StreamingText.vue` (markdown).
  - Renders `image` blocks inline as a thumbnail (lightbox on click).
  - Renders `document` blocks as a chip with filename + size + a "view" link that opens the file.
  - Renders inlined-text attachments via the existing context-attachment chip pattern from `ResolvedContextPanel.vue`.
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
- **A9** US6 — drop a `.go` file → resolved-context panel shows a chip; the file content appears in the LLM request's system-prompt order; on send, the model can quote the file in its response.
- **A10** US7 — `@~/.zshrc` token → file content appears in the resolved panel; tab-complete returns ≤ 32 candidate paths; deny-list rejects `@/etc/passwd`.

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
