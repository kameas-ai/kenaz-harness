<script setup lang="ts">
/**
 * BashPermissionModal — permission modal for bash command requests.
 *
 * Subscribes to `bash:permission-pending` via useEventStream.
 * Queues up to 5 pending requests; overflow auto-denies with a toast.
 *
 * Dangerous-tier ops disable "Allow always" unless the user has
 * enabled Settings.PermissionCacheDangerousOps.
 */

import { computed, ref } from 'vue';
import BasePermissionModal from './BasePermissionModal.vue';
import { useEventStream } from '@/lib/useEventStream';
import { useHarnessClient } from '@/lib/harnessClientContext';
import type { PermissionRequest } from '@/lib/types';

const MAX_QUEUE = 5;

const client = useHarnessClient();
const queue = ref<PermissionRequest[]>([]);
const cacheDangerousOps = ref(false);

// Load the dangerous-ops override flag on mount
void client.settings.getPermissionCacheDangerousOps().then((v) => {
  cacheDangerousOps.value = v;
}).catch(() => {});

useEventStream<PermissionRequest>('bash:permission-pending', (payload) => {
  if (!payload || !payload.request_id) return;
  if (queue.value.some((q) => q.request_id === payload.request_id)) return;
  if (queue.value.length >= MAX_QUEUE) {
    // Overflow: auto-deny
    void client.permissions.resolve(payload.request_id, 'deny').catch(() => {});
    return;
  }
  queue.value = [...queue.value, payload];
});

const head = computed<PermissionRequest | null>(() => queue.value[0] ?? null);

const allowAlwaysEnabled = computed(() => {
  if (!head.value?.dangerous_tier) return true;
  return cacheDangerousOps.value;
});

const allowAlwaysLabel = computed(() => {
  if (head.value?.dangerous_tier && !cacheDangerousOps.value) {
    return 'Allow always (disabled for dangerous ops)';
  }
  return 'Allow always';
});

function onResolved(requestID: string) {
  queue.value = queue.value.filter((q) => q.request_id !== requestID);
}

defineExpose({ queue });
</script>

<template>
  <BasePermissionModal
    family-icon="⚡"
    family-label="Bash command"
    :request="head"
    :queue-length="queue.length"
    :allow-always-enabled="allowAlwaysEnabled"
    :allow-always-label="allowAlwaysLabel"
    data-testid="bash-permission-modal"
    @resolved="onResolved"
  />
</template>
