<script setup lang="ts">
/**
 * AuditView — append-only event-log viewer (NN/SECTION pattern).
 *
 * Wires the audit RPC + audit:event topic into a paginated, filterable
 * stream of EventStreamRow primitives. Server-side redaction has
 * already run before any Entry reaches this view (privacy CI invariant
 * #2); the view renders entries verbatim and never re-renders the raw
 * payload.
 *
 * WP04: rich filter rail (kind/category, time range, actor, free-text,
 * verbose toggle, saved queries). WP02: verify-chain button + pill.
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import CanvasHead from '@/shell/CanvasHead.vue';
import EventStreamRow from '@/components/ui/EventStreamRow.vue';
import AuditEventDrawer from './AuditEventDrawer.vue';
import { useHarnessClient, useEventLogStream } from '@/lib/useHarnessAPI';
import { CATEGORIES, type Category } from '@/lib/categories';
import type { AuditEntry, AuditFilter, AuditFilterQuery, SavedAuditQuery, AuditExportOptions } from '@/lib/types';
import { defaultAuditSince, defaultAuditUntil } from '@/lib/auditDateFilters';

const client = useHarnessClient();

// ── Filter state ────────────────────────────────────────────────────────
// audit-that-tells-the-truth-01PMZA10 UNIT-6 (WP08): selectedCategories
// and actorIds are ARRAYS — the whole reason a saved query with two
// kinds and two actors used to get silently narrowed to one of each on
// load, then that narrowed (destroyed) version got persisted back on
// the next Save. selectedCategory (singular) is gone; nothing reads
// only element [0] anymore.
const selectedCategories = ref<string[]>([]);
// WP11 / FR-001: default Since = last-7-days (UTC midnight); Until = ''
// (open-ended, maps to undefined in the filter → no upper bound, always
// >= Since). Placeholder text in the template uses neutral "YYYY-MM-DD"
// rather than a hard-coded past date so new users aren't confused.
const sinceInput = ref<string>(defaultAuditSince());
const untilInput = ref<string>(defaultAuditUntil());
// Comma-separated free text, parsed into actorIds below — a multi-select
// widget is overkill for a field with no fixed value set (emitter ids
// are open-ended, unlike Kind/category).
const actorInput = ref<string>('');
const actorIds = computed<string[]>(() =>
  actorInput.value
    .split(',')
    .map((s) => s.trim())
    .filter((s) => s.length > 0),
);
const freeText = ref<string>('');
const verboseToggle = ref<boolean>(false);
const selectedSavedQuery = ref<string>('');

// Saved queries loaded from server.
const savedQueries = ref<SavedAuditQuery[]>([]);
const saveQueryName = ref<string>('');
const saveQueryError = ref<string>('');

// The narrow AuditFilter — used ONLY for StartStream (the live-tail
// subscription), whose Go-side Filter wire type is not multi-category
// (core/rpc/views/audit/api.go's Filter struct). Widening that is a
// Go wire-type change (bindings regeneration) outside this WP's scope;
// the live stream stays best-effort single-category ([0]) while the
// SEEDED/historical fetch below (refresh, via audit.filter()) is fully
// multi-term. StartStream already unions with the ring/store
// independently per event, so this narrowing affects only which NEW
// live events are pre-filtered client-side, not what a saved query
// persists.
const filter = computed<AuditFilter>(() => ({
  categories: selectedCategories.value.length > 0 ? [selectedCategories.value[0]] : undefined,
  since: sinceInput.value || undefined,
  until: untilInput.value || undefined,
  limit: 500,
}));

const richFilter = computed<AuditFilterQuery>(() => ({
  since: sinceInput.value || undefined,
  until: untilInput.value || undefined,
  kinds: selectedCategories.value.length > 0 ? selectedCategories.value : undefined,
  actor_ids: actorIds.value.length > 0 ? actorIds.value : undefined,
  free_text: freeText.value || undefined,
  verbose: verboseToggle.value,
  limit: 500,
}));

// ── Entry state ─────────────────────────────────────────────────────────
const seeded = ref<readonly AuditEntry[]>([]);
const verifyResult = ref<null | { ok: boolean; checked: number; brokenAt?: string }>(null);
const loading = ref(false);

// Selection state (for WP08 bulk-purge; pre-wired here).
const selectedIDs = ref<Set<string>>(new Set());

// Drawer state.
const drawerEntry = ref<AuditEntry | null>(null);

// Export state.
const exportFormat = ref<'csv' | 'jsonl' | 'pdf'>('jsonl');
const exportToast = ref<string>('');

// audit-that-tells-the-truth-01PMZA10 UNIT-6 (WP07): the seeded/
// historical fetch now goes through audit.filter() — the complete,
// multi-term backend (core/rpc/views/audit/impl.go's Filter method) —
// instead of the narrow audit.listEntries(), which is single-category
// only. Before this change audit.filter() had no caller anywhere in
// the frontend despite being a fully-implemented RPC method.
async function refresh() {
  loading.value = true;
  try {
    seeded.value = await client.audit.filter(richFilter.value);
  } catch {
    seeded.value = [];
  } finally {
    loading.value = false;
  }
}

watch(richFilter, () => {
  void refresh();
}, { immediate: true });

const stream = useEventLogStream(filter);

// Display: union of the seeded snapshot with the live tail. Newest first.
const entries = computed<readonly AuditEntry[]>(() => {
  const seen = new Set<string>();
  const out: AuditEntry[] = [];
  for (const e of stream.events.value) {
    if (seen.has(e.id)) continue;
    seen.add(e.id);
    out.push(e);
  }
  for (const e of seeded.value) {
    if (seen.has(e.id)) continue;
    seen.add(e.id);
    out.push(e);
  }
  return out.sort((a, b) =>
    a.timestamp < b.timestamp ? 1 : a.timestamp > b.timestamp ? -1 : 0,
  );
});

// ── Controls ─────────────────────────────────────────────────────────────
function onTogglePause() {
  if (stream.paused.value) {
    stream.resume();
  } else {
    stream.pause();
  }
}

async function verifyVisible() {
  const visible = entries.value;
  if (visible.length === 0) {
    verifyResult.value = { ok: true, checked: 0 };
    return;
  }
  const fromID = visible[visible.length - 1].id;
  const toID = visible[0].id;
  try {
    const res = await client.audit.verifyChain(fromID, toID);
    verifyResult.value = {
      ok: res.verified,
      checked: res.rows_checked,
      brokenAt: res.broken_at_id,
    };
  } catch {
    verifyResult.value = { ok: false, checked: 0 };
  }
}

async function exportAudit() {
  exportToast.value = 'Exporting…';
  const opts: AuditExportOptions = {
    filter: richFilter.value,
    format: exportFormat.value,
  };
  try {
    const path = await client.audit.export(opts);
    exportToast.value = `Exported to ${path}`;
  } catch (e) {
    exportToast.value = `Export failed: ${String(e)}`;
  }
  setTimeout(() => { exportToast.value = ''; }, 5000);
}

function clearFilters() {
  selectedCategories.value = [];
  sinceInput.value = '';
  untilInput.value = '';
  actorInput.value = '';
  freeText.value = '';
  verboseToggle.value = false;
  selectedSavedQuery.value = '';
}

// ── Saved queries ─────────────────────────────────────────────────────────
async function loadSavedQueries() {
  try {
    savedQueries.value = await client.audit.listSavedQueries();
  } catch {
    savedQueries.value = [];
  }
}

async function applySavedQuery(id: string) {
  const sq = savedQueries.value.find((q) => q.id === id);
  if (!sq) return;
  sinceInput.value = sq.query.since ?? '';
  untilInput.value = sq.query.until ?? '';
  // audit-that-tells-the-truth-01PMZA10 UNIT-6 (WP08): full arrays, not
  // ?.[0] — this was the truncation. kinds/actor_ids used to keep only
  // the first element on load, then saveCurrentQuery persisted that
  // narrowed (destroyed) version back on the very next Save.
  selectedCategories.value = sq.query.kinds ? [...sq.query.kinds] : [];
  actorInput.value = (sq.query.actor_ids ?? []).join(', ');
  freeText.value = sq.query.free_text ?? '';
  verboseToggle.value = sq.query.verbose ?? false;
}

async function saveCurrentQuery() {
  saveQueryError.value = '';
  const name = saveQueryName.value.trim();
  if (!name) {
    saveQueryError.value = 'Name is required';
    return;
  }
  const id = `sq-${Date.now()}`;
  const sq: SavedAuditQuery = {
    id,
    name,
    query: richFilter.value,
    created_at: new Date().toISOString(),
  };
  try {
    await client.audit.saveQuery(sq);
    saveQueryName.value = '';
    await loadSavedQueries();
  } catch (e) {
    saveQueryError.value = String(e);
  }
}

async function deleteSavedQuery(id: string) {
  try {
    await client.audit.deleteQuery(id);
    await loadSavedQueries();
  } catch {
    // ignore
  }
}

// ── Selection (WP08 pre-wire) ──────────────────────────────────────────────
function toggleSelect(id: string) {
  const next = new Set(selectedIDs.value);
  if (next.has(id)) {
    next.delete(id);
  } else {
    next.add(id);
  }
  selectedIDs.value = next;
}

function openDrawer(e: AuditEntry) {
  drawerEntry.value = e;
}

function categoryFor(e: AuditEntry): Category {
  const upper = (e.category ?? '').toUpperCase();
  return (CATEGORIES as readonly string[]).includes(upper)
    ? (upper as Category)
    : 'STORAGE';
}

// ── Bulk purge (WP08) ─────────────────────────────────────────────────────
const showPurgeModal = ref(false);
const purging = ref(false);
const purgeError = ref<string>('');

function requestPurge() {
  if (selectedIDs.value.size === 0) return;
  purgeError.value = '';
  showPurgeModal.value = true;
}

function cancelPurge() {
  showPurgeModal.value = false;
}

async function confirmPurge() {
  purging.value = true;
  purgeError.value = '';
  try {
    const ids = Array.from(selectedIDs.value);
    await client.audit.bulkPurge(ids);
    // Remove purged entries from local state.
    selectedIDs.value = new Set();
    showPurgeModal.value = false;
    // Refresh to reflect the deletion.
    await refresh();
  } catch (e) {
    purgeError.value = e instanceof Error ? e.message : String(e);
  } finally {
    purging.value = false;
  }
}

onMounted(() => {
  void loadSavedQueries();
});

onBeforeUnmount(() => {
  void stream.stop();
});
</script>

<template>
  <div>
    <CanvasHead
      number="05"
      section="AUDIT"
      title="Append-only event log"
      subtitle="Every event is redacted server-side before it lands here. Nothing leaves the device unless a connector explicitly opts in, or fleet config distribution is active (config-apply ACKs and telemetry opt-ins egress to your fleet server)."
    />

    <!-- Rich filter rail -->
    <div class="px-6 py-3 border-b border-border-muted bg-surface-1">
      <div class="flex flex-wrap items-center gap-3 font-ui text-[12px] text-ink-muted">
        <!-- Kind / Category — multi-select (audit-that-tells-the-truth-
             01PMZA10 WP08): a saved query can carry more than one kind,
             and the control that edits it must be able to show and set
             more than one, or every load→save round trip narrows it. -->
        <label class="flex items-center gap-2">
          <span>Kind</span>
          <select
            v-model="selectedCategories"
            multiple
            size="3"
            :aria-label="selectedCategories.length > 0 ? `Kinds: ${selectedCategories.join(', ')}` : 'All categories'"
            class="bg-surface-2 text-ink rounded-sm border border-border px-2 py-1 text-[12px] min-w-[8rem]"
          >
            <option v-for="c in CATEGORIES" :key="c" :value="c">{{ c }}</option>
          </select>
        </label>

        <!-- Time range -->
        <label class="flex items-center gap-2">
          <span>Since</span>
          <input
            v-model="sinceInput"
            type="text"
            placeholder="YYYY-MM-DD"
            class="bg-surface-2 text-ink rounded-sm border border-border px-2 py-1 text-[12px] w-48"
          />
        </label>
        <label class="flex items-center gap-2">
          <span>Until</span>
          <input
            v-model="untilInput"
            type="text"
            placeholder="YYYY-MM-DD (open)"
            class="bg-surface-2 text-ink rounded-sm border border-border px-2 py-1 text-[12px] w-48"
          />
        </label>

        <!-- Actor — comma-separated (audit-that-tells-the-truth-01PMZA10
             WP08): parsed into actorIds below; a plain text field is
             enough for an open-ended id set (no fixed value list, unlike
             Kind above). -->
        <label class="flex items-center gap-2">
          <span>Actor</span>
          <input
            v-model="actorInput"
            type="text"
            placeholder="emitter-id, emitter-id2"
            class="bg-surface-2 text-ink rounded-sm border border-border px-2 py-1 text-[12px] w-36"
          />
        </label>

        <!-- Free-text -->
        <label class="flex items-center gap-2">
          <span>Search</span>
          <input
            v-model="freeText"
            type="search"
            placeholder="free-text..."
            class="bg-surface-2 text-ink rounded-sm border border-border px-2 py-1 text-[12px] w-40"
          />
        </label>

        <!-- Verbose toggle -->
        <label class="flex items-center gap-1 cursor-pointer select-none">
          <input v-model="verboseToggle" type="checkbox" class="accent-accent" />
          <span>Verbose</span>
        </label>

        <!-- Saved queries dropdown -->
        <select
          v-if="savedQueries.length > 0"
          v-model="selectedSavedQuery"
          class="bg-surface-2 text-ink rounded-sm border border-border px-2 py-1 text-[12px]"
          @change="applySavedQuery(selectedSavedQuery)"
        >
          <option value="">Saved queries…</option>
          <option v-for="sq in savedQueries" :key="sq.id" :value="sq.id">{{ sq.name }}</option>
        </select>

        <!-- Stream controls -->
        <button
          type="button"
          class="ml-auto px-2 py-1 text-[11px] font-ui rounded-sm border border-border hover:border-border-strong text-ink-muted hover:text-ink"
          :aria-label="stream.paused.value ? 'Resume audit stream' : 'Pause audit stream'"
          @click="onTogglePause"
        >
          {{ stream.paused.value ? 'Resume' : 'Pause' }}
        </button>
        <button
          type="button"
          class="px-2 py-1 text-[11px] font-ui rounded-sm border border-accent-hairline hover:border-accent text-accent hover:text-accent"
          @click="verifyVisible"
        >
          Verify chain
        </button>
        <!-- Export controls -->
        <select
          v-model="exportFormat"
          class="bg-surface-2 text-ink rounded-sm border border-border px-2 py-1 text-[11px]"
          aria-label="Export format"
        >
          <option value="jsonl">JSONL</option>
          <option value="csv">CSV</option>
          <option value="pdf">PDF</option>
        </select>
        <button
          type="button"
          class="px-2 py-1 text-[11px] font-ui rounded-sm border border-border text-ink-muted hover:text-ink"
          @click="exportAudit"
        >
          Export
        </button>
        <button
          type="button"
          class="px-2 py-1 text-[11px] font-ui rounded-sm border border-border text-ink-muted hover:text-ink"
          @click="clearFilters"
        >
          Clear
        </button>
        <!-- Bulk purge (WP08) — visible when rows are selected -->
        <button
          v-if="selectedIDs.size > 0"
          type="button"
          class="px-2 py-1 text-[11px] font-ui rounded-sm border border-signal-danger text-signal-danger hover:bg-signal-danger hover:text-white transition-colors"
          data-testid="audit-purge-selected"
          @click="requestPurge"
        >
          Purge {{ selectedIDs.size }} selected
        </button>
      </div>

      <!-- Save query row -->
      <div class="mt-2 flex items-center gap-2">
        <input
          v-model="saveQueryName"
          type="text"
          placeholder="Save current filter as…"
          class="bg-surface-2 text-ink rounded-sm border border-border px-2 py-1 text-[11px] w-48"
          @keydown.enter="saveCurrentQuery"
        />
        <button
          type="button"
          class="px-2 py-1 text-[11px] font-ui rounded-sm border border-border text-ink-muted hover:text-ink"
          @click="saveCurrentQuery"
        >
          Save
        </button>
        <span v-if="saveQueryError" class="text-[11px] text-signal-danger">{{ saveQueryError }}</span>
        <!-- Saved query chips for deletion -->
        <span
          v-for="sq in savedQueries"
          :key="sq.id"
          class="inline-flex items-center gap-1 px-2 py-0.5 text-[11px] rounded-full bg-surface-2 border border-border text-ink-muted"
        >
          {{ sq.name }}
          <button
            type="button"
            class="hover:text-signal-danger"
            :aria-label="`Delete saved query ${sq.name}`"
            @click="deleteSavedQuery(sq.id)"
          >×</button>
        </span>
      </div>

      <!-- Export toast -->
      <div
        v-if="exportToast"
        class="mt-1 text-[11px] font-ui text-ink-muted"
        data-testid="export-toast"
      >
        {{ exportToast }}
      </div>

      <!-- Verify chain result pill -->
      <div
        v-if="verifyResult"
        class="mt-2 text-[11px] font-ui"
        :class="verifyResult.ok ? 'text-signal-ok' : 'text-signal-danger'"
        data-testid="verify-result"
      >
        Verified {{ verifyResult.checked }} entr{{ verifyResult.checked === 1 ? 'y' : 'ies' }} —
        {{ verifyResult.ok
          ? 'chain intact'
          : `tamper detected${verifyResult.brokenAt ? ' at ' + verifyResult.brokenAt : ''}` }}
      </div>
    </div>

    <!-- Entry list -->
    <div
      class="flex-1 overflow-y-auto"
      role="log"
      aria-live="polite"
      aria-relevant="additions"
      data-testid="audit-stream"
    >
      <div
        v-if="!loading && entries.length === 0"
        class="px-6 py-4 font-ui text-sm text-ink-muted"
      >
        No audit entries match the current filter.
      </div>
      <div
        v-for="e in entries"
        :key="e.id"
        class="flex items-center"
        :class="selectedIDs.has(e.id) ? 'bg-surface-2' : ''"
        data-testid="audit-row"
      >
        <label class="flex items-center px-2 shrink-0 cursor-pointer" :aria-label="`Select event ${e.id}`">
          <input
            type="checkbox"
            class="accent-accent"
            :checked="selectedIDs.has(e.id)"
            @change="toggleSelect(e.id)"
          />
        </label>
        <div class="flex-1 min-w-0" @click="openDrawer(e)">
          <EventStreamRow
            :timestamp="e.timestamp"
            :category="categoryFor(e)"
            :subject="e.subject"
            :trailing="e.trailing"
          />
        </div>
      </div>
    </div>

    <!-- Bulk purge confirmation modal (WP08) -->
    <Teleport to="body">
      <div
        v-if="showPurgeModal"
        class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
        role="dialog"
        aria-modal="true"
        aria-labelledby="purge-modal-title"
        data-testid="audit-purge-modal"
      >
        <div class="bg-surface-1 rounded border border-border shadow-xl p-6 max-w-sm w-full mx-4">
          <h2 id="purge-modal-title" class="font-ui text-[13px] font-semibold text-ink mb-2">
            Confirm bulk purge
          </h2>
          <p class="font-ui text-[12px] text-ink-muted mb-4">
            Permanently delete {{ selectedIDs.size }} audit event{{ selectedIDs.size === 1 ? '' : 's' }}?
            This operation is <strong class="text-signal-danger">irreversible</strong>.
          </p>
          <div
            v-if="purgeError"
            role="alert"
            class="mb-3 rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
            data-testid="purge-modal-error"
          >
            {{ purgeError }}
          </div>
          <div class="flex gap-2 justify-end">
            <button
              type="button"
              class="px-3 py-1.5 font-ui text-[12px] rounded-sm border border-border text-ink-muted hover:text-ink"
              data-testid="purge-modal-cancel"
              @click="cancelPurge"
            >
              Cancel
            </button>
            <button
              type="button"
              :disabled="purging"
              class="px-3 py-1.5 font-ui text-[12px] rounded-sm border border-signal-danger bg-signal-danger text-white hover:opacity-90 disabled:opacity-50 transition-opacity"
              data-testid="purge-modal-confirm"
              @click="confirmPurge"
            >
              {{ purging ? 'Purging…' : 'Delete permanently' }}
            </button>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- Per-event drawer -->
    <AuditEventDrawer
      :entry="drawerEntry"
      :entries="entries"
      @close="drawerEntry = null"
      @select="drawerEntry = $event"
    />
  </div>
</template>
