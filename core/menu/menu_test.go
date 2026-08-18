package menu

import (
	"runtime"
	"testing"

	wailsmenu "github.com/wailsapp/wails/v2/pkg/menu"
)

// noopHandlers returns a Handlers with all callbacks set to nil, suitable for
// structural assertions that never call handler functions.
func noopHandlers() *Handlers {
	return &Handlers{}
}

// recentSubmenu walks the menu tree and returns the "Open Recent" submenu,
// or nil if not found.
func recentSubmenu(m *wailsmenu.Menu) *wailsmenu.Menu {
	for _, top := range m.Items {
		if top.Label != "File" {
			continue
		}
		if top.SubMenu == nil {
			return nil
		}
		for _, fi := range top.SubMenu.Items {
			if fi.Label == "Open Recent" && fi.SubMenu != nil {
				return fi.SubMenu
			}
		}
	}
	return nil
}

func TestBuild_TopLevelMenusPresent(t *testing.T) {
	state := MenuState{ThemeMode: ThemeSystem}
	m := Build(state, noopHandlers())
	if m == nil {
		t.Fatal("Build returned nil")
	}

	labels := make([]string, 0, len(m.Items))
	for _, item := range m.Items {
		labels = append(labels, item.Label)
	}

	// File, View, Help must always be present.
	for _, want := range []string{"File", "View", "Help"} {
		found := false
		for _, got := range labels {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing top-level menu %q; got: %v", want, labels)
		}
	}
}

func TestBuild_MacOSAppMenuFirst(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-only test")
	}
	m := Build(MenuState{}, noopHandlers())
	if len(m.Items) == 0 {
		t.Fatal("menu empty")
	}
	// Custom App menu replaces AppMenuRole so we can wire Preferences…
	// The first item is a "Kenaz Harness" submenu containing Preferences.
	first := m.Items[0]
	if first.Label != "Kenaz Harness" {
		t.Errorf("first item label = %q, want %q", first.Label, "Kenaz Harness")
	}
	if first.SubMenu == nil {
		t.Fatal("Kenaz Harness menu has no submenu")
	}
	var foundPrefs bool
	for _, item := range first.SubMenu.Items {
		if item.Label == "Preferences…" {
			foundPrefs = true
		}
	}
	if !foundPrefs {
		t.Error("Preferences… item not found in Kenaz Harness app menu")
	}
}

func TestBuild_MacOSPreferences_HasAccelerator(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-only test")
	}
	m := Build(MenuState{}, noopHandlers())
	if len(m.Items) == 0 {
		t.Fatal("menu empty")
	}
	appMenu := m.Items[0]
	if appMenu.SubMenu == nil {
		t.Fatal("no app submenu")
	}
	for _, item := range appMenu.SubMenu.Items {
		if item.Label == "Preferences…" {
			if item.Accelerator == nil {
				t.Error("Preferences… missing accelerator (want ⌘,)")
			}
			return
		}
	}
	t.Error("Preferences… item not found")
}

func TestBuild_WinLinux_PreferencesInFile(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("Windows/Linux-only test")
	}
	m := Build(MenuState{}, noopHandlers())
	for _, top := range m.Items {
		if top.Label != "File" || top.SubMenu == nil {
			continue
		}
		for _, fi := range top.SubMenu.Items {
			if fi.Label == "Preferences…" {
				if fi.Accelerator == nil {
					t.Error("Preferences… missing accelerator (want Ctrl+,)")
				}
				return
			}
		}
		t.Error("Preferences… not found in File menu on Windows/Linux")
		return
	}
	t.Error("File menu not found")
}

