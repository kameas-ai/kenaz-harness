# Session Export — Operator Guide

Mission: `session-export-01NDFSEX05`

Target release: **v0.14.0**.

## Status

| WP | Title | Status |
|---|---|---|
| WP01 | Cedar action + audit kind + policy file | **Shipped** |
| WP02 | Pure export renderer + RPC surface + Wails binding | **Shipped** |
| WP03 | Frontend menus (LeftRail + SessionHeader) | **Shipped** |
| WP04 | Integration tests + operator docs | **Shipped** |

## What ships in v0.14.0

One-click export of a session transcript to a local file in Markdown or
JSON format.  The export is:

- **Local-only** — no upload, no cloud storage; the file goes exactly
  where the user's OS save-dialog places it.
- **Credential-safe** — the redaction engine (10 built-in patterns) runs
  over every string field in every message before bytes touch the disk.
  Redacted substrings are replaced with `<REDACTED:profile_id>` in the
  output.
- **Policy-gated** — Cedar action `Action::"session.export"` guards every
  export call.  The default policy (`policies/default_session_export_policy.cedar`)
  permits the local user.  A custom policy can restrict the format or
  disable export entirely.
- **Auditable** — a `session.export` audit event fires on every successful
  export.  The payload includes `session_id`, `format`, `output_basename`,
  and `byte_count`.  The full file path is **never** recorded — only the
  basename is included (privacy invariant).

## User-facing surfaces

### Per-session row in the left rail

Each session row shows a Download icon button that appears on hover.
Clicking it opens a native `window.confirm()` prompt:

> Export session — click OK for Markdown (.md) or Cancel for JSON (.json).

After the user picks the format, the OS-native save dialog opens with a
suggested filename (`<session-title>-<YYYY-MM-DD>.<ext>`).

### SessionHeader "Export…" button

In the active-session chat view, the session header bar exposes an
**Export…** button (to the left of "Suggest new title"). The same
two-step flow applies: confirm dialog → OS save dialog.

Clicking **Cancel** on the OS save dialog silently closes the flow with
no file written and no error shown.

## File formats

### Markdown (`format = "markdown"`)

Structure:
```
---
session_id: <id>
export_format_version: 2
exported_at: <RFC3339>
---

# <Session Title>

## Turn 1

**user** · <timestamp>

<message content>

## Turn 2

**assistant** · <timestamp>

<message content>

<details>
<summary>🔧 bash</summary>

```json
{ "command": "ls -la" }
```

Result:
...

</details>
```

Inline image and document attachments are extracted to a sibling
`<filename>-artifacts/` directory and linked with relative paths.

### JSON (`format = "json"`)

```json
{
  "meta": {
    "export_format_version": 2,
    "exported_at": "2026-05-14T10:00:00Z",
    "session_id": "sess-abc"
  },
  "session": {
    "id": "sess-abc",
    "name": "My Session",
    "created_at": "2026-05-14T09:00:00Z",
    "updated_at": "2026-05-14T10:00:00Z"
  },
  "messages": [
    {
      "id": "msg-1",
      "session_id": "sess-abc",
      "sequence": 1,
      "role": "user",
      "content": "Hello!",
      "created_at": "2026-05-14T09:01:00Z",
      "tool_calls": []
    }
  ]
}
```

Attachment bytes are inlined as base64 within each message's
`content_blocks` array field.  No external sidecar files are written for
JSON exports.

## Cedar policy customisation

The default policy is bundled at
`core/policy/cedar/policies/default_session_export_policy.cedar`:

```cedar
permit (
    principal == User::"local",
    action == Action::"session.export",
    resource is Session
);
```

To restrict export to Markdown only, add a custom policy:

```cedar
forbid (
    principal == User::"local",
    action == Action::"session.export",
    resource is Session
) when {
    context.format == "json"
};
```

Operators can disable export entirely by forbidding the action for all
principals.

## Credential redaction patterns

The redaction engine applies these patterns before serialisation:

| Profile ID | Pattern description |
|---|---|
| `anthropic-key` | `sk-ant-` prefix followed by ≥ 20 alphanumerics |
| `openai-key` | `sk-` prefix followed by ≥ 20 alphanumerics |
| `aws-access-key-id` | `AKIA` prefix followed by 16 uppercase alphanumerics |
| `aws-secret-key` | `secret_access_key = …` style key-value pair |
| `jwt` | Three-part base64url `eyJ…` string |
| `bearer-token` | `Bearer <token>` in any case |
| `basic-auth` | `Basic <base64>` in any case |
| `pem-block` | `-----BEGIN PRIVATE KEY-----` blocks |
| `generic-password-querystring` | `password=`, `secret=`, `apikey=` style KVPs |
| `github-token` | `ghp_`, `gho_`, `ghu_`, `ghs_`, `ghr_` prefixes |

Redacted substrings appear in the output as `<REDACTED:profile_id>`, e.g.
`<REDACTED:anthropic-key>`.  The pattern is deterministic and does not
use HMAC or session-specific salts, so the same credential always
produces the same marker across exports.

## Audit event

Kind: `session.export`

Payload shape (Go):
```go
type SessionExportPayload struct {
    SessionID      string `json:"session_id"`
    Format         string `json:"format"`
    OutputBasename string `json:"output_basename"`
    ByteCount      int64  `json:"byte_count"`
}
```

The `output_basename` field is the filename component of the path
(e.g. `my-session-2026-05-14.md`) — the full directory path is never
recorded.

## Known limitations / follow-ups

- **Audit emitter not yet bridged**: the `audit.Emitter` port in
  `sessions.WithExportOpts` is wired as `nil` at the RPC boot layer
  (consistent with other consumers: workflows, cedarpolicy, branches).
  Until `core/context/audit.Emitter` is bridged to the ring-buffer path,
  export audit events are silently dropped.  Tracked for a follow-up.
- **Format selector UX**: the current `window.confirm()` approach is
  functional but minimal.  A future UX polish pass can replace it with a
  small modal that also exposes the "include sub-agent branches" checkbox
  discussed in the spec's open questions.
- **Sub-agent branches**: sessions with branch children do not
  automatically include the branch transcripts in the export.  The
  top-level session is exported alone.  Including branches is tracked as
  a follow-up to this mission.
