<script setup lang="ts">
/**
 * SettingsTabs — vertical sub-nav rail for the Settings hub.
 *
 * Rendered (via SettingsShell) on the left of SettingsView, ProvidersView,
 * BundlesView, HooksView, PermissionsView, and PolicyView so users land in a
 * coherent "settings" surface no matter which route they enter through.
 * Routes stay individually addressable for deep-linking (/providers, /policy,
 * /permissions/*, and the /settings?tab=… query-param panels).
 *
 * Layout: a vertical, grouped, icon'd rail mirroring the app's LeftRail /
 * RailEntry vocabulary (icon + label, grouped under uppercase small-caps
 * headers, active = surface-2 + accent hairline ring + accent icon). Labels
 * collapse to icons-only under the two-col (960px) breakpoint.
 */
import type { Component } from 'vue';
import { computed } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import {
  Activity,
  AlertTriangle,
  Archive,
  CalendarClock,
  CircleUser,
  Code,
  Command,
  Download,
  FileText,
  Flag,
  GitBranch,
  Globe,
  KeyRound,
  Package,
  Plug,
  RefreshCw,
  Route,
  Scale,
  Server,
  Shield,
  SlidersHorizontal,
  Webhook,
} from '@/shell/icons';

// useRoute / useRouter return undefined when the component is mounted
// outside a router context (vitest unit tests). Guard so the rail
// degrades to "no active state" instead of crashing the host view.
const route = useRoute() as ReturnType<typeof useRoute> | undefined;
const router = useRouter() as ReturnType<typeof useRouter> | undefined;

interface Tab {
  to: string;
  label: string;
  icon: Component;
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

// Groups keep related items adjacent so the rail reads as a structured
// nav instead of a long undifferentiated list.
const groups: ReadonlyArray<TabGroup> = [
  {
    label: 'App',
    tabs: [
      { to: '/settings', label: 'General', icon: SlidersHorizontal },
      // fleet-auth-foundation-01NDFSEX08 WP06 — Account (fleet identity) sub-tab.
      { to: '/settings?tab=account', label: 'Account', query: 'account', icon: CircleUser },
      { to: '/settings?tab=updates', label: 'Updates', query: 'updates', icon: Download },
      { to: '/settings?tab=flags', label: 'Flags', query: 'flags', icon: Flag },
      { to: '/settings?tab=health', label: 'Health', query: 'health', icon: Activity },
    ],
  },
  {
    label: 'Authoring',
    tabs: [
      { to: '/settings?tab=compaction', label: 'Compaction', query: 'compaction', icon: Archive },
      { to: '/settings?tab=slashcmds', label: 'Slash Commands', query: 'slashcmds', icon: Command },
      { to: '/settings?tab=workflows', label: 'Workflows', query: 'workflows', icon: GitBranch },
      { to: '/settings?tab=hooks', label: 'Hooks', query: 'hooks', icon: Webhook },
    ],
  },
  {
    label: 'Runtime',
    tabs: [
      // The 'Tasks' entry was removed 2026-08-14: the background-task
      // subsystem has no producer, so the panel it linked to was always
      // empty. See docs/unwired-ledger.md.
      {
        to: '/settings?tab=scheduledchats',
        label: 'Scheduled Chats',
        query: 'scheduledchats',
        icon: CalendarClock,
      },
    ],
  },
  {
    label: 'Integrations',
    tabs: [
      { to: '/providers', label: 'Providers', icon: Plug },
      { to: '/bundles', label: 'Bundles', icon: Package },
      { to: '/settings?tab=secrets', label: 'Secrets', query: 'secrets', icon: KeyRound },
      { to: '/settings?tab=llm-routing', label: 'LLM Routing', query: 'llm-routing', icon: Route },
      // acp-orchestration-integration-01NDFSEX06 WP05 — ACP Peers tab.
      { to: '/settings?tab=peers', label: 'Peers', query: 'peers', icon: Globe },
      // fleet-share-and-sync-01NDFSEX14 WP06 — Settings sync tab.
      { to: '/settings?tab=sync', label: 'Sync', query: 'sync', icon: RefreshCw },
    ],
  },
  {
    label: 'Security',
    tabs: [
      { to: '/permissions/bash', label: 'Permissions', matchPrefix: '/permissions', icon: Shield },
      { to: '/policy', label: 'Policy', matchPrefix: '/policy', icon: Scale },
      // audit-log-enhancement-01KX5R8F WP07 — Audit retention settings
      // nav-settings-ia-cleanup WP04: renamed from "Audit" to "Audit Settings" to
      // distinguish from the "Audit Log" viewer entry below.
      { to: '/settings?tab=audit', label: 'Audit Settings', query: 'audit', icon: FileText },
      // nav-settings-ia-cleanup WP04: audit-log viewer promoted into Settings → Security.
      // Navigates directly to /audit (the viewer keeps its own CanvasHead layout).
      { to: '/audit', label: 'Audit Log', matchPrefix: '/audit', icon: Activity },
      // mission 01NLOGS01 WP05: in-app runtime log ring buffer.
      { to: '/settings?tab=logs', label: 'Logs', query: 'logs', icon: Code },
      // fleet-audit-archival-01NDFSEX13 WP06 — Compliance (Team+ tier gated)
      { to: '/settings?tab=compliance', label: 'Compliance', query: 'compliance', icon: Server },
    ],
  },
  {
    label: 'Privacy',
    tabs: [
      {
        to: '/settings?tab=crash-reporting',
        label: 'Crash Reporting',
        query: 'crash-reporting',
        icon: AlertTriangle,
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
    class="shrink-0 w-14 two-col:w-56 h-full overflow-y-auto border-r border-border-muted bg-surface-0 p-2 two-col:p-3"
    aria-label="Settings sections"
    data-testid="settings-tabs"
  >
    <ul class="grid gap-3">
      <li
        v-for="group in groups"
        :key="group.label"
        :aria-label="group.label"
      >
        <h3
          class="hidden two-col:block px-3 pb-1 font-ui text-[10px] font-medium uppercase tracking-[0.18em] text-ink-subtle"
        >
          {{ group.label }}
        </h3>
        <ul class="grid gap-0.5">
          <li v-for="t in group.tabs" :key="t.to">
            <button
              type="button"
              class="flex items-center gap-2 px-3 py-2 rounded-sm w-full text-left text-sm font-ui transition-fast ease-kenaz"
              :class="
                isActive(t)
                  ? 'text-ink bg-surface-2 ring-1 ring-accent-hairline'
                  : 'text-ink-muted hover:text-ink hover:bg-surface-2'
              "
              :aria-current="isActive(t) ? 'page' : undefined"
              :title="t.label"
              :data-testid="`settings-tab-${t.label.toLowerCase().replace(/\s+/g, '-')}`"
              @click="goto(t.to)"
            >
              <component
                :is="t.icon"
                :size="14"
                class="shrink-0"
                :class="isActive(t) ? 'text-accent' : ''"
              />
              <span class="truncate hidden two-col:inline">{{ t.label }}</span>
            </button>
          </li>
        </ul>
      </li>
    </ul>
  </nav>
</template>
