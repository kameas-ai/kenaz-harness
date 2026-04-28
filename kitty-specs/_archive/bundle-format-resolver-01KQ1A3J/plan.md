# Implementation Plan — Bundle Format and Resolver

**Feature Branch**: `feat/bundle-format-resolver-01KQ1A3J`
**Mission slug**: `bundle-format-resolver-01KQ1A3J`
**Created**: 2026-04-25
**Status**: Draft (HOW)
**Input**: `spec.md` (WHAT — 18 FRs, 7 NFRs, 5 constraints), `research.md` (D1–D9 final), `data-model.md` (entities, relationships, validation rules).

This plan is the *HOW* for the foundation mission. It cites research decisions by id (D1–D9) without re-litigating them. Implementation is deliberately deferred: this document fixes architecture, package layout, public API shape, internal layering, data-model bindings, integration points, phasing, and risks.

---

## 1. Overview

The bundle layer is the universal distribution substrate of kaneaz-harness. Every other unit of harness configuration — provider profiles (`llm-connector`), agent definitions and MCP servers (`acp-orchestration`), context packs (`shared-context-distribution`), signed cards (`a2a-signed-cards-trust`), hooks, scripts — ships *inside a bundle*. A bundle is therefore the single packaging primitive the rest of the system is allowed to assume.

This mission defines five inseparable parts of that substrate:

1. **Bundle format** — `kaneaz.yaml` manifest (YAML 1.2, JSON-Schema-validated; D1) plus a directory of named artifacts.
2. **Distribution channels** — pluggable adapters for `git`, `oci`, `http_mirror`, `local_path` (D2, D3, D9).
3. **Lockfile** — `kaneaz.lock` at project root, TOML, Cargo-flavored, schema-versioned, deterministic byte-wise sort (D6).
4. **Integrity model** — SHA-256 mandatory for every artifact and the manifest as a whole (D5); optional Sigstore keyless or detached Ed25519 signatures, verified through the public API of `a2a-signed-cards-trust` (D4).
5. **Resolver** — graph build → fetch → verify → activate, deterministic, cancellable, append-only event-logged, local-first.

The resolver is invoked at startup, on lockfile change, and on operator-initiated update. With a warm cache it is local-only (no network), which is the charter's non-negotiable steady-state invariant (NFR-005, C-004). With a cold cache it is dominated by network fetch but reports per-artifact progress and is cancellable without leaving partial state (FR-015, FR-016).

The existing stubs in `core/bundle/{lockfile.go,manifest.go,resolver.go,store.go}` are placeholders; they are replaced by the package layout in §2 and the API in §3. The stub field shapes (e.g. `Manifest.Exports`, `Manifest.Overrides`) are largely consistent with this plan and will be migrated, not reinvented.

### What this plan does *not* cover

- Bundle UI, marketplaces, or pack-authoring tooling beyond the format.
- The trust-anchor configuration surface (owned by `a2a-signed-cards-trust`).
- Credential storage internals (owned by `secrets-keychain`).
- Event-log persistence internals (owned by `event-log` / `storage-foundations`).
- Each *kind handler's* internal logic — provider profile parsing, MCP descriptor activation, etc. live in their own missions and packages; this plan only defines the *registry* and the *contract*.

---

## 2. Architectural Placement

DIRECTIVE_001 (architectural integrity) requires every channel and every artifact kind to live in its own subpackage. The package tree below enforces that — the resolver `core/bundle/resolver` has no compile-time dependency on any specific channel or kind implementation.

