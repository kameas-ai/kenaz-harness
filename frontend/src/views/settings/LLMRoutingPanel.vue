<script setup lang="ts">
/**
 * LLMRoutingPanel — Settings → LLM Routing tab.
 *
 * Lists all fallback chains (bundled defaults + user-managed). Clicking
 * a row opens the inline edit form. "New chain" opens a blank form.
 * Bundled chains are read-only in the list but can be examined. Only
 * user-managed chains can be deleted.
 *
 * Reached via /settings?tab=llm-routing.
 * model-fallback-routing-01NDFSEX04 WP05.
 */
import { onMounted, ref } from 'vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import type { FallbackChain, FallbackChainEntry, FallbackChainSummary, TriggerCondition } from '@/lib/types';

const client = useHarnessClient();

// ── list state ─────────────────────────────────────────────────────────

const chains = ref<FallbackChainSummary[]>([]);
const loading = ref(false);
const listError = ref<string | null>(null);

// ── editor state ───────────────────────────────────────────────────────

const editorOpen = ref(false);
const editorChain = ref<FallbackChain | null>(null);
const saving = ref(false);
const saveError = ref<string | null>(null);
const deleteError = ref<string | null>(null);

// ── data loading ───────────────────────────────────────────────────────

async function refresh(): Promise<void> {
  loading.value = true;
  listError.value = null;
  try {
    chains.value = await client.llm.listFallbackChains();
  } catch (err) {
    listError.value = err instanceof Error ? err.message : String(err);
    chains.value = [];
  } finally {
    loading.value = false;
  }
}

async function openEdit(summary: FallbackChainSummary): Promise<void> {
  saveError.value = null;
  deleteError.value = null;
  try {
    editorChain.value = await client.llm.loadChain(summary.id);
  } catch (err) {
    listError.value = err instanceof Error ? err.message : String(err);
    return;
  }
  editorOpen.value = true;
}

function openNew(): void {
  saveError.value = null;
  deleteError.value = null;
  editorChain.value = {
    id: '',
    name: '',
    description: '',
    entries: [],
  };
  editorOpen.value = true;
}

function closeEditor(): void {
  editorOpen.value = false;
  editorChain.value = null;
  saveError.value = null;
  deleteError.value = null;
}

async function save(): Promise<void> {
  if (!editorChain.value) return;
  saving.value = true;
  saveError.value = null;
  try {
    await client.llm.saveChain(editorChain.value);
    closeEditor();
    await refresh();
  } catch (err) {
    saveError.value = err instanceof Error ? err.message : String(err);
  } finally {
    saving.value = false;
  }
}

async function deleteChain(id: string): Promise<void> {
  deleteError.value = null;
  try {
    await client.llm.deleteChain(id);
    closeEditor();
    await refresh();
  } catch (err) {
    deleteError.value = err instanceof Error ? err.message : String(err);
  }
}

// ── entry helpers ──────────────────────────────────────────────────────

const ALL_TRIGGERS: TriggerCondition[] = [
  'error_5xx',
  'error_429',
  'error_auth_failed',
  'error_context_overflow',
  'error_safety_block',
  'error_any',
];

function addEntry(): void {
  if (!editorChain.value) return;
  const entry: FallbackChainEntry = {
    providerID: '',
    model: '',
    triggers: ['error_5xx'],
    maxAttempts: 1,
    paramOverrides: {},
  };
  editorChain.value.entries = [...editorChain.value.entries, entry];
}

function removeEntry(idx: number): void {
  if (!editorChain.value) return;
  editorChain.value.entries = editorChain.value.entries.filter((_, i) => i !== idx);
}

function toggleTrigger(entry: FallbackChainEntry, t: TriggerCondition): void {
  const triggers = entry.triggers as TriggerCondition[];
  if (triggers.includes(t)) {
    entry.triggers = triggers.filter((x) => x !== t);
  } else {
    entry.triggers = [...triggers, t];
  }
}

onMounted(refresh);
</script>

