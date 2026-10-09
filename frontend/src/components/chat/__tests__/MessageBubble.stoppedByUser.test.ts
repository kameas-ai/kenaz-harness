/**
 * MessageBubble — a user Stop is not a connection loss
 * (dogfood 2026-10-08 round 2: Cancel mid-generation read "Connection
 * lost — partial reply preserved. Resume").
 */
import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import MessageBubble from '@/components/chat/MessageBubble.vue';
import { failureFromClosed, STOP_CALLED_REASON } from '@/lib/delivery';

describe('MessageBubble — stopped by the user', () => {
  it('renders "Stopped by you — partial reply kept" with no Connection-lost copy and no Resume', () => {
    const w = mount(MessageBubble, {
      props: {
        role: 'assistant',
        content: 'Here is the first part of a long answer',
        messageId: 'm-1',
        streamingError: STOP_CALLED_REASON,
      },
    });
    expect(w.find('[data-testid="message-stopped-by-user"]').text()).toBe('Stopped by you — partial reply kept.');
    expect(w.text()).not.toContain('Connection lost');
    expect(w.find('[data-testid="message-partial-output-footer"]').exists()).toBe(false);
    expect(w.find('[data-testid="message-partial-resume-button"]').exists()).toBe(false);
    expect(w.text()).toContain('Here is the first part of a long answer');
  });

  it('keeps the connection-lost copy and Resume for a real drop', () => {
    const w = mount(MessageBubble, {
      props: {
        role: 'assistant',
        content: 'partial',
        messageId: 'm-2',
        streamingError: 'backend-error',
        streamingFailedAt: '2026-10-08T21:00:00Z',
        streamingRecoverable: true,
      },
    });
    expect(w.find('[data-testid="message-stopped-by-user"]').exists()).toBe(false);
    expect(w.text()).toContain('Connection lost');
    expect(w.find('[data-testid="message-partial-resume-button"]').exists()).toBe(true);
  });

  it('a mid-stream Stop raises no delivery failure (no composer error bar)', () => {
    expect(
      failureFromClosed({ reason: STOP_CALLED_REASON, turn_span_id: 'u-1', delivered: true }),
    ).toBeNull();
  });
});
