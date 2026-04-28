# Spec: Artifacts storage — capture model-produced outputs in-harness

**Mission ID**: `artifacts-storage-01KQ5K4T`
**Status**: draft
**Owner**: alecfeeman
**Planning base**: `main`
**Merge target**: `main`

## 1. Why this mission

The harness already lets users put text into chats, attach files, and run MCP tools. But every time a model writes a non-trivial chunk of structured output — a code block with a filename hint, a generated PDF, a research summary returned by a future tool — it dies on the next turn. The user has to copy-paste, re-prompt, or just lose it.

This mission introduces **artifacts**: a first-class concept for "things the model produced for you, captured automatically, browsable later". They're scoped to the active session, optionally promotable to the project, and never touch the user's local filesystem.

**Important boundary**: artifacts are NOT files the model creates *on the user's disk* via the filesystem MCP recipe. Those are real user files; the user already sees them via the workspace "Open in OS browser" affordance. Artifacts are the **harness-internal output stream** — content the model emitted *inside the chat* that's worth keeping.

## 2. Goals

- **Auto-capture three sources** without requiring user action per artifact:
  1. **Fenced code blocks with filename hints** in assistant text. e.g. ` ```html title="page.html" ` or `<!-- file: page.html -->` or `// file: foo.go` near the top of the block.
  2. **Tool outputs that return inline file-shaped content** — image-gen tools returning bytes, summarizer tools returning markdown payloads, etc.
  3. **Manual user pin** — right-click an assistant message → "Save as artifact" with a title prompt.
- **Session-scoped storage** — artifacts default to the session that produced them. User can promote to project scope (move semantics; original session reference preserved as backlink).
- **Shared CAS** — content stored under `<DataDir>/media/<sha256>` (the same CAS multimodal-io lays down). Refcounted; deleted when no artifact or attachment references the hash.
- **Browsable surface** — per-session "Artifacts" tab + per-project rollup + global all-artifacts view (filterable). Preview (text/image/code), download, copy-to-clipboard, open-in-OS-browser.
- **Provenance** — every artifact carries a backlink to the message and (when applicable) the tool call that produced it.

## 3. Non-goals

- Capturing files the model writes via filesystem MCP. Those are user files, not artifacts.
- Versioning of artifacts. Each capture is a new artifact even when the content is identical (CAS dedup means disk usage doesn't double, but two metadata rows exist).
- Diffing or merging artifacts.
- Cross-session search across artifact content. v1 list-and-filter only; full-text search rides on a future indexer.
- Streaming captures during a long-running tool call. The capture fires on terminal events: assistant message finalized OR `notifications/progress { stop: true }` from a tool. Mid-stream artifact preview is a follow-up.
- Auto-publishing artifacts to external targets (S3, GitHub gist, etc.).

## 4. User stories

- **US1** As a user prompting "give me a tic-tac-toe HTML page", the assistant returns a code block fenced with `` ```html title="tictactoe.html" ``. The harness saves the file content as an artifact tied to this session. A small "📎 generated artifact: tictactoe.html" chip appears in the message bubble.
- **US2** As a user, I open the session's "Artifacts" tab and see `tictactoe.html` listed. Click → preview rendered HTML in a sandboxed iframe + Download / Copy buttons.
- **US3** As a user, after running a future image-gen tool, the returned image bytes are captured as an artifact. The tool result in the chat shows the image inline; the artifacts tab also lists it with a thumbnail.
- **US4** As a user, I right-click any assistant message → "Save as artifact" → enter a title → the message text is captured as a markdown artifact.
- **US5** As a user with a project containing 3 sessions, the project landing page shows a "Project artifacts" panel rolling up artifacts from all three sessions, sortable by created-at and source-type.
- **US6** As a user, I promote an artifact from session scope to project scope. Sister sessions in the project see it in their per-session view via a "Project artifacts" sub-section. The original session's backlink is preserved.
- **US7** As a user deleting a session, I'm warned: "this session has 4 artifacts. Delete them too?" Default yes; checkbox to keep them at session scope but promote orphan-to-project (only when the session is in a project).

## 5. Functional requirements

### 5.1 Schema

- **FR-001** New `core/artifacts/` package. `Artifact` struct:
  ```go
  type Artifact struct {
      ID          string    // ULID
      SessionID   string    // FK to session that produced it; required
      ProjectID   *string   // FK to project (nullable)
      Title       string    // human-readable
      MimeType    string    // "text/html", "image/png", "text/markdown", …
      ContentHash string    // sha256 hex; entry in <DataDir>/media/<hash>
      ByteSize    int64
      Source      string    // "code_block" | "tool_output" | "user_pin"
      SourceRef   ArtifactSourceRef
      ScopeKind   string    // "session" | "project"
      CreatedAt   time.Time
  }

  type ArtifactSourceRef struct {
      MessageID string  // always present
      ToolCallID string `json:"tool_call_id,omitempty"` // present when Source=="tool_output"
      CodeBlockIndex int `json:"code_block_index,omitempty"` // when Source=="code_block"
      Filename string `json:"filename,omitempty"` // hint extracted from the block
  }
  ```
- **FR-002** Persisted in a new `artifacts` table. Migration in the sessions block (next free version after multimodal-io's 0302; this mission lands on 0303).
- **FR-003** Reuses the multimodal-io CAS layer (`core/attachments/media.go::MediaStore`). Artifacts and message-attached media share `<DataDir>/media/<sha256>`. Refcount: an entry is deletable iff no `attachments` row AND no `artifacts` row references its content_hash.

### 5.2 Capture

- **FR-004** **Code-block detector** (`core/artifacts/detect_code_block.go`):
  - Runs as a post-finalization hook on assistant messages (after the stream-closed-not-tool_use path). Receives the full message text.
  - Parses fenced markdown blocks (` ``` `). For each block:
    - Title hint extraction order: attr-style `title="..."` after the language tag → first-line comment matching `// file: <name>`, `# file: <name>`, `<!-- file: <name> -->`, or `/* file: <name> */` → falls through if none.
    - Capture criteria: title hint present AND (block length ≥ 10 lines OR block ≥ 200 bytes). The size threshold is tunable in settings; default values prevent capturing trivial snippets.
    - Each captured block becomes one `Artifact{Source: "code_block", ...}`.
  - Configurable: `Settings.AutoCaptureCodeBlocks bool` default `true`. When off, hooks no-op; user can still manually pin via US4.

