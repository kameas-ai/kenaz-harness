/**
 * useCommandPalette — Cmd/Ctrl+K command palette state + global key
 * binding (FR-009 / WP09). Keeps a registry of actions that surfaces can extend.
 *
 * WP09: Built out with full rail navigation, settings destinations,
 * "New Session" (dispatches a custom event to open the dialog), and
 * theme toggle that routes through useTheme for persistence.
 */

import { ref, onMounted, onBeforeUnmount, type Ref } from 'vue';

export interface PaletteAction {
  id: string;
  label: string;
  hint?: string;
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
  { id: 'nav.corpora', label: 'Go to Corpora', hint: 'Corpora knowledge stores', perform: () => navigate('#/corpora') },
  { id: 'nav.memory', label: 'Go to Memory', hint: 'Memory capture settings', perform: () => navigate('#/memory') },
  { id: 'nav.workflows', label: 'Go to Workflows', hint: 'Scheduled workflows', perform: () => navigate('#/workflows') },
  { id: 'nav.artifacts', label: 'Go to Artifacts', hint: 'Saved artifacts & outputs', perform: () => navigate('#/artifacts') },
  { id: 'nav.agentgraph', label: 'Go to Agent graphs', hint: 'Visual agent graph editor', perform: () => navigate('#/agentgraph') },
  { id: 'nav.audit', label: 'Go to Audit log', hint: 'Session & tool audit trail', perform: () => navigate('#/audit') },
  { id: 'nav.permissions', label: 'Go to Permissions', hint: 'Tool & bash permissions', perform: () => navigate('#/permissions') },
  { id: 'nav.policy', label: 'Go to Security policy', hint: 'Advanced security policy editor', perform: () => navigate('#/policy') },
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
  // ── Theme toggle — routes through document class + persists via hook ─
  {
    id: 'palette.toggle-theme',
    label: 'Toggle theme',
    hint: 'Switch between dark and light mode',
    perform: () => {
      if (typeof document === 'undefined') return;
      const isDark = document.documentElement.classList.contains('dark');
      document.documentElement.classList.toggle('dark', !isDark);
      document.documentElement.style.colorScheme = isDark ? 'light' : 'dark';
      // Dispatch a custom event so useTheme watchers can persist the change.
      window.dispatchEvent(
        new CustomEvent('kenaz:theme-toggle', { detail: { dark: !isDark } }),
      );
    },
  },
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

function onKey(e: KeyboardEvent) {
  const isPaletteShortcut =
    (e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k';
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
