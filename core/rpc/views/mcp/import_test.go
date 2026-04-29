package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/mcp/recipes"
)

type stubCatalog struct {
	rs []recipes.Recipe
}

func (s stubCatalog) Recipes() []recipes.Recipe { return s.rs }

func newImportAPI(t *testing.T, dataDir string, cat MergedCatalogReader) *ImportAPI {
	t.Helper()
	return NewImportAPI(ImportConfig{
		Catalog: cat,
		DataDir: func() string { return dataDir },
	})
}

func TestImportRPC_DryRun_NoWrites(t *testing.T) {
	dir := t.TempDir()
	api := newImportAPI(t, dir, stubCatalog{})

	payload := `{"mcpServers":{"foo":{"command":"npx","args":["x"]}}}`
	resp, err := api.ImportClaudeDesktopConfig(context.Background(), ImportRequest{
		RawJSON: payload,
		DryRun:  true,
	})
	if err != nil {
		t.Fatalf("ImportClaudeDesktopConfig: %v", err)
	}
	if resp.Report.KeptCount != 1 {
		t.Errorf("KeptCount = %d, want 1", resp.Report.KeptCount)
	}
	if len(resp.WrotePaths) != 0 {
		t.Errorf("dryRun wrote %d files, want 0", len(resp.WrotePaths))
	}
	// _imports dir must NOT exist post-dryRun.
	importsDir := filepath.Join(dir, "mcp", "recipes", "_imports")
	if _, err := os.Stat(importsDir); err == nil {
		// Allow the dir to exist (a different test could have created
		// it) but it must be empty.
		entries, _ := os.ReadDir(importsDir)
		if len(entries) > 0 {
			t.Errorf("dryRun left %d files under %s", len(entries), importsDir)
		}
	}
}

func TestImportRPC_NotDryRun_WritesArtifacts(t *testing.T) {
	dir := t.TempDir()
	api := newImportAPI(t, dir, stubCatalog{})

	payload := `{"mcpServers":{
		"foo":{"command":"npx","args":["a"]},
		"bar":{"command":"uvx","args":["b"],"env":{"TOKEN":"x"}}
	}}`
	resp, err := api.ImportClaudeDesktopConfig(context.Background(), ImportRequest{
		RawJSON: payload,
		DryRun:  false,
	})
	if err != nil {
		t.Fatalf("ImportClaudeDesktopConfig: %v", err)
	}
	if resp.Report.KeptCount != 2 {
		t.Errorf("KeptCount = %d, want 2", resp.Report.KeptCount)
	}
	if len(resp.WrotePaths) != 2 {
		t.Fatalf("WrotePaths = %d, want 2", len(resp.WrotePaths))
	}
	for _, p := range resp.WrotePaths {
		if _, err := os.Stat(p.YAMLPath); err != nil {
			t.Errorf("yaml not on disk: %v", err)
		}
		if _, err := os.Stat(p.JSONPath); err != nil {
			t.Errorf("json not on disk: %v", err)
		}
	}
}

func TestImportRPC_KeepIDsFilter(t *testing.T) {
	dir := t.TempDir()
	api := newImportAPI(t, dir, stubCatalog{})

	payload := `{"mcpServers":{
		"foo":{"command":"npx","args":["a"]},
		"bar":{"command":"uvx","args":["b"]},
		"baz":{"command":"node","args":["c"]}
	}}`
	resp, err := api.ImportClaudeDesktopConfig(context.Background(), ImportRequest{
		RawJSON: payload,
		DryRun:  false,
		KeepIDs: []string{"bar"},
	})
	if err != nil {
		t.Fatalf("ImportClaudeDesktopConfig: %v", err)
	}
	if len(resp.WrotePaths) != 1 {
		t.Fatalf("WrotePaths = %d, want 1", len(resp.WrotePaths))
	}
	if resp.WrotePaths[0].ID != "bar" {
		t.Errorf("WrotePaths[0].ID = %q, want bar", resp.WrotePaths[0].ID)
	}
}

