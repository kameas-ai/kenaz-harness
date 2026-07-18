<script setup lang="ts">
/**
 * MemoryHealthPanel — §2.4 health dashboard tab.
 *
 * Renders at-a-glance counts (total / raw / narrative / long-term
 * promoted, embedded vs. unembedded), the last-7-day activity window,
 * and embedder info.  Auto-refreshes every 30 s while the tab is
 * mounted; the user can also click "Refresh" at any time.
 *
 * Two action buttons at the bottom:
 *   • "Test embedder" — calls Memory_TestEmbedder, shows success/error + dims.
 *   • "Re-embed unembedded chunks" — stub modal (backend job not yet shipped).
 *
 * FR-003: when the backend returns an empty model string but embeddings
 * clearly exist (dimensions > 0 or Embedded% > 0), show "(provider default)"
 * rather than a bare "—" so the row is never silently uninformative.
 */
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type { MemoryHealthSnapshot } from '@/lib/types';
import { formatActivitySigned } from '@/lib/memoryFormatters';

const client = useHarnessClient();

const snapshot = ref<MemoryHealthSnapshot | null>(null);
const loading = ref(false);
const error = ref<string | null>(null);

// Embedder probe state
const testResult = ref<string | null>(null);
const testRunning = ref(false);

// Re-embed modal (stub)
const showReembedModal = ref(false);

let refreshTimer: ReturnType<typeof setInterval> | null = null;

async function refresh() {
  loading.value = true;
  error.value = null;
  try {
    snapshot.value = await client.memory.healthSnapshot();
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  } finally {
    loading.value = false;
  }
}

async function testEmbedder() {
  testRunning.value = true;
  testResult.value = null;
  try {
    const dims = await client.memory.testEmbedder();
    testResult.value = `success — ${dims} dimensions`;
  } catch (err) {
    testResult.value = `error: ${err instanceof Error ? err.message : String(err)}`;
  } finally {
    testRunning.value = false;
  }
}

function pct(n: number, total: number): string {
  if (total === 0) return '0%';
  return `${Math.round((n / total) * 100)}%`;
}

// formatActivitySigned is imported from @/lib/memoryFormatters (FR-004).

/**
 * FR-003: Resolve the display model name for the embedder row.
 *
 * When the backend returns model="" but embeddings clearly exist (dimensions
 * > 0 or some chunks are already embedded), the provider is using a default
 * model that it did not surface in the API response. Show "(provider default)"
 * rather than a bare "—" so the row is never silently uninformative.
 *
 * If there is genuinely no embedder configured (dimensions = 0 AND embedded
 * = 0), fall back to "—" as before.
 */
const effectiveEmbedderModel = computed<string>(() => {
  const snap = snapshot.value;
  if (!snap) return '—';
  const { model, dimensions } = snap.embedder;
  if (model) return model;
  // Embeddings exist but model is not surfaced — provider is using its default.
  const hasEmbeddings = dimensions > 0 || snap.counts.embedded > 0;
  return hasEmbeddings ? '(provider default)' : '—';
});

onMounted(() => {
  void refresh();
  // Auto-refresh every 30 s while the tab is visible.
  refreshTimer = setInterval(() => {
    void refresh();
  }, 30_000);
});

onUnmounted(() => {
  if (refreshTimer !== null) {
    clearInterval(refreshTimer);
    refreshTimer = null;
  }
});

defineExpose({ refresh });
</script>

