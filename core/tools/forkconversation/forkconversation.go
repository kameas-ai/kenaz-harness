// Package forkconversation implements the kenaz__fork_conversation
// built-in tool: the model's surface for creating a CONVERSATION branch
// of the session the tool call runs in.
//
// Why it exists (dogfood finding, 2026-10-05): a user asked the in-app
// agent for "a conversation fork/branch". With no tool for it, the agent
// first created a git branch, then correctly said it could not fork the
// conversation and pointed the user at the "+ Fork" button. Humans had
// the capability end to end (branches sidebar, "Branch from this turn",
// Branch Advisor accept); the model had nothing.
//
// This package owns only argument parsing, scoping and the result
// envelope. The fork itself is delegated to a Forker, which production
// binds (core/rpc/builtins_wiring.go) to the SAME branches view
// CreateBranch entry point every human fork path calls — no second
// branch-creation implementation, and the optional handoff seed lands
// through that path's own single session-message writer.
//
// Session scope: the parent session is ALWAYS the session the tool call
// runs in, read from the dispatch context (toolloop.SessionIDFromContext).
// The schema has no session argument, and unknown fields — including any
// attempt to smuggle a session id — are refused, never honoured.
package forkconversation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

const (
	// ToolName is the namespaced identifier surfaced to the model.
	ToolName = "kenaz__fork_conversation"

	// ToolDescription is what the model sees in its catalog. Phrased to
	// disambiguate from version control: the dogfood failure was the
	// model reading "branch" as a git branch.
	ToolDescription = "Fork THIS chat conversation into a new conversation branch (not a git branch). " +
		"Use when the user asks to fork, branch, or split off the conversation, or to explore a tangent " +
		"in a separate thread. The branch copies the conversation up to the branch point and can open " +
		"with an optional handoff message. It does not run by itself: the user opens it from the " +
		"branches sidebar (or the link on this tool call) and continues there."

	// MaxTitleRunes bounds the branch title (rendered inline in the
	// branches sidebar).
	MaxTitleRunes = 200

	// MaxHandoffBytes bounds the optional seed message.
	MaxHandoffBytes = 64 * 1024
)

const inputSchema = `{
  "type": "object",
  "properties": {
    "title": {
      "type": "string",
      "description": "Short title for the new conversation branch, shown in the branches sidebar."
    },
    "handoff": {
      "type": "string",
      "description": "Optional seed message the branch opens with: the context and instructions for the branched thread."
    },
    "from_message_id": {
      "type": "string",
      "description": "Optional id of a message in THIS conversation to branch from. Defaults to the latest message."
    }
  },
  "required": ["title"],
  "additionalProperties": false
}`

// ForkRequest is what the tool asks the Forker to do. ParentSessionID is
// always the dispatch-context session, never a model argument.
type ForkRequest struct {
	ParentSessionID string
	Title           string
	Handoff         string
	FromMessageID   string
}

// ForkResult is what a successful fork produced.
type ForkResult struct {
	BranchID        string
	BranchSessionID string
	Title           string
	// FromMessageID is the anchor the branch was cut at (resolved from
	// the default when the model did not name one).
	FromMessageID string
	// HandoffSeeded is true when the handoff message was written.
	HandoffSeeded bool
}

// ErrNothingToFork is returned by a Forker when the session has no
// message to branch from.
var ErrNothingToFork = errors.New("forkconversation: session has no messages to branch from")

// ErrSeedFailed is returned (wrapped, WITH a populated ForkResult) when
// the branch exists but the handoff message did not land.
var ErrSeedFailed = errors.New("forkconversation: branch created but handoff seed failed")

// Forker creates the branch. Production: an adapter over
// core/rpc/views/branches.API.CreateBranch.
type Forker interface {
	Fork(ctx context.Context, req ForkRequest) (ForkResult, error)
}

// Options configures a Tool. Forker is required. SessionResolver
// defaults to toolloop.SessionIDFromContext. Logger defaults to
// slog.Default.
type Options struct {
	Forker          Forker
	SessionResolver func(ctx context.Context) string
	Logger          *slog.Logger
}

// Tool implements kenaz__fork_conversation. Safe for concurrent use.
type Tool struct {
	forker   Forker
	resolver func(ctx context.Context) string
	logger   *slog.Logger
}

