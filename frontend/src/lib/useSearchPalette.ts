/**
 * useSearchPalette — reactive state for the floating search palette overlay.
 *
 * Provides a module-level singleton so the palette can be opened/closed from
 * any surface (Titlebar icon, global ⌘K handler, keyboard shortcut in Shell).
 *
 * Future: when unified-search-01KX5R8C ships in v0.7.0, expand `open()` to
 * accept an initial query or entity-kind hint — the isOpen state shape here
 * remains stable.
 */
import { ref, type Ref } from 'vue';

// Module-level singleton so callers share state across the component tree.
const isOpen = ref(false);

interface UseSearchPaletteResult {
  isOpen: Ref<boolean>;
  open(): void;
  close(): void;
  toggle(): void;
}

export function useSearchPalette(): UseSearchPaletteResult {
  return {
    isOpen,
    open() {
      isOpen.value = true;
    },
    close() {
      isOpen.value = false;
    },
    toggle() {
      isOpen.value = !isOpen.value;
    },
  };
}
