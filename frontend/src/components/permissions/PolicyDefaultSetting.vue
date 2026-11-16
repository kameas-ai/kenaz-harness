<script setup lang="ts">
/**
 * PolicyDefaultSetting — launch spec L09: where the host policy default is
 * changed (Settings → Permissions). Starter: reads and in-workspace edits
 * run; shell commands, web requests and writes outside the workspace ask.
 * Previous default: every file read and write asks; web requests run.
 * A Cedar policy (the Policy page, or your organization's Fleet policy)
 * overrides either.
 *
 * In served (workbench) mode it states the workbench rule and offers no
 * control.
 */
import { onMounted, ref } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { isServedMode } from '@/lib/useServedMode';
import { LEGACY_SUMMARY, STARTER_SUMMARY, WORKBENCH_SUMMARY } from './policyDefaultCopy';

const client = useHarnessClient();
const mode = ref<string>('');
const busy = ref(false);
const error = ref('');

onMounted(async () => {
  if (isServedMode()) {
    mode.value = 'workbench';
    return;
  }
  if (!client?.settings?.getPolicyDefault) return;
  try {
    mode.value = (await client.settings.getPolicyDefault()).mode;
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  }
});

async function select(next: 'starter' | 'legacy') {
  if (next === mode.value || !client?.settings?.setPolicyDefault) return;
  busy.value = true;
  error.value = '';
  try {
    await client.settings.setPolicyDefault(next);
    mode.value = next;
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  } finally {
    busy.value = false;
  }
}

const OPTIONS = [
  { value: 'starter' as const, label: 'Starter policy', summary: STARTER_SUMMARY },
  { value: 'legacy' as const, label: 'Ask for every file access', summary: LEGACY_SUMMARY },
];
</script>

<template>
  <section v-if="mode" class="flex flex-col gap-2" data-testid="policy-default-setting">
    <p class="font-ui text-caption uppercase tracking-eyebrow text-ink-muted">Default policy</p>
    <p v-if="mode === 'workbench'" class="font-ui text-xs text-ink-muted" data-testid="policy-default-workbench">
      {{ WORKBENCH_SUMMARY }}
    </p>
    <div v-else class="flex flex-col gap-1" role="radiogroup" aria-label="Default policy">
      <label
        v-for="opt in OPTIONS"
        :key="opt.value"
        class="flex items-start gap-2 font-ui text-xs text-ink cursor-pointer"
        :data-testid="`policy-default-option-${opt.value}`"
      >
        <input
          type="radio"
          name="policy-default"
          class="mt-0.5"
          :value="opt.value"
          :checked="mode === opt.value"
          :disabled="busy"
          @change="select(opt.value)"
        />
        <span>
          <span class="font-medium">{{ opt.label }}.</span>
          <span class="text-ink-muted"> {{ opt.summary }}</span>
        </span>
      </label>
      <p class="font-ui text-xs text-ink-muted">
        A policy on the Policy page, or your organization's Fleet policy, overrides either default.
      </p>
    </div>
    <p v-if="error" class="font-ui text-xs text-signal-danger" role="alert">{{ error }}</p>
  </section>
</template>
