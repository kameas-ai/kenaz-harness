/**
 * MemoryCaptureToggle — knowledge-home-01DOGF0E WP03 (pins P-2, P-3, P-4).
 *
 * P-2: toggling calls setMemory then the starter-hook install/remove, in that
 *      order, exactly as KenazToolsPanel's toggleMemory did (optimistic flip,
 *      rollback on failure).
 * P-3: Tools no longer offers a memory toggle — only a pointer to Learned.
 * P-4: memory off → Learned shows the off banner (and still lists chunks).
 * FR-5: setting on but a starter hook missing → partial-install warning with
 *      a repair action.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h } from 'vue';
import MemoryCaptureToggle from '@/views/memory/MemoryCaptureToggle.vue';
import MemoryView from '@/views/memory/MemoryView.vue';
import CapabilitySurface from '@/views/capabilities/CapabilitySurface.vue';
import { createFakeHarnessClient, type HarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { Hook, MemoryChunk } from '@/lib/types';

function starterHooks(opts: { retrieve?: boolean; persist?: boolean } = {}): Hook[] {
  const out: Hook[] = [];
  if (opts.retrieve !== false) {
    out.push({ id: 'starter:memory.retrieve', name: 'Memory · retrieve', event: 'pre_send', kind: 'builtin', enabled: true, match: {}, builtin: 'memory.retrieve' });
  }
  if (opts.persist !== false) {
    out.push({ id: 'starter:memory.persist', name: 'Memory · persist', event: 'post_send', kind: 'builtin', enabled: true, match: {}, builtin: 'memory.persist' });
  }
  return out;
}

// In-memory fake client, DELIBERATELY (WP-PI AC-PI-2): these tests pin which
// RPCs the UI calls and in what order, not storage. The real-storage round
// trip of memoryEnabled is core/rpc/views/settings/knowledge_home_pi_test.go
// (settings.FileStore seeded from the committed v0.64.0 settings.json).
function makeClient(initial: boolean, hooks: Hook[] = starterHooks()) {
  const base = createFakeHarnessClient();
  const calls: string[] = [];
  let current = initial;
  let installed = hooks;
  const client: HarnessClient = createFakeHarnessClient({
    settings: {
      ...base.settings,
      getMemory: async () => current,
      setMemory: async (v: boolean) => {
        calls.push(`setMemory(${v})`);
        current = v;
      },
    },
    hooks: {
      ...base.hooks,
      list: async () => installed,
      installStarterMemory: async () => {
        calls.push('installStarterMemory');
        installed = starterHooks();
      },
      removeStarterMemory: async () => {
        calls.push('removeStarterMemory');
        installed = [];
      },
    },
  });
  return { client, calls };
}

function mountToggle(client: HarnessClient) {
  return mount(MemoryCaptureToggle, {
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
}

async function flip(w: ReturnType<typeof mountToggle>, checked: boolean) {
  const input = w.find('[data-testid="memory-toggle"]');
  (input.element as HTMLInputElement).checked = checked;
  await input.trigger('change');
  await flushPromises();
}

describe('MemoryCaptureToggle (P-2)', () => {
  it('turning on calls setMemory(true) then installStarterMemory, in order', async () => {
    const { client, calls } = makeClient(false, []);
    const w = mountToggle(client);
    await flushPromises();
    await flip(w, true);
    expect(calls).toEqual(['setMemory(true)', 'installStarterMemory']);
    expect(w.find('[data-testid="memory-capture-state"]').text()).toBe('on');
    expect(w.find('[data-testid="memory-off-banner"]').exists()).toBe(false);
  });

  it('turning off calls setMemory(false) then removeStarterMemory, in order', async () => {
    const { client, calls } = makeClient(true);
    const w = mountToggle(client);
    await flushPromises();
    await flip(w, false);
    expect(calls).toEqual(['setMemory(false)', 'removeStarterMemory']);
    expect(w.find('[data-testid="memory-off-banner"]').exists()).toBe(true);
  });

  it('rolls the switch back and shows the error when setMemory fails', async () => {
    const { client } = makeClient(false, []);
    client.settings.setMemory = vi.fn(async () => {
      throw new Error('settings.json read-only');
    });
    const w = mountToggle(client);
    await flushPromises();
    await flip(w, true);
    expect(w.find('[data-testid="memory-capture-state"]').text()).toBe('off');
    expect(w.find('[data-testid="memory-toggle-error"]').text()).toContain('settings.json read-only');
  });

  it('setting saved but hook install failed → re-reads and shows the partial install immediately (review F7a)', async () => {
    const { client } = makeClient(false, []);
    client.hooks.installStarterMemory = vi.fn(async () => {
      throw new Error('registry offline');
    });
    const w = mountToggle(client);
    await flushPromises();
    await flip(w, true);
    // The truth after the failure: memoryEnabled=true on disk, no hooks.
    expect(w.find('[data-testid="memory-capture-state"]').text()).toBe('on');
    expect(w.find('[data-testid="memory-partial-install"]').exists()).toBe(true);
    expect(w.find('[data-testid="memory-toggle-error"]').text()).toContain('registry offline');
  });

  it('copy names the embeddings requirement (review F7b — core/memory/eligibility.go)', async () => {
    const { client } = makeClient(true);
    const w = mountToggle(client);
    await flushPromises();
    expect(w.text()).toContain('OpenAI, OpenRouter, Azure OpenAI, or a custom OpenAI-compatible endpoint');
  });
});

describe('MemoryCaptureToggle honesty (P-4, FR-5)', () => {
  it('memory off → the off banner says nothing is being captured', async () => {
    const { client } = makeClient(false, []);
    const w = mountToggle(client);
    await flushPromises();
    expect(w.find('[data-testid="memory-off-banner"]').text()).toContain(
      'Memory capture is off — nothing new is being captured',
    );
  });

  it('memory on with the persist hook missing → partial-install warning; Repair reinstalls', async () => {
    const { client, calls } = makeClient(true, starterHooks({ persist: false }));
    const w = mountToggle(client);
    await flushPromises();
    const warn = w.find('[data-testid="memory-partial-install"]');
    expect(warn.exists()).toBe(true);
    expect(warn.text()).toContain('memory.persist');
    expect(warn.text()).toContain('nothing new is being captured');
    await w.find('[data-testid="memory-repair-hooks"]').trigger('click');
    await flushPromises();
    expect(calls).toEqual(['installStarterMemory']);
    expect(w.find('[data-testid="memory-partial-install"]').exists()).toBe(false);
  });

  it('a failed setting read is reported, not rendered as "off"', async () => {
    const { client } = makeClient(false);
    client.settings.getMemory = async () => {
      throw new Error('settings.json unreadable');
    };
    const w = mountToggle(client);
    await flushPromises();
    expect(w.find('[data-testid="memory-off-banner"]').exists()).toBe(false);
    expect(w.find('[data-testid="memory-setting-load-error"]').text()).toContain('settings.json unreadable');
    expect(w.find('[data-testid="memory-toggle"]').attributes('disabled')).toBeDefined();
  });

  it('Learned (MemoryView) shows the off banner AND still lists existing chunks', async () => {
    const { client } = makeClient(false, []);
    const chunk = {
      id: 'c1', content: 'remember the deploy runbook', createdAt: '2026-10-04T00:00:00Z',
      scopeKind: 'global', scopeId: '', sessionId: 's1',
    } as unknown as MemoryChunk;
    client.memory.listChunks = async () => [chunk];
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/', component: defineComponent({ render: () => h('div') }) }],
    });
    await router.push('/');
    await router.isReady();
    const w = mount(MemoryView, {
      props: { embedded: true },
      global: { plugins: [router], provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(w.find('[data-testid="memory-off-banner"]').exists()).toBe(true);
    expect(w.text()).toContain('remember the deploy runbook');
    // Embedded: Knowledge owns the page header.
    expect(w.text()).not.toContain('Every snippet you have asked the harness to remember');
    w.unmount();
  });
});

describe('Tools no longer offers a memory toggle (P-3)', () => {
  it('the Tools surface (CapabilitySurface, formerly KenazToolsPanel) renders a pointer to Learned and no memory switch', async () => {
    const { client, calls } = makeClient(false);
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/', component: defineComponent({ render: () => null }) },
        { path: '/knowledge/learned', component: defineComponent({ render: () => null }) },
      ],
    });
    await router.push('/');
    await router.isReady();
    const w = mount(CapabilitySurface, {
      global: { plugins: [router], provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(w.find('[data-testid="memory-toggle"]').exists()).toBe(false);
    expect(w.find('[aria-label="Enable memory tool"]').exists()).toBe(false);
    expect(w.find('[data-testid="memory-moved-pointer"]').text()).toContain('Knowledge › Learned');
    await w.find('[data-testid="memory-view-link"]').trigger('click');
    await flushPromises();
    expect(router.currentRoute.value.path).toBe('/knowledge/learned');
    expect(calls).toEqual([]);
    w.unmount();
  });
});
