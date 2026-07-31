<script setup lang="ts">
/**
 * CatalogView — browse installable workflow templates (WP03).
 *
 * Renders a searchable grid of catalog cards. Each card shows the
 * workflow icon, name, description, source badge, and install-status
 * pill. Clicking a card emits `select` so the parent can open the
 * CatalogPreviewDrawer.
 */
import { ref, computed, onMounted } from 'vue';
import type { WorkflowsClient, WorkflowsCatalogEntry } from '@/lib/workflowsClient';

const props = defineProps<{
  client: WorkflowsClient;
}>();

const emit = defineEmits<{
  (e: 'select', entry: WorkflowsCatalogEntry): void;
}>();

const entries = ref<WorkflowsCatalogEntry[]>([]);
const loading = ref(false);
const loadError = ref<string | null>(null);
const query = ref('');

const filtered = computed(() => {
  const q = query.value.trim().toLowerCase();
  if (!q) return entries.value;
  return entries.value.filter(
    (e) =>
      e.name.toLowerCase().includes(q) ||
      (e.description ?? '').toLowerCase().includes(q),
  );
});

function statusLabel(status: string): string {
  switch (status) {
    case 'installed':
      return 'Installed';
    case 'installed_outdated':
      return 'Update available';
    default:
      return 'Not installed';
  }
}

function statusClass(status: string): string {
  switch (status) {
    case 'installed':
      return 'bg-signal-ok-soft text-signal-ok';
    case 'installed_outdated':
      return 'bg-signal-warn-soft text-signal-warn';
    default:
      return 'bg-surface-2 text-ink-muted';
  }
}

async function load() {
  loading.value = true;
  loadError.value = null;
  try {
    entries.value = await props.client.catalog.list();
  } catch (err) {
    loadError.value = err instanceof Error ? err.message : String(err);
  } finally {
    loading.value = false;
  }
}

onMounted(load);
</script>

<template>
  <div data-testid="catalog-view" class="space-y-4">
    <div class="flex items-center gap-2">
      <input
        v-model="query"
        type="search"
        aria-label="Search catalog"
        placeholder="Search catalog…"
        class="flex-1 rounded-sm border border-border-muted bg-surface-2 px-3 py-1.5 font-ui text-sm text-ink placeholder:text-ink-muted focus:outline-none focus:ring-1 focus:ring-accent"
        data-testid="catalog-search"
      />
    </div>

    <div v-if="loading" class="font-ui text-sm text-ink-muted">
      Loading catalog…
    </div>

    <div
      v-else-if="loadError"
      class="rounded-sm border border-signal-danger bg-signal-danger-soft p-4 font-ui text-sm text-signal-danger"
      data-testid="catalog-load-error"
    >
      Failed to load catalog: {{ loadError }}
    </div>

    <div
      v-else-if="filtered.length === 0"
      class="font-ui text-sm text-ink-muted"
      data-testid="catalog-empty"
    >
      {{ query ? 'No workflows match your search.' : 'No workflows in the catalog.' }}
    </div>

    <div
      v-else
      class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3"
      data-testid="catalog-grid"
    >
      <button
        v-for="entry in filtered"
        :key="entry.id"
        type="button"
        class="text-left rounded-sm border border-border-muted bg-surface-1 p-4 hover:bg-surface-2 focus:outline-none focus:ring-1 focus:ring-accent transition-colors"
        :data-testid="`catalog-card-${entry.id}`"
        @click="emit('select', entry)"
      >
        <div class="flex items-start justify-between gap-2">
          <span v-if="entry.icon" class="text-2xl leading-none flex-shrink-0">
            {{ entry.icon }}
          </span>
          <div class="flex-1 min-w-0">
            <div class="flex items-center gap-2 flex-wrap">
              <span class="font-ui text-sm font-medium text-ink truncate">
                {{ entry.name }}
              </span>
              <span class="font-ui text-xs text-ink-muted flex-shrink-0">
                {{ entry.source }}
              </span>
            </div>
            <p
              v-if="entry.description"
              class="mt-1 font-ui text-xs text-ink-muted line-clamp-2"
            >
              {{ entry.description }}
            </p>
          </div>
          <span
            class="flex-shrink-0 rounded-full px-2 py-0.5 font-ui text-xs"
            :class="statusClass(entry.installStatus)"
            :data-testid="`catalog-status-${entry.id}`"
          >
            {{ statusLabel(entry.installStatus) }}
          </span>
        </div>
      </button>
    </div>
  </div>
</template>
