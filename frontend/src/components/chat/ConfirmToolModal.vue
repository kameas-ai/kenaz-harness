<script setup lang="ts">
/**
 * ConfirmToolModal — the batched confirm-each tool-confirmation dialog
 * (confirm-each-enforcement-01PMAG05 WP02/WP03).
 *
 * ## What is actually happening behind this dialog
 *
 * A tool call whose permission verdict is `confirm_each` is PARKED
 * server-side: a goroutine is blocked on a channel with no deadline
 * (owner decision 1 — elapsed time resolves to nothing). This dialog is
 * the only thing that can unblock it. That single fact drives every
 * design choice below:
 *
 *  - Dismissing must CALL cancelBatch, not just unmount. Closing a
 *    dialog that owns a blocked goroutine and telling nobody leaves the
 *    turn hung forever. Dismissal is deny-all, never allow-all (FR-003).
 *  - Reloading must re-fetch. The pause lives in the harness process,
 *    not in this component, so `listPending` on mount rebuilds a dialog
 *    the browser forgot about.
 *  - Navigating away must leave a way back. The rows stay parked; the
 *    "waiting for approval" pill (see `hasParked` / the emitted state)
 *    is how the user finds the dialog again instead of watching a chat
 *    that appears to have stalled.
 *
 * ## Batching (owner decision 3)
 *
 * The kernel stamps one `batch_id` per turn of model-emitted tool calls,
 * so N parallel calls arrive as N events sharing one id. We render ONE
 * dialog for the OLDEST batch with one row per call — never a modal
 * storm, never a sequential interrogation. Rows resolve independently:
 * approving row 2 dispatches row 2 immediately while rows 1 and 3 stay
 * parked.
 *
 * ## The two "remember" controls are deliberately not alike
 *
 * "Allow for this session" is a checkbox on the row: low commitment,
 * process memory only, gone on restart. "Always allow" is a separate
 * button with its own confirm step, because it writes a durable policy
 * rule revoked from Settings → Permissions rather than from here. Owner
 * decision 2 asks for exactly this asymmetry — the destructive-to-undo
 * option must not be reachable by reflex.
 *
 * ## Session attribution
 *
 * The queue is CROSS-SESSION on purpose, matching the elicit precedent:
 * a background session's tool call is parked just as hard as the one in
 * front of you, and filtering it out of the dialog would leave it parked
 * with nothing to answer it. But an unlabelled row is worse than a
 * filtered one — a background session asking to run write_file reads as
 * the chat you are looking at asking to run write_file, and you approve
 * it on that understanding. So every row names its session, and rows
 * from a session OTHER than the active one are visually marked. The fix
 * is attribution, not filtering.
 *
 * ## Redaction
 *
 * `args_summary` is a STRUCTURAL description ("2 arguments: content
 * (string), path (string)"). Argument values never cross the RPC
 * boundary, so there is nothing here to pretty-print — showing the
 * summary verbatim is showing everything there is.
 */

import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useEventStream } from '@/lib/useEventStream';
import { useHarnessClient } from '@/lib/harnessClientContext';
import type { ToolConfirmPending } from '@/lib/types';

const props = withDefaults(
  defineProps<{
    /**
     * The session the user is currently looking at. Used ONLY to label
     * and mark rows — never to filter them; a row from another session
     * still has a goroutine blocked on it. Empty means "no session in
     * front", which marks every row as foreign, and that is the honest
     * reading.
     */
    activeSessionId?: string;
  }>(),
  { activeSessionId: '' },
);

const client = useHarnessClient();

// ── queue ──────────────────────────────────────────────────────────────

/** Every parked row, in arrival order. May span multiple batches. */
const rows = ref<ToolConfirmPending[]>([]);
const submitting = ref(false);
const lastError = ref<string | null>(null);

