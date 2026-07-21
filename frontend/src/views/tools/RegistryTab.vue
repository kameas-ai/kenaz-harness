<script setup lang="ts">
/**
 * RegistryTab — lists curated registry entries (source === 'registry')
 * from the recipe catalog. Each row has an "Install" button that calls
 * tools.install via the parent's install prop.
 *
 * BACKEND GAP: The shipped catalog does not currently carry a `source`
 * discriminator field on the wire shape. This tab renders ALL recipes
 * that are NOT currently enabled as candidate registry entries — once the
 * backend surfaces a `source` field (WP10+), this filter should be
 * tightened to `source === 'registry'`.
 */
import { ref, computed, onMounted } from 'vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import { Wrench, Search, Folder, Brain, Globe, CheckSquare, Code, Scale, MessageSquare } from '@/shell/icons';
import type { RecipeListing, RecipeCategory, Recipe, RecipeStatus } from '@/lib/types';
import RecipeKeyPromptModal from './RecipeKeyPromptModal.vue';

const emit = defineEmits<{
  (e: 'installed'): void;
}>();

const client = useHarnessClient();

const listings = ref<RecipeListing[]>([]);
const loading = ref(false);
const error = ref<string | null>(null);
const busyById = ref<Record<string, boolean>>({});
const rowError = ref<Record<string, string | null>>({});

// Recipe currently being configured in the install modal. When set, the
// RecipeKeyPromptModal collects the recipe's env keys / config options /
// warning ack and runs the install with the user-provided values — instead
// of the old behaviour of calling install with empty maps and surfacing the
// "required env key missing" error inline.
const configuringRecipe = ref<Recipe | null>(null);

// A recipe needs the install modal when it has credentials to collect, config
// options to set, or a warning the user must acknowledge. Zero-config recipes
// (e.g. fetch) install in one click without an intermediate dialog.
function needsConfigure(recipe: Recipe): boolean {
  return (
    recipe.envKeys.length > 0 ||
    (recipe.configOptions?.length ?? 0) > 0 ||
    Boolean(recipe.warning)
  );
}

function installRecipe(
  id: string,
  env: Record<string, string>,
  config: Record<string, unknown>,
): Promise<RecipeStatus> {
  return client.tools.recipes.install(id, env, config);
}

function onModalInstalled() {
  configuringRecipe.value = null;
  emit('installed');
  void load();
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
    default:
      return Wrench;
  }
}

async function load() {
  loading.value = true;
  error.value = null;
  try {
    const all = await client.tools.recipes.list();
    // Show non-enabled recipes as candidates for installation.
    // When backend surfaces source field, filter to source === 'registry'.
    listings.value = all.filter((l) => !l.enabled);
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  } finally {
    loading.value = false;
  }
}

async function install(listing: RecipeListing) {
  const id = listing.recipe.id;
  if (busyById.value[id]) return;
  rowError.value = { ...rowError.value, [id]: null };

  // Recipes that need credentials, config, or a warning ack are configured
  // through the modal so the user supplies what's required up front — never a
  // raw "missing key" error after the fact.
  if (needsConfigure(listing.recipe)) {
    configuringRecipe.value = listing.recipe;
    return;
  }

  busyById.value = { ...busyById.value, [id]: true };
  try {
    await client.tools.recipes.install(id, {}, {});
    emit('installed');
  } catch (e) {
    rowError.value = {
      ...rowError.value,
      [id]: e instanceof Error ? e.message : String(e),
    };
  } finally {
    busyById.value = { ...busyById.value, [id]: false };
  }
}

onMounted(() => {
  void load();
});

const visibleListings = computed(() => listings.value);
</script>

<template>
  <div class="space-y-3" data-testid="registry-tab">
    <div
      v-if="loading"
      class="py-4 text-center font-ui text-[12px] text-ink-muted"
      data-testid="registry-loading"
    >
      Loading registry…
    </div>
    <div
      v-else-if="error"
      class="rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
      role="alert"
      data-testid="registry-error"
    >
      {{ error }}
    </div>
    <div
      v-else-if="visibleListings.length === 0"
      class="py-4 text-center font-ui text-[12px] text-ink-muted"
      data-testid="registry-empty"
    >
      No registry entries available. All shipped recipes are already installed.
    </div>
    <div
      v-else
      class="rounded-sm border border-border-muted bg-surface-1 divide-y divide-border-muted"
      data-testid="registry-list"
    >
      <div
        v-for="listing in visibleListings"
        :key="listing.recipe.id"
        class="px-4 py-3 grid gap-3 items-start"
        style="grid-template-columns: 1.25rem 1fr auto"
        :data-testid="`registry-row-${listing.recipe.id}`"
      >
        <component
          :is="categoryIcon(listing.recipe.category)"
          class="mt-0.5 h-4 w-4 text-ink-dim"
          aria-hidden="true"
        />
        <div>
          <div class="font-ui text-[13px] text-ink">
            {{ listing.recipe.displayName }}
          </div>
          <p class="mt-1 text-[11px] text-ink-muted max-w-prose">
            {{ listing.recipe.description }}
          </p>
          <div
            v-if="rowError[listing.recipe.id]"
            class="mt-1 text-[11px] text-signal-danger"
            role="alert"
            :data-testid="`registry-row-error-${listing.recipe.id}`"
          >
            {{ rowError[listing.recipe.id] }}
          </div>
        </div>
        <button
          type="button"
          class="rounded-sm border border-accent-hairline bg-surface-1 px-3 py-1 font-ui text-[12px] text-accent hover:bg-accent-glow disabled:opacity-50 disabled:cursor-not-allowed"
          :disabled="busyById[listing.recipe.id]"
          :data-testid="`registry-install-${listing.recipe.id}`"
          @click="install(listing)"
        >
          {{ busyById[listing.recipe.id] ? 'Installing…' : 'Install' }}
        </button>
      </div>
    </div>

    <!-- Install modal: collects env keys / config / warning ack for recipes
         that need them, instead of erroring out after a blank install. -->
    <RecipeKeyPromptModal
      v-if="configuringRecipe"
      :open="configuringRecipe !== null"
      :recipe="configuringRecipe"
      :install="installRecipe"
      @close="configuringRecipe = null"
      @installed="onModalInstalled"
    />
  </div>
</template>
