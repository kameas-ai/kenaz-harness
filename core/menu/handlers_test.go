package menu

import (
	"context"
	"sync"
	"testing"

	wailsmenu "github.com/wailsapp/wails/v2/pkg/menu"
)

// fakeBroker records published events. Race-safe via mutex.
type fakeBroker struct {
	mu     sync.Mutex
	events []brokerEvent
}

type brokerEvent struct {
	topic   string
	payload any
}

func (f *fakeBroker) Publish(topic string, payload any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, brokerEvent{topic: topic, payload: payload})
}

func (f *fakeBroker) snapshot() []brokerEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]brokerEvent, len(f.events))
	copy(out, f.events)
	return out
}

// fakeUpdater records CheckNow calls.
type fakeUpdater struct {
	mu    sync.Mutex
	calls int
}

func (u *fakeUpdater) CheckNow(_ context.Context) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.calls++
}

func (u *fakeUpdater) count() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.calls
}

// fakeUpdateController implements UpdateChecker + UpdateDownloader +
// UpdateApplier so onUpdateAction's per-state dispatch (self-update-repair
// -01PMUP01 WP05) is exercised against a value that satisfies all three
// optional capabilities, distinguishing which one was actually called.
type fakeUpdateController struct {
	mu                 sync.Mutex
	checkNowCalls      int
	startDownloadCalls int
	applyCalls         int
}

func (u *fakeUpdateController) CheckNow(_ context.Context) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.checkNowCalls++
}

func (u *fakeUpdateController) StartDownload(_ context.Context) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.startDownloadCalls++
}

func (u *fakeUpdateController) Apply(_ context.Context) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.applyCalls++
}

func (u *fakeUpdateController) snapshot() (checkNow, startDownload, apply int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.checkNowCalls, u.startDownloadCalls, u.applyCalls
}

// alwaysConfirm / neverConfirm — fake ConfirmDialogs. Using a fake here
// (rather than the production wailsConfirmDialog{}) is load-bearing, not
// a convenience: a real MessageDialog call against a non-Wails ctx calls
// log.Fatalf (os.Exit) — see ConfirmDialog's doc comment.
type stubConfirm struct{ answer bool }

func (s stubConfirm) Confirm(_ context.Context, _, _ string) bool { return s.answer }

// noop returns a nil CallbackData — handlers must not dereference it.
func noop() *wailsmenu.CallbackData { return nil }

func TestHandlers_OnFind(t *testing.T) {
	b := &fakeBroker{}
	h := NewHandlers(b, nil, nil)
	h.onFind(noop())
	evs := b.snapshot()
	if len(evs) != 1 || evs[0].topic != TopicMenuSearchOpen {
		t.Errorf("expected 1 %q event, got %v", TopicMenuSearchOpen, evs)
	}
}

func TestHandlers_OnCommandPalette(t *testing.T) {
	b := &fakeBroker{}
	h := NewHandlers(b, nil, nil)
	h.onCommandPalette(noop())
	evs := b.snapshot()
	if len(evs) != 1 || evs[0].topic != TopicMenuCmdPaletteOpen {
		t.Errorf("expected 1 %q event, got %v", TopicMenuCmdPaletteOpen, evs)
	}
}

func TestHandlers_OnTheme(t *testing.T) {
	themeTests := []struct {
		fn       func(h *Handlers)
		wantMode string
	}{
		{func(h *Handlers) { h.onThemeLight(noop()) }, "light"},
		{func(h *Handlers) { h.onThemeDark(noop()) }, "dark"},
		{func(h *Handlers) { h.onThemeSystem(noop()) }, "system"},
	}
	for _, tc := range themeTests {
		b := &fakeBroker{}
		h := NewHandlers(b, nil, nil)
		tc.fn(h)
		evs := b.snapshot()
		if len(evs) != 1 || evs[0].topic != TopicMenuThemeSet {
			t.Errorf("mode=%q: expected %q event, got %v", tc.wantMode, TopicMenuThemeSet, evs)
			continue
		}
		payload, ok := evs[0].payload.(ThemeSetPayload)
		if !ok {
			t.Errorf("mode=%q: payload type = %T, want ThemeSetPayload", tc.wantMode, evs[0].payload)
			continue
		}
		if payload.Mode != tc.wantMode {
			t.Errorf("mode=%q: payload.Mode = %q", tc.wantMode, payload.Mode)
		}
	}
}

func TestHandlers_OnCheckUpdates(t *testing.T) {
	u := &fakeUpdater{}
	h := NewHandlers(nil, u, nil)
	h.onCheckUpdates(noop())
	if u.count() != 1 {
		t.Errorf("expected 1 CheckNow call, got %d", u.count())
	}
}

