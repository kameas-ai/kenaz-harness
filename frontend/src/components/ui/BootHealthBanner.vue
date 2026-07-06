<script setup lang="ts">
import { ref, onMounted, computed } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';

/**
 * BootHealthBanner — surfaces init-phase errors from MCP, skills, or fleet
 * startup so the user sees a targeted warning instead of silent degradation
 * (FR-008 / agent-loop-robustness-parity WP08).
 *
 * Calls BootHealth_Get on mount via the harness client (isolation rule:
 * no direct wailsjs imports outside harnessClient.ts). When any subsystem
 * reported an init error a dismissable banner is shown per failing subsystem.
 * In served mode BootHealth_Get throws ServedUnsupportedError; the banner
 * silently shows nothing.
 */

interface BootHealthReport {
  mcpInitError?: string;
  skillsInitError?: string;
  fleetInitError?: string;
}

const client = useHarnessClient();
const report = ref<BootHealthReport | null>(null);
const dismissed = ref(false);

onMounted(async () => {
  try {
    const r = await client.BootHealth_Get();
    // Only store when at least one error is non-empty.
    if (r.mcpInitError || r.skillsInitError || r.fleetInitError) {
      report.value = {
        mcpInitError: r.mcpInitError ?? undefined,
        skillsInitError: r.skillsInitError ?? undefined,
        fleetInitError: r.fleetInitError ?? undefined,
      };
    }
  } catch {
    // BootHealth_Get not available (e.g. served mode) — silent.
  }
});

const errors = computed<string[]>(() => {
  if (!report.value) return [];
  const out: string[] = [];
  if (report.value.mcpInitError) out.push(`MCP: ${report.value.mcpInitError}`);
  if (report.value.skillsInitError) out.push(`Skills: ${report.value.skillsInitError}`);
  if (report.value.fleetInitError) out.push(`Fleet: ${report.value.fleetInitError}`);
  return out;
});

const visible = computed(() => !dismissed.value && errors.value.length > 0);
</script>

<template>
  <div
    v-if="visible"
    class="px-4 py-2 bg-surface-2 border-b border-signal-warning flex items-start justify-between gap-3"
    role="alert"
  >
    <div class="flex flex-col gap-1">
      <span class="font-ui text-sm font-medium text-ink">
        Startup errors — some features may be degraded.
      </span>
      <ul class="list-disc list-inside">
        <li
          v-for="(err, i) in errors"
          :key="i"
          class="font-ui text-xs text-ink-muted"
        >
          {{ err }}
        </li>
      </ul>
    </div>
    <button
      type="button"
      class="font-ui text-xs text-ink-muted hover:text-ink px-2 py-1 rounded-sm flex-shrink-0"
      @click="dismissed = true"
    >
      Dismiss
    </button>
  </div>
</template>
