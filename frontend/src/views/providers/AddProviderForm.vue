<script setup lang="ts">
/**
 * AddProviderForm — three-step flow:
 *   1. Pick provider Kind (dropdown).
 *   2. Paste API key, click Connect → backend probes the provider's
 *      /models endpoint and returns the available models.
 *   3. Pick a Model from the dropdown. ID + Name auto-populate from
 *      kind + model id (e.g. "anthropic-claude-sonnet-4-5").
 *
 * For kinds without a /models endpoint or adapter (returns []), the
 * UI falls back to a manual model-id text field so the user can still
 * configure unknown providers.
 *
 * The plaintext API key never lives in this component longer than the
 * Connect → Submit window. It is forwarded to the backend (which writes
 * it to the OS keychain and zeros the in-memory copy) and cleared from
 * local form state immediately on submit.
 */

import { computed, onMounted, reactive, ref, watch } from 'vue';
import type {
  AddProviderInput,
  GeminiEndpointKind,
  AppInfo,
  ModelInfo,
  ProviderKind,
  CustomAuthScheme,
  CustomCapabilityMatrix,
} from '@/lib/types';
import Button from '@/components/ui/Button.vue';
import CustomEndpointFields from './CustomEndpointFields.vue';
import type { CustomEndpointValue } from './CustomEndpointFields.vue';
import { useHarnessClient } from '@/lib/harnessClientContext';

const props = defineProps<{
  /**
   * When present, the form is in edit mode: kind + id are read-only,
   * fields are pre-filled from this provider's current state, and the
   * API key field becomes optional with a "leave blank to keep current"
   * placeholder.
   */
  editing?: {
    id: string;
    name: string;
    kind: ProviderKind;
    model: string;
    models?: string[];
    region?: string;
    credKind: 'keychain' | 'aws_profile' | 'env' | 'file';
    credLocator: string;
  } | null;
}>();
const emit = defineEmits<{
  (e: 'submit', input: AddProviderInput): void;
  (e: 'cancel'): void;
}>();

const client = useHarnessClient();
const isEditing = computed(() => !!props.editing);

const appInfo = ref<AppInfo | null>(null);

onMounted(async () => {
  try {
    appInfo.value = await client.appInfo();
  } catch {
    appInfo.value = null;
  }
});

const ALL_KINDS: { id: ProviderKind; label: string }[] = [
  { id: 'anthropic', label: 'Anthropic' },
  { id: 'openai', label: 'OpenAI' },
  { id: 'openrouter', label: 'OpenRouter' },
  { id: 'bedrock', label: 'AWS Bedrock' },
  { id: 'ollama', label: 'Ollama (local)' },
  { id: 'azure-openai', label: 'Azure OpenAI' },
  { id: 'gemini', label: 'Google Gemini' },
  { id: 'custom-openai', label: 'Custom OpenAI-compatible' },
];

// Exclude custom-openai when the feature flag is explicitly disabled.
// The flag defaults to enabled (undefined or true both mean "show it").
// OSS-first contract: never render a disabled button — only add when enabled.
//
// The fleet-hosted inference provider option was removed alongside the
// fleet-hosted-LLM surface (harness-fleet-sync-activation-01NSYNC01).
const KINDS = computed(() => {
  return ALL_KINDS.filter(
    (k) =>
      k.id !== 'custom-openai' || appInfo.value?.customOpenAIEnabled !== false,
  );
});

// AWS Bedrock regions where Bedrock is generally available. Source:
// https://docs.aws.amazon.com/bedrock/latest/userguide/bedrock-regions.html
// Sorted by US → EU → APAC → other so the picker reads naturally.
const BEDROCK_REGIONS: { id: string; label: string }[] = [
  { id: 'us-east-1', label: 'US East (N. Virginia) — us-east-1' },
  { id: 'us-east-2', label: 'US East (Ohio) — us-east-2' },
  { id: 'us-west-2', label: 'US West (Oregon) — us-west-2' },
  { id: 'ca-central-1', label: 'Canada (Central) — ca-central-1' },
  { id: 'sa-east-1', label: 'South America (São Paulo) — sa-east-1' },
  { id: 'eu-central-1', label: 'Europe (Frankfurt) — eu-central-1' },
  { id: 'eu-west-1', label: 'Europe (Ireland) — eu-west-1' },
  { id: 'eu-west-2', label: 'Europe (London) — eu-west-2' },
  { id: 'eu-west-3', label: 'Europe (Paris) — eu-west-3' },
  { id: 'eu-north-1', label: 'Europe (Stockholm) — eu-north-1' },
  { id: 'eu-south-1', label: 'Europe (Milan) — eu-south-1' },
  { id: 'ap-northeast-1', label: 'Asia Pacific (Tokyo) — ap-northeast-1' },
  { id: 'ap-northeast-2', label: 'Asia Pacific (Seoul) — ap-northeast-2' },
  { id: 'ap-northeast-3', label: 'Asia Pacific (Osaka) — ap-northeast-3' },
  { id: 'ap-south-1', label: 'Asia Pacific (Mumbai) — ap-south-1' },
  { id: 'ap-southeast-1', label: 'Asia Pacific (Singapore) — ap-southeast-1' },
  { id: 'ap-southeast-2', label: 'Asia Pacific (Sydney) — ap-southeast-2' },
];

