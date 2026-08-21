<script setup lang="ts">
/**
 * BundlesView — installed bundles list (NN/SECTION pattern).
 *
 * Shows every bundle pinned in kenaz.lock with its source channel,
 * VERIFICATION state, and artifact count. Expanding a row calls
 * Bundle_Get and renders the per-artifact name/kind/content-hash
 * triples — payload bytes never traverse the rpc surface.
 *
 * Empty state surfaces a doc link the user can follow when they want
 * to install a bundle (resolver mission ships the resolver itself).
 *
 * The install form offers every channel kind the backend has a
 * REGISTERED FACTORY for (UNIT-8, bundle-download-and-verify-01PMZ909,
 * spec §5.7 / tasks.md UNIT-8 step 1) — see CHANNEL_KINDS below for why
 * this list is hand-maintained rather than fetched from a
 * Bundle_ListChannelKinds RPC, and check-bundle-channel-kinds-sync.sh
 * for what keeps it honest. Both call straight through to
 * Bundle_Install / Bundle_Remove on the harness.
 */
import { onMounted, ref, computed } from 'vue';
import SettingsShell from '@/views/settings/SettingsShell.vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type { Bundle, TrustAnchor } from '@/lib/types';
// fleet-share-and-sync-01NDFSEX14 WP03 — Publish to team catalog
import PublishDialog from '@/views/marketplace/PublishDialog.vue';
import { signedIn } from '@/lib/featureFlags';

const client = useHarnessClient();

const bundles = ref<readonly Bundle[]>([]);
const loading = ref(false);
const error = ref<string | null>(null);

const expanded = ref<Record<string, Bundle | undefined>>({});

// UNIT-8: the channel picker offers exactly the kinds the backend has a
// registered channels.Registry factory for — core/bundle/channels/
// localpath/localpath.go:31 (Kind = "local_path", always registered by
// bundle.NewDefaultRegistry) and core/bundle/channels/http/http.go:36
// (Kind = "http_mirror", registered ONLY by core/rpc/api.go's UNIT-7
// wiring, which is also the only place a Cedar gate exists in front of
// it — see ActionBundleInstall). There is no RPC that returns this set
// at runtime: adding one requires regenerating frontend/wailsjs/**,
// which per this repo's tooling rules only happens via `wails generate
// module` against an overridden HOME/KENAZ_HARNESS_ENV, a step this
// change deliberately does not take. check-bundle-channel-kinds-sync.sh
// (scripts/ci/) greps both this literal list and every core/bundle
// channel package's `const Kind = "..."` declaration and fails the
// build the moment they diverge — so an unimplemented (or since-removed)
// kind can still never survive here, just via a CI gate instead of a
// live query. Update this array AND run/extend that gate in the same
// commit that adds or removes a channel package.
const CHANNEL_KINDS: ReadonlyArray<{ kind: string; label: string; field: 'path' | 'url' }> = [
  { kind: 'local_path', label: 'local_path — an absolute filesystem path', field: 'path' },
  { kind: 'http_mirror', label: 'http_mirror — a URL served over HTTP(S)', field: 'url' },
];

const installKind = ref(CHANNEL_KINDS[0].kind);
const installLocator = ref('');
const installBusy = ref(false);
const installError = ref<string | null>(null);
const removingId = ref<string | null>(null);

const activeChannel = computed(
  () => CHANNEL_KINDS.find((c) => c.kind === installKind.value) ?? CHANNEL_KINDS[0],
);

// UNIT-8: verification state is derived from the SAME field UNIT-4 made
// honest server-side — Bundle.tier, set from lockfile.LockedBundle.Verified
// (a real recorded VerifyManifestSignatures result), never from
// Bundle.signature's mere presence (a locator string an old, pre-UNIT-4
// lockfile row can carry with Verified defaulting false — AC-008). A row
// whose signature was verified can ever produce tier "signed" — a
// "verification failed" state cannot appear in this list at all, because
// AC-006 refuses the install outright and writes no lockfile row for it.
function isVerified(b: Bundle): boolean {
  return b.tier === 'signed' || b.tier.startsWith('signed ');
}

// ── trust anchors (UNIT-3's writer, UNIT-8's panel) ──────────────────────
const anchors = ref<readonly TrustAnchor[]>([]);
const anchorsLoading = ref(false);
const anchorsError = ref<string | null>(null);

