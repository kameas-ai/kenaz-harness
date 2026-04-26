import { describe, it, expect } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import DirectoryPicker from '@/views/tools/DirectoryPicker.vue';

describe('DirectoryPicker', () => {
  it('renders an empty state with the add button when no paths', async () => {
    const w = mount(DirectoryPicker, {
      props: { modelValue: [] },
    });
    await flushPromises();
    expect(w.find('[data-testid=dirpicker-add]').exists()).toBe(true);
    expect(w.find('[data-testid=dirpicker-empty]').exists()).toBe(true);
    expect(w.find('[data-testid=dirpicker-list]').exists()).toBe(false);
  });

  it('renders one chip per path in modelValue', async () => {
    const w = mount(DirectoryPicker, {
      props: { modelValue: ['/tmp/a', '/tmp/b'] },
    });
    await flushPromises();
    expect(w.find('[data-testid=dirpicker-chip-0]').text()).toContain('/tmp/a');
    expect(w.find('[data-testid=dirpicker-chip-1]').text()).toContain('/tmp/b');
  });

  it('removing a chip emits update:modelValue with the chip dropped', async () => {
    const w = mount(DirectoryPicker, {
      props: { modelValue: ['/tmp/a', '/tmp/b', '/tmp/c'] },
    });
    await flushPromises();
    await w.get('[data-testid=dirpicker-remove-1]').trigger('click');
    const events = w.emitted('update:modelValue');
    expect(events).toBeTruthy();
    expect(events?.[0]?.[0]).toEqual(['/tmp/a', '/tmp/c']);
  });

  it('inline-edit Enter commits the new value', async () => {
    const w = mount(DirectoryPicker, {
      props: { modelValue: ['/tmp/a'] },
    });
    await flushPromises();
    await w.get('[data-testid=dirpicker-chip-text-0]').trigger('click');
    await flushPromises();
    const input = w.get('[data-testid=dirpicker-edit-0]');
    await input.setValue('/tmp/edited');
    await input.trigger('keydown', { key: 'Enter' });
    await flushPromises();
    const events = w.emitted('update:modelValue');
    expect(events).toBeTruthy();
    expect(events?.at(-1)?.[0]).toEqual(['/tmp/edited']);
  });

  it('inline-edit Escape reverts the value', async () => {
    const w = mount(DirectoryPicker, {
      props: { modelValue: ['/tmp/a'] },
    });
    await flushPromises();
    await w.get('[data-testid=dirpicker-chip-text-0]').trigger('click');
    await flushPromises();
    const input = w.get('[data-testid=dirpicker-edit-0]');
    await input.setValue('/tmp/should-not-stick');
    await input.trigger('keydown', { key: 'Escape' });
    await flushPromises();
    expect(w.emitted('update:modelValue')).toBeUndefined();
    // Editing UI exited; chip text restored.
    expect(w.find('[data-testid=dirpicker-edit-0]').exists()).toBe(false);
    expect(w.get('[data-testid=dirpicker-chip-text-0]').text()).toContain(
      '/tmp/a',
    );
  });

  it('committing an empty edit drops the chip', async () => {
    const w = mount(DirectoryPicker, {
      props: { modelValue: ['/tmp/a', '/tmp/b'] },
    });
    await flushPromises();
    await w.get('[data-testid=dirpicker-chip-text-0]').trigger('click');
    await flushPromises();
    const input = w.get('[data-testid=dirpicker-edit-0]');
    await input.setValue('');
    await input.trigger('keydown', { key: 'Enter' });
    await flushPromises();
    const events = w.emitted('update:modelValue');
    expect(events).toBeTruthy();
    expect(events?.at(-1)?.[0]).toEqual(['/tmp/b']);
  });

  it('webkitdirectory file pick adds the picked root once (de-duped)', async () => {
    const w = mount(DirectoryPicker, {
      props: { modelValue: [] },
    });
    await flushPromises();

    // Build synthetic File objects with webkitRelativePath set.
    function fileWithRelPath(rel: string): File {
      const f = new File(['x'], rel.split('/').pop() ?? 'f');
      Object.defineProperty(f, 'webkitRelativePath', {
        value: rel,
        configurable: true,
      });
      return f;
    }

    const fileInput = w.get('[data-testid=dirpicker-file]')
      .element as HTMLInputElement;
    Object.defineProperty(fileInput, 'files', {
      value: [
        fileWithRelPath('myfolder/sub/a.txt'),
        fileWithRelPath('myfolder/b.txt'),
      ],
      configurable: true,
    });
    await w.get('[data-testid=dirpicker-file]').trigger('change');
    await flushPromises();

    const events = w.emitted('update:modelValue');
    expect(events).toBeTruthy();
    expect(events?.at(-1)?.[0]).toEqual(['myfolder']);
  });

  it('uses only design tokens — no raw hex / rgba', async () => {
    const w = mount(DirectoryPicker, {
      props: { modelValue: ['/tmp/a'] },
    });
    await flushPromises();
    const html = w.html();
    expect(html).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(html).not.toMatch(/rgba?\s*\(/i);
  });
});
