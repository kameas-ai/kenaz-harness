/**
 * useEventToasts.test.ts — covers the MigrationDriftDetected toast
 * (upgrade-path-coverage-01PMUG01 WP04, FR-3c).
 *
 * severity:"error" migration-ledger drift used to be visible ONLY at
 * /settings?tab=health — a query-param-gated tab a user has no reason to
 * know exists. This composable now subscribes to the
 * `storage.migration.drift-detected` broker topic (forwarded in served
 * mode via SERVED_STREAM_TOPICS, see core/serve/wsstream_topics_parity_test.go)
 * and surfaces a persistent toast when the payload's hasError is true.
 *
 * Tests run in served mode (no window.runtime) via dispatchServedEvent,
 * the same test-injection hook frontend/src/lib/__tests__/useEventStream.test.ts
 * uses — there is no window.runtime in vitest's jsdom environment by
 * default, so useEventStream already falls back to the served-event bus.
 *
 * Only the migration-drift toast is covered here; the composable's other
 * toasts (cost threshold, retry-after-rotate, merge suggestion, update,
 * one-time migration-toast) are exercised indirectly by their own
 * consuming views today and are out of this WP's scope.
 */
import { describe, it, expect, beforeEach } from 'vitest';
import { defineComponent, h } from 'vue';
import { mount } from '@vue/test-utils';
import { createRouter, createMemoryHistory } from 'vue-router';
import { useEventToasts, _resetMigrationDriftToastState } from '@/composables/useEventToasts';
import { useToastQueue, _resetToastQueue } from '@/composables/useToastQueue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import { dispatchServedEvent } from '@/lib/useServedEvents';
import type { MigrationDriftDetectedPayload } from '@/lib/types';

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: { template: '<div />' } },
      { path: '/settings', component: { template: '<div />' } },
    ],
  });
}

function mountHost() {
  const client = createFakeHarnessClient();
  const router = makeRouter();
  const Host = defineComponent({
    setup() {
      useEventToasts();
      return () => h('div');
    },
  });
  return mount(Host, {
    global: {
      plugins: [router],
      provide: { [HarnessClientKey as symbol]: client },
    },
  });
}

function driftPayload(overrides: Partial<MigrationDriftDetectedPayload> = {}): MigrationDriftDetectedPayload {
  return { driftCount: 1, versions: [322], hasError: true, ...overrides };
}

describe('useEventToasts — migration drift', () => {
  beforeEach(() => {
    _resetToastQueue();
    _resetMigrationDriftToastState();
  });

  it('does NOT toast for code_only/ledger_only-only drift (hasError: false)', () => {
    const w = mountHost();
    dispatchServedEvent('storage.migration.drift-detected', driftPayload({ hasError: false }));

    const { toasts } = useToastQueue();
    expect(toasts.length).toBe(0);
    w.unmount();
  });

  it('surfaces a persistent error toast with a Review action when hasError is true', () => {
    const w = mountHost();
    dispatchServedEvent('storage.migration.drift-detected', driftPayload());

    const { toasts } = useToastQueue();
    expect(toasts.length).toBe(1);
    expect(toasts[0].level).toBe('error');
    expect(toasts[0].durationMs).toBe(0); // persistent — no auto-dismiss
    expect(toasts[0].message).toContain('drift');
    expect(toasts[0].actions?.map((a) => a.label)).toContain('Review');
    w.unmount();
  });

  it('the Review action navigates to /settings?tab=health', async () => {
    const client = createFakeHarnessClient();
    const router = makeRouter();
    const Host = defineComponent({
      setup() {
        useEventToasts();
        return () => h('div');
      },
    });
    const w = mount(Host, {
      global: {
        plugins: [router],
        provide: { [HarnessClientKey as symbol]: client },
      },
    });

    dispatchServedEvent('storage.migration.drift-detected', driftPayload());
    const { toasts } = useToastQueue();
    const action = toasts[0].actions?.find((a) => a.label === 'Review');
    expect(action).toBeTruthy();

    await action!.perform();
    expect(router.currentRoute.value.fullPath).toBe('/settings?tab=health');
    w.unmount();
  });
});
