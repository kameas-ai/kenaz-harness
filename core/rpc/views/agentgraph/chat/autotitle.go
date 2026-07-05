package chat

// autotitle.go — WP04 (session-auto-titling-01KQ8TDS): chat-runner trigger
// for one-shot automatic session titling.
//
// DIRECTIVE_001: backend-only. No frontend imports.
// NFR-001: 5-second hard timeout on the LLM call.
// Race safety: re-reads the session record inside fireAutoTitle; if the
// user renamed between trigger schedule and fire, manager.AutoTitle returns
// ErrAutoTitleSuperseded and the goroutine exits cleanly.

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/session"
	autotitle "github.com/kameas-ai/kenaz-harness/core/sessions/autotitle"
)

// AutoTitleManager is the narrowed session.Manager surface the auto-title
// trigger requires. Using an interface avoids a hard import cycle and lets
// tests inject a fake.
type AutoTitleManager interface {
	// Get fetches the current session record.
	Get(ctx context.Context, id string) (session.Record, error)
	// ListMessagesActive returns only live (non-archived) messages.
	ListMessagesActive(ctx context.Context, sessionID string) ([]session.Message, error)
	// AutoTitle atomically writes the generated title if auto_titled==0.
	// Returns session.ErrAutoTitleSuperseded when the guard fires.
	AutoTitle(ctx context.Context, id, title string) error
	// MarkAutoTitleAttempted sets auto_titled=1 without changing the name.
	MarkAutoTitleAttempted(ctx context.Context, id string) error
}

// AutoTitleAudit is the narrowed audit surface the auto-title trigger
// uses. Production binds this to the manager's own Audit sink.
type AutoTitleAudit interface {
	Emit(ctx context.Context, kind string, payload map[string]any)
}

// AutoTitleGenerator is the surface the trigger calls. Tests inject a fake.
type AutoTitleGenerator interface {
	GenerateTitle(ctx context.Context, transcript autotitle.Transcript) (string, error)
}

// AutoTitleListBroker is the narrow publish surface the auto-title trigger
// needs to emit TopicSessionListChanged after a successful title write.
// Production binds this to *rpc.StreamBroker; tests inject a recording fake.
type AutoTitleListBroker interface {
	Publish(topic string, payload any)
}

// AutoTitleDeps bundles every collaborator the chat-runner auto-title
// trigger requires. All fields are required when AutoTitleDeps is non-nil.
type AutoTitleDeps struct {
	// Manager is the session manager's narrowed surface.
	Manager AutoTitleManager
	// Generator produces a concise title from the transcript.
	Generator AutoTitleGenerator
	// Audit emitter for EventKindSessionAutoTitled events.
	Audit AutoTitleAudit
	// EffectiveEnabled returns true when the feature flag is on.
	// Defaults to an always-true stub when nil.
	EffectiveEnabled func() bool
	// Broker is the optional event publisher for TopicSessionListChanged.
	// When non-nil, a successful auto-title write emits a "title_set"
	// event so the LeftRail refreshes without polling (v0.5.3 fix).
	Broker AutoTitleListBroker
}

// autoTitleEnabled reports whether the feature flag allows auto-titling.
// Falls back to true (enabled) when no function is provided.
func (d *AutoTitleDeps) autoTitleEnabled() bool {
	if d == nil || d.EffectiveEnabled == nil {
		return true
	}
	return d.EffectiveEnabled()
}

// maxMessagesForTitle is the cap on messages fed to the generator.
// Matching the spec: "up to 10 most-recent messages".
const maxMessagesForTitle = 10

// isPlaceholderName reports whether the session name looks like an
// auto-generated placeholder that should be replaced by the title engine.
// The predicate is deliberately broad: if auto_titled==false the session
// is a candidate. The name check is a secondary guard for sessions where
// the user explicitly set a non-empty name at creation time — those are
// excluded to avoid overwriting intentional names.
//
// Current rules:
//   - empty name → placeholder (always auto-title eligible)
//   - "New chat" prefix (case-insensitive) → placeholder
//   - leading "Chat " followed by a date / number → placeholder
//
// Any other non-empty name is treated as intentionally set and is skipped.
func isPlaceholderName(name string) bool {
	if name == "" {
		return true
	}
	lower := strings.ToLower(strings.TrimSpace(name))
	if strings.HasPrefix(lower, "new chat") {
		return true
	}
	if strings.HasPrefix(lower, "chat ") {
		return true
	}
	return false
}

