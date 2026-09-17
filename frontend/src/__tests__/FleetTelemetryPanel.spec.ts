/**
 * FleetTelemetryPanel.spec.ts
 *
 * Unit tests for the fleet telemetry settings panel.
 * (fleet-otel-archival-01NDFSEX11 WP06)
 */
import { describe, it, expect, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import FleetTelemetryPanel from '@/views/settings/FleetTelemetryPanel.vue';
import {
  createFakeHarnessClient,
  type FleetClient,
  type FleetTelemetryStatus,
} from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';

function makeClient(fleetOverride: Partial<FleetClient> = {}) {
  return createFakeHarnessClient({
    fleet: {
      ...createFakeHarnessClient().fleet,
      getTelemetryConsent: async () => 'none',
      setTelemetryConsent: async () => undefined,
      ...fleetOverride,
    },
  });
}

async function baseStatus(): Promise<FleetTelemetryStatus> {
  return createFakeHarnessClient().fleet.getTelemetryStatus();
}

/** status() with a few fields overridden. */
async function statusWith(
  over: Partial<FleetTelemetryStatus>,
  pipeline: Partial<FleetTelemetryStatus['pipeline']> = {},
): Promise<FleetTelemetryStatus> {
  const b = await baseStatus();
  return {
    ...b,
    wired: true,
    opted_in_classes: ['harness.usage_counts'],
    ...over,
    pipeline: { ...b.pipeline, ...pipeline },
  };
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

describe('FleetTelemetryPanel — disclosure', () => {
  it('states that telemetry is account-attributed and makes no signing claim', async () => {
    const text = (await mountPanel()).text();
    expect(text).toContain('attributed to you');
    expect(text).toContain('not anonymous');
    // Nothing signs the OTLP export; the panel used to say it was.
    expect(text.toLowerCase()).not.toContain('signed with');
    expect(text.toLowerCase()).not.toContain('device key');
  });
});

describe('FleetTelemetryPanel — status line explains why nothing is reporting', () => {
  const cases: Array<[string, Promise<FleetTelemetryStatus>, string]> = [
    ['not opted in', statusWith({ stored_consent: 'none' }), 'you have not opted in'],
    [
      'tier clamps consent',
      statusWith({ stored_consent: 'full', effective_consent: 'none', org_tier: 'pro' }),
      'does not include',
    ],
    [
      'workbench waiting for host sign-in',
      statusWith({
        stored_consent: 'aggregate',
        effective_consent: 'aggregate',
        enroll: { auth_state: 'anonymous', enrolled: false, enroll_attempts: 0, enroll_failures: 0 },
      }),
      'sign in to Kenaz on the host',
    ],
    [
      'enroll failing',
      statusWith({
        stored_consent: 'aggregate',
        effective_consent: 'aggregate',
        enroll: {
          auth_state: 'signed_in',
          enrolled: false,
          enroll_attempts: 3,
          enroll_failures: 3,
          last_error: 'network',
        },
      }),
      'network',
    ],
    [
      'all classes off in Fleet preferences',
      statusWith({
        stored_consent: 'full',
        effective_consent: 'full',
        enrolled: true,
        opted_in_classes: [],
      }),
      'turned off in your Fleet preferences',
    ],
    [
      'fleet rejecting the token',
      statusWith(
        { stored_consent: 'full', effective_consent: 'full', enrolled: true },
        { active: true, exports_unauthorized: 2 },
      ),
      '401',
    ],
    [
      'active, idle',
      statusWith({ stored_consent: 'full', effective_consent: 'full', enrolled: true }, { active: true }),
      'nothing to report yet',
    ],
    [
      'reporting',
      statusWith(
        { stored_consent: 'full', effective_consent: 'full', enrolled: true },
        { active: true, events_accepted: 4, exports_ok: 1 },
      ),
      'Reporting to Fleet',
    ],
  ];
  for (const [name, status, want] of cases) {
    it(name, async () => {
      const st = await status;
      const w = await mountPanel(makeClient({ getTelemetryStatus: async () => st }));
      expect(w.find('[data-testid="telemetry-status-line"]').text()).toContain(want);
    });
  }

  it('says the listed classes are per-user preferences', async () => {
    const st = await statusWith(
      { stored_consent: 'full', effective_consent: 'full', enrolled: true },
      { active: true },
    );
    const w = await mountPanel(makeClient({ getTelemetryStatus: async () => st }));
    const text = w.find('[data-testid="telemetry-status-classes"]').text();
    expect(text).toContain('harness.usage_counts');
    expect(text).toContain('not an organization default');
  });

  it('a failing status call never blocks the consent controls', async () => {
    const w = await mountPanel(
      makeClient({
        getTelemetryStatus: async () => {
          throw new Error('boom');
        },
      }),
    );
    expect(w.find('[data-testid="telemetry-status"]').exists()).toBe(false);
    expect(w.find('[data-testid="fleet-telemetry-panel"]').exists()).toBe(true);
  });
});
