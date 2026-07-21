<script setup lang="ts">
/**
 * KenazToolsPanel — built-in toggleable tools shipped with the
 * harness. v1 hosts the Memory tool; the WP06 extension below it
 * adds the shipped MCP recipes catalog (Brave Search, etc.) with
 * toggle / key-prompt-modal / status pill UX.
 *
 * Memory toggle wires through Settings.SetMemory + Hooks.Install/Remove
 * StarterMemory so flipping it on auto-installs the memory.persist /
 * memory.retrieve hooks and unhides the Memory tab.
 *
 * Recipes section lives below the Memory row. The Memory row is
 * intentionally untouched — privacy CI invariant + WP06 constraint.
 */
import { computed, onMounted, onBeforeUnmount, ref, watch } from 'vue';
import { useRouter } from 'vue-router';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { useToolsRecipes, useShell } from '@/lib/useHarnessAPI';
import {
  Brain,
  CheckSquare,
  ChevronDown,
  ChevronRight,
  Code,
  Folder,
  FolderOpen,
  Globe,
  MessageSquare,
  Scale,
  Search,
  Wrench,
  Zap,
} from '@/shell/icons';
import RecipeKeyPromptModal from './RecipeKeyPromptModal.vue';
import AddMCPServerModal from './AddMCPServerModal.vue';
import HealthPill from './HealthPill.vue';
import type {
  EnvKey,
  Recipe,
  RecipeCategory,
  RecipeListing,
  RecipeState,
  RecipeStatus,
} from '@/lib/types';

const client = useHarnessClient();
const router = useRouter();

// ── Memory tool (unchanged) ────────────────────────────────────────────
const memoryEnabled = ref(false);
const memoryError = ref<string | null>(null);
const memoryBusy = ref(false);

async function refreshMemory() {
  try {
    memoryEnabled.value = await client.settings.getMemory();
  } catch {
    memoryEnabled.value = false;
  }
}

async function toggleMemory(event: Event) {
  if (memoryBusy.value) return;
  const next = (event.target as HTMLInputElement).checked;
  memoryBusy.value = true;
  memoryError.value = null;
  const previous = memoryEnabled.value;
  memoryEnabled.value = next;
  try {
    await client.settings.setMemory(next);
    if (next) {
      await client.hooks.installStarterMemory();
    } else {
      await client.hooks.removeStarterMemory();
    }
  } catch (e) {
    memoryEnabled.value = previous;
    memoryError.value =
      e instanceof Error ? e.message : 'Failed to toggle memory.';
  } finally {
    memoryBusy.value = false;
  }
}

function gotoMemory() {
  void router.push('/memory');
}

// ── Web search built-in (core/tools/websearch) ─────────────────────────
const webSearchEnabled = ref(false);
const webSearchError = ref<string | null>(null);
const webSearchBusy = ref(false);

async function refreshWebSearch() {
  try {
    webSearchEnabled.value = await client.settings.getWebSearch();
  } catch {
    webSearchEnabled.value = false;
  }
}

async function toggleWebSearch(event: Event) {
  if (webSearchBusy.value) return;
  const next = (event.target as HTMLInputElement).checked;
  webSearchBusy.value = true;
  webSearchError.value = null;
  const previous = webSearchEnabled.value;
  webSearchEnabled.value = next;
  try {
    await client.settings.setWebSearch(next);
  } catch (e) {
    webSearchEnabled.value = previous;
    webSearchError.value =
      e instanceof Error ? e.message : 'Failed to toggle web search.';
  } finally {
    webSearchBusy.value = false;
  }
}

// ── Web fetch built-in (core/tools/webfetch) ───────────────────────────
const webFetchEnabled = ref(false);
const webFetchError = ref<string | null>(null);
const webFetchBusy = ref(false);

async function refreshWebFetch() {
  try {
    webFetchEnabled.value = await client.settings.getWebFetchEnabled();
  } catch {
    webFetchEnabled.value = false;
  }
}

async function toggleWebFetch(event: Event) {
  if (webFetchBusy.value) return;
  const next = (event.target as HTMLInputElement).checked;
  webFetchBusy.value = true;
  webFetchError.value = null;
  const previous = webFetchEnabled.value;
  webFetchEnabled.value = next;
  try {
    await client.settings.setWebFetchEnabled(next);
  } catch (e) {
    webFetchEnabled.value = previous;
    webFetchError.value =
      e instanceof Error ? e.message : 'Failed to toggle web fetch.';
  } finally {
    webFetchBusy.value = false;
  }
}

// ── Bash built-in (core/tools/bash) ────────────────────────────────────
const bashEnabled = ref(false);
const bashError = ref<string | null>(null);
const bashBusy = ref(false);

async function refreshBash() {
  try {
    bashEnabled.value = await client.settings.getBash();
  } catch {
    bashEnabled.value = false;
  }
}

async function toggleBash(event: Event) {
  if (bashBusy.value) return;
  const next = (event.target as HTMLInputElement).checked;
  bashBusy.value = true;
  bashError.value = null;
  const previous = bashEnabled.value;
  bashEnabled.value = next;
  try {
    await client.settings.setBash(next);
  } catch (e) {
    bashEnabled.value = previous;
    bashError.value =
      e instanceof Error ? e.message : 'Failed to toggle bash.';
  } finally {
    bashBusy.value = false;
  }
}

// ── Builtin filesystem read tools (builtin-filesystem-tools-01KR3N4P WP06) ──
// Default OFF — tools are disabled until the user opts in.
const fsReadEnabled = ref(false);
const fsReadError = ref<string | null>(null);
const fsReadBusy = ref(false);

async function refreshFSRead() {
  try {
    fsReadEnabled.value = await client.settings.getFSReadEnabled();
  } catch {
    fsReadEnabled.value = false;
  }
}

