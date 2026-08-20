package menu

import (
	"runtime"

	wailsmenu "github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
)

// Build constructs the application menu from a MenuState snapshot and a set of
// Handlers. It is a pure function: no side-effects, no package-level state.
// Callers may rebuild and re-apply at any time.
//
// Platform branching:
//   - macOS: standard Apple-HIG App menu (About, Preferences, Hide, Quit) added
//     first, then File / Edit / View / Help. Wails AppMenu() produces the
//     complete native NSApplicationMenu structure.
//   - Windows / Linux: no App menu; Quit lives under File.
//
// The menu bar carries no fleet-bound items (spec FR-005). Sign-in / sign-out /
// Account Settings remain in the in-window UserMenu dropdown.
func Build(state MenuState, h *Handlers) *wailsmenu.Menu {
	app := wailsmenu.NewMenu()

	if runtime.GOOS == "darwin" {
		// macOS: custom App menu replacing AppMenuRole so we can inject
		// Preferences… (⌘,) between About and the standard window items.
		// Wails v2 does not expose individual Role constants (HideRole,
		// QuitRole, etc.) so we wire Quit via onQuit and omit Hide/HideOthers
		// rather than using a no-op Role=0 on menu items.
		appSub := wailsmenu.NewMenu()
		appSub.AddText("About Kenaz Harness", nil, h.onAboutDialog)
		appSub.AddSeparator()
		appSub.AddText("Preferences…", keys.CmdOrCtrl(","), h.onPreferences)
		appSub.AddSeparator()
		appSub.AddText("Quit Kenaz Harness", keys.CmdOrCtrl("q"), h.onQuit)
		app.Append(wailsmenu.SubMenu("Kenaz Harness", appSub))
	}

	app.Append(buildFileMenu(state, h))
	app.Append(buildEditMenu(h))
	app.Append(buildViewMenu(state, h))
	app.Append(buildHelpMenu(state, h))

	return app
}

// buildFileMenu builds the File top-level menu.
// On Win/Linux the Quit item is appended; on macOS Quit lives in the App menu.
// On Win/Linux Preferences… is placed at the top before New Session (macOS
// Preferences lives in the App menu via onPreferences wired to ⌘,).
func buildFileMenu(state MenuState, h *Handlers) *wailsmenu.MenuItem {
	sub := wailsmenu.NewMenu()

	if runtime.GOOS != "darwin" {
		sub.AddText("Preferences…", keys.CmdOrCtrl(","), h.onPreferences)
		sub.AddSeparator()
	}

	sub.AddText("New Session", keys.CmdOrCtrl("n"), h.onNewSession)

	// Open Recent submenu — populated from MenuState.RecentSessions.
	recentSub := wailsmenu.NewMenu()
	if len(state.RecentSessions) == 0 {
		recentSub.AddText("No recent sessions", nil, nil).Disable()
	} else {
		for _, ref := range state.RecentSessions {
			// Capture loop variable for the closure.
			sessionID := ref.ID
			title := ref.Title
			if title == "" {
				title = sessionID
			}
			recentSub.AddText(title, nil, h.onOpenRecentSessionFunc(sessionID))
		}
	}
	sub.Append(wailsmenu.SubMenu("Open Recent", recentSub))

	sub.AddSeparator()
	sub.AddText("Close Window", keys.CmdOrCtrl("w"), h.onCloseWindow)

	if runtime.GOOS != "darwin" {
		sub.AddSeparator()
		sub.AddText("Quit", keys.CmdOrCtrl("q"), h.onQuit)
	}

	return wailsmenu.SubMenu("File", sub)
}

