/**
 * TelemetryOnboardingModal.spec.ts
 *
 * Unit tests for the first-launch fleet-telemetry consent modal.
 *
 * Coverage:
 *  FR-001 — opaque panel: bg-surface-2 class present on the panel element
 *  FR-002 — gated tiers disabled/uncommittable when account tier is insufficient
 *  FR-004 — Esc / Skip leaves telemetry at 'none' and marks modal as seen
 *  FR-005 — focus-trap is delegated to BaseDialog (tested structurally)
 *
 * (fleet-otel-archival-01NDFSEX11 WP06; bug-fix 01NBUG05)
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import TelemetryOnboardingModal from '@/components/onboarding/TelemetryOnboardingModal.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { Settings } from '@/lib/types';

// ── helpers ────────────────────────────────────────────────────────────────

type Tier = 'pro' | 'team' | 'enterprise' | '';

const BASE_SETTINGS: Settings = {
  schemaVersion: 1,
  lastRoute: '/sessions',
  theme: 'system',
  accent: 'default',
  windowSize: { width: 1280, height: 800 },
  memoryEnabled: false,
  confirmEachDisabled: false,
  hasSeenFleetTelemetryOnboarding: false,
};

/**
 * BaseDialog stub template.
 *
 * - Renders its slot when :open is true.
 * - Passes panelClass to a wrapper so FR-001 class assertions work.
 * - Emits 'close' when the synthetic Esc key arrives (FR-004 / FR-005).
 */
const BASE_DIALOG_STUB = {
  name: 'BaseDialog',
  props: ['open', 'title', 'panelClass', 'closeOnOverlayClick'],
  emits: ['close'],
  template: `
    <div
      v-if="open"
      data-testid="base-dialog-root"
      :class="panelClass"
    >
      <slot />
    </div>
  `,
};

function setup(accountTier: Tier = '') {
  const client = createFakeHarnessClient();

  vi.spyOn(client.settings, 'get').mockResolvedValue({ ...BASE_SETTINGS });
  const setFn = vi.spyOn(client.settings, 'set').mockResolvedValue(undefined);
  const setConsentFn = vi.spyOn(client.fleet, 'setTelemetryConsent').mockResolvedValue(undefined);

  const wrapper = mount(TelemetryOnboardingModal, {
    props: { accountTier },
    global: {
      provide: { [HarnessClientKey as symbol]: client },
      stubs: {
        BaseDialog: BASE_DIALOG_STUB,
      },
    },
  });

  return { wrapper, setFn, setConsentFn };
}

// ── FR-001: opaque panel ───────────────────────────────────────────────────

describe('FR-001 — opaque backdrop panel', () => {
  it('renders the panel with bg-surface-2 (opaque surface token)', () => {
    const { wrapper } = setup();
    // BaseDialog stub applies panelClass to its root div.
    const panel = wrapper.find('[data-testid="base-dialog-root"]');
    expect(panel.exists()).toBe(true);
    expect(panel.classes()).toContain('bg-surface-2');
  });

  it('renders the modal data-testid inside the panel', () => {
    const { wrapper } = setup();
    expect(wrapper.find('[data-testid="telemetry-onboarding-modal"]').exists()).toBe(true);
  });
});

// ── FR-002: gated tiers disabled / uncommittable ───────────────────────────

