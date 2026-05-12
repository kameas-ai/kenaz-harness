/**
 * HooksPanel.spec.ts
 *
 * Vitest unit tests for the Settings → Hooks panel surface.
 * hooks-event-surface-expansion-01KZNP3A WP07c.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createRouter, createWebHashHistory } from 'vue-router';

import HooksPanel from '@/components/settings/HooksPanel.vue';
import HookEditor from '@/components/settings/HookEditor.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { Hook, BuiltinDescriptor, DryRunResult } from '@/lib/types';

// ── fixtures ────────────────────────────────────────────────────────────

const FAKE_HOOK: Hook = {
  id: 'hk-test',
  name: 'Test hook',
  event: 'pre_send',
  kind: 'shell',
  enabled: true,
  match: {},
  command: 'echo {}',
};

const FAKE_BUILTIN_DESC: BuiltinDescriptor = {
  id: 'memory.retrieve',
  name: 'Memory retrieve',
  description: 'Retrieve memory chunks',
  events: ['pre_send'],
};

const BLANK_DRY_RUN: DryRunResult = {
  output: { decision: 'approve' },
  merged: { blocked: false, permissionDenied: false, permissionAllowed: false },
  stdout: '',
  stderr: '',
  exitCode: 0,
  latencyMs: 5,
};

// ── helpers ─────────────────────────────────────────────────────────────

const router = createRouter({
  history: createWebHashHistory(),
  routes: [{ path: '/', component: { template: '<div/>' } }],
});

function buildClient(overrides: {
  list?: Hook[];
  dryRunFn?: ReturnType<typeof vi.fn>;
  removeFn?: ReturnType<typeof vi.fn>;
}) {
  const removeFn = overrides.removeFn ?? vi.fn(async () => undefined);
  const dryRunFn = overrides.dryRunFn ?? vi.fn(async () => BLANK_DRY_RUN);
  const client = createFakeHarnessClient({
    hooks: {
      list: async () => overrides.list ?? [],
      get: async (id) => ({ id, name: id, event: 'pre_send', kind: 'shell', enabled: true, match: {} }),
      add: async (h) => h,
      update: async () => undefined,
      remove: removeFn,
      availableBuiltins: async () => [FAKE_BUILTIN_DESC],
      installStarterMemory: async () => undefined,
      removeStarterMemory: async () => undefined,
      dryRun: dryRunFn,
    },
  });
  return { client, removeFn, dryRunFn };
}

function mountPanel(
  hooks: Hook[] = [],
  options: { flagEnabled?: boolean; error?: string } = {},
) {
  return mount(HooksPanel, {
    props: {
      hooks,
      builtins: [FAKE_BUILTIN_DESC],
      loading: false,
      error: options.error ?? null,
      flagEnabled: options.flagEnabled ?? true,
    },
    global: {
      plugins: [router],
      stubs: {
        Button: {
          template: '<button type="button" v-bind="$attrs" @click="$emit(\'click\')"><slot/></button>',
          emits: ['click'],
        },
      },
    },
  });
}

function mountEditor(hook: Hook | null = null) {
  return mount(HookEditor, {
    props: {
      hook,
      builtins: [FAKE_BUILTIN_DESC],
      saving: false,
    },
    global: {
      plugins: [router],
      stubs: {
        Button: {
          template: '<button type="button" v-bind="$attrs" @click="$emit(\'click\')"><slot/></button>',
          emits: ['click'],
        },
      },
    },
  });
}

// ── HooksPanel tests ─────────────────────────────────────────────────────

describe('HooksPanel', () => {
  describe('flag disabled', () => {
    it('renders flag-disabled notice when flagEnabled=false', () => {
      const wrapper = mountPanel([], { flagEnabled: false });
      expect(wrapper.find('[data-testid="hooks-flag-disabled"]').exists()).toBe(true);
      expect(wrapper.find('[data-testid="hooks-table"]').exists()).toBe(false);
    });

    it('does not show table when flag disabled even if hooks exist', () => {
      const wrapper = mountPanel([FAKE_HOOK], { flagEnabled: false });
      expect(wrapper.find('[data-testid="hooks-table"]').exists()).toBe(false);
    });
  });

  describe('empty state', () => {
    it('shows empty state when no hooks', () => {
      const wrapper = mountPanel([]);
      expect(wrapper.find('[data-testid="hooks-empty"]').exists()).toBe(true);
      expect(wrapper.find('[data-testid="hooks-table"]').exists()).toBe(false);
    });

    it('shows install-starter button in empty state', () => {
      const wrapper = mountPanel([]);
      expect(wrapper.find('[data-testid="hooks-install-starter-btn"]').exists()).toBe(true);
    });
  });

  describe('list rendering', () => {
    it('renders a row per hook when hooks exist', () => {
      const wrapper = mountPanel([FAKE_HOOK]);
      expect(wrapper.find('[data-testid="hooks-table"]').exists()).toBe(true);
      expect(wrapper.find(`[data-testid="hook-row-${FAKE_HOOK.id}"]`).exists()).toBe(true);
    });

    it('shows hook name and id in row', () => {
      const wrapper = mountPanel([FAKE_HOOK]);
      const row = wrapper.find(`[data-testid="hook-row-${FAKE_HOOK.id}"]`);
      expect(row.text()).toContain(FAKE_HOOK.name);
      expect(row.text()).toContain(FAKE_HOOK.id);
    });

    it('shows error banner when error prop set', () => {
      const wrapper = mountPanel([], { error: 'load failed' });
      expect(wrapper.find('[data-testid="hooks-panel-error"]').text()).toContain('load failed');
    });
  });

  describe('toggle emit', () => {
    it('emits toggle when checkbox changes', async () => {
      const wrapper = mountPanel([FAKE_HOOK]);
      const checkbox = wrapper.find(`[data-testid="hook-toggle-${FAKE_HOOK.id}"]`);
      await checkbox.trigger('change');
      expect(wrapper.emitted('toggle')).toBeTruthy();
      expect((wrapper.emitted('toggle') as Hook[][])[0][0].id).toBe(FAKE_HOOK.id);
    });
  });

  describe('edit emit', () => {
    it('emits edit when row clicked', async () => {
      const wrapper = mountPanel([FAKE_HOOK]);
      await wrapper.find(`[data-testid="hook-row-${FAKE_HOOK.id}"]`).trigger('click');
      expect(wrapper.emitted('edit')).toBeTruthy();
    });
  });

  describe('add emit', () => {
    it('emits add when Add hook button clicked', async () => {
      const wrapper = mountPanel([]);
      // In empty state the "add custom hook" button is present
      await wrapper.find('[data-testid="hooks-add-first-btn"]').trigger('click');
      expect(wrapper.emitted('add')).toBeTruthy();
    });
  });
});

// ── HookEditor tests ──────────────────────────────────────────────────────

describe('HookEditor', () => {
  describe('create mode', () => {
    it('renders in create mode when hook is null', () => {
      const wrapper = mountEditor(null);
      expect(wrapper.find('[data-testid="hook-editor"]').exists()).toBe(true);
      expect(wrapper.text()).toContain('ADD HOOK');
    });

    it('shows all 18 events in the event picker', () => {
      const wrapper = mountEditor(null);
      const options = wrapper.find('[data-testid="hook-editor-event"]').findAll('option');
      expect(options.length).toBe(18);
    });

    it('shows all 3 kinds in the kind picker', () => {
      const wrapper = mountEditor(null);
      const options = wrapper.find('[data-testid="hook-editor-kind"]').findAll('option');
      expect(options.length).toBe(3);
    });
  });

  describe('edit mode', () => {
    it('renders in edit mode with hook data', () => {
      const wrapper = mountEditor(FAKE_HOOK);
      expect(wrapper.text()).toContain('EDIT HOOK');
      const nameInput = wrapper.find('[data-testid="hook-editor-name"]');
      expect((nameInput.element as HTMLInputElement).value).toBe(FAKE_HOOK.name);
    });
  });

  describe('validation', () => {
    it('shows validation error when save clicked with empty name', async () => {
      const wrapper = mountEditor(null);
      // Clear the name field
      const nameInput = wrapper.find('[data-testid="hook-editor-name"]');
      await nameInput.setValue('');
      await wrapper.find('[data-testid="hook-editor-save"]').trigger('click');
      expect(wrapper.find('[data-testid="hook-editor-validation-error"]').exists()).toBe(true);
    });

    it('shows validation error for shell hook with empty command', async () => {
      const wrapper = mountEditor({ ...FAKE_HOOK, command: '' });
      await wrapper.find('[data-testid="hook-editor-save"]').trigger('click');
      const errEl = wrapper.find('[data-testid="hook-editor-validation-error"]');
      expect(errEl.exists()).toBe(true);
      expect(errEl.text()).toContain('Command');
    });

    it('emits save with hook when valid', async () => {
      const wrapper = mountEditor(FAKE_HOOK);
      await wrapper.find('[data-testid="hook-editor-save"]').trigger('click');
      expect(wrapper.emitted('save')).toBeTruthy();
    });
  });

  describe('kind-conditional fields', () => {
    it('shows command textarea for shell kind', async () => {
      const wrapper = mountEditor(null);
      // Default kind after creation may be 'shell'
      const kindSelect = wrapper.find('[data-testid="hook-editor-kind"]');
      await kindSelect.setValue('shell');
      expect(wrapper.find('[data-testid="hook-editor-command"]').exists()).toBe(true);
    });

    it('shows builtin picker for builtin kind', async () => {
      const wrapper = mountEditor(null);
      await wrapper.find('[data-testid="hook-editor-kind"]').setValue('builtin');
      expect(wrapper.find('[data-testid="hook-editor-builtin"]').exists()).toBe(true);
    });

    it('shows mcp tool field for mcp kind', async () => {
      const wrapper = mountEditor(null);
      await wrapper.find('[data-testid="hook-editor-kind"]').setValue('mcp');
      expect(wrapper.find('[data-testid="hook-editor-mcp-tool"]').exists()).toBe(true);
    });
  });

  describe('dry-run emit', () => {
    it('emits dryRun with the current draft when Dry run clicked', async () => {
      const wrapper = mountEditor(FAKE_HOOK);
      await wrapper.find('[data-testid="hook-editor-dry-run"]').trigger('click');
      expect(wrapper.emitted('dryRun')).toBeTruthy();
      const emittedHook = (wrapper.emitted('dryRun') as Hook[][])[0][0];
      expect(emittedHook.id).toBe(FAKE_HOOK.id);
    });
  });
});

// ── integration: HooksSettingsView (smoke) ───────────────────────────────

describe('HooksSettingsView smoke', () => {
  it('renders the hooks panel via the settings view path', async () => {
    // We mount HooksPanel directly here (the real integration test lives
    // in HooksSettingsView.vue which needs the full app context).
    // This test just verifies that the component tree composes without errors.
    const { client } = buildClient({ list: [FAKE_HOOK] });
    const wrapper = mount(HooksPanel, {
      props: {
        hooks: [FAKE_HOOK],
        builtins: [FAKE_BUILTIN_DESC],
        loading: false,
        error: null,
        flagEnabled: true,
      },
      global: {
        provide: { [HarnessClientKey as symbol]: client },
        plugins: [router],
        stubs: {
          Button: {
            template: '<button type="button" v-bind="$attrs" @click="$emit(\'click\')"><slot/></button>',
            emits: ['click'],
          },
        },
      },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="hooks-table"]').exists()).toBe(true);
  });
});
