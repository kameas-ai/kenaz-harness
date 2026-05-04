# tasks.md — Wails-native folder picker

## WP01 — Tools.PickDirectory RPC + frontend client method

**Title**: Add Wails-backed `Tools.PickDirectory` Go RPC, expose through Bindings, surface in harnessClient.

**Dependencies**: none.

**Effort**: M.

**Files touched**:
- `core/rpc/views/tools/api.go` (add `PickDirectoryOptions`, extend `ToolsAPI` interface).
- `core/rpc/views/tools/picker.go` (new — `PickDirectory` impl + `Dialoger` seam + `SetContext`).
- `core/rpc/views/tools/impl.go` (add `wailsCtx` + `mu` + `dialer Dialoger` field).
- `core/rpc/api.go` (extend `*rpc.API.SetContext` fan-out to `a.toolsImpl`; reshape `newToolsAPI` to return `*tools.API`).
- `core/rpc/stubs.go` (add `(s *stubTools) PickDirectory(...)` returning `("", nil)`).
- `core/rpc/bindings.go` (add `Tools_PickDirectory` after `Tools_RecipeConfig`).
- `frontend/src/lib/harnessClient.ts` (add to `WailsBindingsLike`, `pickDirectory` to `ToolsClient`, both live and noop).

**Acceptance**:
1. `go test ./core/rpc/views/tools/...` passes; new picker-impl unit test injects a fake Dialoger and asserts (a) returned path forwards verbatim, (b) Wails-ctx-not-wired returns clear error, (c) `HARNESS_DIR_PICKER_NATIVE=0` returns disabled-flag error without invoking the dialer.
2. `go test ./core/rpc/...` passes (bindings still build).
3. `npm run typecheck` passes — `client.tools.pickDirectory({ defaultDirectory: '', title: '' })` resolves to `Promise<string>`.
4. `wails generate module` re-emits a binding for `Tools_PickDirectory` without errors.

---

## WP02 — DirectoryPicker.vue pick-only flow + common-parent default

**Title**: Rewrite "Add directory" to call `client.tools.pickDirectory()` with the LCP-derived default; emit a single chip on success, no-op on cancel.

**Dependencies**: WP01.

**Effort**: M.

**Files touched**:
- `frontend/src/views/tools/DirectoryPicker.vue` (replace `openPicker` with async RPC call; add `computeDefaultDirectory()` helper; preserve `add()` de-dupe; emit `error` event for system-failure surfacing).
- `frontend/src/views/tools/RecipeKeyPromptModal.vue` (wire the new `@error` listener to populate `errorMsg.value`; remove the now-dead `dirpicker-edit-` carve-out in `onKeydown`).

**Acceptance**:
1. With ≥2 chips sharing a parent, the picker is invoked with `defaultDirectory` = the shared parent (asserted via stubbed `pickDirectory`).
2. With chips that have divergent parents, `defaultDirectory` falls back to the most recently added chip's parent.
3. Empty chip list → `defaultDirectory` is empty string.
4. `pickDirectory` returning empty string ⇒ no `update:modelValue` emission.
5. `pickDirectory` returning an absolute path ⇒ chip added, de-dupe respected.
6. `pickDirectory` throwing ⇒ `error` event emitted with the message.

---

## WP03 — Remove inline-edit + desktop webkitdirectory fallback

**Title**: Delete inline-edit-on-click affordance and the desktop-rendered webkitdirectory `<input>`; preserve `<input>` only when `window.go.rpc.Bindings` is unavailable (browser preview).

**Dependencies**: WP02.

**Effort**: S.

**Files touched**:
- `frontend/src/views/tools/DirectoryPicker.vue` (delete `editingIndex`, `editingValue`, `editingOriginal`, `editingInput`, `startEdit`, `commitEdit`, `cancelEdit`, `onEditKeydown`, the cancel-on-external-mutate watcher, and the `<input v-if="editingIndex === i">` template branch; replace chip text `<button>` with `<span>`; gate the `<input type="file" webkitdirectory>` behind a `useFallback` boolean set at setup via try/catch around harnessClient resolution).

**Acceptance**:
1. Mounting in JSDOM (no `window.go.rpc.Bindings`) renders the webkitdirectory `<input>` and the existing fallback test passes.
2. Mounting with stubbed `window.go.rpc.Bindings` does NOT render the webkitdirectory `<input>` (querySelector null).
3. Clicking chip text emits no events and does not enter edit mode (no `dirpicker-edit-N` element).
4. `dirpicker-chip-text-N` test-id renders as `<span>`, not `<button>`.
5. `npm run lint` passes — no unused refs.

---

## WP04 — Test sweep: drop inline-edit specs, add RPC integration test

**Title**: Update Vue test fixtures to match the picker-only contract; add Go integration test for the new RPC surface.

**Dependencies**: WP01–WP03.

**Effort**: S.

**Files touched**:
- `frontend/src/views/tools/__tests__/DirectoryPicker.test.ts` (delete inline-edit cases; rename "webkitdirectory file pick" to "browser-preview fallback adds picked root once" and gate on `window.go.rpc.Bindings === undefined`; add three new cases: empty default with empty list, common parent of chips as default, empty-string return is no-op).
- `frontend/src/views/tools/__tests__/RecipeKeyPromptModal.test.ts` (delete chip inline-edit assertions if present).
- `core/rpc/views/tools/picker_test.go` (new — covers user-cancel, system-failure, feature-flag-disabled, ctx-not-wired branches).

**Acceptance**:
1. `npm run test:unit` passes — all DirectoryPicker assertions reflect the picker-only contract.
2. `go test ./core/rpc/views/tools/...` passes; new `picker_test.go` provides ≥80% line coverage of the new file.
3. `go vet ./...` and `golangci-lint run` emit no new findings.
4. CI privacy-invariant test still passes — `runtime.OpenDirectoryDialog` is not an event emitter, doesn't trip the rule.
