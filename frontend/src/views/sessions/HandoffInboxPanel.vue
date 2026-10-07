<script setup lang="ts">
/**
 * HandoffInboxPanel — sessions teammates shared with you
 * (device-keys-handoff-01DEVKH01 WP06).
 *
 * Each item can be opened (Handoff_Accept decrypts it with THIS device's
 * key, saves it as a new local session and the panel navigates there) or
 * dismissed (Handoff_Delete). Items fleet flags `undecryptable` — sent to a
 * device key of yours that no longer exists — are shown disabled with
 * honest copy; nothing promises they can be recovered.
 *
 * Fleet hides an item 24 h after it is first opened and opening one here
 * removes it for all your devices, so an item disappearing from this list
 * is normal.
 *
 * Privacy: only opaque ids and the sender's email cross this component.
 */
import { computed, ref } from 'vue';
import Button from '@/components/ui/Button.vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { fleetSession } from '@/lib/fleetSession';
import type { FleetInboxItemView } from '@/lib/types';
import { handoffErrorText } from '@/views/sessions/handoffErrors';

const props = defineProps<{ items: FleetInboxItemView[] }>();
const emit = defineEmits<{
  (e: 'opened', localSessionID: string): void;
  (e: 'changed'): void;
  (e: 'close'): void;
}>();

const client = useHarnessClient();
const busyID = ref('');
const errorByID = ref<Record<string, string>>({});

/** Why THIS device cannot receive shares, when its key isn't registered. */
const receiveWarning = computed(() => {
  const dk = fleetSession.value?.deviceKeys;
  if (!dk || dk.status === 'registered') return '';
  return dk.message ?? '';
});

function fromLabel(it: FleetInboxItemView): string {
  return it.senderEmail || 'A teammate';
}

function receivedLabel(it: FleetInboxItemView): string {
  const d = new Date(it.receivedAt);
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleString();
}

async function onOpen(it: FleetInboxItemView) {
  if (busyID.value) return;
  busyID.value = it.inboxItemID;
  errorByID.value = { ...errorByID.value, [it.inboxItemID]: '' };
  try {
    const res = await client.Handoff_Accept(it.inboxItemID);
    emit('changed');
    if (res.localSessionID) emit('opened', res.localSessionID);
  } catch (err) {
    errorByID.value = { ...errorByID.value, [it.inboxItemID]: handoffErrorText(err) };
  } finally {
    busyID.value = '';
  }
}

async function onDismiss(it: FleetInboxItemView) {
  if (busyID.value) return;
  busyID.value = it.inboxItemID;
  try {
    await client.Handoff_Delete(it.inboxItemID);
    emit('changed');
  } catch (err) {
    errorByID.value = { ...errorByID.value, [it.inboxItemID]: handoffErrorText(err) };
  } finally {
    busyID.value = '';
  }
}
</script>

<template>
  <div
    class="rounded-md border border-border-muted bg-surface-1 p-3 shadow-lg w-80"
    data-testid="handoff-inbox-panel"
    role="dialog"
    aria-label="Sessions shared with you"
  >
    <div class="flex items-center justify-between">
      <h3 class="font-ui text-xs uppercase tracking-[0.15em] text-ink-muted">Shared with you</h3>
      <button
        type="button"
        class="text-ink-muted hover:text-ink text-xs"
        aria-label="Close"
        data-testid="handoff-inbox-close"
        @click="emit('close')"
      >
        ×
      </button>
    </div>
    <p v-if="receiveWarning" class="mt-2 font-ui text-xs text-signal-warn" data-testid="handoff-receive-warning">
      {{ receiveWarning }}
    </p>
    <p v-if="props.items.length === 0" class="mt-2 font-ui text-xs text-ink-muted" data-testid="handoff-inbox-empty">
      Nothing waiting. Opened items leave this list.
    </p>
    <ul class="mt-2 space-y-2">
      <li
        v-for="it in props.items"
        :key="it.inboxItemID"
        class="rounded border border-border-muted px-2 py-1.5"
        :data-testid="`handoff-inbox-item-${it.inboxItemID}`"
      >
        <div class="font-ui text-sm text-ink truncate">{{ fromLabel(it) }}</div>
        <div class="font-ui text-[11px] text-ink-muted">{{ receivedLabel(it) }}</div>
        <p
          v-if="it.undecryptable"
          class="mt-1 font-ui text-[11px] text-ink-muted"
          data-testid="handoff-inbox-undecryptable"
        >
          Sent to a device key of yours that no longer exists, so it can't be opened on any device.
        </p>
        <p
          v-if="errorByID[it.inboxItemID]"
          class="mt-1 font-ui text-[11px] text-signal-danger"
          role="alert"
          data-testid="handoff-inbox-error"
        >
          {{ errorByID[it.inboxItemID] }}
        </p>
        <div class="mt-1 flex justify-end gap-2">
          <Button
            variant="ghost"
            :disabled="busyID !== ''"
            data-testid="handoff-inbox-dismiss"
            @click="onDismiss(it)"
          >
            Dismiss
          </Button>
          <Button
            variant="accent"
            :disabled="it.undecryptable || busyID !== ''"
            data-testid="handoff-inbox-open"
            @click="onOpen(it)"
          >
            {{ busyID === it.inboxItemID ? 'Opening…' : 'Open' }}
          </Button>
        </div>
      </li>
    </ul>
  </div>
</template>
