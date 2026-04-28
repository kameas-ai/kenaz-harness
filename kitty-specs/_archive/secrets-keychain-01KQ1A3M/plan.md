# Implementation Plan — Secrets and OS Keychain Abstraction

**Mission**: `secrets-keychain-01KQ1A3M`
**Spec**: `kitty-specs/secrets-keychain-01KQ1A3M/spec.md` (17 FRs, 7 NFRs, 6 constraints)
**Research**: `kitty-specs/secrets-keychain-01KQ1A3M/research.md` (D1–D8 final)
**Data model**: `kitty-specs/secrets-keychain-01KQ1A3M/data-model.md`
**Status**: Plan draft (HOW). Implementation deferred to work-package phase.

---

## 1. Overview

This mission delivers the harness's secret-resolution layer: the single shared
surface every other consumer (`llm-connector`, `a2a-signed-cards-trust`,
`shared-context-distribution`, `bundle-format-resolver`, `storage-foundations`,
`event-log`) calls when it needs a credential value at runtime. It implements
**indirect credential references**, a **pluggable backend contract**, an
**OS-keychain default** with environment / file / AWS-profile / AWS-KMS /
YubiKey-PIV alternatives, **pre-flight validation**, a **TTL cache with
rotation invalidation**, **resolution events**, and **explicit zeroize on
release**.

**In scope**: reference parsing, backend dispatch, resolution caching,
in-memory `Secret` lifecycle, pre-flight checks, health probes, error
taxonomy, lint enforcement of `[]byte`-typed secrets.

**Out of scope** (delegated upstream): credential creation, rotation,
revocation in the source-of-truth system (operator manages those in
Keychain.app / KMS console / `secrets-tool` etc.). The harness consumes
whatever value is current at resolve time. UI for managing references lives
in the Wails frontend mission.

---

## 2. Architectural placement

DIRECTIVE_001 (separation of concerns): all secret logic lives under
`core/secrets/`. No other `core/` package may import a backend SDK
(`go-keyring`, `aws-sdk-go-v2`, `piv-go`, etc.) — they import only
`core/secrets`.

Subpackage layout:

```
core/secrets/
├── secrets.go               # Resolver, package-level New(), wiring
├── ref/
│   ├── reference.go         # CredentialReference type + parsers (env/keychain/file/aws_profile/aws_kms/yubikey_piv/pkcs11)
│   └── reference_test.go
├── registry/
│   ├── registry.go          # Backend registry, dispatch table
│   └── registry_test.go
├── cache/
│   ├── cache.go             # TTL cache with rotation invalidation hooks
│   └── cache_test.go
├── secret/
│   ├── secret.go            # Secret type (interface) + StdlibSecret impl ([]byte + Use+Destroy + KeepAlive zeroize)
│   ├── memguard_secret.go   # Build-tag-gated MemguardSecret (opt-in)
│   └── secret_test.go
├── preflight/
│   ├── preflight.go         # Per-reference resolvability check + structured PreFlightResult
│   └── preflight_test.go
├── events/
│   └── events.go            # ResolutionEvent shape; emits via event-log.Logger
├── errors/
│   └── errors.go            # Typed error taxonomy (FR-014)
├── lint/
│   └── lint.go              # go/analysis Analyzer that flags `string`-typed secret fields/return values
└── backends/
    ├── env/                 # os.Getenv backend
    ├── file/                # mounted-file backend (+ optional argon2id KEK envelope)
    ├── oskeychain/          # zalando/go-keyring wrapper
    ├── xdgportal/           # org.freedesktop.portal.Secret (Linux sandboxed; v1.x)
    ├── awsprofile/          # AWS credential chain
    ├── awskms/              # aws-sdk-go-v2/service/kms + AWS Encryption SDK for Go
    └── yubikey/             # go-piv/piv-go v2.6.0
```

Rules:

- Only backend subpackages import their respective SDKs.
- The top-level `core/secrets` package re-exports the public types
  (`Resolver`, `Backend`, `Secret`, `CredentialReference`,
  `ResolutionEvent`).
- No backend package imports another backend.
- The lint analyzer (`core/secrets/lint`) is wired into the project's
  `golangci-lint` configuration as a custom plugin.

---

## 3. Public API (illustrative — not final)

