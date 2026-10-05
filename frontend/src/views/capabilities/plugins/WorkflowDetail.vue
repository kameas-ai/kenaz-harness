<script setup lang="ts">
/**
 * WorkflowDetail — the workflow detail plugin of the "Add capability"
 * surface (install-framework-01DOGF0B WP05). It replaces the retired
 * Workflows › Catalog browse (CatalogView.vue) and its preview drawer
 * (CatalogPreviewDrawer.vue): for a shipped template it renders the same
 * preview — required permissions, required (missing) MCP servers, the
 * estimated cost per run and the YAML — and installs through
 * Workflows_CatalogInstall, which routes through the install framework
 * (Store.Save + cron, consumer check, capability:installed). A fleet
 * workflow installs through Capability_Install.
 *
 * Removal is the surface's generic Remove (Capability_Uninstall).
 */
import { computed, inject, ref, watch } from 'vue';
import { useRouter } from 'vue-router';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import {
  WorkflowsClientKey,
  createWorkflowsClient,
  type WorkflowsCatalogInstallResult,
  type WorkflowsCatalogPreview,
} from '@/lib/workflowsClient';
import type { CapabilityItem } from '@/lib/types';
import { SOURCE_LABELS } from './labels';

const props = defineProps<{ item: CapabilityItem }>();
const emit = defineEmits<{ (e: 'changed'): void }>();

const client = useHarnessClient();
const workflows = inject(WorkflowsClientKey, null) ?? createWorkflowsClient();
const router = (() => {
  try {
    return useRouter();
  } catch {
    return null;
  }
})();

const isTemplate = computed(() => props.item.source === 'builtin');

const preview = ref<WorkflowsCatalogPreview | null>(null);
const previewError = ref<string | null>(null);
const loading = ref(false);

watch(
  () => props.item.id,
  async () => {
    preview.value = null;
    previewError.value = null;
    if (!isTemplate.value) return;
    loading.value = true;
    try {
      preview.value = await workflows.catalog.get(props.item.id);
    } catch (e) {
      previewError.value = e instanceof Error ? e.message : String(e);
    } finally {
      loading.value = false;
    }
  },
  { immediate: true },
);

const installing = ref(false);
const installError = ref<string | null>(null);
const result = ref<WorkflowsCatalogInstallResult | null>(null);

async function install() {
  installing.value = true;
  installError.value = null;
  try {
    if (isTemplate.value) {
      result.value = await workflows.catalog.install(props.item.id);
    } else {
      await client.capabilities.install('workflow', props.item.id, props.item.version ?? '');
    }
    emit('changed');
  } catch (e) {
    installError.value = e instanceof Error ? e.message : String(e);
  } finally {
    installing.value = false;
  }
}

function formatCost(usd: number): string {
  if (usd === 0) return 'Free (no model turns)';
  if (usd < 0.001) return '< $0.001';
  return `~$${usd.toFixed(4)}`;
}

function openInWorkflows() {
  void router?.push('/workflows');
}
</script>

