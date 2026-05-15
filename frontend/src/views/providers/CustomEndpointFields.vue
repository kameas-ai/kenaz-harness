<script setup lang="ts">
/**
 * CustomEndpointFields — form section for the custom-openai provider kind.
 *
 * Responsibilities:
 *  - Let the user enter a base URL; debounce triggers RecognizeTemplate
 *    to auto-fill auth scheme when the host matches a built-in template.
 *  - Show a template picker (from ListCustomTemplates) that pre-fills URL
 *    and auth scheme.
 *  - Conditional API key field based on the resolved auth scheme.
 *  - Probe button that calls ProbeCustomEndpoint and renders the capability
 *    checklist (streaming / tool-calling / streaming-usage).
 *
 * (custom-openai-compatible-endpoint-01KQ8VN0 WP06)
 */

import { computed, ref, watch } from 'vue';
import type { CustomAuthScheme, CustomTemplateSummary, CustomCapabilityMatrix } from '@/lib/types';
import Button from '@/components/ui/Button.vue';
import { useHarnessClient } from '@/lib/harnessClientContext';

export interface CustomEndpointValue {
  baseURL: string;
  model: string;
  authScheme: CustomAuthScheme;
  authHeader: string;
  apiKey: string;
  templateID: string;
}

const props = defineProps<{
  /** When true, auth-scheme / URL are read-only (edit mode). */
  editing?: boolean;
  /** Pre-fill values in edit mode. */
  initial?: Partial<CustomEndpointValue>;
}>();

const emit = defineEmits<{
  /** Fired whenever any field changes. */
  (e: 'update:value', v: CustomEndpointValue): void;
  /**
   * Fired after a successful probe. The parent can use the matrix to
   * display the checklist outside this component (e.g. in a sidebar).
   */
  (e: 'probed', matrix: CustomCapabilityMatrix): void;
}>();

const client = useHarnessClient();

// ── form state ────────────────────────────────────────────────────────────
const baseURL = ref(props.initial?.baseURL ?? '');
const model = ref(props.initial?.model ?? '');
const authScheme = ref<CustomAuthScheme>(props.initial?.authScheme ?? 'bearer');
const authHeader = ref(props.initial?.authHeader ?? '');
const apiKey = ref(props.initial?.apiKey ?? '');
const templateID = ref(props.initial?.templateID ?? '');

// ── template list ─────────────────────────────────────────────────────────
const templates = ref<CustomTemplateSummary[]>([]);
const templatesLoading = ref(false);

async function loadTemplates() {
  if (templates.value.length > 0 || templatesLoading.value) return;
  templatesLoading.value = true;
  try {
    templates.value = await client.llm.listCustomTemplates();
  } catch {
    // silently fall through — user can type a URL manually
  } finally {
    templatesLoading.value = false;
  }
}
loadTemplates();

function applyTemplate(tmpl: CustomTemplateSummary) {
  templateID.value = tmpl.id;
  baseURL.value = tmpl.base_url;
  authScheme.value = tmpl.auth_scheme;
  authHeader.value = '';
  emitValue();
}

// ── URL recognition (debounce 600 ms) ────────────────────────────────────
let recognizeTimer: ReturnType<typeof setTimeout> | null = null;
const recognizing = ref(false);

watch(baseURL, (val) => {
  if (recognizeTimer) clearTimeout(recognizeTimer);
  // Skip auto-recognize when a template was explicitly selected.
  if (templateID.value) return;
  recognizeTimer = setTimeout(async () => {
    if (!val.trim()) return;
    recognizing.value = true;
    try {
      const r = await client.llm.recognizeTemplate(val.trim());
      if (r.matched && r.template) {
        authScheme.value = r.template.auth_scheme;
        if (!templateID.value) templateID.value = r.template.id;
        emitValue();
      }
    } catch {
      // ignore — user types on
    } finally {
      recognizing.value = false;
    }
  }, 600);
});

// ── capability probe ───────────────────────────────────────────────────────
const probeInFlight = ref(false);
const probeMatrix = ref<CustomCapabilityMatrix | null>(null);
const probeError = ref<string | null>(null);