<template>
  <div
    class="px-6 py-4 max-w-3xl"
    data-testid="llm-routing-panel"
  >
    <div class="flex items-center justify-between mb-4">
      <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
        LLM Routing — Fallback Chains
      </h2>
      <button
        type="button"
        class="font-ui text-xs text-ink-link hover:underline disabled:opacity-40"
        :disabled="loading"
        data-testid="llm-routing-new-chain"
        @click="openNew"
      >
        New chain
      </button>
    </div>

    <!-- list error -->
    <div
      v-if="listError"
      class="mb-4 rounded border border-signal-error bg-surface-0 px-3 py-2 font-ui text-xs text-signal-error"
      data-testid="llm-routing-list-error"
    >
      {{ listError }}
    </div>

    <!-- loading -->
    <div
      v-if="loading"
      class="font-ui text-xs text-ink-muted"
      data-testid="llm-routing-loading"
    >
      Loading…
    </div>

    <!-- empty state -->
    <div
      v-else-if="chains.length === 0 && !listError"
      class="font-ui text-xs text-ink-muted"
      data-testid="llm-routing-empty"
    >
      No fallback chains configured.
    </div>

    <!-- chain list -->
    <ul
      v-else
      class="divide-y divide-border-muted rounded border border-border"
      data-testid="llm-routing-chain-list"
    >
      <li
        v-for="chain in chains"
        :key="chain.id"
        class="flex items-center justify-between px-3 py-2 hover:bg-surface-0 cursor-pointer"
        :data-testid="`llm-routing-chain-${chain.id}`"
        @click="openEdit(chain)"
      >
        <div class="flex flex-col gap-0.5 min-w-0">
          <span class="font-ui text-sm text-ink truncate">{{ chain.name }}</span>
          <span
            v-if="chain.description"
            class="font-ui text-[11px] text-ink-muted truncate"
          >{{ chain.description }}</span>
          <div class="flex items-center gap-2 mt-0.5">
            <span class="font-ui text-[10px] text-ink-subtle">{{ chain.entryCount }} hop{{ chain.entryCount !== 1 ? 's' : '' }}</span>
            <span
              v-if="chain.bundled"
              class="font-ui text-[10px] uppercase tracking-[0.12em] text-accent"
            >bundled</span>
          </div>
        </div>
        <svg
          class="h-4 w-4 text-ink-muted flex-shrink-0 ml-2"
          viewBox="0 0 16 16"
          fill="none"
          stroke="currentColor"
          stroke-width="1.5"
        >
          <path d="M6 4l4 4-4 4" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
      </li>
    </ul>

    <!-- editor panel -->
    <div
      v-if="editorOpen && editorChain"
      class="mt-6 rounded border border-border bg-surface-0 p-4 flex flex-col gap-4"
      data-testid="llm-routing-editor"
    >
      <div class="flex items-center justify-between">
        <h3 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          {{ editorChain.id ? 'Edit chain' : 'New chain' }}
        </h3>
        <button
          type="button"
          class="font-ui text-xs text-ink-muted hover:text-ink"
          data-testid="llm-routing-editor-close"
          @click="closeEditor"
        >
          Close
        </button>
      </div>

      <!-- bundled notice -->
      <div
        v-if="editorChain.bundled"
        class="font-ui text-[11px] text-ink-muted rounded border border-border px-2 py-1"
        data-testid="llm-routing-bundled-notice"
      >
        This is a bundled chain. Save to create a user-managed override.
      </div>

      <!-- id -->
      <label class="flex flex-col gap-1">
        <span class="font-ui text-[11px] text-ink-subtle">ID</span>
        <input
          v-model="editorChain.id"
          type="text"
          class="rounded border border-border bg-surface-1 px-2 py-1 font-mono text-[12px] text-ink outline-none focus:border-accent"
          placeholder="my-chain-id"
          data-testid="llm-routing-editor-id"
          :disabled="!!editorChain.bundled && !!editorChain.id"
        />
      </label>

      <!-- name -->
      <label class="flex flex-col gap-1">
        <span class="font-ui text-[11px] text-ink-subtle">Name</span>
        <input
          v-model="editorChain.name"
          type="text"
          class="rounded border border-border bg-surface-1 px-2 py-1 font-ui text-sm text-ink outline-none focus:border-accent"
          placeholder="My Fallback Chain"
          data-testid="llm-routing-editor-name"
        />
      </label>

      <!-- description -->
      <label class="flex flex-col gap-1">
        <span class="font-ui text-[11px] text-ink-subtle">Description (optional)</span>
        <input
          v-model="editorChain.description"
          type="text"
          class="rounded border border-border bg-surface-1 px-2 py-1 font-ui text-sm text-ink outline-none focus:border-accent"
          placeholder="When the primary provider is down…"
          data-testid="llm-routing-editor-description"
        />
      </label>

      <!-- entries -->
      <div>
        <div class="flex items-center justify-between mb-2">
          <span class="font-ui text-[11px] text-ink-subtle">Hops (in order)</span>
          <button
            type="button"
            class="font-ui text-xs text-ink-link hover:underline"
            data-testid="llm-routing-editor-add-entry"
            @click="addEntry"
          >
            + Add hop
          </button>
        </div>

        <div
          v-if="editorChain.entries.length === 0"
          class="font-ui text-xs text-ink-muted"
          data-testid="llm-routing-editor-no-entries"
        >
          No hops yet. Add one above.
        </div>

        <div
          v-for="(entry, idx) in editorChain.entries"
          :key="idx"
          class="mb-3 rounded border border-border-muted p-3 flex flex-col gap-2"
          :data-testid="`llm-routing-entry-${idx}`"
        >
          <div class="flex items-center justify-between">
            <span class="font-ui text-[10px] uppercase tracking-[0.12em] text-ink-subtle">Hop {{ idx + 1 }}</span>
            <button
              type="button"
              class="font-ui text-[11px] text-signal-error hover:underline"
              :data-testid="`llm-routing-entry-remove-${idx}`"
              @click="removeEntry(idx)"
            >
              Remove
            </button>
          </div>

          <!-- provider -->
          <label class="flex flex-col gap-1">
            <span class="font-ui text-[11px] text-ink-subtle">Provider ID</span>
            <input
              v-model="entry.providerID"
              type="text"
              class="rounded border border-border bg-surface-1 px-2 py-1 font-mono text-[12px] text-ink outline-none focus:border-accent"
              placeholder="openrouter"
              :data-testid="`llm-routing-entry-provider-${idx}`"
            />
          </label>

          <!-- model -->
          <label class="flex flex-col gap-1">
            <span class="font-ui text-[11px] text-ink-subtle">Model (optional — uses provider default if blank)</span>
            <input
              v-model="entry.model"
              type="text"
              class="rounded border border-border bg-surface-1 px-2 py-1 font-mono text-[12px] text-ink outline-none focus:border-accent"
              placeholder="openai/gpt-4o"
              :data-testid="`llm-routing-entry-model-${idx}`"
            />
          </label>

          <!-- max attempts -->
          <label class="flex flex-col gap-1">
            <span class="font-ui text-[11px] text-ink-subtle">Max attempts (whole-chain ceiling: 5)</span>
            <input
              v-model.number="entry.maxAttempts"
              type="number"
              min="1"
              max="5"
              class="rounded border border-border bg-surface-1 px-2 py-1 font-ui text-sm text-ink outline-none focus:border-accent w-24"
              :data-testid="`llm-routing-entry-max-attempts-${idx}`"
            />
          </label>

          <!-- triggers -->
          <div class="flex flex-col gap-1">
            <span class="font-ui text-[11px] text-ink-subtle">Trigger on</span>
            <div class="flex flex-wrap gap-2">
              <label
                v-for="t in ALL_TRIGGERS"
                :key="t"
                class="flex items-center gap-1 cursor-pointer"
                :data-testid="`llm-routing-entry-trigger-${idx}-${t}`"
              >
                <input
                  type="checkbox"
                  :checked="entry.triggers.includes(t)"
                  class="accent-accent"
                  @change="toggleTrigger(entry, t)"
                />
                <span class="font-mono text-[11px] text-ink">{{ t }}</span>
              </label>
            </div>
          </div>
        </div>
      </div>

      <!-- errors -->
      <div
        v-if="saveError"
        class="font-ui text-[11px] text-signal-error"
        data-testid="llm-routing-save-error"
      >
        {{ saveError }}
      </div>
      <div
        v-if="deleteError"
        class="font-ui text-[11px] text-signal-error"
        data-testid="llm-routing-delete-error"
      >
        {{ deleteError }}
      </div>

      <!-- actions -->
      <div class="flex items-center justify-between mt-2">
        <button
          v-if="editorChain.id && !editorChain.bundled"
          type="button"
          class="font-ui text-xs text-signal-error hover:underline"
          data-testid="llm-routing-editor-delete"
          @click="deleteChain(editorChain.id)"
        >
          Delete
        </button>
        <div
          v-else
          class="flex-1"
        />
        <div class="flex items-center gap-3">
          <button
            type="button"
            class="font-ui text-xs text-ink-muted hover:text-ink"
            data-testid="llm-routing-editor-cancel"
            @click="closeEditor"
          >
            Cancel
          </button>
          <button
            type="button"
            class="font-ui text-xs text-ink-link hover:underline disabled:opacity-40"
            :disabled="saving"
            data-testid="llm-routing-editor-save"
            @click="save"
          >
            {{ saving ? 'Saving…' : 'Save' }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
