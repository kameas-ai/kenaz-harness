<script setup lang="ts">
import { computed, ref } from 'vue';
import EventStreamRow from './EventStreamRow.vue';
import type { Category } from '@/lib/categories';
import type { EventStreamEntry } from '@/lib/types';

/**
 * EventStreamList — dense scrollable list of EventStreamRow primitives
 * (FR-001c). rAF batching lives at the useStream layer (lib/useStream.ts);
 * this component just renders the buffered entries.
 */
const props = defineProps<{
  entries: ReadonlyArray<EventStreamEntry>;
}>();

const paused = ref(false);

function pause() {
  paused.value = true;
}
function resume() {
  paused.value = false;
}

const visible = computed<ReadonlyArray<EventStreamEntry>>(() => props.entries);
</script>

<template>
  <div class="flex flex-col h-full">
    <div class="flex items-center justify-end px-3 py-1.5 border-b border-border-muted">
      <button
        type="button"
        class="text-[11px] font-ui text-ink-muted hover:text-ink"
        :aria-label="paused ? 'Resume stream' : 'Pause stream'"
        @click="paused ? resume() : pause()"
      >
        {{ paused ? 'Resume' : 'Pause' }}
      </button>
    </div>
    <div
      class="flex-1 overflow-y-auto"
      role="log"
      aria-live="polite"
      aria-relevant="additions"
    >
      <EventStreamRow
        v-for="e in visible"
        :key="e.id"
        :timestamp="e.timestamp"
        :category="e.category as Category"
        :subject="e.subject"
        :trailing="e.trailing"
        :size="e.size"
      />
    </div>
  </div>
</template>
