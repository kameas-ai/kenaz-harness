<script setup lang="ts">
/**
 * FeatureFlagsView — Settings → Feature Flags tab.
 *
 * Shows all known feature flags, their current enabled/disabled state,
 * and the environment variable that controls each one. Flags are
 * read-only at runtime; users must restart the app after changing an
 * environment variable.
 *
 * Accessible via /settings?tab=flags.
 *
 * user-slash-commands-01KQ8TD9 WP09.
 */
import { onMounted, ref } from 'vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import type { FeatureFlagInfo } from '@/lib/types';

const client = useHarnessClient();
const flags = ref<FeatureFlagInfo[]>([]);
const loading = ref(false);
const error = ref<string | null>(null);

async function refresh(): Promise<void> {
  loading.value = true;
  error.value = null;
  try {
    flags.value = await client.config.getFlags();
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
    flags.value = [];
  } finally {
    loading.value = false;
  }
}

onMounted(() => {
  void refresh();
});
</script>

<template>
  <div class="px-6 py-4 max-w-3xl">
      <div
        v-if="error"
        class="mb-3 rounded-md border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
        role="alert"
        data-testid="feature-flags-error"
      >
        {{ error }}
      </div>

      <div
        v-if="loading"
        class="font-ui text-[12px] text-ink-muted"
        role="status"
        data-testid="feature-flags-loading"
      >
        Loading flags…
      </div>

      <div
        v-else-if="flags.length === 0"
        class="font-ui text-[12px] text-ink-muted"
        data-testid="feature-flags-empty"
      >
        No feature flags registered.
      </div>

      <table
        v-else
        class="w-full border-collapse text-left"
        data-testid="feature-flags-table"
      >
        <thead>
          <tr
            class="border-b border-border-muted text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
          >
            <th class="px-3 py-2 font-ui font-medium">Flag</th>
            <th class="px-3 py-2 font-ui font-medium">Status</th>
            <th class="px-3 py-2 font-ui font-medium">Description</th>
            <th class="px-3 py-2 font-ui font-medium">Env var</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="flag in flags"
            :key="flag.name"
            class="border-b border-border-muted"
            :data-testid="`flag-row-${flag.name}`"
          >
            <td class="px-3 py-2 font-mono text-[12px] text-ink">
              {{ flag.name }}
            </td>
            <td class="px-3 py-2">
              <span
                :class="
                  flag.enabled
                    ? 'bg-accent-glow text-accent border border-accent-hairline'
                    : 'bg-surface-2 text-ink-muted border border-border-muted'
                "
                class="font-ui text-[10px] uppercase tracking-[0.18em] rounded px-1.5 py-0.5"
                :data-testid="`flag-status-${flag.name}`"
              >
                {{ flag.enabled ? 'on' : 'off' }}
              </span>
            </td>
            <td class="px-3 py-2 font-ui text-[12px] text-ink-muted max-w-xs">
              {{ flag.description }}
            </td>
            <td class="px-3 py-2 font-mono text-[11px] text-accent">
              {{ flag.envVar }}
            </td>
          </tr>
        </tbody>
      </table>

      <p class="mt-4 font-ui text-[11px] text-ink-muted">
        Set to <code class="font-mono">0</code> or <code class="font-mono">false</code> to disable;
        any other value (or unset) enables. Requires app restart.
      </p>
    </div>
</template>