```
core/bundle/                          # public façade — exports the API in §3
├── bundle.go                         # top-level entry: New(), Resolver, Lockfile, Manifest
├── errors.go                         # typed error sentinels (see §3.6)
│
├── manifest/                         # YAML 1.2 manifest parsing + JSON Schema validation
│   ├── manifest.go                   # Manifest struct, semver helpers
│   ├── parser.go                     # YAML 1.2 parser (decision § 8 — pin one library)
│   ├── schema.go                     # embedded JSON Schema for kaneaz.yaml
│   └── schema/
│       └── kaneaz.yaml.schema.json   # canonical schema, embedded via go:embed
│
├── lockfile/                         # TOML, Cargo-flavored, schema-versioned
│   ├── lockfile.go                   # Lockfile struct, deterministic Marshal/Unmarshal
│   ├── canonical.go                  # byte-wise canonical sort + serialization
│   ├── schema.go                     # version constants, migration table
│   └── merge.go                      # `kaneaz lock --resolve-conflicts` core (D8)
│
├── resolver/                         # graph build + fetch + verify + activate
│   ├── resolver.go                   # Resolver impl
│   ├── graph.go                      # dependency-graph construction, cycle detection
│   ├── plan.go                       # ResolvedGraph + activation order (deterministic)
│   ├── activate.go                   # activation phase: dispatch to kind handlers
│   ├── conflicts.go                  # FR-009 conflict detection + precedence application
│   ├── progress.go                   # per-artifact progress reporting (FR-016)
│   └── cancel.go                     # FR-015 cancellation safety + rollback
│
├── cache/                            # content-addressable local cache
│   ├── cas.go                        # CAS API: Has/Get/Put by SHA-256
│   ├── layout.go                     # on-disk layout (see §5.3)
│   └── evict.go                      # GC + TTL policies
│
├── integrity/                        # hashing + signature dispatch (verifier client)
│   ├── hash.go                       # SHA-256 mandatory; optional BLAKE3 alongside (D5)
│   └── signature.go                  # façade that calls a2a-signed-cards-trust public API
│
├── channels/                         # one subpackage per channel kind (DIRECTIVE_001)
│   ├── channel.go                    # Channel interface (see §3.4)
│   ├── registry.go                   # ChannelRegistry: Register(kind, factory)
│   ├── git/                          # git: go-git, HTTPS / SSH refs
│   │   └── git.go
│   ├── oci/                          # oci: oras.land/oras-go/v2 (D2, D3)
│   │   ├── oci.go
│   │   ├── auth.go                   # docker-credential-helpers via oras credentials
│   │   └── referrers.go              # OCI v1.1 Referrers API for signature/SBOM lookup
│   ├── http/                         # http_mirror: HTTP(S) GET + content-hash verify
│   │   └── http.go
│   └── localpath/                    # filesystem reads
│       └── localpath.go
│
├── kinds/                            # one subpackage per artifact-kind handler
│   ├── kind.go                       # ArtifactKindHandler interface (see §3.5)
│   ├── registry.go                   # KindRegistry: Register(kind, handler)
│   └── (concrete kind packages live in their own missions, e.g.
│        core/llm/profilekind/, core/mcp/serverkind/, …)
│
└── events/                           # bundle-event kinds emitted to the event log
    └── events.go                     # event kind constants, payload structs
```

Charter constraints met by this layout:

- **C-001 (architectural integrity)**: every channel and kind has its own package. The resolver depends on `channels.Channel` and `kinds.ArtifactKindHandler` interfaces only.
- **DIRECTIVE_001**: same — addable by registration, not by core surgery (NFR-007, US5/SC-005).
- **Charter local-first**: `cache/` and `localpath/` channel ensure steady-state runs without network.

### Stub migration

The four files in `core/bundle/` today (`lockfile.go`, `manifest.go`, `resolver.go`, `store.go`) are absorbed into the new tree:

- `manifest.go` → `core/bundle/manifest/manifest.go` (extended with `schema_version`, `signatures`, `artifacts[].kind`, `artifacts[].content_hash`).
- `lockfile.go` → `core/bundle/lockfile/lockfile.go` (TOML, schema-versioned, flat `[[bundle]]` array; existing `LockedBundle.Source` shape generalizes to channel URL).
- `resolver.go` → `core/bundle/resolver/resolver.go` (Resolver interface preserved, signature widened to return `ResolvedGraph` per §3).
- `store.go` → `core/bundle/cache/cas.go` (CAS by SHA-256 digest).

Migration is a single mechanical refactor in WP-001 of the tasks phase; no field-name churn is in scope of this plan.

---

## 3. Public API (Illustrative)

These signatures are the *shape* of the public surface, not the final code. Keep them stable across implementation; widen with care.

### 3.1 Top-level façade — `core/bundle/bundle.go`

