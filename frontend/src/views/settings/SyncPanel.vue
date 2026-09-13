<script setup lang="ts">
/**
 * SyncPanel — Settings → Sync panel.
 *
 * Per-category sync toggles for:
 *   - provider_profiles (LLM provider profile metadata — no creds)
 *   - model_prefs       (default model + allowlist + per-task prefs)
 *   - mcp_recipes       (MCP recipe templates)
 *   - installed_mcp     (installed server list; secrets never synced)
 *   - ui_theme          (color theme + density + a11y)
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

// ── category display metadata ───────────────────────────────────────────────
//
// fleet-generic-sync-framework-01NSYNC02 WP06: this used to be a hardcoded
// CATEGORIES array of exactly the five original categories, so a kind
// registered later through the SyncKind registry (slash_commands, WP05)
// synced correctly on the backend — Sync_Status already returned a row for
// it — but never rendered here at all: this panel is a Vue array literal
// scan, not a client.sync.status() scan. The fix is to render one row PER
// STATUS ROW THE BACKEND RETURNS (`categories` below), not per hardcoded
// id. CATEGORY_LABELS is now display metadata ONLY — a lookup for the
// five categories whose copy predates this WP, not the source of truth
// for which rows exist.
interface CategoryLabel {
  label: string;
  description: string;
}

const CATEGORY_LABELS: Record<string, CategoryLabel> = {
  provider_profiles: {
    label: 'Provider profiles',
    description: 'LLM provider profile metadata (no credentials — API keys stay on-device).',
  },
  model_prefs: {
    label: 'Model preferences',
    description: 'Default model, provider allowlist, and per-task model prefs.',
  },
  mcp_recipes: {
    label: 'MCP recipes',
    description: 'Recipe templates (the "how to install" definitions, not secrets).',
  },
  // Canonical wire value: corefleet.SyncCategoryInstalledMCP
  // (core/fleet/sync.go:34), registered under this exact string at
  // core/rpc/sync_categories.go:146. This id used to read
  // 'installed_mcp_servers', which never matched a Sync_Status row
  // (toggle silently no-opped: "Last synced: Never" forever) and reached
  // the "unknown category" error on toggle (core/fleet/sync.go:151) —
  // WP07, fleet-enforcement-truth-01PMZ505.
  installed_mcp: {
    label: 'Installed MCP servers',
    description: 'Your installed MCP server list + config overrides. Secrets never leave this device.',
  },
  ui_theme: {
    label: 'UI theme',
    description: 'Color theme, density, and accessibility preferences.',
  },
  slash_commands: {
    label: 'Slash commands',
    description: 'Your custom /slash commands (global scope only).',
  },
};

/**
 * labelFor / descriptionFor — known categories use the curated copy
 * above; an unrecognized id (any future kind registered through the
 * SyncKind registry that this panel's copy hasn't caught up with yet)
 * falls back to a readable title-cased rendering of the wire id rather
 * than silently not rendering, or rendering a raw snake_case string.
 */
function titleCaseFromSnake(id: string): string {
  return id
    .split('_')
    .filter(Boolean)
    .map((w) => w.charAt(0).toUpperCase() + w.slice(1))
    .join(' ');
}

function labelFor(id: string): string {
  return CATEGORY_LABELS[id]?.label ?? titleCaseFromSnake(id);
}

function descriptionFor(id: string): string {
  return CATEGORY_LABELS[id]?.description ?? '';
}

// ── helpers ────────────────────────────────────────────────────────────────
function statusFor(id: string): SyncStatusView | undefined {
  return statusList.value.find((s) => s.category === id);
}

function isEnabled(id: string): boolean {
  return statusFor(id)?.enabled ?? false;
}

/**
 * isOrgOnly — a kind whose registered Scopes include "org" but NOT
 * "user" has nothing for a member to toggle personally: the row is
 * entirely org-managed (FR-007: "org kinds show 'managed'"). No live
 * kind is org-only yet (every current registration also declares
 * ScopeUser), but the registry's Scopes are real data, not a guess, so
 * this renders correctly the moment one exists — including PR review's
 * forward-compat case, `provider_setups` (fleet-org-config-inheritance-
 * 01NORGX01 WP04, still gated on the fleet-side blocker).
 */
function isOrgOnly(id: string): boolean {
  const scopes = statusFor(id)?.scopes;
  if (!scopes || scopes.length === 0) return false;
  return scopes.includes('org') && !scopes.includes('user');
}

