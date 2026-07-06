package fleet

// session_sync.go — session-event tail + sync toggle + audit.
//
// SessionSyncer wraps EventStream for session-specific events. The caller
// provides the session messages (from core/session.Manager); this layer
// handles encryption, fleet routing, toggle persistence, and audit.
//
// Toggle persistence: the sync-enabled flag is stored in the OS keychain
// under "fleet:<env>:session:<sessionID>:sync_enabled". No DB migration
// needed; the keychain is the right store for device-scoped fleet settings.
//
// Privacy invariant: session messages (user/assistant content) are encrypted
// before they leave this layer. No plaintext bytes appear in slog or audit.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/zalando/go-keyring"

	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/paths"
)

// ErrSessionSyncCapabilityRequired is returned when the user attempts to enable
// session sync but the context_sync capability is not in their tier.
var ErrSessionSyncCapabilityRequired = errors.New("fleet: context_sync capability required to enable session sync")

// SessionEventRecord is the opaque payload unit for a session event. The caller
// (session tail) marshals the session.Message into JSON and passes it here.
// This layer never inspects or logs the bytes.
//
// Privacy invariant: Bytes is NEVER logged.
type SessionEventRecord struct {
	// Seq is the message sequence within the session.
	Seq uint64
	// Bytes is the encrypted-ready payload (usually JSON of session.Message).
	Bytes []byte
}

// SessionSyncer bridges session events to an EventStream.
// Nil client means fleet is disabled; all operations become no-ops.
type SessionSyncer struct {
	client   *Client
	emitter  contextaudit.Emitter // may be nil
	caps     *Capabilities     // may be nil
}

// NewSessionSyncer constructs a SessionSyncer.
// client may be nil (fleet-disabled path).
// emitter may be nil (audit-free paths, e.g. tests).
// caps may be nil (no tier check).
func NewSessionSyncer(client *Client, emitter contextaudit.Emitter, caps *Capabilities) *SessionSyncer {
	return &SessionSyncer{client: client, emitter: emitter, caps: caps}
}

// EnableSync enables fleet sync for sessionID, derives its stream key,
// backfills existingEvents, and persists the toggle.
//
// Returns ErrSessionSyncCapabilityRequired when the user's tier lacks
// context_sync. Returns ErrFleetDisabled for nop clients.
func (ss *SessionSyncer) EnableSync(ctx context.Context, sessionID string, existingEvents []SessionEventRecord) error {
	if ss.client == nil || ss.client.isNop {
		return ErrFleetDisabled
	}
	if ss.caps != nil && !ss.caps.Has(CapContextSync) {
		return ErrSessionSyncCapabilityRequired
	}

	seed, err := SeedKey()
	if err != nil {
		return fmt.Errorf("fleet: session sync enable: seed: %w", err)
	}
	key, err := DeriveKey(seed, LabelSessionEvents)
	if err != nil {
		return fmt.Errorf("fleet: session sync enable: derive key: %w", err)
	}

	streamID := sessionStreamID(sessionID)
	es, err := NewEventStream(ss.client, streamID, key)
	if err != nil {
		return fmt.Errorf("fleet: session sync enable: stream: %w", err)
	}

	// Backfill existing events.
	if len(existingEvents) > 0 {
		batch := make([]Event, 0, len(existingEvents))
		for _, r := range existingEvents {
			batch = append(batch, Event{Seq: r.Seq, Payload: r.Bytes})
		}
		if err := es.Backfill(ctx, batch); err != nil {
			return fmt.Errorf("fleet: session sync backfill: %w", err)
		}
	}

	// Persist toggle.
	if err := setSessionSyncEnabled(sessionID, true); err != nil {
		return fmt.Errorf("fleet: session sync persist toggle: %w", err)
	}

	logging.L().Info("fleet.session_sync.enabled",
		"session_id", shortID(sessionID),
		"stream_id", shortID(streamID),
		"backfill_count", len(existingEvents),
	)
	ss.emitAudit(ctx, contextaudit.KindFleetSessionSyncEnabled, contextaudit.FleetSessionSyncPayload{
		SessionID: sessionID,
		StreamID:  streamID,
	})
	return nil
}

// DisableSync disables fleet sync for sessionID. Does not delete remote events.
func (ss *SessionSyncer) DisableSync(ctx context.Context, sessionID string) error {
	if ss.client == nil || ss.client.isNop {
		return ErrFleetDisabled
	}

	if err := setSessionSyncEnabled(sessionID, false); err != nil {
		return fmt.Errorf("fleet: session sync disable: %w", err)
	}

	streamID := sessionStreamID(sessionID)
	logging.L().Info("fleet.session_sync.disabled",
		"session_id", shortID(sessionID),
	)
	ss.emitAudit(ctx, contextaudit.KindFleetSessionSyncDisabled, contextaudit.FleetSessionSyncPayload{
		SessionID: sessionID,
		StreamID:  streamID,
	})
	return nil
}

