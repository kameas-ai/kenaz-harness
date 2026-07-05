<script setup lang="ts">
/**
 * PermissionDialsPanel — Permission mode radio + Cache-dangerous-ops
 * toggle. Lives at the top of the Permissions page (extracted from
 * SettingsView so the General tab stays focused on app preferences).
 *
 * (silent-failure-elimination-QQ5GXW50 WP02 / FR-002b)
 * Security-significant toggles (permission mode, dangerous-ops cache)
 * surface failures via a dedicated inline error state rather than a
 * silent snap-back.
 */
import { onMounted, ref } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { runAsyncAction } from '@/composables/useAsyncAction';
import RadioStrip from './RadioStrip.vue';
import type { PermissionMode } from '@/lib/types';

const client = useHarnessClient();

const PERMISSION_MODES: ReadonlyArray<{ value: PermissionMode; label: string; note?: string }> = [
  { value: 'strict', label: 'Strict', note: 'every call prompts' },
  { value: 'normal', label: 'Normal', note: 'default' },
  { value: 'permissive', label: 'Permissive', note: 'non-dangerous skip prompt' },
];

const permissionMode = ref<PermissionMode>('normal');
const permissionCacheDangerousOps = ref(false);

/** Inline error state for security-significant write failures. */
const permissionModeError = ref<string | null>(null);
const dangerousOpsError = ref<string | null>(null);

// In-flight pending value for the dangerous-ops confirm bar. The
// dangerous-ops toggle is the actual security-significant action and
// keeps its inline confirm; permission-mode changes are reversible
// and apply immediately.
const pendingDangerousToggle = ref<boolean | null>(null);

// Permission mode changes apply immediately — the click IS the
// confirm. The dial is reversible: click Strict / Normal at any time
// to dial back. (Earlier the Permissive jump was gated behind an
// inline confirm bar; HMR-cached component instances and Wails
// webview window.confirm quirks made that path silently swallow
// clicks. Direct apply is friction-free and matches every other
// reversible toggle in the app.)
async function requestPermissionMode(mode: PermissionMode) {
  const previous = permissionMode.value;
  permissionModeError.value = null;
  const result = await runAsyncAction({
    optimistic: () => { permissionMode.value = mode; },
    revert:     () => { permissionMode.value = previous; },
    action:     () => client.settings.setPermissionMode(mode),
    errorLabel: 'Set permission mode',
    toastMessage: (err) => {
      const msg = err instanceof Error ? err.message : String(err);
      return `Permission mode change failed: ${msg}. Your previous setting is still active.`;
    },
  });
  if (result.error) {
    permissionModeError.value = result.err instanceof Error
      ? result.err.message
      : String(result.err);
  }
}

function requestDangerousToggle() {
  const next = !permissionCacheDangerousOps.value;
  if (next) {
    pendingDangerousToggle.value = true;
    return;
  }
  void applyDangerousToggle(false);
}

async function applyDangerousToggle(next: boolean) {
  pendingDangerousToggle.value = null;
  dangerousOpsError.value = null;
  const result = await runAsyncAction({
    optimistic: () => { permissionCacheDangerousOps.value = next; },
    revert:     () => { permissionCacheDangerousOps.value = !next; },
    action:     () => client.settings.setPermissionCacheDangerousOps(next),
    errorLabel: 'Set dangerous-ops cache',
    toastMessage: (err) => {
      const msg = err instanceof Error ? err.message : String(err);
      return `Security setting change failed: ${msg}. Your previous setting is still active.`;
    },
  });
  if (result.error) {
    dangerousOpsError.value = result.err instanceof Error
      ? result.err.message
      : String(result.err);
  }
}

function cancelDangerousToggle() {
  pendingDangerousToggle.value = null;
}

onMounted(async () => {
  try {
    permissionMode.value = (await client.settings.getPermissionMode()) as PermissionMode;
  } catch {
    permissionMode.value = 'normal';
  }
  try {
    permissionCacheDangerousOps.value = await client.settings.getPermissionCacheDangerousOps();
  } catch {
    permissionCacheDangerousOps.value = false;
  }
});
</script>

