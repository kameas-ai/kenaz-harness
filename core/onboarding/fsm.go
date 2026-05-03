package onboarding

import (
	"context"
	"fmt"
	"time"
)

// State identifies which step of the onboarding flow the user is in.
type State string

const (
	// StateWelcome is the initial state — a greeting and a single "get started" CTA.
	StateWelcome State = "welcome"
	// StatePickProviderKind lets the user choose which LLM provider to configure.
	StatePickProviderKind State = "pick_provider_kind"
	// StateEnterAPIKey collects the API key for the chosen provider.
	StateEnterAPIKey State = "enter_api_key"
	// StateTestConnection is an in-flight state while the key is being verified.
	StateTestConnection State = "test_connection"
	// StateDone is the terminal state after a successful connection test.
	StateDone State = "done"
)

// Event is the machine-readable identifier for a user or system action.
type Event string

const (
	// EventNext advances from welcome → pick-provider-kind.
	EventNext Event = "next"
	// EventBack goes back one step (test-connection → enter-api-key, or
	// enter-api-key → pick-provider-kind).
	EventBack Event = "back"
	// EventFinish is the terminal action from the done state.
	EventFinish Event = "finish"
	// EventSubmitKey transitions enter-api-key → test-connection and
	// triggers the LLMTester call.
	EventSubmitKey Event = "submit_key"
	// EventTestOK is emitted internally (or by callers driving the FSM
	// asynchronously) when the connection test succeeds.
	EventTestOK Event = "test_ok"
	// EventTestFail is emitted when the connection test fails; the FSM
	// decrements the retry counter and either loops back to enter-api-key
	// or surfaces a hard failure card.
	EventTestFail Event = "test_fail"
)

const (
	// MaxRetries is the number of additional attempts allowed after the first
	// test-connection failure. On the (MaxRetries+1)th failure the FSM still
	// returns to enter-api-key so the user can correct the key; there is no
	// hard-stop terminal error state — the user can always try again.
	MaxRetries = 3
	// testDeadline is the per-attempt timeout for the LLMTester call.
	testDeadline = 30 * time.Second
)

// LLMTester is the interface the FSM uses to verify a provider's API key.
// In production it is satisfied by core/llm/registry.Registry (or a thin
// wrapper); in tests it is satisfied by a mock.
//
// TestProvider should return nil on success and a descriptive error on
// failure (auth failure, network error, quota exceeded, etc.).
type LLMTester interface {
	TestProvider(ctx context.Context, kind ProviderKind, apiKey string) error
}

// FSMContext carries the mutable context that accumulates as the user
// progresses through the flow. It is held by the caller (not the FSM)
// because the FSM is stateless from the chassis perspective.
type FSMContext struct {
	// ChosenKind is the provider the user selected in pick-provider-kind.
	ChosenKind ProviderKind
	// APIKey is the key entered by the user (cleared to "" after a successful test).
	APIKey string
	// RetriesLeft is decremented on each test-connection failure.
	// Starts at MaxRetries; stepping below 0 is capped at 0.
	RetriesLeft int
	// LastError is the human-readable error from the most recent failed test.
	LastError string
}

// NewFSMContext returns a fresh FSMContext with all retry budget available.
func NewFSMContext() FSMContext {
	return FSMContext{RetriesLeft: MaxRetries}
}

// StepResult bundles the next state and its rendered card.
type StepResult struct {
	State State
	Card  Card
}

// FSM is the onboarding finite-state machine. It is stateless: all mutable
// data lives in FSMContext, which the caller holds across requests.
type FSM struct {
	tester LLMTester
}

// New constructs an FSM. tester may be nil, in which case the
// test-connection state always succeeds (useful for embedding in contexts
// where no real LLM is available, like a guided demo).
func New(tester LLMTester) *FSM {
	return &FSM{tester: tester}
}

// InitialCard returns the card for the initial state without consuming an event.
// Callers use this to populate the first screen without sending a dummy event.
func InitialCard() StepResult {
	return StepResult{
		State: StateWelcome,
		Card:  renderWelcome(),
	}
}

// Step advances the FSM: given the current state, an event, the mutable
// context, and (optionally) the parent context for deadline propagation, it:
//
//  1. Validates the (state, event) pair.
//  2. Executes any side effects (the LLMTester call on EventSubmitKey).
//  3. Mutates fsmCtx to reflect the transition.
//  4. Returns the new State and the Card to render.
//
// Step is the only method that mutates fsmCtx. All other behaviour is
// pure: the caller supplies the full context and receives a self-describing
// Card back.
func (f *FSM) Step(
	ctx context.Context,
	current State,
	event Event,
	payload map[string]string, // optional field values from the frontend
	fsmCtx *FSMContext,
) (StepResult, error) {
	switch current {
	case StateWelcome:
		return f.stepWelcome(event, fsmCtx)
	case StatePickProviderKind:
		return f.stepPickProviderKind(event, payload, fsmCtx)
	case StateEnterAPIKey:
		return f.stepEnterAPIKey(ctx, event, payload, fsmCtx)
	case StateTestConnection:
		return f.stepTestConnection(event, fsmCtx)
	case StateDone:
		return f.stepDone(event, fsmCtx)
	default:
		return StepResult{}, fmt.Errorf("onboarding: unknown state %q", current)
	}
}

