package workflows

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// runnerRegistry is mutated only by RegisterStepRunner at init time.
var runnerRegistry = map[StepKind]StepRunner{
	StepKindModelTurn: stubModelTurn{},
	StepKindShell:     shellRunner{},
}

// DefaultRunners returns the package-default StepRunner registry.
// Callers (notably the rpc impl) inject this into Engine.Runners.
func DefaultRunners() map[StepKind]StepRunner {
	out := make(map[StepKind]StepRunner, len(runnerRegistry))
	for k, v := range runnerRegistry {
		out[k] = v
	}
	return out
}

// RegisterStepRunner installs runner for kind at process start. Future
// WPs that ship a real model_turn or http_request runner call this
// from their package init.
func RegisterStepRunner(kind StepKind, runner StepRunner) {
	runnerRegistry[kind] = runner
}

// stubModelTurn is the v0.3.0-beta placeholder for the LLM-backed
// model_turn runner. WP03 swaps this for the real Registry.Stream
// dispatch. Until then we echo the prompt with a banner so the
// frontend has a non-empty visual surface to render.
type stubModelTurn struct{}

func (stubModelTurn) Validate(st Step) error {
	if st.UserPrompt == "" {
		return fmt.Errorf("model_turn step %q: user_prompt required", st.Name)
	}
	return nil
}

func (stubModelTurn) Run(_ context.Context, st Step, _ *RunContext) (TypedValue, error) {
	return TypedValue{
		Type: ValueTypeText,
		Text: "[model_turn stub] " + st.UserPrompt,
	}, nil
}

// shellRunner runs Cmd + Args via os/exec. It enforces TimeoutMS
// (defaulting to 30s) and rejects empty Cmd. v0.3.0-beta scope: this
// does NOT route through core/tools/bash's Cedar gate. WP04 will swap
// in the gated dispatcher.
type shellRunner struct{}

func (shellRunner) Validate(st Step) error {
	if st.Cmd == "" {
		return fmt.Errorf("shell step %q: cmd required", st.Name)
	}
	return nil
}

func (shellRunner) Run(ctx context.Context, st Step, _ *RunContext) (TypedValue, error) {
	timeout := time.Duration(st.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, st.Cmd, st.Args...)
	if st.Cwd != "" {
		cmd.Dir = st.Cwd
	}
	out, err := cmd.CombinedOutput()
	text := strings.TrimRight(string(out), "\n")
	if err != nil {
		return TypedValue{Type: ValueTypeText, Text: text}, fmt.Errorf("shell step %q: %w", st.Name, err)
	}
	return TypedValue{Type: ValueTypeText, Text: text}, nil
}
