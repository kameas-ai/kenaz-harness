<script setup lang="ts">
/**
 * TelemetryOnboardingModal — first-launch one-time modal for fleet telemetry
 * consent.
 *
 * Shown once when hasSeenFleetTelemetryOnboarding == false. The user picks a
 * consent tier (none / aggregate / full) or dismisses, which defaults to none
 * and marks the modal as seen.
 *
 * Wires to the existing fleet telemetry consent backend
 * (Fleet_GetTelemetryConsent / Fleet_SetTelemetryConsent) — does NOT
 * duplicate any consent logic from FleetTelemetryPanel.vue.
 *
 * FR-001: Opaque panel over a dimmed backdrop via BaseDialog — no underlying
 *         surface content shows through.
 * FR-002: Tiers the account is not entitled to render disabled and cannot be
 *         committed via Confirm.
 * FR-004: "None" is the default; Skip / Esc dismisses WITHOUT enabling
 *         telemetry and persists that choice.
 * FR-005: Focus trap while open; Esc = Skip (delegated to BaseDialog).
 *
 * Subscription-gating seam:
 *   The `accountTier` prop carries the fleet subscription tier
 *   ('pro' | 'team' | 'enterprise' | ''). Wired from App.vue via
 *   client.settings.fleetCapabilities().tier (consent-surfaces-truth
 *   -01PMTR01 WP02).
 *
 *   `null` (the default) means "unresolved" — the capabilities fetch
 *   hasn't completed, or it rejected. Unresolved is deliberately
 *   distinct from '' ("resolved: no tier"): an unresolved tier is
 *   fail-ungated (no gate, no badge), matching FleetTelemetryPanel's
 *   sibling behaviour, which enforces nothing at all. Rendering
 *   "Requires Pro+" for an account we simply couldn't identify would be
 *   a new false claim, not a smaller one.
 *
 * (fleet-otel-archival-01NDFSEX11 WP06; bug-fix 01NBUG05;
 *  consent-surfaces-truth-01PMTR01 WP02)
 */
import { computed, ref } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import BaseDialog from '@/components/ui/BaseDialog.vue';

const props = withDefaults(
  defineProps<{
    /**
     * Fleet subscription tier for the current account.
     * Pass client.settings.fleetCapabilities().tier here.
     *
     * '' (resolved, no tier) gates both options and shows the "Requires
     * …" badges. An explicit null (App.vue's "unresolved" ref state —
     * not yet fetched, or the fetch rejected) is fail-ungated: neither
     * option is disabled and no badge renders.
     *
     * The default below applies only when the prop is omitted entirely
     * (no caller wired it) and stays the most-restrictive value ''
     * deliberately — App.vue always binds an explicit value (starting
     * at null), so this default is a defensive fallback for a caller
     * that doesn't wire the prop at all, not the "unresolved" state.
     */
    accountTier?: 'pro' | 'team' | 'enterprise' | '' | null;
  }>(),
  {
    accountTier: '',
  },
);

const emit = defineEmits<{
  (e: 'close'): void;
}>();

const client = useHarnessClient();
const selected = ref<'none' | 'aggregate' | 'full'>('none');
const saving = ref(false);

// ── entitlement gates ───────────────────────────────────────────────────────
// "Aggregate" requires Pro+  (pro | team | enterprise)
// "Full"      requires Team+ (team | enterprise)
const TIER_RANK: Record<string, number> = {
  '': 0,
  pro: 1,
  team: 2,
  enterprise: 3,
};

// Unresolved (null/undefined) is fail-ungated: no gate, no badge. Only
// a RESOLVED tier ('' | 'pro' | 'team' | 'enterprise') is ranked.
const aggregateAllowed = computed(() => {
  if (props.accountTier == null) return true;
  return TIER_RANK[props.accountTier] >= TIER_RANK['pro'];
});
const fullAllowed = computed(() => {
  if (props.accountTier == null) return true;
  return TIER_RANK[props.accountTier] >= TIER_RANK['team'];
});

/**
 * Whether the current selection is committable.
 * Gated tiers that are selected (e.g. via programmatic set) cannot be
 * confirmed — Confirm is disabled in that case.
 */
const canConfirm = computed(() => {
  if (saving.value) return false;
  if (selected.value === 'aggregate' && !aggregateAllowed.value) return false;
  if (selected.value === 'full' && !fullAllowed.value) return false;
  return true;
});

// ── actions ─────────────────────────────────────────────────────────────────

async function confirm() {
  // Guard: never commit a gated tier the account isn't entitled to.
  if (!canConfirm.value) return;
  saving.value = true;
  try {
    await client.fleet.setTelemetryConsent(selected.value);
    const s = await client.settings.get();
    s.hasSeenFleetTelemetryOnboarding = true;
    await client.settings.set(s);
  } finally {
    saving.value = false;
    emit('close');
  }
}

