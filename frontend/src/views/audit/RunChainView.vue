<script setup lang="ts">
/**
 * RunChainView — vertical timeline of all audit entries for a single
 * session_id / run_id (spec §4.2 "run chain" variant).
 *
 * Rendered as a standalone view reachable via deep-link:
 *   /audit/run/<session-id>
 *
 * Each entry is shown as a timeline node with timestamp, kind, category
 * badge, and payload hash prefix for forensic correlation.
 */
import { computed, onMounted, ref } from 'vue';
import { useRoute } from 'vue-router';
import CanvasHead from '@/shell/CanvasHead.vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type { AuditEntry } from '@/lib/types';
import { CATEGORIES, type Category } from '@/lib/categories';

const route = useRoute();
const client = useHarnessClient();

const sessionId = computed(() => {
  const id = route?.params?.sessionId;
  return Array.isArray(id) ? id[0] : (id ?? '');
});

const entries = ref<AuditEntry[]>([]);
const loading = ref(false);
const error = ref('');

async function load() {
  if (!sessionId.value) return;
  loading.value = true;
  error.value = '';
  try {
    entries.value = await client.audit.listEntries({
      limit: 1000,
    });
  } catch (e) {
    error.value = String(e);
  } finally {
    loading.value = false;
  }
}

function categoryFor(e: AuditEntry): Category {
  const upper = (e.category ?? '').toUpperCase();
  return (CATEGORIES as readonly string[]).includes(upper)
    ? (upper as Category)
    : 'STORAGE';
}

// DS-token category legend. 8 categories over the DS's 7 signal tokens
// means one deliberate reuse (MCP / A2A both -> violet — both are
// "delegated execution" categories, so the visual grouping is coherent).
const categoryColour: Record<string, string> = {
  LLM: 'bg-signal-info',
  MCP: 'bg-signal-violet',
  POLICY: 'bg-signal-warn',
  SECRETS: 'bg-signal-danger',
  STORAGE: 'bg-signal-neutral',
  CONTEXT: 'bg-signal-git',
  A2A: 'bg-signal-violet',
  BUNDLE: 'bg-signal-ok',
};

function dotColour(e: AuditEntry): string {
  return categoryColour[categoryFor(e)] ?? 'bg-signal-neutral';
}

onMounted(() => {
  void load();
});
</script>

<template>
  <div>
    <CanvasHead
      number="05"
      section="AUDIT"
      :title="`Run chain — ${sessionId || 'unknown'}`"
      subtitle="Chronological timeline of audit events for this session."
    />

    <div v-if="loading" class="px-6 py-4 text-sm text-ink-muted font-ui">
      Loading…
    </div>
    <div v-else-if="error" class="px-6 py-4 text-sm text-signal-danger font-ui">
      {{ error }}
    </div>
    <div v-else-if="entries.length === 0" class="px-6 py-4 text-sm text-ink-muted font-ui">
      No audit entries found for this session.
    </div>

    <!-- Vertical timeline -->
    <ol
      v-else
      class="relative px-8 py-4 space-y-0"
      aria-label="Run chain timeline"
      data-testid="run-chain-timeline"
    >
      <li
        v-for="(e, idx) in entries"
        :key="e.id"
        class="relative flex gap-4"
        :data-testid="`run-chain-entry-${idx}`"
      >
        <!-- Vertical line + dot -->
        <div class="flex flex-col items-center">
          <span
            class="w-2.5 h-2.5 rounded-full mt-1 flex-shrink-0"
            :class="dotColour(e)"
          />
          <span
            v-if="idx < entries.length - 1"
            class="w-px flex-1 bg-border-muted mt-0.5"
          />
        </div>

        <!-- Entry content -->
        <div class="pb-4 min-w-0">
          <p class="font-mono text-[10px] text-ink-muted">{{ e.timestamp }}</p>
          <p class="font-ui text-[12px] text-ink mt-0.5 break-all">{{ e.subject }}</p>
          <div class="flex flex-wrap gap-1 mt-1">
            <span
              class="px-1.5 py-0.5 text-[10px] uppercase rounded-sm bg-surface-2 border border-border-muted text-ink-muted"
            >
              {{ e.category }}
            </span>
            <span
              v-if="e.trailing"
              class="px-1.5 py-0.5 text-[10px] font-mono rounded-sm bg-surface-2 border border-border-muted text-ink-muted"
              :title="`hash prefix: ${e.trailing}`"
            >
              {{ e.trailing }}
            </span>
          </div>
        </div>
      </li>
    </ol>
  </div>
</template>
