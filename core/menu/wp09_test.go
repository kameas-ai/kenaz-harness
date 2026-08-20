package menu

// wp09_test.go — controls-and-readouts-that-tell-the-truth-01PMZ808
// UNIT-5 / WP09. AC-023: on Windows/Linux, Ctrl+F resolves to exactly
// one menu registration (before this WP, buildEditMenu's "Find" and
// buildViewMenu's "Search" both registered Ctrl+F -> h.onFind).

import (
	"runtime"
	"testing"

	wailsmenu "github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
)

// countCtrlFRegistrations walks the whole menu tree and counts items
// whose accelerator is CmdOrCtrl+F, regardless of label or nesting.
func countCtrlFRegistrations(items []*wailsmenu.MenuItem) int {
	n := 0
	for _, item := range items {
		if item.Accelerator != nil && item.Accelerator.Key == "f" {
			hasCmdOrCtrl := false
			for _, mod := range item.Accelerator.Modifiers {
				if mod == keys.CmdOrCtrlKey {
					hasCmdOrCtrl = true
				}
			}
			if hasCmdOrCtrl {
				n++
			}
		}
		if item.SubMenu != nil {
			n += countCtrlFRegistrations(item.SubMenu.Items)
		}
	}
	return n
}

func TestBuild_WinLinux_CtrlF_ExactlyOneRegistration(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("Windows/Linux-only test — buildEditMenu returns wailsmenu.EditMenu() on darwin, a different code path entirely")
	}
	m := Build(MenuState{}, noopHandlers())
	got := countCtrlFRegistrations(m.Items)
	if got != 1 {
		t.Fatalf("Ctrl+F registered %d times in the menu tree, want exactly 1 (View -> Search). Mutation guard: restoring buildEditMenu's second AddText(\"Find\", keys.CmdOrCtrl(\"f\"), h.onFind) must make this fail.", got)
	}
}
