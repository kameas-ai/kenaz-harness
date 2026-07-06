# Fleet OTel Archival — Operator & User Guide

Mission: `fleet-otel-archival-01NDFSEX11`

## Background

OTel spans and metrics are collected locally by the harness. This mission adds
an opt-in pipeline that posts a signed, redacted copy to the fleet endpoint so
per-team analytics (acceptance rates, latency p95s, error counts per provider)
can populate the fleet dashboard.

## Status

| Work Package | Title | Status |
|---|---|---|
| WP01 | MultiExporter — local + fleet fan-out | Shipped |
| WP02 | FleetRedactor — deny-list + @secret: pattern | Shipped |
| WP03 | FleetBuffer ring + FleetExporter batch sender | Shipped |
| WP04 | Tier enum + Settings persistence + capability gate | Shipped |
| WP05 | Settings → Privacy → Fleet Telemetry panel | Shipped |
| WP06 | First-launch telemetry onboarding modal | Shipped |
| WP07 | Privacy integration test + operator docs | Shipped |

---

## Architecture

### Consent tiers

| Tier | What is sent | Subscription required |
|---|---|---|
| **None** (default) | Nothing | Any |
| **Aggregate** | Span names, durations, status codes, metric counters | Pro+ |
| **Full** | All redactor-cleaned spans, metrics, log records | Team+ |

The effective tier is the lower of:
1. The user's stored `fleetTelemetryTier` setting (via Settings → Privacy → Fleet
   Telemetry or the first-launch onboarding modal).
2. The team-admin default pushed via the `fleet-config-pull` bundle's
   `model_prefs.telemetry_tier_default`.
3. The capability gate: `personal_fleet_dashboard` capability must be `true`
   or the tier falls back to `none`.

### Redactor

`core/fleet/telemetry_redactor.go` runs on every span before it enters the
buffer. It strips:

- Any attribute value matching `@secret:<locator>` (credential references).
- Attributes whose key is in the deny-list: `prompt`, `response`, `tool_input`,
  `tool_output`.
- Attributes with keys prefixed `private.`.
- Under `aggregate` consent, all attributes except a small whitelist
  (`http.status_code`, `duration_ms`, `provider`, `model`).

### Buffer

`FleetSpanExporter` maintains a bounded ring buffer (capacity 1000 spans).
When the buffer is 80 % full, a flush fires immediately. A background goroutine
also flushes every 30 seconds. When the fleet endpoint is unreachable, the
exporter backs off exponentially (1 min → 5 min → 30 min) and abandons after
24 hours with a single `WARN` log entry. Local OTel collection is completely
unaffected by fleet endpoint failures.

### Signing

Every POST is signed with the device's ed25519 key (same key used by fleet auth).
The signature covers the canonical JSON body with `signature` and
`device_pubkey_fingerprint` zeroed. The fleet server verifies the signature and
can reject batches from devices whose key has been revoked.

---

## User guide

### Configuring telemetry tier

Open **Settings → Privacy → Fleet Telemetry** and select a tier from the radio
picker:

- **None** — nothing is posted. Default for new installs and OSS builds.
- **Aggregate** — counts + durations only. Requires Pro+ subscription.
- **Full** — all redactor-cleaned data. Requires Team+ subscription.

On first launch you will see a one-time onboarding modal with the same picker.
Dismiss it to skip telemetry (sets tier to None); click **Confirm** to apply
your selection. You can always change your choice later from Settings.

### What is never sent

Regardless of tier, the following are always stripped by the redactor and never
reach the fleet endpoint:

- Conversation messages, prompt text, assistant responses.
- API keys, bearer tokens, `sk-*` keys, JWT values.
- OAuth secrets or any attribute value matching `@secret:<locator>`.
- Attributes whose key starts with `private.`.
- Log record bodies under the Aggregate tier.

---

## Operator guide

### Disabling fleet telemetry organisation-wide

Set the environment variable `HARNESS_FLEET_TELEMETRY=off` before starting the
harness. This forces the `FleetExporter` to a nop regardless of the per-user
consent tier, and hides the Settings tier picker from the UI.

### Configuring the team default tier

Push the following key in the `fleet-config-pull` bundle's `model_prefs` section:

```json
{
  "model_prefs": {
    "telemetry_tier_default": "aggregate"
  }
}
```

Valid values: `"none"` | `"aggregate"` | `"full"`. The user's per-user tier
takes priority when it is stricter; the team default can never escalate a user's
individual opt-out.

### Fork recipe: removing fleet OTel entirely

To ship a fork that never calls the fleet telemetry endpoint:

1. Delete `core/fleet/otel_exporter.go`, `core/fleet/otel_buffer.go`, and
   `core/fleet/otel_batch.go`.
2. In `core/telemetry/otel/multi_exporter.go`, remove the `fleet` exporter slot
   and return `local` directly.
3. Remove the `FleetTelemetryPanel.vue` and `TelemetryOnboardingModal.vue`
   components and their references from `SettingsView.vue` and `App.vue`.
4. Remove the `Fleet_GetTelemetryConsent` / `Fleet_SetTelemetryConsent` Wails
   bindings from `core/rpc/bindings.go`.
5. Run `go test ./core/...` to verify no remaining references.

---

## Privacy integration test

`core/fleet/otel_integration_test.go` plants known-bad patterns in span
attributes and asserts that:

1. `tier=full` — a span carrying a `prompt` attribute arrives at the fake fleet
   server without the `prompt` attribute (redactor strips it).
2. `tier=none` — no HTTP POST is made to the fake fleet server at all.
3. The ed25519 signature on the posted batch verifies against the test key.

Run locally:

```sh
CGO_ENABLED=1 go test ./core/fleet/... -run TestIntegration -race -count=1 -v
```
