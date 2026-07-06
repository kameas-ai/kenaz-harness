package menu

import (
	"context"

	wailsmenu "github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Broker is the minimal event-publish surface the menu handlers need to fire
// frontend events. Fulfilled by the rpc.StreamBroker in production; in tests
// a recording fake satisfies the interface.
type Broker interface {
	// Publish emits a payload on the given topic to all subscribers (frontend
	// and served-mode WebSocket). topic is a dot-separated broker topic string.
	Publish(topic string, payload any)
}

// UpdateChecker is the minimal surface the OnCheckUpdates handler needs.
type UpdateChecker interface {
	// CheckNow triggers an immediate update check.
	CheckNow(ctx context.Context)
}

// ContextProvider supplies the Wails app context to handler methods that need
// to call runtime.BrowserOpenURL or other context-bound Wails APIs.
type ContextProvider interface {
	// AppCtx returns the Wails OnStartup context. May return
	// context.Background() before SetContext is called (pre-startup).
	AppCtx() context.Context
}

// ContextProviderFunc adapts a func to ContextProvider.
type ContextProviderFunc func() context.Context

func (f ContextProviderFunc) AppCtx() context.Context { return f() }

// Menu broker topic constants — published by Go handler, consumed by Vue App.vue.
const (
	// TopicMenuSearchOpen asks the frontend to open the search palette.
	TopicMenuSearchOpen = "menu:search:open"
	// TopicMenuCmdPaletteOpen asks the frontend to open the command palette.
	TopicMenuCmdPaletteOpen = "menu:cmd-palette:open"
	// TopicMenuThemeSet asks the frontend to switch the colour theme.
	// Payload: ThemeSetPayload.
	TopicMenuThemeSet = "menu:theme:set"
	// TopicMenuRoute asks the frontend to push a Vue Router route.
	// Payload: MenuRoutePayload.
	TopicMenuRoute = "menu:route"
	// TopicMenuAboutOpen asks the frontend to open the About dialog.
	TopicMenuAboutOpen = "menu:about:open"
	// TopicMenuCheatSheetToggle asks the frontend to toggle the cheat sheet.
	TopicMenuCheatSheetToggle = "menu:cheat-sheet:toggle"
)

// ThemeSetPayload is the broker payload for TopicMenuThemeSet.
type ThemeSetPayload struct {
	Mode string `json:"mode"` // "light" | "dark" | "system"
}

// MenuRoutePayload is the broker payload for TopicMenuRoute.
type MenuRoutePayload struct {
	Path string `json:"path"` // Vue Router path, e.g. "/settings"
}

// Handlers wraps all menu-item action implementations.
// Each method has the Wails *menu.Callback-compatible signature.
//
// Thread safety: the Wails menu-callback goroutine is different from the main
// goroutine. All fields set during construction; no mutation after that.
type Handlers struct {
	broker  Broker
	updater UpdateChecker
	ctxProv ContextProvider
}

// NewHandlers constructs a Handlers value. All parameters are optional — nil
// values produce no-op behaviour so the menu can be built before all
// subsystems are wired (e.g. during tests).
func NewHandlers(broker Broker, updater UpdateChecker, ctxProv ContextProvider) *Handlers {
	return &Handlers{
		broker:  broker,
		updater: updater,
		ctxProv: ctxProv,
	}
}

// SetCtxProv wires the ContextProvider after construction. Called from
// main.go OnStartup once the Wails app context is available.
// Safe for concurrent use (the ctxProv field is only set here and read
// in handler methods; Wails delivers menu callbacks after OnStartup).
func (h *Handlers) SetCtxProv(p ContextProvider) {
	h.ctxProv = p
}

// appCtx returns the Wails app context if available, otherwise background.
func (h *Handlers) appCtx() context.Context {
	if h.ctxProv != nil {
		return h.ctxProv.AppCtx()
	}
	return context.Background()
}

// publish emits a topic+payload via the broker if wired; otherwise no-op.
func (h *Handlers) publish(topic string, payload any) {
	if h.broker != nil {
		h.broker.Publish(topic, payload)
	}
}

// onFind opens the search palette.
func (h *Handlers) onFind(_ *wailsmenu.CallbackData) {
	h.publish(TopicMenuSearchOpen, nil)
}

// onCommandPalette opens the command palette.
func (h *Handlers) onCommandPalette(_ *wailsmenu.CallbackData) {
	h.publish(TopicMenuCmdPaletteOpen, nil)
}

// onThemeLight switches the theme to light mode.
func (h *Handlers) onThemeLight(_ *wailsmenu.CallbackData) {
	h.publish(TopicMenuThemeSet, ThemeSetPayload{Mode: "light"})
}

// onThemeDark switches the theme to dark mode.
func (h *Handlers) onThemeDark(_ *wailsmenu.CallbackData) {
	h.publish(TopicMenuThemeSet, ThemeSetPayload{Mode: "dark"})
}

// onThemeSystem switches the theme to system mode.
func (h *Handlers) onThemeSystem(_ *wailsmenu.CallbackData) {
	h.publish(TopicMenuThemeSet, ThemeSetPayload{Mode: "system"})
}

// onCheckUpdates triggers an immediate update check.
func (h *Handlers) onCheckUpdates(_ *wailsmenu.CallbackData) {
	if h.updater != nil {
		h.updater.CheckNow(h.appCtx())
	}
}

// onNewSession routes the frontend to the new-session screen.
func (h *Handlers) onNewSession(_ *wailsmenu.CallbackData) {
	h.publish(TopicMenuRoute, MenuRoutePayload{Path: "/sessions/new"})
}

// onOpenRecentSessionFunc returns a Wails callback that navigates to a specific session.
func (h *Handlers) onOpenRecentSessionFunc(sessionID string) func(_ *wailsmenu.CallbackData) {
	return func(_ *wailsmenu.CallbackData) {
		h.publish(TopicMenuRoute, MenuRoutePayload{Path: "/sessions/" + sessionID})
	}
}

// onAboutDialog asks the frontend to open the About dialog.
func (h *Handlers) onAboutDialog(_ *wailsmenu.CallbackData) {
	h.publish(TopicMenuAboutOpen, nil)
}

// onDocumentation opens the documentation URL in the system browser.
func (h *Handlers) onDocumentation(_ *wailsmenu.CallbackData) {
	runtime.BrowserOpenURL(h.appCtx(), "https://docs.kameas.ai")
}

// onReportIssue opens the issue tracker in the system browser.
func (h *Handlers) onReportIssue(_ *wailsmenu.CallbackData) {
	runtime.BrowserOpenURL(h.appCtx(), "https://github.com/kameas-ai/kenaz-harness/issues/new")
}

// onCheatSheet toggles the cheat sheet overlay.
func (h *Handlers) onCheatSheet(_ *wailsmenu.CallbackData) {
	h.publish(TopicMenuCheatSheetToggle, nil)
}

// onPreferences routes the frontend to the settings panel.
func (h *Handlers) onPreferences(_ *wailsmenu.CallbackData) {
	h.publish(TopicMenuRoute, MenuRoutePayload{Path: "/settings"})
}

// onCloseWindow hides the main window (macOS: minimise to dock; Win/Linux: minimize).
func (h *Handlers) onCloseWindow(_ *wailsmenu.CallbackData) {
	runtime.Hide(h.appCtx())
}

// onQuit quits the application (Windows/Linux — macOS uses the App menu role).
func (h *Handlers) onQuit(_ *wailsmenu.CallbackData) {
	runtime.Quit(h.appCtx())
}
