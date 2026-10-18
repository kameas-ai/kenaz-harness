/**
 * MemoryView ?chunk=<id> — knowledge-home-01DOGF0E review F3a.
 * Audit cross-reference links target one chunk; Learned highlights it, or
 * says plainly that it is not in the list.
 */
import { describe, it, expect } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h } from 'vue';
import MemoryView from '@/views/memory/MemoryView.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { MemoryChunk } from '@/lib/types';

const chunk = (id: string, content: string) =>
  ({ id, content, createdAt: '2026-10-04T00:00:00Z', scopeKind: 'global', scopeId: '', sessionId: 's1' }) as unknown as MemoryChunk;

async function mountAt(path: string) {
  const client = createFakeHarnessClient();
  client.memory.listChunks = async () => [chunk('c1', 'first'), chunk('c2', 'second')];
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/knowledge/learned', component: defineComponent({ render: () => h('div') }) }],
  });
  await router.push(path);
  await router.isReady();
  const w = mount(MemoryView, {
    props: { embedded: true },
    global: { plugins: [router], provide: { [HarnessClientKey as symbol]: client } },
  });
  await flushPromises();
  return w;
}

describe('MemoryView linked chunk', () => {
  it('highlights the targeted chunk', async () => {
    const w = await mountAt('/knowledge/learned?chunk=c2');
    expect(w.find('[data-testid="memory-chunk-c2"]').attributes('aria-current')).toBe('true');
    expect(w.find('[data-testid="memory-chunk-c1"]').attributes('aria-current')).toBeUndefined();
    expect(w.find('[data-testid="memory-linked-chunk-missing"]').exists()).toBe(false);
    w.unmount();
  });

  it('says so when the targeted chunk is not in the list', async () => {
    const w = await mountAt('/knowledge/learned?chunk=gone');
    expect(w.find('[data-testid="memory-linked-chunk-missing"]').text()).toContain('gone');
    w.unmount();
  });

  it('no ?chunk → no highlight, no notice', async () => {
    const w = await mountAt('/knowledge/learned');
    expect(w.find('[aria-current="true"]').exists()).toBe(false);
    expect(w.find('[data-testid="memory-linked-chunk-missing"]').exists()).toBe(false);
    w.unmount();
  });
});
