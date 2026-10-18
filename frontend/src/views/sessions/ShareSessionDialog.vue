<script setup lang="ts">
/**
 * ShareSessionDialog — typeahead team-member picker + confirm for session handoff.
 *
 * Opens over the current session. Fetches the org team list via
 * Handoff_ListTeam, filters by display name / email, and calls
 * Handoff_Share on confirm.
 *
 * Privacy: no session content is passed through this component.
 * The actual re-encryption + upload happens inside the Go backend.
 *
 * Multi-device (device-keys-handoff-01DEVKH01 WP06): once a teammate is
 * picked, Handoff_RecipientDevices lists the devices the session will be
 * encrypted for, with their key fingerprints (fleet is a trusted key
 * directory — the fingerprint is what a cautious user compares). A key
 * rotation racing the send is re-wrapped and retried by the backend, so it
 * is invisible on success. Errors are the backend's human copy.
 *
 * (fleet-context-sync-01NDFSEX15 WP07)
 */

import { ref, computed, watch } from 'vue';
import BaseDialog from '@/components/ui/BaseDialog.vue';
import Button from '@/components/ui/Button.vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type { FleetRecipientDeviceView, FleetTeamMemberView } from '@/lib/types';
import { handoffErrorText } from '@/views/sessions/handoffErrors';

const props = defineProps<{
  open: boolean;
  sessionID: string;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'shared'): void;
}>();

const client = useHarnessClient();

// ── State ──────────────────────────────────────────────────────────────────

const query = ref('');
const team = ref<FleetTeamMemberView[]>([]);
const selected = ref<FleetTeamMemberView | null>(null);
const loading = ref(false);
const sharing = ref(false);
const errorMsg = ref('');
const devices = ref<FleetRecipientDeviceView[]>([]);
const devicesLoading = ref(false);
const devicesError = ref('');

// ── Load team when dialog opens ────────────────────────────────────────────

watch(
  () => props.open,
  async (isOpen) => {
    if (!isOpen) {
      query.value = '';
      selected.value = null;
      errorMsg.value = '';
      team.value = [];
      devices.value = [];
      devicesError.value = '';
      return;
    }
    loading.value = true;
    try {
      team.value = await client.Handoff_ListTeam();
    } catch (err) {
      errorMsg.value = handoffErrorText(err);
    } finally {
      loading.value = false;
    }
  },
  { immediate: true },
);

// ── Filtered list ──────────────────────────────────────────────────────────

const filtered = computed<FleetTeamMemberView[]>(() => {
  const q = query.value.toLowerCase().trim();
  if (!q) return team.value.filter((m) => m.canReceive);
  return team.value.filter(
    (m) =>
      m.canReceive &&
      (m.displayName.toLowerCase().includes(q) || m.email.toLowerCase().includes(q)),
  );
});

// ── Actions ────────────────────────────────────────────────────────────────

function selectMember(member: FleetTeamMemberView) {
  selected.value = member;
  query.value = member.displayName;
  void loadDevices(member.userID);
}

async function loadDevices(userID: string) {
  devices.value = [];
  devicesError.value = '';
  devicesLoading.value = true;
  try {
    const got = await client.Handoff_RecipientDevices(userID);
    if (selected.value?.userID === userID) devices.value = got ?? [];
  } catch (err) {
    if (selected.value?.userID === userID) devicesError.value = handoffErrorText(err);
  } finally {
    devicesLoading.value = false;
  }
}

function clearSelection() {
  selected.value = null;
  query.value = '';
  devices.value = [];
  devicesError.value = '';
}

const deviceSummary = computed(() => {
  const n = devices.value.length;
  if (n === 0) return '';
  return n === 1
    ? 'Encrypted for 1 device registered to this teammate.'
    : `Encrypted separately for each of this teammate's ${n} registered devices.`;
});

async function onShare() {
  if (!selected.value) return;
  sharing.value = true;
  errorMsg.value = '';
  try {
    await client.Handoff_Share(props.sessionID, selected.value.userID);
    emit('shared');
    emit('close');
  } catch (err) {
    errorMsg.value = handoffErrorText(err);
  } finally {
    sharing.value = false;
  }
}
</script>

