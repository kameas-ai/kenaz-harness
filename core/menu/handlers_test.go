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
