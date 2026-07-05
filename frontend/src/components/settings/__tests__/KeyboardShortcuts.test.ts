import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import KeyboardShortcuts from '../KeyboardShortcuts.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { Settings } from '@/lib/types';

function buildWrapper(overrides: Record<string, string> = {}) {
  const settings: Settings = {
    schemaVersion: 1,
    lastRoute: '/sessions',
    theme: 'system',
    accent: 'default',
    windowSize: { width: 1280, height: 800 },
    keyboardShortcuts: { ...overrides },
  };
  const set = vi.fn().mockResolvedValue(undefined);
  const get = vi.fn().mockResolvedValue(settings);
  const client = createFakeHarnessClient({
    settings: {
      get,
      set,
      loadRoute: async () => '/sessions',
      saveRoute: async () => undefined,
      logRouteChange: async () => undefined,
      loadTheme: async () => 'system',
      saveTheme: async () => undefined,
      getMemory: async () => false,
      setMemory: async () => undefined,
      getWebSearch: async () => false,
      setWebSearch: async () => undefined,
      getBash: async () => false,
      setBash: async () => undefined,
      getSaveArtifact: async () => true,
      setSaveArtifact: async () => undefined,
      getMaxAgentTurns: async () => 0,
      setMaxAgentTurns: async () => undefined,
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } as any,
  });
  return {
    wrapper: mount(KeyboardShortcuts, {
      global: {
        provide: { [HarnessClientKey as symbol]: client },
      },
    }),
    set,
    get,
  };
}

describe('KeyboardShortcuts', () => {
  it('renders the section heading', async () => {
    const { wrapper } = buildWrapper();
    await flushPromises();
    expect(wrapper.find('[data-testid="keyboard-shortcuts-section"]').exists()).toBe(true);
    expect(wrapper.text()).toContain('Keyboard shortcuts');
  });

  it('renders a row for chat.send', async () => {
    const { wrapper } = buildWrapper();
    await flushPromises();
    expect(wrapper.find('[data-testid="shortcut-row-chat.send"]').exists()).toBe(true);
  });

  it('shows the default binding for chat.send', async () => {
    const { wrapper } = buildWrapper();
    await flushPromises();
    const row = wrapper.find('[data-testid="shortcut-row-chat.send"]');
    // KeycapDisplay renders aria-label with the canonical binding.
    expect(row.html()).toContain('Cmd+Enter');
  });

  it('shows override binding when one exists', async () => {
    const { wrapper } = buildWrapper({ 'chat.send': 'Cmd+Shift+Enter' });
    await flushPromises();
    const row = wrapper.find('[data-testid="shortcut-row-chat.send"]');
    expect(row.html()).toContain('Cmd+Shift+Enter');
  });

  it('enters capture mode when Record is clicked', async () => {
    const { wrapper } = buildWrapper();
    await flushPromises();
    await wrapper.find('[data-testid="record-btn-chat.send"]').trigger('click');
    expect(wrapper.find('[data-testid="capture-mode-chat.send"]').exists()).toBe(true);
  });

  it('exits capture mode on Cancel click', async () => {
    const { wrapper } = buildWrapper();
    await flushPromises();
    await wrapper.find('[data-testid="record-btn-chat.send"]').trigger('click');
    // Find and click the Cancel button inside capture mode.
    const captureMode = wrapper.find('[data-testid="capture-mode-chat.send"]');
    const cancelBtn = captureMode.find('button');
    await cancelBtn.trigger('click');
    expect(wrapper.find('[data-testid="capture-mode-chat.send"]').exists()).toBe(false);
  });

  it('shows search filter input', async () => {
    const { wrapper } = buildWrapper();
    await flushPromises();
    expect(wrapper.find('[data-testid="shortcut-search"]').exists()).toBe(true);
  });

  it('filters shortcuts by label', async () => {
    const { wrapper } = buildWrapper();
    await flushPromises();
    const searchInput = wrapper.find('[data-testid="shortcut-search"]');
    await searchInput.setValue('Send message');
    await flushPromises();
    // Only chat.send row should be visible.
    expect(wrapper.find('[data-testid="shortcut-row-chat.send"]').exists()).toBe(true);
    // A nav shortcut row should be filtered out.
    expect(wrapper.find('[data-testid="shortcut-row-nav.new-session"]').exists()).toBe(false);
  });

  it('shows empty state when no shortcuts match search', async () => {
    const { wrapper } = buildWrapper();
    await flushPromises();
    await wrapper.find('[data-testid="shortcut-search"]').setValue('xyznotexist');
    await flushPromises();
    expect(wrapper.find('[data-testid="shortcut-empty-search"]').exists()).toBe(true);
  });

  it('shows Reset button when override exists', async () => {
    const { wrapper } = buildWrapper({ 'chat.send': 'Cmd+Shift+Enter' });
    await flushPromises();
    expect(wrapper.find('[data-testid="reset-btn-chat.send"]').exists()).toBe(true);
  });

  it('shows reset-all button when overrides exist', async () => {
    const { wrapper } = buildWrapper({ 'chat.send': 'Cmd+Shift+Enter' });
    await flushPromises();
    expect(wrapper.find('[data-testid="reset-all-btn"]').exists()).toBe(true);
  });

  it('opens reset-all confirm dialog', async () => {
    const { wrapper } = buildWrapper({ 'chat.send': 'Cmd+Shift+Enter' });
    await flushPromises();
    await wrapper.find('[data-testid="reset-all-btn"]').trigger('click');
    expect(wrapper.find('[data-testid="reset-all-confirm"]').exists()).toBe(true);
  });

  it('cancel hides reset-all confirm without writing', async () => {
    const { wrapper, set } = buildWrapper({ 'chat.send': 'Cmd+Shift+Enter' });
    await flushPromises();
    await wrapper.find('[data-testid="reset-all-btn"]').trigger('click');
    await wrapper.find('[data-testid="reset-all-confirm-cancel"]').trigger('click');
    expect(wrapper.find('[data-testid="reset-all-confirm"]').exists()).toBe(false);
    // No save should have been triggered.
    expect(set).not.toHaveBeenCalled();
  });
});
