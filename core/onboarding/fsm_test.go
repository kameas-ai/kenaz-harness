package onboarding_test

import (
	"context"
	"errors"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/onboarding"
)

// mockTester is a configurable LLMTester for unit tests.
type mockTester struct {
	// calls records each TestProvider invocation.
	calls []mockCall
	// results is popped FIFO; if empty, subsequent calls return nil.
	results []error
}

type mockCall struct {
	kind   onboarding.ProviderKind
	apiKey string
}

func (m *mockTester) TestProvider(_ context.Context, kind onboarding.ProviderKind, apiKey string) error {
	m.calls = append(m.calls, mockCall{kind: kind, apiKey: apiKey})
	if len(m.results) == 0 {
		return nil
	}
	err := m.results[0]
	m.results = m.results[1:]
	return err
}

// errAuth is a representative auth-failure error returned by the mock.
var errAuth = errors.New("401 Unauthorized: invalid API key")

// submitKeyPayload is a convenience for entering an API key.
func submitKeyPayload(key string) map[string]string {
	return map[string]string{"api_key": key}
}

// TestTransitions exercises every (state, event) pair in the FSM.
func TestTransitions(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name        string
		state       onboarding.State
		event       onboarding.Event
		payload     map[string]string
		setupCtx    func() *onboarding.FSMContext
		tester      onboarding.LLMTester // interface — nil means no tester
		wantState   onboarding.State
		wantErrMsg  string // substring that must appear in card ErrorMessage
		wantErr     bool   // Step itself returns an error
	}{
		// ── welcome ──────────────────────────────────────────────────────────
		{
			name:      "welcome/next → pick-provider-kind",
			state:     onboarding.StateWelcome,
			event:     onboarding.EventNext,
			wantState: onboarding.StatePickProviderKind,
		},
		{
			name:      "welcome/unknown-event → error",
			state:     onboarding.StateWelcome,
			event:     onboarding.EventBack,
			wantErr:   true,
		},

		// ── pick-provider-kind ────────────────────────────────────────────────
		{
			name:      "pick-provider-kind/anthropic → enter-api-key",
			state:     onboarding.StatePickProviderKind,
			event:     onboarding.Event(onboarding.ProviderAnthropic),
			wantState: onboarding.StateEnterAPIKey,
		},
		{
			name:      "pick-provider-kind/openai → enter-api-key",
			state:     onboarding.StatePickProviderKind,
			event:     onboarding.Event(onboarding.ProviderOpenAI),
			wantState: onboarding.StateEnterAPIKey,
		},
		{
			name:      "pick-provider-kind/openrouter → enter-api-key",
			state:     onboarding.StatePickProviderKind,
			event:     onboarding.Event(onboarding.ProviderOpenRouter),
			wantState: onboarding.StateEnterAPIKey,
		},
		{
			name:      "pick-provider-kind/back → welcome",
			state:     onboarding.StatePickProviderKind,
			event:     onboarding.EventBack,
			wantState: onboarding.StateWelcome,
		},
		{
			name:    "pick-provider-kind/unknown-kind → error",
			state:   onboarding.StatePickProviderKind,
			event:   onboarding.Event("badprovider"),
			wantErr: true,
		},

		// ── enter-api-key ─────────────────────────────────────────────────────
		{
			name:  "enter-api-key/submit-key success → account_step",
			state: onboarding.StateEnterAPIKey,
			event: onboarding.EventSubmitKey,
			payload: submitKeyPayload("sk-ant-good"),
			setupCtx: func() *onboarding.FSMContext {
				c := onboarding.NewFSMContext()
				c.ChosenKind = onboarding.ProviderAnthropic
				return &c
			},
			tester:    &mockTester{results: []error{nil}},
			wantState: onboarding.StateAccountStep,
		},
		{
			name:  "enter-api-key/submit-key failure → enter-api-key with error",
			state: onboarding.StateEnterAPIKey,
			event: onboarding.EventSubmitKey,
			payload: submitKeyPayload("sk-ant-bad"),
			setupCtx: func() *onboarding.FSMContext {
				c := onboarding.NewFSMContext()
				c.ChosenKind = onboarding.ProviderAnthropic
				return &c
			},
			tester:     &mockTester{results: []error{errAuth}},
			wantState:  onboarding.StateEnterAPIKey,
			wantErrMsg: "401",
		},
		{
			name:  "enter-api-key/back → pick-provider-kind",
			state: onboarding.StateEnterAPIKey,
			event: onboarding.EventBack,
			setupCtx: func() *onboarding.FSMContext {
				c := onboarding.NewFSMContext()
				c.ChosenKind = onboarding.ProviderOpenAI
				return &c
			},
			tester:    &mockTester{},
			wantState: onboarding.StatePickProviderKind,
		},
		{
			name:  "enter-api-key/empty-key → error",
			state: onboarding.StateEnterAPIKey,
			event: onboarding.EventSubmitKey,
			payload: submitKeyPayload(""),
			setupCtx: func() *onboarding.FSMContext {
				c := onboarding.NewFSMContext()
				c.ChosenKind = onboarding.ProviderAnthropic
				return &c
			},
			tester:  &mockTester{},
			wantErr: true,
		},
		{
			name:  "enter-api-key/nil-tester success → account_step",
			state: onboarding.StateEnterAPIKey,
			event: onboarding.EventSubmitKey,
			payload: submitKeyPayload("any-key"),
			setupCtx: func() *onboarding.FSMContext {
				c := onboarding.NewFSMContext()
				c.ChosenKind = onboarding.ProviderOpenRouter
				return &c
			},
			tester:    nil, // interface nil — FSM treats as "no tester, always pass"
			wantState: onboarding.StateAccountStep,
		},
		{
			name:  "enter-api-key/unknown-event → error",
			state: onboarding.StateEnterAPIKey,
			event: onboarding.EventNext,
			setupCtx: func() *onboarding.FSMContext {
				c := onboarding.NewFSMContext()
				c.ChosenKind = onboarding.ProviderAnthropic
				return &c
			},
			tester:  &mockTester{},
			wantErr: true,
		},

		// ── test-connection ───────────────────────────────────────────────────
		{
			name:  "test-connection/test-ok → account_step",
			state: onboarding.StateTestConnection,
			event: onboarding.EventTestOK,
			setupCtx: func() *onboarding.FSMContext {
				c := onboarding.NewFSMContext()
				c.ChosenKind = onboarding.ProviderAnthropic
				return &c
			},
			wantState: onboarding.StateAccountStep,
		},
		{
			name:  "test-connection/test-fail → enter-api-key",
			state: onboarding.StateTestConnection,
			event: onboarding.EventTestFail,
			setupCtx: func() *onboarding.FSMContext {
				c := onboarding.NewFSMContext()
				c.ChosenKind = onboarding.ProviderAnthropic
				c.LastError = "timeout"
				return &c
			},
			wantState: onboarding.StateEnterAPIKey,
		},
		{
			name:  "test-connection/back → enter-api-key",
			state: onboarding.StateTestConnection,
			event: onboarding.EventBack,
			setupCtx: func() *onboarding.FSMContext {
				c := onboarding.NewFSMContext()
				c.ChosenKind = onboarding.ProviderAnthropic
				return &c
			},
			wantState: onboarding.StateEnterAPIKey,
		},
		{
			name:  "test-connection/unknown-event → error",
			state: onboarding.StateTestConnection,
			event: onboarding.EventNext,
			setupCtx: func() *onboarding.FSMContext {
				c := onboarding.NewFSMContext()
				c.ChosenKind = onboarding.ProviderAnthropic
				return &c
			},
			wantErr: true,
		},

		// ── account_step (WP03) ──────────────────────────────────────────────
		{
			name:  "account_step/skip → guided_action (OSS-standalone invariant)",
			state: onboarding.StateAccountStep,
			event: onboarding.EventSkipAccount,
			setupCtx: func() *onboarding.FSMContext {
				c := onboarding.NewFSMContext()
				c.ChosenKind = onboarding.ProviderAnthropic
				return &c
			},
			wantState: onboarding.StateGuidedAction,
		},
		{
			name:  "account_step/sign-in (nil signer) → guided_action",
			state: onboarding.StateAccountStep,
			event: onboarding.EventSignIn,
			setupCtx: func() *onboarding.FSMContext {
				c := onboarding.NewFSMContext()
				c.ChosenKind = onboarding.ProviderAnthropic
				return &c
			},
			wantState: onboarding.StateGuidedAction,
		},
		{
			name:  "account_step/unknown-event → error",
			state: onboarding.StateAccountStep,
			event: onboarding.EventNext, // "next" is not valid in account_step
			setupCtx: func() *onboarding.FSMContext {
				c := onboarding.NewFSMContext()
				c.ChosenKind = onboarding.ProviderAnthropic
				return &c
			},
			wantErr: true,
		},

		// ── guided_action (WP06) ─────────────────────────────────────────────
		{
			name:  "guided_action/start-new-chat → done",
			state: onboarding.StateGuidedAction,
			event: onboarding.EventStartNewChat,
			setupCtx: func() *onboarding.FSMContext {
				c := onboarding.NewFSMContext()
				return &c
			},
			wantState: onboarding.StateDone,
		},
		{
			name:  "guided_action/open-settings → done",
			state: onboarding.StateGuidedAction,
			event: onboarding.EventOpenSettings,
			setupCtx: func() *onboarding.FSMContext {
				c := onboarding.NewFSMContext()
				return &c
			},
			wantState: onboarding.StateDone,
		},
		{
			name:  "guided_action/finish → done",
			state: onboarding.StateGuidedAction,
			event: onboarding.EventFinish,
			setupCtx: func() *onboarding.FSMContext {
				c := onboarding.NewFSMContext()
				return &c
			},
			wantState: onboarding.StateDone,
		},
		{
			name:  "guided_action/unknown-event → error",
			state: onboarding.StateGuidedAction,
			event: onboarding.EventBack,
			setupCtx: func() *onboarding.FSMContext {
				c := onboarding.NewFSMContext()
				return &c
			},
			wantErr: true,
		},

		// ── done ──────────────────────────────────────────────────────────────
		{
			name:  "done/finish → done (terminal no-op)",
			state: onboarding.StateDone,
			event: onboarding.EventFinish,
			setupCtx: func() *onboarding.FSMContext {
				c := onboarding.NewFSMContext()
				c.ChosenKind = onboarding.ProviderAnthropic
				return &c
			},
			wantState: onboarding.StateDone,
		},
		{
			name:  "done/unknown-event → error",
			state: onboarding.StateDone,
			event: onboarding.EventBack,
			setupCtx: func() *onboarding.FSMContext {
				c := onboarding.NewFSMContext()
				c.ChosenKind = onboarding.ProviderAnthropic
				return &c
			},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var fsmCtx *onboarding.FSMContext
			if tc.setupCtx != nil {
				fsmCtx = tc.setupCtx()
			} else {
				c := onboarding.NewFSMContext()
				fsmCtx = &c
			}

			fsm := onboarding.New(tc.tester)
			result, err := fsm.Step(ctx, tc.state, tc.event, tc.payload, fsmCtx)

			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (state=%q card=%+v)", result.State, result.Card)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.State != tc.wantState {
				t.Errorf("state = %q, want %q", result.State, tc.wantState)
			}
			if tc.wantErrMsg != "" && result.Card.ErrorMessage == "" {
				t.Errorf("card.ErrorMessage is empty, want substring %q", tc.wantErrMsg)
			}
			if tc.wantErrMsg != "" {
				if !contains(result.Card.ErrorMessage, tc.wantErrMsg) {
					t.Errorf("card.ErrorMessage = %q, want substring %q", result.Card.ErrorMessage, tc.wantErrMsg)
				}
			}
		})
	}
}

