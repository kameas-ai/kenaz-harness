<script setup lang="ts">
import UserMenu from '@/components/shell/UserMenu.vue';

defineProps<{
  /** Per-surface override; defaults to true so AI-output disclaimer is
   *  visible on every surface unless explicitly suppressed (FR-001h). */
  showDisclaimer?: boolean;
}>();
</script>

<template>
  <div class="flex items-center justify-between pl-[88px] pr-4 py-2 select-none">
    <div class="flex items-center gap-3">
      <span
        v-if="showDisclaimer !== false"
        class="font-ui text-[11px] text-ink-subtle"
        aria-label="Disclaimer"
      >
        Content is user-generated and unverified.
      </span>
    </div>

    <!-- The single top-right dropdown — owns identity, search, command
         palette, theme toggle, and update notifications. See UserMenu.vue
         for the full menu layout. The trigger always renders (with an
         update-available dot overlay when applicable). -->
    <UserMenu />
  </div>
</template>
