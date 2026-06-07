<script setup lang="ts">
/**
 * ToolsView — connected MCP servers list (NN/SECTION pattern).
 *
 * Renders every MCP server registered with the harness alongside
 * its transport, advertised capabilities, and current state. The
 * mcp-client implementation hasn't landed yet, so the list is empty
 * by construction — the view is structured so it lights up the
 * moment a server registers via a bundle.
 */
import { onMounted, ref } from 'vue';
import CanvasHead from '@/shell/CanvasHead.vue';
import KaneazToolsPanel from './KaneazToolsPanel.vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type { MCPServer } from '@/lib/types';

const client = useHarnessClient();

const servers = ref<readonly MCPServer[]>([]);
const loading = ref(false);
const error = ref<string | null>(null);

async function refresh() {
  loading.value = true;
  error.value = null;
  try {
    servers.value = await client.mcp.listServers();
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load MCP servers.';
    servers.value = [];
  } finally {
    loading.value = false;
  }
}

function stateColor(state: string): string {
  switch (state.toLowerCase()) {
    case 'ready':
    case 'connected':
      return 'text-signal-ok';
    case 'connecting':
    case 'handshake':
      return 'text-signal-warn';
    case 'error':
    case 'lost':
      return 'text-signal-danger';
    default:
      return 'text-ink-muted';
  }
}

onMounted(() => {
  void refresh();
});
</script>

<template>
  <div>
    <CanvasHead
      number="02"
      section="TOOLS"
      title="Tools"
      subtitle="Built-in Kaneaz tools toggle on top; below them, every MCP server registered with the harness. All tool calls flow through the local mcp-client; nothing leaves the device."
    />

    <KaneazToolsPanel />

    <div class="px-6 pt-2 pb-1">
      <h2
        class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
      >
        MCP servers
      </h2>
    </div>

    <div v-if="loading" class="px-6 py-4 font-ui text-sm text-ink-muted">
      Loading servers…
    </div>
    <div
      v-else-if="error"
      class="px-6 py-4 font-ui text-sm text-signal-danger"
      role="alert"
    >
      {{ error }}
    </div>
    <div
      v-else-if="servers.length === 0"
      class="px-6 py-6 font-ui text-sm text-ink-muted"
      data-testid="tools-empty"
    >
      <div class="text-ink">No MCP servers configured</div>
      <p class="mt-2 max-w-prose text-ink-muted">
        Add an MCP server by installing a bundle that registers one. The
        harness's local mcp-client routes every call through the policy
        layer so capability advertisements stay enforceable.
      </p>
      <a
        href="https://github.com/kameas-ai/kenaz-harness/blob/main/docs/mcp.md"
        class="mt-3 inline-block text-accent hover:text-accent-muted"
        target="_blank"
        rel="noopener"
      >Read the MCP docs →</a>
    </div>
    <table v-else class="w-full font-ui text-[12px] text-ink" data-testid="tools-table">
      <thead class="bg-surface-1 text-ink-muted">
        <tr>
          <th class="text-left px-4 py-2 font-medium">Name</th>
          <th class="text-left px-4 py-2 font-medium">Transport</th>
          <th class="text-left px-4 py-2 font-medium">Capabilities</th>
          <th class="text-left px-4 py-2 font-medium">Version</th>
          <th class="text-left px-4 py-2 font-medium">Status</th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="s in servers"
          :key="s.id"
          class="border-t border-border-muted hover:bg-surface-1"
        >
          <td class="px-4 py-2 font-mono">{{ s.name }}</td>
          <td class="px-4 py-2 text-ink-muted">{{ s.transport || '—' }}</td>
          <td class="px-4 py-2 text-ink-muted">
            <span
              v-if="(s.capabilities?.length ?? 0) === 0"
              class="text-ink-subtle"
            >—</span>
            <span v-else class="font-mono text-[11px]">{{ s.capabilities?.join(', ') }}</span>
          </td>
          <td class="px-4 py-2 font-mono text-ink-muted">{{ s.version || '—' }}</td>
          <td class="px-4 py-2">
            <span
              class="text-[11px] uppercase tracking-[0.12em]"
              :class="stateColor(s.state)"
            >
              {{ s.state || 'unknown' }}
            </span>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
