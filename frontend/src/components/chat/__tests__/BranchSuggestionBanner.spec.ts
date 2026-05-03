/**
 * BranchSuggestionBanner + ChatInput branch-advisor integration tests.
 *
 * Coverage (WP02 acceptance criteria):
 *  1. Banner renders when confidence threshold met.
 *  2. 800ms debounce — banner suppressed before timeout fires.
 *  3. "No thanks" button emits dismiss with scope "message".
 *  4. "Don't suggest again" button emits dismiss with scope "session" and
 *     calls client.branches.setAdvisorDismissed + writes sessionStorage.
 *  5. sessionStorage key persists the session-scoped dismiss.
 *  6. "Branch it off" button emits branchOff.
 *  7. Session-dismissed state hides the banner.
 *  8. Streaming active — banner suppressed even when signal fires.
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import BranchSuggestionBanner from '@/components/chat/BranchSuggestionBanner.vue';
import ChatInput from '@/components/chat/ChatInput.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import type { HarnessClient } from '@/lib/harnessClient';
import type { BranchSuggestion } from '@/lib/types';

// ── BranchSuggestionBanner unit tests ────────────────────────────────

function makeSuggestion(overrides: Partial<BranchSuggestion> = {}): BranchSuggestion {
  return {
    id: 'sugg-001',
    confidence: 0.9,
    rationale: 'Signals detected: can_you_also',
    signals: ['can_you_also'],
    proposedTitle: 'figure out X',
    ...overrides,
  };
}

describe('BranchSuggestionBanner (unit)', () => {
  it('renders the banner with the three action buttons', () => {
    const w = mount(BranchSuggestionBanner, {
      props: { suggestion: makeSuggestion() },
    });
    expect(w.find('[data-testid="branch-suggestion-banner"]').exists()).toBe(true);
    expect(w.find('[data-testid="branch-banner-branch-off"]').exists()).toBe(true);
    expect(w.find('[data-testid="branch-banner-no-thanks"]').exists()).toBe(true);
    expect(w.find('[data-testid="branch-banner-dont-suggest"]').exists()).toBe(true);
  });

  it('emits branchOff with the suggestion when "Branch it off" is clicked', async () => {
    const suggestion = makeSuggestion();
    const w = mount(BranchSuggestionBanner, { props: { suggestion } });
    await w.find('[data-testid="branch-banner-branch-off"]').trigger('click');
    const emitted = w.emitted('branchOff');
    expect(emitted).toBeTruthy();
    expect(emitted![0][0]).toEqual(suggestion);
  });

  it('emits dismiss with scope "message" when "No thanks" is clicked', async () => {
    const suggestion = makeSuggestion({ id: 'sugg-abc' });
    const w = mount(BranchSuggestionBanner, { props: { suggestion } });
    await w.find('[data-testid="branch-banner-no-thanks"]').trigger('click');
    const emitted = w.emitted('dismiss');
    expect(emitted).toBeTruthy();
    expect(emitted![0]).toEqual(['message', 'sugg-abc']);
  });

  it('emits dismiss with scope "session" when "Don\'t suggest again" is clicked', async () => {
    const suggestion = makeSuggestion({ id: 'sugg-xyz' });
    const w = mount(BranchSuggestionBanner, { props: { suggestion } });
    await w.find('[data-testid="branch-banner-dont-suggest"]').trigger('click');
    const emitted = w.emitted('dismiss');
    expect(emitted).toBeTruthy();
    expect(emitted![0]).toEqual(['session', 'sugg-xyz']);
  });

  it('shows the rationale tooltip when "Why?" is clicked', async () => {
    const w = mount(BranchSuggestionBanner, {
      props: { suggestion: makeSuggestion({ signals: ['can_you_also'] }) },
    });
    expect(w.find('[data-testid="branch-banner-rationale"]').exists()).toBe(false);
    await w.find('[data-testid="branch-banner-rationale-trigger"]').trigger('click');
    expect(w.find('[data-testid="branch-banner-rationale"]').exists()).toBe(true);
  });

  it('hides the "Why?" trigger when signals list is empty', () => {
    const w = mount(BranchSuggestionBanner, {
      props: { suggestion: makeSuggestion({ signals: [] }) },
    });
    expect(w.find('[data-testid="branch-banner-rationale-trigger"]').exists()).toBe(false);
  });
});

// ── ChatInput + advisor integration tests ────────────────────────────

function mountInput(
  props: Record<string, unknown> = {},
  seed: Partial<HarnessClient> = {},
) {
  return mount(ChatInput, {
    props,
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

beforeEach(() => {
  vi.useFakeTimers();
  // happy-dom stubs
  if (typeof URL.createObjectURL !== 'function') {
    Object.defineProperty(URL, 'createObjectURL', {
      configurable: true,
      value: vi.fn(() => 'blob://fake'),
    });
  }
  if (typeof URL.revokeObjectURL !== 'function') {
    Object.defineProperty(URL, 'revokeObjectURL', {
      configurable: true,
      value: vi.fn(),
    });
  }
  sessionStorage.clear();
});

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  sessionStorage.clear();
});

describe('ChatInput branch-advisor', () => {
  it('banner is hidden before the debounce window elapses', async () => {
    const w = mountInput({ sessionId: 'sess-1' });
    const textarea = w.find('textarea');
    await textarea.setValue('can you also figure out the license');
    await textarea.trigger('input');
    // Banner must NOT appear yet (debounce is 800ms)
    expect(w.find('[data-testid="branch-suggestion-banner"]').exists()).toBe(false);
  });

  it('banner appears after 800ms debounce when positive signal matched', async () => {
    const w = mountInput({ sessionId: 'sess-1' });
    const textarea = w.find('textarea');
    await textarea.setValue('can you also figure out the license');
    await textarea.trigger('input');
    vi.advanceTimersByTime(800);
    await flushPromises();
    expect(w.find('[data-testid="branch-suggestion-banner"]').exists()).toBe(true);
  });

  it('banner does not appear for plain text without signals', async () => {
    const w = mountInput({ sessionId: 'sess-1' });
    const textarea = w.find('textarea');
    await textarea.setValue('what is the weather today');
    await textarea.trigger('input');
    vi.advanceTimersByTime(800);
    await flushPromises();
    expect(w.find('[data-testid="branch-suggestion-banner"]').exists()).toBe(false);
  });

  it('rapid typing within 800ms suppresses the banner (debounce race guard)', async () => {
    const w = mountInput({ sessionId: 'sess-1' });
    const textarea = w.find('textarea');
    // First keystroke
    await textarea.setValue('can you also');
    await textarea.trigger('input');
    vi.advanceTimersByTime(400);
    // Second keystroke before debounce fires
    await textarea.setValue('can you also do this other thing');
    await textarea.trigger('input');
    vi.advanceTimersByTime(400);
    // Only 400ms since last input — banner should still be hidden
    await flushPromises();
    expect(w.find('[data-testid="branch-suggestion-banner"]').exists()).toBe(false);
    // Now let the full 800ms expire from the second input
    vi.advanceTimersByTime(400);
    await flushPromises();
    expect(w.find('[data-testid="branch-suggestion-banner"]').exists()).toBe(true);
  });

  it('"No thanks" hides the banner (message-scope dismiss)', async () => {
    const w = mountInput({ sessionId: 'sess-1' });
    const textarea = w.find('textarea');
    await textarea.setValue('by the way can you also check the logs');
    await textarea.trigger('input');
    vi.advanceTimersByTime(800);
    await flushPromises();
    expect(w.find('[data-testid="branch-suggestion-banner"]').exists()).toBe(true);
    await w.find('[data-testid="branch-banner-no-thanks"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="branch-suggestion-banner"]').exists()).toBe(false);
  });

  it('"Don\'t suggest again" hides the banner, writes sessionStorage, and calls setAdvisorDismissed', async () => {
    const setAdvisorDismissed = vi.fn().mockResolvedValue(undefined);
    const w = mountInput(
      { sessionId: 'sess-42' },
      {
        branches: {
          list: vi.fn(),
          create: vi.fn(),
          status: vi.fn(),
          merge: vi.fn(),
          abandon: vi.fn(),
          recommendModel: vi.fn().mockResolvedValue({ providerId: '', modelId: '', tier: 'small', reason: 'default' }),
          proposeReintegrationSummary: vi.fn().mockResolvedValue({ proposedSummary: '', tokenCount: 0, model: '' }),
          commitReintegration: vi.fn().mockResolvedValue(undefined),
          setAdvisorDismissed,
        },
      },
    );
    const textarea = w.find('textarea');
    await textarea.setValue('quick aside — can you also check this');
    await textarea.trigger('input');
    vi.advanceTimersByTime(800);
    await flushPromises();
    expect(w.find('[data-testid="branch-suggestion-banner"]').exists()).toBe(true);

    await w.find('[data-testid="branch-banner-dont-suggest"]').trigger('click');
    await flushPromises();

    // Banner gone
    expect(w.find('[data-testid="branch-suggestion-banner"]').exists()).toBe(false);
    // sessionStorage key written
    expect(sessionStorage.getItem('branchAdvisorDismissed:sess-42')).toBe('true');
    // Backend called
    expect(setAdvisorDismissed).toHaveBeenCalledWith('sess-42', true);
  });

  it('sessionStorage dismiss prevents banner on re-trigger', async () => {
    sessionStorage.setItem('branchAdvisorDismissed:sess-99', 'true');
    const w = mountInput({ sessionId: 'sess-99' });
    // Wait for onMounted to read sessionStorage
    await flushPromises();
    const textarea = w.find('textarea');
    await textarea.setValue('can you also figure out the build');
    await textarea.trigger('input');
    vi.advanceTimersByTime(800);
    await flushPromises();
    // Even after full debounce, no banner because session is dismissed
    expect(w.find('[data-testid="branch-suggestion-banner"]').exists()).toBe(false);
  });

  it('banner is suppressed while streaming is true', async () => {
    const w = mountInput({ sessionId: 'sess-1', streaming: true });
    const textarea = w.find('textarea');
    await textarea.setValue('can you also figure out X');
    await textarea.trigger('input');
    vi.advanceTimersByTime(800);
    await flushPromises();
    expect(w.find('[data-testid="branch-suggestion-banner"]').exists()).toBe(false);
  });

  it('send clears the banner immediately (race guard on send)', async () => {
    const w = mountInput({ sessionId: 'sess-1' });
    const textarea = w.find('textarea');
    await textarea.setValue('can you also check this thing');
    await textarea.trigger('input');
    vi.advanceTimersByTime(800);
    await flushPromises();
    expect(w.find('[data-testid="branch-suggestion-banner"]').exists()).toBe(true);
    // User sends
    await textarea.trigger('keydown', { key: 'Enter' });
    await flushPromises();
    expect(w.find('[data-testid="branch-suggestion-banner"]').exists()).toBe(false);
  });
});
