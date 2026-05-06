/**
 * MemoryCaptureRatePill — §2.7 LegendBar capture-rate widget tests.
 *
 * Covers:
 *   1. Hidden when chunksPerMinute === 0 and no errors.
 *   2. Visible when chunksPerMinute > 0.
 *   3. Visible when recentErrorCount > 0 (even at 0 chunks/min).
 *   4. Shows correct rate label.
 *   5. Dot is green on "ok" health.
 *   6. Dot is amber on "slow" health.
 *   7. Dot is red on "error" health.
 *   8. Click navigates to /memory.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h } from 'vue';
import MemoryCaptureRatePill from '@/shell/MemoryCaptureRatePill.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import type { MemoryCaptureRateSnapshot } from '@/lib/types';

const memRoute = { path: '/memory', component: defineComponent({ render: () => h('div', 'memory') }) };
const otherRoute = { path: '/', component: defineComponent({ render: () => h('div', 'home') }) };

async function mountPill(snap: Partial<MemoryCaptureRateSnapshot> = {}) {
  const fullSnap: MemoryCaptureRateSnapshot = {
    chunksPerMinute: 0,
    embedderHealth: 'ok',
    lastErrorAt: null,
    recentErrorCount: 0,
    ...snap,
  };
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [otherRoute, memRoute],
  });
  await router.push('/');
  await router.isReady();

  const w = mount(MemoryCaptureRatePill, {
    global: {
      plugins: [
        router,
        {
          install(app) {
            provideFakeClient(app, {
              memory: {
                captureRate: vi.fn().mockResolvedValue(fullSnap),
              } as any,
            });
          },
        },
      ],
    },
  });
  await flushPromises();
  return { w, router };
}

describe('MemoryCaptureRatePill (§2.7)', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  it('is hidden when chunksPerMinute === 0 and no errors', async () => {
    const { w } = await mountPill({ chunksPerMinute: 0, recentErrorCount: 0 });
    expect(w.find('button').exists()).toBe(false);
  });

  it('is visible when chunksPerMinute > 0', async () => {
    const { w } = await mountPill({ chunksPerMinute: 3 });
    expect(w.find('button').exists()).toBe(true);
  });

  it('is visible when recentErrorCount > 0 even at 0 chunks/min', async () => {
    const { w } = await mountPill({ chunksPerMinute: 0, recentErrorCount: 4, embedderHealth: 'error' });
    expect(w.find('button').exists()).toBe(true);
  });

  it('displays rounded chunks/min label', async () => {
    const { w } = await mountPill({ chunksPerMinute: 7.8 });
    expect(w.text()).toContain('8/min');
  });

  it('dot is green on "ok" health', async () => {
    const { w } = await mountPill({ chunksPerMinute: 2, embedderHealth: 'ok' });
    const dot = w.find('.rounded-full');
    expect(dot.classes()).toContain('bg-emerald-500');
  });

  it('dot is amber on "slow" health', async () => {
    const { w } = await mountPill({ chunksPerMinute: 2, embedderHealth: 'slow' });
    const dot = w.find('.rounded-full');
    expect(dot.classes()).toContain('bg-amber-400');
  });

  it('dot is red on "error" health', async () => {
    const { w } = await mountPill({ chunksPerMinute: 0, recentErrorCount: 4, embedderHealth: 'error' });
    const dot = w.find('.rounded-full');
    expect(dot.classes()).toContain('bg-red-500');
  });

  it('click navigates to /memory', async () => {
    const { w, router } = await mountPill({ chunksPerMinute: 5 });
    await w.find('button').trigger('click');
    await flushPromises();
    expect(router.currentRoute.value.path).toBe('/memory');
  });

  // ── v0.5.x audit: 5 s poll timer ─────────────────────────────────────

  it('polls captureRate again after 5 s (v0.5.x audit)', async () => {
    const captureRateMock = vi.fn().mockResolvedValue({
      chunksPerMinute: 3,
      embedderHealth: 'ok',
      lastErrorAt: null,
      recentErrorCount: 0,
    });
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [otherRoute, memRoute],
    });
    await router.push('/');
    await router.isReady();

    const w = mount(MemoryCaptureRatePill, {
      global: {
        plugins: [
          router,
          {
            install(app) {
              provideFakeClient(app, {
                memory: {
                  captureRate: captureRateMock,
                } as any,
              });
            },
          },
        ],
      },
    });

    // Initial poll on mount.
    await flushPromises();
    expect(captureRateMock).toHaveBeenCalledTimes(1);

    // Advance 5 s → second poll.
    vi.advanceTimersByTime(5000);
    await flushPromises();
    expect(captureRateMock).toHaveBeenCalledTimes(2);

    // Advance another 5 s → third poll.
    vi.advanceTimersByTime(5000);
    await flushPromises();
    expect(captureRateMock).toHaveBeenCalledTimes(3);

    w.unmount();
  });

  it('stops polling after unmount (no timer leak)', async () => {
    const captureRateMock = vi.fn().mockResolvedValue({
      chunksPerMinute: 1,
      embedderHealth: 'ok',
      lastErrorAt: null,
      recentErrorCount: 0,
    });
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [otherRoute, memRoute],
    });
    await router.push('/');
    await router.isReady();

    const w = mount(MemoryCaptureRatePill, {
      global: {
        plugins: [
          router,
          {
            install(app) {
              provideFakeClient(app, {
                memory: {
                  captureRate: captureRateMock,
                } as any,
              });
            },
          },
        ],
      },
    });

    await flushPromises();
    w.unmount();
    captureRateMock.mockClear();

    // Advance 30 s after unmount — no additional polls.
    vi.advanceTimersByTime(30_000);
    await flushPromises();
    expect(captureRateMock).not.toHaveBeenCalled();
  });
});
