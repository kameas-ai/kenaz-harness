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
import { useHarnessClient, useSessions } from '@/lib/useHarnessAPI';
import { useSession } from '@/lib/useSession';
import type { MemoryScopeKind, Message, Provider } from '@/lib/types';
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
      if (session.loading.value) return 'Loading…';
      return 'Each session preserves its scroll position and draft input.';
  }
});

const isStreaming = computed(
  () => session.streamSubscriptionId.value !== null,
);

// "Thinking…" — stream is open but no chunks have arrived yet.
const isWaitingForFirstChunk = computed(
  () =>
    session.streamSubscriptionId.value !== null &&
    session.currentlyStreaming.value === null,
);

async function onSend(content: string) {
  if (!hasSession.value || !activeProvider.value) return;
  await session.send(content, activeProvider.value.id, activeModelId.value);
}

async function onCancel() {
  await session.cancel();
}

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
</script>

<template>
  <div class="grid h-full" style="grid-template-rows: auto 1fr">
    <!-- header -->
    <div>
      <CanvasHead
        number="01"
        section="SESSIONS"
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

      <!-- model switcher pill -->
      <div
        v-if="surfaceState === 'loaded' && activeProvider && allChoices.length > 0"
        class="mx-6 mb-2 mt-1 relative"
      >
        <button
          type="button"
          class="flex items-center gap-2 rounded-sm border border-border-muted bg-surface-1 px-3 py-1.5 text-xs font-ui text-ink hover:bg-surface-2"
          :data-testid="'session-model-switcher'"
          @click="toggleSwitcher"
        >
          <span class="text-[10px] uppercase tracking-[0.18em] text-ink-dim">
            Model
          </span>
          <span class="font-mono">{{ activeModelId || '—' }}</span>
          <span class="text-ink-dim">▾</span>
        </button>
        <div
          v-if="switcherOpen"
          class="absolute z-20 mt-1 max-h-72 w-80 overflow-y-auto rounded-sm border border-border-muted bg-surface-1 shadow-lg"
          role="menu"
        >
          <div class="px-3 py-1.5 text-[10px] uppercase tracking-[0.18em] text-ink-subtle border-b border-border-muted">
            {{ activeFamily ? `Same family (${activeFamily})` : 'Available' }}
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
            <div class="text-[10px] text-ink-dim">{{ c.providerName }}</div>
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
            <div class="text-[10px]">{{ c.providerName }} · {{ c.family }}</div>
          </div>
        </div>
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
        <ResolvedContextPanel :session-id="sessionId" />
        <div class="flex-1 min-h-0">
          <MessageList
            :messages="session.messages.value"
            :streaming-message="session.currentlyStreaming.value"
            :waiting="isWaitingForFirstChunk"
            :error-message="session.error.value"
            :rememberable="memoryEnabled"
            :project-id="session.session.value?.projectId ?? ''"
            @remember="onRemember"
          />
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
          @send="onSend"
          @cancel="onCancel"
        />
      </template>
    </div>
    <NewSessionDialog
      :open="newSessionDialogOpen"
      @close="onNewSessionDialogClose"
    />
  </div>
</template>