const anchorId = ref('');
const anchorPeerId = ref('');
const anchorKeyB64 = ref('');
const anchorBusy = ref(false);
const anchorError = ref<string | null>(null);

async function refreshAnchors() {
  anchorsLoading.value = true;
  anchorsError.value = null;
  try {
    anchors.value = await client.trustAnchors.list();
  } catch (e) {
    anchorsError.value = e instanceof Error ? e.message : 'Failed to load trust anchors.';
    anchors.value = [];
  } finally {
    anchorsLoading.value = false;
  }
}

async function installAnchor() {
  anchorError.value = null;
  const id = anchorId.value.trim();
  const keyB64 = anchorKeyB64.value.trim();
  if (!id || !keyB64) {
    anchorError.value = 'Anchor ID and public key (base64) are required.';
    return;
  }
  anchorBusy.value = true;
  try {
    await client.trustAnchors.install({
      anchorId: id,
      peerId: anchorPeerId.value.trim() || undefined,
      keyB64,
    });
    anchorId.value = '';
    anchorPeerId.value = '';
    anchorKeyB64.value = '';
    await refreshAnchors();
  } catch (e) {
    anchorError.value = e instanceof Error ? e.message : 'Install anchor failed.';
  } finally {
    anchorBusy.value = false;
  }
}

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
  const locator = installLocator.value.trim();
  if (!locator) {
    installError.value = `${activeChannel.value.field === 'url' ? 'URL' : 'Path'} is required.`;
    return;
  }
  installBusy.value = true;
  try {
    const req =
      activeChannel.value.field === 'url'
        ? { kind: installKind.value, url: locator }
        : { kind: installKind.value, path: locator };
    await client.bundle.install(req);
    installLocator.value = '';
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
  void refreshAnchors();
});
</script>

