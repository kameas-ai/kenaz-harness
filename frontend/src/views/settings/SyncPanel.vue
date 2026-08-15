<script setup lang="ts">
/**
 * SyncPanel — Settings → Sync panel.
 *
 * Per-category sync toggles for:
 *   - provider_profiles     (LLM provider profile metadata — no creds)
 *   - model_prefs           (default model + allowlist + per-task prefs)
 *   - mcp_recipes           (MCP recipe templates)
 *   - installed_mcp_servers (installed server list; secrets never synced)
 *   - ui_theme              (color theme + density + a11y)
 *
 * Plus:
 *   - "Last synced" timestamp per category (from SyncStatusView)
 *   - Force push / Force pull per category
 *   - Banner: "N MCP servers need credentials" → link to MCP settings
 *
 * Gated: visible only when signedIn && capability('context_sync')
 * (FR-101 / fleet-share-and-sync-01NDFSEX14 WP06)
 *
 * The gate key is the wire value of `fleet.CapContextSync`
 * (core/fleet/capability.go). It was `settings_sync` until 2026-08-14 —
 * a key that never existed on the wire, so the Pro-gate copy below showed
 * to every signed-in user, entitled or not.
 */
import { ref, computed, onMounted } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { signedIn, capability } from '@/lib/featureFlags';
import { useRouter } from 'vue-router';
import type { SyncStatusView, PendingMCPSecret } from '@/lib/types';

const client = useHarnessClient();
const router = useRouter() as ReturnType<typeof useRouter> | undefined;

// ── state ──────────────────────────────────────────────────────────────────
const statusList = ref<SyncStatusView[]>([]);
const pendingSecrets = ref<PendingMCPSecret[]>([]);
const loading = ref(false);
const errorMsg = ref('');
const busyCategory = ref<string | null>(null);

// ── category definitions ───────────────────────────────────────────────────
interface CategoryDef {
  id: string;
  label: string;
  description: string;
}

const CATEGORIES: CategoryDef[] = [
  {
    id: 'provider_profiles',
    label: 'Provider profiles',
    description: 'LLM provider profile metadata (no credentials — API keys stay on-device).',
  },
  {
    id: 'model_prefs',
    label: 'Model preferences',
    description: 'Default model, provider allowlist, and per-task model prefs.',
  },
  {
    id: 'mcp_recipes',
    label: 'MCP recipes',
    description: 'Recipe templates (the "how to install" definitions, not secrets).',
  },
  {
    id: 'installed_mcp_servers',
    label: 'Installed MCP servers',
    description: 'Your installed MCP server list + config overrides. Secrets never leave this device.',
  },
  {
    id: 'ui_theme',
    label: 'UI theme',
    description: 'Color theme, density, and accessibility preferences.',
  },
];

// ── helpers ────────────────────────────────────────────────────────────────
function statusFor(id: string): SyncStatusView | undefined {
  return statusList.value.find((s) => s.category === id);
}

function isEnabled(id: string): boolean {
  return statusFor(id)?.enabled ?? false;
}

function lastSyncLabel(id: string): string {
  const s = statusFor(id);
  if (!s) return 'Never';
  const ts = s.last_push_at ?? s.last_pull_at;
  if (!ts) return 'Never';
  try {
    return new Date(ts).toLocaleString();
  } catch {
    return ts;
  }
}

function lastError(id: string): string {
  return statusFor(id)?.last_error ?? '';
}

const pendingCount = computed(() => pendingSecrets.value.length);

// ── load ───────────────────────────────────────────────────────────────────
async function loadStatus() {
  loading.value = true;
  errorMsg.value = '';
  try {
    const [statuses, pending] = await Promise.all([
      client.sync.status(),
      client.sync.pendingMCPSecrets(),
    ]);
    statusList.value = statuses;
    pendingSecrets.value = pending;
  } catch (err) {
    errorMsg.value = err instanceof Error ? err.message : String(err);
  } finally {
    loading.value = false;
  }
}

onMounted(loadStatus);

// ── actions ────────────────────────────────────────────────────────────────
async function toggle(id: string) {
  const current = isEnabled(id);
  busyCategory.value = id;
  try {
    await client.sync.toggle(id, !current);
    await loadStatus();
  } catch (err) {
    errorMsg.value = err instanceof Error ? err.message : String(err);
  } finally {
    busyCategory.value = null;
  }
}

async function forcePush(id: string) {
  busyCategory.value = `push-${id}`;
  try {
    await client.sync.forcePush(id);
    await loadStatus();
  } catch (err) {
    errorMsg.value = err instanceof Error ? err.message : String(err);
  } finally {
    busyCategory.value = null;
  }
}

async function forcePull(id: string) {
  busyCategory.value = `pull-${id}`;
  try {
    await client.sync.forcePull(id);
    await loadStatus();
  } catch (err) {
    errorMsg.value = err instanceof Error ? err.message : String(err);
  } finally {
    busyCategory.value = null;
  }
}

function goToMCPTools() {
  // TODO: pass ?pending=1 once ToolsView/MCPView consumes a `pending` query
  // param to auto-open the MCP panel filtered to servers awaiting credentials.
  // For now, navigate to /tools without a filter so the link is at least live.
  void router?.push('/tools');
}
</script>