// TestRetryCounter verifies that 3 consecutive failures correctly decrement
// the retry counter and that the card carries the error on each attempt.
func TestRetryCounter(t *testing.T) {
	ctx := context.Background()

	// Set up: 3 failures followed by success.
	tester := &mockTester{
		results: []error{errAuth, errAuth, errAuth, nil},
	}
	fsm := onboarding.New(tester)

	// Seed context: user has already picked Anthropic.
	fsmCtx := onboarding.NewFSMContext()
	fsmCtx.ChosenKind = onboarding.ProviderAnthropic

	payload := submitKeyPayload("sk-ant-bad")
	goodPayload := submitKeyPayload("sk-ant-good")

	// Attempt 1 — fail. RetriesLeft should drop from 3 → 2.
	r1, err := fsm.Step(ctx, onboarding.StateEnterAPIKey, onboarding.EventSubmitKey, payload, &fsmCtx)
	if err != nil {
		t.Fatalf("attempt 1: unexpected error: %v", err)
	}
	if r1.State != onboarding.StateEnterAPIKey {
		t.Errorf("attempt 1: state = %q, want %q", r1.State, onboarding.StateEnterAPIKey)
	}
	if r1.Card.ErrorMessage == "" {
		t.Error("attempt 1: card.ErrorMessage is empty")
	}
	if fsmCtx.RetriesLeft != 2 {
		t.Errorf("attempt 1: RetriesLeft = %d, want 2", fsmCtx.RetriesLeft)
	}

	// Attempt 2 — fail. RetriesLeft 2 → 1.
	r2, err := fsm.Step(ctx, onboarding.StateEnterAPIKey, onboarding.EventSubmitKey, payload, &fsmCtx)
	if err != nil {
		t.Fatalf("attempt 2: unexpected error: %v", err)
	}
	if r2.State != onboarding.StateEnterAPIKey {
		t.Errorf("attempt 2: state = %q, want %q", r2.State, onboarding.StateEnterAPIKey)
	}
	if fsmCtx.RetriesLeft != 1 {
		t.Errorf("attempt 2: RetriesLeft = %d, want 1", fsmCtx.RetriesLeft)
	}

	// Attempt 3 — fail. RetriesLeft 1 → 0.
	r3, err := fsm.Step(ctx, onboarding.StateEnterAPIKey, onboarding.EventSubmitKey, payload, &fsmCtx)
	if err != nil {
		t.Fatalf("attempt 3: unexpected error: %v", err)
	}
	if r3.State != onboarding.StateEnterAPIKey {
		t.Errorf("attempt 3: state = %q, want %q", r3.State, onboarding.StateEnterAPIKey)
	}
	if r3.Card.ErrorMessage == "" {
		t.Error("attempt 3: card.ErrorMessage is empty")
	}
	if fsmCtx.RetriesLeft != 0 {
		t.Errorf("attempt 3: RetriesLeft = %d, want 0", fsmCtx.RetriesLeft)
	}

	// After 3 failures the user can still try with a corrected key and succeed.
	// The FSM now routes through account_step (WP03) rather than directly to done.
	r4, err := fsm.Step(ctx, onboarding.StateEnterAPIKey, onboarding.EventSubmitKey, goodPayload, &fsmCtx)
	if err != nil {
		t.Fatalf("attempt 4 (success): unexpected error: %v", err)
	}
	if r4.State != onboarding.StateAccountStep {
		t.Errorf("attempt 4: state = %q, want %q", r4.State, onboarding.StateAccountStep)
	}
	if r4.Card.ErrorMessage != "" {
		t.Errorf("attempt 4: card.ErrorMessage should be empty, got %q", r4.Card.ErrorMessage)
	}

	// Confirm the tester was called 4 times total.
	if len(tester.calls) != 4 {
		t.Errorf("tester.calls = %d, want 4", len(tester.calls))
	}
}

