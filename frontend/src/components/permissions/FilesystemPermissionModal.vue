<script setup lang="ts">
/**
 * FilesystemPermissionModal — permission modal for filesystem op requests.
 *
 * Subscribes to `fs:permission-pending` via useEventStream.
 * Extends BasePermissionModal with a two-tier "Allow always" radio:
 *   - Tier 1 (default): "Allow this exact path"
 *   - Tier 2: "Allow this directory and below"
 *
 * The selected tier is passed as a query parameter in the resolve call
 * so the backend can write the appropriate Cedar policy.
 *
 * Dangerous-path ops (system paths) disable "Allow always" unless
 * Settings.PermissionCacheDangerousOps is true.
 *
 * Reconciles against Permissions_ListPending on mount (WP03) — a
 * reload does not un-park the goroutine backing an fs prompt, only
 * the frontend's memory of it. See usePermissionReconcile's doc
 * comment for the full contract.
 */

import { computed, onMounted, ref } from 'vue';
import BasePermissionModal from './BasePermissionModal.vue';
import { useEventStream } from '@/lib/useEventStream';
import { useHarnessClient } from '@/lib/harnessClientContext';
import { usePermissionReconcile } from '@/lib/usePermissionReconcile';
import type { PermissionRequest } from '@/lib/types';

const MAX_QUEUE = 5;

const client = useHarnessClient();
const queue = ref<PermissionRequest[]>([]);
const cacheDangerousOps = ref(false);
// Two-tier radio: 'exact' (default) | 'directory'
const grantScope = ref<'exact' | 'directory'>('exact');

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
  // Reset radio to default on each new request
  grantScope.value = 'exact';
  queue.value = [...queue.value, payload];
});

const head = computed<PermissionRequest | null>(() => queue.value[0] ?? null);

const allowAlwaysEnabled = computed(() => {
  if (!head.value?.dangerous_tier) return true;
  return cacheDangerousOps.value;
});

function onResolved(requestID: string) {
  queue.value = queue.value.filter((q) => q.request_id !== requestID);
  grantScope.value = 'exact';
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
    </div>
  </BasePermissionModal>
</template>