// New constructs a Tool. Panics on a nil Forker — the wiring site must
// not register a tool that can only fail (FR-007 of
// crash-recovery-tool-gating-0XQTC4RK).
func New(opts Options) *Tool {
	if opts.Forker == nil {
		panic("forkconversation.New: nil Forker")
	}
	resolver := opts.SessionResolver
	if resolver == nil {
		resolver = toolloop.SessionIDFromContext
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Tool{forker: opts.Forker, resolver: resolver, logger: logger}
}

// Name returns the namespaced tool identifier.
func (t *Tool) Name() string { return ToolName }

// Description returns the model-facing description.
func (t *Tool) Description() string { return ToolDescription }

// InputSchema returns the JSON Schema for the tool's args.
func (t *Tool) InputSchema() json.RawMessage { return json.RawMessage(inputSchema) }

type forkArgs struct {
	Title         string `json:"title"`
	Handoff       string `json:"handoff,omitempty"`
	FromMessageID string `json:"from_message_id,omitempty"`
}

// SuccessResult is the JSON the model (and the transcript's tool chip)
// receives on success. The frontend's "Open branch" affordance reads
// branch_session_id off this exact shape (frontend/src/lib/forkTool.ts).
type SuccessResult struct {
	BranchID        string `json:"branch_id"`
	BranchSessionID string `json:"branch_session_id"`
	Title           string `json:"title"`
	FromMessageID   string `json:"from_message_id,omitempty"`
	HandoffSeeded   bool   `json:"handoff_seeded"`
	Message         string `json:"message"`
}

type errorResult struct {
	Error           string `json:"error"`
	Message         string `json:"message"`
	BranchID        string `json:"branch_id,omitempty"`
	BranchSessionID string `json:"branch_session_id,omitempty"`
}

const (
	errKindInvalidArgs   = "invalid_args"
	errKindNoSession     = "no_session"
	errKindNothingToFork = "nothing_to_fork"
	errKindForkFailed    = "fork_failed"
	errKindSeedFailed    = "handoff_not_seeded"
)

// Call parses args, scopes the fork to the dispatch session and
// delegates to the Forker. Expected failures are returned as a JSON
// error envelope with a nil Go error (same convention as
// kenaz__save_artifact), so the model can read and recover.
func (t *Tool) Call(ctx context.Context, argsJSON json.RawMessage) (json.RawMessage, error) {
	if t == nil {
		return nil, errors.New("forkconversation: nil tool")
	}
	if len(argsJSON) == 0 {
		return marshalErr(errKindInvalidArgs, "empty args", "", "")
	}
	var args forkArgs
	dec := json.NewDecoder(bytes.NewReader(argsJSON))
	// Unknown fields are refused, not ignored: the only session this tool
	// may fork is the one it runs in, and a silently-dropped
	// "session_id" argument would let the model believe it had forked
	// some other conversation.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&args); err != nil {
		return marshalErr(errKindInvalidArgs,
			fmt.Sprintf("parse args: %v (accepted fields: title, handoff, from_message_id; the fork is always of the current conversation)", err),
			"", "")
	}
	args.Title = strings.TrimSpace(args.Title)
	args.Handoff = strings.TrimSpace(args.Handoff)
	args.FromMessageID = strings.TrimSpace(args.FromMessageID)
	if args.Title == "" {
		return marshalErr(errKindInvalidArgs, "title is required", "", "")
	}
	if utf8.RuneCountInString(args.Title) > MaxTitleRunes {
		return marshalErr(errKindInvalidArgs, fmt.Sprintf("title exceeds %d runes", MaxTitleRunes), "", "")
	}
	if len(args.Handoff) > MaxHandoffBytes {
		return marshalErr(errKindInvalidArgs, fmt.Sprintf("handoff exceeds %d bytes", MaxHandoffBytes), "", "")
	}

	sessionID := t.resolver(ctx)
	if sessionID == "" {
		t.logger.Warn("forkconversation.no_session_in_context", "tool", ToolName)
		return marshalErr(errKindNoSession, "no session id in context — cannot determine which conversation to fork", "", "")
	}

	res, err := t.forker.Fork(ctx, ForkRequest{
		ParentSessionID: sessionID,
		Title:           args.Title,
		Handoff:         args.Handoff,
		FromMessageID:   args.FromMessageID,
	})
	switch {
	case err == nil:
	case errors.Is(err, ErrSeedFailed):
		t.logger.Warn("forkconversation.seed_failed",
			"session_id", sessionID, "branch_id", res.BranchID, "err", err.Error())
		return marshalErr(errKindSeedFailed,
			"The conversation branch was created but the handoff message could not be written to it. "+
				"Tell the user the branch exists without the handoff; they can open it from the branches sidebar and paste the instructions there.",
			res.BranchID, res.BranchSessionID)
	case errors.Is(err, ErrNothingToFork):
		return marshalErr(errKindNothingToFork, "this conversation has no messages yet, so there is nothing to branch from", "", "")
	default:
		t.logger.Warn("forkconversation.fork_failed", "session_id", sessionID, "err", err.Error())
		return marshalErr(errKindForkFailed, fmt.Sprintf("fork failed: %v", err), "", "")
	}

	t.logger.Info("forkconversation.forked",
		"session_id", sessionID,
		"branch_id", res.BranchID,
		"branch_session_id", res.BranchSessionID,
		"handoff_seeded", res.HandoffSeeded,
	)
	msg := fmt.Sprintf("Created conversation branch %q. It is a separate conversation the user can open from the branches sidebar "+
		"(or the \"Open branch\" link on this tool call); it does not run on its own and this conversation continues unchanged.",
		res.Title)
	if res.HandoffSeeded {
		msg += " The branch opens with your handoff message."
	}
	out, err := json.Marshal(SuccessResult{
		BranchID:        res.BranchID,
		BranchSessionID: res.BranchSessionID,
		Title:           res.Title,
		FromMessageID:   res.FromMessageID,
		HandoffSeeded:   res.HandoffSeeded,
		Message:         msg,
	})
	if err != nil {
		return nil, fmt.Errorf("forkconversation: marshal success: %w", err)
	}
	return out, nil
}

func marshalErr(kind, message, branchID, branchSessionID string) (json.RawMessage, error) {
	return json.Marshal(errorResult{
		Error:           kind,
		Message:         message,
		BranchID:        branchID,
		BranchSessionID: branchSessionID,
	})
}
