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
// item id → {local session id, state, sender, source session id, accepted
// at, fleet copy deleted}. Idempotent on the inbox item id (review fix #7):
// the entry is written in state "importing" right after the session row is
// created and BEFORE the transcript replay; it flips to "complete" only
// after the replay. A crash anywhere in between leaves an "importing"
// entry, and the next accept deletes that partial session and imports
// again — never a second session alongside the first. The RPC layer deletes
// the fleet copy only after Persist returned (so after the ledger write),
// and retries a failed delete on the next re-open. A user who deleted
// their local copy can accept again while fleet still holds the item. No SQL migration: the session row
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
	LocalSessionID string `json:"local_session_id"`
	// State is "importing" (session created, transcript not yet fully
	// written) or "complete". Empty reads as complete.
	State string `json:"state,omitempty"`
	// FleetDeleted is true once the post-accept fleet DELETE succeeded.
	FleetDeleted    bool      `json:"fleet_deleted,omitempty"`
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

const (
	acceptStateImporting = "importing"
	acceptStateComplete  = "complete"
)

// existing returns the COMPLETE entry for inboxItemID when its session still
// exists. Caller holds s.mu.
func (s *handoffAcceptStore) existing(ctx context.Context, inboxItemID string) (acceptedHandoffEntry, bool) {
	e, ok := s.readLedger()[inboxItemID]
	if !ok || e.LocalSessionID == "" || s.sessions == nil || e.State == acceptStateImporting {
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

// FleetCopyDeleted implements contextsyncview.AcceptedSessionStore.
func (s *handoffAcceptStore) FleetCopyDeleted(_ context.Context, inboxItemID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readLedger()[inboxItemID].FleetDeleted
}

// MarkFleetCopyDeleted implements contextsyncview.AcceptedSessionStore.
func (s *handoffAcceptStore) MarkFleetCopyDeleted(_ context.Context, inboxItemID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.readLedger()
	e, ok := l[inboxItemID]
	if !ok {
		return nil
	}
	e.FleetDeleted = true
	l[inboxItemID] = e
	return s.writeLedger(l)
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

// persistStageHook, when set (tests), runs at the two crash windows review
// fix #7 closes: "before_replay" (importing recorded, transcript not
// written) and "after_replay" (transcript written, not yet complete).
var persistStageHook func(stage string)

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

	l := s.readLedger()
	prev, hadPrev := l[rec.InboxItemID]
	if hadPrev && prev.State == acceptStateImporting && prev.LocalSessionID != "" {
		// A previous import of THIS item was interrupted: its session may
		// hold a partial transcript. Remove it, then import afresh.
		if _, err := s.sessions.Get(ctx, prev.LocalSessionID); err == nil {
			if err := s.sessions.Delete(ctx, prev.LocalSessionID); err != nil {
				return contextsyncview.AcceptedSessionView{}, fmt.Errorf("remove interrupted import: %w", err)
			}
			logging.L().Info("rpc.handoff.interrupted_import_removed")
		}
	}

	title := acceptedTitle(tr.Title, rec.SenderEmail)
	created, err := s.sessions.Create(ctx, title)
	if err != nil {
		return contextsyncview.AcceptedSessionView{}, fmt.Errorf("create local session: %w", err)
	}
	entry := acceptedHandoffEntry{
		LocalSessionID:  created.ID,
		State:           acceptStateImporting,
		SenderUserID:    rec.SenderUserID,
		SenderEmail:     rec.SenderEmail,
		SourceSessionID: rec.SessionID,
		Title:           title,
		EventCount:      len(tr.Messages),
		AcceptedAt:      time.Now().UTC(),
	}
	l[rec.InboxItemID] = entry
	if err := s.writeLedger(l); err != nil {
		// Without the record the import is not idempotent: refuse.
		_ = s.sessions.Delete(ctx, created.ID)
		return contextsyncview.AcceptedSessionView{}, fmt.Errorf("record shared-session provenance: %w", err)
	}
	if persistStageHook != nil {
		persistStageHook("before_replay")
	}
	if _, err := s.sessions.ReplayTranscript(ctx, created.ID, tr.Messages); err != nil {
		// Never leave a half-imported session behind.
		if delErr := s.sessions.Delete(ctx, created.ID); delErr != nil {
			logging.L().Warn("rpc.handoff.accept_cleanup_failed", "err", delErr.Error())
		}
		return contextsyncview.AcceptedSessionView{}, fmt.Errorf("write shared transcript: %w", err)
	}
	if persistStageHook != nil {
		persistStageHook("after_replay")
	}
	entry.State = acceptStateComplete
	l[rec.InboxItemID] = entry
	if err := s.writeLedger(l); err != nil {
		// The import is whole but not marked so: report failure (the RPC
		// layer then keeps the fleet copy); a retry replaces this session
		// rather than adding a second one.
		return contextsyncview.AcceptedSessionView{}, fmt.Errorf("record shared-session provenance: %w", err)
	}
	logging.L().Info("rpc.handoff.accepted_persisted", "events", entry.EventCount)
	return entryView(entry), nil
}
