# Fleet Config Pull — Operator & Developer Guide

Mission: `fleet-config-pull-01NDFSEX10`  
Shipped: v0.19.0

---

## What is fleet config pull?

Fleet config pull is the mechanism by which the harness automatically receives
and applies policy and preference updates from the fleet server without user
action. Org admins push a signed *bundle* to the fleet server; every enrolled
harness instance polls for it, verifies the ed25519 signature, and applies the
relevant sections (Cedar policy rules, MCP allow-list, model preferences, and
ML weight URLs) — all while remaining fully functional as a local-only product
when fleet is not configured.

---

## Bundle anatomy

A bundle is a JSON object signed by the fleet server:

```json
{
  "bundle_id": 42,
  "issued_at": "2026-05-16T12:00:00Z",
  "cedar_delta": { "rules": { "team-allow-github": "permit(principal,action,resource);" } },
  "mcp_allowlist": ["github", "slack"],
  "model_prefs": { "default_model": "anthropic/claude-opus-4" },
  "kameas_ml_weight_urls": ["https://weights.example.com/model-v1.bin"],
  "signature": "<base64-raw ed25519 sig>"
}
```

Every field except `signature` participates in the canonical signing payload.
`bundle_id` is monotonically increasing — the harness rejects any bundle whose
ID is not strictly greater than the last applied ID, preventing replay attacks.

---

## Signature scheme

The fleet server signs bundles with ed25519. The harness verifies using a
public key compiled in via ldflags:

```
-ldflags "-X core/fleet.fleetSigningPublicKeyBytes=<hex-encoded-pubkey>"
```

Signature algorithm:

1. Serialize the bundle as canonical JSON (all fields except `"signature"`),
   sorted by key name.
2. SHA-256 the canonical JSON bytes.
3. `ed25519.Verify(pubKey, sha256[:], signature)`.

A signature mismatch is a **hard reject**: the previous bundle remains in effect
and the harness enters backoff without calling the applier.

---

## Polling lifecycle

| State | Behaviour |
|---|---|
| Not signed in | Poll is skipped silently; no backoff |
| 304 Not Modified | No-op; resets backoff |
| 200 OK | Verify → apply → ACK → persist |
| Signature failure | Hard reject; 5/15/60 min backoff |
| Apply error (partial) | ACK with `applied=false`; bundle_id advances; backoff |
| Other transport error | Log; 5/15/60 min backoff |

Normal polling interval: **5 minutes**.

The checksum of the last-seen bundle JSON body is sent as a `?checksum=<hex>`
query parameter. The server responds with 304 when the bundle has not changed.

Disk state (`<DataDir>/fleet/`):

- `bundle_id.txt` — last applied bundle ID (persists across restarts)
- `bundle_checksum.txt` — last seen bundle checksum (for 304 on next poll)
- `weight_urls.json` — last applied ML weight URL list

---

## ACK protocol

After every 200 response (regardless of apply outcome) the harness POSTs to
`/api/v1/configs/<bundle_id>/ack`:

```json
{ "bundle_id": 42, "applied": true }
// or, on partial failure:
{ "bundle_id": 42, "applied": false, "error": "cedar engine error: ..." }
```

The ACK uses a short-lived background context (10 s timeout) so it completes
even if the poller is stopping concurrently.

---

## Cedar team-managed rules

Cedar rules in `cedar_delta.rules` are loaded under the policy ID prefix
`fleet-team/<team_rule_id>`. This keeps them namespaced away from
user-authored policies in the embedded store.

Local override: an org member with the `opa_custom_rego` capability can create
a forbid policy that shadows a specific team rule:

```
forbid(principal, action, resource)
when { context has "team_rule_id" && context.team_rule_id == "team-allow-github" };
```

The Cedar Editor UI provides a one-click **Override** button that scaffolds
this policy when the user selects a `fleet-team/` file.

---

## MCP allow-list filter

`mcp_allowlist` is a list of MCP recipe IDs that are permitted. When non-empty,
`core/mcp/recipes.IsAllowed(recipeID)` returns `false` for any ID not in the
list. When the list is absent (nil) or the bundle has not yet been applied,
`IsAllowed` returns `true` (no restriction).

The allow-list filter is a process-level singleton (`globalAllowlist`) stored in
`core/mcp/recipes`. It is set atomically when the bundle is applied and is
safe for concurrent reads from the MCP dispatch path.

---

## Model preferences

`model_prefs.default_model` overrides the user-configured default model for
new conversations when set. Fleet model preferences are surfaced through
`settings.FleetModelPrefs()` and applied in the conversation start path when
the bundle is current.

---

## OSS-first contract

Only `core/rpc/views/settings/` imports `core/fleet`. The OSS policy engine
(`core/mcp/recipes`, `core/policy/cedar`) must not reference the fleet package.
The `compositeConfigApplier` in `core/rpc/views/settings/fleet.go` is the
fan-out point that bridges the fleet bundle to each OSS subsystem.

---

## Observability

Three audit events are emitted:

| Kind | Trigger |
|---|---|
| `fleet.config.applied` | Bundle applied successfully |
| `fleet.config.signature_rejected` | Signature verification failed |
| `fleet.config.partial_failure` | ApplyBundle returned a non-nil error |

Frontend: the Cedar Editor shows a fleet config-pull status banner (last applied
bundle ID, timestamp, source) when the user is signed in.

Settings RPC: `Settings_FleetConfigPullStatus()` returns a
`FleetConfigPullStatusView` with `lastAppliedId`, `lastAppliedAt`, `lastError`,
`source`, and `bundleChecksum`.

---

## Key files

| File | Purpose |
|---|---|
| `core/fleet/bundle.go` | Bundle struct, signing payload, Verify() |
| `core/fleet/signing_key.go` | ldflag-populated public key |
| `core/fleet/config_pull.go` | ConfigPoller — polling, verify, apply, ack |
| `core/fleet/ack.go` | PostConfigACK |
| `core/fleet/cedar_apply.go` | CedarBundleApplier, SetTeamBundle |
| `core/fleet/weight_urls.go` | SetWeightURLs, CurrentWeightURLs |
| `core/mcp/recipes/allowlist.go` | AllowlistFilter, ApplyFleetAllowlist, IsAllowed |
| `core/rpc/views/settings/fleet.go` | compositeConfigApplier, FleetConfigPullStatus RPC |
| `frontend/src/views/policy/CedarEditor.vue` | Policy editor with team-badge + Override button |
