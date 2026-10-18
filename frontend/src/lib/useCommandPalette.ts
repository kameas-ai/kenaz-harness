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
  // nav-ia-sweep-01DOGF0F WP03: relabelled, not removed — the palette has
  // "New Session" but no "search sessions" action, so this is still the
  // keyboard way back to the session surface. The old hint promised a "Main
  // session list"; /sessions with no id is an empty state, the list is the rail.
  {
    id: 'nav.sessions',
    label: 'Go to Sessions home',
    hint: 'Pick a session in the rail or start a new one',
    perform: () => navigate('#/sessions'),
  },
  // install-framework-01DOGF0B Phase 4 WP09: Tools + Marketplace are one
  // Capabilities surface (route /tools). The id stays nav.tools so palette
  // history keeps resolving; nav.marketplace is gone (/marketplace redirects).
  {
    id: 'nav.tools',
    label: 'Go to Capabilities',
    hint: 'Add capability — tools, MCP servers, skills, workflows & the fleet catalog',
    perform: () => navigate('#/tools'),
  },
  { id: 'nav.providers', label: 'Go to Providers', hint: 'AI provider configuration', perform: () => navigate('#/providers') },
  // knowledge-home-01DOGF0E WP02: Contexts + Memory → one Knowledge home.
  // The old nav.memory hint promised "Memory capture settings"; the view never
  // showed the setting (it lived under Tools).
  { id: 'nav.knowledge', label: 'Go to Knowledge', hint: 'Curated context files & learned memory', perform: () => navigate('#/knowledge/curated') },
  { id: 'nav.knowledge.curated', label: 'Go to Knowledge › Curated', hint: 'Context files you write and attach to conversations', perform: () => navigate('#/knowledge/curated') },
  { id: 'nav.knowledge.learned', label: 'Go to Knowledge › Learned', hint: 'Memory captured from conversations — on/off switch and saved chunks', perform: () => navigate('#/knowledge/learned') },
  { id: 'nav.workflows', label: 'Go to Workflows', hint: 'Scheduled workflows', perform: () => navigate('#/workflows') },
  // artifacts-as-units-01DOGF0C WP06: Artifacts + Documents → one Library.
  { id: 'nav.library', label: 'Go to Library', hint: 'Captured artifacts & authored documents', perform: () => navigate('#/library/captured') },
  // agentgraph-total-convergence-01PMGX01 WP16 put Agent graphs back in the
  // left rail; agentgraph-settings-linkage-01DOGF0D WP05 moved the library
  // under Settings › Authoring (the rail entry is gone — each chat turn links
  // to its own run graph instead). The palette entry stays, routing to the
  // same /agentgraph library, now framed by the Settings hub.
  // served-mode-is-a-real-mode-01PMZ707 WP03: gated on !isServedMode() —
  // both routes render NotAvailableInServedMode in a served build (D-701;
  // Graph_*/CedarPolicy_*/Policy_* have no serve dispatch case), mirroring
  // the nav.sites predicate below.
  {
    id: 'nav.agentgraph',
    label: 'Go to Agent graphs',
    hint: 'Settings › Authoring — the graph library and editor',
    visible: () => !isServedMode(),
    perform: () => navigate('#/agentgraph'),
  },
  // served-mode-is-a-real-mode-01PMZ707 WP05: gated on !isServedMode() —
  // all eleven Audit_* RPCs are unrouted in served mode, so /audit now
  // renders NotAvailableInServedMode wholesale (unlike nav.permissions
  // below, whose view has a genuinely working half — D-710).
  {
    id: 'nav.audit',
    label: 'Go to Audit log',
    hint: 'Session & tool audit trail',
    visible: () => !isServedMode(),
    perform: () => navigate('#/audit'),
  },
  { id: 'nav.permissions', label: 'Go to Permissions', hint: 'Tool & bash permissions', perform: () => navigate('#/permissions') },
  {
    id: 'nav.policy',
    label: 'Go to Security policy',
    hint: 'Advanced security policy editor',
    visible: () => !isServedMode(),
    perform: () => navigate('#/policy'),
  },
  // Fleet surface. The route exists only in the desktop bundle
  // (docs/served-mode-boundary.md) and is entitlement-gated, so this carries
  // the same predicate as its LeftRail entry. Before
  // docs/dead-code-audit-2026-08-16.md finding A4 was fixed the gate was
  // permanently false and the view had no reachable entry point at all — no
  // rail item, no palette action, no router.push anywhere in the tree. (The
  // Marketplace, its sibling here, folded into Capabilities —
  // install-framework-01DOGF0B Phase 4.)
  {
    id: 'nav.sites',
    label: 'Go to Sites',
    hint: 'Fleet-hosted static sites',
    visible: () => !isServedMode() && signedIn.value && capability('sites_hosting'),
    perform: () => navigate('#/sites'),
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