/**
 * Rows the user chose to leave for later by navigating away. The pause
 * is untouched — this only hides the dialog, and `hasParked` keeps the
 * "waiting for approval" affordance visible so it can be reopened.
 */
const minimized = ref(false);

function addRow(row: ToolConfirmPending | null | undefined) {
  if (!row?.call_id) return;
  // Dedup: the reconnect snapshot and the live topic can both deliver
  // the same row, and answering a row twice is an error server-side.
  if (rows.value.some((r) => r.call_id === row.call_id)) return;
  rows.value = [...rows.value, row];
  // A newly-arrived confirmation re-opens a minimized dialog: the model
  // is asking about something the user has not seen yet.
  minimized.value = false;
}

useEventStream<ToolConfirmPending>('tool:confirm-pending', addRow);

/**
 * The batch currently on screen: the oldest one still parked. Later
 * batches queue behind it and surface as soon as this one clears.
 */
const activeBatchID = computed<string | null>(() => rows.value[0]?.batch_id ?? null);

const activeRows = computed<ToolConfirmPending[]>(() =>
  activeBatchID.value === null
    ? []
    : rows.value.filter((r) => r.batch_id === activeBatchID.value),
);

/** Rows parked behind the visible batch. */
const queuedBehind = computed<number>(
  () => rows.value.length - activeRows.value.length,
);

/** Whether anything at all is parked — drives the "waiting" affordance. */
const hasParked = computed<boolean>(() => rows.value.length > 0);

const open = computed<boolean>(() => hasParked.value && !minimized.value);

/** Per-row "allow for this session" ticks, keyed by call_id. */
const remember = ref<Record<string, boolean>>({});

function rememberFor(callID: string): boolean {
  return remember.value[callID] === true;
}

function toggleRemember(callID: string, value: boolean) {
  remember.value = { ...remember.value, [callID]: value };
  // Any other interaction with the row disarms "always allow". An armed
  // button is a loaded second click: if the user's next act was to reach
  // for the session checkbox instead, they have changed their mind, and
  // the durable control must not stay one click from firing.
  disarmAll();
}

/**
 * The call_id currently armed for "always allow", or null.
 *
 * At most ONE row can be armed. A map would let several buttons sit
 * loaded at once, so a user who armed row 1, moved on, and later
 * double-tapped row 3 could persist two rules having consciously
 * confirmed neither.
 */
const armedAlways = ref<string | null>(null);

function isArmed(callID: string): boolean {
  return armedAlways.value === callID;
}

function disarmAll() {
  armedAlways.value = null;
}

function dropRows(callIDs: string[]) {
  const gone = new Set(callIDs);
  rows.value = rows.value.filter((r) => !gone.has(r.call_id));
  const nextRemember = { ...remember.value };
  for (const id of callIDs) delete nextRemember[id];
  remember.value = nextRemember;
  if (armedAlways.value !== null && gone.has(armedAlways.value)) {
    disarmAll();
  }
}

// ── session attribution ────────────────────────────────────────────────

/**
 * session_id → display name. Populated lazily from the sessions list;
 * rows fall back to a short id, so a failed lookup degrades to a less
 * readable label rather than to no attribution at all.
 */
const sessionNames = ref<Record<string, string>>({});

/** Short, stable id for display when no name is known. */
function shortID(id: string): string {
  return id.length <= 12 ? id : `${id.slice(0, 8)}…${id.slice(-3)}`;
}

function sessionLabel(sessionID: string): string {
  if (!sessionID) return 'unknown session';
  return sessionNames.value[sessionID] ?? shortID(sessionID);
}

function isForeign(sessionID: string): boolean {
  return sessionID !== props.activeSessionId;
}

/**
 * Fetch display names for any parked session we cannot yet name. One
 * list call covers every row; failures are silent because the fallback
 * (a short id) is already a correct label.
 */
