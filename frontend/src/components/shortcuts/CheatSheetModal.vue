<script setup lang="ts">
/**
 * CheatSheetModal — full-page overlay listing every registered shortcut
 * grouped by category (keyboard-shortcuts-settings-01KQ8TDR plan §2.6).
 *
 * Opened by the `help.cheat-sheet` shortcut (`?` when not in an input).
 * Each row is clickable → navigates to /settings with the Keyboard
 * Shortcuts section in view.
 *
 * Mount in Shell.vue alongside other modals.
 */
import { computed } from 'vue';
import { useRouter } from 'vue-router';
import { SHORTCUTS, groupByCategory, resolveBinding } from '@/lib/shortcuts/registry';
import KeycapDisplay from '@/components/shortcuts/KeycapDisplay.vue';

const props = defineProps<{
  open: boolean;
  /** User overrides map from settings. */
  overrides?: Record<string, string>;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
}>();

const router = useRouter() as ReturnType<typeof useRouter> | undefined;

const groups = computed(() => {
  return groupByCategory(SHORTCUTS);
});

function close() {
  emit('close');
}

function onBackdropClick(e: MouseEvent) {
  if (e.target === e.currentTarget) close();
}

function goToSettings(shortcutId: string) {
  close();
  void router?.push(`/settings#shortcut-${shortcutId}`);
}

function effectiveBinding(id: string): string {
  return resolveBinding(id, props.overrides ?? {}) ?? '';
}
</script>

<template>
  <Teleport to="body">
    <div
      v-if="open"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/60 backdrop-blur-sm"
      role="dialog"
      aria-modal="true"
      aria-label="Keyboard shortcuts"
      data-testid="cheat-sheet-modal"
      @click="onBackdropClick"
      @keydown.escape="close"
    >
      <div
        class="relative max-h-[80vh] w-full max-w-2xl overflow-y-auto rounded-md border border-border bg-surface-1 shadow-xl"
        tabindex="-1"
      >
        <!-- Header -->
        <div class="sticky top-0 z-10 flex items-center justify-between border-b border-border bg-surface-1 px-6 py-4">
          <h2 class="font-ui text-[13px] font-semibold uppercase tracking-[0.18em] text-ink">
            Keyboard shortcuts
          </h2>
          <button
            type="button"
            class="rounded p-1 text-ink-muted transition-colors hover:text-ink"
            aria-label="Close cheat sheet"
            data-testid="cheat-sheet-close"
            @click="close"
          >
            <svg class="h-4 w-4" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5">
              <path d="M3 3l10 10M13 3L3 13" />
            </svg>
          </button>
        </div>

        <!-- Body — grouped shortcut table -->
        <div class="px-6 py-4 grid gap-6">
          <section
            v-for="[category, shortcuts] in groups"
            :key="category"
            :data-testid="`cheat-sheet-category-${category.toLowerCase()}`"
          >
            <h3 class="mb-2 font-ui text-[10px] uppercase tracking-[0.2em] text-ink-subtle">
              {{ category }}
            </h3>
            <table class="w-full border-collapse">
              <tbody>
                <tr
                  v-for="sc in shortcuts"
                  :key="sc.id"
                  class="group cursor-pointer border-b border-border-muted last:border-b-0 transition-colors hover:bg-surface-2"
                  :data-testid="`cheat-sheet-row-${sc.id}`"
                  @click="goToSettings(sc.id)"
                >
                  <td class="py-2 pr-4 font-ui text-[12px] text-ink">
                    {{ sc.label }}
                  </td>
                  <td class="py-2 pr-4 font-ui text-[11px] text-ink-muted">
                    {{ sc.description }}
                  </td>
                  <td class="py-2 text-right">
                    <KeycapDisplay :binding="effectiveBinding(sc.id)" />
                  </td>
                </tr>
              </tbody>
            </table>
          </section>
        </div>

        <!-- Footer hint -->
        <div class="border-t border-border-muted px-6 py-3 font-ui text-[11px] text-ink-dim">
          Press
          <KeycapDisplay binding="?" />
          or
          <KeycapDisplay binding="Escape" />
          to close. Click a row to jump to the shortcut in Settings.
        </div>
      </div>
    </div>
  </Teleport>
</template>
