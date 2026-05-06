<script setup lang="ts">
/**
 * MemoryBadge — displays the number of memory chunks in the active project
 * (or all chunks when no project is active). Renders adjacent to the
 * "New session" button in LeftRail so users understand memory's coverage
 * before spawning a new session.
 *
 * Behaviour (v0.5.6 memory-trust-signals):
 *   - Polls Memory_HealthSnapshot on mount and every 30 s.
 *   - Filters by projectId client-side (HealthSnapshot returns the global
 *     count; ListChunks + filter is used for project-scoped counts).
 *   - Click navigates to /memory (optionally with ?project=<id> filter).
 *   - 0-chunk state shows a brief onboarding modal.
 *   - Tooltip text explains the value proposition.
 */

import { computed, onBeforeUnmount, onMounted, ref } from 'vue';
import { useRouter } from 'vue-router';
import { useHarnessClient } from '@/lib/useHarnessAPI';

const props = defineProps<{
  /** Active project id. Empty string or undefined means "all projects". */
  projectId?: string;
}>();

const client = useHarnessClient();
const router = useRouter();

// Chunk count for the active project (or global when no project).
const chunkCount = ref<number | null>(null);
// Whether the 0-memories onboarding modal is open.
const zeroModalOpen = ref(false);

let pollTimer: ReturnType<typeof setInterval> | null = null;

async function fetchCount() {
  try {
    if (props.projectId) {
      // Project-scoped count: use ListChunks with a ScopeKind=project filter.
      const chunks = await client.memory.listChunks({
        scopeKind: 'project',
        scopeId: props.projectId,
      });
      chunkCount.value = chunks.length;
    } else {
      // Global count: use the lightweight HealthSnapshot (indexed, no full scan).
      const snap = await client.memory.healthSnapshot();
      chunkCount.value = snap.counts.total;
    }
  } catch {
    // Leave chunkCount at its previous value on transient errors.
  }
}

onMounted(() => {
  void fetchCount();
  pollTimer = setInterval(() => {
    void fetchCount();
  }, 30_000);
});

onBeforeUnmount(() => {
  if (pollTimer !== null) {
    clearInterval(pollTimer);
    pollTimer = null;
  }
});

/** Human-readable count: 0 / 845 / 1.2k / 12k
 *  - < 1,000 → exact ("845")
 *  - 1,000–9,999 → one decimal k ("1.2k") truncated at 100s
 *  - >= 10,000 → whole-number k truncated ("12k" for 12,840)
 */
const countLabel = computed<string>(() => {
  const n = chunkCount.value;
  if (n === null) return '…';
  if (n === 0) return '0';
  if (n < 1_000) return n.toLocaleString();
  if (n < 10_000) {
    // 1200 → "1.2k"; 1250 → "1.2k" (truncate at 100s)
    const whole = Math.floor(n / 1_000);
    const frac = Math.floor((n % 1_000) / 100); // 0-9
    if (frac === 0) return `${whole}k`;
    return `${whole}.${frac}k`;
  }
  return Math.floor(n / 1_000) + 'k'; // truncate, not round
});

const tooltipText = computed<string>(() => {
  const n = chunkCount.value;
  if (n === null) return 'Loading memory count…';
  if (n === 0) return '0 memories yet. Memory will carry what matters forward between sessions.';
  const label = n.toLocaleString();
  const scope = props.projectId ? 'this project' : 'this workspace';
  return `${label} memory chunks in ${scope}. Switch sessions freely — memory carries what matters forward.`;
});

function onClick() {
  if (chunkCount.value === 0) {
    zeroModalOpen.value = true;
    return;
  }
  const query = props.projectId ? `?project=${encodeURIComponent(props.projectId)}` : '';
  void router.push(`/memory${query}`);
}

function dismissZeroModal() {
  zeroModalOpen.value = false;
}
</script>

<template>
  <!-- Badge button — always rendered even during load (shows "…"). -->
  <button
    type="button"
    class="flex items-center gap-1 rounded-sm px-2 py-1 font-ui text-[11px] text-ink-muted hover:text-ink hover:bg-surface-2 transition-fast"
    :title="tooltipText"
    data-testid="memory-badge"
    @click="onClick"
  >
    <span aria-hidden="true">💭</span>
    <span class="hidden two-col:inline tabular-nums" data-testid="memory-badge-count">
      {{ countLabel }}
    </span>
    <span class="sr-only">{{ tooltipText }}</span>
  </button>

  <!-- Zero-memories onboarding modal -->
  <div
    v-if="zeroModalOpen"
    class="fixed inset-0 z-50 flex items-center justify-center"
    role="dialog"
    aria-modal="true"
    aria-label="About memory"
    data-testid="memory-badge-zero-modal"
  >
    <div
      class="absolute inset-0 bg-modal-overlay"
      @click="dismissZeroModal"
    />
    <div
      class="relative z-10 w-[360px] max-w-[90vw] rounded-md border border-border-muted bg-surface-0 shadow-lg p-5"
    >
      <h2 class="font-ui text-base font-semibold text-ink">
        Memory carries context across sessions
      </h2>
      <p class="mt-2 font-ui text-sm text-ink-muted">
        As you chat, important context is automatically stored as memory chunks.
        Start a fresh session on a new topic and memory will surface what's relevant — no need to copy-paste from old conversations.
      </p>
      <div class="mt-4 flex justify-end">
        <button
          type="button"
          class="rounded-sm border border-accent px-3 py-1.5 font-ui text-xs text-accent hover:bg-accent-glow"
          data-testid="memory-badge-zero-modal-ok"
          @click="dismissZeroModal"
        >
          Got it
        </button>
      </div>
    </div>
  </div>
</template>
