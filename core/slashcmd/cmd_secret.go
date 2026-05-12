package slashcmd

// secretCommand implements /secret <subcommand>.
//
// Supported subcommands:
//   /secret add <locator> [--kind bearer|basic|header|raw|aws_credential]
//        Interactively adds a secret to the model-accessible exposure
//        index. The user is prompted for the plaintext via the elicit
//        surface when running in an interactive session; in the CLI path
//        the plaintext is read from stdin with echo disabled.
//
//        This subcommand returns a ResultKindElicit so the chat frontend
//        can open a password-type input field for the secret value, then
//        call back with a /secret-confirm interaction. The slash command
//        loop handles the round-trip.
//
//   /secret list
//        Lists all currently exposed @secret: ref tokens.
//
// Spec mapping: model-secret-references-01KW7M5A WP11.

import (
	"context"
	"fmt"
	"strings"
)

type secretCommand struct{}

func (secretCommand) Name() string { return "secret" }
func (secretCommand) Description() string {
	return "Manage model-accessible secrets — /secret add <locator> [--kind <kind>] · /secret list"
}
func (secretCommand) Hidden() bool     { return false }
func (secretCommand) ComingSoon() bool { return false }

func (secretCommand) Run(ctx context.Context, env Env, args []string) (Result, error) {
	if env.Secrets == nil {
		return Result{
			Kind: ResultKindError,
			Text: "/secret: secrets subsystem not wired",
		}, nil
	}

	if len(args) == 0 {
		return Result{
			Kind: ResultKindError,
			Text: "/secret: missing subcommand — usage:\n  /secret add <locator> [--kind bearer|basic|header|raw|aws_credential]\n  /secret list",
		}, nil
	}

	sub := strings.ToLower(strings.TrimSpace(args[0]))
	rest := args[1:]

	switch sub {
	case "add":
		return secretAdd(ctx, env, rest)
	case "list":
		return secretList(ctx, env)
	default:
		return Result{
			Kind: ResultKindError,
			Text: fmt.Sprintf("/secret: unknown subcommand %q — try 'add' or 'list'", sub),
		}, nil
	}
}

// secretAdd handles /secret add <locator> [--kind <kind>].
//
// This subcommand cannot read the plaintext directly from the slash
// command args (that would expose it in the transcript). Instead it
// returns a ResultKindInfo with an Elicit metadata hint so the frontend
// opens a password input dialog. The actual Expose call happens when
// the user submits via Secrets_Expose (WP10 Bindings method).
//
// Note: elicitation round-trips are not yet wired here — the current
// implementation returns a friendly hint pointing to the Settings panel.
// Full elicit-based secret capture lands in a follow-up WP.
func secretAdd(ctx context.Context, env Env, args []string) (Result, error) {
	if len(args) == 0 {
		return Result{
			Kind: ResultKindError,
			Text: "/secret add: missing locator — usage: /secret add <locator> [--kind bearer|basic|header|raw|aws_credential]",
		}, nil
	}

	// First arg is the locator; strip a stray @secret: prefix.
	locator := strings.TrimSpace(args[0])
	locator = strings.TrimPrefix(locator, "@secret:")
	if locator == "" {
		return Result{
			Kind: ResultKindError,
			Text: "/secret add: locator must not be empty",
		}, nil
	}
	if strings.ContainsAny(locator, " \t\n\r") {
		return Result{
			Kind: ResultKindError,
			Text: "/secret add: locator must not contain whitespace",
		}, nil
	}

	// Parse --kind flag.
	kind := "bearer"
	for i := 1; i < len(args); i++ {
		if (args[i] == "--kind" || args[i] == "-k") && i+1 < len(args) {
			kind = strings.TrimSpace(args[i+1])
			i++ // consume value
		}
	}
	validKinds := map[string]bool{
		"bearer": true, "basic": true, "header": true,
		"raw": true, "aws_credential": true,
	}
	if !validKinds[kind] {
		return Result{
			Kind: ResultKindError,
			Text: fmt.Sprintf("/secret add: unknown kind %q — valid values: bearer, basic, header, raw, aws_credential", kind),
		}, nil
	}

	// Check if the locator is already exposed.
	locators, err := env.Secrets.ListLocators(ctx)
	if err != nil {
		return Result{
			Kind: ResultKindError,
			Text: "/secret add: failed to check existing secrets: " + err.Error(),
		}, err
	}
	for _, l := range locators {
		if l == locator {
			return Result{
				Kind: ResultKindInfo,
				Text: fmt.Sprintf("@secret:%s is already exposed — use Settings > Secrets to revoke and re-add it, or choose a different locator.", locator),
			}, nil
		}
	}

	// The plaintext cannot be captured safely in a slash command arg.
	// Direct the user to the Settings panel which has a password input.
	// A future WP will wire the elicit surface here so the model can
	// trigger a modal password dialog inline.
	return Result{
		Kind: ResultKindInfo,
		Text: fmt.Sprintf(
			"To expose @secret:%s with kind=%s:\n"+
				"  Open Settings → Secrets → Expose secret\n"+
				"  Locator: %s\n"+
				"  Kind: %s\n\n"+
				"The plaintext is zeroed immediately after being stored.\n"+
				"Once exposed, use @secret:%s in tool arguments (e.g. web_fetch headers).",
			locator, kind, locator, kind, locator,
		),
		Metadata: map[string]any{
			"action":  "secret_add_pending",
			"locator": locator,
			"kind":    kind,
		},
	}, nil
}

// secretList handles /secret list.
func secretList(ctx context.Context, env Env) (Result, error) {
	locators, err := env.Secrets.ListLocators(ctx)
	if err != nil {
		return Result{
			Kind: ResultKindError,
			Text: "/secret list: " + err.Error(),
		}, err
	}
	if len(locators) == 0 {
		return Result{
			Kind: ResultKindInfo,
			Text: "No secrets are currently exposed to the model.\nUse /secret add <locator> or Settings → Secrets to expose one.",
		}, nil
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Exposed secrets (%d):\n", len(locators)))
	for _, l := range locators {
		sb.WriteString("  @secret:")
		sb.WriteString(l)
		sb.WriteString("\n")
	}
	return Result{
		Kind: ResultKindInfo,
		Text: strings.TrimRight(sb.String(), "\n"),
	}, nil
}
