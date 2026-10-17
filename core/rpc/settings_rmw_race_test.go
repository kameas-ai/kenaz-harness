package rpc

// settings_rmw_race_test.go — every read-modify-write of the settings
// record goes through settings.Update, under the store lock, so concurrent
// writers cannot lose each other's changes. Drives a real FileStore (run
// under -race in CI).
//
// Before the fix each writer did a bare LoadAll then SaveAll: a ui_theme
// pull applied, a whole-settings save, or a toggle that read before
// Sync_ConfirmUploads and wrote after it put back the old
// SettingsSyncUploadsConfirmed=false, silently withdrawing the user's
// consent (and dropping opt-ins the same way).

import (
	"context"
	"fmt"
	"sync"
	"testing"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
)

func TestSettingsRMW_ConcurrentWritersLoseNothing(t *testing.T) {
	ctx := context.Background()
	for round := 0; round < 5; round++ {
		dir := t.TempDir()
		store, err := settings.NewFileStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		consent := settingsUploadConsent{store: store}
		optIns := newSettingsSyncOptIns(store)
		theme := uiThemeKind(store)
		api := settings.NewAPI(store)
		stale, _ := store.LoadAll() // a copy loaded before everything below

		cats := []string{"ui_theme", "model_prefs", "installed_mcp", "c4", "c5", "c6", "c7", "c8"}
		var wg sync.WaitGroup
		start := make(chan struct{})
		run := func(f func() error) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if err := f(); err != nil {
					t.Error(err)
				}
			}()
		}
		run(consent.ConfirmUploads)
		for _, c := range cats {
			c := c
			run(func() error { return optIns.SetCategoryEnabled(c, true) })
		}
		for i := 0; i < 8; i++ {
			i := i
			run(func() error {
				return theme.Apply(ctx, corefleet.ScopeUser, []byte(fmt.Sprintf(`{"theme":"t%d"}`, i)))
			})
			run(func() error { return api.Set(ctx, stale) })
		}
		close(start)
		wg.Wait()

		got, err := settings.NewFileStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		s, err := got.LoadAll()
		if err != nil {
			t.Fatal(err)
		}
		if !s.SettingsSyncUploadsConfirmed {
			t.Fatalf("round %d: a concurrent read-modify-write reverted the upload confirmation", round)
		}
		have := map[string]bool{}
		for _, c := range s.SettingsSyncEnabledCategories {
			have[c] = true
		}
		for _, c := range cats {
			if !have[c] {
				t.Fatalf("round %d: opt-in %q lost to a concurrent write (have %v)", round, c, s.SettingsSyncEnabledCategories)
			}
		}
	}
}
