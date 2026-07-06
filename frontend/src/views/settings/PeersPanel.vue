<script setup lang="ts">
/**
 * PeersPanel — Settings panel for ACP peer management.
 *
 * Exposes:
 *   - Peer list: all trusted peers with trust tier, transport, last seen.
 *   - Trust a peer: paste an agent card JSON blob to register a new peer.
 *   - Revoke a peer: remove trust for an existing peer.
 *   - Inspect last-seen envelope: fetch and display the trace for a peer's
 *     most recent envelope exchange.
 *
 * All calls flow through client.ACP_* (acp-orchestration-integration-01NDFSEX06).
 */
import { onMounted, ref } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type { ACPPeer, ACPEnvelopeTrace } from '@/lib/types';

const client = useHarnessClient();

// ── Peer list ──────────────────────────────────────────────────────────────

const loading = ref(true);
const peers = ref<ACPPeer[]>([]);
const listError = ref<string | null>(null);

async function loadPeers() {
  loading.value = true;
  listError.value = null;
  try {
    peers.value = await client.ACP_ListPeers();
  } catch (e) {
    listError.value = e instanceof Error ? e.message : String(e);
  } finally {
    loading.value = false;
  }
}

onMounted(() => {
  void loadPeers();
});

// ── Trust a peer ───────────────────────────────────────────────────────────

const trustCardBlob = ref('');
const trusting = ref(false);
const trustError = ref<string | null>(null);
const trustSuccess = ref<string | null>(null);

async function trustPeer() {
  if (!trustCardBlob.value.trim()) return;
  trusting.value = true;
  trustError.value = null;
  trustSuccess.value = null;
  try {
    const result = await client.ACP_TrustPeer(trustCardBlob.value.trim());
    trustSuccess.value = `Peer "${result.peerId}" trusted at tier "${result.trustTier}".`;
    trustCardBlob.value = '';
    await loadPeers();
    setTimeout(() => { trustSuccess.value = null; }, 4000);
  } catch (e) {
    trustError.value = e instanceof Error ? e.message : String(e);
  } finally {
    trusting.value = false;
  }
}

// ── Revoke a peer ──────────────────────────────────────────────────────────

const revokingId = ref<string | null>(null);
const revokeError = ref<string | null>(null);

async function revokePeer(peerID: string) {
  revokingId.value = peerID;
  revokeError.value = null;
  try {
    await client.ACP_RevokePeer(peerID);
    await loadPeers();
  } catch (e) {
    revokeError.value = e instanceof Error ? e.message : String(e);
  } finally {
    revokingId.value = null;
  }
}

// ── Inspect last-seen envelope trace ──────────────────────────────────────

const inspectingPeerID = ref<string | null>(null);
const traceMap = ref<Map<string, ACPEnvelopeTrace>>(new Map());
const traceError = ref<string | null>(null);

async function inspectTrace(peer: ACPPeer) {
  // The last-seen envelope ID comes from a per-peer last_envelope_id field;
  // since Peer doesn't carry that, we use ACP_GetTrace with the peer's ID
  // as a lookup hint. The backend resolves the most-recent trace for the peer.
  inspectingPeerID.value = peer.id;
  traceError.value = null;
  try {
    const trace = await client.ACP_GetTrace(peer.id);
    traceMap.value = new Map(traceMap.value).set(peer.id, trace);
  } catch (e) {
    traceError.value = e instanceof Error ? e.message : String(e);
  } finally {
    inspectingPeerID.value = null;
  }
}

function clearTrace(peerID: string) {
  const next = new Map(traceMap.value);
  next.delete(peerID);
  traceMap.value = next;
}

// ── Formatting helpers ─────────────────────────────────────────────────────

function formatLastSeen(iso?: string): string {
  if (!iso) return 'never';
  try {
    return new Intl.DateTimeFormat(undefined, {
      dateStyle: 'short',
      timeStyle: 'short',
    }).format(new Date(iso));
  } catch {
    return iso;
  }
}

function tierBadgeClass(tier: string): string {
  switch (tier) {
    case 'full':    return 'bg-signal-success/20 text-signal-success';
    case 'limited': return 'bg-signal-warn/20 text-signal-warn';
    default:        return 'bg-surface-2 text-ink-muted';
  }
}

function outcomeBadgeClass(outcome: string): string {
  switch (outcome) {
    case 'ok':                 return 'text-signal-success';
    case 'denied_by_policy':   return 'text-signal-danger';
    case 'transport_error':    return 'text-signal-warn';
    case 'verify_rejected':    return 'text-signal-warn';
    default:                   return 'text-ink-muted';
  }
}
</script>