type BedrockAuth = 'api_key' | 'aws_profile';
type GeminiVertexAuth = 'adc' | 'service_account_paste' | 'service_account_path';

interface FormState {
  kind: ProviderKind;
  apiKey: string;
  awsProfile: string;
  bedrockAuth: BedrockAuth;
  region: string;
  // Azure OpenAI resource hostname (e.g. myresource.openai.azure.com).
  // Stored in profile.Region for azure-openai kind.
  azureHost: string;
  // Set of model ids the user has ticked from the probe-populated
  // dropdown. The first id (in declared order from the probe) is the
  // default sent on the wire.
  selectedModelIds: string[];
  // Free-form fallback when the kind has no /models endpoint
  // (bedrock, ollama). One id per line OR comma-separated.
  manualModelIds: string;
  customId: string;
  // Gemini-specific fields
  geminiEndpointKind: GeminiEndpointKind;
  geminiVertexAuth: GeminiVertexAuth;
  geminiProject: string;
  geminiRegion: string;
  geminiSAPath: string;
  // custom-openai specific fields (WP06)
  customEndpoint: CustomEndpointValue;
}

const form = reactive<FormState>({
  kind: 'anthropic',
  apiKey: '',
  awsProfile: 'default',
  bedrockAuth: 'api_key',
  region: 'us-east-1',
  azureHost: '',
  selectedModelIds: [],
  manualModelIds: '',
  customId: '',
  geminiEndpointKind: 'ai_studio',
  geminiVertexAuth: 'adc',
  geminiProject: '',
  geminiRegion: 'us-central1',
  geminiSAPath: '',
  customEndpoint: {
    baseURL: '',
    model: '',
    authScheme: 'bearer',
    authHeader: '',
    apiKey: '',
    templateID: '',
  },
});

// Pre-fill from props.editing when in edit mode. Runs once at setup.
if (props.editing) {
  form.kind = props.editing.kind;
  form.region = props.editing.region || 'us-east-1';
  form.manualModelIds = (props.editing.models ?? [props.editing.model])
    .filter(Boolean)
    .join('\n');
  form.customId = props.editing.id;
  if (props.editing.kind === 'bedrock') {
    form.bedrockAuth =
      props.editing.credKind === 'aws_profile' ? 'aws_profile' : 'api_key';
    if (props.editing.credKind === 'aws_profile') {
      form.awsProfile = props.editing.credLocator || 'default';
    }
  }
  if (props.editing.kind === 'azure-openai') {
    // For azure-openai, the resource host is stored in profile.Region.
    form.azureHost = props.editing.region || '';
  }
  // custom-openai edit mode: the editing prop does not carry the
  // endpoint/auth fields directly (they live in the profile), so
  // the CustomEndpointFields component handles its own pre-fill
  // via the `initial` prop below.
}

const submitting = ref(false);
const probing = ref(false);
const probeError = ref<string | null>(null);
const models = ref<ModelInfo[]>([]);
// Tracks whether we have probed for this kind+key combination yet.
// Reset to false whenever kind or apiKey changes.
const probed = ref(false);
// True when probe ran and returned an empty list — UI falls back to
// manual model id entry for this kind.
const fallbackToManual = ref(false);
// custom-openai: last probed matrix (WP06)
const customProbeMatrix = ref<CustomCapabilityMatrix | null>(null);

