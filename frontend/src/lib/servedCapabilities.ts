/**
 * servedCapabilities — the ONE map of what the in-workbench (served) build
 * can and cannot do, for surfaces the user reaches from navigation or a
 * session's chrome (audit findings HR-4, HR-5, HR-10, 2026-10-21).
 *
 * Before this map each surface decided on its own, and most decided by
 * rendering a NotAvailableInServedMode panel only AFTER the user clicked
 * in: 6 of 8 nav entries were dead ends, and session-header actions like
 * "Sync to fleet" or "Export…" were shown with no hint they do nothing
 * here. The nav and the session chrome now ask this map first, so an
 * unavailable surface is marked (or withheld) where the user meets it.
 *
 * Source of truth for "available": the served RPC allowlist in
 * core/serve/methods.go. An entry is `null` when the surface's RPCs are
 * routed there, and a reason string when they are not. Keep the reasons
 * actionable from inside a workbench (see NotAvailableInServedMode's copy
 * rule) — "use the desktop app" is not advice to someone who has none.
 *
 * In the desktop build every surface is available: servedUnavailable()
 * returns null without consulting the map.
 */
import { isServedMode } from './useServedMode';

export type ServedSurface =
  // Left-rail navigation
  | 'nav.capabilities'
  | 'nav.workflows'
  | 'nav.knowledge'
  | 'nav.library'
  | 'nav.settings'
  // Settings › Authoring
  | 'graphs'
  // Session chrome
  | 'session.syncToFleet'
  | 'session.share'
  | 'session.purgeRemote'
  | 'session.export'
  | 'session.move'
  | 'session.suggestTitle';

const DESKTOP_ONLY_HINT = 'Configure it in Kenaz on the host.';

/** null = served; string = why it is not (shown as the tooltip). */
export const SERVED_SURFACES: Readonly<Record<ServedSurface, string | null>> = {
  'nav.capabilities':
    `Installing and toggling capabilities is not available in the workbench. Tools you already have still run. ${DESKTOP_ONLY_HINT}`,
  'nav.workflows': 'Workflow definitions and runs are not available in the workbench.',
  'nav.knowledge': 'Contexts and memory are not available in the workbench.',
  // Library › Authored (Documents_*) is served; Captured (Artifacts_*) is
  // not. The entry stays live and lands on Authored — see servedLibraryPath.
  'nav.library': null,
  // Fleet telemetry consent and Agent Profiles are served; the rest of
  // Settings says so on the page.
  'nav.settings': null,
  graphs: 'Agent graph authoring and runs are not available in the workbench.',
  'session.syncToFleet': 'Fleet sync is not available in the workbench.',
  'session.share': 'Sharing is not available in the workbench.',
  'session.purgeRemote': 'Fleet sync is not available in the workbench.',
  'session.export': 'Export is not available in the workbench.',
  // Sessions_MoveToProject and Sessions_SuggestTitle are in methods.go.
  'session.move': null,
  'session.suggestTitle': null,
};

/**
 * servedUnavailable returns the reason `surface` is unavailable in this
 * build, or null when it works here. Always null on desktop.
 */
export function servedUnavailable(surface: ServedSurface): string | null {
  if (!isServedMode()) return null;
  return SERVED_SURFACES[surface];
}

/** The short label an unavailable entry carries. */
export const DESKTOP_ONLY_LABEL = 'Desktop only';

/**
 * Where the Library rail entry lands. In served mode Captured is a dead
 * end, so the entry opens Authored, which works.
 */
export function servedLibraryPath(): string {
  return isServedMode() ? '/library/authored' : '/library/captured';
}
