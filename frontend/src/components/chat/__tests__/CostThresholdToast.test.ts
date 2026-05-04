import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import CostThresholdToast from '@/components/chat/CostThresholdToast.vue';
import { setConnectionState } from '@/lib/useConnectionState';
import type { CostThresholdCrossedPayload } from '@/lib/types';

interface FakeRuntime {
  EventsOn: (topic: string, cb: (payload: unknown) => void) => () => void;
  emit: (topic: string, payload: unknown) => void;
  handlers: Map<string, Set<(payload: unknown) => void>>;
}

function installFakeRuntime(): FakeRuntime {
  const handlers = new Map<string, Set<(payload: unknown) => void>>();
  const rt: FakeRuntime = {
    handlers,
    EventsOn: (topic, cb) => {
      let s = handlers.get(topic);
      if (!s) {
        s = new Set();
        handlers.set(topic, s);
      }
      s.add(cb);
      return () => s!.delete(cb);
    },
    emit: (topic, payload) => {
      const s = handlers.get(topic);
      if (!s) return;
      for (const cb of s) cb(payload);
    },
  };
  (window as unknown as { runtime: FakeRuntime }).runtime = rt;
  return rt;
}

function uninstallRuntime() {
  delete (window as unknown as { runtime?: unknown }).runtime;
}

function mountToast(initial: CostThresholdCrossedPayload | null = null) {
  return mount(CostThresholdToast, { props: { initial } });
}

const samplePayload: CostThresholdCrossedPayload = {
  pct: 80,
  monthTotalUsd: 8.5,
  thresholdUsd: 10,
  yearMonth: '2026-05',
  firedAt: '2026-05-01T12:00:00Z',
};

describe('CostThresholdToast', () => {
  beforeEach(() => {
    installFakeRuntime();
    setConnectionState('ready');
  });
  afterEach(() => {
    uninstallRuntime();
  });

  it('renders nothing when no threshold is queued', () => {
    const w = mountToast();
    expect(w.find('[data-testid="cost-threshold-toast"]').exists()).toBe(false);
  });

  it('renders the toast for an initial seed', () => {
    const w = mountToast(samplePayload);
    expect(w.find('[data-testid="cost-threshold-toast"]').exists()).toBe(true);
    expect(w.text()).toContain('80%');
    expect(w.text()).toContain('$8.50');
    expect(w.text()).toContain('$10.00');
    expect(w.text()).toContain('2026-05');
  });

  it('appears when a backend event fires on the broker topic', async () => {
    const rt = installFakeRuntime();
    const w = mountToast();
    rt.emit('cost.threshold.crossed', samplePayload);
    await flushPromises();
    expect(w.find('[data-testid="cost-threshold-toast"]').exists()).toBe(true);
  });

  it('escalates copy at the 100% tier', () => {
    const w = mountToast({ ...samplePayload, pct: 100 });
    expect(w.find('[data-testid="cost-threshold-headline"]').text()).toContain(
      'Monthly budget reached',
    );
  });

  it('escalates copy at the 200% tier', () => {
    const w = mountToast({ ...samplePayload, pct: 200 });
    expect(w.find('[data-testid="cost-threshold-headline"]').text()).toContain(
      '2× over your monthly budget',
    );
  });

  it('emits dismissed and clears the toast on click', async () => {
    const w = mountToast(samplePayload);
    await w.find('[data-testid="cost-threshold-dismiss"]').trigger('click');
    expect(w.emitted('dismissed')?.[0]).toEqual([80]);
    expect(w.find('[data-testid="cost-threshold-toast"]').exists()).toBe(false);
  });

  it('dedupes duplicate events for the same (yearMonth, pct)', async () => {
    const rt = installFakeRuntime();
    const w = mountToast();
    rt.emit('cost.threshold.crossed', samplePayload);
    rt.emit('cost.threshold.crossed', samplePayload);
    rt.emit('cost.threshold.crossed', samplePayload);
    await flushPromises();
    // After dismissing once, the queue is empty (the duplicates were
    // ignored at enqueue time so there's nothing else to surface).
    await w.find('[data-testid="cost-threshold-dismiss"]').trigger('click');
    expect(w.find('[data-testid="cost-threshold-toast"]').exists()).toBe(false);
  });

  it('queues distinct tiers and surfaces them one at a time', async () => {
    const rt = installFakeRuntime();
    const w = mountToast();
    rt.emit('cost.threshold.crossed', { ...samplePayload, pct: 50 });
    rt.emit('cost.threshold.crossed', { ...samplePayload, pct: 80 });
    rt.emit('cost.threshold.crossed', { ...samplePayload, pct: 100 });
    await flushPromises();
    // Head is the first one queued (50%).
    expect(w.find('[data-testid="cost-threshold-headline"]').text()).toContain(
      '50%',
    );
    await w.find('[data-testid="cost-threshold-dismiss"]').trigger('click');
    expect(w.find('[data-testid="cost-threshold-headline"]').text()).toContain(
      '80%',
    );
    await w.find('[data-testid="cost-threshold-dismiss"]').trigger('click');
    expect(w.find('[data-testid="cost-threshold-headline"]').text()).toContain(
      'Monthly budget reached',
    );
  });
});