<template>
  <section
    class="px-6 py-4 grid gap-6 max-w-3xl"
    data-testid="peers-panel"
  >
    <!-- Header description -->
    <p class="font-ui text-[11px] text-ink-dim">
      Manage trusted agent peers reachable via ACP (Agent Communication
      Protocol). Peers are registered by pasting their signed agent card.
      Each peer is assigned a trust tier that gates Cedar policy decisions
      on dispatch.
    </p>

    <!-- ── Trust a peer ─────────────────────────────────────────────────── -->
    <section data-testid="trust-peer-section">
      <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
        Trust a new peer
      </h2>
      <p class="mt-1 font-ui text-[11px] text-ink-dim">
        Paste the agent card JSON blob supplied by the remote peer to
        register it. The card is verified and its fingerprint stored.
      </p>

      <form
        class="mt-3 grid gap-3"
        data-testid="trust-peer-form"
        @submit.prevent="trustPeer"
      >
        <div class="grid gap-1">
          <label
            for="trust-card-blob"
            class="font-ui text-[11px] text-ink-muted"
          >
            Agent card JSON
          </label>
          <textarea
            id="trust-card-blob"
            v-model="trustCardBlob"
            rows="4"
            class="w-full rounded-sm border border-border bg-surface-1 px-2 py-1.5 font-mono text-[11px] text-ink focus:outline-none focus:ring-1 focus:ring-accent resize-y"
            placeholder='{"id":"…","name":"…","public_key":"…"}'
            data-testid="trust-card-blob-input"
          />
        </div>

        <!-- Error -->
        <div
          v-if="trustError"
          role="alert"
          class="rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
          data-testid="trust-peer-error"
        >
          {{ trustError }}
        </div>

        <!-- Success -->
        <div
          v-if="trustSuccess"
          role="status"
          class="rounded-sm border border-signal-success bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-success"
          data-testid="trust-peer-success"
        >
          {{ trustSuccess }}
        </div>

        <div>
          <button
            type="submit"
            :disabled="trusting || !trustCardBlob.trim()"
            class="rounded-sm border border-border bg-surface-2 px-3 py-1.5 font-ui text-[12px] text-ink hover:bg-surface-3 disabled:opacity-50 transition-colors"
            data-testid="trust-peer-submit"
          >
            {{ trusting ? 'Registering…' : 'Trust peer' }}
          </button>
        </div>
      </form>
    </section>

    <!-- ── Peer list ───────────────────────────────────────────────────── -->
    <section data-testid="peer-list-section">
      <div class="flex items-center gap-3 mb-2">
        <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          Trusted peers
        </h2>
        <button
          type="button"
          class="ml-auto font-ui text-[11px] text-ink-muted hover:text-ink transition-colors"
          data-testid="peers-refresh"
          @click="loadPeers"
        >
          Refresh
        </button>
      </div>

      <!-- Loading -->
      <div
        v-if="loading"
        class="font-ui text-[12px] text-ink-muted"
        data-testid="peers-loading"
      >
        Loading peers…
      </div>

      <!-- List error -->
      <div
        v-else-if="listError"
        role="alert"
        class="rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
        data-testid="peers-list-error"
      >
        {{ listError }}
      </div>

      <!-- Empty state -->
      <div
        v-else-if="peers.length === 0"
        class="font-ui text-[12px] text-ink-dim"
        data-testid="peers-empty"
      >
        No trusted peers. Paste an agent card above to register one.
      </div>

      <!-- Peer rows -->
      <ul
        v-else
        class="grid gap-2"
        data-testid="peers-list"
      >
        <li
          v-for="peer in peers"
          :key="peer.id"
          class="rounded-sm border border-border bg-surface-1 px-3 py-3 grid gap-2"
          :data-testid="`peer-row-${peer.id}`"
        >
          <!-- Row header -->
          <div class="flex items-start gap-3">
            <div class="flex-1 min-w-0">
              <p class="font-ui text-[12px] font-medium text-ink truncate">
                {{ peer.agentName || peer.id }}
              </p>
              <p
                v-if="peer.agentName"
                class="font-mono text-[10px] text-ink-muted truncate"
              >
                {{ peer.id }}
              </p>
              <p
                v-if="peer.agentDescription"
                class="mt-0.5 font-ui text-[11px] text-ink-dim"
              >
                {{ peer.agentDescription }}
              </p>
            </div>

            <!-- Trust tier badge -->
            <span
              class="shrink-0 rounded px-1.5 py-0.5 font-ui text-[10px] uppercase tracking-wider"
              :class="tierBadgeClass(peer.trustTier)"
              :data-testid="`peer-tier-${peer.id}`"
            >
              {{ peer.trustTier }}
            </span>
          </div>

          <!-- Details row -->
          <dl class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 font-ui text-[11px]">
            <dt class="text-ink-muted">Transport</dt>
            <dd class="font-mono text-ink truncate">{{ peer.transport }}</dd>
            <dt class="text-ink-muted">Endpoint</dt>
            <dd class="font-mono text-ink truncate">{{ peer.endpointUrl || '—' }}</dd>
            <dt class="text-ink-muted">Last seen</dt>
            <dd class="text-ink">{{ formatLastSeen(peer.lastSeen) }}</dd>
            <dt class="text-ink-muted">Fingerprint</dt>
            <dd class="font-mono text-[10px] text-ink-muted truncate">{{ peer.cardFingerprint }}</dd>
          </dl>

          <!-- Revoke error -->
          <div
            v-if="revokeError && revokingId === peer.id"
            role="alert"
            class="rounded-sm border border-signal-danger bg-surface-1 px-2 py-1 font-ui text-[11px] text-signal-danger"
            :data-testid="`peer-revoke-error-${peer.id}`"
          >
            {{ revokeError }}
          </div>

          <!-- Envelope trace (when loaded) -->
          <div
            v-if="traceMap.has(peer.id)"
            class="rounded-sm border border-border-muted bg-surface-0 px-2 py-2 grid gap-1"
            :data-testid="`peer-trace-${peer.id}`"
          >
            <div class="flex items-center gap-2">
              <span class="font-ui text-[10px] uppercase tracking-wider text-ink-subtle">Last envelope</span>
              <button
                type="button"
                class="ml-auto font-ui text-[10px] text-ink-muted hover:text-ink"
                @click="clearTrace(peer.id)"
              >
                Close
              </button>
            </div>
            <dl class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 font-ui text-[11px]">
              <dt class="text-ink-muted">Envelope ID</dt>
              <dd class="font-mono text-[10px] text-ink truncate">{{ traceMap.get(peer.id)!.envelopeId }}</dd>
              <dt class="text-ink-muted">Direction</dt>
              <dd class="text-ink">{{ traceMap.get(peer.id)!.direction }}</dd>
              <dt class="text-ink-muted">Outcome</dt>
              <dd :class="outcomeBadgeClass(traceMap.get(peer.id)!.outcome)">
                {{ traceMap.get(peer.id)!.outcome }}
              </dd>
              <dt class="text-ink-muted">Transport</dt>
              <dd class="font-mono text-ink">{{ traceMap.get(peer.id)!.transport }}</dd>
              <dt class="text-ink-muted">Bytes in</dt>
              <dd class="text-ink">{{ traceMap.get(peer.id)!.bytesIn }}</dd>
              <dt class="text-ink-muted">Bytes out</dt>
              <dd class="text-ink">{{ traceMap.get(peer.id)!.bytesOut }}</dd>
              <template v-if="traceMap.get(peer.id)!.errorCode">
                <dt class="text-ink-muted">Error</dt>
                <dd class="text-signal-danger font-mono text-[10px]">{{ traceMap.get(peer.id)!.errorCode }}</dd>
              </template>
              <dt class="text-ink-muted">Created at</dt>
              <dd class="text-ink">{{ formatLastSeen(traceMap.get(peer.id)!.createdAt) }}</dd>
            </dl>
          </div>

          <!-- Trace error -->
          <div
            v-if="traceError && inspectingPeerID === peer.id"
            role="alert"
            class="rounded-sm border border-signal-warn bg-surface-1 px-2 py-1 font-ui text-[11px] text-signal-warn"
            :data-testid="`peer-trace-error-${peer.id}`"
          >
            {{ traceError }}
          </div>

          <!-- Actions -->
          <div class="flex items-center gap-2 pt-1">
            <button
              type="button"
              class="font-ui text-[11px] rounded-sm border border-border bg-surface-1 px-2 py-1 text-ink-muted hover:text-ink hover:bg-surface-2 transition-colors disabled:opacity-50"
              :disabled="inspectingPeerID === peer.id"
              :data-testid="`peer-inspect-${peer.id}`"
              @click="inspectTrace(peer)"
            >
              {{ inspectingPeerID === peer.id ? 'Loading…' : 'Inspect last envelope' }}
            </button>
            <button
              type="button"
              class="ml-auto font-ui text-[11px] rounded-sm border border-signal-danger/40 bg-surface-1 px-2 py-1 text-signal-danger hover:bg-signal-danger/10 transition-colors disabled:opacity-50"
              :disabled="revokingId === peer.id"
              :data-testid="`peer-revoke-${peer.id}`"
              @click="revokePeer(peer.id)"
            >
              {{ revokingId === peer.id ? 'Revoking…' : 'Revoke' }}
            </button>
          </div>
        </li>
      </ul>
    </section>
  </section>
</template>
