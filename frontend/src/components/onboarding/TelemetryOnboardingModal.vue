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
 * (fleet-otel-archival-01NDFSEX11 WP06)
 */
import { ref } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';

const emit = defineEmits<{
  (e: 'close'): void;
}>();

const client = useHarnessClient();
const selected = ref<'none' | 'aggregate' | 'full'>('none');
const saving = ref(false);

async function confirm() {
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
  <div
    class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
    role="dialog"
    aria-modal="true"
    aria-labelledby="telemetry-onboarding-title"
    data-testid="telemetry-onboarding-modal"
  >
    <div class="bg-surface rounded-lg shadow-xl p-6 max-w-md w-full space-y-4">
      <h2 id="telemetry-onboarding-title" class="text-base font-semibold text-ink">
        Share performance telemetry
      </h2>
      <p class="text-sm text-ink-muted">
        Help improve Kenaz Harness by sharing redacted performance data with the
        fleet endpoint. No conversation content, API keys, or credentials are
        ever included — the redactor strips them before transmission.
      </p>

      <fieldset class="space-y-2">
        <legend class="sr-only">Choose a telemetry tier</legend>
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
        <label class="flex items-start gap-2 cursor-pointer">
          <input
            v-model="selected"
            type="radio"
            value="aggregate"
            class="mt-0.5"
          />
          <span>
            <span class="text-sm font-medium text-ink">Aggregate</span>
            <span class="block text-xs text-ink-muted">
              Span names, durations, and status counts only. No string
              payloads or log records. Requires Pro+ subscription.
            </span>
          </span>
        </label>
        <label class="flex items-start gap-2 cursor-pointer">
          <input
            v-model="selected"
            type="radio"
            value="full"
            class="mt-0.5"
          />
          <span>
            <span class="text-sm font-medium text-ink">Full</span>
            <span class="block text-xs text-ink-muted">
              All redactor-cleaned spans, metrics, and log records. Errors
              still have credentials removed. Requires Team+ subscription.
            </span>
          </span>
        </label>
      </fieldset>

      <!-- What we never send -->
      <div class="rounded border border-border-muted p-3 space-y-1 text-xs">
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
          @click="dismiss"
        >
          Skip
        </button>
        <button
          type="button"
          class="text-sm px-3 py-1.5 rounded bg-accent text-white hover:opacity-90"
          :disabled="saving"
          @click="confirm"
        >
          {{ saving ? 'Saving…' : 'Confirm' }}
        </button>
      </div>
    </div>
  </div>
</template>
