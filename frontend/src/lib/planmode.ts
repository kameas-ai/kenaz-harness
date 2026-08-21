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

import { ref } from 'vue';
import { useEventStream } from '@/lib/useEventStream';

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

/** Outcomes that clear plan_mode posture — approval, discard, or an
 * edit-then-approve. Any pending plan is resolved on these. */
const TERMINAL_OUTCOMES = ['approved', 'discarded', 'edited_and_approved'];

/**
 * usePlanMode returns reactive state for the session's plan_mode posture.
 *
 * `isActive`      — true while the session is in plan_mode.
 * `pendingPlanId` — the artifact ID of the plan awaiting user approval,
 *                   or null when no plan is pending.
 *
 * The composable subscribes to the `plan_mode_changed` broker event and
 * auto-unsubscribes on component unmount.
 *
 * Usage:
 *
 *   const { isActive, pendingPlanId } = usePlanMode(props.sessionId);
 */
export function usePlanMode(sessionId: string) {
  const isActive = ref(false);
  const pendingPlanId = ref<string | null>(null);

  function handlePlanModeChanged(payload: PlanModeChangedPayload) {
    if (payload.session_id !== sessionId) return;

    if (TERMINAL_OUTCOMES.includes(payload.outcome)) {
      // Approve / Discard / Edit-and-approve — posture cleared, any
      // pending plan is resolved.
      isActive.value = false;
      pendingPlanId.value = null;
      return;
    }

    if (payload.posture === 'plan_mode') {
      isActive.value = true;
      // A non-terminal payload carrying a plan_id is the "entered
      // plan_mode with a plan awaiting approval" transition — this is
      // what makes PlanApprovalModal mount (AC-15a). A payload with no
      // plan_id yet (just-entered, no plan drafted) leaves the previous
      // pendingPlanId alone rather than clobbering it with null, so a
      // late-arriving duplicate "entered" event can't race a
      // just-set pending id back to null.
      if (payload.plan_id) {
        pendingPlanId.value = payload.plan_id;
      }
    } else {
      isActive.value = false;
      pendingPlanId.value = null;
    }
  }

  // Subscribe through the app's real broker/event-stream path (Wails
  // runtime.EventsOn in desktop, the served WS event bus otherwise) —
  // see useEventStream.ts. Replaces the permanently-false
  // `client.subscribeEvent` probe: HarnessClient never declared that
  // method, so handlePlanModeChanged was never invoked and
  // pendingPlanId could never become non-null (trust-surfaces-that-
  // fire-01PMZ202 WP21 / UNIT-19, AC-15a). useEventStream auto-
  // unsubscribes on unmount (onBeforeUnmount internally) — no extra
  // cleanup needed here.
  useEventStream<PlanModeChangedPayload>('plan_mode_changed', handlePlanModeChanged);

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
