// Package planmode provides the RPC handlers for the plan-mode approval flow:
// Approve, Edit, and Discard. These are called from the frontend's
// PlanApprovalModal when the user decides the fate of a pending plan.
//
// Mission: plan-mode-posture-01KZNP3F WP05
//
// Flow:
//
//  1. Model calls __exit_plan_mode(plan) → artifact persisted, result
//     {awaiting_user_approval:true, plan_id} returned.
//  2. Frontend shows PlanApprovalModal with the plan text.
//  3. User clicks Approve / Edit / Discard.
//  4. Frontend calls Planmode_Approve / Planmode_Edit / Planmode_Discard.
//  5. These RPCs:
//     - Restore the pre-plan session layer (clearing plan_mode posture).
//     - Emit a plan_mode_changed event so the frontend chip updates.
//     - Return the outcome to the caller.
//
// The plan artifact is ALWAYS persisted regardless of the user's decision
// (it was captured in __exit_plan_mode). Edit merely updates the artifact's
// content and then approves; Discard leaves the artifact but returns
// approved:false.
package planmode

import (
	"context"
	"fmt"

	coreart "github.com/kameas-ai/kenaz-harness/core/artifacts"
	coreplanmode "github.com/kameas-ai/kenaz-harness/core/tools/planmode"
)

// ApproveRequest is the input to Planmode_Approve.
type ApproveRequest struct {
	// SessionID identifies the session whose plan_mode should be cleared.
	SessionID string `json:"session_id"`
	// PlanID is the artifact ID of the plan being approved.
	PlanID string `json:"plan_id"`
}

// ApproveResponse is returned to the frontend on success.
type ApproveResponse struct {
	Approved  bool   `json:"approved"`
	SessionID string `json:"session_id"`
	PlanID    string `json:"plan_id"`
}

// DiscardRequest is the input to Planmode_Discard.
type DiscardRequest struct {
	SessionID string `json:"session_id"`
	PlanID    string `json:"plan_id"`
}

// DiscardResponse is returned on discard.
type DiscardResponse struct {
	Approved  bool   `json:"approved"`
	Reason    string `json:"reason"`
	SessionID string `json:"session_id"`
	PlanID    string `json:"plan_id"`
}

// EditRequest is the input to Planmode_Edit. The caller provides the edited
// plan markdown; the RPC updates the artifact and then approves.
type EditRequest struct {
	SessionID   string `json:"session_id"`
	PlanID      string `json:"plan_id"`
	EditedPlan  string `json:"edited_plan"`
}

// EditResponse is returned when an edited plan is approved.
type EditResponse struct {
	Approved  bool   `json:"approved"`
	SessionID string `json:"session_id"`
	PlanID    string `json:"plan_id"`
}

// ArtifactUpdater is the narrow write surface for plan edit. It writes
// a new content version for an existing artifact. *coreart.Manager
// satisfies it.
type ArtifactUpdater interface {
	WriteVersion(ctx context.Context, artifactID string, bytes []byte, mimeType string, summary, path *string) (coreart.ArtifactVersion, error)
}

// EventEmitter is the narrow surface for firing plan_mode_changed events.
// The chassis wires this against its hooks.Runner; tests pass a stub.
type EventEmitter interface {
	Emit(ctx context.Context, sessionID, event string, payload map[string]any) error
}

// EventPlanModeChanged is the canonical event name fired when the session
// transitions in/out of plan_mode. The frontend's planmode.ts composable
// subscribes to this event to update the PlanModeBadge chip.
const EventPlanModeChanged = "plan_mode_changed"

// API holds the dependencies for the three planmode approval RPCs.
// Constructed once by the chassis wiring (builtins_wiring.go or api.go)
// and injected into the Wails-bound surface.
type API struct {
	posture  coreplanmode.SessionPostureManager
	emitter  EventEmitter
	updater  ArtifactUpdater
}

