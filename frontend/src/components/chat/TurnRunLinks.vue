<script setup lang="ts">
/**
 * TurnRunLinks — the per-turn link from a conversation to the graph of
 * what the agent actually did (agentgraph-settings-linkage-01DOGF0D WP04,
 * spec FR-3 / FR-5).
 *
 * agentgraph-total-convergence-01PMGX01 WP16 restored Agent graphs to the
 * top nav because "every run materializes as a graph — this is where you
 * go to see what the agent actually did". Nothing on the chat surface
 * linked a turn to that graph, so the promise was half-kept. This strip
 * is the other half: every completed (and the live) turn offers
 *
 *   - "View run graph" → /agentgraph/run/:runId/graph (the materialized,
 *     read-only editor view; it states its own provenance tier), and
 *   - "Run details"    → /agentgraph/run/:runId (RunView: status, trace,
 *     approvals),
 *
 * keyed by the turn span id the backend recorded against the run
 * (Sessions_TurnRuns, migration sessions/0342). A turn with no recorded
 * run — it predates the mapping, the record write failed, it has no
 * span anchor, or it synced in from another device — gets the reason,
 * never a link: pre-fix run ids were reused across restarts, so guessing
 * one could open the wrong turn's graph.
 *
 * The parent decides whether this renders at all: served builds pass no
 * run map (Graph_* has no serve dispatch, D-701), and neither does any
 * caller that does not know about runs — so MessageList's classic golden
 * stays byte-exact.
 */
import { RouterLink } from 'vue-router';

const props = defineProps<{
  /** The kernel run that executed this turn, or '' when none is recorded. */
  runId: string;
}>();
</script>

<template>
  <div
    v-if="props.runId"
    class="mt-1 flex items-center gap-3 font-ui text-[11px] text-ink-subtle"
    data-testid="turn-run-links"
  >
    <RouterLink
      :to="`/agentgraph/run/${encodeURIComponent(props.runId)}/graph`"
      class="hover:text-accent underline-offset-2 hover:underline"
      data-testid="turn-run-graph-link"
    >
      View run graph
    </RouterLink>
    <RouterLink
      :to="`/agentgraph/run/${encodeURIComponent(props.runId)}`"
      class="hover:text-accent underline-offset-2 hover:underline"
      data-testid="turn-run-details-link"
    >
      Run details
    </RouterLink>
  </div>
  <div
    v-else
    class="mt-1 font-ui text-[11px] text-ink-subtle opacity-70"
    data-testid="turn-run-unrecorded"
    title="No run was recorded for this turn — it predates run-graph linkage, the recording failed, or the turn arrived from another device. Its run cannot be identified safely."
  >
    Run graph not recorded for this turn
  </div>
</template>
