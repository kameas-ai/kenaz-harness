<script setup lang="ts">
/**
 * SettingsTabs — sub-nav for the Settings hub.
 *
 * Rendered at the top of SettingsView, ProvidersView, HooksView, and
 * BundlesView so users land in a coherent "settings" surface no matter
 * which route they enter through. Routes stay individually addressable
 * for deep-linking (legacy /providers, /hooks, /bundles).
 */
import { computed } from 'vue';
import { useRoute, useRouter } from 'vue-router';

// useRoute / useRouter return undefined when the component is mounted
// outside a router context (vitest unit tests). Guard so the tabs strip
// degrades to "no active state" instead of crashing the host view.
const route = useRoute() as ReturnType<typeof useRoute> | undefined;
const router = useRouter() as ReturnType<typeof useRouter> | undefined;

const tabs: ReadonlyArray<{ to: string; label: string }> = [
  { to: '/settings', label: 'General' },
  { to: '/providers', label: 'Providers' },
  { to: '/hooks', label: 'Hooks' },
  { to: '/bundles', label: 'Bundles' },
];

const activePath = computed(() => route?.path ?? '');

function goto(to: string) {
  if (!router || activePath.value === to) return;
  void router.push(to);
}
</script>

<template>
  <nav
    class="px-6 pt-2 border-b border-border-muted"
    aria-label="Settings sections"
    data-testid="settings-tabs"
  >
    <ul class="flex gap-1">
      <li v-for="t in tabs" :key="t.to">
        <button
          type="button"
          class="px-3 py-1.5 -mb-px font-ui text-[12px] uppercase tracking-[0.16em] border-b-2 transition-colors"
          :class="
            activePath === t.to
              ? 'border-accent text-ink'
              : 'border-transparent text-ink-muted hover:text-ink'
          "
          :aria-current="activePath === t.to ? 'page' : undefined"
          :data-testid="`settings-tab-${t.label.toLowerCase()}`"
          @click="goto(t.to)"
        >
          {{ t.label }}
        </button>
      </li>
    </ul>
  </nav>
</template>
