package cedar

import (
	"context"
	"testing"
)

// fakePermissionHookRunner records calls and returns preset results.
type fakePermissionHookRunner struct {
	permRequestCalls []fakePermCall
	permDeniedCalls  []fakePermCall
	// preset return values for FirePermissionRequest
	presetAllow  bool
	presetDeny   bool
	presetReason string
}

type fakePermCall struct {
	SessionID  string
	ActionKind string
	Resource   string
}

func (f *fakePermissionHookRunner) FirePermissionRequest(
	_ context.Context, sessionID, actionKind, resource, _ string,
) (allow, deny bool, reason string) {
	f.permRequestCalls = append(f.permRequestCalls, fakePermCall{
		SessionID: sessionID, ActionKind: actionKind, Resource: resource,
	})
	return f.presetAllow, f.presetDeny, f.presetReason
}

func (f *fakePermissionHookRunner) FirePermissionDenied(
	_ context.Context, sessionID, actionKind, resource, _ string,
) {
	f.permDeniedCalls = append(f.permDeniedCalls, fakePermCall{
		SessionID: sessionID, ActionKind: actionKind, Resource: resource,
	})
}

func makeTestRegistryWithHook(t *testing.T, hook PermissionHookRunner) *Registry {
	t.Helper()
	opts := []RegistryOption{}
	if hook != nil {
		opts = append(opts, WithPermissionHookRunner(hook))
	}
	opts = append(opts, withTestDispatcher(nil))
	return NewRegistry(opts...)
}

// withTestDispatcher is a RegistryOption that sets a no-op dispatcher.
func withTestDispatcher(d PromptDispatcher) RegistryOption {
	return func(r *Registry) {
		r.dispatcher = d
	}
}

func TestPermissionHook_AllowShortCircuitsModal(t *testing.T) {
	t.Parallel()
	hook := &fakePermissionHookRunner{presetAllow: true, presetReason: "trusted-pattern"}
	reg := makeTestRegistryWithHook(t, hook)

	surface := PromptSurface{
		Tool:      &ToolPromptSurface{ToolName: "bash", ServerName: ""},
		SessionID: "sess-1",
	}
	res, err := reg.RequestInteractive(context.Background(), surface)
	if err != nil {
		t.Fatalf("RequestInteractive: %v", err)
	}
	if res.Decision != DecisionAllowOnce {
		t.Errorf("Decision=%q, want %q", res.Decision, DecisionAllowOnce)
	}
	if len(hook.permRequestCalls) != 1 {
		t.Fatalf("permRequestCalls len=%d, want 1", len(hook.permRequestCalls))
	}
	if hook.permRequestCalls[0].SessionID != "sess-1" {
		t.Errorf("SessionID=%q, want %q", hook.permRequestCalls[0].SessionID, "sess-1")
	}
}

func TestPermissionHook_DenyShortCircuitsModal(t *testing.T) {
	t.Parallel()
	hook := &fakePermissionHookRunner{presetDeny: true, presetReason: "blocked-by-policy"}
	reg := makeTestRegistryWithHook(t, hook)

	surface := PromptSurface{
		Bash:      &BashPromptSurface{Pattern: "rm -rf", Argv: []string{"rm", "-rf", "/"}},
		SessionID: "sess-2",
	}
	res, err := reg.RequestInteractive(context.Background(), surface)
	if err != nil {
		t.Fatalf("RequestInteractive: %v", err)
	}
	if res.Decision != DecisionDeny {
		t.Errorf("Decision=%q, want %q", res.Decision, DecisionDeny)
	}
}

func TestPermissionHook_NoOpContinuesNormalFlow(t *testing.T) {
	t.Parallel()
	// Hook returns no opinion — flow continues to auto-allow posture.
	// Note: PostureAutoAllow returns before the hook fires (hooks run in the
	// interactive flow only — when a modal would normally be raised).
	hook := &fakePermissionHookRunner{presetAllow: false, presetDeny: false}
	reg := makeTestRegistryWithHook(t, hook)
	reg.SetPosture(PostureAutoAllow)

	surface := PromptSurface{
		Tool:      &ToolPromptSurface{ToolName: "read_file"},
		SessionID: "sess-3",
	}
	res, err := reg.RequestInteractive(context.Background(), surface)
	if err != nil {
		t.Fatalf("RequestInteractive: %v", err)
	}
	// auto-allow posture fires before the hook — falls through immediately.
	if res.Decision != DecisionAllowOnce {
		t.Errorf("Decision=%q, want %q (from auto-allow posture)", res.Decision, DecisionAllowOnce)
	}
	// Hook is NOT called because PostureAutoAllow short-circuits before the
	// permission_request hook site (the hook fires in the interactive path).
	if len(hook.permRequestCalls) != 0 {
		t.Fatalf("permRequestCalls len=%d, want 0 (auto-allow bypasses hook)", len(hook.permRequestCalls))
	}
}

func TestPermissionHook_NilHookSkipped(t *testing.T) {
	t.Parallel()
	reg := makeTestRegistryWithHook(t, nil)
	reg.SetPosture(PostureAutoAllow)

	surface := PromptSurface{
		FS:        &FSPromptSurface{Op: "read", CanonicalPath: "/tmp/x"},
		SessionID: "sess-nil",
	}
	res, err := reg.RequestInteractive(context.Background(), surface)
	if err != nil {
		t.Fatalf("RequestInteractive: %v", err)
	}
	// No hook → falls through to auto-allow.
	if res.Decision != DecisionAllowOnce {
		t.Errorf("Decision=%q, want allow_once", res.Decision)
	}
}
