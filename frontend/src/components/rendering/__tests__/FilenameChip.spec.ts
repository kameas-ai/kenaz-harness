/**
 * FilenameChip.spec.ts — covers WP07 file-mention chip:
 * happy-path render, click → clipboard fallback when no openInEditor
 * surface, missing target shows disabled chip.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import FilenameChip from '@/components/rendering/FilenameChip.vue';

// Mock harnessClient — leave fs.exists controllable per-test.
const fsExistsMock = vi.fn<(p: string) => Promise<boolean>>();
const openInEditorMock = vi.fn<(p: string) => Promise<void>>();

vi.mock('@/lib/harnessClient', () => ({
  createHarnessClient: () => ({
    fs: { exists: fsExistsMock },
    tools: { openInEditor: openInEditorMock },
  }),
}));

describe('FilenameChip', () => {
  beforeEach(() => {
    fsExistsMock.mockReset();
    openInEditorMock.mockReset();
  });

  it('renders the path label', async () => {
    fsExistsMock.mockResolvedValue(true);
    const w = mount(FilenameChip, { props: { path: 'src/main.ts' } });
    expect(w.text()).toContain('src/main.ts');
  });

  it('disables itself when fs.exists returns false', async () => {
    fsExistsMock.mockResolvedValue(false);
    const w = mount(FilenameChip, { props: { path: 'missing.ts' } });
    await flushPromises();
    expect(w.find('button').attributes('disabled')).toBeDefined();
    expect(w.find('button').attributes('title')).toBe('file not found');
  });

  it('click invokes openInEditor when available', async () => {
    fsExistsMock.mockResolvedValue(true);
    openInEditorMock.mockResolvedValue(undefined);
    const w = mount(FilenameChip, { props: { path: 'a.ts' } });
    await flushPromises();
    await w.find('button').trigger('click');
    await flushPromises();
    expect(openInEditorMock).toHaveBeenCalledWith('a.ts');
  });

  it('falls back to clipboard when openInEditor throws', async () => {
    fsExistsMock.mockResolvedValue(true);
    openInEditorMock.mockRejectedValue(new Error('rpc not bound'));
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText },
      configurable: true,
    });
    const w = mount(FilenameChip, { props: { path: 'b.ts' } });
    await flushPromises();
    await w.find('button').trigger('click');
    await flushPromises();
    expect(writeText).toHaveBeenCalledWith('b.ts');
  });

  it('disabled chip click is a no-op', async () => {
    fsExistsMock.mockResolvedValue(false);
    const w = mount(FilenameChip, { props: { path: 'gone.ts' } });
    await flushPromises();
    await w.find('button').trigger('click');
    await flushPromises();
    expect(openInEditorMock).not.toHaveBeenCalled();
  });
});
