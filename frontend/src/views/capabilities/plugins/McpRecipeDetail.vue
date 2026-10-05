<script setup lang="ts">
/**
 * McpRecipeDetail — the MCP-recipe detail plugin of the "Add capability"
 * surface (install-framework-01DOGF0B WP04). Per-kind flows are preserved,
 * not rewritten (spec §6): the 58 KB RecipeKeyPromptModal is the install /
 * edit-configuration flow (keys, OAuth, device code, directory picker,
 * warning consent, recommended policy), AddMCPServerModal's Custom tab is
 * the edit-recipe flow. Everything here moved from the retired
 * KenazToolsPanel.vue row: health pill, source + org badges, warming hint,
 * health alert, forget-key, open workspace, edit configuration, remove (with
 * confirmation), and the status details.
 *
 * Installs from the key prompt go through tools.recipes.install →
 * Tools_InstallRecipe, and removal through tools.recipes.uninstall →
 * Tools_UninstallRecipe; both bindings route through the install framework
 * (consumer check + capability:* event), so the surface's list repaints
 * from the event like any other install.
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useToolsRecipes, useShell } from '@/lib/useHarnessAPI';
import { ChevronDown, ChevronRight, FolderOpen } from '@/shell/icons';
import { categoryIconFor } from '@/lib/recipeCategories';
import RecipeKeyPromptModal from '@/views/tools/RecipeKeyPromptModal.vue';
import AddMCPServerModal from '@/views/tools/AddMCPServerModal.vue';
import HealthPill from '@/views/tools/HealthPill.vue';
import type {
  CapabilityItem,
  EnvKey,
  Recipe,
  RecipeListing,
  RecipeState,
  RecipeStatus,
} from '@/lib/types';

const props = defineProps<{ item: CapabilityItem }>();
const emit = defineEmits<{ (e: 'changed'): void }>();

const tools = useToolsRecipes();
const shell = useShell();

const listing = computed<RecipeListing | null>(
  () => tools.recipes.value.find((l) => l.recipe.id === props.item.id) ?? null,
);
const status = computed<RecipeStatus | null>(() => listing.value?.status ?? null);
const installed = computed(() => listing.value?.enabled ?? props.item.state.installed);
const orgManaged = computed(() => listing.value?.source === 'org' || Boolean(props.item.read_only));

const busy = ref(false);
const error = ref<string | null>(null);
const expanded = ref(false);
const confirmingRemove = ref(false);

// ── install / edit-configuration flow (RecipeKeyPromptModal) ──────────
const modalOpen = ref(false);
const modalInitialConfig = ref<Record<string, unknown>>({});
const recipeConfig = ref<Record<string, unknown> | null>(null);

/** Opened by the surface's Install button when the row has unmet requirements. */
async function beginInstall() {
  error.value = null;
  if (!listing.value) await tools.refresh();
  if (!listing.value) {
    error.value = `Recipe ${props.item.id} is not in the catalog.`;
    return;
  }
  modalInitialConfig.value = recipeConfig.value ?? {};
  modalOpen.value = true;
}
defineExpose({ beginInstall });

const installFromModal = async (
  id: string,
  env: Record<string, string>,
  config: Record<string, unknown>,
) => {
  const st = await tools.install(id, env, config);
  recipeConfig.value = { ...config };
  return st;
};

function onModalInstalled() {
  modalOpen.value = false;
  emit('changed');
}

function editConfig() {
  modalInitialConfig.value = recipeConfig.value ?? {};
  modalOpen.value = true;
}

// ── edit recipe (Custom tab) ──────────────────────────────────────────
const editRecipeOpen = ref(false);
const editRecipe = ref<Recipe | null>(null);
const existingIds = computed(() =>
  tools.recipes.value.filter((l) => l.enabled).map((l) => l.recipe.id),
);
function openEditRecipe() {
  if (!listing.value || orgManaged.value) return; // FR-302 parity
  editRecipe.value = listing.value.recipe;
  editRecipeOpen.value = true;
}
function onEditRecipeDone() {
  editRecipeOpen.value = false;
  editRecipe.value = null;
  void tools.refresh();
  emit('changed');
}

