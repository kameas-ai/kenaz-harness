/**
 * ProjectLandingPage.served.test.ts — served-mode-is-a-real-mode-01PMZ707
 * WP04 (E-705). Projects_Get — the very first read this view makes — has
 * no serve dispatch case, and neither does almost everything below it
 * (Contexts_*, Attachments_*, Artifacts_*, Memory_*, ProjectSync_*).
 * E-705 asked WP04 to record whether the answer for `/projects/:id` is
 * "boundary-panel the whole view"; this pins that answer: the panel
 * renders, and no client method is ever called while served.
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { ref } from 'vue';
import ProjectLandingPage from '@/views/projects/ProjectLandingPage.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';

let servedModeFlag = true;
vi.mock('@/lib/useServedMode', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/useServedMode')>();
  return {
    ...actual,
    isServedMode: () => servedModeFlag,
    useServedMode: () => ref(servedModeFlag),
  };
});

function failingClient() {
  const fail = () => {
    throw new Error('must not be called in served mode — unrouted RPC');
  };
  return createFakeHarnessClient({
    projects: {
      list: fail,
      get: fail,
      create: fail,
      rename: fail,
      updateDescription: fail,
      remove: fail,
      addSession: fail,
      removeSession: fail,
      listSessions: fail,
    } as any,
    attachments: {
      list: fail,
      listResolved: fail,
      add: fail,
      remove: fail,
      reorder: fail,
      refresh: fail,
    } as any,
  });
}

function setupRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/projects/:id', name: 'project', component: ProjectLandingPage },
      { path: '/sessions/:id?', name: 'sessions', component: { template: '<div />' } },
    ],
  });
}

afterEach(() => {
  servedModeFlag = true;
});

describe('ProjectLandingPage (served mode)', () => {
  it('renders the boundary panel and never calls Projects_Get', async () => {
    servedModeFlag = true;
    const client = failingClient();
    const router = setupRouter();
    await router.push('/projects/p1');
    await router.isReady();

    const w = mount(ProjectLandingPage, {
      global: {
        plugins: [router],
        provide: { [HarnessClientKey as symbol]: client },
      },
    });
    await flushPromises();

    expect(
      w.find('[data-testid="not-available-in-served-mode"]').exists(),
    ).toBe(true);
    expect(w.find('[data-testid="project-landing"]').exists()).toBe(false);
  });
});

describe('ProjectLandingPage (desktop mode regression)', () => {
  it('keeps rendering the real page, not the panel', async () => {
    servedModeFlag = false;
    const client = createFakeHarnessClient({
      projects: {
        list: async () => [],
        get: async () => ({
          id: 'p1',
          name: 'Foo',
          description: '',
          createdAt: '',
          updatedAt: '',
        }),
        create: async () => ({ id: 'p', name: 'p', description: '', createdAt: '', updatedAt: '' }),
        rename: async () => undefined,
        updateDescription: async () => undefined,
        remove: async () => undefined,
        addSession: async () => undefined,
        removeSession: async () => undefined,
        listSessions: async () => [],
      } as any,
      attachments: {
        list: async () => [],
        listResolved: async () => [],
        add: async () => ({}) as any,
        remove: async () => undefined,
        reorder: async () => undefined,
        refresh: async () => ({}) as any,
      } as any,
    });
    const router = setupRouter();
    await router.push('/projects/p1');
    await router.isReady();

    const w = mount(ProjectLandingPage, {
      global: {
        plugins: [router],
        provide: { [HarnessClientKey as symbol]: client },
      },
    });
    await flushPromises();

    expect(
      w.find('[data-testid="not-available-in-served-mode"]').exists(),
    ).toBe(false);
    expect(w.find('[data-testid="project-landing"]').exists()).toBe(true);
  });
});
