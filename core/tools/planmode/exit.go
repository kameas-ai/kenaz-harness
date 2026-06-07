package planmode

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	coreart "github.com/kameas-ai/kenaz-harness/core/artifacts"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

const (
	// ExitToolName is the model-facing tool identifier.
	ExitToolName = "kaneaz__exit_plan_mode"

	// ExitToolDescription steers the model toward calling this tool
	// only when it has a complete, well-structured plan to present.
	ExitToolDescription = "Exit plan mode by presenting a markdown plan for user approval. " +
		"The plan field should be a complete, well-structured markdown document outlining " +
		"every change you intend to make, why each change is necessary, and the order of " +
		"execution. The user will review the plan and Approve, Edit, or Discard it. " +
		"After approval, write-capable tools are re-enabled. " +
		"Only call this tool when you have a complete plan — partial plans will be discarded."

	// MaxPlanBytes caps the plan at 1 MiB. A plan larger than this is almost
	// certainly a model output error; refuse it cleanly rather than bloating
	// the artifact store.
	MaxPlanBytes = 1 * 1024 * 1024
)

// exitPlanModeInputSchema is the JSON Schema for __exit_plan_mode args.
const exitPlanModeInputSchema = `{
  "type": "object",
  "properties": {
    "plan": {
      "type": "string",
      "description": "Complete markdown document describing the planned changes: what, why, and in what order."
    }
  },
  "required": ["plan"]
}`

// ArtifactCapturer is the narrow interface ExitTool uses to persist
// the plan artifact. *coreart.Manager satisfies it.
type ArtifactCapturer interface {
	Capture(ctx context.Context, candidates []coreart.CaptureCandidate, sessionID string) ([]coreart.Artifact, error)
}

// ExitOptions configures an ExitTool.
type ExitOptions struct {
	// Posture is required: reads/writes the session autonomy layer.
	Posture SessionPostureManager
	// Artifacts is required: persists the plan artifact.
	Artifacts ArtifactCapturer
	// SessionResolver derives the sessionID from context.
	// Defaults to toolloop.SessionIDFromContext.
	SessionResolver func(ctx context.Context) string
	// Logger is optional; nil falls back to slog.Default.
	Logger *slog.Logger
}

// ExitTool implements kaneaz__exit_plan_mode. Safe for concurrent use.
type ExitTool struct {
	posture   SessionPostureManager
	artifacts ArtifactCapturer
	resolver  func(ctx context.Context) string
	logger    *slog.Logger
}

