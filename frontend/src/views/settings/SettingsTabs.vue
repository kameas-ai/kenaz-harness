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

interface Tab {
  to: string;
  label: string;
  /** Optional path-prefix used to keep the tab highlighted across
   *  nested routes (e.g. /permissions/fs still highlights Permissions). */
  matchPrefix?: string;
  /**
   * Optional query-param marker used to keep two tabs that share the
   * same path distinguishable (e.g. General and Updates both live under
   * /settings; Updates is reached via /settings?tab=updates). When set,
   * isActive() requires both the path AND the query.tab to match this
   * value. Tabs without a `query` only match when query.tab is absent.
   */
  query?: string;
}

const tabs: ReadonlyArray<Tab> = [
  { to: '/settings', label: 'General' },
  // auto-update-v0.4.0 WP05 — Updates tab. Shares /settings with the
  // General tab and disambiguates via ?tab=updates so we don't have to
  // touch the router. See SettingsView.vue for the mount switch.
  { to: '/settings?tab=updates', label: 'Updates', query: 'updates' },
  // v0.5.1 migration-doctor — Health tab. Disambiguates via ?tab=health.
  // See SettingsView.vue for the mount switch.
  { to: '/settings?tab=health', label: 'Health', query: 'health' },
  // compaction-strategy-ui-01KQ8TD8 — Compaction strategy-authoring tab.
  // Disambiguates via ?tab=compaction. See SettingsView.vue for the mount switch.
  { to: '/settings?tab=compaction', label: 'Compaction', query: 'compaction' },
  // user-slash-commands-01KQ8TD9 WP07 — Slash Commands tab.
  // Disambiguates via ?tab=slashcmds. See SettingsView.vue for the mount switch.
  { to: '/settings?tab=slashcmds', label: 'Slash Commands', query: 'slashcmds' },
  { to: '/providers', label: 'Providers' },
  { to: '/hooks', label: 'Hooks' },
  { to: '/bundles', label: 'Bundles' },
  { to: '/permissions/bash', label: 'Permissions', matchPrefix: '/permissions' },
];

const activePath = computed(() => route?.path ?? '');
const activeQuery = computed<string>(() => {
  const v = route?.query?.tab;
  if (typeof v === 'string') return v;
  return '';
});

function isActive(t: Tab): boolean {
  if (t.matchPrefix) return activePath.value.startsWith(t.matchPrefix);
  // For tabs that share /settings, require an exact query.tab match so
  // General and Updates highlight independently.
  if (t.to.startsWith('/settings')) {
    if (activePath.value !== '/settings') return false;
    return (t.query ?? '') === activeQuery.value;
  }
  return activePath.value === t.to;
}

function goto(to: string) {
  if (!router) return;
  // Preserve the query-param semantics — vue-router parses ?tab=… off
  // the string-form push target so we don't have to split it manually.
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
            isActive(t)
              ? 'border-accent text-ink'
              : 'border-transparent text-ink-muted hover:text-ink'
          "
          :aria-current="isActive(t) ? 'page' : undefined"
          :data-testid="`settings-tab-${t.label.toLowerCase().replace(/\s+/g, '-')}`"
          @click="goto(t.to)"
        >
          {{ t.label }}
        </button>
      </li>
    </ul>
  </nav>
</template>
