<script setup lang="ts">
/**
 * RecoveryCodeFlow — two-mode dialog for context-sync seed recovery.
 *
 * Mode "generate":
 *   Calls ContextSync_GenerateRecoveryCode, displays the code ONE TIME
 *   with copy-to-clipboard, requires explicit "I've saved it" acknowledgement
 *   before closing.
 *
 * Mode "apply":
 *   Accepts a pasted KENAZ-{...} code and calls ContextSync_ApplyRecoveryCode
 *   to restore the seed, re-deriving all stream keys.
 *
 * Privacy: the recovery code is a base64url-encoded seed. It never touches
 * slog or audit — only displayed once in this dialog, then discarded.
 *
 * (fleet-context-sync-01NDFSEX15 WP07)
 */

import { ref, computed } from 'vue';
import BaseDialog from '@/components/ui/BaseDialog.vue';
import Button from '@/components/ui/Button.vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';

const props = defineProps<{
  open: boolean;
  /** 'generate' shows the new code; 'apply' accepts a pasted code. */
  mode: 'generate' | 'apply';
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'done'): void;
}>();

const client = useHarnessClient();

// ── State ──────────────────────────────────────────────────────────────────

/** The generated code — set only in generate mode after backend call. */
const generatedCode = ref('');
const applyCode = ref('');
const loading = ref(false);
const acknowledged = ref(false);
const copied = ref(false);
const errorMsg = ref('');

// ── Derived ────────────────────────────────────────────────────────────────

const title = computed(() =>
  props.mode === 'generate' ? 'Generate recovery code' : 'Apply recovery code',
);

const canClose = computed(() =>
  props.mode === 'apply' || acknowledged.value || generatedCode.value === '',
);

// ── Generate flow ──────────────────────────────────────────────────────────

async function onGenerate() {
  loading.value = true;
  errorMsg.value = '';
  try {
    generatedCode.value = await client.ContextSync_GenerateRecoveryCode();
    acknowledged.value = false;
  } catch (err) {
    errorMsg.value = String(err);
  } finally {
    loading.value = false;
  }
}

async function copyCode() {
  if (!generatedCode.value) return;
  try {
    await navigator.clipboard.writeText(generatedCode.value);
    copied.value = true;
    setTimeout(() => { copied.value = false; }, 2000);
  } catch {
    // clipboard unavailable — let user copy manually
  }
}

function onAcknowledge() {
  acknowledged.value = true;
}

function onGenerateDone() {
  generatedCode.value = '';
  acknowledged.value = false;
  emit('done');
  emit('close');
}

// ── Apply flow ────────────────────────────────────────────────────────────

async function onApply() {
  if (!applyCode.value.trim()) return;
  loading.value = true;
  errorMsg.value = '';
  try {
    await client.ContextSync_ApplyRecoveryCode(applyCode.value.trim());
    applyCode.value = '';
    emit('done');
    emit('close');
  } catch (err) {
    errorMsg.value = String(err);
  } finally {
    loading.value = false;
  }
}

function handleClose() {
  if (!canClose.value) return;
  generatedCode.value = '';
  applyCode.value = '';
  acknowledged.value = false;
  errorMsg.value = '';
  emit('close');
}
</script>

<template>
  <BaseDialog
    :open="open"
    :title="title"
    panel-class="w-full max-w-md rounded-md border border-border-muted bg-surface-1 p-5 shadow-xl"
    :close-on-overlay-click="canClose"
    @close="handleClose"
  >
    <div data-testid="recovery-code-flow">
      <h2 class="font-ui text-base font-semibold text-ink">
        {{ title }}
      </h2>

      <!-- ── Generate mode ──────────────────────────────────────────────── -->
      <template v-if="mode === 'generate'">
        <p class="mt-2 font-ui text-sm text-ink-muted">
          Generate a recovery code for your E2E-encrypted context sync seed.
          Store it in a password manager — it will only be shown once.
        </p>

        <!-- Code display (after generation) -->
        <template v-if="generatedCode">
          <div
            class="mt-4 rounded border border-border-muted bg-surface-2 p-3 font-mono text-xs text-ink break-all select-all"
            data-testid="recovery-code-display"
          >
            {{ generatedCode }}
          </div>

          <div class="mt-2 flex items-center gap-2">
            <Button
              variant="ghost"
              class="text-xs"
              data-testid="copy-code-btn"
              @click="copyCode"
            >
              {{ copied ? 'Copied!' : 'Copy to clipboard' }}
            </Button>
          </div>

          <!-- Acknowledgement gate -->
          <div
            v-if="!acknowledged"
            class="mt-4 flex items-start gap-2"
            data-testid="acknowledge-row"
          >
            <input
              id="recovery-ack-checkbox"
              type="checkbox"
              class="mt-0.5 accent-accent cursor-pointer"
              data-testid="recovery-ack-checkbox"
              @change="onAcknowledge"
            />
            <label for="recovery-ack-checkbox" class="font-ui text-sm text-ink cursor-pointer">
              I've saved my recovery code in a safe place.
            </label>
          </div>

          <div class="mt-5 flex justify-end">
            <Button
              variant="accent"
              :disabled="!acknowledged"
              data-testid="recovery-done-btn"
              @click="onGenerateDone"
            >
              Done
            </Button>
          </div>
        </template>

        <!-- Pre-generation -->
        <template v-else>
          <p
            v-if="errorMsg"
            class="mt-3 font-ui text-xs text-signal-err"
            data-testid="recovery-error"
            role="alert"
          >
            {{ errorMsg }}
          </p>
          <div class="mt-5 flex justify-end gap-2">
            <Button variant="ghost" data-testid="recovery-cancel-btn" @click="handleClose">
              Cancel
            </Button>
            <Button
              variant="accent"
              :disabled="loading"
              data-testid="generate-code-btn"
              @click="onGenerate"
            >
              {{ loading ? 'Generating…' : 'Generate code' }}
            </Button>
          </div>
        </template>
      </template>

      <!-- ── Apply mode ─────────────────────────────────────────────────── -->
      <template v-else>
        <p class="mt-2 font-ui text-sm text-ink-muted">
          Paste your KENAZ recovery code to restore your context sync seed on this device.
          All encrypted streams will be accessible again.
        </p>

        <div class="mt-4">
          <label for="apply-code-input" class="block font-ui text-xs uppercase tracking-[0.15em] text-ink-muted mb-1">
            Recovery code
          </label>
          <textarea
            id="apply-code-input"
            v-model="applyCode"
            rows="3"
            placeholder="KENAZ-XXXXXXXX-XXXXXXXX-…"
            class="w-full rounded border border-border-muted bg-surface-2 px-3 py-1.5 font-mono text-xs text-ink placeholder:text-ink-subtle focus:outline-none focus:ring-1 focus:ring-accent resize-none"
            data-testid="apply-code-input"
          />
        </div>

        <p
          v-if="errorMsg"
          class="mt-2 font-ui text-xs text-signal-err"
          data-testid="recovery-error"
          role="alert"
        >
          {{ errorMsg }}
        </p>

        <div class="mt-5 flex justify-end gap-2">
          <Button variant="ghost" data-testid="recovery-cancel-btn" @click="handleClose">
            Cancel
          </Button>
          <Button
            variant="accent"
            :disabled="loading || !applyCode.trim()"
            data-testid="apply-code-btn"
            @click="onApply"
          >
            {{ loading ? 'Applying…' : 'Apply code' }}
          </Button>
        </div>
      </template>
    </div>
  </BaseDialog>
</template>
