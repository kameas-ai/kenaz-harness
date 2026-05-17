/**
 * FleetTelemetryPanel.spec.ts
 *
 * Unit tests for the fleet telemetry settings panel.
 * (fleet-otel-archival-01NDFSEX11 WP06)
 */
import { describe, it, expect, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import FleetTelemetryPanel from '@/views/settings/FleetTelemetryPanel.vue';
import { createFakeHarnessClient, type FleetClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';

function makeClient(fleetOverride: Partial<FleetClient> = {}) {
  return createFakeHarnessClient({
    fleet: {
      getTelemetryConsent: async () => 'none',
      setTelemetryConsent: async () => undefined,
      ...fleetOverride,
    },
  });
}

async function mountPanel(client = makeClient()) {
  const wrapper = mount(FleetTelemetryPanel, {
    global: {
      provide: {
        [HarnessClientKey as symbol]: client,
      },
    },
  });
  // Wait for the onMounted async to settle.
  await new Promise((r) => setTimeout(r, 0));
  return wrapper;
}

describe('FleetTelemetryPanel', () => {
  it('renders the panel with testid', async () => {
    const w = await mountPanel();
    expect(w.find('[data-testid="fleet-telemetry-panel"]').exists()).toBe(true);
  });

  it('loads consent level on mount', async () => {
    const client = makeClient({
      getTelemetryConsent: async () => 'aggregate',
    });
    const w = await mountPanel(client);
    const agg = w.find('input[value="aggregate"]').element as HTMLInputElement;
    expect(agg.checked).toBe(true);
  });

  it('shows "none" as default when getTelemetryConsent returns "none"', async () => {
    const w = await mountPanel();
    const noneInput = w.find('input[value="none"]').element as HTMLInputElement;
    expect(noneInput.checked).toBe(true);
  });

  it('calls setTelemetryConsent when a radio is selected', async () => {
    const setFn = vi.fn().mockResolvedValue(undefined);
    const client = makeClient({ setTelemetryConsent: setFn });
    const w = await mountPanel(client);

    // Simulate selecting "aggregate".
    const radio = w.find('input[value="aggregate"]');
    await radio.trigger('change');
    expect(setFn).toHaveBeenCalledWith('aggregate');
  });

  it('shows error message when setTelemetryConsent rejects', async () => {
    const client = makeClient({
      setTelemetryConsent: async () => { throw new Error('tier too low'); },
    });
    const w = await mountPanel(client);

    const radio = w.find('input[value="full"]');
    await radio.trigger('change');
    // Wait for async error handler.
    await new Promise((r) => setTimeout(r, 0));

    const err = w.find('[data-testid="fleet-error"]');
    expect(err.exists()).toBe(true);
    expect(err.text()).toContain('tier too low');
  });

  it('renders the live preview section', async () => {
    const w = await mountPanel();
    expect(w.find('[data-testid="telemetry-preview"]').exists()).toBe(true);
  });
});
