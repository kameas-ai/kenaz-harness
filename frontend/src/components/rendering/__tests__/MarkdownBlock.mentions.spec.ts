/**
 * MarkdownBlock.mentions.spec.ts — covers WP07 mention parser:
 * MarkdownBlock recognises `@filename` and `@artifact:<id>` patterns
 * in rendered text and emits placeholder spans → mounted chips.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import MarkdownBlock from '@/components/rendering/MarkdownBlock.vue';

vi.mock('@/lib/harnessClient', () => ({
  createHarnessClient: () => ({
    fs: { exists: vi.fn().mockResolvedValue(true) },
    tools: { openInEditor: vi.fn().mockResolvedValue(undefined) },
  }),
}));

describe('MarkdownBlock @-mentions (WP07)', () => {
  it('renders a FilenameChip placeholder for @path mentions', async () => {
    const w = mount(MarkdownBlock, {
      props: {
        text: 'See @frontend/src/main.ts for details.',
        extensions: 'basic',
      },
    });
    await flushPromises();
    const html = w.html();
    expect(html).toContain('frontend/src/main.ts');
    // Either the placeholder or the chip should appear
    expect(
      html.includes('data-md-mention="file"') ||
        html.includes('md-mention-chip'),
    ).toBe(true);
  });

  it('renders an artifact chip placeholder for @artifact:<id>', async () => {
    const w = mount(MarkdownBlock, {
      props: { text: 'Open @artifact:abc123 to view.', extensions: 'basic' },
    });
    await flushPromises();
    const html = w.html();
    expect(html).toContain('abc123');
    expect(
      html.includes('data-md-mention="artifact"') ||
        html.includes('artifact-mention-abc123'),
    ).toBe(true);
  });

  it('skips mentions inside <code>', async () => {
    const w = mount(MarkdownBlock, {
      props: { text: 'Inline `@not.a/mention` and outside @real/path.ts.', extensions: 'basic' },
    });
    await flushPromises();
    const html = w.html();
    // `@not.a/mention` inside code should remain literal, not chipped
    expect(html).toContain('@not.a/mention');
  });
});
