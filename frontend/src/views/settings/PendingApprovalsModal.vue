<script setup lang="ts">
/**
 * PendingApprovalsModal — steps through Fleet's pending-approvals hub
 * (ml-producer-01MLPRD01 WP06; kenaz-fleet docs/contract-pending-approvals.md
 * "Client behaviour").
 *
 * Opened from the settings banner's "N items need your approval" issue.
 * Per item: title, summary, what is paused (when required), then the body
 * text VERBATIM as plain text, or a link to the external document.
 *
 *   - legal (any item with a document): Approve stays disabled until the
 *     per-document box "I have read and accept <title> (<version>)" is
 *     ticked. The document opens in the system browser; it is not fetched.
 *   - ml_notice: the server-rendered notice, including any "Changed since
 *     you last approved" section; "I agree" sends the hub's approve action.
 *   - ml_exclusions_change: informational ("Dismiss"); the backend lists it
 *     only while nothing required is pending.
 *   - unknown kinds: rendered generically ("I agree" / "Got it").
 *
 * The approve action never reaches this component: it approves by id and
 * the backend re-reads the list and sends the action only if its path is in
 * the harness allowlist. An item whose action is outside the allowlist is
 * shown without an approve button. Nothing is ever auto-approved, and
 * closing approves nothing (the banner keeps showing the count).
 */
import { computed, onMounted, ref } from 'vue';
import BaseDialog from '@/components/ui/BaseDialog.vue';
import type { HarnessClient } from '@/lib/harnessClient';
import type { PendingApprovalItem } from '@/lib/types';

const props = defineProps<{ client: HarnessClient }>();
const emit = defineEmits<{ (e: 'close'): void }>();

const items = ref<PendingApprovalItem[]>([]);
const index = ref(0);
const loaded = ref(false);
const busy = ref(false);
const errorMsg = ref('');
const changed = ref(false);
/** Per-document "I have read and accept" ticks, keyed by item id. */
const accepted = ref<Record<string, boolean>>({});

const current = computed<PendingApprovalItem | null>(() => items.value[index.value] ?? null);

