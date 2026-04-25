<script setup lang="ts">
import { ref, onMounted } from 'vue';
import RailEntry from './RailEntry.vue';
import {
  Plus,
  MessageSquare,
  Wrench,
  Layers,
  Server,
  FileText,
  Settings,
} from './icons';
import { useSessions } from '@/lib/useHarnessAPI';

/**
 * LeftRail — three vertical regions (plan §2.1):
 *   1. New-session affordance (top)
 *   2. Sessions list (middle, scrollable)
 *   3. Primary-surfaces nav (bottom)
 *
 * Collapses to icons-only at < 960px (--breakpoint-two-col, FR-016).
 */
const sessions = useSessions();
const newSessionLoading = ref(false);

onMounted(() => {
  sessions.refresh();
});

async function newSession() {
  newSessionLoading.value = true;
  try {
    await sessions.create('New session');
  } finally {
    newSessionLoading.value = false;
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
        <li v-for="session in sessions.list.value" :key="session.id">
          <RailEntry
            :icon="MessageSquare"
            :label="session.name"
            :to="`/sessions#${session.id}`"
          />
        </li>
        <li v-if="sessions.list.value.length === 0" class="px-3 py-2 text-xs text-ink-subtle">
          No sessions yet
        </li>
      </ul>
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