async function toggleFSRead(event: Event) {
  if (fsReadBusy.value) return;
  const next = (event.target as HTMLInputElement).checked;
  fsReadBusy.value = true;
  fsReadError.value = null;
  const previous = fsReadEnabled.value;
  fsReadEnabled.value = next;
  try {
    await client.settings.setFSReadEnabled(next);
  } catch (e) {
    fsReadEnabled.value = previous;
    fsReadError.value =
      e instanceof Error ? e.message : 'Failed to toggle filesystem read tools.';
  } finally {
    fsReadBusy.value = false;
  }
}

// ── Builtin filesystem write tools (builtin-filesystem-tools-01KR3N4P WP06) ──
// Default OFF — write tools are disabled until the user opts in.
const fsWriteEnabled = ref(false);
const fsWriteError = ref<string | null>(null);
const fsWriteBusy = ref(false);

async function refreshFSWrite() {
  try {
    fsWriteEnabled.value = await client.settings.getFSWriteEnabled();
  } catch {
    fsWriteEnabled.value = false;
  }
}

async function toggleFSWrite(event: Event) {
  if (fsWriteBusy.value) return;
  const next = (event.target as HTMLInputElement).checked;
  fsWriteBusy.value = true;
  fsWriteError.value = null;
  const previous = fsWriteEnabled.value;
  fsWriteEnabled.value = next;
  try {
    await client.settings.setFSWriteEnabled(next);
  } catch (e) {
    fsWriteEnabled.value = previous;
    fsWriteError.value =
      e instanceof Error ? e.message : 'Failed to toggle filesystem write tools.';
  } finally {
    fsWriteBusy.value = false;
  }
}

// ── Todo write built-in (builtin-tools-search-and-elicitation-01KZNP3D WP07) ──
// Default OFF — todo_write modifies session state; the user opts in.
const todoEnabled = ref(false);
const todoError = ref<string | null>(null);
const todoBusy = ref(false);

async function refreshTodo() {
  try {
    todoEnabled.value = await client.settings.getTodoEnabled();
  } catch {
    todoEnabled.value = false;
  }
}

async function toggleTodo(event: Event) {
  if (todoBusy.value) return;
  const next = (event.target as HTMLInputElement).checked;
  todoBusy.value = true;
  todoError.value = null;
  const previous = todoEnabled.value;
  todoEnabled.value = next;
  try {
    await client.settings.setTodoEnabled(next);
  } catch (e) {
    todoEnabled.value = previous;
    todoError.value =
      e instanceof Error ? e.message : 'Failed to toggle todo_write.';
  } finally {
    todoBusy.value = false;
  }
}

// ── Save artifact built-in (core/tools/saveartifact) ───────────────────
// Default ON: saving deliverables is a low-risk primitive that should
// work on first launch. Reads default to true on backend errors so a
// settings-store glitch doesn't disable the dial UI.
const saveArtifactEnabled = ref(true);
const saveArtifactError = ref<string | null>(null);
const saveArtifactBusy = ref(false);

async function refreshSaveArtifact() {
  try {
    saveArtifactEnabled.value = await client.settings.getSaveArtifact();
  } catch {
    saveArtifactEnabled.value = true;
  }
}

async function toggleSaveArtifact(event: Event) {
  if (saveArtifactBusy.value) return;
  const next = (event.target as HTMLInputElement).checked;
  saveArtifactBusy.value = true;
  saveArtifactError.value = null;
  const previous = saveArtifactEnabled.value;
  saveArtifactEnabled.value = next;
  try {
    await client.settings.setSaveArtifact(next);
  } catch (e) {
    saveArtifactEnabled.value = previous;
    saveArtifactError.value =
      e instanceof Error ? e.message : 'Failed to toggle save_artifact.';
  } finally {
    saveArtifactBusy.value = false;
  }
}

onMounted(() => {
  void refreshMemory();
  void refreshWebSearch();
  void refreshWebFetch();
  void refreshBash();
  void refreshFSRead();
  void refreshFSWrite();
  void refreshSaveArtifact();
  void refreshTodo();
});

// ── Recipes (WP06) ─────────────────────────────────────────────────────
const tools = useToolsRecipes();
const recipes = tools.recipes;
const startedAt = tools.startedAt;
const recipesLoading = tools.loading;
const recipesError = tools.error;

const shell = useShell();

const busyById = ref<Record<string, boolean>>({});
const rowError = ref<Record<string, string | null>>({});
const expanded = ref<Record<string, boolean>>({});
const modalOpen = ref(false);
const modalRecipe = ref<Recipe | null>(null);
const modalInitialConfig = ref<Record<string, unknown>>({});
const recipeConfigs = ref<Record<string, Record<string, unknown>>>({});

// Tick the warming indicator without resorting to per-row timers.
const now = ref<number>(Date.now());
let warmTimer: ReturnType<typeof setInterval> | null = null;

onMounted(() => {
  warmTimer = setInterval(() => {
    now.value = Date.now();
  }, 1000);
});

onBeforeUnmount(() => {
  if (warmTimer) {
    clearInterval(warmTimer);
    warmTimer = null;
  }
});

const COLD_SPAWN_MS = 4000;

function isWarming(id: string, state: RecipeState): boolean {
  if (state !== 'starting') return false;
  const t = startedAt.value[id];
  if (!t) return false;
  return now.value - t > COLD_SPAWN_MS;
}

function categoryIcon(category: RecipeCategory) {
  switch (category) {
    case 'search':
      return Search;
    case 'filesystem':
      return Folder;
    case 'memory':
      return Brain;
    case 'fetch':
      return Globe;
    case 'productivity':
      return CheckSquare;
    case 'developer':
      return Code;
    case 'finance':
      return Scale;
    case 'communication':
      return MessageSquare;
    case 'automation':
      return Zap;
    default:
      return Wrench;
  }
}

function setRowError(id: string, msg: string | null) {
  rowError.value = { ...rowError.value, [id]: msg };
}

