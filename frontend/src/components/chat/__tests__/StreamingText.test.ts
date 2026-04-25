import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import StreamingText from '@/components/chat/StreamingText.vue';

describe('StreamingText (chat-ui)', () => {
  it('renders the text content verbatim', () => {
    const w = mount(StreamingText, {
      props: { text: 'Hello, harness!', streaming: false },
    });
    expect(w.text()).toContain('Hello, harness!');
  });

  it('shows the brass caret when streaming', () => {
    const w = mount(StreamingText, {
      props: { text: 'partial', streaming: true },
    });
    expect(w.find('.streaming-caret').exists()).toBe(true);
    expect(w.find('.streaming-caret').classes()).toContain('bg-accent');
  });

  it('hides the caret when not streaming', () => {
    const w = mount(StreamingText, {
      props: { text: 'done', streaming: false },
    });
    expect(w.find('.streaming-caret').exists()).toBe(false);
  });

  it('updates text without remounting (token-by-token)', async () => {
    const w = mount(StreamingText, {
      props: { text: 'a', streaming: true },
    });
    expect(w.text()).toContain('a');
    await w.setProps({ text: 'ab' });
    expect(w.text()).toContain('ab');
    await w.setProps({ text: 'abc' });
    expect(w.text()).toContain('abc');
  });

  it('exposes a text role for screen readers', () => {
    const w = mount(StreamingText, {
      props: { text: 'x', streaming: false },
    });
    expect(w.attributes('role')).toBe('text');
  });
});
