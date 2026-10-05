/**
 * featureFlags.ts — Fleet capability gating for the frontend.
 *
 * The OSS-first contract requires that every fleet-dependent UI affordance
 * uses `v-if="signedIn && capability('<key>')"`. This module provides:
 *
 *   - `capability(key)` — returns true only when the user is signed in AND
 *     the capability is explicitly enabled in the capability snapshot.
 *   - `signedIn` — computed ref that is true when `appInfo.capabilities`
 *     is populated (i.e. the user has an active fleet session).
 *
 * fleet-session-truth-01DOGF0A WP05: both helpers are now views over the
 * ONE fleet-session store (`lib/fleetSession.ts`) — the same store UserMenu,
 * the Account panel and ContextsView read, kept live by the backend's
 * `fleet:session-changed` push. Before this, `signedIn` here meant "the boot
 * AppInfo had a non-empty capability map" and was refreshed only from
 * AccountPanel, so a sign-in from the UserMenu left the rail gates stale
 * (dogfood 2026-10-04) and the rail could disagree with every other surface.
 * Boot AppInfo still SEEDS the store (initFeatureFlags) so gates open on the
 * first tick; the first real snapshot supersedes the seed.
 *
 * Historical note (pre-WP05): both helpers read from a module-level
 * `appInfo` ref. **The entry points own the write.** `bootFeatureFlags(client)` is called exactly once from
 * `main.ts` and once from `main-served.ts`; no component calls it. Until that
 * promise resolves the defaults (false for capability, false for signedIn)
 * apply, so every gate fails closed during boot and opens reactively when the
 * snapshot lands.
 *
 * Fleet state also changes *after* boot — sign-in, sign-out and an explicit
 * identity refresh all mutate it. `AccountPanel.vue` calls
 * `refreshFeatureFlags(client)` on each of those transitions; a boot-only
 * write would leave a user who signs in mid-session gated until restart.
 *
 * This docstring previously claimed components call `initFeatureFlags` from
 * `onMounted`. None ever did, and there was no other caller either — so every
 * gate in the app was permanently false from the day it shipped. See
 * `docs/dead-code-audit-2026-08-16.md` finding A4; the regression test that
 * pins the wiring is `src/__tests__/entrypoint.featureFlags.test.ts`, which
 * drives the real entry point rather than seeding this module by hand.
 *
 * (fleet-capability-surface-01NDFSEX09 WP12)
 */

import { computed, ref, type ComputedRef, type Ref } from 'vue';
import { isServedMode } from './useServedMode';
import {
  fleetSessionCapability,
  fleetSignedIn,
  seedFleetSessionFromAppInfo,
  useFleetSession,
  type FleetSessionSource,
} from './fleetSession';
import type { AppInfo } from './types';
import type { Capability } from './capability-keys';

// Re-exported so call sites that need to name the type (tests, helpers that
// take a key parameter) do not have to reach into the generated module.
export type { Capability };

// Module-level appInfo ref shared across all callers in the same Vue app.
// Starts null (not yet fetched) and is updated via initFeatureFlags().
const _appInfo: Ref<AppInfo | null> = ref(null);

/**
 * initFeatureFlags populates the internal appInfo ref with the result of
 * `client.appInfo()`. Prefer `bootFeatureFlags` / `refreshFeatureFlags`,
 * which own the fetch and the fail-closed error handling; this setter is
 * the seam they write through and the one tests seed directly.
 *
 * Calling it multiple times is safe; the last write wins.
 */
export function initFeatureFlags(info: AppInfo | null): void {
  _appInfo.value = info;
  // Seed the fleet-session store from the same capability map (never
  // overwrites a real backend snapshot — see seedFleetSessionFromAppInfo).
  seedFleetSessionFromAppInfo(info?.capabilities ?? null);
}

/**
 * FeatureFlagSource is the structural slice of the harness client this module
 * needs. Declared structurally rather than importing `HarnessClient` so that
 * `featureFlags.ts` stays free of a dependency on `harnessClient.ts` (which
 * imports half the app's wire types) and so tests can pass a two-method stub.
 */
export interface FeatureFlagSource {
  appInfo(): Promise<AppInfo>;
  settings: {
    /** Forces an immediate capability fetch from fleet, updating the poller. */
    fleetRefreshCapabilities(): Promise<unknown>;
  } & Partial<FleetSessionSource['settings']>;
}

/**
 * startFleetSession hands the client to the fleet-session store (initial
 * Settings_FleetSession read + the single fleet:session-changed
 * subscription). Clients without the session RPCs (narrow test stubs) are
 * skipped; the AppInfo seed then stands alone.
 */
async function startFleetSession(client: FeatureFlagSource): Promise<void> {
  const s = client.settings;
  if (!s || typeof s.fleetSession !== 'function') return;
  const { refresh } = useFleetSession(); // subscribe; the read is below
  await refresh(client as unknown as FleetSessionSource);
}

/**
 * bootFeatureFlags fetches AppInfo and installs it as the capability snapshot.
 *
 * Called once per entry point (`main.ts`, `main-served.ts`) — it is the ONLY
 * thing standing between the capability gates and a permanent `false`. It
 * returns the AppInfo so the caller can reuse it (main.ts feeds the same
 * object to Sentry's release/gitsha) rather than issuing a second RPC.
 *
 * Never throws: a failed fetch leaves the snapshot null, i.e. every gate
 * closed, which is the safe direction for a fleet entitlement check.
 */