const canProbe = computed(() => {
  const url = baseURL.value.trim();
  if (!url) return false;
  try {
    new URL(url.startsWith('http') ? url : `https://${url}`);
    return true;
  } catch {
    return false;
  }
});

async function onProbe() {
  if (!canProbe.value || probeInFlight.value) return;
  probeInFlight.value = true;
  probeError.value = null;
  probeMatrix.value = null;
  try {
    const result = await client.llm.probeCustomEndpoint({
      base_url: baseURL.value.trim(),
      model: model.value.trim(),
      auth_scheme: authScheme.value,
      auth_header: authHeader.value.trim() || undefined,
      plaintext_api_key: apiKey.value || undefined,
    });
    probeMatrix.value = result.matrix;
    if (result.error) {
      probeError.value = result.error;
    } else {
      emit('probed', result.matrix);
    }
  } catch (err) {
    probeError.value = err instanceof Error ? err.message : String(err);
  } finally {
    probeInFlight.value = false;
  }
}

// ── auth scheme computed helpers ──────────────────────────────────────────
const requiresKey = computed(() =>
  authScheme.value === 'bearer' ||
  authScheme.value === 'api-key-header' ||
  authScheme.value === 'custom',
);

// ── emit on change ─────────────────────────────────────────────────────────
function emitValue() {
  emit('update:value', {
    baseURL: baseURL.value,
    model: model.value,
    authScheme: authScheme.value,
    authHeader: authHeader.value,
    apiKey: apiKey.value,
    templateID: templateID.value,
  });
}

watch([baseURL, model, authScheme, authHeader, apiKey, templateID], emitValue);

// ── capability value display ───────────────────────────────────────────────
function capLabel(val: 'true' | 'false' | 'unknown'): string {
  switch (val) {
    case 'true': return 'Yes';
    case 'false': return 'No';
    default: return '?';
  }
}
function capClass(val: 'true' | 'false' | 'unknown'): string {
  switch (val) {
    case 'true': return 'text-signal-success';
    case 'false': return 'text-signal-danger';
    default: return 'text-ink-dim';
  }
}

defineExpose({ baseURL, model, authScheme, authHeader, apiKey, templateID, probeMatrix });
</script>