<template>
  <div class="px-6 py-4 max-w-3xl" data-testid="memory-health-panel">
    <!-- Header row -->
    <div class="mb-4 flex items-center gap-3">
      <h2 class="font-ui text-[12px] uppercase tracking-[0.18em] text-ink-subtle">
        Memory health
      </h2>
      <button
        type="button"
        class="ml-auto px-3 py-1 rounded-sm border border-border-muted font-ui text-[11px] uppercase tracking-[0.18em] text-ink-dim hover:bg-surface-2 disabled:opacity-50"
        :disabled="loading"
        data-testid="memory-health-refresh"
        @click="refresh"
      >
        {{ loading ? 'Refreshing…' : 'Refresh' }}
      </button>
    </div>

    <div
      v-if="error"
      class="mb-4 rounded-md border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
      role="alert"
      data-testid="memory-health-error"
    >
      {{ error }}
    </div>

    <div
      v-if="!snapshot && !error && !loading"
      class="font-ui text-[12px] text-ink-muted"
    >
      Loading…
    </div>

    <template v-if="snapshot">
      <!-- Chunk counts table -->
      <section
        class="mb-5 rounded-md border border-border-muted bg-surface-1 p-4"
        data-testid="memory-health-counts"
      >
        <h3 class="mb-3 font-ui text-[10px] uppercase tracking-[0.18em] text-ink-dim">
          Total chunks
        </h3>
        <table class="w-full font-mono text-[12px] text-ink">
          <tbody>
            <tr data-testid="health-count-total">
              <td class="py-0.5 font-ui text-[11px] text-ink font-medium" colspan="2">
                Total chunks
              </td>
              <td class="py-0.5 text-right tabular-nums text-ink">
                {{ snapshot.counts.total.toLocaleString() }}
              </td>
            </tr>
            <tr data-testid="health-count-raw">
              <td class="w-4 py-0.5 text-ink-dim">•</td>
              <td class="py-0.5 text-ink-dim">Raw (session)</td>
              <td class="py-0.5 text-right tabular-nums">
                {{ snapshot.counts.raw.toLocaleString() }}
                <span class="ml-2 text-ink-subtle">({{ pct(snapshot.counts.raw, snapshot.counts.total) }})</span>
              </td>
            </tr>
            <tr data-testid="health-count-narrative">
              <td class="py-0.5 text-ink-dim">•</td>
              <td class="py-0.5 text-ink-dim">
                Narrative
                <span class="ml-1 text-[10px] text-ink-subtle">[ships with narrative-layer]</span>
              </td>
              <td class="py-0.5 text-right tabular-nums">
                {{ snapshot.counts.narrative.toLocaleString() }}
                <span class="ml-2 text-ink-subtle">({{ pct(snapshot.counts.narrative, snapshot.counts.total) }})</span>
              </td>
            </tr>
            <tr data-testid="health-count-promoted">
              <td class="py-0.5 text-ink-dim">•</td>
              <td class="py-0.5 text-ink-dim">Long-term promoted</td>
              <td class="py-0.5 text-right tabular-nums">
                {{ snapshot.counts.longTermPromoted.toLocaleString() }}
                <span class="ml-2 text-ink-subtle">({{ pct(snapshot.counts.longTermPromoted, snapshot.counts.total) }})</span>
              </td>
            </tr>
          </tbody>
        </table>

        <div class="mt-4 mb-2 border-t border-border-muted" />

        <h3 class="mb-2 font-ui text-[10px] uppercase tracking-[0.18em] text-ink-dim">
          Embedded vs. unembedded
        </h3>
        <table class="w-full font-mono text-[12px] text-ink">
          <tbody>
            <tr data-testid="health-count-embedded">
              <td class="py-0.5 text-ink-dim">Embedded</td>
              <td class="py-0.5 text-right tabular-nums">
                {{ snapshot.counts.embedded.toLocaleString() }}
                <span class="ml-2 text-ink-subtle">({{ pct(snapshot.counts.embedded, snapshot.counts.total) }})</span>
              </td>
            </tr>
            <tr data-testid="health-count-unembedded">
              <td class="py-0.5 text-signal-warning">Unembedded</td>
              <td class="py-0.5 text-right tabular-nums text-signal-warning">
                {{ snapshot.counts.unembedded.toLocaleString() }}
                <span class="ml-2">({{ pct(snapshot.counts.unembedded, snapshot.counts.total) }})</span>
                <span
                  v-if="snapshot.counts.unembedded > 0"
                  class="ml-1 text-[10px]"
                >— not retrievable</span>
              </td>
            </tr>
          </tbody>
        </table>
      </section>

      <!-- Last 7 days activity -->
      <section
        class="mb-5 rounded-md border border-border-muted bg-surface-1 p-4"
        data-testid="memory-health-activity"
      >
        <h3 class="mb-3 font-ui text-[10px] uppercase tracking-[0.18em] text-ink-dim">
          Last 7 days
        </h3>
        <table class="w-full font-mono text-[12px] text-ink">
          <tbody>
            <tr data-testid="health-activity-captured">
              <td class="py-0.5 text-ink-dim">•</td>
              <td class="py-0.5 text-ink-dim">Captured</td>
              <td class="py-0.5 text-right tabular-nums text-signal-success">
                {{ formatActivitySigned(snapshot.activity.captured, '+') }}
              </td>
            </tr>
            <tr data-testid="health-activity-pruned">
              <td class="py-0.5 text-ink-dim">•</td>
              <td class="py-0.5 text-ink-dim">Pruned</td>
              <td class="py-0.5 text-right tabular-nums text-signal-warning">
                {{ formatActivitySigned(snapshot.activity.pruned, '-') }}
              </td>
            </tr>
            <tr data-testid="health-activity-promoted">
              <td class="py-0.5 text-ink-dim">•</td>
              <td class="py-0.5 text-ink-dim">Promoted</td>
              <td class="py-0.5 text-right tabular-nums">
                {{ formatActivitySigned(snapshot.activity.promoted, '+') }}
              </td>
            </tr>
          </tbody>
        </table>
      </section>

      <!-- Embedder info -->
      <section
        class="mb-5 rounded-md border border-border-muted bg-surface-1 p-4"
        data-testid="memory-health-embedder"
      >
        <h3 class="mb-3 font-ui text-[10px] uppercase tracking-[0.18em] text-ink-dim">
          Embedder
        </h3>
        <table class="w-full font-mono text-[12px] text-ink">
          <tbody>
            <tr>
              <td class="py-0.5 pr-6 text-ink-dim">Provider</td>
              <td class="py-0.5 tabular-nums">{{ snapshot.embedder.kind }}</td>
            </tr>
            <tr>
              <td class="py-0.5 pr-6 text-ink-dim">Model</td>
              <td class="py-0.5" data-testid="health-embedder-model">{{ effectiveEmbedderModel }}</td>
            </tr>
            <tr>
              <td class="py-0.5 pr-6 text-ink-dim">Dimensions</td>
              <td class="py-0.5 tabular-nums">{{ snapshot.embedder.dimensions || '—' }}</td>
            </tr>
          </tbody>
        </table>
        <p
          v-if="testResult"
          class="mt-2 font-mono text-[11px]"
          :class="testResult.startsWith('error') ? 'text-signal-danger' : 'text-signal-success'"
          data-testid="memory-embedder-test-result"
        >
          {{ testResult }}
        </p>
      </section>

      <!-- Action buttons -->
      <div class="flex flex-wrap items-center gap-3">
        <button
          type="button"
          class="px-3 py-1 rounded-sm border border-border-muted font-ui text-[11px] uppercase tracking-[0.18em] text-ink-dim hover:bg-surface-2 disabled:opacity-50"
          :disabled="testRunning"
          data-testid="memory-health-test-embedder"
          @click="testEmbedder"
        >
          {{ testRunning ? 'Testing…' : 'Test embedder' }}
        </button>
        <button
          type="button"
          class="px-3 py-1 rounded-sm border border-border-muted font-ui text-[11px] uppercase tracking-[0.18em] text-ink-dim hover:bg-surface-2"
          :disabled="snapshot.counts.unembedded === 0"
          data-testid="memory-health-reembed"
          @click="showReembedModal = true"
        >
          Re-embed unembedded chunks ({{ snapshot.counts.unembedded.toLocaleString() }})
        </button>
      </div>
    </template>

    <!-- Re-embed stub modal -->
    <div
      v-if="showReembedModal"
      class="fixed inset-0 z-40 grid place-items-center bg-surface-0/70"
      role="dialog"
      aria-modal="true"
      aria-labelledby="memory-reembed-title"
      data-testid="memory-reembed-modal"
    >
      <div class="w-[28rem] max-w-[92vw] rounded-md border border-border-muted bg-surface-1 p-4 shadow-lg">
        <h3
          id="memory-reembed-title"
          class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
        >
          Re-embed unembedded chunks
        </h3>
        <p class="mt-2 font-ui text-sm text-ink">
          This will run in the background — not implemented yet.
          The background re-embedding job will ship in a future release.
        </p>
        <div class="mt-4 flex justify-end">
          <button
            type="button"
            class="px-3 py-1 rounded-sm border border-border-muted font-ui text-[11px] uppercase tracking-[0.18em] text-ink-dim hover:bg-surface-2"
            data-testid="memory-reembed-close"
            @click="showReembedModal = false"
          >
            Close
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