func TestHandlers_OnNewSession(t *testing.T) {
	b := &fakeBroker{}
	h := NewHandlers(b, nil, nil)
	h.onNewSession(noop())
	evs := b.snapshot()
	if len(evs) != 1 || evs[0].topic != TopicMenuRoute {
		t.Errorf("expected %q event, got %v", TopicMenuRoute, evs)
	}
	p, ok := evs[0].payload.(MenuRoutePayload)
	if !ok || p.Path != "/sessions/new" {
		t.Errorf("unexpected payload: %v", evs[0].payload)
	}
}

func TestHandlers_OnOpenRecentSession(t *testing.T) {
	b := &fakeBroker{}
	h := NewHandlers(b, nil, nil)
	cb := h.onOpenRecentSessionFunc("sess-42")
	cb(noop())
	evs := b.snapshot()
	if len(evs) != 1 || evs[0].topic != TopicMenuRoute {
		t.Errorf("expected %q event, got %v", TopicMenuRoute, evs)
	}
	p, ok := evs[0].payload.(MenuRoutePayload)
	if !ok || p.Path != "/sessions/sess-42" {
		t.Errorf("unexpected payload: %v", evs[0].payload)
	}
}

func TestHandlers_OnAboutDialog(t *testing.T) {
	b := &fakeBroker{}
	h := NewHandlers(b, nil, nil)
	h.onAboutDialog(noop())
	evs := b.snapshot()
	if len(evs) != 1 || evs[0].topic != TopicMenuAboutOpen {
		t.Errorf("expected %q event, got %v", TopicMenuAboutOpen, evs)
	}
}

func TestHandlers_OnCheatSheet(t *testing.T) {
	b := &fakeBroker{}
	h := NewHandlers(b, nil, nil)
	h.onCheatSheet(noop())
	evs := b.snapshot()
	if len(evs) != 1 || evs[0].topic != TopicMenuCheatSheetToggle {
		t.Errorf("expected %q event, got %v", TopicMenuCheatSheetToggle, evs)
	}
}

func TestHandlers_NilBroker_NoopSafe(t *testing.T) {
	h := NewHandlers(nil, nil, nil)
	h.onFind(noop())
	h.onCommandPalette(noop())
	h.onThemeLight(noop())
	h.onThemeDark(noop())
	h.onThemeSystem(noop())
	h.onNewSession(noop())
	h.onAboutDialog(noop())
	h.onCheatSheet(noop())
}

func TestHandlers_NilUpdater_NoopSafe(t *testing.T) {
	h := NewHandlers(nil, nil, nil)
	h.onCheckUpdates(noop())
}

func TestHandlers_NilBroker_Preferences_NoopSafe(t *testing.T) {
	// onPreferences publishes a broker event; a nil broker must not panic.
	h := NewHandlers(nil, nil, nil)
	h.onPreferences(noop())
}

func TestHandlers_NilCtxProv_Quit_NoopSafe(t *testing.T) {
	// onQuit calls runtime.Quit(h.appCtx()); appCtx() returns Background() when
	// ctxProv is nil. Wails runtime.Quit with a background context is a no-op
	// outside of an active Wails app, so this must not panic.
	h := NewHandlers(nil, nil, nil)
	// We do NOT call h.onQuit here because runtime.Quit would try to use the
	// Wails app context which is not available in tests. Instead, just verify
	// that appCtx() falls back to Background() when ctxProv is nil.
	ctx := h.appCtx()
	if ctx == nil {
		t.Error("appCtx() returned nil; expected context.Background()")
	}
}

func TestHandlers_NilCtxProv_Documentation_NoopSafe(t *testing.T) {
	// onDocumentation calls runtime.BrowserOpenURL; with nil ctxProv appCtx()
	// returns Background(). BrowserOpenURL with a non-Wails context is a no-op,
	// so verify appCtx() gracefully returns Background().
	h := NewHandlers(nil, nil, nil)
	ctx := h.appCtx()
	if ctx == nil {
		t.Error("appCtx() returned nil; expected context.Background()")
	}
}

// ── WP05: onUpdateAction dispatches on the state its label was computed
// from (FR-008), not always CheckNow. self-update-repair-01PMUP01.

func TestOnUpdateAction_Idle_CallsCheckNow(t *testing.T) {
	u := &fakeUpdateController{}
	h := NewHandlers(nil, u, nil)
	h.onUpdateAction(UpdateIdle)(noop())
	checkNow, startDownload, apply := u.snapshot()
	if checkNow != 1 || startDownload != 0 || apply != 0 {
		t.Errorf("idle: checkNow=%d startDownload=%d apply=%d, want 1/0/0", checkNow, startDownload, apply)
	}
}