```go
package bundle

// New constructs a Resolver wired with the default registries. Callers who
// need custom channels or kinds may replace registries via options.
func New(opts ...Option) (Resolver, error)

type Option func(*config) error

func WithDataDir(path string) Option           // CAS root
func WithChannelRegistry(r channels.Registry) Option
func WithKindRegistry(r kinds.Registry) Option
func WithEventEmitter(e events.Emitter) Option // event-log integration
func WithCredentialResolver(c secrets.Resolver) Option // secrets-keychain
func WithSignatureVerifier(v trust.Verifier) Option    // a2a-signed-cards-trust
func WithSigningPolicy(p SigningPolicy) Option
```

### 3.2 Resolver — `core/bundle/resolver/resolver.go`

```go
type Resolver interface {
    // Resolve produces a ResolvedGraph from a top-level config and a
    // (possibly nil) existing Lockfile. The returned graph is deterministic
    // for identical inputs.
    Resolve(ctx context.Context, cfg TopLevelConfig, lock *lockfile.Lockfile) (*ResolvedGraph, error)

    // Activate dispatches each artifact in the graph to its registered
    // ArtifactKindHandler. Activation is cancellable; partial activation
    // rolls back to the last-known-good state (FR-015).
    Activate(ctx context.Context, g *ResolvedGraph) error

    // Verify re-checks every content hash and signature in a graph or
    // lockfile without fetching new artifacts (`harness bundle verify`,
    // FR-018).
    Verify(ctx context.Context, target VerifyTarget) (*VerifyReport, error)

    // Remove tears down a bundle's activations and lockfile entries
    // (FR-017). Cache contents survive (FR-011).
    Remove(ctx context.Context, ref BundleRef) error
}

type ResolvedGraph struct {
    SnapshotID       string                  // ULID
    Bundles          []ResolvedBundle        // resolution order
    ActivationOrder  []ArtifactRef           // deterministic
    ContentHash      string                  // SHA-256 of canonical serialization
    ResolutionMeta   ResolutionMeta          // timing, channels, cache hit/miss per artifact
}
```

### 3.3 Manifest — `core/bundle/manifest/`

```go
type Manifest struct {
    SchemaVersion int                  `yaml:"schema_version"`
    Name          string               `yaml:"name"`
    Version       string               `yaml:"version"` // semver
    License       string               `yaml:"license"` // SPDX id
    Dependencies  []BundleReference    `yaml:"dependencies,omitempty"`
    Artifacts     []ArtifactDescriptor `yaml:"artifacts"`
    Signatures    []SignatureRef       `yaml:"signatures,omitempty"`
    Metadata      map[string]string    `yaml:"metadata,omitempty"`
}

func Parse(data []byte) (*Manifest, error)        // YAML 1.2 + JSON Schema validate
func (m *Manifest) ContentHash() string           // SHA-256 of canonical serialization
func (m *Manifest) Validate(opts ValidateOpts) error
```

### 3.4 Channel — `core/bundle/channels/channel.go`

```go
type Channel interface {
    Kind() string                                 // "git" | "oci" | "http_mirror" | "local_path"
    Reachable(ctx context.Context) error          // pre-flight (FR-012)
    Fetch(ctx context.Context, ref ArtifactCoord, sink io.Writer) (FetchResult, error)
    LookupSignatures(ctx context.Context, ref ArtifactCoord) ([]SignatureRef, error)
}

type Registry interface {
    Register(kind string, factory Factory) error
    Open(spec ChannelSpec, creds secrets.Resolver) (Channel, error)
}
```

`channels.Registry` is the only seam other packages cross to add a new channel kind (NFR-007). Concrete registries live in their own subpackages (`channels/oci/`, `channels/git/`, …).

### 3.5 ArtifactKindHandler — `core/bundle/kinds/kind.go`

```go
type ArtifactKindHandler interface {
    Kind() string
    ParamSchema() []byte                                  // JSON Schema bytes
    Parse(ctx context.Context, src ArtifactSource) (Parsed, error)
    Validate(ctx context.Context, p Parsed) error
    Activate(ctx context.Context, p Parsed, env Environment) (Activation, error)
    Deactivate(ctx context.Context, a Activation) error   // for FR-017 removal
}

type Registry interface {
    Register(h ArtifactKindHandler) error
    Lookup(kind string) (ArtifactKindHandler, bool)
    List() []string
}
```