func TestImportRPC_CollisionStillWrites(t *testing.T) {
	// Per WP08 acceptance: id collision is "kept_with_warning", not
	// auto-shadowed-out. The user opted in, the file gets written,
	// MergedCatalog precedence handles which wins at runtime.
	dir := t.TempDir()
	cat := stubCatalog{rs: []recipes.Recipe{
		{ID: "filesystem", Command: []string{"x"}, Source: recipes.SourceShipped},
	}}
	api := newImportAPI(t, dir, cat)

	payload := `{"mcpServers":{"filesystem":{"command":"my-fs","args":["x"]}}}`
	resp, err := api.ImportClaudeDesktopConfig(context.Background(), ImportRequest{
		RawJSON: payload,
		DryRun:  false,
	})
	if err != nil {
		t.Fatalf("ImportClaudeDesktopConfig: %v", err)
	}
	if resp.Report.CollisionCount != 1 {
		t.Errorf("CollisionCount = %d, want 1", resp.Report.CollisionCount)
	}
	if len(resp.WrotePaths) != 1 {
		t.Errorf("collision should still write, got %d paths", len(resp.WrotePaths))
	}
}

func TestImportRPC_UnsupportedNotWritten(t *testing.T) {
	dir := t.TempDir()
	api := newImportAPI(t, dir, stubCatalog{})

	payload := `{"mcpServers":{
		"good":{"command":"npx","args":["a"]},
		"http-server":{"type":"http","url":"https://x"}
	}}`
	resp, err := api.ImportClaudeDesktopConfig(context.Background(), ImportRequest{
		RawJSON: payload,
		DryRun:  false,
	})
	if err != nil {
		t.Fatalf("ImportClaudeDesktopConfig: %v", err)
	}
	if len(resp.WrotePaths) != 1 {
		t.Errorf("WrotePaths = %d, want 1 (good only)", len(resp.WrotePaths))
	}
	if resp.WrotePaths[0].ID != "good" {
		t.Errorf("wrote unexpected id %q", resp.WrotePaths[0].ID)
	}
}

func TestImportRPC_PayloadCapBubblesUp(t *testing.T) {
	dir := t.TempDir()
	api := newImportAPI(t, dir, stubCatalog{})

	huge := strings.Repeat("A", recipes.MaxImportPayloadBytes+1024)
	payload := `{"mcpServers":{"x":{"command":"npx","args":["` + huge + `"]}}}`
	_, err := api.ImportClaudeDesktopConfig(context.Background(), ImportRequest{
		RawJSON: payload,
		DryRun:  true,
	})
	if !errors.Is(err, recipes.ErrImportPayloadTooLarge) {
		t.Errorf("err = %v, want ErrImportPayloadTooLarge", err)
	}
}

func TestImportRPC_NotConfigured(t *testing.T) {
	api := NewImportAPI(ImportConfig{}) // catalog + dataDir both nil
	_, err := api.ImportClaudeDesktopConfig(context.Background(), ImportRequest{RawJSON: "{}"})
	if !errors.Is(err, ErrImportNotConfigured) {
		t.Errorf("err = %v, want ErrImportNotConfigured", err)
	}
}

func TestImportRPC_NoMCPServersKey(t *testing.T) {
	dir := t.TempDir()
	api := newImportAPI(t, dir, stubCatalog{})
	_, err := api.ImportClaudeDesktopConfig(context.Background(), ImportRequest{
		RawJSON: `{"foo":"bar"}`,
		DryRun:  true,
	})
	if !errors.Is(err, recipes.ErrImportMissingMCPServers) {
		t.Errorf("err = %v, want ErrImportMissingMCPServers", err)
	}
}

func TestImportRPC_RoundTripIntoUserStore(t *testing.T) {
	dir := t.TempDir()
	api := newImportAPI(t, dir, stubCatalog{})

	payload := `{"mcpServers":{"my-imported":{"command":"npx","args":["x"]}}}`
	if _, err := api.ImportClaudeDesktopConfig(context.Background(), ImportRequest{
		RawJSON: payload,
		DryRun:  false,
	}); err != nil {
		t.Fatalf("ImportClaudeDesktopConfig: %v", err)
	}

	store := recipes.NewUserStore(dir, nil)
	got, err := store.Load()
	if err != nil {
		t.Fatalf("UserStore.Load: %v", err)
	}
	var found bool
	for _, r := range got {
		if r.ID == "my-imported" {
			found = true
			if r.Source != recipes.SourceImported {
				t.Errorf("Source = %q, want %q", r.Source, recipes.SourceImported)
			}
		}
	}
	if !found {
		t.Errorf("imported recipe not found via UserStore (loaded %d)", len(got))
	}
}
