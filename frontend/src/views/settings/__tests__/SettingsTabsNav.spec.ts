/**
 * SettingsTabs — vertical icon-rail structure test.
 *
 * After the settings-nav redesign the strip is a grouped vertical rail
 * (mirroring the app LeftRail). This pins the structure: the six category
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
  it('renders the six category group headers', () => {
    const wrapper = mount(SettingsTabs);
    const headers = wrapper.findAll('h3').map((h) => h.text());
    expect(headers).toEqual([
      'App',
      'Authoring',
      'Runtime',
      'Integrations',
      'Security',
      'Privacy',
    ]);
  });

  it('renders every nav item with an icon and a test id', () => {
    const wrapper = mount(SettingsTabs);
    const items = wrapper.findAll('[data-testid^="settings-tab-"]');
    // 5 (App) + 5 (Authoring) + 1 (Runtime) + 6 (Integrations: Providers/Bundles/Secrets/LLMRouting/Peers/Sync)
    // + 6 (Security: Permissions/Policy/Audit Settings/Audit Log/Compliance/Logs) + 1 (Privacy) = 24
    // mission 01NLOGS01 WP05: +1 for the "Logs" runtime-log viewer in Security.
    // 2026-08-14: -1 — the "Tasks" entry was removed (see next spec).
    // engineer-truth-pass-01PMTP01 WP03: +1 — Branch Advisor sub-tab in Authoring.
    expect(items).toHaveLength(24);
    for (const item of items) {
      // lucide-vue-next renders an <svg>; every row should carry one.
      expect(item.find('svg').exists()).toBe(true);
    }
  });

  it('does not offer a Tasks entry — the background-task subsystem has no producer', () => {
    // core/tasks.Registry never receives a row in production: the only
    // Register call is subagentdispatch/tool.go, guarded by opts.Tasks,
    // whose sole production assignment is `Tasks: nil`
    // (core/rpc/builtins_wiring.go:317); and bash.Options.BackgroundSpawn
    // has no non-test assignment. The panel behind this link was therefore
    // permanently empty. TasksPanel.vue is parked, not deleted — the
    // product decision on background execution is still open.
    const wrapper = mount(SettingsTabs);
    expect(wrapper.find('[data-testid="settings-tab-tasks"]').exists()).toBe(false);
    expect(wrapper.html()).not.toContain('tab=tasks');
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
