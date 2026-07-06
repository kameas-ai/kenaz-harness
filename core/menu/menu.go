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
		// macOS: standard App menu first (AppMenuRole delegates to NSApp).
		app.Append(wailsmenu.AppMenu())
	}

	app.Append(buildFileMenu(state, h))
	app.Append(buildEditMenu())
	app.Append(buildViewMenu(state, h))
	app.Append(buildHelpMenu(state, h))

	return app
}

// buildFileMenu builds the File top-level menu.
// On Win/Linux the Quit item is appended; on macOS Quit lives in the App menu.
func buildFileMenu(state MenuState, h *Handlers) *wailsmenu.MenuItem {
	sub := wailsmenu.NewMenu()

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
func buildEditMenu() *wailsmenu.MenuItem {
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
	updateItem := sub.AddText(updateLabel, nil, h.onCheckUpdates)
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
