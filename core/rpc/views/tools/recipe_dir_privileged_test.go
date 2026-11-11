package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

// Fleet e2e 2026-11-07 E2E-3: after E2E-2 the agent asked for
// recipe_dir_add on /var/lib/kenaz-harness — the served harness's HOME,
// which holds its own state — and the prompt gave no hint.
func TestRequestAdditionalAllowedDir_HarnessState(t *testing.T) {
	t.Parallel()
	home := t.TempDir() // stands in for /var/lib/kenaz-harness
	dataDir := filepath.Join(home, ".kenaz", "harness", "prod")
	if err := os.MkdirAll(filepath.Join(dataDir, "policy"), 0o700); err != nil {
		t.Fatal(err)
	}
	newAPI := func(reg *cedar.Registry) *API {
		return New(Config{
			Catalog:        fsCatalogMinimal(),
			Enabled:        installFSRecipe([]string{t.TempDir()}),
			Pool:           newFakePool(),
			Secrets:        secrets.NewMemoryBackend(),
			DataDir:        dataDir,
			PromptRegistry: reg,
		})
	}

	t.Run("inside the state directory is refused before any prompt", func(t *testing.T) {
		reg := cedar.NewRegistry()
		granted, _, err := newAPI(reg).RequestAdditionalAllowedDir(context.Background(), "filesystem", filepath.Join(dataDir, "policy"), "workaround")
		if granted || !errors.Is(err, ErrPrivilegedDir) {
			t.Fatalf("granted=%v err=%v, want refusal with ErrPrivilegedDir", granted, err)
		}
		if n := reg.PendingCount(); n != 0 {
			t.Fatalf("pending = %d, want 0 (no prompt for a refused path)", n)
		}
	})

	t.Run("a directory containing the state prompts with a privileged label", func(t *testing.T) {
		reg := cedar.NewRegistry(cedar.WithTimeout(5 * time.Second))
		got := make(chan string, 1)
		go func() {
			for {
				if p := reg.ListPending(); len(p) > 0 {
					got <- p[0].Surface.FS.Privileged
					_ = reg.Resolve(p[0].RequestID, cedar.DecisionDeny)
					return
				}
				time.Sleep(2 * time.Millisecond)
			}
		}()
		granted, _, _ := newAPI(reg).RequestAdditionalAllowedDir(context.Background(), "filesystem", home, "workaround")
		if granted {
			t.Fatal("granted after deny")
		}
		if label := <-got; !strings.Contains(label, "harness's own state") {
			t.Fatalf("Privileged = %q, want the harness-state label", label)
		}
	})
}
