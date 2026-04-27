// Package slashcmd implements the in-chat slash-command surface.
//
// The user types "/foo arg1 arg2" in the chat composer; the harness
// routes that to a Go-side handler before any LLM call. The package
// owns the Command interface, the Registry that dispatches on name,
// and the six v1 commands (3 real + 4 stubs).
//
// Real commands ship in this mission:
//
//   - /help — list the registered commands.
//   - /clear — append a system divider to the current session transcript.
//   - /model <id> — return metadata so the frontend can apply the new
//     (provider, model) tuple to the SessionsView's local active state.
//
// Stub commands register so /help advertises the full surface today;
// their handlers return a friendly "coming soon" message that points
// at the agent-kernel-graph mission. Stubs in v1: /memorize, /recall,
// /forget, /branch.
//
// DIRECTIVE_001: this package consumes core/session via narrow
// interfaces (SessionAppender, ProviderLister) so the slashcmd
// package never imports core/rpc/* and the import graph stays acyclic.
package slashcmd

import (
	"context"
	"errors"
	"fmt"
)

// ResultKind enumerates the four user-visible bubble styles the
// frontend renders for a command result.
const (
	ResultKindInfo    = "info"
	ResultKindError   = "error"
	ResultKindWarning = "warning"
	ResultKindSystem  = "system"
)

// MetaKey constants are well-known metadata keys frontend code reads.
// Keep them stable — adding a new key is fine; renaming an existing
// one breaks the SessionsView's apply path.
const (
	// MetaKeyProviderID — string. The frontend applies this to
	// SessionsView.activeProviderId when present.
	MetaKeyProviderID = "providerId"
	// MetaKeyModelID — string. The frontend applies this to
	// SessionsView.activeModelId when present.
	MetaKeyModelID = "modelId"
	// MetaKeyOwningMission — string. Stub commands set this to the
	// id of the mission that owns the real implementation; useful for
	// debug rendering and forward-debug navigation.
	MetaKeyOwningMission = "owningMission"
)

// owningMissionAgentKernelGraph is the spec slug the stub commands
// point at. Hard-coded so a typo can't drift the reference.
const owningMissionAgentKernelGraph = "agent-kernel-graph-01KQ6391"

// comingSoonTemplate is the canned body every stub command returns.
// %s is the bare command name (without the leading slash) so the
// rendered bubble starts with the command the user typed.
const comingSoonTemplate = "/%s: memory pipeline lands with the agent-kernel-graph mission — this command is registered but not yet wired."

// ErrUnknownCommand is returned by Registry.Execute when the parsed
// name has no registered command. The user-visible message is fixed
// (see UnknownCommandMessage) so the frontend renders the same text
// regardless of how the error reaches the surface.
var ErrUnknownCommand = errors.New("slashcmd: unknown command")

// UnknownCommandMessage is the canned body the RPC view surfaces to
// the frontend when ErrUnknownCommand is returned. Keep stable —
// the spec acceptance walkthrough A6 asserts the exact text.
const UnknownCommandMessage = "unknown command — type /help to list available commands"

// Command is the contract every registered slash command implements.
//
// Implementations MUST be safe for concurrent use — Run can fire from
// multiple goroutines if two RPC calls land in parallel. None of the
// v1 commands hold mutable state, so the contract is trivially met
// by value receivers + pure dispatch.
type Command interface {
	// Name is the bare command token (no leading slash). MUST be
	// non-empty and unique within a Registry.
	Name() string
	// Description is a one-line description rendered in /help and
	// in the autocomplete dropdown.
	Description() string
	// Hidden, when true, removes the command from List() and
	// /help's enumeration. The command is still dispatchable by
	// name. Useful for operator / dev-only commands.
	Hidden() bool
	// ComingSoon, when true, marks the command as registered-but-
	// stubbed. /help and the autocomplete render a "(coming soon)"
	// tag. The command's Run still executes and is expected to
	// return a friendly canned message.
	ComingSoon() bool
	// Run executes the command. env carries the harness-side
	// dependencies the command needs (session id, session manager,
	// provider lister, …). args is the post-name token slice.
	Run(ctx context.Context, env Env, args []string) (Result, error)
}

