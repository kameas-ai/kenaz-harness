<script setup lang="ts">
import { computed, ref, onMounted } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import RailEntry from './RailEntry.vue';
import {
  Plus,
  MessageSquare,
  Wrench,
  Layers,
  Server,
  FileText,
  Settings,
  Trash2,
  X,
} from './icons';
import { useSessions } from '@/lib/useHarnessAPI';

/**
 * LeftRail — three vertical regions (plan §2.1):
 *   1. New-session affordance (top)
 *   2. Sessions list (middle, scrollable, hover-to-delete per row)
 *   3. Primary-surfaces nav (bottom)
 *
 * Collapses to icons-only at < 960px (--breakpoint-two-col, FR-016).
 */
const sessions = useSessions();
const newSessionLoading = ref(false);
const deletingId = ref<string | null>(null);
const route = useRoute();
const router = useRouter();

const activeSessionId = computed(() => {
  const h = route.hash || '';
  return h.startsWith('#') ? h.slice(1) : '';
});

onMounted(() => {
  sessions.refresh();
});

async function newSession() {
  if (newSessionLoading.value) return;
  newSessionLoading.value = true;
  try {
    const s = await sessions.create('New session');
    await router.push(`/sessions#${s.id}`);
  } finally {
    newSessionLoading.value = false;
  }
}

async function deleteSession(id: string, event: Event) {
  event.preventDefault();
  event.stopPropagation();
  if (deletingId.value) return;
  deletingId.value = id;
  try {
    await sessions.remove(id);
    if (activeSessionId.value === id) {
      await router.push('/sessions');
    }
  } finally {
    deletingId.value = null;
  }
}

async function clearAll() {
  if (sessions.list.value.length === 0) return;
  if (
    !window.confirm(
      `Delete all ${sessions.list.value.length} sessions? This cannot be undone.`,
    )
  ) {
    return;
  }
  for (const s of [...sessions.list.value]) {
    await sessions.remove(s.id);
  }
  if (activeSessionId.value) {
    await router.push('/sessions');
  }
}
</script>

<template>
  <div class="flex flex-col h-full">
    <!-- new-session affordance -->
    <div class="px-2 pt-3 pb-2">
      <button
        type="button"
        class="flex items-center gap-2 px-3 py-2 rounded-sm w-full text-left text-sm font-ui text-accent border border-accent-hairline hover:bg-accent-glow transition-fast ease-kenaz disabled:opacity-50"
        :disabled="newSessionLoading"
        aria-label="New session"
        @click="newSession"
      >
        <Plus :size="14" />
        <span class="hidden two-col:inline">New session</span>
      </button>
    </div>

    <!-- sessions list -->
    <nav
      class="flex-1 overflow-y-auto px-2 py-1"
      aria-label="Sessions"
    >
      <ul class="space-y-1">
        <li
          v-for="session in sessions.list"
          :key="session.id"
          class="group relative"
        >
          <router-link
            :to="`/sessions#${session.id}`"
            class="flex items-center gap-2 pl-3 pr-8 py-2 rounded-sm w-full text-left text-sm font-ui transition-fast ease-kenaz"
            :class="
              activeSessionId === session.id
                ? 'text-ink bg-surface-2 ring-1 ring-accent-hairline'
                : 'text-ink-muted hover:text-ink hover:bg-surface-2'
            "
            :aria-current="activeSessionId === session.id ? 'page' : undefined"
          >
            <MessageSquare
              :size="14"
              :class="activeSessionId === session.id ? 'text-accent' : ''"
            />
            <span class="truncate hidden two-col:inline">
              {{ session.name }}
            </span>
          </router-link>
          <button
            type="button"
            class="absolute right-1 top-1/2 -translate-y-1/2 p-1 rounded-sm text-ink-dim hover:text-signal-danger hover:bg-surface-3 focus:outline-none focus:ring-1 focus:ring-accent disabled:opacity-50"
            :aria-label="`Delete session ${session.name}`"
            :disabled="deletingId === session.id"
            :data-testid="`delete-session-${session.id}`"
            @click="deleteSession(session.id, $event)"
          >
            <X :size="14" />
          </button>
        </li>
        <li v-if="sessions.list.length === 0" class="px-3 py-2 text-xs text-ink-subtle">
          No sessions yet
        </li>
      </ul>
      <div v-if="sessions.list.length > 0" class="mt-2 px-2">
        <button
          type="button"
          class="flex items-center gap-1.5 px-2 py-1 rounded-sm text-[11px] uppercase tracking-[0.16em] text-ink-dim hover:text-signal-danger transition-fast"
          :title="`Delete all ${sessions.list.length} sessions`"
          :data-testid="'clear-all-sessions'"
          @click="clearAll"
        >
          <Trash2 :size="12" />
          <span class="hidden two-col:inline">
            Clear all ({{ sessions.list.length }})
          </span>
        </button>
      </div>
    </nav>

    <!-- primary-surfaces nav -->
    <nav class="px-2 py-2 border-t border-border-muted" aria-label="Surfaces">
      <ul class="space-y-1">
        <li><RailEntry :icon="MessageSquare" label="Sessions" to="/sessions" /></li>
        <li><RailEntry :icon="Wrench" label="Tools" to="/tools" /></li>
        <li><RailEntry :icon="Layers" label="Bundles" to="/bundles" /></li>
        <li><RailEntry :icon="Server" label="Providers" to="/providers" /></li>
        <li><RailEntry :icon="FileText" label="Audit log" to="/audit" /></li>
        <li><RailEntry :icon="Settings" label="Settings" to="/settings" /></li>
      </ul>
    </nav>
  </div>
</template>