// stepWelcome handles transitions from the welcome state.
func (f *FSM) stepWelcome(event Event, fsmCtx *FSMContext) (StepResult, error) {
	switch event {
	case EventNext:
		return StepResult{
			State: StatePickProviderKind,
			Card:  renderPickProviderKind(),
		}, nil
	default:
		return StepResult{}, unsupportedEvent(StateWelcome, event)
	}
}

// stepPickProviderKind handles transitions from the pick-provider-kind state.
func (f *FSM) stepPickProviderKind(event Event, payload map[string]string, fsmCtx *FSMContext) (StepResult, error) {
	switch event {
	case EventBack:
		fsmCtx.ChosenKind = ""
		return StepResult{
			State: StateWelcome,
			Card:  renderWelcome(),
		}, nil
	default:
		// The event ID doubles as the chosen provider kind.
		kind := ProviderKind(event)
		if !isKnownKind(kind) {
			return StepResult{}, fmt.Errorf("onboarding: unknown provider kind %q", kind)
		}
		fsmCtx.ChosenKind = kind
		fsmCtx.RetriesLeft = MaxRetries
		fsmCtx.LastError = ""
		return StepResult{
			State: StateEnterAPIKey,
			Card:  renderEnterAPIKey(kind, ""),
		}, nil
	}
}

// stepEnterAPIKey handles transitions from the enter-api-key state.
// On EventSubmitKey it invokes the LLMTester inline (blocking) with a 30 s
// deadline derived from ctx.
func (f *FSM) stepEnterAPIKey(
	ctx context.Context,
	event Event,
	payload map[string]string,
	fsmCtx *FSMContext,
) (StepResult, error) {
	switch event {
	case EventBack:
		fsmCtx.APIKey = ""
		fsmCtx.LastError = ""
		return StepResult{
			State: StatePickProviderKind,
			Card:  renderPickProviderKind(),
		}, nil

	case EventSubmitKey:
		key := payload["api_key"]
		if key == "" {
			return StepResult{}, fmt.Errorf("onboarding: api_key must not be empty")
		}
		fsmCtx.APIKey = key

		// Run the connection test.
		testCtx, cancel := context.WithTimeout(ctx, testDeadline)
		defer cancel()

		var testErr error
		if f.tester != nil {
			testErr = f.tester.TestProvider(testCtx, fsmCtx.ChosenKind, key)
		}

		if testErr == nil {
			// Success — clear the key (it will be stored by the caller) and
			// advance to done.
			fsmCtx.LastError = ""
			fsmCtx.RetriesLeft = MaxRetries
			return StepResult{
				State: StateDone,
				Card:  renderDone(fsmCtx.ChosenKind),
			}, nil
		}

		// Failure — decrement retry counter and return to enter-api-key with
		// the error surfaced in the Card. We never block the user from
		// retrying beyond MaxRetries (they are free to try again after the
		// counter hits 0); we simply always return them to enter-api-key so
		// they can correct the key.
		if fsmCtx.RetriesLeft > 0 {
			fsmCtx.RetriesLeft--
		}
		fsmCtx.LastError = testErr.Error()
		return StepResult{
			State: StateEnterAPIKey,
			Card:  renderEnterAPIKey(fsmCtx.ChosenKind, fsmCtx.LastError),
		}, nil

	default:
		return StepResult{}, unsupportedEvent(StateEnterAPIKey, event)
	}
}

// stepTestConnection handles transitions from the test-connection state.
// This state exists as a semantic placeholder for asynchronous callers
// that split the test into two RPCs (submit → poll). The inline path in
// stepEnterAPIKey never actually lands here. For completeness:
func (f *FSM) stepTestConnection(event Event, fsmCtx *FSMContext) (StepResult, error) {
	switch event {
	case EventTestOK:
		fsmCtx.LastError = ""
		return StepResult{
			State: StateDone,
			Card:  renderDone(fsmCtx.ChosenKind),
		}, nil
	case EventTestFail:
		if fsmCtx.RetriesLeft > 0 {
			fsmCtx.RetriesLeft--
		}
		return StepResult{
			State: StateEnterAPIKey,
			Card:  renderEnterAPIKey(fsmCtx.ChosenKind, fsmCtx.LastError),
		}, nil
	case EventBack:
		return StepResult{
			State: StateEnterAPIKey,
			Card:  renderEnterAPIKey(fsmCtx.ChosenKind, ""),
		}, nil
	default:
		return StepResult{}, unsupportedEvent(StateTestConnection, event)
	}
}

// stepDone handles transitions from the done state.
func (f *FSM) stepDone(event Event, fsmCtx *FSMContext) (StepResult, error) {
	switch event {
	case EventFinish:
		// Terminal — return the same done card; the caller closes the flow.
		return StepResult{
			State: StateDone,
			Card:  renderDone(fsmCtx.ChosenKind),
		}, nil
	default:
		return StepResult{}, unsupportedEvent(StateDone, event)
	}
}

// isKnownKind reports whether k is one of the supported ProviderKinds.
func isKnownKind(k ProviderKind) bool {
	for _, s := range SupportedProviders {
		if s == k {
			return true
		}
	}
	return false
}

// unsupportedEvent formats a standard error for unexpected (state, event) pairs.
func unsupportedEvent(state State, event Event) error {
	return fmt.Errorf("onboarding: event %q not valid in state %q", event, state)
}
