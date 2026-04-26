<script setup lang="ts">
/**
 * WorkflowsView — agentic workflows surface (stub).
 *
 * Lands the nav slot for pre-canned multi-step agent recipes
 * (plan → research → implement → review and similar). v1 ships
 * an empty-state card describing the concept; the runtime that
 * executes a workflow + the catalog of shipped workflows arrive
 * in a follow-up mission.
 */
import CanvasHead from '@/shell/CanvasHead.vue';
</script>

<template>
  <div>
    <CanvasHead
      number="08"
      section="WORKFLOWS"
      title="Agentic Workflows"
      subtitle="Pre-canned multi-step agent recipes."
    />
    <div class="px-6 py-4">
      <div
        class="rounded-sm border border-border-muted bg-surface-1 p-6 max-w-2xl"
      >
        <p class="font-ui text-sm text-ink mb-2">
          No workflows installed yet.
        </p>
        <p class="font-ui text-sm text-ink-muted mb-3">
          Workflows chain agents together: a plan step, a research step, an
          implement step, a review step. Each step picks a model, a context
          scope, and a tool allowlist; the runtime hands the next step the
          previous step's output.
        </p>
        <p class="font-ui text-sm text-ink-muted">
          The execution runtime + shipped catalog land in a follow-up
          mission. Until then this surface is a placeholder for the nav
          slot.
        </p>
      </div>
    </div>
  </div>
</template>
