<script setup lang="ts">
/**
 * ModelAccessibleSecretsPanel — Settings panel for model-accessible secrets.
 * Mission: model-secret-references-01KW7M5A WP10.
 *
 * The model uses @secret:<locator> reference tokens in tool arguments
 * (web_fetch headers, bash commands, etc.). The harness resolves them
 * at dispatch time — plaintext never enters the conversation context.
 *
 * This panel lets the user:
 *   - View which secrets are currently exposed to the model
 *   - Expose a new secret (locator + description + kind + plaintext)
 *   - Revoke an exposed secret
 *
 * IMPORTANT: The `plaintext` field in the expose form is zeroed on the
 * Go side before this method returns. Do NOT display the entered value
 * in any reactive state beyond the input field (FR-005a).
 */

import { ref, onMounted } from 'vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import type { ModelSecretRow } from '@/lib/types';

const client = useHarnessClient();

const rows = ref<ModelSecretRow[]>([]);
const loading = ref(false);
const loadError = ref<string | null>(null);

const showExposeForm = ref(false);
const exposeLocator = ref('');
const exposeDescription = ref('');
const exposeKind = ref('bearer');
const exposePlaintext = ref('');
const exposing = ref(false);
const exposeError = ref<string | null>(null);

const revoking = ref<Record<string, boolean>>({});
const revokeError = ref<string | null>(null);

async function load() {
  loading.value = true;
  loadError.value = null;
  try {
    rows.value = await client.secrets.list();
  } catch (err) {
    loadError.value = err instanceof Error ? err.message : String(err);
  } finally {
    loading.value = false;
  }
}

async function expose() {
  if (!exposeLocator.value.trim()) {
    exposeError.value = 'Locator is required.';
    return;
  }
  if (!exposePlaintext.value) {
    exposeError.value = 'Plaintext value is required.';
    return;
  }
  exposing.value = true;
  exposeError.value = null;
  try {
    await client.secrets.expose({
      locator: exposeLocator.value.trim(),
      description: exposeDescription.value.trim(),
      kind: exposeKind.value,
      plaintext: exposePlaintext.value,
    });
    // Clear form — especially plaintext (FR-005a: no plaintext in state).
    exposeLocator.value = '';
    exposeDescription.value = '';
    exposeKind.value = 'bearer';
    exposePlaintext.value = '';
    showExposeForm.value = false;
    await load();
  } catch (err) {
    exposeError.value = err instanceof Error ? err.message : String(err);
  } finally {
    exposing.value = false;
  }
}

async function revoke(locator: string) {
  if (!window.confirm(`Revoke @secret:${locator}? The model will no longer be able to use this reference.`)) {
    return;
  }
  revoking.value = { ...revoking.value, [locator]: true };
  revokeError.value = null;
  try {
    await client.secrets.revoke(locator);
    await load();
  } catch (err) {
    revokeError.value = err instanceof Error ? err.message : String(err);
  } finally {
    const updated = { ...revoking.value };
    delete updated[locator];
    revoking.value = updated;
  }
}

function cancelExpose() {
  showExposeForm.value = false;
  exposeError.value = null;
  exposeLocator.value = '';
  exposeDescription.value = '';
  exposeKind.value = 'bearer';
  exposePlaintext.value = '';
}

onMounted(() => {
  load();
});
</script>

