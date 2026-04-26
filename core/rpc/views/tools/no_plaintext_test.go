package tools

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/mcp/recipes"
	"github.com/sigil-tech/kaneaz-harness/core/secrets"
)

// TestInstallRecipe_NoPlaintextOnDisk asserts NFR-006 (no plaintext
// in <DataDir>): every file under the data directory after Install
// must be free of the supplied API-key sentinel. The sentinel is a
// random-looking literal that cannot legitimately appear in
// metadata, so any hit is a real plaintext leak.
//
// The test uses fixedKeychain (which routes writes through the
// in-memory secrets backend) instead of the real OS-keychain
// keychainWriter, but the persistence-disk surface is identical:
// neither writes to <DataDir>. EnabledRecipes.Save persists its
// entry shape (id, audit-hash) but never the plaintext value.
func TestInstallRecipe_NoPlaintextOnDisk(t *testing.T) {
	t.Parallel()
	const sentinel = "test_secret_marker_NFR006_xyz"

	dataDir := t.TempDir()
	cat := &recipes.Catalog{Version: 1, Recipes: []recipes.Recipe{
		{
			ID:          "brave-search",
			DisplayName: "Brave Search",
			Command:     []string{"/bin/echo"},
			EnvKeys: []recipes.EnvKey{
				{Name: "BRAVE_API_KEY", Display: "Brave Search API Key", Required: true},
			},
		},
	}}
	enabled := &recipes.EnabledRecipes{}
	backend := secrets.NewMemoryBackend()
	pool := newFakePool()
	api := New(Config{
		Catalog:  cat,
		Enabled:  enabled,
		Pool:     pool,
		Secrets:  backend,
		Keychain: &fixedKeychain{backend: backend},
		DataDir:  dataDir,
	})

	env := map[string]string{"BRAVE_API_KEY": sentinel}
	if _, err := api.InstallRecipe(context.Background(), "brave-search", env); err != nil {
		t.Fatalf("InstallRecipe: %v", err)
	}

	// Walk the data directory and assert no file contains the
	// sentinel. We skip:
	//  - .lock files / lockfile internals (defensive — none are
	//    written today, but the storage subsystem may add them).
	walkErr := filepath.WalkDir(dataDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".lock") {
			return nil
		}
		data, readErr := os.ReadFile(path) // #nosec G304 -- test temp dir
		if readErr != nil {
			return readErr
		}
		if bytes.Contains(data, []byte(sentinel)) {
			t.Errorf("plaintext sentinel found in %s", path)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk %s: %v", dataDir, walkErr)
	}
}
