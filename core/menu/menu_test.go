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
	// AppMenuRole == 1 per Wails constants.
	if m.Items[0].Role != 1 {
		t.Errorf("first item role = %d, want 1 (AppMenuRole)", m.Items[0].Role)
	}
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
