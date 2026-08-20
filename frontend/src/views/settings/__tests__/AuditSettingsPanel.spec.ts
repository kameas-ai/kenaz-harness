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
    settings: { getAuditSettings, setAuditSettings } as any,
  });
  return { client, getAuditSettings, setAuditSettings };
}

describe('AuditSettingsPanel', () => {
  it('renders without crashing', async () => {
    const { client } = buildClient({ strategy: 'keep_forever', window_days: 90, retention_enforced: false });
    const wrapper = mount(AuditSettingsPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="audit-settings-panel"]').exists()).toBe(true);
  });

  it('loads initial strategy from backend', async () => {
    const { client, getAuditSettings } = buildClient({ strategy: 'keep_forever', window_days: 90, retention_enforced: false });
    const wrapper = mount(AuditSettingsPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    const radio = wrapper.find<HTMLInputElement>('[data-testid="audit-strategy-keep_forever"] input[type="radio"]');
    expect(radio.element.checked).toBe(true);
    expect(getAuditSettings).toHaveBeenCalledOnce();
  });

  it('hides window days input for keep_forever strategy', async () => {
    const { client } = buildClient({ strategy: 'keep_forever', window_days: 90, retention_enforced: false });
    const wrapper = mount(AuditSettingsPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="audit-window-days"]').exists()).toBe(false);
  });

  it('shows window days input for delete_after_window strategy', async () => {
    const { client } = buildClient({ strategy: 'delete_after_window', window_days: 30, retention_enforced: false });
    const wrapper = mount(AuditSettingsPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    const input = wrapper.find<HTMLInputElement>('[data-testid="audit-window-days"]');
    expect(input.exists()).toBe(true);
    expect(input.element.value).toBe('30');
  });

  it('saves new settings on submit', async () => {
    const { client, setAuditSettings } = buildClient({ strategy: 'keep_forever', window_days: 90, retention_enforced: false });
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
      expect.objectContaining({ strategy: 'delete_after_window', window_days: 60 }),
    );
  });

  it('shows success banner after save', async () => {
    const { client } = buildClient({ strategy: 'keep_forever', window_days: 90, retention_enforced: false });
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
        getAuditSettings: async () => ({ strategy: 'keep_forever' as const, window_days: 90, retention_enforced: false }),
        setAuditSettings: async () => {
          throw new Error('disk full');
        },
      } as any,
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

  // ── AC-006 (audit-that-tells-the-truth-01PMZA10 UNIT-4 / WP05) ──────────
  // The panel must never promise a sweep that isn't real, and must say so
  // plainly when it isn't. Both states are driven from the server's
  // retention_enforced fact, never from a literal in the component.

  it('does not promise a sweep when retention is not enforced', async () => {
    const { client } = buildClient({ strategy: 'delete_after_window', window_days: 30, retention_enforced: false });
    const wrapper = mount(AuditSettingsPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    const text = wrapper.text();
    // No sentence anywhere in the panel may claim a sweep is running.
    expect(text).not.toMatch(/sweep runs|permanently deleted during|written to a JSONL archive file then removed/i);
    // It must say so plainly instead.
    expect(wrapper.find('[data-testid="audit-settings-subtitle"]').text()).toMatch(/not yet active/i);
  });

  it('renders the sweep sentence when retention is enforced', async () => {
    const { client } = buildClient({ strategy: 'delete_after_window', window_days: 30, retention_enforced: true });
    const wrapper = mount(AuditSettingsPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    expect(wrapper.find('[data-testid="audit-settings-subtitle"]').text()).toMatch(/sweep runs/i);
    const deleteDesc = wrapper.find('[data-testid="audit-strategy-delete_after_window"]').text();
    expect(deleteDesc).toMatch(/permanently deleted during the retention sweep/i);
    expect(deleteDesc).not.toMatch(/not yet active/i);
  });

  it('renders the archive sentence when retention is enforced, and the honest alternative when it is not', async () => {
    const enforced = buildClient({ strategy: 'archive_after_window', window_days: 30, retention_enforced: true });
    const enforcedWrapper = mount(AuditSettingsPanel, {
      global: { provide: { [HarnessClientKey as symbol]: enforced.client } },
    });
    await flushPromises();
    expect(enforcedWrapper.find('[data-testid="audit-strategy-archive_after_window"]').text())
      .toMatch(/written to a JSONL archive file then removed from the database\./);

    const unenforced = buildClient({ strategy: 'archive_after_window', window_days: 30, retention_enforced: false });
    const unenforcedWrapper = mount(AuditSettingsPanel, {
      global: { provide: { [HarnessClientKey as symbol]: unenforced.client } },
    });
    await flushPromises();
    expect(unenforcedWrapper.find('[data-testid="audit-strategy-archive_after_window"]').text())
      .toMatch(/not yet available/i);
  });

  it('keep_forever description is unconditionally true regardless of enforcement', async () => {
    for (const enforced of [true, false]) {
      const { client } = buildClient({ strategy: 'keep_forever', window_days: 90, retention_enforced: enforced });
      const wrapper = mount(AuditSettingsPanel, {
        global: { provide: { [HarnessClientKey as symbol]: client } },
      });
      await flushPromises();
      expect(wrapper.find('[data-testid="audit-strategy-keep_forever"]').text())
        .toContain('Audit events are never deleted from the database.');
    }
  });
});