```go
// core/secrets/secrets.go (illustrative)
package secrets

type Resolver interface {
    // Resolve returns a Secret for the given reference. Caller MUST
    // call Secret.Use(...) or Secret.Destroy() exactly once.
    Resolve(ctx context.Context, ref CredentialReference, consumerID string) (Secret, error)

    // PreFlight validates every registered reference and returns
    // per-reference results. Used at harness startup.
    PreFlight(ctx context.Context, refs []CredentialReference) ([]PreFlightResult, error)

    // Health returns the current health probe per registered backend.
    Health(ctx context.Context) map[BackendKind]BackendHealth
}

type Backend interface {
    Kind() BackendKind
    SupportedRefKinds() []RefKind
    Resolve(ctx context.Context, ref CredentialReference) (Secret, error)
    Health(ctx context.Context) BackendHealth
}

// Secret is ALWAYS backed by []byte. NEVER string.
type Secret interface {
    // Use grants the caller a borrowed view of the bytes for the
    // duration of fn. After fn returns, the bytes MAY be zeroed.
    Use(fn func(value []byte) error) error
    // Destroy zeroes the underlying bytes and releases any platform
    // resources (memguard locked pages, etc.).
    Destroy()
    // ReferenceID is opaque and safe for logging — never the value.
    ReferenceID() string
}

type CredentialReference struct {
    Kind       RefKind
    Locator    string
    ConsumerID string // optional — declares intended consumer
}

type RefKind int
const (
    RefEnv RefKind = iota
    RefKeychain
    RefFile
    RefAWSProfile
    RefAWSKMS
    RefYubikeyPIV
    RefPKCS11 // v2
)
```

Caller pattern:

```go
secret, err := resolver.Resolve(ctx, ref, "llm-connector:anthropic")
if err != nil { return err }
defer secret.Destroy()
err = secret.Use(func(v []byte) error {
    return llmCall(ctx, v)
})
```

---

## 4. Internal layering

Resolution path (single `Resolver.Resolve` call):

1. **Reference parse** (`ref` package) — validate `kind` + `locator`
   shape; reject unknown kinds; reject empty locators.
2. **Cache check** (`cache` package) — keyed by stable hash of
   `(kind, locator)`; TTL configurable per backend (default 60 s — see
   §9 Open Questions). Cache stores `Secret` handles, NOT raw bytes;
   eviction triggers `Secret.Destroy()`.
3. **Backend dispatch** (`registry`) — look up backend by `RefKind`.
   Backends register themselves at startup via `registry.Register(b)`.
4. **Backend resolve** — backend produces a `Secret`. On error, return
   typed error from `errors` package.
5. **Event emission** (`events`) — emit `ResolutionEvent` with
   outcome, latency, cache hit flag. Never includes the value.
6. **Cache insert** — on success, store in cache with TTL.
7. **Return** — caller receives `Secret`.

**Pre-flight validator** (`preflight` package): at harness startup,
called by `core.Core.Start()`. Iterates every registered
`CredentialReference`, calls `Backend.Resolve` (and immediately
`Destroy()` the result), records result in `PreFlightResult`. Fails
startup if any required reference is unresolvable. Optional references
can degrade gracefully.

**Backend health probe** (`Backend.Health`): cheap idempotent call
(D-Bus ping for Secret Service, `kms:DescribeKey` for AWS KMS, PC/SC
reader presence for YubiKey). Surfaced via `harness secrets status`
(CLI / RPC) per FR-017.

**Cache rotation invalidation**: triggered by (a) operator command
`harness secrets invalidate <ref>`, (b) backend-driven signal (e.g.,
KMS data-key rotation event), (c) explicit `Resolver.Invalidate(ref)`
from a consumer that detected an auth-rejection. Invalidation calls
`Secret.Destroy()` before evicting. NFR-007: rotation propagates within
5× TTL p99.

**Sandbox detection** (Linux): at backend init, the `oskeychain`
backend probes for Flatpak (`/.flatpak-info`) or Snap (`SNAP`
environment) and routes through `xdgportal` instead of direct D-Bus
when detected. v1 logs a warning + falls back to direct D-Bus until
`xdgportal` ships in v1.x.

**Zeroize on Secret.Destroy** (D6, D7, FR-013):

- `StdlibSecret`: explicit `for i := range b { b[i] = 0 }` followed by
  `runtime.KeepAlive(b)` to defeat compiler elision.
- `MemguardSecret` (build tag `memguard`): wraps
  `memguard.NewBuffer(...)`; `Destroy()` calls `buf.Destroy()`.
- A hardening test (in `secret_test.go`) reads `/proc/self/maps` (or
  the macOS equivalent stub) and confirms zeroized buffers don't
  contain the original value (best-effort; documented as advisory).

**Process-arg / env scrub** (Open Question 3, default yes): on
`secrets.New()`, walk `os.Args[1:]` and (for env-backend only) record
which env-var *names* are referenced. Never log values. This is a
defensive baseline; the actual leak prevention comes from never having
plaintext in args/env in the first place.

