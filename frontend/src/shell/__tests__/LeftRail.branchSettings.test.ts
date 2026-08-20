/**
 * LeftRail.branchSettings.test.ts —
 * controls-and-readouts-that-tell-the-truth-01PMZ808 UNIT-1 (WP01 + WP03).
 *
 * AC-001: maxVisibleBranchDepth reaches the sidebar's indentation clamp.
 * AC-003: autoCollapseBranchesInSidebar reaches the sidebar's first-paint
 * collapse state.
 */
import { describe, it, expect } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h, nextTick } from 'vue';
import LeftRail from '@/shell/LeftRail.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import type { HarnessClient } from '@/lib/harnessClient';
import type { Session, Settings } from '@/lib/types';

async function mountRail(seed: Partial<HarnessClient> = {}) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      {
        path: '/sessions/:id?',
        name: 'sessions',
        component: defineComponent({ render: () => h('div', 'sessions') }),
      },
      {
        path: '/projects/:id',
        name: 'project',
        component: defineComponent({ render: () => h('div', 'project') }),
      },
    ],
  });
  await router.push('/sessions');
  await router.isReady();
  const w = mount(LeftRail, {
    global: {
      plugins: [
        router,
        {
          install(app) {
            provideFakeClient(app, seed);
          },
        },
      ],
    },
  });
  await flushPromises();
  await nextTick();
  await flushPromises();
  return { w, router };
}

function branchTreeSessions(): Session[] {
  return [
    { id: 'root-1', name: 'Root', createdAt: '', updatedAt: '' },
    {
      id: 'child-deep',
      name: 'Deep child',
      createdAt: '',
      updatedAt: '',
      parentSessionId: 'root-1',
      branchDepth: 4,
    },
  ];
}

function sessionsClientSeed(sessions: Session[]) {
  return {
    list: async () => sessions,
    get: async (id: string) => sessions.find((s) => s.id === id) ?? sessions[0],
    create: async (name: string) => ({ id: 'x', name, createdAt: '', updatedAt: '' }),
    rename: async () => undefined,
    delete: async () => undefined,
    reorder: async () => undefined,
    startStream: async () => 'sub',
    stopStream: async () => undefined,
    listMessages: async () => [],
    appendMessage: async (id: string, role: string, content: string) => ({
      id: 'm1',
      sessionId: id,
      role,
      content,
      createdAt: '',
    }),
    saveDraft: async () => undefined,
    loadDraft: async () => '',
    setSystemPrompt: async () => undefined,
    moveToProject: async () => undefined,
  } as any;
}

function settingsClientSeed(overrides: Partial<Settings>): any {
  const settings: Settings = {
    schemaVersion: 1,
    lastRoute: '/sessions',
    theme: 'system',
    accent: 'default',
    windowSize: { width: 1280, height: 800 },
    ...overrides,
  };
  return {
    get: async () => settings,
    set: async () => undefined,
    loadRoute: async () => settings.lastRoute,
    saveRoute: async () => undefined,
    logRouteChange: async () => undefined,
    loadTheme: async () => settings.theme,
    saveTheme: async () => undefined,
    getMemory: async () => false,
    setMemory: async () => undefined,
    getWebFetchEnabled: async () => false,
    setWebFetchEnabled: async () => undefined,
  };
}

describe('AC-001 — maxVisibleBranchDepth reaches SessionTreeRow indentation', () => {
  it('clamps a depth-4 row to 24px when the setting is 2', async () => {
    localStorage.clear();
    const { w } = await mountRail({
      sessions: sessionsClientSeed(branchTreeSessions()),
      settings: settingsClientSeed({ maxVisibleBranchDepth: 2, autoCollapseBranchesInSidebar: false }),
      projects: { list: async () => [] } as any,
    });
    const row = w.find('[data-testid="session-row-child-deep"]');
    expect(row.exists()).toBe(true);
    expect(row.attributes('style')).toContain('padding-left: 24px');
  });

  it('clamps a depth-4 row to 48px when the setting is 8', async () => {
    localStorage.clear();
    const { w } = await mountRail({
      sessions: sessionsClientSeed(branchTreeSessions()),
      settings: settingsClientSeed({ maxVisibleBranchDepth: 8, autoCollapseBranchesInSidebar: false }),
      projects: { list: async () => [] } as any,
    });
    const row = w.find('[data-testid="session-row-child-deep"]');
    expect(row.exists()).toBe(true);
    expect(row.attributes('style')).toContain('padding-left: 48px');
  });

  it('MUTATION GUARD: fails if maxBranchDepth is hardcoded back to ref(5) — setting 2 must not render 48px', async () => {
    localStorage.clear();
    const { w } = await mountRail({
      sessions: sessionsClientSeed(branchTreeSessions()),
      settings: settingsClientSeed({ maxVisibleBranchDepth: 2, autoCollapseBranchesInSidebar: false }),
      projects: { list: async () => [] } as any,
    });
    const row = w.find('[data-testid="session-row-child-deep"]');
    expect(row.attributes('style')).not.toContain('padding-left: 48px');
  });
});

describe('AC-003 — autoCollapseBranchesInSidebar reaches first-paint collapse state', () => {
  it('collapses a parent with children on first paint when ON and no stored choice exists', async () => {
    localStorage.clear();
    const { w } = await mountRail({
      sessions: sessionsClientSeed(branchTreeSessions()),
      settings: settingsClientSeed({ autoCollapseBranchesInSidebar: true }),
      projects: { list: async () => [] } as any,
    });
    // The child row must not be present — its parent starts collapsed.
    expect(w.find('[data-testid="session-row-child-deep"]').exists()).toBe(false);
    const toggle = w.find('[data-testid="branch-collapse-toggle-root-1"]');
    expect(toggle.exists()).toBe(true);
    expect(toggle.attributes('aria-expanded')).toBe('false');
  });

  it('leaves a parent with children expanded on first paint when OFF', async () => {
    localStorage.clear();
    const { w } = await mountRail({
      sessions: sessionsClientSeed(branchTreeSessions()),
      settings: settingsClientSeed({ autoCollapseBranchesInSidebar: false }),
      projects: { list: async () => [] } as any,
    });
    expect(w.find('[data-testid="session-row-child-deep"]').exists()).toBe(true);
  });
});
