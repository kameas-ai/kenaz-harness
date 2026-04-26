import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import AttachmentRow from '@/components/contexts/AttachmentRow.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { Attachment } from '@/lib/types';

function makeAttachment(overrides: Partial<Attachment> = {}): Attachment {
  return {
    id: 'a-1',
    scopeKind: 'project',
    scopeId: 'p-1',
    contentSource: 'library:onboarding/intro.md',
    content: 'hello world',
    kind: 'system',
    position: 0,
    createdAt: '2026-04-26T00:00:00Z',
    ...overrides,
  };
}

function mountRow(attachment: Attachment, clientSeed = {}) {
  const client = createFakeHarnessClient(clientSeed);
  return {
    wrapper: mount(AttachmentRow, {
      props: { attachment },
      global: { provide: { [HarnessClientKey as symbol]: client } },
    }),
    client,
  };
}

describe('AttachmentRow', () => {
  it('renders the library path for library-source attachments', () => {
    const a = makeAttachment({ contentSource: 'library:projects/intro.md' });
    const { wrapper } = mountRow(a);
    expect(wrapper.text()).toContain('projects/intro.md');
  });

  it('renders the inline preview snippet for inline-source attachments', () => {
    const longContent = 'a'.repeat(120);
    const a = makeAttachment({
      contentSource: 'inline:abc123',
      content: longContent,
    });
    const { wrapper } = mountRow(a);
    const source = wrapper.find('[data-testid=attachment-source-a-1]');
    // first 60 chars + ellipsis
    expect(source.text()).toContain('inline:');
    expect(source.text().length).toBeLessThan(longContent.length);
  });

  it('renders the scope badge with the canonical glyph', () => {
    const a = makeAttachment({ scopeKind: 'global', scopeId: '' });
    const { wrapper } = mountRow(a);
    const badge = wrapper.find('[data-testid=attachment-scope-a-1]');
    expect(badge.exists()).toBe(true);
    expect(badge.text()).toContain('global');
  });

  it('shows Refresh button only for library-source attachments', () => {
    const a = makeAttachment({ contentSource: 'inline:abc' });
    const { wrapper } = mountRow(a);
    expect(
      wrapper.find('[data-testid=attachment-refresh-a-1]').exists(),
    ).toBe(false);
  });

  it('emits removed after a successful Attachments_Remove', async () => {
    const a = makeAttachment();
    const remove = vi.fn().mockResolvedValue(undefined);
    const { wrapper } = mountRow(a, {
      attachments: {
        list: async () => [],
        listResolved: async () => [],
        add: async () => a,
        remove,
        reorder: async () => undefined,
        refresh: async () => a,
      },
    });
    await wrapper.find('[data-testid=attachment-remove-a-1]').trigger('click');
    await flushPromises();
    expect(remove).toHaveBeenCalledWith('a-1');
    expect(wrapper.emitted('removed')).toBeTruthy();
    expect(wrapper.emitted('removed')![0]).toEqual(['a-1']);
  });

  it('surfaces a "source missing" warning when refresh fails with a not-found sentinel (A10)', async () => {
    const a = makeAttachment({ contentSource: 'library:gone.md' });
    const refresh = vi.fn().mockRejectedValue(new Error('not found: gone.md'));
    const { wrapper } = mountRow(a, {
      attachments: {
        list: async () => [],
        listResolved: async () => [],
        add: async () => a,
        remove: async () => undefined,
        reorder: async () => undefined,
        refresh,
      },
    });
    await wrapper.find('[data-testid=attachment-refresh-a-1]').trigger('click');
    await flushPromises();
    const warn = wrapper.find('[data-testid=attachment-source-missing-a-1]');
    expect(warn.exists()).toBe(true);
    expect(warn.text()).toContain('Source missing');
    // Detach affordance is wired
    expect(
      wrapper.find('[data-testid=attachment-detach-a-1]').exists(),
    ).toBe(true);
  });

  it('emits refreshed with the updated attachment on success', async () => {
    const a = makeAttachment();
    const updated: Attachment = { ...a, content: 'reloaded content' };
    const refresh = vi.fn().mockResolvedValue(updated);
    const { wrapper } = mountRow(a, {
      attachments: {
        list: async () => [],
        listResolved: async () => [],
        add: async () => a,
        remove: async () => undefined,
        reorder: async () => undefined,
        refresh,
      },
    });
    await wrapper.find('[data-testid=attachment-refresh-a-1]').trigger('click');
    await flushPromises();
    expect(refresh).toHaveBeenCalledWith('a-1');
    const emitted = wrapper.emitted('refreshed');
    expect(emitted).toBeTruthy();
    expect(emitted![0][0]).toMatchObject({ id: 'a-1', content: 'reloaded content' });
  });

  it('hides action buttons in readonly mode (used by ResolvedContextPanel)', () => {
    const a = makeAttachment();
    const { wrapper } = mountRow(a);
    // Sanity: actions present by default.
    expect(
      wrapper.find('[data-testid=attachment-remove-a-1]').exists(),
    ).toBe(true);
    const ro = mount(AttachmentRow, {
      props: { attachment: a, readonly: true },
      global: {
        provide: {
          [HarnessClientKey as symbol]: createFakeHarnessClient(),
        },
      },
    });
    expect(
      ro.find('[data-testid=attachment-remove-a-1]').exists(),
    ).toBe(false);
    expect(
      ro.find('[data-testid=attachment-refresh-a-1]').exists(),
    ).toBe(false);
  });

  it('uses only design tokens — no raw hex/rgba', () => {
    const a = makeAttachment();
    const { wrapper } = mountRow(a);
    const html = wrapper.html();
    expect(html).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(html).not.toMatch(/rgba?\s*\(/i);
  });
});
