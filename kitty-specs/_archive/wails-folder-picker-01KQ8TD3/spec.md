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
| FR-002 | `PickDirectoryOptions` carries `default_directory string`, `title string`. The frontend computes `default_directory` per Q5.2 = C: when the active recipe form already has one or more chips, default to the **common parent** of the existing chips (`filepath.Dir` applied to each, then longest common prefix); when no chips exist yet, default to `$HOME`. The Go RPC takes whatever the frontend supplies — the contextual default is a UI concern, not a backend one. | proposed |
| FR-002b | "Common parent" computation: when chips share a parent (e.g. `~/Code/foo` + `~/Code/bar`), default to that shared parent (`~/Code`). When chips have divergent parents (e.g. `~/Code/foo` + `~/Documents/sketch`), fall back to the most recently added chip's parent. Empty chip list → `$HOME`. | proposed |
| FR-003 | Returns absolute path on success, empty string on user-cancel, non-nil error on system failure. | proposed |
| FR-004 | DirectoryPicker.vue replaces its `<input type=file webkitdirectory>` with a button that invokes `client.tools.pickDirectory()`. | proposed |
| FR-005 | The chip text is the absolute path (decided Q5.1 = D — native picker only, no inline edit affordance). The "Add directory" button is the sole entry point for new chips; users no longer type or paste paths. The webkitdirectory fallback stays ONLY in browser-mode preview builds (FR-006); on desktop the native picker is the only path. | proposed |
| FR-005b | Existing chips remain removable via the X button. Inline-edit-on-click is also removed in lockstep with the picker-only entry path — a typo'd chip is removed and re-picked, not edited. Every chip's path is provably the result of an OS-level dialog rather than user-typed text that may not exist on disk. The path-expansion server-side fallback (`1f99baa`) stays in place for legacy / non-UI callers but is no longer reachable from this UI. | proposed |
| FR-006 | When Wails runtime is unavailable (browser preview), fallback to the existing webkitdirectory hack with a note. | proposed |

## 5. Success criteria

- Picking a folder via the native dialog inserts an absolute path; install proceeds without further editing.
- No regression in path-expansion typed input.