// buildEditMenu builds the Edit top-level menu.
// EditMenuRole delegates to the OS-native edit menu on macOS.
// On Windows/Linux we provide explicit Undo/Redo/Cut/Copy/Paste/SelectAll.
//
// controls-and-readouts-that-tell-the-truth-01PMZ808 WP09 (FR-012,
// AC-023): this used to also register "Find" at Ctrl+F, calling the same
// h.onFind as buildViewMenu's "Search" item below — two menu
// registrations for the identical accelerator and handler on
// Windows/Linux. View → "Search" is the one Shell.vue's own comment
// documents as the frontend's entry point
// (menu:search:open -> App.vue -> searchPalette.open()), so it is the
// one kept; the Edit menu's duplicate is removed rather than the
// documented one.
func buildEditMenu(h *Handlers) *wailsmenu.MenuItem {
	if runtime.GOOS == "darwin" {
		return wailsmenu.EditMenu()
	}
	// Windows / Linux: explicit items because there is no EditMenuRole equivalent.
	sub := wailsmenu.NewMenu()
	sub.AddText("Undo", keys.CmdOrCtrl("z"), nil)
	sub.AddText("Redo", keys.Combo("z", keys.CmdOrCtrlKey, keys.ShiftKey), nil)
	sub.AddSeparator()
	sub.AddText("Cut", keys.CmdOrCtrl("x"), nil)
	sub.AddText("Copy", keys.CmdOrCtrl("c"), nil)
	sub.AddText("Paste", keys.CmdOrCtrl("v"), nil)
	sub.AddText("Select All", keys.CmdOrCtrl("a"), nil)
	// "Find" (Ctrl+F -> h.onFind) removed here — see the WP09 doc comment
	// above buildEditMenu. View -> "Search" (buildViewMenu) is the one
	// live registration for this accelerator on Windows/Linux.
	return wailsmenu.SubMenu("Edit", sub)
}

// buildViewMenu builds the View top-level menu.
func buildViewMenu(state MenuState, h *Handlers) *wailsmenu.MenuItem {
	sub := wailsmenu.NewMenu()

	sub.AddText("Command Palette", keys.CmdOrCtrl("k"), h.onCommandPalette)
	sub.AddText("Search", keys.CmdOrCtrl("f"), h.onFind)
	sub.AddSeparator()

	// Theme submenu — radio buttons reflect current ThemeMode.
	themeSub := wailsmenu.NewMenu()
	themeSub.AddRadio("Light", state.ThemeMode == ThemeLight, nil, h.onThemeLight)
	themeSub.AddRadio("Dark", state.ThemeMode == ThemeDark, nil, h.onThemeDark)
	themeSub.AddRadio("System", state.ThemeMode == ThemeSystem || state.ThemeMode == "", nil, h.onThemeSystem)
	sub.Append(wailsmenu.SubMenu("Theme", themeSub))

	sub.AddSeparator()
	sub.AddText("Toggle Cheat Sheet", nil, h.onCheatSheet)

	return wailsmenu.SubMenu("View", sub)
}

// buildHelpMenu builds the Help top-level menu.
func buildHelpMenu(state MenuState, h *Handlers) *wailsmenu.MenuItem {
	sub := wailsmenu.NewMenu()

	sub.AddText("Keyboard Shortcuts", nil, h.onCheatSheet)
	sub.AddText("Documentation…", nil, h.onDocumentation)
	sub.AddText("Report Issue…", nil, h.onReportIssue)
	sub.AddSeparator()

	updateLabel := UpdateMenuLabel(state.UpdateState)
	// self-update-repair-01PMUP01 FR-008: dispatch on the state the label
	// was computed from, not always CheckNow — see onUpdateAction.
	updateItem := sub.AddText(updateLabel, nil, h.onUpdateAction(state.UpdateState))
	if state.UpdateState == UpdateDownloading {
		updateItem.Disable()
	}

	sub.AddSeparator()

	// On macOS, About is already in the App menu; hide it here to avoid
	// duplication (Apple HIG). On Windows/Linux, show it under Help.
	aboutItem := sub.AddText("About Kenaz Harness", nil, h.onAboutDialog)
	if runtime.GOOS == "darwin" {
		aboutItem.Hide()
	}

	return wailsmenu.SubMenu("Help", sub)
}
