<script setup lang="ts">
/**
 * BundlesView — installed bundles list (NN/SECTION pattern).
 *
 * Shows every bundle pinned in kaneaz.lock with its source channel,
 * signature status, and artifact count. Expanding a row calls
 * Bundle_Get and renders the per-artifact name/kind/content-hash
 * triples — payload bytes never traverse the rpc surface.
 *
 * Empty state surfaces a doc link the user can follow when they want
 * to install a bundle (resolver mission ships the resolver itself).
 */
import { onMounted, ref } from 'vue';
import CanvasHead from '@/shell/CanvasHead.vue';
import SettingsTabs from '@/views/settings/SettingsTabs.vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type { Bundle } from '@/lib/types';

const client = useHarnessClient();

const bundles = ref<readonly Bundle[]>([]);
const loading = ref(false);
const error = ref<string | null>(null);

const expanded = ref<Record<string, Bundle | undefined>>({});

async function refresh() {
  loading.value = true;
  error.value = null;
  try {
    bundles.value = await client.bundle.list();
  } catch (e) {
    error.value = e instanceof Error ? e.message : 'Failed to load bundles.';
    bundles.value = [];
  } finally {
    loading.value = false;
  }
}

async function toggle(id: string) {
  if (expanded.value[id]) {
    expanded.value = { ...expanded.value, [id]: undefined };
    return;
  }
  try {
    const detail = await client.bundle.get(id);
    expanded.value = { ...expanded.value, [id]: detail };
  } catch {
    // Leave un-expanded on error; the row still renders the summary.
  }
}

onMounted(() => {
  void refresh();
});
</script>

<template>
  <div>
    <CanvasHead
      number="03"
      section="BUNDLES"
      title="Installed bundles"
      subtitle="Every bundle pinned in kaneaz.lock with its source channel, signature, and artifact count. Bytes live in the local CAS — nothing leaves the device."
    />
    <SettingsTabs />

    <div v-if="loading" class="px-6 py-4 font-ui text-sm text-ink-muted">
      Loading bundles…
    </div>
    <div
      v-else-if="error"
      class="px-6 py-4 font-ui text-sm text-signal-danger"
      role="alert"
    >
      {{ error }}
    </div>
    <div
      v-else-if="bundles.length === 0"
      class="px-6 py-6 font-ui text-sm text-ink-muted"
    >
      <div class="text-ink">No bundles installed</div>
      <p class="mt-2 max-w-prose text-ink-muted">
        Bundles pin connector configurations, MCP servers, and policy
        clauses in a content-addressed store. Add one by running the
        bundle resolver against a kaneaz.toml manifest.
      </p>
      <a
        href="https://github.com/sigil-tech/kaneaz-harness/blob/main/docs/bundles.md"
        class="mt-3 inline-block text-accent hover:text-accent-muted"
        target="_blank"
        rel="noopener"
      >Read the bundle docs →</a>
    </div>
    <table v-else class="w-full font-ui text-[12px] text-ink" data-testid="bundles-table">
      <thead class="bg-surface-1 text-ink-muted">
        <tr>
          <th class="text-left px-4 py-2 font-medium">Name</th>
          <th class="text-left px-4 py-2 font-medium">Version</th>
          <th class="text-left px-4 py-2 font-medium">Source</th>
          <th class="text-left px-4 py-2 font-medium">Signature</th>
          <th class="text-left px-4 py-2 font-medium">Artifacts</th>
          <th class="text-right px-4 py-2 font-medium"></th>
        </tr>
      </thead>
      <tbody>
        <template v-for="b in bundles" :key="b.id">
          <tr class="border-t border-border-muted hover:bg-surface-1">
            <td class="px-4 py-2 font-mono">{{ b.name }}</td>
            <td class="px-4 py-2 font-mono text-ink-muted">{{ b.version || '—' }}</td>
            <td class="px-4 py-2 text-ink-muted truncate max-w-[24ch]">
              {{ b.source || '—' }}
            </td>
            <td class="px-4 py-2">
              <span
                class="text-[11px] uppercase tracking-[0.12em]"
                :class="b.signature ? 'text-signal-ok' : 'text-ink-subtle'"
              >
                {{ b.signature ? 'Signed' : 'Unsigned' }}
              </span>
            </td>
            <td class="px-4 py-2 text-ink-muted">{{ b.artifactCount }}</td>
            <td class="px-4 py-2 text-right">
              <button
                type="button"
                class="text-[11px] text-ink-muted hover:text-ink"
                :data-testid="`bundle-toggle-${b.id}`"
                @click="toggle(b.id)"
              >
                {{ expanded[b.id] ? 'Hide' : 'View artifacts' }}
              </button>
            </td>
          </tr>
          <tr v-if="expanded[b.id]" class="bg-surface-2 border-t border-border-muted">
            <td colspan="6" class="px-6 py-3">
              <div
                v-if="(expanded[b.id]?.artifacts?.length ?? 0) === 0"
                class="text-[11px] text-ink-muted font-ui"
              >
                Bundle declares no artifacts.
              </div>
              <ul v-else class="space-y-1 font-mono text-[11px]">
                <li
                  v-for="a in expanded[b.id]?.artifacts"
                  :key="a.contentHash + a.name"
                  class="flex items-baseline gap-3"
                >
                  <span class="text-ink-muted w-12 shrink-0">{{ a.kind }}</span>
                  <span class="text-ink truncate flex-1">{{ a.name }}</span>
                  <span class="text-ink-subtle">{{ a.contentHash.slice(0, 19) }}…</span>
                </li>
              </ul>
            </td>
          </tr>
        </template>
      </tbody>
    </table>
  </div>
</template>
