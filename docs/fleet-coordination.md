# Fleet integration — cross-repo coordination

**Last updated**: 2026-05-16
**Companion**: `docs/fleet-spec-breakdown.md` (the 8-mission roadmap)
**Repos involved**:
- `kenaz-harness` (this repo) — implements the consumer side
- `kenaz-fleet` — implements the server side (Go + Postgres). **Important**: the `origin/feat/coreui-dashboard` branch is significantly further along than `main` — 24 additional endpoints (enrollment, teams, audit-log query, org settings, compliance, SCIM, ML, recommendations). The harness specs should target the coreui-dashboard branch's state, NOT `main`. When in doubt, `git log origin/feat/coreui-dashboard` is the reference.
- `sigil-infra` — owns the Zitadel app definitions (Terraform) under `aws/terraform/modules/zitadel-kameas-apps/`

This doc tracks what each repo needs to ship before each harness fleet mission can implement against real contracts. It's the single place to look when a mission gets blocked on "we assumed an endpoint that doesn't exist".

## Fleet's actual identity surface (per `origin/feat/coreui-dashboard`)

Fleet's `service/auth.go` middleware extracts an `AuthContext` from every authenticated request:

```go
type AuthContext struct {
    UserID string  // Zitadel sub
    OrgID  int
    TeamID int
    Role   string  // "member" | "team_lead" | "org_admin"
    Tier   string  // "free" | "pro" | "team" | "enterprise"
}
```

Fleet looks up org/team/role/tier from its own DB keyed on `UserID` (Zitadel `sub` claim). **The `org_id` gap I initially flagged is solved on this branch** — no Zitadel custom claim needed.

The natural "first call after sign-in" endpoint is **`POST /api/v1/enroll`** — accepts `{node_id, platform, version}` and returns:

```json
{
  "org_id": 1,
  "team_id": 1,
  "org_name": "...",
  "team_name": "...",
  "role": "member",
  "org_settings": { ... }
}
```

**Decision**: the harness uses `POST /api/v1/enroll` as its identity endpoint (no separate `/api/v1/me` needed). On first sign-in, harness posts `{node_id, platform, version}`; thereafter calls it again to refresh identity. Fleet should add `tier` + `email` + `display_name` to the response body (the values already live in `AuthContext`, just need to surface them).

## Endpoints that exist on `origin/feat/coreui-dashboard` and are relevant to harness specs

| Endpoint | Harness mission(s) | Notes |
|---|---|---|
| `POST /api/v1/enroll` | auth-foundation | Replaces the spec's assumed `/api/v1/me`. Used for identity bootstrap + refresh. |
| `GET /api/v1/team/members` | context-sync (handoff) | Role-gated (`team_lead`+). Returns nodes in team. Suitable for handoff recipient picker. |
| `GET /api/v1/audit-log` | audit-archival (READ side) | Admin queries past audit. Not the harness's archive-write path — that's still missing. |
| `GET /api/v1/org/settings`, `PUT /api/v1/org/settings` | config-pull (partial) | Limited to notification floor, analysis freq, metric collection, rec scopes. Does NOT carry Cedar/MCP/model bundle — that endpoint is still missing. |
| `GET /api/v1/recommendations`, `POST /api/v1/recommendations/{id}/dismiss` | n/a | Orthogonal to harness fleet missions. |
| `GET /api/v1/compliance` | audit-archival, share-and-sync | Admin compliance report. Read-only, dashboard-oriented. |
| `GET /api/v1/org/scim-config`, `PUT /api/v1/org/scim-config` | n/a | SCIM provisioning for enterprise SSO. Orthogonal to harness. |
| `GET /api/v1/models/{model}` | n/a (kameas-ml is its own thing) | ML predictions endpoint; not a harness concern. |

---

## Per-mission coordination status

### v0.18.0 critical path

**`fleet-auth-foundation-01NDFSEX08`**

| Dependency | Owner | Status | Blocks |
|---|---|---|---|
| Zitadel `kameas-native` app (PKCE + loopback) | sigil-infra | ✅ EXISTS (`zitadel_application_oidc.kameas_native`, NATIVE app type, AUTH_CODE + REFRESH_TOKEN grants, PKCE-only no-secret) | nothing |
| `kameas-native` redirect URIs | sigil-infra | ✅ **RESOLVED**. TF default = `["http://127.0.0.1/callback", "http://[::1]/callback"]`. Live `zitadel-lle-apps` terragrunt does not override. Per RFC 8252 (and explicit in TF variable description), Zitadel wildcard-matches the loopback port at runtime, so harness can use any ephemeral port. | nothing |
| `org_id` from Fleet-side DB lookup | kenaz-fleet | ✅ **RESOLVED** on `origin/feat/coreui-dashboard`. `service/auth.go` populates `AuthContext{UserID, OrgID, TeamID, Role, Tier}` from DB keyed on Zitadel `sub`. No Zitadel custom claim needed. | nothing |
| JWKS endpoint (Zitadel discovery) | Zitadel SaaS | ✅ Standard `.well-known/openid-configuration` discovery available | nothing |
| Project roles in JWT | sigil-infra | ✅ `access_token_role_assertion = true`, `id_token_role_assertion = true` (lines 72-73 of TF module). Roles arrive under `urn:zitadel:iam:org:project:roles` claim. **Note**: harness uses Fleet roles (`member`/`team_lead`/`org_admin`) from `/api/v1/enroll` response, not Zitadel project roles. | nothing |
| Identity bootstrap endpoint | kenaz-fleet | ✅ **`POST /api/v1/enroll` exists** on `origin/feat/coreui-dashboard`. Returns `{org_id, team_id, org_name, team_name, role, org_settings}`. Harness uses this as identity endpoint. Fleet should add `tier`, `email`, `display_name` to response body (values already in `AuthContext`). | WP03 — small fleet-side response-body extension only |
| Fleet auth middleware extracts `org_id` | kenaz-fleet | ✅ **RESOLVED** on `origin/feat/coreui-dashboard`. See "Fleet's actual identity surface" above. | nothing |

