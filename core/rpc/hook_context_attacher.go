package rpc

// hook_context_attacher.go — v0.86.0 unwired sweep (2026-10-04).
//
// session_start and subagent_start hooks may return additional_context.
// Both events' docs promised it reaches the new session's system context
// (session.SessionHookRunner.FireSessionStart: "AdditionalContext to
// prepend to the session's first system message"; hooks.SubagentStartEvent:
// "The hook's AdditionalContext is prepended to the child's system
// context"), and both fire sites threw the result away
// (core/session/manager.go's `_, _ = m.hooks.FireSessionStart(...)`,
// subagent_run_spawner.go's `_, _ = deps.HookRunner.Fire(...)`).
//
// The delivery mechanism is the one the chat path already reads every
// turn: a session-scoped, system-kind attachment, which
// LLMProviderAdapter.buildAttachmentsBlock composes into the system
// prompt. That makes hook context durable for the session's lifetime
// (it is session-start context, not a one-turn note) and visible and
// removable in the session's attachments, rather than invisible prompt
// text.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	coreatt "github.com/kameas-ai/kenaz-harness/core/attachments"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/session"
)

// hookContextAttacher persists hook additional_context onto a session.
type hookContextAttacher func(ctx context.Context, sessionID, text string) error

// newHookContextAttacher returns nil when there is no attachments manager
// (nil-core chassis) — callers treat nil as "no delivery path" and log.
func newHookContextAttacher(mgr *coreatt.Manager) hookContextAttacher {
	if mgr == nil {
		return nil
	}
	return func(ctx context.Context, sessionID, text string) error {
		if sessionID == "" || strings.TrimSpace(text) == "" {
			return nil
		}
		existing, err := mgr.List(ctx, coreatt.ScopeFilter{
			ScopeKind: coreatt.ScopeKindSession,
			ScopeID:   sessionID,
		})
		if err != nil {
			return err
		}
		// Never position 0: that inline slot is owned by
		// Sessions_SetSystemPrompt, which deletes whatever inline
		// attachment sits there on every re-set.
		pos := 1
		for _, a := range existing {
			if a.Position >= pos {
				pos = a.Position + 1
			}
		}
		hash := sha256.Sum256([]byte(text))
		_, err = mgr.Add(ctx, coreatt.Attachment{
			ScopeKind:     coreatt.ScopeKindSession,
			ScopeID:       sessionID,
			ContentSource: "inline:" + hex.EncodeToString(hash[:]),
			Content:       text,
			Kind:          coreatt.KindSystem,
			Position:      pos,
		})
		return err
	}
}

// deliverHookContext is the shared tail both fire sites use: attach, or
// log why the context could not be delivered. Never fails the caller —
// both events are non-blocking by design.
func deliverHookContext(ctx context.Context, attach hookContextAttacher, event, sessionID, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	if attach == nil {
		logging.L().Info("rpc.hook_context.dropped",
			"event", event, "session_id", sessionID,
			"reason", "no attachments manager", "len", len(text))
		return
	}
	if err := attach(ctx, sessionID, text); err != nil {
		logging.L().Warn("rpc.hook_context.attach_failed",
			"event", event, "session_id", sessionID, "err", err.Error())
	}
}

// sessionStartContextRunner decorates the production
// session.SessionHookRunner so session_start's additional_context is
// delivered instead of discarded. setup and cwd_changed pass through
// untouched (neither event fires yet — i17 allowlist).
type sessionStartContextRunner struct {
	session.SessionHookRunner
	attach hookContextAttacher
}

func (r *sessionStartContextRunner) FireSessionStart(ctx context.Context, req session.SessionStartRequest) (session.SessionHookResult, error) {
	res, err := r.SessionHookRunner.FireSessionStart(ctx, req)
	if err == nil {
		deliverHookContext(ctx, r.attach, "session_start", req.SessionID, res.AdditionalContext)
	}
	return res, err
}
