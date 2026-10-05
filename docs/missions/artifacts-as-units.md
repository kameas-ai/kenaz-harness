# Artifacts as units — decision record

Mission `artifacts-as-units-01DOGF0C` (WP01). Finishes
`unified-context-artifacts-01NCTXU01` FR-003: artifacts become `core/units`
rows with `kind=artifact`. Every citation below was re-read against `main` @
`b8079d48` (v0.85.2) on 2026-10-04. The mission's spec/plan/tasks live in the
gitignored `kitty-specs/`; this file is the committed copy of the WP01
decisions so reviewers of the WP04 migration can check it against something.

## D1 — Field mapping (spec FR-1)

`units` row (one per artifact):

| units column | value | source |
|---|---|---|
| `id` | the artifact id, **unchanged** | `artifacts.id` (D2) |
| `kind` | `'artifact'` | constant |
| `scope` | `scope_kind` | `session` / `project` / `global` — already the units vocabulary |
| `scope_id` | session → `session_id`; project → `project_id`; global → `''` | NULL → `''` (the column is `NOT NULL DEFAULT ''`) |
| `classification` | `'personal'` | constant — see D3 |
| `load_policy` | `'on_demand'` | constant — `ResolveLoadable` only injects `always` units, so artifacts never enter model context by themselves |
| `version` | `MAX(artifact_versions.version)`, or `1` when synthesized (D4); new inserts start at `0` | |
| `title` | `title` | |
| `body` | `''` | bytes stay in the media CAS (01NCTXU01 non-goal). The migration cannot read `<DataDir>/media/` from inside a SQL transaction, and a text copy would be a second source of truth for content the CAS already owns. FR-1's "body = text for small text artifacts" is **declined** for this mission; a later reader that wants inline text resolves `content_hash` through the media store. |
| `metadata` | `{content_hash, byte_size, mime_type, source, source_ref, session_id, project_id, legacy_artifact_id}` | `source_ref` is the parsed `source_ref_json` object; an unparseable value is kept verbatim under `source_ref_raw` (lossless) |
| `created_at` | `created_at` | |
| `updated_at` | newest version's `created_at`, else `created_at` | |

`session_id` and `project_id` are kept in metadata even though `scope_id`
carries one of them: an artifact has BOTH an origin session and (optionally) a
project at once, and `ArtifactFilter{SessionID}` / `{ProjectID}` filter on the
origin columns regardless of scope (`store_sql.go:149-156`). `scope_id` alone
cannot answer those queries.

**Head row = the capture, not the newest revision.** Doc units rewrite the
head on every `Update`; artifacts never did — `ArtifactVersion` is append-only
and `Get` returns the original capture (`artifact.go:96-100`). The units-backed
store preserves that: `WriteVersion` appends a `unit_versions` row and bumps
`units.version`/`updated_at` only. `units.Manager.Update` is never called on an
artifact unit.

`unit_versions` row (one per `artifact_versions` row): `unit_id` = artifact
id, `version` = `version`, `body` = `''`, `metadata` =
`{content_hash, byte_size, mime_type, summary, path, legacy_version_id}`,
`created_at` = `created_at`. `ArtifactVersion.ID` becomes the
`unit_versions.id` (the legacy integer id cannot be preserved — doc units
already occupy that AUTOINCREMENT space; it is kept as `legacy_version_id`,
and nothing outside the store reads `ArtifactVersion.ID`).

**Promote lineage.** `UpdateScope` re-scopes the row **in place** — same id,
no second row (`store_sql.go:189-246`). `unit_edges(promoted_from)` needs two
units; there is only one. So promote writes no edge; the decision is to keep
in-place semantics (id stability, D2) over an edge FR-1 pencilled in as "WP01
finalises". `units.Manager.Promote` (copy + edge) is not used for artifacts.

## D2 — Ids are preserved (FR-2)

Artifact ids come from `artifactULIDGen` (`store_sql.go:457-539`): 26-char
Crockford base32, 48-bit ms + 80 random bits. Unit ids come from
`defaultUnitID` (`core/units/ulid.go:11-14`): the same format from an
independent generator. Collision probability between the two namespaces is
the ULID random-collision probability (negligible), and both columns are
`TEXT PRIMARY KEY`, so the migration does not trust the odds: it counts
`artifacts JOIN units ON id` first and **aborts** (whole transaction rolls
back) if any id is already taken.

Newest snapshot check (v0.85.2 `dump.sql`): artifact ids
`{seed-artifact-1}`, unit ids `{seed-unit-1, seed-unit-2}` — disjoint. Every
committed snapshot carries the same seed corpus (`testdata/upgrade/seed.sql`).

