/**
 * aboutDialogStore — module-level singleton that controls the About dialog.
 *
 * Consumed by:
 *   - App.vue: subscribes to the menu:about:open broker event and calls open()
 *   - AboutDialog.vue: reads isOpen + calls close()
 *
 * Module-scoped so the broker event in App.vue and the dialog in the component
 * tree share the same ref without prop-drilling or Pinia.
 */
import { ref, type Ref } from 'vue';

const isOpen: Ref<boolean> = ref(false);

export interface AboutDialogStore {
  isOpen: Ref<boolean>;
  open(): void;
  close(): void;
}

export function useAboutDialogStore(): AboutDialogStore {
  return {
    isOpen,
    open() { isOpen.value = true; },
    close() { isOpen.value = false; },
  };
}
