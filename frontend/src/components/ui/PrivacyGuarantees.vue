<script setup lang="ts">
/**
 * PrivacyGuarantees — Kenaz right-rail panel pattern (FR-001e).
 * Status (APPLIED / PARTIAL / OFF) + checkmarked guarantees.
 *
 * NEVER renders a credential value. The prop type explicitly forbids
 * `value` / `secret` / `password` / `apiKey` / `token` fields on each
 * guarantee — this is the in-component defence-in-depth complement to
 * the WP14 lint rule (FR-020 / C-004).
 */

import { computed } from 'vue';

type ForbiddenSecretField = 'value' | 'secret' | 'password' | 'apiKey' | 'token';

type SafeGuarantee = {
  label: string;
  on: boolean;
} & {
  [K in ForbiddenSecretField]?: never;
};

const props = defineProps<{
  status: 'APPLIED' | 'PARTIAL' | 'OFF';
  guarantees: ReadonlyArray<SafeGuarantee>;
}>();

const statusColor = computed(() => {
  switch (props.status) {
    case 'APPLIED':
      return 'text-signal-ok';
    case 'PARTIAL':
      return 'text-signal-warn';
    case 'OFF':
      return 'text-signal-danger';
  }
});
</script>

<template>
  <aside
    class="rounded-md border border-accent-hairline bg-surface-1 px-4 py-3"
    aria-label="Privacy guarantees"
  >
    <div
      class="font-ui text-[11px] uppercase tracking-[0.18em]"
      :class="statusColor"
    >
      {{ status }}
    </div>
    <div class="mt-1 font-ui text-sm text-ink">Privacy guarantees</div>
    <ul class="mt-3 space-y-1.5">
      <li
        v-for="g in guarantees"
        :key="g.label"
        class="flex items-start gap-2 text-[12px] font-ui"
        :class="g.on ? 'text-ink' : 'text-ink-dim'"
      >
        <span
          class="inline-block w-3 leading-none mt-0.5"
          :class="g.on ? 'text-signal-ok' : 'text-ink-dim'"
          aria-hidden="true"
        >
          {{ g.on ? '✓' : '–' }}
        </span>
        <span>{{ g.label }}</span>
      </li>
    </ul>
  </aside>
</template>
