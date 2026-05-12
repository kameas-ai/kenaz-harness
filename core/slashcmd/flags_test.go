package slashcmd_test

import (
	"context"
	"errors"
	"testing"

	coreslashcmd "github.com/sigil-tech/kaneaz-harness/core/slashcmd"
	"github.com/sigil-tech/kaneaz-harness/core/storage"
	storagesqlite "github.com/sigil-tech/kaneaz-harness/core/storage/sqlite"
)

func openFlagsTestDB(t *testing.T) (storage.DB, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := storagesqlite.Open(storage.Config{
		DataDir:          dir,
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	return db, dir
}

func TestUserSlashcmdEnabled_DefaultOn(t *testing.T) {
	// Default (no env var set) should be on.
	t.Setenv("HARNESS_USER_SLASHCMD", "")
	if !coreslashcmd.UserSlashcmdEnabled() {
		t.Error("expected UserSlashcmdEnabled() = true by default")
	}
}

func TestUserSlashcmdEnabled_ExplicitTrue(t *testing.T) {
	for _, v := range []string{"1", "true", "TRUE", "yes", "on"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("HARNESS_USER_SLASHCMD", v)
			if !coreslashcmd.UserSlashcmdEnabled() {
				t.Errorf("UserSlashcmdEnabled() = false for HARNESS_USER_SLASHCMD=%q, want true", v)
			}
		})
	}
}

func TestUserSlashcmdEnabled_ExplicitFalse(t *testing.T) {
	for _, v := range []string{"0", "false", "FALSE", "False"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv("HARNESS_USER_SLASHCMD", v)
			if coreslashcmd.UserSlashcmdEnabled() {
				t.Errorf("UserSlashcmdEnabled() = true for HARNESS_USER_SLASHCMD=%q, want false", v)
			}
		})
	}
}

func TestDispatch_Run_FeatureDisabledReturnsError(t *testing.T) {
	t.Setenv("HARNESS_USER_SLASHCMD", "0")

	db, dir := openFlagsTestDB(t)
	store := coreslashcmd.NewStore(db, dir)

	// Save a command that would otherwise succeed.
	ctx := context.Background()
	if err := store.SaveUser(ctx, coreslashcmd.UserCommand{
		Name:        "blocked",
		Scope:       "global",
		Kind:        "text",
		Description: "Blocked by flag",
		Body:        "should not appear",
	}); err != nil {
		// SaveUser itself doesn't check the flag; only Run does.
		t.Fatalf("SaveUser unexpectedly failed: %v", err)
	}

	dispatch := coreslashcmd.NewDispatch(store, nil)
	_, err := dispatch.Run(ctx, "blocked", nil, coreslashcmd.SessionContext{SessionID: "s1"})

	if err == nil {
		t.Fatal("expected error when feature flag is off")
	}
	if !errors.Is(err, coreslashcmd.ErrFeatureDisabled) {
		t.Errorf("err = %v, want ErrFeatureDisabled", err)
	}
}
