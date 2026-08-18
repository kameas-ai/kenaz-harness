<script setup lang="ts">
/**
 * ToolPermissionModal — permission modal for generic tool requests.
 *
 * Subscribes to `tool:permission-pending` via useEventStream.
 * Renders server name + tool name + redacted args summary. The
 * resource_display field carries the tool identifier.
 *
 * Reconciles against Permissions_ListPending on mount (WP03) — a
 * reload does not un-park the goroutine backing a tool prompt, only
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

void client.settings.getPermissionCacheDangerousOps().then((v) => {
  cacheDangerousOps.value = v;
}).catch(() => {});

const reconcile = usePermissionReconcile(client, 'tool', queue, MAX_QUEUE);
onMounted(() => {
  void reconcile();
});

useEventStream<PermissionRequest>('tool:permission-pending', (payload) => {
  if (!payload || !payload.request_id) return;
  if (queue.value.some((q) => q.request_id === payload.request_id)) return;
  if (queue.value.length >= MAX_QUEUE) {
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

function onResolved(requestID: string) {
  queue.value = queue.value.filter((q) => q.request_id !== requestID);
}

defineExpose({ queue, reconcile });
</script>

<template>
  <BasePermissionModal
    family-icon="🔧"
    family-label="Tool"
    :request="head"
    :queue-length="queue.length"
    :allow-always-enabled="allowAlwaysEnabled"
    data-testid="tool-permission-modal"
    @resolved="onResolved"
  />
</template>
