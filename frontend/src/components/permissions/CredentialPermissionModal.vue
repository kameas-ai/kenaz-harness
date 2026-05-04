<script setup lang="ts">
/**
 * CredentialPermissionModal — permission modal for credential requests.
 *
 * Subscribes to `cred:permission-pending` via useEventStream.
 * Renders the credential provider + purpose. The resource_display
 * field carries the provider::purpose string. No dangerous-tier
 * radio (credentials use "manual_export" as the dangerous tier,
 * handled server-side as default-deny; the prompt only fires for
 * mcp_spawn which is not dangerous).
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

void client.settings.getPermissionCacheDangerousOps().then((v) => {
  cacheDangerousOps.value = v;
}).catch(() => {});

useEventStream<PermissionRequest>('cred:permission-pending', (payload) => {
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

defineExpose({ queue });
</script>

<template>
  <BasePermissionModal
    family-icon="🔑"
    family-label="Credential"
    :request="head"
    :queue-length="queue.length"
    :allow-always-enabled="allowAlwaysEnabled"
    data-testid="credential-permission-modal"
    @resolved="onResolved"
  />
</template>