- **FR-005** **Tool-output detector** (`core/artifacts/detect_tool_output.go`):
  - Post-tool-use hook (rides on the existing `core/toolloop` post-hook). Receives `PostToolUseEvent { Tool, Server, Result }`.
  - File-shaped detection heuristic:
    - `Result` is a JSON object with `{type:"image"|"file"|"document", data:"<base64>", media_type:"<mime>"}` shape (per MCP convention) → capture.
    - `Result` is a string with a leading `data:<media_type>;base64,<bytes>` URL → capture.
    - Otherwise: skip. Don't try to be clever about plain text returns — those are tool conversation, not artifacts.
  - Tools can opt OUT via per-tool config: future enhancement.

- **FR-006** **Manual user pin** (`core/rpc/views/sessions/impl.go SaveAsArtifact(messageID, title, sourceRef) → Artifact`):
  - Right-click on any assistant message → "Save as artifact". Prompts for a title (default: first 60 chars of the message). Captures the FULL message text as a `text/markdown` artifact with `Source: "user_pin"`.
  - User can also select a sub-range of the message text (text-selection in the bubble) and save just that range.

### 5.3 RPC view

- **FR-007** New `core/rpc/views/artifacts/` package:
  ```go
  type ArtifactsAPI interface {
      List(ctx context.Context, filter ArtifactFilter) ([]Artifact, error)
      Get(ctx context.Context, id string) (Artifact, []byte, error)  // body via base64-on-wire
      Promote(ctx context.Context, id string, newScope string, newScopeID string) (Artifact, error) // session→project
      Delete(ctx context.Context, id string) error
      // Manual capture surface
      SaveFromMessage(ctx context.Context, sessionID, messageID, title, sourceRangeStart, sourceRangeEnd string) (Artifact, error)
  }
  type ArtifactFilter struct {
      SessionID string
      ProjectID string
      MimeTypePrefix string // "image/" filters all images; "" = all
      Source string
  }
  ```
- **FR-008** Bindings: `Artifacts_List`, `Artifacts_Get`, `Artifacts_Promote`, `Artifacts_Delete`, `Artifacts_SaveFromMessage`.

### 5.4 Frontend

- **FR-009** **Session "Artifacts" tab** in `frontend/src/views/sessions/SessionsView.vue`:
  - Sub-tab next to "Resolved context" (existing collapsible panel from context-library WP04).
  - List artifacts produced by THIS session. Default sort: most recent first.
  - Each row: title, source-type icon (code_block→Code, tool_output→Wrench, user_pin→Pin), mime type, size, created-at, actions (preview, download, copy, promote, delete).
  - Empty state: "No artifacts yet. Code blocks with `title=\"filename.ext\"` and tool outputs are captured automatically."

