<script setup lang="ts">
/**
 * UnitConflictsPanel — Fleet → Unit Sync settings section.
 *
 * Shows:
 *   - Last pull/push errors from the UnitSyncer (lastPullErr / lastPushErr).
 *   - The list of unresolved same-unit pull conflicts with MERGE / ENSHRINE
 *     resolution actions.
 *
 * Resolution is a two-step flow:
 *   1. Expand the conflict row to see the diff context (version numbers).
 *   2. Choose MERGE (write a combined body) or ENSHRINE (keep both).
 *
 * (fleet-integrity-observability WP08)
 */
import { ref, computed, onMounted } from 'vue';
import type { UnitConflictView, UnitSyncStatusView } from '@/lib/types';
import { useHarnessClient } from '@/lib/useHarnessAPI';

const client = useHarnessClient();

// ── State ──────────────────────────────────────────────────────────────────

const syncStatus = ref<UnitSyncStatusView | null>(null);
const conflicts = ref<UnitConflictView[]>([]);
const loading = ref(false);
const loadErr = ref('');

// Resolution form state (only one conflict at a time).
const resolving = ref<string | null>(null); // unit_id being resolved
const resolveMode = ref<'merge' | 'enshrine' | null>(null);
const mergeBody = ref('');
const enshrinedTitle = ref('');
const enshrinedBody = ref('');
const enshrinedReason = ref('');
const resolveSaving = ref(false);
const resolveErr = ref('');

// ── Load ────────────────────────────────────────────────────────────────────

async function load() {
  loading.value = true;
  loadErr.value = '';
  try {
    const [st, cs] = await Promise.all([
      client.Unit_SyncStatus(),
      client.Unit_ListConflicts(),
    ]);
    syncStatus.value = st;
    conflicts.value = cs;
  } catch (err) {
    loadErr.value = String(err);
  } finally {
    loading.value = false;
  }
}

onMounted(load);

// ── Computed ────────────────────────────────────────────────────────────────

const hasErrors = computed(() => {
  const st = syncStatus.value;
  return st && (st.lastPullErr || st.lastPushErr);
});

// ── Resolution actions ───────────────────────────────────────────────────────

function startResolve(unitID: string, mode: 'merge' | 'enshrine') {
  resolving.value = unitID;
  resolveMode.value = mode;
  mergeBody.value = '';
  enshrinedTitle.value = '';
  enshrinedBody.value = '';
  enshrinedReason.value = '';
  resolveErr.value = '';
}

function cancelResolve() {
  resolving.value = null;
  resolveMode.value = null;
}

async function submitResolve() {
  if (!resolving.value || !resolveMode.value) return;
  resolveSaving.value = true;
  resolveErr.value = '';
  try {
    if (resolveMode.value === 'merge') {
      await client.Unit_ResolveMerge(resolving.value, mergeBody.value);
    } else {
      await client.Unit_ResolveEnshrine(
        resolving.value,
        enshrinedTitle.value,
        enshrinedBody.value,
        enshrinedReason.value,
      );
    }
    cancelResolve();
    await load();
  } catch (err) {
    resolveErr.value = String(err);
  } finally {
    resolveSaving.value = false;
  }
}
</script>

