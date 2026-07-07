// Package onboarding implements the Phase-1 onboarding finite-state machine
// and its card-rendering layer.
//
// Architecture: the FSM is stateless. Each Step(state, event) call is
// self-contained; the caller (frontend RPC view) holds the current State and
// passes it on every call. The FSM returns the next State and a Card that
// describes what to render.
//
// Isolation contract: this package MUST NOT import core/session, core/storage,
// or core/agentgraph. The only external dependency is the LLMTester interface
// (satisfied by core/llm/registry.Registry in production, or a mock in tests).
package onboarding

// ProviderKind is the string tag identifying a supported LLM provider.
// These values mirror the Kind constants in the provider sub-packages.
type ProviderKind string

const (
	ProviderAnthropic  ProviderKind = "anthropic"
	ProviderOpenAI     ProviderKind = "openai"
	ProviderOpenRouter ProviderKind = "openrouter"
)

// SupportedProviders is the ordered list of provider kinds the onboarding
// flow presents to the user.
var SupportedProviders = []ProviderKind{
	ProviderAnthropic,
	ProviderOpenAI,
	ProviderOpenRouter,
}

// Action represents one choice the user can take from a rendered Card.
// The frontend renders each Action as a button or selectable item; the
// user's selection becomes the Event sent back to Step.
type Action struct {
	// ID is the machine-readable event that this action triggers.
	ID string `json:"id"`
	// Label is the human-readable button text.
	Label string `json:"label"`
	// Primary marks the recommended / default action (rendered prominently).
	Primary bool `json:"primary,omitempty"`
}

// Card is the render descriptor emitted by the FSM for one state. The
// frontend does not need to know the FSM internals; it renders the Card
// and sends back the user's choice as an Event ID.
type Card struct {
	// Title is the card heading.
	Title string `json:"title"`
	// Body is the explanatory text (may be empty).
	Body string `json:"body,omitempty"`
	// Actions are the choices presented to the user.
	Actions []Action `json:"actions,omitempty"`
	// Fields carries optional structured input descriptors (e.g. the
	// API-key text field on the enter-api-key state).
	Fields []Field `json:"fields,omitempty"`
	// ErrorMessage carries a prior-attempt failure reason (non-empty on
	// retry transitions from test-connection failure).
	ErrorMessage string `json:"error_message,omitempty"`
	// ProviderHint carries the chosen ProviderKind for states that need
	// to render provider-specific copy.
	ProviderHint ProviderKind `json:"provider_hint,omitempty"`
}

// Field describes one form input the user must fill in before advancing.
type Field struct {
	// ID is the machine-readable field name (used to key the user's value).
	ID string `json:"id"`
	// Label is the human-readable field label.
	Label string `json:"label"`
	// Placeholder is the placeholder text shown in the input.
	Placeholder string `json:"placeholder,omitempty"`
	// Secret marks fields that should be obscured (e.g. API keys).
	Secret bool `json:"secret,omitempty"`
}

// renderWelcome returns the Card for the welcome state.
func renderWelcome() Card {
	return Card{
		Title: "Welcome to Kenaz",
		Body:  "Let's connect your first AI provider so you can start chatting. This takes about a minute.",
		Actions: []Action{
			{ID: string(EventNext), Label: "Get started", Primary: true},
		},
	}
}

// renderPickProviderKind returns the Card for the pick-provider-kind state.
func renderPickProviderKind() Card {
	actions := make([]Action, 0, len(SupportedProviders))
	for _, k := range SupportedProviders {
		label := providerLabel(k)
		actions = append(actions, Action{
			ID:      string(k),
			Label:   label,
			Primary: k == ProviderAnthropic,
		})
	}
	return Card{
		Title:   "Choose a provider",
		Body:    "Select the AI provider whose API key you have. You can add more providers later in Settings.",
		Actions: actions,
	}
}

