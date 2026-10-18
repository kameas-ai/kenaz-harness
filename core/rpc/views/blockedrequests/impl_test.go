package blockedrequests_test

// AC-008 (model-scheduled-jobs-01PMSJ01 WP07, FR-004): grant a pending
// fs request; assert a Cedar snippet permitting Action::"write_filesystem"
// for that path is written to <DataDir>/policy/, that a FRESH engine
// load evaluates the same request as Allow, and that the row moves to
// status='granted'.
//
// *Fails if:* only the in-memory engine is mutated —
// docs/unwired-ledger.md:934-940 records that the in-session policy
// editor already tells users a rule is live when it is not. This test
// deliberately builds a SECOND, freshly-constructed cedar.Engine over
// the same DataDir after Grant returns, and evaluates through it — not
// the engine Grant itself wrote through — so persistence, not an
// in-memory mutation, is what's actually being asserted.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	blockedrequestsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/blockedrequests"
	cedarpolicyview "github.com/kameas-ai/kenaz-harness/core/rpc/views/cedarpolicy"

	"github.com/kameas-ai/kenaz-harness/core/policy/blockedrequests"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	corefs "github.com/kameas-ai/kenaz-harness/core/tools/fs"

	_ "modernc.org/sqlite"
)

func openTestStore(t *testing.T) blockedrequests.Store {
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
	return blockedrequests.NewSQLiteStore(db)
}

func TestAPI_AC008_GrantWritesPersistedSnippetAndTransitionsStatus(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	target := filepath.Join(t.TempDir(), "briefing.md")

	if err := store.Create(ctx, blockedrequests.Record{
		ID:        "req-ac008",
		Origin:    "scheduled_chat_run",
		OriginID:  "chatrun-1",
		SessionID: "sess-1",
		Family:    "fs",
		Action:    "write_filesystem",
		Resource:  target,
		Reason:    "no policy permits this write",
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed pending row: %v", err)
	}

	policyDataDir := t.TempDir()
	engine, err := cedar.NewEngine(cedar.Options{DataDir: policyDataDir, LoadFromDisk: true, IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("cedar.NewEngine: %v", err)
	}
	cedarAPI := cedarpolicyview.NewAPIWithDataDir(engine, policyDataDir)

	api := blockedrequestsview.New(blockedrequestsview.Config{Store: store, CedarPolicy: cedarAPI})
	if err := api.Grant(ctx, "req-ac008"); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	// The row transitions to granted.
	got, err := store.Get(ctx, "req-ac008")
	if err != nil {
		t.Fatalf("Get after Grant: %v", err)
	}
	if got.Status != blockedrequests.StatusGranted {
		t.Errorf("Status = %q, want granted", got.Status)
	}
	if got.ResolvedAt == nil {
		t.Error("ResolvedAt was not stamped")
	}

	// A snippet permitting write_filesystem for the exact path exists on
	// disk under <DataDir>/policy/.
	policyDir := filepath.Join(policyDataDir, cedar.PolicyDir)
	entries, err := os.ReadDir(policyDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("expected a cedar snippet under %s, got err=%v entries=%v", policyDir, err, entries)
	}
	found := false
	for _, e := range entries {
		body, rerr := os.ReadFile(filepath.Join(policyDir, e.Name()))
		if rerr != nil {
			continue
		}
		s := string(body)
		if strings.Contains(s, target) && strings.Contains(s, "write_filesystem") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no snippet under %s permits write_filesystem for %s", policyDir, target)
	}

	// *Fails if only the in-memory engine is mutated*: build a FRESH
	// engine from the SAME DataDir and evaluate independently.
	reloaded, err := cedar.NewEngine(cedar.Options{DataDir: policyDataDir, LoadFromDisk: true, IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("cedar.NewEngine (reload): %v", err)
	}
	reloadGate := corefs.NewGate(corefs.GateOptions{Engine: reloaded}) // no Prompter — an Allow here can ONLY come from the persisted snippet
	decision, err := reloadGate.Evaluate(ctx, corefs.OpWrite, target)
	if err != nil {
		t.Fatalf("reload Evaluate: %v", err)
	}
	if decision.Outcome != cedar.Allow {
		t.Fatalf("reloaded engine outcome = %s, want Allow (grant did not survive reload)", decision.Outcome)
	}
}

func TestAPI_ListPending(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := store.Create(ctx, blockedrequests.Record{
		ID: "p1", Origin: "interactive", Family: "fs", Action: "write_filesystem",
		Resource: "/tmp/a", CreatedAt: now,
	}); err != nil {
		t.Fatalf("create p1: %v", err)
	}
	if err := store.Create(ctx, blockedrequests.Record{
		ID: "p2", Origin: "interactive", Family: "fs", Action: "read_filesystem",
		Resource: "/tmp/b", CreatedAt: now.Add(time.Second),
	}); err != nil {
		t.Fatalf("create p2: %v", err)
	}
	if err := store.SetStatus(ctx, "p2", blockedrequests.StatusDismissed, now.Add(2*time.Second)); err != nil {
		t.Fatalf("dismiss p2: %v", err)
	}

	api := blockedrequestsview.New(blockedrequestsview.Config{Store: store})
	got, err := api.ListPending(ctx)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(got) != 1 || got[0].ID != "p1" {
		t.Fatalf("ListPending = %+v, want exactly [p1] (p2 was dismissed)", got)
	}
}

func TestAPI_Dismiss(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	if err := store.Create(ctx, blockedrequests.Record{
		ID: "d1", Origin: "interactive", Family: "fs", Action: "write_filesystem",
		Resource: "/tmp/d", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	api := blockedrequestsview.New(blockedrequestsview.Config{Store: store})
	if err := api.Dismiss(ctx, "d1"); err != nil {
		t.Fatalf("Dismiss: %v", err)
	}
	got, err := store.Get(ctx, "d1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != blockedrequests.StatusDismissed {
		t.Errorf("Status = %q, want dismissed", got.Status)
	}
}

func TestAPI_GrantTwiceReturnsAlreadyResolved(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	target := filepath.Join(t.TempDir(), "x.txt")
	if err := store.Create(ctx, blockedrequests.Record{
		ID: "g1", Origin: "interactive", Family: "fs", Action: "write_filesystem",
		Resource: target, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	dataDir := t.TempDir()
	engine, err := cedar.NewEngine(cedar.Options{DataDir: dataDir, LoadFromDisk: true, IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("cedar.NewEngine: %v", err)
	}
	api := blockedrequestsview.New(blockedrequestsview.Config{
		Store:       store,
		CedarPolicy: cedarpolicyview.NewAPIWithDataDir(engine, dataDir),
	})
	if err := api.Grant(ctx, "g1"); err != nil {
		t.Fatalf("first Grant: %v", err)
	}
	if err := api.Grant(ctx, "g1"); err == nil {
		t.Fatal("second Grant on an already-granted row must fail")
	} else if err != blockedrequestsview.ErrAlreadyResolved {
		t.Errorf("second Grant error = %v, want ErrAlreadyResolved", err)
	}
}

func TestAPI_GrantUnsupportedFamily(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	if err := store.Create(ctx, blockedrequests.Record{
		ID: "b1", Origin: "interactive", Family: "bash", Action: "exec",
		Resource: "rm -rf /", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	api := blockedrequestsview.New(blockedrequestsview.Config{Store: store})
	if err := api.Grant(ctx, "b1"); err != blockedrequestsview.ErrUnsupportedFamily {
		t.Errorf("Grant on family=bash: err = %v, want ErrUnsupportedFamily", err)
	}
}