async function hydrateSessionNames() {
  const missing = rows.value
    .map((r) => r.session_id)
    .filter((id) => id && sessionNames.value[id] === undefined);
  if (missing.length === 0) return;
  try {
    const sessions = await client.sessions.list();
    const next = { ...sessionNames.value };
    for (const s of sessions) {
      if (s?.id) next[s.id] = s.name;
    }
    sessionNames.value = next;
  } catch {
    // Short ids remain. Attribution survives; only readability suffers.
  }
}

watch(rows, () => void hydrateSessionNames(), { deep: false });

// ── reconnect reconciliation ───────────────────────────────────────────

/**
 * Reconcile the dialog against the server's parked set — a set DIFF, not
 * a union.
 *
 * Adding what the server has is only half of it. The other half is
 * dropping what it does not: a row answered in another window, or by an
 * approve-all that raced, is gone server-side while this component still
 * renders a button for it. Clicking that button rejects with
 * ErrUnknownConfirmation and shows the user an error for a row that was
 * resolved perfectly well. Union-only reconciliation makes a stale row
 * permanent, because nothing else ever removes it.
 *
 * Rows are only dropped when the server ANSWERS. A failed call leaves
 * the queue alone: "I could not reach the harness" must never be read as
 * "nothing is parked", which would hide a live confirmation.
 */
async function reconcile() {
  let pending: ToolConfirmPending[];
  try {
    pending = await client.confirm.listPending('');
  } catch {
    // Best-effort. The live topic still delivers anything that parks
    // from here on, and the rows we could not fetch are still parked
    // server-side — so the safe move is to change nothing.
    return;
  }
  const live = new Set(pending.map((r) => r.call_id));
  const stale = rows.value.filter((r) => !live.has(r.call_id)).map((r) => r.call_id);
  if (stale.length > 0) dropRows(stale);
  for (const row of pending) addRow(row);
}

onMounted(() => {
  void reconcile();
  window.addEventListener('beforeunload', onBeforeUnload);
  document.addEventListener('keydown', onKeydown);
});

onBeforeUnmount(() => {
  window.removeEventListener('beforeunload', onBeforeUnload);
  document.removeEventListener('keydown', onKeydown);
});

/**
 * Window close = explicit dismissal = deny-all (FR-003).
 *
 * Fire-and-forget: `beforeunload` will not wait for a promise. The call
 * is dispatched and the page goes away; if it does not land, the rows
 * simply stay parked, which is the fail-safe direction. What must never
 * happen is the reverse — a closing window must not approve anything.
 */
function onBeforeUnload() {
  const batchID = activeBatchID.value;
  if (!batchID) return;
  void client.confirm.cancelBatch(batchID, 'window closed').catch(() => {});
}

// ── decisions ──────────────────────────────────────────────────────────

/**
 * True when an error means "that row is already resolved" rather than
 * "the call failed".
 *
 * Matched on the message because the error crosses the Wails / HTTP
 * boundary as a string in both transports; there is no error code to key
 * on. Kept deliberately narrow — a mismatch here degrades to showing the
 * user a message, which is the status quo, whereas matching too broadly
 * would silently drop rows that are still parked.
 */
function isAlreadyResolved(err: unknown): boolean {
  const msg = (err instanceof Error ? err.message : String(err)).toLowerCase();
  return msg.includes('unknown or already-resolved');
}

async function run<T>(fn: () => Promise<T>, onDone: () => void) {
  if (submitting.value) return;
  submitting.value = true;
  lastError.value = null;
  try {
    await fn();
    onDone();
  } catch (err) {
    if (isAlreadyResolved(err)) {
      // Someone else got there first — another window, or an approve-all
      // that raced this click. The row IS resolved; the only wrong thing
      // on screen is the row itself. Drop it silently rather than
      // reporting a failure that did not happen.
      onDone();
      // Re-check the server: if this row was stale, others may be too.
      void reconcile();
    } else {
      lastError.value = err instanceof Error ? err.message : String(err);
    }
  } finally {
    submitting.value = false;
  }
}

