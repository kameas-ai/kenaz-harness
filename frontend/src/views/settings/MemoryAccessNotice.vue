<script setup lang="ts">
/**
 * MemoryAccessNotice — the memory-access notice in Settings → Sync's memory
 * panel (kenaz-fleet spec 017 T23, contract-harness-sync §17.3–§17.5,
 * legal D168(2)).
 *
 * Renders nothing until Fleet serves a memory-access text for the
 * kenaz-harness surface (today every request answers 404, so this stays
 * hidden). Once served, the text is shown verbatim — Fleet's words, the
 * backend has checked their hash — with one control that records the read:
 * "I have read this". Never "I agree", "Accept" or "Consent" (§17.5). After an
 * ack, a withdraw control says what it does.
 */
import { computed, onMounted, ref } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type { MemberNotice, MemberNotices } from '@/lib/types';

defineProps<{
  /** The memory push is waiting on this notice (MemorySyncStatus.noticeHold). */
  holdActive?: boolean;
}>();
const emit = defineEmits<{ (e: 'changed'): void }>();

/** The two control labels, fixed by §17.5. */
const READ_LABEL = 'I have read this';
const WITHDRAW_LABEL = 'Stop team leads and admins seeing my notes';

const client = useHarnessClient();
const notice = ref<MemberNotice | null>(null);
const stateError = ref('');
const busy = ref(false);
const errorMsg = ref('');

function apply(v: MemberNotices) {
  notice.value = v.notices.find((n) => n.family === 'memory-access') ?? null;
  stateError.value = v.stateError ?? '';
}

async function load() {
  try {
    apply(await client.fleet.memberNotices());
  } catch {
    // Not available here (served mode, signed out): show nothing.
    notice.value = null;
  }
}

onMounted(load);

/** The backend's refusals are plain sentences in lower case; show them as such. */
function plain(err: unknown): string {
  const msg = (err instanceof Error ? err.message : String(err)).trim();
  if (!msg) return 'Something went wrong. Please try again.';
  const s = msg.charAt(0).toUpperCase() + msg.slice(1);
  return /[.!?]$/.test(s) ? s : `${s}.`;
}

// Offer the read control until this exact edition stands acked; offer the
// withdraw control only while it does.
const readCurrent = computed(
  () => !!notice.value && notice.value.action === 'ack' && notice.value.rowEdition === notice.value.edition,
);
const withdrawn = computed(() => notice.value?.action === 'withdraw');

const stateLine = computed<string>(() => {
  const n = notice.value;
  if (!n) return '';
  if (readCurrent.value) return 'You have read this notice.';
  if (withdrawn.value) return "You've stopped team leads and admins seeing your notes.";
  return '';
});

async function respond(action: 'ack' | 'withdraw') {
  const n = notice.value;
  if (!n || busy.value) return;
  busy.value = true;
  errorMsg.value = '';
  try {
    apply(await client.fleet.respondMemberNotice(n.family, n.edition, n.textHash, action));
    emit('changed');
  } catch (err) {
    errorMsg.value = plain(err);
    await load(); // the text may have changed; show the current one
  } finally {
    busy.value = false;
  }
}

</script>

<template>
  <div
    v-if="notice"
    class="space-y-2 rounded-sm border border-border-muted p-3 text-[12px]"
    data-testid="memory-access-notice"
  >
    <p class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">Notice</p>
    <p v-if="holdActive" class="text-signal-warn" data-testid="memory-access-notice-hold">
      Memory sync is waiting until you read this notice. Your notes stay on this device until then.
    </p>
    <p class="whitespace-pre-wrap" data-testid="memory-access-notice-text">{{ notice.text }}</p>
    <p v-if="stateLine" class="text-ink-muted" data-testid="memory-access-notice-state">{{ stateLine }}</p>
    <p v-if="stateError" class="text-ink-muted" data-testid="memory-access-notice-state-error">{{ stateError }}</p>
    <div class="flex flex-wrap gap-2">
      <button
        v-if="!readCurrent"
        type="button"
        class="px-3 py-1.5 rounded-sm border border-border-muted text-[11px] uppercase tracking-[0.18em] hover:bg-surface-2 disabled:opacity-50 disabled:cursor-not-allowed"
        :disabled="busy"
        data-testid="memory-access-notice-read"
        @click="respond('ack')"
      >
        {{ READ_LABEL }}
      </button>
      <button
        v-else
        type="button"
        class="px-3 py-1.5 text-[11px] uppercase tracking-[0.18em] text-ink-dim hover:text-accent disabled:opacity-50 disabled:cursor-not-allowed"
        :disabled="busy"
        data-testid="memory-access-notice-withdraw"
        @click="respond('withdraw')"
      >
        {{ WITHDRAW_LABEL }}
      </button>
    </div>
    <p v-if="errorMsg" class="text-signal-danger" data-testid="memory-access-notice-error">{{ errorMsg }}</p>
  </div>
</template>
