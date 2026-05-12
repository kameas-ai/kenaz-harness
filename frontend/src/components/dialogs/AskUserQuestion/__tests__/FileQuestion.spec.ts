import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import FileQuestion from '../FileQuestion.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';

function makeWrapper(pickFileImpl?: () => Promise<string>) {
  const pickFileMock = pickFileImpl ? vi.fn(pickFileImpl) : vi.fn().mockResolvedValue('');
  const wrapper = mount(FileQuestion, {
    attachTo: document.body,
    global: {
      plugins: [
        {
          install(app) {
            provideFakeClient(app, {
              shell: {
                openInOSBrowser: vi.fn(),
                pathComplete: vi.fn().mockResolvedValue([]),
                readFile: vi.fn().mockResolvedValue({ dataBase64: '', mediaType: '' }),
                pickFile: pickFileMock,
              },
            });
          },
        },
      ],
    },
  });
  return { wrapper, pickFileMock };
}

describe('FileQuestion', () => {
  it('renders without a selected path initially', () => {
    const { wrapper } = makeWrapper();
    expect(wrapper.find('[data-testid="file-question"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="file-question-path"]').text()).toContain('No file selected');
  });

  it('calls shell.pickFile when the pick button is clicked', async () => {
    const { wrapper, pickFileMock } = makeWrapper(() => Promise.resolve('/tmp/report.pdf'));
    await wrapper.find('[data-testid="file-question-pick-btn"]').trigger('click');
    await flushPromises();
    expect(pickFileMock).toHaveBeenCalledOnce();
    expect(wrapper.find('[data-testid="file-question-path"]').text()).toContain('/tmp/report.pdf');
  });

  it('emits update:modelValue with the selected path', async () => {
    const { wrapper } = makeWrapper(() => Promise.resolve('/home/user/doc.txt'));
    await wrapper.find('[data-testid="file-question-pick-btn"]').trigger('click');
    await flushPromises();
    const events = wrapper.emitted('update:modelValue') as string[][];
    expect(events).toBeDefined();
    expect(events[0][0]).toBe('/home/user/doc.txt');
  });

  it('treats empty string response as cancellation (no path set)', async () => {
    const { wrapper } = makeWrapper(() => Promise.resolve(''));
    await wrapper.find('[data-testid="file-question-pick-btn"]').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-testid="file-question-path"]').text()).toContain('No file selected');
    const events = wrapper.emitted('update:modelValue') as Array<[string | null]>;
    expect(events[0][0]).toBeNull();
  });

  it('shows clear button after selection and clears on click', async () => {
    const { wrapper } = makeWrapper(() => Promise.resolve('/a/b/c.csv'));
    await wrapper.find('[data-testid="file-question-pick-btn"]').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-testid="file-question-clear"]').exists()).toBe(true);
    await wrapper.find('[data-testid="file-question-clear"]').trigger('click');
    expect(wrapper.find('[data-testid="file-question-path"]').text()).toContain('No file selected');
    const events = wrapper.emitted('update:modelValue') as Array<[string | null]>;
    expect(events[1][0]).toBeNull();
  });

  it('shows error message when pickFile rejects', async () => {
    const { wrapper } = makeWrapper(() => Promise.reject(new Error('dialog closed')));
    await wrapper.find('[data-testid="file-question-pick-btn"]').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-testid="file-question-error"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="file-question-error"]').text()).toContain('dialog closed');
  });
});
