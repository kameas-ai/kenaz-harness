package sessions

import (
	"context"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// The delete hook closes confirm-each-enforcement-01PMAG05 review
// finding 7: "allow for this session" grants are process-lifetime, so
// without a teardown call a deleted-then-recreated session id inherited
// the approvals of a session the user threw away. The chassis wires
// WithDeleteHookOpt to SessionGrantCache.RevokeSession (core/rpc/api.go);
// this test drives the same pair end-to-end through a real delete.
func TestManagerAPI_Delete_FiresTeardownHookAndRevokesSessionGrants(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db, err := storagesqlite.Open(storage.Config{
		DataDir:          dir,
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ctx := context.Background()
	defer db.Close(ctx)

	sessMgr := session.NewManager(session.NewSQLStore(session.NewStorageDB(db)))
	grants := toolloop.NewSessionGrantCache()
	var hookCalls []string
	api := WithDeleteHookOpt(NewManagerAPI(sessMgr), func(sessionID string) {
		hookCalls = append(hookCalls, sessionID)
		grants.RevokeSession(sessionID)
	})

	rec, err := api.Create(ctx, "doomed")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	grants.Grant(rec.ID, "filesystem", "write_file")
	if !grants.Has(rec.ID, "filesystem", "write_file") {
		t.Fatal("precondition: grant not recorded")
	}

	// A FAILED delete must not fire the hook: the session still exists,
	// so its grants are still scoped to something.
	if err := api.Delete(ctx, "no-such-session"); err == nil {
		t.Fatal("Delete of unknown id succeeded; want error")
	}
	if len(hookCalls) != 0 {
		t.Fatalf("hook fired on a failed delete: %v", hookCalls)
	}
	if !grants.Has(rec.ID, "filesystem", "write_file") {
		t.Fatal("grant vanished without its session being deleted")
	}

	if err := api.Delete(ctx, rec.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(hookCalls) != 1 || hookCalls[0] != rec.ID {
		t.Fatalf("hook calls = %v, want exactly [%s]", hookCalls, rec.ID)
	}
	if grants.Has(rec.ID, "filesystem", "write_file") {
		t.Fatal("session grant survived its session's deletion (01PMAG05 review finding 7)")
	}
}