describe('FR-002 — subscription gating (free / no tier)', () => {
  it('disables the aggregate radio for free-tier users', () => {
    const { wrapper } = setup('');
    const aggregateRadio = wrapper.find('[data-testid="aggregate-radio"]');
    expect((aggregateRadio.element as HTMLInputElement).disabled).toBe(true);
  });

  it('disables the full radio for free-tier users', () => {
    const { wrapper } = setup('');
    const fullRadio = wrapper.find('[data-testid="full-radio"]');
    expect((fullRadio.element as HTMLInputElement).disabled).toBe(true);
  });

  it('shows "Requires Pro+" badge when aggregate is gated', () => {
    const { wrapper } = setup('');
    const badge = wrapper.find('[data-testid="aggregate-gate-badge"]');
    expect(badge.exists()).toBe(true);
    expect(badge.text()).toContain('Requires Pro+');
  });

  it('shows "Requires Team+" badge when full is gated', () => {
    const { wrapper } = setup('');
    const badge = wrapper.find('[data-testid="full-gate-badge"]');
    expect(badge.exists()).toBe(true);
    expect(badge.text()).toContain('Requires Team+');
  });

  it('Confirm is enabled for default "none" selection (none is always committable)', () => {
    const { wrapper } = setup('');
    const confirmBtn = wrapper.find('[data-testid="confirm-button"]');
    expect((confirmBtn.element as HTMLButtonElement).disabled).toBe(false);
  });

  it('does NOT call setTelemetryConsent with aggregate when clicking Confirm at free tier', async () => {
    const { wrapper, setConsentFn } = setup('');
    // Default selection is 'none'; clicking Confirm should only submit 'none'.
    await wrapper.find('[data-testid="confirm-button"]').trigger('click');
    await flushPromises();
    expect(setConsentFn).toHaveBeenCalledWith('none');
    expect(setConsentFn).not.toHaveBeenCalledWith('aggregate');
    expect(setConsentFn).not.toHaveBeenCalledWith('full');
  });
});

describe('FR-002 — subscription gating (pro tier)', () => {
  it('enables aggregate radio for pro users', () => {
    const { wrapper } = setup('pro');
    const aggregateRadio = wrapper.find('[data-testid="aggregate-radio"]');
    expect((aggregateRadio.element as HTMLInputElement).disabled).toBe(false);
  });

  it('still disables full radio for pro users (requires team+)', () => {
    const { wrapper } = setup('pro');
    const fullRadio = wrapper.find('[data-testid="full-radio"]');
    expect((fullRadio.element as HTMLInputElement).disabled).toBe(true);
  });

  it('hides "Requires Pro+" badge for pro users', () => {
    const { wrapper } = setup('pro');
    expect(wrapper.find('[data-testid="aggregate-gate-badge"]').exists()).toBe(false);
  });

  it('still shows "Requires Team+" badge for pro users', () => {
    const { wrapper } = setup('pro');
    expect(wrapper.find('[data-testid="full-gate-badge"]').exists()).toBe(true);
  });

  it('allows committing aggregate consent for pro users', async () => {
    const { wrapper, setConsentFn } = setup('pro');
    await wrapper.find('[data-testid="aggregate-radio"]').setValue(true);
    const confirmBtn = wrapper.find('[data-testid="confirm-button"]');
    expect((confirmBtn.element as HTMLButtonElement).disabled).toBe(false);
    await confirmBtn.trigger('click');
    await flushPromises();
    expect(setConsentFn).toHaveBeenCalledWith('aggregate');
  });
});

describe('FR-002 — subscription gating (team tier)', () => {
  it('enables both aggregate and full radios for team users', () => {
    const { wrapper } = setup('team');
    expect((wrapper.find('[data-testid="aggregate-radio"]').element as HTMLInputElement).disabled).toBe(false);
    expect((wrapper.find('[data-testid="full-radio"]').element as HTMLInputElement).disabled).toBe(false);
  });

  it('hides both gate badges for team users', () => {
    const { wrapper } = setup('team');
    expect(wrapper.find('[data-testid="aggregate-gate-badge"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="full-gate-badge"]').exists()).toBe(false);
  });

  it('allows committing full consent for team users', async () => {
    const { wrapper, setConsentFn } = setup('team');
    await wrapper.find('[data-testid="full-radio"]').setValue(true);
    await wrapper.find('[data-testid="confirm-button"]').trigger('click');
    await flushPromises();
    expect(setConsentFn).toHaveBeenCalledWith('full');
  });
});

// ── FR-004: Skip / Esc → none, marks seen ─────────────────────────────────