<template>
  <SettingsShell
    number="03"
    section="BUNDLES"
    title="Installed bundles"
    subtitle="Every bundle pinned in kenaz.lock with its source channel, verification state, and artifact count. Bytes live in the local CAS — bundle bytes never leave the device (fleet config-apply ACKs and opted-in telemetry are the only egress when fleet config distribution is active)."
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
          <th class="text-left px-4 py-2 font-medium">Verification</th>
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
              <!--
                UNIT-8 (spec §5.4/FR-006, G-2 at the frontend layer):
                derived from Bundle.tier — a REAL recorded
                VerifyManifestSignatures result (UNIT-4) — never from
                Bundle.signature's mere presence. A pre-UNIT-4 lockfile
                row can carry a non-empty signature locator with
                Verified defaulting false (AC-008); it must render as
                "Unsigned" here, not "Signed".
              -->
              <span
                class="text-[11px] uppercase tracking-[0.12em]"
                :class="isVerified(b) ? 'text-signal-ok' : 'text-ink-subtle'"
                :data-testid="`bundle-verification-${b.id}`"
              >
                {{ isVerified(b) ? 'Signed' : 'Unsigned' }}
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
      <div class="text-ink mb-2">Install a bundle</div>
      <p class="text-ink-muted mb-3 max-w-prose">
        Every channel below fetches every declared artifact into the
        local content-addressed cache and verifies each one against its
        content hash before the bundle is registered; a mismatch, an
        unreachable channel, or a rejected signature refuses the install
        with nothing left behind.
      </p>
      <div class="flex items-center gap-2 mb-2">
        <label class="text-ink-muted" for="bundle-install-kind">Channel</label>
        <select
          id="bundle-install-kind"
          v-model="installKind"
          class="px-2 py-1 font-mono text-[12px] bg-surface-1 text-ink border border-border-muted rounded"
          :disabled="installBusy"
          data-testid="bundle-install-kind"
        >
          <option v-for="c in CHANNEL_KINDS" :key="c.kind" :value="c.kind">
            {{ c.label }}
          </option>
        </select>
      </div>
      <div class="flex items-center gap-2">
        <input
          v-model="installLocator"
          type="text"
          :aria-label="activeChannel.field === 'url' ? 'Bundle mirror URL' : 'Bundle installation path'"
          :placeholder="
            activeChannel.field === 'url'
              ? 'https://mirror.example.com/bundles/my-bundle'
              : '/absolute/path/to/bundle'
          "
          class="flex-1 px-2 py-1 font-mono text-[12px] bg-surface-1 text-ink border border-border-muted rounded"
          :disabled="installBusy"
          data-testid="bundle-install-locator"
        />
        <button
          type="submit"
          class="px-3 py-1 text-[11px] bg-accent text-ink hover:bg-accent-muted disabled:opacity-50 rounded"
          :disabled="installBusy || !installLocator.trim()"
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

    <!-- UNIT-8: trust anchor management. Without this panel an install
         refused for "anchor_missing" (UNIT-4) has no user remedy — see
         core/rpc/views/trustanchor. -->
    <div class="mt-6 px-6 py-4 border-t border-border-muted font-ui text-[12px]">
      <div class="text-ink mb-2">Trust anchors</div>
      <p class="text-ink-muted mb-3 max-w-prose">
        A signed bundle verifies only against an installed anchor's
        public key. Anchors persist across restarts.
      </p>
      <div v-if="anchorsLoading" class="text-ink-muted">Loading anchors…</div>
      <div v-else-if="anchorsError" class="text-signal-danger" role="alert">
        {{ anchorsError }}
      </div>
      <div v-else-if="anchors.length === 0" class="text-ink-subtle mb-3">
        No trust anchors installed.
      </div>
      <table v-else class="w-full mb-3" data-testid="anchors-table">
        <thead class="bg-surface-1 text-ink-muted">
          <tr>
            <th class="text-left px-2 py-1 font-medium">Anchor ID</th>
            <th class="text-left px-2 py-1 font-medium">Kind</th>
            <th class="text-left px-2 py-1 font-medium">Algorithm</th>
            <th class="text-left px-2 py-1 font-medium">Fingerprint</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="a in anchors"
            :key="a.anchorId"
            class="border-t border-border-muted"
            :data-testid="`anchor-row-${a.anchorId}`"
          >
            <td class="px-2 py-1 font-mono">{{ a.anchorId }}</td>
            <td class="px-2 py-1 text-ink-muted">{{ a.kind }}</td>
            <td class="px-2 py-1 text-ink-muted">{{ a.algorithm }}</td>
            <td class="px-2 py-1 text-ink-subtle font-mono">
              {{ a.publicKey.fingerprint.slice(0, 19) }}…
            </td>
          </tr>
        </tbody>
      </table>
      <form
        class="flex flex-wrap items-center gap-2"
        data-testid="anchor-install-form"
        @submit.prevent="installAnchor"
      >
        <input
          v-model="anchorId"
          type="text"
          aria-label="Anchor ID"
          placeholder="anchor id"
          class="px-2 py-1 font-mono text-[12px] bg-surface-1 text-ink border border-border-muted rounded"
          :disabled="anchorBusy"
          data-testid="anchor-install-id"
        />
        <input
          v-model="anchorPeerId"
          type="text"
          aria-label="Peer ID (optional)"
          placeholder="peer id (optional)"
          class="px-2 py-1 font-mono text-[12px] bg-surface-1 text-ink border border-border-muted rounded"
          :disabled="anchorBusy"
          data-testid="anchor-install-peer"
        />
        <input
          v-model="anchorKeyB64"
          type="text"
          aria-label="Public key (base64)"
          placeholder="public key, base64"
          class="flex-1 min-w-[16ch] px-2 py-1 font-mono text-[12px] bg-surface-1 text-ink border border-border-muted rounded"
          :disabled="anchorBusy"
          data-testid="anchor-install-key"
        />
        <button
          type="submit"
          class="px-3 py-1 text-[11px] bg-accent text-ink hover:bg-accent-muted disabled:opacity-50 rounded"
          :disabled="anchorBusy || !anchorId.trim() || !anchorKeyB64.trim()"
          data-testid="anchor-install-submit"
        >
          {{ anchorBusy ? 'Installing…' : 'Install anchor' }}
        </button>
      </form>
      <div
        v-if="anchorError"
        class="mt-2 text-[11px] text-signal-danger"
        role="alert"
        data-testid="anchor-install-error"
      >
        {{ anchorError }}
      </div>
    </div>
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