// TestInitialCard verifies the bootstrap helper.
func TestInitialCard(t *testing.T) {
	result := onboarding.InitialCard()
	if result.State != onboarding.StateWelcome {
		t.Errorf("state = %q, want %q", result.State, onboarding.StateWelcome)
	}
	if result.Card.Title == "" {
		t.Error("card.Title is empty")
	}
	if len(result.Card.Actions) == 0 {
		t.Error("card.Actions is empty")
	}
}

// TestHappyPath exercises the canonical end-to-end flow.
// Updated for harness-onboarding-01NHON01 WP03/WP06: the FSM now routes
// through account_step (optional) and guided_action before reaching done.
func TestHappyPath(t *testing.T) {
	ctx := context.Background()
	tester := &mockTester{results: []error{nil}}
	fsm := onboarding.New(tester)

	fsmCtx := onboarding.NewFSMContext()

	// welcome → pick-provider-kind
	r, err := fsm.Step(ctx, onboarding.StateWelcome, onboarding.EventNext, nil, &fsmCtx)
	requireOK(t, "welcome/next", err, r, onboarding.StatePickProviderKind)

	// pick-provider-kind → enter-api-key (choose OpenAI)
	r, err = fsm.Step(ctx, onboarding.StatePickProviderKind, onboarding.Event(onboarding.ProviderOpenAI), nil, &fsmCtx)
	requireOK(t, "pick/openai", err, r, onboarding.StateEnterAPIKey)
	if fsmCtx.ChosenKind != onboarding.ProviderOpenAI {
		t.Errorf("ChosenKind = %q, want %q", fsmCtx.ChosenKind, onboarding.ProviderOpenAI)
	}

	// enter-api-key → account_step (provider configured successfully)
	r, err = fsm.Step(ctx, onboarding.StateEnterAPIKey, onboarding.EventSubmitKey, submitKeyPayload("sk-goodkey"), &fsmCtx)
	requireOK(t, "enter/submit", err, r, onboarding.StateAccountStep)

	// account_step → guided_action (skip account — OSS-standalone path)
	r, err = fsm.Step(ctx, onboarding.StateAccountStep, onboarding.EventSkipAccount, nil, &fsmCtx)
	requireOK(t, "account/skip", err, r, onboarding.StateGuidedAction)

	// guided_action → done (user chooses to start a new conversation)
	r, err = fsm.Step(ctx, onboarding.StateGuidedAction, onboarding.EventStartNewChat, nil, &fsmCtx)
	requireOK(t, "guided/start-new-chat", err, r, onboarding.StateDone)

	// done → done (finish — terminal no-op)
	r, err = fsm.Step(ctx, onboarding.StateDone, onboarding.EventFinish, nil, &fsmCtx)
	requireOK(t, "done/finish", err, r, onboarding.StateDone)
}

