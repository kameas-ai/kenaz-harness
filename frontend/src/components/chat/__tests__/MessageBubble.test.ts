import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import MessageBubble from '@/components/chat/MessageBubble.vue';

describe('MessageBubble (chat-ui)', () => {
  it('renders user message with right-justified shape', () => {
    const w = mount(MessageBubble, {
      props: { role: 'user', content: 'Hello!' },
    });
    const article = w.find('article');
    expect(article.classes().join(' ')).toContain('justify-end');
    expect(w.text()).toContain('Hello!');
    expect(w.text()).toContain('USER');
    expect(article.attributes('aria-label')).toBe('USER message');
  });

  it('renders assistant message with brass live indicator while streaming', () => {
    const w = mount(MessageBubble, {
      props: { role: 'assistant', content: 'streaming…', streaming: true },
    });
    expect(w.html()).toContain('text-accent');
    expect(w.html()).toContain('live');
    // brass border applied while live
    expect(w.html()).toContain('border-accent');
  });

  it('does not show live indicator when assistant is not streaming', () => {
    const w = mount(MessageBubble, {
      props: { role: 'assistant', content: 'done.', streaming: false },
    });
    expect(w.text()).not.toContain('live');
    expect(w.html()).toContain('border-border-muted');
  });

  it('renders system role centred, italic, ink-muted', () => {
    const w = mount(MessageBubble, {
      props: { role: 'system', content: 'context loaded' },
    });
    expect(w.find('article').classes().join(' ')).toContain('justify-center');
    expect(w.html()).toContain('italic');
    expect(w.html()).toContain('text-ink-muted');
  });

  it('renders tool role as monospace EventStreamRow-style', () => {
    const w = mount(MessageBubble, {
      props: { role: 'tool', content: 'fs.read · /tmp/x.txt' },
    });
    expect(w.html()).toContain('font-mono');
    expect(w.find('article').attributes('role')).toBe('log');
  });

  it('renders tool-call rows with namespaced labels', () => {
    const w = mount(MessageBubble, {
      props: {
        role: 'assistant',
        content: 'will call a tool',
        toolCalls: [
          { id: 't1', name: 'fs.read', argsSummary: '/etc/hosts', latency: '12ms' },
        ],
      },
    });
    expect(w.text()).toContain('tool · fs.read');
    expect(w.text()).toContain('/etc/hosts');
    expect(w.text()).toContain('12ms');
  });

  it('reacts to prop changes (content + streaming)', async () => {
    const w = mount(MessageBubble, {
      props: { role: 'assistant', content: 'a', streaming: true },
    });
    expect(w.text()).toContain('a');
    expect(w.html()).toContain('border-accent');
    await w.setProps({ content: 'ab', streaming: false });
    expect(w.text()).toContain('ab');
    expect(w.html()).toContain('border-border-muted');
  });

  it('uses no raw color literal (privacy CI invariant #4)', () => {
    const w = mount(MessageBubble, {
      props: { role: 'assistant', content: 'x', streaming: true },
    });
    expect(w.html()).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
  });
});
