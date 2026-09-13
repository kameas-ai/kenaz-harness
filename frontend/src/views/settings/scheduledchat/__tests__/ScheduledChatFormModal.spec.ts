/**
 * ScheduledChatFormModal.spec.ts
 *
 * Vitest unit tests for the create / edit modal.
 * scheduled-chat-runs-01KX5R8B (WP05).
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import ScheduledChatFormModal from '@/views/settings/scheduledchat/ScheduledChatFormModal.vue';
import {
  createFakeScheduledChatClient,
  type ScheduledChatEntry,
} from '@/lib/scheduledChatClient';

// ── fixtures ────────────────────────────────────────────────────────────────

const STUB_ENTRY: ScheduledChatEntry = {
  id: 'run-1',
  name: 'Daily briefing',
  promptTemplate: 'Summarize {{date}}.',
  cron: '0 9 * * *',
  timezone: 'America/New_York',
  model: 'claude-3-5-sonnet',
  outputSink: 'banner',
  enabled: true,
  createdAt: '2026-01-01T00:00:00Z',
  updatedAt: '2026-01-01T00:00:00Z',
};

const FILE_ENTRY: ScheduledChatEntry = {
  ...STUB_ENTRY,
  id: 'run-2',
  outputSink: 'file:/tmp/briefing.md',
};

// ── helpers ────────────────────────────────────────────────────────────────

function mountCreate() {
  const client = createFakeScheduledChatClient();
  const wrapper = mount(ScheduledChatFormModal, {
    props: { client, editing: null },
    attachTo: document.body,
  });
  return { wrapper, client };
}

function mountEdit(entry: ScheduledChatEntry = STUB_ENTRY) {
  const client = createFakeScheduledChatClient();
  const wrapper = mount(ScheduledChatFormModal, {
    props: { client, editing: entry },
    attachTo: document.body,
  });
  return { wrapper, client };
}

// ── tests ──────────────────────────────────────────────────────────────────

describe('ScheduledChatFormModal', () => {
  it('renders the modal overlay', () => {
    const { wrapper } = mountCreate();
    expect(wrapper.find('[data-testid="scheduled-chat-form-modal"]').exists()).toBe(true);
  });

  it('shows "New scheduled chat" title in create mode', () => {
    const { wrapper } = mountCreate();
    expect(wrapper.find('[data-testid="modal-title"]').text()).toBe('New scheduled chat');
  });

  it('shows "Edit scheduled chat" title in edit mode', () => {
    const { wrapper } = mountEdit();
    expect(wrapper.find('[data-testid="modal-title"]').text()).toBe('Edit scheduled chat');
  });

  it('pre-populates form fields in edit mode', () => {
    const { wrapper } = mountEdit();
    expect((wrapper.find('[data-testid="sc-name-input"]').element as HTMLInputElement).value).toBe(STUB_ENTRY.name);
    expect((wrapper.find('[data-testid="sc-cron-input"]').element as HTMLInputElement).value).toBe(STUB_ENTRY.cron);
    expect((wrapper.find('[data-testid="sc-tz-input"]').element as HTMLInputElement).value).toBe(STUB_ENTRY.timezone);
    expect((wrapper.find('[data-testid="sc-model-input"]').element as HTMLInputElement).value).toBe(STUB_ENTRY.model);
  });

  it('shows file path input when outputSink is file', () => {
    const { wrapper } = mountEdit(FILE_ENTRY);
    expect(wrapper.find('[data-testid="sc-file-path-input"]').exists()).toBe(true);
    expect((wrapper.find('[data-testid="sc-file-path-input"]').element as HTMLInputElement).value).toBe('/tmp/briefing.md');
  });

  it('hides file path input when outputSink is banner', () => {
    const { wrapper } = mountCreate();
    expect(wrapper.find('[data-testid="sc-file-path-input"]').exists()).toBe(false);
  });

  it('disables Save when cron is invalid', async () => {
    const { wrapper } = mountCreate();
    const cronInput = wrapper.find('[data-testid="sc-cron-input"]');
    await cronInput.setValue('not-a-cron');
    const saveBtn = wrapper.find('[data-testid="modal-save"]') as any;
    expect(saveBtn.element.disabled).toBe(true);
  });

  it('enables Save when cron is valid', async () => {
    const { wrapper } = mountCreate();
    const saveBtn = wrapper.find('[data-testid="modal-save"]') as any;
    // Default cron "0 9 * * *" is valid
    expect(saveBtn.element.disabled).toBe(false);
  });

  it('disables Save when sink=file but filePath is empty', async () => {
    const { wrapper } = mountCreate();
    await wrapper.find('[data-testid="sc-sink-file"]').trigger('click');
    // file path input appears but is empty
    const saveBtn = wrapper.find('[data-testid="modal-save"]') as any;
    expect(saveBtn.element.disabled).toBe(true);
  });

  it('emits cancel when close button is clicked', async () => {
    const { wrapper } = mountCreate();
    await wrapper.find('[data-testid="modal-close"]').trigger('click');
    expect(wrapper.emitted('cancel')).toBeTruthy();
  });

  it('emits cancel when Cancel button is clicked', async () => {
    const { wrapper } = mountCreate();
    await wrapper.find('[data-testid="modal-cancel"]').trigger('click');
    expect(wrapper.emitted('cancel')).toBeTruthy();
  });

  it('calls client.create and emits saved in create mode', async () => {
    const createMock = vi.fn().mockResolvedValue({ ...STUB_ENTRY, id: 'new-id' });
    const client = createFakeScheduledChatClient({ create: createMock });
    const wrapper = mount(ScheduledChatFormModal, {
      props: { client, editing: null },
      attachTo: document.body,
    });
    await wrapper.find('[data-testid="sc-name-input"]').setValue('Test run');
    // submit the form
    await wrapper.find('form').trigger('submit');
    await flushPromises();
    expect(createMock).toHaveBeenCalled();
    const savedEvent = wrapper.emitted('saved');
    expect(savedEvent).toBeTruthy();
    expect((savedEvent![0] as ScheduledChatEntry[])[0].id).toBe('new-id');
  });

  it('calls client.update and emits saved in edit mode', async () => {
    const updateMock = vi.fn().mockResolvedValue({ ...STUB_ENTRY, name: 'Updated' });
    const client = createFakeScheduledChatClient({ update: updateMock });
    const wrapper = mount(ScheduledChatFormModal, {
      props: { client, editing: STUB_ENTRY },
      attachTo: document.body,
    });
    await wrapper.find('[data-testid="sc-name-input"]').setValue('Updated');
    await wrapper.find('form').trigger('submit');
    await flushPromises();
    expect(updateMock).toHaveBeenCalledWith(expect.objectContaining({ id: STUB_ENTRY.id, name: 'Updated' }));
    expect(wrapper.emitted('saved')).toBeTruthy();
  });

  it('shows save error when create rejects', async () => {
    const client = createFakeScheduledChatClient({
      create: vi.fn().mockRejectedValue(new Error('server error')),
    });
    const wrapper = mount(ScheduledChatFormModal, {
      props: { client, editing: null },
      attachTo: document.body,
    });
    await wrapper.find('form').trigger('submit');
    await flushPromises();
    expect(wrapper.find('[data-testid="sc-save-error"]').text()).toContain('server error');
  });

  it('shows cron validation error on bad expression', async () => {
    const { wrapper } = mountCreate();
    await wrapper.find('[data-testid="sc-cron-input"]').setValue('bad');
    // The error text appears below the input
    expect(wrapper.text()).toContain('valid 5-field cron');
  });

  // ── one-shot schedules (model-scheduled-jobs-01PMSJ01 WP08, FR-006) ──────

  it('defaults to the cron trigger and shows the cron field', () => {
    const { wrapper } = mountCreate();
    expect((wrapper.find('[data-testid="sc-trigger-cron"]').element as HTMLInputElement).checked).toBe(true);
    expect(wrapper.find('[data-testid="sc-cron-input"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="sc-run-at-input"]').exists()).toBe(false);
  });

  it('switches to the run-at field and hides cron when "Once" is selected', async () => {
    const { wrapper } = mountCreate();
    await wrapper.find('[data-testid="sc-trigger-once"]').trigger('click');
    expect(wrapper.find('[data-testid="sc-cron-input"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="sc-run-at-input"]').exists()).toBe(true);
  });

  it('disables Save for a "once" trigger with no run-at chosen', async () => {
    const { wrapper } = mountCreate();
    await wrapper.find('[data-testid="sc-trigger-once"]').trigger('click');
    const saveBtn = wrapper.find('[data-testid="modal-save"]') as any;
    expect(saveBtn.element.disabled).toBe(true);
  });

  it('enables Save for a "once" trigger once a run-at is chosen, without requiring a cron expression', async () => {
    const { wrapper } = mountCreate();
    await wrapper.find('[data-testid="sc-trigger-once"]').trigger('click');
    await wrapper.find('[data-testid="sc-run-at-input"]').setValue('2030-01-01T09:00');
    const saveBtn = wrapper.find('[data-testid="modal-save"]') as any;
    expect(saveBtn.element.disabled).toBe(false);
  });

  it('sends triggerKind=once and an empty cron on create for a one-shot schedule', async () => {
    const createMock = vi.fn().mockResolvedValue({ ...STUB_ENTRY, id: 'new-id', triggerKind: 'once' });
    const client = createFakeScheduledChatClient({ create: createMock });
    const wrapper = mount(ScheduledChatFormModal, {
      props: { client, editing: null },
      attachTo: document.body,
    });
    await wrapper.find('[data-testid="sc-trigger-once"]').trigger('click');
    await wrapper.find('[data-testid="sc-run-at-input"]').setValue('2030-01-01T09:00');
    await wrapper.find('form').trigger('submit');
    await flushPromises();
    expect(createMock).toHaveBeenCalledWith(
      expect.objectContaining({ triggerKind: 'once', cron: '' }),
    );
    const call = createMock.mock.calls[0][0];
    expect(typeof call.runAt).toBe('string');
    expect(call.runAt).not.toBe('');
  });

  it('pre-populates the "once" trigger and run-at when editing a one-shot entry', () => {
    const onceEntry: ScheduledChatEntry = {
      ...STUB_ENTRY,
      triggerKind: 'once',
      runAt: '2030-06-15T13:00:00Z',
    };
    const { wrapper } = mountEdit(onceEntry);
    expect((wrapper.find('[data-testid="sc-trigger-once"]').element as HTMLInputElement).checked).toBe(true);
    expect(wrapper.find('[data-testid="sc-run-at-input"]').exists()).toBe(true);
    expect((wrapper.find('[data-testid="sc-run-at-input"]').element as HTMLInputElement).value).not.toBe('');
  });

  it('shows "Save changes" label in edit mode and "Create" in create mode', () => {
    const { wrapper: editWrapper } = mountEdit();
    expect(wrapper_saveBtnText(editWrapper)).toBe('Save changes');

    const { wrapper: createWrapper } = mountCreate();
    expect(wrapper_saveBtnText(createWrapper)).toBe('Create');
  });
});

function wrapper_saveBtnText(wrapper: ReturnType<typeof mount>) {
  return wrapper.find('[data-testid="modal-save"]').text();
}