const requiresRegion = computed(() => form.kind === 'bedrock');
const requiresAzureHost = computed(() => form.kind === 'azure-openai');
const requiresApiKey = computed(
  () =>
    form.kind === 'anthropic' ||
    form.kind === 'openai' ||
    form.kind === 'openrouter' ||
    form.kind === 'azure-openai' ||
    (form.kind === 'bedrock' && form.bedrockAuth === 'api_key') ||
    (form.kind === 'gemini' && form.geminiEndpointKind === 'ai_studio') ||
    (form.kind === 'gemini' && form.geminiVertexAuth === 'service_account_paste'),
);
const requiresAwsProfile = computed(
  () => form.kind === 'bedrock' && form.bedrockAuth === 'aws_profile',
);
// Gemini AI Studio probes via Connect (listModels uses API key).
// Gemini Vertex skips probe — no /models endpoint on the Vertex path.
const isGemini = computed(() => form.kind === 'gemini');
const isGeminiAIStudio = computed(() => isGemini.value && form.geminiEndpointKind === 'ai_studio');
const isGeminiVertex = computed(() => isGemini.value && form.geminiEndpointKind === 'vertex');
const geminiVertexUsesADC = computed(() => isGeminiVertex.value && form.geminiVertexAuth === 'adc');
const geminiVertexUsesSAPaste = computed(() => isGeminiVertex.value && form.geminiVertexAuth === 'service_account_paste');
const geminiVertexUsesSAPath = computed(() => isGeminiVertex.value && form.geminiVertexAuth === 'service_account_path');

// custom-openai uses its own CustomEndpointFields sub-component
const isCustomOpenAI = computed(() => form.kind === 'custom-openai');
// Bedrock has no /models endpoint that mirrors the Connect flow we
// use for Anthropic, and the user already knows the model id from the
// AWS console. Skip the probe and go straight to manual entry. Edit
// mode also skips the probe — the user already has a working profile
// and we don't want to force a Connect just to tweak the model id.
// Azure OpenAI's deployments are operator-configured, not discoverable
// via a simple /models list, so it also falls back to manual entry.
// Gemini Vertex skips probe — no /models endpoint on the Vertex path.
// custom-openai also skips this probe path (it has its own probe in CustomEndpointFields).
const skipsProbe = computed(
  () =>
    form.kind === 'bedrock' ||
    form.kind === 'azure-openai' ||
    form.kind === 'custom-openai' ||
    isGeminiVertex.value ||
    isEditing.value,
);

// Auto-derived ID from kind + model. Hidden behind a "Customize" toggle
// so most users never have to think about it.
const customizeId = ref(false);
const derivedId = computed(() => {
  // custom-openai: derive from template id + model
  if (form.kind === 'custom-openai') {
    const tmpl = form.customEndpoint.templateID;
    const model = form.customEndpoint.model.trim();
    if (tmpl && model) {
      return `custom-${tmpl}-${model}`.toLowerCase().replace(/[^a-z0-9-_]/g, '-');
    }
    if (model) {
      return `custom-${model}`.toLowerCase().replace(/[^a-z0-9-_]/g, '-');
    }
    return 'custom-openai';
  }
  // For multi-model rows, the auto-derived id uses the kind + count
  // (e.g. "anthropic-3-models") rather than naming any one model so
  // the row id is stable when the user adjusts the model set later.
  const ids = effectiveModelIds.value;
  if (ids.length === 0) return '';
  if (ids.length === 1) {
    return `${form.kind}-${ids[0]}`
      .toLowerCase()
      .replace(/[^a-z0-9-_]/g, '-');
  }
  return `${form.kind}-${ids.length}-models`.toLowerCase();
});
const effectiveId = computed(() =>
  customizeId.value && form.customId.trim()
    ? form.customId.trim()
    : derivedId.value,
);

// Resolved list of model ids the user has authorised. The first one
// is the primary (= profile.Model on the backend); the rest live in
// profile.Models for the chat-surface switcher to pick.
const effectiveModelIds = computed<string[]>(() => {
  // custom-openai: single model from the endpoint fields
  if (form.kind === 'custom-openai') {
    const m = form.customEndpoint.model.trim();
    return m ? [m] : [];
  }
  if (fallbackToManual.value) {
    return form.manualModelIds
      .split(/[\n,]/)
      .map((s) => s.trim())
      .filter(Boolean);
  }
  // Preserve the order of `models` so the primary is always the
  // top entry of the probe-returned list, not click order.
  const tickedSet = new Set(form.selectedModelIds);
  return models.value.map((m) => m.id).filter((id) => tickedSet.has(id));
});
const primaryModelId = computed(() => effectiveModelIds.value[0] ?? '');
const selectedModel = computed<ModelInfo | undefined>(() =>
  models.value.find((m) => m.id === primaryModelId.value),
);
const effectiveModelName = computed(() => {
  if (form.kind === 'custom-openai') {
    return form.customEndpoint.model.trim() || '';
  }
  if (selectedModel.value?.displayName) return selectedModel.value.displayName;
  return primaryModelId.value;
});

