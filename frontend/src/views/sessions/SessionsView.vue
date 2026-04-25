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

import { computed, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import CanvasHead from '@/shell/CanvasHead.vue';
import MessageList from '@/components/chat/MessageList.vue';
import ChatInput from '@/components/chat/ChatInput.vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { useSession } from '@/lib/useSession';
import type { Provider } from '@/lib/types';
import { flattenChoices, inferFamily } from '@/lib/modelFamily';

const route = useRoute();
const router = useRouter();
const client = useHarnessClient();

// Session id is the route's :id param (e.g. /sessions/sess-abc).
const sessionId = computed(() => {
  const p = route.params.id;
  return typeof p === 'string' ? p : '';
});

const hasSession = computed(() => sessionId.value.length > 0);

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

const sessionTitle = computed(() => session.session.value?.name ?? 'Sessions');
const sessionSubtitle = computed(() => {
  if (!hasSession.value) {
    return 'Pick a session from the left rail or click "New session" to start one.';
  }
  if (session.loading.value) return 'Loading…';
  if (session.error.value) return `Error: ${session.error.value}`;
  return 'Each session preserves its scroll position and draft input.';
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
        v-if="providersLoaded && providers.length === 0 && hasSession"
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
        v-else-if="activeProviderUnsupported && hasSession"
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
        v-if="hasSession && activeProvider && allChoices.length > 0"
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

      <!-- empty session state -->
      <div
        v-if="!hasSession"
        class="flex-1 grid place-items-center px-6 py-4 font-ui text-sm text-ink-muted"
      >
        No session selected. Click "New session" in the rail to start one.
      </div>

      <!-- live chat surface -->
      <template v-else>
        <div
          v-if="session.streamingTimedOut.value"
          class="px-4 py-2 border-b border-signal-warn bg-surface-1 font-ui text-[12px] text-signal-warn"
          role="status"
        >
          waiting for stream… (no chunks received in 30s)
        </div>
        <div class="flex-1 min-h-0">
          <MessageList
            :messages="session.messages.value"
            :streaming-message="session.currentlyStreaming.value"
            :waiting="isWaitingForFirstChunk"
            :error-message="session.error.value"
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
  </div>
</template>
