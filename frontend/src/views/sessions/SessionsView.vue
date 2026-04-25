<script setup lang="ts">
import CanvasHead from '@/shell/CanvasHead.vue';
import PrivacyGuarantees from '@/components/ui/PrivacyGuarantees.vue';
import { useShellStatus } from '@/lib/useHarnessAPI';
import { computed } from 'vue';

const status = useShellStatus();

const guarantees = computed(() => [
  { label: 'Credentials never persisted', on: true },
  { label: 'Event-log redaction applied', on: status.value.redactionOn },
  { label: 'Local-first: zero outbound traffic', on: status.value.localFirstOn },
  { label: 'Org policy applied', on: status.value.policyApplied },
]);
</script>

<template>
  <div class="grid gap-6" style="grid-template-columns: 1fr 280px">
    <div>
      <CanvasHead
        number="01"
        section="SESSIONS"
        title="Recent runs"
        subtitle="Each session preserves its scroll position and draft input. Switch via the left rail; nothing is lost."
      />
      <div class="px-6 py-4 font-ui text-sm text-ink-muted">
        No sessions yet. Click "New session" in the rail to start one.
      </div>
    </div>
    <div class="px-4 pt-6">
      <PrivacyGuarantees status="APPLIED" :guarantees="guarantees" />
    </div>
  </div>
</template>