watch(
  () => [form.kind, form.apiKey],
  () => {
    probed.value = false;
    fallbackToManual.value = skipsProbe.value;
    models.value = [];
    form.selectedModelIds = [];
    probeError.value = null;
  },
  { immediate: true },
);

async function onProbe(): Promise<void> {
  if (probing.value) return;
  if (requiresApiKey.value && !form.apiKey.trim()) {
    probeError.value = 'API key is required.';
    return;
  }
  probing.value = true;
  probeError.value = null;
  try {
    const list = await client.llm.listModels(form.kind, form.apiKey);
    models.value = list ?? [];
    probed.value = true;
    if (models.value.length === 0) {
      fallbackToManual.value = true;
    } else {
      fallbackToManual.value = false;
      // Default to first model ticked. User can tick more.
      form.selectedModelIds = [models.value[0].id];
    }
  } catch (err) {
    probeError.value = err instanceof Error ? err.message : String(err);
    models.value = [];
    probed.value = true;
    fallbackToManual.value = true;
  } finally {
    probing.value = false;
  }
}

const validation = computed(() => {
  const errors: Record<string, string> = {};
  // In edit mode the API key is optional — empty means "keep the
  // existing keychain entry."
  if (
    requiresApiKey.value &&
    !isEditing.value &&
    !form.apiKey.trim()
  )
    errors.apiKey = 'API key is required.';
  if (requiresAwsProfile.value && !form.awsProfile.trim())
    errors.awsProfile = 'AWS profile name is required.';
  if (requiresRegion.value && !form.region.trim())
    errors.region = 'Region is required for Bedrock.';
  if (requiresAzureHost.value && !form.azureHost.trim())
    errors.azureHost = 'Resource hostname is required for Azure OpenAI.';
  // Gemini Vertex validation
  if (isGeminiVertex.value && !form.geminiProject.trim())
    errors.geminiProject = 'Google Cloud project ID is required for Vertex.';
  if (isGeminiVertex.value && !form.geminiRegion.trim())
    errors.geminiRegion = 'Region is required for Vertex.';
  if (geminiVertexUsesSAPaste.value && !isEditing.value && !form.apiKey.trim())
    errors.apiKey = 'Service account JSON is required.';
  if (geminiVertexUsesSAPath.value && !form.geminiSAPath.trim())
    errors.geminiSAPath = 'Service account file path is required.';
  if (effectiveModelIds.value.length === 0)
    errors.model = skipsProbe.value
      ? 'At least one model id is required.'
      : probed.value
        ? 'Pick at least one model.'
        : 'Connect first to load available models.';

  if (form.kind === 'custom-openai') {
    if (!form.customEndpoint.baseURL.trim())
      errors.baseURL = 'Base URL is required.';
    if (!form.customEndpoint.model.trim())
      errors.model = 'Model ID is required.';
    if (
      !isEditing.value &&
      form.customEndpoint.authScheme !== 'none' &&
      !form.customEndpoint.apiKey.trim()
    )
      errors.apiKey = 'API key is required for this auth scheme.';
  } else {
    // In edit mode the API key is optional — empty means "keep the
    // existing keychain entry."
    if (
      requiresApiKey.value &&
      !isEditing.value &&
      !form.apiKey.trim()
    )
      errors.apiKey = 'API key is required.';
    if (requiresAwsProfile.value && !form.awsProfile.trim())
      errors.awsProfile = 'AWS profile name is required.';
    if (requiresRegion.value && !form.region.trim())
      errors.region = 'Region is required for Bedrock.';
    if (effectiveModelIds.value.length === 0)
      errors.model = skipsProbe.value
        ? 'At least one model id is required.'
        : probed.value
          ? 'Pick at least one model.'
          : 'Connect first to load available models.';
  }
  if (customizeId.value && !form.customId.trim())
    errors.customId = 'Custom ID is required when "Customize ID" is on.';
  return errors;
});

const isValid = computed(() => Object.keys(validation.value).length === 0);

