/**
 * MarkdownBlock.spec.ts — WP01 acceptance tests.
 *
 * Coverage:
 *   - Simple paragraph renders correctly.
 *   - Code block renders with header bar (lang label + copy + save buttons).
 *   - Copy button invokes navigator.clipboard.writeText with raw code text.
 *   - Save button invokes sessions.saveAsArtifact and shows the Undo toast.
 *   - Undo button invokes artifacts.remove with the saved artifact id.
 *   - Multiple code blocks each get their own header.
 *   - Streaming caret is shown when streaming=true, hidden when false.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createApp, defineComponent, h } from 'vue';
import MarkdownBlock from '@/components/chat/MarkdownBlock.vue';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import type { HarnessClient } from '@/lib/harnessClient';
import type { Artifact } from '@/lib/types';

// ── helpers ────────────────────────────────────────────────────────────────

function makeArtifact(overrides: Partial<Artifact> = {}): Artifact {
  return {
    id: 'art-123',
    sessionId: 'sess-1',
    title: 'typescript-snippet-abc123.ts',
    mimeType: 'text/plain',
    contentHash: 'fake-hash',
    byteSize: 42,
    source: 'code_block',
    sourceRef: { messageId: 'msg-1' },
    scopeKind: 'session',
    createdAt: new Date().toISOString(),
    ...overrides,
  };
}

/**
 * Mount MarkdownBlock with an optional fake client injected.
 * Mirrors the pattern used in MergeSuggestionToast.test.ts.
 */
function mountBlock(
  source: string,
  opts: {
    streaming?: boolean;
    sessionId?: string;
    messageId?: string;
    client?: Partial<HarnessClient>;
  } = {},
) {
  const fakeClient = createFakeHarnessClient(opts.client ?? {});
  return mount(MarkdownBlock, {
    props: {
      source,
      streaming: opts.streaming,
      sessionId: opts.sessionId,
      messageId: opts.messageId,
    },
    global: {
      provide: {
        [HarnessClientKey as symbol]: fakeClient,
      },
    },
    attachTo: document.body,
  });
}

// ── clipboard mock ─────────────────────────────────────────────────────────

beforeEach(() => {
  Object.defineProperty(navigator, 'clipboard', {
    value: { writeText: vi.fn().mockResolvedValue(undefined) },
    writable: true,
    configurable: true,
  });
});

afterEach(() => {
  vi.restoreAllMocks();
  document.body.innerHTML = '';
});

// ── tests ──────────────────────────────────────────────────────────────────