func TestBuild_ThemeRadioCheckmarks(t *testing.T) {
	cases := []struct {
		mode    ThemeMode
		wantIdx int // 0=Light, 1=Dark, 2=System
	}{
		{ThemeLight, 0},
		{ThemeDark, 1},
		{ThemeSystem, 2},
		{"", 2},
	}

	for _, tc := range cases {
		state := MenuState{ThemeMode: tc.mode}
		m := Build(state, noopHandlers())

		var themeItems []*wailsmenu.MenuItem
		for _, top := range m.Items {
			if top.Label != "View" || top.SubMenu == nil {
				continue
			}
			for _, vi := range top.SubMenu.Items {
				if vi.Label != "Theme" || vi.SubMenu == nil {
					continue
				}
				themeItems = vi.SubMenu.Items
			}
		}

		if len(themeItems) != 3 {
			t.Errorf("mode=%q: want 3 theme radio items, got %d", tc.mode, len(themeItems))
			continue
		}
		for i, item := range themeItems {
			wantChecked := i == tc.wantIdx
			if item.Checked != wantChecked {
				t.Errorf("mode=%q: item[%d] (%q) Checked=%v, want %v",
					tc.mode, i, item.Label, item.Checked, wantChecked)
			}
		}
	}
}

func TestBuild_UpdateLabels(t *testing.T) {
	cases := []struct {
		state     UpdateMenuState
		wantLabel string
		wantDisab bool
	}{
		{UpdateIdle, "Check for Updates…", false},
		{UpdateAvailable, "Install Update", false},
		{UpdateDownloading, "Downloading…", true},
		{UpdateStaged, "Install & Restart", false},
		{UpdateFailed, "Retry Update", false},
	}

	for _, tc := range cases {
		m := Build(MenuState{UpdateState: tc.state}, noopHandlers())
		var found bool
		for _, top := range m.Items {
			if top.Label != "Help" || top.SubMenu == nil {
				continue
			}
			for _, hi := range top.SubMenu.Items {
				if hi.Label == tc.wantLabel {
					found = true
					if hi.Disabled != tc.wantDisab {
						t.Errorf("state=%v: label=%q Disabled=%v, want %v",
							tc.state, tc.wantLabel, hi.Disabled, tc.wantDisab)
					}
				}
			}
		}
		if !found {
			t.Errorf("state=%v: did not find Help item with label %q", tc.state, tc.wantLabel)
		}
	}
}

func TestBuild_RecentSessions_Empty(t *testing.T) {
	m := Build(MenuState{}, noopHandlers())
	recent := recentSubmenu(m)
	if recent == nil {
		t.Fatal("Open Recent submenu not found")
	}
	if len(recent.Items) != 1 {
		t.Fatalf("expected 1 item in empty Open Recent, got %d", len(recent.Items))
	}
	if recent.Items[0].Label != "No recent sessions" {
		t.Errorf("item label = %q, want %q", recent.Items[0].Label, "No recent sessions")
	}
	if !recent.Items[0].Disabled {
		t.Error("'No recent sessions' should be disabled")
	}
}

func TestBuild_RecentSessions_Populated(t *testing.T) {
	sessions := []SessionRef{
		{ID: "s1", Title: "First session"},
		{ID: "s2", Title: ""},
	}
	m := Build(MenuState{RecentSessions: sessions}, noopHandlers())
	recent := recentSubmenu(m)
	if recent == nil {
		t.Fatal("Open Recent submenu not found")
	}
	if len(recent.Items) != 2 {
		t.Fatalf("expected 2 recent items, got %d", len(recent.Items))
	}
	if recent.Items[0].Label != "First session" {
		t.Errorf("item[0].Label = %q, want %q", recent.Items[0].Label, "First session")
	}
	// Empty title falls back to session ID.
	if recent.Items[1].Label != "s2" {
		t.Errorf("item[1].Label = %q, want %q", recent.Items[1].Label, "s2")
	}
}

func TestBuild_QuitPlacement(t *testing.T) {
	m := Build(MenuState{}, noopHandlers())
	if runtime.GOOS == "darwin" {
		for _, top := range m.Items {
			if top.Label != "File" || top.SubMenu == nil {
				continue
			}
			for _, fi := range top.SubMenu.Items {
				if fi.Label == "Quit" {
					t.Error("Quit should NOT appear in File on macOS")
				}
			}
		}
	} else {
		found := false
		for _, top := range m.Items {
			if top.Label != "File" || top.SubMenu == nil {
				continue
			}
			for _, fi := range top.SubMenu.Items {
				if fi.Label == "Quit" {
					found = true
				}
			}
		}
		if !found {
			t.Error("Quit must appear in File on Windows/Linux")
		}
	}
}

