package contextsync_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/contextsync"
)

// ── stubs ─────────────────────────────────────────────────────────────────────

type stubSessionBackend struct {
	mu                sync.Mutex
	enabled           map[string]bool
	err               error
	deleteRemoteCalls []string // sessionIDs actually passed to DeleteRemote
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

func (s *stubSessionBackend) DeleteRemote(_ context.Context, sessionID string) error {
	s.mu.Lock()
	s.deleteRemoteCalls = append(s.deleteRemoteCalls, sessionID)
	s.mu.Unlock()
	return s.err
}

// deleteRemoteCallCount reports how many times DeleteRemote actually ran —
// the "nothing was deleted" assertion needs to know this was never
// invoked, not just that an error came back from somewhere else.
func (s *stubSessionBackend) deleteRemoteCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.deleteRemoteCalls)
}

func (s *stubSessionBackend) IsSyncEnabled(sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enabled != nil && s.enabled[sessionID]
}

type stubProjectBackend struct {
	mu                sync.Mutex
	enabled           map[string]bool
	opts              map[string]contextsync.ProjectSyncOpts
	err               error
	deleteRemoteCalls []string // projectIDs actually passed to DeleteRemote
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

func (s *stubProjectBackend) DeleteRemote(_ context.Context, projectID string) error {
	s.mu.Lock()
	s.deleteRemoteCalls = append(s.deleteRemoteCalls, projectID)
	s.mu.Unlock()
	return s.err
}

// deleteRemoteCallCount reports how many times DeleteRemote actually ran —
// see stubSessionBackend.deleteRemoteCallCount's doc for why this matters.
func (s *stubProjectBackend) deleteRemoteCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.deleteRemoteCalls)
}

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

// ── Cedar gate integration (fleet-enforcement-truth-01PMZ505 WP13, owner
// ruling G-7: "gate the DESTRUCTIVE operations now — purge and delete —
// fail-closed") ─────────────────────────────────────────────────────────────
//
// These prove the gate at the Impl boundary three ways, per the mission
// brief: (1) an explicit Deny refuses the purge AND nothing is deleted —
// asserted via the stub's call count, not just the returned error; (2) a
// permit still allows it, so the gate discriminates rather than
// blanket-refusing; (3) engine.go's shipped default policy is what makes
// the ordinary local-user purge keep working — a raw AllowAll{} gate (the
// nil-engine production fallback) also permits, matching (2).

func newForbidEngine(t *testing.T, action string) cedar.Gate {
	t.Helper()
	src := `
forbid (
    principal,
    action == Action::"` + action + `",
    resource
);
`
	e, err := cedar.NewEngine(cedar.Options{IncludeEmbedded: false, LoadFromDisk: false})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if err := e.SetPolicyText("forbid.cedar", []byte(src)); err != nil {
		t.Fatalf("SetPolicyText: %v", err)
	}
	return e
}