<template>
  <section class="space-y-6 p-4" data-testid="sync-panel">
    <!-- Not signed in / capability gate -->
    <div v-if="!signedIn" class="text-sm text-ink-muted" data-testid="sync-not-signed-in">
      Sign in to fleet to enable cross-device settings sync.
      Go to Settings → Account to sign in.
    </div>
    <div v-else-if="!capability('context_sync')" class="text-sm text-ink-muted" data-testid="sync-pro-gate">
      Cross-device sync requires a Pro+ subscription.
    </div>

    <template v-else>
      <!-- Header -->
      <div>
        <h2 class="text-sm font-semibold text-ink mb-1">Cross-device sync</h2>
        <p class="text-xs text-ink-muted">
          Toggle categories to sync between your devices. Credentials and API keys
          are <strong>never</strong> synced — only configuration metadata.
          Changes push within 5s; fleet polls every 60s.
        </p>
      </div>

      <!-- Pending MCP secrets banner -->
      <div
        v-if="pendingCount > 0"
        class="flex items-start gap-3 rounded-md border border-signal-warn bg-signal-warn-soft px-4 py-3"
        data-testid="sync-pending-secrets-banner"
      >
        <span class="mt-0.5 text-signal-warn" aria-hidden="true">⚠</span>
        <div class="flex-1">
          <p class="text-sm font-medium text-signal-warn">
            {{ pendingCount }} MCP {{ pendingCount === 1 ? 'server' : 'servers' }} need credentials
          </p>
          <p class="text-xs text-signal-warn">
            MCP servers synced from another device are waiting for credentials before they can start.
          </p>
        </div>
        <button
          type="button"
          class="shrink-0 rounded-sm bg-signal-warn px-3 py-1 text-xs font-medium text-white hover:brightness-90"
          data-testid="sync-go-to-mcp-btn"
          @click="goToMCPTools"
        >
          Provide credentials
        </button>
      </div>

      <!-- Error -->
      <div
        v-if="errorMsg"
        class="rounded-sm border border-signal-danger bg-signal-danger-soft px-3 py-2 text-xs text-signal-danger"
        role="alert"
        data-testid="sync-error"
      >
        {{ errorMsg }}
      </div>

      <!-- Loading -->
      <div v-if="loading" class="text-xs text-ink-muted" data-testid="sync-loading">
        Loading sync status…
      </div>

      <!-- Category list -->
      <ul v-else class="space-y-3" data-testid="sync-category-list">
        <li
          v-for="cat in CATEGORIES"
          :key="cat.id"
          class="rounded-md border border-border-muted bg-surface-1 p-4"
          :data-testid="`sync-category-${cat.id}`"
        >
          <div class="flex items-start gap-3">
            <!-- Toggle -->
            <label class="mt-0.5 flex shrink-0 cursor-pointer items-center">
              <input
                type="checkbox"
                :checked="isEnabled(cat.id)"
                :disabled="busyCategory === cat.id"
                class="h-4 w-4 rounded accent-accent"
                :aria-label="`Toggle ${cat.label} sync`"
                :data-testid="`sync-toggle-${cat.id}`"
                @change="toggle(cat.id)"
              />
            </label>

            <!-- Labels + actions -->
            <div class="flex-1 min-w-0">
              <div class="flex items-center justify-between gap-2">
                <span class="text-sm font-medium text-ink">{{ cat.label }}</span>
                <!-- Force push/pull — only when enabled -->
                <div v-if="isEnabled(cat.id)" class="flex items-center gap-1.5 shrink-0">
                  <button
                    type="button"
                    class="h-6 rounded-sm border border-border-muted px-2 font-ui text-[10px] text-ink-muted hover:bg-surface-2 disabled:opacity-50"
                    :disabled="busyCategory !== null"
                    :data-testid="`sync-force-push-${cat.id}`"
                    @click="forcePush(cat.id)"
                  >
                    {{ busyCategory === `push-${cat.id}` ? '…' : '↑ Push' }}
                  </button>
                  <button
                    type="button"
                    class="h-6 rounded-sm border border-border-muted px-2 font-ui text-[10px] text-ink-muted hover:bg-surface-2 disabled:opacity-50"
                    :disabled="busyCategory !== null"
                    :data-testid="`sync-force-pull-${cat.id}`"
                    @click="forcePull(cat.id)"
                  >
                    {{ busyCategory === `pull-${cat.id}` ? '…' : '↓ Pull' }}
                  </button>
                </div>
              </div>

              <p class="mt-0.5 text-xs text-ink-muted">{{ cat.description }}</p>

              <!-- Last synced + error -->
              <div class="mt-1 flex flex-wrap gap-x-4 gap-y-0.5 text-[10px]">
                <span class="text-ink-subtle">
                  Last sync: <span :data-testid="`sync-last-ts-${cat.id}`">{{ lastSyncLabel(cat.id) }}</span>
                </span>
                <span
                  v-if="lastError(cat.id)"
                  class="text-signal-danger"
                  :data-testid="`sync-last-error-${cat.id}`"
                >
                  Error: {{ lastError(cat.id) }}
                </span>
              </div>
            </div>
          </div>
        </li>
      </ul>

      <!-- Refresh button -->
      <button
        type="button"
        class="h-7 rounded-sm border border-border-muted px-3 font-ui text-xs text-ink-muted hover:bg-surface-2 disabled:opacity-50"
        :disabled="loading"
        data-testid="sync-refresh-btn"
        @click="loadStatus"
      >
        Refresh
      </button>
    </template>
  </section>
</template>
