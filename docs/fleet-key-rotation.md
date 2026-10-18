# Fleet Signing-Key Rotation Runbook

**Last updated**: 2026-10-06
**Spec**: fleet-integrity-observability-ZJ1TEPGQ WP03 / FR-003; bundle-key-rotation WP01–WP04

---

## Overview

Fleet config bundles are signed with an ed25519 private key held by Fleet
(created by Terraform). Every harness binary **pins a set** of the matching
public keys at build time:

```
-ldflags "-X github.com/kameas-ai/kenaz-harness/core/fleet.fleetSigningPublicKeys=<hex>[,<hex>...]"
```

Each entry is exactly 64 hex characters: the raw 32-byte ed25519 public key.
Whitespace and empty entries are ignored; **any malformed entry rejects the
whole set** — including a non-canonical encoding or a small-order
(degenerate) point such as the identity, under which signatures are
forgeable without a private key (config distribution is then disabled and the harness logs a boot
ERROR; `release.yml` also fails the build on a malformed entry).

`release.yml` picks the list per target env from repo Actions **variables**
(public keys — variables, not secrets, deliberately):

| Env | Variable |
|---|---|
| dev | `FLEET_SIGNING_PUBKEYS_DEV` |
| stage | `FLEET_SIGNING_PUBKEYS_STAGE` |
| prod | `FLEET_SIGNING_PUBKEYS_PROD` |

An empty/unset variable does not fail the build: that env's binary ships with
config distribution disabled, and the build emits a `::warning` naming the
variable.

> **Transition (dated 2026-10-06):** the legacy single-key pin
> (`fleetSigningPublicKeyBytes`, fed from the `FLEET_SIGNING_PUBKEY` secret)
> is still merged into the set for one release. Drop both in the release
> after the first one that ships the per-env variables.

### key_id routing

Every bundle carries a top-level `"key_id"` **inside the signed payload**:

```
key_id = lowercase hex( SHA-256( raw 32-byte ed25519 public key )[0:8] )   // 16 chars
```

Compute it for a public key with:

```bash
printf '%s' "<64-hex pubkey>" | xxd -r -p | shasum -a 256 | cut -c1-16
```

Verification (`core/fleet` `VerifyWithKeySet`):

| Bundle | Result |
|---|---|
| no keys pinned | reject — `ErrSigningKeyNotConfigured` (Settings: "no-key") |
| has `key_id`, a pinned key matches | verify with **only** that key |
| has `key_id`, no pinned key matches | reject — `ErrSigningKeyUnknown` (Settings: "unknown-key") |
| lacks `key_id` | try every pinned key; any one verifying is valid |
| signature does not verify | reject — `ErrInvalidSignature` |
| valid, but `bundle_id` ≤ last applied | reject — `ErrBundleIDNonMonotonic` (replay guard, unchanged) |

Because `key_id` is signed, it cannot be rewritten in transit to steer
verification: a changed `key_id` either selects no key or breaks the
signature.

### What `ErrSigningKeyUnknown` means operationally

The install's pins **predate the newest fleet signing key**: Fleet is signing
with a key this binary was never built with. The harness keeps its last
applied config, applies nothing new, and shows **"unknown-key"** in the fleet
health chip (`FleetHealth.ConfigSource`) with the config-pull error
*"bundle signed with an unknown key: key_id … matches none of the N key(s)
pinned in this build"*. Retrying does not help — the remedy is **updating the
harness** to a release that pins that key. If many installs show it, step (c)
below was cut short.

---

## Rotation: the 5-step overlap runbook

**(a) Terraform creates the next key.** Add the next ed25519 key alongside the
current one (do not destroy the current key). Record its public key as 64 hex
chars and its key_id.

**(b) Release a harness pinning current + next.** Set
`FLEET_SIGNING_PUBKEYS_<ENV>` to `<current>,<next>` for each env and cut a
release. Fleet is still signing with *current*; the new pin is dormant.

**(c) Wait for adoption.** Wait until the installs you care about run a build
from step (b) or later. Any install still on an older build will report
"unknown-key" after step (d).

**(d) Flip Fleet to the next key, then invalidate cached bundles.** Point
Fleet's bundle signer at the *next* key, then run:

```bash
fleet-admin bundle invalidate --all-orgs
```

so cached bundles signed by *current* are re-signed under *next* (and get a
higher `bundle_id`, so the replay guard admits them).

**(e) Drop the old pin in a later release.** Set
`FLEET_SIGNING_PUBKEYS_<ENV>` to `<next>` only and release. Then Terraform can
retire the old key.

---

## Emergency revocation

If a private key is compromised:

1. **Immediately** stop Fleet signing with it (flip to a fresh key, or stop
   signing).
2. Set `FLEET_SIGNING_PUBKEYS_<ENV>` to exclude the compromised key (and
   include the fresh one) and ship an emergency patch release.
3. Installs that upgrade reject anything signed by the compromised key;
   installs that have not upgraded reject bundles from the fresh key as
   "unknown-key". Both are fail-closed.

---

## Tests

`core/fleet/signing_keyset_test.go` pins the key_id formula
(`TestSigningKeyID_ContractVector`: pubkey `000102…1f` → key_id
`630dcd2966c43366`), the pin-list grammar (`TestParseSigningKeySet`,
`TestFleetSigningKeys_*`), the {no/one/two keys} × routing matrix including
replayed `bundle_id` (`TestVerifyWithKeySet_KeyIDMatrix`), the payload field
order (`TestSigningPayload_KeyIDFieldOrder`), and the poller status
(`TestConfigPoller_UnknownKeyID_SurfacesInStatus`). The Settings projection is
`core/rpc/views/settings/fleet_keyrotation_test.go`.
