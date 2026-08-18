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
// stubCommand (cmd_stubs.go) is a kept building block for commands
// registered before their real implementation lands. It is not
// currently used: /memorize, /recall, /forget and /branch — the four
// v1 commands that originally shipped as stubs pointing at the
// agent-kernel-graph mission — are all real today. That mission is
// archived (kitty-specs/_archive/agent-kernel-graph-01KQ6391); see the
// dated keep-decision in cmd_stubs_test.go for why the type still
// exists (engineer-truth-pass-01PMTP01 WP07).
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

// owningMissionUnassigned is the placeholder MetaKeyOwningMission value
// a stub command carries until a real command is registered against
// stubCommand. Previously named owningMissionAgentKernelGraph and
// hard-coded to that mission's slug — but the memory pipeline shipped
// and agent-kernel-graph-01KQ6391 archived two releases ago
// (engineer-truth-pass-01PMTP01 WP07, finding B18). No command uses
// stubCommand today, so there is no real mission to name; a future
// caller registering a genuine stub should set its own
// MetaKeyOwningMission rather than rely on a shared constant that may
// name the wrong mission for its command.
const owningMissionUnassigned = "unassigned"

// comingSoonTemplate is the canned body a stub command returns. %s is
// the bare command name (without the leading slash) so the rendered
// bubble starts with the command the user typed. Mission-agnostic —
// it used to name the (now-archived) agent-kernel-graph mission by
// name and claim the "memory pipeline" specifically, which would have
// rendered a sentence about a closed mission to any future command
// registered against the kept stubCommand building block.
const comingSoonTemplate = "/%s: registered but not yet wired."

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
	// Memory is the long-term-memory gateway used by /memorize,
	// /recall, /forget. Nil means those commands return a friendly
	// "memory subsystem not wired" error result.
	Memory MemoryGateway
	// Branches is the conversation-branching gateway used by /branch.
	// Nil means /branch returns a friendly "branching not wired" error.
	Branches BranchGateway
	// Workflows is the workflow gateway used by /wf.
	// Nil means /wf returns a friendly "workflows not wired" error.
	Workflows WorkflowsGateway
	// Secrets is the model-accessible secrets gateway used by /secret.
	// Nil means /secret returns a friendly "secrets not wired" error.
	// (model-secret-references-01KW7M5A WP11)
	Secrets SecretExposer
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

// MemoryGateway is the narrow contract /memorize, /recall, /forget
// consume. The rpc layer adapts core/memory.Store + core/memory.Embedder
// (plus the project resolver) onto this shape so the slashcmd package
// stays out of the core/memory import graph for unit-test isolation.
//
// Memorize persists the supplied text as a new chunk, marks it pinned
// (immune to the prune sweep) and returns the new chunk id.
//
// Recall runs a similarity query against the memory store and returns
// up to k matches above the configured threshold, newest-first within
// equal-similarity buckets. The implementation decides the threshold —
// the slash command only cares about the returned hit list.
//
// Forget removes the chunk by id; ErrMemoryChunkNotFound surfaces the
// "no such id" case so /forget can render the right user-facing text.
type MemoryGateway interface {
	Memorize(ctx context.Context, sessionID, text string) (string, error)
	Recall(ctx context.Context, sessionID, query string, k int) ([]MemoryHit, error)
	Forget(ctx context.Context, id string) error
}

// MemoryHit is a single recall result the slash command renders. The
// Score is the cosine similarity (0..1); the slash command formats it
// as a percentage in the bubble.
type MemoryHit struct {
	ID      string
	Content string
	Score   float32
}

// ErrMemoryChunkNotFound is the typed sentinel /forget surfaces when
// the supplied id does not match any chunk. MemoryGateway implementations
// should wrap their backend's "not found" error so errors.Is works.
var ErrMemoryChunkNotFound = errors.New("slashcmd: memory chunk not found")

