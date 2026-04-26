<script setup lang="ts">
/**
 * KaneazToolsPanel — built-in toggleable tools shipped with the
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
import { computed, onMounted, onBeforeUnmount, ref } from 'vue';
import { useRouter } from 'vue-router';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { useToolsRecipes } from '@/lib/useHarnessAPI';
import {
  Brain,
  ChevronDown,
  ChevronRight,
  Folder,
  Globe,
  Search,
  Wrench,
} from '@/shell/icons';
import RecipeKeyPromptModal from './RecipeKeyPromptModal.vue';
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

onMounted(() => {
  void refreshMemory();
});

// ── Recipes (WP06) ─────────────────────────────────────────────────────
const tools = useToolsRecipes();
const recipes = tools.recipes;
const startedAt = tools.startedAt;
const recipesLoading = tools.loading;
const recipesError = tools.error;

const busyById = ref<Record<string, boolean>>({});
const rowError = ref<Record<string, string | null>>({});
const expanded = ref<Record<string, boolean>>({});
const modalOpen = ref(false);
const modalRecipe = ref<Recipe | null>(null);

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
    default:
      return Wrench;
  }
}

function stateColor(state: RecipeState): string {
  switch (state) {
    case 'running':
      return 'text-signal-ok';
    case 'starting':
    case 'restarting':
      return 'text-signal-warn';
    case 'failed':
      return 'text-signal-danger';
    case 'stopped':
    default:
      return 'text-ink-dim';
  }
}

function setRowError(id: string, msg: string | null) {
  rowError.value = { ...rowError.value, [id]: msg };
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
    if (listing.keysPresent) {
      await doInstall(listing.recipe, {});
    } else {
      // Revert the optimistic toggle until the modal commits.
      target.checked = false;
      modalRecipe.value = listing.recipe;
      modalOpen.value = true;
    }
  } else {
    await doUninstall(id);
  }
}

async function doInstall(recipe: Recipe, env: Record<string, string>) {
  const id = recipe.id;
  busyById.value = { ...busyById.value, [id]: true };
  setRowError(id, null);
  try {
    await tools.install(id, env);
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
}

function onModalClose() {
  modalOpen.value = false;
  modalRecipe.value = null;
}

const installFromModal = async (id: string, env: Record<string, string>) => {
  const recipe = modalRecipe.value;
  if (!recipe || recipe.id !== id) {
    throw new Error('Modal recipe mismatch');
  }
  return tools.install(id, env);
};

const visibleRecipes = computed<readonly RecipeListing[]>(() => recipes.value);

function statusOf(listing: RecipeListing): RecipeStatus {
  return listing.status;
}
</script>

<template>
  <section class="px-6 py-4">
    <h2
      class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle mb-3"
    >
      Kaneaz tools
    </h2>

    <div
      class="rounded-sm border border-border-muted bg-surface-1 divide-y divide-border-muted"
    >
      <!-- Memory tool row -->
      <div
        class="px-4 py-3 grid gap-3 items-start"
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
        <label
          class="inline-flex items-center cursor-pointer select-none"
          :class="memoryBusy ? 'opacity-60 cursor-wait' : ''"
        >
          <input
            type="checkbox"
            class="accent-accent w-4 h-4"
            :checked="memoryEnabled"
            :disabled="memoryBusy"
            data-testid="memory-toggle"
            @change="toggleMemory"
          />
        </label>
      </div>
    </div>

    <h2
      class="mt-6 font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle mb-3"
    >
      Connected MCP recipes
    </h2>

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
      v-else-if="visibleRecipes.length === 0"
      class="rounded-sm border border-border-muted bg-surface-1 px-4 py-3 font-ui text-[12px] text-ink-muted"
      data-testid="recipes-empty"
    >
      No shipped recipes available. Drop a JSON entry into
      <span class="font-mono">core/mcp/recipes/shipped.json</span> to add one.
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
              <span
                class="text-[10px] uppercase tracking-[0.16em]"
                :class="stateColor(statusOf(listing).state)"
                :data-testid="`recipe-state-${listing.recipe.id}`"
              >
                {{ statusOf(listing).state }}
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
      @installed="onModalInstalled"
      @close="onModalClose"
    />
  </section>
</template>
