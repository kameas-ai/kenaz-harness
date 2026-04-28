# Research Decision Log — Bundle Format and Resolver

## Summary

- **Feature**: `bundle-format-resolver-01KQ1A3J` — manifest format, distribution channels (especially OCI/ORAS), lockfile design, supply-chain integrity (Sigstore / SLSA), hashing primitives.
- **Date**: 2026-04-25
- **Researchers**: alecfeeman, Claude (assisting), background research subagent.
- **Caveat**: WebFetch was denied for this subagent's session; findings are derived from WebSearch result summaries plus subagent's working knowledge of these tools. Confidence is "high" only where I have independent verification or where the WebSearch summary cites a primary source. Re-confirm any claim before locking the v1 design.
- **Open Questions** (after research):
  - YAML 1.1 vs 1.2 parser — which Go library do we pin?
  - Sigstore keyless signing requires OIDC; how does an OSS contributor sign locally without a keyless flow? (Likely: Ed25519 detached signatures as the offline path; Sigstore for CI-built artifacts.)
  - Lockfile sort order — `(name, version, source)` deterministic, but what locale are we using for string comparison? (Default: byte-wise, no locale.)

---

## Landscape snapshot (April 2026)

### Manifest format

| Format | 2026 status | Verdict |
|---|---|---|
| **YAML 1.2** | Universal language of declarative tooling (Kubernetes, Helm, GitHub Actions, OCI itself). Norway problem fixed in 1.2 (2009) but PyYAML / LibYAML / some Go libs still default to 1.1 — **must explicitly pin a 1.2 parser**. | **Recommended.** Mitigate Norway with mandatory quoted strings for short identifiers and a published JSON Schema. |
| TOML | Gaining ground for application config (Cargo, uv, Poetry, Ruff). Better for flat config; awkward for deeply nested data. No Norway-class surprises. | Defensible second choice. Skip for v1; YAML wins on ecosystem familiarity. |
| KDL | Node-based; small-scale adoption (Alice package manager moved TOML→KDL in 0.4.0). Tiny ecosystem. | Skip for v1. |
| Pkl (Apple) | Full programming language for config; powerful, heavyweight; will stay niche outside Apple. | Skip for v1; users can pipe Pkl/CUE/Jsonnet output into our CLI. |
| CUE | Type-safe constraints, schema unification. Steep learning curve. | Worth offering as an *optional* schema layer over YAML. |
| Jsonnet, Nickel, KCL, Dhall, HCL | Niche. | Skip. |

### OCI registries and ORAS

- **`oras-project/oras-go` v2** is the production line; v1 is legacy. Supports the two latest Go releases on a rolling basis. Provides unified push/pull/copy across remote registries, OCI layouts, in-memory stores, and filesystems.
- **Auth is solved**: `oras-go/v2/registry/remote/credentials` reads `~/.docker/config.json`, supports `credsStore` / `credHelpers`, integrates with macOS Keychain, Windows Credential Manager, and `pass` on Linux.
- **Distribution-spec v1.1** (released 2024) adds the `subject` field on manifests and the `/v2/<name>/referrers/<digest>` endpoint — this is how non-image artifacts (signatures, SBOMs, attestations) get associated with a primary artifact without tag-naming hacks. ECR, GHCR, GAR, ACR, Harbor, JFrog, Quay, and Zot are all conformant in 2026.
- **For "ORAS-friendly"** a registry needs: blob/manifest push-pull, custom `mediaType`/`artifactType`, and ideally the Referrers API. If a registry lacks Referrers, ORAS automatically falls back to a tag-based referrers list.
- **Adopters worth copying**: Helm 3.8+ (charts as OCI), Cosign (signatures), in-toto (attestations), WASM (wasm-to-oci, now standardized as OCI artifact type), AI model bundles (ModelKit/KitOps push GGUFs as OCI). Helm's auth pattern — `helm registry login` writing to docker config — is the de-facto UX.
- **Auth flow universals**: OAuth2 token exchange against the registry's `/v2/token` with HTTP Basic creds from docker config. Quirks: ECR tokens expire every 12h (need `aws ecr get-login-password` refresh); GAR uses short-lived `gcloud auth print-access-token`; GHCR wants a PAT with `read:packages`/`write:packages`. `oras-go` handles all of this transparently.

