<script setup lang="ts">
/**
 * MarketplaceView — browse and install items from the fleet catalog.
 *
 * Renders a filterable grid of CatalogItemView cards. Each card has an
 * Install / Uninstall button wired to client.catalog.install /
 * client.catalog.uninstall. An "Installed" pill is shown on items whose
 * `installed` flag is true.
 *
 * Filters: kind (all / workflow / agent_pack / bundle) + visibility +
 * free-text search on slug / description.
 *
 * (fleet-share-and-sync-01NDFSEX14 WP03)
 */
import { ref, computed, onMounted } from 'vue';
import CanvasHead from '@/shell/CanvasHead.vue';
import { signedIn } from '@/lib/featureFlags';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { push as pushToast } from '@/composables/useToastQueue';
import type { CatalogItemView } from '@/lib/types';

const client = useHarnessClient();

// ── state ─────────────────────────────────────────────────────────────────
const items = ref<CatalogItemView[]>([]);
const loading = ref(false);
const errorMsg = ref('');

// filters
const filterKind = ref('');
const filterVisibility = ref('');
const searchQuery = ref('');

// per-item busy flags
const busyIds = ref<Set<string>>(new Set());

// ── load ──────────────────────────────────────────────────────────────────
async function loadItems() {
  loading.value = true;
  errorMsg.value = '';
  try {
    items.value = await client.catalog.list({
      kind: filterKind.value || undefined,
      visibility: filterVisibility.value || undefined,
    });
  } catch (err) {
    errorMsg.value = err instanceof Error ? err.message : String(err);
  } finally {
    loading.value = false;
  }
}

onMounted(loadItems);

// ── filtered list ─────────────────────────────────────────────────────────
const filtered = computed<CatalogItemView[]>(() => {
  const q = searchQuery.value.toLowerCase().trim();
  return items.value.filter((item) => {
    if (!q) return true;
    return (
      item.slug.toLowerCase().includes(q) ||
      item.description.toLowerCase().includes(q) ||
      item.kind.toLowerCase().includes(q)
    );
  });
});

// ── actions ───────────────────────────────────────────────────────────────
function setBusy(id: string, on: boolean) {
  const s = new Set(busyIds.value);
  if (on) s.add(id); else s.delete(id);
  busyIds.value = s;
}

async function install(item: CatalogItemView) {
  setBusy(item.id, true);
  try {
    await client.catalog.install(item.id, item.version);
    pushToast(`Installed: ${item.slug} v${item.version}`);
    await loadItems();
  } catch (err) {
    pushToast(`Install failed: ${err instanceof Error ? err.message : String(err)}`, { level: 'error' });
  } finally {
    setBusy(item.id, false);
  }
}

async function uninstall(item: CatalogItemView) {
  setBusy(item.id, true);
  try {
    await client.catalog.uninstall(item.kind, item.id, item.version);
    pushToast(`Uninstalled: ${item.slug} v${item.version}`);
    await loadItems();
  } catch (err) {
    pushToast(`Uninstall failed: ${err instanceof Error ? err.message : String(err)}`, { level: 'error' });
  } finally {
    setBusy(item.id, false);
  }
}

async function applyFilters() {
  await loadItems();
}
</script>