Consequence: no `legacy_artifact_id` lookup is needed by any consumer;
`SourceRef`, chat references, plan-mode `PlanID`s and persisted UI state keep
resolving. `legacy_artifact_id` is still written to metadata so the provenance
of a migrated row is visible.

## D3 — Sync eligibility (FR-5 input)

The fleet push path: `UnitSyncer.PushDirty` (`core/fleet/unit_sync.go:133-160`)
iterates `[ClassTeam, ClassOrg]` only and calls `Manager.ListDirty(class)`,
which returns `nil` for `ClassPersonal` (`core/units/manager.go:147-150`) and
otherwise lists by classification. `MapUnitToNode` additionally refuses
personal units (`unit_mapper.go:68-80`).

So the eligibility rule is **classification ∈ {team, org}**. Kind is not
consulted. Migrated and newly captured artifact units are written
`personal`, and no artifacts code path ever changes classification
(`UpdateScope` touches scope only), so **they would not be pushed today**.
WP05 is therefore a `test:` WP pinning the invariant, not a `fix:`.

The one route by which an artifact unit could become team/org is the fleet
view's explicit promote-as-merge-request on a unit id — an explicit share,
which the spec permits. No UI lists artifact units in the fleet view
(`Unit_ResolveLoadable` forces `load_policy=always`, `resolution.go:212-216`;
artifacts are `on_demand`).

## D4 — Version-less artifacts get a synthesized v1

`sessions/0327` emptied `artifact_versions` on installs that reached it with
version rows, and a never-updated artifact also has zero rows. The two are
indistinguishable after the fact. Per FR-3 every version-less artifact gets a
synthesized `unit_versions` v1 from the parent row (metadata carries
`"synthesized": true`) and `units.version = 1`. A migrated artifact with real
versions 1..k keeps exactly 1..k.

Observable effect: the next `update_artifact` on a migrated version-less
artifact reports version 2. `ListVersions` has **no production reader**
(only the store implementations and their tests call it) — the version history
is write-only in the product today, which is noted for the unwired ledger
(WP07), not fixed here. New artifacts inserted after the migration start at
version 0 with no history row (legacy behaviour; first update = v1).

## D5 — Migration id: `units/1104-artifacts-to-units`, not `sessions/03xx`

The spec pencils `sessions/03xx-artifacts-to-units` "after G's and D's". The
runner applies pending migrations in **version order across all blocks**
(`registry.go:164-179` `Pending` walks `All()` ascending; `runner.go` `Apply` runs that slice in order). A sessions-block
migration (version 3xx) therefore runs **before** `units/1100-init` on every
fresh install — the `units` table does not exist yet and the copy fails, so a
fresh install could not open its database. Version order is the hard
constraint; the block number is not cosmetic.

`units/1104` sits after every `sessions/03xx` migration in every apply pass,
which is a strictly stronger form of the ordering the plan asks for: it runs
after G's dedupe and D's mapping migration **whatever numbers they take**, so
C's referential checks always see post-G `session_messages` ids, and there is
no `sessions/03xx` number for the three missions to race for. Verified no
sibling worktree claims `units/1104+` (2026-10-04). Per-mission contiguity
(`VerifyLedger`) is unaffected: 1104 is the next units version after 1103.

Hazard noted for the future: after this migration `artifacts` and
`artifact_versions` no longer exist under those names. Any later migration
that references them must target `artifacts_legacy` /
`artifact_versions_legacy` or run before 1104 — and nothing should, since the
next release drops them (FR-3.3).

## D6 — Legacy tables renamed, never dropped

`ALTER TABLE artifacts RENAME TO artifacts_legacy` and
`ALTER TABLE artifact_versions RENAME TO artifact_versions_legacy`.
`legacy_alter_table` is OFF (SQLite default), so the child FK in
`artifact_versions` is rewritten to point at `artifacts_legacy`; no
`DROP TABLE`, no implicit `DELETE`, so the 0327/0332 cascade class cannot
fire. The legacy rows keep their own `sessions(id) ON DELETE SET NULL` FK —
deleting a session still nulls their `session_id`, which is harmless for a
read-only archive. Dropping them is a separate migration in the next release
(WP07 files it).

## D7 — Agentgraph `artifact` node does not touch the store

`artifactExecutor.Execute` (`core/agentgraph/exec_state.go:358-386`) defaults
`output_target` to `session_message` and, for every target, only emits an
`artifact_emitted` event (`mime_type`, `output_target`, `attachment_ref`,
`content_bytes`). It holds no `artifacts.Store` or `Manager`. No target writes
the artifacts store, so the node needs no change for this mission.

## D8 — Media refcount + delete cascade

- The units-backed store's `RefcountFor(hash)` counts `units` **and**
  `unit_versions` rows whose `metadata.content_hash` matches, across all kinds
  (counting more than artifacts is strictly safer for byte retention). The
  legacy source counted only parent rows; version blobs were kept alive only by
  their `media_artifacts` row.
