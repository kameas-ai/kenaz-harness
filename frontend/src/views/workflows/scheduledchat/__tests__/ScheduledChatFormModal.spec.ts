/**
 * ScheduledChatFormModal.spec.ts
 *
 * Vitest unit tests for the create / edit modal.
 * scheduled-chat-runs-01KX5R8B (WP05).
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import ScheduledChatFormModal from '@/views/workflows/scheduledchat/ScheduledChatFormModal.vue';
import {
  createFakeScheduledChatClient,
  type ScheduledChatEntry,
} from '@/lib/scheduledChatClient';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { ServerSchemaCost } from '@/lib/types';

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
    global: { provide: { [HarnessClientKey as symbol]: createFakeHarnessClient() } },
    attachTo: document.body,
  });
  return { wrapper, client };
}

function mountEdit(entry: ScheduledChatEntry = STUB_ENTRY) {
  const client = createFakeScheduledChatClient();
  const wrapper = mount(ScheduledChatFormModal, {
    props: { client, editing: entry },
    global: { provide: { [HarnessClientKey as symbol]: createFakeHarnessClient() } },
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
    const wrapper = mount(ScheduledChatFormModal, { global: { provide: { [HarnessClientKey as symbol]: createFakeHarnessClient() } }, 
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
    const wrapper = mount(ScheduledChatFormModal, { global: { provide: { [HarnessClientKey as symbol]: createFakeHarnessClient() } }, 
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
    const wrapper = mount(ScheduledChatFormModal, { global: { provide: { [HarnessClientKey as symbol]: createFakeHarnessClient() } }, 
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
    const wrapper = mount(ScheduledChatFormModal, { global: { provide: { [HarnessClientKey as symbol]: createFakeHarnessClient() } }, 
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

// ── tool set (tool-context-budget-01TCBUD01 WP06, FR-K3) ─────────────────────

describe('ScheduledChatFormModal — tool set', () => {
  it('shows the default tool set’s per-request cost; Custom servers is disabled with its reason', async () => {
    const base = createFakeHarnessClient();
    const schemaCosts = vi.fn(async (): Promise<ServerSchemaCost[]> => [
      {
        server: 'kenaz', state: 'running', running: true, toolCount: 3, tokenEst: 5000,
        tier: 'mixed', source: '', pinned: false, sendableTokenEst: 4200,
        tools: [
          { name: 'read_file', tokenEst: 2000, tier: 'full', source: 'default', activated: false, sendable: true, hot: false },
          { name: 'bash', tokenEst: 2200, tier: 'full', source: 'default', activated: false, sendable: true, hot: false },
          { name: 'monitor', tokenEst: 800, tier: 'summary', source: 'default', activated: false, sendable: false, hot: false },
        ],
      },
    ]);
    const wrapper = mount(ScheduledChatFormModal, {
      props: { client: createFakeScheduledChatClient(), editing: null },
      global: { provide: { [HarnessClientKey as symbol]: { ...base, tools: { ...base.tools, schemaCosts } } } },
    });
    await flushPromises();
    // Schedules have no project and no session: the user's default tiers.
    expect(schemaCosts).toHaveBeenCalledWith('', '');
    expect(wrapper.find('[data-testid="sc-tool-set-cost"]').text()).toBe(
      'Each request sends 2 tool definitions (~4.2k tokens); the run can load more as it needs them.',
    );
    expect(wrapper.find('[data-testid="sc-tool-set-custom"]').attributes('disabled')).toBeDefined();
    expect(wrapper.find('#sc-tool-set-custom-reason').text()).toContain('not available yet');
  });

  it('renders no cost line when the read fails', async () => {
    const base = createFakeHarnessClient();
    const wrapper = mount(ScheduledChatFormModal, {
      props: { client: createFakeScheduledChatClient(), editing: null },
      global: {
        provide: {
          [HarnessClientKey as symbol]: {
            ...base,
            tools: { ...base.tools, schemaCosts: async () => { throw new Error('boom'); } },
          },
        },
      },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="sc-tool-set-cost"]').exists()).toBe(false);
  });

  it('a contained schedule says its cost is not shown', async () => {
    const { wrapper } = mountEdit({ ...STUB_ENTRY, toolAllowlist: ['fetch__fetch', 'kenaz__read_file'] });
    await flushPromises();
    expect(wrapper.find('[data-testid="sc-tool-set-cost"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="sc-tool-set-contained"]').text()).toContain('Contained run');
  });

  it('the tool-set radios share a group name', () => {
    const { wrapper } = mountCreate();
    expect(wrapper.find('[data-testid="sc-tool-set-default"]').attributes('name')).toBe('sc-tool-set');
    expect(wrapper.find('[data-testid="sc-tool-set-custom"]').attributes('name')).toBe('sc-tool-set');
    expect(wrapper.find('fieldset[data-testid="sc-tool-set"] legend').text()).toBe('Tool set');
  });
});

describe('ScheduledChatFormModal — conversation memory (scheduled-chat-context-budget)', () => {
  it('shows the context-policy defaults on a new schedule', () => {
    const { wrapper } = mountCreate();
    expect((wrapper.find('[data-testid="sc-budget-input"]').element as HTMLInputElement).value).toBe('20000');
    expect((wrapper.find('[data-testid="sc-keep-input"]').element as HTMLInputElement).value).toBe('2');
    expect((wrapper.find('[data-testid="sc-memory-focus-input"]').element as HTMLTextAreaElement).value).toBe('');
  });

  it('pre-populates the context policy when editing', () => {
    const { wrapper } = mountEdit({ ...STUB_ENTRY, contextBudgetTokens: 12000, keepRecentRuns: 0, memoryFocus: 'open items' });
    expect((wrapper.find('[data-testid="sc-budget-input"]').element as HTMLInputElement).value).toBe('12000');
    expect((wrapper.find('[data-testid="sc-keep-input"]').element as HTMLInputElement).value).toBe('0');
    expect((wrapper.find('[data-testid="sc-memory-focus-input"]').element as HTMLTextAreaElement).value).toBe('open items');
  });

  it('sends the context policy on create and update', async () => {
    const createMock = vi.fn().mockResolvedValue({ ...STUB_ENTRY, id: 'new-id' });
    const { wrapper } = (() => {
      const client = createFakeScheduledChatClient({ create: createMock });
      const w = mount(ScheduledChatFormModal, {
        props: { client, editing: null },
        global: { provide: { [HarnessClientKey as symbol]: createFakeHarnessClient() } },
        attachTo: document.body,
      });
      return { wrapper: w };
    })();
    await wrapper.find('[data-testid="sc-budget-input"]').setValue('8000');
    await wrapper.find('[data-testid="sc-keep-input"]').setValue('3');
    await wrapper.find('[data-testid="sc-memory-focus-input"]').setValue('  track open items  ');
    await wrapper.find('form').trigger('submit');
    await flushPromises();
    expect(createMock).toHaveBeenCalledWith(
      expect.objectContaining({ contextBudgetTokens: 8000, keepRecentRuns: 3, memoryFocus: 'track open items' }),
    );

    const updateMock = vi.fn().mockResolvedValue(STUB_ENTRY);
    const client = createFakeScheduledChatClient({ update: updateMock });
    const edit = mount(ScheduledChatFormModal, {
      props: { client, editing: { ...STUB_ENTRY, contextBudgetTokens: 12000, keepRecentRuns: 0, memoryFocus: 'x' } },
      global: { provide: { [HarnessClientKey as symbol]: createFakeHarnessClient() } },
      attachTo: document.body,
    });
    await edit.find('form').trigger('submit');
    await flushPromises();
    expect(updateMock).toHaveBeenCalledWith(
      expect.objectContaining({ id: STUB_ENTRY.id, contextBudgetTokens: 12000, keepRecentRuns: 0, memoryFocus: 'x' }),
    );
  });

  it.each([
    ['3999', 'At least 4,000 tokens'],
    ['', 'whole number'],
    ['4500.5', 'whole number'],
  ])('rejects a context budget of %j', async (value, message) => {
    const { wrapper } = mountCreate();
    await wrapper.find('[data-testid="sc-budget-input"]').setValue(value);
    expect(wrapper.find('[data-testid="sc-budget-error"]').text()).toContain(message);
    expect((wrapper.find('[data-testid="modal-save"]').element as HTMLButtonElement).disabled).toBe(true);
  });

  it('caps the budget at the default model\'s context window', async () => {
    const client = createFakeScheduledChatClient({
      defaultModel: () => Promise.resolve({ profileId: 'p', model: 'm', maxContextTokens: 32000 }),
    });
    const wrapper = mount(ScheduledChatFormModal, {
      props: { client, editing: null },
      global: { provide: { [HarnessClientKey as symbol]: createFakeHarnessClient() } },
      attachTo: document.body,
    });
    await flushPromises();
    await wrapper.find('[data-testid="sc-budget-input"]').setValue('32001');
    expect(wrapper.find('[data-testid="sc-budget-error"]').text()).toContain("32,000 tokens (the model's context window)");
    await wrapper.find('[data-testid="sc-budget-input"]').setValue('32000');
    expect(wrapper.find('[data-testid="sc-budget-error"]').exists()).toBe(false);
    // A named model is checked by the server against its own window.
    await wrapper.find('[data-testid="sc-budget-input"]').setValue('64000');
    await wrapper.find('[data-testid="sc-model-input"]').setValue('other-model');
    expect(wrapper.find('[data-testid="sc-budget-error"]').exists()).toBe(false);
  });

  it.each([['-1'], ['11'], ['']])('rejects keeping %j recent runs', async (value) => {
    const { wrapper } = mountCreate();
    await wrapper.find('[data-testid="sc-keep-input"]').setValue(value);
    expect(wrapper.find('[data-testid="sc-keep-error"]').text()).toContain('Between 0 and 10');
    expect((wrapper.find('[data-testid="modal-save"]').element as HTMLButtonElement).disabled).toBe(true);
  });

  it('accepts keeping 0 and 10 recent runs', async () => {
    const { wrapper } = mountCreate();
    for (const v of ['0', '10']) {
      await wrapper.find('[data-testid="sc-keep-input"]').setValue(v);
      expect(wrapper.find('[data-testid="sc-keep-error"]').exists()).toBe(false);
    }
  });

  it('rejects a memory focus over 2000 characters', async () => {
    const { wrapper } = mountCreate();
    await wrapper.find('[data-testid="sc-memory-focus-input"]').setValue('x'.repeat(2001));
    expect(wrapper.find('[data-testid="sc-memory-focus-error"]').text()).toContain('2000 characters');
    expect((wrapper.find('[data-testid="modal-save"]').element as HTMLButtonElement).disabled).toBe(true);
  });
});
