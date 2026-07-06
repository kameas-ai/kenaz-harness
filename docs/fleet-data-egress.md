# Fleet data-egress enumeration

This document enumerates *every* network egress path that is active when fleet
config distribution is enabled in the Kenaz harness. It is the authoritative
source for privacy copy across the UI (`AuditView`, `ToolsView`,
`ContextsView`, `BundlesView`, `FleetHealthChip`).

---

## Baseline (fleet config distribution disabled / OSS builds)

When `FleetHealthView.configDistributionEnabled == false` (the default for OSS
builds and dev installs where no signing key is wired), **no fleet traffic
leaves the device**. The harness is entirely local:

- All audit events remain on-device.
- Tool invocations route through the local MCP client only.
- Context files, bundle bytes, and session data never leave the device.
- Crash reporting is separately gated by the crash-reporting tier setting.

---

## Fleet-config-distribution ON (`configDistributionEnabled == true`)

When an `FLEET_SIGNING_KEY` public key is wired into the binary at build time,
the following egress paths become active.

### 1. Fleet capability poll

**Endpoint:** `GET /api/v1/capabilities` (and `/api/v1/configs/current`)  
**Trigger:** Periodic background poll (interval: ~60 s with ±10 % jitter, backoff
on failure up to 60 min).  
**Payload sent:** Fleet auth token in `Authorization: Bearer` header.  
**Data received:** Capability flags, lockdown status, config bundle metadata.  
**What leaves the device:** Auth token only. No user content, no paths, no
session data.

### 2. Config-bundle download

**Endpoint:** `GET /api/v1/configs/<bundle_id>`  
**Trigger:** When a new bundle ID is detected on the capability poll response.  
**Payload sent:** Auth token in the `Authorization: Bearer` header.  
**Data received:** Signed bundle JSON (policy, model prefs, telemetry opt-ins,
lockdown flags).  
**What leaves the device:** Auth token only.

### 3. Config-apply ACK

**Endpoint:** `POST /api/v1/configs/<bundle_id>/ack`  
**Trigger:** After every config-bundle apply attempt (successful or partial).  
**Payload sent:**

```json
{
  "bundle_id": 42,
  "applied": true,
  "error": "",
  "errors": []
}
```

On partial failure, `applied` is `false` and `error`/`errors` carry the
per-section error messages **after path redaction**: absolute filesystem paths
in error strings are replaced with `"<path>"` before the payload is
serialised. No user content, session identifiers, or credential material is
included.  
**Best-effort:** ACK failures are logged and not retried.

### 4. Token refresh

**Endpoint:** `POST /api/v1/auth/refresh` (fleet server auth endpoint)  
**Trigger:** Proactively, when the stored access token is within 30 s of expiry
and a fleet API call is about to be made. Also triggered reactively on
`HTTP 401` from the fleet server.  
**Payload sent:** Fleet refresh token (opaque, server-issued).  
**Data received:** New access token + expiry timestamp.  
**What leaves the device:** The refresh token only.

### 5. Telemetry opt-ins (optional, user-gated)

**Endpoint:** `POST /api/v1/telemetry/optins` (or equivalent on the fleet server)  
**Trigger:** Only when the user has explicitly opted into one or more telemetry
categories via Settings → Account → Fleet Telemetry.  
**Payload sent:** A list of opted-in category keys (e.g. `["model_usage"]`).
No user content, message bodies, or personal identifiable information is
attached.  
**Requires active consent:** This path is inactive if the user has not opted
into any telemetry category. Each category is individually user-controlled.

---

## What never egresses (regardless of fleet configuration)

The following data **never** leaves the device via the fleet paths:

- Chat message bodies or tool call arguments/responses.
- Audit event payloads (redacted and stored locally only).
- Context file contents.
- Bundle artifact bytes (stored in the local CAS only).
- MCP server configurations or tool definitions.
- Keyring secrets, API keys, or other credentials.
- Local filesystem paths (redacted to `<path>` in ACK error strings).
- Session IDs, project IDs, or any user-generated identifiers.

---

## Fleet-session-expired event

When the fleet access token expires and cannot be refreshed (e.g. the refresh
token is also expired or the fleet server is unreachable), the harness emits a
`fleet:session:expired` event over the internal broker. This surfaces a
re-authentication banner in the UI. No data egresses at this point; the event
is local only.

---

*Last updated: 2026-07-05 (fleet-integrity-observability WP10 / FR-011)*
