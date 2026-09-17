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
      // Keep in step with core/fleet/usage_emitter.go (aggregate lane) and
      // UsageCounters() in otlp_usage_lane.go — six label-less counters.
      return [
        'Counts only: conversations started and ended, tool calls, errors',
        'Token totals in and out',
        'No labels: no tool names, no model names, no durations',
        'No log records, no spans',
        'Nothing is sent for an interval with no activity',
      ];
    case 'full':
      // Keep in step with core/fleet/usage_emitter.go (full lane): the four
      // harness.* event kinds and their closed bodies.
      return [
        'Conversation start and end: duration, token totals, cost, model provider',
        'Tool calls: built-in tool name, latency, success. Tools from your own MCP servers are reported only as "external_tool"',
        'Errors: a category (auth, transient, cancelled, budget, unknown) — never the message',
        'A random id per conversation that links its start to its end and nothing else',
        'Diagnostic spans only if your organization has enabled the diagnostics class',
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
              Counts only. No names, no string payloads, no log records.
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
              Usage events with bounded fields: conversation and tool-call
              records, error categories. Requires Team+ subscription.
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
        <li>Conversation messages, prompt text, or model output</li>
        <li>Source code, file contents, or file paths</li>
        <li>Tool arguments or tool results</li>
        <li>Names of your own MCP servers or tools</li>
        <li>Error messages or stack traces</li>
        <li>Session ids, API keys, tokens, or credentials</li>
        <li>Application log lines</li>
        <li>Log records under Aggregate consent</li>
      </ul>
    </div>

    <p v-if="errorMsg" class="text-xs text-signal-danger" data-testid="fleet-error">
      {{ errorMsg }}
    </p>
    <p v-if="saving" class="text-xs text-ink-muted">Saving…</p>
  </section>
</template>
