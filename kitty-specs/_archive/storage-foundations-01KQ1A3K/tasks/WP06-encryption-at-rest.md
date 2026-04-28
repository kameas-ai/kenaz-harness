---
work_package_id: "WP06"
title: "Encryption-at-rest with libSQL page encryption and keychain reference"
dependencies:
  - "WP01"
  - "WP05"
planning_base_branch: "feat/storage-foundations-01KQ1A3K"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on feat/storage-foundations-01KQ1A3K; completed changes must merge back into main via squash merge per charter Branch Strategy."
subtasks:
  - "T001: define secrets.CredentialReference + Backend interface contract (stub for now)"
  - "T002: wire libSQL WithEncryption hookup behind cfg.EncryptionKey"
  - "T003: zero key bytes immediately after libSQL ingests them"
  - "T004: record encryption_status in harness_meta (enabled/disabled/disabled_with_disk_encryption)"
  - "T005: implement re-key drain + libSQL rekey + sentinel-row recovery"
  - "T006: implement decrypt-to-plaintext-copy operation for forensics"
  - "T007: tests for missing-key, wrong-key, rotation-interrupted, opt-out audit"
phase: "Phase 6 - Encryption-at-rest"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP06 – Encryption-at-rest with libSQL page encryption

## Goal

Wire libSQL's page-level encryption to a `secrets.CredentialReference` resolved through the (forthcoming) `secrets-keychain` mission. Default-on for new installs, opt-out recorded explicitly. Provide re-key (rotate) with interruption-safe recovery and a `harness db decrypt` operation for forensics. Until the secrets-keychain mission lands, depend only on a stable interface stub so this WP can land independently.

## Spec references

- FR-008 (encryption at rest)
- NFR-005 (encryption performance overhead < 10% p95)
- C-003 (no plaintext encryption keys in config)
- US 4 acceptance scenarios 1-3 (open with key, fail without key, opt-out recorded)
- Edge case: encryption key rotated; in-place re-encryption supported but slow
- SC-004 (encryption adds < 10% p95 write-latency overhead)

## Plan references

- §4.2 Encryption Hookup (WithEncryption, byte zeroing, re-key flow)
- §6.1 secrets-keychain integration (CredentialReference shape)
- §8 Risk Register R3 (forensic decrypt), R9 (rotation interrupted recovery)
- §7 v1.0 ships `harness db decrypt`

## Cross-mission dependency

- **secrets-keychain**: this WP depends on the `CredentialReference` type and `Backend.Resolve(ref) ([]byte, error)` interface that the `secrets-keychain` mission will define. To unblock independent merging, define the interface stub in `core/storage/internal/secrets/iface.go` and document the migration to the real type once secrets-keychain lands. Coordinate with that mission's owner to keep the shape compatible.

## Subtasks

1. In `core/storage/internal/secrets/iface.go`, declare `CredentialReference` (struct with Keychain alias + opaque ID), `Backend` interface (`Resolve(ctx, ref) ([]byte, error)`), and a no-op `NoopBackend` for tests. Add a TODO/note pointing at secrets-keychain mission ID for replacement.
2. Modify `Config` to carry `EncryptionKey *CredentialReference` (nil = unencrypted) and `SecretsBackend Backend`. Add `EncryptionStatusOptOut` enum value `disabled_with_disk_encryption` for explicit operator opt-out.
3. In `core/storage/db/encryption.go`, implement key resolution at `Open`: call `cfg.SecretsBackend.Resolve(cfg.EncryptionKey)`, pass bytes to libSQL `WithEncryption` (or equivalent option), then zero the local slice (`for i := range b { b[i] = 0 }` plus runtime memory barrier). Document why we cannot fully guarantee zeroing under Go's GC.
4. After Open, write `encryption_status` into `harness_meta`: `enabled` (key present and used), `disabled` (no key, no opt-out), or `disabled_with_disk_encryption` (operator explicitly opted out via Config flag). Emit `db_opened` event with the encryption_status payload.
5. Implement re-key (`Diagnostics.RotateEncryption(ctx, newRef)` or a method on `DB`): drain write queue, write a `rotation_in_progress` sentinel row to `harness_meta`, call libSQL re-key, on success clear the sentinel and emit `encryption_rotated`. On crash mid-rotation, next `Open` detects sentinel + version mismatch and recovers to the prior key (per plan §8 R9).
6. Implement `harness db decrypt`: `DecryptCopy(ctx, srcKey, dst string) error`; opens the source encrypted DB, does an online-backup-style copy to an unencrypted destination. Emits `db_decrypted` event (new kind; add to WP05 taxonomy). Operator-confirmed only.
7. Tests: missing key -> `ErrEncryptionKeyMissing`; wrong key -> `ErrEncryptionKeyMismatch`; opt-out path records `disabled_with_disk_encryption` in `harness_meta` AND emits a `db_opened` event with the explicit choice payload; rotation succeeds + emits one `encryption_rotated`; rotation interrupted (simulated crash mid-rotate) recovers cleanly on next Open. Bench under NFR-005 budget on a developer laptop.

## Acceptance criteria

- Opening a DB whose key reference resolves succeeds; opening it without the keychain entry fails with `ErrEncryptionKeyMissing`.
- Opening with the wrong key returns `ErrEncryptionKeyMismatch`.
- `encryption_status` in `harness_meta` matches the operator's choice; opt-out is auditable via the `db_opened` event payload.
- Rotate-in-progress -> crash -> Open recovers with prior key and surfaces a structured `EncryptionRotationRecovered` event (plus the original prior-key `db_opened`).
- `DecryptCopy` produces an unencrypted file readable by stock SQLite tools.
- NFR-005 microbench gate green: encrypted-vs-unencrypted write p95 overhead < 10% on the CI benchmark fixture.

## Files to create/modify

- Create: `core/storage/internal/secrets/iface.go` (interface stub)
- Create: `core/storage/db/encryption.go`
- Modify: `core/storage/storage.go` (Config additions; new sentinel errors; rotate API)
- Modify: `core/storage/db/conn.go` (call encryption.Open before pragma application)
- Modify: `core/storage/migrations/bootstrap.go` (`harness_meta.encryption_status` column already present from WP03; populate at Open)
- Modify: `core/storage/eventkinds.go` (add `db_decrypted`)
- Create: `core/storage/db/encryption_test.go`, `core/storage/db/encryption_bench_test.go`

## Definition of done

- Encryption-at-rest end-to-end: resolve via stub Backend, libSQL ingests key, status recorded.
- Rotation works and is interruption-safe.
- Forensic decrypt operation in place.
- Performance budget verified by bench.
- Stub interface clearly marked for swap-in once `secrets-keychain` lands.
