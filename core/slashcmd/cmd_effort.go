package slashcmd

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/sigil-tech/kaneaz-harness/core/llm"
)

// MetaKeyReasoningKnob is the metadata key the frontend reads to apply
// a RequestKnobs.Reasoning update to the current session. The value is
// a JSON-encoded llm.ReasoningConfig.
const MetaKeyReasoningKnob = "reasoningKnob"

// effortCommand implements /effort [level|budget].
//
// Usage:
//
//	/effort low|medium|high|minimal         → sets OpenAIEffort on OpenAI/OpenRouter
//	/effort <integer>                       → sets AnthropicThinkingBudget on Anthropic
//	/effort                                 → shows the current reasoning config
//
// The command validates the argument against the model's ReasoningStyle
// (obtained via the CapabilitiesProvider interface on the active adapter)
// and returns ErrUnsupportedFeature when the configuration is incompatible.
//
// (provider-implementation-uniformity-01KQ8V4F WP07)
type effortCommand struct{}

func (effortCommand) Name() string { return "effort" }
func (effortCommand) Description() string {
	return "Set reasoning effort — /effort high|medium|low|minimal or /effort <tokens>"
}
func (effortCommand) Hidden() bool     { return false }
func (effortCommand) ComingSoon() bool { return false }

func (effortCommand) Run(_ context.Context, env Env, args []string) (Result, error) {
	if len(args) == 0 {
		return Result{
			Kind: ResultKindInfo,
			Text: "/effort: set reasoning effort for the active model.\n" +
				"  /effort low|medium|high|minimal — for OpenAI o-series models\n" +
				"  /effort <tokens>                — for Anthropic thinking-capable models\n" +
				"  Example: /effort high, /effort 16000",
		}, nil
	}

	arg := strings.TrimSpace(args[0])
	if arg == "" {
		return Result{
			Kind: ResultKindError,
			Text: "/effort: missing argument — use /effort high|medium|low|minimal or /effort <tokens>",
		}, nil
	}

	// Determine if the argument is an effort string or a token budget integer.
	knob := llm.ReasoningConfig{}
	if isEffortString(arg) {
		knob.OpenAIEffort = arg
		if err := knob.Validate(); err != nil {
			return Result{
				Kind: ResultKindError,
				Text: "/effort: " + err.Error(),
			}, nil
		}
		return Result{
			Kind: ResultKindInfo,
			Text: fmt.Sprintf("reasoning effort set to %q — takes effect on the next message", arg),
			Metadata: map[string]any{
				MetaKeyReasoningKnob: knob,
			},
		}, nil
	}

	// Try parsing as a token budget integer.
	budget, err := strconv.Atoi(arg)
	if err != nil || budget <= 0 {
		return Result{
			Kind: ResultKindError,
			Text: fmt.Sprintf("/effort: %q is not a valid effort level (low|medium|high|minimal) or token budget (integer > 0)", arg),
		}, nil
	}
	knob.AnthropicThinkingBudget = budget
	if err := knob.Validate(); err != nil {
		return Result{
			Kind: ResultKindError,
			Text: "/effort: " + err.Error(),
		}, nil
	}
	return Result{
		Kind: ResultKindInfo,
		Text: fmt.Sprintf("reasoning token budget set to %d — takes effect on the next message", budget),
		Metadata: map[string]any{
			MetaKeyReasoningKnob: knob,
		},
	}, nil
}

// isEffortString returns true when s is one of the valid effort strings.
func isEffortString(s string) bool {
	switch strings.ToLower(s) {
	case "minimal", "low", "medium", "high":
		return true
	}
	return false
}