<template>
  <section data-testid="model-accessible-secrets-panel">
    <!-- Header -->
    <div class="flex items-center justify-between">
      <div>
        <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          Model-accessible secrets
        </h2>
        <p class="mt-1 font-ui text-[11px] text-ink-dim">
          Secrets the model can reference using <code class="font-mono">@secret:&lt;locator&gt;</code> tokens in tool calls.
          Plaintext never enters the conversation context.
        </p>
      </div>
      <button
        type="button"
        class="rounded-sm border border-border px-2 py-1 font-ui text-[11px] uppercase tracking-[0.14em] text-ink hover:bg-surface-2 disabled:opacity-50 transition-colors"
        :disabled="showExposeForm"
        data-testid="secrets-expose-btn"
        @click="showExposeForm = true"
      >
        + Expose secret
      </button>
    </div>

    <!-- Expose form -->
    <div
      v-if="showExposeForm"
      class="mt-3 rounded-sm border border-border bg-surface-1 p-4 grid gap-3"
      data-testid="secrets-expose-form"
    >
      <h3 class="font-ui text-[11px] uppercase tracking-[0.14em] text-ink-subtle">
        Expose a secret to the model
      </h3>

      <div class="grid gap-1">
        <label class="font-ui text-[11px] text-ink-muted" for="expose-locator">
          Locator <span class="text-signal-danger">*</span>
        </label>
        <input
          id="expose-locator"
          v-model="exposeLocator"
          type="text"
          placeholder="user:my-api-key"
          class="rounded-sm border border-border bg-surface-0 px-2 py-1 font-mono text-[12px] text-ink focus:outline-none focus:ring-1 focus:ring-accent"
          data-testid="expose-locator-input"
          :disabled="exposing"
        />
        <p class="font-ui text-[10px] text-ink-dim">
          The identifier used in <code class="font-mono">@secret:&lt;locator&gt;</code>. Example: <code class="font-mono">user:github-pat</code>
        </p>
      </div>

      <div class="grid gap-1">
        <label class="font-ui text-[11px] text-ink-muted" for="expose-description">
          Description
        </label>
        <input
          id="expose-description"
          v-model="exposeDescription"
          type="text"
          placeholder="GitHub personal access token"
          class="rounded-sm border border-border bg-surface-0 px-2 py-1 font-ui text-[12px] text-ink focus:outline-none focus:ring-1 focus:ring-accent"
          data-testid="expose-description-input"
          :disabled="exposing"
        />
      </div>

      <div class="grid gap-1">
        <label class="font-ui text-[11px] text-ink-muted" for="expose-kind">
          Kind
        </label>
        <select
          id="expose-kind"
          v-model="exposeKind"
          class="rounded-sm border border-border bg-surface-0 px-2 py-1 font-ui text-[12px] text-ink focus:outline-none focus:ring-1 focus:ring-accent"
          data-testid="expose-kind-select"
          :disabled="exposing"
        >
          <option value="bearer">Bearer token</option>
          <option value="basic">Basic auth</option>
          <option value="header">Custom header</option>
          <option value="raw">Raw value</option>
          <option value="aws_credential">AWS credential</option>
        </select>
      </div>

      <div class="grid gap-1">
        <label class="font-ui text-[11px] text-ink-muted" for="expose-plaintext">
          Secret value <span class="text-signal-danger">*</span>
        </label>
        <input
          id="expose-plaintext"
          v-model="exposePlaintext"
          type="password"
          placeholder="Paste your secret here"
          class="rounded-sm border border-border bg-surface-0 px-2 py-1 font-mono text-[12px] text-ink focus:outline-none focus:ring-1 focus:ring-accent"
          data-testid="expose-plaintext-input"
          :disabled="exposing"
          autocomplete="off"
        />
        <p class="font-ui text-[10px] text-ink-dim">
          Zeroed immediately after being stored. Never logged or returned to the frontend.
        </p>
      </div>

      <div
        v-if="exposeError"
        class="rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[11px] text-signal-danger"
        role="alert"
        data-testid="expose-error"
      >
        {{ exposeError }}
      </div>

      <div class="flex gap-2 justify-end">
        <button
          type="button"
          class="rounded-sm border border-border px-3 py-1 font-ui text-[11px] text-ink-muted hover:text-ink transition-colors"
          :disabled="exposing"
          @click="cancelExpose"
        >
          Cancel
        </button>
        <button
          type="button"
          class="rounded-sm bg-accent px-3 py-1 font-ui text-[11px] text-white hover:bg-accent-hover disabled:opacity-50 transition-colors"
          :disabled="exposing"
          data-testid="expose-submit-btn"
          @click="expose"
        >
          {{ exposing ? 'Exposing…' : 'Expose secret' }}
        </button>
      </div>
    </div>

    <!-- Error/loading states -->
    <div
      v-if="loadError"
      class="mt-3 rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[11px] text-signal-danger"
      role="alert"
    >
      {{ loadError }}
    </div>
    <div
      v-if="revokeError"
      class="mt-3 rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[11px] text-signal-danger"
      role="alert"
    >
      {{ revokeError }}
    </div>

    <!-- Secrets table -->
    <div class="mt-3">
      <div
        v-if="loading"
        class="font-ui text-[11px] text-ink-dim py-4 text-center"
        data-testid="secrets-loading"
      >
        Loading…
      </div>
      <div
        v-else-if="rows.length === 0 && !loadError"
        class="rounded-sm border border-border bg-surface-1 px-4 py-6 text-center font-ui text-[11px] text-ink-dim"
        data-testid="secrets-empty"
      >
        No secrets are currently exposed to the model.
        Use <strong>+ Expose secret</strong> to add one.
      </div>
      <table
        v-else
        class="w-full text-left"
        data-testid="secrets-table"
      >
        <thead>
          <tr class="border-b border-border">
            <th class="pb-1 pr-4 font-ui text-[10px] uppercase tracking-[0.14em] text-ink-subtle">Ref token</th>
            <th class="pb-1 pr-4 font-ui text-[10px] uppercase tracking-[0.14em] text-ink-subtle">Description</th>
            <th class="pb-1 pr-4 font-ui text-[10px] uppercase tracking-[0.14em] text-ink-subtle">Kind</th>
            <th class="pb-1 pr-4 font-ui text-[10px] uppercase tracking-[0.14em] text-ink-subtle">Scope</th>
            <th class="pb-1 font-ui text-[10px] uppercase tracking-[0.14em] text-ink-subtle">Actions</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="row in rows"
            :key="row.locator"
            class="border-b border-border-muted last:border-b-0"
            :data-testid="`secrets-row-${row.locator}`"
          >
            <td class="py-2 pr-4 font-mono text-[11px] text-ink">
              {{ row.ref }}
            </td>
            <td class="py-2 pr-4 font-ui text-[11px] text-ink-muted">
              {{ row.description || '—' }}
            </td>
            <td class="py-2 pr-4 font-ui text-[11px] text-ink-muted">
              {{ row.kind }}
            </td>
            <td class="py-2 pr-4 font-ui text-[11px] text-ink-muted">
              {{ row.scope }}
            </td>
            <td class="py-2">
              <button
                type="button"
                class="rounded-sm border border-signal-danger px-2 py-0.5 font-ui text-[10px] uppercase tracking-[0.12em] text-signal-danger hover:bg-signal-danger hover:text-white disabled:opacity-50 transition-colors"
                :disabled="revoking[row.locator]"
                :data-testid="`secrets-revoke-${row.locator}`"
                @click="revoke(row.locator)"
              >
                {{ revoking[row.locator] ? 'Revoking…' : 'Revoke' }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </section>
</template>
