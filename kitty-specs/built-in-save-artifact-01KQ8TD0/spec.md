# Spec: Built-in `save_artifact` tool

**Status**: draft · **Owner**: alecfeeman · **Planning base**: `main` · **Merge target**: `main`

## 1. Why

Two built-in tools today (`kaneaz__bash`, `kaneaz__web_search`) plus three MCP filesystem recipes for file writes. After commit `1d7679e` the filesystem `__write_file` path produces artifacts via `core/rpc/views/artifacts/sink.go:captureFromFileWriteToolArgs`, but it requires a recipe install, leaves a real file on disk, and forces path-management on the model. A fresh-install user can produce zero artifacts.

A dedicated `kaneaz__save_artifact(title, content[, mime_type])` built-in solves the cleanest deliverable-saving path: zero recipe gating, CAS-only storage, explicit intent signalling.

## 2. Goals

- New built-in tool `kaneaz__save_artifact` discoverable alongside the other kaneaz built-ins.
- Tool result returned to the model: `{"artifact_id":"...","title":"...","size":N,"mime_type":"..."}`.
- Settings dial `Settings.SaveArtifactEnabled` (default `true`).
- Capture path uses `Source: SourceToolOutput`; provenance tracks tool invocation.
- **Artifacts are immutable post-save and never auto-deleted.** User-only deletion via the artifact preview UI.
- **Large artifacts trigger a confirmation prompt, not a refusal.** Threshold configurable via `Settings.SaveArtifactPromptBytes` (default 5 MB).

## 3. Non-goals

- Editing existing artifacts (covered by `update-artifact-tool` mission).
- Tool-driven deletion (user-only via UI).
- Cross-session pinning (project-promotion is a separate concern).

## 4. Functional requirements

| ID | Requirement | Status |
|---|---|---|
| FR-001 | Package `core/builtins/saveartifact/` implements `toolloop.Builtin` mirroring `core/builtins/websearch/`. | proposed |
| FR-002 | Tool surfaces as `kaneaz__save_artifact` in tool discovery. | proposed |
| FR-003 | Schema requires `title` + `content` (non-empty); optional `mime_type`. | proposed |
| FR-004 | Mime resolution: explicit arg → `mime.TypeByExtension(filepath.Ext(title))` → `text/plain; charset=utf-8`. | proposed |
| FR-005 | Capture via `coreart.Manager.Capture` with `Source: SourceToolOutput`, `SourceRef.MessageID: "tool:kaneaz__save_artifact"`. | proposed |
| FR-006 | Tool result is JSON `{artifact_id, title, size, mime_type}`. | proposed |
| FR-007 | New `Settings.SaveArtifactEnabled` (default `true`) and `Settings.SaveArtifactPromptBytes` (default 5 MB). | proposed |
| FR-008 | Tool gated by `EnabledFilter`; disabled → absent from discovery. | proposed |
| FR-009 | Writes with `len(content) > SaveArtifactPromptBytes` trigger a frontend confirm dialog. Cancel → `IsError: true`; Save → normal capture. | proposed |
| FR-010 | KaneazToolsPanel renders a toggle row + a numeric input for the prompt threshold. | proposed |
| FR-011 | Cedar policy gate applies to writes via existing `gate.Add` hook. | proposed |
| FR-012 | The pruner never auto-deletes `kaneaz__save_artifact`-sourced rows. | proposed |

## 5. Non-functional requirements

| ID | Requirement | Threshold | Status |
|---|---|---|---|
| NFR-001 | Capture latency, sub-MB content. | p95 ≤ 50 ms. | proposed |
| NFR-002 | Confirm round trip. | UX latency Save click → tool result ≤ 500 ms. | proposed |
| NFR-003 | Test suite addition. | ≤ 2 s added to core test time under `-race -short`. | proposed |

## 6. Constraints

| ID | Constraint | Status |
|---|---|---|
| C-001 | Builtin path is `core/builtins/saveartifact/`. | accepted |
| C-002 | Source enum value is `SourceToolOutput` (matches `1d7679e`'s file-write capture). | accepted |
| C-003 | File-write capture from `1d7679e` is unchanged; both paths coexist. | accepted |
| C-004 | Confirm dialog uses the existing `ConfirmToolModal` pattern. | accepted |

## 7. Success criteria

- Fresh-install user (no MCP recipes) can ask agent to "save this as design.md" → row in Artifacts tab.
- 100% of >5 MB writes block until user-approved (test fixture).
- Disabling the toggle removes the tool from discovery within one StartStream.
- No regression in existing `core/rpc/views/artifacts/...` tests.

## 8. Acceptance scenarios

- **A1** Chat-graph integration test calls the tool against a fake provider; one row inserts with the right source/mime/size; tool result has the artifact id.
- **A2** 6 MB content → `confirm.tool.large_save` broker event fires; user-Save commits, user-Cancel does not.
- **A3** `SaveArtifactEnabled: false` → tool absent from `chat.tool_discovery.ok` log entry.
- **A4** Prune cycle leaves `kaneaz__save_artifact` rows untouched.