<template>
  <section class="font-ui text-sm space-y-4" data-testid="unit-conflicts-panel">
    <!-- Sync error summary -->
    <div v-if="hasErrors" class="space-y-1">
      <p class="text-xs font-semibold text-signal-warn uppercase tracking-wide">Sync errors</p>
      <p
        v-if="syncStatus?.lastPullErr"
        class="text-xs text-signal-warn bg-signal-warn/10 rounded px-2 py-1"
        data-testid="unit-pull-err"
      >
        Pull: {{ syncStatus.lastPullErr }}
      </p>
      <p
        v-if="syncStatus?.lastPushErr"
        class="text-xs text-signal-warn bg-signal-warn/10 rounded px-2 py-1"
        data-testid="unit-push-err"
      >
        Push: {{ syncStatus.lastPushErr }}
      </p>
    </div>

    <!-- Conflict list -->
    <div>
      <div class="flex items-center gap-2 mb-2">
        <p class="text-xs font-semibold uppercase tracking-wide text-ink-subtle">
          Conflicts ({{ conflicts.length }})
        </p>
        <button
          type="button"
          class="text-xs text-accent hover:underline"
          :disabled="loading"
          @click="load"
        >
          Refresh
        </button>
      </div>
      <p v-if="loadErr" class="text-xs text-signal-danger">{{ loadErr }}</p>
      <p v-if="loading" class="text-xs text-ink-subtle">Loading…</p>
      <p v-else-if="conflicts.length === 0" class="text-xs text-ink-subtle">
        No unresolved conflicts.
      </p>

      <ul v-else class="space-y-2">
        <li
          v-for="c in conflicts"
          :key="c.unit_id"
          class="border border-border-muted rounded p-2 space-y-1"
          data-testid="conflict-row"
        >
          <p class="font-mono text-xs truncate" :title="c.unit_id">{{ c.unit_id }}</p>
          <p class="text-xs text-ink-subtle">
            Local v{{ c.local_version }} · Synced v{{ c.synced_version }} · Server v{{ c.server_version }}
          </p>

          <!-- Resolution actions -->
          <div v-if="resolving !== c.unit_id" class="flex gap-2 mt-1">
            <button
              type="button"
              class="text-xs border border-border-muted rounded px-2 py-0.5 hover:bg-surface-2"
              @click="startResolve(c.unit_id, 'merge')"
            >
              Merge
            </button>
            <button
              type="button"
              class="text-xs border border-border-muted rounded px-2 py-0.5 hover:bg-surface-2"
              @click="startResolve(c.unit_id, 'enshrine')"
            >
              Enshrine (keep both)
            </button>
          </div>

          <!-- Inline merge form -->
          <div v-else-if="resolveMode === 'merge'" class="space-y-1 mt-1">
            <p class="text-xs text-ink-subtle">
              Paste or type the resolved body:
            </p>
            <textarea
              v-model="mergeBody"
              rows="4"
              class="w-full text-xs font-mono border border-border-muted rounded p-1 bg-surface-1 resize-y"
              placeholder="Resolved content…"
            />
            <p v-if="resolveErr" class="text-xs text-signal-danger">{{ resolveErr }}</p>
            <div class="flex gap-2">
              <button
                type="button"
                class="text-xs rounded px-2 py-0.5 bg-accent text-white hover:bg-accent/90"
                :disabled="resolveSaving || !mergeBody"
                @click="submitResolve"
              >
                Save merge
              </button>
              <button
                type="button"
                class="text-xs text-ink-muted hover:text-ink"
                @click="cancelResolve"
              >
                Cancel
              </button>
            </div>
          </div>

          <!-- Inline enshrine form -->
          <div v-else-if="resolveMode === 'enshrine'" class="space-y-1 mt-1">
            <p class="text-xs text-ink-subtle">
              Create a coexisting unit that preserves both sides:
            </p>
            <input
              v-model="enshrinedTitle"
              type="text"
              class="w-full text-xs border border-border-muted rounded p-1 bg-surface-1"
              placeholder="Title for enshrined unit…"
            />
            <textarea
              v-model="enshrinedBody"
              rows="3"
              class="w-full text-xs font-mono border border-border-muted rounded p-1 bg-surface-1 resize-y"
              placeholder="Body of enshrined unit…"
            />
            <input
              v-model="enshrinedReason"
              type="text"
              class="w-full text-xs border border-border-muted rounded p-1 bg-surface-1"
              placeholder="Reason for enshrinement…"
            />
            <p v-if="resolveErr" class="text-xs text-signal-danger">{{ resolveErr }}</p>
            <div class="flex gap-2">
              <button
                type="button"
                class="text-xs rounded px-2 py-0.5 bg-accent text-white hover:bg-accent/90"
                :disabled="resolveSaving || !enshrinedTitle || !enshrinedBody"
                @click="submitResolve"
              >
                Save enshrine
              </button>
              <button
                type="button"
                class="text-xs text-ink-muted hover:text-ink"
                @click="cancelResolve"
              >
                Cancel
              </button>
            </div>
          </div>
        </li>
      </ul>
    </div>
  </section>
</template>
