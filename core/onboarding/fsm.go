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
	// StateAccountStep is the optional account/federation-switch step (WP03).
	// Always skippable — OSS-standalone users press "Continue without account".
	// INVARIANT: the account step is ALWAYS skippable; the harness must be
	// fully usable with no Fleet account (FR-003, NFR OSS-standalone).
	StateAccountStep State = "account_step"
	// StateGuidedAction is the final step that concludes onboarding on a
	// concrete first task rather than a dead confirmation screen (FR-007 WP06).
	StateGuidedAction State = "guided_action"
	// StateDone is the terminal state after the full onboarding flow completes.
	StateDone State = "done"
)

// Event is the machine-readable identifier for a user or system action.
type Event string

const (
	// EventNext advances from welcome → pick-provider-kind, and from
	// account_done / guided_action to the next step.
	EventNext Event = "next"
	// EventBack goes back one step (test-connection → enter-api-key, or
	// enter-api-key → pick-provider-kind).
	EventBack Event = "back"
	// EventFinish is the terminal action from the done / guided_action state.
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

	// EventSignIn triggers the owned-login sign-in flow from account_step.
	// The FSM delegates to AccountSigner; on success transitions to guided_action.
	EventSignIn Event = "sign_in"
	// EventSkipAccount skips the account step without signing in.
	// OSS-standalone path — the harness is fully usable without an account.
	// INVARIANT: this event must always be accepted in StateAccountStep.
	EventSkipAccount Event = "skip_account"

	// EventStartNewChat is the "start a new conversation" CTA from guided_action.
	EventStartNewChat Event = "start_new_chat"
	// EventOpenSettings is the "explore settings" CTA from guided_action.
	EventOpenSettings Event = "open_settings"
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

// AccountSigner drives the optional owned-login sign-in flow (WP03).
// In production it is satisfied by a thin wrapper over the fleet auth
// surface in core/rpc/views/settings (already allowlisted for fleet
// imports). The FSM calls SignIn in the account_step state.
//
// SEAM: this interface is the fleet integration point for the account step.
// Packages that must remain fleet-free implement a no-op or a stub that
// always returns ErrAccountNotConfigured. The interface itself carries no
// fleet import — the production binding lives in core/rpc/onboarding_wiring.go
// which IS allowlisted for fleet imports.
//
// INVARIANT: a nil AccountSigner means fleet is absent. The FSM treats this
// as "sign-in unavailable" — EventSignIn returns to guided_action with a
// descriptive error; EventSkipAccount still succeeds (OSS-standalone path).
type AccountSigner interface {
	// SignIn opens the owned-login browser flow and blocks until the user
	// completes or cancels authentication. On success returns a non-empty
	// email. On cancellation or error returns a non-nil error.
	SignIn(ctx context.Context) (email string, err error)
}

// SessionKindTransitioner transitions a session's Kind when the onboarding
// FSM reaches its terminal state (harness-self-mcp-onboarding-01KQ8TDU
// WP09). In production it is satisfied by session.Manager.SetKind; in
// tests it is satisfied by a mock or left nil (nil-safe: the FSM skips
// the call when no transitioner is configured).
type SessionKindTransitioner interface {
	SetKind(ctx context.Context, sessionID, kind string) error
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
	// SessionID is the id of the onboarding session. When set, the FSM
	// transitions its Kind from "onboarding" → "chat" on terminal state
	// via the configured SessionKindTransitioner (WP09).
	SessionID string

	// ── Account step fields (WP03) ─────────────────────────────────────────
	//
	// SignedIn is true after a successful sign-in in account_step. Used by
	// the guided_action step to surface context-bootstrap hints for Team+.
	SignedIn bool
	// SignedInEmail is the email address returned by the sign-in flow.
	// Empty when signed out or when sign-in was skipped.
	SignedInEmail string
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
	tester       LLMTester
	transitioner SessionKindTransitioner
	// signer drives the optional account/sign-in step (WP03).
	// nil means fleet is absent — EventSignIn is unavailable; EventSkipAccount
	// still succeeds so the OSS-standalone path is always reachable.
	signer AccountSigner
}

// New constructs an FSM. tester may be nil, in which case the
// test-connection state always succeeds (useful for embedding in contexts
// where no real LLM is available, like a guided demo).
func New(tester LLMTester) *FSM {
	return &FSM{tester: tester}
}

// NewWithTransitioner constructs an FSM with a SessionKindTransitioner that
// is called when the FSM reaches its terminal state (WP09). transitioner may
// be nil — the FSM then skips the kind transition (backwards-compatible).
func NewWithTransitioner(tester LLMTester, transitioner SessionKindTransitioner) *FSM {
	return &FSM{tester: tester, transitioner: transitioner}
}

// NewFull constructs an FSM with the full production dependency set for the
// extended onboarding flow (harness-onboarding-01NHON01). Any argument may
// be nil; nil dependencies cause the corresponding step to degrade gracefully
// rather than hard-failing.
func NewFull(tester LLMTester, transitioner SessionKindTransitioner, signer AccountSigner) *FSM {
	return &FSM{tester: tester, transitioner: transitioner, signer: signer}
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
	case StateAccountStep:
		return f.stepAccountStep(ctx, event, fsmCtx)
	case StateGuidedAction:
		return f.stepGuidedAction(ctx, event, fsmCtx)
	case StateDone:
		return f.stepDone(ctx, event, fsmCtx)
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
			// advance to the account step so the user can optionally sign in
			// to Fleet before the guided action concludes onboarding (WP03).
			fsmCtx.LastError = ""
			fsmCtx.RetriesLeft = MaxRetries
			return StepResult{
				State: StateAccountStep,
				Card:  renderAccountStep(),
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
			State: StateAccountStep,
			Card:  renderAccountStep(),
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
//
// StateDone is terminal — no further transitions are supported. The caller
// closes the flow when it receives StateDone from stepGuidedAction.
// This handler exists for legacy callers that send EventFinish directly
// to StateDone (e.g. test fixtures that bypassed the guided_action step).
func (f *FSM) stepDone(ctx context.Context, event Event, fsmCtx *FSMContext) (StepResult, error) {
	switch event {
	case EventFinish:
		// Phase-2 handoff: transition session kind onboarding → chat.
		// Also fires here for legacy callers that land on done before
		// guided_action was introduced.
		if f.transitioner != nil && fsmCtx.SessionID != "" {
			// Non-fatal: a DB error here should not abort the UX flow.
			_ = f.transitioner.SetKind(ctx, fsmCtx.SessionID, "chat")
		}
		// Terminal — return the same done card; the caller closes the flow.
		return StepResult{
			State: StateDone,
			Card:  renderDone(fsmCtx.ChosenKind),
		}, nil
	default:
		return StepResult{}, unsupportedEvent(StateDone, event)
	}
}

// stepAccountStep handles transitions from the account_step state (WP03).
//
// INVARIANT: EventSkipAccount MUST always succeed in this state regardless of
// whether a signer is configured. The OSS-standalone path depends on it.
//
// On EventSignIn with a nil signer the FSM advances directly to guided_action
// with a note that sign-in is unavailable rather than returning an error —
// this prevents the account step from becoming a hard blocker on fleet-free
// builds.
func (f *FSM) stepAccountStep(ctx context.Context, event Event, fsmCtx *FSMContext) (StepResult, error) {
	switch event {
	case EventSkipAccount:
		// OSS-standalone path — always allowed (INVARIANT).
		fsmCtx.SignedIn = false
		fsmCtx.SignedInEmail = ""
		return StepResult{
			State: StateGuidedAction,
			Card:  renderGuidedAction(),
		}, nil

	case EventSignIn:
		// SEAM: fleet integration point — deferred to production AccountSigner.
		// When signer is nil (fleet absent / OSS build), degrade to guided_action.
		if f.signer == nil {
			// Fleet is not configured — treat as skip (graceful degradation).
			// Do NOT return an error: the dialog must still advance.
			fsmCtx.SignedIn = false
			return StepResult{
				State: StateGuidedAction,
				Card:  renderGuidedAction(),
			}, nil
		}
		email, err := f.signer.SignIn(ctx)
		if err != nil {
			// Sign-in failed or was cancelled — stay in account_step with the
			// error surfaced so the user can retry or skip.
			return StepResult{
				State: StateAccountStep,
				Card: Card{
					Title:        renderAccountStep().Title,
					Body:         renderAccountStep().Body,
					Actions:      renderAccountStep().Actions,
					ErrorMessage: fmt.Sprintf("Sign-in failed: %v", err),
				},
			}, nil
		}
		fsmCtx.SignedIn = true
		fsmCtx.SignedInEmail = email
		return StepResult{
			State: StateGuidedAction,
			Card:  renderGuidedAction(),
		}, nil

	case EventBack:
		// Allow going back to provider setup if the user wants to change it.
		return StepResult{
			State: StatePickProviderKind,
			Card:  renderPickProviderKind(),
		}, nil

	default:
		return StepResult{}, unsupportedEvent(StateAccountStep, event)
	}
}

// stepGuidedAction handles transitions from the guided_action state (WP06).
// This step concludes onboarding on a concrete first task (FR-007).
func (f *FSM) stepGuidedAction(ctx context.Context, event Event, fsmCtx *FSMContext) (StepResult, error) {
	switch event {
	case EventStartNewChat, EventOpenSettings, EventFinish:
		// All three CTAs from the guided-action card conclude the flow.
		// Phase-2 handoff: transition session kind onboarding → chat.
		if f.transitioner != nil && fsmCtx.SessionID != "" {
			// Non-fatal: a DB error here should not abort the UX flow.
			_ = f.transitioner.SetKind(ctx, fsmCtx.SessionID, "chat")
		}
		return StepResult{
			State: StateDone,
			Card:  renderDone(fsmCtx.ChosenKind),
		}, nil
	default:
		return StepResult{}, unsupportedEvent(StateGuidedAction, event)
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