---

## 5. Data model summary

Defined in detail in `data-model.md`. Plan-level shape:

| Type | Storage | Lifetime | Notes |
|---|---|---|---|
| `CredentialReference` | config / lockfile / RPC | static (per session) | indirect pointer; never carries value |
| `Backend` | in-memory registry | process | one per registered kind |
| `Secret` | heap (`[]byte`) | scoped to consumer call | MUST be `[]byte`; lint-enforced |
| `ResolutionEvent` | event-log (append-only) | persistent | redacted; no value |
| `PreFlightResult` | startup snapshot, optionally event-logged | per-startup | drives fail-closed startup |

**ResolutionEvent kinds** (per FR-012, aligns with `event-log` spec):

```
secret.resolution.requested
secret.resolution.cache_hit
secret.resolution.cache_miss
secret.resolution.backend_dispatched
secret.resolution.ok
secret.resolution.failed
secret.preflight.ok
secret.preflight.failed
secret.cache.invalidated
secret.rotation.detected
```

Each event payload: `consumer_id`, `reference_kind` (NOT locator),
`backend_kind`, `outcome`, `latency_ms`, `cache_hit` boolean. Locator
is redacted (hash) — even reference *names* can be sensitive in
multi-tenant audit contexts.

**Error taxonomy** (FR-014, exported sentinel errors in
`core/secrets/errors`):

```go
var (
    ErrBackendUnavailable = errors.New("secrets: backend unavailable")
    ErrReferenceNotFound  = errors.New("secrets: reference not found")
    ErrReferenceEmpty     = errors.New("secrets: reference value empty")
    ErrPermissionDenied   = errors.New("secrets: permission denied")
    ErrFormatInvalid      = errors.New("secrets: reference format invalid")
    ErrRotatedMidUse      = errors.New("secrets: credential rotated during use")
    ErrInlinePlaintext    = errors.New("secrets: inline plaintext credential refused")
)
```

Errors wrap with `fmt.Errorf("%w: ref=%s", ErrReferenceNotFound, refID)`
where the *ref id* is a redaction-safe hash, not the locator.

---

## 6. Integration points

| Consumer | Integration | Responsibility split |
|---|---|---|
| `llm-connector` | Calls `Resolver.Resolve(ref)` per provider auth ref | Connector owns provider selection; secrets layer owns resolution |
| `a2a-signed-cards-trust` | Resolves signing-key reference per signing operation | Signing backend selection (file/keychain/HSM) abstracted via this layer's backend kind |
| `shared-context-distribution` | Resolves channel/pack auth refs | Per-channel scoping uses `consumer_id` |
| `bundle-format-resolver` | Resolves channel auth refs (mirror of shared-context) | Same path |
| `storage-foundations` | Resolves DB encryption-key reference once at boot | Long-lived `Secret` (lifetime = DB connection); explicit `Destroy()` on shutdown |
| `event-log` | Resolves HMAC redaction-salt reference once at boot | Same long-lived pattern as DB key |

**Event-log emission shape**: this layer calls
`event.Logger.Append(ctx, kind, payload)` where `kind` is one of the
`secret.*` kinds above. Event-log mission's redaction pipeline
double-checks no bytes-shaped substrings leak (defense in depth).

**Cycle break**: `event-log` depends on `secrets-keychain` for the
HMAC salt reference; `secrets-keychain` depends on `event-log` for
emission. Resolved by:

- `event-log` boots in a "no-redaction" mode just long enough to
  resolve its own salt reference;
- `secrets-keychain` accepts an optional `event.Logger` (nil tolerated
  during pre-boot) and buffers events until the logger is wired.

---

## 7. Phasing

**v1.0 (this mission's scope)**:

- Backends: `env`, `file` (plain + argon2id KEK envelope opt-in),
  `oskeychain` (zalando/go-keyring), `awsprofile`, `awskms`
  (aws-sdk-go-v2/service/kms + AWS Encryption SDK for Go),
  `yubikey` (go-piv/piv-go v2.6.0).
- Memory hygiene: `StdlibSecret` baseline (zero loop +
  `runtime.KeepAlive`).
- Pre-flight: full validation + fail-closed startup.
- Cache: TTL with default 60 s; rotation invalidation API.
- Resolution events: all kinds emitted.
- Lint analyzer: `string`-typed secret detection; CI-blocking.
- CLI: `harness secrets status` (FR-017), `harness secrets invalidate <ref>`.

**v1.x (follow-up, same release line)**:

- `xdgportal` backend for Flatpak / Snap Linux distribution
  (depends on whether v1 ships those targets — Open Q1 in research).
- `MemguardSecret` opt-in (build tag `memguard`).
- Linux file-backend headless mode hardening (argon2id KEK pulled
  from prompt or hardware token).

**v2 (future mission, separate slug)**:

- Azure Key Vault backend (`Azure/azure-sdk-for-go`).
- GCP KMS backend.
- PKCS#11 / general HSM backend (`miekg/pkcs11` +
  `ThalesGroup/crypto11`).
