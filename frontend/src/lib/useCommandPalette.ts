/**
 * useCommandPalette — Cmd/Ctrl+K command palette state + global key
 * binding (FR-009 / WP09). Keeps a registry of actions that surfaces can extend.
 *
 * WP09: Built out with full rail navigation, settings destinations,
 * "New Session" (dispatches a custom event to open the dialog), and
 * theme toggle that routes through useTheme for persistence.
 */

import { ref, onMounted, onBeforeUnmount, type Ref } from 'vue';
import { signedIn, capability } from '@/lib/featureFlags';
import { isServedMode } from '@/lib/useServedMode';
import { resolveBinding } from '@/lib/shortcuts/registry';
import { matchesEvent } from '@/lib/shortcuts/platform';

export interface PaletteAction {
  id: string;
  label: string;
  hint?: string;
  /**
   * Optional gate. When present, CommandPalette.vue hides the action — and
   * refuses to run it — unless this returns true. Evaluated on every render,
   * so it may read reactive state (e.g. the fleet capability snapshot).
   *
   * Most actions omit it: their destinations are always available. It exists
   * for the fleet surfaces, whose rail entries are capability-gated; a palette
   * entry that ignored the gate would be a second, ungated door to a screen
   * the user is not entitled to.
   */
  visible?(): boolean;
  perform(): void | Promise<void>;
}

// ── Navigation destinations ───────────────────────────────────────────────

function navigate(hash: string) {
  if (typeof window !== 'undefined') window.location.hash = hash;
}

const NAV_ACTIONS: PaletteAction[] = [
  { id: 'nav.sessions', label: 'Go to Sessions', hint: 'Main session list', perform: () => navigate('#/sessions') },
  { id: 'nav.tools', label: 'Go to Tools', hint: 'MCP servers & tool bundles', perform: () => navigate('#/tools') },
  { id: 'nav.providers', label: 'Go to Providers', hint: 'AI provider configuration', perform: () => navigate('#/providers') },
  { id: 'nav.contexts', label: 'Go to Contexts', hint: 'Session context files', perform: () => navigate('#/contexts') },
  { id: 'nav.memory', label: 'Go to Memory', hint: 'Memory capture settings', perform: () => navigate('#/memory') },
  { id: 'nav.workflows', label: 'Go to Workflows', hint: 'Scheduled workflows', perform: () => navigate('#/workflows') },
  { id: 'nav.artifacts', label: 'Go to Artifacts', hint: 'Saved artifacts & outputs', perform: () => navigate('#/artifacts') },
  // agentgraph-total-convergence-01PMGX01 WP16: back in the left rail as well
  // (see LeftRail.vue). The palette entry stays — a rail entry and a Cmd+K
  // action are not redundant, they serve different reach.
  { id: 'nav.agentgraph', label: 'Go to Agent graphs', hint: 'Graphs the kernel runs, and the materialized graph of each conversation', perform: () => navigate('#/agentgraph') },
  { id: 'nav.audit', label: 'Go to Audit log', hint: 'Session & tool audit trail', perform: () => navigate('#/audit') },
  { id: 'nav.permissions', label: 'Go to Permissions', hint: 'Tool & bash permissions', perform: () => navigate('#/permissions') },
  { id: 'nav.policy', label: 'Go to Security policy', hint: 'Advanced security policy editor', perform: () => navigate('#/policy') },
  // Fleet surfaces. Both routes exist only in the desktop bundle
  // (docs/served-mode-boundary.md) and both are entitlement-gated, so these
  // carry the same predicate as their LeftRail entries. Before
  // docs/dead-code-audit-2026-08-16.md finding A4 was fixed the gate was
  // permanently false and these two views had no reachable entry point at all
  // — no rail item, no palette action, no router.push anywhere in the tree.
  {
    id: 'nav.sites',
    label: 'Go to Sites',
    hint: 'Fleet-hosted static sites',
    visible: () => !isServedMode() && signedIn.value && capability('sites_hosting'),
    perform: () => navigate('#/sites'),
  },
  {
    id: 'nav.marketplace',
    label: 'Go to Marketplace',
    hint: 'Team catalog of shared bundles, workflows & skills',
    visible: () => !isServedMode() && signedIn.value,
    perform: () => navigate('#/marketplace'),
  },
];

