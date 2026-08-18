package rpc

// api_mcp_user_source_test.go — mcp-connector-lifecycle-01PMMC01 WP03/FR-003.
//
// Before WP03, core/rpc/api.go wired every mergedRecipeCatalog() consumer
// (the chassis mcpAPI catalog, the import-collision reader, the boot-time
// recipe bootstrap, and tools.Config.Catalog — what Tools_ListRecipes
// reads) with a hardcoded `nil` user source. A paste-config import really
// did write to <DataDir>/mcp/recipes/_imports/, but nothing in the desktop
// process ever read it back: the Tools list had a nil user source in both
// modes, per spec §1.2.
//
// These tests boot the REAL chassis (rpc.New over a real Core + a real
// on-disk DataDir — blind spot 1 from CLAUDE.md: a fixture built on a
// *recipes.Catalog literal or an in-memory store would prove nothing about
// this wiring) and drive the import through the same RPC surface the
// frontend calls, then assert the SAME process sees it without a restart.
import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
	mcpview "github.com/kameas-ai/kenaz-harness/core/rpc/views/mcp"
)

// twoServerImportPayload is a minimal Claude-desktop mcpServers config
// with two entries, used by every test in this file.
const twoServerImportPayload = `{"mcpServers":{
	"wp03-alpha":{"command":"wp03-alpha-cmd","args":["x"]},
	"wp03-beta":{"command":"wp03-beta-cmd","args":["y"]}
}}`

// TestMCPUserSource_ImportThenListRecipesSeesIt is AC-003's desktop half
// (spec §9): import a two-server config with dry_run:false, then — WITHOUT
// restarting the process — assert Tools_ListRecipes (via api.Tools(),
// exactly what the Tools_ListRecipes Wails binding calls) returns both
// imported ids.
//
// Mutation: in newToolsAPI's Config{Catalog: ...} construction, revert
// mcpLiveCatalog{userSource: userSource} to a bare
// mergedRecipeCatalog(nil) snapshot (i.e. drop live per-call re-resolution
// or the userSource argument). This assertion must fail — a *recipes.Catalog
// snapshotted once at boot, before the import ran, can never contain ids
// written after construction.
func TestMCPUserSource_ImportThenListRecipesSeesIt(t *testing.T) {
	sandboxUserConfigDir(t)
	dataDir := t.TempDir()
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c)

	ctx := context.Background()
	imp := api.MCPImport()
	if imp == nil {
		t.Fatalf("api.MCPImport() is nil — import surface not wired for a real DataDir")
	}
	resp, err := imp.ImportClaudeDesktopConfig(ctx, mcpview.ImportRequest{
		RawJSON: twoServerImportPayload,
		DryRun:  false,
	})
	if err != nil {
		t.Fatalf("ImportClaudeDesktopConfig: %v", err)
	}
	if len(resp.WrotePaths) != 2 {
		t.Fatalf("WrotePaths = %d, want 2 (got report %+v)", len(resp.WrotePaths), resp.Report)
	}

	listed, err := api.Tools().ListRecipes(ctx)
	if err != nil {
		t.Fatalf("Tools().ListRecipes: %v", err)
	}
	got := make(map[string]bool, len(listed))
	for _, l := range listed {
		got[l.Recipe.ID] = true
	}
	for _, want := range []string{"wp03-alpha", "wp03-beta"} {
		if !got[want] {
			t.Errorf("Tools_ListRecipes (same process, no restart) missing imported id %q; got ids: %v", want, keysOf(got))
		}
	}
}

// TestMCPUserSource_ImportCollisionSeesPriorImport is AC-005: importing the
// same id twice in one process must report collision_warning on the second
// attempt — proving importCatalogReader resolves through a catalog that
// includes what THIS process already wrote, not just shipped+registry.
//
// Mutation: revert the `userSource:` field on the importCatalogReader{}
// literal at api.go's mcpImportAPI construction site back to unset (zero
// value nil). This assertion must fail — importCatalogReader.Recipes()
// would then only ever see shipped+registry, so a second import of the
// same id would report `kept` again instead of `collision_warning`.
func TestMCPUserSource_ImportCollisionSeesPriorImport(t *testing.T) {
	sandboxUserConfigDir(t)
	dataDir := t.TempDir()
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c)
	ctx := context.Background()
	imp := api.MCPImport()
	if imp == nil {
		t.Fatalf("api.MCPImport() is nil")
	}

	payload := `{"mcpServers":{"wp03-dupe":{"command":"wp03-dupe-cmd","args":["x"]}}}`

	if _, err := imp.ImportClaudeDesktopConfig(ctx, mcpview.ImportRequest{
		RawJSON: payload,
		DryRun:  false,
	}); err != nil {
		t.Fatalf("first import: %v", err)
	}

	// Second import of the SAME id, dry-run this time (mirrors the
	// frontend's "translate" preview step) — must see the first import as
	// a collision, not report it as freshly kept.
	resp, err := imp.ImportClaudeDesktopConfig(ctx, mcpview.ImportRequest{
		RawJSON: payload,
		DryRun:  true,
	})
	if err != nil {
		t.Fatalf("second (dry-run) import: %v", err)
	}
	if len(resp.Report.Entries) != 1 {
		t.Fatalf("Entries = %d, want 1", len(resp.Report.Entries))
	}
	entry := resp.Report.Entries[0]
	if entry.ID != "wp03-dupe" {
		t.Fatalf("entry.ID = %q, want wp03-dupe", entry.ID)
	}
	if entry.Status != recipes.ImportStatusCollisionWarning {
		t.Errorf("entry.Status = %q, want %q (the import collision reader did not see the prior import)",
			entry.Status, recipes.ImportStatusCollisionWarning)
	}
	if resp.Report.CollisionCount != 1 {
		t.Errorf("CollisionCount = %d, want 1", resp.Report.CollisionCount)
	}
}

