/**
 * AppTelemetryTierGate.spec.ts — the anti-regression pin for A10
 * (consent-surfaces-truth-01PMTR01 WP02).
 *
 * TelemetryOnboardingModal's own unit tests
 * (components/onboarding/__tests__/TelemetryOnboardingModal.spec.ts)
 * pass `accountTier` directly as a mount prop and are green today — on
 * a component whose prop no parent actually bound before this WP.
 * That is exactly CLAUDE.md's "mounted surface can still be dead" blind
 * spot: the component's gating logic was correct in isolation while
 * App.vue silently defaulted every user, including paying Team/
 * Enterprise accounts, to the free-tier badge.
 *
 * These tests therefore mount App.vue — not the modal — and drive
 * client.settings.fleetCapabilities() to prove the wiring, not just the
 * gate. TelemetryOnboardingModal renders through BaseDialog, which
 * Teleports its panel to document.body, so assertions query `document`
 * directly rather than the mounted wrapper (same pattern as
 * fleetSurfaces.newlyVisible.spec.ts).
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createRouter, createMemoryHistory } from 'vue-router';
import App from '@/App.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import { setConnectionState } from '@/lib/useConnectionState';

const stubComponent = { template: '<div />' };

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: stubComponent },
      { path: '/sessions', component: stubComponent },
    ],
  });
}

/** Flush the microtask queue N times — App.vue's onMounted chains several
 * sequential awaits (shellStatus, settings.get, restoreLastRoute,
 * onboarding.state, settings.get again, fleetCapabilities); a single
 * flushPromises() only drains one hop of that chain. */
async function flushAll(times = 8) {
  for (let i = 0; i < times; i++) {
    await flushPromises();
  }
}

function mountApp(client: ReturnType<typeof createFakeHarnessClient>) {
  return mount(App, {
    global: {
      plugins: [makeRouter()],
      provide: {
        [HarnessClientKey as symbol]: client,
      },
      stubs: {
        Shell: stubComponent,
        CommandPalette: stubComponent,
        ToastRoot: stubComponent,
        OnboardingDialog: stubComponent,
        AboutDialog: stubComponent,
      },
    },
  });
}

describe('App.vue — telemetry onboarding modal account-tier wiring', () => {
  beforeEach(() => {
    setConnectionState('ready');
    // Teleported dialog panels persist in document.body between tests; a
    // stale one would make the assertions below pass for the wrong reason.
    document.body.innerHTML = '';
  });

  afterEach(() => {
    vi.restoreAllMocks();
    document.body.innerHTML = '';
  });

  it("tier 'team' resolved: Aggregate and Full are enabled, no Requires badge", async () => {
    const client = createFakeHarnessClient();
    vi.spyOn(client.settings, 'fleetCapabilities').mockResolvedValue({
      tier: 'team',
      enabled: {},
      fetchedAt: new Date().toISOString(),
      source: 'fleet',
    });

    mountApp(client);
    await flushAll();

    const aggregateRadio = document.querySelector<HTMLInputElement>('[data-testid="aggregate-radio"]');
    const fullRadio = document.querySelector<HTMLInputElement>('[data-testid="full-radio"]');
    expect(aggregateRadio).not.toBeNull();
    expect(fullRadio).not.toBeNull();
    expect(aggregateRadio!.disabled).toBe(false);
    expect(fullRadio!.disabled).toBe(false);
    expect(document.querySelector('[data-testid="aggregate-gate-badge"]')).toBeNull();
    expect(document.querySelector('[data-testid="full-gate-badge"]')).toBeNull();
  });

  // Mutation: hardcode `aggregateAllowed = true` / `fullAllowed = true`
  // in TelemetryOnboardingModal.vue → this test fails (badges would not
  // render for a resolved-empty tier).
  //
  // Mutation: remove the `:account-tier="accountTier"` binding from
  // App.vue's <TelemetryOnboardingModal> → the modal falls back to its
  // own default prop ('', the most-restrictive fallback for an unwired
  // caller — see the prop doc comment) regardless of what
  // fleetCapabilities() resolves. That happens to still gate — same
  // observable result as a correctly-wired '' tier — so it is THIS
  // test's companion ('team', above) that actually distinguishes
  // "wired and resolved to team" from "unwired and defaulted to ''": if
  // the binding is removed, the 'team' test above starts failing
  // (unwired-default '' gates a tier that should be ungated), which is
  // the tasks.md-named assertion for this mutation.
  it("tier '' resolved: both options badged and disabled", async () => {
    const client = createFakeHarnessClient();
    vi.spyOn(client.settings, 'fleetCapabilities').mockResolvedValue({
      tier: '',
      enabled: {},
      fetchedAt: new Date().toISOString(),
      source: 'fleet',
    });

    mountApp(client);
    await flushAll();

    const aggregateRadio = document.querySelector<HTMLInputElement>('[data-testid="aggregate-radio"]');
    const fullRadio = document.querySelector<HTMLInputElement>('[data-testid="full-radio"]');
    expect(aggregateRadio).not.toBeNull();
    expect(fullRadio).not.toBeNull();
    expect(aggregateRadio!.disabled).toBe(true);
    expect(fullRadio!.disabled).toBe(true);
    expect(document.querySelector('[data-testid="aggregate-gate-badge"]')).not.toBeNull();
    expect(document.querySelector('[data-testid="full-gate-badge"]')).not.toBeNull();
  });

  // Mutation: in App.vue's onMounted catch branch, fall through to
  // accountTier.value = '' instead of leaving it at the unresolved
  // default → this test fails (badges would render for a rejected
  // fetch).
  it('fleetCapabilities() rejects: options remain enabled, no badge (fail-ungated)', async () => {
    const client = createFakeHarnessClient();
    vi.spyOn(client.settings, 'fleetCapabilities').mockRejectedValue(new Error('fleet unreachable'));

    mountApp(client);
    await flushAll();

    const aggregateRadio = document.querySelector<HTMLInputElement>('[data-testid="aggregate-radio"]');
    const fullRadio = document.querySelector<HTMLInputElement>('[data-testid="full-radio"]');
    expect(aggregateRadio).not.toBeNull();
    expect(fullRadio).not.toBeNull();
    expect(aggregateRadio!.disabled).toBe(false);
    expect(fullRadio!.disabled).toBe(false);
    expect(document.querySelector('[data-testid="aggregate-gate-badge"]')).toBeNull();
    expect(document.querySelector('[data-testid="full-gate-badge"]')).toBeNull();
  });
});
