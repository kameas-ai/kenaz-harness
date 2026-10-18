package sidecar

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/mlsidecar"
)

// TestUninstall_RefusedWhileKenazUsesTheSharedEngine: the install root is
// shared with Kenaz (Amendment A5). Uninstall from the harness must not
// pull the engine out from under a live Kenaz lease — it says so, leaves
// everything installed, and the panel surfaces the error text.
func TestUninstall_RefusedWhileKenazUsesTheSharedEngine(t *testing.T) {
	f := newFixture(t)
	resets := 0
	f.impl.ResetLabelCursor = func(context.Context) error { resets++; return nil }
	if _, err := f.impl.Enable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := mlsidecar.AcquireOrRenewLease(f.mgr.Layout, "kenaz", os.Getpid(), "2.0.0"); err != nil {
		t.Fatal(err)
	}
	v, err := f.impl.Uninstall(context.Background())
	if !errors.Is(err, mlsidecar.ErrEngineInUse) {
		t.Fatalf("err = %v, want ErrEngineInUse", err)
	}
	if !v.Installed {
		t.Fatalf("the engine must stay installed: %+v", v)
	}
	if _, serr := os.Stat(f.root); serr != nil {
		t.Fatalf("install root was removed while another app uses it: %v", serr)
	}
	f.eng.mu.Lock()
	defer f.eng.mu.Unlock()
	if f.eng.shutdowns != 0 {
		t.Fatalf("engine shutdown requests = %d, want 0", f.eng.shutdowns)
	}
	// The engine (and its label mirror) is still there: rewinding the
	// cursor would re-push everything for nothing.
	if resets != 0 {
		t.Fatalf("label cursor resets = %d on a refused uninstall, want 0", resets)
	}
}
