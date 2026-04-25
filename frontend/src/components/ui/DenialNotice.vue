<script setup lang="ts">
import type { Denial } from '@/lib/types';

/**
 * DenialNotice — uniform render path for policy-engine denials. FR-012,
 * SC-009. Downstream policy mission feeds Denial objects via
 * usePolicyDecisions().onDenied.
 */
defineProps<{
  denial: Denial;
}>();
</script>

<template>
  <section
    class="rounded-md border border-signal-danger bg-surface-1 px-4 py-3"
    role="alert"
    aria-label="Action denied by policy"
  >
    <div class="font-ui text-[11px] uppercase tracking-[0.18em] text-signal-danger">
      Denied
    </div>
    <dl class="mt-2 grid gap-1 text-[12px] font-ui">
      <div class="flex gap-2">
        <dt class="text-ink-muted w-28">Policy</dt>
        <dd class="font-mono text-ink">{{ denial.policyId }}</dd>
      </div>
      <div class="flex gap-2">
        <dt class="text-ink-muted w-28">Clause</dt>
        <dd class="font-mono text-ink">{{ denial.clauseId }}</dd>
      </div>
      <div class="flex gap-2">
        <dt class="text-ink-muted w-28">Violating input</dt>
        <dd class="font-mono text-ink truncate">{{ denial.violatingInput }}</dd>
      </div>
      <div class="flex gap-2">
        <dt class="text-ink-muted w-28">Remediation</dt>
        <dd class="text-ink">{{ denial.remediation }}</dd>
      </div>
    </dl>
  </section>
</template>
