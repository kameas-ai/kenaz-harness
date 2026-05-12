import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import DeferredAskPill from '../DeferredAskPill.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import type { ElicitRequest } from '@/lib/types';

// Stub useEventStream so we can control events manually.
const eventHandlers: Record<string, ((p: unknown) => void)[]> = {};

vi.mock('@/lib/useEventStream', () => ({
  useEventStream: (topic: string, handler: (p: unknown) => void) => {
    if (!eventHandlers[topic]) eventHandlers[topic] = [];
    eventHandlers[topic].push(handler);
  },
}));

function fireEvent(topic: string, payload: unknown) {
  for (const h of eventHandlers[topic] ?? []) h(payload);
}

function makeWrapper() {
  return mount(DeferredAskPill, {
    attachTo: document.body,
    global: {
      plugins: [
        {
          install(app) {
            provideFakeClient(app, {
              elicit: {
                submitAnswer: vi.fn(),
                submitWizardStep: vi.fn(),
                registerDeferred: vi.fn(),
                answerDeferred: vi.fn().mockResolvedValue(''),
                listPending: vi.fn().mockResolvedValue([]),
              },
            });
          },
        },
      ],
    },
  });
}

const fakeAsk: ElicitRequest = {
  request_id: 'ask-d1',
  question: 'Deploy to prod?',
  kind: 'text',
  mode: 'deferred',
};

beforeEach(() => {
  for (const k of Object.keys(eventHandlers)) delete eventHandlers[k];
});

describe('DeferredAskPill', () => {
  it('is hidden when no deferred asks', () => {
    const wrapper = makeWrapper();
    expect(wrapper.find('[data-testid="deferred-ask-pill"]').exists()).toBe(false);
  });

  it('shows pill when a deferred ask arrives', async () => {
    const wrapper = makeWrapper();
    fireEvent('elicit:deferred', fakeAsk);
    await flushPromises();
    const pill = wrapper.find('[data-testid="deferred-ask-pill"]');
    expect(pill.exists()).toBe(true);
    expect(pill.text()).toContain('1');
  });

  it('opens panel on pill click', async () => {
    const wrapper = makeWrapper();
    fireEvent('elicit:deferred', fakeAsk);
    await flushPromises();
    await wrapper.find('[data-testid="deferred-ask-pill"]').trigger('click');
    expect(wrapper.find('[data-testid="deferred-ask-panel"]').exists()).toBe(true);
  });

  it('removes ask when answered event arrives', async () => {
    const wrapper = makeWrapper();
    fireEvent('elicit:deferred', fakeAsk);
    await flushPromises();
    fireEvent('elicit:deferred:answered', { ask_id: 'ask-d1' });
    await flushPromises();
    expect(wrapper.find('[data-testid="deferred-ask-pill"]').exists()).toBe(false);
  });

  it('shows count when multiple asks arrive', async () => {
    const wrapper = makeWrapper();
    fireEvent('elicit:deferred', fakeAsk);
    fireEvent('elicit:deferred', { ...fakeAsk, request_id: 'ask-d2', question: 'Another?' });
    await flushPromises();
    expect(wrapper.find('[data-testid="deferred-ask-pill"]').text()).toContain('2');
  });
});
