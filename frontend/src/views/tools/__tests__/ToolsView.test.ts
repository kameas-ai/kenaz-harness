import { describe, it, expect } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import ToolsView from '@/views/tools/ToolsView.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { MCPServer } from '@/lib/types';

function provide(seed: MCPServer[] = []) {
  const client = createFakeHarnessClient({
    mcp: {
      listServers: async () => seed,
      startStream: async () => 'fake-mcp-sub',
      stopStream: async () => undefined,
    },
  });
  return { client };
}

describe('ToolsView (FR-001b numbered-section header)', () => {
  it('renders the canvas head with section number 02', async () => {
    const { client } = provide();
    const w = mount(ToolsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(w.text()).toContain('02');
    expect(w.text()).toContain('TOOLS');
    expect(w.text()).toContain('Connected MCP servers');
  });

  it('renders the empty-state copy + doc link when no servers are configured', async () => {
    const { client } = provide([]);
    const w = mount(ToolsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    const empty = w.find('[data-testid=tools-empty]');
    expect(empty.exists()).toBe(true);
    expect(empty.text()).toContain('No MCP servers configured');
    expect(w.html()).toContain('docs/mcp.md');
  });

  it('renders a row per server when the registry returns entries', async () => {
    const seed: MCPServer[] = [
      {
        id: 'fs',
        name: 'filesystem',
        state: 'ready',
        version: '0.1.0',
        transport: 'stdio',
        capabilities: ['read', 'write'],
      },
      {
        id: 'web',
        name: 'web-search',
        state: 'connecting',
        version: '0.2.0',
        transport: 'ws',
      },
    ];
    const { client } = provide(seed);
    const w = mount(ToolsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    const table = w.find('[data-testid=tools-table]');
    expect(table.exists()).toBe(true);
    expect(w.text()).toContain('filesystem');
    expect(w.text()).toContain('web-search');
    expect(w.text()).toContain('stdio');
    expect(w.text()).toContain('read, write');
  });

  it('uses only design tokens — no raw hex/rgba', async () => {
    const { client } = provide();
    const w = mount(ToolsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    const html = w.html();
    expect(html).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(html).not.toMatch(/rgba?\s*\(/i);
  });
});
