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

import { computed, ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import CanvasHead from '@/shell/CanvasHead.vue';
import MessageList from '@/components/chat/MessageList.vue';
import ChatInput from '@/components/chat/ChatInput.vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { useSession } from '@/lib/useSession';
import type { Provider } from '@/lib/types';

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
void (async () => {
  try {
    providers.value = await client.llm.listProviders();
  } catch {
    providers.value = [];
  } finally {
    providersLoaded.value = true;
  }
})();

const activeProvider = computed<Provider | null>(() => {
  if (providers.value.length === 0) return null;
  return providers.value[0];
});

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

async function onSend(content: string) {
  if (!hasSession.value || !activeProvider.value) return;
  await session.send(content, activeProvider.value.id);
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
          />
        </div>
        <ChatInput
          v-model="session.draft.value"
          :streaming="isStreaming"
          :disabled="!activeProvider || session.loading.value"
          :estimate="{ tokens: 0, usd: 0 }"
          @send="onSend"
          @cancel="onCancel"
        />
      </template>
    </div>
  </div>
</template>
