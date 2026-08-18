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

// UpdateDownloader is the OPTIONAL capability the Help → "Install Update" /
// "Retry Update" states need (self-update-repair-01PMUP01 WP05). Deliberately
// separate from UpdateChecker rather than folded in, so an UpdateChecker
// implementation that only supports CheckNow (e.g. a bare UpdateCheckerFunc
// closure, or handlers_test.go's fakeUpdater) remains a valid value —
// onUpdateAction type-asserts for it and falls back to CheckNow when absent.
type UpdateDownloader interface {
	// StartDownload begins (or retries) the staged-artifact download.
	StartDownload(ctx context.Context)
}

// UpdateApplier is the OPTIONAL capability the Help → "Install & Restart"
// state needs. See UpdateDownloader for why this is a separate,
// type-asserted interface rather than a UpdateChecker method.
type UpdateApplier interface {
	// Apply installs the most recently staged download and restarts.
	Apply(ctx context.Context)
}

// ConfirmDialog is the yes/no confirmation surface the staged-restart
// action uses before calling Apply (spec §7 escalation default: a menu
// misclick on "Install & Restart" must not discard in-flight work without
// a confirmation step; the Settings-panel Install button keeps its
// existing one-click behaviour — this only gates the menu path).
//
// This MUST be an injectable interface rather than a direct
// wailsruntime.MessageDialog call inline in the handler: MessageDialog's
// getFrontend(ctx) calls log.Fatalf (→ os.Exit) when ctx carries no live
// Wails frontend, which would kill the test binary the instant a test
// exercised the staged-dispatch path with any non-production
// ContextProvider. Production gets wailsConfirmDialog{} (NewHandlers'
// default); tests inject a fake via SetConfirmDialog.
type ConfirmDialog interface {
	// Confirm shows a blocking yes/no prompt and reports whether the
	// user chose to proceed.
	Confirm(ctx context.Context, title, message string) bool
}

// wailsConfirmDialog is the production ConfirmDialog backed by Wails'
// native MessageDialog.
type wailsConfirmDialog struct{}

func (wailsConfirmDialog) Confirm(ctx context.Context, title, message string) bool {
	choice, err := runtime.MessageDialog(ctx, runtime.MessageDialogOptions{
		Type:          runtime.QuestionDialog,
		Title:         title,
		Message:       message,
		Buttons:       []string{"Install & Restart", "Cancel"},
		DefaultButton: "Cancel",
		CancelButton:  "Cancel",
	})
	if err != nil {
		return false
	}
	return choice == "Install & Restart"
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
	confirm ConfirmDialog
}

// NewHandlers constructs a Handlers value. All parameters are optional — nil
// values produce no-op behaviour so the menu can be built before all
// subsystems are wired (e.g. during tests). confirm defaults to the
// production wailsruntime-backed ConfirmDialog; override with
// SetConfirmDialog in tests before exercising the staged-restart path.
func NewHandlers(broker Broker, updater UpdateChecker, ctxProv ContextProvider) *Handlers {
	return &Handlers{
		broker:  broker,
		updater: updater,
		ctxProv: ctxProv,
		confirm: wailsConfirmDialog{},
	}
}

// SetConfirmDialog overrides the confirm-dialog surface used by the staged
// Help → "Install & Restart" handler. Production code never needs to call
// this (NewHandlers' default is the real Wails dialog); tests inject a
// fake before exercising onUpdateAction(UpdateStaged) — see ConfirmDialog's
// doc comment for why calling the real one in a test would os.Exit.
func (h *Handlers) SetConfirmDialog(c ConfirmDialog) {
	h.confirm = c
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

// onCheckUpdates triggers an immediate update check. Bound to the Help
// item when its label was computed from UpdateIdle (self-update-repair
// -01PMUP01 WP05 — see onUpdateAction for the other four states).
func (h *Handlers) onCheckUpdates(_ *wailsmenu.CallbackData) {
	if h.updater != nil {
		h.updater.CheckNow(h.appCtx())
	}
}

// onUpdateAction returns the Wails callback for the Help → "Check for
// Updates…" item, DISPATCHING ON THE STATE ITS LABEL WAS COMPUTED FROM
// (self-update-repair-01PMUP01 FR-008) — before this, the handler always
// called CheckNow regardless of label, so a user reading "Install Update"
// and clicking it got a silent re-check instead (spec §1.4: "the
// retirement target is itself a liar").
//
//	UpdateIdle        → CheckNow (StartCheck)
//	UpdateAvailable   → StartDownload (start the install)
//	UpdateDownloading → no-op (menu.go already disables this item)
//	UpdateStaged      → confirm, then Apply (install + restart)
//	UpdateFailed      → StartDownload (retry)
//
// UpdateDownloader/UpdateApplier are OPTIONAL capabilities on h.updater
// (type-asserted); an updater that only implements UpdateChecker falls
// back to CheckNow for every state rather than silently no-op'ing, so a
// misconfigured wiring still does *something* observable.
func (h *Handlers) onUpdateAction(state UpdateMenuState) func(_ *wailsmenu.CallbackData) {
	return func(_ *wailsmenu.CallbackData) {
		if h.updater == nil {
			return
		}
		switch state {
		case UpdateAvailable, UpdateFailed:
			if d, ok := h.updater.(UpdateDownloader); ok {
				d.StartDownload(h.appCtx())
				return
			}
		case UpdateStaged:
			if a, ok := h.updater.(UpdateApplier); ok {
				ctx := h.appCtx()
				if h.confirm != nil && h.confirm.Confirm(ctx,
					"Install & Restart",
					"Kenaz will install the update and restart now. Any unsaved "+
						"in-flight work in an active session may be interrupted. Continue?",
				) {
					a.Apply(ctx)
				}
				return
			}
		case UpdateDownloading:
			// menu.go disables the item in this state; defensive no-op if
			// a click still lands (e.g. a race during rebuild).
			return
		}
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
