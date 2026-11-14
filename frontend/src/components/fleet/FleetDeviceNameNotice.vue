<script setup lang="ts">
/**
 * FleetDeviceNameNotice: the plain line that tells the member which device
 * name Fleet's admins will see (the enroll `hostname`, Fleet ER-43), shown
 * where they sign in to Fleet and on the Account card, so the name is
 * disclosed before it is sent. Display only: it never blocks sign-in, and a
 * failed read renders nothing. Nothing is shown when sharing is off.
 */
import { onMounted, ref, watch } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { fleetDeviceNameNoticeText, fleetDeviceNameVersion } from '@/lib/fleetDeviceName';

const props = defineProps<{
  /** Re-read when this changes (e.g. the Fleet session state). */
  refreshKey?: unknown;
}>();

const client = useHarnessClient();
const text = ref('');

async function load() {
  try {
    text.value = fleetDeviceNameNoticeText(await client.settings.getFleetDeviceName());
  } catch {
    text.value = '';
  }
}

onMounted(load);
watch(() => props.refreshKey, load);
watch(fleetDeviceNameVersion, load);
</script>

<template>
  <p v-if="text" class="text-xs text-ink-muted" data-testid="fleet-device-name-notice">{{ text }}</p>
</template>
