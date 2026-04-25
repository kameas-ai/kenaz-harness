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
const lastError = ref<string | null>(null);
const route = useRoute();
const router = useRouter();

const activeSessionId = computed(() => {
  const p = route.params.id;
  return typeof p === 'string' ? p : '';
});

onMounted(() => {
  sessions.refresh();
});

async function newSession() {
  if (newSessionLoading.value) return;
  newSessionLoading.value = true;
  try {
    const s = await sessions.create('New session');
    await router.push(`/sessions/${s.id}`);
  } finally {
    newSessionLoading.value = false;
  }
}

function openSession(session: unknown) {
  const id =
    session && typeof session === 'object' && 'id' in session
      ? String((session as { id?: string }).id)
      : '';
  if (!id || id === 'undefined' || deletingId.value === id) return;
  void router.push(`/sessions/${id}`);
}

async function deleteSession(session: unknown, event: Event) {
  event.preventDefault();
  event.stopPropagation();
  // Diagnostic: dump everything we know about the row so we can see
  // exactly what shape Vue is passing into v-for. Removed once stable.
  const dump = JSON.stringify(session, null, 2);
  const id =
    session && typeof session === 'object' && 'id' in session
      ? String((session as { id?: string }).id)
      : '';
  lastError.value = `Click captured. id=${id || '(empty)'}\nrow=${dump}`;
  if (!id || id === 'undefined') {
    return;
  }
  if (deletingId.value) return;
  deletingId.value = id;
  try {
    if (activeSessionId.value === id) {
      await router.push('/sessions');
    }
    await sessions.remove(id);
    lastError.value = `Deleted ${id}`;
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    console.error('Delete session failed:', err);
    lastError.value = `Delete failed: ${msg}`;
    await sessions.refresh();
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
  lastError.value = null;
  if (activeSessionId.value) {
    await router.push('/sessions');
  }
  const failures: string[] = [];
  for (const s of [...sessions.list.value]) {
    try {
      await sessions.remove(s.id);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      failures.push(`${s.id}: ${msg}`);
    }
  }
  if (failures.length > 0) {
    lastError.value = `Some deletes failed:\n${failures.join('\n')}`;
  }
  await sessions.refresh();
}

function dismissError() {
  lastError.value = null;
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

    <!-- error banner -->
    <div
      v-if="lastError"
      class="mx-2 mb-1 rounded-sm border border-signal-danger bg-surface-1 px-2 py-1.5 font-ui text-[11px] text-signal-danger"
      role="alert"
    >
      <pre class="whitespace-pre-wrap break-words">{{ lastError }}</pre>
      <button
        type="button"
        class="mt-1 text-[10px] uppercase tracking-[0.18em] text-ink-dim hover:text-ink"
        @click="dismissError"
      >
        Dismiss
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
          class="flex items-stretch gap-1"
        >
          <button
            type="button"
            class="flex-1 min-w-0 flex items-center gap-2 px-3 py-2 rounded-sm text-left text-sm font-ui transition-fast ease-kenaz"
            :class="
              activeSessionId === session.id
                ? 'text-ink bg-surface-2 ring-1 ring-accent-hairline'
                : 'text-ink-muted hover:text-ink hover:bg-surface-2'
            "
            :title="session.name || session.id"
            :aria-current="activeSessionId === session.id ? 'page' : undefined"
            :data-testid="`open-session-${session.id}`"
            @click="openSession(session)"
          >
            <MessageSquare
              :size="14"
              :class="activeSessionId === session.id ? 'text-accent' : ''"
            />
            <span class="truncate hidden two-col:inline">
              {{ session.name }}
            </span>
          </button>
          <button
            type="button"
            class="shrink-0 p-2 rounded-sm text-ink-dim hover:text-signal-danger hover:bg-surface-3 focus:outline-none focus:ring-1 focus:ring-accent disabled:opacity-50"
            :aria-label="`Delete session ${session.name}`"
            :disabled="deletingId === session.id"
            :data-testid="`delete-session-${session.id}`"
            @click="deleteSession(session, $event)"
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
