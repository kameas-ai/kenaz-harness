// Package planmode implements the __enter_plan_mode and __exit_plan_mode
// builtin tools for the plan-mode-posture-01KZNP3F mission.
//
// Plan mode is a session-scoped, read-only investigation posture. While
// active, every write-class Cedar action is denied. The model enters by
// calling __enter_plan_mode and exits by calling __exit_plan_mode with a
// markdown plan. Exit requires explicit user approval before write capability
// returns.
//
// Architecture:
//   - EnterTool sets session.Layer.PostureMode = "plan_mode" via the
//     SessionPostureManager and snapshots the prior layer in the session's
//     pre_plan_layer slot.
//   - ExitTool validates posture, persists the plan as a kind:"plan"
//     artifact, and returns a "pending approval" result. The toolloop
//     pauses until the user calls Planmode_Approve / Planmode_Discard.
//   - The approval RPCs are in core/rpc/views/planmode/.
//
// Both tools follow the same pattern as core/tools/saveartifact: a plain
// Go struct satisfying toolloop.BuiltinTool; wired by builtins_wiring.go.
package planmode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

const (
	// EnterToolName is the model-facing tool identifier.
	EnterToolName = "kenaz__enter_plan_mode"

	// EnterToolDescription steers the model toward entering plan mode
	// before multi-file or architecturally ambiguous implementations.
	EnterToolDescription = "Enter plan mode: a strict read-only investigation posture. " +
		"Use this when a request involves non-trivial implementation across multiple files, " +
		"architectural decisions, or ambiguous scope. " +
		"While in plan mode you may only read, search, and explore — no writes are permitted. " +
		"Design a plan, then call __exit_plan_mode to present it for user approval."
)

// enterPlanModeInputSchema is the JSON Schema for __enter_plan_mode args.
const enterPlanModeInputSchema = `{
  "type": "object",
  "properties": {
    "reason": {
      "type": "string",
      "description": "Optional explanation of why plan mode is being entered (e.g. 'multi-file refactor with unclear scope')."
    }
  }
}`

// SessionPostureManager is the narrow interface the plan_mode tools consume
// to read and mutate the session's autonomy layer. The concrete
// implementation is core/rpc/views/sessions.managerAPI (via the
// SessionsAPI contract). Tests pass a fake.
type SessionPostureManager interface {
	// LoadAutonomyProfile returns the current autonomy.Layer for sessionID.
	LoadAutonomyProfile(ctx context.Context, sessionID string) (autonomy.Layer, error)
	// SaveAutonomyProfile persists a new autonomy.Layer for sessionID.
	SaveAutonomyProfile(ctx context.Context, sessionID string, layer autonomy.Layer) error
}

// EnterOptions configures an EnterTool.
type EnterOptions struct {
	// Posture is required: the session manager through which the tool
	// reads and mutates the session's autonomy layer.
	Posture SessionPostureManager
	// SessionResolver is consulted on every Call to derive the sessionID
	// from context. Defaults to toolloop.SessionIDFromContext.
	SessionResolver func(ctx context.Context) string
	// Logger is optional; nil falls back to slog.Default.
	Logger *slog.Logger
}

// EnterTool implements kenaz__enter_plan_mode. Safe for concurrent use.
type EnterTool struct {
	posture  SessionPostureManager
	resolver func(ctx context.Context) string
	logger   *slog.Logger
}