The handler contract is the *only* extension surface for new artifact kinds. Adding a kind requires zero changes outside its own package (SC-005).

### 3.6 Errors

Typed sentinels for every error class the spec calls out:

```go
var (
    ErrSchemaUnsupported       = errors.New("manifest schema_version unsupported")
    ErrManifestInvalid         = errors.New("manifest invalid")
    ErrPathTraversal           = errors.New("artifact path escapes bundle root")  // FR-014
    ErrDuplicateArtifact       = errors.New("duplicate (kind,name) within bundle")
    ErrCyclicDependency        = errors.New("circular bundle dependency")
    ErrChannelUnreachable      = errors.New("distribution channel unreachable")
    ErrPinnedArtifactMissing   = errors.New("pinned artifact unavailable at any channel")
    ErrIntegrityMismatch       = errors.New("content hash does not match lockfile")  // FR-006
    ErrSignatureInvalid        = errors.New("signature verification failed")          // FR-007
    ErrSignatureRequired       = errors.New("operator policy requires signature; bundle is unsigned")
    ErrCancelled               = errors.New("resolution cancelled")                   // FR-015
    ErrConflictUnresolved      = errors.New("bundle artifact conflict requires precedence policy")
)
```

Errors are values, not strings — operator UX (`harness bundle why`, `harness bundle status`) and SOC 2 audit replay both depend on stable typed errors.

---

## 4. Internal Layering

Resolution is a strict pipeline. Each phase has a single responsibility and a single seam to the next.

### 4.1 Pre-flight validator (FR-012)

- Parse top-level config; resolve channel specs; check `Channel.Reachable` for each.
- Parse incoming `kaneaz.lock` (if present); validate its `schema_version` is ≤ harness max; validate canonical sort.
- Resolve any credential refs through the `secrets.Resolver` injected from `secrets-keychain` (D9 + secrets-keychain spec FR-001).
- Fail closed with an actionable error if any channel is unreachable *and* its artifacts are not in cache (otherwise warn + continue from cache, per spec edge case).

### 4.2 Graph builder (FR-005, FR-008)

- Walk top-level config in declared order — top-level config encodes layered composition: `team` → `personal` (FR-008).
- For each `BundleReference`, resolve to a concrete `(name, version)` using the lockfile (if pinned) or the manifest's semver range.
- Detect cycles during traversal; emit `ErrCyclicDependency` with the full cycle path.
- Detect duplicate artifact names within a single bundle (`ErrDuplicateArtifact`).
- Produce a `ResolutionPlan`: deterministic activation order = stable topological sort with byte-wise tie-breaker on `(name, version, channel_url)`.

### 4.3 Fetch pipeline (FR-003, FR-006, FR-007)

For each artifact in the plan:

1. **Cache lookup** — `cache.CAS.Has(content_hash)`. If present, emit `cache_hit`, skip to verify.
2. **Channel dispatch** — call `Channel.Fetch(ctx, coord)` for the artifact's source channel. Emit `cache_miss`, `artifact_fetched`.
3. **Hash verify** — recompute SHA-256 on fetched bytes, compare to lockfile pin. On mismatch, emit `artifact_rejected` and return `ErrIntegrityMismatch`. Never write to cache before verification (NFR-003, US3).
4. **Signature verify** (if present or required) — call `integrity/signature.go`, which calls into `a2a-signed-cards-trust`'s public `trust.Verifier.Verify(ctx, payload, sig, anchors)` API. Do NOT reimplement signature math here. Emit `bundle_signature_verified` or `bundle_signature_failed`.
5. **CAS write** — atomic write under `<data_dir>/cache/sha256/<aa>/<bb>/<digest>` (see §5.3). Write succeeds only after verification.

The pipeline is fan-out-with-bounded-parallelism (configurable, default = `runtime.NumCPU()`); each artifact is independent; failure of one does not poison others' results until consolidation.

### 4.4 Activation phase (FR-002)

- For each artifact in `ActivationOrder`, resolve its kind via `kinds.Registry.Lookup`.
- Call `handler.Parse → Validate → Activate`. Activation is a *transactional commitment*: the resolver maintains an `ActivationJournal` so that `Cancel` rolls back via `handler.Deactivate(...)` in reverse order.
- Emit `artifact_activated` after each successful activation.

