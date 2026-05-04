<script setup lang="ts">
/**
 * UpdateIndicator — the Chrome-style "tiny dot" surfacing the auto-update
 * affordance (mission auto-update WP04).
 *
 * Design discipline:
 *   - 16px dot, never a modal, never a persistent toast
 *   - hidden when nothing is offered or the user skipped
 *   - states are encoded in colour + animation, not iconography:
 *       available + downloading → pulsing accent (brass)
 *       available + idle/staged → solid success (sage)
 *       failed                  → solid danger (red)
 *   - clicking opens UpdateMenu anchored to the dot — a popover, not a modal
 *
 * Mounts once in the Titlebar. The store boots on first mount; the menu and
 * any later remounts share the same module-scoped state so we never double-
 * subscribe to the broker topic.
 */

import { computed, onMounted, ref, useTemplateRef } from 'vue';
import { useUpdateStore } from './useUpdateStore';
import UpdateMenu from './UpdateMenu.vue';
import UpdateToast from './UpdateToast.vue';

const store = useUpdateStore();
const menuOpen = ref(false);
const buttonRef = useTemplateRef<HTMLButtonElement>('buttonRef');

onMounted(() => {
  void store.ensureBoot();
});

type DotState = 'pulsing' | 'solid' | 'failed';

const dotState = computed<DotState>(() => {
  const s = store.status.value;
  if (!s) return 'pulsing';
  if (s.downloadState === 'failed') return 'failed';
  if (s.downloadState === 'downloading') return 'pulsing';
  // staged or idle: ready to apply / ready to start
  if (s.downloadState === 'staged') return 'solid';
  return 'pulsing';
});

const ariaLabel = computed(() => {
  const s = store.status.value;
  if (!s || !s.availableVersion) return 'Update available';
  if (s.downloadState === 'failed') {
    return `Update to ${s.availableVersion} failed — click for options`;
  }
  if (s.downloadState === 'staged') {
    return `Update to ${s.availableVersion} ready to install`;
  }
  if (s.downloadState === 'downloading') {
    return `Downloading update to ${s.availableVersion}`;
  }
  return `Update ${s.availableVersion} available`;
});

function toggleMenu() {
  menuOpen.value = !menuOpen.value;
}

function closeMenu() {
  menuOpen.value = false;
}
</script>

<template>
  <!-- One-shot toast (separate component, mounts even when indicator is hidden
       because the toast fires on first detection regardless of click state). -->
  <UpdateToast />

  <div v-if="store.visible.value" class="relative inline-flex items-center">
    <button
      ref="buttonRef"
      type="button"
      class="grid h-6 w-6 place-items-center rounded-sm hover:bg-surface-2 transition-fast ease-kenaz"
      :aria-label="ariaLabel"
      :aria-expanded="menuOpen"
      aria-haspopup="dialog"
      data-testid="update-indicator"
      @click="toggleMenu"
    >
      <span
        class="block h-2 w-2 rounded-full"
        :class="{
          'bg-accent indicator-pulse': dotState === 'pulsing',
          'bg-signal-ok': dotState === 'solid',
          'bg-signal-danger': dotState === 'failed',
        }"
        :data-state="dotState"
        data-testid="update-indicator-dot"
      />
    </button>

    <UpdateMenu
      v-if="menuOpen"
      :anchor="buttonRef"
      @close="closeMenu"
    />
  </div>
</template>

<style scoped>
/*
 * The pulse animation is the only motion we ship in WP04. Kept local to this
 * component so it doesn't leak into the global stylesheet — and so the
 * `prefers-reduced-motion` carve-out lives next to its only consumer.
 */
.indicator-pulse {
  box-shadow: 0 0 0 0 var(--accent-glow);
  animation: update-indicator-pulse 1.6s var(--motion-base) ease-in-out infinite;
}
@keyframes update-indicator-pulse {
  0% {
    box-shadow: 0 0 0 0 var(--accent-glow);
  }
  70% {
    box-shadow: 0 0 0 6px transparent;
  }
  100% {
    box-shadow: 0 0 0 0 transparent;
  }
}
@media (prefers-reduced-motion: reduce) {
  .indicator-pulse {
    animation: none;
  }
}
</style>
