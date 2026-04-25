/**
 * useCommandPalette — Cmd/Ctrl+K command palette state + global key
 * binding. FR-010. Keeps a registry of actions surfaces can extend.
 */

import { ref, onMounted, onBeforeUnmount, type Ref } from 'vue';

export interface PaletteAction {
  id: string;
  label: string;
  hint?: string;
  perform(): void | Promise<void>;
}

const open = ref(false);
const actions = ref<PaletteAction[]>([
  {
    id: 'palette.open-settings',
    label: 'Open Settings',
    perform: () => {
      if (typeof window !== 'undefined') window.location.hash = '#/settings';
    },
  },
  {
    id: 'palette.new-session',
    label: 'New Session',
    perform: () => {
      if (typeof window !== 'undefined') window.location.hash = '#/sessions';
    },
  },
  {
    id: 'palette.toggle-theme',
    label: 'Toggle theme',
    perform: () => {
      if (typeof document === 'undefined') return;
      document.documentElement.classList.toggle('dark');
    },
  },
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
