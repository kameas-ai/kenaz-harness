<script setup lang="ts">
import { computed } from 'vue';
import { useShellStatus } from '@/lib/useHarnessAPI';

/**
 * StatusPill — minimal build-version footer. The original FR-001f
 * triple (provider · trust · build) is reduced to just the build
 * label until the upstream surfaces emit real values for the other
 * two fields. Slot stays in LegendBar for surface-injected pills.
 *
 * BuildVersion arrives from main.Version via core.BuildVersion(),
 * which canonically carries a leading "v" (e.g. "v0.4.3"). We render
 * it as-is — historically the template prefixed a literal "v" which
 * produced "v v0.4.3"; that prefix is now gone, and we explicitly
 * normalize so a future double-prefix or missing-prefix can't visibly
 * regress.
 */
const status = useShellStatus();

const displayVersion = computed(() => {
  const raw = (status.value.harnessBuild ?? '').trim();
  if (!raw) return '';
  if (raw === 'dev') return 'dev';
  // Strip any leading "v" or "V" then re-prefix exactly one.
  const stripped = raw.replace(/^v+/i, '');
  return `v${stripped}`;
});
</script>

<template>
  <div
    class="flex items-center gap-2 px-3 py-1 text-[11px] font-ui text-ink-muted"
    role="status"
    aria-label="Harness build"
  >
    <span class="font-mono text-ink">{{ displayVersion }}</span>
  </div>
</template>
