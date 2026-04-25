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

import { computed, reactive, ref, watch } from 'vue';
import type {
  AddProviderInput,
  ModelInfo,
  ProviderKind,
} from '@/lib/types';
import Button from '@/components/ui/Button.vue';
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

const KINDS: { id: ProviderKind; label: string }[] = [
  { id: 'anthropic', label: 'Anthropic' },
  { id: 'openai', label: 'OpenAI' },
  { id: 'openrouter', label: 'OpenRouter' },
  { id: 'bedrock', label: 'AWS Bedrock' },
  { id: 'ollama', label: 'Ollama (local)' },
];

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

interface FormState {
  kind: ProviderKind;
  apiKey: string;
  awsProfile: string;
  bedrockAuth: BedrockAuth;
  region: string;
  modelId: string;
  manualModelId: string;
  customId: string;
}

const form = reactive<FormState>({
  kind: 'anthropic',
  apiKey: '',
  awsProfile: 'default',
  bedrockAuth: 'api_key',
  region: 'us-east-1',
  modelId: '',
  manualModelId: '',
  customId: '',
});

// Pre-fill from props.editing when in edit mode. Runs once at setup.
if (props.editing) {
  form.kind = props.editing.kind;
  form.region = props.editing.region || 'us-east-1';
  form.manualModelId = props.editing.model;
  form.customId = props.editing.id;
  if (props.editing.kind === 'bedrock') {
    form.bedrockAuth =
      props.editing.credKind === 'aws_profile' ? 'aws_profile' : 'api_key';
    if (props.editing.credKind === 'aws_profile') {
      form.awsProfile = props.editing.credLocator || 'default';
    }
  }
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

const requiresRegion = computed(() => form.kind === 'bedrock');
const requiresApiKey = computed(
  () =>
    form.kind === 'anthropic' ||
    form.kind === 'openai' ||
    form.kind === 'openrouter' ||
    (form.kind === 'bedrock' && form.bedrockAuth === 'api_key'),
);
const requiresAwsProfile = computed(
  () => form.kind === 'bedrock' && form.bedrockAuth === 'aws_profile',
);
// Bedrock has no /models endpoint that mirrors the Connect flow we
// use for Anthropic, and the user already knows the model id from the
// AWS console. Skip the probe and go straight to manual entry. Edit
// mode also skips the probe — the user already has a working profile
// and we don't want to force a Connect just to tweak the model id.
const skipsProbe = computed(() => form.kind === 'bedrock' || isEditing.value);

// Auto-derived ID from kind + model. Hidden behind a "Customize" toggle
// so most users never have to think about it.
const customizeId = ref(false);
const derivedId = computed(() => {
  const m = effectiveModelId.value;
  if (!m) return '';
  return `${form.kind}-${m}`.toLowerCase().replace(/[^a-z0-9-_]/g, '-');
});
const effectiveId = computed(() =>
  customizeId.value && form.customId.trim()
    ? form.customId.trim()
    : derivedId.value,
);

const selectedModel = computed<ModelInfo | undefined>(() =>
  models.value.find((m) => m.id === form.modelId),
);
const effectiveModelId = computed(() =>
  fallbackToManual.value ? form.manualModelId.trim() : form.modelId,
);
const effectiveModelName = computed(() => {
  if (selectedModel.value?.displayName) return selectedModel.value.displayName;
  return effectiveModelId.value;
});

watch(
  () => [form.kind, form.apiKey],
  () => {
    probed.value = false;
    fallbackToManual.value = skipsProbe.value;
    models.value = [];
    form.modelId = '';
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
      form.modelId = models.value[0].id;
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
  if (!effectiveModelId.value)
    errors.model = skipsProbe.value
      ? 'Model id is required.'
      : probed.value
        ? 'Pick a model.'
        : 'Connect first to load available models.';
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
  const cred = requiresAwsProfile.value
    ? { kind: 'aws_profile' as const, locator: form.awsProfile.trim() }
    : { kind: 'keychain' as const, locator: `kaneaz-harness/${id}` };
  const input: AddProviderInput = {
    id,
    name: effectiveModelName.value,
    kind: form.kind,
    model: effectiveModelId.value,
    cred,
  };
  if (requiresRegion.value) input.region = form.region.trim();
  if (cred.kind === 'keychain' && form.apiKey.trim()) {
    input.plaintextApiKey = form.apiKey;
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

defineExpose({ form, validation, isValid });
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

    <!-- Step 2a: API key + Connect (most providers + bedrock-API-key mode) -->
    <div v-if="requiresApiKey">
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
              : form.kind === 'anthropic'
                ? 'sk-ant-…'
                : form.kind === 'bedrock'
                  ? 'ABSK… (Bedrock API key)'
                  : form.kind === 'openai'
                    ? 'sk-…'
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
    <div v-if="requiresAwsProfile">
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
    <div v-if="probed && !fallbackToManual">
      <label
        for="prov-model"
        class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
      >
        Model
      </label>
      <select
        id="prov-model"
        v-model="form.modelId"
        class="mt-1 w-full rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 text-sm text-ink focus:border-accent focus:outline-none"
        :data-testid="'add-provider-model'"
      >
        <option v-for="m in models" :key="m.id" :value="m.id">
          {{ m.displayName || m.id }}
        </option>
      </select>
      <p v-if="validation.model" class="mt-1 text-xs text-signal-danger">
        {{ validation.model }}
      </p>
    </div>

    <!-- Manual model entry — for kinds with no /models endpoint
         (bedrock today) and as a fallback when probing returns empty. -->
    <div v-if="fallbackToManual">
      <label
        for="prov-model-manual"
        class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
      >
        Model ID
      </label>
      <input
        id="prov-model-manual"
        v-model="form.manualModelId"
        class="mt-1 w-full rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 text-sm font-mono text-ink focus:border-accent focus:outline-none"
        autocomplete="off"
        :data-testid="'add-provider-model-manual'"
        :placeholder="
          form.kind === 'bedrock'
            ? 'meta.llama3-70b-instruct-v1:0'
            : form.kind === 'ollama'
              ? 'llama3.1:8b'
              : 'model-id'
        "
      />
      <p class="mt-1 text-[11px] text-ink-dim">
        This provider does not expose a model list — type the model id
        manually.
      </p>
      <p v-if="validation.model" class="mt-1 text-xs text-signal-danger">
        {{ validation.model }}
      </p>
    </div>

    <!-- Optional: customize the auto-derived ID -->
    <div v-if="effectiveModelId">
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
