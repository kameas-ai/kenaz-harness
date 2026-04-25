<script setup lang="ts">
/**
 * AddProviderForm — form component for the Add Provider drawer.
 *
 * Validates the user input client-side before delegating to the parent
 * via `submit`. Validation rules mirror the Go-side ValidateProfile so a
 * user never round-trips through the backend just to learn the form is
 * incomplete.
 *
 * The plaintext API key never leaves this component as state once the
 * form submits — the parent forwards it to the backend, which writes it
 * to the OS keychain and zeroes its in-memory copy.
 */

import { computed, reactive, ref } from 'vue';
import type { AddProviderInput, ProviderKind } from '@/lib/types';
import Button from '@/components/ui/Button.vue';

const emit = defineEmits<{
  (e: 'submit', input: AddProviderInput): void;
  (e: 'cancel'): void;
}>();

const KINDS: ProviderKind[] = [
  'anthropic',
  'openai',
  'openrouter',
  'bedrock',
  'ollama',
];

interface FormState {
  id: string;
  name: string;
  kind: ProviderKind;
  model: string;
  region: string;
  apiKey: string;
}

const form = reactive<FormState>({
  id: '',
  name: '',
  kind: 'anthropic',
  model: '',
  region: '',
  apiKey: '',
});

const submitting = ref(false);

const requiresRegion = computed(() => form.kind === 'bedrock');
const requiresApiKey = computed(() => form.kind !== 'ollama');

const validation = computed(() => {
  const errors: Record<string, string> = {};
  if (!form.id.trim()) errors.id = 'ID is required.';
  else if (!/^[a-z0-9][a-z0-9-_]{1,63}$/i.test(form.id.trim()))
    errors.id = 'ID must be 2-64 chars: letters, digits, dash, underscore.';
  if (!form.name.trim()) errors.name = 'Name is required.';
  if (!form.model.trim()) errors.model = 'Model is required.';
  if (requiresRegion.value && !form.region.trim())
    errors.region = 'Region is required for Bedrock.';
  if (requiresApiKey.value && !form.apiKey.trim())
    errors.apiKey = 'API key is required.';
  return errors;
});

const isValid = computed(() => Object.keys(validation.value).length === 0);

function onSubmit(): void {
  if (!isValid.value || submitting.value) return;
  submitting.value = true;
  const id = form.id.trim();
  const input: AddProviderInput = {
    id,
    name: form.name.trim(),
    kind: form.kind,
    model: form.model.trim(),
    cred: {
      kind: 'keychain',
      locator: `kaneaz-harness/${id}`,
    },
  };
  if (requiresRegion.value) input.region = form.region.trim();
  if (form.apiKey.trim()) input.plaintextApiKey = form.apiKey;
  // Drop our local copy of the plaintext immediately. The parent
  // component forwards the input synchronously to the backend.
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
    <div>
      <label
        for="prov-id"
        class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
      >
        ID
      </label>
      <input
        id="prov-id"
        v-model="form.id"
        class="mt-1 w-full rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 text-sm text-ink focus:border-accent focus:outline-none"
        autocomplete="off"
        :data-testid="'add-provider-id'"
      />
      <p
        v-if="validation.id"
        class="mt-1 text-xs text-signal-danger"
        :data-testid="'add-provider-id-error'"
      >
        {{ validation.id }}
      </p>
    </div>

    <div>
      <label
        for="prov-name"
        class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
      >
        Name
      </label>
      <input
        id="prov-name"
        v-model="form.name"
        class="mt-1 w-full rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 text-sm text-ink focus:border-accent focus:outline-none"
        autocomplete="off"
        :data-testid="'add-provider-name'"
      />
      <p v-if="validation.name" class="mt-1 text-xs text-signal-danger">
        {{ validation.name }}
      </p>
    </div>

    <div>
      <label
        for="prov-kind"
        class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
      >
        Kind
      </label>
      <select
        id="prov-kind"
        v-model="form.kind"
        class="mt-1 w-full rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 text-sm text-ink focus:border-accent focus:outline-none"
        :data-testid="'add-provider-kind'"
      >
        <option v-for="k in KINDS" :key="k" :value="k">{{ k }}</option>
      </select>
    </div>

    <div>
      <label
        for="prov-model"
        class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
      >
        Model
      </label>
      <input
        id="prov-model"
        v-model="form.model"
        class="mt-1 w-full rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 text-sm text-ink focus:border-accent focus:outline-none"
        autocomplete="off"
        :data-testid="'add-provider-model'"
      />
      <p v-if="validation.model" class="mt-1 text-xs text-signal-danger">
        {{ validation.model }}
      </p>
    </div>

    <div v-if="requiresRegion">
      <label
        for="prov-region"
        class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
      >
        Region
      </label>
      <input
        id="prov-region"
        v-model="form.region"
        class="mt-1 w-full rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 text-sm text-ink focus:border-accent focus:outline-none"
        autocomplete="off"
        :data-testid="'add-provider-region'"
      />
      <p v-if="validation.region" class="mt-1 text-xs text-signal-danger">
        {{ validation.region }}
      </p>
    </div>

    <div v-if="requiresApiKey">
      <label
        for="prov-apikey"
        class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
      >
        API Key
      </label>
      <input
        id="prov-apikey"
        v-model="form.apiKey"
        type="password"
        class="mt-1 w-full rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 text-sm font-mono text-ink focus:border-accent focus:outline-none"
        autocomplete="off"
        :data-testid="'add-provider-apikey'"
      />
      <p
        v-if="validation.apiKey"
        class="mt-1 text-xs text-signal-danger"
      >
        {{ validation.apiKey }}
      </p>
      <p class="mt-1 text-[11px] text-ink-dim">
        Stored in your OS keychain. Never written to providers.json.
      </p>
    </div>

    <div class="flex items-center justify-end gap-2 pt-2">
      <Button variant="ghost" type="button" @click="onCancel">Cancel</Button>
      <Button
        variant="accent"
        type="submit"
        :disabled="!isValid || submitting"
      >
        Add provider
      </Button>
    </div>
  </form>
</template>