// NewExitTool constructs an ExitTool. Posture and Artifacts are required.
func NewExitTool(opts ExitOptions) *ExitTool {
	if opts.Posture == nil {
		panic("planmode.NewExitTool: nil Posture")
	}
	if opts.Artifacts == nil {
		panic("planmode.NewExitTool: nil Artifacts")
	}
	resolver := opts.SessionResolver
	if resolver == nil {
		resolver = toolloop.SessionIDFromContext
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &ExitTool{
		posture:   opts.Posture,
		artifacts: opts.Artifacts,
		resolver:  resolver,
		logger:    logger,
	}
}

// Name satisfies toolloop.BuiltinTool.
func (t *ExitTool) Name() string { return ExitToolName }

// Description satisfies toolloop.BuiltinTool.
func (t *ExitTool) Description() string { return ExitToolDescription }

// InputSchema satisfies toolloop.BuiltinTool.
func (t *ExitTool) InputSchema() json.RawMessage {
	return json.RawMessage(exitPlanModeInputSchema)
}

// exitArgs is the wire-shape for __exit_plan_mode arguments.
type exitArgs struct {
	Plan string `json:"plan"`
}

// exitPendingResult is returned to the model when the plan is awaiting
// user approval. The toolloop pauses the session on receipt of this.
type exitPendingResult struct {
	AwaitingUserApproval bool   `json:"awaiting_user_approval"`
	PlanID               string `json:"plan_id"`
	Message              string `json:"message"`
}

// exitErrorResult is returned when the call fails at the tool level.
type exitErrorResult struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// Call dispatches a single __exit_plan_mode invocation. It:
//  1. Resolves sessionID from context.
//  2. Validates that the session is currently in plan_mode.
//  3. Validates the plan argument.
//  4. Persists the plan as a kind:"plan" artifact (regardless of whether
//     the user later approves or discards — the artifact is permanent).
//  5. Returns {awaiting_user_approval:true, plan_id} to signal the
//     toolloop to pause and wait for the user's Approve/Discard/Edit decision.
//
// The toolloop itself does not poll; the approval RPCs in
// core/rpc/views/planmode/ complete the flow by calling RestorePrePlanLayer.
func (t *ExitTool) Call(ctx context.Context, argsJSON json.RawMessage) (json.RawMessage, error) {
	sessionID := t.resolver(ctx)
	if sessionID == "" {
		return marshalExitErr("no_session", "no session id in context")
	}

	if len(argsJSON) == 0 {
		return marshalExitErr("invalid_args", "empty args")
	}
	var args exitArgs
	if err := json.Unmarshal(argsJSON, &args); err != nil {
		return marshalExitErr("invalid_args", fmt.Sprintf("parse args: %v", err))
	}
	args.Plan = strings.TrimSpace(args.Plan)
	if args.Plan == "" {
		return marshalExitErr("invalid_args", "plan is required and must not be empty")
	}
	if len(args.Plan) > MaxPlanBytes {
		return marshalExitErr("plan_too_large",
			fmt.Sprintf("plan exceeds %d bytes", MaxPlanBytes))
	}

	// Validate posture.
	current, err := t.posture.LoadAutonomyProfile(ctx, sessionID)
	if err != nil {
		t.logger.Warn("planmode.exit.load_layer_failed", "session_id", sessionID, "err", err.Error())
		return marshalExitErr("load_failed", fmt.Sprintf("load session layer: %v", err))
	}
	if current.PostureMode == nil || *current.PostureMode != autonomy.PostureModePlanMode {
		return marshalExitErr("not_in_plan_mode",
			"session is not in plan_mode; call __enter_plan_mode first")
	}

	// Persist the plan as an artifact tagged as a plan. The artifact's
	// MimeType ("text/markdown; plan") encodes kind information for the
	// artifacts panel to distinguish plan artifacts from other tool outputs.
	// Source=SourceToolOutput is the correct source for model-produced
	// artifacts (validated by the artifacts store).
	title := planArtifactTitle(sessionID)
	candidates := []coreart.CaptureCandidate{{
		Title:    title,
		MimeType: "text/markdown; charset=utf-8; kind=plan",
		Bytes:    []byte(args.Plan),
		Source:   coreart.SourceToolOutput,
		SourceRef: coreart.ArtifactSourceRef{
			MessageID: "tool:" + ExitToolName,
		},
	}}
	captured, err := t.artifacts.Capture(ctx, candidates, sessionID)
	if err != nil {
		t.logger.Warn("planmode.exit.capture_failed",
			"session_id", sessionID,
			"err", err.Error(),
		)
		return marshalExitErr("capture_failed", fmt.Sprintf("persist plan artifact: %v", err))
	}
	if len(captured) == 0 {
		return marshalExitErr("capture_failed", "artifact manager returned zero artifacts")
	}
	planID := captured[0].ID

	t.logger.Info("planmode.exit.awaiting_approval",
		"session_id", sessionID,
		"plan_id", planID,
	)

	out := exitPendingResult{
		AwaitingUserApproval: true,
		PlanID:               planID,
		Message:              "Plan presented to user for approval. The session is paused until the user approves, edits, or discards the plan.",
	}
	return json.Marshal(out)
}

// planArtifactTitle generates a title for the plan artifact embedding the
// current UTC timestamp so multiple plans in the same session are
// distinguishable.
func planArtifactTitle(sessionID string) string {
	ts := time.Now().UTC().Format("2006-01-02T15:04")
	_ = sessionID // future: embed short session prefix
	return fmt.Sprintf("Plan — %s", ts)
}

// marshalExitErr returns a JSON-encoded exitErrorResult with no Go error.
func marshalExitErr(kind, message string) (json.RawMessage, error) {
	out := exitErrorResult{Error: kind, Message: message}
	return json.Marshal(out)
}

// RestorePrePlanLayer decodes and persists the pre-plan snapshot layer
// for the given session. Called by the approval RPCs on Approve/Discard.
// On Discard, keepPlanMode can be set to false to clear the posture; on
// Approve it is also false (write capability restored).
//
// Returns the restored Layer for callers that need to emit it on the wire.
func RestorePrePlanLayer(ctx context.Context, posture SessionPostureManager, sessionID string) (autonomy.Layer, error) {
	current, err := posture.LoadAutonomyProfile(ctx, sessionID)
	if err != nil {
		return autonomy.Layer{}, fmt.Errorf("planmode: restore: load: %w", err)
	}
	if current.PrePlanLayer == nil {
		// No snapshot — restore to empty default layer. This happens when
		// the model entered plan_mode from a zero-value session layer.
		restored := autonomy.DefaultLayer()
		restored.PostureMode = nil
		restored.PrePlanLayer = nil
		if err := posture.SaveAutonomyProfile(ctx, sessionID, restored); err != nil {
			return autonomy.Layer{}, fmt.Errorf("planmode: restore: save default: %w", err)
		}
		return restored, nil
	}

	var snap autonomy.Layer
	if err := json.Unmarshal(current.PrePlanLayer, &snap); err != nil {
		return autonomy.Layer{}, fmt.Errorf("planmode: restore: decode snapshot: %w", err)
	}
	snap.PostureMode = nil
	snap.PrePlanLayer = nil
	if err := posture.SaveAutonomyProfile(ctx, sessionID, snap); err != nil {
		return autonomy.Layer{}, fmt.Errorf("planmode: restore: save: %w", err)
	}
	return snap, nil
}