<template>
  <section class="space-y-4" :data-testid="`workflow-detail-${item.id}`">
    <header>
      <h3 class="font-ui text-[13px] font-semibold text-ink">{{ item.name }}</h3>
      <p class="font-ui text-[11px] text-ink-muted">
        Workflow · {{ SOURCE_LABELS[item.source] ?? item.source }}<span v-if="item.version"> · {{ item.version }}</span>
        <span v-if="item.state.installed" class="text-signal-ok"> · installed</span>
      </p>
      <p v-if="item.description" class="mt-2 whitespace-pre-line font-ui text-[12px] text-ink-muted">
        {{ item.description }}
      </p>
    </header>

    <p
      v-if="isTemplate && item.state.update_available"
      class="font-ui text-[11px] text-accent"
      data-testid="workflow-detail-update-note"
    >
      A newer version of this template ships with the app. Update (in the list) replaces your
      installed copy with it — edits you made to the copy are overwritten; its schedule is kept.
    </p>

    <p
      v-if="!isTemplate"
      class="font-ui text-[11px] text-ink-muted"
      data-testid="workflow-detail-unverified"
    >
      From your org's catalog. Fleet catalog installs are not signature-verified yet — the
      workflow is installed as published, without a cryptographic check.
    </p>

    <div v-if="loading" class="font-ui text-[12px] text-ink-muted">Loading preview…</div>
    <div
      v-else-if="previewError"
      class="rounded-sm border border-signal-danger px-3 py-2 font-ui text-[12px] text-signal-danger"
      role="alert"
      data-testid="workflow-detail-preview-error"
    >
      {{ previewError }}
    </div>
    <template v-else-if="preview">
      <section v-if="(preview.entry.requiresCedarGrants ?? []).length > 0">
        <h4 class="mb-1 font-ui text-[11px] uppercase tracking-wide text-ink-muted">Required permissions</h4>
        <div class="flex flex-wrap gap-1.5" data-testid="workflow-detail-grants">
          <span
            v-for="g in preview.entry.requiresCedarGrants"
            :key="g"
            class="rounded-full bg-signal-warn-soft px-2 py-0.5 font-ui text-[11px] text-signal-warn"
          >{{ g }}</span>
        </div>
      </section>
      <section v-if="(preview.entry.requiresCredentials ?? []).length > 0">
        <h4 class="mb-1 font-ui text-[11px] uppercase tracking-wide text-ink-muted">Needs MCP servers</h4>
        <div class="flex flex-wrap gap-1.5" data-testid="workflow-detail-creds">
          <span
            v-for="c in preview.entry.requiresCredentials"
            :key="c"
            class="rounded-full bg-signal-danger-soft px-2 py-0.5 font-ui text-[11px] text-signal-danger"
          >{{ c }} — not installed</span>
        </div>
        <p class="mt-1 font-ui text-[11px] text-ink-muted">
          It installs anyway; those steps fail until the servers are installed from this list.
        </p>
      </section>
      <section>
        <h4 class="mb-1 font-ui text-[11px] uppercase tracking-wide text-ink-muted">Estimated cost / run</h4>
        <p class="font-ui text-[12px] text-ink" data-testid="workflow-detail-cost">
          {{ formatCost(preview.entry.estimatedCostUSD) }}
        </p>
      </section>
      <section>
        <h4 class="mb-1 font-ui text-[11px] uppercase tracking-wide text-ink-muted">Workflow YAML</h4>
        <pre
          class="max-h-64 overflow-auto rounded-sm border border-border-muted bg-surface-2 p-3 font-mono text-[11px] text-ink-muted whitespace-pre"
          data-testid="workflow-detail-yaml"
        >{{ preview.yamlSource }}</pre>
      </section>
    </template>

    <div v-if="installError" class="font-ui text-[12px] text-signal-danger" role="alert" data-testid="workflow-detail-install-error">
      {{ installError }}
    </div>
    <p v-if="result" class="font-ui text-[12px] text-ink" data-testid="workflow-detail-result">
      Installed{{ result.scheduled ? ' and scheduled' : '' }}.
      <span v-if="(result.missingCredentials ?? []).length > 0">
        Install {{ result.missingCredentials!.join(', ') }} to run every step.
      </span>
    </p>

    <div class="flex items-center gap-2">
      <button
        v-if="!item.state.installed"
        type="button"
        class="rounded-sm border border-accent-hairline bg-surface-1 px-3 py-1 font-ui text-[12px] text-accent hover:bg-accent-glow disabled:opacity-50"
        :disabled="installing || loading || !!previewError"
        :data-testid="`workflow-detail-install-${item.id}`"
        @click="install"
      >
        {{ installing ? 'Installing…' : 'Install' }}
      </button>
      <button
        v-else
        type="button"
        class="rounded-sm border border-border-muted px-3 py-1 font-ui text-[12px] text-ink hover:bg-surface-2"
        data-testid="workflow-detail-open"
        @click="openInWorkflows"
      >
        Open in Workflows →
      </button>
    </div>
  </section>
</template>
