<script setup lang="ts">
/**
 * MCPHealthSettingsPanel — settings dial for MCP server health behaviour.
 * (mcp-server-health-ui-01KQ8TD6 WP06)
 *
 * Surfaces one dial:
 *   Auto-restart on disconnect — when ON (default), the supervisor restarts
 *   a stdio MCP server after two consecutive ping failures. When OFF, the
 *   unresponsive server is left running (a warning is logged per trip) for
 *   manual inspection. The Go reader is the healthPinger's trip decision
 *   (core/mcp/transport/stdio/health.go autoRestartEnabled, fed live from
 *   Settings by core/rpc/api.go's stdio.PoolOptions.AutoRestartEnabled).
 *   A server whose process EXITS is still restarted either way — the dial
 *   only governs the ping-failure path, and the copy below says so.
 *
 * Mounted at Settings › Integrations › MCP Servers (?tab=mcp-servers).
 */
import { onMounted, ref } from 'vue';
import { useHarnessClient } from '@/lib/harnessClientContext';

const client = useHarnessClient();

const autoRestart = ref(true); // default ON per zero-value semantics
const busy = ref(false);
const error = ref<string | null>(null);

async function load() {
  try {
    autoRestart.value = await client.settings.getMCPAutoRestart();
  } catch {
    // Default true on read failure — don't break the panel.
    autoRestart.value = true;
  }
}

async function toggle(event: Event) {
  if (busy.value) return;
  const next = (event.target as HTMLInputElement).checked;
  busy.value = true;
  error.value = null;
  const prev = autoRestart.value;
  autoRestart.value = next;
  try {
    await client.settings.setMCPAutoRestart(next);
  } catch (e) {
    autoRestart.value = prev;
    error.value = e instanceof Error ? e.message : 'Failed to save setting.';
  } finally {
    busy.value = false;
  }
}

onMounted(() => {
  void load();
});
</script>

<template>
  <section class="px-6 py-4" data-testid="mcp-health-settings-panel">
    <h2
      class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle mb-3"
    >
      MCP server health
    </h2>

    <div
      class="rounded-sm border border-border-muted bg-surface-1 divide-y divide-border-muted"
    >
      <!-- Auto-restart row -->
      <div
        class="px-4 py-3 grid gap-3 items-start"
        style="grid-template-columns: 1fr auto"
        data-testid="mcp-auto-restart-row"
      >
        <div>
          <div class="font-ui text-[13px] text-ink">
            Auto-restart on disconnect
          </div>
          <p class="mt-1 text-[11px] text-ink-muted max-w-prose">
            When enabled, the harness automatically restarts a local (stdio)
            MCP server after two consecutive failed health pings (default:
            on). Disable to leave an unresponsive server running for manual
            inspection instead. A server whose process exits is still
            restarted.
          </p>
          <div
            v-if="error"
            class="mt-2 text-[11px] text-signal-danger"
            role="alert"
            data-testid="mcp-auto-restart-error"
          >
            {{ error }}
          </div>
        </div>
        <label
          class="inline-flex items-center cursor-pointer select-none"
          :class="busy ? 'opacity-60 cursor-wait' : ''"
        >
          <input
            type="checkbox"
            class="accent-accent w-4 h-4"
            :checked="autoRestart"
            :disabled="busy"
            data-testid="mcp-auto-restart-toggle"
            @change="toggle"
          />
        </label>
      </div>
    </div>
  </section>
</template>
