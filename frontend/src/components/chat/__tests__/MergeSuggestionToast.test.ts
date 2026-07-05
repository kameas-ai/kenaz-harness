import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import MergeSuggestionToast from '@/components/chat/MergeSuggestionToast.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import type { HarnessClient } from '@/lib/harnessClient';
import { setConnectionState } from '@/lib/useConnectionState';

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

function mountToast(seed: Partial<HarnessClient> = {}, initial: { branchId: string; reason: string; detail?: string } | null = null) {
  return mount(MergeSuggestionToast, {
    props: { initial },
    global: {
      plugins: [
        {
          install(app) {
            provideFakeClient(app, seed);
          },
        },
      ],
    },
  });
}

describe('MergeSuggestionToast', () => {
  beforeEach(() => {
    installFakeRuntime();
    setConnectionState('ready');
  });
  afterEach(() => {
    uninstallRuntime();
  });

  it('renders nothing when no suggestion is queued', () => {
    const w = mountToast();
    expect(w.find('[data-testid="merge-suggestion-toast"]').exists()).toBe(false);
  });

  it('renders the toast for an initial suggestion', () => {
    const w = mountToast({}, {
      branchId: 'br-1',
      reason: 'terminal_state_token',
      detail: 'Branch said Done.',
    });
    expect(w.find('[data-testid="merge-suggestion-toast"]').exists()).toBe(true);
    expect(w.text()).toContain('Branch reply looks terminal');
  });

  it('appears when a backend event fires', async () => {
    const rt = installFakeRuntime();
    const w = mountToast();
    rt.emit('branches:merge-suggested', {
      branchId: 'br-2',
      reason: 'idle_timeout',
    });
    await flushPromises();
    expect(w.find('[data-testid="merge-suggestion-toast"]').exists()).toBe(true);
  });

  it('calls merge on accept and emits merged', async () => {
    const merge = vi.fn().mockResolvedValue(undefined);
    const w = mountToast(
      {
        branches: {
          list: vi.fn(),
          create: vi.fn(),
          createExplicit: vi.fn(),
          status: vi.fn(),
          merge,
          abandon: vi.fn(),
          recommendModel: vi.fn(),
          proposeReintegrationSummary: vi.fn(),
          commitReintegration: vi.fn(),
          setAdvisorDismissed: vi.fn(),
          listWithBranchTree: vi.fn(),
        },
      },
      { branchId: 'br-1', reason: 'terminal_state_token' },
    );
    await w.find('[data-testid="merge-suggestion-merge"]').trigger('click');
    await flushPromises();
    expect(merge).toHaveBeenCalledWith('br-1');
    expect(w.emitted('merged')?.[0]).toEqual(['br-1']);
  });

  it('emits dismissed on Keep working', async () => {
    const w = mountToast({}, { branchId: 'br-1', reason: 'idle_timeout' });
    await w.find('[data-testid="merge-suggestion-keep"]').trigger('click');
    expect(w.emitted('dismissed')?.[0]).toEqual(['br-1']);
  });
});
