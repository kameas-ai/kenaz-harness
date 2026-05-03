import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import MessageBubble from '@/components/chat/MessageBubble.vue';
import type { Artifact } from '@/lib/types';

function makeArtifact(overrides: Partial<Artifact> = {}): Artifact {
  return {
    id: 'art-1',
    sessionId: 'sess-1',
    title: 'snippet.html',
    mimeType: 'text/html',
    contentHash: 'sha256:abc',
    byteSize: 1024,
    source: 'code_block',
    sourceRef: { messageId: 'm-1' },
    scopeKind: 'session',
    createdAt: '2026-04-26T00:00:00Z',
    ...overrides,
  };
}

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

  // ── artifacts-storage WP03 — per-message chip + Save as artifact ────

  it('renders an ArtifactChip row when artifacts are present', () => {
    const w = mount(MessageBubble, {
      props: {
        role: 'assistant',
        content: 'see attached',
        messageId: 'm-1',
        artifacts: [makeArtifact()],
      },
    });
    expect(w.find('[data-testid="message-artifacts-row"]').exists()).toBe(true);
    expect(w.find('[data-testid="artifact-chip-art-1"]').exists()).toBe(true);
  });

  it('does not render the chip row when no artifacts are present', () => {
    const w = mount(MessageBubble, {
      props: { role: 'assistant', content: 'plain' },
    });
    expect(w.find('[data-testid="message-artifacts-row"]').exists()).toBe(false);
  });

  it('emits open-artifact when an ArtifactChip is clicked', async () => {
    const a = makeArtifact({ id: 'art-9' });
    const w = mount(MessageBubble, {
      props: {
        role: 'assistant',
        content: 'see attached',
        messageId: 'm-1',
        artifacts: [a],
      },
    });
    await w.find('[data-testid="artifact-chip-art-9"]').trigger('click');
    const events = w.emitted('open-artifact');
    expect(events).toBeTruthy();
    expect(events![0][0]).toEqual(a);
  });

  it('opens the save-artifact menu via right-click when saveable=true and not rememberable', async () => {
    const w = mount(MessageBubble, {
      props: {
        role: 'assistant',
        content: 'pin me as artifact',
        messageId: 'm-1',
        saveable: true,
      },
    });
    expect(w.find('[data-testid="save-artifact-menu"]').exists()).toBe(false);
    await w.find('article').trigger('contextmenu');
    expect(w.find('[data-testid="save-artifact-menu"]').exists()).toBe(true);
  });

  it('emits save-artifact with the messageId when "Save as artifact" is picked', async () => {
    const w = mount(MessageBubble, {
      props: {
        role: 'assistant',
        content: 'long content here',
        messageId: 'm-42',
        saveable: true,
      },
    });
    await w.find('article').trigger('contextmenu');
    await w.find('[data-testid="save-artifact-menu-save"]').trigger('click');
    const events = w.emitted('save-artifact');
    expect(events).toBeTruthy();
    expect(events![0]).toEqual(['m-42']);
  });

  it('exposes a Save-as-artifact button when saveable and rememberable both true', async () => {
    const w = mount(MessageBubble, {
      props: {
        role: 'assistant',
        content: 'mixed',
        messageId: 'm-1',
        rememberable: true,
        saveable: true,
      },
    });
    expect(w.find('[data-testid="save-artifact-button"]').exists()).toBe(true);
    await w.find('[data-testid="save-artifact-button"]').trigger('click');
    expect(w.emitted('save-artifact')![0]).toEqual(['m-1']);
  });

  it('does not open the save-artifact menu via right-click when saveable=false', async () => {
    const w = mount(MessageBubble, {
      props: {
        role: 'assistant',
        content: 'plain',
        messageId: 'm-1',
      },
    });
    await w.find('article').trigger('contextmenu');
    expect(w.find('[data-testid="save-artifact-menu"]').exists()).toBe(false);
  });

  // Long-turn-resilience-01KR3PRS WP03 — partial-output footer + Resume.
  it('renders Resume button on a recoverable partial assistant bubble', async () => {
    const w = mount(MessageBubble, {
      props: {
        role: 'assistant',
        content: 'hello par',
        messageId: 'partial-1',
        streamingFailedAt: '2026-04-30T12:00:00Z',
        streamingFailureKind: 'transient',
        streamingRecoverable: true,
      },
    });
    const footer = w.find('[data-testid="message-partial-output-footer"]');
    expect(footer.exists()).toBe(true);
    expect(footer.text()).toContain('Connection lost');

    const btn = w.find('[data-testid="message-partial-resume-button"]');
    expect(btn.exists()).toBe(true);
    expect(btn.attributes('data-message-id')).toBe('partial-1');

    await btn.trigger('click');
    expect(w.emitted('resume')).toBeTruthy();
    expect(w.emitted('resume')![0]).toEqual(['partial-1']);
  });

  it('suppresses Resume button when streamingRecoverable is false', () => {
    const w = mount(MessageBubble, {
      props: {
        role: 'assistant',
        content: 'ran tool then died',
        messageId: 'partial-2',
        streamingFailedAt: '2026-04-30T12:00:00Z',
        streamingFailureKind: 'transient',
        streamingRecoverable: false,
      },
    });
    expect(w.find('[data-testid="message-partial-output-footer"]').exists()).toBe(true);
    expect(w.find('[data-testid="message-partial-resume-button"]').exists()).toBe(false);
    // Non-recoverable copy steers the user to re-issue the request.
    expect(w.text()).toContain('re-send your request');
  });

  it('renders the WP00 frontend-only fallback footer when only streamingError is set', () => {
    // No streamingFailedAt → server-side row hasn't materialized yet, but
    // the WP00 streamingError marker still drives the footer so the user
    // sees something rather than a silent drop.
    const w = mount(MessageBubble, {
      props: {
        role: 'assistant',
        content: 'streaming text',
        messageId: 'partial-3',
        streamingError: 'backend-error',
      },
    });
    expect(w.find('[data-testid="message-partial-output-footer"]').exists()).toBe(true);
    // streamingRecoverable defaults to undefined → falsy → resume button
    // suppressed (the row hasn't been classified yet).
    expect(w.find('[data-testid="message-partial-resume-button"]').exists()).toBe(false);
  });

  it('does not render the partial-output footer on a healthy assistant bubble', () => {
    const w = mount(MessageBubble, {
      props: { role: 'assistant', content: 'all good' },
    });
    expect(w.find('[data-testid="message-partial-output-footer"]').exists()).toBe(false);
  });
});
