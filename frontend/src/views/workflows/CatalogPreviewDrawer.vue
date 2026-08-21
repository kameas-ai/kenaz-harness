<script setup lang="ts">
/**
 * CatalogPreviewDrawer — side drawer for a selected catalog entry (WP03).
 *
 * Shows the full YAML (read-only), required Cedar grants (chips),
 * required credentials (chips with missing/configured status), estimated
 * cost per run, and an Install button.
 *
 * On install with missing credentials the component emits
 * `redirect-providers` so the parent can route to /providers/add.
 */
import { ref, computed, watch } from 'vue';
import type {
  WorkflowsClient,
  WorkflowsCatalogEntry,
  WorkflowsCatalogPreview,
} from '@/lib/workflowsClient';

const props = defineProps<{
  client: WorkflowsClient;
  /** The entry selected in CatalogView — null when drawer is hidden. */
  entry: WorkflowsCatalogEntry | null;
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'installed', workflowId: string): void;
  (e: 'redirect-providers'): void;
}>();

const preview = ref<WorkflowsCatalogPreview | null>(null);
const previewLoading = ref(false);
const previewError = ref<string | null>(null);

const installing = ref(false);
const installError = ref<string | null>(null);

const isOpen = computed(() => props.entry !== null);

// Reload the preview whenever the selected entry changes.
watch(
  () => props.entry?.id,
  async (id) => {
    if (!id) {
      preview.value = null;
      return;
    }
    previewLoading.value = true;
    previewError.value = null;
    installError.value = null;
    try {
      preview.value = await props.client.catalog.get(id);
    } catch (err) {
      previewError.value = err instanceof Error ? err.message : String(err);
      preview.value = null;
    } finally {
      previewLoading.value = false;
    }
  },
  { immediate: true },
);

const hasMissingCreds = computed(
  () => (preview.value?.entry.requiresCredentials ?? []).length > 0,
);

// automation-actually-runs-01PMZ404 UNIT-10: `requiresCredentials` is
// ALREADY the server-computed missing set — its Go doc comment
// (core/workflows/catalog/catalog.go Entry.RequiresCredentials) says
// "MCP server names ... NOT already in the recipe registry". The old
// credStatus() checked `missing.includes(cred)` against the very array
// `cred` was drawn from in the v-for below, which is true for every
// element by construction — the 'configured' branch was unreachable.
// There is no wire field carrying a "these ARE configured" list to
// render a genuine green chip against, so every item in this section is
// 'missing' by definition; the fix is to stop pretending otherwise
// rather than to keep a check that can never go the other way.

async function install() {
  if (!props.entry) return;
  installing.value = true;
  installError.value = null;
  try {
    const result = await props.client.catalog.install(props.entry.id);
    emit('installed', result.workflowId);
    if (result.missingCredentials && result.missingCredentials.length > 0) {
      emit('redirect-providers');
    }
  } catch (err) {
    installError.value = err instanceof Error ? err.message : String(err);
  } finally {
    installing.value = false;
  }
}

function formatCost(usd: number): string {
  if (usd === 0) return 'Free (no model turns)';
  if (usd < 0.001) return '< $0.001';
  return `~$${usd.toFixed(4)}`;
}
</script>

