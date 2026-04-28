<script setup lang="ts">
/**
 * SessionsView — the chat surface (FR-001b). Renders:
 *   - CanvasHead with the active session name + numbered section header.
 *   - MessageList of historical + currently-streaming messages.
 *   - ChatInput at the bottom.
 *
 * Route shape:
 *   `/sessions`        → empty state ("pick a session in the rail")
 *   `/sessions/<id>`   → load + render that session
 *
 * Uses the chat-ui composables `useSession` + `useShellStatus` and the
 * typed RPC client from `useHarnessClient`. Components never reach into
 * `wailsjs/*` directly — that's the FR-007 isolation rule.
 */

import { computed, onMounted, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import CanvasHead from '@/shell/CanvasHead.vue';
import NewSessionDialog from '@/shell/NewSessionDialog.vue';
import MessageList from '@/components/chat/MessageList.vue';
import ChatInput from '@/components/chat/ChatInput.vue';
import ResolvedContextPanel from '@/views/sessions/ResolvedContextPanel.vue';
import ConfirmToolModal from '@/components/chat/ConfirmToolModal.vue';
import BranchSidebar from '@/components/chat/BranchSidebar.vue';
import CreateBranchModal from '@/components/chat/CreateBranchModal.vue';
import MergeSuggestionToast from '@/components/chat/MergeSuggestionToast.vue';
import ArtifactPreview from '@/views/artifacts/ArtifactPreview.vue';
import { useArtifacts, useHarnessClient, useSessions } from '@/lib/useHarnessAPI';
import { useSession } from '@/lib/useSession';
import type {
  Artifact,
  ArtifactScope,
  ArtifactWithBytes,
  MemoryScopeKind,
  Message,
  Provider,
  SlashExecuteResult,
} from '@/lib/types';
import { flattenChoices, inferFamily } from '@/lib/modelFamily';

const route = useRoute();
const router = useRouter();
const client = useHarnessClient();
const { list: sessionList, refresh: refreshSessions } = useSessions();

// Refresh the rail's session list when SessionsView mounts so the
// empty-state checks against an up-to-date count instead of an
// initially-empty ref.
void refreshSessions();

// Session id is the route's :id param (e.g. /sessions/sess-abc).
const sessionId = computed(() => {
  const p = route.params.id;
  return typeof p === 'string' ? p : '';
});

const hasSession = computed(() => sessionId.value.length > 0);

// Three distinct empty / error states:
//   - 'no-sessions'        no sessions exist anywhere → big welcome
//   - 'no-selection'       sessions exist, none selected → pick from rail
//   - 'session-not-found'  url id has no matching backend row
//   - 'loaded'             session loaded successfully (may have a
//                          send-time error — that's surfaced inline,
//                          not by replacing the surface)
const surfaceState = computed<
  'no-sessions' | 'no-selection' | 'session-not-found' | 'loaded'
>(() => {
  if (!hasSession.value) {
    return sessionList.value.length === 0 ? 'no-sessions' : 'no-selection';
  }
  // session.value goes to null only when load() failed (404 or
  // backend error during sessions.get / listMessages). Send-time
  // errors set session.error but leave session.value populated, so
  // we don't conflate them here.
  if (session.session.value === null && !session.loading.value) {
    return 'session-not-found';
  }
  return 'loaded';
});

const sessionIdRef = computed(() => sessionId.value);
const session = useSession(sessionIdRef);

const providers = ref<readonly Provider[]>([]);
const providersLoaded = ref(false);

async function refreshProviders() {
  try {
    providers.value = await client.llm.listProviders();
  } catch {
    providers.value = [];
  } finally {
    providersLoaded.value = true;
  }
}

// Refresh providers whenever the user lands on / changes sessions —
// vue-router keeps SessionsView mounted across /sessions/<id> param
// changes, so a once-on-setup fetch misses providers that were added
// while we were already on the page.
watch(
  () => route.path,
  () => {
    void refreshProviders();
  },
  { immediate: true },
);

// Also refresh when the window regains focus, in case the user just
// returned from the providers tab.
window.addEventListener('focus', () => {
  void refreshProviders();
});

// Kinds the backend currently has a working stream adapter for.
// Adding others (openai, bedrock, …) is a per-kind mission; until then
// the chat surface picks the first provider whose kind we know works,
// rather than blindly using providers[0] and hitting "no adapter for
// kind X" on send.
const SUPPORTED_KINDS = new Set([
  'anthropic',
  'bedrock',
  'openai',
  'openrouter',
]);

// Active (provider, model) tuple for this session. Model defaults to
// the profile's primary; the switcher pill below lets the user pick
// a different model from the same FAMILY (cross-family transfers
// would mean swapping tokenisers mid-conversation, so we block them).
const activeProviderId = ref<string>('');
const activeModelId = ref<string>('');

const activeProvider = computed<Provider | null>(() => {
  if (providers.value.length === 0) return null;
  // Prefer a provider whose kind has a working stream adapter.
  const supported = providers.value.filter((p) => {
    const kind = p.kind ?? '';
    return SUPPORTED_KINDS.has(kind);
  });
  const pool = supported.length > 0 ? supported : providers.value;
  // Honour an explicitly-selected provider when it exists.
  if (activeProviderId.value) {
    const match = pool.find((p) => p.id === activeProviderId.value);
    if (match) return match;
  }
  return pool[0] ?? null;
});

const activeProviderUnsupported = computed(() => {
  const p = activeProvider.value;
  if (!p) return false;
  return !SUPPORTED_KINDS.has(p.kind ?? '');
});

const activeFamily = computed<string>(() => {
  const p = activeProvider.value;
  if (!p || !activeModelId.value) return '';
  return inferFamily(p.kind, activeModelId.value);
});

// All (provider, model) tuples available across all configured
// providers, with their inferred family. Drives the switcher pill.
const allChoices = computed(() => flattenChoices(providers.value));

// Same family as the active session — these are click-able. Other
// families render disabled with a tooltip.
const familyChoices = computed(() =>
  allChoices.value.filter((c) => c.family === activeFamily.value),
);
const otherFamilyChoices = computed(() =>
  allChoices.value.filter((c) => c.family !== activeFamily.value),
);

// Read the new-session-dialog's localStorage stash for this session,
// if present. NewSessionDialog writes the user's chosen
// (providerId, modelId) under "kaneaz.session.config.<id>" so we
// can honour cross-family choices that the mid-conversation switcher
// would otherwise block.
function readSessionConfig(sessionID: string): {
  providerId: string;
  modelId: string;
} | null {
  if (!sessionID) return null;
  try {
    const raw = window.localStorage.getItem(
      `kaneaz.session.config.${sessionID}`,
    );
    if (!raw) return null;
    const parsed = JSON.parse(raw) as {
      providerId?: string;
      modelId?: string;
    };
    if (parsed.providerId && parsed.modelId) {
      return { providerId: parsed.providerId, modelId: parsed.modelId };
    }
  } catch {
    /* malformed stash — ignore */
  }
  return null;
}

// On session id or provider list change, seed the active selection.
// Priority: stashed dialog config > previously-set value > provider
// primary model.
watch(
  [sessionId, providers],
  ([newSid]) => {
    // Reset prior selection when switching sessions.
    if (newSid) {
      const stashed = readSessionConfig(newSid);
      if (stashed) {
        activeProviderId.value = stashed.providerId;
        activeModelId.value = stashed.modelId;
        return;
      }
    }
    const p = activeProvider.value;
    if (!p) return;
    if (!activeProviderId.value) activeProviderId.value = p.id;
    if (!activeModelId.value) {
      const list = p.models && p.models.length > 0 ? p.models : [p.model];
      activeModelId.value = list[0] || '';
    }
  },
  { immediate: true },
);

const switcherOpen = ref(false);
function toggleSwitcher() {
  switcherOpen.value = !switcherOpen.value;
}
function pickModel(providerId: string, modelId: string) {
  activeProviderId.value = providerId;
  activeModelId.value = modelId;
  switcherOpen.value = false;
}

const sessionTitle = computed(() => {
  switch (surfaceState.value) {
    case 'no-sessions':
      return 'Welcome';
    case 'no-selection':
      return 'Sessions';
    case 'session-not-found':
      return 'Session not found';
    default:
      return session.session.value?.name ?? 'Session';
  }
});
const sessionSubtitle = computed(() => {
  switch (surfaceState.value) {
    case 'no-sessions':
      return 'Configure a provider, then start your first conversation.';
    case 'no-selection':
      return 'Pick a session from the rail or start a new one.';
    case 'session-not-found':
      return 'This session was deleted or its id is wrong.';
    default:
      // Active chat: show no subtitle. The session name in the header
      // is sufficient; chrome below it just wastes vertical space.
      return undefined;
  }
});

// In the active-chat default, drop the breadcrumb (`01 / SESSIONS`)
// chrome too — the session name alone is enough orientation. Empty/
// error states keep the breadcrumb so the user knows where they are.
const showSessionBreadcrumb = computed(
  () => surfaceState.value !== 'loaded',
);

const isStreaming = computed(
  () => session.streamSubscriptionId.value !== null,
);

// "Thinking…" — stream is open but no chunks have arrived yet.
const isWaitingForFirstChunk = computed(
  () =>
    session.streamSubscriptionId.value !== null &&
    session.currentlyStreaming.value === null,
);

// Per-model context window. The connector's /models endpoint knows
// these but they're not surfaced through the Provider type yet, so
// we keep a small substring-matched fallback table here. Anything
// unmatched falls back to 200k (covers the modern Anthropic /
// OpenAI flagships). Move this to a backend-derived prop when the
// model-info plumbing lands.
const MODEL_CONTEXT_FALLBACK = 200_000;
const MODEL_CONTEXT_HINTS: Array<[RegExp, number]> = [
  [/claude-(?:opus|sonnet)-(?:4(?:-?[1-9])?|3-?7)/, 200_000],
  [/claude-haiku/, 200_000],
  [/claude-3(?:-5)?-haiku/, 200_000],
  [/claude-3-(?:opus|sonnet|haiku)/, 200_000],
  [/gpt-5/, 256_000],
  [/gpt-4o|gpt-4-turbo/, 128_000],
  [/gpt-4(?!o)/, 8_192],
  [/o1|o3/, 200_000],
  [/gemini-(?:1\.5|2)/, 1_000_000],
  [/llama-3\.1-405/, 128_000],
];

function modelContextWindow(modelId: string): number {
  if (!modelId) return MODEL_CONTEXT_FALLBACK;
  const id = modelId.toLowerCase();
  for (const [re, n] of MODEL_CONTEXT_HINTS) {
    if (re.test(id)) return n;
  }
  return MODEL_CONTEXT_FALLBACK;
}

// Cheap client-side token estimate. The backend's estimateTokens uses
// a similar chars/4 heuristic so the % we display roughly tracks what
// the kernel sees. Don't trust this to the digit — it's a usage cue,
// not a billing surface.
function estimateMessageTokens(s: string): number {
  if (!s) return 0;
  return Math.ceil(s.length / 4);
}

const conversationTokens = computed(() => {
  let total = 0;
  for (const m of visibleMessages.value) {
    total += estimateMessageTokens(m.content);
  }
  const streaming = session.currentlyStreaming.value;
  if (streaming?.content) total += estimateMessageTokens(streaming.content);
  return total;
});

const contextWindowPct = computed(() => {
  const max = modelContextWindow(activeModelId.value);
  if (max <= 0) return 0;
  return Math.min(100, Math.round((conversationTokens.value / max) * 100));
});

const contextWindowLabel = computed(() => {
  const max = modelContextWindow(activeModelId.value);
  const used = conversationTokens.value;
  // Compact thousands formatter ("1.2k", "12k", "128k").
  const fmt = (n: number) => {
    if (n < 1_000) return String(n);
    if (n < 10_000) return (n / 1_000).toFixed(1).replace(/\.0$/, '') + 'k';
    return Math.round(n / 1_000) + 'k';
  };
  return `${fmt(used)} / ${fmt(max)}`;
});

const contextBarTone = computed<'ok' | 'warn' | 'danger'>(() => {
  const pct = contextWindowPct.value;
  if (pct >= 85) return 'danger';
  if (pct >= 65) return 'warn';
  return 'ok';
});

async function onSend(content: string) {
  if (!hasSession.value || !activeProvider.value) return;
  // When the user staged any multimodal attachments, the
  // sendBlocks event has already fired and routed through
  // session.sendBlocks — skip the legacy text-only path so we don't
  // double-persist the user turn. We also bail when content is empty
  // (e.g. attachments-only sends).
  if (sentBlocksThisTurn.value || content.length === 0) {
    sentBlocksThisTurn.value = false;
    return;
  }
  await session.send(content, activeProvider.value.id, activeModelId.value);
}

const sentBlocksThisTurn = ref(false);

async function onSendBlocks(contentBlocks: import('@/lib/types').ContentBlock[]) {
  if (!hasSession.value || !activeProvider.value) return;
  sentBlocksThisTurn.value = true;
  await session.sendBlocks(
    contentBlocks,
    activeProvider.value.id,
    activeModelId.value,
  );
}

async function onCancel() {
  await session.cancel();
}

// ── slash commands ──────────────────────────────────────────────────
//
// Slash commands ride alongside the regular chat flow. The composer
// emits `slashCommand(raw)` instead of `send` / `sendBlocks` when the
// user submits a `/...` line. We dispatch via the typed RPC, then
// surface the result inline as a synthetic system / assistant-shaped
// Message in the chat. `/model` results carry metadata that updates
// the local active provider+model. `/clear` returns metadata that
// triggers a refresh of the on-disk message list (the divider was
// persisted server-side).

interface SlashTransientMessage {
  id: string;
  role: 'system' | 'assistant';
  content: string;
  createdAt: string;
}

// Per-session transient slash output. Keyed by session id so
// switching sessions doesn't leak previous-session results into the
// new view.
const slashResults = ref<Record<string, SlashTransientMessage[]>>({});

const activeSlashResults = computed<readonly SlashTransientMessage[]>(() => {
  const sid = sessionId.value;
  if (!sid) return [];
  return slashResults.value[sid] ?? [];
});

function appendSlashResult(sid: string, kind: string, text: string) {
  // Map slash result kinds to message-bubble roles. The system role
  // renders centered + italic which fits info / warning / system; the
  // assistant role renders left-aligned which fits errors well
  // enough that the user notices something went wrong without us
  // shipping a new bubble variant in v1.
  const role: 'system' | 'assistant' = kind === 'error' ? 'assistant' : 'system';
  const id = `slash-${sid}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
  const entry: SlashTransientMessage = {
    id,
    role,
    content: text,
    createdAt: new Date().toISOString(),
  };
  const existing = slashResults.value[sid] ?? [];
  slashResults.value = {
    ...slashResults.value,
    [sid]: [...existing, entry],
  };
}

async function onSlashCommand(raw: string) {
  const sid = sessionId.value;
  if (!sid) {
    return;
  }
  let result: SlashExecuteResult;
  try {
    result = await client.slash.execute(sid, raw);
  } catch (err) {
    appendSlashResult(
      sid,
      'error',
      err instanceof Error ? err.message : String(err),
    );
    return;
  }
  appendSlashResult(sid, result.kind, result.text);

  // /model — apply provider+model metadata to the local active state
  // so the next regular message dispatches to the chosen tuple.
  const modelId = result.metadata?.['modelId'];
  const providerId = result.metadata?.['providerId'];
  if (typeof modelId === 'string' && typeof providerId === 'string' && modelId !== '' && providerId !== '') {
    pickModel(providerId, modelId);
  }

  // /clear — the backend appended a system divider; refresh the
  // message list so the divider shows up in the transcript.
  if (raw.trim().startsWith('/clear')) {
    void refreshActiveMessages();
  }
}

async function refreshActiveMessages() {
  const sid = sessionId.value;
  if (!sid) return;
  try {
    await session.refresh();
  } catch {
    /* swallow — the next user turn will re-fetch anyway. */
  }
}

// Combined view: persisted messages + transient slash results,
// ordered by createdAt so a slash result lands in the right spot.
const visibleMessages = computed<readonly Message[]>(() => {
  const persisted = session.messages.value;
  const transient = activeSlashResults.value;
  if (transient.length === 0) return persisted;
  // Transient messages are not real Message objects; map them up.
  const sid = sessionId.value;
  const synthetic: Message[] = transient.map((t) => ({
    id: t.id,
    sessionId: sid,
    role: t.role,
    content: t.content,
    createdAt: t.createdAt,
  }));
  // Append after persisted; v1 doesn't reorder by createdAt — the
  // user just submitted the slash command, so it's freshest.
  return [...persisted, ...synthetic];
});

function gotoProviders() {
  void router.push('/providers');
}

const newSessionDialogOpen = ref(false);
function openNewSession() {
  newSessionDialogOpen.value = true;
}
async function onNewSessionDialogClose() {
  newSessionDialogOpen.value = false;
  await refreshSessions();
}

// ── branches sidebar (agent-kernel-graph; Bundle B WP09) ──────────────
const createBranchModalOpen = ref(false);
function openCreateBranchModal() {
  createBranchModalOpen.value = true;
}
function closeCreateBranchModal() {
  createBranchModalOpen.value = false;
}
function onBranchOpen(childSessionId: string) {
  if (!childSessionId) return;
  void router.push(`/sessions/${childSessionId}`);
}

const hasAnyProvider = computed(() => providers.value.length > 0);

// Long-term-memory opt-in. Off by default (privacy posture); read once
// on mount and again when the window regains focus so toggling it in
// settings takes effect on the next chat.
const memoryEnabled = ref(false);
const lastRememberError = ref<string | null>(null);

async function refreshMemoryFlag() {
  try {
    memoryEnabled.value = await client.settings.getMemory();
  } catch {
    memoryEnabled.value = false;
  }
}

onMounted(() => {
  void refreshMemoryFlag();
});
window.addEventListener('focus', () => {
  void refreshMemoryFlag();
});

async function onRemember(m: Message, scope: MemoryScopeKind = 'session') {
  if (!sessionId.value || !m.id) return;
  lastRememberError.value = null;
  try {
    await client.memory.rememberMessage(sessionId.value, m.id, scope);
  } catch (err) {
    lastRememberError.value =
      err instanceof Error ? err.message : String(err);
  }
}

// ── Artifacts (artifacts-storage WP03) ───────────────────────────────
//
// Session-scoped artifact list — fetched once per session via the
// useArtifacts composable, projected to a per-message map so individual
// MessageBubbles never trigger their own RPC fetches (FR-009 / plan §5).
const sessionArtifacts = useArtifacts({});
const activeTab = ref<'chat' | 'artifacts' | 'context'>('chat');
const artifactPreviewOpen = ref(false);
const artifactPreviewPayload = ref<ArtifactWithBytes | null>(null);
const lastArtifactError = ref<string | null>(null);

function refreshSessionArtifacts() {
  if (!sessionId.value) return;
  void sessionArtifacts.setFilter({ sessionId: sessionId.value });
}

watch(sessionId, () => {
  refreshSessionArtifacts();
}, { immediate: true });

const artifactsByMessage = computed<ReadonlyMap<string, readonly Artifact[]>>(() => {
  const map = new Map<string, Artifact[]>();
  for (const a of sessionArtifacts.list.value) {
    const mid = a.sourceRef.messageId;
    if (!mid) continue;
    const list = map.get(mid) ?? [];
    list.push(a);
    map.set(mid, list);
  }
  return map;
});

function defaultArtifactTitle(content: string): string {
  const flat = content.replace(/\s+/g, ' ').trim();
  return flat.slice(0, 60) || 'pinned message';
}

async function onSaveArtifactFromMessage(m: Message) {
  if (!sessionId.value || !m.id) return;
  const suggested = defaultArtifactTitle(m.content);
  const title =
    typeof window !== 'undefined' && typeof window.prompt === 'function'
      ? window.prompt('Save as artifact — title:', suggested)
      : suggested;
  if (title === null) return;
  const trimmed = title.trim() || suggested;
  lastArtifactError.value = null;
  try {
    await sessionArtifacts.saveFromMessage(
      sessionId.value,
      m.id,
      trimmed,
    );
  } catch (err) {
    lastArtifactError.value = err instanceof Error ? err.message : String(err);
  }
}

async function openArtifactPreview(a: Artifact) {
  lastArtifactError.value = null;
  try {
    artifactPreviewPayload.value = await client.artifacts.get(a.id);
    artifactPreviewOpen.value = true;
  } catch (err) {
    lastArtifactError.value = err instanceof Error ? err.message : String(err);
  }
}

function closeArtifactPreview() {
  artifactPreviewOpen.value = false;
  artifactPreviewPayload.value = null;
}

async function promoteArtifact(
  id: string,
  scopeKind: ArtifactScope,
  scopeId: string,
): Promise<Artifact> {
  const updated = await client.artifacts.promote(id, scopeKind, scopeId);
  refreshSessionArtifacts();
  if (
    artifactPreviewPayload.value &&
    artifactPreviewPayload.value.artifact.id === id
  ) {
    artifactPreviewPayload.value = {
      artifact: updated,
      bytes: artifactPreviewPayload.value.bytes,
    };
  }
  return updated;
}

async function deleteArtifact(id: string): Promise<void> {
  await client.artifacts.remove(id);
  refreshSessionArtifacts();
}

function formatTimestamp(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString();
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const kb = bytes / 1024;
  if (kb < 1024) return kb < 10 ? `${kb.toFixed(1)} KB` : `${Math.round(kb)} KB`;
  const mb = kb / 1024;
  return mb < 10 ? `${mb.toFixed(1)} MB` : `${Math.round(mb)} MB`;
}
</script>

<template>
  <div class="grid h-full" style="grid-template-rows: auto 1fr">
    <!-- header -->
    <div>
      <CanvasHead
        :number="showSessionBreadcrumb ? '01' : undefined"
        :section="showSessionBreadcrumb ? 'SESSIONS' : undefined"
        :title="sessionTitle"
        :subtitle="sessionSubtitle"
      />
    </div>

    <!-- main canvas -->
    <div class="flex flex-col min-h-0">
      <div
        v-if="providersLoaded && providers.length === 0 && surfaceState === 'loaded'"
        class="mx-6 my-3 rounded-md border border-signal-warn bg-surface-1 px-4 py-3 font-ui text-[12px]"
        role="status"
      >
        <div class="text-signal-warn uppercase tracking-[0.18em] text-[11px]">
          No provider configured
        </div>
        <p class="mt-1 text-ink">
          Add an LLM provider to start a conversation.
        </p>
        <button
          type="button"
          class="mt-2 px-3 py-1 rounded-md border border-accent text-accent text-[11px] uppercase tracking-[0.18em] hover:bg-surface-2"
          @click="gotoProviders"
        >
          Configure providers
        </button>
      </div>

      <div
        v-else-if="activeProviderUnsupported && surfaceState === 'loaded'"
        class="mx-6 my-3 rounded-md border border-signal-warn bg-surface-1 px-4 py-3 font-ui text-[12px]"
        role="status"
      >
        <div class="text-signal-warn uppercase tracking-[0.18em] text-[11px]">
          Provider not yet supported
        </div>
        <p class="mt-1 text-ink">
          Streaming for this provider's kind isn't wired in this build.
          Add an Anthropic provider to start chatting today.
        </p>
        <button
          type="button"
          class="mt-2 px-3 py-1 rounded-md border border-accent text-accent text-[11px] uppercase tracking-[0.18em] hover:bg-surface-2"
          @click="gotoProviders"
        >
          Configure providers
        </button>
      </div>

      <!-- ────── State A: no sessions exist anywhere ────── -->
      <div
        v-if="surfaceState === 'no-sessions'"
        class="flex-1 grid place-items-center px-6 py-12"
      >
        <div class="max-w-md text-center">
          <div
            class="mx-auto mb-6 flex h-16 w-16 items-center justify-center rounded-md border border-accent-hairline bg-surface-1 text-accent text-3xl"
            aria-hidden="true"
          >
            +
          </div>
          <h2 class="font-ui text-xl font-semibold text-ink">
            Start your first conversation
          </h2>
          <p class="mt-2 font-ui text-sm text-ink-muted">
            {{
              hasAnyProvider
                ? 'Pick a model and start chatting — sessions live locally and persist across restarts.'
                : 'Configure an LLM provider, then start chatting. Sessions live locally and persist across restarts.'
            }}
          </p>
          <div class="mt-6 flex items-center justify-center gap-2">
            <button
              v-if="!hasAnyProvider"
              type="button"
              class="rounded-md border border-accent text-accent px-4 py-2 text-xs uppercase tracking-[0.18em] font-ui hover:bg-surface-2"
              @click="gotoProviders"
            >
              Configure providers
            </button>
            <button
              v-else
              type="button"
              class="rounded-md border border-accent bg-surface-1 text-accent px-4 py-2 text-xs uppercase tracking-[0.18em] font-ui hover:bg-surface-2"
              :data-testid="'welcome-new-session'"
              @click="openNewSession"
            >
              New session
            </button>
          </div>
        </div>
      </div>

      <!-- ────── State B: sessions exist but none selected ────── -->
      <div
        v-else-if="surfaceState === 'no-selection'"
        class="flex-1 grid place-items-center px-6 py-12"
      >
        <div class="max-w-md text-center">
          <div
            class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
          >
            {{ sessionList.length }}
            {{ sessionList.length === 1 ? 'session' : 'sessions' }} ready
          </div>
          <h2 class="mt-2 font-ui text-lg font-semibold text-ink">
            Pick a session from the rail
          </h2>
          <p class="mt-1 font-ui text-sm text-ink-muted">
            Or start a fresh one — the new-session dialog lets you pick a
            different model up front.
          </p>
          <button
            type="button"
            class="mt-5 rounded-md border border-accent text-accent px-4 py-2 text-xs uppercase tracking-[0.18em] font-ui hover:bg-surface-2"
            @click="openNewSession"
          >
            New session
          </button>
        </div>
      </div>

      <!-- ────── State C: url id has no matching session ────── -->
      <div
        v-else-if="surfaceState === 'session-not-found'"
        class="flex-1 grid place-items-center px-6 py-12"
      >
        <div class="max-w-md text-center">
          <div
            class="font-ui text-[11px] uppercase tracking-[0.18em] text-signal-warn"
          >
            Gone
          </div>
          <h2 class="mt-2 font-ui text-lg font-semibold text-ink">
            This session doesn't exist
          </h2>
          <p class="mt-1 font-ui text-sm text-ink-muted">
            It may have been deleted, or the link is wrong. Pick another
            from the rail or start a new one.
          </p>
          <div class="mt-5 flex items-center justify-center gap-2">
            <button
              v-if="sessionList.length > 0"
              type="button"
              class="rounded-md border border-border-muted text-ink px-4 py-2 text-xs uppercase tracking-[0.18em] font-ui hover:bg-surface-2"
              @click="router.push('/sessions')"
            >
              Back to sessions
            </button>
            <button
              type="button"
              class="rounded-md border border-accent text-accent px-4 py-2 text-xs uppercase tracking-[0.18em] font-ui hover:bg-surface-2"
              @click="openNewSession"
            >
              New session
            </button>
          </div>
        </div>
      </div>

      <!-- ────── State D: live chat surface ────── -->
      <template v-else>
        <div
          v-if="session.streamingTimedOut.value"
          class="px-4 py-2 border-b border-signal-warn bg-surface-1 font-ui text-[12px] text-signal-warn"
          role="status"
        >
          waiting for stream… (no chunks received in 30s)
        </div>
        <div
          v-if="lastRememberError"
          class="mx-4 my-2 rounded-md border border-signal-warn bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-warn"
          role="alert"
        >
          Could not remember message: {{ lastRememberError }}
        </div>
        <div
          class="px-4 pt-2 flex items-center gap-1 border-b border-border-muted"
          data-testid="session-tabs"
        >
          <button
            type="button"
            class="px-3 py-1 rounded-t-sm font-ui text-[11px] uppercase tracking-[0.18em] border-b-2"
            :class="
              activeTab === 'chat'
                ? 'text-accent border-accent'
                : 'text-ink-muted border-transparent hover:text-ink'
            "
            data-testid="session-tab-chat"
            @click="activeTab = 'chat'"
          >
            Chat
          </button>
          <button
            type="button"
            class="px-3 py-1 rounded-t-sm font-ui text-[11px] uppercase tracking-[0.18em] border-b-2 flex items-center gap-1"
            :class="
              activeTab === 'artifacts'
                ? 'text-accent border-accent'
                : 'text-ink-muted border-transparent hover:text-ink'
            "
            data-testid="session-tab-artifacts"
            @click="activeTab = 'artifacts'"
          >
            <span>Artifacts</span>
            <span class="text-ink-dim font-mono text-[10px]">
              ({{ sessionArtifacts.list.value.length }})
            </span>
          </button>
          <button
            type="button"
            class="px-3 py-1 rounded-t-sm font-ui text-[11px] uppercase tracking-[0.18em] border-b-2"
            :class="
              activeTab === 'context'
                ? 'text-accent border-accent'
                : 'text-ink-muted border-transparent hover:text-ink'
            "
            data-testid="session-tab-context"
            @click="activeTab = 'context'"
          >
            Context
          </button>
        </div>
        <div
          v-if="lastArtifactError"
          class="mx-4 my-2 rounded-md border border-signal-warn bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-warn"
          role="alert"
        >
          {{ lastArtifactError }}
        </div>
        <div
          v-if="activeTab === 'chat'"
          class="flex-1 min-h-0 grid grid-cols-[minmax(0,1fr)_auto]"
          style="grid-template-rows: minmax(0, 1fr)"
        >
          <MessageList
            :messages="visibleMessages"
            :streaming-message="session.currentlyStreaming.value"
            :waiting="isWaitingForFirstChunk"
            :error-message="session.error.value"
            :rememberable="memoryEnabled"
            :saveable="true"
            :project-id="session.session.value?.projectId ?? ''"
            :artifacts-by-message="artifactsByMessage"
            @remember="onRemember"
            @save-artifact="onSaveArtifactFromMessage"
            @open-artifact="openArtifactPreview"
          />
          <BranchSidebar
            v-if="hasSession"
            :parent-session-id="sessionId"
            class="hidden lg:flex"
            @open="onBranchOpen"
            @create="openCreateBranchModal"
          />
        </div>
        <div
          v-else-if="activeTab === 'artifacts'"
          class="flex-1 min-h-0 overflow-y-auto px-4 py-3"
          data-testid="session-artifacts-tab"
        >
          <p
            v-if="sessionArtifacts.list.value.length === 0"
            class="font-ui text-xs text-ink-muted"
            data-testid="session-artifacts-empty"
          >
            No artifacts yet. Code blocks with
            <code class="font-mono">title="filename.ext"</code> and tool
            outputs are captured automatically.
          </p>
          <table
            v-else
            class="w-full text-left font-ui text-xs"
            data-testid="session-artifacts-table"
          >
            <thead>
              <tr
                class="text-[10px] uppercase tracking-[0.18em] text-ink-subtle"
              >
                <th class="px-2 py-1.5 font-medium">Source</th>
                <th class="px-2 py-1.5 font-medium">Title</th>
                <th class="px-2 py-1.5 font-medium">Mime</th>
                <th class="px-2 py-1.5 font-medium">Size</th>
                <th class="px-2 py-1.5 font-medium">Created</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="a in sessionArtifacts.list.value"
                :key="a.id"
                class="cursor-pointer hover:bg-surface-2"
                :data-testid="`session-artifacts-row-${a.id}`"
                @click="openArtifactPreview(a)"
              >
                <td class="px-2 py-1.5 text-ink-dim font-mono text-[11px]">
                  {{ a.source }}
                </td>
                <td class="px-2 py-1.5 text-ink truncate max-w-[40ch]">
                  {{ a.title }}
                </td>
                <td class="px-2 py-1.5 text-ink-muted font-mono text-[11px]">
                  {{ a.mimeType }}
                </td>
                <td class="px-2 py-1.5 text-ink-muted font-mono text-[11px]">
                  {{ formatSize(a.byteSize) }}
                </td>
                <td class="px-2 py-1.5 text-ink-dim font-mono text-[11px]">
                  {{ formatTimestamp(a.createdAt) }}
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <div
          v-else-if="activeTab === 'context'"
          class="flex-1 min-h-0 overflow-y-auto"
          data-testid="session-context-tab"
        >
          <ResolvedContextPanel :session-id="sessionId" />
        </div>

        <!-- footer status bar: muted model picker + context window meter -->
        <div
          v-if="activeProvider && allChoices.length > 0"
          class="relative flex items-center justify-between gap-3 border-t border-border-muted bg-surface-0 px-4 py-1.5 font-ui text-[11px] text-ink-dim"
          data-testid="session-status-bar"
        >
          <button
            type="button"
            class="flex items-center gap-1.5 rounded-sm px-1.5 py-0.5 hover:bg-surface-2 hover:text-ink"
            :data-testid="'session-model-switcher'"
            @click="toggleSwitcher"
          >
            <span class="uppercase tracking-[0.14em] text-ink-subtle">
              model
            </span>
            <span class="font-mono text-ink-muted">
              {{ activeModelId || '—' }}
            </span>
            <span aria-hidden="true">▾</span>
          </button>
          <div
            v-if="switcherOpen"
            class="absolute z-30 bottom-full mb-1 left-3 max-h-72 w-80 overflow-y-auto rounded-sm border border-border-muted bg-surface-1 shadow-lg"
            role="menu"
          >
            <div
              class="px-3 py-1.5 text-[10px] uppercase tracking-[0.18em] text-ink-subtle border-b border-border-muted"
            >
              {{
                activeFamily ? `Same family (${activeFamily})` : 'Available'
              }}
            </div>
            <button
              v-for="c in familyChoices"
              :key="`${c.providerId}::${c.modelId}`"
              type="button"
              class="block w-full px-3 py-1.5 text-left text-sm font-ui hover:bg-surface-2"
              :class="
                c.providerId === activeProviderId &&
                c.modelId === activeModelId
                  ? 'text-accent'
                  : 'text-ink'
              "
              @click="pickModel(c.providerId, c.modelId)"
            >
              <div class="font-mono text-xs">{{ c.modelId }}</div>
              <div class="text-[10px] text-ink-dim">
                {{ c.providerName }}
              </div>
            </button>
            <div
              v-if="otherFamilyChoices.length > 0"
              class="px-3 py-1.5 text-[10px] uppercase tracking-[0.18em] text-ink-subtle border-t border-border-muted"
            >
              Other families (cross-family blocked)
            </div>
            <div
              v-for="c in otherFamilyChoices"
              :key="`disabled-${c.providerId}::${c.modelId}`"
              class="block w-full px-3 py-1.5 text-left text-sm font-ui text-ink-dim opacity-60 cursor-not-allowed"
              :title="
                `Different family (${c.family}) — would swap tokenisers ` +
                `mid-conversation. Start a new session to use this model.`
              "
            >
              <div class="font-mono text-xs">{{ c.modelId }}</div>
              <div class="text-[10px]">
                {{ c.providerName }} · {{ c.family }}
              </div>
            </div>
          </div>
          <div
            class="flex items-center gap-2"
            data-testid="session-context-meter"
            :title="`Estimated context use — ${conversationTokens.toLocaleString()} of ${modelContextWindow(activeModelId).toLocaleString()} tokens`"
          >
            <span class="uppercase tracking-[0.14em] text-ink-subtle">
              context
            </span>
            <div class="h-1 w-24 rounded-full bg-surface-2 overflow-hidden">
              <div
                class="h-full transition-[width] duration-300"
                :class="{
                  'bg-signal-ok': contextBarTone === 'ok',
                  'bg-signal-warn': contextBarTone === 'warn',
                  'bg-signal-danger': contextBarTone === 'danger',
                }"
                :style="{ width: contextWindowPct + '%' }"
              ></div>
            </div>
            <span class="font-mono text-ink-muted tabular-nums">
              {{ contextWindowPct }}%
            </span>
            <span class="font-mono text-ink-subtle">
              {{ contextWindowLabel }}
            </span>
          </div>
        </div>
        <ChatInput
          v-model="session.draft.value"
          :streaming="isStreaming"
          :disabled="
            !activeProvider ||
            activeProviderUnsupported ||
            session.loading.value
          "
          :estimate="{ tokens: 0, usd: 0 }"
          :session-id="sessionId"
          :error-banner="session.error.value"
          @send="onSend"
          @send-blocks="onSendBlocks"
          @cancel="onCancel"
          @slash-command="onSlashCommand"
        />
      </template>
    </div>
    <NewSessionDialog
      :open="newSessionDialogOpen"
      @close="onNewSessionDialogClose"
    />
    <ConfirmToolModal />
    <CreateBranchModal
      v-if="hasSession"
      :parent-session-id="sessionId"
      :open="createBranchModalOpen"
      @close="closeCreateBranchModal"
      @created="closeCreateBranchModal"
    />
    <MergeSuggestionToast />
    <ArtifactPreview
      :open="artifactPreviewOpen"
      :payload="artifactPreviewPayload"
      :project-id="session.session.value?.projectId ?? ''"
      :on-promote="promoteArtifact"
      :on-delete="deleteArtifact"
      @close="closeArtifactPreview"
    />
  </div>
</template>
