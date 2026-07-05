<script setup lang="ts">
import LiveRateIndicator from '@/components/ui/LiveRateIndicator.vue';
import StatusPill from './StatusPill.vue';
import MemoryCaptureRatePill from './MemoryCaptureRatePill.vue';
import FleetHealthChip from './FleetHealthChip.vue';
import { useShellStatus, useHarnessClient } from '@/lib/useHarnessAPI';
import { ref, onMounted } from 'vue';

/**
 * LegendBar — bottom rail. The Kenaz-inherited category dots
 * (FILESYSTEM / PROCESS / CLIPBOARD / NETWORK / KEYSTROKE) are
 * hidden by default in the harness because nothing emits per-
 * category telemetry yet. The `legend` slot is preserved so a
 * future surface can inject its own legend without re-adding the
 * dots here.
 */
const status = useShellStatus();

// ── crash-reporting tier indicator (sentry-error-monitoring-01KX5R8G WP05) ──
// Show a discreet pill when crash reporting is active (tier != off), so the
// user always has a visible reminder without opening Settings → Privacy.
const client = useHarnessClient();
const crashTier = ref<string>('off');

onMounted(async () => {
  try {
    const s = await client.settings.get();
    crashTier.value = s.crashReportingTier ?? 'off';
  } catch {
    // ignore — pill is cosmetic
  }
});
</script>

<template>
  <div class="flex items-center justify-between px-3 py-1.5 text-[11px]">
    <div class="flex items-center gap-3" aria-label="Status legend">
      <slot name="legend" />
      <slot name="rate">
        <LiveRateIndicator
          v-if="status.eventRate > 0"
          :rate="status.eventRate"
          unit="e/s"
        />
      </slot>
      <MemoryCaptureRatePill />
      <!-- Crash-reporting tier indicator — cosmetic reminder when active -->
      <span
        v-if="crashTier !== 'off'"
        class="text-[10px] text-ink-muted px-1.5 py-0.5 rounded border border-border-muted"
        :title="`Crash reporting: ${crashTier}`"
        data-testid="crash-reporting-pill"
      >reporting: {{ crashTier }}</span>
      <!-- fleet-integrity-observability WP10: persistent fleet-health indicator -->
      <FleetHealthChip />
    </div>
    <StatusPill />
  </div>
</template>