<template>
  <Transition
    enter-active-class="transition-transform duration-200 ease-out"
    enter-from-class="translate-x-full"
    enter-to-class="translate-x-0"
    leave-active-class="transition-transform duration-150 ease-in"
    leave-from-class="translate-x-0"
    leave-to-class="translate-x-full"
  >
    <aside
      v-if="isOpen"
      class="fixed inset-y-0 right-0 z-40 flex flex-col w-[480px] max-w-full border-l border-border-muted bg-surface-1 shadow-xl"
      data-testid="catalog-preview-drawer"
    >
      <!-- Header -->
      <header class="flex items-center justify-between border-b border-border-muted px-4 py-3">
        <div>
          <h2 class="font-ui text-base text-ink">
            {{ entry?.icon ? `${entry.icon} ` : '' }}{{ entry?.name }}
          </h2>
          <span class="font-ui text-xs text-ink-muted">{{ entry?.source }} · {{ entry?.version }}</span>
        </div>
        <button
          type="button"
          class="rounded-sm p-1 text-ink-muted hover:text-ink focus:outline-none focus:ring-1 focus:ring-accent"
          data-testid="catalog-drawer-close"
          @click="emit('close')"
        >
          <svg class="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
          </svg>
        </button>
      </header>

      <!-- Body -->
      <div class="flex-1 overflow-y-auto p-4 space-y-5">
        <div v-if="previewLoading" class="font-ui text-sm text-ink-muted">
          Loading preview…
        </div>

        <div
          v-else-if="previewError"
          class="rounded-sm border border-signal-danger bg-signal-danger-soft p-3 font-ui text-sm text-signal-danger"
          data-testid="catalog-drawer-error"
        >
          {{ previewError }}
        </div>

        <template v-else-if="preview">
          <!-- Description -->
          <p
            v-if="preview.entry.description"
            class="font-ui text-sm text-ink-muted whitespace-pre-line"
          >
            {{ preview.entry.description }}
          </p>

          <!-- Cedar grants -->
          <section v-if="(preview.entry.requiresCedarGrants ?? []).length > 0">
            <h3 class="font-ui text-xs uppercase tracking-wide text-ink-muted mb-1.5">
              Required permissions
            </h3>
            <div class="flex flex-wrap gap-1.5" data-testid="catalog-drawer-grants">
              <span
                v-for="grant in preview.entry.requiresCedarGrants"
                :key="grant"
                class="rounded-full bg-signal-warn-soft px-2 py-0.5 font-ui text-xs text-signal-warn"
                :data-testid="`catalog-grant-${grant}`"
              >
                {{ grant }}
              </span>
            </div>
          </section>

          <!-- Credentials -->
          <section v-if="(preview.entry.requiresCredentials ?? []).length > 0">
            <h3 class="font-ui text-xs uppercase tracking-wide text-ink-muted mb-1.5">
              Required credentials
            </h3>
            <div class="flex flex-wrap gap-1.5" data-testid="catalog-drawer-creds">
              <span
                v-for="cred in preview.entry.requiresCredentials"
                :key="cred"
                class="rounded-full bg-signal-danger-soft px-2 py-0.5 font-ui text-xs text-signal-danger"
                :data-testid="`catalog-cred-${cred}`"
              >
                {{ cred }} — missing
              </span>
            </div>
          </section>

          <!-- Estimated cost -->
          <section>
            <h3 class="font-ui text-xs uppercase tracking-wide text-ink-muted mb-1">
              Estimated cost / run
            </h3>
            <p class="font-ui text-sm text-ink" data-testid="catalog-drawer-cost">
              {{ formatCost(preview.entry.estimatedCostUSD) }}
            </p>
          </section>

          <!-- YAML preview -->
          <section>
            <h3 class="font-ui text-xs uppercase tracking-wide text-ink-muted mb-1.5">
              Workflow YAML
            </h3>
            <pre
              class="overflow-x-auto rounded-sm border border-border-muted bg-surface-2 p-3 font-mono text-xs text-ink-muted whitespace-pre"
              data-testid="catalog-drawer-yaml"
            >{{ preview.yamlSource }}</pre>
          </section>
        </template>
      </div>

      <!-- Footer / actions -->
      <footer class="border-t border-border-muted px-4 py-3 space-y-2">
        <div
          v-if="installError"
          class="font-ui text-sm text-signal-danger"
          data-testid="catalog-drawer-install-error"
        >
          {{ installError }}
        </div>

        <div
          v-if="hasMissingCreds && !installError"
          class="font-ui text-xs text-signal-warn"
          data-testid="catalog-drawer-missing-creds-warning"
        >
          Some credentials are missing. You will be redirected to add them after install.
        </div>

        <div class="flex items-center gap-2">
          <button
            type="button"
            class="flex-1 rounded-sm border border-accent bg-accent px-4 py-2 font-ui text-sm text-bg hover:opacity-90 disabled:opacity-50"
            :disabled="installing || previewLoading || !!previewError"
            data-testid="catalog-drawer-install"
            @click="install"
          >
            {{ installing ? 'Installing…' : 'Install' }}
          </button>
          <button
            type="button"
            class="rounded-sm border border-border-muted bg-surface-2 px-4 py-2 font-ui text-sm text-ink hover:bg-surface-1"
            data-testid="catalog-drawer-cancel"
            @click="emit('close')"
          >
            Cancel
          </button>
        </div>
      </footer>
    </aside>
  </Transition>
</template>