<template>
  <div class="grid gap-6 max-w-3xl" data-testid="permission-dials-panel">
    <section id="permissions-mode" data-testid="permission-mode-section">
      <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
        Permission mode
      </h2>
      <p class="mt-1 font-ui text-[11px] text-ink-dim">
        Controls when permission prompts appear across all resource families (bash, filesystem, credentials, tools).
      </p>
      <div class="mt-2">
        <RadioStrip
          :model-value="permissionMode"
          :options="PERMISSION_MODES"
          aria-label="Permission mode"
          testid-prefix="permission-mode"
          @update:model-value="requestPermissionMode"
        />
      </div>
      <p
        v-if="permissionMode === 'permissive'"
        class="mt-3 font-ui text-[11px] text-signal-warn"
        data-testid="permissive-active-warning"
      >
        Permissive: non-dangerous ops skip the prompt. Dial back to Strict / Normal at any time.
      </p>
      <!-- Explicit error state for security-significant permission mode failures. -->
      <p
        v-if="permissionModeError"
        class="mt-2 font-ui text-[11px] text-signal-danger"
        role="alert"
        data-testid="permission-mode-error"
      >
        Save failed: {{ permissionModeError }}
      </p>
    </section>

    <section data-testid="permission-cache-dangerous-section">
      <div class="flex items-center justify-between gap-4">
        <div>
          <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
            Cache dangerous operation grants
          </h2>
          <p class="mt-0.5 font-ui text-[11px] text-ink-dim">
            When enabled, "Allow always" is offered for dangerous ops (rm, sudo, system paths, etc).
            Default off — dangerous ops re-prompt every time.
          </p>
        </div>
        <button
          type="button"
          class="ml-4 shrink-0 rounded-full w-9 h-5 transition-colors"
          :class="permissionCacheDangerousOps ? 'bg-accent' : 'bg-surface-3'"
          role="switch"
          :aria-checked="permissionCacheDangerousOps"
          data-testid="permission-cache-dangerous-toggle"
          @click="requestDangerousToggle"
        >
          <span class="sr-only">Cache dangerous operation grants</span>
          <span
            class="block w-4 h-4 rounded-full bg-white shadow transition-transform mx-0.5"
            :class="permissionCacheDangerousOps ? 'translate-x-4' : 'translate-x-0'"
          />
        </button>
      </div>
      <!-- Explicit error state for security-significant dangerous-ops failures. -->
      <p
        v-if="dangerousOpsError"
        class="mt-2 font-ui text-[11px] text-signal-danger"
        role="alert"
        data-testid="dangerous-ops-error"
      >
        Save failed: {{ dangerousOpsError }}
      </p>
      <div
        v-if="pendingDangerousToggle"
        class="mt-3 rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-ink"
        role="alertdialog"
        data-testid="dangerous-confirm"
      >
        <p class="text-signal-danger">
          Enabling this allows <strong>"Allow always"</strong> for dangerous
          ops like <code class="font-mono">rm</code>, <code class="font-mono">sudo</code>,
          and system path writes. This is a significant security override.
        </p>
        <div class="mt-2 flex justify-end gap-2">
          <button
            type="button"
            class="rounded-sm px-2 py-1 font-ui text-[11px] text-ink-dim hover:text-ink"
            data-testid="dangerous-confirm-cancel"
            @click="cancelDangerousToggle"
          >
            Cancel
          </button>
          <button
            type="button"
            class="rounded-sm border border-signal-danger bg-surface-1 px-2 py-1 font-ui text-[11px] text-signal-danger hover:bg-surface-2"
            data-testid="dangerous-confirm-yes"
            @click="applyDangerousToggle(true)"
          >
            Enable anyway
          </button>
        </div>
      </div>
    </section>
  </div>
</template>
