# Fleet Context Sync — Privacy Threat Model & Implementation Notes

**Mission:** `fleet-context-sync-01NDFSEX15`
**Shipped in:** v0.22.0
**Last updated:** 2026-07-06

---

## Overview

Fleet context sync allows a user's session history and project bundles to persist
across devices and survive OS reinstalls. The feature is opt-in per-session and
per-project.

Three sub-systems ship in this mission:

| Sub-system | Capability gate |
|---|---|
| Session sync | `context_sync` |
| Project sync (with artifact-class opt-in) | `context_sync` |
| Team session handoff | `team_session_handoff` |

Recovery codes enable cross-device seed import without a network round-trip.

---

## Key derivation chain

```
OS keychain[fleet:<env>:context_seed]
    → 32-byte random per-device seed
    → HKDF-SHA256(seed, "session-events-v1")   → session stream AEAD key
    → HKDF-SHA256(seed, "project-events-v1")   → project stream AEAD key
```

**Fleet stores ciphertext only.** The seed never leaves the device.

The seed is generated on first `EnableSync` call and persisted in the OS keychain
(macOS Keychain, Windows Credential Manager, Linux Secret Service).

---

## Privacy invariants (enforced by guard scripts)

All invariants are checked by CI on every PR:

| Guard | File | What it checks |
|---|---|---|
| `check-no-cred-bytes-in-rpc.sh` | `core/rpc/bindings.go` | No seed / key bytes transmitted over RPC |
| `check-no-user-content-in-slog.sh` | `core/fleet/*.go` | Event `Payload` / `Bytes` fields never appear in slog calls |
| `check-no-fleet-imports.sh` | `core/rpc/*.go` | RPC layer imports fleet via interface only |

Specific invariants in code:

- `SessionEventRecord.Bytes` and `ProjectEventRecord.Bytes` are `// Privacy invariant: Bytes is NEVER logged.`
- `Event.Payload` carries `json:"-"` so it cannot accidentally be marshalled into a log.
- `wireEvent` only contains encrypted bytes; plaintext is never in the wire type.
- `MintRecoveryCode` return value comment: "the returned code contains the raw seed. Never log it."

---

## Encryption

Algorithm: **XChaCha20-Poly1305** (192-bit nonce, 128-bit tag).

- Nonce: randomly generated per call to `Encrypt`.
- Nonce and ciphertext are stored together in `wireEvent{Nonce, EncryptedPayload}`.
- Fleet cannot decrypt: it holds ciphertext bytes only, never the key.

Key size: 32 bytes (`chacha20poly1305.KeySize`). Derived via HKDF-SHA256 from the
32-byte seed. Compromise of the project key does not compromise the session key
(different HKDF labels, independent expansion).

---

## Team handoff key exchange

Sender path:
1. Fetch recipient X25519 public key from `GET /api/v1/identity/public-key?user_id=<id>`.
2. Generate ephemeral X25519 private key (per-handoff, discarded after use).
3. ECDH(ephemeral_priv, recipient_pub) → shared_secret (32 bytes).
4. HKDF(shared_secret, info="handoff-v1") → 32-byte AEAD key.
5. Encrypt each session event with the handoff AEAD key.
6. POST `{events, ephemeral_public_key}` to `/api/v1/handoff/send`.

Recipient path:
1. GET `/api/v1/handoff/{id}` → `{events, ephemeral_public_key}`.
2. Derive receive key: `HKDF(seed XOR ephemeral_pub_bytes, "handoff-v1")`.
   *(v0.21.0 simplified model — see note below)*
3. Decrypt events.

**v0.21.0 note:** The receive-key derivation uses a seed-XOR approximation rather
than a proper persistent X25519 identity key pair. This means the sender and
recipient must share the same seed to complete a full round-trip — which is only
the case within a single device's sync chain. Team handoff to a separate device
with a different seed will produce decryption failures. A future work package will
replace this with a proper persistent asymmetric key pair stored separately from
the context seed.

---

## Recovery code format

```
KENAZ-<base64std-block1>-<base64std-block2>-...
```

- Base64 alphabet: standard (A-Z, a-z, 0-9, +, /) — **not** URL-safe.
  URL-safe base64 uses `-` as a character, which would conflict with the
  hyphen delimiter. Standard base64 avoids this.
- Block size: 8 chars per block (readability).
- Full code for a 32-byte seed: `KENAZ-` + 5 blocks of 8 + 1 block of 3 = 7 dash-separated parts.

The code encodes the raw 32-byte seed. Whoever has the code can decrypt all
fleet-stored sessions and projects for this device. Treat it like a master password.

UX requirements:
- Display the code once only (one-time reveal pattern in `RecoveryCodeFlow.vue`).
- Require explicit acknowledgement before dismissing.
- Never store or transmit the code after display.

---

## Threat model

### Threats mitigated

| Threat | Mitigation |
|---|---|
| Fleet server compromise | Fleet holds only ciphertext. No key material is stored server-side. |
| Network interception | TLS for transport; XChaCha20-Poly1305 AEAD for data-at-rest. |
| Side-channel via logs | No event bytes in slog; `Payload` is `json:"-"`. Guard scripts enforce this. |
| Cross-stream key compromise | Separate HKDF labels for session vs. project streams. |
| Recovery code leakage | One-time display with explicit ack gate; code not logged or persisted. |
| RPC surface leakage | Seed / key bytes never passed over Wails RPC bindings. |

### Threats accepted / deferred

| Threat | Status |
|---|---|
| Fleet stores event count / sequence numbers | Accepted. Metadata leakage is the same as any cloud storage. Payload bytes are encrypted. |
| OS keychain compromise | Accepted. Threat model ends at the OS keychain; compromise of the OS keychain gives access to the seed. |
| Team handoff sender-side re-encryption | Accepted. Sender must trust the fleet identity service to provide a correct recipient public key. TOFU model in v0.21.0. |
| v0.21.0 seed-XOR receive key | Deferred to v0.22+. Current model limits cross-device handoff to devices sharing the same seed. |
| Recovery code QR / clipboard exposure | Deferred. The display component shows plaintext; clipboard/screenshot protection is OS-layer. |

---

## Fork recipe: add a new stream type

To add a new category of context sync (e.g., `agent-memory-events`):

1. Add an HKDF label constant to `context_crypto.go`:
   ```go
   LabelAgentMemory DeriveLabel = "agent-memory-events-v1"
   ```

2. Create `agent_memory_sync.go` modelled on `session_sync.go` / `project_sync.go`.
   Key structural requirements:
   - Accept `*Client`, `contextaudit.Emitter`, `*Capabilities` in constructor.
   - Call `SeedKey()` (not `LoadContextSeed()`) in `EnableSync` to auto-generate seed on first use.
   - Call `LoadContextSeed()` in `Resume` / `AppendEvent` / `DeleteRemote`.
   - Never log event bytes. Use `shortID()` for stream/session IDs in slog.
   - Emit audit events for enable/disable/delete/resume lifecycle transitions.

3. Add the new `Capabilities` constant in `capability.go` if a tier gate is needed.

4. Wire the new syncer into `core/rpc/api.go` via the view package interface.

5. Add vitest specs for the new Vue view components.

6. Run `bash scripts/ci/check-codegen.sh` and all guard scripts before opening the PR.
