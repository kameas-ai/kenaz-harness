package scheduler_test

// containment_test.go — model-harness-toolset-01MHTS001 WP02 (H-1):
// ResolveRunContainment's table, plus the store-level proof that a
// corrupted tool_allowlist column reads back as unresolvable (real
// sqlite), never as "no allowlist".

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/scheduler"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

// openChatStoreWithDB is openTestChatStore plus the underlying DB, for a
// test that must write a raw column value no production writer produces.
func openChatStoreWithDB(t *testing.T) (scheduler.ScheduledChatStore, storage.DB) {
	t.Helper()
	db, err := storagesqlite.Open(storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	return scheduler.NewSQLiteChatStore(db), db
}

func TestResolveRunContainment(t *testing.T) {
	model, user := scheduler.ScheduledRunCreatedByModel, scheduler.ScheduledRunCreatedByUser
	cases := []struct {
		name      string
		spec      *scheduler.ChatRunSpec
		rec       scheduler.ChatRunRecord
		refuse    bool
		contained bool
		allow     []string
	}{
		{name: "model, list on both sides", spec: &scheduler.ChatRunSpec{CreatedBy: model, ToolAllowlist: []string{"kenaz__a"}},
			rec: scheduler.ChatRunRecord{CreatedBy: model, ToolAllowlist: []string{"kenaz__a"}}, contained: true, allow: []string{"kenaz__a"}},
		{name: "model, row widened after gate -> intersection", spec: &scheduler.ChatRunSpec{CreatedBy: model, ToolAllowlist: []string{"kenaz__a"}},
			rec: scheduler.ChatRunRecord{CreatedBy: model, ToolAllowlist: []string{"kenaz__a", "kenaz__b"}}, contained: true, allow: []string{"kenaz__a"}},
		{name: "model, no spec (direct caller) -> row list", spec: nil,
			rec: scheduler.ChatRunRecord{CreatedBy: model, ToolAllowlist: []string{"kenaz__a"}}, contained: true, allow: []string{"kenaz__a"}},
		{name: "model, empty row list refuses", spec: &scheduler.ChatRunSpec{CreatedBy: model, ToolAllowlist: []string{"kenaz__a"}},
			rec: scheduler.ChatRunRecord{CreatedBy: model}, refuse: true},
		{name: "model, unresolvable row list refuses", spec: &scheduler.ChatRunSpec{CreatedBy: model, ToolAllowlist: []string{"kenaz__a"}},
			rec: scheduler.ChatRunRecord{CreatedBy: model, ToolAllowlistUnresolvable: true}, refuse: true},
		{name: "model, spec without list refuses", spec: &scheduler.ChatRunSpec{CreatedBy: model},
			rec: scheduler.ChatRunRecord{CreatedBy: model, ToolAllowlist: []string{"kenaz__a"}}, refuse: true},
		{name: "model, disjoint lists refuse", spec: &scheduler.ChatRunSpec{CreatedBy: model, ToolAllowlist: []string{"kenaz__a"}},
			rec: scheduler.ChatRunRecord{CreatedBy: model, ToolAllowlist: []string{"kenaz__b"}}, refuse: true},
		{name: "spec says model, row says user -> model rules", spec: &scheduler.ChatRunSpec{CreatedBy: model, ToolAllowlist: []string{"kenaz__a"}},
			rec: scheduler.ChatRunRecord{CreatedBy: user}, refuse: true},
		{name: "user, no list anywhere -> unchanged, uncontained", spec: &scheduler.ChatRunSpec{CreatedBy: user},
			rec: scheduler.ChatRunRecord{CreatedBy: user}},
		{name: "legacy empty created_by, no list -> uncontained", spec: nil,
			rec: scheduler.ChatRunRecord{}},
		{name: "user with declared list -> contained", spec: &scheduler.ChatRunSpec{CreatedBy: user, ToolAllowlist: []string{"kenaz__a"}},
			rec: scheduler.ChatRunRecord{CreatedBy: user, ToolAllowlist: []string{"kenaz__a"}}, contained: true, allow: []string{"kenaz__a"}},
		{name: "user, unresolvable list -> contained to nothing", spec: &scheduler.ChatRunSpec{CreatedBy: user},
			rec: scheduler.ChatRunRecord{CreatedBy: user, ToolAllowlistUnresolvable: true}, contained: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scheduler.ResolveRunContainment(tc.spec, tc.rec)
			if (got.Refuse != "") != tc.refuse {
				t.Fatalf("Refuse = %q, want refuse=%v", got.Refuse, tc.refuse)
			}
			if tc.refuse {
				return
			}
			if got.Contained != tc.contained || !reflect.DeepEqual(got.Allow, tc.allow) {
				t.Fatalf("got contained=%v allow=%v, want contained=%v allow=%v", got.Contained, got.Allow, tc.contained, tc.allow)
			}
		})
	}
}

// TestChatStore_CorruptAllowlistReadsUnresolvable: a non-empty
// tool_allowlist that does not decode to at least one name is flagged,
// so the fire path can deny rather than read it as "no allowlist".
func TestChatStore_CorruptAllowlistReadsUnresolvable(t *testing.T) {
	store, db := openChatStoreWithDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for _, id := range []string{"good", "bad", "emptyjson", "none"} {
		rec := scheduler.ChatRunRecord{ID: id, Name: id, PromptTemplate: "p", Cron: "0 9 * * *", CreatedAt: now, UpdatedAt: now}
		if id != "none" {
			rec.ToolAllowlist = []string{"kenaz__a"}
		}
		if err := store.Create(ctx, rec); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
	}
	for id, raw := range map[string]string{"bad": `{oops`, "emptyjson": `[]`} {
		if err := db.WriteTx(ctx, func(tx storage.WriteTx) error {
			_, err := tx.Exec(ctx, `UPDATE scheduled_chat_runs SET tool_allowlist = ? WHERE id = ?`, raw, id)
			return err
		}); err != nil {
			t.Fatalf("corrupt %s: %v", id, err)
		}
	}
	want := map[string]bool{"good": false, "bad": true, "emptyjson": true, "none": false}
	for id, unresolvable := range want {
		rec, err := store.Get(ctx, id)
		if err != nil {
			t.Fatalf("Get %s: %v", id, err)
		}
		if rec.ToolAllowlistUnresolvable != unresolvable {
			t.Errorf("%s: ToolAllowlistUnresolvable = %v, want %v (allowlist %v)", id, rec.ToolAllowlistUnresolvable, unresolvable, rec.ToolAllowlist)
		}
	}
}
