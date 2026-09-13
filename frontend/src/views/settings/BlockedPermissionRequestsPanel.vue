<script setup lang="ts">
/**
 * BlockedPermissionRequestsPanel — Settings panel surfacing denied
 * permission requests (model-scheduled-jobs-01PMSJ01 WP07, FR-004's
 * second half — owner decision 2: "surfaced to the user next time they
 * open the app so they can grant a durable permit and re-run").
 *
 * Lists pending blocked_permission_requests rows with two actions:
 *   - Grant: writes a durable Cedar permit for the exact (action,
 *     resource) and moves the row to "granted".
 *   - Dismiss: moves the row to "dismissed" without granting anything —
 *     the next identical attempt is denied and recorded again.
 *
 * Rows whose origin is a scheduled chat run additionally show a
 * "Re-run" affordance calling the EXISTING ScheduledChat_RunNow binding
 * (no new RPC for this — see spec.md §5.6 point 3).
 *
 * Live-updates via TopicBlockedPermissionRequestPending so a request
 * blocked while this panel is already open appears without a manual
 * refresh.
 */
import { ref, onMounted } from 'vue';
import {
  createBlockedRequestsClient,
  type BlockedRequestsClient,
  type BlockedPermissionRequest,
} from '@/lib/blockedRequestsClient';
import { createScheduledChatClient, type ScheduledChatClient } from '@/lib/scheduledChatClient';
import { useEventStream } from '@/lib/useEventStream';

const props = defineProps<{
  client?: BlockedRequestsClient;
  chatClient?: ScheduledChatClient;
}>();

const client: BlockedRequestsClient = props.client ?? createBlockedRequestsClient();
const chatClient: ScheduledChatClient = props.chatClient ?? createScheduledChatClient();

const requests = ref<BlockedPermissionRequest[]>([]);
const loading = ref(false);
const actionError = ref<string | null>(null);
const busyIds = ref<Set<string>>(new Set());
const reRunResults = ref<Map<string, 'ok' | 'error'>>(new Map());

async function load() {
  loading.value = true;
  actionError.value = null;
  try {
    requests.value = await client.listPending();
  } catch (err) {
    actionError.value = err instanceof Error ? err.message : String(err);
  } finally {
    loading.value = false;
  }
}

onMounted(load);

// Live refresh — see TopicBlockedPermissionRequestPending's doc
// (core/rpc/stream_broker.go): payload is the FULL current pending set,
// not a delta.
useEventStream<BlockedPermissionRequest[]>(
  'policy:blocked-permission-request-pending',
  (payload) => {
    if (Array.isArray(payload)) {
      requests.value = payload;
    }
  },
);

function describeAction(r: BlockedPermissionRequest): string {
  return r.action === 'write_filesystem' ? 'Write' : r.action === 'read_filesystem' ? 'Read' : r.action;
}

async function grant(r: BlockedPermissionRequest) {
  if (busyIds.value.has(r.id)) return;
  busyIds.value.add(r.id);
  actionError.value = null;
  try {
    await client.grant(r.id);
    // Mark granted IN PLACE rather than removing the row: a scheduled-run
    // request still wants its Re-run affordance visible right after
    // granting — that is the whole point of owner decision 2's "grant a
    // durable permit and re-run" flow. An interactive-origin row has no
    // Re-run action, so it simply shows a "Granted" label until the next
    // list refresh drops it.
    const found = requests.value.find((x) => x.id === r.id);
    if (found) found.status = 'granted';
  } catch (err) {
    actionError.value = err instanceof Error ? err.message : String(err);
  } finally {
    busyIds.value.delete(r.id);
  }
}

async function dismiss(r: BlockedPermissionRequest) {
  if (busyIds.value.has(r.id)) return;
  busyIds.value.add(r.id);
  actionError.value = null;
  try {
    await client.dismiss(r.id);
    requests.value = requests.value.filter((x) => x.id !== r.id);
  } catch (err) {
    actionError.value = err instanceof Error ? err.message : String(err);
  } finally {
    busyIds.value.delete(r.id);
  }
}

