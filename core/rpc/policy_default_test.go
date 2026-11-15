package rpc

import (
	"context"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
)

// Launch spec L09 D1 / R6: a fresh host install runs the starter policy; an
// existing install keeps its behaviour until the person accepts it; served
// mode is the workbench (VM-bounded) and never applies it.
func TestPolicyDefault_Resolution(t *testing.T) {
	t.Run("fresh host install gets the starter policy", func(t *testing.T) {
		api := settings.NewAPI(nil)
		pd := newPolicyDefault(api.Store(), false)
		if !pd.StarterActive() {
			t.Fatal("fresh install: starter policy not active")
		}
		v, _ := pd.View()
		if v.Mode != settings.PolicyDefaultStarter || v.Upgraded || v.NoticeSeen {
			t.Fatalf("view = %+v", v)
		}
		got, _ := api.Store().LoadAll()
		if got.PolicyDefault != settings.PolicyDefaultStarter {
			t.Fatalf("persisted %q, want starter", got.PolicyDefault)
		}
		// A second boot reads the persisted choice, even after first-boot
		// markers are set.
		_ = api.Store().SaveBashAllowlistMigrated(true)
		if !newPolicyDefault(api.Store(), false).StarterActive() {
			t.Fatal("second boot lost the starter policy")
		}
	})

	for name, mark := range map[string]func(settings.SettingsStore){
		"bash migration ran": func(s settings.SettingsStore) { _ = s.SaveBashAllowlistMigrated(true) },
		"onboarding done":    func(s settings.SettingsStore) { _ = s.SaveFirstRunOnboardingCompleted(true) },
	} {
		t.Run("existing install keeps legacy until accepted ("+name+")", func(t *testing.T) {
			api := settings.NewAPI(nil)
			mark(api.Store())
			pd := newPolicyDefault(api.Store(), false)
			if pd.StarterActive() {
				t.Fatal("upgrade switched on the starter policy without the person accepting it")
			}
			v, _ := pd.View()
			if v.Mode != settings.PolicyDefaultLegacy || !v.Upgraded {
				t.Fatalf("view = %+v, want legacy + upgraded", v)
			}
			if err := pd.Set(settings.PolicyDefaultStarter); err != nil {
				t.Fatal(err)
			}
			if !pd.StarterActive() {
				t.Fatal("accepting did not switch it on")
			}
			v, _ = pd.View()
			if !v.NoticeSeen || v.Upgraded {
				t.Fatalf("after accept view = %+v", v)
			}
			if err := pd.Set(settings.PolicyDefaultLegacy); err != nil || pd.StarterActive() {
				t.Fatalf("switching back: err=%v active=%v", err, pd.StarterActive())
			}
			if err := pd.Set("allow-everything"); err != ErrPolicyDefaultUnknown {
				t.Fatalf("unknown mode: err=%v", err)
			}
		})
	}

	t.Run("served mode is the workbench and never starter", func(t *testing.T) {
		api := settings.NewAPI(nil)
		pd := newPolicyDefault(api.Store(), true)
		if pd.StarterActive() {
			t.Fatal("starter policy active in served mode")
		}
		v, _ := pd.View()
		if v.Mode != PolicyDefaultWorkbench {
			t.Fatalf("served view = %+v", v)
		}
		if err := pd.Set(settings.PolicyDefaultStarter); err != ErrPolicyDefaultServed {
			t.Fatalf("served Set: err=%v", err)
		}
		got, _ := api.Store().LoadAll()
		if got.PolicyDefault != "" {
			t.Fatalf("served mode persisted %q", got.PolicyDefault)
		}
	})

	t.Run("a whole-settings save cannot change the default", func(t *testing.T) {
		api := settings.NewAPI(nil)
		pd := newPolicyDefault(api.Store(), false)
		stale, _ := api.Store().LoadAll()
		stale.PolicyDefault = ""
		stale.PolicyDefaultNoticeSeen = true
		if err := api.Set(context.Background(), stale); err != nil {
			t.Fatal(err)
		}
		got, _ := api.Store().LoadAll()
		if got.PolicyDefault != settings.PolicyDefaultStarter || got.PolicyDefaultNoticeSeen {
			t.Fatalf("Settings_Set rewrote the policy default: %+v", got.PolicyDefault)
		}
		_ = pd
	})

	t.Run("nil is legacy", func(t *testing.T) {
		var pd *policyDefault
		if pd.StarterActive() {
			t.Fatal("nil active")
		}
		if v, _ := pd.View(); v.Mode != settings.PolicyDefaultLegacy {
			t.Fatalf("nil view %+v", v)
		}
	})
}

func TestPolicyDefault_NoPersistDecidesWithoutWriting(t *testing.T) {
	api := settings.NewAPI(nil)
	pd := newPolicyDefault(api.Store(), false, false)
	if !pd.StarterActive() {
		t.Fatal("fresh store: want starter")
	}
	got, _ := api.Store().LoadAll()
	if got.PolicyDefault != "" {
		t.Fatalf("persist=false wrote %q", got.PolicyDefault)
	}
}
