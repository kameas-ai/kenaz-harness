/**
 * planmode.spec.ts — trust-surfaces-that-fire-01PMZ202 WP21 / UNIT-19,
 * AC-15a: a `plan_mode_changed` payload with a pending plan makes
 * pendingPlanId non-null.
 *
 * Before this WP, usePlanMode's subscription guard
 * (`typeof (client as any).subscribeEvent === 'function'`) was always
 * false — HarnessClient never declared a `subscribeEvent` method — so
 * handlePlanModeChanged was never invoked at all, for any event.
 * Mutation: revert the useEventStream subscription back to the dead
 * `client.subscribeEvent` probe. Must fail every test below (nothing
 * ever reaches pendingPlanId).
 */
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { defineComponent, h } from 'vue';
import { mount } from '@vue/test-utils';
import { usePlanMode, type PlanModeChangedPayload } from '@/lib/planmode';

interface FakeRuntime {
  EventsOn: (topic: string, cb: (payload: unknown) => void) => () => void;
  emit: (topic: string, payload: unknown) => void;
}

function installFakeRuntime(): FakeRuntime {
  const handlers = new Map<string, Set<(payload: unknown) => void>>();
  const rt: FakeRuntime = {
    EventsOn: (topic, cb) => {
      let s = handlers.get(topic);
      if (!s) {
        s = new Set();
        handlers.set(topic, s);
      }
      s.add(cb);
      return () => s!.delete(cb);
    },
    emit: (topic, payload) => {
      const s = handlers.get(topic);
      if (!s) return;
      for (const cb of s) cb(payload);
    },
  };
  (window as unknown as { runtime: FakeRuntime }).runtime = rt;
  return rt;
}

function uninstallRuntime() {
  delete (window as unknown as { runtime?: unknown }).runtime;
}

function mountPlanMode(sessionId: string) {
  let handle!: ReturnType<typeof usePlanMode>;
  const Comp = defineComponent({
    setup() {
      handle = usePlanMode(sessionId);
      return () =>
        h('div', `${handle.isActive.value}|${handle.pendingPlanId.value ?? ''}`);
    },
  });
  const wrapper = mount(Comp);
  return { wrapper, handle: () => handle };
}

describe('usePlanMode', () => {
  let rt: FakeRuntime;

  beforeEach(() => {
    rt = installFakeRuntime();
  });

  afterEach(() => {
    uninstallRuntime();
  });

  it('AC-15a: a plan_mode_changed payload with a pending plan makes pendingPlanId non-null', async () => {
    const { wrapper, handle } = mountPlanMode('sess-1');

    const payload: PlanModeChangedPayload = {
      session_id: 'sess-1',
      outcome: '',
      plan_id: 'plan-xyz',
      posture: 'plan_mode',
    };
    rt.emit('plan_mode_changed', payload);
    await wrapper.vm.$nextTick();

    expect(handle().isActive.value).toBe(true);
    expect(handle().pendingPlanId.value).toBe('plan-xyz');
    wrapper.unmount();
  });

  it('ignores payloads for a different session', async () => {
    const { wrapper, handle } = mountPlanMode('sess-1');

    rt.emit('plan_mode_changed', {
      session_id: 'sess-OTHER',
      outcome: '',
      plan_id: 'plan-xyz',
      posture: 'plan_mode',
    } satisfies PlanModeChangedPayload);
    await wrapper.vm.$nextTick();

    expect(handle().pendingPlanId.value).toBeNull();
    wrapper.unmount();
  });

  it('clears pendingPlanId on a terminal outcome (approved)', async () => {
    const { wrapper, handle } = mountPlanMode('sess-1');

    rt.emit('plan_mode_changed', {
      session_id: 'sess-1',
      outcome: '',
      plan_id: 'plan-xyz',
      posture: 'plan_mode',
    } satisfies PlanModeChangedPayload);
    await wrapper.vm.$nextTick();
    expect(handle().pendingPlanId.value).toBe('plan-xyz');

    rt.emit('plan_mode_changed', {
      session_id: 'sess-1',
      outcome: 'approved',
      plan_id: 'plan-xyz',
      posture: '',
    } satisfies PlanModeChangedPayload);
    await wrapper.vm.$nextTick();

    expect(handle().isActive.value).toBe(false);
    expect(handle().pendingPlanId.value).toBeNull();
    wrapper.unmount();
  });

  it('clears pendingPlanId on a terminal outcome (discarded)', async () => {
    const { wrapper, handle } = mountPlanMode('sess-1');
    rt.emit('plan_mode_changed', {
      session_id: 'sess-1',
      outcome: '',
      plan_id: 'plan-xyz',
      posture: 'plan_mode',
    } satisfies PlanModeChangedPayload);
    await wrapper.vm.$nextTick();

    rt.emit('plan_mode_changed', {
      session_id: 'sess-1',
      outcome: 'discarded',
      plan_id: 'plan-xyz',
      posture: '',
    } satisfies PlanModeChangedPayload);
    await wrapper.vm.$nextTick();

    expect(handle().isActive.value).toBe(false);
    expect(handle().pendingPlanId.value).toBeNull();
    wrapper.unmount();
  });

  it('setActive(false) clears pendingPlanId', () => {
    const { wrapper, handle } = mountPlanMode('sess-1');
    handle().setPendingPlan('p1');
    expect(handle().pendingPlanId.value).toBe('p1');
    handle().setActive(false);
    expect(handle().pendingPlanId.value).toBeNull();
    wrapper.unmount();
  });
});
