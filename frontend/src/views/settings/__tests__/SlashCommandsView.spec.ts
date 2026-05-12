/**
 * SlashCommandsView.spec.ts
 *
 * Tests for the Settings → Slash Commands list + editor surface.
 * user-slash-commands-01KQ8TD9 WP07.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createRouter, createWebHashHistory } from 'vue-router';

import SlashCommandsView from '@/views/settings/SlashCommandsView.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { UserCommandSummary, UserCommand } from '@/lib/types';

const FAKE_SUMMARY: UserCommandSummary = {
  name: 'standup',
  scope: 'global',
  kind: 'text',
  description: 'Daily standup',
  modelInvokable: false,
};

const FAKE_FULL: UserCommand = {
  name: 'standup',
  scope: 'global',
  kind: 'text',
  description: 'Daily standup',
  modelInvokable: false,
  body: 'What did I do? What am I doing? Any blockers?',
};

function buildClient(overrides: {
  list?: UserCommandSummary[];
  full?: UserCommand;
  saveFn?: ReturnType<typeof vi.fn>;
  deleteFn?: ReturnType<typeof vi.fn>;
}) {
  const saveFn = overrides.saveFn ?? vi.fn(async () => undefined);
  const deleteFn = overrides.deleteFn ?? vi.fn(async () => undefined);
  const client = createFakeHarnessClient({
    slashcmd: {
      list: async () => overrides.list ?? [],
      get: async () => overrides.full ?? FAKE_FULL,
      save: saveFn,
      delete: deleteFn,
      run: async () => ({ kind: 'info' as const, text: '' }),
    },
  });
  return { client, saveFn, deleteFn };
}

// Minimal router to satisfy useRoute / SettingsTabs
const router = createRouter({
  history: createWebHashHistory(),
  routes: [{ path: '/', component: { template: '<div/>' } }],
});

function mountView(
  overrides: Parameters<typeof buildClient>[0] = {},
) {
  const { client, saveFn, deleteFn } = buildClient(overrides);
  const wrapper = mount(SlashCommandsView, {
    global: {
      provide: { [HarnessClientKey as symbol]: client },
      plugins: [router],
      stubs: {
        CanvasHead: { template: '<div><slot name="trailing"/></div>' },
        SettingsTabs: { template: '<div/>' },
      },
    },
  });
  return { wrapper, client, saveFn, deleteFn };
}

describe('SlashCommandsView', () => {
  describe('empty state', () => {
    it('shows empty state when no commands', async () => {
      const { wrapper } = mountView({ list: [] });
      await flushPromises();
      expect(wrapper.find('[data-testid="slashcmds-empty"]').exists()).toBe(true);
    });

    it('empty state CTA opens the editor', async () => {
      const { wrapper } = mountView({ list: [] });
      await flushPromises();
      await wrapper.find('[data-testid="slashcmds-empty-create-btn"]').trigger('click');
      expect(wrapper.find('[data-testid="slash-editor-panel"]').exists()).toBe(true);
    });
  });

  describe('list rendering', () => {
    it('renders command rows', async () => {
      const { wrapper } = mountView({ list: [FAKE_SUMMARY] });
      await flushPromises();
      expect(wrapper.find('[data-testid="slashcmd-row-standup"]').exists()).toBe(true);
    });

    it('renders kind chip', async () => {
      const { wrapper } = mountView({ list: [FAKE_SUMMARY] });
      await flushPromises();
      expect(wrapper.find('[data-testid="slashcmd-kind-chip-standup"]').text()).toBe('text');
    });

    it('renders model-invokable badge when set', async () => {
      const summary: UserCommandSummary = { ...FAKE_SUMMARY, modelInvokable: true };
      const { wrapper } = mountView({ list: [summary] });
      await flushPromises();
      // The badge is present when modelInvokable is true
      expect(
        wrapper.find('[data-testid="slashcmds-table"]').text(),
      ).toContain('AI');
    });
  });

  describe('filters', () => {
    it('filter by kind hides non-matching rows', async () => {
      const text: UserCommandSummary = { ...FAKE_SUMMARY, kind: 'text', name: 'expand' };
      const tool: UserCommandSummary = { ...FAKE_SUMMARY, kind: 'tool', name: 'deploy' };
      const { wrapper } = mountView({ list: [text, tool] });
      await flushPromises();

      // Select 'tool' filter
      await wrapper.find('[data-testid="filter-kind-select"]').setValue('tool');
      await wrapper.vm.$nextTick();

      expect(wrapper.find('[data-testid="slashcmd-row-expand"]').exists()).toBe(false);
      expect(wrapper.find('[data-testid="slashcmd-row-deploy"]').exists()).toBe(true);
    });

    it('model-invokable filter hides non-AI commands', async () => {
      const normal: UserCommandSummary = { ...FAKE_SUMMARY, name: 'normal', modelInvokable: false };
      const ai: UserCommandSummary = { ...FAKE_SUMMARY, name: 'aicmd', modelInvokable: true };
      const { wrapper } = mountView({ list: [normal, ai] });
      await flushPromises();

      await wrapper.find('[data-testid="filter-model-invokable-checkbox"]').setValue(true);
      await wrapper.vm.$nextTick();

      expect(wrapper.find('[data-testid="slashcmd-row-normal"]').exists()).toBe(false);
      expect(wrapper.find('[data-testid="slashcmd-row-aicmd"]').exists()).toBe(true);
    });
  });

  describe('editor open/close', () => {
    it('opens editor on row click', async () => {
      const { wrapper } = mountView({ list: [FAKE_SUMMARY], full: FAKE_FULL });
      await flushPromises();
      await wrapper.find('[data-testid="slashcmd-row-standup"]').trigger('click');
      await flushPromises();
      expect(wrapper.find('[data-testid="slash-editor-panel"]').exists()).toBe(true);
    });

    it('"New command" button opens blank editor', async () => {
      const { wrapper } = mountView({ list: [FAKE_SUMMARY] });
      await flushPromises();
      await wrapper.find('[data-testid="new-slash-command-btn"]').trigger('click');
      expect(wrapper.find('[data-testid="slash-editor-panel"]').exists()).toBe(true);
    });

    it('cancel closes the editor', async () => {
      const { wrapper } = mountView({ list: [FAKE_SUMMARY] });
      await flushPromises();
      await wrapper.find('[data-testid="new-slash-command-btn"]').trigger('click');
      expect(wrapper.find('[data-testid="slash-editor-panel"]').exists()).toBe(true);

      await wrapper.find('[data-testid="cmd-cancel-btn"]').trigger('click');
      expect(wrapper.find('[data-testid="slash-editor-panel"]').exists()).toBe(false);
    });
  });

  describe('save flow', () => {
    it('calls slashcmd.save on editor save', async () => {
      const { wrapper, saveFn } = mountView({ list: [], full: FAKE_FULL });
      await flushPromises();

      await wrapper.find('[data-testid="new-slash-command-btn"]').trigger('click');
      // Fill required fields via the editor
      await wrapper.find('[data-testid="cmd-name-input"]').setValue('greet');
      await wrapper.find('[data-testid="cmd-body-textarea"]').setValue('Hello!');
      // Trigger save
      await wrapper.find('[data-testid="cmd-save-btn"]').trigger('click');
      await flushPromises();

      expect(saveFn).toHaveBeenCalledOnce();
      const arg = saveFn.mock.calls[0][0] as UserCommand;
      expect(arg.name).toBe('greet');
    });
  });
});