### Lockfile prior art

- **Cargo.lock** (gold standard): flat `[[package]]` array-of-tables, schema `version` field (now V4), single-line checksum per package, deterministic sort. Transitive deps don't cascade — only affected entries change. **Use this shape.**
- **npm `package-lock.json`** v1/v2 had nested-tree merge-conflict issues; v3+ flattened into `packages` keyed by install path.
- **`pnpm-lock.yaml`** v6 deliberately removed hashes from package IDs for readability; added `git-branch-lockfiles`. Lesson: a `lock --resolve-conflicts` subcommand pays off.
- **`uv.lock`** (Astral, 2024–2025): TOML format, **universal/cross-platform** — captures all valid resolutions across OS/arch/Python-version markers in one file. Python now has tool-agnostic `pylock.toml`. **For an artifact-not-source bundle system, the universal model is exactly right.**
- **`Pipfile.lock` / `poetry.lock`**: nested, noisy diffs. Poetry has a content-hash protecting against partial edits.
- **`go.sum` / `go.mod`**: cleanest split for an artifact-not-source system. `go.mod` declares versions; `go.sum` is line-based hash records. Backed by Go Checksum Database (`sum.golang.org`). **Lesson**: separate "what I want" from "what I got"; line-oriented hashes; consider a public transparency log analog (Sigstore Rekor does this for free).
- **Pitfalls to avoid**: nested structures (false conflicts), non-deterministic sort, single-platform locks, missing source pinning (typosquat / dependency-confusion), no schema-version field.

### Supply-chain integrity

- **Sigstore is the consensus answer in 2026.** Cosign signatures use the OCI 1.1 referrer model — a signature is just an artifact whose `subject` points at the thing it signs. Keyless signing via Fulcio binds an OIDC identity to an ephemeral key; the signature is logged in Rekor.
- **`sigstore/sigstore-go`** is the right Go SDK — stable, minimal-dependency, passes the sigstore-conformance suite, **explicitly the recommended embedding library** (Cosign itself is being refactored to depend on it).
- **in-toto attestations** are the recommended payload format inside Sigstore signatures. SLSA Provenance is one predicate type. As of late 2025, in-toto attestation framework v1 is broadly adopted by SLSA, GitHub `attest-build-provenance`, and GitLab signing examples.
- **SLSA target**: v1.0 stable, v1.2 RC2 was out for review November 2025. Realistic v1 target for kaneaz-harness: **L2** (signed provenance from a hosted builder). L1 is "we generated provenance"; L2 adds a digital signature; L3 requires hardened builder isolation. GitHub Actions reusable workflows give L3 later; L2 is the right v1 bar.
- **Hashing**: SHA-256 mandatory because every OCI registry uses it as the digest algorithm. BLAKE3 is 4–10× faster and avoids length-extension attacks, but the perf gap is irrelevant for typical artifact sizes (MB–GB). Recommend SHA-256 canonical, optional `additionalHashes` field for BLAKE3.
- **Signing primitives**: **Ed25519** is the unambiguous default for new offline keys in 2026 — deterministic, no RNG-failure class of bugs, faster, smaller signatures.

---

## Decisions & Rationale