func TestBuild_NoFleetItems(t *testing.T) {
	// The menu bar must contain no fleet-identity items.
	forbidden := []string{"Sign in", "Sign out", "Sign In", "Sign Out", "Account settings", "Account"}
	m := Build(MenuState{}, noopHandlers())

	var checkItems func(items []*wailsmenu.MenuItem)
	checkItems = func(items []*wailsmenu.MenuItem) {
		for _, item := range items {
			for _, f := range forbidden {
				if item.Label == f {
					t.Errorf("fleet item %q must not appear in the menu bar (FR-005)", item.Label)
				}
			}
			if item.SubMenu != nil {
				checkItems(item.SubMenu.Items)
			}
		}
	}
	checkItems(m.Items)
}

// helpUpdateItem walks a built menu and returns the Help submenu's
// update item (the one whose label UpdateMenuLabel produced), or nil.
func helpUpdateItem(m *wailsmenu.Menu, state MenuState) *wailsmenu.MenuItem {
	want := UpdateMenuLabel(state.UpdateState)
	for _, top := range m.Items {
		if top.Label != "Help" || top.SubMenu == nil {
			continue
		}
		for _, hi := range top.SubMenu.Items {
			if hi.Label == want {
				return hi
			}
		}
	}
	return nil
}

// TestBuild_HelpUpdateItem_ClickDispatchesOnItsOwnLabel is the FR-008
// pin AT THE BINDING SITE. handlers_test.go exercises onUpdateAction
// directly, which leaves menu.go's one-line `sub.AddText(updateLabel,
// nil, h.onUpdateAction(state.UpdateState))` completely untested — and
// tasks.md's named WP05 mutation ("restore the unconditional CheckNow")
// is most naturally applied exactly there. Reverting that argument to
// `h.onCheckUpdates` reinstates the original bug with every
// handlers_test.go assertion still green; this test is what fails.
//
// It also pins the property the label makes a promise about: the
// callback bound to an item is derived from the SAME state snapshot the
// label was, so a stale menu dispatches the stale label's action rather
// than a different, possibly destructive one.
func TestBuild_HelpUpdateItem_ClickDispatchesOnItsOwnLabel(t *testing.T) {
	cases := []struct {
		state                              UpdateMenuState
		wantCheck, wantDownload, wantApply int
	}{
		{UpdateIdle, 1, 0, 0},
		{UpdateAvailable, 0, 1, 0},
		{UpdateDownloading, 0, 0, 0},
		{UpdateStaged, 0, 0, 1},
		{UpdateFailed, 0, 1, 0},
	}
	for _, tc := range cases {
		t.Run(UpdateMenuLabel(tc.state), func(t *testing.T) {
			u := &fakeUpdateController{}
			h := NewHandlers(nil, u, nil)
			h.SetConfirmDialog(stubConfirm{answer: true})
			state := MenuState{ThemeMode: ThemeSystem, UpdateState: tc.state}
			item := helpUpdateItem(Build(state, h), state)
			if item == nil {
				t.Fatalf("no Help item labelled %q", UpdateMenuLabel(tc.state))
			}
			if item.Click == nil {
				t.Fatal("Help update item has no Click callback")
			}
			item.Click(nil)
			check, download, apply := u.snapshot()
			if check != tc.wantCheck || download != tc.wantDownload || apply != tc.wantApply {
				t.Errorf("%q: checkNow=%d startDownload=%d apply=%d, want %d/%d/%d",
					UpdateMenuLabel(tc.state), check, download, apply, tc.wantCheck, tc.wantDownload, tc.wantApply)
			}
		})
	}
}