// NewAPI constructs an approval API. posture is required. emitter and
// updater are nil-tolerated for test and stub paths.
func NewAPI(posture coreplanmode.SessionPostureManager, emitter EventEmitter, updater ArtifactUpdater) *API {
	if posture == nil {
		panic("planmode.NewAPI: nil posture")
	}
	return &API{posture: posture, emitter: emitter, updater: updater}
}

// Approve clears the plan_mode posture, restores the prior layer, and
// fires plan_mode_changed. Called by the frontend when the user clicks
// "Approve & continue".
func (a *API) Approve(ctx context.Context, req ApproveRequest) (ApproveResponse, error) {
	if req.SessionID == "" {
		return ApproveResponse{}, fmt.Errorf("planmode.Approve: empty session_id")
	}
	if _, err := coreplanmode.RestorePrePlanLayer(ctx, a.posture, req.SessionID); err != nil {
		return ApproveResponse{}, fmt.Errorf("planmode.Approve: %w", err)
	}
	a.emitChanged(ctx, req.SessionID, "approved", req.PlanID)
	return ApproveResponse{
		Approved:  true,
		SessionID: req.SessionID,
		PlanID:    req.PlanID,
	}, nil
}

// Discard clears the plan_mode posture and fires plan_mode_changed. The
// plan artifact is NOT deleted — it remains in the artifacts panel for
// history. The model receives {approved:false, reason:"discarded"} on its
// next turn (the toolloop delivers this via the pending-approval mechanism).
func (a *API) Discard(ctx context.Context, req DiscardRequest) (DiscardResponse, error) {
	if req.SessionID == "" {
		return DiscardResponse{}, fmt.Errorf("planmode.Discard: empty session_id")
	}
	if _, err := coreplanmode.RestorePrePlanLayer(ctx, a.posture, req.SessionID); err != nil {
		return DiscardResponse{}, fmt.Errorf("planmode.Discard: %w", err)
	}
	a.emitChanged(ctx, req.SessionID, "discarded", req.PlanID)
	return DiscardResponse{
		Approved:  false,
		Reason:    "discarded",
		SessionID: req.SessionID,
		PlanID:    req.PlanID,
	}, nil
}

// Edit updates the plan artifact with edited content and then approves.
// Used by the frontend's inline editor: "Save" in the edit view triggers
// this RPC rather than separate Edit+Approve calls.
func (a *API) Edit(ctx context.Context, req EditRequest) (EditResponse, error) {
	if req.SessionID == "" {
		return EditResponse{}, fmt.Errorf("planmode.Edit: empty session_id")
	}
	if req.PlanID == "" {
		return EditResponse{}, fmt.Errorf("planmode.Edit: empty plan_id")
	}

	// Update the artifact content if an updater is wired.
	if a.updater != nil && req.EditedPlan != "" {
		mimeType := "text/markdown; charset=utf-8; kind=plan"
		summary := "User-edited plan"
		if _, err := a.updater.WriteVersion(ctx, req.PlanID, []byte(req.EditedPlan), mimeType, &summary, nil); err != nil {
			return EditResponse{}, fmt.Errorf("planmode.Edit: write version: %w", err)
		}
	}

	// Restore posture (same as Approve).
	if _, err := coreplanmode.RestorePrePlanLayer(ctx, a.posture, req.SessionID); err != nil {
		return EditResponse{}, fmt.Errorf("planmode.Edit: %w", err)
	}
	a.emitChanged(ctx, req.SessionID, "edited_and_approved", req.PlanID)
	return EditResponse{
		Approved:  true,
		SessionID: req.SessionID,
		PlanID:    req.PlanID,
	}, nil
}

// emitChanged fires the plan_mode_changed event. Errors are logged but not
// propagated — a failed event emission must not block the approval flow.
func (a *API) emitChanged(ctx context.Context, sessionID, outcome, planID string) {
	if a.emitter == nil {
		return
	}
	payload := map[string]any{
		"session_id": sessionID,
		"outcome":    outcome,
		"plan_id":    planID,
		"posture":    "", // cleared — no longer in plan_mode
	}
	_ = a.emitter.Emit(ctx, sessionID, EventPlanModeChanged, payload)
}