| Decision | Rationale | Evidence | Status |
|----------|-----------|----------|--------|
| **D1**: Manifest format is **YAML 1.2** with a published JSON Schema and explicit-string-tagging in docs. | Universal in declarative tooling our users already know. Norway-problem mitigation: pin 1.2 parser, mandate quoted strings for short identifiers. | E1, E19 | final |
| **D2**: OCI client is **`oras.land/oras-go/v2`** with `oras.land/oras-go/v2/registry/remote/credentials` for auth via docker-credential-helpers. | First-party, production-line, auth-solved across Docker Hub / GHCR / ECR / GAR / Harbor / Nexus / Zot. | E2, E9 | final |
| **D3**: Conform to **OCI Distribution-Spec v1.1**; use `subject` + Referrers API for signatures and attestations. Tag-based fallback handled by ORAS automatically. | All major registries are conformant in 2026; aligns with Helm / Cosign / in-toto patterns. | E4, E5, E12 | final |
| **D4**: Signing path is **Sigstore via `sigstore-go`** (not the full Cosign codebase) for keyless OIDC signing + Rekor transparency. **Ed25519 detached signatures** as the offline / air-gapped fallback. | sigstore-go is the recommended embedding library; Cosign is moving to depend on it. Ed25519 is the unambiguous default for new offline keys. | E13, E18 | final |
| **D5**: Hash algorithm is **SHA-256 mandatory** (matches OCI digest baseline); optional `additionalHashes` field in lock for BLAKE3 if anyone wants it. | OCI registries use SHA-256 as digest; BLAKE3 perf gap is irrelevant at typical bundle sizes. | E10 | final |
| **D6**: Lockfile format is **TOML, Cargo-flavored** (`kaneaz.lock` at project root). Flat array-of-tables, deterministic sort by `(name, version, source)`, schema `version` field, per-artifact SHA-256 + signature ref, source URL, resolution metadata. **Universal/cross-platform** like `uv.lock`. | Cargo.lock is the gold standard for transitive-dep-stable diffs; uv.lock is the modern entrant for the universal model — bundles aren't platform-specific source so this fits. | E6, E7, E14, E16 | final |
| **D7**: SLSA target for v1 is **Build Track Level 2** (signed provenance from a hosted builder like GitHub Actions). L3 is achievable later via reusable workflows. | L2 is the realistic enterprise-pilot bar; L3 requires builder isolation work that doesn't pay off until we have a hosted CI pipeline of our own. | E11, E17 | final |
| **D8**: Ship a `kaneaz lock --resolve-conflicts` subcommand that re-runs resolution to merge conflicting lockfiles. | pnpm taught everyone that lockfile merge conflicts are inevitable; building the tool now saves operator pain. | E16 | final |
| **D9**: **Operator UX expectation**: Helm-style `kaneaz registry login` that writes to docker config so operators don't learn a new auth surface. | De-facto pattern across Helm and other OCI-artifact tools. | E12 | final |

---

## Evidence Highlights

- **Key insight 1 — `oras-go v2 + oras-go credentials` solves the OCI auth + multi-registry problem out of the box.** No per-registry code. (E2, E9)
- **Key insight 2 — Distribution-Spec v1.1 + Referrers is universally adopted.** Signatures and attestations attach via `subject`, not tag hacks. (E4, E5)
- **Key insight 3 — Cargo.lock is the gold standard for diff stability; uv.lock is the modern universal-cross-platform innovation.** Both lessons apply. (E6, E7)
- **Key insight 4 — Sigstore via `sigstore-go` is the embedding library; full Cosign is over-kill.** (E13)
- **Key insight 5 — SLSA L2 is the realistic v1 bar.** (E11)
- **Risks / Concerns**:
  - WebFetch was denied for the subagent — re-verify each cited URL's primary content before locking the v1 design.
  - Sigstore keyless signing requires OIDC; OSS contributors signing locally need the Ed25519 fallback. UX must surface both paths cleanly.
  - YAML's foot-guns (Norway, indentation, anchors) require consistent linting in our manifest validator.

---

## Next Actions

1. Resolve `bundle-format-resolver` Open Question 1 (YAML vs TOML for manifest): D1 — YAML 1.2.
2. Resolve Open Question 2 (lockfile location): keep `kaneaz.lock` at project root (Cargo / uv convention).
3. Resolve Open Question 3 (semver-ranges vs exact-pins): ranges in `kaneaz.yaml`, exact pins in `kaneaz.lock` (standard pattern).
4. Plan-phase: pick a Go YAML 1.2 library; pin in go.mod.
5. Plan-phase: write the JSON Schema for the manifest and lock format.
6. Plan-phase: write the kaneaz analog of `helm registry login`.
7. Plan-phase: integrate `sigstore-go` for the signing path; design the offline Ed25519 fallback UX.
8. Plan-phase: design the `kaneaz lock --resolve-conflicts` subcommand UX.

> Re-verify URLs before locking the v1 design. The subagent's WebFetch was denied; primary-source verification on `oras-go`, `sigstore-go`, OCI v1.1, and `uv.lock` is worth a 30-minute pass before final commitment.
