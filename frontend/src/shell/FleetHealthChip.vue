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
 *
 * Freshness (dogfood 2026-10-08 P2): the chip used to read fleetHealth once
 * in onMounted and never again, so it said `default-deny` all session after
 * a verified bundle apply. It now re-reads on every fleet:session-changed
 * snapshot (the fleet event the frontend already receives — enroll_ok,
 * capabilities and sync-lane transitions all push one, and a bundle apply
 * unmasks capabilities), on window focus, and on a 60s interval while
 * mounted as the backstop for applies that change no session field.
 */
import { ref, onMounted, onBeforeUnmount, computed, watch } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type { FleetHealthView } from '@/lib/types';
import { useFleetSession } from '@/lib/fleetSession';
import { ORG_PAUSED_TITLE, orgPausedCategoryLine } from '@/lib/orgPausedCopy';

const client = useHarnessClient();
const { orgPaused, pausedCategory, session } = useFleetSession(client);

const health = ref<FleetHealthView | null>(null);

/** Re-read interval while mounted (backstop for event-less applies). */
const FLEET_HEALTH_REFRESH_MS = 60_000;

let readSeq = 0;
async function refreshHealth(): Promise<void> {
  const mySeq = ++readSeq;
  try {
    const v = await client.settings.fleetHealth();
    if (mySeq === readSeq) health.value = v;
  } catch {
    // Best-effort — keep the last answer (or stay hidden if there is none).
  }
}

// Every pushed fleet-session snapshot is a fleet transition worth a re-read.
watch(session, () => void refreshHealth());

let interval: ReturnType<typeof setInterval> | null = null;
const onFocus = () => void refreshHealth();

onMounted(() => {
  void refreshHealth();
  interval = setInterval(() => void refreshHealth(), FLEET_HEALTH_REFRESH_MS);
  if (typeof window !== 'undefined') window.addEventListener('focus', onFocus);
});

onBeforeUnmount(() => {
  if (interval) clearInterval(interval);
  interval = null;
  if (typeof window !== 'undefined') window.removeEventListener('focus', onFocus);
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

// What each state means, in words (dogfood 2026-10-08: the chip had no
// affordance explaining its states).
const stateDescription = computed(() => {
  const src = health.value?.configSource ?? '';
  if (src === 'fleet')
    return "Fleet: live — your fleet server's latest signed config bundle is applied.";
  if (src === 'stale-cache' || src === 'cache')
    return 'Fleet: stale cache — the fleet server could not be reached; the last verified bundle stays in force.';
  if (src === 'default-deny' || src === 'default-deny-degraded')
    return 'Fleet: default-deny — no fleet config bundle has ever been applied on this device, so fleet-gated features stay off.';
  if (src === 'no-key')
    return 'Fleet: no signing key is pinned in this build, so fleet config distribution is off.';
  if (src === 'unknown-key')
    return 'Fleet: the latest bundle was signed with a key this build does not trust — update the app.';
  return `Fleet: ${label.value}`;
});

// Tooltip text.
const tooltip = computed(() => {
  if (orgPaused.value) return `${ORG_PAUSED_TITLE}. ${orgPausedCategoryLine(pausedCategory.value)}`;
  const err = health.value?.configLastError;
  if (err) return `${stateDescription.value} Last error: ${err}`;
  return stateDescription.value;
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
