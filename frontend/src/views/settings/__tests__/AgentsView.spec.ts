/**
 * AgentsView.spec.ts
 *
 * Tests for the Settings → Agents panel (sub-agent profile registry).
 * branch-subagent-interactive-01KZNP3B WP05.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { nextTick } from 'vue';

import AgentsView from '@/views/settings/AgentsView.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { AgentProfileSummary, AgentProfile } from '@/lib/types';

const BUNDLED_EXPLORE: AgentProfileSummary = {
  id: 'explore',
  name: 'Explore',
  description: 'Read-only research worker.',
  model: 'anthropic/claude-haiku-4-5',
  mergePolicy: 'auto',
  bundled: true,
};

const USER_PROFILE: AgentProfileSummary = {
  id: 'my-agent',
  name: 'My Agent',
  description: 'Custom agent.',
  mergePolicy: 'confirm',
  bundled: false,
};

const FULL_EXPLORE: AgentProfile = {
  id: 'explore',
  name: 'Explore',
  description: 'Read-only research worker.',
  model: 'anthropic/claude-haiku-4-5',
  autonomyTier: 'cautious',
  allowedTools: ['kaneaz__read_file'],
  deniedTools: ['kaneaz__bash'],
  budgetTokens: 50000,
  budgetTimeS: 300,
  mergePolicy: 'auto',
  bundled: true,
};

function buildClient(overrides: {
  profiles?: AgentProfileSummary[];
  fullProfile?: AgentProfile;
  saveFn?: ReturnType<typeof vi.fn>;
  deleteFn?: ReturnType<typeof vi.fn>;
}) {
  const saveFn = overrides.saveFn ?? vi.fn(async () => undefined);
  const deleteFn = overrides.deleteFn ?? vi.fn(async () => undefined);
  return createFakeHarnessClient({
    agents: {
      listProfiles: async () => overrides.profiles ?? [],
      loadProfile: async () => overrides.fullProfile ?? FULL_EXPLORE,
      saveProfile: saveFn,
      deleteProfile: deleteFn,
    },
  });
}

function mountView(client = buildClient({})) {
  return mount(AgentsView, {
    global: {
      provide: { [HarnessClientKey as symbol]: client },
      stubs: { AgentProfileEditor: true },
    },
  });
}

describe('AgentsView', () => {
  it('renders loading state initially', async () => {
    // Create a never-resolving promise to freeze the loading state.
    let resolveProfiles!: (v: AgentProfileSummary[]) => void;
    const pending = new Promise<AgentProfileSummary[]>((res) => { resolveProfiles = res; });
    const client = buildClient({ profiles: [] });
    client.agents.listProfiles = () => pending;

    const wrapper = mountView(client);
    // Wait one tick for Vue to flush the reactive `loading = true` update.
    await nextTick();
    // Before the promise resolves, the loading indicator must be shown.
    expect(wrapper.find('[data-testid="agents-loading"]').exists()).toBe(true);
    // Release the promise so the component can finish without leaking.
    resolveProfiles([]);
    await flushPromises();
    expect(wrapper.find('[data-testid="agents-loading"]').exists()).toBe(false);
  });

  it('renders bundled and user profiles after load', async () => {
    const wrapper = mountView(
      buildClient({ profiles: [BUNDLED_EXPLORE, USER_PROFILE] }),
    );
    await flushPromises();

    expect(wrapper.find('[data-testid="bundled-profile-list"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="user-profile-list"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="profile-row-explore"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="profile-row-my-agent"]').exists()).toBe(true);
  });

  it('shows empty-state when no user profiles exist', async () => {
    const wrapper = mountView(buildClient({ profiles: [BUNDLED_EXPLORE] }));
    await flushPromises();

    expect(wrapper.find('[data-testid="user-profiles-empty"]').exists()).toBe(true);
  });

  it('calls listProfiles on mount', async () => {
    const listFn = vi.fn(async () => [BUNDLED_EXPLORE]);
    const client = buildClient({ profiles: [BUNDLED_EXPLORE] });
    client.agents.listProfiles = listFn;
    mountView(client);
    await flushPromises();
    expect(listFn).toHaveBeenCalledOnce();
  });

  it('shows inspect and duplicate buttons for bundled profiles', async () => {
    const wrapper = mountView(buildClient({ profiles: [BUNDLED_EXPLORE] }));
    await flushPromises();

    expect(wrapper.find('[data-testid="profile-inspect-explore"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="profile-duplicate-explore"]').exists()).toBe(true);
    // No delete button for bundled
    expect(wrapper.find('[data-testid="profile-delete-explore"]').exists()).toBe(false);
  });

  it('shows edit and delete buttons for user profiles', async () => {
    const wrapper = mountView(buildClient({ profiles: [USER_PROFILE] }));
    await flushPromises();

    expect(wrapper.find('[data-testid="profile-edit-my-agent"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="profile-delete-my-agent"]').exists()).toBe(true);
    // No inspect / duplicate for user profiles
    expect(wrapper.find('[data-testid="profile-inspect-my-agent"]').exists()).toBe(false);
  });

  it('opens new profile form on + New Profile click', async () => {
    const wrapper = mountView(buildClient({ profiles: [] }));
    await flushPromises();

    await wrapper.find('[data-testid="agents-new-btn"]').trigger('click');
    // AgentProfileEditor is stubbed; just verify it's rendered
    expect(wrapper.findComponent({ name: 'AgentProfileEditor' }).exists()).toBe(true);
  });

  it('displays error state when listProfiles rejects', async () => {
    const client = buildClient({});
    client.agents.listProfiles = async () => {
      throw new Error('network error');
    };
    const wrapper = mountView(client);
    await flushPromises();

    const err = wrapper.find('[data-testid="agents-error"]');
    expect(err.exists()).toBe(true);
    expect(err.text()).toContain('network error');
  });
});