**`org_id` resolution** (no longer an open question): Fleet-side DB lookup, already implemented in `service/auth.go` on `origin/feat/coreui-dashboard`. The harness reads its identity from `POST /api/v1/enroll`'s response body; no JWT claim-extraction work needed on the harness side.

**`fleet-capability-surface-01NDFSEX09`**

| Dependency | Owner | Status | Blocks |
|---|---|---|---|
| `GET /api/v1/me/capabilities` endpoint | kenaz-fleet | ❌ NOT IMPLEMENTED. Greenfield. Response shape per harness spec: `{tier: "pro"|"team"|"enterprise", capabilities: {<key>: bool}}` with ~25 keys mirroring `orgs-tiers-billing-design.md` §2 | WP01–WP06 (entire mission is blocked at runtime; specs can ship, harness logic can ship against a httptest fake) |
| Capability matrix freeze | product/billing | ⚠️ Defined in `orgs-tiers-billing-design.md` §2 but not yet committed to a JSON schema. Harness ships its own Go enum (parity-checked); fleet must respond with matching keys. | parity check (WP07) |

**`fleet-config-pull-01NDFSEX10`**

| Dependency | Owner | Status | Blocks |
|---|---|---|---|
| `GET /api/v1/configs?machine=<id>&checksum=<x>` endpoint | kenaz-fleet | ❌ NOT IMPLEMENTED | entire mission |
| `POST /api/v1/configs/<id>/ack` endpoint | kenaz-fleet | ❌ NOT IMPLEMENTED | WP05 (ACK) |
| ed25519 signing key + distribution strategy | kenaz-fleet + sigil-infra | ❌ NOT SPECIFIED. Harness assumes static-embed of public key in binary at release time, with rotation deferred. **Lock this before harness embeds anything.** | WP01 |
| Cedar bundle composition contract (team rule + local rule merge with `priority: team > user`) | kenaz-fleet + kenaz-harness | ⚠️ Harness `core/policy/cedar/` may not yet support a `SetTeamBundle` API. Verify before WP03. | WP03 |

### v0.19.0

**`fleet-otel-archival-01NDFSEX11`**

| Dependency | Owner | Status | Blocks |
|---|---|---|---|
| `POST /api/v1/telemetry/otel` endpoint | kenaz-fleet | ❌ NOT IMPLEMENTED. Must accept signed batched OTel payload. | entire mission |
| OTel storage backend (Clickhouse? PG? immudb?) | kenaz-fleet ops | ❌ NOT SPECIFIED | mission ship |

**`fleet-emergency-lockdown-01NDFSEX12`**

| Dependency | Owner | Status | Blocks |
|---|---|---|---|
| `GET /api/v1/lockdown/wait` long-poll endpoint | kenaz-fleet | ❌ NOT IMPLEMENTED | entire mission |
| `GET /api/v1/lockdown/status` sync endpoint | kenaz-fleet | ❌ NOT IMPLEMENTED | WP02 (restart-during-lockdown bootstrap) |
| Lockdown admin UX on Fleet dashboard | kenaz-fleet | ❌ NOT IMPLEMENTED | end-to-end manual test only |

### v0.20.0

**`fleet-audit-archival-01NDFSEX13`**

| Dependency | Owner | Status | Blocks |
|---|---|---|---|
| `POST /api/v1/audit/append` endpoint + immudb backing | kenaz-fleet | ❌ NOT IMPLEMENTED. immudb cluster also needs provisioning. | entire mission |

**`fleet-share-and-sync-01NDFSEX14`**

