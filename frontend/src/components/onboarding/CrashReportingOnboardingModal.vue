<script setup lang="ts">
/**
 * CrashReportingOnboardingModal — first-launch one-time modal.
 *
 * Shown once when hasSeenCrashReportingOnboarding == false. The user can
 * choose a tier (or dismiss/skip, which sets tier=Off and marks the modal
 * as seen).
 *
 * (sentry-error-monitoring-01KX5R8G WP05 §4.1)
 */
import { ref } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';

const emit = defineEmits<{
  (e: 'close'): void;
}>();

const client = useHarnessClient();
const selected = ref<string>('off');
const saving = ref(false);

async function confirm() {
  saving.value = true;
  try {
    const s = await client.settings.get();
    s.crashReportingTier = selected.value;
    s.hasSeenCrashReportingOnboarding = true;
    await client.settings.set(s);
  } finally {
    saving.value = false;
    emit('close');
  }
}

async function dismiss() {
  saving.value = true;
  try {
    const s = await client.settings.get();
    s.crashReportingTier = 'off';
    s.hasSeenCrashReportingOnboarding = true;
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
    aria-labelledby="crash-onboarding-title"
    data-testid="crash-reporting-onboarding-modal"
  >
    <div class="bg-surface rounded-lg shadow-xl p-6 max-w-md w-full space-y-4">
      <h2 id="crash-onboarding-title" class="text-base font-semibold text-ink">
        Help improve Kenaz Harness
      </h2>
      <p class="text-sm text-ink-muted">
        When crashes or errors occur, we can automatically send a redacted
        report to help our team diagnose issues. No conversation content,
        API keys, or personal data is ever included.
      </p>

      <fieldset class="space-y-2">
        <legend class="sr-only">Choose a reporting tier</legend>
        <label class="flex items-start gap-2 cursor-pointer">
          <input
            v-model="selected"
            type="radio"
            value="off"
            class="mt-0.5"
          />
          <span>
            <span class="text-sm font-medium text-ink">No thanks</span>
            <span class="block text-xs text-ink-muted">
              Crash reporting disabled. You can change this later in
              Settings → Privacy.
            </span>
          </span>
        </label>
        <label class="flex items-start gap-2 cursor-pointer">
          <input
            v-model="selected"
            type="radio"
            value="anonymous"
            class="mt-0.5"
          />
          <span>
            <span class="text-sm font-medium text-ink">Send anonymous reports</span>
            <span class="block text-xs text-ink-muted">
              Redacted stack traces and error types only. No identity attached.
            </span>
          </span>
        </label>
      </fieldset>

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
