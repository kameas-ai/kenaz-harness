// SessionTunePanel.test.ts — model-settings-reach-the-model-01PMZ101
// UNIT-6 / WP11, AC-010(b).
//
// Before this WP, `saved.value = true` ran unconditionally after
// `emit('change', …)` with no backend call at all — the badge asserted
// nothing about persistence. These tests drive the REAL onSave/onReset
// handlers against a fake harnessClient and assert the badge (and the
// error text) track the persist call's outcome, not the local form
// state. "Fails if the client stub always resolves" (the AC's own
// failure-mode clause) is why the reject-path test below exists.

import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';

import SessionTunePanel from '@/views/sessions/SessionTunePanel.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';

function mountPanel(
  setKnobsDefault: (
    id: string,
    knobs: unknown,
  ) => Promise<void>,
) {
  const client = createFakeHarnessClient({
    sessions: {
      ...createFakeHarnessClient().sessions,
      setKnobsDefault: setKnobsDefault as never,
    },
  });
  const wrapper = mount(SessionTunePanel, {
    props: {
      sessionId: 'sess-1',
      reasoningStyle: 'effort_string',
    },
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
  return { wrapper };
}

async function selectEffortAndSave(wrapper: ReturnType<typeof mount>) {
  await wrapper
    .find('[data-testid="effort-level-select"]')
    .setValue('high');
  await wrapper.find('[data-testid="tune-panel-save"]').trigger('click');
  await flushPromises();
}

describe('SessionTunePanel — persistence-gated save badge', () => {
  it('renders the saved badge only after a successful persist call', async () => {
    const setKnobsDefault = vi.fn(async () => undefined);
    const { wrapper } = mountPanel(setKnobsDefault);

    await selectEffortAndSave(wrapper);

    expect(setKnobsDefault).toHaveBeenCalledWith('sess-1', {
      openAIEffort: 'high',
    });
    expect(
      wrapper.find('[data-testid="tune-panel-saved-badge"]').exists(),
    ).toBe(true);
    expect(wrapper.emitted('change')).toEqual([[{ openAIEffort: 'high' }]]);
  });

  it('does NOT render the saved badge when the persist call rejects', async () => {
    const setKnobsDefault = vi.fn(async () => {
      throw new Error('store unavailable');
    });
    const { wrapper } = mountPanel(setKnobsDefault);

    await selectEffortAndSave(wrapper);

    expect(setKnobsDefault).toHaveBeenCalled();
    expect(
      wrapper.find('[data-testid="tune-panel-saved-badge"]').exists(),
    ).toBe(false);
    expect(wrapper.find('[data-testid="tune-panel-error"]').text()).toContain(
      'store unavailable',
    );
    // The rejected save must not have told the parent anything changed.
    expect(wrapper.emitted('change')).toBeUndefined();
  });

  it('reset persists a null override and only clears local state on success', async () => {
    const setKnobsDefault = vi.fn(async () => undefined);
    const { wrapper } = mountPanel(setKnobsDefault);

    await wrapper.find('[data-testid="tune-panel-reset"]').trigger('click');
    await flushPromises();

    expect(setKnobsDefault).toHaveBeenCalledWith('sess-1', null);
    expect(wrapper.emitted('change')).toEqual([[null]]);
  });

  it('reset does NOT emit change when the persist call rejects', async () => {
    const setKnobsDefault = vi.fn(async () => {
      throw new Error('store unavailable');
    });
    const { wrapper } = mountPanel(setKnobsDefault);

    await wrapper.find('[data-testid="tune-panel-reset"]').trigger('click');
    await flushPromises();

    expect(wrapper.emitted('change')).toBeUndefined();
    expect(wrapper.find('[data-testid="tune-panel-error"]').text()).toContain(
      'store unavailable',
    );
  });
});