async function approve(row: ToolConfirmPending) {
  disarmAll();
  await run(
    () =>
      client.confirm.resolve(
        row.session_id,
        row.call_id,
        true,
        'approved by user',
        rememberFor(row.call_id),
      ),
    () => dropRows([row.call_id]),
  );
}

async function deny(row: ToolConfirmPending) {
  disarmAll();
  await run(
    () =>
      client.confirm.resolve(row.session_id, row.call_id, false, 'denied by user', false),
    () => dropRows([row.call_id]),
  );
}

/**
 * "Always allow" is two clicks by design: the first arms the row and
 * swaps the label to say what is about to happen, the second commits.
 * Owner decision 2 wants this control visually separated so it cannot be
 * hit by reflex; an arming step is the same intent in the time domain.
 */
async function alwaysAllow(row: ToolConfirmPending) {
  if (!isArmed(row.call_id)) {
    // Arming a row disarms whatever was armed before it.
    armedAlways.value = row.call_id;
    return;
  }
  await run(
    () =>
      client.confirm.resolveAlways(row.session_id, row.call_id, 'always allow (persisted)'),
    () => dropRows([row.call_id]),
  );
}

async function approveAll() {
  disarmAll();
  const batchID = activeBatchID.value;
  if (!batchID) return;
  const ids = activeRows.value.map((r) => r.call_id);
  // A batch-level "remember" applies only where the user ticked the row.
  // Rather than guess, approve-all carries remember=false and the ticked
  // rows are approved individually first.
  const ticked = activeRows.value.filter((r) => rememberFor(r.call_id));
  await run(
    async () => {
      for (const row of ticked) {
        await client.confirm.resolve(
          row.session_id,
          row.call_id,
          true,
          'approved by user',
          true,
        );
      }
      await client.confirm.approveBatch(batchID, false);
    },
    () => dropRows(ids),
  );
}

/** Explicit dismissal: deny every remaining row in the batch (FR-003). */
async function dismiss() {
  disarmAll();
  const batchID = activeBatchID.value;
  if (!batchID) return;
  const ids = activeRows.value.map((r) => r.call_id);
  await run(
    () => client.confirm.cancelBatch(batchID, 'dismissed by user'),
    () => dropRows(ids),
  );
}

/**
 * Leave the pause intact and get out of the way. Nothing is resolved —
 * the run stays parked and the "waiting for approval" affordance stays
 * on screen so the dialog can be reopened.
 */
function decideLater() {
  minimized.value = true;
}

function reopen() {
  minimized.value = false;
}

// ── keyboard ───────────────────────────────────────────────────────────

function onKeydown(e: KeyboardEvent) {
  if (!open.value) return;
  if (e.key === 'Escape') {
    e.preventDefault();
    // Escape is a dismissal, and dismissal denies. It does NOT silently
    // hide the dialog: leaving a blocked goroutine behind an invisible
    // dialog is the failure mode this whole component exists to avoid.
    void dismiss();
  }
}

// Reset the transient per-row UI state whenever the visible batch changes
// so an arming click cannot survive into the next batch's rows.
watch(activeBatchID, () => {
  disarmAll();
  lastError.value = null;
});

defineExpose({
  rows,
  activeRows,
  activeBatchID,
  reconcile,
  sessionLabel,
  isForeign,
  approve,
  deny,
  alwaysAllow,
  approveAll,
  dismiss,
  decideLater,
  reopen,
  remember,
});
</script>

