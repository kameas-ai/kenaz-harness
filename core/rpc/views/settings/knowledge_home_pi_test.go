package settings

// knowledge_home_pi_test.go — knowledge-home-01DOGF0E WP-PI (persistence
// integrity, kitty-specs/_templates/WP-persistence-integrity.md).
//
// The mission moved the long-term memory switch from Tools into
// Knowledge › Learned and retired the /contexts and /memory routes in favour
// of /knowledge/{curated,learned}. Two persisted values in settings.json are
// on that path:
//
//   - memoryEnabled — the Learned toggle calls the SAME Settings_SetMemory /
//     Settings_GetMemory bindings the Tools row did. This pins the round trip
//     on the real FileStore (not a fake), starting from the committed
//     previous-release fixture testdata/upgrade/v0.64.0/settings.json, which
//     predates nothing relevant but is a real, non-empty file with no
//     memoryEnabled key: absent must read as OFF (privacy default), and a
//     write must survive a fresh store instance without disturbing unrelated
//     fields.
//   - lastRoute — a previous release persisted "/memory" or "/contexts" when
//     the user quit on those surfaces. The backend stores the string
//     verbatim; the frontend's route table redirects it on restore
//     (frontend/src/__tests__/lastRoute.knowledgeRedirect.test.ts drives
//     restoreLastRoute over the real route tables). This half pins that the
//     stored value comes back byte-for-byte, so the redirect has something
//     to act on.
//
// settings.json is a JSON file, not sqlite, so TestUpgradePath's dump.sql
// chain cannot reach it (see graph_authoring_upgrade_test.go's header); the
// committed JSON fixture is the previous-release state for this store.

import (
	"os"
	"path/filepath"
	"testing"
)

func openFromV0640Fixture(t *testing.T) (string, *FileStore) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "upgrade", "v0.64.0", "settings.json"))
	if err != nil {
		t.Fatalf("read committed v0.64.0 settings fixture: %v", err)
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "kenaz-harness", "settings.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	return dir, store
}

func TestKnowledgeHome_MemorySetting_RoundTripsFromPreviousReleaseFile(t *testing.T) {
	dir, store := openFromV0640Fixture(t)

	on, err := store.LoadMemory()
	if err != nil {
		t.Fatalf("LoadMemory: %v", err)
	}
	if on {
		t.Fatal("LoadMemory on a previous-release file with no memoryEnabled key = true; absent must read as off")
	}

	for _, want := range []bool{true, false, true} {
		if err := store.SaveMemory(want); err != nil {
			t.Fatalf("SaveMemory(%v): %v", want, err)
		}
		// A fresh store instance reads the file, not an in-process cache.
		reopened, err := NewFileStore(dir)
		if err != nil {
			t.Fatalf("NewFileStore (reopen): %v", err)
		}
		got, err := reopened.LoadMemory()
		if err != nil {
			t.Fatalf("LoadMemory after reopen: %v", err)
		}
		if got != want {
			t.Fatalf("LoadMemory after SaveMemory(%v) + reopen = %v", want, got)
		}
	}

	// Unrelated fields from the previous release survive the writes.
	all, err := store.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if all.Theme != "dark" || all.LastRoute != "/sessions/abc123" ||
		all.WindowSize.Width != 1280 || all.WindowSize.Height != 800 {
		t.Fatalf("SaveMemory disturbed unrelated fields: theme=%q lastRoute=%q window=%+v",
			all.Theme, all.LastRoute, all.WindowSize)
	}
}

func TestKnowledgeHome_LegacyLastRoute_StoredVerbatim(t *testing.T) {
	dir, store := openFromV0640Fixture(t)
	for _, route := range []string{"/memory", "/contexts", "/memory?scopeKind=project&scopeId=p1"} {
		if err := store.SaveRoute(route); err != nil {
			t.Fatalf("SaveRoute(%q): %v", route, err)
		}
		reopened, err := NewFileStore(dir)
		if err != nil {
			t.Fatalf("NewFileStore (reopen): %v", err)
		}
		got, err := reopened.LoadRoute()
		if err != nil {
			t.Fatalf("LoadRoute: %v", err)
		}
		if got != route {
			t.Fatalf("LoadRoute after SaveRoute(%q) = %q", route, got)
		}
	}
}
