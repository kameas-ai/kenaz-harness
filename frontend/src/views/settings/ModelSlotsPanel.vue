<script setup lang="ts">
/**
 * ModelSlotsPanel — served Settings: the effective harness model slots
 * (default sub-agent model, advisor, compaction, branch advisor, risk
 * rater) and which layer set each one. Read-only by design: a workbench
 * cannot write settings (HR-4); Kenaz sets host defaults and Fleet sets
 * org-wide values (org_config.harness_models). fleet-workbench-next-plan B11.
 */
import { ref, onMounted } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type { ModelSlot, ModelSlotsView } from '@/lib/harnessClient';

const client = useHarnessClient();
const view = ref<ModelSlotsView | null>(null);
const errorMsg = ref('');

const SLOT_LABELS: Record<ModelSlot['slot'], string> = {
  subagent_default: 'Default sub-agent',
  advisor: 'Advisor',
  compaction: 'Compaction',
  branch_advisor: 'Branch advisor',
  risk_rater: 'Risk rater',
};

const SOURCE_LABELS: Record<ModelSlot['source'], string> = {
  org: 'Organization (Fleet)',
  settings: 'Your setting',
  host: 'Kenaz host',
  derived: 'Derived from session provider',
  profile: 'Agent profile',
  none: 'Not set',
};

onMounted(async () => {
  if (!client.settings.modelSlots) return;
  try {
    view.value = await client.settings.modelSlots();
  } catch (err) {
    errorMsg.value = String(err);
  }
});
</script>

<template>
  <section v-if="view || errorMsg" class="space-y-3 p-4" data-testid="model-slots-panel">
    <div>
      <h2 class="text-sm font-semibold text-ink mb-1">Models</h2>
      <p class="text-xs text-ink-muted">
        The models this workbench uses for sub-agents and background roles.
        Read-only here: Kenaz sets host defaults and your organization can set
        them in Fleet; anything unset is derived from the session's provider.
      </p>
    </div>
    <p v-if="errorMsg" class="text-xs text-danger" data-testid="model-slots-error">{{ errorMsg }}</p>
    <table v-if="view" class="text-xs w-full">
      <tbody>
        <tr v-for="s in view.slots" :key="s.slot" :data-testid="`model-slot-${s.slot}`">
          <td class="py-1 pr-4 text-ink">{{ SLOT_LABELS[s.slot] ?? s.slot }}</td>
          <td class="py-1 pr-4 font-mono text-ink">{{ s.modelId || '—' }}</td>
          <td class="py-1 text-ink-muted" data-testid="model-slot-source">{{ SOURCE_LABELS[s.source] ?? s.source }}</td>
        </tr>
      </tbody>
    </table>
    <ul v-if="view?.orgErrors?.length" class="text-xs text-danger list-disc pl-4" data-testid="model-slots-org-errors">
      <li v-for="e in view.orgErrors" :key="e">{{ e }}</li>
    </ul>
  </section>
</template>
