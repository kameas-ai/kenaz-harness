/**
 * routing — first-paint route restoration via SettingsStore.LoadRoute.
 * FR-017, NFR-001, plan §4.1. Falls back to `/sessions` if absent.
 *
 * FR-004 (surface-error-boundary-recovery-01NBUG02):
 *   Route restoration is crash-safe. `restoreLastRoute` catches any
 *   navigation error (e.g. the target route component throws on mount)
 *   and falls back to /sessions. It also does NOT persist /sessions as
 *   the new lastRoute in that fallback path — only successful navigations
 *   are audited by `installRouteAuditing`.
 */

import type { Router } from 'vue-router';
import type { HarnessClient } from './harnessClient';

/** Safe fallback route used when the persisted route fails to restore. */
export const SAFE_FALLBACK_ROUTE = '/sessions';

export async function restoreLastRoute(
  router: Router,
  client: HarnessClient,
): Promise<void> {
  let route: string;
  try {
    route = await client.settings.loadRoute();
  } catch {
    // No persisted route or backend not ready — keep current.
    return;
  }

  if (!route || route === router.currentRoute.value.path) {
    return;
  }

  try {
    await router.replace(route);
  } catch {
    // The target route threw on navigation (e.g. component mount error).
    // Fall back to /sessions WITHOUT persisting the crashing route (FR-004).
    try {
      await router.replace(SAFE_FALLBACK_ROUTE);
    } catch {
      // Best-effort: if even /sessions fails, just stay wherever we are.
    }
  }
}

export function installRouteAuditing(
  router: Router,
  client: HarnessClient,
): void {
  router.afterEach((to, from) => {
    void client.settings.logRouteChange(from.path, to.path).catch(() => {});
    void client.settings.saveRoute(to.path).catch(() => {});
  });
}
