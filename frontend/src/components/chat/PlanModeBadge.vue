<script setup lang="ts">
/**
 * PlanModeBadge — orange session-header chip displayed while the session
 * is in plan_mode (plan-mode-posture-01KZNP3F WP06).
 *
 * The badge is read-only: it shows the current posture status with a
 * tooltip explaining what plan_mode means. It coexists with AutonomyChip.
 *
 * It is shown/hidden by the parent (SessionHeader) which subscribes to
 * the plan_mode_changed Wails event via the usePlanMode composable.
 */

const props = defineProps<{
  /** When true the badge is visible. */
  isActive: boolean;
  /** Optional: the plan_id of the pending plan (not yet approved). */
  pendingPlanId?: string | null;
}>();

const tooltipText =
  'Plan mode — the model is in read-only exploration mode. ' +
  'No write tools are permitted. The model is designing a plan ' +
  'that you will review before any changes are made.';
</script>

<template>
  <div
    v-if="props.isActive"
    class="relative inline-flex"
    data-testid="plan-mode-badge"
  >
    <span
      class="inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 font-ui text-[11px] uppercase tracking-[0.12em]
             border-signal-warn text-signal-warn bg-signal-warn-soft"
      :title="tooltipText"
      data-testid="plan-mode-badge-chip"
      role="status"
      :aria-label="tooltipText"
    >
      <span aria-hidden="true" class="text-signal-warn">◆</span>
      PLAN MODE
      <span
        v-if="props.pendingPlanId"
        class="ml-0.5 text-[9px] font-normal not-italic text-signal-warn"
        data-testid="plan-mode-badge-pending"
      >
        pending approval
      </span>
    </span>
  </div>
</template>