// Result is the typed payload Run returns. The RPC view forwards it
// verbatim to the frontend; the frontend renders Text in a bubble
// styled by Kind, and reads well-known Metadata keys to apply local
// side effects (see MetaKey*).
type Result struct {
	// Text is the user-visible body.
	Text string
	// Kind is one of ResultKindInfo / Error / Warning / System.
	// Empty defaults to Info.
	Kind string
	// Metadata carries typed side-effect values keyed by the
	// MetaKey* constants. Nil is valid (no side effects).
	Metadata map[string]any
}

// Env carries the runtime dependencies a command needs to run. The
// Env is constructed by the RPC view at dispatch time (one Env per
// Execute call) and threaded through Run unchanged.
//
// All fields are optional — a nil SessionAppender simply means the
// /clear command will return an error result instead of crashing.
// This matches the "real commands are real, but the chassis still
// boots if a dep is missing" stance of the rest of the rpc layer.
type Env struct {
	// SessionID is the active session the slash command targets.
	// Always populated by the RPC view; empty only in unit tests
	// that exercise the registry without a session-aware command.
	SessionID string
	// Sessions is the appender used by /clear. Nil means /clear
	// returns an error result. The interface is narrow so tests
	// can stub it without dragging in core/session.
	Sessions SessionAppender
	// Providers lists the configured LLM providers. /model uses
	// it to look up which provider authorises the requested model.
	// Nil means /model returns an error result.
	Providers ProviderLister
	// Registry references the owning Registry so meta-commands like
	// /help can enumerate their siblings without a back-reference
	// the registry would have to inject post-construction. Always
	// populated by the registry before Run is called.
	Registry *Registry
}

// SessionAppender is the narrow contract /clear consumes. A
// concrete *session.Manager satisfies this shape; the indirection
// keeps the slashcmd package out of the core/session import graph
// for unit-test isolation.
type SessionAppender interface {
	// AppendSystemMessage appends a system-role message to the
	// session and returns the resulting message id (or empty + an
	// error). Implementations may transform / truncate the content
	// per their own rules — the slash command does not care.
	AppendSystemMessage(ctx context.Context, sessionID, content string) (string, error)
}

// ProviderLister is the narrow contract /model consumes. The RPC
// view adapts the LLM-connector view's ListProviders into this
// shape so the slashcmd package never imports the rpc/views/llm
// package directly.
type ProviderLister interface {
	// ListProviders returns the configured providers. Each entry
	// MUST populate Name, ID, and Models (the authorised set). When
	// Models is empty, callers fall back to [DefaultModel].
	ListProviders(ctx context.Context) ([]Provider, error)
}

// Provider is the slashcmd-local view of an LLM provider profile.
// Mirrors the relevant subset of core/rpc/views/llm.Provider so the
// slashcmd package has no rpc dependency.
type Provider struct {
	ID           string
	Name         string
	Kind         string
	DefaultModel string
	Models       []string
}

// AuthorisedModels returns the union of DefaultModel + Models with
// duplicates removed. Tests and /model lookups consume this so the
// lookup matches both single-model and multi-model rows.
func (p Provider) AuthorisedModels() []string {
	seen := make(map[string]struct{}, len(p.Models)+1)
	out := make([]string, 0, len(p.Models)+1)
	add := func(m string) {
		if m == "" {
			return
		}
		if _, ok := seen[m]; ok {
			return
		}
		seen[m] = struct{}{}
		out = append(out, m)
	}
	add(p.DefaultModel)
	for _, m := range p.Models {
		add(m)
	}
	return out
}

// comingSoonResult is the helper every stub command uses so the
// canned body + metadata stay in lockstep across stubs.
func comingSoonResult(name string) Result {
	return Result{
		Kind: ResultKindInfo,
		Text: fmt.Sprintf(comingSoonTemplate, name),
		Metadata: map[string]any{
			MetaKeyOwningMission: owningMissionAgentKernelGraph,
		},
	}
}