// BranchGateway is the narrow contract /branch consumes. The rpc layer
// adapts the BranchesAPI (CreateBranch + RecommendModel) onto this shape.
//
// CreateBranch forks the parent session onto a new child session that
// uses the supplied (providerID, modelID) tuple. Returns the new
// branch id + the child session id; both are echoed in the result body
// so the frontend can route the user into the child session immediately.
//
// RecommendModels returns the recommended smaller / larger models for
// the parent session, used by the no-arg /branch listing path. Either
// recommendation may be a zero value when no fit exists.
type BranchGateway interface {
	CreateBranch(ctx context.Context, parentSessionID, modelID string) (BranchHandle, error)
	RecommendModels(ctx context.Context, parentSessionID string) (BranchRecommendations, error)
}

// BranchHandle is the result of a successful CreateBranch call.
type BranchHandle struct {
	BranchID       string
	ChildSessionID string
	ProviderID     string
	ModelID        string
}

// BranchRecommendations bundles the smaller / larger / same-tier
// recommendations RecommendModels returns. The Same field carries the
// parent's current pair so /branch's listing path can show it as a
// sanity-check baseline.
type BranchRecommendations struct {
	Smaller BranchModel
	Larger  BranchModel
	Same    BranchModel
}

// BranchModel is one (provider, model) row in a recommendation list.
type BranchModel struct {
	ProviderID string
	ModelID    string
	Tier       string
	Reason     string
}

// WorkflowsGateway is the narrow contract /wf consumes. The rpc layer
// adapts the real *workflowsview.API onto this shape so the slashcmd
// package never imports the rpc/views/workflows package directly.
//
// List returns the installed workflow catalog (id, name, description).
//
// Get returns the full workflow including declared inputs so /wf can
// detect required fields that lack defaults.
//
// Run dispatches the workflow inline — inputs is a loose string map
// and opts.Inline must be true for the chat-invocation path. Returns
// a channel of progress events that is closed once the run terminates.
type WorkflowsGateway interface {
	List(ctx context.Context) ([]WorkflowSummary, error)
	Get(ctx context.Context, id string) (WorkflowDetail, error)
	Run(ctx context.Context, id string, inputs map[string]string, opts WorkflowRunOptions) (<-chan WorkflowProgressEvent, error)
}

// WorkflowSummary is the slashcmd-local view of one catalog entry.
type WorkflowSummary struct {
	ID          string
	Name        string
	Description string
}

// WorkflowInput is one declared input field on a workflow.
type WorkflowInput struct {
	Name     string
	Required bool
	Default  string
}

// WorkflowDetail carries the workflow fields /wf needs to prompt for
// missing required inputs before dispatching.
type WorkflowDetail struct {
	ID          string
	Name        string
	Description string
	Inputs      []WorkflowInput
}

// WorkflowRunOptions tunes a /wf dispatch call.
type WorkflowRunOptions struct {
	// Inline, when true, routes through the inline-run path so progress
	// events stream into the current session transcript.
	Inline bool
}

// WorkflowProgressEvent is one step-transition event forwarded from
// the engine into the chat turn.
type WorkflowProgressEvent struct {
	RunID  string
	Step   string
	Status string
	Output string
	Err    string
}

// IsZero reports whether the model row is unpopulated.
func (m BranchModel) IsZero() bool {
	return m.ProviderID == "" && m.ModelID == ""
}

// SecretExposer is the narrow contract /secret consumes.
// The rpc layer adapts the concrete SecretsAPI onto this shape so
// the slashcmd package never imports the rpc/views/secrets package.
//
// Expose adds a secret to the model-accessible ExposureIndex. The
// plaintext slice is zeroed by the implementation immediately after
// being stored (FR-003 / WP11).
//
// List returns all currently exposed locators (no plaintext) so
// `/secret list` can render the model-facing ref tokens.
type SecretExposer interface {
	// Expose stores a secret under the given locator. The plaintext slice
	// is zeroed before this method returns.
	Expose(ctx context.Context, locator, description, kind string, plaintext []byte) error
	// ListLocators returns the set of currently exposed locators.
	ListLocators(ctx context.Context) ([]string, error)
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
			MetaKeyOwningMission: owningMissionUnassigned,
		},
	}
}
