# Spec — Save artifact built-in tool (`save-artifact-builtin-01KQCZ4C`)

**Status**: draft · **Owner**: alecfeeman

## 1. Why

The harness ships two built-in tools today: `kenaz__web_search` (gated by `Settings.WebSearchEnabled`) and `kenaz__bash` (gated by `Settings.BashEnabled`). Saving a deliverable today routes through MCP filesystem servers (`filesystem` sandboxed to `~/.harness/agent-workspace`, or `filesystem-full` unrestricted). Since commit `1d7679e`, those `__write_file` calls produce artifacts via `core/rpc/views/artifacts/sink.go::captureFromFileWriteToolArgs`, but they require:

1. The user has enabled an MCP filesystem recipe (`RecipeKeyPromptModal` approve-and-grant flow).
2. The agent picks and manages a path.
3. A real file lands on disk **in addition** to the artifact row.

A fresh install with no MCP recipes can't save anything. The recipe-gating is also a cognitive tax for "save this as a deliverable" — the user shouldn't have to think about filesystem permissions.

A dedicated `kenaz__save_artifact(title, content[, mime_type])` built-in solves the cleanest deliverable-saving path:

- **Zero recipe gating** — works on first launch like bash and websearch.
- **CAS-only storage** — content lands in the existing `attachments.MediaStore` + `core/artifacts` row pipeline; no filesystem touchpoint.
- **Explicit intent** — the tool name signals "this is something the user wants to keep" to the model, leading to better tool-selection accuracy than "discover through trial that filesystem-full is unrestricted enough."
- **Testable surface** — the artifact is the only side effect; no two-system reconciliation between disk + DB.

The artifact capture pipeline (`core/artifacts.Manager.Capture`) already handles dedup, mime detection, source tracking, and the SQL row. The new tool is mostly glue — the heavy lifting exists.

## 2. Goals

- A third in-binary tool sitting alongside `kenaz__web_search` and `kenaz__bash`, available on first launch with no setup.
- Single-call save: `(title, content[, mime_type])` in → artifact ID out.
- Result content the model can reference in its next turn (so it can tell the user "I saved Q4-summary.md, see the Artifacts tab").
- Honest surface: when disabled, returns an error result the model can recover from; never silently captures.
- Coexistence: the `1d7679e` file-write capture path stays as is — both paths produce artifact rows independently.

## 3. Functional requirements

