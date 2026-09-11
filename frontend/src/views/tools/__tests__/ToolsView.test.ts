import { describe, it, expect, vi, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { readonly, ref } from 'vue';
import ToolsView from '@/views/tools/ToolsView.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { MCPServer, MCPToolPolicyRule } from '@/lib/types';

// #66 (test-honesty-trio): onMounted() used to call refresh() and
// refreshPolicies() unconditionally, so a served build fired
// MCP_ListServers / MCP_ListToolPolicies even though the template's
// NotAvailableInServedMode guard means neither response is ever
// rendered. Mocking useServedMode (rather than the window.go.rpc.Bindings
// stub in test-setup.ts) lets one test flip served mode on while the rest
// of this file stays in the desktop-mode default.
const servedFlag = ref(false);
vi.mock('@/lib/useServedMode', () => ({
  isServedMode: () => servedFlag.value,
  useServedMode: () => readonly(servedFlag),
}));

afterEach(() => {
  servedFlag.value = false;
});

function provide(
  seed: MCPServer[] = [],
  policies: MCPToolPolicyRule[] = [],
  mcpOverrides: Record<string, unknown> = {},
) {
  const setToolPolicy = vi.fn(async () => undefined);
  const client = createFakeHarnessClient({
    mcp: {
      listServers: async () => seed,
      startStream: async () => 'fake-mcp-sub',
      stopStream: async () => undefined,
      healthSnapshot: async () => ({}),
      subscribeHealthChanges: async () => 'fake-health-sub',
      listToolPolicies: async () => policies,
      setToolPolicy,
      ...mcpOverrides,
    } as any,
  });
  return { client, setToolPolicy };
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
    expect(w.text()).toContain('MCP servers');
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

  // #66: the template guard (NotAvailableInServedMode v-if) blocks the
  // UI, but onMounted ran past it — this pins that the eager fetches are
  // now ALSO gated, matching the i15 allowlist's boundary-panelled claim
  // for MCP_ListServers / MCP_ListToolPolicies.
  it('does not call listServers or listToolPolicies when served', async () => {
    servedFlag.value = true;
    const listServers = vi.fn(async () => []);
    const listToolPolicies = vi.fn(async () => []);
    const client = createFakeHarnessClient({
      mcp: {
        listServers,
        startStream: async () => 'fake-mcp-sub',
        stopStream: async () => undefined,
        healthSnapshot: async () => ({}),
        subscribeHealthChanges: async () => 'fake-health-sub',
        listToolPolicies,
        setToolPolicy: vi.fn(async () => undefined),
      } as any,
    });
    const w = mount(ToolsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(w.find('[data-testid=tools-empty]').exists()).toBe(false);
    expect(w.find('[data-testid=tools-table]').exists()).toBe(false);
    expect(listServers).not.toHaveBeenCalled();
    expect(listToolPolicies).not.toHaveBeenCalled();
  });
});

// trust-surfaces-that-fire-01PMZ202 WP24 (CHAT-05): the per-server
// policy control is the shipped surface that makes a confirm_each
// verdict producible at all. These pin the UI half; the production
// path from a written rule to an actual parked tool call is proven in
// Go (core/rpc/views/agentgraph/chat — real toolloop resolver, real
// file on disk, real ConfirmBus).
describe('ToolsView — tool policy control (WP24, CHAT-05)', () => {
  const seed: MCPServer[] = [
    { id: 'fs', name: 'filesystem', state: 'ready', version: '0.1.0' },
  ];

  it('defaults the policy select to auto_allow when no rule is persisted', async () => {
    const { client } = provide(seed, []);
    const w = mount(ToolsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    const select = w.find('[data-testid=tool-policy-select-filesystem]');
    expect(select.exists()).toBe(true);
    expect((select.element as HTMLSelectElement).value).toBe('auto_allow');
  });

  it('seeds the select from a persisted confirm_each rule', async () => {
    const { client } = provide(seed, [
      { server: 'filesystem', tool: '*', policy: 'confirm_each', reason: 'fs default' },
    ]);
    const w = mount(ToolsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    const select = w.find('[data-testid=tool-policy-select-filesystem]');
    expect((select.element as HTMLSelectElement).value).toBe('confirm_each');
  });

  it('calls setToolPolicy with a whole-server ("*") rule when the select changes', async () => {
    const { client, setToolPolicy } = provide(seed, []);
    const w = mount(ToolsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    const select = w.find('[data-testid=tool-policy-select-filesystem]');
    await select.setValue('confirm_each');
    await flushPromises();
    expect(setToolPolicy).toHaveBeenCalledWith(
      'filesystem',
      '*',
      'confirm_each',
      expect.any(String),
    );
  });

  it('surfaces a policy save failure without crashing the view', async () => {
    const { client } = provide(seed, [], {
      setToolPolicy: async () => {
        throw new Error('disk full');
      },
    });
    const w = mount(ToolsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    const select = w.find('[data-testid=tool-policy-select-filesystem]');
    await select.setValue('deny');
    await flushPromises();
    const err = w.find('[data-testid=tool-policy-error]');
    expect(err.exists()).toBe(true);
    expect(err.text()).toContain('disk full');
  });
});
