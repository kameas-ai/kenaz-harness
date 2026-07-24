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
import { ref, computed, onMounted, watch } from 'vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import { Search, Zap, Plus } from '@/shell/icons';
import { categoryIconFor, categoryLabel } from '@/lib/recipeCategories';
import type { RecipeListing, Recipe, RecipeStatus } from '@/lib/types';
import RecipeKeyPromptModal from './RecipeKeyPromptModal.vue';

/** Category the long-tail callout points users at (FR-006). */
const AUTOMATION_CATEGORY = 'automation';

/** Prefilled GitHub new-issue URL for the "Request a connector" link. */
const REQUEST_CONNECTOR_URL =
  'https://github.com/kameas-ai/kenaz-harness/issues/new?title=Connector%20request%3A%20';

type IconComponent = ReturnType<typeof categoryIconFor>;

/** One rendered line in the catalog: a category header or a recipe row. */
type CatalogRow =
  | { kind: 'header'; category: string; label: string; icon: IconComponent; count: number }
  | { kind: 'row'; listing: RecipeListing };

const emit = defineEmits<{
  (e: 'installed'): void;
  /** Ask the parent modal to switch tabs (FR-006 custom-recipe path). */
  (e: 'switch-tab', tab: 'custom'): void;
}>();

const client = useHarnessClient();

const listings = ref<RecipeListing[]>([]);
const loading = ref(false);
const error = ref<string | null>(null);
const busyById = ref<Record<string, boolean>>({});
const rowError = ref<Record<string, string | null>>({});

// Free-text catalog filter (FR-005). Empty → categorized default view;
// non-empty → flat, filtered list.
const search = ref('');
const searchActive = computed(() => search.value.trim().length > 0);

// Optional single-category filter driven by the long-tail callout (FR-006).
// Mutually exclusive with text search: typing clears it.
const categoryFilter = ref<string | null>(null);
watch(search, (q) => {
  if (q.trim()) categoryFilter.value = null;
});

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

// True when the recipe matches the query in any of displayName /
// description / id / aliases (case-insensitive).
function matchesQuery(recipe: Recipe, q: string): boolean {
  const haystack = [
    recipe.displayName,
    recipe.description,
    recipe.id,
    ...(recipe.aliases ?? []),
  ];
  return haystack.some((field) => field?.toLowerCase().includes(q));
}

// Search results: flat, alphabetical by display name. Empty when the
// search box is empty (the default grouped view renders instead).
const filteredListings = computed<RecipeListing[]>(() => {
  const q = search.value.trim().toLowerCase();
  if (!q) return [];
  return listings.value
    .filter((l) => matchesQuery(l.recipe, q))
    .sort((a, b) => a.recipe.displayName.localeCompare(b.recipe.displayName));
});

// Default view: recipes grouped by category. Sections are sorted
// alphabetically by display label; recipes are alphabetical within.
interface CategorySection {
  category: string;
  label: string;
  icon: IconComponent;
  listings: RecipeListing[];
}

const groupedSections = computed<CategorySection[]>(() => {
  const byCategory = new Map<string, RecipeListing[]>();
  for (const l of listings.value) {
    const cat = String(l.recipe.category ?? '');
    const bucket = byCategory.get(cat) ?? [];
    bucket.push(l);
    byCategory.set(cat, bucket);
  }
  return [...byCategory.entries()]
    .map(([category, ls]) => ({
      category,
      label: categoryLabel(category),
      icon: categoryIconFor(category),
      listings: [...ls].sort((a, b) =>
        a.recipe.displayName.localeCompare(b.recipe.displayName),
      ),
    }))
    .sort((a, b) => a.label.localeCompare(b.label));
});

// Flattened render list: a header row per category followed by its recipe
// rows in the default view, or a flat filtered list under active search.
const catalogRows = computed<CatalogRow[]>(() => {
  if (searchActive.value) {
    return filteredListings.value.map<CatalogRow>((listing) => ({
      kind: 'row',
      listing,
    }));
  }
  const sections = categoryFilter.value
    ? groupedSections.value.filter((s) => s.category === categoryFilter.value)
    : groupedSections.value;
  const rows: CatalogRow[] = [];
  for (const section of sections) {
    rows.push({
      kind: 'header',
      category: section.category,
      label: section.label,
      icon: section.icon,
      count: section.listings.length,
    });
    for (const listing of section.listings) {
      rows.push({ kind: 'row', listing });
    }
  }
  return rows;
});

// True when the registry has entries but the active search matches none.
const noResults = computed(
  () => searchActive.value && filteredListings.value.length === 0,
);

// The active category label shown in the filter chip, if any.
const activeCategoryLabel = computed(() =>
  categoryFilter.value ? categoryLabel(categoryFilter.value) : '',
);

// FR-006 — long-tail affordances.
function browseAutomation() {
  search.value = '';
  categoryFilter.value = AUTOMATION_CATEGORY;
}

function clearCategoryFilter() {
  categoryFilter.value = null;
}

function addCustomServer() {
  emit('switch-tab', 'custom');
}

