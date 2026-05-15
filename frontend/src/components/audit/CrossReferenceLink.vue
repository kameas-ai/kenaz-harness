<script setup lang="ts">
/**
 * CrossReferenceLink — deep-link chip for cross-referencing audit entry
 * IDs to their canonical views.
 *
 * Supported reference kinds:
 *   session_id   → /sessions/<id>
 *   branch_id    → /branches/<id>
 *   artifact_id  → /artifacts/<id>
 *   workflow_id  → /workflows/<id>
 *   memory_chunk_id → /memory/<id>
 *
 * Unknown kinds render as a monospaced span with no link.
 */
import { computed } from 'vue';
import { useRouter } from 'vue-router';

export type CrossRefKind =
  | 'session_id'
  | 'branch_id'
  | 'artifact_id'
  | 'workflow_id'
  | 'memory_chunk_id';

const props = defineProps<{
  kind: CrossRefKind | string;
  id: string;
}>();

const router = useRouter() as ReturnType<typeof useRouter> | undefined;

const route = computed(() => {
  switch (props.kind) {
    case 'session_id': return `/sessions/${props.id}`;
    case 'branch_id': return `/branches/${props.id}`;
    case 'artifact_id': return `/artifacts/${props.id}`;
    case 'workflow_id': return `/workflows/${props.id}`;
    case 'memory_chunk_id': return `/memory/${props.id}`;
    default: return null;
  }
});

const label = computed(() => {
  const prefix = props.kind.replace(/_id$/, '');
  return `${prefix}:${props.id.slice(0, 8)}…`;
});

function navigate() {
  if (!route.value || !router) return;
  void router.push(route.value);
}
</script>

<template>
  <button
    v-if="route"
    type="button"
    class="inline-flex items-center gap-1 px-1.5 py-0.5 text-[10px] font-mono rounded border border-border-muted text-accent hover:text-accent hover:border-accent transition-colors"
    :title="`Navigate to ${kind}: ${id}`"
    @click.stop="navigate"
  >
    {{ label }}
  </button>
  <span
    v-else
    class="inline-block px-1.5 py-0.5 text-[10px] font-mono rounded border border-border-muted text-ink-muted"
    :title="`${kind}: ${id}`"
  >
    {{ label }}
  </span>
</template>