<template>
  <BaseDialog
    :open="open"
    title="Share session"
    panel-class="w-full max-w-md rounded-md border border-border-muted bg-surface-1 p-5 shadow-xl"
    :close-on-overlay-click="true"
    @close="emit('close')"
  >
    <div data-testid="share-session-dialog">
      <h2 class="font-ui text-base font-semibold text-ink">
        Share session
      </h2>
      <p class="mt-1 font-ui text-sm text-ink-muted">
        The session is encrypted on this device for each of the recipient's registered devices
        before it leaves. Fleet only ever stores ciphertext.
      </p>

      <!-- Recipient search -->
      <div class="mt-4">
        <label for="share-recipient-input" class="block font-ui text-xs uppercase tracking-[0.15em] text-ink-muted mb-1">
          Recipient
        </label>
        <input
          id="share-recipient-input"
          v-model="query"
          type="text"
          placeholder="Search by name or email…"
          class="w-full rounded border border-border-muted bg-surface-2 px-3 py-1.5 font-ui text-sm text-ink placeholder:text-ink-subtle focus:outline-none focus:ring-1 focus:ring-accent"
          data-testid="share-recipient-input"
          autocomplete="off"
          @focus="clearSelection"
        />

        <!-- Loading indicator -->
        <p v-if="loading" class="mt-1 font-ui text-xs text-ink-muted" data-testid="share-team-loading">
          Loading team members…
        </p>

        <!-- Dropdown -->
        <ul
          v-else-if="!selected && filtered.length > 0 && query.length > 0"
          class="mt-1 rounded border border-border-muted bg-surface-1 shadow-lg max-h-48 overflow-y-auto"
          data-testid="share-member-list"
          role="listbox"
        >
          <li
            v-for="member in filtered"
            :key="member.userID"
            role="option"
            :aria-selected="false"
            class="flex flex-col px-3 py-2 cursor-pointer hover:bg-surface-2"
            :data-testid="`share-member-${member.userID}`"
            @click="selectMember(member)"
          >
            <span class="font-ui text-sm text-ink">{{ member.displayName }}</span>
            <span class="font-ui text-xs text-ink-muted">{{ member.email }}</span>
          </li>
        </ul>

        <!-- No results -->
        <p
          v-else-if="!selected && query.length > 0 && !loading && filtered.length === 0"
          class="mt-1 font-ui text-xs text-ink-muted"
          data-testid="share-no-results"
        >
          No matching team members.
        </p>

        <!-- Selected badge -->
        <div
          v-if="selected"
          class="mt-2 flex items-center gap-2 rounded border border-accent/40 bg-accent/10 px-3 py-1.5"
          data-testid="share-selected-member"
        >
          <span class="font-ui text-sm text-ink flex-1">{{ selected.displayName }}</span>
          <span class="font-ui text-xs text-ink-muted">{{ selected.email }}</span>
          <button
            type="button"
            class="text-ink-muted hover:text-ink ml-2 text-xs"
            aria-label="Clear selection"
            data-testid="share-clear-selection"
            @click="clearSelection"
          >
            ×
          </button>
        </div>
      </div>

      <!-- Recipient devices + fingerprints (trust model: show fingerprints) -->
      <div v-if="selected" class="mt-2" data-testid="share-recipient-devices">
        <p v-if="devicesLoading" class="font-ui text-xs text-ink-muted">Checking their devices…</p>
        <p v-else-if="devicesError" class="font-ui text-xs text-signal-warn" data-testid="share-devices-error">
          {{ devicesError }}
        </p>
        <details v-else-if="devices.length > 0" class="font-ui text-xs text-ink-muted">
          <summary class="cursor-pointer" data-testid="share-device-count">{{ deviceSummary }}</summary>
          <ul class="mt-1 space-y-0.5">
            <li
              v-for="d in devices"
              :key="d.keyID"
              class="font-mono text-[10px] break-all"
              data-testid="share-device-fingerprint"
            >
              {{ d.fingerprint }}
            </li>
          </ul>
        </details>
      </div>

      <!-- Error -->
      <p
        v-if="errorMsg"
        class="mt-3 font-ui text-xs text-signal-danger"
        data-testid="share-error"
        role="alert"
      >
        {{ errorMsg }}
      </p>

      <!-- Actions -->
      <div class="mt-5 flex justify-end gap-2">
        <Button variant="ghost" data-testid="share-cancel-btn" @click="emit('close')">
          Cancel
        </Button>
        <Button
          variant="accent"
          :disabled="!selected || sharing"
          data-testid="share-confirm-btn"
          @click="onShare"
        >
          {{ sharing ? 'Sharing…' : 'Share' }}
        </Button>
      </div>
    </div>
  </BaseDialog>
</template>
