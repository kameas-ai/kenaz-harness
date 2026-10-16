<script setup lang="ts">
/**
 * ModelCallTelemetryPanel — Settings → Account → "Model-call telemetry"
 * (llm-call-telemetry-01LLMTEL01 WP02), next to the Fleet Telemetry
 * controls.
 *
 * Three settings, read and written through Settings_Get/
 * SetModelCallTelemetry:
 *   - "Fetch OpenRouter generation stats" (default on) — the async,
 *     rate-limited pull of OpenRouter's per-generation stats into the
 *     local record;
 *   - retention in days (blank / 0 = 30);
 *   - "Share model-call telemetry with Fleet" (default off, separate from
 *     the Fleet Telemetry level above) — what is sent is spelled out, and
 *     that content never is.
 */
import { onMounted, ref } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type { ModelCallTelemetrySettings } from '@/lib/types';

const client = useHarnessClient();

/** The published Telemetry Disclosure; opened in the system browser. */
const DISCLOSURE_URL = 'https://kenaz.kameas.ai/telemetry';
function openDisclosure() {
  client.openExternalURL(DISCLOSURE_URL);
}

const loaded = ref(false);
const saving = ref(false);
const errorMsg = ref('');
const statsPullEnabled = ref(true);
const shareWithFleet = ref(false);
const retentionInput = ref<string | number>(''); // v-model on type=number yields a number
const effectiveRetentionDays = ref(30);

function apply(mc: ModelCallTelemetrySettings) {
  statsPullEnabled.value = mc.statsPullEnabled;
  shareWithFleet.value = mc.shareWithFleet;
  retentionInput.value = mc.retentionDays > 0 ? String(mc.retentionDays) : '';
  effectiveRetentionDays.value = mc.effectiveRetentionDays;
}

onMounted(async () => {
  try {
    apply(await client.settings.getModelCallTelemetry());
    loaded.value = true;
  } catch (err) {
    errorMsg.value = String(err);
  }
});

async function save() {
  const raw = String(retentionInput.value ?? '').trim();
  const days = raw === '' ? 0 : Number(raw);
  if (!Number.isInteger(days) || days < 0 || days > 3650) {
    errorMsg.value = 'Retention must be a whole number of days from 1 to 3650 (blank = 30).';
    return;
  }
  saving.value = true;
  errorMsg.value = '';
  try {
    await client.settings.setModelCallTelemetry({
      statsPullEnabled: statsPullEnabled.value,
      retentionDays: days,
      effectiveRetentionDays: 0,
      shareWithFleet: shareWithFleet.value,
    });
    apply(await client.settings.getModelCallTelemetry());
  } catch (err) {
    errorMsg.value = String(err);
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <section class="space-y-4 p-4" data-testid="model-call-telemetry-panel">
    <p class="text-xs text-ink-muted">
      Kenaz keeps a record of every model call on this device: time, model, provider, token
      counts, latency, cost, finish reason and error class. Never the prompt, the response, tool
      arguments or tool results. Open it from the session's cost readout ("calls").
    </p>

    <fieldset class="space-y-3" :disabled="!loaded || saving">
      <label class="flex items-start gap-2 cursor-pointer">
        <input
          v-model="statsPullEnabled"
          type="checkbox"
          class="mt-0.5"
          data-testid="model-call-stats-pull"
          @change="save"
        />
        <span>
          <span class="text-sm text-ink font-medium">Fetch OpenRouter generation stats</span>
          <span class="block text-xs text-ink-muted">
            After an OpenRouter call, ask OpenRouter for that generation's provider, latency,
            native token counts and cost, using the same API key. At most about one request per
            second; nothing else is fetched.
          </span>
        </span>
      </label>

      <label class="block">
        <span class="text-sm text-ink font-medium">Keep records for</span>
        <span class="mt-1 flex items-center gap-2">
          <input
            v-model="retentionInput"
            type="number"
            min="1"
            max="3650"
            placeholder="30"
            class="w-24 rounded border border-hairline bg-surface-1 px-2 py-1 text-sm"
            data-testid="model-call-retention"
            @change="save"
          />
          <span class="text-xs text-ink-muted">days (now {{ effectiveRetentionDays }})</span>
        </span>
      </label>

      <label class="flex items-start gap-2 cursor-pointer">
        <input
          v-model="shareWithFleet"
          type="checkbox"
          class="mt-0.5"
          data-testid="model-call-share-fleet"
          @change="save"
        />
        <span>
          <span class="text-sm text-ink font-medium">Share model-call telemetry with Fleet</span>
          <span class="block text-xs text-ink-muted" data-testid="model-call-share-explainer">
            <!-- Counsel-approved text (kameas-ai-8f, B4a / D54, package doc 02), VERBATIM. Do not edit without legal review. -->
            Off by default, and separate from the Fleet Telemetry level above. This setting applies to
            this device only. When it is on and you are signed in, each model call you make from then
            on is sent to your organization's Fleet account, attributed to you: model and provider IDs,
            token counts, latency, cost, finish reason, error class and a keyed hash of the session ID.
            Your organization's Fleet administrators can see these records. Calls made before you turn
            it on are never sent. Prompts, responses, tool arguments and tool results are never sent.
            Turning it off stops sending; records already sent are kept under your organization's
            retention settings. This setting does not control usage records your organization receives
            directly from an AI provider it has connected to Fleet, such as OpenRouter; Fleet lists any
            such connection under Settings. Details:
            <a
              href="https://kenaz.kameas.ai/telemetry"
              class="underline"
              data-testid="model-call-disclosure-link"
              @click.prevent.stop="openDisclosure"
            >kenaz.kameas.ai/telemetry</a>
          </span>
        </span>
      </label>
    </fieldset>

    <p v-if="errorMsg" class="text-xs text-signal-danger" data-testid="model-call-telemetry-error">
      {{ errorMsg }}
    </p>
    <p v-if="saving" class="text-xs text-ink-muted">Saving…</p>
  </section>
</template>