// ── remove ────────────────────────────────────────────────────────────
function requestRemove() {
  // Org-provisioned recipes are re-applied by the next bundle poll; the
  // framework refuses them too (install.ErrReadOnly) — guard anyway.
  if (orgManaged.value) return;
  confirmingRemove.value = true;
}
async function confirmRemove() {
  confirmingRemove.value = false;
  busy.value = true;
  error.value = null;
  try {
    await tools.uninstall(props.item.id);
    emit('changed');
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  } finally {
    busy.value = false;
  }
}

async function forgetKey(key: EnvKey) {
  error.value = null;
  try {
    await tools.forgetKey(props.item.id, key.name);
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  }
}

// ── config-derived affordances ────────────────────────────────────────
async function loadConfig() {
  try {
    recipeConfig.value = await tools.config(props.item.id);
  } catch {
    // best-effort: the row falls back to recipe defaults.
  }
}

function allowedDirs(): readonly string[] {
  const dirs = recipeConfig.value?.['allowed_directories'];
  return Array.isArray(dirs) ? dirs.filter((v): v is string => typeof v === 'string') : [];
}

async function openWorkspace() {
  error.value = null;
  if (!recipeConfig.value) await loadConfig();
  const path = allowedDirs()[0];
  if (!path) {
    error.value = 'No workspace directory configured.';
    return;
  }
  try {
    await shell.openInOSBrowser(path);
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  }
}

watch(
  () => [listing.value?.enabled, listing.value?.recipe.configOptions?.length] as const,
  ([enabled, n]) => {
    if (enabled && (n ?? 0) > 0 && recipeConfig.value === null) void loadConfig();
  },
  { immediate: true },
);

// ── warming indicator (first npm fetch) ───────────────────────────────
const COLD_SPAWN_MS = 4000;
const now = ref(Date.now());
let warmTimer: ReturnType<typeof setInterval> | null = null;
onMounted(() => {
  warmTimer = setInterval(() => {
    now.value = Date.now();
  }, 1000);
});
onBeforeUnmount(() => {
  if (warmTimer) clearInterval(warmTimer);
});
function isWarming(state: RecipeState | undefined): boolean {
  if (state !== 'starting') return false;
  const t = tools.startedAt.value[props.item.id];
  return Boolean(t) && now.value - t > COLD_SPAWN_MS;
}

function sourceBadge(): string {
  switch (listing.value?.source) {
    case 'org':
      return 'org-managed';
    case 'registry':
      return 'registry';
    case 'user':
      return 'custom';
    case 'imported':
      return 'imported';
    default:
      return 'shipped';
  }
}

</script>

