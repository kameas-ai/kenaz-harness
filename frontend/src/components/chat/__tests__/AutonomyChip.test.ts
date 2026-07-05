import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';

import AutonomyChip from '@/components/chat/AutonomyChip.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { ResolvedAutonomy } from '@/lib/types';

function makeResolved(overrides: Partial<ResolvedAutonomy> = {}): ResolvedAutonomy {
  return {
    resolved: {
      maxIterations: 25,
      askOnAmbiguity: 'major',
      autoApproveFamilies: ['read', 'write'],
      tokenCeilingPerTurn: 0,
      recapStyle: 'brief',
      continueOnError: 'retry-once',
      destructiveActionPosture: 'confirm',
      sourceTrace: {
        maxIterations: 'session',
        askOnAmbiguity: 'project',
        autoApproveFamilies: 'global',
        tokenCeilingPerTurn: 'tier-default',
        recapStyle: 'tier-default',
        continueOnError: 'tier-default',
        destructiveActionPosture: 'tier-default',
      },
      tier: 'bold',
    },
    global: { level: null, overrides: {} },
    project: { level: null, overrides: {} },
    session: { level: 'bold', overrides: { maxIterations: 99 } },
    ...overrides,
  };
}

function mountChip(resolved: ResolvedAutonomy) {
  const setAutonomy = vi.fn(async () => undefined);
  const resolveAutonomy = vi.fn(async () => resolved);
  const client = createFakeHarnessClient({
    sessions: {
      list: async () => [],
      get: async () => ({
        id: 's1',
        name: 's1',
        createdAt: '',
        updatedAt: '',
        systemPrompt: '',
        contextKind: 'system',
      }),
      create: async () => ({
        id: 's1',
        name: 's1',
        createdAt: '',
        updatedAt: '',
        systemPrompt: '',
        contextKind: 'system',
      }),
      rename: async () => undefined,
      delete: async () => undefined,
      reorder: async () => undefined,
      startStream: async () => 'sub',
      stopStream: async () => undefined,
      listMessages: async () => [],
      listMessagesActive: async () => ({ messages: [], sweptCount: 0 }),
      listMessagesAll: async () => ({ messages: [], sweptCount: 0 }),
      appendMessage: async () => ({
        id: 'm',
        sessionId: 's1',
        role: 'user',
        content: '',
        createdAt: '',
      }),
      sendMessageWithBlocks: async () => ({
        id: 'm',
        sessionId: 's1',
        role: 'user',
        content: '',
        createdAt: '',
      }),
      saveDraft: async () => undefined,
      loadDraft: async () => '',
      setSystemPrompt: async () => undefined,
      moveToProject: async () => undefined,
      resumeMessage: async () => ({ subscriptionId: 's', originalMessageId: 'm' }),
      getUsage: async () => ({
        promptTokens: 0,
        completionTokens: 0,
        totalTokens: 0,
        costUsd: 0,
        costSource: 'unknown' as const,
        messageCount: 0,
        pricingDataDate: '',
      }),
      saveAsArtifact: async () => ({
        id: 'a',
        sessionId: 's1',
        title: '',
        mimeType: '',
        contentHash: '',
        byteSize: 0,
        source: 'user_pin' as const,
        sourceRef: { messageId: 'm' },
        scopeKind: 'session' as const,
        createdAt: '',
      }),
      suggestTitle: async () => '',
      clearTitle: async () => undefined,
      getAutonomy: async () => resolved.session,
      setAutonomy,
      resolveAutonomy,
      export: async () => ({ path: '', byteCount: 0 }),
    },
  });
  const wrapper = mount(AutonomyChip, {
    props: { sessionId: 's1', resolvedOverride: resolved, skipFetch: true },
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
  return { wrapper, client, setAutonomy, resolveAutonomy };
}

describe('AutonomyChip', () => {
  it('renders the effective tier label', async () => {
    const { wrapper } = mountChip(makeResolved());
    await flushPromises();
    const btn = wrapper.find('[data-testid="autonomy-chip-button"]');
    expect(btn.exists()).toBe(true);
    expect(btn.text()).toContain('Bold');
  });

  it('exposes a custom badge when overrides are present', async () => {
    const { wrapper } = mountChip(makeResolved());
    await flushPromises();
    expect(
      wrapper.find('[data-testid="autonomy-chip-custom-badge"]').exists(),
    ).toBe(true);
  });

  it('opens and closes the popover with the session panel', async () => {
    const { wrapper } = mountChip(makeResolved());
    await flushPromises();
    expect(
      wrapper.find('[data-testid="autonomy-chip-popover"]').exists(),
    ).toBe(false);
    await wrapper.find('[data-testid="autonomy-chip-button"]').trigger('click');
    expect(
      wrapper.find('[data-testid="autonomy-chip-popover"]').exists(),
    ).toBe(true);
    expect(
      wrapper.find('[data-testid="session-autonomy-panel"]').exists(),
    ).toBe(true);
    await wrapper.find('[data-testid="autonomy-chip-close"]').trigger('click');
    expect(
      wrapper.find('[data-testid="autonomy-chip-popover"]').exists(),
    ).toBe(false);
  });

  it('uses no raw color literal (privacy CI invariant #4)', async () => {
    const { wrapper } = mountChip(makeResolved());
    await flushPromises();
    expect(wrapper.html()).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
  });
});
