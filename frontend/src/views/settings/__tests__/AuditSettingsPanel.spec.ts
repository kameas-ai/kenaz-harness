/**
 * AuditSettingsPanel tests — audit-log-enhancement-01KX5R8F WP07
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import AuditSettingsPanel from '@/views/settings/AuditSettingsPanel.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { AuditSettings } from '@/lib/types';

function buildClient(initial: AuditSettings) {
  let current: AuditSettings = { ...initial };
  const getAuditSettings = vi.fn(async () => ({ ...current }));
  const setAuditSettings = vi.fn(async (s: AuditSettings) => {
    current = { ...s };
  });
  const client = createFakeHarnessClient({
    settings: { getAuditSettings, setAuditSettings },
  });
  return { client, getAuditSettings, setAuditSettings };
}

describe('AuditSettingsPanel', () => {
  it('renders without crashing', async () => {
    const { client } = buildClient({ strategy: 'keep_forever', windowDays: 90 });
    const wrapper = mount(AuditSettingsPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="audit-settings-panel"]').exists()).toBe(true);
  });

  it('loads initial strategy from backend', async () => {
    const { client, getAuditSettings } = buildClient({ strategy: 'keep_forever', windowDays: 90 });
    const wrapper = mount(AuditSettingsPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    const radio = wrapper.find<HTMLInputElement>('[data-testid="audit-strategy-keep_forever"] input[type="radio"]');
    expect(radio.element.checked).toBe(true);
    expect(getAuditSettings).toHaveBeenCalledOnce();
  });

  it('hides window days input for keep_forever strategy', async () => {
    const { client } = buildClient({ strategy: 'keep_forever', windowDays: 90 });
    const wrapper = mount(AuditSettingsPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="audit-window-days"]').exists()).toBe(false);
  });

  it('shows window days input for delete_after_window strategy', async () => {
    const { client } = buildClient({ strategy: 'delete_after_window', windowDays: 30 });
    const wrapper = mount(AuditSettingsPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    const input = wrapper.find<HTMLInputElement>('[data-testid="audit-window-days"]');
    expect(input.exists()).toBe(true);
    expect(input.element.value).toBe('30');
  });

  it('saves new settings on submit', async () => {
    const { client, setAuditSettings } = buildClient({ strategy: 'keep_forever', windowDays: 90 });
    const wrapper = mount(AuditSettingsPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    // Switch to delete_after_window.
    const radio = wrapper.find<HTMLInputElement>('[data-testid="audit-strategy-delete_after_window"] input[type="radio"]');
    await radio.setValue(true);
    await radio.trigger('change');
    await flushPromises();

    // Set window days.
    const input = wrapper.find<HTMLInputElement>('[data-testid="audit-window-days"]');
    await input.setValue(60);
    await input.trigger('input');

    // Submit.
    await wrapper.find('[data-testid="audit-settings-form"]').trigger('submit');
    await flushPromises();

    expect(setAuditSettings).toHaveBeenCalledWith(
      expect.objectContaining({ strategy: 'delete_after_window', windowDays: 60 }),
    );
  });

  it('shows success banner after save', async () => {
    const { client } = buildClient({ strategy: 'keep_forever', windowDays: 90 });
    const wrapper = mount(AuditSettingsPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    await wrapper.find('[data-testid="audit-settings-form"]').trigger('submit');
    await flushPromises();
    expect(wrapper.find('[data-testid="audit-settings-saved"]').exists()).toBe(true);
  });

  it('shows error banner when save fails', async () => {
    const client = createFakeHarnessClient({
      settings: {
        getAuditSettings: async () => ({ strategy: 'keep_forever' as const, windowDays: 90 }),
        setAuditSettings: async () => {
          throw new Error('disk full');
        },
      },
    });
    const wrapper = mount(AuditSettingsPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    await wrapper.find('[data-testid="audit-settings-form"]').trigger('submit');
    await flushPromises();
    const err = wrapper.find('[data-testid="audit-settings-error"]');
    expect(err.exists()).toBe(true);
    expect(err.text()).toContain('disk full');
  });
});