function onSubmit(): void {
  if (!isValid.value || submitting.value) return;
  submitting.value = true;
  // Edit mode preserves the original id so the existing personal-store
  // row + keychain entry locator stay aligned.
  const id = isEditing.value && props.editing ? props.editing.id : effectiveId.value;

  // Determine credential shape.
  // custom-openai no-auth endpoints get a kind="keychain" cred with an empty
  // locator that the personal store ignores (auth_scheme:none path).
  const isNoneAuth =
    form.kind === 'custom-openai' && form.customEndpoint.authScheme === 'none';
  let cred: AddProviderInput['cred'];
  if (requiresAwsProfile.value) {
    cred = { kind: 'aws_profile' as const, locator: form.awsProfile.trim() };
  } else if (geminiVertexUsesSAPath.value) {
    cred = { kind: 'file' as const, locator: form.geminiSAPath.trim() };
  } else if (geminiVertexUsesADC.value) {
    cred = { kind: 'env' as const, locator: 'GOOGLE_APPLICATION_CREDENTIALS' };
  } else if (isNoneAuth) {
    cred = { kind: 'keychain' as const, locator: '' };
  } else {
    cred = { kind: 'keychain' as const, locator: `kaneaz-harness/${id}` };
  }

  const input: AddProviderInput = {
    id,
    name: effectiveModelName.value || id,
    kind: form.kind,
    model: primaryModelId.value,
    models: effectiveModelIds.value,
    cred,
  };
  if (requiresRegion.value) input.region = form.region.trim();
  // For azure-openai, the resource hostname is stored as profile.Region.
  if (requiresAzureHost.value) input.region = form.azureHost.trim();
  if (isGemini.value) {
    input.geminiEndpointKind = form.geminiEndpointKind;
    if (isGeminiVertex.value) {
      input.project = form.geminiProject.trim();
      input.region = form.geminiRegion.trim();
    }
  }
  if (cred.kind === 'keychain' && cred.locator && form.apiKey.trim()) {
    input.plaintextApiKey = form.apiKey;
  }
  // custom-openai: forward the plaintext API key when auth requires one
  if (form.kind === 'custom-openai' && !isNoneAuth && form.customEndpoint.apiKey) {
    input.plaintextApiKey = form.customEndpoint.apiKey;
    form.customEndpoint.apiKey = '';
  }
  // Drop our local copy of the plaintext immediately so we don't
  // accidentally retain it across re-renders.
  form.apiKey = '';
  emit('submit', input);
  submitting.value = false;
}

function onCancel(): void {
  emit('cancel');
}

defineExpose({ form, validation, isValid, customProbeMatrix });
</script>