<template>
  <div class="flex h-full flex-col" data-testid="marketplace-view">
    <CanvasHead
      title="Marketplace"
      subtitle="Browse and install items from the fleet catalog."
    />

    <!-- Not signed in -->
    <div
      v-if="!signedIn"
      class="flex flex-1 flex-col items-center justify-center gap-2 text-center"
      data-testid="marketplace-not-signed-in"
    >
      <p class="font-ui text-sm text-ink-muted">
        Sign in to fleet to access the team catalog.
      </p>
      <p class="font-ui text-xs text-ink-subtle">
        Go to Settings → Account to sign in.
      </p>
    </div>

    <template v-else>
      <!-- Filter toolbar -->
      <div
        class="flex flex-wrap items-center gap-2 border-b border-border-muted px-4 py-2"
        data-testid="marketplace-toolbar"
      >
        <!-- Search -->
        <input
          v-model="searchQuery"
          type="search"
          placeholder="Search catalog…"
          class="h-7 min-w-[160px] flex-1 rounded-sm border border-border-muted bg-surface-0 px-2 font-ui text-sm text-ink focus:outline-none focus:ring-1 focus:ring-accent"
          data-testid="marketplace-search"
        />
        <!-- Kind filter -->
        <select
          v-model="filterKind"
          class="h-7 rounded-sm border border-border-muted bg-surface-0 px-2 font-ui text-sm text-ink focus:outline-none focus:ring-1 focus:ring-accent"
          data-testid="marketplace-kind-filter"
          @change="applyFilters"
        >
          <option value="">All kinds</option>
          <option value="workflow">Workflow</option>
          <option value="agent_pack">Agent pack</option>
          <option value="bundle">Bundle</option>
        </select>
        <!-- Visibility filter -->
        <select
          v-model="filterVisibility"
          class="h-7 rounded-sm border border-border-muted bg-surface-0 px-2 font-ui text-sm text-ink focus:outline-none focus:ring-1 focus:ring-accent"
          data-testid="marketplace-visibility-filter"
          @change="applyFilters"
        >
          <option value="">All visibility</option>
          <option value="private">Private</option>
          <option value="team">Team</option>
          <option value="org_public">Org-public</option>
        </select>
        <!-- Refresh -->
        <button
          type="button"
          class="h-7 rounded-sm border border-border-muted px-2 font-ui text-xs text-ink-muted hover:bg-surface-2"
          :disabled="loading"
          data-testid="marketplace-refresh-btn"
          @click="loadItems"
        >
          {{ loading ? 'Loading…' : '↺ Refresh' }}
        </button>
      </div>

      <!-- Error -->
      <div
        v-if="errorMsg"
        class="border-b border-red-200 bg-red-50 px-4 py-2 font-ui text-xs text-red-700"
        role="alert"
        data-testid="marketplace-error"
      >
        {{ errorMsg }}
      </div>

      <!-- Empty state -->
      <div
        v-if="!loading && filtered.length === 0"
        class="flex flex-1 flex-col items-center justify-center gap-1 text-center"
        data-testid="marketplace-empty"
      >
        <p class="font-ui text-sm text-ink-muted">No catalog items found.</p>
        <p class="font-ui text-xs text-ink-subtle">
          Ask a team admin to publish workflows or packs to the catalog.
        </p>
      </div>

      <!-- Grid -->
      <ul
        v-else
        class="grid flex-1 auto-rows-min grid-cols-[repeat(auto-fill,minmax(260px,1fr))] gap-3 overflow-y-auto p-4"
        data-testid="marketplace-grid"
      >
        <li
          v-for="item in filtered"
          :key="`${item.id}@${item.version}`"
          class="flex flex-col gap-2 rounded-md border border-border-muted bg-surface-1 p-4 shadow-sm"
          :data-testid="`marketplace-item-${item.slug}`"
        >
          <!-- Header row -->
          <div class="flex items-start justify-between gap-2">
            <div class="flex flex-col gap-0.5">
              <span class="font-ui text-sm font-semibold text-ink" data-testid="item-slug">
                {{ item.slug }}
              </span>
              <span class="font-mono text-[10px] text-ink-subtle">v{{ item.version }}</span>
            </div>
            <div class="flex shrink-0 items-center gap-1">
              <!-- Installed badge -->
              <span
                v-if="item.installed"
                class="rounded-sm bg-accent/10 px-1.5 py-0.5 font-ui text-[10px] font-medium text-accent"
                data-testid="item-installed-badge"
              >
                Installed
              </span>
              <!-- Kind badge -->
              <span
                class="rounded-sm bg-surface-2 px-1.5 py-0.5 font-ui text-[10px] text-ink-muted"
                data-testid="item-kind-badge"
              >
                {{ item.kind }}
              </span>
            </div>
          </div>

          <!-- Description -->
          <p class="flex-1 font-ui text-xs text-ink-muted line-clamp-3" data-testid="item-description">
            {{ item.description || 'No description.' }}
          </p>

          <!-- Footer: visibility + action -->
          <div class="flex items-center justify-between gap-2">
            <span class="font-ui text-[10px] text-ink-subtle" data-testid="item-visibility">
              {{ item.visibility }}
            </span>
            <button
              v-if="!item.installed"
              type="button"
              class="h-6 rounded-sm bg-accent px-3 font-ui text-xs font-medium text-white hover:bg-accent/90 disabled:opacity-50"
              :disabled="busyIds.has(item.id)"
              :data-testid="`item-install-btn-${item.slug}`"
              @click="install(item)"
            >
              {{ busyIds.has(item.id) ? 'Installing…' : 'Install' }}
            </button>
            <button
              v-else
              type="button"
              class="h-6 rounded-sm border border-border-muted px-3 font-ui text-xs text-ink-muted hover:bg-surface-2 disabled:opacity-50"
              :disabled="busyIds.has(item.id)"
              :data-testid="`item-uninstall-btn-${item.slug}`"
              @click="uninstall(item)"
            >
              {{ busyIds.has(item.id) ? 'Removing…' : 'Uninstall' }}
            </button>
          </div>
        </li>
      </ul>
    </template>
  </div>
</template>