/**
 * orgProvenanceLabel — the WP03 generic org-provenance signal
 * (fleet.KindRegistry.OrgAppliedAt) rendered as human text, or '' when
 * this kind has never been org-provisioned on this device.
 */
function orgProvenanceLabel(id: string): string {
  const at = statusFor(id)?.org_applied_at;
  if (!at) return '';
  try {
    return `Provisioned by your org — ${new Date(at).toLocaleString()}`;
  } catch {
    return `Provisioned by your org — ${at}`;
  }
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

      <!-- Category list — renders one row PER STATUS ROW THE BACKEND
           RETURNS (fleet-generic-sync-framework-01NSYNC02 WP06), not per
           a hardcoded id list. This is what makes slash_commands (and
           any future SyncKind registration) visible here at all. -->
      <ul v-else class="space-y-3" data-testid="sync-category-list">
        <li
          v-for="s in statusList"
          :key="s.category"
          class="rounded-md border border-border-muted bg-surface-1 p-4"
          :data-testid="`sync-category-${s.category}`"
        >
          <div class="flex items-start gap-3">
            <!-- Toggle — hidden for an org-only kind (FR-007: "org kinds
                 show 'managed'"); every live kind today also declares
                 ScopeUser, so this branch is forward-compat rather than
                 currently load-bearing. -->
            <label
              v-if="!isOrgOnly(s.category)"
              class="mt-0.5 flex shrink-0 cursor-pointer items-center"
            >
              <input
                type="checkbox"
                :checked="isEnabled(s.category)"
                :disabled="busyCategory === s.category"
                class="h-4 w-4 rounded accent-accent"
                :aria-label="`Toggle ${labelFor(s.category)} sync`"
                :data-testid="`sync-toggle-${s.category}`"
                @change="toggle(s.category)"
              />
            </label>
            <span
              v-else
              class="mt-0.5 shrink-0 rounded-sm bg-surface-2 px-1.5 py-0.5 font-ui text-[10px] uppercase tracking-[0.14em] text-ink-dim"
              :data-testid="`sync-managed-${s.category}`"
            >
              Managed
            </span>

            <!-- Labels + actions -->
            <div class="flex-1 min-w-0">
              <div class="flex items-center justify-between gap-2">
                <span class="text-sm font-medium text-ink">{{ labelFor(s.category) }}</span>
                <!-- Force push/pull — only when enabled -->
                <div v-if="isEnabled(s.category)" class="flex items-center gap-1.5 shrink-0">
                  <button
                    type="button"
                    class="h-6 rounded-sm border border-border-muted px-2 font-ui text-[10px] text-ink-muted hover:bg-surface-2 disabled:opacity-50"
                    :disabled="busyCategory !== null"
                    :data-testid="`sync-force-push-${s.category}`"
                    @click="forcePush(s.category)"
                  >
                    {{ busyCategory === `push-${s.category}` ? '…' : '↑ Push' }}
                  </button>
                  <button
                    type="button"
                    class="h-6 rounded-sm border border-border-muted px-2 font-ui text-[10px] text-ink-muted hover:bg-surface-2 disabled:opacity-50"
                    :disabled="busyCategory !== null"
                    :data-testid="`sync-force-pull-${s.category}`"
                    @click="forcePull(s.category)"
                  >
                    {{ busyCategory === `pull-${s.category}` ? '…' : '↓ Pull' }}
                  </button>
                </div>
              </div>

              <p v-if="descriptionFor(s.category)" class="mt-0.5 text-xs text-ink-muted">
                {{ descriptionFor(s.category) }}
              </p>

              <!-- Last synced + error + org provenance -->
              <div class="mt-1 flex flex-wrap gap-x-4 gap-y-0.5 text-[10px]">
                <span class="text-ink-subtle">
                  Last sync: <span :data-testid="`sync-last-ts-${s.category}`">{{ lastSyncLabel(s.category) }}</span>
                </span>
                <span
                  v-if="lastError(s.category)"
                  class="text-signal-danger"
                  :data-testid="`sync-last-error-${s.category}`"
                >
                  Error: {{ lastError(s.category) }}
                </span>
                <span
                  v-if="orgProvenanceLabel(s.category)"
                  class="text-signal-info"
                  :data-testid="`sync-org-provenance-${s.category}`"
                >
                  {{ orgProvenanceLabel(s.category) }}
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
