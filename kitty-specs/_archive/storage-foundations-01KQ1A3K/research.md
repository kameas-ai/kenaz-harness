# Research Decision Log — Storage Foundations

## Summary

- **Feature**: `storage-foundations-01KQ1A3K` — embedded SQLite app database with optional encryption-at-rest, embedded vector store, migration framework, cross-platform concurrency.
- **Date**: 2026-04-25
- **Researchers**: alecfeeman, Claude (assisting), background research subagent
- **Open Questions** (after research):
  - Confirm libSQL's encrypted-DB *not* readable by stock SQLite tools is acceptable for the OSS user base, or whether we need a documented "decrypt for inspection" workflow.
  - When does "switch to LanceDB" become mandatory rather than optional? Plan-phase decision once we have concrete embedding-volume numbers from the memory/RAG mission.
  - Sandboxed Flatpak/Snap users: do we ship those builds in v1, or defer? Affects Linux backend selection.

---

## Landscape snapshot (April 2026)

### Encrypted SQLite

| Option | License | Go binding | Verdict |
|---|---|---|---|
| **SQLCipher Community Edition** | BSD-3-clause-style (Zetetic) — **requires reproducing copyright in user-accessible UI/about screen** and documentation | `mutecomm/go-sqlcipher` (CGo) | Adds compliance surface (attribution screen, BSD endorsement clause). Workable but friction. |
| **libSQL (Turso fork) + `tursodatabase/go-libsql`** | Apache-2.0/MIT | First-party Go SDK (CGo wrapping Rust) | **Recommended.** Page-level encryption (SQLCipher-compatible cipher + wxSQLite3 AES-256), encrypted WAL and bottomless backups, no attribution screen required, embedded-replica sync as an additive future feature. Caveat: encrypted libSQL files are not readable by stock SQLite tools. |
| **SQLite SEE** | Paid commercial (~$2k+/dev) | n/a | Not appropriate for an OSS harness. |
| **SQLite3MultipleCiphers (wxSQLite3)** | Permissive | Go bindings not first-class as of mid-2026 | Strong technical option, ecosystem too thin for v1. |
| **modernc.org/sqlite (pure Go)** | MIT | Native | No SQLCipher/cipher support — rules out CGo-free encrypted path. |
| **App-layer crypto over plain SQLite** | n/a | n/a | Loses index/range queries on encrypted columns. Suitable as a *complement* (per-column secret encryption), not as the default. |

### Embedded Vector Stores

| Option | License | Status (April 2026) | Verdict |
|---|---|---|---|
| **sqlite-vec** | MIT/Apache-2.0 | v0.1.9 (2026-03-31), explicitly pre-v1 | **Recommended default.** Co-locates with libSQL in the same DB file. Brute-force KNN production-stable; ANN (DiskANN, IVF, rescore) experimental. Realistic ceiling ~1M vectors with binary quantization (32× storage reduction, ~95% recall on text-embedding-3-large). |
| **LanceDB / lancedb-go** | Apache-2.0 | Rust core mature; Go bindings v0.1.2 — early stage | **Recommended opt-in for >500k-vector workloads.** Columnar Lance file format, Mac/Linux/Windows prebuilt native libs, CGo + env-var build complexity. |
| **chromem-go** | MIT | Pure Go, zero deps | **Recommended pure-Go escape hatch.** 100k docs queried in ~40 ms; suitable for small corpora, CGo-free builds, and tests. Not appropriate for >100k. |
| **Chroma server** | n/a | Requires separate process | Wrong shape for a desktop harness. |
| **Qdrant** | n/a | gRPC client only; no embedded mode in 2026 | Not viable for a single-binary desktop app. |
| **Faiss/Annoy/Vespa** | n/a | n/a | No native Go without CGo glue (Faiss); Annoy stale; Vespa server-shaped. Skip. |

### File Portability + Concurrency

- **WAL multi-process**: works on macOS, Linux ext4/btrfs, Windows NTFS. Single writer + multiple readers. Shared-memory `-shm` file required.
- **Windows NTFS critical fix**: SQLite 3.51.0 (2025-11-04) added defenses against `close()`-induced lock breakage in multi-process WAL access. **Pre-3.51 leaves the .db file locked after `close()`.** libSQL tracks current SQLite, so this is automatically handled if we pin libSQL to a release built on SQLite ≥ 3.51.
- **Network filesystems**: WAL is **unsafe** on NFS, SMB, CIFS, Dropbox, iCloud, OneDrive. Shared-memory wal-index cannot be coordinated across hosts; locking primitives are buggy. Authoritative source: sqlite.org/howtocorrupt.html.
- **macOS APFS**: standard POSIX advisory locks work; no 2026-specific issues surfaced.

---

## Decisions & Rationale

