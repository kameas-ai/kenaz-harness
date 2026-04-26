import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import ContextPreview from '@/views/contexts/ContextPreview.vue';

/**
 * ContextPreview tests cover the WP05 in-place editor:
 *   - Edit toggle swaps the read-only block for a textarea.
 *   - Save flows the new content through the supplied onSave callback
 *     and re-renders the read view.
 *   - Cancel discards the draft.
 *   - Oversize files (>1 MiB) reject before the callback fires.
 *   - The token chip surfaces in the header (US5).
 *   - Snapshot of the edit-mode rendering for layout regressions.
 */
describe('ContextPreview', () => {
  it('shows the token estimate chip in the header', async () => {
    const w = mount(ContextPreview, {
      props: {
        path: 'a.md',
        content: 'x'.repeat(40), // 40 chars → ~10 tokens
      },
    });
    expect(w.find('[data-testid=context-preview-tokens]').text()).toContain(
      '~10 tokens',
    );
  });

  it('hides the Edit button when no onSave callback is provided', () => {
    const w = mount(ContextPreview, {
      props: { path: 'a.md', content: 'hi' },
    });
    expect(w.find('[data-testid=context-preview-edit]').exists()).toBe(false);
  });

  it('reveals the Edit button when onSave is provided', () => {
    const w = mount(ContextPreview, {
      props: {
        path: 'a.md',
        content: 'hi',
        onSave: async () => {},
      },
    });
    expect(w.find('[data-testid=context-preview-edit]').exists()).toBe(true);
  });

  it('matches the edit-mode snapshot', async () => {
    const w = mount(ContextPreview, {
      props: {
        path: 'notes/welcome.md',
        content: 'hello',
        onSave: async () => {},
      },
    });
    await w.find('[data-testid=context-preview-edit]').trigger('click');
    await flushPromises();
    // Sanity: the textarea is mounted with the current content.
    const ta = w.find('[data-testid=context-preview-editor]')
      .element as HTMLTextAreaElement;
    expect(ta.value).toBe('hello');
    // Snapshot guards the editor footer + token-chip layout.
    expect(w.html()).toMatchSnapshot();
  });

  it('saves edits via the onSave callback and returns to read mode', async () => {
    const onSave = vi.fn(async () => {});
    const w = mount(ContextPreview, {
      props: { path: 'doc.md', content: 'before', onSave },
    });
    await w.find('[data-testid=context-preview-edit]').trigger('click');
    await flushPromises();
    const ta = w.find('[data-testid=context-preview-editor]');
    await ta.setValue('after');
    await w.find('[data-testid=context-preview-save]').trigger('click');
    await flushPromises();

    expect(onSave).toHaveBeenCalledWith({ path: 'doc.md', content: 'after' });
    // After a successful save the editor unmounts and the read block
    // returns. The parent owns the `content` prop refresh; tests
    // simulate that by re-rendering with the new value.
    await w.setProps({ content: 'after' });
    await flushPromises();
    expect(w.find('[data-testid=context-preview-editor]').exists()).toBe(
      false,
    );
    expect(w.find('[data-testid=context-preview-content]').text()).toContain(
      'after',
    );
  });

  it('cancel discards the draft and exits edit mode', async () => {
    const onSave = vi.fn(async () => {});
    const w = mount(ContextPreview, {
      props: { path: 'doc.md', content: 'pristine', onSave },
    });
    await w.find('[data-testid=context-preview-edit]').trigger('click');
    await flushPromises();
    await w.find('[data-testid=context-preview-editor]').setValue('drift');
    await w.find('[data-testid=context-preview-cancel]').trigger('click');
    await flushPromises();
    expect(onSave).not.toHaveBeenCalled();
    expect(w.find('[data-testid=context-preview-editor]').exists()).toBe(
      false,
    );
    expect(w.find('[data-testid=context-preview-content]').text()).toContain(
      'pristine',
    );
  });

  it('rejects oversize content (>1 MiB) without invoking onSave', async () => {
    const onSave = vi.fn(async () => {});
    const w = mount(ContextPreview, {
      props: { path: 'big.md', content: 'small', onSave },
    });
    await w.find('[data-testid=context-preview-edit]').trigger('click');
    await flushPromises();
    // 1 MiB + 1 byte triggers the cap.
    const huge = 'x'.repeat((1 << 20) + 1);
    await w.find('[data-testid=context-preview-editor]').setValue(huge);
    await w.find('[data-testid=context-preview-save]').trigger('click');
    await flushPromises();

    expect(onSave).not.toHaveBeenCalled();
    const err = w.find('[data-testid=context-preview-save-error]');
    expect(err.exists()).toBe(true);
    expect(err.text()).toContain('too large');
  });

  it('surfaces save callback rejections inline', async () => {
    const onSave = vi.fn(async () => {
      throw new Error('disk full');
    });
    const w = mount(ContextPreview, {
      props: { path: 'doc.md', content: 'x', onSave },
    });
    await w.find('[data-testid=context-preview-edit]').trigger('click');
    await flushPromises();
    await w.find('[data-testid=context-preview-editor]').setValue('y');
    await w.find('[data-testid=context-preview-save]').trigger('click');
    await flushPromises();

    expect(onSave).toHaveBeenCalled();
    const err = w.find('[data-testid=context-preview-save-error]');
    expect(err.exists()).toBe(true);
    expect(err.text()).toContain('disk full');
    // Stays in edit mode so the user can retry.
    expect(w.find('[data-testid=context-preview-editor]').exists()).toBe(true);
  });

  it('Cmd+S triggers save from inside the textarea', async () => {
    const onSave = vi.fn(async () => {});
    const w = mount(ContextPreview, {
      props: { path: 'doc.md', content: 'x', onSave },
    });
    await w.find('[data-testid=context-preview-edit]').trigger('click');
    await flushPromises();
    const ta = w.find('[data-testid=context-preview-editor]');
    await ta.setValue('y');
    await ta.trigger('keydown', { key: 's', metaKey: true });
    await flushPromises();
    expect(onSave).toHaveBeenCalledWith({ path: 'doc.md', content: 'y' });
  });

  it('Esc cancels edit mode from inside the textarea', async () => {
    const onSave = vi.fn(async () => {});
    const w = mount(ContextPreview, {
      props: { path: 'doc.md', content: 'pristine', onSave },
    });
    await w.find('[data-testid=context-preview-edit]').trigger('click');
    await flushPromises();
    const ta = w.find('[data-testid=context-preview-editor]');
    await ta.setValue('drift');
    await ta.trigger('keydown', { key: 'Escape' });
    await flushPromises();
    expect(onSave).not.toHaveBeenCalled();
    expect(w.find('[data-testid=context-preview-editor]').exists()).toBe(
      false,
    );
  });
});
