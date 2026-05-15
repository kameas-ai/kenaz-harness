<script setup lang="ts">
/**
 * PasteConfigTab — clipboard JSON translation from Claude Desktop / Cursor
 * mcpServers config to harness recipes.
 *
 * Flow:
 *   1. User pastes JSON into textarea.
 *   2. "Translate" button calls importClaudeDesktopConfig({rawJson, dryRun:true}).
 *   3. Results table shows per-entry status badges (kept / unsupported /
 *      malformed / collision_warning).
 *   4. "kept" and "collision_warning" rows have checkboxes; user picks which
 *      to import.
 *   5. "Import N entries" calls with dryRun:false + keepIds.
 *   6. "Skip and continue" confirmation on any unsupported/malformed entries.
 */
import { ref, computed } from 'vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import type { MCPImportEntry, MCPTranslationReport } from '@/lib/types';

const emit = defineEmits<{
  (e: 'imported'): void;
}>();

const client = useHarnessClient();

const rawJson = ref('');
const translating = ref(false);
const importing = ref(false);
const translateError = ref<string | null>(null);
const importError = ref<string | null>(null);
const report = ref<MCPTranslationReport | null>(null);
const selectedIds = ref<Set<string>>(new Set());
const importSuccess = ref(false);
const showSkipConfirm = ref(false);

const importableEntries = computed<MCPImportEntry[]>(() => {
  if (!report.value) return [];
  return report.value.entries.filter(
    (e) => e.status === 'kept' || e.status === 'collision_warning',
  );
});

const skippableEntries = computed<MCPImportEntry[]>(() => {
  if (!report.value) return [];
  return report.value.entries.filter(
    (e) => e.status === 'unsupported' || e.status === 'malformed',
  );
});

const selectedCount = computed(() => selectedIds.value.size);

const canImport = computed(
  () => selectedCount.value > 0 && !importing.value,
);

function badgeClass(status: MCPImportEntry['status']): string {
  switch (status) {
    case 'kept':
      return 'text-signal-ok';
    case 'collision_warning':
      return 'text-signal-warn';
    case 'unsupported':
    case 'malformed':
    default:
      return 'text-signal-danger';
  }
}

function badgeLabel(status: MCPImportEntry['status']): string {
  switch (status) {
    case 'kept':
      return 'kept';
    case 'collision_warning':
      return 'collision';
    case 'unsupported':
      return 'unsupported';
    case 'malformed':
      return 'malformed';
    default:
      return status;
  }
}

function toggleSelected(id: string) {
  const next = new Set(selectedIds.value);
  if (next.has(id)) {
    next.delete(id);
  } else {
    next.add(id);
  }
  selectedIds.value = next;
}

async function translate() {
  if (translating.value) return;
  if (!rawJson.value.trim()) {
    translateError.value = 'Paste a Claude Desktop / Cursor mcpServers JSON config first.';
    return;
  }
  translating.value = true;
  translateError.value = null;
  importError.value = null;
  report.value = null;
  selectedIds.value = new Set();
  importSuccess.value = false;
  showSkipConfirm.value = false;
  try {
    const resp = await client.mcp.importClaudeDesktopConfig({
      raw_json: rawJson.value,
      dry_run: true,
    });
    report.value = resp.report;
    // Pre-select all importable (kept) entries.
    const preSelected = new Set<string>();
    for (const entry of resp.report.entries) {
      if (entry.status === 'kept') {
        preSelected.add(entry.id);
      }
    }
    selectedIds.value = preSelected;
  } catch (e) {
    translateError.value = e instanceof Error ? e.message : String(e);
  } finally {
    translating.value = false;
  }
}

function confirmSkipAndImport() {
  showSkipConfirm.value = false;
  void doImport();
}

function requestImport() {
  // If there are skippable entries (unsupported/malformed), show confirmation.
  if (skippableEntries.value.length > 0) {
    showSkipConfirm.value = true;
    return;
  }
  void doImport();
}

async function doImport() {
  if (importing.value) return;
  if (selectedCount.value === 0) return;
  importing.value = true;
  importError.value = null;
  try {
    await client.mcp.importClaudeDesktopConfig({
      raw_json: rawJson.value,
      dry_run: false,
      keep_ids: Array.from(selectedIds.value),
    });
    importSuccess.value = true;
    emit('imported');
  } catch (e) {
    importError.value = e instanceof Error ? e.message : String(e);
  } finally {
    importing.value = false;
  }
}
</script>

