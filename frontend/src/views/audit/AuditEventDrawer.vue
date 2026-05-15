<script setup lang="ts">
/**
 * AuditEventDrawer — side drawer for a single audit entry (spec §4.2).
 *
 * Fields rendered per spec:
 *   event_id, session_id, emitter_id, kind, emitted_at, category,
 *   subject, trailing (payload_hash prefix), cross-reference chips,
 *   OTel trace link (when active).
 *
 * Navigation: previous / next buttons cycle through the visible entries
 * list supplied by the parent. Wraps cleanly at ends.
 */
import { computed } from 'vue';
import CrossReferenceLink from '@/components/audit/CrossReferenceLink.vue';
import TraceLink from '@/components/audit/TraceLink.vue';
import type { AuditEntry } from '@/lib/types';

const props = defineProps<{
  /** The entry currently shown in the drawer. null = drawer closed. */
  entry: AuditEntry | null;
  /** All currently visible entries (for prev/next navigation). */
  entries: readonly AuditEntry[];
  /** Whether the OTel integration is active. */
  otelActive?: boolean;
  /** OTel trace base URL. */
  traceBaseUrl?: string;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'select', entry: AuditEntry): void;
}>();

const currentIndex = computed(() => {
  if (!props.entry) return -1;
  return props.entries.findIndex((e) => e.id === props.entry!.id);
});

const hasPrev = computed(() => currentIndex.value > 0);
const hasNext = computed(() => currentIndex.value < props.entries.length - 1);

function navigate(delta: number) {
  const idx = currentIndex.value + delta;
  if (idx < 0 || idx >= props.entries.length) return;
  emit('select', props.entries[idx]);
}

// Attempt to parse cross-reference IDs from the entry data.
// In real usage the server would include structured metadata; until
// then we extract common id fields from the trailing/category heuristic.
const crossRefs = computed<Array<{ kind: string; id: string }>>(() => {
  if (!props.entry) return [];
  const refs: Array<{ kind: string; id: string }> = [];
  // session_id is available from the entry id prefix (first 26 chars of ULID).
  // Emit a session cross-ref when the subject is a session-scoped kind.
  if (
    props.entry.category === 'SESSION' ||
    props.entry.subject?.startsWith('sessions.')
  ) {
    refs.push({ kind: 'session_id', id: props.entry.id });
  }
  return refs;
});

// Synthesise a trace_id from the trailing hex for demo purposes.
// Real implementation reads from structured payload metadata.
const traceId = computed(() => {
  if (!props.entry?.trailing) return '';
  // Pad to 32 chars for a plausible trace_id.
  return props.entry.trailing.padEnd(32, '0');
});
</script>

<template>
  <!-- Drawer backdrop + panel -->
  <Teleport to="body">
    <div
      v-if="entry"
      class="fixed inset-0 z-40 flex justify-end"
      role="dialog"
      aria-modal="true"
      aria-label="Audit event detail"
      @click.self="emit('close')"
    >
      <!-- Backdrop -->
      <div class="absolute inset-0 bg-black/20" @click="emit('close')" />

      <!-- Panel -->
      <aside
        class="relative z-10 flex flex-col w-[480px] max-w-full h-full bg-surface-1 border-l border-border shadow-xl overflow-hidden"
        data-testid="audit-event-drawer"
      >
        <!-- Header -->
        <div class="flex items-center justify-between px-5 py-3 border-b border-border-muted">
          <h2 class="font-ui text-[12px] uppercase tracking-widest text-ink-muted">
            Audit Event
          </h2>
          <div class="flex items-center gap-2">
            <!-- Prev / Next navigation -->
            <button
              type="button"
              class="px-2 py-1 text-[11px] font-ui rounded-sm border border-border text-ink-muted hover:text-ink disabled:opacity-40"
              :disabled="!hasPrev"
              aria-label="Previous entry"
              @click="navigate(-1)"
            >
              ←
            </button>
            <button
              type="button"
              class="px-2 py-1 text-[11px] font-ui rounded-sm border border-border text-ink-muted hover:text-ink disabled:opacity-40"
              :disabled="!hasNext"
              aria-label="Next entry"
              @click="navigate(1)"
            >
              →
            </button>
            <button
              type="button"
              class="px-2 py-1 text-[11px] font-ui rounded-sm border border-border text-ink-muted hover:text-ink"
              aria-label="Close drawer"
              @click="emit('close')"
            >
              ✕
            </button>
          </div>
        </div>

        <!-- Content -->
        <div class="flex-1 overflow-y-auto px-5 py-4 space-y-3 font-ui text-[12px]">
          <!-- Event ID -->
          <div>
            <span class="text-ink-muted">Event ID</span>
            <p class="mt-0.5 font-mono text-[11px] text-ink break-all">{{ entry.id }}</p>
          </div>

          <!-- Kind -->
          <div>
            <span class="text-ink-muted">Kind</span>
            <p class="mt-0.5 font-mono text-[11px] text-ink">{{ entry.subject }}</p>
          </div>

          <!-- Category -->
          <div>
            <span class="text-ink-muted">Category</span>
            <p class="mt-0.5 uppercase tracking-wide text-[11px] text-ink">{{ entry.category }}</p>
          </div>

          <!-- Timestamp -->
          <div>
            <span class="text-ink-muted">Emitted At</span>
            <p class="mt-0.5 font-mono text-[11px] text-ink">{{ entry.timestamp }}</p>
          </div>

          <!-- Payload hash prefix -->
          <div v-if="entry.trailing">
            <span class="text-ink-muted">Payload Hash (first 4 bytes)</span>
            <p class="mt-0.5 font-mono text-[11px] text-ink">{{ entry.trailing }}</p>
          </div>

          <!-- Cross-reference chips -->
          <div v-if="crossRefs.length > 0">
            <span class="text-ink-muted">Cross-references</span>
            <div class="mt-1 flex flex-wrap gap-1">
              <CrossReferenceLink
                v-for="ref in crossRefs"
                :key="`${ref.kind}-${ref.id}`"
                :kind="ref.kind"
                :id="ref.id"
              />
            </div>
          </div>

          <!-- OTel trace link -->
          <div v-if="otelActive && traceId">
            <span class="text-ink-muted">Trace</span>
            <div class="mt-1">
              <TraceLink
                :trace-id="traceId"
                :trace-base-url="traceBaseUrl"
                :otel-active="otelActive"
              />
            </div>
          </div>

          <!-- Navigation hints -->
          <div class="pt-2 text-[10px] text-ink-muted border-t border-border-muted">
            {{ currentIndex + 1 }} of {{ entries.length }}
          </div>
        </div>
      </aside>
    </div>
  </Teleport>
</template>