- Migration 1104 adds expression indexes on
  `json_extract(metadata,'$.content_hash')` for both tables (and on
  `$.session_id` for the session filter) so `PruneOrphans` — one refcount query
  per file on disk — stays an index probe, not a full scan per file.
- The store switch and the `RegisterRefcountSource` call move together in
  WP04's single commit (`core/rpc/api.go` `newArtifactsStack`).
- `units.scope_id` has no FK. Session and project deletes reach artifact units
  through explicit code: `session.Manager` and `projects.Manager` gain delete
  observers; the artifacts store registers a purge that deletes the
  session/project-scoped artifact units (versions and edges in the same
  transaction, not left to the FK pragma) and nulls `metadata.session_id` /
  `metadata.project_id` on units that merely originated there (the legacy
  `ON DELETE SET NULL` parity). Media GC then reclaims the bytes. The
  `rpc/views/sessions` delete funnel keeps its existing explicit
  list-then-delete-then-sweep cascade on top of that.
- Project delete **removes** project-scoped artifact units (spec FR-6). Before
  this mission the FK nulled `project_id` and left a project-scoped row with no
  project — visible only in the unfiltered list. This is a deliberate,
  spec-ruled behaviour change.

## D9 — Rail entry and scope of the new surface

- Rail label **"Library"** (icon: the existing Archive glyph). E's mission names
  its entry "Knowledge"; no collision. Route `/library/:view(captured|authored)`.
- Views: **Captured** (today's ArtifactsView: source/scope/mime filters,
  preview, promote, provenance; save-from-message stays in the chat message
  menu where it lives today) and **Authored** (today's DocumentsView: editor,
  sanitized preview, version conflicts, BuildSite).
- Shared chrome: one search box filtering both views by title, owned by the
  Library shell. Scope filter and delete stay inside each view this release —
  their semantics differ (artifacts: session/project/global across the whole
  store; documents: one session's visible set).
- `/artifacts` → `/library/captured`, `/documents` → `/library/authored`
  (query string preserved, both desktop and served routers); palette entry
  `nav.library` replaces `nav.artifacts`; persisted `lastRoute` values of the
  old paths resolve through the redirects.
- "Convert captured → authored document" is **out of scope** (not required by
  FR-7; it would need a body-from-CAS read path this mission declines in D1).
- E's generic section switcher is not on `main`; the Library shell ships its own
  two-tab switcher and marks the swap point. Nav-IA (F) restructuring of
  `LeftRail.vue` is not touched beyond replacing the two entries with one.

## D10 — Batching plan

The copy is three `INSERT … SELECT` statements plus verification inside the
migration's one write transaction — no per-row Go loop, so there is no batch
boundary to manage and the all-or-nothing guarantee is the transaction's.
The 10k-artifact synthetic measurement is taken in WP04 against the real
migration (`TestMigration1104_SyntheticTenThousand`, skipped under `-short`)
and recorded below; batching is only introduced if it exceeds a few seconds.

Measured (WP04, 2026-10-04, Apple Silicon laptop, `go test` without
`-race`): `storagesqlite.Open` of the v0.85.2 snapshot inflated to 10,001
artifacts and 10,002 version rows — i.e. the whole pending set, 1104
included — took **301 ms** (7.6 s under `-race`). No batching: the single
transaction stays all-or-nothing and well under a second at 10k.

## Records (WP07)

- **01NCTXU01 FR-003 — shipped** by this mission (WP04: migration
  `units/1104-artifacts-to-units` + `newArtifactsStack` on
  `artifacts.NewUnitsStore`; WP06: one Library surface). Archive note to
  append to `kitty-specs/_archive/unified-context-artifacts-01NCTXU01/spec.md`
  (gitignored; mirror in the release PR description):

  > FR-003 shipped 2026-10-04 by artifacts-as-units-01DOGF0C: artifacts are
  > `core/units` rows with `kind=artifact` (migration
  > `units/1104-artifacts-to-units`, ids preserved, legacy tables renamed
  > `*_legacy` for one release). Capture, list, promote (including
  > `scope=global`) and delete go through the unified store; the UI is the
  > Library rail entry with Captured and Authored views.

- **Roadmap** (`docs/roadmap.md`, gitignored): move "artifacts as units /
  unified Library" to *Already shipped* in the release that carries 1104,
  and slot `units/1105-drop-artifacts-legacy` into the following release.
- **Unwired ledger** (`docs/unwired-ledger.md`, 2026-10-04 entries): closes
  "KindArtifact defined, test-only"; files the dated drop-legacy follow-up
  (owner: this mission); records the write-only version history found in D4.