function recipeNeedsModal(recipe: Recipe, keysPresent: boolean): boolean {
  // Recipes with declared ConfigOptions ALWAYS go through the modal so
  // the user can review / edit the directory list (or future
  // boolean / string knobs). Recipes carrying a Warning ALWAYS go
  // through the modal so the user must explicitly confirm. Recipes
  // without ConfigOptions/Warning only need the modal when their env
  // keys haven't been resolved yet.
  if ((recipe.configOptions?.length ?? 0) > 0) return true;
  if (recipe.warning) return true;
  return !keysPresent;
}

async function onToggle(listing: RecipeListing, event: Event) {
  const target = event.target as HTMLInputElement;
  const next = target.checked;
  const id = listing.recipe.id;
  if (busyById.value[id]) {
    target.checked = listing.enabled;
    return;
  }
  setRowError(id, null);
  if (next) {
    if (recipeNeedsModal(listing.recipe, listing.keysPresent)) {
      // Revert the optimistic toggle until the modal commits.
      target.checked = false;
      modalRecipe.value = listing.recipe;
      modalInitialConfig.value = recipeConfigs.value[id] ?? {};
      modalOpen.value = true;
    } else {
      await doInstall(listing.recipe, {}, {});
    }
  } else {
    await doUninstall(id);
  }
}

async function doInstall(
  recipe: Recipe,
  env: Record<string, string>,
  config: Record<string, unknown>,
) {
  const id = recipe.id;
  busyById.value = { ...busyById.value, [id]: true };
  setRowError(id, null);
  try {
    await tools.install(id, env, config);
    // Cache the freshly-installed config so the "Open workspace" + "Edit
    // configuration" affordances on the row can resolve it without a
    // round-trip on every render.
    recipeConfigs.value = { ...recipeConfigs.value, [id]: { ...config } };
  } catch (e) {
    setRowError(id, e instanceof Error ? e.message : String(e));
    throw e;
  } finally {
    busyById.value = { ...busyById.value, [id]: false };
  }
}

async function doUninstall(id: string) {
  busyById.value = { ...busyById.value, [id]: true };
  setRowError(id, null);
  try {
    await tools.uninstall(id);
  } catch (e) {
    setRowError(id, e instanceof Error ? e.message : String(e));
  } finally {
    busyById.value = { ...busyById.value, [id]: false };
  }
}

async function onForgetKey(listing: RecipeListing, key: EnvKey) {
  const id = listing.recipe.id;
  setRowError(id, null);
  try {
    await tools.forgetKey(id, key.name);
  } catch (e) {
    setRowError(id, e instanceof Error ? e.message : String(e));
  }
}

function toggleExpanded(id: string) {
  expanded.value = { ...expanded.value, [id]: !expanded.value[id] };
}

function onModalInstalled() {
  // The modal's caller (this component) routed `install` through
  // `tools.install`, so the row state is already merged. Just close.
  modalOpen.value = false;
  modalRecipe.value = null;
  modalInitialConfig.value = {};
}

function onModalClose() {
  modalOpen.value = false;
  modalRecipe.value = null;
  modalInitialConfig.value = {};
}

const installFromModal = async (
  id: string,
  env: Record<string, string>,
  config: Record<string, unknown>,
) => {
  const recipe = modalRecipe.value;
  if (!recipe || recipe.id !== id) {
    throw new Error('Modal recipe mismatch');
  }
  const status = await tools.install(id, env, config);
  recipeConfigs.value = { ...recipeConfigs.value, [id]: { ...config } };
  return status;
};

async function loadConfigFor(id: string) {
  try {
    const cfg = await tools.config(id);
    recipeConfigs.value = { ...recipeConfigs.value, [id]: cfg };
  } catch {
    // best-effort; the row falls back to recipe defaults if the
    // backend lookup fails.
  }
}

function workspacePathFor(listing: RecipeListing): string | null {
  if (listing.recipe.category !== 'filesystem') return null;
  const cfg = recipeConfigs.value[listing.recipe.id];
  if (!cfg) return null;
  const dirs = cfg['allowed_directories'];
  if (Array.isArray(dirs) && dirs.length > 0) {
    const first = dirs[0];
    if (typeof first === 'string' && first !== '') return first;
  }
  return null;
}

function allowedDirsFor(listing: RecipeListing): readonly string[] {
  const cfg = recipeConfigs.value[listing.recipe.id];
  if (!cfg) return [];
  const dirs = cfg['allowed_directories'];
  if (!Array.isArray(dirs)) return [];
  return dirs.filter((v): v is string => typeof v === 'string');
}

async function openWorkspace(listing: RecipeListing) {
  const id = listing.recipe.id;
  setRowError(id, null);
  let path = workspacePathFor(listing);
  if (!path) {
    // Cold path: no cached config. Fetch on demand.
    await loadConfigFor(id);
    path = workspacePathFor(listing);
  }
  if (!path) {
    setRowError(id, 'No workspace directory configured.');
    return;
  }
  try {
    await shell.openInOSBrowser(path);
  } catch (e) {
    setRowError(id, e instanceof Error ? e.message : String(e));
  }
}

function editConfig(listing: RecipeListing) {
  modalRecipe.value = listing.recipe;
  modalInitialConfig.value = recipeConfigs.value[listing.recipe.id] ?? {};
  modalOpen.value = true;
}

// Show only enabled (installed) recipes in the main panel. The full
// catalog is browsable via the Add MCP Server modal (RegistryTab).
// This ensures a clean-install shows zero rows, and that the Delete
// button is only presented for recipes that are actually enabled —
// clicking Delete on a non-enabled catalog entry would be a no-op.

// WP11: text filter for the MCP recipes list.
const mcpTextFilter = ref<string>('');

