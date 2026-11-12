<script setup lang="ts">
/**
 * FilesystemPermissionModal — permission modal for filesystem op requests.
 *
 * Subscribes to `fs:permission-pending` via useEventStream.
 * Extends BasePermissionModal with a two-tier "Allow always" radio:
 *   - Tier 1 (default): "Allow this exact path"
 *   - Tier 2: "Allow this directory and below"
 *
 * The selected tier travels with an Allow-always as
 * `allow_always:directory` (or `allow_always:directory_rw` when the
 * "also allow reading" box is ticked on a write) so the backend writes
 * the matching Cedar grant. Before this was wired the radio was never
 * sent and every Allow-always was an exact-path grant (E2E-1).
 *
 * After an Allow-always the queue is reconciled: the backend releases
 * every queued fs request the new grant covers inside the same Resolve
 * call, so those rows disappear instead of prompting again.
 *
 * Dangerous-path ops (system paths) disable "Allow always" unless
 * Settings.PermissionCacheDangerousOps is true.
 *
 * Reconciles against Permissions_ListPending on mount (WP03) — a
 * reload does not un-park the goroutine backing an fs prompt, only
 * the frontend's memory of it. See usePermissionReconcile's doc
 * comment for the full contract.
 */

import { computed, onMounted, ref, watch } from 'vue';
import BasePermissionModal from './BasePermissionModal.vue';
import { useEventStream } from '@/lib/useEventStream';
import { useHarnessClient } from '@/lib/harnessClientContext';
import { usePermissionReconcile } from '@/lib/usePermissionReconcile';
import type { PermissionRequest } from '@/lib/types';

const MAX_QUEUE = 5;

const client = useHarnessClient();
const queue = ref<PermissionRequest[]>([]);
const cacheDangerousOps = ref(false);
// Two-tier radio: 'exact' | 'directory'. Defaults per request (see
// defaultScope): a write that creates its directory defaults to the
// directory, because it is almost never the only file going there.
const grantScope = ref<'exact' | 'directory'>('exact');
// With a directory grant on a write: also remember reads of it, so the
// agent can read back what it just wrote without another prompt.
const alsoAllowReads = ref(true);

void client.settings.getPermissionCacheDangerousOps().then((v) => {
  cacheDangerousOps.value = v;
}).catch(() => {});

const reconcile = usePermissionReconcile(client, 'fs', queue, MAX_QUEUE);
onMounted(() => {
  void reconcile();
});

useEventStream<PermissionRequest>('fs:permission-pending', (payload) => {
  if (!payload || !payload.request_id) return;
  if (queue.value.some((q) => q.request_id === payload.request_id)) return;
  if (queue.value.length >= MAX_QUEUE) {
    void client.permissions.resolve(payload.request_id, 'deny').catch(() => {});
    return;
  }
  queue.value = [...queue.value, payload];
});

const head = computed<PermissionRequest | null>(() => queue.value[0] ?? null);

function defaultScope(r: PermissionRequest | null): 'exact' | 'directory' {
  return r?.surface?.fs?.parent_missing ? 'directory' : 'exact';
}

// Reset the radio whenever a different request reaches the head.
watch(
  () => head.value?.request_id,
  () => {
    grantScope.value = defaultScope(head.value);
    alsoAllowReads.value = true;
  },
  { immediate: true },
);

const privileged = computed(() => head.value?.surface?.fs?.privileged ?? '');

const wireScope = computed(() => {
  if (grantScope.value !== 'directory') return 'exact';
  return head.value?.op === 'write' && alsoAllowReads.value ? 'directory_rw' : 'directory';
});

const allowAlwaysEnabled = computed(() => {
  if (!head.value?.dangerous_tier) return true;
  return cacheDangerousOps.value;
});

function onResolved(requestID: string, decision?: string) {
  queue.value = queue.value.filter((q) => q.request_id !== requestID);
  if (decision === 'allow_always') {
    // Drop queued rows the new grant released server-side.
    void reconcile();
  }
}

defineExpose({ queue, reconcile });
</script>

<template>
  <BasePermissionModal
    family-icon="📁"
    family-label="Filesystem"
    :request="head"
    :queue-length="queue.length"
    :allow-always-enabled="allowAlwaysEnabled"
    :allow-always-scope="wireScope"
    data-testid="filesystem-permission-modal"
    @resolved="onResolved"
  >
    <!-- Model-initiated expanded-access notice (recipe_dir_add) -->
    <p
      v-if="head?.op === 'recipe_dir_add'"
      class="mt-2 font-ui text-[12px] text-ink-muted"
      data-testid="fs-recipe-dir-add-notice"
    >
      The model is requesting expanded filesystem access to this directory.
    </p>

    <!-- Harness-state warning (E2E-3) -->
    <p
      v-if="privileged"
      class="mt-2 rounded-sm border border-signal-warn bg-surface-2 px-3 py-2 font-ui text-[12px] text-signal-warn"
      data-testid="fs-privileged-notice"
    >
      {{ privileged }}
    </p>

    <!-- Two-tier scope radio (spec FR-018) -->
    <div
      v-if="head"
      class="mt-3"
      data-testid="fs-grant-scope-radio"
    >
      <p class="font-ui text-[11px] uppercase tracking-[0.16em] text-ink-subtle mb-1">
        If allowed always, scope to:
      </p>
      <label class="flex items-center gap-2 cursor-pointer font-ui text-[12px] text-ink">
        <input
          v-model="grantScope"
          type="radio"
          value="exact"
          name="fs-grant-scope"
          class="accent-accent"
          data-testid="fs-scope-exact"
        />
        This exact path
      </label>
      <label class="mt-1 flex items-center gap-2 cursor-pointer font-ui text-[12px] text-ink">
        <input
          v-model="grantScope"
          type="radio"
          value="directory"
          name="fs-grant-scope"
          class="accent-accent"
          data-testid="fs-scope-directory"
        />
        This directory and below
      </label>
      <label
        v-if="grantScope === 'directory' && head.op === 'write'"
        class="mt-1 ml-5 flex items-center gap-2 cursor-pointer font-ui text-[12px] text-ink"
      >
        <input
          v-model="alsoAllowReads"
          type="checkbox"
          class="accent-accent"
          data-testid="fs-scope-also-read"
        />
        Also allow reading this directory
      </label>
    </div>
  </BasePermissionModal>
</template>
