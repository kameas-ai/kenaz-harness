<script setup lang="ts">
/**
 * SettingsTabs — sub-nav for the Settings hub.
 *
 * Rendered at the top of SettingsView, ProvidersView, HooksView, and
 * BundlesView so users land in a coherent "settings" surface no matter
 * which route they enter through. Routes stay individually addressable
 * for deep-linking (legacy /providers, /hooks, /bundles).
 *
 * Layout: tabs are grouped into categories and rendered in a wrapping
 * flex strip so the nav reflows to multiple rows at narrow widths
 * instead of forcing a horizontal scroll.
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

interface TabGroup {
  label: string;
  tabs: ReadonlyArray<Tab>;
}

// Groups are visual only — they keep related tabs adjacent so the strip
// reads as a structured nav instead of a long undifferentiated row.
const groups: ReadonlyArray<TabGroup> = [
  {
    label: 'App',
    tabs: [
      { to: '/settings', label: 'General' },
      { to: '/settings?tab=updates', label: 'Updates', query: 'updates' },
      { to: '/settings?tab=flags', label: 'Flags', query: 'flags' },
      { to: '/settings?tab=health', label: 'Health', query: 'health' },
    ],
  },
  {
    label: 'Authoring',
    tabs: [
      { to: '/settings?tab=compaction', label: 'Compaction', query: 'compaction' },
      { to: '/settings?tab=slashcmds', label: 'Slash Commands', query: 'slashcmds' },
      { to: '/settings?tab=workflows', label: 'Workflows', query: 'workflows' },
      { to: '/settings?tab=hooks', label: 'Hooks', query: 'hooks' },
    ],
  },
  {
    label: 'Runtime',
    tabs: [
      { to: '/settings?tab=tasks', label: 'Tasks', query: 'tasks' },
      { to: '/settings?tab=scheduledchats', label: 'Scheduled Chats', query: 'scheduledchats' },
    ],
  },
  {
    label: 'Integrations',
    tabs: [
      { to: '/providers', label: 'Providers' },
      { to: '/bundles', label: 'Bundles' },
      { to: '/settings?tab=secrets', label: 'Secrets', query: 'secrets' },
      { to: '/settings?tab=llm-routing', label: 'LLM Routing', query: 'llm-routing' },
    ],
  },
  {
    label: 'Security',
    tabs: [
      { to: '/permissions/bash', label: 'Permissions', matchPrefix: '/permissions' },
      { to: '/policy', label: 'Policy', matchPrefix: '/policy' },
    ],
  },
  {
    label: 'Privacy',
    tabs: [
      {
        to: '/settings?tab=crash-reporting',
        label: 'Crash Reporting',
        query: 'crash-reporting',
      },
    ],
  },
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
    <ul class="flex flex-wrap items-end gap-x-4 gap-y-1">
      <li
        v-for="(group, gIdx) in groups"
        :key="group.label"
        class="flex flex-wrap items-end gap-1"
        :class="gIdx > 0 ? 'border-l border-border-muted pl-4' : ''"
        :aria-label="group.label"
      >
        <button
          v-for="t in group.tabs"
          :key="t.to"
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