// TestMCPUserSource_BootstrapAndToolsListAgree is the plan.md risk this WP
// exists to close: "mergedRecipeCatalog()'s three call sites drift apart."
// It drives BOTH consumers through their real production call sites in the
// same process — the boot-time recipe bootstrap (via c.Start, which invokes
// exactly the closure New() wired with c.SetMCPRecipeBootstrap) and the
// Tools view (via api.Tools().ListRecipes) — and requires both to agree
// that an imported-then-enabled recipe id exists in the catalog.
//
// The bootstrap's only observable signal for "catalog.Get found the entry"
// vs. "catalog.Get missed it" is a log line (rpc.mcp_bootstrap.unknown_recipe
// on miss) — the imported recipe's command does not exist on this test
// machine, so pool.Open legitimately fails downstream of a *successful*
// catalog lookup, and that failure is logged under a DIFFERENT key
// (rpc.mcp_bootstrap.partial_open). Asserting on the log line — not a
// spawn outcome — isolates exactly the catalog-resolution behaviour this
// WP changed. Per CLAUDE.md's "assert the exit code / decision, not a log
// line" caution: this is the one case where the log line genuinely is the
// only observable the production code emits for a catalog miss; the test
// still additionally asserts the Tools-side membership through a real API
// call, not a log line, as the second half of the agreement check.
//
// Mutation A (fix only the tools site): revert mcpLiveCatalog back to a
// boot-time mergedRecipeCatalog(nil) snapshot as in the test above. The
// Tools_ListRecipes-side assertion below fails.
// Mutation B (fix only the bootstrap site): drop
// mcpUserRecipeSource(a.mcpUserStore) from the
// c.SetMCPRecipeBootstrap(makeMCPRecipeBootstrap(...)) call in New(). The
// log-absence assertion below fails (unknown_recipe fires).
func TestMCPUserSource_BootstrapAndToolsListAgree(t *testing.T) {
	sandboxUserConfigDir(t)
	dataDir := t.TempDir()
	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c)
	ctx := context.Background()

	const recipeID = "wp03-bootstrap-agree"
	payload := `{"mcpServers":{"` + recipeID + `":{"command":"wp03-bootstrap-agree-cmd","args":["x"]}}}`
	imp := api.MCPImport()
	if imp == nil {
		t.Fatalf("api.MCPImport() is nil")
	}
	if _, err := imp.ImportClaudeDesktopConfig(ctx, mcpview.ImportRequest{
		RawJSON: payload,
		DryRun:  false,
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	// Enable the imported recipe so the boot bootstrap actually attempts
	// to resolve + spawn it, the same way a user's prior "Install" click
	// would have persisted it.
	enabled := &recipes.EnabledRecipes{}
	enabled.Add(recipes.EnabledRecipe{ID: recipeID, EnabledAt: time.Now().UTC()})
	if err := enabled.Save(dataDir); err != nil {
		t.Fatalf("save enabled recipes: %v", err)
	}

	logs := captureLog(t, func() {
		startCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// Start's mcpRecipeBootstrap failures are non-fatal by design
		// (FR-030 — a single bad spec must not break boot); the
		// nonexistent "wp03-bootstrap-agree-cmd" binary WILL fail to
		// spawn. That is expected and is not what this test asserts on.
		_ = c.Start(startCtx)
	})

	if strings.Contains(logs, "rpc.mcp_bootstrap.unknown_recipe") && strings.Contains(logs, recipeID) {
		t.Errorf("bootstrap logged unknown_recipe for %q — its merged catalog did not see the import "+
			"(bootstrap and tools list disagree about what the catalog contains)\nlogs:\n%s", recipeID, logs)
	}

	listed, err := api.Tools().ListRecipes(ctx)
	if err != nil {
		t.Fatalf("Tools().ListRecipes: %v", err)
	}
	var foundInTools bool
	for _, l := range listed {
		if l.Recipe.ID == recipeID {
			foundInTools = true
			break
		}
	}
	if !foundInTools {
		t.Errorf("Tools_ListRecipes does not contain %q — the tools-view catalog did not see the import", recipeID)
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
