/**
 * useSearchPalette — reactive state for the floating search palette overlay.
 *
 * Provides a module-level singleton so the palette can be opened/closed from
 * any surface. Today exactly one production surface does: App.vue's
 * `menu:search:open` subscription, fed by the OS menu bar's View → Search
 * item (⌘F). The Titlebar icon and the Shell.vue ⌘K handler this comment
 * used to name are both gone — the former since v0.20.0's native-menu move,
 * the latter since engineer-truth-pass-01PMTP01 WP01.
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
