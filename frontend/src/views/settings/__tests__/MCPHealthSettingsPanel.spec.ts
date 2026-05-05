/**
 * MCPHealthSettingsPanel tests — WP06 (mcp-server-health-ui-01KQ8TD6)
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import MCPHealthSettingsPanel from '@/views/settings/MCPHealthSettingsPanel.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';

function buildClient(initialAutoRestart: boolean) {
  let current = initialAutoRestart;
  const getMCPAutoRestart = vi.fn(async () => current);
  const setMCPAutoRestart = vi.fn(async (v: boolean) => {
    current = v;
  });
  const client = createFakeHarnessClient({
    settings: {
      getMCPAutoRestart,
      setMCPAutoRestart,
    },
  });
  return { client, getMCPAutoRestart, setMCPAutoRestart };
}

describe('MCPHealthSettingsPanel', () => {
  it('renders the panel', async () => {
    const { client } = buildClient(true);
    const wrapper = mount(MCPHealthSettingsPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="mcp-health-settings-panel"]').exists()).toBe(true);
  });

  it('loads initial auto-restart state (ON)', async () => {
    const { client, getMCPAutoRestart } = buildClient(true);
    const wrapper = mount(MCPHealthSettingsPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    const toggle = wrapper.find<HTMLInputElement>('[data-testid="mcp-auto-restart-toggle"]');
    expect(toggle.element.checked).toBe(true);
    expect(getMCPAutoRestart).toHaveBeenCalledOnce();
  });

  it('loads initial auto-restart state (OFF)', async () => {
    const { client } = buildClient(false);
    const wrapper = mount(MCPHealthSettingsPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    const toggle = wrapper.find<HTMLInputElement>('[data-testid="mcp-auto-restart-toggle"]');
    expect(toggle.element.checked).toBe(false);
  });

  it('calls setMCPAutoRestart when toggled', async () => {
    const { client, setMCPAutoRestart } = buildClient(true);
    const wrapper = mount(MCPHealthSettingsPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    const toggle = wrapper.find<HTMLInputElement>('[data-testid="mcp-auto-restart-toggle"]');
    // Simulate unchecking.
    await toggle.setValue(false);
    await toggle.trigger('change');
    await flushPromises();
    expect(setMCPAutoRestart).toHaveBeenCalledWith(false);
  });

  it('shows error message on save failure', async () => {
    const client = createFakeHarnessClient({
      settings: {
        getMCPAutoRestart: async () => true,
        setMCPAutoRestart: async () => {
          throw new Error('store write failed');
        },
      },
    });
    const wrapper = mount(MCPHealthSettingsPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    const toggle = wrapper.find<HTMLInputElement>('[data-testid="mcp-auto-restart-toggle"]');
    await toggle.setValue(false);
    await toggle.trigger('change');
    await flushPromises();
    const errorEl = wrapper.find('[data-testid="mcp-auto-restart-error"]');
    expect(errorEl.exists()).toBe(true);
    expect(errorEl.text()).toContain('store write failed');
  });
});