### 4.5 Conflict detector (FR-009)

- Two activations of the same `(kind, name)` across bundles is a conflict.
- Default policy: later layer in the top-level config wins (personal > team), the loser is recorded in the resolution event, and the resolver does not silently merge.
- Operator may override per-conflict with `kaneaz.yaml`'s `overrides:` array (the existing stub `Manifest.Overrides` field is the seed for this).
- An unresolved conflict (precedence ambiguous) yields `ErrConflictUnresolved` and aborts before activation.

### 4.6 Cache evictor

- LRU + TTL, both configurable; default TTL is "indefinite" (never auto-evict) because a content-hash CAS never has stale data — it has unused data.
- Manual eviction via `harness bundle gc`.
- Eviction is recorded in the event log (`cache_evicted`).

### 4.7 Cancellation safety (FR-015)

- Every long-running phase respects `ctx.Done()`.
- Mid-fetch: in-flight downloads are aborted and partial files in CAS staging are deleted (CAS staging is `<data_dir>/cache/staging/<random>` — never under the canonical `sha256/...` path).
- Mid-activation: the `ActivationJournal` rolls back via `handler.Deactivate` for each completed activation in reverse.
- After cancel, the harness is in the *previous* `ResolvedGraph` state, and the lockfile is untouched.

---

## 5. Data Model — Concrete Bindings

### 5.1 Manifest schema (`kaneaz.yaml`)

YAML 1.2; canonical example:

```yaml
schema_version: 1
name: team-baseline
version: 0.4.2
license: Apache-2.0
metadata:
  homepage: https://example.com/team-baseline
  authors: ["Team Platform <platform@example.com>"]
dependencies:
  - name: org-providers
    version: ">=1.2.0,<2.0.0"
    source: { kind: oci, ref: ghcr.io/example/org-providers }
artifacts:
  - name: claude-anthropic
    kind: provider_profile           # registered kind id
    path: artifacts/providers/claude-anthropic.yaml
    content_hash: sha256:abc123...
  - name: github-mcp
    kind: mcp_server
    path: artifacts/mcp/github.yaml
    content_hash: sha256:def456...
signatures:
  - kind: sigstore_referrer
    locator: ghcr.io/example/team-baseline@sha256:...
```

JSON Schema lives in `core/bundle/manifest/schema/kaneaz.yaml.schema.json`, embedded via `go:embed`. Schema versions are integers; harness-supported range is `[1, current]`. Older harness + newer manifest → `ErrSchemaUnsupported` (FR-013).

### 5.2 Lockfile schema (`kaneaz.lock`)

TOML, Cargo-flavored, project root (D6, Open Question 2 resolution):

```toml
schema_version = 1

[[bundle]]
name           = "team-baseline"
version        = "0.4.2"
source         = "oci://ghcr.io/example/team-baseline"
content_hash   = "sha256:abc123..."
signature_ref  = "sigstore:ghcr.io/example/team-baseline@sha256:..."
dependencies   = [
  { name = "org-providers", version = "1.3.1", content_hash = "sha256:def456..." },
]

[[bundle.artifact]]
name           = "claude-anthropic"
kind           = "provider_profile"
content_hash   = "sha256:..."

[universal]
# uv-style universal markers — for future cross-platform variations.
```

Determinism rules (NFR-002, D6, data-model.md validation):

- Sort `[[bundle]]` entries byte-wise on `(name, version, source)`. No locale.
- Sort `[[bundle.artifact]]` byte-wise on `(name, kind)`.
- Canonical TOML serializer — fixed key order, fixed indentation, trailing newline mandatory.
- Top-level `[universal]` table reserved for FR-008 cross-platform variants (uv.lock-style universal model).

### 5.3 CAS layout

Rooted at `<project_data_dir>/cache/`:

```
<data_dir>/cache/
├── sha256/
│   ├── ab/
│   │   └── cd/
│   │       └── abcd...full-digest    # canonical content
├── staging/
│   └── <random>                      # in-flight downloads (deleted on cancel)
└── manifests/
    └── sha256/
        └── ...                       # parsed-and-validated manifest cache
```