<template>
  <section class="space-y-3" :data-testid="`recipe-row-${item.id}`">
    <header class="flex items-start gap-3">
      <component
        :is="categoryIconFor(item.category ?? '')"
        class="mt-0.5 h-4 w-4 text-ink-dim"
        aria-hidden="true"
      />
      <div class="min-w-0 flex-1">
        <div class="flex flex-wrap items-center gap-2 font-ui text-[13px] text-ink">
          <span class="font-semibold">{{ item.name }}</span>
          <HealthPill
            v-if="installed && status"
            :state="status.state"
            :data-testid="`recipe-state-${item.id}`"
          />
          <span
            class="text-[10px] uppercase tracking-[0.14em]"
            :class="orgManaged ? 'text-signal-info' : 'text-ink-dim'"
            :data-testid="`recipe-source-${item.id}`"
          >
            {{ sourceBadge() }}
          </span>
          <span
            v-if="orgManaged"
            class="rounded-sm bg-signal-info-soft px-1.5 py-0.5 font-ui text-[10px] font-medium text-signal-info"
            :data-testid="`recipe-org-badge-${item.id}`"
          >
            Provisioned by your org
          </span>
          <span
            v-if="isWarming(status?.state)"
            class="text-[10px] text-signal-warn"
            :data-testid="`recipe-warming-${item.id}`"
          >
            warming… (npm fetch on first run can take 5–15 s)
          </span>
        </div>
        <p class="mt-1 max-w-prose text-[11px] text-ink-muted">{{ item.description }}</p>
      </div>
    </header>

    <div
      v-if="installed && status?.state === 'failed' && status.lastError"
      class="rounded-sm border border-signal-danger bg-signal-danger/10 px-2 py-1 font-mono text-[10px] text-signal-danger"
      role="alert"
      :data-testid="`recipe-health-alert-${item.id}`"
    >
      {{ status.lastError }}
    </div>
    <div
      v-if="error"
      class="text-[11px] text-signal-danger"
      role="alert"
      :data-testid="`recipe-row-error-${item.id}`"
    >
      {{ error }}
    </div>

    <!-- Not installed: the per-kind install flow. -->
    <div v-if="!installed" class="flex items-center gap-2">
      <button
        type="button"
        class="rounded-sm border border-accent-hairline bg-surface-1 px-3 py-1 font-ui text-[12px] text-accent hover:bg-accent-glow"
        :data-testid="`recipe-configure-install-${item.id}`"
        @click="beginInstall"
      >
        Set up and install…
      </button>
    </div>

    <template v-else>
      <div v-if="listing?.keysPresent && (listing?.recipe.envKeys.length ?? 0) > 0" class="flex flex-wrap gap-2">
        <button
          v-for="key in listing!.recipe.envKeys"
          :key="key.name"
          type="button"
          class="rounded-sm border border-border-muted px-2 py-0.5 font-ui text-[10px] uppercase tracking-[0.14em] text-ink-dim hover:bg-surface-2 hover:text-ink"
          :data-testid="`recipe-forget-${item.id}-${key.name}`"
          @click="forgetKey(key)"
        >
          Forget {{ key.display }}
        </button>
      </div>
      <div v-if="item.category === 'filesystem' && status?.state === 'running'">
        <button
          type="button"
          class="inline-flex items-center gap-1 rounded-sm border border-border-muted bg-surface-1 px-2 py-1 font-ui text-[11px] text-ink hover:bg-surface-2"
          :data-testid="`recipe-open-workspace-${item.id}`"
          @click="openWorkspace"
        >
          <FolderOpen class="h-3 w-3" aria-hidden="true" />
          <span>Open workspace</span>
        </button>
      </div>
      <div
        v-if="(listing?.recipe.configOptions?.length ?? 0) > 0"
        class="flex items-start gap-2"
        :data-testid="`recipe-config-summary-${item.id}`"
      >
        <button
          type="button"
          class="rounded-sm border border-border-muted px-2 py-0.5 font-ui text-[10px] uppercase tracking-[0.14em] text-ink-dim hover:bg-surface-2 hover:text-ink"
          :data-testid="`recipe-edit-config-${item.id}`"
          @click="editConfig"
        >
          Edit configuration
        </button>
        <ul v-if="expanded && allowedDirs().length > 0" class="flex flex-wrap gap-1" :data-testid="`recipe-allowed-dirs-${item.id}`">
          <li
            v-for="(p, i) in allowedDirs()"
            :key="`${i}:${p}`"
            class="inline-flex items-center rounded-sm border border-border-muted bg-surface-2 px-1.5 py-0.5 font-mono text-[10px] text-ink"
          >
            {{ p }}
          </li>
        </ul>
      </div>
      <!-- Edit + Remove — hidden for org-managed recipes (spec §2.2,
           FR-302 parity): an edit would be silently re-shadowed by the
           next org bundle, and removal would not stick. -->
      <div v-if="!orgManaged" class="flex items-center gap-2">
        <button
          type="button"
          class="rounded-sm border border-border-muted px-2 py-0.5 font-ui text-[10px] uppercase tracking-[0.14em] text-ink-dim hover:bg-surface-2 hover:text-ink"
          :data-testid="`recipe-edit-btn-${item.id}`"
          @click="openEditRecipe"
        >
          Edit
        </button>
        <button
          type="button"
          class="rounded-sm border border-border-muted px-2 py-0.5 font-ui text-[10px] uppercase tracking-[0.14em] text-signal-danger hover:bg-signal-danger/10 disabled:opacity-50"
          :disabled="busy"
          :data-testid="`recipe-delete-btn-${item.id}`"
          @click="requestRemove"
        >
          Remove
        </button>
      </div>
      <div
        v-if="confirmingRemove"
        class="space-y-2 rounded-sm border border-signal-danger bg-signal-danger/10 px-3 py-2"
        data-testid="recipe-delete-confirm"
      >
        <p class="font-ui text-[12px] text-ink">
          Remove <strong>{{ item.name }}</strong>? This stops the server and removes it from your enabled list.
        </p>
        <div class="flex gap-2">
          <button
            type="button"
            class="rounded-sm border border-signal-danger bg-signal-danger/10 px-2 py-0.5 font-ui text-[10px] uppercase tracking-[0.14em] text-signal-danger hover:bg-signal-danger/20"
            :data-testid="`recipe-delete-confirm-yes-${item.id}`"
            @click="confirmRemove"
          >
            Remove
          </button>
          <button
            type="button"
            class="rounded-sm border border-border-muted px-2 py-0.5 font-ui text-[10px] uppercase tracking-[0.14em] text-ink-dim hover:text-ink"
            :data-testid="`recipe-delete-confirm-no-${item.id}`"
            @click="confirmingRemove = false"
          >
            Cancel
          </button>
        </div>
      </div>
      <button
        type="button"
        class="inline-flex items-center gap-1 text-[10px] uppercase tracking-[0.16em] text-ink-dim hover:text-ink"
        :data-testid="`recipe-detail-toggle-${item.id}`"
        @click="expanded = !expanded"
      >
        <component :is="expanded ? ChevronDown : ChevronRight" class="h-3 w-3" aria-hidden="true" />
        <span>{{ expanded ? 'Hide' : 'Show' }} details</span>
      </button>
      <div
        v-if="expanded && status"
        class="rounded-sm border border-border-muted bg-surface-0 px-3 py-2 font-ui text-[11px] text-ink-muted"
        :data-testid="`recipe-detail-${item.id}`"
      >
        <dl class="grid gap-1" style="grid-template-columns: max-content 1fr">
          <dt class="text-ink-subtle">Protocol</dt>
          <dd class="font-mono text-ink">{{ status.protocolVersion || '—' }}</dd>
          <dt class="text-ink-subtle">Server</dt>
          <dd class="font-mono text-ink">
            {{ status.serverName || '—' }}
            <span v-if="status.serverVersion" class="text-ink-dim">{{ status.serverVersion }}</span>
          </dd>
          <dt class="text-ink-subtle">Tools / Resources / Prompts</dt>
          <dd class="font-mono text-ink">
            {{ status.toolCount }} / {{ status.resourceCount }} / {{ status.promptCount }}
          </dd>
          <dt class="text-ink-subtle">Restart attempts</dt>
          <dd class="font-mono text-ink">{{ status.restartAttempts }}</dd>
          <template v-if="status.lastError">
            <dt class="text-ink-subtle">Last error</dt>
            <dd class="font-mono text-signal-danger">{{ status.lastError }}</dd>
          </template>
        </dl>
        <pre
          v-if="status.stderrTail"
          class="mt-2 max-h-40 overflow-auto rounded-sm bg-surface-2 px-2 py-1 font-mono text-[10px] text-ink"
        >{{ status.stderrTail }}</pre>
      </div>
    </template>

    <RecipeKeyPromptModal
      v-if="listing && modalOpen"
      :open="modalOpen"
      :recipe="listing.recipe"
      :install="installFromModal"
      :initial-config="modalInitialConfig"
      @installed="onModalInstalled"
      @close="modalOpen = false"
    />
    <AddMCPServerModal
      v-if="editRecipeOpen"
      :open="true"
      :edit-recipe="editRecipe"
      :existing-ids="existingIds"
      @installed="onEditRecipeDone"
      @close="editRecipeOpen = false"
    />
  </section>
</template>