- **FR-010** **Artifact preview modal** (`frontend/src/views/artifacts/ArtifactPreview.vue`):
  - Open from any list item.
  - Renders by mime type:
    - `text/markdown` → existing markdown renderer.
    - `text/html` → sandboxed iframe (`sandbox="allow-same-origin"` only; no scripts unless an explicit "Run in sandbox" button is clicked, which adds `allow-scripts`).
    - `text/*` (code, plain text) → `<pre>` with syntax highlighting via existing markdown engine (` ```<lang> ``` ` wrapper).
    - `image/*` → existing `ImageLightbox.vue` from multimodal-io.
    - Anything else → "Preview not available; download to view".
  - Footer: Download button (saves to OS Downloads via Wails `SaveFileDialog`), Copy button (copies content as text or as base64 for binaries), Promote-scope dropdown, Delete.

- **FR-011** **Per-message inline chip** in `frontend/src/components/chat/MessageBubble.vue`:
  - When a message has captured artifacts (`message.artifactIDs`), render a chip below the message text: "📎 saved: tictactoe.html, foo.go, …". Click → opens artifact preview.

- **FR-012** **Project rollup** in `frontend/src/views/projects/ProjectLandingPage.vue`:
  - New "Project artifacts" section (alongside the existing "Project context" + sessions table from context-library WP07). Lists project-scoped artifacts; click → preview.

- **FR-013** **Global view** at `/artifacts` route:
  - Lists all artifacts across all sessions/projects. Filter pills: All / Session-scope / Project-scope; Mime-type-prefix dropdown.
  - Linkable from a new rail entry "Artifacts" under Memory.

### 5.5 Lifecycle

- **FR-014** **Session delete cascade**: when a session is deleted, the existing cascade prompt extends to "this session has N artifacts. Delete them?" with a checkbox "Promote orphan artifacts to project scope (only when the session is in a project)". Defaults: delete=yes, promote=no.
- **FR-015** **Project delete cascade**: when a project is deleted with cascade-sessions=true, project artifacts are deleted along with sessions. With cascade-sessions=false, project artifacts demote to session scope (each artifact stays with its source session).
- **FR-016** **Refcount sweep**: on delete, the artifact row is removed; if no other row references the content_hash, the CAS file is removed. Boot-time orphan sweep (existing in multimodal-io) covers crash-loss scenarios.

### 5.6 Settings

- **FR-017** New settings:
  - `Settings.AutoCaptureCodeBlocks bool` — default true.
  - `Settings.CodeBlockMinLines int` — default 10.
  - `Settings.CodeBlockMinBytes int` — default 200.
  - `Settings.AutoCaptureToolOutputs bool` — default true.
- All under a new "Artifacts" section in `SettingsView.vue` (under the existing "Long-term memory" reference pattern).

## 6. Non-functional requirements

- **NFR-001** `go test -race -count=1 -short ./core/...` ≥ baseline + new tests.
- **NFR-002** Frontend tests + build clean.
- **NFR-003** Code-block detection runs in < 50 ms on a 16 KB assistant message — must not delay the next-turn responsiveness.
- **NFR-004** Tool-output detection runs synchronously in the post-tool-use hook with the same < 50 ms budget.
- **NFR-005** No artifact storage outside `<DataDir>/`. Verified by NFR-006 of multimodal-io extended to also walk artifacts.
- **NFR-006** Artifact list query at session scope: < 100 ms for 500 artifacts (indexed on session_id + created_at).

## 7. Acceptance criteria

- **A1** US1 round-trip: assistant returns ` ```html title="tictactoe.html" ... ``` `; artifact appears in the session's tab + chip in the message.
- **A2** US2: preview renders HTML in a sandboxed iframe; Download saves a `tictactoe.html` to the OS Downloads folder.
- **A3** US3: a stub tool returns `{type:"image", data:"<base64>"}`; an artifact materializes; thumbnail visible in the artifacts tab.
- **A4** US4: right-click → save as artifact → markdown artifact appears with the message's text.
- **A5** US5: project landing page lists artifacts from all sessions in the project.
- **A6** US6: promote artifact session→project; sister session in the same project sees it in its rollup.
- **A7** US7: delete-session cascade prompt works; with cascade=true, artifacts are gone; with cascade=false + promote=true, artifacts move to project scope (when the session has one).
- **A8** Trivial code blocks (3 lines, no title hint) are NOT captured.
- **A9** Code block with title hint via `// file: foo.go` first-line comment IS captured.
- **A10** Settings toggle off → no auto-capture; manual pin still works.
- **A11** Refcount: same content emitted by two captures shares one CAS file.
- **A12** No artifact bytes leak outside `<DataDir>` (NFR-005 scan).

## 8. Architecture