const visibleRecipes = computed<readonly RecipeListing[]>(() => {
  const enabled = recipes.value.filter((l) => l.enabled);
  const q = mcpTextFilter.value.trim().toLowerCase();
  if (!q) return enabled;
  return enabled.filter(
    (l) =>
      l.recipe.displayName.toLowerCase().includes(q) ||
      (l.recipe.description ?? '').toLowerCase().includes(q),
  );
});

function statusOf(listing: RecipeListing): RecipeStatus {
  return listing.status;
}

// ── Add MCP Server modal (WP09) ────────────────────────────────────────
const addModalOpen = ref(false);
const addModalEditRecipe = ref<Recipe | null>(null);

const existingRecipeIds = computed<readonly string[]>(() =>
  visibleRecipes.value.map((l) => l.recipe.id),
);

function openAddModal() {
  addModalEditRecipe.value = null;
  addModalOpen.value = true;
}

function openEditModal(listing: RecipeListing) {
  addModalEditRecipe.value = listing.recipe;
  addModalOpen.value = true;
}

function onAddModalClose() {
  addModalOpen.value = false;
  addModalEditRecipe.value = null;
}

function onAddModalInstalled() {
  addModalOpen.value = false;
  addModalEditRecipe.value = null;
  // Refresh the recipe list to pick up newly installed/imported servers.
  void tools.refresh();
}

// ── Delete recipe ──────────────────────────────────────────────────────
const deleteConfirmId = ref<string | null>(null);

function requestDelete(id: string) {
  deleteConfirmId.value = id;
}

async function confirmDelete() {
  const id = deleteConfirmId.value;
  if (!id) return;
  deleteConfirmId.value = null;
  await doUninstall(id);
}

function cancelDelete() {
  deleteConfirmId.value = null;
}

/**
 * sourceBadge — derive a human-readable source label for a recipe row.
 *
 * BACKEND GAP: The wire shape does not yet carry a `source` discriminator.
 * When the backend adds it (WP10+), read it directly instead of this
 * heuristic fallback.
 *
 * Current heuristic: all recipes returned by Tools_ListRecipes are
 * considered "shipped" because that is the only catalog the backend
 * exposes today. Registry / user / imported will be distinguishable once
 * the backend surfaces the field.
 */
function sourceBadge(_listing: RecipeListing): string {
  return 'shipped';
}

function sourceBadgeClass(_listing: RecipeListing): string {
  return 'text-ink-dim';
}

// Whenever the recipes list refreshes, fetch persisted config for any
// enabled row that needs ConfigOption awareness (filesystem currently;
// future categories that ship configOptions auto-pick up the same
// path).
watch(
  () => recipes.value,
  (list) => {
    for (const r of list) {
      const needsConfig =
        r.enabled && (r.recipe.configOptions?.length ?? 0) > 0;
      if (needsConfig && !(r.recipe.id in recipeConfigs.value)) {
        void loadConfigFor(r.recipe.id);
      }
    }
  },
  { immediate: true },
);
</script>

