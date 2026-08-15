import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import MessageList from '@/components/chat/MessageList.vue';
import type { Message } from '@/lib/types';

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

describe('MessageList (chat-ui)', () => {
  it('renders an empty-state message when there are no messages', () => {
    const w = mount(MessageList, { props: { messages: [] } });
    expect(w.text()).toContain('No messages yet');
  });

  it('renders one MessageBubble per message and exposes log role', () => {
    const messages: Message[] = [
      msg({ id: 'a', role: 'user', content: 'q' }),
      msg({ id: 'b', role: 'assistant', content: 'r' }),
    ];
    const w = mount(MessageList, { props: { messages } });
    const log = w.find('[role="log"]');
    expect(log.exists()).toBe(true);
    expect(log.attributes('aria-live')).toBe('polite');
    expect(w.findAll('article').length).toBe(2);
  });

  it('appends a streaming placeholder after the message list', () => {
    const w = mount(MessageList, {
      props: {
        messages: [msg({ id: 'a', role: 'user', content: 'q' })],
        streamingMessages: [
          msg({
            id: 'live',
            role: 'assistant',
            content: 'streaming…',
            streaming: true,
          }),
        ],
      },
    });
    expect(w.findAll('article').length).toBe(2);
    expect(w.text()).toContain('streaming…');
  });

  it('exposes a scrollToBottom imperative handle', () => {
    const w = mount(MessageList, {
      props: { messages: [msg({})] },
    });
    expect(typeof (w.vm as unknown as { scrollToBottom: () => void }).scrollToBottom).toBe(
      'function',
    );
  });
});
