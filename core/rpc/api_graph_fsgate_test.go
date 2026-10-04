package rpc

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/runposture"
	corefs "github.com/kameas-ai/kenaz-harness/core/tools/fs"
)

// graph-fs-gate-01GFSG01 WP02 — FR-4(a) and the confirmed-roots half of
// FR-1, through the PRODUCTION wiring: a real API over a temp DataDir,
// the real graph manager's env.Policy (the late-bound fs.Gate the fs
// builtin tools use), and a real Kernel running read_file -> write_file
// on an UNATTENDED context (the scheduled/headless posture).
//
// On main before this WP the write_file node consulted only the Cedar
// file_write action, which the embedded default bundle permits for every
// resource, so the unattended run below wrote dst with no prompt and no
// confirmed-roots check — runGraphWrite returned nil and the file existed.

// confirmPath persists the same exact-path grant the fs tool's
// "Allow always" writes (corefs.BuildFilesystemAllowSnippet) — the
// user-confirmed-root mechanism — and reloads the shared engine.
func confirmPath(t *testing.T, api *API, dataDir string, op corefs.Op, path string) {
	t.Helper()
	canonical, err := corefs.Canonicalize(path)
	if err != nil {
		t.Fatalf("Canonicalize: %v", err)
	}
	name, body := corefs.BuildFilesystemAllowSnippet(op, canonical)
	writeRawPolicy(t, dataDir, name, body)
	if err := api.CedarPolicy().ReloadPolicies(context.Background()); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if lerr := api.cedarEngine.UserPolicyLoadError(); lerr != nil {
		t.Fatalf("confirm snippet failed to load: %v", lerr)
	}
}

func runGraphWrite(t *testing.T, api *API, ctx context.Context, src, dst string) (*coreag.Env, error) {
	t.Helper()
	g := &coreag.Graph{
		SpecVersion: coreag.SpecVersion,
		ID:          "graph-fsgate-pin",
		Entrypoints: []string{"rf"},
		Nodes: []coreag.Node{
			{ID: "rf", Kind: coreag.NodeKindReadFile, Attrs: coreag.ReadFileAttrs{Path: src}},
			{ID: "wf", Kind: coreag.NodeKindWriteFile, Attrs: coreag.WriteFileAttrs{Path: dst, Content: "payload"}},
		},
		Edges: []coreag.Edge{
			{From: coreag.EndpointRef{Node: "rf", Port: "result"}, To: coreag.EndpointRef{Node: "wf", Port: "payload"}},
		},
	}
	env := &coreag.Env{RunID: "graph-fsgate-pin-" + filepath.Base(dst), Graph: g, SessionID: "s"}
	api.graphMgr.EnvDefaults()(env)
	err := coreag.NewKernel().Run(ctx, env)
	return env, err
}

func TestGraphWriteFile_UnattendedOutsideConfirmedRoots_Denied(t *testing.T) {
	api, dataDir := hoistSiteAPI(t)
	work := t.TempDir()
	src := filepath.Join(work, "src.txt")
	dst := filepath.Join(work, "dst.txt")
	if err := os.WriteFile(src, []byte("graph payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The user confirmed the SOURCE for reading (same snippet the fs
	// tool's allow-always writes), but never confirmed dst.
	confirmPath(t, api, dataDir, corefs.OpRead, src)

	ctx := runposture.Unattended(context.Background())
	env, err := runGraphWrite(t, api, ctx, src, dst)
	if env.State != nil && !env.State.Completed("rf") {
		t.Fatalf("read_file of a user-confirmed path did not complete (confirmed roots not honoured): run err=%v", err)
	}
	if env.State != nil && env.State.Completed("wf") {
		t.Fatal("unattended graph write_file outside confirmed roots COMPLETED — want denied")
	}
	if _, statErr := os.Stat(dst); statErr == nil {
		t.Fatal("unattended graph write_file outside confirmed roots wrote the file")
	}
	// The denial is the fs gate's: the unattended prompt resolved to deny.
	pol := graphPolicyOf(t, api)
	fg, ok := pol.(coreag.FileAccessGate)
	if !ok {
		t.Fatalf("production graph policy %T does not implement FileAccessGate", pol)
	}
	if derr := fg.AuthorizeFileWrite(ctx, dst); !cedar.IsPolicyDenied(derr) {
		t.Fatalf("AuthorizeFileWrite(unattended, unconfirmed) = %v, want policy denied", derr)
	}
}

func TestGraphWriteFile_UnattendedInsideConfirmedRoot_Allowed(t *testing.T) {
	api, dataDir := hoistSiteAPI(t)
	work := t.TempDir()
	src := filepath.Join(work, "src.txt")
	dst := filepath.Join(work, "dst.txt")
	if err := os.WriteFile(src, []byte("graph payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	confirmPath(t, api, dataDir, corefs.OpRead, src)
	confirmPath(t, api, dataDir, corefs.OpWrite, dst)

	env, err := runGraphWrite(t, api, runposture.Unattended(context.Background()), src, dst)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !env.State.Completed("wf") {
		t.Fatal("write_file to a user-confirmed path did not complete")
	}
	got, rerr := os.ReadFile(dst)
	if rerr != nil || string(got) != "graph payload" {
		t.Fatalf("dst = %q err=%v", got, rerr)
	}
}
