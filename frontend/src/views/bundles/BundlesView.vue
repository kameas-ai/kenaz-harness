<script setup lang="ts">
/**
 * BundlesView — installed bundles list (NN/SECTION pattern).
 *
 * Shows every bundle pinned in kenaz.lock with its source channel,
 * signature status, and artifact count. Expanding a row calls
 * Bundle_Get and renders the per-artifact name/kind/content-hash
 * triples — payload bytes never traverse the rpc surface.
 *
 * Empty state surfaces a doc link the user can follow when they want
 * to install a bundle (resolver mission ships the resolver itself).
 *
 * v0.3.0 beta also exposes a minimal Install form (local_path channel
 * only) and a per-row Remove affordance. Both call straight through to
 * Bundle_Install / Bundle_Remove on the harness.
 */
import { onMounted, ref } from 'vue';
import SettingsShell from '@/views/settings/SettingsShell.vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type { Bundle } from '@/lib/types';
// fleet-share-and-sync-01NDFSEX14 WP03 — Publish to team catalog
import PublishDialog from '@/views/marketplace/PublishDialog.vue';
import { signedIn } from '@/lib/featureFlags';

const client = useHarnessClient();

const bundles = ref<readonly Bundle[]>([]);
const loading = ref(false);
const error = ref<string | null>(null);

const expanded = ref<Record<string, Bundle | undefined>>({});

const installPath = ref('');
const installBusy = ref(false);
const installError = ref<string | null>(null);
const removingId = ref<string | null>(null);

// ── publish-to-team state (WP03) ─────────────────────────────────────────
const publishDialogOpen = ref(false);
const publishingBundle = ref<Bundle | null>(null);
const publishToast = ref<string | null>(null);
let publishToastTimer: ReturnType<typeof setTimeout> | null = null;

function showPublishToast(msg: string) {
  publishToast.value = msg;
  if (publishToastTimer) clearTimeout(publishToastTimer);
  publishToastTimer = setTimeout(() => { publishToast.value = null; }, 3000);
}

function openPublishDialog(b: Bundle) {
  publishingBundle.value = b;
  publishDialogOpen.value = true;
}

function onBundlePublished() {
  publishDialogOpen.value = false;
  publishingBundle.value = null;
  showPublishToast('Bundle published to team catalog.');
}

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

async function install() {
  installError.value = null;
  const path = installPath.value.trim();
  if (!path) {
    installError.value = 'Path is required.';
    return;
  }
  installBusy.value = true;
  try {
    await client.bundle.install({ kind: 'local_path', path });
    installPath.value = '';
    await refresh();
  } catch (e) {
    installError.value = e instanceof Error ? e.message : 'Install failed.';
  } finally {
    installBusy.value = false;
  }
}

async function remove(id: string) {
  removingId.value = id;
  try {
    await client.bundle.remove(id);
    expanded.value = { ...expanded.value, [id]: undefined };
    await refresh();
  } catch (e) {
    error.value = e instanceof Error ? e.message : `Failed to remove ${id}.`;
  } finally {
    removingId.value = null;
  }
}

onMounted(() => {
  void refresh();
});
</script>

<template>
  <SettingsShell
    number="03"
    section="BUNDLES"
    title="Installed bundles"
    subtitle="Every bundle pinned in kenaz.lock with its source channel, signature, and artifact count. Bytes live in the local CAS — bundle bytes never leave the device (fleet config-apply ACKs and opted-in telemetry are the only egress when fleet config distribution is active)."
  >
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
        bundle resolver against a kenaz.toml manifest.
      </p>
      <a
        href="https://github.com/sigil-tech/kenaz-harness/blob/main/docs/bundles.md"
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
            <td class="px-4 py-2 text-right space-x-3">
              <button
                type="button"
                class="text-[11px] text-ink-muted hover:text-ink"
                :data-testid="`bundle-toggle-${b.id}`"
                @click="toggle(b.id)"
              >
                {{ expanded[b.id] ? 'Hide' : 'View artifacts' }}
              </button>
              <!-- fleet-share-and-sync-01NDFSEX14 WP03 -->
              <button
                v-if="signedIn"
                type="button"
                class="text-[11px] text-ink-muted hover:text-accent"
                :data-testid="`bundle-publish-${b.id}`"
                @click="openPublishDialog(b)"
              >
                Publish to team
              </button>
              <button
                type="button"
                class="text-[11px] text-signal-danger hover:text-ink disabled:opacity-50"
                :data-testid="`bundle-remove-${b.id}`"
                :disabled="removingId === b.id"
                @click="remove(b.id)"
              >
                {{ removingId === b.id ? 'Removing…' : 'Remove' }}
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

    <form
      class="mt-6 px-6 py-4 border-t border-border-muted font-ui text-[12px]"
      data-testid="bundle-install-form"
      @submit.prevent="install"
    >
      <div class="text-ink mb-2">Install a local_path bundle</div>
      <p class="text-ink-muted mb-3 max-w-prose">
        Beta scope: point at an absolute filesystem path that contains a
        <code class="font-mono">kenaz.yaml</code> manifest at its root.
        The bundle is registered in the lockfile; artifact bytes stay
        on disk where they already are.
      </p>
      <div class="flex items-center gap-2">
        <input
          v-model="installPath"
          type="text"
          aria-label="Bundle installation path"
          placeholder="/absolute/path/to/bundle"
          class="flex-1 px-2 py-1 font-mono text-[12px] bg-surface-1 text-ink border border-border-muted rounded"
          :disabled="installBusy"
          data-testid="bundle-install-path"
        />
        <button
          type="submit"
          class="px-3 py-1 text-[11px] bg-accent text-ink hover:bg-accent-muted disabled:opacity-50 rounded"
          :disabled="installBusy || !installPath.trim()"
          data-testid="bundle-install-submit"
        >
          {{ installBusy ? 'Installing…' : 'Install' }}
        </button>
      </div>
      <div
        v-if="installError"
        class="mt-2 text-[11px] text-signal-danger"
        role="alert"
        data-testid="bundle-install-error"
      >
        {{ installError }}
      </div>
    </form>
    <!-- fleet-share-and-sync-01NDFSEX14 WP03 — Publish dialog -->
    <PublishDialog
      :open="publishDialogOpen"
      kind="bundle"
      :slug="publishingBundle?.name ?? ''"
      :payload-json="JSON.stringify(publishingBundle ?? {})"
      @close="publishDialogOpen = false; publishingBundle = null"
      @published="onBundlePublished"
    />
    <!-- Publish toast -->
    <div
      v-if="publishToast"
      class="fixed bottom-6 right-6 z-[9999] rounded-sm border border-border-muted bg-surface-3 px-4 py-2 font-ui text-sm text-ink shadow-lg"
      role="status"
      data-testid="bundle-publish-toast"
    >
      {{ publishToast }}
    </div>
  </SettingsShell>
</template>