func newPermitEngine(t *testing.T) cedar.Gate {
	t.Helper()
	e, err := cedar.NewEngine(cedar.Options{IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

// TestImpl_SessionSync_DeleteRemote_DeniedByPolicy_NothingDeleted is the
// falsifiable core of the gate: with an explicit Cedar forbid installed,
// SessionSync_DeleteRemote must error AND the backend's DeleteRemote must
// never have run — asserted via the call counter, not just the error
// return, so a bug that deletes-then-reports-an-error would still fail
// this test.
func TestImpl_SessionSync_DeleteRemote_DeniedByPolicy_NothingDeleted(t *testing.T) {
	sb := &stubSessionBackend{}
	im := &contextsync.Impl{Session: sb, Gate: newForbidEngine(t, "context_sync.session.purge")}

	err := im.SessionSync_DeleteRemote(context.Background(), "sess-1")
	if err == nil {
		t.Fatal("expected policy denial, got nil error")
	}
	var pde *cedar.PolicyDeniedError
	if !errors.As(err, &pde) {
		t.Fatalf("expected error to wrap *cedar.PolicyDeniedError, got %T: %v", err, err)
	}
	if got := sb.deleteRemoteCallCount(); got != 0 {
		t.Fatalf("backend DeleteRemote was called %d times; want 0 (nothing may be deleted on Deny)", got)
	}
}

// TestImpl_SessionSync_DeleteRemote_Permitted_DeletesOnce is the paired
// positive: a permit reaches the backend and discriminates from the deny
// case above — the gate is not a blanket refusal.
func TestImpl_SessionSync_DeleteRemote_Permitted_DeletesOnce(t *testing.T) {
	sb := &stubSessionBackend{}
	im := &contextsync.Impl{Session: sb, Gate: newPermitEngine(t)}

	if err := im.SessionSync_DeleteRemote(context.Background(), "sess-1"); err != nil {
		t.Fatalf("expected the shipped default policy to permit, got denied: %v", err)
	}
	if got := sb.deleteRemoteCallCount(); got != 1 {
		t.Fatalf("backend DeleteRemote was called %d times; want exactly 1", got)
	}
}

// TestImpl_ProjectSync_DeleteRemote_DeniedByPolicy_NothingDeleted is the
// project-scoped twin of the session test above.
func TestImpl_ProjectSync_DeleteRemote_DeniedByPolicy_NothingDeleted(t *testing.T) {
	pb := &stubProjectBackend{}
	im := &contextsync.Impl{Project: pb, Gate: newForbidEngine(t, "context_sync.project.purge")}

	err := im.ProjectSync_DeleteRemote(context.Background(), "proj-1")
	if err == nil {
		t.Fatal("expected policy denial, got nil error")
	}
	var pde *cedar.PolicyDeniedError
	if !errors.As(err, &pde) {
		t.Fatalf("expected error to wrap *cedar.PolicyDeniedError, got %T: %v", err, err)
	}
	if got := pb.deleteRemoteCallCount(); got != 0 {
		t.Fatalf("backend DeleteRemote was called %d times; want 0 (nothing may be deleted on Deny)", got)
	}
}

// TestImpl_ProjectSync_DeleteRemote_Permitted_DeletesOnce is the paired
// positive for the project surface.
func TestImpl_ProjectSync_DeleteRemote_Permitted_DeletesOnce(t *testing.T) {
	pb := &stubProjectBackend{}
	im := &contextsync.Impl{Project: pb, Gate: newPermitEngine(t)}

	if err := im.ProjectSync_DeleteRemote(context.Background(), "proj-1"); err != nil {
		t.Fatalf("expected the shipped default policy to permit, got denied: %v", err)
	}
	if got := pb.deleteRemoteCallCount(); got != 1 {
		t.Fatalf("backend DeleteRemote was called %d times; want exactly 1", got)
	}
}

// TestImpl_SessionSync_DeleteRemote_NoShippedPolicy_Denies is the "no
// shipped policy" case named explicitly in the mission brief — the case
// that bit the scheduled-run gate's own development. With a real engine
// that has NO rule at all for the action (IncludeEmbedded: false), the
// purge must still refuse and nothing may be deleted.
func TestImpl_SessionSync_DeleteRemote_NoShippedPolicy_Denies(t *testing.T) {
	e, err := cedar.NewEngine(cedar.Options{IncludeEmbedded: false, LoadFromDisk: false})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	sb := &stubSessionBackend{}
	im := &contextsync.Impl{Session: sb, Gate: e}

	if err := im.SessionSync_DeleteRemote(context.Background(), "sess-1"); err == nil {
		t.Fatal("expected deny with no shipped policy installed, got nil error")
	}
	if got := sb.deleteRemoteCallCount(); got != 0 {
		t.Fatalf("backend DeleteRemote was called %d times; want 0", got)
	}
}

// TestImpl_SessionSync_Toggle_RemainsUngated_ByPolicy pins ruling G-7's
// explicit carve-out: SessionSync_Toggle must NOT be gated by a forbid
// rule against the purge action — toggle/resume are deliberately left
// ungated for now, a recorded gap rather than one silently closed by
// extending this WP's gate.
func TestImpl_SessionSync_Toggle_RemainsUngated_ByPolicy(t *testing.T) {
	sb := &stubSessionBackend{}
	im := &contextsync.Impl{Session: sb, Gate: newForbidEngine(t, "context_sync.session.purge")}

	if _, err := im.SessionSync_Toggle(context.Background(), "sess-1", true); err != nil {
		t.Fatalf("SessionSync_Toggle must be unaffected by the purge forbid rule, got: %v", err)
	}
}