// TestHappyPathSignIn exercises the full flow with a successful sign-in
// in the account step.
func TestHappyPathSignIn(t *testing.T) {
	ctx := context.Background()
	tester := &mockTester{results: []error{nil}}
	fsm := onboarding.New(tester)

	fsmCtx := onboarding.NewFSMContext()
	fsmCtx.ChosenKind = onboarding.ProviderAnthropic

	// enter-api-key → account_step
	r, err := fsm.Step(ctx, onboarding.StateEnterAPIKey, onboarding.EventSubmitKey, submitKeyPayload("sk-good"), &fsmCtx)
	requireOK(t, "enter/submit", err, r, onboarding.StateAccountStep)

	// account_step + sign_in: nil signer gracefully degrades to guided_action.
	// (Production wires a real signer via AccountSigner interface.)
	r, err = fsm.Step(ctx, onboarding.StateAccountStep, onboarding.EventSignIn, nil, &fsmCtx)
	requireOK(t, "account/sign_in (nil signer)", err, r, onboarding.StateGuidedAction)

	// guided_action → done (open settings CTA)
	r, err = fsm.Step(ctx, onboarding.StateGuidedAction, onboarding.EventOpenSettings, nil, &fsmCtx)
	requireOK(t, "guided/open-settings", err, r, onboarding.StateDone)
}

