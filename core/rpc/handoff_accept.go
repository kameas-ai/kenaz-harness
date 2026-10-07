package rpc

// handoff_accept.go — persisting an accepted team handoff as a NEW local
// session (device-keys-handoff-01DEVKH01 WP05, OQ-2 ruling).
//
// Shape: a new session titled from the handoff ("<sender's title> — shared
// by <email>", or "Shared by <email>"), its transcript written through the
// sanctioned cross-session replay seam session.Manager.ReplayTranscript
// (one writer call site, allowlisted in
// scripts/ci/allowlists/i-session-message-writers.txt), never a new writer.
//
// Provenance + dedupe: <dataDir>/fleet/handoff_accepted.json maps inbox
// item id → {local session id, sender, source session id, accepted at}.
// A second accept of the same item returns the existing local session as
// long as it still exists (a user who deleted their copy can accept again
// while fleet still holds the item). No SQL migration: the session row
// itself is unchanged; the sidecar is a real file under the data dir
// (tmp+rename, 0600), the same durability class as identity.json.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logging"
	contextsyncview "github.com/kameas-ai/kenaz-harness/core/rpc/views/contextsync"
	"github.com/kameas-ai/kenaz-harness/core/session"
)

// acceptedHandoffEntry is one provenance record.
type acceptedHandoffEntry struct {
	LocalSessionID  string    `json:"local_session_id"`
	SenderUserID    string    `json:"sender_user_id"`
	SenderEmail     string    `json:"sender_email,omitempty"`
	SourceSessionID string    `json:"source_session_id"`
	Title           string    `json:"title"`
	EventCount      int       `json:"event_count"`
	AcceptedAt      time.Time `json:"accepted_at"`
}

// handoffAcceptStore implements contextsyncview.AcceptedSessionStore.
type handoffAcceptStore struct {
	sessions *session.Manager
	dataDir  string

	mu sync.Mutex // serialises ledger read-modify-write + persist
}

func newHandoffAcceptStore(sessions *session.Manager, dataDir string) *handoffAcceptStore {
	return &handoffAcceptStore{sessions: sessions, dataDir: dataDir}
}

func (s *handoffAcceptStore) ledgerPath() string {
	return filepath.Join(s.dataDir, "fleet", "handoff_accepted.json")
}

func (s *handoffAcceptStore) readLedger() map[string]acceptedHandoffEntry {
	out := map[string]acceptedHandoffEntry{}
	if s.dataDir == "" {
		return out
	}
	data, err := os.ReadFile(s.ledgerPath())
	if err != nil {
		return out
	}
	if err := json.Unmarshal(data, &out); err != nil {
		logging.L().Warn("rpc.handoff.accepted_ledger_unreadable", "err", err.Error())
		return map[string]acceptedHandoffEntry{}
	}
	return out
}

func (s *handoffAcceptStore) writeLedger(l map[string]acceptedHandoffEntry) error {
	if s.dataDir == "" {
		return nil
	}
	dir := filepath.Dir(s.ledgerPath())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.ledgerPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.ledgerPath())
}

// existing returns the entry for inboxItemID when its session still exists.
// Caller holds s.mu.
func (s *handoffAcceptStore) existing(ctx context.Context, inboxItemID string) (acceptedHandoffEntry, bool) {
	e, ok := s.readLedger()[inboxItemID]
	if !ok || e.LocalSessionID == "" || s.sessions == nil {
		return acceptedHandoffEntry{}, false
	}
	if _, err := s.sessions.Get(ctx, e.LocalSessionID); err != nil {
		return acceptedHandoffEntry{}, false
	}
	return e, true
}

func entryView(e acceptedHandoffEntry) contextsyncview.AcceptedSessionView {
	return contextsyncview.AcceptedSessionView{LocalSessionID: e.LocalSessionID, EventCount: e.EventCount, Title: e.Title}
}

// Lookup implements contextsyncview.AcceptedSessionStore.
func (s *handoffAcceptStore) Lookup(ctx context.Context, inboxItemID string) (contextsyncview.AcceptedSessionView, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.existing(ctx, inboxItemID)
	if !ok {
		return contextsyncview.AcceptedSessionView{}, false
	}
	return entryView(e), true
}

// acceptedTitle names the new local session.
func acceptedTitle(sourceTitle, senderEmail string) string {
	from := strings.TrimSpace(senderEmail)
	if from == "" {
		from = "a teammate"
	}
	if t := strings.TrimSpace(sourceTitle); t != "" {
		return t + " — shared by " + from
	}
	return "Shared by " + from
}

// Persist implements contextsyncview.AcceptedSessionStore.
func (s *handoffAcceptStore) Persist(ctx context.Context, rec contextsyncview.AcceptedShareRecord) (contextsyncview.AcceptedSessionView, error) {
	if s == nil || s.sessions == nil {
		return contextsyncview.AcceptedSessionView{}, contextsyncview.ErrHandoffAcceptUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.existing(ctx, rec.InboxItemID); ok {
		v := entryView(e)
		v.AlreadyAccepted = true
		return v, nil
	}
	payloads := make([][]byte, len(rec.Events))
	for i, ev := range rec.Events {
		if ev.Seq != uint64(i+1) {
			return contextsyncview.AcceptedSessionView{}, fmt.Errorf("shared session events out of order (event %d has seq %d)", i+1, ev.Seq)
		}
		payloads[i] = ev.Bytes
	}
	tr, err := session.DecodeHandoffTranscript(payloads)
	if err != nil {
		return contextsyncview.AcceptedSessionView{}, err
	}
	title := acceptedTitle(tr.Title, rec.SenderEmail)
	created, err := s.sessions.Create(ctx, title)
	if err != nil {
		return contextsyncview.AcceptedSessionView{}, fmt.Errorf("create local session: %w", err)
	}
	if _, err := s.sessions.ReplayTranscript(ctx, created.ID, tr.Messages); err != nil {
		// Never leave a half-imported session behind.
		if delErr := s.sessions.Delete(ctx, created.ID); delErr != nil {
			logging.L().Warn("rpc.handoff.accept_cleanup_failed", "err", delErr.Error())
		}
		return contextsyncview.AcceptedSessionView{}, fmt.Errorf("write shared transcript: %w", err)
	}
	entry := acceptedHandoffEntry{
		LocalSessionID:  created.ID,
		SenderUserID:    rec.SenderUserID,
		SenderEmail:     rec.SenderEmail,
		SourceSessionID: rec.SessionID,
		Title:           title,
		EventCount:      len(tr.Messages),
		AcceptedAt:      time.Now().UTC(),
	}
	l := s.readLedger()
	l[rec.InboxItemID] = entry
	if err := s.writeLedger(l); err != nil {
		// The session exists; only dedupe is weakened. Say so, don't fail.
		logging.L().Warn("rpc.handoff.accepted_ledger_write_failed", "err", err.Error())
	}
	logging.L().Info("rpc.handoff.accepted_persisted", "events", entry.EventCount)
	return entryView(entry), nil
}