export async function bootFeatureFlags(
  client: FeatureFlagSource,
): Promise<AppInfo | null> {
  let info: AppInfo | null = null;
  try {
    info = await client.appInfo();
    initFeatureFlags(info);
  } catch {
    // Fail closed. A transport error must not grant capabilities.
    initFeatureFlags(null);
  }
  // Then the real snapshot (never throws).
  await startFleetSession(client);
  return info;
}

/**
 * refreshFeatureFlags re-reads the capability snapshot after a fleet session
 * transition (sign-in, sign-out, manual identity refresh).
 *
 * Two calls, in this order, on purpose:
 *
 *  1. `fleetRefreshCapabilities()` — the capability poller's cached snapshot
 *     is what `AppInfo` reads (`core/rpc/api.go` → `FleetCapabilities` →
 *     `poller.Current()`), and immediately after sign-in that cache still
 *     holds the signed-out answer. `Refresh` forces a fetch and publishes the
 *     result into `Current()` (`core/fleet/capability_poller.go`), so this is
 *     the correct refresh path — not a redundant second door.
 *  2. `bootFeatureFlags` — re-reads AppInfo, which is also where the
 *     `source == "default-deny"` → *no capabilities* mapping lives. That
 *     mapping is why sign-out works: `FleetSignOut` stops the poller, so
 *     AppInfo comes back with no capability map and every gate closes.
 *
 * Step 1 is allowed to fail. On sign-out it always does — the poller has been
 * torn down, so the RPC returns `ErrFleetDisabled`. Step 2 is what actually
 * settles the state either way.
 */
export async function refreshFeatureFlags(
  client: FeatureFlagSource,
): Promise<void> {
  try {
    await client.settings.fleetRefreshCapabilities();
  } catch {
    // Expected on sign-out (poller stopped) and when fleet is disabled.
    // AppInfo below is authoritative regardless.
  }
  await bootFeatureFlags(client);
}

/**
 * signedIn is a computed boolean ref that is true when the fleet-session
 * store says the session's tokens are usable (signed_in or degraded).
 *
 * Usage in templates: `v-if="signedIn"`
 */
export const signedIn: ComputedRef<boolean> = computed(() => {
  // Served mode has no fleet surface AT ALL. `AppInfo` is in the served
  // allowlist and answers with the desktop process's real capability map, so
  // a browser client of a signed-in harness would otherwise read `signedIn`
  // as true — and every gate below it would open onto an RPC that served mode
  // refuses. The allowlist in core/serve/methods.go carries no Sites_*,
  // Catalog_*, Sync_* or Slashcmd_SkillPublish, so there is no fleet
  // affordance that could work here and no gate that should open. (This
  // comment used to hardcode the allowlist's size — "33 methods" — which
  // went stale the first time a method was ported; the SIZE isn't the
  // invariant that matters, the ABSENCE of any fleet method is, and that's
  // asserted by core/serve/wp08_served_count_test.go rather than restated
  // here in a number nothing keeps in sync. See docs/served-mode-boundary.md
  // for the count as of the last release that touched it.)
  //
  // Closed HERE rather than at each call site because the call sites do not
  // agree on how they are protected, and one of them is not. Sites and
  // Marketplace are unrouted in served mode AND carry a `!served` rail guard;
  // WorkflowsView, SettingsView (which owns SyncPanel and SlashCommandsView)
  // render `NotAvailableInServedMode` over their whole template. BundlesView
  // does neither: `/bundles` is a served route, it has no boundary panel, and
  // its "Publish to team" button gates on `signedIn` alone — so wiring the
  // capability snapshot made it render in a browser, against a
  // `Catalog_Publish` that served mode refuses. One fence in the helper
  // covers that and every gate added later. See docs/served-mode-boundary.md.
  if (isServedMode()) return false;
  // The fleet-session store's answer: signed_in OR degraded (FR-3).
  return fleetSignedIn.value;
});

/**
 * capability(key) returns true when:
 *   1. The user is signed in (signedIn === true), AND
 *   2. The capability key is explicitly set to `true` in the snapshot.
 *
 * Returns false for missing keys, empty maps, signed-out state, or when
 * fleet is disabled. Never throws.
 *
 * `key` is the generated `Capability` union (capability-keys.ts, emitted
 * from `fleet.AllCapabilities()` and CI-gated by check-codegen.sh), not
 * `string`. A key that is not a real wire value is a compile error rather
 * than a gate that silently answers false forever — which is how
 * SyncPanel shipped gating on the nonexistent `settings_sync`.
 *
 * Usage in templates: `v-if="signedIn && capability('launcher_updates')"`
 */
export function capability(key: Capability): boolean {
  // Same served-mode fence as `signedIn`. Repeated rather than delegated so
  // that neither helper can be made safe by accident while the other is not.
  if (isServedMode()) return false;
  // The store's capability set — the same one ContextsView's team gate
  // reads, so a capability arriving re-renders every gate in one tick (FR-8).
  return fleetSessionCapability(key);
}

/**
 * useFeatureFlags returns a reactive bundle of the capability helpers.
 * Prefer importing `signedIn` and `capability` directly when possible;
 * use this composable when you need the full bundle or want to inject it.
 */
export function useFeatureFlags(): {
  signedIn: ComputedRef<boolean>;
  capability: (key: Capability) => boolean;
  appInfo: Ref<AppInfo | null>;
} {
  return { signedIn, capability, appInfo: _appInfo };
}