// TestBackNavigation verifies that back events unwind correctly.
func TestBackNavigation(t *testing.T) {
	ctx := context.Background()
	tester := &mockTester{}
	fsm := onboarding.New(tester)

	fsmCtx := onboarding.NewFSMContext()
	fsmCtx.ChosenKind = onboarding.ProviderAnthropic

	// enter-api-key → back → pick-provider-kind
	r, err := fsm.Step(ctx, onboarding.StateEnterAPIKey, onboarding.EventBack, nil, &fsmCtx)
	requireOK(t, "enter/back", err, r, onboarding.StatePickProviderKind)
	if fsmCtx.APIKey != "" {
		t.Error("APIKey should be cleared after back from enter-api-key")
	}

	// pick-provider-kind → back → welcome
	fsmCtx.ChosenKind = onboarding.ProviderAnthropic
	r, err = fsm.Step(ctx, onboarding.StatePickProviderKind, onboarding.EventBack, nil, &fsmCtx)
	requireOK(t, "pick/back", err, r, onboarding.StateWelcome)
	if fsmCtx.ChosenKind != "" {
		t.Error("ChosenKind should be cleared after back from pick-provider-kind")
	}
}

// TestUnknownState verifies that an unrecognised state surfaces an error.
func TestUnknownState(t *testing.T) {
	ctx := context.Background()
	fsm := onboarding.New(nil)
	c := onboarding.NewFSMContext()
	_, err := fsm.Step(ctx, onboarding.State("bogus"), onboarding.EventNext, nil, &c)
	if err == nil {
		t.Fatal("expected error for unknown state, got nil")
	}
}

// TestCardStructure spot-checks that rendered cards have required fields.
func TestCardStructure(t *testing.T) {
	ctx := context.Background()
	tester := &mockTester{}
	fsm := onboarding.New(tester)

	fsmCtx := onboarding.NewFSMContext()
	fsmCtx.ChosenKind = onboarding.ProviderAnthropic

	// Welcome card
	welcome := onboarding.InitialCard()
	if welcome.Card.Title == "" {
		t.Error("welcome card missing title")
	}

	// Pick-provider card
	r, _ := fsm.Step(ctx, onboarding.StateWelcome, onboarding.EventNext, nil, &fsmCtx)
	if len(r.Card.Actions) < len(onboarding.SupportedProviders) {
		t.Errorf("pick-provider card actions = %d, want >= %d", len(r.Card.Actions), len(onboarding.SupportedProviders))
	}

	// Enter-api-key card
	r, _ = fsm.Step(ctx, onboarding.StatePickProviderKind, onboarding.Event(onboarding.ProviderAnthropic), nil, &fsmCtx)
	if len(r.Card.Fields) == 0 {
		t.Error("enter-api-key card has no fields")
	}
	if r.Card.Fields[0].Secret != true {
		t.Error("api_key field should be marked secret")
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

func requireOK(t *testing.T, label string, err error, r onboarding.StepResult, wantState onboarding.State) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", label, err)
	}
	if r.State != wantState {
		t.Errorf("%s: state = %q, want %q", label, r.State, wantState)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub)))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
