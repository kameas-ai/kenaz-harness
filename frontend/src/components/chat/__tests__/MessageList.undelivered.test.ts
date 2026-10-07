/**
 * undelivered-message-retry (owner dogfood 2026-10-07): the NOT DELIVERED
 * state lives ON the user message — a sticky badge with the classified
 * reason — and only the newest message carries Retry (the backend re-runs
 * the newest user row, so a Retry on an older one would run the wrong
 * message). The composer half is DeliveryBanner.
 */
import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import MessageList from '@/components/chat/MessageList.vue';
import DeliveryBanner from '@/components/chat/DeliveryBanner.vue';
import type { Message } from '@/lib/types';
import type { DeliveryFailure } from '@/lib/delivery';

function row(o: Partial<Message>): Message {
  return { id: 'm', sessionId: 's-1', role: 'user', content: 'hi', createdAt: '', ...o };
}

const PAY: DeliveryFailure = {
  turnSpanId: 'u-2',
  failureClass: 'user_actionable',
  code: 'payment_required',
  status: 402,
  provider: 'openrouter',
  summary: 'Out of credits with OpenRouter',
  message: 'This request requires more credits.',
};

const messages = [
  row({ id: 'u-1', content: 'first' }),
  row({ id: 'a-1', role: 'assistant', content: 'answer' }),
  row({ id: 'u-2', content: 'summarise the release notes' }),
];

describe('MessageList — NOT DELIVERED badge', () => {
  it('renders the classified reason on the undelivered message only', () => {
    const w = mount(MessageList, {
      props: { messages, undelivered: new Map([['u-2', PAY]]), retryMessageId: 'u-2' },
    });
    const badges = w.findAll('[data-testid="undelivered-badge"]');
    expect(badges).toHaveLength(1);
    expect(w.find('[data-message-id="u-2"] [data-testid="undelivered-badge"]').exists()).toBe(true);
    expect(w.find('[data-testid="undelivered-reason"]').text()).toBe(
      'Not delivered — Out of credits with OpenRouter. Add credits, then retry.',
    );
    expect(w.find('[data-testid="undelivered-provider-message"]').text()).toContain('requires more credits');
    // Not the generic "Send failed" transcript banner.
    expect(w.text()).not.toContain('Send failed');
  });

  it('Retry emits once with the message id; an older undelivered message gets no button', async () => {
    const older: DeliveryFailure = { ...PAY, turnSpanId: 'u-1' };
    const w = mount(MessageList, {
      props: {
        messages,
        undelivered: new Map([['u-1', older], ['u-2', PAY]]),
        retryMessageId: 'u-2',
      },
    });
    expect(w.findAll('[data-testid="undelivered-badge"]')).toHaveLength(2);
    const buttons = w.findAll('[data-testid="undelivered-retry"]');
    expect(buttons).toHaveLength(1);
    await buttons[0].trigger('click');
    expect(w.emitted('retry-delivery')).toEqual([['u-2']]);
  });

  it('shows the auto-retry countdown with Cancel instead of Retry while one is pending', async () => {
    const w = mount(MessageList, {
      props: {
        messages,
        undelivered: new Map<string, DeliveryFailure>([['u-2', { ...PAY, failureClass: 'transient', code: 'rate_limited' }]]),
        retryMessageId: 'u-2',
        autoRetry: { attempt: 2, max: 3, delayMs: 8000 },
      },
    });
    expect(w.find('[data-testid="undelivered-auto-retry"]').text()).toBe('Retrying (2/3) in 8s…');
    expect(w.find('[data-testid="undelivered-retry"]').exists()).toBe(false);
    await w.find('[data-testid="undelivered-cancel-retry"]').trigger('click');
    expect(w.emitted('cancel-retry')).toHaveLength(1);
  });

  it('renders nothing without the prop (classic callers unchanged)', () => {
    const w = mount(MessageList, { props: { messages } });
    expect(w.find('[data-testid="undelivered-badge"]').exists()).toBe(false);
  });
});

describe('DeliveryBanner', () => {
  it('shows the reason + Retry; Open settings only for key problems', async () => {
    const w = mount(DeliveryBanner, { props: { failure: PAY } });
    expect(w.find('[data-testid="delivery-banner-reason"]').text()).toContain('Out of credits with OpenRouter');
    expect(w.find('[data-testid="delivery-banner-settings"]').exists()).toBe(false);
    await w.find('[data-testid="delivery-banner-retry"]').trigger('click');
    expect(w.emitted('retry')).toHaveLength(1);

    const k = mount(DeliveryBanner, {
      props: { failure: { ...PAY, code: 'auth_invalid', summary: 'OpenRouter rejected the API key' } },
    });
    await k.find('[data-testid="delivery-banner-settings"]').trigger('click');
    expect(k.emitted('open-settings')).toHaveLength(1);

    const served = mount(DeliveryBanner, {
      props: { failure: { ...PAY, code: 'auth_invalid' }, settingsAvailable: false },
    });
    expect(served.find('[data-testid="delivery-banner-settings"]').exists()).toBe(false);
  });
});