async function reRun(r: BlockedPermissionRequest) {
  if (!r.originId || busyIds.value.has(r.id)) return;
  busyIds.value.add(r.id);
  try {
    await chatClient.runNow(r.originId);
    reRunResults.value.set(r.id, 'ok');
  } catch {
    reRunResults.value.set(r.id, 'error');
  } finally {
    busyIds.value.delete(r.id);
  }
}
</script>

<template>
  <section data-testid="blocked-permission-requests-panel">
    <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
      🚧 Blocked permission requests
    </h2>
    <p class="mt-1 font-ui text-[11px] text-ink-dim">
      Filesystem operations denied because no policy permitted them — including scheduled chat
      runs that had nobody to ask. Grant a durable permit, or dismiss without granting.
    </p>

    <div
      v-if="actionError"
      class="mt-2 rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
      role="alert"
      data-testid="blocked-requests-error"
    >
      {{ actionError }}
    </div>

    <p
      v-if="!loading && requests.length === 0"
      class="mt-3 font-ui text-[12px] text-ink-dim"
      data-testid="blocked-requests-empty"
    >
      Nothing pending.
    </p>

    <ul v-else class="mt-3 space-y-2" data-testid="blocked-requests-list">
      <li
        v-for="r in requests"
        :key="r.id"
        class="rounded-sm border border-border-muted bg-surface-1 px-3 py-2"
        :data-testid="`blocked-request-row-${r.id}`"
      >
        <div class="flex items-start justify-between gap-3">
          <div class="min-w-0">
            <div class="font-mono text-[12px] text-ink truncate" :title="r.resource">
              {{ describeAction(r) }}: {{ r.resource }}
            </div>
            <div class="mt-0.5 font-ui text-[11px] text-ink-dim">
              {{ r.origin === 'scheduled_chat_run' ? 'Scheduled chat run' : 'Interactive' }}
              — {{ r.reason }}
            </div>
          </div>
          <div class="flex shrink-0 items-center gap-1.5">
            <span
              v-if="r.status === 'granted'"
              class="font-ui text-[11px] uppercase tracking-[0.1em] text-signal-success"
              data-testid="blocked-request-granted-label"
            >
              Granted
            </span>
            <template v-else>
              <button
                type="button"
                class="rounded-sm border border-border-muted px-2 py-1 font-ui text-[11px] uppercase tracking-[0.1em] text-ink hover:bg-surface-2 disabled:opacity-50"
                :disabled="busyIds.has(r.id)"
                :data-testid="`blocked-request-grant-${r.id}`"
                @click="grant(r)"
              >
                Grant
              </button>
              <button
                type="button"
                class="rounded-sm border border-border-muted px-2 py-1 font-ui text-[11px] uppercase tracking-[0.1em] text-ink-muted hover:bg-surface-2 disabled:opacity-50"
                :disabled="busyIds.has(r.id)"
                :data-testid="`blocked-request-dismiss-${r.id}`"
                @click="dismiss(r)"
              >
                Dismiss
              </button>
            </template>
            <button
              v-if="r.origin === 'scheduled_chat_run' && r.originId"
              type="button"
              class="rounded-sm border border-accent px-2 py-1 font-ui text-[11px] uppercase tracking-[0.1em] text-accent hover:bg-accent hover:text-white disabled:opacity-50"
              :disabled="busyIds.has(r.id)"
              :data-testid="`blocked-request-rerun-${r.id}`"
              @click="reRun(r)"
            >
              Re-run
            </button>
          </div>
        </div>
        <p
          v-if="reRunResults.get(r.id) === 'ok'"
          class="mt-1 font-ui text-[11px] text-signal-success"
        >
          Re-run dispatched.
        </p>
        <p
          v-else-if="reRunResults.get(r.id) === 'error'"
          class="mt-1 font-ui text-[11px] text-signal-danger"
        >
          Re-run failed to dispatch.
        </p>
      </li>
    </ul>
  </section>
</template>