function errorText(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

function clampIndex(keepId?: string) {
  if (keepId) {
    const i = items.value.findIndex((it) => it.id === keepId);
    if (i >= 0) {
      index.value = i;
      return;
    }
  }
  if (index.value >= items.value.length) index.value = Math.max(0, items.value.length - 1);
}

async function load() {
  try {
    const v = await props.client.fleet.pendingApprovals();
    items.value = v.items ?? [];
    if (v.fleetError) errorMsg.value = v.fleetError;
    clampIndex();
  } catch (e) {
    errorMsg.value = errorText(e);
  } finally {
    loaded.value = true;
  }
}

onMounted(load);

/** Legal acceptance, or any item that points at an external document. */
function needsCheckbox(it: PendingApprovalItem): boolean {
  return it.kind === 'legal_acceptance' || !!it.documentUrl;
}

function checkboxLabel(it: PendingApprovalItem): string {
  return `I have read and accept ${it.title} (${it.version})`;
}

function approveLabel(it: PendingApprovalItem): string {
  if (it.kind === 'ml_exclusions_change') return 'Dismiss';
  if (needsCheckbox(it)) return 'Accept';
  return it.required ? 'I agree' : 'Got it';
}

const canApprove = computed(() => {
  const it = current.value;
  if (!it || busy.value || !it.approveAllowed) return false;
  return !needsCheckbox(it) || !!accepted.value[it.id];
});

function setAccepted(id: string, ev: Event) {
  accepted.value = { ...accepted.value, [id]: (ev.target as HTMLInputElement).checked };
}

function openDocument(it: PendingApprovalItem) {
  if (it.documentUrl) props.client.openExternalURL(it.documentUrl);
}

async function approve() {
  const it = current.value;
  if (!it || !canApprove.value) return;
  busy.value = true;
  errorMsg.value = '';
  changed.value = false;
  try {
    const v = await props.client.fleet.approveItem(it.id);
    items.value = v.items ?? [];
    changed.value = !!v.changed;
    // A stale item stays in view (with its fresh text); an approved one is
    // gone and the next item slides into its place.
    clampIndex(v.changed ? it.id : undefined);
    if (v.changed) accepted.value = { ...accepted.value, [it.id]: false };
  } catch (e) {
    errorMsg.value = errorText(e);
  } finally {
    busy.value = false;
  }
}

function prev() {
  if (index.value > 0) index.value -= 1;
  changed.value = false;
}

function next() {
  if (index.value < items.value.length - 1) index.value += 1;
  changed.value = false;
}
</script>

<template>
  <BaseDialog
    :open="true"
    title="Items that need your approval"
    panel-class="w-[560px] max-w-[92vw] max-h-[85vh] overflow-y-auto rounded-sm border border-border-muted bg-surface-1 p-5 text-ink"
    :close-on-overlay-click="false"
    @close="emit('close')"
  >
    <div class="space-y-3" data-testid="pending-approvals-modal">
      <div class="flex items-baseline justify-between gap-3">
        <h2 class="font-ui text-[13px] font-medium">Items that need your approval</h2>
        <span v-if="items.length > 1" class="text-[11px] text-ink-muted" data-testid="pending-approvals-step">
          {{ index + 1 }} of {{ items.length }}
        </span>
      </div>

      <p v-if="loaded && items.length === 0" class="text-[12px] text-ink-muted" data-testid="pending-approvals-empty">
        Nothing needs your approval right now.
      </p>

      <section v-if="current" class="space-y-2 text-[12px]" :data-testid="`pending-approval-${current.kind}`">
        <h3 class="text-[13px] font-medium" data-testid="pending-approval-title">{{ current.title }}</h3>
        <p class="text-ink-muted" data-testid="pending-approval-summary">{{ current.summary }}</p>
        <p v-if="current.required && current.blocking" class="text-signal-warn" data-testid="pending-approval-blocking">
          Paused until you approve: {{ current.blocking }}
        </p>
        <p v-if="changed" class="text-signal-warn" data-testid="pending-approval-changed">
          This changed since it was shown. Please read it again.
        </p>

        <p
          v-if="current.bodyText"
          class="whitespace-pre-wrap rounded-sm border border-border-muted p-3"
          data-testid="pending-approval-body"
        >{{ current.bodyText }}</p>

        <div v-if="current.documentUrl" class="space-y-1" data-testid="pending-approval-document">
          <p class="break-all text-ink-muted" data-testid="pending-approval-document-url">{{ current.documentUrl }}</p>
          <button
            type="button"
            class="px-3 py-1.5 rounded-sm border border-border-muted text-[11px] hover:bg-surface-2"
            data-testid="pending-approval-document-open"
            @click="openDocument(current)"
          >
            Open {{ current.title }}
          </button>
        </div>

        <p v-if="!current.approveAllowed" class="text-ink-muted" data-testid="pending-approval-not-allowed">
          This item can’t be approved from this app. Open your Kenaz Fleet dashboard to review it.
        </p>

        <label
          v-if="current.approveAllowed && needsCheckbox(current)"
          class="flex items-start gap-2"
          data-testid="pending-approval-accept"
        >
          <input
            type="checkbox"
            :checked="!!accepted[current.id]"
            :disabled="busy"
            data-testid="pending-approval-accept-box"
            @change="setAccepted(current.id, $event)"
          />
          <span>{{ checkboxLabel(current) }}</span>
        </label>
      </section>

      <p v-if="errorMsg" class="text-[12px] text-signal-danger" data-testid="pending-approvals-error">{{ errorMsg }}</p>

      <div class="flex items-center justify-between gap-2 pt-2">
        <div class="flex gap-2">
          <button
            v-if="items.length > 1"
            type="button"
            class="px-3 py-1.5 rounded-sm border border-border-muted text-[11px] hover:bg-surface-2 disabled:opacity-50"
            :disabled="index === 0"
            data-testid="pending-approvals-prev"
            @click="prev"
          >
            Back
          </button>
          <button
            v-if="items.length > 1"
            type="button"
            class="px-3 py-1.5 rounded-sm border border-border-muted text-[11px] hover:bg-surface-2 disabled:opacity-50"
            :disabled="index >= items.length - 1"
            data-testid="pending-approvals-next"
            @click="next"
          >
            Next
          </button>
        </div>
        <div class="flex gap-2">
          <button
            type="button"
            class="px-3 py-1.5 rounded-sm border border-border-muted text-[11px] hover:bg-surface-2"
            data-testid="pending-approvals-close"
            @click="emit('close')"
          >
            Close
          </button>
          <button
            v-if="current && current.approveAllowed"
            type="button"
            class="rounded px-3 py-1.5 text-[12px] font-ui bg-accent text-on-accent hover:bg-accent-hover disabled:opacity-50 disabled:cursor-not-allowed"
            :disabled="!canApprove"
            data-testid="pending-approval-approve"
            @click="approve"
          >
            {{ busy ? 'Working…' : approveLabel(current) }}
          </button>
        </div>
      </div>
    </div>
  </BaseDialog>
</template>
