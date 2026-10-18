/**
 * railMatch — active-state matching for the left rail's surface entries.
 *
 * nav-ia-sweep-01DOGF0F WP02 (FR-2). RailEntry used to light only on an
 * exact `route.path === to`, so every nested route (a chat at
 * `/sessions/abc`, a run at `/agentgraph/run/x/graph`, a permissions family
 * at `/permissions/fs`) left the rail with no active entry at all.
 *
 * Segment-bounded so `/memory` does not light on a hypothetical
 * `/memoryX` route: a prefix matches the path itself or the path followed
 * by `/`.
 */
export function railPathMatches(
  path: string,
  prefix: string | readonly string[],
): boolean {
  const prefixes = typeof prefix === 'string' ? [prefix] : prefix;
  return prefixes.some((p) => path === p || path.startsWith(`${p}/`));
}

/**
 * Every route the Settings hub renders (SettingsShell + SettingsTabs):
 * /settings itself plus the separately-routed panels SettingsTabs links to.
 * The rail's Settings entry lights on all of them.
 */
export const SETTINGS_HUB_PREFIXES: readonly string[] = [
  '/settings',
  '/providers',
  '/bundles',
  '/permissions',
  '/policy',
  '/audit',
  // agentgraph-settings-linkage-01DOGF0D WP05: Settings › Authoring › Agent
  // graphs is the separately-routed /agentgraph library. The editor
  // (/agentgraph/edit/:id) and run views (/agentgraph/run/…) carry no rail
  // entry of their own any more, so they light Settings too.
  '/agentgraph',
];
