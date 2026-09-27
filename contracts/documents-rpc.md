# Documents RPC contract (spec 092 US1/US3 local slice + knowledge sites)

**Status:** DRAFT — local to kenaz-harness; not yet mirrored into workspace `CONTRACTS.md`
**Owner:** `golang` (Go surface), `frontend` (Vue view + TS client)
**Last updated:** 2026-09-16
**Mission:** `kitty-specs/documents-knowledge-site-01NDOCS01/` (gitignored, local)
**Upstream:** workspace `specs/092-kenaz-documents/spec.md` FR-001..FR-017; the
store and site format are `core/docs` (commit `9a73c50b`).

This is the decision record that gates the transport. Nothing below may be
implemented differently without changing this file first.

---

## 1. Scope

The `Documents_*` family lets a person — not the model — list, read, create,
edit, preview, and build documents into a **local, self-hostable static
knowledge site**, from the harness app in both entry points:

- desktop (Wails bindings on `*rpc.Bindings`), and
- served mode (the default app inside every Kenaz workbench: `POST /rpc`).

Both entry points expose the **same seven methods** (§3). There is no
served-only or desktop-only half, so both I15 drift allowlists stay untouched.

**Out of scope, by constitution rather than by schedule:** publishing or
uploading anything (§XIII); Fleet Sites calls; team/org classification (spec
092 plan R-7); a WYSIWYG editor (spec 092 plan R-2 / operator question 0.7).
The editing workflow is plain HTML or Markdown source with a server-sanitized
preview.

## 2. Authorization and visibility (read before adding a method)

1. **Transport auth is unchanged.** Served mode: the single
   `HARNESS_SERVE_TOKEN` bearer on `/rpc`. Desktop: the Wails bridge. There is
   no per-method gradation, exactly as for `Sessions_*`.
2. **Every document method except `Documents_Preview` takes a `sessionId`, and
   the session must exist.** The view resolves it through
   `api.Sessions().Get` before touching the store. An unknown id is rejected
   with `no_session`; a caller cannot mint a session id to create orphan
   documents.
3. **Visibility is `core/docs.Service`'s, never re-implemented.** A session
   sees its own session-scoped documents and global documents; classification
   must be `personal`. Anything else — another session's document, a non-doc
   unit, a team/org unit — is `document_not_found`, never a distinct
   "forbidden", so existence does not leak. The UI therefore has **no
   cross-session document list**: to see another session's documents it must
   name that session, which the same bearer can already do for its messages
   (`Sessions_ListMessages`). This family grants nothing `Sessions_*` does not.
4. **`Documents_BuildSite` resolves every requested id through the same
   visibility check** (`Service.Get` per id, or `Service.ListVisible` when the
   list is empty). A site can only contain documents the named session sees.
5. **Writes are user actions.** The model-tool Settings dials
   (`save_artifact`, filesystem write) and the Cedar filesystem gate govern
   *agent tools* (`kenaz__*`); no user-initiated RPC in this codebase consults
   them, and these do not either. The builder's own invariants still hold for
   user builds: output only under `<workspace>/documents-exports/`, symlinked
   export paths refused, a directory that is not a previous build of the same
   slug never replaced (`docs.WriteSite`).
6. **Privacy.** No title, body, or filesystem path in any log line (FR-032).
   Paths are returned to the caller (the UI must show them); they are not
   logged.

## 3. Methods

All params and results are JSON objects with camelCase keys. Timestamps are
RFC3339Nano UTC strings.

| Method | Params | Result |
|---|---|---|
| `Documents_List` | `{sessionId}` | `DocumentSummary[]` — own session docs, then global; each group newest first |
| `Documents_Get` | `{sessionId, id}` | `Document` |
| `Documents_Create` | `{sessionId, title, body}` | `Document` (version 0) |
| `Documents_Update` | `{sessionId, id, baseVersion, body}` | `Document` (version + 1) |
| `Documents_Preview` | `{body}` | `PreviewResult` |
| `Documents_BuildSite` | `{sessionId, slug, title, documentIds}` | `SiteBuildResult` |
| `Documents_ExportsDir` | `{}` | `{dir}` — the absolute `<workspace>/documents-exports` path builds write under |

Shapes:

```ts
DocumentSummary = { id, title, scope: 'session'|'global', version, contentSha256, byteSize, createdAt, updatedAt }
Document        = DocumentSummary & { body }            // body is the sanitized, stored HTML
PreviewResult   = { html, sanitized: boolean, byteSize } // html is exactly what Create/Update would store
SiteBuildResult = {
  siteDir, publicDir, bundle, bundleSha256,             // absolute paths on the machine running the harness
  documents: number,
  warnings: { documentId, warnings: string[] }[],      // Markdown-companion degradation (FR-015)
  published: false                                      // always false; this family never publishes
}
```

`body` is always HTML on the wire. Markdown authoring is converted to HTML
**client-side** with the already-bundled `marked`, then sent through
`Documents_Preview` / `Create` / `Update`, where the Go sanitizer (the store
invariant) produces the canonical form. The preview the user sees is the
server's sanitized output rendered in the existing `IframeSandbox`
(`sandbox=""`, injected CSP) — never client-rendered HTML.

## 4. Errors

Errors travel as the transport's error string (Wails rejection / served
`{"error": "..."}`) in exactly this form:

```
documents: <code>: <fixed message>
```

`<code>` is one of the `core/docs` wire codes: `document_not_found`,
`invalid_title`, `empty_body`, `no_session`, `version_conflict`,
`content_too_large`, `content_too_nested`, `content_not_utf8`,
`invalid_site_slug`, `no_documents`, `too_many_documents`,
`export_target_in_use`, plus `bad_params` and `unavailable` (no database
wired). Messages are fixed per code and never contain document text, so an
error can be shown verbatim. The TS client exposes `documentsErrorCode(err)`
which extracts `<code>`; the view branches on it (notably
`version_conflict` → conflict banner offering reload).

## 5. Binding rules (enforced)

- Go: `*rpc.Bindings` methods ↔ `core/serve/server.go` dispatch cases ↔
  `core/serve/methods.go` `servedMethods` — `check-serve-dispatch-drift.sh`
  and `TestServedMethodsMatchDispatchSwitch`.
- TS: `WailsBindingsLike`, `createHarnessClient().documents`,
  `createServedHarnessClient().documents`, `createFakeHarnessClient().documents`
  — `harnessClient.documents.test.ts` asserts the served overlay calls every
  method in §3 by its wire name, so the Go allowlist and the TS overlay cannot
  drift apart (the spec 092 plan H-RPC3 test, scoped to this family).
- Caller: `views/documents/DocumentsView.vue`, routed at `/documents` in both
  `main.ts` and `main-served.ts`, reached from a `LeftRail` entry.

## 6. What would make this stale

Adding team/org classification, a publish action, a per-method auth model, or
cross-session listing. Each of those changes §2 and must land here first.
