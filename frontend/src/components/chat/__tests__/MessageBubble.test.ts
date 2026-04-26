import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
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

  // ── WP06 T005: pin-menu behaviour ─────────────────────────────────────

  it('opens the pin menu with three scope options on 📌 click', async () => {
    const w = mount(MessageBubble, {
      props: {
        role: 'assistant',
        content: 'pin me',
        rememberable: true,
        projectId: 'proj-1',
      },
    });
    expect(w.find('[data-testid="pin-menu"]').exists()).toBe(false);
    await w.find('[data-testid="remember-message"]').trigger('click');
    const menu = w.find('[data-testid="pin-menu"]');
    expect(menu.exists()).toBe(true);
    expect(w.find('[data-testid="pin-menu-session"]').exists()).toBe(true);
    expect(w.find('[data-testid="pin-menu-project"]').exists()).toBe(true);
    expect(w.find('[data-testid="pin-menu-global"]').exists()).toBe(true);
  });

  it('disables "Pin to project" when projectId is empty/undefined', async () => {
    const w = mount(MessageBubble, {
      props: {
        role: 'assistant',
        content: 'pin me',
        rememberable: true,
      },
    });
    await w.find('[data-testid="remember-message"]').trigger('click');
    const projectBtn = w.find('[data-testid="pin-menu-project"]');
    expect(projectBtn.exists()).toBe(true);
    expect(projectBtn.attributes('disabled')).toBeDefined();
    expect(projectBtn.attributes('title')).toBe('session is not in a project');
    // session and global remain enabled
    expect(
      w.find('[data-testid="pin-menu-session"]').attributes('disabled'),
    ).toBeUndefined();
    expect(
      w.find('[data-testid="pin-menu-global"]').attributes('disabled'),
    ).toBeUndefined();
  });

  it('emits remember(scope) with the picked option', async () => {
    const w = mount(MessageBubble, {
      props: {
        role: 'assistant',
        content: 'pin me',
        rememberable: true,
        projectId: 'proj-1',
      },
    });
    await w.find('[data-testid="remember-message"]').trigger('click');
    await w.find('[data-testid="pin-menu-project"]').trigger('click');
    const events = w.emitted('remember');
    expect(events).toBeTruthy();
    expect(events![0]).toEqual(['project']);
  });

  it('emits remember("session") for the session option (legacy default)', async () => {
    const w = mount(MessageBubble, {
      props: {
        role: 'assistant',
        content: 'pin me',
        rememberable: true,
        projectId: 'proj-1',
      },
    });
    await w.find('[data-testid="remember-message"]').trigger('click');
    await w.find('[data-testid="pin-menu-session"]').trigger('click');
    expect(w.emitted('remember')![0]).toEqual(['session']);
  });

  it('emits remember("global") for the global option', async () => {
    const w = mount(MessageBubble, {
      props: {
        role: 'assistant',
        content: 'pin me',
        rememberable: true,
      },
    });
    await w.find('[data-testid="remember-message"]').trigger('click');
    await w.find('[data-testid="pin-menu-global"]').trigger('click');
    expect(w.emitted('remember')![0]).toEqual(['global']);
  });

  it('flashes a confirmation badge after a successful pin', async () => {
    vi.useFakeTimers();
    try {
      const w = mount(MessageBubble, {
        props: {
          role: 'assistant',
          content: 'pin me',
          rememberable: true,
        },
      });
      await w.find('[data-testid="remember-message"]').trigger('click');
      await w.find('[data-testid="pin-menu-session"]').trigger('click');
      await flushPromises();
      expect(w.find('[data-testid="remember-confirm"]').exists()).toBe(true);
      vi.advanceTimersByTime(1500);
      await flushPromises();
      expect(w.find('[data-testid="remember-confirm"]').exists()).toBe(false);
    } finally {
      vi.useRealTimers();
    }
  });

  it('opens the pin menu via right-click on the bubble body', async () => {
    const w = mount(MessageBubble, {
      props: {
        role: 'assistant',
        content: 'pin me',
        rememberable: true,
      },
    });
    expect(w.find('[data-testid="pin-menu"]').exists()).toBe(false);
    await w.find('article').trigger('contextmenu');
    expect(w.find('[data-testid="pin-menu"]').exists()).toBe(true);
  });

  it('does not open the menu via right-click when not rememberable', async () => {
    const w = mount(MessageBubble, {
      props: { role: 'assistant', content: 'x' },
    });
    await w.find('article').trigger('contextmenu');
    expect(w.find('[data-testid="pin-menu"]').exists()).toBe(false);
  });

  // ── multimodal-io WP04 block-aware rendering ─────────────────────────

  it('renders a text block via StreamingText when contentBlocks are present', () => {
    const w = mount(MessageBubble, {
      props: {
        role: 'user',
        content: 'fallback ignored',
        contentBlocks: [{ type: 'text', text: 'hello blocks' }],
      },
    });
    expect(w.text()).toContain('hello blocks');
  });

  it('renders an image block as a thumbnail', () => {
    const w = mount(MessageBubble, {
      props: {
        role: 'user',
        content: '',
        contentBlocks: [
          {
            type: 'image',
            source: {
              kind: 'base64',
              media_type: 'image/png',
              data: 'aGVsbG8=',
              original_name: 'pic.png',
            },
          },
        ],
      },
    });
    expect(w.find('[data-testid="image-block-thumbnail"]').exists()).toBe(true);
  });

  it('opens ImageLightbox when the image thumbnail is clicked', async () => {
    const w = mount(MessageBubble, {
      props: {
        role: 'user',
        content: '',
        contentBlocks: [
          {
            type: 'image',
            source: {
              kind: 'base64',
              media_type: 'image/png',
              data: 'aGVsbG8=',
              original_name: 'pic.png',
            },
          },
        ],
      },
    });
    await w.find('[data-testid="image-block-thumbnail"]').trigger('click');
    expect(w.find('[data-testid="image-lightbox"]').exists()).toBe(true);
  });

  it('renders a document block as a chip', () => {
    const w = mount(MessageBubble, {
      props: {
        role: 'user',
        content: '',
        contentBlocks: [
          {
            type: 'document',
            source: {
              kind: 'base64',
              media_type: 'application/pdf',
              data: 'JVBERi0=',
              original_name: 'doc.pdf',
            },
          },
        ],
      },
    });
    expect(w.find('[data-testid="document-chip"]').exists()).toBe(true);
  });

  it('falls back to legacy text rendering when contentBlocks are empty', () => {
    const w = mount(MessageBubble, {
      props: {
        role: 'user',
        content: 'legacy plain text',
      },
    });
    expect(w.text()).toContain('legacy plain text');
  });
});
