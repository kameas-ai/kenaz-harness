<script setup lang="ts">
/**
 * BranchBreadcrumb — displays a contextual "Branch of <parent>" bar at
 * the top of a branch session's chat view, with an ancestor-depth
 * expander when the branch nests more than one level deep.
 *
 * Hidden when parentSessionId is empty.
 * Hidden when HARNESS_BRANCHING_POLISH feature flag is off.
 *
 * branching-ux-polish-01KQ8TD7 WP04.
 *
 * NARROWED (controls-and-readouts-that-tell-the-truth-01PMZ808 UNIT-8,
 * WP13, spec D-8): the docstring used to promise "Branch from turn N of
 * <parent>" — turnNumber is never passed by the sole mount
 * (SessionsView.vue), because nothing in the tree converts a parent
 * message id into a turn ordinal, and SessionsView does not hold the
 * parent's message list turnNumber's own doc claimed it would be
 * computed from. `justify(blocker: "no message-id-to-turn-ordinal
 * conversion exists", owner: alec, date: 2026-08-19)`. `ancestorCount`
 * IS wired (session.branchDepth, pre-computed server-side).
 */
import { computed, ref } from 'vue';
import { useRouter } from 'vue-router';
import { ChevronRight, GitBranch } from '@/shell/icons';

const props = defineProps<{
  /** The session ID of the parent (forked-from) session. */
  parentSessionId: string;
  /** The message anchor in the parent session. Maps to a turn number. */
  parentMessageId?: string;
  /** Human-readable name of the parent session. */
  parentTitle: string;
  /**
   * 1-based turn number in the parent session where the fork was made.
   * NARROWED (01PMZ808 UNIT-8 WP13): no caller passes this today — the
   * host (SessionsView) does not hold the parent's message list this
   * would be computed from, and no message-id-to-turn-ordinal
   * conversion exists anywhere in the tree. Omit (undefined, the only
   * value ever supplied) falls back to "Branch of <parent>" wording.
   */
  turnNumber?: number;
  /**
   * When > 1, a "..." expand button appears before the parent title
   * that reveals the full ancestor stack.
   */
  ancestorCount?: number;
  /**
   * When true, the parent session has been deleted; render the
   * "[parent deleted: <name>]" fallback form.
   */
  parentDeleted?: boolean;
}>();

// Feature flag consistent with LeftRail's constant.
const BRANCHING_POLISH_ENABLED: boolean = (() => {
  try {
    const v = localStorage.getItem('harness.feature.branchingPolish');
    if (v === 'off' || v === 'false') return false;
  } catch { /* ignore */ }
  return true;
})();

const router = useRouter();

const showAncestors = ref(false);

/** Whether this component should render at all. */
const visible = computed(() =>
  BRANCHING_POLISH_ENABLED && !!props.parentSessionId,
);

/** Primary label: "Branch from turn N of" or "Branch of". */
const label = computed(() => {
  if (props.parentDeleted) return `Branch of [parent deleted: ${props.parentTitle}]`;
  if (props.turnNumber !== undefined) return `Branch from turn ${props.turnNumber} of`;
  return 'Branch of';
});

/** Whether the parent title is a clickable link (not deleted). */
const hasLink = computed(() => !props.parentDeleted && !!props.parentSessionId);

function navigate() {
  if (hasLink.value) {
    void router.push(`/sessions/${props.parentSessionId}`);
  }
}

function toggleAncestors() {
  showAncestors.value = !showAncestors.value;
}
</script>

<template>
  <div
    v-if="visible"
    class="flex items-center gap-1.5 px-4 py-1.5 bg-surface-1 border-b border-border-muted font-ui text-[11px] text-ink-muted"
    data-testid="branch-breadcrumb"
    role="navigation"
    aria-label="Branch ancestry"
  >
    <GitBranch :size="11" class="flex-shrink-0 text-ink-dim" aria-hidden="true" />

    <!-- Multi-level ancestor expand affordance -->
    <button
      v-if="(ancestorCount ?? 0) > 1"
      type="button"
      class="px-1 py-0.5 rounded-sm text-ink-dim hover:text-ink hover:bg-surface-2 transition-fast"
      :aria-expanded="showAncestors"
      :aria-label="showAncestors ? 'Hide ancestor chain' : 'Show ancestor chain'"
      data-testid="branch-breadcrumb-ancestors-toggle"
      @click="toggleAncestors"
    >
      ...
    </button>

    <!-- Ancestor popover (when expanded) -->
    <div
      v-if="showAncestors && (ancestorCount ?? 0) > 1"
      class="absolute z-50 mt-6 ml-8 rounded-sm border border-border-muted bg-surface-0 shadow-lg py-1 px-2 text-[11px]"
      data-testid="branch-breadcrumb-ancestor-popover"
    >
      <span class="text-ink-subtle italic">{{ ancestorCount }} levels deep</span>
    </div>

    <!-- Primary label text -->
    <span data-testid="branch-breadcrumb-label">{{ label }}</span>

    <!-- Parent title (router-link when not deleted) -->
    <button
      v-if="hasLink"
      type="button"
      class="text-accent hover:underline truncate max-w-[200px]"
      :title="parentTitle"
      data-testid="branch-breadcrumb-parent-link"
      @click="navigate"
    >
      {{ parentTitle }}
    </button>
    <span
      v-else-if="!parentDeleted"
      class="text-ink truncate max-w-[200px]"
      data-testid="branch-breadcrumb-parent-name"
    >
      {{ parentTitle }}
    </span>

    <ChevronRight v-if="!parentDeleted" :size="10" class="text-ink-dim" aria-hidden="true" />
  </div>
</template>
