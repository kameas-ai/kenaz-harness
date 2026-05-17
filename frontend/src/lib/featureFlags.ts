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
 * Both helpers read from a module-level `appInfo` ref that is populated by
 * `initFeatureFlags(client)` at app boot. Components that need capability
 * gating call `initFeatureFlags` from `onMounted` to ensure the snapshot
 * is available; for components that render before any mount, the defaults
 * (false for capability, false for signedIn) are safe.
 *
 * (fleet-capability-surface-01NDFSEX09 WP12)
 */

import { computed, ref, type ComputedRef, type Ref } from 'vue';
import type { AppInfo } from './types';

// Module-level appInfo ref shared across all callers in the same Vue app.
// Starts null (not yet fetched) and is updated via initFeatureFlags().
const _appInfo: Ref<AppInfo | null> = ref(null);

/**
 * initFeatureFlags populates the internal appInfo ref. Call this from
 * your root component's `onMounted` (or any early component) with the
 * result of `client.appInfo()`.
 *
 * Calling it multiple times is safe; the last write wins.
 */
export function initFeatureFlags(info: AppInfo | null): void {
  _appInfo.value = info;
}

/**
 * signedIn is a computed boolean ref that is true when the AppInfo
 * capabilities map is non-empty — which the backend only populates when the
 * user is signed in to fleet.
 *
 * Usage in templates: `v-if="signedIn"`
 */
export const signedIn: ComputedRef<boolean> = computed(() => {
  const caps = _appInfo.value?.capabilities;
  if (!caps) return false;
  // The map is non-empty → we have a fleet session.
  return Object.keys(caps).length > 0;
});

/**
 * capability(key) returns true when:
 *   1. The user is signed in (signedIn === true), AND
 *   2. The capability key is explicitly set to `true` in the snapshot.
 *
 * Returns false for missing keys, empty maps, signed-out state, or when
 * fleet is disabled. Never throws.
 *
 * Usage in templates: `v-if="signedIn && capability('hosted_inference')"`
 */
export function capability(key: string): boolean {
  return _appInfo.value?.capabilities?.[key] === true;
}

/**
 * useFeatureFlags returns a reactive bundle of the capability helpers.
 * Prefer importing `signedIn` and `capability` directly when possible;
 * use this composable when you need the full bundle or want to inject it.
 */
export function useFeatureFlags(): {
  signedIn: ComputedRef<boolean>;
  capability: (key: string) => boolean;
  appInfo: Ref<AppInfo | null>;
} {
  return { signedIn, capability, appInfo: _appInfo };
}