- `runtime/secret` evaluation when Go 1.27/1.28 stabilizes the
  experiment.

Each phase delivers a working set of FRs; v1.0 covers all P1 + P2
acceptance scenarios.

---

## 8. Risk register

| # | Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|---|
| R1 | macOS Keychain prompts user on first access in headless mode | High | Med | First-run UX hint; document `--secret-backend=env` for CI; pre-flight surfaces prompt-required state explicitly |
| R2 | Windows `VirtualLock` only weakly prevents swap; SOC 2 reviewers ask about it | Med | Med | Document the limitation honestly; recommend BitLocker as the disk-layer compensating control; offer memguard build tag for hardened deployments |
| R3 | Headless Linux with no Secret Service daemon | High | High | Refuse silent fallback (D2). Require explicit `--secret-backend=file:<path>` with argon2id KEK. Pre-flight emits clear error |
| R4 | AWS KMS network unavailability mid-session | Med | High | Cache resolved values at TTL; on cache miss, retry with backoff per configurable budget; fail closed with `ErrBackendUnavailable` (C-006) |
| R5 | YubiKey PC/SC platform variance (Linux libpcsclite version skew) | Med | Med | Pin go-piv/piv-go v2.6.0; document required pcsclite version in deployment guide; YubiKey backend is opt-in, never default |
| R6 | Lint enforcement of `[]byte`-typed secrets misses cases (interfaces, generics) | Med | High | Two layers: (a) `go/analysis` analyzer in CI; (b) code review checklist item for any `core/secrets` change; (c) periodic security-review skill scan |
| R7 | Cache TTL too short → user keychain prompt fatigue on macOS | Med | Low | Default 60 s; per-backend override; ADR captures the trade-off |
| R8 | Cache TTL too long → rotation propagation delayed (NFR-007) | Med | Med | Same default; explicit `Resolver.Invalidate` API for consumers that detect auth failure |
| R9 | Event-log emission cycle (this layer needs event-log; event-log needs salt from this layer) | High | Med | Boot-order resolution: event-log starts in no-redaction mode for its salt fetch; secrets-keychain buffers events until logger is wired |
| R10 | Inline-plaintext refusal (FR-015) false-positives on legitimate config | Low | Low | Detection regex tuned conservatively; operator-overridable per field with explicit ADR-grade justification |

---

## 9. Open questions

These mirror spec.md's three `[NEEDS CLARIFICATION]` items:

1. **Cache TTL default** — spec defaults to 60 s; this plan adopts 60 s.
   Per-backend override available. Captured in ADR at implementation
   time. **Proposal: 60 s. Confirm at acceptance.**

2. **Linux fallback chain** — spec proposes Secret Service → kernel
   keyring → fail. Research D2 supersedes: **Secret Service → XDG
   portal Secret (sandboxed) → explicit-opt-in file backend with
   argon2id KEK. NO kernel keyctl as primary, NO silent file
   fallback.** This plan adopts D2.

3. **Process-arg / environment scrub at startup** — spec defaults to
   yes. **Proposal: yes; the harness records env-var *names* it
   references via the env backend but never logs values; `os.Args[1:]`
   is scanned at startup and any credential-shaped substring triggers
   a startup error per FR-015.** Confirm at acceptance.

---

## 10. Test strategy (high-level)

Per charter testing standards (≥ 80 % line coverage on `core/`
packages, black-box integration tests at boundaries, no event-log
mocking):

- **Unit**: `ref` parsing, error taxonomy, cache TTL, lint analyzer.
  Table-driven.
- **Integration (black-box per DIRECTIVE_036)**:
  - `oskeychain` backend on each platform (macOS / Linux Secret
    Service / Windows Credential Manager) via real OS calls in CI
    runners; mark as platform-gated.
  - `awskms` backend against LocalStack KMS in CI.
  - `yubikey` backend with a software-emulated PIV applet
    (go-piv/piv-go ships test fixtures).
  - Pre-flight + event emission against a real on-disk event log
    under `t.TempDir()`.