/** Skip / Esc — always persists 'none' and marks the modal as seen. */
async function dismiss() {
  saving.value = true;
  try {
    await client.fleet.setTelemetryConsent('none');
    const s = await client.settings.get();
    s.hasSeenFleetTelemetryOnboarding = true;
    await client.settings.set(s);
  } finally {
    saving.value = false;
    emit('close');
  }
}
</script>

<template>
  <!--
    BaseDialog provides:
      • bg-modal-overlay opaque backdrop — no underlying content bleeds through (FR-001)
      • Focus trap (Tab cycles within the panel) (FR-005)
      • Esc → emits 'close' → dismiss() (FR-004 / FR-005)
    The `open` prop is always true here because App.vue uses v-if to
    mount/unmount this component.
  -->
  <BaseDialog
    :open="true"
    title="Share performance telemetry"
    panel-class="bg-surface-2 rounded-lg shadow-xl p-6 max-w-md w-full space-y-4"
    :close-on-overlay-click="false"
    @close="dismiss"
  >
    <div data-testid="telemetry-onboarding-modal">
      <h2 id="telemetry-onboarding-title" class="text-base font-semibold text-ink mb-3">
        Share performance telemetry
      </h2>
      <p class="text-sm text-ink-muted mb-4">
        Help improve Kenaz Harness by sharing redacted performance data with the
        fleet endpoint. No conversation content, API keys, or credentials are
        ever included — the redactor strips them before transmission.
      </p>

      <fieldset class="space-y-2 mb-4">
        <legend class="sr-only">Choose a telemetry tier</legend>

        <!-- None (always available) -->
        <label class="flex items-start gap-2 cursor-pointer">
          <input
            v-model="selected"
            type="radio"
            value="none"
            class="mt-0.5"
          />
          <span>
            <span class="text-sm font-medium text-ink">None</span>
            <span class="block text-xs text-ink-muted">
              No telemetry is sent. You can change this later in
              Settings &rarr; Privacy.
            </span>
          </span>
        </label>

        <!-- Aggregate — Requires Pro+ -->
        <label
          class="flex items-start gap-2"
          :class="aggregateAllowed ? 'cursor-pointer' : 'opacity-50 cursor-not-allowed'"
        >
          <input
            v-model="selected"
            type="radio"
            value="aggregate"
            :disabled="!aggregateAllowed"
            class="mt-0.5"
            data-testid="aggregate-radio"
          />
          <span>
            <span class="text-sm font-medium text-ink">Aggregate</span>
            <span
              v-if="!aggregateAllowed"
              class="ml-1.5 text-xs font-medium text-ink-subtle bg-surface-3 rounded px-1.5 py-0.5"
              data-testid="aggregate-gate-badge"
            >Requires Pro+</span>
            <span class="block text-xs text-ink-muted">
              Span names, durations, and status counts only. No string
              payloads or log records.
            </span>
          </span>
        </label>

        <!-- Full — Requires Team+ -->
        <label
          class="flex items-start gap-2"
          :class="fullAllowed ? 'cursor-pointer' : 'opacity-50 cursor-not-allowed'"
        >
          <input
            v-model="selected"
            type="radio"
            value="full"
            :disabled="!fullAllowed"
            class="mt-0.5"
            data-testid="full-radio"
          />
          <span>
            <span class="text-sm font-medium text-ink">Full</span>
            <span
              v-if="!fullAllowed"
              class="ml-1.5 text-xs font-medium text-ink-subtle bg-surface-3 rounded px-1.5 py-0.5"
              data-testid="full-gate-badge"
            >Requires Team+</span>
            <span class="block text-xs text-ink-muted">
              All redactor-cleaned spans, metrics, and log records. Errors
              still have credentials removed.
            </span>
          </span>
        </label>
      </fieldset>

      <!-- What we never send -->
      <div class="rounded border border-border-muted p-3 space-y-1 text-xs mb-4">
        <p class="font-semibold text-ink">What we never send:</p>
        <ul class="text-ink-muted list-disc list-inside space-y-0.5">
          <li>Conversation messages or prompt text</li>
          <li>API keys, bearer tokens, or credentials</li>
          <li>Attributes prefixed <code>private.</code></li>
          <li>Log records under Aggregate consent</li>
        </ul>
      </div>

      <div class="flex justify-end gap-3 pt-2">
        <button
          type="button"
          class="text-sm px-3 py-1.5 rounded border border-border-muted text-ink-muted hover:text-ink"
          :disabled="saving"
          data-testid="skip-button"
          @click="dismiss"
        >
          Skip
        </button>
        <button
          type="button"
          class="text-sm px-3 py-1.5 rounded bg-accent text-white hover:opacity-90 disabled:opacity-40 disabled:cursor-not-allowed"
          :disabled="!canConfirm"
          data-testid="confirm-button"
          @click="confirm"
        >
          {{ saving ? 'Saving…' : 'Confirm' }}
        </button>
      </div>
    </div>
  </BaseDialog>
</template>
