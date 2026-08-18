/**
 * ChatInput.branchAdvisor.spec.ts — the branch-advisor master switch
 * (engineer-truth-pass-01PMTP01 WP02, finding B2).
 *
 * Settings.BranchAdvisorEnabled's Go docstring says "When false the banner
 * never mounts, regardless of confidence score. Default false." — but
 * before WP02 nothing read the field: ChatInput.vue's onMounted hydrated
 * only branchAdvisorMinConfidence, and runAdvisorDetector gated on
 * `props.streaming` / session-dismissed / signal match / threshold, never
 * on the master switch. The banner mounted on every matching message for
 * every user regardless of the documented default.
 *
 * Both directions are asserted so the test cannot pass by simply never
 * rendering the banner.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import ChatInput from '@/components/chat/ChatInput.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { Settings } from '@/lib/types';

const ADVISOR_DEBOUNCE_MS = 800;

function buildSettings(overrides: Partial<Settings> = {}): Settings {
  return {
    schemaVersion: 1,
    lastRoute: '/sessions',
    theme: 'dark',
    accent: 'default',
    windowSize: { width: 1280, height: 800 },
    ...overrides,
  };
}

function mountInputWithSettings(overrides: Partial<Settings>) {
  const settings = buildSettings(overrides);
  const client = createFakeHarnessClient({
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    settings: { get: async () => settings } as any,
  });
  return mount(ChatInput, {
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
}

beforeEach(() => {
  if (typeof URL.createObjectURL !== 'function') {
    Object.defineProperty(URL, 'createObjectURL', {
      configurable: true,
      value: vi.fn(() => 'blob://fake'),
    });
  }
});

afterEach(() => {
  vi.useRealTimers();
});

describe('ChatInput — branch advisor master switch (B2 / FR-002)', () => {
  it('with branchAdvisorEnabled false, a positive-signal message produces no banner', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const w = mountInputWithSettings({ branchAdvisorEnabled: false });
    await flushPromises();

    const textarea = w.find('textarea');
    await textarea.setValue('by the way, can you also check the logs');

    await vi.advanceTimersByTimeAsync(ADVISOR_DEBOUNCE_MS + 50);
    await flushPromises();

    expect(w.find('[data-testid="chat-input-branch-banner"]').exists()).toBe(false);
  });

  it('with branchAdvisorEnabled true, the same input produces the banner', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const w = mountInputWithSettings({ branchAdvisorEnabled: true });
    await flushPromises();

    const textarea = w.find('textarea');
    await textarea.setValue('by the way, can you also check the logs');

    await vi.advanceTimersByTimeAsync(ADVISOR_DEBOUNCE_MS + 50);
    await flushPromises();

    expect(w.find('[data-testid="chat-input-branch-banner"]').exists()).toBe(true);
  });
});
