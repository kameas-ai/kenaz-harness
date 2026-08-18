<script setup lang="ts">
/**
 * SettingsShell — two-pane layout for the Settings hub.
 *
 * Left: the vertical SettingsTabs rail (full canvas height, fixed width).
 * Right: a scrolling column holding the CanvasHead breadcrumb/title and the
 * page content (default slot). The rail stays put while the content scrolls.
 *
 * Host views (SettingsView, ProvidersView, BundlesView, PermissionsView,
 * PolicyView) wrap their content in this shell instead of stacking
 * CanvasHead + SettingsTabs + content themselves, so the settings surface
 * reads consistently no matter which route the user enters through.
 * (HooksView used to be in this list — deleted in engineer-truth-pass-
 * 01PMTP01 WP05, finding B13b: it was a routed-but-unlinked rival of
 * HooksSettingsView, which SettingsView already hosts via SettingsShell
 * without needing its own top-level route.)
 *
 * Content padding/max-width is intentionally NOT imposed here — slotted
 * content keeps its own wrapper (panels vary between max-w-3xl, max-w-5xl,
 * and full-bleed).
 */
import CanvasHead from '@/shell/CanvasHead.vue';
import SettingsTabs from '@/views/settings/SettingsTabs.vue';

defineProps<{
  number?: string;
  section?: string;
  title: string;
  subtitle?: string;
}>();
</script>

<template>
  <div class="flex h-full min-h-0">
    <SettingsTabs />
    <div class="flex-1 min-w-0 overflow-auto">
      <CanvasHead
        :number="number"
        :section="section"
        :title="title"
        :subtitle="subtitle"
      >
        <template #trailing><slot name="head-trailing" /></template>
      </CanvasHead>
      <slot />
    </div>
  </div>
</template>