<template>
  <div class="space-y-4">
    <!-- Template picker -->
    <div v-if="templates.length > 0">
      <label class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
        Quick-select template
      </label>
      <div class="mt-1 flex flex-wrap gap-1.5">
        <button
          v-for="tmpl in templates"
          :key="tmpl.id"
          type="button"
          class="rounded-sm border border-border-muted bg-surface-1 px-2 py-0.5 text-xs font-ui text-ink-muted hover:border-accent hover:text-ink transition-fast"
          :class="templateID === tmpl.id ? 'border-accent text-ink bg-surface-2' : ''"
          :data-testid="`custom-template-${tmpl.id}`"
          @click="applyTemplate(tmpl)"
        >
          {{ tmpl.name }}
        </button>
      </div>
    </div>

    <!-- Base URL -->
    <div>
      <label
        for="custom-base-url"
        class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
      >
        Base URL
      </label>
      <div class="mt-1 relative">
        <input
          id="custom-base-url"
          v-model="baseURL"
          type="url"
          class="w-full rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 text-sm font-mono text-ink focus:border-accent focus:outline-none"
          :disabled="editing"
          autocomplete="off"
          :data-testid="'custom-base-url'"
          placeholder="https://api.example.com/v1"
        />
        <span
          v-if="recognizing"
          class="absolute right-2 top-1/2 -translate-y-1/2 text-[10px] text-ink-dim"
        >
          detecting…
        </span>
      </div>
      <p class="mt-1 text-[11px] text-ink-dim">
        The root URL of the OpenAI-compatible API (no trailing slash).
      </p>
    </div>

    <!-- Model ID (optional — used in probe + as default for chat) -->
    <div>
      <label
        for="custom-model"
        class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
      >
        Model ID
      </label>
      <input
        id="custom-model"
        v-model="model"
        type="text"
        class="mt-1 w-full rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 text-sm font-mono text-ink focus:border-accent focus:outline-none"
        autocomplete="off"
        :data-testid="'custom-model'"
        placeholder="e.g. meta-llama/Llama-3-8b-instruct"
      />
      <p class="mt-1 text-[11px] text-ink-dim">
        The model name sent with every request. Required for probe.
      </p>
    </div>

    <!-- Auth scheme -->
    <div>
      <span class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
        Auth scheme
      </span>
      <div class="mt-1 flex gap-1 rounded-sm border border-border-muted bg-surface-1 p-0.5">
        <button
          v-for="scheme in (['bearer', 'api-key-header', 'custom', 'none'] as CustomAuthScheme[])"
          :key="scheme"
          type="button"
          class="flex-1 px-2 py-1 rounded-sm text-xs font-ui transition-fast"
          :class="
            authScheme === scheme
              ? 'bg-surface-2 text-ink ring-1 ring-accent-hairline'
              : 'text-ink-muted hover:text-ink'
          "
          :data-testid="`custom-auth-scheme-${scheme}`"
          @click="authScheme = scheme"
        >
          {{ scheme }}
        </button>
      </div>
    </div>

    <!-- Custom auth header (shown for api-key-header and custom) -->
    <div v-if="authScheme === 'api-key-header' || authScheme === 'custom'">
      <label
        for="custom-auth-header"
        class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
      >
        Auth header name
      </label>
      <input
        id="custom-auth-header"
        v-model="authHeader"
        type="text"
        class="mt-1 w-full rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 text-sm font-mono text-ink focus:border-accent focus:outline-none"
        autocomplete="off"
        :data-testid="'custom-auth-header'"
        :placeholder="authScheme === 'api-key-header' ? 'X-API-Key' : 'Authorization'"
      />
    </div>

    <!-- API key (all schemes except none) -->
    <div v-if="requiresKey">
      <label
        for="custom-api-key"
        class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
      >
        API Key
      </label>
      <input
        id="custom-api-key"
        v-model="apiKey"
        type="password"
        class="mt-1 w-full rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 text-sm font-mono text-ink focus:border-accent focus:outline-none"
        autocomplete="off"
        :data-testid="'custom-api-key'"
        :placeholder="editing ? 'leave blank to keep current key' : 'paste key'"
      />
      <p class="mt-1 text-[11px] text-ink-dim">
        Stored in your OS keychain. Never written to providers.json.
      </p>
    </div>

    <!-- Probe button -->
    <div class="flex items-center gap-2">
      <Button
        variant="ghost"
        type="button"
        :disabled="!canProbe || probeInFlight"
        :data-testid="'custom-probe-btn'"
        @click="onProbe"
      >
        {{
          probeInFlight
            ? 'Probing…'
            : probeMatrix
              ? 'Re-probe'
              : 'Probe endpoint'
        }}
      </Button>
      <span v-if="!canProbe && baseURL.trim()" class="text-xs text-ink-dim">
        Enter a valid URL to enable probe.
      </span>
    </div>

    <!-- Probe error -->
    <p
      v-if="probeError"
      class="text-xs text-signal-danger"
      :data-testid="'custom-probe-error'"
    >
      {{ probeError }}
    </p>

    <!-- Capability matrix result -->
    <div
      v-if="probeMatrix"
      class="rounded-sm border border-border-muted bg-surface-1 px-3 py-2 text-xs"
      :data-testid="'custom-capability-matrix'"
    >
      <p class="mb-1.5 text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
        Capabilities
      </p>
      <ul class="space-y-1">
        <li class="flex justify-between">
          <span class="text-ink-muted">Streaming</span>
          <span :class="capClass(probeMatrix.streaming)" :data-testid="'cap-streaming'">
            {{ capLabel(probeMatrix.streaming) }}
          </span>
        </li>
        <li class="flex justify-between">
          <span class="text-ink-muted">Tool calling</span>
          <span :class="capClass(probeMatrix.tool_calling)" :data-testid="'cap-tool-calling'">
            {{ capLabel(probeMatrix.tool_calling) }}
          </span>
        </li>
        <li class="flex justify-between">
          <span class="text-ink-muted">Usage in stream</span>
          <span :class="capClass(probeMatrix.streaming_usage)" :data-testid="'cap-streaming-usage'">
            {{ capLabel(probeMatrix.streaming_usage) }}
          </span>
        </li>
      </ul>
    </div>
  </div>
</template>