<template>
  <!-- Waiting affordance: shown whenever something is parked but the
       dialog is minimized. The run is paused; this is the way back. -->
  <button
    v-if="hasParked && minimized"
    type="button"
    class="fixed bottom-4 right-4 z-50 rounded-md border border-signal-warn bg-surface-1 px-3 py-2 font-ui text-[11px] uppercase tracking-[0.18em] text-signal-warn shadow-xl hover:bg-surface-2"
    data-testid="confirm-tool-waiting-pill"
    @click="reopen"
  >
    Waiting for approval · {{ rows.length }}
  </button>

  <div
    v-if="open"
    class="fixed inset-0 z-50 flex items-center justify-center"
    role="dialog"
    aria-modal="true"
    aria-labelledby="confirm-tool-modal-title"
    data-testid="confirm-tool-modal"
  >
    <!-- Overlay. Clicking it does NOT dismiss: a stray click must not
         deny a batch of tool calls. -->
    <div class="absolute inset-0 bg-modal-overlay" />

    <div
      class="relative flex w-full max-w-2xl flex-col rounded-md border border-accent-hairline bg-surface-1 p-5 shadow-xl"
    >
      <div class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
        Tool confirmation required
      </div>
      <h2
        id="confirm-tool-modal-title"
        class="mt-1 font-ui text-sm font-semibold text-ink"
      >
        {{ activeRows.length === 1 ? 'The model wants to run 1 tool' : `The model wants to run ${activeRows.length} tools` }}
      </h2>
      <p class="mt-1 font-ui text-[12px] text-ink-muted">
        Nothing runs until you decide. Leaving this open keeps the turn paused.
      </p>

      <!-- Rows -->
      <ul class="mt-4 flex max-h-[52vh] flex-col gap-2 overflow-y-auto" data-testid="confirm-tool-rows">
        <li
          v-for="row in activeRows"
          :key="row.call_id"
          class="rounded-sm border bg-surface-2 p-3"
          :class="isForeign(row.session_id)
            ? 'border-l-2 border-signal-warn/60 border-y-border-muted border-r-border-muted'
            : 'border-border-muted'"
          :data-foreign-session="isForeign(row.session_id) ? 'true' : 'false'"
          :data-testid="`confirm-tool-row-${row.call_id}`"
        >
          <div class="flex items-baseline justify-between gap-3">
            <div class="min-w-0">
              <!-- Session attribution. A row from a background session
                   reads as the chat in front of you unless it says
                   otherwise, and you approve it on that misreading. -->
              <div
                class="mb-1 flex items-center gap-1.5 font-ui text-[10px] uppercase tracking-[0.18em]"
                :class="isForeign(row.session_id) ? 'text-signal-warn' : 'text-ink-subtle'"
                :data-testid="`confirm-tool-session-${row.call_id}`"
              >
                <span class="truncate">{{ sessionLabel(row.session_id) }}</span>
                <span
                  v-if="isForeign(row.session_id)"
                  :data-testid="`confirm-tool-foreign-${row.call_id}`"
                >· other session</span>
              </div>
              <div class="truncate font-mono text-[12px] text-accent">
                {{ row.server }}.{{ row.tool }}
              </div>
              <!-- Structural summary only — never argument values. -->
              <div
                class="mt-1 truncate font-ui text-[11px] text-ink-muted"
                :data-testid="`confirm-tool-args-${row.call_id}`"
                :title="row.args_summary"
              >
                {{ row.args_summary }}
              </div>
              <div
                v-if="row.reason"
                class="mt-1 font-ui text-[11px] text-ink-subtle"
              >
                {{ row.reason }}
              </div>
            </div>
          </div>

          <div class="mt-3 flex flex-wrap items-center gap-2">
            <button
              type="button"
              class="rounded-md border border-accent bg-accent/10 px-3 py-1.5 font-ui text-[11px] uppercase tracking-[0.18em] text-accent hover:bg-accent/20 disabled:cursor-not-allowed disabled:opacity-50"
              :disabled="submitting"
              :data-testid="`confirm-tool-approve-${row.call_id}`"
              @click="approve(row)"
            >
              Approve
            </button>
            <button
              type="button"
              class="rounded-md border border-border-muted px-3 py-1.5 font-ui text-[11px] uppercase tracking-[0.18em] text-ink-muted hover:bg-surface-1 disabled:cursor-not-allowed disabled:opacity-50"
              :disabled="submitting"
              :data-testid="`confirm-tool-deny-${row.call_id}`"
              @click="deny(row)"
            >
              Deny
            </button>
            <label
              class="flex cursor-pointer items-center gap-1.5 font-ui text-[11px] text-ink-muted"
              :for="`confirm-tool-remember-${row.call_id}`"
              :data-testid="`confirm-tool-remember-label-${row.call_id}`"
            >
              <input
                :id="`confirm-tool-remember-${row.call_id}`"
                type="checkbox"
                :checked="rememberFor(row.call_id)"
                :disabled="submitting"
                :data-testid="`confirm-tool-remember-${row.call_id}`"
                @change="toggleRemember(row.call_id, ($event.target as HTMLInputElement).checked)"
              >
              Allow for this session
            </label>
          </div>

          <!-- The durable grant. Separated by a rule and its own row so
               it cannot be hit while aiming for Approve. -->
          <div class="mt-3 border-t border-border-muted pt-2">
            <button
              type="button"
              class="rounded-md border px-3 py-1.5 font-ui text-[10px] uppercase tracking-[0.18em] disabled:cursor-not-allowed disabled:opacity-50"
              :class="isArmed(row.call_id)
                ? 'border-signal-warn bg-signal-warn/10 text-signal-warn'
                : 'border-border-muted text-ink-subtle hover:bg-surface-1'"
              :disabled="submitting"
              :data-testid="`confirm-tool-always-${row.call_id}`"
              @click="alwaysAllow(row)"
            >
              {{ isArmed(row.call_id)
                ? 'Confirm: always allow, saved until revoked'
                : 'Always allow this tool…' }}
            </button>
            <span
              v-if="isArmed(row.call_id)"
              class="ml-2 font-ui text-[10px] text-ink-subtle"
            >
              Writes a permission rule. Revoke in Settings → Permissions.
            </span>
          </div>
        </li>
      </ul>

      <div
        v-if="lastError"
        class="mt-3 rounded-sm border border-signal-warn bg-surface-2 px-3 py-2 font-ui text-[12px] text-signal-warn"
        role="alert"
        data-testid="confirm-tool-error"
      >
        {{ lastError }}
      </div>

      <!-- Batch actions -->
      <div class="mt-5 flex flex-wrap items-center gap-2">
        <button
          v-if="activeRows.length > 1"
          type="button"
          class="rounded-md border border-accent bg-accent/10 px-4 py-2 font-ui text-[11px] uppercase tracking-[0.18em] text-accent hover:bg-accent/20 disabled:cursor-not-allowed disabled:opacity-50"
          :disabled="submitting"
          data-testid="confirm-tool-approve-all"
          @click="approveAll"
        >
          Approve all {{ activeRows.length }}
        </button>
        <button
          type="button"
          class="rounded-md border border-border-muted px-4 py-2 font-ui text-[11px] uppercase tracking-[0.18em] text-ink-muted hover:bg-surface-2 disabled:cursor-not-allowed disabled:opacity-50"
          :disabled="submitting"
          data-testid="confirm-tool-dismiss"
          @click="dismiss"
        >
          Deny all
        </button>
        <button
          type="button"
          class="rounded-md px-4 py-2 font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle hover:bg-surface-2 disabled:cursor-not-allowed disabled:opacity-50"
          :disabled="submitting"
          data-testid="confirm-tool-decide-later"
          @click="decideLater"
        >
          Decide later
        </button>
      </div>

      <div
        v-if="queuedBehind > 0"
        class="mt-3 font-ui text-[10px] uppercase tracking-[0.18em] text-ink-subtle"
        data-testid="confirm-tool-queue-badge"
      >
        +{{ queuedBehind }} more waiting in another batch
      </div>
    </div>
  </div>
</template>
