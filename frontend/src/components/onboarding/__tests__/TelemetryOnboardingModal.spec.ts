/**
 * TelemetryOnboardingModal.spec.ts
 *
 * Unit tests for the first-launch fleet-telemetry consent modal.
 * (fleet-otel-archival-01NDFSEX11 WP06)
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import TelemetryOnboardingModal from '@/components/onboarding/TelemetryOnboardingModal.vue';
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
    hasSeenFleetTelemetryOnboarding: false,
  } as Parameters<typeof client.settings.set>[0]);
  const setFn = vi.spyOn(client.settings, 'set').mockResolvedValue(undefined);
  const setConsentFn = vi.spyOn(client.fleet, 'setTelemetryConsent').mockResolvedValue(undefined);

  const wrapper = mount(TelemetryOnboardingModal, {
    global: {
      provide: { [HarnessClientKey as symbol]: client },
    },
  });

  return { wrapper, setFn, setConsentFn };
}

// ── tests ─────────────────────────────────────────────────────────────────

describe('TelemetryOnboardingModal', () => {
  it('renders the modal with the correct role and test-id', () => {
    const { wrapper } = setup();
    expect(wrapper.find('[data-testid="telemetry-onboarding-modal"]').exists()).toBe(true);
    expect(wrapper.find('[role="dialog"]').exists()).toBe(true);
  });

  it('has "none" selected by default', () => {
    const { wrapper } = setup();
    const noneRadio = wrapper.find('input[value="none"]');
    expect((noneRadio.element as HTMLInputElement).checked).toBe(true);
  });

  it('confirm saves selected consent level and marks onboarding seen', async () => {
    const { wrapper, setFn, setConsentFn } = setup();
    await flushPromises();

    // Select aggregate then confirm
    await wrapper.find('input[value="aggregate"]').setValue(true);

    const confirmBtn = wrapper.findAll('button').find((b) => b.text().includes('Confirm'));
    expect(confirmBtn).toBeDefined();
    await confirmBtn!.trigger('click');
    await flushPromises();

    expect(setConsentFn).toHaveBeenCalledOnce();
    expect(setConsentFn).toHaveBeenCalledWith('aggregate');

    expect(setFn).toHaveBeenCalledOnce();
    const saved = setFn.mock.calls[0][0];
    expect(saved.hasSeenFleetTelemetryOnboarding).toBe(true);
  });

  it('skip sets consent=none and marks onboarding seen', async () => {
    const { wrapper, setFn, setConsentFn } = setup();
    await flushPromises();

    const skipBtn = wrapper.findAll('button').find((b) => b.text().includes('Skip'));
    expect(skipBtn).toBeDefined();
    await skipBtn!.trigger('click');
    await flushPromises();

    expect(setConsentFn).toHaveBeenCalledOnce();
    expect(setConsentFn).toHaveBeenCalledWith('none');

    expect(setFn).toHaveBeenCalledOnce();
    const saved = setFn.mock.calls[0][0];
    expect(saved.hasSeenFleetTelemetryOnboarding).toBe(true);
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