const SETTINGS_ACTIONS: PaletteAction[] = [
  { id: 'settings.root', label: 'Open Settings', hint: 'General settings', perform: () => navigate('#/settings') },
  { id: 'settings.providers', label: 'Settings: Providers', perform: () => navigate('#/settings?tab=providers') },
  { id: 'settings.autonomy', label: 'Settings: Autonomy', hint: 'Agent autonomy tiers', perform: () => navigate('#/settings?tab=autonomy') },
  { id: 'settings.hooks', label: 'Settings: Hooks', hint: 'Event hooks & scripts', perform: () => navigate('#/settings?tab=hooks') },
  { id: 'settings.bash', label: 'Settings: Bash permissions', perform: () => navigate('#/permissions/bash') },
  { id: 'settings.updates', label: 'Settings: Updates', perform: () => navigate('#/settings?tab=updates') },
];

const open = ref(false);
const actions = ref<PaletteAction[]>([
  // ── Session actions ──────────────────────────────────────────────────
  {
    id: 'session.new',
    label: 'New Session',
    hint: 'Open the new-session dialog',
    perform: () => {
      // Dispatch a custom event that Shell / App.vue listens to, to open
      // the NewSessionDialog. This avoids direct prop threading through
      // every command palette caller.
      if (typeof window !== 'undefined') {
        window.dispatchEvent(new CustomEvent('kenaz:open-new-session'));
      }
    },
  },
  // ── Theme toggle — registered from CommandPalette.vue via useTheme (WP09 review fix) ─
  // (action registered dynamically in CommandPalette.vue so it has Vue context for useTheme)
  // ── Navigation ──────────────────────────────────────────────────────
  ...NAV_ACTIONS,
  // ── Settings ────────────────────────────────────────────────────────
  ...SETTINGS_ACTIONS,
]);

interface UseCommandPaletteResult {
  isOpen: Ref<boolean>;
  actions: Ref<PaletteAction[]>;
  open(): void;
  close(): void;
  toggle(): void;
  register(action: PaletteAction): () => void;
}

let installed = false;

// controls-and-readouts-that-tell-the-truth-01PMZ808 WP09 (FR-011, AC-022):
// onKey is module-level (installed once outside any component instance),
// so a Shell.vue setup-local shortcutOverrides ref is unreachable from
// here, and there is no settings store/composable anywhere else in the
// frontend to read from directly. Shell.vue pushes the resolved
// overrides into this module-level holder via setCommandPaletteOverrides
// once settings load; onKey resolves through the registry against it
// instead of a hard-coded 'k' literal.
let overrides: Record<string, string> = {};

/** Called by Shell.vue once client.settings.get() resolves. */
export function setCommandPaletteOverrides(next: Record<string, string>): void {
  overrides = next;
}

function onKey(e: KeyboardEvent) {
  const binding = resolveBinding('nav.command-palette', overrides);
  const isPaletteShortcut = !!binding && matchesEvent(binding, e);
  if (isPaletteShortcut) {
    e.preventDefault();
    open.value = !open.value;
  } else if (e.key === 'Escape' && open.value) {
    open.value = false;
  }
}

export function useCommandPalette(): UseCommandPaletteResult {
  onMounted(() => {
    if (installed || typeof window === 'undefined') return;
    window.addEventListener('keydown', onKey);
    installed = true;
  });

  onBeforeUnmount(() => {
    if (typeof window === 'undefined' || !installed) return;
    window.removeEventListener('keydown', onKey);
    installed = false;
  });

  function register(action: PaletteAction): () => void {
    actions.value = [...actions.value, action];
    return () => {
      actions.value = actions.value.filter((a) => a.id !== action.id);
    };
  }

  return {
    isOpen: open,
    actions,
    open: () => {
      open.value = true;
    },
    close: () => {
      open.value = false;
    },
    toggle: () => {
      open.value = !open.value;
    },
    register,
  };
}