| Dependency | Owner | Status | Blocks |
|---|---|---|---|
| `POST /api/v1/catalog/publish` + `GET /api/v1/catalog/list` + `POST /api/v1/catalog/install` | kenaz-fleet | ❌ NOT IMPLEMENTED | Track A (catalog) |
| `PUT /api/v1/sync/<category>` + `GET /api/v1/sync/<category>?since=<ts>` | kenaz-fleet | ❌ NOT IMPLEMENTED | Track B (sync) |
| **`POST /api/v1/policy/publish` for Cedar bundles** | kenaz-fleet | ⚠️ **SHAPE MISMATCH.** Existing `PUT /api/v1/policy` is for routing policy (provider allowlist + routing mode), NOT Cedar OPA bundles. Either repurpose-and-rename (`PUT /api/v1/routing-policy` + new `POST /api/v1/cedar-policy/publish`) or add new endpoint alongside. **Lock the choice before WP07.** | Track C |
| `policy_admin` role on Identity | kenaz-fleet | ⚠️ Fleet's RBAC system has `org_owner`/`org_admin`/`org_member`/`org_billing`/`org_auditor` (per `signup-and-rbac-design.md`), separate from Zitadel project roles. Harness's `Identity.Roles` is sourced from Zitadel JWT. **Either**: extend Zitadel JWT to include Fleet RBAC roles, **OR** have `/api/v1/me` return Fleet RBAC roles in the response (Fleet looks them up by user_id). Lean: latter. | WP07 |

### v0.21.0

**`fleet-context-sync-01NDFSEX15`**

| Dependency | Owner | Status | Blocks |
|---|---|---|---|
| `POST /api/v1/context/append` + `GET /api/v1/context/replay?since=<seq>` | kenaz-fleet | ❌ NOT IMPLEMENTED | entire mission |
| `POST /api/v1/handoff/send` + `GET /api/v1/handoff/inbox` + `POST /api/v1/handoff/<id>/accept` | kenaz-fleet | ❌ NOT IMPLEMENTED | Team handoff feature |
| `GET /api/v1/team/members` (recipient picker source) | kenaz-fleet | ✅ **EXISTS** on `origin/feat/coreui-dashboard`. Role-gated `team_lead`+. Returns team's enrolled nodes. Harness uses this to populate the "Share with…" picker. | nothing |
| Recipient public-key lookup (KX via identity service) | kenaz-fleet | ❌ NOT IMPLEMENTED. Fleet needs a per-user public key associated with the user's Zitadel identity. | Team handoff |
| Storage capacity for ciphertext (session events at scale) | kenaz-fleet ops | ❌ NOT SPECIFIED. Per-session 100k event limit assumed in spec; cold storage strategy TBD. | mission ship |

---

## Cross-cutting open items

1. ~~Zitadel `org_id` claim resolution~~ — **RESOLVED** by Fleet-side DB lookup in `service/auth.go` on `origin/feat/coreui-dashboard`.
2. **Fleet signing key (ed25519) issuance + rotation strategy** — applies to config-pull bundles, audit ACKs, catalog signatures, telemetry batches. Single key family or per-purpose keys? Pick before any signed-payload endpoint ships.
3. ~~Fleet RBAC vs Zitadel project roles~~ — **RESOLVED**: harness uses Fleet roles (`member`/`team_lead`/`org_admin`) from `/api/v1/enroll` response, NOT Zitadel project roles. Zitadel JWT is just the authentication signal.
4. **Capability matrix wire-format freeze** — `orgs-tiers-billing-design.md` §2 has the conceptual matrix; needs a committed JSON schema. Until then, harness ships its enum and fleet must mirror. CI parity check (capability-surface WP07) catches drift only post-hoc.
5. **`/api/v1/policy` shape disambiguation** — existing endpoint serves routing-policy in kenaz-fleet `service/policy.go`. Harness's Cedar publish endpoint is therefore named `POST /api/v1/cedar-policy/publish` (separate path, separate table). Decided.
6. **Fleet-side `tier`/`email`/`display_name` exposure** — these values exist in `AuthContext` but aren't in the `/api/v1/enroll` response body. Small fleet-side change to surface them so the harness Identity panel can render them. Coordinate before harness AccountPanel ships in WP06.
7. **coreui-dashboard merge timing** — the harness specs target this branch's state. If it stalls in PR review on the fleet side, harness work that depends on its endpoints (e.g. `/api/v1/team/members` for context-sync) slips with it. Track via fleet PR status.

---

## Sequencing recommendation (revised after discovery)

Given the fleet repo is mostly greenfield, the practical critical path is:

1. **NOW**: Coordinate on the 5 cross-cutting items above. Mostly low-effort decisions.
2. **NEXT**: Fleet repo ships `/api/v1/me` + `/api/v1/me/capabilities` (the two v0.18.0 critical-path endpoints). Even a simple in-memory impl unblocks harness WP02–WP06.
3. **PARALLEL**: Harness can dispatch auth-foundation + capability-surface sub-agents now, coding against a `httptest` fake of the fleet API. End-to-end testing waits for the fleet endpoints, but unit + integration tests against fakes can ship.
4. **LATER**: v0.19.0+ missions wait for their corresponding fleet endpoints. Spec the work, dispatch when fleet catches up.

The harness side has more design surface area; the fleet side is mostly endpoint-implementation work mirroring the harness specs.
