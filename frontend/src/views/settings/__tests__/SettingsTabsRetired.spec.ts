/**
 * SettingsTabs — retired developer tabs (settings-cleanup-01SETUX01 WP01).
 *
 * Owner ruling 2026-10-09: Flags, Health and Logs are developer surfaces and
 * are deleted outright — not hidden behind a dev build or a toggle. This
 * pins their absence in BOTH rails: served and desktop differ (desktopOnly
 * entries), so one build passing says nothing about the other.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount } from '@vue/test-utils';
import { ref, readonly } from 'vue';

const served = ref(false);
vi.mock('@/lib/useServedMode', () => ({
  isServedMode: () => served.value,
  useServedMode: () => readonly(served),
}));

import SettingsTabs from '@/views/settings/SettingsTabs.vue';

const RETIRED_IDS = ['settings-tab-flags', 'settings-tab-health', 'settings-tab-logs'];
const RETIRED_LABELS = ['Flags', 'Health', 'Logs'];

describe.each([
  ['desktop', false, 21],
  ['served', true, 20],
] as const)('SettingsTabs — %s rail', (_name, isServed, count) => {
  beforeEach(() => {
    served.value = isServed;
  });

  it('shows no Flags, Health or Logs entry in any group', () => {
    const wrapper = mount(SettingsTabs);
    for (const id of RETIRED_IDS) {
      expect(wrapper.find(`[data-testid="${id}"]`).exists(), id).toBe(false);
    }
    const labels = wrapper.findAll('[data-testid^="settings-tab-"]').map((e) => e.text());
    for (const gone of RETIRED_LABELS) {
      expect(labels).not.toContain(gone);
    }
  });

  it(`renders ${count} entries, Agent graphs only on desktop`, () => {
    const wrapper = mount(SettingsTabs);
    expect(wrapper.findAll('[data-testid^="settings-tab-"]')).toHaveLength(count);
    expect(wrapper.find('[data-testid="settings-tab-agent-graphs"]').exists()).toBe(!isServed);
  });
});
