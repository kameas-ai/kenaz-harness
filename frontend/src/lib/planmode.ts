/**
 * planmode.ts — composable + typed wrappers for the plan-mode posture
 * (plan-mode-posture-01KZNP3F WP06).
 *
 * PlanMode is the frontend representation of the plan_mode autonomy posture.
 * It exposes:
 *
 *   - `usePlanMode(sessionId)` — a Vue composable that subscribes to the
 *     `plan_mode_changed` Wails event and keeps `isActive` + `pendingPlanId`
 *     in sync.
 *   - Typed request/response shapes for the Approve/Edit/Discard RPCs.
 *
 * The composable is safe to mount in the ChatView / SessionHeader; it
 * auto-cleans-up via onUnmounted.
 */

import { ref, onUnmounted } from 'vue';
import { useHarnessClient } from '@/lib/harnessClientContext';

// ── Event shape ────────────────────────────────────────────────────────────

/**
 * PlanModeChangedPayload is the wire shape of the `plan_mode_changed` event
 * emitted by the approval RPCs when the session transitions in/out of
 * plan_mode.
 */
export interface PlanModeChangedPayload {
  session_id: string;
  /** "approved" | "discarded" | "edited_and_approved" | "entered" */
  outcome: string;
  plan_id: string;
  /** "" when posture is cleared; "plan_mode" when just entered */
  posture: string;
}

// ── RPC request/response shapes ────────────────────────────────────────────

export interface PlanApproveRequest {
  session_id: string;
  plan_id: string;
}

export interface PlanApproveResponse {
  approved: boolean;
  session_id: string;
  plan_id: string;
}

export interface PlanDiscardRequest {
  session_id: string;
  plan_id: string;
}

export interface PlanDiscardResponse {
  approved: boolean;
  reason: string;
  session_id: string;
  plan_id: string;
}

export interface PlanEditRequest {
  session_id: string;
  plan_id: string;
  edited_plan: string;
}

export interface PlanEditResponse {
  approved: boolean;
  session_id: string;
  plan_id: string;
}

// ── Composable ─────────────────────────────────────────────────────────────

/**
 * usePlanMode returns reactive state for the session's plan_mode posture.
 *
 * `isActive`      — true while the session is in plan_mode.
 * `pendingPlanId` — the artifact ID of the plan awaiting user approval,
 *                   or null when no plan is pending.
 *
 * The composable subscribes to the `plan_mode_changed` Wails event and
 * auto-unsubscribes on component unmount.
 *
 * Usage:
 *
 *   const { isActive, pendingPlanId } = usePlanMode(props.sessionId);
 */
export function usePlanMode(sessionId: string) {
  const isActive = ref(false);
  const pendingPlanId = ref<string | null>(null);

  const client = useHarnessClient();

  function handlePlanModeChanged(payload: PlanModeChangedPayload) {
    if (payload.session_id !== sessionId) return;

    if (payload.posture === 'plan_mode') {
      isActive.value = true;
    } else {
      isActive.value = false;
      pendingPlanId.value = null;
    }

    if (payload.outcome === '' && payload.posture === 'plan_mode') {
      // Entered plan_mode — no pending plan yet.
      isActive.value = true;
    }

    if (['approved', 'discarded', 'edited_and_approved'].includes(payload.outcome)) {
      isActive.value = false;
      pendingPlanId.value = null;
    }
  }

  // Subscribe via the harness client's event stream if available.
  let unsubscribe: (() => void) | null = null;
  if (client && typeof (client as any).subscribeEvent === 'function') {
    unsubscribe = (client as any).subscribeEvent('plan_mode_changed', handlePlanModeChanged);
  }

  onUnmounted(() => {
    if (unsubscribe) unsubscribe();
  });

  /**
   * setPendingPlan should be called when __exit_plan_mode returns
   * {awaiting_user_approval:true, plan_id} — the toolloop notifies the
   * frontend of the pending approval.
   */
  function setPendingPlan(planId: string) {
    pendingPlanId.value = planId;
  }

  /** Manually flip isActive (used by tests and by enter_plan_mode tool result handlers). */
  function setActive(active: boolean) {
    isActive.value = active;
    if (!active) pendingPlanId.value = null;
  }

  return { isActive, pendingPlanId, setPendingPlan, setActive };
}