<template>
  <div class="space-y-4" data-testid="paste-config-tab">
    <!-- Skip-and-continue confirmation overlay -->
    <div
      v-if="showSkipConfirm"
      class="rounded-sm border border-signal-warn bg-signal-warn/10 px-4 py-3 space-y-2"
      data-testid="paste-skip-confirm"
    >
      <p class="font-ui text-[12px] text-ink">
        {{ skippableEntries.length }} entr{{ skippableEntries.length === 1 ? 'y is' : 'ies are' }}
        unsupported or malformed and will be skipped.
        Continue importing the {{ selectedCount }} selected {{ selectedCount === 1 ? 'entry' : 'entries' }}?
      </p>
      <div class="flex gap-2">
        <button
          type="button"
          class="rounded-sm border border-accent-hairline bg-surface-1 px-3 py-1 font-ui text-[12px] text-accent hover:bg-accent-glow"
          data-testid="paste-skip-confirm-yes"
          @click="confirmSkipAndImport"
        >
          Skip and continue
        </button>
        <button
          type="button"
          class="rounded-sm border border-border-muted px-3 py-1 font-ui text-[12px] text-ink-dim hover:text-ink"
          data-testid="paste-skip-confirm-no"
          @click="showSkipConfirm = false"
        >
          Cancel
        </button>
      </div>
    </div>

    <!-- Paste area -->
    <div class="space-y-2">
      <label
        for="paste-config-json"
        class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
      >
        Paste Claude Desktop / Cursor mcpServers JSON
      </label>
      <textarea
        id="paste-config-json"
        v-model="rawJson"
        rows="8"
        spellcheck="false"
        autocomplete="off"
        class="w-full rounded-sm border border-border-muted bg-surface-1 px-2.5 py-2 font-mono text-[12px] text-ink focus:border-accent focus:outline-none resize-y"
        placeholder='{"mcpServers": {"brave-search": {"command": "npx", "args": [...]}}}'
        data-testid="paste-config-textarea"
      />
    </div>

    <div
      v-if="translateError"
      class="rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
      role="alert"
      data-testid="paste-translate-error"
    >
      {{ translateError }}
    </div>

    <button
      type="button"
      class="rounded-sm border border-accent-hairline bg-surface-1 px-3 py-1.5 font-ui text-[12px] text-accent hover:bg-accent-glow disabled:opacity-50 disabled:cursor-not-allowed"
      :disabled="translating || !rawJson.trim()"
      data-testid="paste-translate-btn"
      @click="translate"
    >
      {{ translating ? 'Translating…' : 'Translate' }}
    </button>

    <!-- Translation report -->
    <div v-if="report" class="space-y-3" data-testid="paste-report">
      <div class="flex gap-4 font-ui text-[11px] text-ink-muted">
        <span data-testid="paste-report-kept">kept: {{ report.kept_count }}</span>
        <span data-testid="paste-report-unsupported">unsupported: {{ report.unsupported_count }}</span>
        <span data-testid="paste-report-malformed">malformed: {{ report.malformed_count }}</span>
        <span
          v-if="report.collision_count > 0"
          class="text-signal-warn"
          data-testid="paste-report-collision"
        >
          collisions: {{ report.collision_count }}
        </span>
      </div>

      <div
        v-if="report.entries.length === 0"
        class="text-[12px] text-ink-muted"
        data-testid="paste-report-empty"
      >
        No entries found in the pasted config.
      </div>

      <div
        v-else
        class="rounded-sm border border-border-muted bg-surface-1 divide-y divide-border-muted"
        data-testid="paste-entries-table"
      >
        <div
          v-for="entry in report.entries"
          :key="entry.id"
          class="px-3 py-2 grid gap-3 items-start"
          style="grid-template-columns: 1rem 1fr auto"
          :data-testid="`paste-entry-${entry.id}`"
        >
          <!-- Checkbox (only for importable entries) -->
          <div class="pt-0.5">
            <input
              v-if="entry.status === 'kept' || entry.status === 'collision_warning'"
              type="checkbox"
              :aria-label="`Select entry ${entry.id}`"
              class="accent-accent w-3.5 h-3.5"
              :checked="selectedIds.has(entry.id)"
              :data-testid="`paste-entry-check-${entry.id}`"
              @change="toggleSelected(entry.id)"
            />
          </div>
          <div>
            <div class="font-ui text-[12px] text-ink">
              {{ entry.original_name || entry.id }}
            </div>
            <div
              v-if="entry.reason"
              class="mt-0.5 text-[11px] text-ink-muted"
              :data-testid="`paste-entry-reason-${entry.id}`"
            >
              {{ entry.reason }}
            </div>
            <div
              v-if="entry.status === 'collision_warning'"
              class="mt-0.5 text-[11px] text-signal-warn"
              :data-testid="`paste-entry-shadow-warning-${entry.id}`"
            >
              This entry will shadow the existing recipe with the same id.
            </div>
          </div>
          <span
            class="text-[10px] uppercase tracking-[0.16em]"
            :class="badgeClass(entry.status)"
            :data-testid="`paste-entry-status-${entry.id}`"
          >
            {{ badgeLabel(entry.status) }}
          </span>
        </div>
      </div>

      <!-- Import action -->
      <div
        v-if="importableEntries.length > 0"
        class="flex items-center gap-3"
      >
        <button
          type="button"
          class="rounded-sm border border-accent-hairline bg-surface-1 px-3 py-1.5 font-ui text-[12px] text-accent hover:bg-accent-glow disabled:opacity-50 disabled:cursor-not-allowed"
          :disabled="!canImport"
          data-testid="paste-import-btn"
          @click="requestImport"
        >
          {{ importing ? 'Importing…' : `Import ${selectedCount} ${selectedCount === 1 ? 'entry' : 'entries'}` }}
        </button>
        <span
          v-if="importSuccess"
          class="text-[11px] text-signal-ok"
          data-testid="paste-import-success"
        >
          Imported successfully.
        </span>
      </div>

      <div
        v-if="importError"
        class="rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
        role="alert"
        data-testid="paste-import-error"
      >
        {{ importError }}
      </div>
    </div>
  </div>
</template>
