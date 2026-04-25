/**
 * routing — first-paint route restoration via SettingsStore.LoadRoute.
 * FR-017, NFR-001, plan §4.1. Falls back to `/sessions` if absent.
 */

import type { Router } from 'vue-router';
import type { HarnessClient } from './harnessClient';

export async function restoreLastRoute(
  router: Router,
  client: HarnessClient,
): Promise<void> {
  try {
    const route = await client.settings.loadRoute();
    if (route && route !== router.currentRoute.value.path) {
      await router.replace(route);
    }
  } catch {
    // No persisted route or backend not ready — keep current.
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
