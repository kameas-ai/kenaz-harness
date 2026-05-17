<script setup lang="ts">
/**
 * FleetTelemetryPanel — Privacy → Fleet Telemetry settings section.
 *
 * Three-tier opt-in: none / aggregate / full. Reads and persists the
 * consent level via the FleetTelemetryConsent / SetFleetTelemetryConsent
 * RPC pair. Shows a live preview of what each tier sends.
 *
 * (fleet-otel-archival-01NDFSEX11 WP06)
 */
import { ref, computed, onMounted } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';

const client = useHarnessClient();

// ── State ──────────────────────────────────────────────────────────────────

const consentLevel = ref<'none' | 'aggregate' | 'full'>('none');
const saving = ref(false);
const errorMsg = ref('');

// ── Load ────────────────────────────────────────────────────────────────────

onMounted(async () => {
  try {
    const level = await client.fleet.getTelemetryConsent();
    consentLevel.value = (level as 'none' | 'aggregate' | 'full') ?? 'none';
  } catch (err) {
    errorMsg.value = String(err);
  }
});

// ── Computed ────────────────────────────────────────────────────────────────

const previewLines = computed<string[]>(() => {
  switch (consentLevel.value) {
    case 'aggregate':
      return [
        'Span names + durations',
        'Status codes (ok / error)',
        'Metric counters + histograms (no labels)',
        'No log records',
        'No string attribute values',
        'Credentials always stripped',
      ];
    case 'full':
      return [
        'All spans with redactor-cleaned attributes',
        'All metrics with labels',
        'Log records (body redacted of secrets)',
        'Credentials, bearer tokens, API keys stripped by redactor',
        'Signed with device ed25519 key',
      ];
    default:
      return ['Nothing is sent to the fleet endpoint.'];
  }
});

// ── Actions ─────────────────────────────────────────────────────────────────

async function saveConsent(level: 'none' | 'aggregate' | 'full') {
  saving.value = true;
  errorMsg.value = '';
  try {
    await client.fleet.setTelemetryConsent(level);
    consentLevel.value = level;
  } catch (err) {
    errorMsg.value = String(err);
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <section class="space-y-6 p-4" data-testid="fleet-telemetry-panel">
    <div>
      <h2 class="text-sm font-semibold text-ink mb-1">Fleet Telemetry</h2>
      <p class="text-xs text-ink-muted">
        Opt in to share performance telemetry with the fleet endpoint. Data is
        signed with your device key and cleaned by the redactor before
        transmission. No conversation content, API keys, or credentials are
        ever included.
      </p>
    </div>

    <!-- Tier picker -->
    <fieldset class="space-y-2" :disabled="saving">
      <legend class="text-xs font-medium text-ink-muted uppercase tracking-wide">
        Telemetry level
      </legend>
      <div class="space-y-1">
        <label class="flex items-start gap-2 cursor-pointer">
          <input
            type="radio"
            name="fleet-telemetry-level"
            value="none"
            :checked="consentLevel === 'none'"
            class="mt-0.5"
            @change="saveConsent('none')"
          />
          <span>
            <span class="text-sm text-ink font-medium">None</span>
            <span class="block text-xs text-ink-muted">
              No telemetry is sent. This is the default.
            </span>
          </span>
        </label>
        <label class="flex items-start gap-2 cursor-pointer">
          <input
            type="radio"
            name="fleet-telemetry-level"
            value="aggregate"
            :checked="consentLevel === 'aggregate'"
            class="mt-0.5"
            @change="saveConsent('aggregate')"
          />
          <span>
            <span class="text-sm text-ink font-medium">Aggregate</span>
            <span class="block text-xs text-ink-muted">
              Counts + durations only. No string payloads, no log records.
              Requires Pro+ subscription.
            </span>
          </span>
        </label>
        <label class="flex items-start gap-2 cursor-pointer">
          <input
            type="radio"
            name="fleet-telemetry-level"
            value="full"
            :checked="consentLevel === 'full'"
            class="mt-0.5"
            @change="saveConsent('full')"
          />
          <span>
            <span class="text-sm text-ink font-medium">Full</span>
            <span class="block text-xs text-ink-muted">
              All redactor-cleaned data: spans, metrics, and log records. Errors
              still have credentials removed. Requires Team+ subscription.
            </span>
          </span>
        </label>
      </div>
    </fieldset>

    <!-- Live preview -->
    <div class="rounded border border-border-muted p-3 space-y-1" data-testid="telemetry-preview">
      <p class="text-xs font-semibold text-ink">
        {{ consentLevel === 'none' ? 'Nothing sent' : 'What gets sent:' }}
      </p>
      <ul class="text-xs text-ink-muted list-disc list-inside space-y-0.5">
        <li v-for="line in previewLines" :key="line">{{ line }}</li>
      </ul>
    </div>

    <!-- What we never send -->
    <div class="rounded border border-border-muted p-3 space-y-1">
      <p class="text-xs font-semibold text-ink">What we never send:</p>
      <ul class="text-xs text-ink-muted list-disc list-inside space-y-0.5">
        <li>Conversation messages or prompt text</li>
        <li>API keys, bearer tokens, or credentials (stripped by redactor)</li>
        <li>OAuth secrets, JWTs, or sk-* keys (stripped by redactor)</li>
        <li>Attributes marked <code>private.</code> in structured logs</li>
        <li>Log records under Aggregate consent</li>
      </ul>
    </div>

    <p v-if="errorMsg" class="text-xs text-red-500" data-testid="fleet-error">
      {{ errorMsg }}
    </p>
    <p v-if="saving" class="text-xs text-ink-muted">Saving…</p>
  </section>
</template>