```
core/artifacts/
├── artifact.go              # Artifact, ArtifactSourceRef, ArtifactFilter; sentinel errors
├── manager.go               # Manager — CRUD, capture orchestration, refcount
├── detect_code_block.go     # Code-block parser + heuristics
├── detect_tool_output.go    # Tool-output detector
├── store_sql.go             # SQL-backed Store
├── store_mem.go             # In-memory store for tests
├── manager_test.go
├── detect_code_block_test.go
├── detect_tool_output_test.go
└── store_sql_test.go

core/session/migrations.go          # MODIFIED: migration 0303 (artifacts table)
core/session/migrations_artifacts.go # NEW

core/toolloop/hooks.go              # Existing post_tool_use surface; artifact detector subscribes
core/rpc/views/llm/impl.go          # MODIFIED: post-stream finalize fires code-block detector

core/rpc/views/artifacts/
├── api.go                          # ArtifactsAPI
├── impl.go
└── impl_test.go

core/rpc/views/sessions/impl.go    # MODIFIED: SaveAsArtifact passthrough; cascade prompt

core/rpc/api.go                     # MODIFIED: Artifacts() accessor + bindings
core/rpc/bindings.go                # MODIFIED

frontend/src/lib/types.ts          # MODIFIED: Artifact, ArtifactSourceRef
frontend/src/lib/harnessClient.ts  # MODIFIED: artifacts namespace

frontend/src/views/sessions/SessionsView.vue    # MODIFIED: Artifacts tab
frontend/src/views/projects/ProjectLandingPage.vue # MODIFIED: project artifacts section
frontend/src/views/artifacts/
├── ArtifactsView.vue               # NEW: /artifacts global view
├── ArtifactPreview.vue             # NEW: preview modal
└── __tests__/
frontend/src/components/chat/MessageBubble.vue  # MODIFIED: per-message artifact chip
frontend/src/shell/LeftRail.vue                  # MODIFIED: Artifacts rail entry
frontend/src/main.ts                              # MODIFIED: /artifacts route

docs/artifacts.md                   # NEW
```

## 9. Edge cases

1. **Title hint on a one-line block** → still rejected unless byte threshold met. Title alone isn't enough.
2. **Two code blocks with the same title in one message** → both captured; titles are not unique. List view differentiates by created-at.
3. **Multi-block content split across re-pumps** (model emits a tool_use mid-block) → only finalized blocks are captured; partial blocks dropped.
4. **Streaming finalization race** — capture runs AFTER the message is persisted (not during streaming) so we never capture a half-block.
5. **HTML preview XSS** — sandboxed iframe with no `allow-scripts` by default; user toggles to "Run with scripts" explicitly.
6. **Title hint with path traversal** (`title="../../etc/passwd"`) — filename is metadata only, not used for filesystem operations. Still sanitized for display (replace path separators with `_`).
7. **Tool output that's a giant base64 image (>20 MiB)** — capture but cap inline preview at 5 MiB; "download to view" past that.
8. **Refcount race**: two simultaneous deletes of artifacts pointing at the same CAS file. Use a transaction + refcount-check-after-delete pattern; if refcount returns to zero atomically with the row delete, remove the file.

## 10. Out of scope (explicit)

- Filesystem MCP-write capture (those are user files; out of scope by definition).
- Versioning of artifacts.
- Cross-session search.
- Streaming captures during long-running tool calls.
- External publishing (S3 / gist / etc.).
- Inline artifact editing — preview is read-only; download to edit.
- Artifact-level permissions (no read/write ACLs; if you can see the session, you see its artifacts).

## 11. Open questions

1. **Code-block title hint syntax** — settling on `title="filename.ext"` after the language tag (matches existing markdown ecosystem precedent like Pandoc) plus `// file: ...` first-line fallback. Open: should we also accept `<file>filename.ext</file>` blocks at the model's discretion?
2. **Tool-output capture opt-out per tool** — deferred. v1 captures every tool that returns file-shaped content. Add an allow-list / deny-list when noise becomes a problem.
3. **Settings.CodeBlockMinLines tuning** — start at 10; revisit after dogfood.
4. **Scope-promote semantics** — current design: move (not copy). Sister sessions in the same project see promoted artifacts via the project rollup. Open: do we want a "copy on promote" mode? Defer until requested.

## 12. Out-of-band dependencies

- **Multimodal-io mission MUST land first** — this mission depends on `core/attachments/media.go::MediaStore` (CAS layer).
- mcp-tool-execution mission's post-tool-use hook surface (already merged).
- context-library WP04 (`ResolvedContextPanel.vue`) for the tab pattern reference.
- context-library WP07 (`ProjectLandingPage` "Project context" section) for the project rollup pattern reference.
