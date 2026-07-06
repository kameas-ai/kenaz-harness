package contextsync_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/rpc/views/contextsync"
)

// ── stubs ─────────────────────────────────────────────────────────────────────

type stubSessionBackend struct {
	mu      sync.Mutex
	enabled map[string]bool
	err     error
}

func (s *stubSessionBackend) EnableSync(_ context.Context, sessionID string, _ []contextsync.SessionEventRecord) error {
	if s.err != nil {
		return s.err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.enabled == nil {
		s.enabled = map[string]bool{}
	}
	s.enabled[sessionID] = true
	return nil
}

func (s *stubSessionBackend) DisableSync(_ context.Context, sessionID string) error {
	if s.err != nil {
		return s.err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.enabled != nil {
		s.enabled[sessionID] = false
	}
	return nil
}

func (s *stubSessionBackend) Resume(_ context.Context, _ string, _ uint64, apply func(contextsync.SessionEventRecord) error) error {
	if s.err != nil {
		return s.err
	}
	// Return 3 synthetic events.
	for i := uint64(0); i < 3; i++ {
		if err := apply(contextsync.SessionEventRecord{Seq: i + 1, Bytes: []byte("encrypted")}); err != nil {
			return err
		}
	}
	return nil
}

func (s *stubSessionBackend) DeleteRemote(_ context.Context, _ string) error { return s.err }

func (s *stubSessionBackend) IsSyncEnabled(sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enabled != nil && s.enabled[sessionID]
}

type stubProjectBackend struct {
	mu      sync.Mutex
	enabled map[string]bool
	opts    map[string]contextsync.ProjectSyncOpts
	err     error
}

func (s *stubProjectBackend) EnableSync(_ context.Context, projectID string, _ []contextsync.ProjectEventRecord, opts contextsync.ProjectSyncOpts) error {
	if s.err != nil {
		return s.err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.enabled == nil {
		s.enabled = map[string]bool{}
	}
	if s.opts == nil {
		s.opts = map[string]contextsync.ProjectSyncOpts{}
	}
	s.enabled[projectID] = true
	s.opts[projectID] = opts
	return nil
}

func (s *stubProjectBackend) DisableSync(_ context.Context, projectID string) error {
	if s.err != nil {
		return s.err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.enabled != nil {
		s.enabled[projectID] = false
	}
	return nil
}

func (s *stubProjectBackend) DeleteRemote(_ context.Context, _ string) error { return s.err }

func (s *stubProjectBackend) SetArtifactClassOptions(projectID string, opts contextsync.ProjectSyncOpts) error {
	if s.err != nil {
		return s.err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.opts == nil {
		s.opts = map[string]contextsync.ProjectSyncOpts{}
	}
	s.opts[projectID] = opts
	return nil
}

func (s *stubProjectBackend) GetArtifactClassOptions(projectID string) contextsync.ProjectSyncOpts {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.opts == nil {
		return contextsync.ProjectSyncOpts{Notes: true, Memory: true}
	}
	return s.opts[projectID]
}

func (s *stubProjectBackend) IsSyncEnabled(projectID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enabled != nil && s.enabled[projectID]
}

type stubHandoffBackend struct {
	err    error
	shared []string // sessionID sent via ShareSession
}

func (s *stubHandoffBackend) ListTeam(_ context.Context) ([]contextsync.TeamMemberRecord, error) {
	if s.err != nil {
		return nil, s.err
	}
	return []contextsync.TeamMemberRecord{
		{UserID: "u1", DisplayName: "Alice", Email: "alice@example.com"},
	}, nil
}

func (s *stubHandoffBackend) ShareSession(_ context.Context, sessionID, _ string, _ []contextsync.SessionEventRecord) error {
	if s.err != nil {
		return s.err
	}
	s.shared = append(s.shared, sessionID)
	return nil
}

func (s *stubHandoffBackend) Inbox(_ context.Context) ([]contextsync.InboxItemRecord, error) {
	if s.err != nil {
		return nil, s.err
	}
	return []contextsync.InboxItemRecord{
		{InboxItemID: "item-1", SessionID: "sess-abc", SenderUserID: "u2", ReceivedAt: "2026-01-01T00:00:00Z"},
	}, nil
}

func (s *stubHandoffBackend) AcceptShare(_ context.Context, _ string) ([]contextsync.SessionEventRecord, error) {
	if s.err != nil {
		return nil, s.err
	}
	return []contextsync.SessionEventRecord{
		{Seq: 1, Bytes: []byte("decrypted")},
		{Seq: 2, Bytes: []byte("decrypted")},
	}, nil
}

type stubRecoveryBackend struct{ err error }

func (s *stubRecoveryBackend) GenerateRecoveryCode() (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return "KENAZ-ABCDEFGH-IJKLMNOP-QRSTUVWX-YZABCDEF", nil
}

func (s *stubRecoveryBackend) ApplyRecoveryCode(_ string) error { return s.err }

// ── tests ─────────────────────────────────────────────────────────────────────

func TestImpl_SessionSync_Toggle(t *testing.T) {
	sb := &stubSessionBackend{}
	im := &contextsync.Impl{Session: sb}

	status, err := im.SessionSync_Toggle(context.Background(), "sess-1", true)
	if err != nil {
		t.Fatalf("Toggle enable: %v", err)
	}
	if !status.Enabled {
		t.Error("expected Enabled=true after enable")
	}

	status, err = im.SessionSync_Toggle(context.Background(), "sess-1", false)
	if err != nil {
		t.Fatalf("Toggle disable: %v", err)
	}
	if status.Enabled {
		t.Error("expected Enabled=false after disable")
	}
}

func TestImpl_SessionSync_ResumeFrom(t *testing.T) {
	sb := &stubSessionBackend{}
	im := &contextsync.Impl{Session: sb}

	count, err := im.SessionSync_ResumeFrom(context.Background(), "sess-1", 0)
	if err != nil {
		t.Fatalf("ResumeFrom: %v", err)
	}
	if count != 3 {
		t.Errorf("expected 3 events, got %d", count)
	}
}

func TestImpl_SessionSync_Unavailable(t *testing.T) {
	im := &contextsync.Impl{} // nil Session

	_, err := im.SessionSync_Toggle(context.Background(), "sess-1", true)
	if !errors.Is(err, contextsync.ErrContextSyncUnavailable) {
		t.Errorf("expected ErrContextSyncUnavailable, got %v", err)
	}
}

func TestImpl_ProjectSync_Toggle(t *testing.T) {
	pb := &stubProjectBackend{}
	im := &contextsync.Impl{Project: pb}

	status, err := im.ProjectSync_Toggle(context.Background(), "proj-1", true)
	if err != nil {
		t.Fatalf("ProjectSync enable: %v", err)
	}
	if !status.Enabled {
		t.Error("expected Enabled=true")
	}
}

func TestImpl_ProjectSync_SetArtifactClass(t *testing.T) {
	pb := &stubProjectBackend{}
	im := &contextsync.Impl{Project: pb}

	// Enable first.
	if _, err := im.ProjectSync_Toggle(context.Background(), "proj-1", true); err != nil {
		t.Fatalf("enable: %v", err)
	}

	opts := contextsync.ArtifactClassOptionsView{Notes: true, Binaries: true, Memory: false}
	status, err := im.ProjectSync_SetArtifactClass(context.Background(), "proj-1", opts)
	if err != nil {
		t.Fatalf("SetArtifactClass: %v", err)
	}
	if !status.ArtifactClassOptions.Binaries {
		t.Error("expected Binaries=true after set")
	}
}

func TestImpl_Handoff_ListTeam(t *testing.T) {
	hb := &stubHandoffBackend{}
	im := &contextsync.Impl{Handoff: hb}

	members, err := im.Handoff_ListTeam(context.Background())
	if err != nil {
		t.Fatalf("ListTeam: %v", err)
	}
	if len(members) != 1 || members[0].UserID != "u1" {
		t.Errorf("unexpected members: %v", members)
	}
}

func TestImpl_Handoff_Inbox(t *testing.T) {
	hb := &stubHandoffBackend{}
	im := &contextsync.Impl{Handoff: hb}

	items, err := im.Handoff_Inbox(context.Background())
	if err != nil {
		t.Fatalf("Inbox: %v", err)
	}
	if len(items) != 1 || items[0].InboxItemID != "item-1" {
		t.Errorf("unexpected inbox: %v", items)
	}
}

func TestImpl_Handoff_Accept(t *testing.T) {
	hb := &stubHandoffBackend{}
	im := &contextsync.Impl{Handoff: hb}

	accepted, err := im.Handoff_Accept(context.Background(), "item-1")
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if accepted.EventCount != 2 {
		t.Errorf("expected 2 events, got %d", accepted.EventCount)
	}
}

func TestImpl_ContextSync_RecoveryCode(t *testing.T) {
	rb := &stubRecoveryBackend{}
	im := &contextsync.Impl{Recovery: rb}

	code, err := im.ContextSync_GenerateRecoveryCode(context.Background())
	if err != nil {
		t.Fatalf("GenerateRecoveryCode: %v", err)
	}
	if code == "" {
		t.Error("expected non-empty recovery code")
	}

	if err := im.ContextSync_ApplyRecoveryCode(context.Background(), code); err != nil {
		t.Fatalf("ApplyRecoveryCode: %v", err)
	}
}

func TestImpl_AllMethods_NilBackends(t *testing.T) {
	im := &contextsync.Impl{} // all nil

	if _, err := im.SessionSync_Toggle(context.Background(), "s", true); !errors.Is(err, contextsync.ErrContextSyncUnavailable) {
		t.Errorf("SessionSync_Toggle: got %v", err)
	}
	if err := im.SessionSync_DeleteRemote(context.Background(), "s"); !errors.Is(err, contextsync.ErrContextSyncUnavailable) {
		t.Errorf("SessionSync_DeleteRemote: got %v", err)
	}
	if _, err := im.SessionSync_ResumeFrom(context.Background(), "s", 0); !errors.Is(err, contextsync.ErrContextSyncUnavailable) {
		t.Errorf("SessionSync_ResumeFrom: got %v", err)
	}
	if _, err := im.ProjectSync_Toggle(context.Background(), "p", true); !errors.Is(err, contextsync.ErrContextSyncUnavailable) {
		t.Errorf("ProjectSync_Toggle: got %v", err)
	}
	if err := im.ProjectSync_DeleteRemote(context.Background(), "p"); !errors.Is(err, contextsync.ErrContextSyncUnavailable) {
		t.Errorf("ProjectSync_DeleteRemote: got %v", err)
	}
	if _, err := im.ProjectSync_SetArtifactClass(context.Background(), "p", contextsync.ArtifactClassOptionsView{}); !errors.Is(err, contextsync.ErrContextSyncUnavailable) {
		t.Errorf("ProjectSync_SetArtifactClass: got %v", err)
	}
	if _, err := im.Handoff_ListTeam(context.Background()); !errors.Is(err, contextsync.ErrContextSyncUnavailable) {
		t.Errorf("Handoff_ListTeam: got %v", err)
	}
	if err := im.Handoff_Share(context.Background(), "s", "r"); !errors.Is(err, contextsync.ErrContextSyncUnavailable) {
		t.Errorf("Handoff_Share: got %v", err)
	}
	if _, err := im.Handoff_Inbox(context.Background()); !errors.Is(err, contextsync.ErrContextSyncUnavailable) {
		t.Errorf("Handoff_Inbox: got %v", err)
	}
	if _, err := im.Handoff_Accept(context.Background(), "i"); !errors.Is(err, contextsync.ErrContextSyncUnavailable) {
		t.Errorf("Handoff_Accept: got %v", err)
	}
	if _, err := im.ContextSync_GenerateRecoveryCode(context.Background()); !errors.Is(err, contextsync.ErrContextSyncUnavailable) {
		t.Errorf("ContextSync_GenerateRecoveryCode: got %v", err)
	}
	if err := im.ContextSync_ApplyRecoveryCode(context.Background(), "code"); !errors.Is(err, contextsync.ErrContextSyncUnavailable) {
		t.Errorf("ContextSync_ApplyRecoveryCode: got %v", err)
	}
}
