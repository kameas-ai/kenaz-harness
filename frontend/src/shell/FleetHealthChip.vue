<script setup lang="ts">
/**
 * FleetHealthChip — persistent fleet health indicator in the LegendBar.
 *
 * Shows a small labeled chip with the config source and, when non-empty,
 * the last error as a tooltip. The chip is hidden when fleet is not
 * configured in this build (configDistributionEnabled=false) so OSS users
 * don't see a fleet indicator that has no meaning for them.
 *
 * States displayed:
 *   fleet          — live config from fleet server (green)
 *   stale-cache    — cached bundle being used (yellow)
 *   default-deny   — no bundle ever applied (muted)
 *   no-key         — signing key not wired in this binary (muted/hidden)
 *   unknown-key    — latest bundle signed with a key this build never pinned;
 *                    the install must update (warn)
 *   paused         — a Kameas-staff "pause paid features" hold is on the org
 *                    (kenaz-fleet PR 206); the last applied bundle stays in
 *                    force. Overrides the config source (warn), never upsell.
 *
 * (fleet-integrity-observability WP10 / FR-010)
 */
import { ref, onMounted, computed } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type { FleetHealthView } from '@/lib/types';
import { useFleetSession } from '@/lib/fleetSession';
import { ORG_PAUSED_TITLE, orgPausedCategoryLine } from '@/lib/orgPausedCopy';

const client = useHarnessClient();
const { orgPaused, pausedCategory } = useFleetSession(client);

const health = ref<FleetHealthView | null>(null);

onMounted(async () => {
  try {
    health.value = await client.settings.fleetHealth();
  } catch {
    // Best-effort — chip simply stays hidden on error.
  }
});

// Chip is visible only when the binary has a signing key wired.
const visible = computed(() =>
  health.value !== null && health.value.configDistributionEnabled,
);

// Color class based on configSource.
const chipClass = computed(() => {
  if (orgPaused.value) return 'text-signal-warn border-signal-warn/30 bg-signal-warn/10';
  const src = health.value?.configSource ?? '';
  if (src === 'fleet') return 'text-signal-ok border-signal-ok/30 bg-signal-ok/10';
  if (src.startsWith('stale') || src === 'cache' || src === 'unknown-key')
    return 'text-signal-warn border-signal-warn/30 bg-signal-warn/10';
  return 'text-ink-muted border-border-muted';
});

// Short label for the chip.
const label = computed(() => {
  if (orgPaused.value) return 'paused by org';
  const src = health.value?.configSource ?? '';
  if (src === 'fleet') return 'fleet';
  if (src === 'stale-cache' || src === 'cache') return 'stale-cache';
  if (src === 'default-deny' || src === 'default-deny-degraded') return 'default-deny';
  if (src === 'unknown-key') return 'unknown key — update';
  return src || 'fleet?';
});

// Tooltip text.
const tooltip = computed(() => {
  if (orgPaused.value) return `${ORG_PAUSED_TITLE}. ${orgPausedCategoryLine(pausedCategory.value)}`;
  const err = health.value?.configLastError;
  if (err) return `Fleet: ${label.value} — ${err}`;
  return `Fleet: ${label.value}`;
});
</script>

<template>
  <span
    v-if="visible"
    class="text-[10px] px-1.5 py-0.5 rounded border"
    :class="chipClass"
    :title="tooltip"
    data-testid="fleet-health-chip"
  >fleet: {{ label }}</span>
</template>
