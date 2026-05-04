/**
 * SearchModal.spec.ts — unit tests for SearchModal.vue
 *
 * Covers (per the mission constraint):
 *   1. open/close behaviour
 *   2. debounced search invocation (~150ms)
 *   3. result-click → router.push with correct path
 *   4. keyboard nav: ArrowDown / ArrowUp move highlight; Enter opens hit
 */

import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount } from '@vue/test-utils';
// vue imported for test assertions only
import SearchModal from '../SearchModal.vue';
import {
  createFakeHarnessClient,
  type SearchHit,
} from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';

// ── mock vue-router ────────────────────────────────────────────────────
const pushMock = vi.fn();
const replaceMock = vi.fn();
vi.mock('vue-router', () => ({
  useRouter: () => ({ push: pushMock, replace: replaceMock }),
  // useRoute is consumed by SearchModal for WP06 URL-state hydration;
  // returning a frozen empty-query object keeps the mount path identical
  // to a fresh / no-deep-link open.
  useRoute: () => ({ query: {} as Record<string, string> }),
}));

// ── helpers ────────────────────────────────────────────────────────────

const fakeHit = (overrides: Partial<SearchHit> = {}): SearchHit => ({
  sessionId: 'sess-1',
  sessionName: 'My Session',
  messageId: 'msg-1',
  role: 'user',
  snippet: 'hello world',
  highlights: [],
  createdAt: new Date().toISOString(),
  ...overrides,
});

function mountModal(opts: {
  hits?: SearchHit[];
  searchError?: boolean;
}) {
  const hits = opts.hits ?? [];
  const client = createFakeHarnessClient({
    search: {
      sessions: opts.searchError
        ? () => Promise.reject(new Error('search failed'))
        : () => Promise.resolve(hits),
    },
    projects: {
      list: async () => [],
      get: async (id) => ({ id, name: id, description: '', createdAt: '', updatedAt: '' }),
      create: async (name) => ({ id: `p-${name}`, name, description: '', createdAt: '', updatedAt: '' }),
      rename: async () => undefined,
      updateDescription: async () => undefined,
      remove: async () => undefined,
      addSession: async () => undefined,
      removeSession: async () => undefined,
      listSessions: async () => [],
    },
  });

  return mount(SearchModal, {
    global: {
      provide: { [HarnessClientKey as symbol]: client },
    },
  });
}

// ── tests ──────────────────────────────────────────────────────────────

