/**
 * SettingsTabs — vertical icon-rail structure test.
 *
 * After the settings-nav redesign the strip is a grouped vertical rail
 * (mirroring the app LeftRail). This pins the structure: the five category
 * group headers render, every nav item carries an icon + its settings-tab
 * test id, and the full item set is present.
 */
import { describe, it, expect } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import SettingsTabs from '@/views/settings/SettingsTabs.vue';

// vue-router returns undefined outside a router context — SettingsTabs
// guards for this and degrades to "no active state".
describe('SettingsTabs — vertical nav rail', () => {
  // nav-ia-sweep-01DOGF0F WP05: the Runtime group left for Workflows.
  it('renders the five category group headers', () => {
    const wrapper = mount(SettingsTabs);
    const headers = wrapper.findAll('h3').map((h) => h.text());
    expect(headers).toEqual([
      'App',
      'Authoring',
      'Integrations',
      'Security',
      'Privacy',
    ]);
  });

  it('renders every nav item with an icon and a test id', () => {
    const wrapper = mount(SettingsTabs);
    const items = wrapper.findAll('[data-testid^="settings-tab-"]');
    // 5 (App) + 5 (Authoring) + 2 (Runtime: Scheduled Chats/Tasks)
    // + 6 (Integrations: Providers/Bundles/Secrets/LLMRouting/Peers/Sync)
    // + 6 (Security: Permissions/Policy/Audit Settings/Audit Log/Compliance/Logs) + 1 (Privacy) = 25
    // mission 01NLOGS01 WP05: +1 for the "Logs" runtime-log viewer in Security.
    // 2026-08-14: -1 — the "Tasks" entry was removed (see next spec, since inverted).
    // engineer-truth-pass-01PMTP01 WP03: +1 — Branch Advisor sub-tab in Authoring.
    // subagent-control-and-background-tasks-01PMZB11 UNIT-11: +1 — the
    // "Tasks" entry is restored (Runtime group moves 1 -> 2 items).
    // laya-advisors-01LAYA001 WP13: +1 — Recommendations (local ML engine)
    // sub-tab in Authoring.
    // nav-ia-sweep-01DOGF0F WP05: -3 — Runtime (Scheduled Chats, Tasks) and
    // Authoring › Workflows moved to the Workflows surface.
    // agentgraph-settings-linkage-01DOGF0D WP05: +1 — Agent graphs in
    // Authoring (moved from the top-level rail; desktop-only).
    expect(items).toHaveLength(24);
    for (const item of items) {
      // lucide-vue-next renders an <svg>; every row should carry one.
      expect(item.find('svg').exists()).toBe(true);
    }
  });

  // History: the Tasks entry was removed 2026-08-14 (no producer), then
  // restored by subagent-control-and-background-tasks-01PMZB11 UNIT-11 once
  // bash.Options.BackgroundSpawn had a real caller (see
  // core/rpc/background_task_wiring_test.go). nav-ia-sweep-01DOGF0F WP05
  // (owner ruling F9a) MOVED it — with Scheduled Chats and the Workflows
  // panel — to the Workflows surface. The capability is not gone: its
  // real-parent mount is pinned in
  // views/workflows/__tests__/WorkflowsView.tasks.spec.ts, and the old URL
  // redirects (__tests__/legacySettingsRedirects.test.ts).
  it('offers no Tasks, Scheduled Chats or Workflows entry — they live under Workflows now', () => {
    const wrapper = mount(SettingsTabs);
    for (const id of [
      'settings-tab-tasks',
      'settings-tab-scheduled-chats',
      'settings-tab-workflows',
    ]) {
      expect(wrapper.find(`[data-testid="${id}"]`).exists(), id).toBe(false);
    }
    const labels = wrapper.findAll('[data-testid^="settings-tab-"]').map((e) => e.text());
    for (const gone of ['Tasks', 'Scheduled Chats', 'Workflows']) {
      expect(labels).not.toContain(gone);
    }
  });

  // consent-surfaces-truth-01PMTR01 WP06 (FR-007). The denial panel is a
  // sub-tab of PolicyView, so its reachability is exactly as good as this
  // nav entry plus the /policy route (pinned in
  // src/__tests__/entrypoint.routes.test.ts). Losing this link would leave
  // the panel mounted-but-dead — the defect the acceptance criterion
  // names, one level above the component's own spec.
  it('keeps the Policy entry, and clicking it navigates to /policy', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/', name: 'home', component: { render: () => null } },
        { path: '/policy', name: 'policy', component: { render: () => null } },
      ],
    });
    await router.push('/');
    await router.isReady();

    const wrapper = mount(SettingsTabs, { global: { plugins: [router] } });
    const policy = wrapper.find('[data-testid="settings-tab-policy"]');
    expect(policy.exists(), 'the Policy nav entry must exist').toBe(true);

    await policy.trigger('click');
    await flushPromises();
    expect(router.currentRoute.value.path).toBe('/policy');
  });

  it('keeps the addressable tabs (General, Providers, Permissions, Audit Settings, Audit Log)', () => {
    const wrapper = mount(SettingsTabs);
    for (const id of [
      'settings-tab-general',
      'settings-tab-providers',
      'settings-tab-permissions',
      // nav-settings-ia-cleanup WP04: "Audit" renamed to "Audit Settings"; "Audit Log" added.
      'settings-tab-audit-settings',
      'settings-tab-audit-log',
    ]) {
      expect(wrapper.find(`[data-testid="${id}"]`).exists()).toBe(true);
    }
  });
});
