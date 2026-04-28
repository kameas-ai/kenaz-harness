# Spec: Wails-native folder picker RPC

**Status**: draft · **Owner**: alecfeeman

## 1. Why

`DirectoryPicker.vue` currently uses an `<input type="file" webkitdirectory>` hack because Wails 2.x does not expose `OpenDirectoryDialog` in its TS surface. The hack returns only the relative root *name* ("Desktop"), not the absolute path — which leaves the user typing/pasting paths or relying on the path-expansion fallback (`1f99baa`). For a desktop app this is the wrong default.

## 2. Goals

- New Go RPC method `Tools.PickDirectory(opts) (path string, err error)` that calls `runtime.OpenDirectoryDialog(ctx, options)` (Wails Go runtime, fully supported).
- Frontend `DirectoryPicker.vue` calls the RPC; the webkitdirectory fallback stays only for browser-mode (web preview) builds.
- The picker honours an optional starting directory (defaults to `$HOME`).
- Selected path is always absolute; existing path-expansion stays as a typed-input convenience.

## 3. Non-goals

- File picker (separate; existing paperclip uses `<input type=file>`).
- Multi-directory bulk select.

## 4. Functional requirements

| ID | Requirement | Status |
|---|---|---|
| FR-001 | New RPC method `Tools.PickDirectory(ctx, opts PickDirectoryOptions) (string, error)`. | proposed |
| FR-002 | `PickDirectoryOptions` carries `default_directory string`, `title string`. | proposed |
| FR-003 | Returns absolute path on success, empty string on user-cancel, non-nil error on system failure. | proposed |
| FR-004 | DirectoryPicker.vue replaces its `<input type=file webkitdirectory>` with a button that invokes `client.tools.pickDirectory()`. | proposed |
| FR-005 | The chip text is the absolute path (no inline edit needed for native path). | proposed |
| FR-006 | When Wails runtime is unavailable (browser preview), fallback to the existing webkitdirectory hack with a note. | proposed |

## 5. Success criteria

- Picking a folder via the native dialog inserts an absolute path; install proceeds without further editing.
- No regression in path-expansion typed input.