<template>
  <section class="px-6 py-4">
    <h2
      class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle mb-3"
    >
      Kenaz tools
    </h2>

    <div
      class="rounded-sm border border-border-muted bg-surface-1 divide-y divide-border-muted"
    >
      <!-- Web search row (built-in; local-only DuckDuckGo + Wikipedia) -->
      <label
        class="px-4 py-3 grid gap-3 items-start cursor-pointer"
        style="grid-template-columns: 1fr auto"
        data-testid="websearch-tool-row"
      >
        <div>
          <div class="flex items-center gap-2 font-ui text-[13px] text-ink">
            <span>Web search</span>
            <span
              v-if="webSearchEnabled"
              class="text-[10px] uppercase tracking-[0.16em] text-signal-ok"
            >
              on
            </span>
          </div>
          <p class="mt-1 text-[11px] text-ink-muted max-w-prose">
            Local-first web search built into the harness. Queries DuckDuckGo's
            HTML endpoint + Wikipedia's API and extracts readable content via
            go-readability. No API key, no third-party SDK, no data leaves
            your machine beyond the search query itself. Cedar policy gates
            the outbound HTTP request hostname.
          </p>
          <div
            v-if="webSearchError"
            class="mt-2 text-[11px] text-signal-danger"
            role="alert"
          >
            {{ webSearchError }}
          </div>
        </div>
        <div
          class="inline-flex items-center select-none"
          :class="webSearchBusy ? 'opacity-60 cursor-wait' : ''"
        >
          <input
            type="checkbox"
            class="accent-accent w-4 h-4"
            :checked="webSearchEnabled"
            :disabled="webSearchBusy"
            aria-label="Enable web search tool"
            data-testid="websearch-toggle"
            @change="toggleWebSearch"
          />
        </div>
      </label>

      <!-- Web fetch row (built-in; Cedar-gated network fetch) -->
      <label
        class="px-4 py-3 grid gap-3 items-start cursor-pointer"
        style="grid-template-columns: 1fr auto"
        data-testid="webfetch-tool-row"
      >
        <div>
          <div class="flex items-center gap-2 font-ui text-[13px] text-ink">
            <span>Web fetch</span>
            <span
              v-if="webFetchEnabled"
              class="text-[10px] uppercase tracking-[0.16em] text-signal-ok"
            >
              on
            </span>
          </div>
          <p class="mt-1 text-[11px] text-ink-muted max-w-prose">
            Lets the assistant fetch URLs and resolve
            <code class="font-mono">@secret:</code> references in request
            headers. Outbound HTTP is gated by Cedar policy (host allowlist).
            Default OFF — enable only when the assistant needs to read live web
            content or call authenticated endpoints.
          </p>
          <div
            v-if="webFetchError"
            class="mt-2 text-[11px] text-signal-danger"
            role="alert"
          >
            {{ webFetchError }}
          </div>
        </div>
        <div
          class="inline-flex items-center select-none"
          :class="webFetchBusy ? 'opacity-60 cursor-wait' : ''"
        >
          <input
            type="checkbox"
            class="accent-accent w-4 h-4"
            :checked="webFetchEnabled"
            :disabled="webFetchBusy"
            aria-label="Enable web fetch tool"
            data-testid="webfetch-toggle"
            @change="toggleWebFetch"
          />
        </div>
      </label>

      <!-- Bash row (built-in; sandboxed via per-command allowlist) -->
      <label
        class="px-4 py-3 grid gap-3 items-start cursor-pointer"
        style="grid-template-columns: 1fr auto"
        data-testid="bash-tool-row"
      >
        <div>
          <div class="flex items-center gap-2 font-ui text-[13px] text-ink">
            <span>Bash</span>
            <span
              v-if="bashEnabled"
              class="text-[10px] uppercase tracking-[0.16em] text-signal-ok"
            >
              on
            </span>
          </div>
          <p class="mt-1 text-[11px] text-ink-muted max-w-prose">
            Local bash execution gated by a per-command allowlist. The model
            can only run commands you've allowed; everything else is denied
            at parse time. Output stays local and is gated through the Cedar
            policy engine. Use Bash with care — pair with the
            <code class="font-mono">filesystem</code> recipe or
            <code class="font-mono">read_bash_output</code> state node to
            keep results in context.
          </p>
          <div
            v-if="bashError"
            class="mt-2 text-[11px] text-signal-danger"
            role="alert"
          >
            {{ bashError }}
          </div>
        </div>
        <div
          class="inline-flex items-center select-none"
          :class="bashBusy ? 'opacity-60 cursor-wait' : ''"
        >
          <input
            type="checkbox"
            class="accent-accent w-4 h-4"
            :checked="bashEnabled"
            :disabled="bashBusy"
            aria-label="Enable bash tool"
            data-testid="bash-toggle"
            @change="toggleBash"
          />
        </div>
      </label>

      <!-- Save artifact row (built-in; CAS-only, default ON) -->
      <label
        class="px-4 py-3 grid gap-3 items-start cursor-pointer"
        style="grid-template-columns: 1fr auto"
        data-testid="saveartifact-tool-row"
      >
        <div>
          <div class="flex items-center gap-2 font-ui text-[13px] text-ink">
            <span>Save artifact</span>
            <span
              v-if="saveArtifactEnabled"
              class="text-[10px] uppercase tracking-[0.16em] text-signal-ok"
            >
              on
            </span>
          </div>
          <p class="mt-1 text-[11px] text-ink-muted max-w-prose">
            Lets the assistant save deliverables (notes, code, summaries)
            straight to the Artifacts tab. Content is stored content-addressed
            in the same media-store the rest of the harness uses — no
            filesystem touch, no MCP filesystem recipe required. Default ON;
            the model picks this tool when you ask it to save, export, or
            produce a document.
          </p>
          <div
            v-if="saveArtifactError"
            class="mt-2 text-[11px] text-signal-danger"
            role="alert"
          >
            {{ saveArtifactError }}
          </div>
        </div>
        <div
          class="inline-flex items-center select-none"
          :class="saveArtifactBusy ? 'opacity-60 cursor-wait' : ''"
        >
          <input
            type="checkbox"
            class="accent-accent w-4 h-4"
            :checked="saveArtifactEnabled"
            :disabled="saveArtifactBusy"
            aria-label="Enable save artifact tool"
            data-testid="saveartifact-toggle"
            @change="toggleSaveArtifact"
          />
        </div>
      </label>

      <!-- Builtin filesystem read tools row (builtin-filesystem-tools-01KR3N4P WP06) -->
      <label
        class="px-4 py-3 grid gap-3 items-start cursor-pointer"
        style="grid-template-columns: 1fr auto"
        data-testid="fs-read-tool-row"
      >
        <div>
          <div class="flex items-center gap-2 font-ui text-[13px] text-ink">
            <span>Filesystem read tools</span>
            <span
              v-if="fsReadEnabled"
              class="text-[10px] uppercase tracking-[0.16em] text-signal-ok"
            >
              on
            </span>
          </div>
          <p class="mt-1 text-[11px] text-ink-muted max-w-prose">
            Enables the read-family builtin tools shipped with the harness:
          </p>
          <ul class="mt-1 font-mono text-[10px] text-ink-dim list-none">
            <li>kenaz__read_file</li>
            <li>kenaz__list_dir</li>
            <li>kenaz__glob</li>
            <li>kenaz__grep</li>
            <li>kenaz__list_open_worklist</li>
          </ul>
          <p class="mt-1 text-[11px] text-ink-muted max-w-prose">
            Default OFF. Cedar policy still applies to every call. Filesystem access
            requests are gated by the per-directory allowlist.
          </p>
          <div
            v-if="fsReadError"
            class="mt-2 text-[11px] text-signal-danger"
            role="alert"
          >
            {{ fsReadError }}
          </div>
        </div>
        <div
          class="inline-flex items-center select-none"
          :class="fsReadBusy ? 'opacity-60 cursor-wait' : ''"
        >
          <input
            type="checkbox"
            class="accent-accent w-4 h-4"
            :checked="fsReadEnabled"
            :disabled="fsReadBusy"
            aria-label="Enable filesystem read tool"
            data-testid="fs-read-toggle"
            @change="toggleFSRead"
          />
        </div>
      </label>

      <!-- Builtin filesystem write tools row (builtin-filesystem-tools-01KR3N4P WP06) -->
      <label
        class="px-4 py-3 grid gap-3 items-start cursor-pointer"
        style="grid-template-columns: 1fr auto"
        data-testid="fs-write-tool-row"
      >
        <div>
          <div class="flex items-center gap-2 font-ui text-[13px] text-ink">
            <span>Filesystem write tools</span>
            <span
              v-if="fsWriteEnabled"
              class="text-[10px] uppercase tracking-[0.16em] text-signal-ok"
            >
              on
            </span>
          </div>
          <p class="mt-1 text-[11px] text-ink-muted max-w-prose">
            Enables the write-family builtin tools shipped with the harness:
          </p>
          <ul class="mt-1 font-mono text-[10px] text-ink-dim list-none">
            <li>kenaz__write_file</li>
            <li>kenaz__edit_file</li>
          </ul>
          <p class="mt-1 text-[11px] text-ink-muted max-w-prose">
            Default OFF. Write tools modify your filesystem directly.
            Cedar policy still applies — read about gating in the
            <a
              href="https://cedar-spec.org/docs"
              target="_blank"
              rel="noopener noreferrer"
              class="underline hover:text-ink"
            >Cedar policy docs</a>.
          </p>
          <div
            v-if="fsWriteError"
            class="mt-2 text-[11px] text-signal-danger"
            role="alert"
          >
            {{ fsWriteError }}
          </div>
        </div>
        <div
          class="inline-flex items-center select-none"
          :class="fsWriteBusy ? 'opacity-60 cursor-wait' : ''"
        >
          <input
            type="checkbox"
            class="accent-accent w-4 h-4"
            :checked="fsWriteEnabled"
            :disabled="fsWriteBusy"
            aria-label="Enable filesystem write tool"
            data-testid="fs-write-toggle"
            @change="toggleFSWrite"
          />
        </div>
      </label>

      <!-- Todo write tool row (builtin-tools-search-and-elicitation-01KZNP3D WP07) -->
      <label
        class="px-4 py-3 grid gap-3 items-start cursor-pointer"
        style="grid-template-columns: 1fr auto"
        data-testid="todo-tool-row"
      >
        <div>
          <div class="flex items-center gap-2 font-ui text-[13px] text-ink">
            <span>Task list</span>
            <span
              v-if="todoEnabled"
              class="text-[10px] uppercase tracking-[0.16em] text-signal-ok"
            >
              on
            </span>
          </div>
          <p class="mt-1 text-[11px] text-ink-muted max-w-prose">
            Enables the <code class="font-mono">kenaz__todo_write</code> builtin.
            The model maintains a session-scoped structured task list — items have
            status (pending, in_progress, done, cancelled) and priority. The list
            is written atomically on each call and visible in the chat via the
            task-list chip. Default OFF.
          </p>
          <div
            v-if="todoError"
            class="mt-2 text-[11px] text-signal-danger"
            role="alert"
          >
            {{ todoError }}
          </div>
        </div>
        <div
          class="inline-flex items-center select-none"
          :class="todoBusy ? 'opacity-60 cursor-wait' : ''"
        >
          <input
            type="checkbox"
            class="accent-accent w-4 h-4"
            :checked="todoEnabled"
            :disabled="todoBusy"
            aria-label="Enable todo tool"
            data-testid="todo-toggle"
            @change="toggleTodo"
          />
        </div>
      </label>

      <!-- Memory tool row -->
      <label
        class="px-4 py-3 grid gap-3 items-start cursor-pointer"
        style="grid-template-columns: 1fr auto"
      >
        <div>
          <div class="flex items-center gap-2 font-ui text-[13px] text-ink">
            <span>Long-term memory</span>
            <span
              v-if="memoryEnabled"
              class="text-[10px] uppercase tracking-[0.16em] text-signal-ok"
            >
              on
            </span>
          </div>
          <p class="mt-1 text-[11px] text-ink-muted max-w-prose">
            Cross-session memory. Pin messages with 📌 to embed them; the
            harness retrieves relevant memories before each model call. When
            enabled, the memory.retrieve / memory.persist hooks install
            automatically and the Memory tab unhides. Requires an OpenAI
            provider for embeddings. Data lives at
            <span class="font-mono">&lt;DataDir&gt;/memory.gob</span>.
          </p>
          <div v-if="memoryEnabled" class="mt-2">
            <button
              type="button"
              class="px-2 py-0.5 rounded-sm border border-border-muted text-ink text-[10px] uppercase tracking-[0.16em] hover:bg-surface-2"
              data-testid="memory-view-link"
              @click="gotoMemory"
            >
              View saved memories →
            </button>
          </div>
          <div
            v-if="memoryError"
            class="mt-2 text-[11px] text-signal-danger"
            role="alert"
          >
            {{ memoryError }}
          </div>
        </div>
        <div
          class="inline-flex items-center select-none"
          :class="memoryBusy ? 'opacity-60 cursor-wait' : ''"
        >
          <input
            type="checkbox"
            class="accent-accent w-4 h-4"
            :checked="memoryEnabled"
            :disabled="memoryBusy"
            aria-label="Enable memory tool"
            data-testid="memory-toggle"
            @change="toggleMemory"
          />
        </div>
      </label>
    </div>

    <!-- Connected MCP recipes header + Add CTA -->
    <div class="mt-6 flex items-center justify-between mb-2">
      <h2
        class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
      >
        Connected MCP recipes
      </h2>
      <button
        type="button"
        class="rounded-sm border border-accent-hairline bg-surface-1 px-3 py-1 font-ui text-[11px] uppercase tracking-[0.16em] text-accent hover:bg-accent-glow"
        data-testid="add-mcp-server-btn"
        @click="openAddModal"
      >
        + Add MCP Server
      </button>
    </div>

    <!-- WP11: text filter for MCP recipes -->
    <div class="mb-3">
      <input
        v-model="mcpTextFilter"
        type="text"
        placeholder="Filter MCP servers…"
        aria-label="Filter MCP servers"
        spellcheck="false"
        autocomplete="off"
        class="w-full rounded-sm border border-border-muted bg-surface-1 px-3 py-1.5 font-ui text-[12px] text-ink placeholder:text-ink-dim focus:border-accent focus:outline-none"
        data-testid="mcp-text-filter"
      />
    </div>

    <div
      v-if="recipesLoading && visibleRecipes.length === 0"
      class="px-1 py-2 font-ui text-[12px] text-ink-muted"
      data-testid="recipes-loading"
    >
      Loading recipes…
    </div>
    <div
      v-else-if="recipesError"
      class="rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
      role="alert"
      data-testid="recipes-error"
    >
      {{ recipesError }}
    </div>
    <div
      v-else-if="visibleRecipes.length === 0 && !mcpTextFilter"
      class="rounded-sm border border-border-muted bg-surface-1 px-4 py-3 font-ui text-[12px] text-ink-muted"
      data-testid="recipes-empty"
    >
      No MCP servers installed. Click <strong>+ Add MCP Server</strong> to browse the catalog and install one.
    </div>
    <div
      v-else-if="visibleRecipes.length === 0 && mcpTextFilter"
      class="rounded-sm border border-border-muted bg-surface-1 px-4 py-3 font-ui text-[12px] text-ink-muted"
      data-testid="recipes-filter-empty"
    >
      No MCP servers match "{{ mcpTextFilter }}"
    </div>

    <div
      v-else
      class="rounded-sm border border-border-muted bg-surface-1 divide-y divide-border-muted"
      data-testid="recipes-list"
    >
      <div
        v-for="listing in visibleRecipes"
        :key="listing.recipe.id"
        class="px-4 py-3"
        :data-testid="`recipe-row-${listing.recipe.id}`"
      >
        <div
          class="grid gap-3 items-start"
          style="grid-template-columns: 1.25rem 1fr auto"
        >
          <component
            :is="categoryIcon(listing.recipe.category)"
            class="mt-0.5 h-4 w-4 text-ink-dim"
            aria-hidden="true"
          />
          <div>
            <div class="flex items-center gap-2 font-ui text-[13px] text-ink">
              <span>{{ listing.recipe.displayName }}</span>
              <!-- WP04: HealthPill replaces the plain-text state span -->
              <HealthPill
                :state="statusOf(listing).state"
                :data-testid="`recipe-state-${listing.recipe.id}`"
              />
              <!-- Source badge -->
              <span
                class="text-[10px] uppercase tracking-[0.14em]"
                :class="sourceBadgeClass(listing)"
                :data-testid="`recipe-source-${listing.recipe.id}`"
              >
                {{ sourceBadge(listing) }}
              </span>
              <span
                v-if="isWarming(listing.recipe.id, statusOf(listing).state)"
                class="text-[10px] text-signal-warn"
                :data-testid="`recipe-warming-${listing.recipe.id}`"
              >
                warming… (npm fetch on first run can take 5–15 s)
              </span>
            </div>
            <p class="mt-1 text-[11px] text-ink-muted max-w-prose">
              {{ listing.recipe.description }}
            </p>
            <!-- WP05: Health alert banner — visible when server is in an
                 error or failed state. Surfaces lastError without the
                 user having to expand the detail panel. -->
            <div
              v-if="statusOf(listing).state === 'failed' && statusOf(listing).lastError"
              class="mt-1 rounded-sm border border-signal-danger bg-signal-danger/10 px-2 py-1 font-mono text-[10px] text-signal-danger"
              role="alert"
              :data-testid="`recipe-health-alert-${listing.recipe.id}`"
            >
              {{ statusOf(listing).lastError }}
            </div>
            <div
              v-if="rowError[listing.recipe.id]"
              class="mt-1 text-[11px] text-signal-danger"
              role="alert"
              :data-testid="`recipe-row-error-${listing.recipe.id}`"
            >
              {{ rowError[listing.recipe.id] }}
            </div>
            <div
              v-if="listing.keysPresent && listing.recipe.envKeys.length > 0"
              class="mt-2 flex flex-wrap gap-2"
            >
              <button
                v-for="key in listing.recipe.envKeys"
                :key="key.name"
                type="button"
                class="rounded-sm border border-border-muted px-2 py-0.5 font-ui text-[10px] uppercase tracking-[0.14em] text-ink-dim hover:text-ink hover:bg-surface-2"
                :data-testid="`recipe-forget-${listing.recipe.id}-${key.name}`"
                @click="onForgetKey(listing, key)"
              >
                Forget {{ key.display }}
              </button>
            </div>
            <div
              v-if="
                listing.enabled &&
                listing.recipe.category === 'filesystem' &&
                statusOf(listing).state === 'running'
              "
              class="mt-2"
            >
              <button
                type="button"
                class="inline-flex items-center gap-1 rounded-sm border border-border-muted bg-surface-1 px-2 py-1 font-ui text-[11px] text-ink hover:bg-surface-2"
                :data-testid="`recipe-open-workspace-${listing.recipe.id}`"
                @click="openWorkspace(listing)"
              >
                <FolderOpen class="h-3 w-3" aria-hidden="true" />
                <span>Open workspace</span>
              </button>
            </div>
            <div
              v-if="
                listing.enabled &&
                (listing.recipe.configOptions?.length ?? 0) > 0
              "
              class="mt-2 flex items-start gap-2"
              :data-testid="`recipe-config-summary-${listing.recipe.id}`"
            >
              <button
                type="button"
                class="rounded-sm border border-border-muted px-2 py-0.5 font-ui text-[10px] uppercase tracking-[0.14em] text-ink-dim hover:text-ink hover:bg-surface-2"
                :data-testid="`recipe-edit-config-${listing.recipe.id}`"
                @click="editConfig(listing)"
              >
                Edit configuration
              </button>
              <ul
                v-if="
                  expanded[listing.recipe.id] &&
                  allowedDirsFor(listing).length > 0
                "
                class="flex flex-wrap gap-1"
                :data-testid="`recipe-allowed-dirs-${listing.recipe.id}`"
              >
                <li
                  v-for="(p, i) in allowedDirsFor(listing)"
                  :key="`${i}:${p}`"
                  class="inline-flex items-center rounded-sm border border-border-muted bg-surface-2 px-1.5 py-0.5 font-mono text-[10px] text-ink"
                >
                  {{ p }}
                </li>
              </ul>
            </div>
            <!-- Edit + Delete row actions -->
            <div class="mt-2 flex items-center gap-2">
              <button
                type="button"
                class="rounded-sm border border-border-muted px-2 py-0.5 font-ui text-[10px] uppercase tracking-[0.14em] text-ink-dim hover:text-ink hover:bg-surface-2"
                :data-testid="`recipe-edit-btn-${listing.recipe.id}`"
                @click="openEditModal(listing)"
              >
                Edit
              </button>
              <button
                type="button"
                class="rounded-sm border border-border-muted px-2 py-0.5 font-ui text-[10px] uppercase tracking-[0.14em] text-signal-danger hover:bg-signal-danger/10"
                :data-testid="`recipe-delete-btn-${listing.recipe.id}`"
                @click="requestDelete(listing.recipe.id)"
              >
                Delete
              </button>
            </div>
            <!-- Delete confirmation dialog -->
            <div
              v-if="deleteConfirmId === listing.recipe.id"
              class="mt-2 rounded-sm border border-signal-danger bg-signal-danger/10 px-3 py-2 space-y-2"
              data-testid="recipe-delete-confirm"
            >
              <p class="font-ui text-[12px] text-ink">
                Remove <strong>{{ listing.recipe.displayName }}</strong>? This stops the server and removes it from your enabled list.
              </p>
              <div class="flex gap-2">
                <button
                  type="button"
                  class="rounded-sm border border-signal-danger bg-signal-danger/10 px-2 py-0.5 font-ui text-[10px] uppercase tracking-[0.14em] text-signal-danger hover:bg-signal-danger/20"
                  :data-testid="`recipe-delete-confirm-yes-${listing.recipe.id}`"
                  @click="confirmDelete"
                >
                  Remove
                </button>
                <button
                  type="button"
                  class="rounded-sm border border-border-muted px-2 py-0.5 font-ui text-[10px] uppercase tracking-[0.14em] text-ink-dim hover:text-ink"
                  :data-testid="`recipe-delete-confirm-no-${listing.recipe.id}`"
                  @click="cancelDelete"
                >
                  Cancel
                </button>
              </div>
            </div>
            <button
              type="button"
              class="mt-2 inline-flex items-center gap-1 text-[10px] uppercase tracking-[0.16em] text-ink-dim hover:text-ink"
              :data-testid="`recipe-detail-toggle-${listing.recipe.id}`"
              @click="toggleExpanded(listing.recipe.id)"
            >
              <component
                :is="expanded[listing.recipe.id] ? ChevronDown : ChevronRight"
                class="h-3 w-3"
                aria-hidden="true"
              />
              <span>{{ expanded[listing.recipe.id] ? 'Hide' : 'Show' }} details</span>
            </button>
            <div
              v-if="expanded[listing.recipe.id]"
              class="mt-2 rounded-sm border border-border-muted bg-surface-0 px-3 py-2 font-ui text-[11px] text-ink-muted"
              :data-testid="`recipe-detail-${listing.recipe.id}`"
            >
              <dl class="grid gap-1" style="grid-template-columns: max-content 1fr">
                <dt class="text-ink-subtle">Protocol</dt>
                <dd class="font-mono text-ink">
                  {{ statusOf(listing).protocolVersion || '—' }}
                </dd>
                <dt class="text-ink-subtle">Server</dt>
                <dd class="font-mono text-ink">
                  {{ statusOf(listing).serverName || '—' }}
                  <span
                    v-if="statusOf(listing).serverVersion"
                    class="text-ink-dim"
                  >
                    {{ statusOf(listing).serverVersion }}
                  </span>
                </dd>
                <dt class="text-ink-subtle">Tools / Resources / Prompts</dt>
                <dd class="font-mono text-ink">
                  {{ statusOf(listing).toolCount }} /
                  {{ statusOf(listing).resourceCount }} /
                  {{ statusOf(listing).promptCount }}
                </dd>
                <dt class="text-ink-subtle">Restart attempts</dt>
                <dd class="font-mono text-ink">
                  {{ statusOf(listing).restartAttempts }}
                </dd>
                <dt
                  v-if="statusOf(listing).lastError"
                  class="text-ink-subtle"
                >
                  Last error
                </dt>
                <dd
                  v-if="statusOf(listing).lastError"
                  class="font-mono text-signal-danger"
                >
                  {{ statusOf(listing).lastError }}
                </dd>
              </dl>
              <pre
                v-if="statusOf(listing).stderrTail"
                class="mt-2 max-h-40 overflow-auto rounded-sm bg-surface-2 px-2 py-1 font-mono text-[10px] text-ink"
              >{{ statusOf(listing).stderrTail }}</pre>
            </div>
          </div>
          <label
            class="inline-flex items-center cursor-pointer select-none"
            :class="busyById[listing.recipe.id] ? 'opacity-60 cursor-wait' : ''"
          >
            <input
              type="checkbox"
              class="accent-accent w-4 h-4"
              :checked="listing.enabled"
              :disabled="busyById[listing.recipe.id]"
              :aria-label="`Enable ${listing.recipe.displayName || listing.recipe.id}`"
              :data-testid="`recipe-toggle-${listing.recipe.id}`"
              @change="onToggle(listing, $event)"
            />
          </label>
        </div>
      </div>
    </div>

    <RecipeKeyPromptModal
      v-if="modalRecipe"
      :open="modalOpen"
      :recipe="modalRecipe"
      :install="installFromModal"
      :initial-config="modalInitialConfig"
      @installed="onModalInstalled"
      @close="onModalClose"
    />

    <AddMCPServerModal
      :open="addModalOpen"
      :edit-recipe="addModalEditRecipe"
      :existing-ids="existingRecipeIds"
      @installed="onAddModalInstalled"
      @close="onAddModalClose"
    />
  </section>
</template>
