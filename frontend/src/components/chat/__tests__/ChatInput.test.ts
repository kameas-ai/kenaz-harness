import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import ChatInput from '@/components/chat/ChatInput.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import type { HarnessClient } from '@/lib/harnessClient';
import { axe } from 'vitest-axe';

function mountInput(
  props: Record<string, unknown> = {},
  seed: Partial<HarnessClient> = {},
) {
  return mount(ChatInput, {
    props,
    global: {
      plugins: [
        {
          install(app) {
            provideFakeClient(app, seed);
          },
        },
      ],
    },
  });
}

beforeEach(() => {
  // happy-dom doesn't ship URL.createObjectURL by default; stub it so
  // ChatInput's image-thumbnail path doesn't crash in tests.
  if (typeof URL.createObjectURL !== 'function') {
    Object.defineProperty(URL, 'createObjectURL', {
      configurable: true,
      value: vi.fn(() => 'blob://fake'),
    });
  }
  if (typeof URL.revokeObjectURL !== 'function') {
    Object.defineProperty(URL, 'revokeObjectURL', {
      configurable: true,
      value: vi.fn(),
    });
  }
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('ChatInput (chat-ui)', () => {
  it('emits send on Enter when there is content', async () => {
    const w = mountInput();
    const textarea = w.find('textarea');
    await textarea.setValue('hello');
    await textarea.trigger('keydown', { key: 'Enter' });
    expect(w.emitted('send')).toBeTruthy();
    expect(w.emitted('send')![0]).toEqual(['hello']);
  });

  it('does not emit send when content is empty', async () => {
    const w = mountInput();
    const textarea = w.find('textarea');
    await textarea.trigger('keydown', { key: 'Enter' });
    expect(w.emitted('send')).toBeFalsy();
  });

  it('inserts newline on Shift+Enter (does not emit send)', async () => {
    const w = mountInput();
    const textarea = w.find('textarea');
    await textarea.setValue('line one');
    await textarea.trigger('keydown', { key: 'Enter', shiftKey: true });
    expect(w.emitted('send')).toBeFalsy();
  });

  it('keeps the textarea enabled while streaming and shows Cancel (queue-while-streaming UX)', async () => {
    // Per current contract the textarea stays enabled while a turn is
    // streaming so the user can queue a follow-up message; the
    // surrounding view drains the queue once the active turn finishes.
    // `disabled` (no provider / no session) still hard-locks the input
    // — that path is covered separately below.
    const w = mountInput({ streaming: true, modelValue: 'pending' });
    const textarea = w.find('textarea');
    expect(textarea.attributes('disabled')).toBeUndefined();
    const cancel = w.find('button[aria-label="Cancel stream"]');
    expect(cancel.exists()).toBe(true);
    await cancel.trigger('click');
    expect(w.emitted('cancel')).toBeTruthy();
  });

  it('Enter while streaming queues the message (parent drains the queue)', async () => {
    // Streaming no longer suppresses send — it routes the message into
    // the parent's queue. The send button label flips to "queue" but
    // the emit fires the same way.
    const w = mountInput({ streaming: true, modelValue: 'queued' });
    const textarea = w.find('textarea');
    await textarea.trigger('keydown', { key: 'Enter' });
    expect(w.emitted('send')).toBeTruthy();
    expect(w.emitted('send')![0]).toEqual(['queued']);
  });

  it('disabled prop also gates send', async () => {
    const w = mountInput({ disabled: true });
    const textarea = w.find('textarea');
    await textarea.setValue('blocked');
    await textarea.trigger('keydown', { key: 'Enter' });
    expect(w.emitted('send')).toBeFalsy();
    expect(textarea.attributes('disabled')).toBeDefined();
  });

  it('renders token + cost estimate placeholders', () => {
    const w = mountInput({ estimate: { tokens: 1234, usd: 0.0123 } });
    expect(w.text()).toContain('1,234 tok');
    expect(w.text()).toContain('$0.0123');
  });

  it('exposes accessible label and aria-multiline on textarea', () => {
    const w = mountInput();
    const t = w.find('textarea');
    expect(t.attributes('aria-label')).toBe('Message');
    expect(t.attributes('aria-multiline')).toBe('true');
  });

  it('clears the textarea after sending', async () => {
    const w = mountInput();
    const textarea = w.find('textarea');
    await textarea.setValue('msg');
    await textarea.trigger('keydown', { key: 'Enter' });
    expect(w.emitted('update:modelValue')).toBeTruthy();
    const updates = w.emitted('update:modelValue') as unknown[][];
    expect(updates[updates.length - 1]).toEqual(['']);
  });

  // ── multimodal-io WP04 paperclip + drag-drop ─────────────────────────

  it('renders the paperclip button', () => {
    const w = mountInput();
    expect(w.find('[data-testid="chat-input-paperclip"]').exists()).toBe(true);
  });

  it('appends an image thumbnail to the pending row when an image file is chosen', async () => {
    const addMedia = vi.fn(async (_kind, _id, _b64, mt, name) => ({
      id: 'att-img-1',
      scopeKind: 'session' as const,
      scopeId: 'sess-1',
      contentSource: `media:fake-${mt}`,
      content: name,
      kind: 'system' as const,
      position: 0,
      createdAt: '',
    }));
    const w = mountInput(
      { sessionId: 'sess-1' },
      { attachments: { ...({}), addMedia } as unknown as HarnessClient['attachments'] },
    );
    const file = new File([new Uint8Array([0x89, 0x50, 0x4e, 0x47])], 'pic.png', {
      type: 'image/png',
    });
    const cmp = w.vm as unknown as {
      onFileChosen: (f: File) => Promise<void>;
    };
    await cmp.onFileChosen(file);
    await flushPromises();
    expect(w.find('[data-testid="pending-attachment-0"]').exists()).toBe(true);
    expect(w.find('[data-testid="pending-thumb-0"]').exists()).toBe(true);
    expect(addMedia).toHaveBeenCalledTimes(1);
  });

  it('appends a document chip when a PDF is dropped', async () => {
    const addMedia = vi.fn(async () => ({
      id: 'att-pdf-1',
      scopeKind: 'session' as const,
      scopeId: 'sess-1',
      contentSource: 'media:fake-pdf',
      content: 'doc.pdf',
      kind: 'system' as const,
      position: 0,
      createdAt: '',
    }));
    const w = mountInput(
      { sessionId: 'sess-1' },
      { attachments: { ...({}), addMedia } as unknown as HarnessClient['attachments'] },
    );
    const file = new File([new Uint8Array([0x25, 0x50, 0x44, 0x46])], 'doc.pdf', {
      type: 'application/pdf',
    });
    const cmp = w.vm as unknown as {
      onFileChosen: (f: File) => Promise<void>;
    };
    await cmp.onFileChosen(file);
    await flushPromises();
    expect(w.find('[data-testid="pending-attachment-0"]').exists()).toBe(true);
    expect(w.find('[data-testid="pending-thumb-0"]').exists()).toBe(false);
    expect(addMedia).toHaveBeenCalledTimes(1);
  });

  it('routes a .go file to the text-snapshot attachment path', async () => {
    const add = vi.fn(async (input) => ({
      id: 'att-text-1',
      scopeKind: input.scopeKind,
      scopeId: input.scopeId ?? '',
      contentSource: input.contentSource,
      content: input.content,
      kind: input.kind ?? 'system',
      position: 0,
      createdAt: '',
    }));
    const w = mountInput(
      { sessionId: 'sess-1' },
      { attachments: { ...({}), add } as unknown as HarnessClient['attachments'] },
    );
    const file = new File(['package main\n'], 'main.go', { type: '' });
    const cmp = w.vm as unknown as {
      onFileChosen: (f: File) => Promise<void>;
    };
    await cmp.onFileChosen(file);
    await flushPromises();
    expect(w.find('[data-testid="pending-attachment-0"]').exists()).toBe(true);
    expect(add).toHaveBeenCalledTimes(1);
    const callArg = add.mock.calls[0][0];
    expect(callArg.content).toContain('package main');
    expect(callArg.contentSource).toMatch(/^inline:/);
  });

  it('rejects an unrecognised binary drop with a friendly banner', async () => {
    const w = mountInput({ sessionId: 'sess-1' });
    const file = new File([new Uint8Array([0xff, 0xfe, 0x00])], 'mystery.bin', {
      type: '',
    });
    const cmp = w.vm as unknown as {
      onFileChosen: (f: File) => Promise<void>;
    };
    await cmp.onFileChosen(file);
    await flushPromises();
    expect(w.find('[data-testid="chat-input-error"]').exists()).toBe(true);
    expect(w.find('[data-testid="chat-input-error"]').text()).toMatch(
      /only supported as images or PDFs/i,
    );
  });

  it('rejects images larger than 20 MiB', async () => {
    const w = mountInput({ sessionId: 'sess-1' });
    const big = new Uint8Array(21 * 1024 * 1024);
    const file = new File([big], 'huge.png', { type: 'image/png' });
    const cmp = w.vm as unknown as {
      onFileChosen: (f: File) => Promise<void>;
    };
    await cmp.onFileChosen(file);
    await flushPromises();
    expect(w.find('[data-testid="chat-input-error"]').exists()).toBe(true);
    expect(w.find('[data-testid="chat-input-error"]').text()).toMatch(
      /too large/i,
    );
  });

  it('emits sendBlocks with [image, text] when sending after staging an image', async () => {
    const addMedia = vi.fn(async (_kind, _id, _b64, mt, name) => ({
      id: 'att-img-2',
      scopeKind: 'session' as const,
      scopeId: 'sess-1',
      contentSource: `media:fake-${mt}`,
      content: name,
      kind: 'system' as const,
      position: 0,
      createdAt: '',
    }));
    const w = mountInput(
      { sessionId: 'sess-1' },
      { attachments: { ...({}), addMedia } as unknown as HarnessClient['attachments'] },
    );
    const file = new File([new Uint8Array([0x89, 0x50, 0x4e, 0x47])], 'pic.png', {
      type: 'image/png',
    });
    const cmp = w.vm as unknown as {
      onFileChosen: (f: File) => Promise<void>;
    };
    await cmp.onFileChosen(file);
    await flushPromises();
    const textarea = w.find('textarea');
    await textarea.setValue('describe this');
    await textarea.trigger('keydown', { key: 'Enter' });
    await flushPromises();
    const blocks = w.emitted('sendBlocks');
    expect(blocks).toBeTruthy();
    const arr = blocks![0][0] as { type: string }[];
    expect(arr.length).toBe(2);
    expect(arr[0].type).toBe('image');
    expect(arr[1].type).toBe('text');
  });

  it('renders an external errorBanner without dropping the typed text', async () => {
    const w = mountInput({
      modelValue: 'still here',
      errorBanner: 'Model `claude-3-haiku` doesn\'t support images.',
    });
    expect(w.find('[data-testid="chat-input-error"]').text()).toMatch(
      /doesn't support images/,
    );
    expect(w.find('textarea').element.value).toBe('still here');
  });

  // ── @filepath shortcut ───────────────────────────────────────────────

  it('queries shell.pathComplete when the user types @<token>', async () => {
    const pathComplete = vi.fn(async () => ['/tmp/foo.txt', '/tmp/foobar.txt']);
    const w = mountInput(
      { sessionId: 'sess-1' },
      {
        shell: {
          openInOSBrowser: vi.fn(),
          pathComplete,
          readFile: vi.fn(),
        } as unknown as HarnessClient['shell'],
      },
    );
    const textarea = w.find<HTMLTextAreaElement>('textarea');
    textarea.element.value = '@/tmp/foo';
    textarea.element.selectionStart = textarea.element.value.length;
    await textarea.trigger('input');
    await new Promise((r) => setTimeout(r, 120));
    await flushPromises();
    expect(pathComplete).toHaveBeenCalled();
    expect(w.find('[data-testid="at-filepath-suggestions"]').exists()).toBe(
      true,
    );
  });

  it('reads + attaches the file when @path is committed via Tab', async () => {
    const pathComplete = vi.fn(async () => ['/tmp/notes.go']);
    const readFile = vi.fn(async () => ({
      dataBase64: btoa('package main'),
      mediaType: 'text/plain',
    }));
    const add = vi.fn(async (input) => ({
      id: 'att-text-2',
      scopeKind: input.scopeKind,
      scopeId: input.scopeId ?? '',
      contentSource: input.contentSource,
      content: input.content,
      kind: input.kind ?? 'system',
      position: 0,
      createdAt: '',
    }));
    const w = mountInput(
      { sessionId: 'sess-1' },
      {
        shell: {
          openInOSBrowser: vi.fn(),
          pathComplete,
          readFile,
        } as unknown as HarnessClient['shell'],
        attachments: { ...({}), add } as unknown as HarnessClient['attachments'],
      },
    );
    const textarea = w.find<HTMLTextAreaElement>('textarea');
    textarea.element.value = '@/tmp/note';
    textarea.element.selectionStart = textarea.element.value.length;
    await textarea.trigger('input');
    await new Promise((r) => setTimeout(r, 120));
    await flushPromises();
    await textarea.trigger('keydown', { key: 'Tab' });
    // commitAtPath chains: readFile → atob → sha256Hex → attachments.add.
    // happy-dom's microtask flush isn't enough; let the macrotask queue drain.
    await flushPromises();
    await new Promise((r) => setTimeout(r, 30));
    await flushPromises();
    expect(readFile).toHaveBeenCalledWith('/tmp/notes.go');
    expect(add).toHaveBeenCalledTimes(1);
    expect(w.find('[data-testid="pending-attachment-0"]').exists()).toBe(true);
  });

  it('surfaces a deny-list error when readFile rejects an @path', async () => {
    const pathComplete = vi.fn(async () => ['/etc/passwd']);
    const readFile = vi.fn(async () => {
      throw new Error('shell.readFile: tools: path is in deny-list');
    });
    const w = mountInput(
      { sessionId: 'sess-1' },
      {
        shell: {
          openInOSBrowser: vi.fn(),
          pathComplete,
          readFile,
        } as unknown as HarnessClient['shell'],
      },
    );
    const textarea = w.find<HTMLTextAreaElement>('textarea');
    textarea.element.value = '@/etc/passwd';
    textarea.element.selectionStart = textarea.element.value.length;
    await textarea.trigger('input');
    await new Promise((r) => setTimeout(r, 120));
    await flushPromises();
    await textarea.trigger('keydown', { key: 'Tab' });
    await flushPromises();
    expect(w.find('[data-testid="chat-input-error"]').exists()).toBe(true);
    expect(w.find('[data-testid="chat-input-error"]').text()).toMatch(
      /deny-list/i,
    );
  });

  // ── axe-core accessibility assertion ──────────────────────────────────
  it('has no axe-core violations in idle state (a11y)', async () => {
    // Disabled axe rules with rationale:
    //   color-contrast — happy-dom returns 0/0 ratio (no CSS layout).
    //     needs_manual_verification: check contrast in a real browser.
    //   region — component mounted in isolation, not inside a Shell landmark.
    //     In production it lives inside <main class="shell-main">.
    const w = mountInput();
    await flushPromises();
    const results = await axe(w.element, {
      rules: {
        'color-contrast': { enabled: false },
        region: { enabled: false },
      },
    });
    // @ts-expect-error — toHaveNoViolations is added via test-setup.ts extend
    expect(results).toHaveNoViolations();
  });
});
