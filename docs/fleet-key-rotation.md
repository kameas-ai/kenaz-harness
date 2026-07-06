# Fleet Signing-Key Rotation Runbook

**Last updated**: 2026-07-05  
**Spec**: fleet-integrity-observability-ZJ1TEPGQ WP03 / FR-003

---

## Overview

Fleet config bundles are signed with an ed25519 private key held by ops.
The corresponding public key is injected into every release binary via:

```
-ldflags "-X …core/fleet.fleetSigningPublicKeyBytes=<64-char hex>"
```

The public-key byte string is stored in the `FLEET_SIGNING_PUBKEY` GitHub
Actions secret (org-level, scoped to the `kameas-ai/kenaz-harness` repo).

The accept-set mechanism (`FleetSigningKeys()`) returns a slice of public keys.
`VerifyWithKeySet()` accepts any bundle signed by any key in the set. This
allows a **rotation overlap window** where both the outgoing and the incoming
key are valid, so clients do not need to upgrade simultaneously.

---

## When to rotate

Rotate the fleet signing key when:

1. The private key is (or may be) compromised.
2. The key age exceeds your org's key-lifetime policy.
3. The key algorithm needs to change (currently ed25519, no upgrade planned).

---

## Rotation procedure

### Step 1 — Generate a new keypair

```bash
# Generate a new ed25519 private key (keep this secret; do NOT commit):
openssl genpkey -algorithm ed25519 -out /tmp/fleet-signing-new.pem

# Extract the raw 32-byte public key as 64 lowercase hex chars:
openssl pkey -in /tmp/fleet-signing-new.pem -pubout -outform DER \
  | tail -c 32 | xxd -p | tr -d '\n'
# Copy the output — this is the new FLEET_SIGNING_PUBKEY value.
```

### Step 2 — Add the new key to the accept-set (overlap window starts)

**At present, the accept-set is a single-key list from `fleetSigningPublicKeyBytes`.**
A future enhancement will support a second env variable for the overlap window.
Until then, the overlap window is implemented by:

1. Updating `FLEET_SIGNING_PUBKEY` to the new public key in GitHub Actions secrets.
2. Tagging a new release binary so all clients get the new key.
3. Starting to sign all new bundles with the new private key **after** the overlap window.

Because `FLEET_SIGNING_PUBKEY` is injected at binary build time, a key swap
requires a new release. If you need a zero-downtime rotation (all running clients
keep working while the new key rolls out), contact the platform team to evaluate
adding a second ldflag variable (`FLEET_SIGNING_PUBKEY_SECONDARY`) before
rotating.

### Step 3 — Sign all new bundles with the new key

Once the new release is deployed to all clients, switch the server-side signing
to use the new private key. The old key can now be retired.

### Step 4 — Retire the old key

Update `FLEET_SIGNING_PUBKEY` in GitHub Actions to contain ONLY the new public
key. Build and deploy a new release. Old clients running binaries with the old
key will stop accepting bundles signed by the new key — this is intended; they
must upgrade.

---

## Emergency revocation

If the private key is compromised and you cannot wait for a normal rotation:

1. **Immediately** stop signing bundles on the server.
2. Update `FLEET_SIGNING_PUBKEY` to a new key value.
3. Build and deploy an emergency patch release.
4. All clients that upgrade will reject any bundle signed by the compromised key.
5. Clients that have NOT upgraded will reject all bundles (because the server
   has stopped signing). This is the fail-closed safe state.

---

## Testing a key swap locally

```bash
# Generate a fixture keypair for integration tests:
openssl genpkey -algorithm ed25519 -out /tmp/test-fleet-priv.pem
TEST_PUB=$(openssl pkey -in /tmp/test-fleet-priv.pem -pubout -outform DER \
  | tail -c 32 | xxd -p | tr -d '\n')

# Run the config-pull integration test with the fixture key:
FLEET_SIGNING_PUBKEY_TEST="${TEST_PUB}" \
  go test ./core/fleet/... -run TestConfigPull -v
```

See `core/fleet/bundle_test.go` for the `TestVerifyWithKeySet_*` acceptance
tests that verify:
- Empty accept-set rejects all bundles.
- A foreign (unknown) key is rejected.
- Both keys in the rotation-overlap window are accepted.
- After retirement, only the new key is accepted.