function requestConnector() {
  client.openExternalURL(REQUEST_CONNECTOR_URL);
}
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
      v-else-if="listings.length === 0"
      class="py-4 text-center font-ui text-[12px] text-ink-muted"
      data-testid="registry-empty"
    >
      No registry entries available. All shipped recipes are already installed.
    </div>
    <template v-else>
      <!-- Search box (FR-005). Empty search shows the categorized view. -->
      <div class="relative">
        <Search
          class="pointer-events-none absolute left-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-ink-dim"
          aria-hidden="true"
        />
        <input
          v-model="search"
          type="search"
          aria-label="Search connectors"
          placeholder="Search connectors…"
          class="w-full rounded-sm border border-border-muted bg-surface-1 pl-8 pr-3 py-2 font-ui text-[12px] text-ink placeholder:text-ink-subtle focus:border-accent focus:outline-none"
          data-testid="registry-search"
        />
      </div>

      <!-- Active category filter chip (set by the long-tail callout). -->
      <div
        v-if="categoryFilter"
        class="flex items-center gap-2 font-ui text-[11px] text-ink-muted"
        data-testid="registry-active-filter"
      >
        <span>Showing</span>
        <span class="rounded-sm bg-surface-2 px-2 py-0.5 text-ink">{{
          activeCategoryLabel
        }}</span>
        <button
          type="button"
          class="text-accent hover:text-accent-muted"
          data-testid="registry-clear-filter"
          @click="clearCategoryFilter"
        >
          Clear
        </button>
      </div>

      <!-- Zero-result state under active search. -->
      <div
        v-if="noResults"
        class="py-4 text-center font-ui text-[12px] text-ink-muted"
        data-testid="registry-no-results"
      >
        No connectors match “{{ search.trim() }}”.
      </div>

      <!-- Grouped default view / flat search results share one list. -->
      <div
        v-else
        class="rounded-sm border border-border-muted bg-surface-1 divide-y divide-border-muted"
        data-testid="registry-list"
      >
        <template v-for="row in catalogRows">
          <div
            v-if="row.kind === 'header'"
            :key="`header-${row.category}`"
            class="flex items-center gap-2 px-4 py-2 bg-surface-2"
            :data-testid="`registry-section-${row.category || 'other'}`"
          >
            <component
              :is="row.icon"
              class="h-3.5 w-3.5 text-ink-dim"
              aria-hidden="true"
            />
            <span
              class="font-ui text-[11px] uppercase tracking-[0.14em] text-ink-muted"
            >
              {{ row.label }}
            </span>
            <span class="font-ui text-[11px] text-ink-subtle">{{ row.count }}</span>
          </div>
          <div
            v-else
            :key="`row-${row.listing.recipe.id}`"
            class="px-4 py-3 grid gap-3 items-start"
            style="grid-template-columns: 1.25rem 1fr auto"
            :data-testid="`registry-row-${row.listing.recipe.id}`"
          >
            <component
              :is="categoryIconFor(row.listing.recipe.category)"
              class="mt-0.5 h-4 w-4 text-ink-dim"
              aria-hidden="true"
            />
            <div>
              <div class="font-ui text-[13px] text-ink">
                {{ row.listing.recipe.displayName }}
              </div>
              <p class="mt-1 text-[11px] text-ink-muted max-w-prose">
                {{ row.listing.recipe.description }}
              </p>
              <div
                v-if="rowError[row.listing.recipe.id]"
                class="mt-1 text-[11px] text-signal-danger"
                role="alert"
                :data-testid="`registry-row-error-${row.listing.recipe.id}`"
              >
                {{ rowError[row.listing.recipe.id] }}
              </div>
            </div>
            <button
              type="button"
              class="rounded-sm border border-accent-hairline bg-surface-1 px-3 py-1 font-ui text-[12px] text-accent hover:bg-accent-glow disabled:opacity-50 disabled:cursor-not-allowed"
              :disabled="busyById[row.listing.recipe.id]"
              :data-testid="`registry-install-${row.listing.recipe.id}`"
              @click="install(row.listing)"
            >
              {{ busyById[row.listing.recipe.id] ? 'Installing…' : 'Install' }}
            </button>
          </div>
        </template>
      </div>

      <!-- FR-006 — long-tail affordance. Always at the bottom of results;
           rendered prominently when a search returns nothing. -->
      <div
        :class="[
          'rounded-sm border px-4 py-3',
          noResults
            ? 'border-accent-hairline bg-accent-glow'
            : 'border-border-muted bg-surface-1',
        ]"
        data-testid="registry-long-tail"
      >
        <div class="font-ui text-[12px] font-semibold text-ink">
          Don't see your tool?
        </div>
        <p class="mt-1 text-[11px] text-ink-muted max-w-prose">
          Reach thousands more apps through Automation &amp; iPaaS connectors
          (Zapier, Make, n8n, Pipedream, Composio, Workato), or add any MCP
          server yourself.
        </p>
        <div class="mt-2 flex flex-wrap items-center gap-2">
          <button
            type="button"
            class="inline-flex items-center gap-1 rounded-sm border border-accent-hairline bg-surface-1 px-2.5 py-1 font-ui text-[11px] text-accent hover:bg-accent-glow"
            data-testid="registry-browse-automation"
            @click="browseAutomation"
          >
            <Zap class="h-3.5 w-3.5" aria-hidden="true" />
            Browse automation connectors
          </button>
          <button
            type="button"
            class="inline-flex items-center gap-1 rounded-sm border border-accent-hairline bg-surface-1 px-2.5 py-1 font-ui text-[11px] text-accent hover:bg-accent-glow"
            data-testid="registry-add-custom"
            @click="addCustomServer"
          >
            <Plus class="h-3.5 w-3.5" aria-hidden="true" />
            Add a custom MCP server
          </button>
          <button
            type="button"
            class="font-ui text-[11px] text-ink-dim hover:text-ink underline"
            data-testid="registry-request-connector"
            @click="requestConnector"
          >
            Request a connector →
          </button>
        </div>
      </div>
    </template>

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