`<project_data_dir>` comes from `storage-foundations` (its FR-001 owns the data-directory layout). The bundle layer requests a subpath under `<data_dir>/cache/` and treats it as opaque storage; it does NOT create a SQLite table for resolver state — the lockfile + CAS together are the source of truth (charter local-first; spec data-model entity "Lockfile" is authoritative).

If `storage-foundations` later wants resolver state queryable (e.g., for `harness bundle status` joins against session data), an optional `resolved_snapshots` table can be added under storage-foundations' migration framework — out of scope for this mission.

### 5.4 Event kinds — `core/bundle/events/events.go`

Per the event-log spec (FR-001), these kinds are typed and carry stable payload shapes:

| Kind | When | Payload |
|---|---|---|
| `bundle_resolved` | After Resolve completes | `snapshot_id`, `content_hash`, `bundle_count`, `duration_ms` |
| `artifact_fetched` | After a successful channel fetch | `bundle_ref`, `artifact_name`, `channel_kind`, `bytes`, `duration_ms` |
| `artifact_verified` | After SHA-256 match | `bundle_ref`, `artifact_name`, `content_hash` |
| `artifact_activated` | After kind handler activation | `bundle_ref`, `artifact_name`, `kind` |
| `artifact_rejected` | Hash mismatch, signature fail, schema fail | `bundle_ref`, `artifact_name`, `reason`, `error_code` |
| `bundle_signature_verified` | Sig OK | `bundle_ref`, `signature_kind`, `key_id`, `anchor_id` |
| `bundle_signature_failed` | Sig bad / required-but-missing | `bundle_ref`, `reason` |
| `lockfile_updated` | New lockfile written | `prior_hash`, `new_hash`, `diff_summary` |
| `cache_hit` | CAS lookup hit | `content_hash`, `bytes` |
| `cache_miss` | CAS lookup miss | `content_hash` |
| `channel_unreachable` | Pre-flight or fetch | `channel_kind`, `endpoint`, `reason` |

All events flow through `events.Emitter` injected at `bundle.New(...)`; concrete emitter is `event-log`'s public API. The bundle layer never writes to the event log directly.

---

## 6. Integration Points

### 6.1 `storage-foundations`

- The bundle layer requests a data-directory path from storage-foundations and uses CAS-on-disk under it; no SQLite tables are claimed by `core/bundle/`.
- Encryption-at-rest of CAS contents is *not* required for v1 — artifact contents are content-addressable and signed; their integrity is the security property, not their secrecy. Operator-marked-sensitive artifacts (e.g., a context pack containing PII) are out of scope of this layer; secrecy is a `shared-context-distribution` concern.

### 6.2 `secrets-keychain`

- Channel auth is via `secrets.Resolver` (the indirect credential reference, secrets-keychain FR-001).
- A bundle config for an OCI channel that requires login looks like:
  ```yaml
  source:
    kind: oci
    ref: ghcr.io/private/example
    auth: { keychain: "ghcr-token" }
  ```
  The bundle layer never sees the resolved value; it passes the reference to the channel adapter, which calls `secrets.Resolver.Resolve(ref)` only at fetch time.
- Operator UX: `kaneaz registry login` (D9) is a thin wrapper that writes to docker config (for OCI) or the OS keychain — operator workflow, not bundle-layer code.
- Pre-flight (FR-012, FR-019) calls `secrets.Resolver.Lookup(ref)` to confirm presence without resolving the value.

### 6.3 `a2a-signed-cards-trust`

- Signature verification is **delegated**, never reimplemented (D4). The bundle layer imports a `trust.Verifier` from the public API of `a2a-signed-cards-trust` and calls:
  ```go
  result, err := verifier.Verify(ctx, trust.VerifyRequest{
      Payload:   manifestCanonicalBytes,
      Signature: sigRef,
      Anchors:   operatorTrustSet,
  })
  ```
- The bundle layer is responsible for *offering* the bytes and the SignatureRef; `a2a-signed-cards-trust` owns algorithm policy, anchor matching, key rotation, and revocation.
- For Sigstore: the SignatureRef is a `sigstore_referrer` whose locator is an OCI digest; the OCI channel resolves it via the Referrers API (D3) and hands the artifact bytes to the verifier.
- For offline: the SignatureRef is `ed25519_detached`; the bundle layer resolves the detached `.sig` file from the same channel as the bundle and hands it over.