describe('MarkdownBlock', () => {
  // ── FR: simple paragraph ──────────────────────────────────────────────────

  it('renders a simple paragraph', () => {
    const w = mountBlock('Hello, world!');
    expect(w.text()).toContain('Hello, world!');
  });

  it('renders bold and italic markdown', () => {
    const w = mountBlock('**bold** and _italic_');
    const html = w.html();
    expect(html).toContain('<strong>');
    expect(html).toContain('<em>');
  });

  it('has role="text" on the root element', () => {
    const w = mountBlock('some text');
    expect(w.attributes('role')).toBe('text');
  });

  // ── FR: streaming caret ────────────────────────────────────────────────────

  it('shows the streaming caret when streaming=true', () => {
    const w = mountBlock('partial…', { streaming: true });
    expect(w.find('.streaming-caret').exists()).toBe(true);
  });

  it('hides the streaming caret when streaming=false', () => {
    const w = mountBlock('done.', { streaming: false });
    expect(w.find('.streaming-caret').exists()).toBe(false);
  });

  // ── FR: code-block header ─────────────────────────────────────────────────

  it('renders a code-block header bar with language label', async () => {
    const w = mountBlock('```typescript\nconst x = 1;\n```');
    await flushPromises();
    const header = w.find('[data-testid="code-block-header-0"]');
    expect(header.exists()).toBe(true);
    const langLabel = w.find('[data-testid="code-block-lang-0"]');
    expect(langLabel.exists()).toBe(true);
    expect(langLabel.element.textContent).toContain('typescript');
  });

  it('renders "code" as fallback language label for unlabelled blocks', async () => {
    const w = mountBlock('```\necho hello\n```');
    await flushPromises();
    const langLabel = w.find('[data-testid="code-block-lang-0"]');
    expect(langLabel.exists()).toBe(true);
    expect(langLabel.element.textContent).toContain('code');
  });

  it('renders a copy button for each code block', async () => {
    const w = mountBlock('```python\nprint("hi")\n```');
    await flushPromises();
    const copyBtn = w.find('[data-testid="code-block-copy-0"]');
    expect(copyBtn.exists()).toBe(true);
  });

  it('renders headers for multiple code blocks with correct indices', async () => {
    const src = [
      '```typescript\nconst a = 1;\n```',
      '',
      '```python\nprint("b")\n```',
    ].join('\n');
    const w = mountBlock(src);
    await flushPromises();
    expect(w.find('[data-testid="code-block-header-0"]').exists()).toBe(true);
    expect(w.find('[data-testid="code-block-header-1"]').exists()).toBe(true);
  });

  // ── FR: copy button ───────────────────────────────────────────────────────

  it('copy button calls clipboard API with raw code text', async () => {
    const code = 'const x = 42;';
    const w = mountBlock(`\`\`\`typescript\n${code}\n\`\`\``);
    await flushPromises();

    const copyBtn = w.find('[data-testid="code-block-copy-0"]');
    expect(copyBtn.exists()).toBe(true);
    await copyBtn.trigger('click');
    await flushPromises();

    expect(navigator.clipboard.writeText).toHaveBeenCalledWith(code);
  });

  it('copy button does NOT copy rendered HTML (only raw text)', async () => {
    const code = 'const x = 42;';
    const w = mountBlock(`\`\`\`typescript\n${code}\n\`\`\``);
    await flushPromises();

    await w.find('[data-testid="code-block-copy-0"]').trigger('click');
    await flushPromises();

    const written = (navigator.clipboard.writeText as ReturnType<typeof vi.fn>).mock.calls[0][0];
    expect(written).not.toContain('<');
    expect(written).toBe(code);
  });

  // ── FR: save button visibility ────────────────────────────────────────────

  it('does NOT render save button when sessionId is absent', async () => {
    const w = mountBlock('```typescript\nconst x = 1;\n```', {
      messageId: 'msg-1',
      // sessionId intentionally omitted
    });
    await flushPromises();
    expect(w.find('[data-testid="code-block-save-0"]').exists()).toBe(false);
  });

  it('does NOT render save button when messageId is absent', async () => {
    const w = mountBlock('```typescript\nconst x = 1;\n```', {
      sessionId: 'sess-1',
      // messageId intentionally omitted
    });
    await flushPromises();
    expect(w.find('[data-testid="code-block-save-0"]').exists()).toBe(false);
  });

  it('renders save button when both sessionId and messageId are provided', async () => {
    const w = mountBlock('```typescript\nconst x = 1;\n```', {
      sessionId: 'sess-1',
      messageId: 'msg-1',
    });
    await flushPromises();
    expect(w.find('[data-testid="code-block-save-0"]').exists()).toBe(true);
  });

  // ── FR: save RPC + undo toast ─────────────────────────────────────────────

  it('save button calls sessions.saveAsArtifact with correct args', async () => {
    const savedArtifact = makeArtifact({ id: 'art-save-1' });
    const saveAsArtifact = vi.fn().mockResolvedValue(savedArtifact);

    const w = mountBlock('```typescript\nconst x = 1;\n```', {
      sessionId: 'sess-1',
      messageId: 'msg-1',
      client: {
        sessions: createFakeHarnessClient().sessions,
      },
    });
    // Override saveAsArtifact after mount
    const fakeClient = createFakeHarnessClient({
      sessions: {
        ...createFakeHarnessClient().sessions,
        saveAsArtifact,
      },
    });

    // Re-mount with the spy client
    const w2 = mount(MarkdownBlock, {
      props: {
        source: '```typescript\nconst x = 1;\n```',
        sessionId: 'sess-1',
        messageId: 'msg-1',
      },
      global: {
        provide: { [HarnessClientKey as symbol]: fakeClient },
      },
      attachTo: document.body,
    });
    await flushPromises();

    const saveBtn = w2.find('[data-testid="code-block-save-0"]');
    expect(saveBtn.exists()).toBe(true);
    await saveBtn.trigger('click');
    await flushPromises();

    expect(saveAsArtifact).toHaveBeenCalledWith('sess-1', 'msg-1', expect.stringContaining('typescript'), 0, 0);
    w.unmount();
    w2.unmount();
  });

  it('shows Undo toast after successful save', async () => {
    const savedArtifact = makeArtifact({ id: 'art-toast-1', title: 'typescript-snippet-aabbcc.ts' });
    const saveAsArtifact = vi.fn().mockResolvedValue(savedArtifact);
    const fakeClient = createFakeHarnessClient({
      sessions: {
        ...createFakeHarnessClient().sessions,
        saveAsArtifact,
      },
    });

    const w = mount(MarkdownBlock, {
      props: {
        source: '```typescript\nconst x = 1;\n```',
        sessionId: 'sess-1',
        messageId: 'msg-1',
      },
      global: {
        provide: { [HarnessClientKey as symbol]: fakeClient },
      },
      attachTo: document.body,
    });
    await flushPromises();

    await w.find('[data-testid="code-block-save-0"]').trigger('click');
    await flushPromises();

    const toast = w.find('[data-testid="undo-toast"]');
    expect(toast.exists()).toBe(true);
    expect(toast.text()).toContain('Saved as');
    expect(toast.text()).toContain('Undo');
    w.unmount();
  });

  it('Undo button calls artifacts.remove with the saved artifact id', async () => {
    const savedArtifact = makeArtifact({ id: 'art-undo-99' });
    const saveAsArtifact = vi.fn().mockResolvedValue(savedArtifact);
    const remove = vi.fn().mockResolvedValue(undefined);
    const fakeClient = createFakeHarnessClient({
      sessions: {
        ...createFakeHarnessClient().sessions,
        saveAsArtifact,
      },
      artifacts: {
        ...createFakeHarnessClient().artifacts,
        remove,
      },
    });

    const w = mount(MarkdownBlock, {
      props: {
        source: '```python\nprint("hello")\n```',
        sessionId: 'sess-1',
        messageId: 'msg-1',
      },
      global: {
        provide: { [HarnessClientKey as symbol]: fakeClient },
      },
      attachTo: document.body,
    });
    await flushPromises();

    // Trigger save
    await w.find('[data-testid="code-block-save-0"]').trigger('click');
    await flushPromises();

    // Toast should be visible
    expect(w.find('[data-testid="undo-toast"]').exists()).toBe(true);

    // Click Undo
    await w.find('[data-testid="undo-toast-button"]').trigger('click');
    await flushPromises();

    expect(remove).toHaveBeenCalledWith('art-undo-99');

    // Toast should be gone
    expect(w.find('[data-testid="undo-toast"]').exists()).toBe(false);
    w.unmount();
  });

  // ── FR: no raw color literals (privacy CI invariant #4) ───────────────────

  it('uses no raw color literals', () => {
    const w = mountBlock('# Heading\n\nsome body\n\n```js\nconst x = 1;\n```');
    expect(w.html()).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
  });
});
