# Spec: Context Library

**Mission:** context-library-01KQ3MF1
**Mission type:** software-dev
**Owner:** kaneaz-harness
**Status:** Draft
**Created:** 2026-04-26
**Builds on:** Mission A (per-session starting context — single file upload)

## 1. Problem statement

Mission A let users attach a Markdown file at session-creation time
that becomes the session's system prompt. That works for one-off
files, but quickly breaks down once a user accumulates a personal
prompt library: project briefs, role instructions, codebase
summaries, "always read this first" preambles, post-mortem notes.

This mission delivers a **persistent, browseable repository of
context files** under a configurable root (`<DataDir>/contexts/`),
with multi-level folder organisation and a tree-picker integrated
into both the New Session dialog and a top-level `/contexts`
surface for browsing/preview/editing.

The user model: drop your `.md` files into nested folders under
`~/.harness/contexts/` (or import via the UI), then pick from the
tree when starting a session. The harness reads the file and
applies it as the system prompt — the existing Mission A
plumbing handles the rest.

## 2. Non-goals

- **Cloud sync** — the repository is local-only. Future bundle resolution can ship signed contexts; out of scope here.
- **Markdown rendering with embeds / transclusion** — context files are rendered as plain markdown for preview only. We do not resolve `![[...]]` Obsidian links, frontmatter inheritance, or variable interpolation in v1.
- **Tagging / search by content** — folders are the only organisation mechanism. Full-text search is a follow-up.
- **Versioning** — files are flat-on-disk. Git-managed folders work transparently because we don't write metadata files into the repo, but we don't expose version-control affordances in the UI.
- **Sharing across sessions mid-conversation** — once a session starts, the system prompt is fixed (Mission A's contract). The library doesn't change that.

## 3. User stories

**US1 — Browse + preview.**
User clicks the new "Contexts" entry in the left-rail bottom nav. They see a tree of folders + files rooted at the configured contexts directory. Click a folder to expand; click a file to preview its contents in the right-side preview pane (rendered markdown).

**US2 — Pick from library at session creation.**
In New Session dialog, the existing "Starting context" section gains a second tab: **From library**. The tab shows the same tree component; clicking a file selects it. The dialog displays the file path, byte size, and token estimate just like the upload flow. Submit applies it as system prompt (or user_seed, respecting the existing toggle).

**US3 — Manage files.**
On `/contexts`, the user can:
- Create a new folder (right-click → "New folder", or a "+ Folder" button).
- Create a new context file (right-click → "New context", or "+ Context"). Opens an inline markdown editor.
- Rename, move, or delete folders/files.
- Import an existing `.md` from disk via the existing file picker.

**US4 — Edit in place.**
Clicking a file's "Edit" button switches the right-pane to an editable monaco-style textarea. Save commits the changes. Recent preview state is preserved. No autosave — explicit Save / Cancel.

**US5 — Token-aware previews.**
Each file's preview shows a small chip in the header: `~N tokens` (rough heuristic: `Math.ceil(content.length / 4)`). Helps the user notice when a starting context will eat half their context window.

**US6 — Recently used.**
The contexts page shows a "Recent" section above the tree (most-recently-applied-as-system-prompt files, top 5). Click to preview / re-apply.

**US7 — Empty state.**
First-run / no files: the contexts page shows a welcome card explaining the directory layout, with a "Create first context" button and a "Show me where files live" affordance that opens `<DataDir>/contexts/` in Finder / Explorer.

## 4. Functional requirements

**FR-001 — Configured root.** The library root defaults to `<DataDir>/contexts/`. Operators can override via `Settings.ContextLibraryPath` (path string). Empty / invalid path falls back to the default with a one-time warning in the audit log.

**FR-002 — Tree representation.** The backend exposes the library as a recursive tree:
```go
type Node struct {
    Name      string
    Path      string
    Kind      NodeKind  // "folder" | "file"
    Size      int64
    Modified  time.Time
    Children  []Node
}
```
The whole tree is returned by `Contexts_List() (Node, error)`. For v1 we don't paginate — performance is fine up to a few thousand files; profile later.

**FR-003 — File read.** `Contexts_Get(path) (string, error)` returns the content of a single file. Path validation: only allow `.md`, `.markdown`, `.txt`. Reject paths containing `..` or that resolve outside the configured root (defence-in-depth).

**FR-004 — File write.** `Contexts_Save(path, content) error` writes a file. Creates parent folders if absent. Same path validation as Get. Max file size 1 MiB (rejects beyond that — larger contexts are an antipattern for system prompts).

**FR-005 — Folder + file ops.** Bindings for `Contexts_CreateFolder(path)`, `Contexts_Rename(oldPath, newPath)`, `Contexts_Delete(path)`. Rename / delete on a folder cascades. Delete is trash-style: moves to `<DataDir>/contexts/.trash/<timestamp>-<name>` rather than unlinking, so accidental deletes are recoverable for 30 days. A periodic cleanup (weekly, on harness boot) purges trash older than the retention window.

**FR-006 — Apply-to-session.** When the user picks a file in the NewSessionDialog "From library" tab, the dialog calls `Contexts_Get(path)`, treats the returned content the same as a file-upload flow (system / user_seed toggle still applies), and records the source path in a new `Session.ContextSource` field (see FR-007) for traceability.

**FR-007 — Source provenance.** Extend `session.Record` with `ContextSource string` — the library path the system prompt came from (empty when user uploaded a file or typed inline). The session details surface in `/sessions/<id>` shows "Context: projects/onboarding.md" when set so the user can re-find or re-edit the source.

**FR-008 — Recent list.** `Contexts_RecentlyApplied(limit int) []string` returns the top-N file paths most-recently selected in NewSessionDialog. Stored in `<DataDir>/contexts.recent.json` (simple JSON array, capped at 50, LRU on apply).

**FR-009 — Watch + invalidate.** Optional fsnotify watcher emitting `contexts:tree-changed` events whenever the root subtree mutates outside the harness (operator drops a file in manually). The frontend listens and re-fetches on receipt. If fsnotify is unavailable on the platform, fall back to a 5s poll while `/contexts` is open; idle when the route changes.

**FR-010 — Markdown preview.** The frontend uses an existing safe markdown renderer (no raw HTML; allow only headings, lists, links, code blocks, tables, images-with-data-uri-only). Reuse whatever ChatBubble's content renderer does today.

## 5. Non-functional requirements

- **NFR-001:** Tree fetch for ≤ 1k files completes in ≤ 50 ms p99.
- **NFR-002:** File read for ≤ 256 KiB completes in ≤ 10 ms p99.
- **NFR-003:** No path-traversal. Every Path argument must resolve under the configured root. Tests cover `..`, absolute paths, symlink shenanigans (refuse to follow symlinks that exit the root).
- **NFR-004:** Trash retention configurable; default 30 days.
- **NFR-005:** No telemetry — context bodies never leave the device unless explicitly applied to a session.

## 6. UI design

### Left-rail entry

Add a new entry to the bottom nav:
```
[FileText] Contexts  →  /contexts
```
Position: between **Bundles** and **Providers**, so the order reads: Sessions / Tools / Bundles / **Contexts** / Providers / Audit log / Settings.

### `/contexts` route layout

Three-column layout:

```
┌────────────┬──────────────────────────────────┬────────────┐
│  Tree      │  Preview / editor                │  Recent    │
│  (left)    │  (centre)                        │  (right)   │
│            │                                  │            │
│  ▶ folder  │  # File header                   │  • a.md    │
│  ▼ folder  │  ~N tokens                       │  • b.md    │
│   • a.md   │  ──────────────────              │  • c.md    │
│   • b.md   │  Markdown body…                  │            │
│  + folder  │                                  │            │
│  + file    │  [Edit] [Apply to new session]   │            │
└────────────┴──────────────────────────────────┴────────────┘
```

- **Left**: tree view, click-to-expand folders, click-to-preview files. Right-click → context menu (rename, delete, new child). "+" buttons at the bottom.
- **Centre**: preview by default; toggle to edit mode via the Edit button. Edit-mode footer: Cancel / Save.
- **Right**: list of recently-applied contexts. Shows top 5; "Show more" expands to top 50. Click a recent entry → loads it in the preview pane.

### NewSessionDialog integration

The existing "Starting context (optional)" section gains a tab control:

```
[Upload file] [From library]
```

- **Upload file** — current behaviour preserved.
- **From library** — embedded tree picker (smaller footprint: no preview pane, just a single-selection tree). Selected file shows its path + size + token estimate below. Toggle between system / user_seed kinds applies the same way.

### Empty state on `/contexts`

When the tree is empty (first run):
- Big welcome card centred in the canvas.
- Heading: "Build a context library."
- Body: "Drop `.md` files into `~/.harness/contexts/` or create one below. Pick any context when starting a new session to seed the conversation."
- Two buttons: **Create first context** (opens inline editor for `untitled.md`), **Open folder** (uses `runtime.BrowserOpenURL` with `file://<root>/`).

## 7. Architectural sketch

```
core/contexts/                    ← new package
  library.go                      ← Library struct, Open(), Tree, Get, Save, Rename, Delete, RecentlyApplied
  watcher.go                      ← fsnotify-backed root watcher
  trash.go                        ← .trash/ retention + cleanup
  library_test.go
core/rpc/views/contexts/          ← new view (note: existing core/rpc/views/contextview is unrelated)
  api.go                          ← ContextsAPI interface
  impl.go                         ← managerAPI wrapping core/contexts.Library
  impl_test.go
core/rpc/bindings.go              ← Contexts_List / Get / Save / CreateFolder / Rename / Delete / RecentlyApplied
core/rpc/api.go                   ← wires the library at boot
core/session/types.go             ← adds ContextSource string
core/session/migrations.go        ← migration 305: ALTER sessions ADD COLUMN context_source TEXT NOT NULL DEFAULT ''
core/session/sqlitedb/sqlitedb.go ← idempotent ADD COLUMN guard
core/session/store.go + manager.go ← wire ContextSource through Get/Create/SetContextSource

frontend/src/views/contexts/
  ContextsView.vue                ← three-column layout
  ContextTree.vue                 ← tree with expand / context-menu
  ContextPreview.vue              ← markdown render + edit toggle
  ContextRecent.vue               ← recently-applied list
  __tests__/ContextsView.test.ts
frontend/src/shell/NewSessionDialog.vue ← tab control + tree picker
frontend/src/lib/types.ts         ← ContextNode, ContextNodeKind types
frontend/src/lib/harnessClient.ts ← contexts client
frontend/src/main.ts              ← /contexts route
frontend/src/shell/LeftRail.vue   ← FileText entry
```

## 8. Wails surface additions

```
Contexts_List()                          → ContextNode  (whole tree)
Contexts_Get(path)                       → string       (file content)
Contexts_Save(path, content)             → void
Contexts_CreateFolder(path)              → void
Contexts_Rename(oldPath, newPath)        → void
Contexts_Delete(path)                    → void          (moves to trash)
Contexts_RecentlyApplied(limit)          → []string
Contexts_RootPath()                      → string       (for "Open folder" affordance)
```

Plus the existing `Sessions_SetSystemPrompt` is reused — the NewSessionDialog calls `Contexts_Get(path)` then `Sessions_SetSystemPrompt(id, content, kind)` exactly as the upload path does today.

## 9. Edge cases

| # | Case | Handling |
|---|---|---|
| 1 | Path traversal (`../../etc/passwd`) | Reject with `ErrPathOutOfRoot` |
| 2 | Symlink that escapes root | `os.Lstat` + `filepath.EvalSymlinks` check; refuse to follow |
| 3 | File >1 MiB | `Save` rejects; `Get` truncates with `[truncated at 1 MiB]` and a warning chip |
| 4 | Non-`.md/.markdown/.txt` extension | Hidden from tree; rejected on `Save` |
| 5 | Hidden files (dotfiles) | Hidden by default; toggle "Show hidden" in `/contexts` reveals them |
| 6 | Folder with 10k+ files | Render with virtualisation — only mount visible rows |
| 7 | Operator drops a file while UI is open | Watcher emits `contexts:tree-changed`; frontend re-fetches |
| 8 | User edits a file in place + same file is applied to a session in flight | The applied content is whatever `Contexts_Get` returned at session-create; subsequent edits don't retroactively change live sessions |
| 9 | Trash entry with the same name as live | Append `-1`, `-2` suffix |
| 10 | Library root doesn't exist | Auto-create on first use (mode 0700) |
| 11 | Library root path is a file (not folder) | Refuse to start; surface error |
| 12 | Concurrent edit / rename / delete races | Per-path mutex; last-writer-wins for content; rename of a file currently open in editor invalidates the editor with a warning |

## 10. Acceptance criteria

A1. Tree fetch + render of a 100-file library completes within 50 ms (US1).

A2. Picking a library file in NewSessionDialog applies it as the session's system prompt; `Session.ContextSource` is recorded (US2, FR-007).

A3. Folder + file CRUD operations round-trip via the UI (US3).

A4. In-place editor saves changes durably; reopening shows the edited content (US4).

A5. Recently-applied list updates correctly across session creates (US6, FR-008).

A6. Empty-state welcome card renders on first run; "Create first context" produces a usable file in `<root>/untitled.md` (US7).

A7. Path-traversal attacks rejected (NFR-003); symlink-escape rejected; non-MD extensions invisible.

A8. Watcher fires when external file system changes occur (FR-009).

## 11. Open questions

- **OQ1:** Hot-reload UX when an operator edits a file outside the UI while the user has it open — toast a "this file changed externally; reload?" warning, or auto-merge? Initial spec: toast + manual reload.
- **OQ2:** Markdown frontmatter — many users add `--- title: ... ---` blocks. Should we strip them before applying as system prompt, or pass through? Initial spec: pass through; models tolerate it. Revisit if it pollutes outputs.
- **OQ3:** Bundle integration: when a signed bundle ships context artifacts, do they get merged into the same tree (read-only branch under `bundle:<id>/...`) or a separate surface? Defer until bundles mission lands more.
- **OQ4:** Per-context default `kind` — should a file remember whether it was last applied as `system` or `user_seed`? Initial: no, the toggle in NewSessionDialog is per-creation.
- **OQ5:** Exposing the library to the LLM via tool-calling (e.g., a `contexts.search` tool the model can invoke mid-conversation) is interesting but out of scope for v1. Note as a follow-up.

## 12. Dependencies

- **Mission A — Per-session starting context** (merged): we reuse `Sessions_SetSystemPrompt` and the system-prompt injection in `buildMessages`.
- **Settings store** (existing): `ContextLibraryPath` setting lives there.
- **Audit emitter** (existing): logs library mutations (`contexts.created`, `contexts.deleted`, etc.) for FR-005's recoverability.

## 13. Out-of-scope follow-ups (parking lot)

- Full-text search across the library.
- Tags / labels per file (would need metadata sidecars).
- Bundle-driven contexts (read-only contexts shipped via signed bundles).
- Tool-calling integration (`contexts.search`, `contexts.read` tools the model can invoke).
- Cross-device sync (S3, Git, iCloud Drive backend).
- Templating / variable interpolation (`{{user_name}}` substitution).