### 6.4 `event-log`

- The bundle layer never appends events directly. It calls `events.Emitter.Emit(kind, payload)` and the event-log layer owns redaction (US3 of event-log spec), append-only enforcement (US2), and persistence to the storage-foundations SQLite database.
- Bundle event payloads carry no credentials — channel auth is by reference, never resolved-value, by construction (§6.2). The redaction policy is therefore a defense-in-depth check, not a primary safeguard.

### 6.5 `sigstore-go` integration shape

- Lives behind the `trust.Verifier` of `a2a-signed-cards-trust` — the bundle layer does not import `sigstore/sigstore-go` directly.
- Behavioral notes (for the trust-mission planner): keyless OIDC flow used for CI-built bundles; `Rekor` transparency log is consulted; `Fulcio` issues the ephemeral cert. Air-gapped fallback is detached Ed25519 (D4).
- This separation keeps `core/bundle/` free of sigstore's transitive deps if an operator opts into Ed25519-only.

---

## 7. Phasing

### v1.0 — foundation (this mission)

In scope:

- YAML 1.2 manifest with JSON Schema validation (D1, FR-001).
- All four day-one channels: git, oci, http_mirror, local_path (FR-003, D2, D3).
- TOML lockfile, Cargo-flavored, deterministic, schema_version=1 (D6, FR-004).
- SHA-256 content-hash integrity (FR-006, D5).
- Layered composition, conflict detection with declared precedence (FR-008, FR-009).
- Local cache (FR-011), pre-flight validation (FR-012), schema versioning (FR-013), path-traversal protection (FR-014), cancellation safety (FR-015), progress reporting (FR-016), removal (FR-017).
- Diagnostic surface: `harness bundle status`, `harness bundle why <artifact>`, `harness bundle verify` (FR-018).
- Event emission (FR-010).
- *Optional* signature verification — present if `signatures:` is in the manifest, required if operator policy says so (FR-007).
- Resolver perf target: <500ms p95 warm cache, 3 bundles / 15 artifacts (NFR-001, SC-004).

### v1.x — security maturation

- Sigstore keyless flow (`sigstore-go`, behind the trust mission).
- Ed25519 detached offline path (D4, the air-gapped story).
- in-toto attestations as Referrers — SBOM and SLSA Provenance attached to bundles (D3, D7).
- `kaneaz lock --resolve-conflicts` subcommand (D8, lockfile/merge.go).
- `kaneaz registry login` operator UX (D9).

### v2 — enterprise + ecosystem

- Private enterprise registry adapters (Harbor, JFrog, Quay) — already work via OCI but may need quirk-specific paths.
- Optional BLAKE3 alongside SHA-256 (`additionalHashes` in manifest, D5).
- SLSA Build Track L3 via reusable hosted builders (D7).
- Universal lockfile cross-platform variants (`[universal]` table populated for true OS/arch divergence, uv.lock-style — currently reserved).

### Non-goals across all versions

- A bundle marketplace, bundle UI, or "bundle hub" service.
- Telemetry on bundle usage outside the local event log.
- Mutable bundles (every change is a new version + new content hash; charter append-only).

---

## 8. Risk Register

