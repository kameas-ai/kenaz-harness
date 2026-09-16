<script setup lang="ts">
/**
 * MarketplaceView — browse and install items from the fleet catalog.
 *
 * Renders a filterable grid of CatalogItemView cards. Each card has an
 * Install / Uninstall button wired to client.catalog.install /
 * client.catalog.uninstall. An "Installed" pill is shown on items whose
 * `installed` flag is true.
 *
 * Skills (kind="skill") are routed through client.slashcmd.skillInstall /
 * skillUninstall instead of the generic catalog path, so they are
 * live-registered into the slash-command registry without a restart.
 *
 * Filters: kind (all / workflow / agent_pack / bundle / skill) + visibility +
 * free-text search on slug / description.
 *
 * Every card also gets a "Withdraw" action (fleet-enforcement-truth-01PMZ505
 * WP11, register C-3/C-8): removes the item from the org catalog listing
 * entirely, distinct from Uninstall (which only removes the caller's local
 * copy). The server enforces the authorization rule (owner or fleet admin) —
 * CatalogItemView carries no publisher identity for the harness to evaluate
 * itself, so the action is shown on every item, unconditionally, and the
 * server's answer (including a 403 → "not the owner or an admin", never a
 * subscription-tier message) is reported as-is. Confirm-guarded, with copy
 * distinct from Uninstall's (AC-021).
 *
 * (fleet-share-and-sync-01NDFSEX14 WP03; fleet-skills-sync-01NDFSEX18 WP04)
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
    if (item.kind === 'skill') {
      // Skills route through the slashcmd surface for live-registration (FR-202).
      await client.slashcmd.skillInstall(item.id, item.version);
    } else {
      await client.catalog.install(item.id, item.version);
    }
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
    if (item.kind === 'skill') {
      // Skills are identified by their catalog ID in the skill store.
      await client.slashcmd.skillUninstall(item.id);
    } else {
      await client.catalog.uninstall(item.kind, item.id, item.version);
    }
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

// ── withdraw (fleet-enforcement-truth-01PMZ505 WP11) ───────────────────────
const pendingWithdraw = ref<CatalogItemView | null>(null);
const withdrawBusy = ref(false);
const withdrawError = ref('');

function promptWithdraw(item: CatalogItemView) {
  pendingWithdraw.value = item;
  withdrawError.value = '';
}

function cancelWithdraw() {
  pendingWithdraw.value = null;
  withdrawError.value = '';
}

async function confirmWithdraw() {
  const item = pendingWithdraw.value;
  if (!item) return;
  withdrawBusy.value = true;
  withdrawError.value = '';
  try {
    await client.catalog.unpublish(item.id);
    pendingWithdraw.value = null;
    pushToast(`Withdrawn from catalog: ${item.slug} v${item.version}`);
    await loadItems();
  } catch (err) {
    withdrawError.value = err instanceof Error ? err.message : String(err);
  } finally {
    withdrawBusy.value = false;
  }
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
          <option value="skill">Skill</option>
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

      <!--
        Unverified-install notice (fleet-enforcement-truth-01PMZ505 WP10,
        register C-2): plain text on the install affordance, not a modal
        or a tooltip — no per-device catalog signing key source exists
        yet, so ed25519 signature verification is skipped on every
        install from this catalog, on both the workflow/pack/bundle path
        (client.catalog.install) and the skill path
        (client.slashcmd.skillInstall). This stays visible until C-2's
        key source lands.
      -->
      <p
        class="border-b border-border-muted bg-surface-1 px-4 py-1.5 font-ui text-[11px] text-ink-subtle"
        data-testid="marketplace-unverified-notice"
      >
        Fleet catalog installs are not signature-verified yet — content comes from your org's catalog but is not cryptographically checked before install.
      </p>

      <!-- Error -->
      <div
        v-if="errorMsg"
        class="border-b border-signal-danger bg-signal-danger-soft px-4 py-2 font-ui text-xs text-signal-danger"
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

          <!-- Footer: visibility + actions -->
          <div class="flex items-center justify-between gap-2">
            <span class="font-ui text-[10px] text-ink-subtle" data-testid="item-visibility">
              {{ item.visibility }}
            </span>
            <div class="flex items-center gap-1.5">
              <button
                type="button"
                class="h-6 rounded-sm border border-signal-danger px-2 font-ui text-[11px] text-signal-danger hover:bg-surface-2 disabled:opacity-50"
                :disabled="busyIds.has(item.id)"
                :data-testid="`item-withdraw-btn-${item.slug}`"
                @click="promptWithdraw(item)"
              >
                Withdraw
              </button>
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
          </div>
        </li>
      </ul>
    </template>

    <!-- Withdraw confirm modal -->
    <div
      v-if="pendingWithdraw !== null"
      class="fixed inset-0 z-50 flex items-center justify-center"
      role="dialog"
      aria-modal="true"
      data-testid="withdraw-confirm-modal"
    >
      <div class="absolute inset-0 bg-modal-overlay" @click="cancelWithdraw" />
      <div
        class="relative z-10 w-[440px] max-w-[90vw] rounded-md border border-border-muted bg-surface-0 shadow-lg p-5"
      >
        <h2 class="font-ui text-base font-semibold text-ink">
          Withdraw "{{ pendingWithdraw.slug }}"?
        </h2>
        <p class="mt-2 font-ui text-xs text-ink-muted" data-testid="withdraw-confirm-copy">
          This removes the item from the org catalog listing entirely —
          other members will no longer be able to find or install it. This is
          different from Uninstall, which only removes your own local copy
          and leaves the org listing untouched.
        </p>
        <div
          v-if="withdrawError"
          class="mt-2 text-xs text-signal-danger font-ui"
          role="alert"
          data-testid="withdraw-error"
        >
          {{ withdrawError }}
        </div>
        <div class="mt-4 flex justify-end gap-2">
          <button
            type="button"
            class="font-ui text-xs px-3 py-1.5 text-ink-dim hover:text-ink"
            data-testid="withdraw-cancel"
            @click="cancelWithdraw"
          >
            Cancel
          </button>
          <button
            type="button"
            class="font-ui text-xs px-3 py-1.5 rounded-sm border border-signal-danger text-signal-danger hover:bg-surface-2 disabled:opacity-50"
            :disabled="withdrawBusy"
            data-testid="withdraw-confirm"
            @click="confirmWithdraw"
          >
            {{ withdrawBusy ? 'Withdrawing…' : 'Withdraw' }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