describe('FR-004 — Skip / Esc leaves telemetry off', () => {
  it('has "none" selected by default', () => {
    const { wrapper } = setup();
    const noneRadio = wrapper.find('input[value="none"]');
    expect((noneRadio.element as HTMLInputElement).checked).toBe(true);
  });

  it('Skip sets consent to none and marks modal as seen', async () => {
    const { wrapper, setFn, setConsentFn } = setup();
    await wrapper.find('[data-testid="skip-button"]').trigger('click');
    await flushPromises();

    expect(setConsentFn).toHaveBeenCalledWith('none');
    const saved = setFn.mock.calls[0][0] as Settings;
    expect(saved.hasSeenFleetTelemetryOnboarding).toBe(true);
  });

  it('Skip emits close', async () => {
    const { wrapper } = setup();
    await wrapper.find('[data-testid="skip-button"]').trigger('click');
    await flushPromises();
    expect(wrapper.emitted('close')).toBeTruthy();
  });

  it('Esc (BaseDialog close event) sets consent to none and emits close', async () => {
    const { wrapper, setConsentFn } = setup();
    // Simulate BaseDialog emitting 'close' on Escape (the real BaseDialog does
    // this via its keydown handler; here we trigger the emitted event directly).
    const baseDialog = wrapper.findComponent({ name: 'BaseDialog' });
    await baseDialog.vm.$emit('close');
    await flushPromises();

    expect(setConsentFn).toHaveBeenCalledWith('none');
    expect(wrapper.emitted('close')).toBeTruthy();
  });

  it('Esc does NOT call setTelemetryConsent with full even when full is selected', async () => {
    const { wrapper, setConsentFn } = setup('team');
    // Select 'full', then Esc — should still save 'none', not 'full'.
    await wrapper.find('[data-testid="full-radio"]').setValue(true);
    const baseDialog = wrapper.findComponent({ name: 'BaseDialog' });
    await baseDialog.vm.$emit('close');
    await flushPromises();

    expect(setConsentFn).toHaveBeenCalledWith('none');
    expect(setConsentFn).not.toHaveBeenCalledWith('full');
  });
});

// ── FR-005: structural focus-trap seam ────────────────────────────────────

describe('FR-005 — focus trap seam (BaseDialog delegation)', () => {
  it('delegates to BaseDialog so focus-trap and Esc are inherited automatically', () => {
    const { wrapper } = setup();
    // If TelemetryOnboardingModal wraps BaseDialog, focus-trap + Esc handling
    // are guaranteed by BaseDialog's implementation (tested in BaseDialog.spec.ts).
    const baseDialog = wrapper.findComponent({ name: 'BaseDialog' });
    expect(baseDialog.exists()).toBe(true);
  });
});

// ── existing passing tests ─────────────────────────────────────────────────

describe('confirm flow', () => {
  it('confirm with none calls setTelemetryConsent(none) and marks seen', async () => {
    const { wrapper, setFn, setConsentFn } = setup();
    await flushPromises();

    await wrapper.find('[data-testid="confirm-button"]').trigger('click');
    await flushPromises();

    expect(setConsentFn).toHaveBeenCalledWith('none');
    const saved = setFn.mock.calls[0][0] as Settings;
    expect(saved.hasSeenFleetTelemetryOnboarding).toBe(true);
  });

  it('emits close after confirm', async () => {
    const { wrapper } = setup();
    await flushPromises();

    await wrapper.find('[data-testid="confirm-button"]').trigger('click');
    await flushPromises();

    expect(wrapper.emitted('close')).toBeTruthy();
    expect(wrapper.emitted('close')!.length).toBe(1);
  });

  it('emits close after skip', async () => {
    const { wrapper } = setup();
    await flushPromises();

    await wrapper.find('[data-testid="skip-button"]').trigger('click');
    await flushPromises();

    expect(wrapper.emitted('close')).toBeTruthy();
    expect(wrapper.emitted('close')!.length).toBe(1);
  });
});