| ID | Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|---|
| R1 | `oras-go v2` primary-source verification gap — research had WebFetch denied (research.md caveat). Some library claims may not match reality in 2026. | Med | Med | 30-min primary-source verification pass during WP-001 (research.md Next Action 8). Pin a specific `oras-go` minor version in `go.mod`. |
| R2 | Go YAML 1.2 parser choice — most Go YAML libs default to 1.1 (Norway problem). Picking the wrong one bakes the Norway bug into manifests. | High | High | **Plan-phase decision required**: pin one of `goccy/go-yaml` (1.2) or `goyaml.v3` (1.2-ish with caveats); document in WP-001. Mandate quoted strings for short identifiers in the JSON Schema as belt-and-suspenders. |
| R3 | Lockfile merge conflicts in real repos — pnpm taught everyone this is inevitable. Without `lock --resolve-conflicts`, operators will hand-edit lockfiles and break determinism. | High | Med | Ship `lock --resolve-conflicts` in v1.x (D8). For v1.0, emit a clear error pointing operators at the deferred command. |
| R4 | Supply-chain attack surface — a compromised channel substituting a malicious artifact at the same content hash is mathematically impossible (SHA-256), but a compromised channel substituting a malicious *new version* over a stale lockfile pin is a real attack class. | Med | High | Lockfile pin is authoritative (US2 acceptance scenario 3); resolver MUST refuse to upgrade a pinned bundle without explicit operator action (`harness bundle upgrade <name>`). Sigstore signatures + transparency log close the residual gap in v1.x. |
| R5 | Channel auth quirks across registries — ECR token expiry, GAR short-lived tokens, GHCR PAT scopes (research.md). `oras-go credentials` claims to handle all of this; reality may diverge. | Med | Med | Integration tests for each major registry kind in CI; a test fixture per registry. Document quirks in `core/bundle/channels/oci/auth.go`. |
| R6 | YAML 1.1 vs 1.2 manifest interop — an author writes a manifest expecting 1.2 semantics, an older harness parses it as 1.1, the Norway problem flips a boolean. | Med | High | `schema_version` is mandatory and parsed as an integer first — older harnesses bail with `ErrSchemaUnsupported` *before* parsing the rest of the YAML. Belt-and-suspenders: JSON Schema requires explicit-string quoting for short identifiers. |
| R7 | Performance regression — warm-cache resolution creeps over 500ms as the bundle ecosystem grows. | Med | Med | Per-resolve `ResolutionMeta` records timing per phase; CI benchmark gate on a representative graph (3 bundles / 15 artifacts) per NFR-001. |
| R8 | Cancellation incomplete — a panic in a kind handler's `Activate` skips its corresponding `Deactivate`, leaving partial state. | Low | High | `ActivationJournal` records *every* commitment with a recovery hook; rollback uses `defer` + recover patterns; integration test exercises panics in handlers. |
| R9 | CAS path collisions or filesystem case-sensitivity issues across Windows / macOS / Linux. | Low | Med | SHA-256 hex digests are case-stable; CAS layout is two hex chars per directory level (`sha256/aa/bb/`), avoiding deep nesting. Filesystem-level filename collisions are mathematically impossible. |
| R10 | DIRECTIVE_001 erosion — a future PR adds an `import "core/bundle/channels/oci"` from `core/bundle/resolver/` to "fix one thing." | Med | High | Architectural-integrity check (charter governance) flags any cross-package import outside the registry seam; ADR required for any addition (DIRECTIVE_003). |

---

## 9. Open Questions for the User

The spec flags three (lines 184–188). Research resolved them; this plan proposes confirmation.

1. **Manifest format — YAML or TOML?** *Resolved by D1 (final): YAML 1.2.* Confirm.
2. **Lockfile location — root or `.harness/lock`?** *Recommend project root (`kaneaz.lock`)*, mirroring `Cargo.lock` / `uv.lock` / `package-lock.json` for visibility (D6 + Open Question 2 of research). Confirm.
3. **Dependency syntax — semver ranges or exact pins?** *Recommend ranges in `kaneaz.yaml`, exact pins in `kaneaz.lock`* — the standard pattern across Cargo, npm, uv. Authoring stays ergonomic; reproducibility is enforced. Confirm.

Additional plan-phase questions raised by this document (NOT in the original spec):

4. **Go YAML 1.2 library** — `goccy/go-yaml` vs `goyaml.v3` vs another. (R2.) *Default if unresolved*: `goccy/go-yaml` (explicit YAML 1.2 support, active maintenance). Plan-phase decision; locked in WP-001.
5. **CAS data directory** — `<XDG_DATA_HOME>/kaneaz/cache` (Linux) / `~/Library/Application Support/kaneaz/cache` (macOS) / `%LOCALAPPDATA%\kaneaz\cache` (Windows). *Defaults consistent with `storage-foundations`*; confirm with that mission's data-dir contract.
6. **Default conflict precedence policy** — *Last-layer-wins (personal > team)* matches FR-008's spec wording. Confirm.
7. **Lockfile sort tie-breaker on identical `(name, version, source)`** — should not occur (those three identify a bundle uniquely), but if it does (e.g., signature-only difference), tie-break on `content_hash`. Confirm.

---

*Plan ends. Proceed to `tasks` phase to decompose into work packages.*
