/**
 * CrashReportingOnboardingModal.spec.ts
 *
 * Unit tests for the first-launch crash-reporting consent modal.
 * (sentry-error-monitoring-01KX5R8G WP05)
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import CrashReportingOnboardingModal from '@/components/onboarding/CrashReportingOnboardingModal.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';

// ── helpers ────────────────────────────────────────────────────────────────

function setup() {
  const client = createFakeHarnessClient();

  vi.spyOn(client.settings, 'get').mockResolvedValue({
    schemaVersion: 1,
    lastRoute: '/sessions',
    theme: 'system',
    accent: 'default',
    windowSize: { width: 1280, height: 800 },
    memoryEnabled: false,
    confirmEachDisabled: false,
    crashReportingTier: 'off',
    hasSeenCrashReportingOnboarding: false,
  } as Parameters<typeof client.settings.set>[0]);
  const setFn = vi.spyOn(client.settings, 'set').mockResolvedValue(undefined);

  const wrapper = mount(CrashReportingOnboardingModal, {
    global: {
      provide: { [HarnessClientKey as symbol]: client },
    },
  });

  return { wrapper, setFn };
}

// ── tests ─────────────────────────────────────────────────────────────────

describe('CrashReportingOnboardingModal', () => {
  it('renders the modal with the correct role', () => {
    const { wrapper } = setup();
    expect(wrapper.find('[data-testid="crash-reporting-onboarding-modal"]').exists()).toBe(true);
    expect(wrapper.find('[role="dialog"]').exists()).toBe(true);
  });

  it('has "off" selected by default', () => {
    const { wrapper } = setup();
    const offRadio = wrapper.find('input[value="off"]');
    expect((offRadio.element as HTMLInputElement).checked).toBe(true);
  });

  it('confirm saves selected tier and marks onboarding seen', async () => {
    const { wrapper, setFn } = setup();
    await flushPromises();

    // Select anonymous then confirm
    await wrapper.find('input[value="anonymous"]').setValue(true);

    const confirmBtn = wrapper.findAll('button').find((b) => b.text().includes('Confirm'));
    expect(confirmBtn).toBeDefined();
    await confirmBtn!.trigger('click');
    await flushPromises();

    expect(setFn).toHaveBeenCalledOnce();
    const saved = setFn.mock.calls[0][0];
    expect(saved.hasSeenCrashReportingOnboarding).toBe(true);
    expect(saved.crashReportingTier).toBe('anonymous');
  });

  it('skip sets tier=off and marks onboarding seen', async () => {
    const { wrapper, setFn } = setup();
    await flushPromises();

    const skipBtn = wrapper.findAll('button').find((b) => b.text().includes('Skip'));
    expect(skipBtn).toBeDefined();
    await skipBtn!.trigger('click');
    await flushPromises();

    expect(setFn).toHaveBeenCalledOnce();
    const saved = setFn.mock.calls[0][0];
    expect(saved.crashReportingTier).toBe('off');
    expect(saved.hasSeenCrashReportingOnboarding).toBe(true);
  });

  it('emits close after confirm', async () => {
    const { wrapper } = setup();
    await flushPromises();

    const confirmBtn = wrapper.findAll('button').find((b) => b.text().includes('Confirm'));
    await confirmBtn!.trigger('click');
    await flushPromises();

    expect(wrapper.emitted('close')).toBeTruthy();
    expect(wrapper.emitted('close')!.length).toBe(1);
  });

  it('emits close after skip', async () => {
    const { wrapper } = setup();
    await flushPromises();

    const skipBtn = wrapper.findAll('button').find((b) => b.text().includes('Skip'));
    await skipBtn!.trigger('click');
    await flushPromises();

    expect(wrapper.emitted('close')).toBeTruthy();
    expect(wrapper.emitted('close')!.length).toBe(1);
  });
});
