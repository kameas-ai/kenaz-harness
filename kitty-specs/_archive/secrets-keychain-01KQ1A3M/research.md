# Research Decision Log — Secrets and OS Keychain Abstraction

## Summary

- **Feature**: `secrets-keychain-01KQ1A3M` — cross-platform OS-keychain library, Linux secret-storage ecosystem, optional cloud KMS / hardware-token backends, credential-memory hygiene.
- **Date**: 2026-04-25
- **Researchers**: alecfeeman, Claude (assisting), background research subagent
- **Open Questions** (after research):
  - Do we ship Flatpak/Snap Linux builds in v1? Affects whether we need the XDG portal shim from day one.
  - Stale `99designs/keyring` upstream — community fork `ByteNess/keyring` is reported but not directly verified.
  - Is the v1 cloud-KMS slot AWS-only, or do we ship Azure + GCP at v1?

---

## Landscape snapshot (April 2026)

### Go OS-keychain libraries

| Library | Last release | Platforms | Notes |
|---|---|---|---|
| **`zalando/go-keyring`** | v0.2.8 (2026-03-23) | macOS, Linux/BSD (Secret Service), Windows | **Recommended.** Active maintenance; simple Set/Get/Delete; pure-Go on Windows/Linux (no CGo). |
| `99designs/keyring` | v1.2.2 (2022-12-19) | macOS, Windows, Secret Service, KWallet, Pass, KeyCtl, encrypted JWT file | Originally aws-vault. **Upstream stale.** Community fork `ByteNess/keyring` reported. |
| `keybase/go-keychain` | v0.0.1 (Feb 2025) | macOS, iOS, Linux (Secret Service) | API mirrors Apple Keychain — "not necessarily idiomatic Go" per its own README. |

No new de-facto library has displaced these in 2026.

### Linux secret-storage ecosystem

- **Secret Service API** (`org.freedesktop.secrets`) — implemented by gnome-keyring, KWallet (≥ 5.97). Standard for desktop sessions.
- **Sandboxed apps** (Flatpak / Snap on Wayland) — must go through `org.freedesktop.portal.Secret` rather than D-Bus directly. Returns a per-app master secret usable to encrypt a local store. `zalando/go-keyring` does not handle the portal directly; we'd ship a `godbus/dbus` shim.
- **Headless Linux** — no clean Secret Service fallback. Recommend: refuse silent storage; require explicit `--secret-backend=file:<path>` with passphrase-derived KEK (argon2id) for ssh-only / CI installs.
- **Kernel keyctl** — appropriate as in-process intermediate cache, not as primary storage. Keys tied to session/process lifetimes.

### Cloud KMS / hardware tokens

| Option | Status (April 2026) | Verdict for v1 |
|---|---|---|
| **AWS KMS** (`aws-sdk-go-v2/service/kms`) | Most mature; SDK v1 EOL announced. AWS Encryption SDK for Go 1.23+ ships envelope encryption with caching/commitment. | **Ship.** Most enterprise pilots default to AWS. |
| Azure Key Vault (`Azure/azure-sdk-for-go`) | Production-ready; API 2026-02-01 supported. | Defer to v2. |
| Google Cloud KMS | Mature Go client. | Defer to v2. |
| **YubiKey PIV** (`go-piv/piv-go` v2.6.0, 2026-04-15) | **Pure Go** (no CGo) over OS PC/SC. macOS / Windows built-in PC/SC; Linux libpcsclite. | **Ship as the hardware-token slot.** |
| PKCS#11 (`miekg/pkcs11` Jan 2026) | Sustainably maintained. CGo + vendor module quirks. | Defer past v1. |

### Memory hygiene

