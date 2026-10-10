/**
 * legacyRoutes — redirects for Settings tabs that moved to other surfaces
 * or were retired.
 *
 * nav-ia-sweep-01DOGF0F WP05 (FR-4). Settings › Runtime (Scheduled Chats,
 * Tasks) and Settings › Authoring › Workflows moved into the Workflows
 * surface. Their old URLs stay valid: bookmarks, a persisted lastRoute from a
 * previous release, and any deep link this sweep missed all land on the new
 * home instead of falling through to Settings › General.
 *
 * Installed as `beforeEnter` on the `/settings` record in BOTH main.ts and
 * main-served.ts (entrypoint.routes.test.ts pins the parity).
 */
import type { RouteLocationNormalized, RouteLocationRaw } from 'vue-router';

/** Old `/settings?tab=<key>` → its new location. */
const LEGACY_SETTINGS_TAB_REDIRECTS: Readonly<Record<string, RouteLocationRaw>> = {
  scheduledchats: { path: '/workflows', query: { tab: 'schedules' } },
  tasks: { path: '/workflows', query: { tab: 'tasks' } },
  // Settings › Workflows was the workflow list + CRUD (now Library, the
  // default tab) plus the cron editor (now Schedules). The list is what the
  // panel opened on, so the bookmark lands on Library.
  workflows: { path: '/workflows' },
  // settings-cleanup-01SETUX01 WP01 (FR-2): Flags, Health and Logs were
  // developer surfaces, deleted by owner ruling. Their old URLs — bookmarks,
  // a persisted lastRoute, and the pre-WP01 drift toast's "Review" action —
  // land on Settings › General, where SettingsIssuesBanner shows any
  // database problem Health used to list.
  flags: { path: '/settings' },
  health: { path: '/settings' },
  logs: { path: '/settings' },
};

export function redirectLegacySettingsTab(
  to: RouteLocationNormalized,
): RouteLocationRaw | true {
  const raw = to.query.tab;
  const tab = Array.isArray(raw) ? raw[0] : raw;
  if (typeof tab === 'string' && Object.prototype.hasOwnProperty.call(LEGACY_SETTINGS_TAB_REDIRECTS, tab)) {
    return LEGACY_SETTINGS_TAB_REDIRECTS[tab];
  }
  return true;
}
