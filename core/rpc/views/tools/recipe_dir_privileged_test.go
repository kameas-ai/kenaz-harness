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
		reg := failOnPrompt(t)
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

// TestRequestAdditionalAllowedDir_StateBehindSymlink: the requested path
// is symlink-resolved before the state check, so the state directories
// must be too. Before the fix the comparison was resolved-vs-unresolved:
// a DataDir reached through a symlink (and every t.TempDir() on macOS,
// /var -> /private/var) never matched, and the refusal fell through to an
// interactive prompt that hung the test for the 5-minute PromptTimeout.
// The explicit symlink here reproduces that on every platform.
func TestRequestAdditionalAllowedDir_StateBehindSymlink(t *testing.T) {
	t.Parallel()
	real := t.TempDir()
	if err := os.MkdirAll(filepath.Join(real, ".kenaz", "harness", "prod", "policy"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "home-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	dataDir := filepath.Join(link, ".kenaz", "harness", "prod")
	for _, req := range []string{
		filepath.Join(dataDir, "policy"),                 // through the link
		filepath.Join(real, ".kenaz", "harness", "prod"), // the real path
	} {
		reg := failOnPrompt(t)
		api := New(Config{
			Catalog:        fsCatalogMinimal(),
			Enabled:        installFSRecipe([]string{t.TempDir()}),
			Pool:           newFakePool(),
			Secrets:        secrets.NewMemoryBackend(),
			DataDir:        dataDir,
			PromptRegistry: reg,
		})
		granted, _, err := api.RequestAdditionalAllowedDir(context.Background(), "filesystem", req, "workaround")
		if granted || !errors.Is(err, ErrPrivilegedDir) {
			t.Fatalf("%s: granted=%v err=%v, want refusal with ErrPrivilegedDir", req, granted, err)
		}
	}
}

// failOnPrompt returns a registry for a test that must never reach an
// interactive prompt. If one is enqueued anyway, the test fails at once
// and the prompt is denied so the call under test returns, instead of
// blocking for the 5-minute PromptTimeout. The short registry timeout is
// a second backstop should the watcher miss it.
func failOnPrompt(t *testing.T) *cedar.Registry {
	t.Helper()
	reg := cedar.NewRegistry(cedar.WithTimeout(5 * time.Second))
	done := make(chan struct{})
	stopped := make(chan struct{})
	t.Cleanup(func() { close(done); <-stopped })
	go func() {
		defer close(stopped)
		tick := time.NewTicker(2 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				for _, p := range reg.ListPending() {
					t.Errorf("unexpected interactive prompt for %+v", p.Surface.FS)
					_ = reg.Resolve(p.RequestID, cedar.DecisionDeny)
				}
			}
		}
	}()
	return reg
}

// On a case-insensitive volume (macOS APFS, Windows NTFS) a case variant
// of the state directory is the state directory, and must be refused.
func TestRequestAdditionalAllowedDir_StateCaseVariant(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dataDir := filepath.Join(home, ".kenaz", "harness", "prod")
	if err := os.MkdirAll(filepath.Join(dataDir, "policy"), 0o700); err != nil {
		t.Fatal(err)
	}
	variant := filepath.Join(home, ".KENAZ", "harness", "prod", "policy")
	if _, err := os.Stat(variant); err != nil {
		t.Skipf("case-sensitive filesystem, no case alias to test: %v", err)
	}
	api := New(Config{
		Catalog:        fsCatalogMinimal(),
		Enabled:        installFSRecipe([]string{t.TempDir()}),
		Pool:           newFakePool(),
		Secrets:        secrets.NewMemoryBackend(),
		DataDir:        dataDir,
		PromptRegistry: failOnPrompt(t),
	})
	granted, _, err := api.RequestAdditionalAllowedDir(context.Background(), "filesystem", variant, "workaround")
	if granted || !errors.Is(err, ErrPrivilegedDir) {
		t.Fatalf("granted=%v err=%v, want refusal with ErrPrivilegedDir", granted, err)
	}
}

// With the workspace = HOME (it CONTAINS the state), the state below it is
// still refused, and HOME itself prompts with the privileged label.
// Before, "inside the workspace" won and the policy dir could be granted.
func TestRequestAdditionalAllowedDir_WorkspaceHomeDoesNotExemptState(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
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
			WorkspaceDir:   home,
			PromptRegistry: reg,
		})
	}
	for _, p := range []string{
		filepath.Join(dataDir, "policy"),
		dataDir,
		filepath.Join(home, ".kenaz", "harness"),
	} {
		granted, _, err := newAPI(failOnPrompt(t)).RequestAdditionalAllowedDir(context.Background(), "filesystem", p, "workaround")
		if granted || !errors.Is(err, ErrPrivilegedDir) {
			t.Fatalf("%s: granted=%v err=%v, want refusal with ErrPrivilegedDir", p, granted, err)
		}
	}

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
	if granted, _, _ := newAPI(reg).RequestAdditionalAllowedDir(context.Background(), "filesystem", home, "workaround"); granted {
		t.Fatal("granted after deny")
	}
	if label := <-got; !strings.Contains(label, "harness's own state") {
		t.Fatalf("Privileged = %q, want the harness-state label", label)
	}
}

// The fallback workspace under DataDir is still not state.
func TestRequestAdditionalAllowedDir_FallbackWorkspaceNotState(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dataDir := filepath.Join(home, ".kenaz", "harness", "prod")
	ws := filepath.Join(dataDir, "agent-workspace")
	if err := os.MkdirAll(filepath.Join(ws, "proj"), 0o700); err != nil {
		t.Fatal(err)
	}
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
	api := New(Config{
		Catalog:        fsCatalogMinimal(),
		Enabled:        installFSRecipe([]string{t.TempDir()}),
		Pool:           newFakePool(),
		Secrets:        secrets.NewMemoryBackend(),
		DataDir:        dataDir,
		WorkspaceDir:   ws,
		PromptRegistry: reg,
	})
	_, _, err := api.RequestAdditionalAllowedDir(context.Background(), "filesystem", filepath.Join(ws, "proj"), "workaround")
	if errors.Is(err, ErrPrivilegedDir) {
		t.Fatalf("fallback workspace path refused as state: %v", err)
	}
	if label := <-got; label != "" {
		t.Fatalf("Privileged = %q, want none for the fallback workspace", label)
	}
}