- **Stdlib baseline** (sufficient for v1): explicit zero loop + `runtime.KeepAlive` to prevent compiler eliding. `crypto/subtle.ConstantTimeCompare` for equality. **Avoid `string`-typed secrets** (immutable, can't be zeroed).
- **`runtime/secret`** (Go 1.26 experimental) — Linux amd64/arm64 only behind `GOEXPERIMENT=runtimesecret`. Not v1-ready.
- **`awnumar/memguard`** (v0.23.0, Aug 2025) — mlock'd LockedBuffers, guard pages + canaries, XSalsa20-Poly1305 encryption-at-rest in memory. **Opt-in** for cold-boot/coredump-resistance.
- Cross-platform mlock: don't roll own; rely on memguard. Windows `VirtualLock` only weakly prevents swap.

---

## Decisions & Rationale

| Decision | Rationale | Evidence | Status |
|----------|-----------|----------|--------|
| **D1**: Adopt **`zalando/go-keyring`** as the primary cross-platform keychain library. | Active, simple, pure-Go on Windows/Linux. `99designs/keyring` upstream stale. | E1, E2, E3 | final |
| **D2**: Linux backend chain — Secret Service via D-Bus → XDG portal Secret for sandboxed builds → explicit-opt-in encrypted file backend with argon2id KEK for headless. **Refuse silent fallback.** | Three-tier chain handles desktop, Flatpak/Snap, ssh-only CI. Refusing silent fallback prevents quiet plain-disk storage. | E4, E5, E6 | final |
| **D3**: **Do not use kernel keyctl as primary storage.** In-process cache only. | Lifetime semantics wrong for desktop apps. | E7, E8 | final |
| **D4**: v1 cloud-KMS slot ships **AWS KMS only** via `aws-sdk-go-v2/service/kms` + AWS Encryption SDK for Go. Azure/GCP land in v2. | Most enterprise pilots default to AWS; tripling surface area for v1 has diminishing return. | E9, E10 | final |
| **D5**: Hardware-token slot ships **`go-piv/piv-go` v2.6.0**. PKCS#11 deferred. | Pure-Go is a meaningful posture and build-toolchain win. PIV is universal. | E11, E12 | final |
| **D6**: Memory hygiene baseline — explicit zeroing + `runtime.KeepAlive` wrapped in a `Secret` type with `Destroy()`. **memguard is opt-in hardening.** `runtime/secret` not v1. | Stdlib pattern is sufficient for SOC 2 baseline. memguard taxes init/build for users without a coredump threat model. | E13, E14 | final |
| **D7**: `Secret` type **MUST be `[]byte`-typed**, never `string`. | Go strings are immutable and unzeroable; can leak via interning, panic traces, error formatting. | standard Go security guidance | final |
| **D8**: Refuse to start if any required reference is unresolvable at pre-flight. **Fail closed** when a backend is unavailable. | Charter security-first invariant + SOC 2 posture. Silent degradation is the failure mode auditors flag. | charter, llm-connector FR-019 | final |

---

## Evidence Highlights

- **Key insight 1 — `zalando/go-keyring` is the active library; `99designs/keyring` is stale.** Cleanly narrows v1 choice. (E1, E2, E3)
- **Key insight 2 — XDG portal `Secret` is mandatory for Flatpak/Snap on Wayland.** Direct D-Bus access is blocked in modern sandboxes. (E4, E5, E6)
- **Key insight 3 — `go-piv/piv-go` is pure Go.** Unusual for hardware-token integration; meaningful posture win for cross-platform builds. (E11)
- **Key insight 4 — Stdlib zeroing + `runtime.KeepAlive` is the baseline; memguard is opt-in.** `runtime/secret` is too experimental — track for Go 1.27/1.28. (E13, E14)
- **Risks / Concerns**:
  - macOS Keychain prompts user on first access — UX needs a clear hint, especially in headless / first-run.
  - Memory hygiene on Windows is weaker than Unix (`VirtualLock` limits) — don't over-promise SOC 2 reviewers.
  - PKCS#11 / HSM customers will eventually ask; the v1 backend contract must accommodate them even if implementation lands in v2.

---

## Next Actions

1. Resolve `secrets-keychain` Open Question 1 (Linux fallback chain): D2 — Secret Service → XDG portal → explicit file backend.
2. Resolve Open Question 2 (Go keychain library): D1 — `zalando/go-keyring`.
3. Plan-phase: design `Secret` type (interface + `LockedSecret` and `MemguardSecret` impls).
4. Plan-phase: design `Backend` interface to accommodate AWS KMS, YubiKey PIV, future PKCS#11 / Azure / GCP without breaking changes.
5. Coordinate with `storage-foundations` D7 — DB encryption key is a credential reference resolved through this layer.
6. If v1 ships Flatpak/Snap, schedule the XDG portal shim work.