| ID | Requirement | Status |
|---|---|---|
| FR-001 | New package `core/tools/saveartifact/` mirroring `core/tools/websearch/` and `core/tools/bash/`. Implements `toolloop.BuiltinTool` (`Name()`, `Description()`, `InputSchema()`, `Call()`). | proposed |
| FR-002 | Tool name is `kenaz__save_artifact` (constant `saveartifact.ToolName`); description: "Save a named deliverable to the session's artifacts. Use this when the user asks you to save, export, or produce a document/file. Returns an artifact ID the user can find in the Artifacts tab." | proposed |
| FR-003 | Input schema: `title` (string, required, ≤ 200 runes), `content` (string, required, ≤ 10 MiB UTF-8), `mime_type` (string, optional). | proposed |
| FR-004 | `Call(ctx, args)` translates `(title, content, mime_type)` into a `core/artifacts.CaptureCandidate` with `Source = SourceToolOutput`, `SourceRef = {MessageID: "tool:kenaz__save_artifact"}`, then invokes `Manager.Capture(ctx, []candidates, sessionID)`. The session ID is supplied by the chassis at construction (per-call session passed through tool-call context — see FR-009). | proposed |
| FR-005 | Mime-type inference: explicit `mime_type` arg wins; otherwise `mime.TypeByExtension(path.Ext(title))`; otherwise `text/plain; charset=utf-8`. | proposed |
| FR-006 | Tool result on success: JSON `{"artifact_id": "<ulid>", "title": "<title>", "size": <bytes>, "mime_type": "<mime>"}`. The model uses these fields in its next assistant turn to confirm the save. | proposed |
| FR-007 | Tool result on failure: `IsError: true` with a structured message (`{"error": "<kind>", "message": "<human>"}`). Failure kinds: `disabled`, `invalid_args`, `content_too_large`, `capture_failed`. | proposed |
| FR-008 | Settings dial `Settings.SaveArtifactDisabled bool` (inverted form, default false → tool ENABLED on a fresh install). Accessor `Settings.SaveArtifactEnabled() bool { return !s.SaveArtifactDisabled }` mirrors the `AutoCaptureCodeBlocks*` pattern. Per-field RPC pair `Settings_GetSaveArtifactEnabled` / `Settings_SetSaveArtifactEnabled` for the frontend toggle. | proposed |
| FR-009 | `core/rpc/builtins_wiring.go::registerBuiltinTools` constructs a `saveartifact.New(Options{Manager, SessionResolver})` and registers it. The `EnabledFilter` predicate in `builtinEnabledPredicate` adds a `case saveartifact.ToolName` branch reading `store.LoadSaveArtifactEnabled()`. The session ID flows through the existing tool-call context plumbing (the chassis already passes session ID through `toolloop.MCPPool.Call` via context — confirm during plan). | proposed |
| FR-010 | Frontend: `KenazToolsPanel.vue` (or wherever the `WebSearchEnabled` / `BashEnabled` toggles live today) gains a third toggle row "Save artifact" wired to `Settings_GetSaveArtifactEnabled` / `Settings_SetSaveArtifactEnabled`. Default-on indicator visible. Layout, copy, and styling match the existing two rows. | proposed |
| FR-011 | The tool surfaces in the `BuiltinPool.Tools()` discovered catalog only when `Settings.SaveArtifactEnabled() == true`. Toggling the dial mid-session takes effect on the next tool-discovery pass without a restart (existing `EnabledFilter` semantics). | proposed |
| FR-012 | Disabled-toggle path: `EnabledFilter.Lookup` returns `(nil, false)` → the model never sees the tool. Defence-in-depth: even when invoked directly (e.g. via a stale tool-catalog reference), `Call()` short-circuits with `IsError: true` and `error="disabled"` if `SaveArtifactEnabled()` is false at invocation time. | proposed |
| FR-013 | Captured artifact has `scope_kind = "session"` (the manager's default). Project promotion stays an explicit user action via the existing artifact preview UI. | proposed |

## 4. Non-functional requirements

| ID | Requirement | Threshold |
|---|---|---|
| NFR-001 | Test runtime budget | New tests run under `-race -count=1 -short` and add ≤ 2s to total `core/...` test wall time. |
| NFR-002 | Save round-trip latency | < 100ms for content ≤ 1 MiB on a developer laptop (CAS write + row insert; no network). |
| NFR-003 | Memory ceiling per call | Bounded by `content` size cap (10 MiB) plus a small constant; no streaming required at this size. |

## 5. Constraints

| ID | Constraint |
|---|---|
| C-001 | The new package lives at `core/tools/saveartifact/` to match the existing `core/tools/{websearch,bash}/` layout. (User brief mentioned `core/builtins/saveartifact/`; corrected to match repo reality.) |
| C-002 | `Source = SourceToolOutput` (consistent with `1d7679e`'s file-write capture). Provenance tracks the tool invocation, not a fenced-block index. |
| C-003 | The artifact is scoped to the live session (`scope_kind = "session"`); project promotion remains a separate explicit action via the artifact preview UI. |
| C-004 | No filesystem touchpoint. The tool MUST NOT create files outside the existing `attachments.MediaStore` CAS path. |
| C-005 | The tool's `Name()` MUST match `kenaz__` prefix convention so `BuiltinPool` namespaced dispatch round-trips correctly. |
| C-006 | Default ON. The user can disable from Settings; default behaviour on a fresh install is "available on first launch like bash and websearch" (note: bash and websearch ship default OFF in the current codebase — this tool deliberately diverges, hence the inverted-disabled wire shape). |

## 6. Locked open questions

- **Q1 (default state)**: **Locked: Default ON.** The user's brief says "Ships ON by default. The user can disable it from KenazToolsPanel like the other built-ins." Rationale: saving deliverables is a low-risk, high-utility primitive — the cost of an unwanted save is "user deletes one artifact row"; the cost of a default-off is "model can't save without setup." Wire shape `SaveArtifactDisabled bool` (inverted) matches the `AutoCaptureCodeBlocksDisabled` precedent for default-true semantics.
- **Q2 (size cap)**: **Locked: 10 MiB content cap.** Above this, return `error="content_too_large"`. Rationale: the artifact content is held in memory during `Manager.Capture` (the bytes are passed wholesale to `MediaStore.Put`); a hard cap prevents an LLM-induced memory blow-up. 10 MiB is well above any expected single deliverable size and consistent with media-store practice elsewhere.
- **Q3 (deduplication)**: **Locked: Trust existing CAS dedup.** `MediaStore.Put` already dedups by content hash. If the model calls `save_artifact` twice with identical content, two `artifacts` rows point at the same `content_hash` — the user sees two entries in the Artifacts tab with potentially different titles, which is the correct UX (each call represents a distinct intent). No tool-level dedup added.
- **Q4 (`update_artifact` follow-up)**: **Locked: Out of scope.** A future `kenaz__update_artifact` for in-place edits is explicitly deferred per the user's brief. Each call here produces a new artifact; the existing dedup handles content-identical re-saves.

## 7. Success criteria

- Fresh install (no MCP recipes enabled) → user asks "save this as Q4-summary.md" → assistant invokes `kenaz__save_artifact` → artifact appears in the Artifacts tab within 100ms of the tool result returning. No filesystem write happens.
- Toggle "Save artifact" off in `KenazToolsPanel` → next chat turn: tool no longer appears in the model's available-tools list. Existing artifacts remain visible.
- Toggle "Save artifact" on → tool reappears on the next turn without restarting.
- Content > 10 MiB → tool returns `IsError: true` with `error="content_too_large"`; no row inserted, no media write.
- Same `(title, content)` saved twice → two artifact rows, one underlying CAS entry (dedup verified by content_hash equality).
- Frontend toggle row renders with the default-on state on a fresh install; persists across reload.

## 8. Out of scope

- Editing or replacing existing artifacts via this tool (a separate `kenaz__update_artifact` is a follow-up if needed).
- Deletion via tool (user-only via the artifact preview UI).
- Cross-session pinning (a project-promotion concern handled elsewhere).
- Auto-promotion to project scope (explicit user action remains required).
- Content streaming (10 MiB cap fits comfortably in memory; streaming adds complexity for no MVP gain).
- Modifying the `1d7679e` file-write capture path. Both paths coexist.
- Any change to MCP filesystem recipes, the `RecipeKeyPromptModal`, or the artifacts pipeline beyond the new `Capture` caller.