// Resume replays fleet events for sessionID from sinceSeq and calls apply for
// each decrypted record. Used when opening a session on a second device.
func (ss *SessionSyncer) Resume(ctx context.Context, sessionID string, sinceSeq uint64, apply func(SessionEventRecord) error) error {
	if ss.client == nil || ss.client.isNop {
		return ErrFleetDisabled
	}

	seed, err := LoadContextSeed()
	if err != nil {
		return fmt.Errorf("fleet: session resume: load seed: %w", err)
	}
	key, err := DeriveKey(seed, LabelSessionEvents)
	if err != nil {
		return fmt.Errorf("fleet: session resume: derive key: %w", err)
	}

	streamID := sessionStreamID(sessionID)
	es, err := NewEventStream(ss.client, streamID, key)
	if err != nil {
		return fmt.Errorf("fleet: session resume: stream: %w", err)
	}

	var count int
	err = es.Replay(ctx, sinceSeq, func(ev Event) error {
		count++
		return apply(SessionEventRecord{Seq: ev.Seq, Bytes: ev.Payload})
	})
	if err != nil {
		return fmt.Errorf("fleet: session resume replay: %w", err)
	}

	logging.L().Info("fleet.session_sync.resumed",
		"session_id", shortID(sessionID),
		"events_replayed", count,
	)
	ss.emitAudit(ctx, contextaudit.KindFleetSessionSyncResumed, contextaudit.FleetSessionSyncResumedPayload{
		SessionID:      sessionID,
		StreamID:       streamID,
		EventsReplayed: count,
	})
	return nil
}

// AppendEvent encrypts and appends a single session event to fleet.
// Intended for streaming: called after each new session.Message is persisted.
func (ss *SessionSyncer) AppendEvent(ctx context.Context, sessionID string, record SessionEventRecord) error {
	if ss.client == nil || ss.client.isNop {
		return ErrFleetDisabled
	}
	if !isSessionSyncEnabled(sessionID) {
		return nil // sync not enabled for this session
	}

	seed, err := LoadContextSeed()
	if err != nil {
		return fmt.Errorf("fleet: session append event: load seed: %w", err)
	}
	key, err := DeriveKey(seed, LabelSessionEvents)
	if err != nil {
		return fmt.Errorf("fleet: session append event: derive key: %w", err)
	}

	streamID := sessionStreamID(sessionID)
	es, err := NewEventStream(ss.client, streamID, key)
	if err != nil {
		return fmt.Errorf("fleet: session append event: stream: %w", err)
	}
	return es.Append(ctx, []Event{{Seq: record.Seq, Payload: record.Bytes}})
}

// DeleteRemote purges all fleet events for sessionID and disables sync.
func (ss *SessionSyncer) DeleteRemote(ctx context.Context, sessionID string) error {
	if ss.client == nil || ss.client.isNop {
		return ErrFleetDisabled
	}

	seed, err := LoadContextSeed()
	if err != nil {
		return fmt.Errorf("fleet: session delete remote: load seed: %w", err)
	}
	key, err := DeriveKey(seed, LabelSessionEvents)
	if err != nil {
		return fmt.Errorf("fleet: session delete remote: derive key: %w", err)
	}

	streamID := sessionStreamID(sessionID)
	es, err := NewEventStream(ss.client, streamID, key)
	if err != nil {
		return fmt.Errorf("fleet: session delete remote: stream: %w", err)
	}

	if err := es.DeleteRemote(ctx); err != nil {
		return err
	}
	_ = setSessionSyncEnabled(sessionID, false) // best-effort

	logging.L().Info("fleet.session_sync.remote_deleted",
		"session_id", shortID(sessionID),
	)
	ss.emitAudit(ctx, contextaudit.KindFleetSessionSyncRemoteDeleted, contextaudit.FleetSessionSyncPayload{
		SessionID: sessionID,
		StreamID:  streamID,
	})
	return nil
}

// IsSyncEnabled returns whether fleet sync is currently enabled for sessionID.
func (ss *SessionSyncer) IsSyncEnabled(sessionID string) bool {
	return isSessionSyncEnabled(sessionID)
}

// ── persistence helpers ───────────────────────────────────────────────────────

// sessionStreamID returns the deterministic fleet stream ID for a session.
// The stream ID is the session ID prefixed with "sess:" so it's unambiguous.
func sessionStreamID(sessionID string) string {
	return "sess:" + sessionID
}

// sessionSyncKey returns the keychain account key for the session sync flag.
func sessionSyncKey(sessionID string) string {
	return "fleet:" + paths.KeychainNamespace() + ":session:" + sessionID + ":sync_enabled"
}

// setSessionSyncEnabled persists the sync toggle for a session in the keychain.
func setSessionSyncEnabled(sessionID string, enabled bool) error {
	svc := paths.FleetKeychainService()
	val := "0"
	if enabled {
		val = "1"
	}
	if err := keyring.Set(svc, sessionSyncKey(sessionID), val); err != nil {
		return fmt.Errorf("fleet: set session sync flag: %w", err)
	}
	return nil
}

// isSessionSyncEnabled reads the sync toggle from the keychain.
func isSessionSyncEnabled(sessionID string) bool {
	svc := paths.FleetKeychainService()
	val, err := keyring.Get(svc, sessionSyncKey(sessionID))
	if err != nil {
		return false
	}
	return val == "1"
}

// ── audit helper ──────────────────────────────────────────────────────────────

// emitAudit emits an audit event. Silently no-ops when emitter is nil or
// when payload marshal fails.
func (ss *SessionSyncer) emitAudit(ctx context.Context, kind contextaudit.Kind, payload any) {
	if ss.emitter == nil {
		return
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		logging.L().Warn("fleet.session_sync.audit_marshal_failed", "kind", string(kind), "err", err.Error())
		return
	}
	_ = ss.emitter.Emit(ctx, contextaudit.Event{
		Kind:    kind,
		TS:      time.Now().UTC(),
		Payload: raw,
	})
}
