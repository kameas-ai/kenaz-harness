/**
 * OnboardingDialog.spec.ts — BLOCKER-1 coverage
 * (context-bootstrap-harness-integration review fix).
 *
 * Asserts:
 *   1. After onAction('next') the dialog enters the 'bootstrap' phase and
 *      renders <BootstrapStep data-testid="bootstrap-step">.
 *   2. "Skip for now" advances to starter-pick without calling runBootstrap.
 *   3. BootstrapStep's @done event advances to starter-pick.
 *   4. After FSM reaches state=done the dialog also routes through bootstrap.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import OnboardingDialog from '../OnboardingDialog.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';

// Stub BaseDialog to avoid focus-trap / Teleport complexity in jsdom.
vi.mock('@/components/ui/BaseDialog.vue', () => ({
  default: {
    props: ['open', 'title', 'panelClass'],
    template: `<div v-if="open" data-testid="base-dialog"><slot /></div>`,
  },
}));

// Stub BootstrapStep so we control when it emits 'done'.
// The stub exposes a data-testid so we can assert it renders.
vi.mock('@/components/context/BootstrapStep.vue', () => ({
  default: {
    props: ['sources'],
    emits: ['done'],
    template: `<div data-testid="bootstrap-step">
      <button data-testid="bootstrap-step-emit-done" @click="$emit('done', 'run-xyz')">Emit done</button>
    </div>`,
  },
}));

function setup(beginState = 'welcome') {
  const client = createFakeHarnessClient({
    onboarding: {
      begin: async () => ({
        state: beginState,
        card: {
          title: 'Welcome',
          body: 'Set up.',
          actions: [
            { id: 'next', label: 'Get started', primary: true },
            { id: 'dismiss', label: 'Skip' },
          ],
        },
      }),
      step: async () => ({ state: 'done', card: { title: 'Done' } }),
      listStarters: async () => [],
      runBootstrap: vi.fn().mockResolvedValue(''),
    } as any,
  });
  const wrapper = mount(OnboardingDialog, {
    props: { open: true },
    global: {
      provide: { [HarnessClientKey as symbol]: client },
    },
  });
  return { wrapper, client };
}

describe('OnboardingDialog', () => {
  it('clicking "next" transitions to the bootstrap phase and renders BootstrapStep', async () => {
    const { wrapper } = setup();
    await flushPromises();

    // Initially in phase1 — bootstrap step should NOT be present.
    expect(wrapper.find('[data-testid="bootstrap-step"]').exists()).toBe(false);

    // Click the "Get started" / next button.
    const nextBtn = wrapper.findAll('button').find((b) => b.text().includes('Get started'));
    expect(nextBtn).toBeDefined();
    await nextBtn!.trigger('click');
    await flushPromises();

    // Now in bootstrap phase — BootstrapStep should render.
    expect(wrapper.find('[data-testid="bootstrap-step"]').exists()).toBe(true);

    // The "Skip for now" button must also be present (dialog never button-less).
    const skipBtn = wrapper.findAll('button').find((b) => b.text().includes('Skip for now'));
    expect(skipBtn).toBeDefined();
  });

  it('"Skip for now" advances from bootstrap to starter-pick without calling runBootstrap', async () => {
    const { wrapper, client } = setup();
    await flushPromises();

    // Advance to bootstrap.
    const nextBtn = wrapper.findAll('button').find((b) => b.text().includes('Get started'));
    await nextBtn!.trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-testid="bootstrap-step"]').exists()).toBe(true);

    // Click Skip.
    const skipBtn = wrapper.findAll('button').find((b) => b.text().includes('Skip for now'));
    await skipBtn!.trigger('click');
    await flushPromises();

    // Should be in starter-pick — the starter list section or text appears.
    expect(wrapper.text()).toContain('starter');
    // runBootstrap must NOT have been called.
    expect(client.onboarding.runBootstrap).not.toHaveBeenCalled();
  });

  it("BootstrapStep's @done event advances to starter-pick", async () => {
    const { wrapper } = setup();
    await flushPromises();

    // Advance to bootstrap.
    const nextBtn = wrapper.findAll('button').find((b) => b.text().includes('Get started'));
    await nextBtn!.trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-testid="bootstrap-step"]').exists()).toBe(true);

    // Trigger the stubbed 'done' event.
    const emitDoneBtn = wrapper.find('[data-testid="bootstrap-step-emit-done"]');
    await emitDoneBtn.trigger('click');
    await flushPromises();

    // Should have advanced past bootstrap.
    expect(wrapper.find('[data-testid="bootstrap-step"]').exists()).toBe(false);
  });

  it('FSM state=done also routes through bootstrap before starter-pick', async () => {

    // Fire the 'configure_key' event (not 'next'/'dismiss') to hit the FSM path.
    // We need a card with an action id that is not 'next'/'dismiss'.
    // The fake step() returns state=done — simulate by calling onAction via
    // a card action that goes through the FSM branch.
    // We'll find any FSM action button (id != 'next'/'dismiss'). Since our fake
    // begin() only returns next/dismiss, we directly trigger a DOM click on a
    // button with text matching a known FSM action. As a simpler approach:
    // remount with a custom step that returns done.
    const client2 = createFakeHarnessClient({
      onboarding: {
        begin: async () => ({
          state: 'configure',
          card: {
            title: 'Configure',
            actions: [{ id: 'save_key', label: 'Save', primary: true }],
          },
        }),
        step: async () => ({ state: 'done', card: { title: 'Done' } }),
        listStarters: async () => [],
        runBootstrap: vi.fn().mockResolvedValue(''),
      } as any,
    });
    const wrapper2 = mount(OnboardingDialog, {
      props: { open: true },
      global: { provide: { [HarnessClientKey as symbol]: client2 } },
    });
    await flushPromises();

    // Click the FSM 'save_key' action.
    const saveBtn = wrapper2.findAll('button').find((b) => b.text().includes('Save'));
    expect(saveBtn).toBeDefined();
    await saveBtn!.trigger('click');
    await flushPromises();

    // Should now be in bootstrap phase (not starter-pick directly).
    expect(wrapper2.find('[data-testid="bootstrap-step"]').exists()).toBe(true);
  });
});