- **Security (in same commit per charter)**:
  - Audit suite scans event log, lockfile, RPC payload fixtures,
    `/tmp` for plaintext after a session — must be zero matches
    (NFR-003).
  - Lint analyzer fixtures: positive (string-typed secret) and
    negative ([]byte-typed) cases.
  - Heap-scan test (best-effort, documented advisory) confirms
    `Secret.Destroy()` zeroes the buffer.
- **Performance**:
  - Warm-cache p99 < 1 ms (NFR-001) — microbenchmark.
  - Cold OS-keychain p95 < 50 ms (NFR-002) — platform-gated bench.

---

## 11. Lint plan (Secret-as-`[]byte` enforcement)

Per D7 + FR-013 + data-model "Secrets MUST be `[]byte`. Lint enforces
it":

- Custom `go/analysis` analyzer at `core/secrets/lint/`:
  - Flags any field declared in a struct under `core/secrets/...`
    whose name matches `(?i)(secret|credential|key|token|password|api_?key)`
    AND whose type is `string`.
  - Flags any function in `core/secrets/...` that returns `string`
    when its name suggests credential-bearing semantics.
  - Flags any conversion `string(secretBytes)` outside of designated
    test files.
- Wired into `golangci-lint` via the custom plugin facility.
- CI gate: blocking on PR.
- Escape hatch: `//nolint:secret-bytes` with required justification
  comment, only valid in test files.

---

## 12. Acceptance mapping (FR / NFR / SC → plan section)

| Spec ID | Title | Plan section |
|---|---|---|
| FR-001 | Indirect-reference syntax | §3 (CredentialReference), §4 (parse) |
| FR-002 | Backend abstraction | §3 (Backend), §2 (subpackages) |
| FR-003 | OS keychain backend (default) | §2 (oskeychain), §7 v1.0 |
| FR-004 | Env backend | §2 (env), §7 v1.0 |
| FR-005 | File backend | §2 (file), §7 v1.0 |
| FR-006 | AWS profile backend | §2 (awsprofile), §7 v1.0 |
| FR-007 | Cloud KMS backend | §2 (awskms), §7 v1.0 |
| FR-008 | HSM backend | §7 v2 (PKCS#11) |
| FR-009 | Pre-flight validation | §4 (preflight), §10 |
| FR-010 | Resolution cache + TTL | §4 (cache), §9 Q1 |
| FR-011 | Cache invalidation on rotation | §4 (rotation invalidation) |
| FR-012 | Resolution events | §5 (event kinds), §6 (event-log integration) |
| FR-013 | Zeroize after use | §4 (zeroize), §11 (lint) |
| FR-014 | Error taxonomy | §5 (errors) |
| FR-015 | Refuse inline plaintext | §9 Q3, §10 |
| FR-016 | Per-reference scoping | §3 (consumer_id), §6 |
| FR-017 | Backend health probe | §4 (health), §7 (`harness secrets status`) |
| NFR-001 | Warm-cache p99 < 1 ms | §10 |
| NFR-002 | Cold OS-keychain p95 < 50 ms | §10 |
| NFR-003 | Zero plaintext leakage | §10 (audit suite), §11 (lint) |
| NFR-004 | Cross-platform parity | §2 (oskeychain), §10 |
| NFR-005 | Backend extensibility | §2 (subpackages), §3 (Backend) |
| NFR-006 | Pre-flight completeness | §4 (preflight) |
| NFR-007 | Rotation propagation within 5× TTL | §4 (invalidation), §9 Q1 |
| C-001 | Architectural integrity | §2 (DIRECTIVE_001) |
| C-002 | No plaintext in persisted state | §5 (events redacted), §11 |
| C-003 | Append-only event log | §6 (event-log integration) |
| C-004 | Charter local-first | §7 v1.0 (oskeychain default) |
| C-005 | SOC 2 readiness | §10 (events as evidence) |
| C-006 | Fail closed | §4 (no silent fallback), §8 R3 |

---

## 13. Deliverables (planning-phase only)

This plan produces no code; it informs the work-package decomposition
that follows. Expected work-package shape (decided at `tasks` phase):

1. `core/secrets/{ref,registry,secret,errors}` skeleton + types.
2. `core/secrets/cache` with TTL + invalidation.
3. `core/secrets/preflight` + event emission.
4. Backend: `env`.
5. Backend: `file` (+ argon2id KEK).
6. Backend: `oskeychain` (zalando/go-keyring; platform-gated tests).
7. Backend: `awsprofile`.
8. Backend: `awskms` (AWS Encryption SDK envelope).
9. Backend: `yubikey` (go-piv/piv-go).
10. Lint analyzer + golangci-lint wiring.
11. CLI: `harness secrets status` + `harness secrets invalidate`.
12. Audit / security tests + ADR.

---

*End of plan. ~430 lines.*
