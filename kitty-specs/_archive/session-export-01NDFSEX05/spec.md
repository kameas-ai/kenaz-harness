# Spec: Session export (Markdown / JSON)

**Status**: draft · **Owner**: alecfeeman

## 1. Why

Sessions are first-class persistent objects but they live behind the app. Users who want to share a useful chat, archive a finished investigation, or feed a session into an external system (a docs site, an issue tracker, a paper draft) have no path that doesn't involve manual copy-paste.

This is a small, highly-requested distribution affordance with a clean security story: export is local-only, user-initiated, and explicit about what's included. It's also a precursor to **Share to fleet** (`fleet-integration-01KX5R8D`) — the format we settle on here is the same payload that ships to fleet later.

## 2. Goals

- One-click export of a session as Markdown (rendered for humans) or JSON (lossless for tooling).
- Export bundles attached artifacts inline (Markdown: linked or embedded; JSON: base64 + manifest entries).
- Redaction-aware: anything tagged by the credential-hygiene analyzer is replaced with `<REDACTED:profile_id>`.
- Audit event on every export with redaction-safe payload (session_id, output_path, format, byte_count).
- Cedar action `Action::"export_session"` gates the operation; default policy permits it for the current user.

## 3. Non-goals

- Upload to external services. Local-disk export only; share-to-fleet is its own mission.
- Import. Round-tripping an exported session back into the harness is post-1.0.
- PDF or HTML rendering. Markdown is the human format; downstream tools handle further conversion.
- Selective per-turn redaction UI. Whole-session export only; per-turn refinement is a follow-up.

## 4. Functional requirements

### Data

| ID | Requirement | Status |
|---|---|---|
| FR-001 | New RPC `Sessions_Export(session_id, format) -> {path, byte_count}` where `format ∈ {"markdown", "json"}`. | proposed |
| FR-002 | Markdown format: H1 session title, H2 per turn, role label, message body with code-fence preservation, tool calls as collapsible `<details>` blocks, attached artifacts referenced by relative path. | proposed |
| FR-003 | JSON format: complete `{session, messages[], tool_calls[], tool_results[], artifacts[]}` payload with stable schema version field. | proposed |
| FR-004 | Both formats include a `meta` header: `harness_version`, `export_format_version`, `exported_at`, `session_id`, `provider`, `model`. | proposed |
| FR-005 | Credential bytes in any field run through the existing redaction filter before serialization. Redactions are explicit in the output (`<REDACTED:profile_id>`), not silent. | proposed |

### Surface

| ID | Requirement | Status |
|---|---|---|
| FR-006 | Sessions view: per-row "Export…" menu item; in-session: "Export this session" in the session-header overflow menu. | proposed |
| FR-007 | Native file-picker via Wails runtime; default filename `<session-title>-<YYYY-MM-DD>.{md,json}`. | proposed |
| FR-008 | Cedar audit log entry per export call: `Kind == "session_export"`, payload includes session_id + format + output basename (NOT path; absolute paths are user data). | proposed |

## 5. Open questions

- **Markdown granularity.** Do we include tool-call payloads inline, or only the high-level "tool X was called → succeeded" line? Proposal: collapsible details with the JSON inline; consumers can strip them.
- **Artifact embedding.** Inline base64 in JSON balloons file size for image-heavy sessions. Markdown linking to a relative path needs a directory-form export. Proposal: JSON inlines base64; Markdown writes a sibling `<filename>-artifacts/` directory.
- **Sub-agent branches.** Should a session export include its child branches? Proposal: include by default; opt out via a checkbox in the export dialog.

## 6. Acceptance criteria

- A 20-turn session with two attached PDFs and three tool calls exports to both Markdown and JSON without errors.
- The Markdown renders cleanly in GitHub's preview and in Obsidian.
- A redacted credential string in the session shows as `<REDACTED:profile-anthropic-01>` in both formats and is *not* present in plaintext in either output.
- An audit event fires with the correct kind and a redaction-safe payload.
