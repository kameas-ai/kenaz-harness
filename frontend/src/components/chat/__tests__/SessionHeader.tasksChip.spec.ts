/**
 * SessionHeader.tasksChip — nav-ia-sweep-01DOGF0F WP05, pin P-3 (producer half).
 *
 * The background-task chip used to push /settings?tab=tasks. Tasks moved to
 * Workflows › Tasks, so the chip now targets /workflows?tab=tasks directly
 * (the legacy URL still redirects — __tests__/legacySettingsRedirects.test.ts —
 * but a first-party producer should not depend on a compatibility shim).
 */
import { describe, it, expect } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h } from 'vue';
import SessionHeader from '@/components/chat/SessionHeader.vue';
import BackgroundTaskChip from '@/components/chat/BackgroundTaskChip.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';

describe('SessionHeader background-task chip (P-3)', () => {
  it('lands on Workflows › Tasks', async () => {
    const Stub = defineComponent({ render: () => h('div') });
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/sessions/:id?', component: Stub },
        { path: '/workflows', component: Stub },
        { path: '/settings', component: Stub },
      ],
    });
    await router.push('/sessions/ses-1');
    await router.isReady();
    const w = mount(SessionHeader, {
      props: { session: { id: 'ses-1', name: 'S', createdAt: '', updatedAt: '' } },
      global: {
        plugins: [router],
        provide: { [HarnessClientKey as symbol]: createFakeHarnessClient() },
      },
    });
    await flushPromises();

    w.findComponent(BackgroundTaskChip).vm.$emit('open-tasks');
    await flushPromises();

    expect(router.currentRoute.value.path).toBe('/workflows');
    expect(router.currentRoute.value.query.tab).toBe('tasks');
  });
});
