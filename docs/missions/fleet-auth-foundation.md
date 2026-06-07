# Fleet Auth Foundation — Operator & Developer Guide

Mission: `fleet-auth-foundation-01NDFSEX08`  
Shipped: v0.18.0

---

## What is fleet?

Fleet is the kenaz harness's optional cloud integration layer. It provides
Zitadel PKCE authentication, per-install identity, org/team context, and the
role/tier contracts that downstream fleet missions build on. The harness is a
fully-functional local-only product without fleet; fleet features are invisible
until the user explicitly signs in.

---

## How to sign in (user flow)

1. Open **Settings → Account** (`/settings?tab=account`).
2. Click **"Sign in to fleet"**.
3. The system browser opens to the Zitadel authorization page for your org.
4. After granting consent you are redirected to `http://127.0.0.1:<ephemeral-port>/callback`.
5. The harness exchanges the authorization code for tokens (PKCE S256 method;
   no client secret — RFC 8252 native app) and calls `POST /api/v1/enroll` on
   the fleet server to populate your identity.
6. The Account panel refreshes showing your email, tier badge, org name, and
   team name.

The access token is stored in the OS keychain (macOS Keychain, Windows
Credential Manager, Linux libsecret). The refresh token is used automatically
when the access token expires; you only need to sign in again when the refresh
token itself expires or is revoked.

---

## How to opt out

Set `HARNESS_FLEET_DISABLED=1` in the process environment before starting the
harness:

```bash
HARNESS_FLEET_DISABLED=1 ./kenaz-harness
```

When this variable is set:
- `fleet.Disabled()` returns `true`.
- `NewClient()` returns a `nopClient` that returns `ErrFleetDisabled` for every
  method call.
- The Account panel shows a banner explaining that fleet is disabled.
- No network connections are made to any fleet server.
- All v0.17.0 local features continue to work identically.

Truthy values: `1`, `true`, `yes` (case-insensitive). Any other non-empty value
is also truthy. False values: `0`, `false`, `no`.

---

## How to switch environments (`KENAZ_HARNESS_ENV`)

The harness ships a single binary embedding dev / stage / prod coordinates as
ldflag-injected vars. `KENAZ_HARNESS_ENV` selects the profile at runtime:

| Value | Profile | Badge color |
|---|---|---|
| unset or `prod` | Production fleet server | None (hidden) |
| `dev` | Dev fleet server | Yellow "DEV" |
| `stage` | Staging fleet server | Blue "STAGE" |
| `local` | Local fleet server (reads env-var overrides) | Red "LOCAL" |

For `local`, the following env vars override individual coordinates:

```bash
export HARNESS_FLEET_ISSUER="http://localhost:8080"
export HARNESS_FLEET_CLIENT_ID="my-native-client-id"
export HARNESS_FLEET_AUDIENCE="my-api-audience"
export HARNESS_FLEET_BASE_URL="http://localhost:8090"
```

---

## Fork recipe

Forking operators who do not want fleet integration can remove it cleanly.
The package is isolated: only `core/rpc/views/settings/` is permitted to
import `core/fleet/` (enforced by `scripts/ci/check-no-fleet-imports.sh`).

**Step 1** — Delete `core/fleet/`:

```bash
rm -r core/fleet/
```

**Step 2** — Remove the four wire-up points:

**`main.go`** — remove the `fleet.NewClient()` call and the `fleet.Disabled()`
guard at startup. Exact lines: the `*fleet.Client` initialisation block and the
early-return guard that skips fleet when `Disabled()` is true.

**`core/rpc/api.go`** — remove the `*fleet.Client` field on `API`, its
initialisation in `New()`, and the `fleet.Disabled()` guard. Remove the
`import "github.com/kameas-ai/kenaz-harness/core/fleet"` line.

**`core/rpc/views/settings/api.go`** — remove the five `Fleet*` method
signatures from `SettingsAPI` interface and the `FleetIdentity` /
`FleetProfileInfo` types.

**`frontend/src/views/settings/AccountPanel.vue`** — delete this file entirely.
Remove the `import AccountPanel from ...` line and the `v-else-if="showAccountTab"`
block from `SettingsView.vue`. Remove the `showAccountTab` computed ref.

**Step 3** — Verify:

```bash
go build ./...
cd frontend && npm run build
```

Both must succeed with no fleet-related errors.

---

## OSS-first contract

The following invariants are binding for all fleet-related work:

1. **No non-fleet feature requires sign-in.** Every v0.17.0 feature must keep
   working identically when signed-out or with `HARNESS_FLEET_DISABLED=1`.

2. **Fleet features are INVISIBLE until sign-in.** Do not render disabled
   buttons with "sign in to use" tooltips — render nothing. The only
   fleet-related affordance in signed-out state is the single "Sign in to fleet"
   entry in Settings → Account.

3. **`HARNESS_FLEET_DISABLED=1` makes the harness behave identically to a fork
   that removed `core/fleet/`.**

4. **Non-fleet RPC views must NOT import `core/fleet/`.** Only
   `core/rpc/views/settings/` is allowed to. Enforced by
   `scripts/ci/check-no-fleet-imports.sh`.

5. **Fork recipe is verifiable**: removing `core/fleet/` and the four wire-up
   points listed above must yield a building harness with both `go build ./...`
   and the frontend build succeeding.
