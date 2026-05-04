# plan.md — Wails-native folder picker

## 1. Branch contract

- Branch: `wails-folder-picker-01KQ8TD3`
- Cuts from `main` at the current HEAD; rebased on top of any merge of `1f99baa` / `0dc61a2` (both already in tree).
- Single PR, four work packages, no schema migrations, no recipe-catalog changes.
- Wire shape additions are purely additive: one new `Tools_PickDirectory` binding + one new wire DTO. No existing RPC method changes signature.

## 2. Architecture

### 2.1 New Go RPC method

The new method lives in `core/rpc/views/tools/` next to the existing recipe-install surface — same package, same `tools.API`, same lock-free read path:

- Add `PickDirectoryOptions{ DefaultDirectory string; Title string }` and `PickDirectory(ctx, opts) (string, error)` to the `ToolsAPI` interface in `core/rpc/views/tools/api.go`.
- Implement on `*API` in a new file `core/rpc/views/tools/picker.go`. The picker is fully orthogonal to recipe installs — no shared mutable state — so it sits outside the `a.mu` lock.
- The implementation calls `runtime.OpenDirectoryDialog(wailsCtx, runtime.OpenDialogOptions{DefaultDirectory: opts.DefaultDirectory, Title: opts.Title})`. Wails returns `(path string, err error)`: non-nil err = system failure; empty string with nil err = user-cancelled (mirrors FR-003).
- A `Dialoger` interface (`type Dialoger func(ctx, opts) (string, error)`) is held on `tools.API` so unit tests inject a fake. Default = `runtime.OpenDirectoryDialog`. Mirrors the `Opener` seam in `core/rpc/views/shell/impl.go`.
- Feature-flag gate: `os.Getenv("HARNESS_DIR_PICKER_NATIVE")`. Empty (default) and any non-`"0"` value → native path. `"0"` → return `errors.New("tools: native picker disabled")` so frontend falls through to its browser-fallback branch. Read on every call so a process restart is never required.

### 2.2 Wails ctx threading

The Wails runtime requires the OnStartup-supplied ctx; passing `context.Background()` crashes:

- `core/rpc/api.go::SetContext(ctx)` is called once from `main.go`'s `OnStartup`.
- Add `a.toolsImpl` (the concrete `*tools.API`) to that fan-out.
- `tools.API` gains a `mu sync.RWMutex` + `wailsCtx context.Context` pair plus `SetContext(ctx)` exactly like `shell.API.SetContext`.
- `newToolsAPI` widens to return `*tools.API`, store on the struct, then `a.toolsImpl.SetContext(ctx)` from rpc-level `SetContext`. Stub path stays untouched.

### 2.3 Bindings + frontend client

- `core/rpc/bindings.go`: add `Tools_PickDirectory(opts tools.PickDirectoryOptions) (string, error)` after `Tools_RecipeConfig` (~line 644), forwards to `b.api.Tools().PickDirectory(b.ctx(), opts)`.
- `frontend/src/lib/harnessClient.ts`: add to `WailsBindingsLike` and to `ToolsClient` (sibling method, NOT a sub-namespace, so `client.tools.pickDirectory(...)`). Implement on the live client and the in-memory `noopClient` (returns empty string for user-cancel mock).
- No new types in `frontend/src/lib/types.ts` strictly required.

### 2.4 DirectoryPicker.vue rewrite shape

- `openPicker()` becomes `async`. Computes `defaultDirectory`:
  1. Empty chip list → empty string (Go falls back to `$HOME` via Wails default).
  2. ≥ 1 chip → derive each chip's parent (`path.split('/').slice(0, -1).join('/')`), compute longest common prefix that ends at `/`. If non-empty → use. Else → most recently added chip's parent (FR-002b).
  3. Calls `client.tools.pickDirectory({ defaultDirectory, title: 'Pick a directory' })`.
  4. Empty return = user-cancel → no-op (no chip, no error toast).
  5. Non-empty = absolute path → existing `add(picked)` (de-dupe stays).
  6. Thrown error → emit `error` event so `RecipeKeyPromptModal` can display.
- Browser-preview detection: try/catch on `useHarnessClient()`. The throw signals browser preview → fall back to existing `<input type="file" webkitdirectory>` flow. Only path where webkitdirectory `<input>` renders.
- Inline-edit-on-click is deleted: chip text becomes `<span>`, not `<button>`. All `editingIndex/editingValue/startEdit/commitEdit/cancelEdit/onEditKeydown` state + functions + template branches removed in WP03.

## 3. Risk register

| Risk | Likelihood | Mitigation |
|---|---|---|
| macOS hardened-runtime / sandbox blocks `OpenDirectoryDialog`. | Low — app currently has no `entitlements.plist`, runs unsandboxed. `OpenDirectoryDialog` is system-wide `NSOpenPanel`-backed. | Confirm by grepping `build/darwin/` for `com.apple.security.app-sandbox` (none at audit time). If hardened runtime is added later, document the entitlement in `scripts/sign-mac.sh`. |
| Browser-preview build breaks because Wails ctx is never wired. | Medium — only matters if a developer relies on browser preview for the picker flow. | Browser-preview path explicitly catches the `wailsBindings()` throw and renders the legacy webkitdirectory `<input>`. Tested in WP04. |
| Existing user flow that pasted absolute paths into the chip via inline-edit loses access. | Medium. Spec FR-005b explicitly accepts this trade. | Documented in component comment. The `ExpandPath` server-side fallback is preserved for legacy callers but unreachable from UI. |
| Wails returns the *contents* path instead of the picked directory. | Very low. | Integration test (WP04) injects a fake Dialoger that returns a known absolute path; verify chip text matches verbatim. |
| The chip-text `<button>` removal breaks accessibility. | Low. The X button stays focusable; chips were never an action target except for inline-edit. | Smoke-check Tab order: env input → config rows → directory picker chips' X buttons → "Add directory" → submit. |
| Common-parent computation produces a parent the user can't read (e.g. `/Users` if chips span two users). | Low. | LCP fallback rule: if chips have divergent parents, use most recently added chip's parent. LCP must end at separator AND be non-empty (not just `/`). |

## 4. Rollout

- **Feature flag**: `HARNESS_DIR_PICKER_NATIVE`. Read on every `PickDirectory` call (no per-process caching) so a stuck user can `export HARNESS_DIR_PICKER_NATIVE=0 && open /Applications/kaneaz-harness.app` and immediately get the legacy fallback. Default = on.
- **Backout**: setting the env var to `"0"` makes the Go RPC return the disabled-flag error. Frontend's catch falls back to webkitdirectory `<input>`. No code revert needed for runtime regression.
- **Telemetry**: not in scope.
- **Migration**: none — chips are transient form state, not persisted.