// NewEnterTool constructs an EnterTool. Posture is required.
func NewEnterTool(opts EnterOptions) *EnterTool {
	if opts.Posture == nil {
		panic("planmode.NewEnterTool: nil Posture")
	}
	resolver := opts.SessionResolver
	if resolver == nil {
		resolver = toolloop.SessionIDFromContext
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &EnterTool{
		posture:  opts.Posture,
		resolver: resolver,
		logger:   logger,
	}
}

// Name satisfies toolloop.BuiltinTool.
func (t *EnterTool) Name() string { return EnterToolName }

// Description satisfies toolloop.BuiltinTool.
func (t *EnterTool) Description() string { return EnterToolDescription }

// InputSchema satisfies toolloop.BuiltinTool.
func (t *EnterTool) InputSchema() json.RawMessage {
	return json.RawMessage(enterPlanModeInputSchema)
}

// enterArgs is the wire-shape for __enter_plan_mode arguments.
type enterArgs struct {
	Reason string `json:"reason,omitempty"`
}

// enterResult is returned to the model on success.
type enterResult struct {
	Posture    string `json:"posture"`
	EnteredAt  string `json:"entered_at"`
	WasAlready bool   `json:"was_already_in_plan_mode,omitempty"`
}

// enterErrorResult is returned when the call fails.
type enterErrorResult struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

// Call implements the __enter_plan_mode dispatch. It:
//  1. Resolves the sessionID from context.
//  2. Loads the current session layer.
//  3. If PostureMode is already "plan_mode", returns idempotent success.
//  4. Snapshots the prior layer into the session's pre_plan_layer slot
//     by encoding it into PostureMode's companion field PrePlanLayer.
//  5. Sets PostureMode = "plan_mode" and persists.
//  6. Emits a structured success result.
func (t *EnterTool) Call(ctx context.Context, argsJSON json.RawMessage) (json.RawMessage, error) {
	sessionID := t.resolver(ctx)
	if sessionID == "" {
		return marshalEnterErr("no_session", "no session id in context")
	}

	var args enterArgs
	if len(argsJSON) > 0 {
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return marshalEnterErr("invalid_args", fmt.Sprintf("parse args: %v", err))
		}
	}

	current, err := t.posture.LoadAutonomyProfile(ctx, sessionID)
	if err != nil {
		t.logger.Warn("planmode.enter.load_layer_failed", "session_id", sessionID, "err", err.Error())
		return marshalEnterErr("load_failed", fmt.Sprintf("load session layer: %v", err))
	}

	// Idempotent: if already in plan_mode return success with a flag.
	if current.PostureMode != nil && *current.PostureMode == autonomy.PostureModePlanMode {
		t.logger.Info("planmode.enter.already_active", "session_id", sessionID)
		out := enterResult{
			Posture:    autonomy.PostureModePlanMode,
			EnteredAt:  time.Now().UTC().Format(time.RFC3339),
			WasAlready: true,
		}
		return json.Marshal(out)
	}

	// Snapshot the pre-plan layer. We encode the prior Layer (without
	// PostureMode) into a JSON string stored as a session-scoped slot.
	// The exit tool and approval RPCs restore from this snapshot.
	//
	// Implementation: store the prior layer's JSON in a synthetic
	// PostureMode "snapshot" field by deriving a PrePlanLayer on the
	// updated layer. Since autonomy.Layer doesn't have a snapshot slot,
	// we encode the prior layer as the pre_plan_layer override value in
	// a JSON comment field in the Overrides map. We use the reserved
	// KnobPrePlanLayer key for this.
	//
	// Simpler alternative adopted: we store the full prior layer JSON in
	// a session-level override under the key _pre_plan_layer. The autonomy
	// package's unmarshal will reject this unknown knob key — so we keep
	// it as a separate field on the Layer struct instead.
	//
	// Actual implementation: the spec says "session-scoped _pre_plan_posture
	// slot". We implement this as a separate field on the session row via
	// SetPrePlanLayer. Since the sessions API doesn't yet have this method,
	// we snapshot via a separate key in the Layer.Overrides map using the
	// approach of a snapshot layer. However, the autonomy package rejects
	// unknown knobs during unmarshal.
	//
	// Clean approach: the snapshot is stored as a PostureMode companion
	// field PrePlanLayer on autonomy.Layer (added in WP01 as the
	// "snapshot" mechanism). Since PostureMode is the only extra field we
	// added, we store the snapshot by encoding the prior Layer's JSON into
	// a separate session metadata slot via a new SavePrePlanLayer method.
	//
	// For this WP we use the simplest viable pattern: store the snapshot
	// by cloning the current layer into a PrePlanLayer field on the
	// updated layer. The exit tool reads it back via LoadPrePlanLayer.

	pm := autonomy.PostureModePlanMode
	newLayer := cloneLayer(current)
	newLayer.PrePlanLayer = prePlanSnapshot(current)
	newLayer.PostureMode = &pm

	if err := t.posture.SaveAutonomyProfile(ctx, sessionID, newLayer); err != nil {
		t.logger.Warn("planmode.enter.save_layer_failed", "session_id", sessionID, "err", err.Error())
		return marshalEnterErr("save_failed", fmt.Sprintf("save session layer: %v", err))
	}

	t.logger.Info("planmode.enter.activated",
		"session_id", sessionID,
		"reason", args.Reason,
	)

	out := enterResult{
		Posture:   autonomy.PostureModePlanMode,
		EnteredAt: time.Now().UTC().Format(time.RFC3339),
	}
	return json.Marshal(out)
}

// cloneLayer returns a shallow copy of l with fresh Overrides map.
func cloneLayer(l autonomy.Layer) autonomy.Layer {
	out := autonomy.Layer{
		Level:       l.Level,
		PostureMode: l.PostureMode,
		PrePlanLayer: l.PrePlanLayer,
	}
	if l.Overrides != nil {
		out.Overrides = make(map[autonomy.Knob]any, len(l.Overrides))
		for k, v := range l.Overrides {
			out.Overrides[k] = v
		}
	} else {
		out.Overrides = map[autonomy.Knob]any{}
	}
	return out
}

// prePlanSnapshot encodes the prior (non-plan-mode) layer as a JSON snapshot
// string for round-trip restoration on exit/discard. Ignores PostureMode
// and PrePlanLayer fields so we don't cascade snapshots.
func prePlanSnapshot(l autonomy.Layer) []byte {
	snap := autonomy.Layer{
		Level:     l.Level,
		Overrides: l.Overrides,
	}
	b, err := json.Marshal(snap)
	if err != nil {
		return nil
	}
	return b
}

// marshalEnterErr returns a JSON-encoded enterErrorResult with no Go error.
func marshalEnterErr(kind, message string) (json.RawMessage, error) {
	out := enterErrorResult{Error: kind, Message: message}
	return json.Marshal(out)
}

// ErrNotInPlanMode is returned by ExitTool and approval RPCs when the
// session is not currently in plan_mode.
var ErrNotInPlanMode = errors.New("planmode: session is not in plan_mode")