// renderEnterAPIKey returns the Card for the enter-api-key state.
// priorErr is non-empty when the user is retrying after a failed connection test.
func renderEnterAPIKey(kind ProviderKind, priorErr string) Card {
	label := providerLabel(kind)
	c := Card{
		Title:        "Enter your " + label + " API key",
		Body:         "Your key is stored locally and never sent to Kenaz servers.",
		ProviderHint: kind,
		Fields: []Field{
			{
				ID:          "api_key",
				Label:       label + " API Key",
				Placeholder: apiKeyPlaceholder(kind),
				Secret:      true,
			},
		},
		Actions: []Action{
			{ID: string(EventSubmitKey), Label: "Test connection", Primary: true},
			{ID: string(EventBack), Label: "Back"},
		},
		ErrorMessage: priorErr,
	}
	return c
}

// renderTestConnection returns the Card shown while the connection test is
// running. In practice the frontend may show a spinner; this card acts as
// the "loading" descriptor.
func renderTestConnection(kind ProviderKind) Card {
	return Card{
		Title:        "Testing connection…",
		Body:         "Verifying your " + providerLabel(kind) + " API key. This may take a few seconds.",
		ProviderHint: kind,
	}
}

// renderDone returns the Card for the done state.
func renderDone(kind ProviderKind) Card {
	return Card{
		Title: "You're all set!",
		Body:  "Your " + providerLabel(kind) + " connection is working. You can start a new conversation now.",
		Actions: []Action{
			{ID: string(EventFinish), Label: "Start chatting", Primary: true},
		},
		ProviderHint: kind,
	}
}

// renderAccountStep returns the Card for the optional account/sign-in step.
// This step is always skippable (OSS-standalone must work with no account).
// When signed in, Fleet capabilities are fetched and Fleet-connected steps unlock.
func renderAccountStep() Card {
	return Card{
		Title: "Connect your account (optional)",
		Body:  "Sign in to unlock team features: context sync, shared policies, and fleet context bootstrap. Kenaz works fully without an account — you can always sign in later from Settings.",
		Actions: []Action{
			{ID: string(EventSignIn), Label: "Sign in / create account", Primary: true},
			{ID: string(EventSkipAccount), Label: "Continue without account"},
		},
	}
}

// renderAccountSigningIn returns the Card shown while the sign-in flow is running.
func renderAccountSigningIn() Card {
	return Card{
		Title: "Opening sign-in…",
		Body:  "A browser window will open for sign-in. Return here when done.",
	}
}

// renderAccountDone returns the Card shown after a successful sign-in.
func renderAccountDone(email string) Card {
	body := "You are signed in and your account is connected."
	if email != "" {
		body = "Signed in as " + email + ". Your account is connected."
	}
	return Card{
		Title: "Account connected",
		Body:  body,
		Actions: []Action{
			{ID: string(EventNext), Label: "Continue", Primary: true},
		},
	}
}

// renderGuidedAction returns the Card for the first-guided-action step.
// This concludes onboarding on a concrete, useful first task rather than
// a dead confirmation screen (FR-007).
func renderGuidedAction() Card {
	return Card{
		Title: "Ready — what would you like to do?",
		Body:  "Kenaz is ready. Try asking it something — describe a project, ask for help with code, or start a new conversation. You can also explore Settings to configure tools, context, and more.",
		Actions: []Action{
			{ID: string(EventStartNewChat), Label: "Start a new conversation", Primary: true},
			{ID: string(EventOpenSettings), Label: "Explore settings"},
			{ID: string(EventFinish), Label: "Close"},
		},
	}
}

// providerLabel returns a human-readable display name for a ProviderKind.
func providerLabel(k ProviderKind) string {
	switch k {
	case ProviderAnthropic:
		return "Anthropic"
	case ProviderOpenAI:
		return "OpenAI"
	case ProviderOpenRouter:
		return "OpenRouter"
	default:
		return string(k)
	}
}

// apiKeyPlaceholder returns the placeholder text for the API key field.
func apiKeyPlaceholder(k ProviderKind) string {
	switch k {
	case ProviderAnthropic:
		return "sk-ant-…"
	case ProviderOpenAI:
		return "sk-…"
	case ProviderOpenRouter:
		return "sk-or-…"
	default:
		return "Enter your API key"
	}
}