| Decision | Rationale | Evidence | Status |
|----------|-----------|----------|--------|
| **D1**: Use **libSQL via `tursodatabase/go-libsql`** as the primary SQLite layer. | Apache-2.0/MIT (no SQLCipher attribution-screen compliance surface), built-in page-level encryption with multiple ciphers, encrypted WAL + backups, first-party Go SDK, single upstream, additive embedded-replica sync as a future feature without re-platforming. | E1, E2, E3, E4, E5; sources `libsql-encryption`, `go-libsql-docs`, `go-libsql-repo`, `libsql-repo`, `embedded-replicas-ga` | final |
| **D2**: Use **`sqlite-vec`** as the default embedded vector store, loaded as an extension into the same libSQL connection. | Co-locates relational + vector data in one DB file (matches local-first invariant), MIT/Apache-2.0, pure C runs anywhere SQLite runs, brute-force KNN + binary quantization covers v1 workloads (≤1M vectors), authoritative perf numbers from author. | E6, E7, E8; sources `sqlite-vec-repo`, `sqlite-vec-perf`, `sqlite-vec-go-guide` | final |
| **D3**: Expose `VectorStore` interface; ship **LanceDB adapter as opt-in** for >500k-vector workloads. | sqlite-vec degrades brute-force latency beyond ~1M vectors. LanceDB's columnar Lance format scales further with HNSW + IVF. Apache-2.0 license aligns. CGo + native lib complexity is acceptable as opt-in. | E9, E10; sources `lancedb-go`, `lancedb-upstream` | final |
| **D4**: Ship **`chromem-go`** as the pure-Go escape hatch for CGo-free builds and small corpora. | Pure Go, zero deps, 100k docs queried in ~40 ms — covers tests, CI environments, and operators who insist on no-CGo. Not the default because performance ceiling is too low. | E11; source `chromem-go` | final |
| **D5**: Pin minimum supported SQLite to **3.51.0** (2025-11-04) via libSQL release floor. | Earlier versions leave the main DB file locked after `close()` on Windows NTFS in multi-process WAL access — a documented production hazard. | E12, E13; sources `bun-issue`, `sqlite-wal` | final |
| **D6**: **Refuse to open the DB on NFS / SMB / CIFS / cloud-sync paths by default**, with a clear error and an opt-out flag. Detect non-local mounts via OS APIs. | WAL on networked filesystems is corruption-prone per authoritative SQLite documentation. The default of "fail loud" prevents silent corruption that operators only discover after losing audit data. | E14, E15; sources `sqlite-corrupt`, `gotosocial-storage-guidance` | final |
| **D7**: **Encryption-at-rest is opt-in but recommended on by default for new installs**; operators on FileVault/BitLocker/dm-crypt may opt out and rely on disk encryption. The encryption key is fetched from OS keychain via the `secrets-keychain` mission. | Charter requires encryption-at-rest but acknowledges environments where double-encryption is wasteful. Default-on protects users who have no other layer; opt-out is honest for those who do. | charter, llm-connector FR-003 (credential-reference machinery) | final |
| **D8**: **Operator guidance to ship in product**: (1) refuse non-local mounts by default, (2) require libSQL release built on SQLite ≥ 3.51, (3) document that two harness instances on one machine under WAL is supported but a remote-mounted DB is not. | All three are direct outputs of the file-portability research. The guidance prevents the common operator failure modes. | E12, E14, E15 | final |

---

## Evidence Highlights

- **Key insight 1 — libSQL gives equivalent crypto to SQLCipher with a permissive license and no attribution-screen requirement.** Single decision unblocks the encryption story (E1, E4).
- **Key insight 2 — sqlite-vec is pre-v1 (v0.1.9) but explicit about it.** Author acknowledges breaking changes are possible. Mitigation: pin a specific extension version; abstraction layer (D3) lets us swap without code changes if the format ever moves. (E6)
- **Key insight 3 — Binary quantization on text-embedding-3-large gets ~95% recall with 32× storage reduction.** This is the lever that lets sqlite-vec cover the v1 working set. (E8)
- **Key insight 4 — Windows NTFS WAL `close()` lock bug is real and recent.** SQLite 3.51 (Nov 2025) is the floor. Production reports include bun#25964. (E12, E13)
- **Key insight 5 — Network filesystems are an absolute "no" for WAL.** Authoritative SQLite docs are explicit. Defaulting to refuse-with-message is a posture choice that avoids silent audit-log corruption. (E14, E15)
- **Risks / Concerns**:
  - libSQL encrypted files are not readable by stock SQLite tools; document this and provide a `harness db decrypt` operation for incident response and forensics.
  - LanceDB Go bindings are at v0.1.2 — early stage. Treat as opt-in with planning-phase reassessment.
  - sqlite-vec's pre-v1 status implies migration risk if format changes; pin and document migration policy.
  - Sandboxed Flatpak/Snap Linux builds need XDG portal integration for keychain access, which compounds with the secrets-keychain mission. Coordinate.

---

## Next Actions

1. Update `storage-foundations` spec.md Open Question 1 (encryption library): SQLCipher question is resolved → libSQL. Either update spec or note resolution in planning.
2. Reflect D1 (libSQL) in any cross-cutting docs: charter languages_frameworks list, llm-connector spec assumptions about the storage layer, event-log spec.
3. Decision for planning phase: which libSQL release floor pins us to "SQLite ≥ 3.51"? Pick a stable libSQL tag.
4. Decision for planning phase: ship `harness db decrypt` operation for forensic / migration use; plan UX.
5. Decision for planning phase: choose detection method for non-local mounts on each OS (macOS `getmntinfo`, Linux `/proc/mounts`, Windows `GetDriveType`).
6. Coordinate with `secrets-keychain` mission for the database encryption key reference shape.

> Keep this document living. Revisit if libSQL's encryption story regresses, sqlite-vec releases v1 (which may shift defaults), or LanceDB Go bindings reach v1.0 (would justify promoting to default for high-volume).
