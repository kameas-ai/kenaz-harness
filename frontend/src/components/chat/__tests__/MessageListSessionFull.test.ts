import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import MessageList from '@/components/chat/MessageList.vue';
import type { Message } from '@/lib/types';

/**
 * Review finding F7 of agentgraph-total-convergence-01PMGX01 WP08.
 *
 * WP08 moved compaction into the graph, which moved the "your session is
 * full" verdict off StartStream's synchronous return and onto the
 * stream-closed payload. It arrives as reason="backend-error", and the
 * chat surface rendered every backend-error under one heading:
 * "Send failed".
 *
 * That heading is not a wording quibble here, it is false. The send
 * succeeded — the user's message was persisted before the kernel ran and
 * is sitting in the transcript above the banner. What failed is that the
 * conversation no longer fits the model's context window, and the
 * remedy is not to retry. A user told "Send failed" will press send
 * again, which produces the identical failure.
 *
 * The backend now stamps error_kind="session_full" on the payload
 * (StreamClosedPayload.ErrorKind) and these tests pin that the surface
 * uses it: accurate copy, and the same escape the LongSessionNudge
 * banner offers.
 */

function msg(overrides: Partial<Message>): Message {
  return {
    id: 'm-1',
    sessionId: 's-1',
    role: 'user',
    content: 'hello',
    createdAt: '2026-04-25T00:00:00Z',
    ...overrides,
  };
}

const SESSION_FULL_COPY = 'session has hit its context window and compaction is unavailable';

describe('MessageList — session-full banner (F7)', () => {
  it('renders the session-full banner instead of "Send failed" when error_kind says so', () => {
    const w = mount(MessageList, {
      props: {
        messages: [msg({ content: 'the turn that did not fit' })],
        errorMessage: SESSION_FULL_COPY,
        errorKind: 'session_full',
      },
    });

    const banner = w.find('[data-testid="session-full-banner"]');
    expect(banner.exists()).toBe(true);
    expect(banner.text()).toContain('Session full');

    // The load-bearing assertion: the generic framing must be gone. The
    // send did succeed.
    expect(w.text()).not.toContain('Send failed');

    // And the copy must tell the user their message survived, since the
    // whole confusion is "did my message get lost?".
    expect(banner.text()).toContain('Your message was saved');
  });

  it('offers the new-session escape rather than inviting a retry', async () => {
    const w = mount(MessageList, {
      props: {
        messages: [msg({})],
        errorMessage: SESSION_FULL_COPY,
        errorKind: 'session_full',
      },
    });

    const cta = w.find('[data-testid="session-full-new-session"]');
    expect(cta.exists()).toBe(true);

    await cta.trigger('click');
    expect(w.emitted('new-session')).toBeTruthy();
    expect(w.emitted('new-session')).toHaveLength(1);
  });

  it('still renders the generic "Send failed" banner for ordinary errors', () => {
    const w = mount(MessageList, {
      props: {
        messages: [msg({})],
        errorMessage: 'provider returned 503',
      },
    });

    expect(w.find('[data-testid="session-full-banner"]').exists()).toBe(false);
    expect(w.text()).toContain('Send failed');
    expect(w.text()).toContain('provider returned 503');
  });

  it('renders neither banner when there is no error', () => {
    const w = mount(MessageList, { props: { messages: [msg({})] } });
    expect(w.find('[data-testid="session-full-banner"]').exists()).toBe(false);
    expect(w.text()).not.toContain('Send failed');
  });

  it('does not treat an unknown error_kind as session-full', () => {
    // A future backend value must fall back to the generic banner rather
    // than silently rendering the session-full copy for an unrelated
    // failure.
    const w = mount(MessageList, {
      props: {
        messages: [msg({})],
        errorMessage: 'something else went wrong',
        errorKind: 'some_future_kind',
      },
    });
    expect(w.find('[data-testid="session-full-banner"]').exists()).toBe(false);
    expect(w.text()).toContain('Send failed');
  });
});
