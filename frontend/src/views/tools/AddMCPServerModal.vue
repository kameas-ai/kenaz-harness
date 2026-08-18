<script setup lang="ts">
/**
 * AddMCPServerModal — three-tab modal for adding MCP servers.
 *
 * Tabs:
 *   - Registry: curated list from the recipe catalog (non-enabled entries).
 *   - Paste config: translate Claude Desktop / Cursor mcpServers JSON.
 *   - Custom recipe: form-based recipe author for stdio/http/sse.
 *
 * Registry tab is hidden when Settings.MCPRegistryEnabled === false.
 * BACKEND GAP: `client.settings.getMCPRegistryEnabled()` does not exist.
 * The registry tab is shown by default; once the RPC lands, wire it here.
 *
 * The modal uses the same no-radix-vue, fixed-overlay shape as
 * RecipeKeyPromptModal.vue (the existing modal primitive in this project).
 */
import { computed, ref } from 'vue';
import RegistryTab from './RegistryTab.vue';
import PasteConfigTab from './PasteConfigTab.vue';
import CustomRecipeTab from './CustomRecipeTab.vue';
import { useCustomRecipeAuthoringEnabled } from '@/lib/customRecipeAuthoring';
import type { Recipe } from '@/lib/types';

type TabId = 'registry' | 'paste' | 'custom';

const props = withDefaults(
  defineProps<{
    open: boolean;
    /** When provided, pre-fills the Custom tab for edit mode. */
    editRecipe?: Recipe | null;
    /** Existing recipe ids for the Custom tab shadow-warning check. */
    existingIds?: readonly string[];
  }>(),
  {
    editRecipe: null,
    existingIds: () => [],
  },
);

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'installed'): void;
}>();

// WP02 stop-gap (mcp-connector-lifecycle-01PMMC01, FR-001): the Custom tab's
// save() has no backend yet (MCP_SaveCustomRecipe does not exist). Gated
// behind the same flag KenazToolsPanel reads for its per-row Edit button —
// see frontend/src/lib/customRecipeAuthoring.ts for the retirement condition
// (WP06). C-001: one flag, two doors — do not fork this into a second v-if.
const customRecipeAuthoringEnabled = useCustomRecipeAuthoringEnabled();

// Start on the Custom tab if we're editing a recipe — but only when the
// Custom tab is actually reachable; otherwise editing a recipe would land on
// a tab bar with no active tab selected.
const activeTab = ref<TabId>(
  props.editRecipe && customRecipeAuthoringEnabled ? 'custom' : 'registry',
);

const tabs = computed<{ id: TabId; label: string }[]>(() => {
  const base: { id: TabId; label: string }[] = [
    { id: 'registry', label: 'Registry' },
    { id: 'paste', label: 'Paste config' },
  ];
  if (customRecipeAuthoringEnabled) {
    base.push({ id: 'custom', label: 'Custom recipe' });
  }
  return base;
});

function close() {
  emit('close');
}

function onInstalled() {
  emit('installed');
  emit('close');
}

function onImported() {
  emit('installed');
  emit('close');
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault();
    close();
  }
}
</script>

<template>
  <div
    v-if="open"
    class="fixed inset-0 z-50 flex items-center justify-center"
    role="dialog"
    aria-modal="true"
    aria-label="Add MCP Server"
    data-testid="add-mcp-modal"
    @keydown="onKeydown"
  >
    <div class="absolute inset-0 bg-modal-overlay" @click="close" />
    <div
      class="relative z-10 w-[620px] max-w-[92vw] max-h-[85vh] overflow-hidden flex flex-col rounded-md border border-border-muted bg-surface-0 shadow-lg"
    >
      <!-- Header -->
      <header
        class="flex items-center justify-between border-b border-border-muted px-5 py-3"
      >
        <div>
          <div class="text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
            MCP Servers
          </div>
          <h2 class="mt-1 font-ui text-base font-semibold text-ink">
            Add MCP Server
          </h2>
        </div>
        <button
          type="button"
          class="text-[11px] text-ink-dim hover:text-ink"
          data-testid="add-mcp-modal-close"
          @click="close"
        >
          Close
        </button>
      </header>

      <!-- Tab bar -->
      <nav
        class="flex gap-0 border-b border-border-muted bg-surface-1"
        role="tablist"
        data-testid="add-mcp-tabs"
      >
        <button
          v-for="tab in tabs"
          :key="tab.id"
          type="button"
          role="tab"
          :aria-selected="activeTab === tab.id"
          :data-testid="`add-mcp-tab-${tab.id}`"
          :class="[
            'px-4 py-2.5 font-ui text-[11px] uppercase tracking-[0.16em] border-b-2 -mb-px transition-colors',
            activeTab === tab.id
              ? 'border-accent text-accent'
              : 'border-transparent text-ink-dim hover:text-ink',
          ]"
          @click="activeTab = tab.id"
        >
          {{ tab.label }}
        </button>
      </nav>

      <!-- Tab content -->
      <div class="flex-1 overflow-y-auto px-5 py-4">
        <RegistryTab
          v-if="activeTab === 'registry'"
          @installed="onInstalled"
          @switch-tab="activeTab = $event"
        />
        <PasteConfigTab
          v-else-if="activeTab === 'paste'"
          @imported="onImported"
        />
        <CustomRecipeTab
          v-else-if="activeTab === 'custom' && customRecipeAuthoringEnabled"
          :initial-recipe="editRecipe"
          :existing-ids="existingIds"
          @saved="onInstalled"
          @cancel="close"
        />
      </div>
    </div>
  </div>
</template>