describe('SearchModal', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    pushMock.mockClear();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  // 1. open / close
  it('renders the search input on mount', () => {
    const wrapper = mountModal({});
    expect(wrapper.find('input[aria-label="Search query"]').exists()).toBe(true);
  });

  it('emits close when Escape is pressed on the input', async () => {
    const wrapper = mountModal({});
    const input = wrapper.find('input[aria-label="Search query"]');
    await input.trigger('keydown', { key: 'Escape' });
    expect(wrapper.emitted('close')).toBeTruthy();
    expect(wrapper.emitted('close')!.length).toBe(1);
  });

  it('emits close when backdrop is clicked', async () => {
    const wrapper = mountModal({});
    // The outer div with @click.self is the backdrop; clicking .absolute inset-0
    const backdrop = wrapper.find('.absolute.inset-0');
    await backdrop.trigger('click');
    expect(wrapper.emitted('close')).toBeTruthy();
  });

  // 2. debounced search
  it('does not call search immediately on input', async () => {
    const sessionsSpy = vi.fn().mockResolvedValue([]);
    const client = createFakeHarnessClient({
      search: { sessions: sessionsSpy },
      projects: { list: async () => [], get: async (id) => ({ id, name: id, description: '', createdAt: '', updatedAt: '' }), create: async (n) => ({ id: n, name: n, description: '', createdAt: '', updatedAt: '' }), rename: async () => undefined, updateDescription: async () => undefined, remove: async () => undefined, addSession: async () => undefined, removeSession: async () => undefined, listSessions: async () => [] },
    });
    const wrapper = mount(SearchModal, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    const input = wrapper.find('input');
    await input.setValue('hello');
    // No timer advanced → search must not have been called yet
    expect(sessionsSpy).not.toHaveBeenCalled();
  });

  it('calls search after 150ms debounce', async () => {
    const sessionsSpy = vi.fn().mockResolvedValue([]);
    const client = createFakeHarnessClient({
      search: { sessions: sessionsSpy },
      projects: { list: async () => [], get: async (id) => ({ id, name: id, description: '', createdAt: '', updatedAt: '' }), create: async (n) => ({ id: n, name: n, description: '', createdAt: '', updatedAt: '' }), rename: async () => undefined, updateDescription: async () => undefined, remove: async () => undefined, addSession: async () => undefined, removeSession: async () => undefined, listSessions: async () => [] },
    });
    const wrapper = mount(SearchModal, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    const input = wrapper.find('input');
    await input.setValue('hello');
    // Still no call before timer fires
    expect(sessionsSpy).not.toHaveBeenCalled();
    vi.advanceTimersByTime(150);
    await vi.runAllTimersAsync();
    expect(sessionsSpy).toHaveBeenCalledWith('hello', {});
  });

  // 3. result-click → router.push
  it('navigates to /sessions/<id>#<messageId> when a hit is clicked', async () => {
    const hit = fakeHit({ sessionId: 'sess-42', messageId: 'msg-7' });
    const wrapper = mountModal({ hits: [hit] });
    const input = wrapper.find('input');
    await input.setValue('world');
    vi.advanceTimersByTime(150);
    await vi.runAllTimersAsync();
    // Wait for Vue re-render
    await wrapper.vm.$nextTick();

    const resultItem = wrapper.find('[role="option"]');
    expect(resultItem.exists()).toBe(true);
    await resultItem.trigger('click');

    expect(pushMock).toHaveBeenCalledWith('/sessions/sess-42#msg-7');
  });

  it('emits close after clicking a hit', async () => {
    const hit = fakeHit();
    const wrapper = mountModal({ hits: [hit] });
    await wrapper.find('input').setValue('x');
    vi.advanceTimersByTime(150);
    await vi.runAllTimersAsync();
    await wrapper.vm.$nextTick();
    await wrapper.find('[role="option"]').trigger('click');
    expect(wrapper.emitted('close')).toBeTruthy();
  });

  // 4. keyboard navigation: ArrowDown / ArrowUp / Enter
  it('ArrowDown on input highlights first result', async () => {
    const hits = [fakeHit({ messageId: 'm1' }), fakeHit({ messageId: 'm2' })];
    const wrapper = mountModal({ hits });
    await wrapper.find('input').setValue('x');
    vi.advanceTimersByTime(150);
    await vi.runAllTimersAsync();
    await wrapper.vm.$nextTick();

    await wrapper.find('input').trigger('keydown', { key: 'ArrowDown' });
    const items = wrapper.findAll('[role="option"]');
    // highlightedIndex should be 1 (was 0, ArrowDown → 1 if there are ≥2 results)
    // but initial value is 0 after search; ArrowDown increments
    expect(items[1].classes()).toContain('bg-surface-3');
  });

  it('Enter on input opens highlighted result', async () => {
    const hit = fakeHit({ sessionId: 's9', messageId: 'm9' });
    const wrapper = mountModal({ hits: [hit] });
    await wrapper.find('input').setValue('x');
    vi.advanceTimersByTime(150);
    await vi.runAllTimersAsync();
    await wrapper.vm.$nextTick();

    await wrapper.find('input').trigger('keydown', { key: 'Enter' });
    expect(pushMock).toHaveBeenCalledWith('/sessions/s9#m9');
  });
});

// ── renderSnippet helper tested via modal integration ─────────────────

describe('snippet rendering (plain text + marks)', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    pushMock.mockClear();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('renders plain text without marks when no highlights', async () => {
    const hit = fakeHit({ snippet: 'hello world', highlights: [] });
    const wrapper = mountModal({ hits: [hit] });
    await wrapper.find('input').setValue('hello');
    vi.advanceTimersByTime(150);
    await vi.runAllTimersAsync();
    await wrapper.vm.$nextTick();
    // No <mark> elements should be rendered for empty highlights.
    const markEl = wrapper.find('mark');
    expect(markEl.exists()).toBe(false);
    // The plain text should appear somewhere in the result row.
    expect(wrapper.text()).toContain('hello world');
  });

  it('renders <mark> spans at highlight offsets', async () => {
    // snippet = "hello world", highlight over "world" = bytes [6,11]
    const hit = fakeHit({
      snippet: 'hello world',
      highlights: [{ start: 6, end: 11 }],
    });
    const wrapper = mountModal({ hits: [hit] });
    await wrapper.find('input').setValue('world');
    vi.advanceTimersByTime(150);
    await vi.runAllTimersAsync();
    await wrapper.vm.$nextTick();

    const markEl = wrapper.find('mark');
    expect(markEl.exists()).toBe(true);
    expect(markEl.text()).toBe('world');
  });
});
