<script setup lang="ts">
/**
 * AuditView — append-only event-log viewer (NN/SECTION pattern).
 *
 * Wires the audit RPC + audit:event topic into a paginated, filterable
 * stream of EventStreamRow primitives. Server-side redaction has
 * already run before any Entry reaches this view (privacy CI invariant
 * #2); the view renders entries verbatim and never re-renders the raw
 * payload.
 */
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import CanvasHead from '@/shell/CanvasHead.vue';
import EventStreamRow from '@/components/ui/EventStreamRow.vue';
import { useHarnessClient, useEventLogStream } from '@/lib/useHarnessAPI';
import { CATEGORIES, type Category } from '@/lib/categories';
import type { AuditEntry, AuditFilter } from '@/lib/types';

const client = useHarnessClient();

const selectedCategory = ref<string>('');
const sinceInput = ref<string>('');
const untilInput = ref<string>('');

const filter = computed<AuditFilter>(() => ({
  categories: selectedCategory.value ? [selectedCategory.value] : undefined,
  since: sinceInput.value || undefined,
  until: untilInput.value || undefined,
  limit: 500,
}));

// Reactive seed of historical entries; live entries arrive via the stream.
const seeded = ref<readonly AuditEntry[]>([]);
const verifyResult = ref<null | { ok: boolean; checked: number; brokenAt?: string }>(null);
const loading = ref(false);

async function refresh() {
  loading.value = true;
  try {
    seeded.value = await client.audit.listEntries(filter.value);
  } catch {
    seeded.value = [];
  } finally {
    loading.value = false;
  }
}

watch(filter, () => {
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
  // seeded is already newest-first; live arrivals are newest-last —
  // so reverse the live block by walking the merged list and sorting
  // by timestamp descending where parseable.
  return out.sort((a, b) => (a.timestamp < b.timestamp ? 1 : a.timestamp > b.timestamp ? -1 : 0));
});

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

function categoryFor(e: AuditEntry): Category {
  const upper = (e.category ?? '').toUpperCase();
  return (CATEGORIES as readonly string[]).includes(upper)
    ? (upper as Category)
    : 'STORAGE';
}

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
      subtitle="Every event is redacted server-side before it lands here. Nothing leaves the device unless a connector explicitly opts in."
    />

    <div class="px-6 py-3 border-b border-border-muted bg-surface-1">
      <div class="flex flex-wrap items-center gap-3 font-ui text-[12px] text-ink-muted">
        <label class="flex items-center gap-2">
          <span>Kind</span>
          <select
            v-model="selectedCategory"
            class="bg-surface-2 text-ink rounded-sm border border-border px-2 py-1 text-[12px]"
          >
            <option value="">All categories</option>
            <option v-for="c in CATEGORIES" :key="c" :value="c">{{ c }}</option>
          </select>
        </label>
        <label class="flex items-center gap-2">
          <span>Since</span>
          <input
            v-model="sinceInput"
            type="text"
            placeholder="2026-04-25T00:00:00Z"
            class="bg-surface-2 text-ink rounded-sm border border-border px-2 py-1 text-[12px] w-56"
          />
        </label>
        <label class="flex items-center gap-2">
          <span>Until</span>
          <input
            v-model="untilInput"
            type="text"
            placeholder="2026-04-26T00:00:00Z"
            class="bg-surface-2 text-ink rounded-sm border border-border px-2 py-1 text-[12px] w-56"
          />
        </label>
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
      </div>
      <div
        v-if="verifyResult"
        class="mt-2 text-[11px] font-ui"
        :class="verifyResult.ok ? 'text-signal-ok' : 'text-signal-danger'"
      >
        Verified {{ verifyResult.checked }} entr{{ verifyResult.checked === 1 ? 'y' : 'ies' }} —
        {{ verifyResult.ok ? 'chain intact' : `tamper detected${verifyResult.brokenAt ? ' at ' + verifyResult.brokenAt : ''}` }}
      </div>
    </div>

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
      <EventStreamRow
        v-for="e in entries"
        :key="e.id"
        :timestamp="e.timestamp"
        :category="categoryFor(e)"
        :subject="e.subject"
        :trailing="e.trailing"
      />
    </div>
  </div>
</template>
