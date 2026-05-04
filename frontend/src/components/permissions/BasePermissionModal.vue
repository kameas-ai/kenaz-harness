<script setup lang="ts">
/**
 * BasePermissionModal — shared shell for all four family-specific
 * permission modals.
 *
 * Wire flow (identical for all four families):
 *   1. Parent subscribes to its broker topic via useEventStream and
 *      calls show(request) when a payload arrives.
 *   2. BasePermissionModal renders the request + three buttons.
 *   3. On decision, BasePermissionModal calls
 *      client.permissions.resolve(requestID, decision) and emits
 *      'resolved' so the parent can pop the queue.
 *   4. Esc key = Deny. 5-minute timeout = auto-Deny.
 *
 * Props:
 *   - familyIcon  — emoji or short label (rendered in the eyebrow)
 *   - familyLabel — e.g. "Bash command", "Filesystem", etc.
 *   - request     — the PermissionRequest to render (null = hidden)
 *   - queueLength — total pending count (renders "+N more" badge)
 *   - allowAlwaysEnabled — when false, the "Allow always" button is
 *     disabled (dangerous-tier + PermissionCacheDangerousOps=false).
 *   - allowAlwaysLabel — override button label for the dangerous tier.
 *
 * Slots:
 *   - default — additional family-specific detail rendered between
 *     the resource display and the reason text.
 *
 * Emits:
 *   - resolved(requestID: string, decision: string)
 */

import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import type { PermissionRequest } from '@/lib/types';

const props = withDefaults(
  defineProps<{
    familyIcon?: string;
    familyLabel: string;
    request: PermissionRequest | null;
    queueLength?: number;
    allowAlwaysEnabled?: boolean;
    allowAlwaysLabel?: string;
  }>(),
  {
    familyIcon: '🔒',
    queueLength: 0,
    allowAlwaysEnabled: true,
    allowAlwaysLabel: 'Allow always',
  },
);

const emit = defineEmits<{
  resolved: [requestID: string, decision: string];
}>();

const client = useHarnessClient();
const submitting = ref(false);
const lastError = ref<string | null>(null);

// 5-minute auto-deny timer
const TIMEOUT_MS = 5 * 60 * 1000;
let timer: ReturnType<typeof setTimeout> | null = null;

function startTimer() {
  if (timer) clearTimeout(timer);
  timer = setTimeout(() => {
    void decide('deny');
  }, TIMEOUT_MS);
}

function clearTimer() {
  if (timer) {
    clearTimeout(timer);
    timer = null;
  }
}

watch(
  () => props.request,
  (r) => {
    if (r) {
      startTimer();
    } else {
      clearTimer();
    }
  },
  { immediate: true },
);

onBeforeUnmount(clearTimer);

// Esc = Deny
function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape' && props.request) {
    void decide('deny');
  }
}

onMounted(() => document.addEventListener('keydown', onKeydown));
onBeforeUnmount(() => document.removeEventListener('keydown', onKeydown));

async function decide(decision: string) {
  const req = props.request;
  if (!req || submitting.value) return;
  submitting.value = true;
  lastError.value = null;
  clearTimer();
  try {
    await client.permissions.resolve(req.request_id, decision);
    emit('resolved', req.request_id, decision);
  } catch (err) {
    lastError.value = err instanceof Error ? err.message : String(err);
    // Re-arm timer after an error so the modal doesn't hang open
    startTimer();
  } finally {
    submitting.value = false;
  }
}

const extraPending = computed(() =>
  Math.max(0, (props.queueLength ?? 0) - 1),
);

defineExpose({ decide });
</script>

<template>
  <div
    v-if="request"
    class="fixed inset-0 z-50 flex items-center justify-center"
    role="dialog"
    aria-modal="true"
    :aria-labelledby="`perm-modal-title-${request.request_id}`"
    data-testid="base-permission-modal"
  >
    <div class="absolute inset-0 bg-modal-overlay" />
    <div
      class="relative w-full max-w-lg rounded-md border border-accent-hairline bg-surface-1 p-5 shadow-xl"
    >
      <!-- Eyebrow -->
      <div class="flex items-center gap-1.5 font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
        <span aria-hidden="true">{{ familyIcon }}</span>
        <span>{{ familyLabel }} — permission required</span>
      </div>

      <!-- Resource display -->
      <h2
        :id="`perm-modal-title-${request.request_id}`"
        class="mt-1 font-mono text-sm font-semibold text-ink break-all"
        data-testid="perm-modal-resource"
      >
        {{ request.resource_display }}
      </h2>

      <!-- Family-specific slot (radio for fs, etc.) -->
      <slot />

      <!-- Dangerous-tier warning -->
      <div
        v-if="request.dangerous_tier"
        class="mt-2 rounded-sm border border-signal-danger bg-surface-2 px-3 py-2 font-ui text-[12px] text-signal-danger"
        role="alert"
        data-testid="perm-modal-danger-copy"
      >
        <span class="font-semibold">Dangerous:</span>
        {{ request.danger_copy || 'This operation is flagged as dangerous.' }}
        <template v-if="!allowAlwaysEnabled">
          Enable "Cache dangerous ops" in Settings to allow persistent grants.
        </template>
      </div>

      <!-- Model reason -->
      <p
        v-if="request.reason"
        class="mt-2 font-ui text-[12px] text-ink-muted line-clamp-2"
        data-testid="perm-modal-reason"
      >
        {{ request.reason }}
      </p>

      <!-- Error -->
      <div
        v-if="lastError"
        class="mt-2 rounded-sm border border-signal-warn bg-surface-2 px-3 py-2 font-ui text-[12px] text-signal-warn"
        role="alert"
      >
        {{ lastError }}
      </div>

      <!-- Buttons -->
      <div class="mt-4 flex flex-col gap-2 font-ui text-[11px] uppercase tracking-[0.18em]">
        <div class="grid grid-cols-2 gap-2">
          <button
            type="button"
            class="rounded-md border border-accent text-accent px-3 py-2 hover:bg-surface-2 disabled:opacity-50"
            :disabled="submitting"
            data-testid="perm-modal-allow-once"
            @click="decide('allow_once')"
          >
            Allow once
          </button>
          <button
            type="button"
            class="rounded-md border border-border-muted text-ink-muted px-3 py-2 hover:bg-surface-2 disabled:opacity-50"
            :disabled="submitting"
            data-testid="perm-modal-deny"
            @click="decide('deny')"
          >
            Deny
          </button>
        </div>
        <button
          type="button"
          class="w-full rounded-md border border-accent-hairline text-accent px-3 py-2 hover:bg-surface-2 disabled:opacity-50 disabled:text-ink-muted disabled:border-border-muted disabled:cursor-not-allowed"
          :disabled="submitting || !allowAlwaysEnabled"
          data-testid="perm-modal-allow-always"
          :title="allowAlwaysEnabled ? '' : 'Disabled for dangerous ops. Enable in Settings → Permissions.'"
          @click="decide('allow_always')"
        >
          {{ allowAlwaysLabel }}
        </button>
      </div>

      <!-- Overflow badge -->
      <div
        v-if="extraPending > 0"
        class="mt-3 font-ui text-[10px] uppercase tracking-[0.18em] text-ink-subtle"
        data-testid="perm-modal-overflow"
      >
        +{{ extraPending }} more pending
      </div>
    </div>
  </div>
</template>