// fireAutoTitle is called as a goroutine after a successful kernel run.
// It checks the session predicates, generates a title, and writes it back.
// All errors are logged; panics are recovered.
//
// Concurrency: runs in its own goroutine; uses a fresh context.Background()
// with 5-second timeout (NFR-001). Re-reads the session record under the
// store's own lock for race-safety.
func (r *ChatRunner) fireAutoTitle(sessionID, profileID, modelOverride string) {
	deps := r.cfg.AutoTitle
	if deps == nil {
		return
	}

	log := logging.L()

	// Recover any panic so the goroutine never takes down the process.
	defer func() {
		if rec := recover(); rec != nil {
			log.Error("chat.autotitle.panic", "session_id", sessionID, "panic", rec)
		}
	}()

	// Feature-flag check (WP05 wired the real flag via EffectiveEnabled).
	if !deps.autoTitleEnabled() {
		log.Debug("chat.autotitle.disabled", "session_id", sessionID)
		return
	}

	// Fresh 5-second context per NFR-001.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Re-read the session record under the store's own read lock.
	// If the user renamed between trigger schedule and fire, auto_titled
	// will already be 1 and AutoTitle will return ErrAutoTitleSuperseded.
	rec, err := deps.Manager.Get(ctx, sessionID)
	if err != nil {
		log.Warn("chat.autotitle.get_failed", "session_id", sessionID, "err", err.Error())
		return
	}

	// Predicate 1: auto_titled must be false.
	if rec.AutoTitled {
		log.Debug("chat.autotitle.already_titled", "session_id", sessionID)
		return
	}

	// Predicate 2: name must look like a placeholder.
	if !isPlaceholderName(rec.Name) {
		log.Debug("chat.autotitle.non_placeholder_name",
			"session_id", sessionID, "name", rec.Name)
		return
	}

	// Fetch the live messages (capped to maxMessagesForTitle most-recent).
	msgs, err := deps.Manager.ListMessagesActive(ctx, sessionID)
	if err != nil {
		log.Warn("chat.autotitle.list_messages_failed",
			"session_id", sessionID, "err", err.Error())
		_ = deps.Manager.MarkAutoTitleAttempted(ctx, sessionID)
		if deps.Audit != nil {
			deps.Audit.Emit(ctx, session.EventKindSessionAutoTitled, map[string]any{
				"session_id": sessionID,
				"trigger":    "auto",
				"error_kind": "list_messages_failed",
			})
		}
		return
	}

	// Cap to the most-recent 10 messages.
	if len(msgs) > maxMessagesForTitle {
		msgs = msgs[len(msgs)-maxMessagesForTitle:]
	}

	// Predicate 3: require at least one assistant message. If the kernel run
	// produced only a user message (e.g. the model never replied), skip.
	if !hasAssistantMessageInSession(msgs) {
		log.Debug("chat.autotitle.no_assistant_message", "session_id", sessionID)
		return
	}

	// Build the transcript — redact raw content from audit by only
	// passing it to the generator, never logging it.
	transcript := make(autotitle.Transcript, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == session.RoleUser || m.Role == session.RoleAssistant {
			transcript = append(transcript, autotitle.TranscriptMessage{
				Role: string(m.Role),
				Text: m.Content,
			})
		}
	}

	start := time.Now()
	generated, err := deps.Generator.GenerateTitle(ctx, transcript)
	durationMS := time.Since(start).Milliseconds()

	if err != nil {
		log.Warn("chat.autotitle.generate_failed",
			"session_id", sessionID, "err", err.Error())
		_ = deps.Manager.MarkAutoTitleAttempted(ctx, sessionID)
		if deps.Audit != nil {
			deps.Audit.Emit(ctx, session.EventKindSessionAutoTitled, map[string]any{
				"session_id":  sessionID,
				"trigger":     "auto",
				"error_kind":  err.Error(),
				"duration_ms": durationMS,
			})
		}
		return
	}

	// Write the generated title. Races with user Rename → AutoTitle returns
	// ErrAutoTitleSuperseded and we no-op silently.
	if werr := deps.Manager.AutoTitle(ctx, sessionID, generated); werr != nil {
		if errors.Is(werr, session.ErrAutoTitleSuperseded) {
			log.Debug("chat.autotitle.superseded", "session_id", sessionID)
		} else {
			log.Warn("chat.autotitle.write_failed",
				"session_id", sessionID, "err", werr.Error())
		}
		// Even on superseded we emit the audit so the dashboard sees the
		// "tried but lost race" signal.
		if deps.Audit != nil {
			deps.Audit.Emit(ctx, session.EventKindSessionAutoTitled, map[string]any{
				"session_id":  sessionID,
				"trigger":     "auto",
				"error_kind":  werr.Error(),
				"duration_ms": durationMS,
			})
		}
		return
	}

	// Success path: emit audit with safe fields only (transcript redacted).
	log.Info("chat.autotitle.success",
		"session_id", sessionID,
		"title", generated,
		"duration_ms", durationMS,
	)
	if deps.Audit != nil {
		deps.Audit.Emit(ctx, session.EventKindSessionAutoTitled, map[string]any{
			"session_id":  sessionID,
			"title":       generated,
			"trigger":     "auto",
			"duration_ms": durationMS,
		})
	}
	// Notify the frontend so the LeftRail updates without polling.
	if deps.Broker != nil {
		deps.Broker.Publish("session.list_changed", autoTitleListChangedPayload{
			Reason:    "title_set",
			SessionID: sessionID,
			Timestamp: time.Now().UnixMilli(),
		})
	}
}

// autoTitleListChangedPayload is the local mirror of
// rpc.SessionListChangedPayload. Duplicated here to avoid the import
// cycle rpc → sessions/autotitle → rpc. JSON field names are identical
// so the frontend deserialises them the same way.
type autoTitleListChangedPayload struct {
	Reason    string `json:"reason"`
	SessionID string `json:"sessionId,omitempty"`
	Timestamp int64  `json:"timestamp"`
}

// hasAssistantMessageInSession reports whether the session messages contain
// at least one non-empty assistant-role message. Used by tests and the
// fireAutoTitle gate to confirm a run produced meaningful output.
func hasAssistantMessageInSession(msgs []session.Message) bool {
	for _, m := range msgs {
		if m.Role == session.RoleAssistant && m.Content != "" {
			return true
		}
	}
	return false
}