<template>
  <form
    class="space-y-4 px-6 py-4 font-ui"
    :data-testid="'add-provider-form'"
    @submit.prevent="onSubmit"
  >
    <!-- Step 1: Kind -->
    <div>
      <label
        for="prov-kind"
        class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
      >
        Provider
      </label>
      <select
        id="prov-kind"
        v-model="form.kind"
        class="mt-1 w-full rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 text-sm text-ink focus:border-accent focus:outline-none disabled:opacity-60 disabled:cursor-not-allowed"
        :disabled="isEditing"
        :data-testid="'add-provider-kind'"
      >
        <option v-for="k in KINDS" :key="k.id" :value="k.id">
          {{ k.label }}
        </option>
      </select>
    </div>

    <!-- custom-openai: delegate to CustomEndpointFields sub-component -->
    <CustomEndpointFields
      v-if="isCustomOpenAI"
      :editing="isEditing"
      :data-testid="'custom-endpoint-fields'"
      @update:value="(v) => { form.customEndpoint = v; }"
      @probed="(m) => { customProbeMatrix = m; }"
    />

    <!-- Validation errors for custom-openai -->
    <p
      v-if="isCustomOpenAI && validation.baseURL"
      class="text-xs text-signal-danger"
    >
      {{ validation.baseURL }}
    </p>
    <p
      v-if="isCustomOpenAI && (validation.model || validation.apiKey)"
      class="text-xs text-signal-danger"
    >
      {{ validation.model || validation.apiKey }}
    </p>

    <!-- Bedrock-only: auth method toggle -->
    <div v-if="form.kind === 'bedrock'">
      <span class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
        Authentication
      </span>
      <div class="mt-1 flex gap-1 rounded-sm border border-border-muted bg-surface-1 p-0.5">
        <button
          type="button"
          class="flex-1 px-2 py-1 rounded-sm text-xs font-ui transition-fast"
          :class="
            form.bedrockAuth === 'api_key'
              ? 'bg-surface-2 text-ink ring-1 ring-accent-hairline'
              : 'text-ink-muted hover:text-ink'
          "
          :data-testid="'add-provider-bedrock-auth-key'"
          @click="form.bedrockAuth = 'api_key'"
        >
          API key
        </button>
        <button
          type="button"
          class="flex-1 px-2 py-1 rounded-sm text-xs font-ui transition-fast"
          :class="
            form.bedrockAuth === 'aws_profile'
              ? 'bg-surface-2 text-ink ring-1 ring-accent-hairline'
              : 'text-ink-muted hover:text-ink'
          "
          :data-testid="'add-provider-bedrock-auth-profile'"
          @click="form.bedrockAuth = 'aws_profile'"
        >
          AWS profile
        </button>
      </div>
      <p class="mt-1 text-[11px] text-ink-dim">
        {{
          form.bedrockAuth === 'api_key'
            ? 'Long-lived Bedrock API key from the AWS console — paste it below.'
            : 'Reads from ~/.aws/credentials. The harness never sees your access keys.'
        }}
      </p>
    </div>

    <!-- Azure OpenAI: resource hostname field -->
    <div v-if="requiresAzureHost">
      <label
        for="prov-azure-host"
        class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
      >
        Resource Hostname
      </label>
      <input
        id="prov-azure-host"
        v-model="form.azureHost"
        class="mt-1 w-full rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 text-sm font-mono text-ink focus:border-accent focus:outline-none"
        autocomplete="off"
        :data-testid="'add-provider-azure-host'"
        placeholder="myresource.openai.azure.com"
      />
      <p v-if="validation.azureHost" class="mt-1 text-xs text-signal-danger">
        {{ validation.azureHost }}
      </p>
      <p class="mt-1 text-[11px] text-ink-dim">
        The hostname of your Azure OpenAI resource (without "https://"). Found in
        the Azure portal under your resource's "Keys and Endpoint" page.
      </p>
    </div>

    <!-- Gemini-only: endpoint kind radio -->
    <div v-if="isGemini" :data-testid="'add-provider-gemini-endpoint'">
      <span class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
        Endpoint
      </span>
      <div class="mt-1 flex gap-1 rounded-sm border border-border-muted bg-surface-1 p-0.5">
        <button
          type="button"
          class="flex-1 px-2 py-1 rounded-sm text-xs font-ui transition-fast"
          :class="form.geminiEndpointKind === 'ai_studio' ? 'bg-surface-2 text-ink ring-1 ring-accent-hairline' : 'text-ink-muted hover:text-ink'"
          :data-testid="'add-provider-gemini-ai-studio'"
          @click="form.geminiEndpointKind = 'ai_studio'"
        >
          AI Studio
        </button>
        <button
          type="button"
          class="flex-1 px-2 py-1 rounded-sm text-xs font-ui transition-fast"
          :class="form.geminiEndpointKind === 'vertex' ? 'bg-surface-2 text-ink ring-1 ring-accent-hairline' : 'text-ink-muted hover:text-ink'"
          :data-testid="'add-provider-gemini-vertex'"
          @click="form.geminiEndpointKind = 'vertex'"
        >
          Vertex AI
        </button>
      </div>
    </div>

    <!-- Gemini Vertex: project + region + auth -->
    <div v-if="isGeminiVertex" class="space-y-3" :data-testid="'add-provider-gemini-vertex-fields'">
      <!-- Project ID -->
      <div>
        <label class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          GCP Project ID
        </label>
        <input
          v-model="form.geminiProject"
          type="text"
          class="mt-1 w-full rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 text-sm text-ink focus:border-accent focus:outline-none"
          placeholder="my-gcp-project"
          :data-testid="'add-provider-gemini-project'"
        />
        <p v-if="validation.geminiProject" class="mt-1 text-xs text-signal-danger">
          {{ validation.geminiProject }}
        </p>
      </div>
      <!-- Region -->
      <div>
        <label class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          Region
        </label>
        <input
          v-model="form.geminiRegion"
          type="text"
          class="mt-1 w-full rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 text-sm text-ink focus:border-accent focus:outline-none"
          placeholder="us-central1"
          :data-testid="'add-provider-gemini-region'"
        />
      </div>
      <!-- Auth method -->
      <div>
        <span class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          Auth method
        </span>
        <div class="mt-1 flex gap-1 rounded-sm border border-border-muted bg-surface-1 p-0.5">
          <button type="button" class="flex-1 px-2 py-1 rounded-sm text-xs font-ui transition-fast"
            :class="form.geminiVertexAuth === 'adc' ? 'bg-surface-2 text-ink ring-1 ring-accent-hairline' : 'text-ink-muted hover:text-ink'"
            :data-testid="'add-provider-gemini-adc'"
            @click="form.geminiVertexAuth = 'adc'">ADC</button>
          <button type="button" class="flex-1 px-2 py-1 rounded-sm text-xs font-ui transition-fast"
            :class="form.geminiVertexAuth === 'service_account_paste' ? 'bg-surface-2 text-ink ring-1 ring-accent-hairline' : 'text-ink-muted hover:text-ink'"
            :data-testid="'add-provider-gemini-sa-paste'"
            @click="form.geminiVertexAuth = 'service_account_paste'">Service account (paste)</button>
          <button type="button" class="flex-1 px-2 py-1 rounded-sm text-xs font-ui transition-fast"
            :class="form.geminiVertexAuth === 'service_account_path' ? 'bg-surface-2 text-ink ring-1 ring-accent-hairline' : 'text-ink-muted hover:text-ink'"
            :data-testid="'add-provider-gemini-sa-path'"
            @click="form.geminiVertexAuth = 'service_account_path'">Service account (path)</button>
        </div>
        <p v-if="geminiVertexUsesADC" class="mt-1 text-[11px] text-ink-dim" :data-testid="'add-provider-gemini-adc-hint'">
          Application Default Credentials will be used. Run <code>gcloud auth application-default login</code> first.
        </p>
        <div v-if="geminiVertexUsesSAPath" class="mt-2">
          <label class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
            Service account JSON path
          </label>
          <input
            v-model="form.geminiSAPath"
            type="text"
            class="mt-1 w-full rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 text-sm font-mono text-ink focus:border-accent focus:outline-none"
            placeholder="/path/to/service-account.json"
            :data-testid="'add-provider-gemini-sa-path-input'"
          />
          <p v-if="validation.geminiSAPath" class="mt-1 text-xs text-signal-danger">
            {{ validation.geminiSAPath }}
          </p>
        </div>
      </div>
    </div>

    <!-- Step 2a: API key + Connect (most providers + bedrock-API-key mode) -->
    <!-- Hidden for custom-openai — handled by CustomEndpointFields -->
    <div v-if="requiresApiKey && !isCustomOpenAI">
      <label
        for="prov-apikey"
        class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
      >
        API Key
      </label>
      <div class="mt-1 flex items-stretch gap-2">
        <input
          id="prov-apikey"
          v-model="form.apiKey"
          type="password"
          class="flex-1 rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 text-sm font-mono text-ink focus:border-accent focus:outline-none"
          autocomplete="off"
          :data-testid="'add-provider-apikey'"
          :placeholder="
            isEditing
              ? 'leave blank to keep current key'
              : form.kind === 'azure-openai'
                ? 'paste Azure OpenAI api-key'
                : form.kind === 'anthropic'
                  ? 'sk-ant-…'
                : form.kind === 'bedrock'
                  ? 'ABSK… (Bedrock API key)'
                  : form.kind === 'openai'
                    ? 'sk-…'
                    : form.kind === 'gemini' && geminiVertexUsesSAPaste
                      ? 'paste service-account JSON (starts with { type: service_account, … })'
                      : form.kind === 'gemini'
                        ? 'AIza… (Google AI Studio key)'
                        : 'paste key'
          "
        />
        <Button
          variant="ghost"
          type="button"
          :disabled="probing || !form.apiKey.trim()"
          :data-testid="'add-provider-connect'"
          @click="onProbe"
        >
          {{ probing ? 'Connecting…' : probed ? 'Reconnect' : 'Connect' }}
        </Button>
      </div>
      <p
        v-if="probeError"
        class="mt-1 text-xs text-signal-danger"
        :data-testid="'add-provider-probe-error'"
      >
        {{ probeError }}
      </p>
      <p v-else-if="validation.apiKey" class="mt-1 text-xs text-signal-danger">
        {{ validation.apiKey }}
      </p>
      <p class="mt-1 text-[11px] text-ink-dim">
        Stored in your OS keychain. Never written to providers.json.
      </p>
    </div>

    <!-- Step 2b: AWS profile (bedrock) -->
    <div v-if="requiresAwsProfile && !isCustomOpenAI">
      <label
        for="prov-aws-profile"
        class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
      >
        AWS Profile
      </label>
      <input
        id="prov-aws-profile"
        v-model="form.awsProfile"
        class="mt-1 w-full rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 text-sm font-mono text-ink focus:border-accent focus:outline-none"
        autocomplete="off"
        :data-testid="'add-provider-aws-profile'"
        placeholder="default"
      />
      <p
        v-if="validation.awsProfile"
        class="mt-1 text-xs text-signal-danger"
      >
        {{ validation.awsProfile }}
      </p>
      <p class="mt-1 text-[11px] text-ink-dim">
        Reads from <span class="font-mono">~/.aws/credentials</span>. The
        harness never sees your access keys.
      </p>
    </div>

    <!-- Step 3: Region (bedrock only) -->
    <div v-if="requiresRegion">
      <label
        for="prov-region"
        class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
      >
        Region
      </label>
      <select
        id="prov-region"
        v-model="form.region"
        class="mt-1 w-full rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 text-sm text-ink focus:border-accent focus:outline-none"
        :data-testid="'add-provider-region'"
      >
        <option v-for="r in BEDROCK_REGIONS" :key="r.id" :value="r.id">
          {{ r.label }}
        </option>
      </select>
      <p v-if="validation.region" class="mt-1 text-xs text-signal-danger">
        {{ validation.region }}
      </p>
    </div>

    <!-- Step 4: Model picker — populated by Connect -->
    <!-- Hidden for custom-openai — model is entered in CustomEndpointFields -->
    <div v-if="probed && !fallbackToManual && !isCustomOpenAI">
      <div class="flex items-center justify-between">
        <span class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          Models ({{ form.selectedModelIds.length }} selected)
        </span>
        <div class="flex gap-2 text-[11px]">
          <button
            type="button"
            class="text-accent hover:text-ink"
            @click="form.selectedModelIds = models.map((m) => m.id)"
          >
            Select all
          </button>
          <button
            type="button"
            class="text-ink-dim hover:text-ink"
            @click="form.selectedModelIds = []"
          >
            Clear
          </button>
        </div>
      </div>
      <div
        class="mt-1 max-h-56 overflow-y-auto rounded-sm border border-border-muted bg-surface-1"
        :data-testid="'add-provider-model-list'"
      >
        <label
          v-for="m in models"
          :key="m.id"
          class="flex items-center gap-2 px-2.5 py-1.5 text-sm font-ui text-ink hover:bg-surface-2 cursor-pointer"
        >
          <input
            type="checkbox"
            :value="m.id"
            v-model="form.selectedModelIds"
            class="h-3.5 w-3.5"
          />
          <span class="truncate">{{ m.displayName || m.id }}</span>
        </label>
      </div>
      <p v-if="validation.model" class="mt-1 text-xs text-signal-danger">
        {{ validation.model }}
      </p>
    </div>

    <!-- Manual model entry — for kinds with no /models endpoint
         (bedrock today) and as a fallback when probing returns empty.
         Multi-line: one model id per line. -->
    <!-- Hidden for custom-openai — model field is in CustomEndpointFields -->
    <div v-if="fallbackToManual && !isCustomOpenAI">
      <label
        for="prov-model-manual"
        class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
      >
        Model IDs
      </label>
      <textarea
        id="prov-model-manual"
        v-model="form.manualModelIds"
        rows="4"
        class="mt-1 w-full rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 text-sm font-mono text-ink focus:border-accent focus:outline-none"
        autocomplete="off"
        :data-testid="'add-provider-model-manual'"
        :placeholder="
          form.kind === 'bedrock'
            ? 'us.meta.llama4-maverick-17b-instruct-v1:0\nus.anthropic.claude-3-5-sonnet-20241022-v2:0'
            : form.kind === 'ollama'
              ? 'llama3.1:8b\nqwen2.5:7b'
              : 'model-id-1\nmodel-id-2'
        "
      />
      <p class="mt-1 text-[11px] text-ink-dim">
        One model id per line (or comma-separated). The first id is the
        default; chat sessions can switch to any of the others.
      </p>
      <p v-if="validation.model" class="mt-1 text-xs text-signal-danger">
        {{ validation.model }}
      </p>
    </div>

    <!-- Optional: customize the auto-derived ID -->
    <div v-if="effectiveModelIds.length > 0">
      <div class="flex items-center justify-between">
        <span class="text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          Provider ID
        </span>
        <button
          type="button"
          class="text-[11px] text-accent hover:text-ink"
          :data-testid="'add-provider-customize-id'"
          @click="customizeId = !customizeId"
        >
          {{ customizeId ? 'Use auto' : 'Customize' }}
        </button>
      </div>
      <input
        v-if="customizeId"
        v-model="form.customId"
        class="mt-1 w-full rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 text-sm font-mono text-ink focus:border-accent focus:outline-none"
        autocomplete="off"
        :data-testid="'add-provider-id'"
        :placeholder="derivedId"
      />
      <p
        v-else
        class="mt-1 font-mono text-xs text-ink-subtle"
        :data-testid="'add-provider-id-derived'"
      >
        {{ derivedId }}
      </p>
      <p
        v-if="validation.customId"
        class="mt-1 text-xs text-signal-danger"
      >
        {{ validation.customId }}
      </p>
    </div>

    <div class="flex items-center justify-end gap-2 pt-2">
      <Button variant="ghost" type="button" @click="onCancel">Cancel</Button>
      <Button
        variant="accent"
        type="submit"
        :disabled="!isValid || submitting"
      >
        {{ isEditing ? 'Save changes' : 'Add provider' }}
      </Button>
    </div>
  </form>
</template>
