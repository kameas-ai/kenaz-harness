/**
 * SlashCommandsView.spec.ts
 *
 * Tests for the Settings → Slash Commands list + editor surface.
 * user-slash-commands-01KQ8TD9 WP07.
 * fleet-skills-sync-01NDFSEX18 WP03 — "Publish to team" button (FR-101/102).
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createRouter, createWebHashHistory } from 'vue-router';

import SlashCommandsView from '@/views/settings/SlashCommandsView.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import { initFeatureFlags } from '@/lib/featureFlags';
import type { UserCommandSummary, UserCommand, AppInfo } from '@/lib/types';

// Suppress toast side effects in tests
vi.mock('@/composables/useToastQueue', () => ({
  push: vi.fn(),
}));

// Helper to build the minimal AppInfo shape used by initFeatureFlags.
function makeAppInfo(caps: Record<string, boolean>): AppInfo {
  return {
    build: 'test',
    commit: 'test',
    buildTime: '',
    goVersion: '',
    platform: 'test',
    windowSize: { width: 1280, height: 800 },
    capabilities: caps,
  };
}

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
  skillPublishFn?: ReturnType<typeof vi.fn>;
}) {
  const saveFn = overrides.saveFn ?? vi.fn(async () => undefined);
  const deleteFn = overrides.deleteFn ?? vi.fn(async () => undefined);
  const skillPublishFn = overrides.skillPublishFn ?? vi.fn(async () => undefined);
  const client = createFakeHarnessClient({
    slashcmd: {
      list: async () => overrides.list ?? [],
      get: async () => overrides.full ?? FAKE_FULL,
      save: saveFn,
      delete: deleteFn,
      run: async () => ({ kind: 'info' as const, text: '' }),
      skillList: async () => [],
      skillInstall: vi.fn(async () => undefined),
      skillUninstall: vi.fn(async () => undefined),
      skillPublish: skillPublishFn,
      skillRenameLocalTrigger: vi.fn(async () => undefined),
    },
  });
  return { client, saveFn, deleteFn, skillPublishFn };
}

// Minimal router to satisfy useRoute / SettingsTabs
const router = createRouter({
  history: createWebHashHistory(),
  routes: [{ path: '/', component: { template: '<div/>' } }],
});

function mountView(
  overrides: Parameters<typeof buildClient>[0] = {},
) {
  const { client, saveFn, deleteFn, skillPublishFn } = buildClient(overrides);
  const wrapper = mount(SlashCommandsView, {
    global: {
      provide: { [HarnessClientKey as symbol]: client },
      plugins: [router],
      stubs: {
        CanvasHead: { template: '<div><slot name="trailing"/></div>' },
        SettingsTabs: { template: '<div/>' },
        // Stub ConfirmDialog to render inline (avoiding Teleport in jsdom tests).
        ConfirmDialog: {
          props: ['open', 'title', 'message', 'confirmLabel', 'cancelLabel', 'danger'],
          template: `
            <div v-if="open" data-testid="confirm-dialog-stub">
              <button data-testid="confirm-dialog-confirm" @click="$emit('confirm')">Confirm</button>
              <button data-testid="confirm-dialog-cancel" @click="$emit('cancel')">Cancel</button>
            </div>
          `,
          emits: ['confirm', 'cancel'],
        },
      },
    },
  });
  return { wrapper, client, saveFn, deleteFn, skillPublishFn };
}

afterEach(() => {
  // Reset featureFlags state between tests to avoid capability bleed.
  initFeatureFlags(null);
});

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

  // ── "Publish to team" (fleet-skills-sync-01NDFSEX18 WP03, FR-101/102) ──

  describe('Publish to team (FR-101/102)', () => {
    it('shows "Publish to team" button when editing an existing command and user has shared_team_graph', async () => {
      initFeatureFlags(makeAppInfo({ shared_team_graph: true }));
      const { wrapper } = mountView({ list: [FAKE_SUMMARY], full: FAKE_FULL });
      await flushPromises();

      await wrapper.find('[data-testid="slashcmd-row-standup"]').trigger('click');
      await flushPromises();

      expect(wrapper.find('[data-testid="publish-to-team-btn"]').exists()).toBe(true);
    });

    it('shows "Team+ required" note when user lacks shared_team_graph capability', async () => {
      initFeatureFlags(makeAppInfo({}));
      const { wrapper } = mountView({ list: [FAKE_SUMMARY], full: FAKE_FULL });
      await flushPromises();

      await wrapper.find('[data-testid="slashcmd-row-standup"]').trigger('click');
      await flushPromises();

      expect(wrapper.find('[data-testid="publish-to-team-btn"]').exists()).toBe(false);
      expect(wrapper.find('[data-testid="publish-to-team-unavailable"]').exists()).toBe(true);
    });

    it('does not show publish section when creating a new command', async () => {
      initFeatureFlags(makeAppInfo({ shared_team_graph: true }));
      const { wrapper } = mountView({ list: [] });
      await flushPromises();

      await wrapper.find('[data-testid="new-slash-command-btn"]').trigger('click');
      await wrapper.vm.$nextTick();

      // publish section only shown for existing (non-null) commands
      expect(wrapper.find('[data-testid="publish-to-team-section"]').exists()).toBe(false);
    });

    it('calls slashcmd.skillPublish with confirmed dialog', async () => {
      initFeatureFlags(makeAppInfo({ shared_team_graph: true }));
      const skillPublishFn = vi.fn(async () => undefined);
      const { wrapper } = mountView({ list: [FAKE_SUMMARY], full: FAKE_FULL, skillPublishFn });
      await flushPromises();

      await wrapper.find('[data-testid="slashcmd-row-standup"]').trigger('click');
      await flushPromises();

      const publishBtn = wrapper.find('[data-testid="publish-to-team-btn"]');
      expect(publishBtn.exists()).toBe(true);
      await publishBtn.trigger('click');
      await wrapper.vm.$nextTick();

      // ConfirmDialog should be open — confirm it
      const confirmBtn = wrapper.find('[data-testid="confirm-dialog-confirm"]');
      expect(confirmBtn.exists()).toBe(true);
      await confirmBtn.trigger('click');
      await flushPromises();

      expect(skillPublishFn).toHaveBeenCalledWith('standup', '', 'team');
    });

    it('does not call skillPublish when confirm dialog is cancelled', async () => {
      initFeatureFlags(makeAppInfo({ shared_team_graph: true }));
      const skillPublishFn = vi.fn(async () => undefined);
      const { wrapper } = mountView({ list: [FAKE_SUMMARY], full: FAKE_FULL, skillPublishFn });
      await flushPromises();

      await wrapper.find('[data-testid="slashcmd-row-standup"]').trigger('click');
      await flushPromises();

      await wrapper.find('[data-testid="publish-to-team-btn"]').trigger('click');
      await wrapper.vm.$nextTick();

      const cancelBtn = wrapper.find('[data-testid="confirm-dialog-cancel"]');
      expect(cancelBtn.exists()).toBe(true);
      await cancelBtn.trigger('click');
      await flushPromises();

      expect(skillPublishFn).not.toHaveBeenCalled();
    });
  });
});