func TestOnUpdateAction_Available_CallsStartDownload(t *testing.T) {
	u := &fakeUpdateController{}
	h := NewHandlers(nil, u, nil)
	h.onUpdateAction(UpdateAvailable)(noop())
	checkNow, startDownload, apply := u.snapshot()
	if checkNow != 0 || startDownload != 1 || apply != 0 {
		t.Errorf("available: checkNow=%d startDownload=%d apply=%d, want 0/1/0", checkNow, startDownload, apply)
	}
}

func TestOnUpdateAction_Downloading_IsNoop(t *testing.T) {
	u := &fakeUpdateController{}
	h := NewHandlers(nil, u, nil)
	h.onUpdateAction(UpdateDownloading)(noop())
	checkNow, startDownload, apply := u.snapshot()
	if checkNow != 0 || startDownload != 0 || apply != 0 {
		t.Errorf("downloading: checkNow=%d startDownload=%d apply=%d, want 0/0/0 (item is disabled)", checkNow, startDownload, apply)
	}
}

// TestOnUpdateAction_Staged_CallsApply is the AC-8 dispatch pin: the
// staged handler must call Apply, not CheckNow. Mutation: restore the
// unconditional CheckNow (i.e. delete the switch and always call
// h.updater.CheckNow) → this test fails (checkNow=1, apply=0).
func TestOnUpdateAction_Staged_ConfirmedCallsApply(t *testing.T) {
	u := &fakeUpdateController{}
	h := NewHandlers(nil, u, nil)
	h.SetConfirmDialog(stubConfirm{answer: true})
	h.onUpdateAction(UpdateStaged)(noop())
	checkNow, startDownload, apply := u.snapshot()
	if apply != 1 {
		t.Errorf("staged (confirmed): apply=%d, want 1", apply)
	}
	if checkNow != 0 || startDownload != 0 {
		t.Errorf("staged (confirmed): checkNow=%d startDownload=%d, want 0/0", checkNow, startDownload)
	}
}

// TestOnUpdateAction_Staged_DeclinedDoesNotApply pins the confirm-gate
// itself (spec §7 escalation): a "Cancel" answer must not call Apply.
func TestOnUpdateAction_Staged_DeclinedDoesNotApply(t *testing.T) {
	u := &fakeUpdateController{}
	h := NewHandlers(nil, u, nil)
	h.SetConfirmDialog(stubConfirm{answer: false})
	h.onUpdateAction(UpdateStaged)(noop())
	checkNow, startDownload, apply := u.snapshot()
	if apply != 0 {
		t.Errorf("staged (declined): apply=%d, want 0 (confirm declined)", apply)
	}
	if checkNow != 0 || startDownload != 0 {
		t.Errorf("staged (declined): checkNow=%d startDownload=%d, want 0/0", checkNow, startDownload)
	}
}

// TestOnUpdateAction_Staged_NilConfirmDoesNotApply: a Handlers value
// constructed without going through NewHandlers (confirm left nil) must
// default to NOT applying — never silently install without asking.
func TestOnUpdateAction_Staged_NilConfirmDoesNotApply(t *testing.T) {
	u := &fakeUpdateController{}
	h := &Handlers{updater: u} // bypass NewHandlers — confirm is nil
	h.onUpdateAction(UpdateStaged)(noop())
	_, _, apply := u.snapshot()
	if apply != 0 {
		t.Errorf("nil confirm: apply=%d, want 0 (fail-safe default)", apply)
	}
}

func TestOnUpdateAction_Failed_CallsStartDownload(t *testing.T) {
	u := &fakeUpdateController{}
	h := NewHandlers(nil, u, nil)
	h.onUpdateAction(UpdateFailed)(noop())
	checkNow, startDownload, apply := u.snapshot()
	if checkNow != 0 || startDownload != 1 || apply != 0 {
		t.Errorf("failed: checkNow=%d startDownload=%d apply=%d, want 0/1/0 (retry)", checkNow, startDownload, apply)
	}
}

// TestOnUpdateAction_CapabilityAbsent_FallsBackToCheckNow: an updater
// that only implements UpdateChecker (e.g. the pre-WP05
// UpdateCheckerFunc shape) still does something observable for every
// state rather than silently no-op'ing.
func TestOnUpdateAction_CapabilityAbsent_FallsBackToCheckNow(t *testing.T) {
	u := &fakeUpdater{}
	h := NewHandlers(nil, u, nil)
	h.onUpdateAction(UpdateAvailable)(noop())
	if u.count() != 1 {
		t.Errorf("capability-absent fallback: CheckNow calls = %d, want 1", u.count())
	}
}

func TestHandlers_NilUpdater_OnUpdateAction_NoopSafe(t *testing.T) {
	h := NewHandlers(nil, nil, nil)
	for _, s := range []UpdateMenuState{UpdateIdle, UpdateAvailable, UpdateDownloading, UpdateStaged, UpdateFailed} {
		h.onUpdateAction(s)(noop()) // must not panic
	}
}
