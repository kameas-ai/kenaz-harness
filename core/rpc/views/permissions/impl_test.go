package permissions

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// fakeEngine counts Reload calls so tests can assert revoke triggers
// the hot-reload.
type fakeEngine struct {
	reloads atomic.Int32
	err     error
}

func (f *fakeEngine) Reload(_ context.Context) error {
	f.reloads.Add(1)
	return f.err
}

func newAPIForTest(t *testing.T) (*API, *cedar.Registry, string, *fakeEngine) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, cedar.PolicyDir), 0o755); err != nil {
		t.Fatalf("mkdir policy dir: %v", err)
	}
	reg := cedar.NewRegistry()
	eng := &fakeEngine{}
	api := New(Config{
		DataDir:  dir,
		Registry: reg,
		Engine:   eng,
	})
	return api, reg, dir, eng
}

func writePolicy(t *testing.T, dir, name string) {
	t.Helper()
	body := []byte(`// auto-generated; revoke via Settings
permit (principal, action, resource);
`)
	if err := os.WriteFile(filepath.Join(dir, cedar.PolicyDir, name), body, 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// TestResolve_RoutesToRegistry — Resolve must round-trip through the
// registry, allowing a separate goroutine's RequestInteractive to
// return.
func TestResolve_RoutesToRegistry(t *testing.T) {
	t.Parallel()
	disp := newRecordingDispatcher()
	reg := cedar.NewRegistry(cedar.WithDispatcher(disp))
	api := New(Config{Registry: reg})

	done := make(chan cedar.Resolution, 1)
	go func() {
		res, _ := reg.RequestInteractive(context.Background(), cedar.PromptSurface{
			Bash: &cedar.BashPromptSurface{Pattern: "git status"},
		})
		done <- res
	}()

	// Wait for the dispatcher to record an emit.
	select {
	case <-disp.fired:
	case <-time.After(time.Second):
		t.Fatal("dispatcher never received emit")
	}
	id := disp.lastID()

	if err := api.Resolve(context.Background(), id, DecisionAllowAlways); err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	select {
	case res := <-done:
		if res.Decision != DecisionAllowAlways {
			t.Fatalf("decision = %q, want %q", res.Decision, DecisionAllowAlways)
		}
	case <-time.After(time.Second):
		t.Fatal("RequestInteractive never returned")
	}
}

// TestResolve_RegistryUnwired — without a registry the call returns
// ErrRegistryUnavailable.
func TestResolve_RegistryUnwired(t *testing.T) {
	t.Parallel()
	api := New(Config{})
	err := api.Resolve(context.Background(), "rid-x", DecisionAllowAlways)
	if !errors.Is(err, ErrRegistryUnavailable) {
		t.Fatalf("err = %v, want ErrRegistryUnavailable", err)
	}
}

// TestListGrants_PersistedAndTransient — both kinds appear in the
// returned list with the right family discriminator.
func TestListGrants_PersistedAndTransient(t *testing.T) {
	t.Parallel()
	api, reg, dir, _ := newAPIForTest(t)

	writePolicy(t, dir, "bash_allow_git_status.cedar")
	writePolicy(t, dir, "fs_allow_read_tmp.cedar")
	// Off-convention file MUST NOT appear in the grants list.
	writePolicy(t, dir, "custom_user_policy.cedar")

	// Seed a transient grant by simulating a full prompt round-trip.
	disp := newRecordingDispatcher()
	reg = cedar.NewRegistry(cedar.WithDispatcher(disp))
	api.registry = reg
	go func() {
		<-disp.fired
		_ = reg.Resolve(disp.lastID(), DecisionAllowOnce)
	}()
	if _, err := reg.RequestInteractive(context.Background(), cedar.PromptSurface{
		Tool: &cedar.ToolPromptSurface{ToolName: "websearch", ServerName: "builtin"},
	}); err != nil {
		t.Fatalf("RequestInteractive: %v", err)
	}

	grants, err := api.ListGrants(context.Background(), "")
	if err != nil {
		t.Fatalf("ListGrants: %v", err)
	}

	// Expected: bash file, fs file, tool transient. Custom policy
	// dropped.
	got := make(map[string]Grant)
	for _, g := range grants {
		got[g.ID] = g
	}
	if _, ok := got["bash_allow_git_status.cedar"]; !ok {
		t.Errorf("missing bash grant; got %v", names(grants))
	}
	if _, ok := got["fs_allow_read_tmp.cedar"]; !ok {
		t.Errorf("missing fs grant; got %v", names(grants))
	}
	if _, ok := got["custom_user_policy.cedar"]; ok {
		t.Errorf("off-convention file should be filtered; got %v", names(grants))
	}
	transientFound := false
	for _, g := range grants {
		if g.Transient {
			transientFound = true
			if g.Family != "tool" {
				t.Errorf("transient family = %q, want tool", g.Family)
			}
		}
	}
	if !transientFound {
		t.Errorf("transient grant absent; got %v", names(grants))
	}

	// Persisted-first ordering: every transient should come after
	// every persisted entry.
	for i, g := range grants {
		if g.Transient {
			for _, prior := range grants[:i] {
				if prior.Transient {
					continue
				}
				_ = prior // OK
			}
			for _, after := range grants[i:] {
				if !after.Transient {
					t.Fatalf("ordering violated: persisted after transient at %d (%v)", i, names(grants))
				}
			}
			break
		}
	}
}

// TestRevokeGrant_DeletesFileAndReloads — revoking a persisted grant
// removes the file, reloads the engine, and clears transient cache.
func TestRevokeGrant_DeletesFileAndReloads(t *testing.T) {
	t.Parallel()
	api, reg, dir, eng := newAPIForTest(t)

	writePolicy(t, dir, "bash_allow_git_status.cedar")

	// Seed a transient grant so we can assert it is cleared on revoke.
	disp := newRecordingDispatcher()
	reg2 := cedar.NewRegistry(cedar.WithDispatcher(disp))
	api.registry = reg2
	go func() { <-disp.fired; _ = reg2.Resolve(disp.lastID(), DecisionAllowOnce) }()
	_, _ = reg2.RequestInteractive(context.Background(), cedar.PromptSurface{
		Bash: &cedar.BashPromptSurface{Pattern: "ls"},
	})
	if reg2.TransientGrantCount() != 1 {
		t.Fatalf("seed transient: count = %d, want 1", reg2.TransientGrantCount())
	}

	if err := api.RevokeGrant(context.Background(), "bash_allow_git_status.cedar"); err != nil {
		t.Fatalf("RevokeGrant: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, cedar.PolicyDir, "bash_allow_git_status.cedar")); !os.IsNotExist(err) {
		t.Fatalf("file still present: %v", err)
	}
	if eng.reloads.Load() != 1 {
		t.Fatalf("engine reloads = %d, want 1", eng.reloads.Load())
	}
	if reg2.TransientGrantCount() != 0 {
		t.Fatalf("transient cache not cleared: %d", reg2.TransientGrantCount())
	}
	_ = reg
}

// TestRevokeGrant_PathTraversalRejected — every path-traversal-shaped
// id must fail with ErrInvalidGrantID, never touching the FS.
func TestRevokeGrant_PathTraversalRejected(t *testing.T) {
	t.Parallel()
	api, _, _, _ := newAPIForTest(t)

	cases := []string{
		"../escape.cedar",
		"sub/file.cedar",
		`bash_allow\..\..cedar`,
		"bash_allow_..\\evil.cedar",
		"random.txt",                  // wrong extension
		"unknown_allow_x.cedar",       // unknown family prefix
		"",                             // empty
	}
	for _, c := range cases {
		err := api.RevokeGrant(context.Background(), c)
		if err == nil {
			t.Errorf("RevokeGrant(%q) succeeded, want error", c)
		}
	}
}

// TestRevokeGrant_TransientResourceKey — passing a transient resource
// key clears the cache and returns nil.
func TestRevokeGrant_TransientResourceKey(t *testing.T) {
	t.Parallel()
	api, _, _, _ := newAPIForTest(t)
	disp := newRecordingDispatcher()
	reg := cedar.NewRegistry(cedar.WithDispatcher(disp))
	api.registry = reg
	go func() { <-disp.fired; _ = reg.Resolve(disp.lastID(), DecisionAllowOnce) }()
	_, _ = reg.RequestInteractive(context.Background(), cedar.PromptSurface{
		Bash: &cedar.BashPromptSurface{Pattern: "ls"},
	})
	if reg.TransientGrantCount() != 1 {
		t.Fatalf("seed: count = %d", reg.TransientGrantCount())
	}
	if err := api.RevokeGrant(context.Background(), "bash::ls"); err != nil {
		t.Fatalf("RevokeGrant transient: %v", err)
	}
	if reg.TransientGrantCount() != 0 {
		t.Fatalf("transient count = %d, want 0", reg.TransientGrantCount())
	}
}

// TestListPending_RoundTrip — ListPending exposes in-flight prompts.
func TestListPending_RoundTrip(t *testing.T) {
	t.Parallel()
	disp := newRecordingDispatcher()
	reg := cedar.NewRegistry(cedar.WithDispatcher(disp), cedar.WithTimeout(time.Hour))
	api := New(Config{Registry: reg})

	go func() {
		_, _ = reg.RequestInteractive(context.Background(), cedar.PromptSurface{
			Cred: &cedar.CredPromptSurface{ProviderID: "openai", Purpose: "provider_call"},
		})
	}()
	select {
	case <-disp.fired:
	case <-time.After(time.Second):
		t.Fatal("dispatcher never received emit")
	}

	pending, err := api.ListPending(context.Background())
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("len(pending) = %d, want 1", len(pending))
	}
	if pending[0].Family != cedar.FamilyCredential {
		t.Fatalf("family = %q, want %q", pending[0].Family, cedar.FamilyCredential)
	}

	// Clean up so the test exits cleanly.
	_ = reg.Resolve(pending[0].RequestID, DecisionDeny)
}

// recordingDispatcher captures the latest emit's request id for tests.
// Local copy — the prompt_test.go variant lives in the cedar package
// and is not exported, so we re-implement the minimal surface here.
type recordingDispatcher struct {
	fired chan struct{}
	idCh  chan string
}

func newRecordingDispatcher() *recordingDispatcher {
	return &recordingDispatcher{
		fired: make(chan struct{}, 16),
		idCh:  make(chan string, 16),
	}
}

func (r *recordingDispatcher) Dispatch(_ context.Context, _ string, payload cedar.PendingRequest) {
	select {
	case r.idCh <- payload.RequestID:
	default:
	}
	select {
	case r.fired <- struct{}{}:
	default:
	}
}

// lastID returns the id of the most recent emit. Drain semantics: the
// returned id is consumed; tests that care about ordering must call
// lastID once per fired signal.
func (r *recordingDispatcher) lastID() string {
	select {
	case id := <-r.idCh:
		return id
	case <-time.After(time.Second):
		return ""
	}
}

func names(gs []Grant) []string {
	out := make([]string, 0, len(gs))
	for _, g := range gs {
		out = append(out, g.ID)
	}
	sort.Strings(out)
	return out
}

// ---- configTrimmer tests -------------------------------------------------

// spyTrimmer records TrimAllowedDir calls so tests can assert the trimmer
// was (or was not) invoked with the expected arguments.
type spyTrimmer struct {
	calls []trimCall
}

type trimCall struct {
	recipeID string
	path     string
}

func (s *spyTrimmer) TrimAllowedDir(_ context.Context, recipeID, path string) {
	s.calls = append(s.calls, trimCall{recipeID: recipeID, path: path})
}

// fsRecipeDirSnippetBody returns a Cedar snippet body in the same format
// that tools.writeAllowAlwaysSnippet produces, so tests can write it to
// the policy dir without importing the tools package.
func fsRecipeDirSnippetBody(canonical string) []byte {
	return []byte("permit(\n  principal,\n  action == Action::\"recipe_dir_add\",\n  resource == FilesystemOp::\"" + canonical + "\"\n);\n")
}

// writeFSRecipeDirPolicy writes a snippet file and returns its filename.
func writeFSRecipeDirPolicy(t *testing.T, dir, stem, canonical string) string {
	t.Helper()
	name := "fs_allow_recipe_dir_" + stem + ".cedar"
	body := fsRecipeDirSnippetBody(canonical)
	if err := os.WriteFile(filepath.Join(dir, cedar.PolicyDir, name), body, 0o644); err != nil {
		t.Fatalf("writeFSRecipeDirPolicy: %v", err)
	}
	return name
}

// TestRevokeGrant_TrimsRecipeConfig_RecipeDirAddSnippet verifies that
// revoking a "fs_allow_recipe_dir_" grant calls TrimAllowedDir with the
// canonical path embedded in the snippet body and the hardcoded recipe id.
func TestRevokeGrant_TrimsRecipeConfig_RecipeDirAddSnippet(t *testing.T) {
	t.Parallel()
	api, _, dir, _ := newAPIForTest(t)

	const canonical = "/Users/alice/projects"
	grantID := writeFSRecipeDirPolicy(t, dir, "usersaliceprojects", canonical)

	spy := &spyTrimmer{}
	api.SetConfigTrimmer(spy)

	if err := api.RevokeGrant(context.Background(), grantID); err != nil {
		t.Fatalf("RevokeGrant: %v", err)
	}

	if len(spy.calls) != 1 {
		t.Fatalf("TrimAllowedDir call count = %d, want 1", len(spy.calls))
	}
	if spy.calls[0].path != canonical {
		t.Errorf("TrimAllowedDir path = %q, want %q", spy.calls[0].path, canonical)
	}
	if spy.calls[0].recipeID != "filesystem" {
		t.Errorf("TrimAllowedDir recipeID = %q, want \"filesystem\"", spy.calls[0].recipeID)
	}
}

// TestRevokeGrant_NoTrim_NonRecipeDirSnippet verifies that revoking a
// "fs_allow_" grant whose name does NOT start with "fs_allow_recipe_dir_"
// does not invoke TrimAllowedDir (it's a plain read/write/etc. grant, not
// a recipe-dir-add).
func TestRevokeGrant_NoTrim_NonRecipeDirSnippet(t *testing.T) {
	t.Parallel()
	api, _, dir, _ := newAPIForTest(t)

	// fs_allow_read_* is a standard fs family grant, not recipe_dir_add.
	writePolicy(t, dir, "fs_allow_read_tmp.cedar")

	spy := &spyTrimmer{}
	api.SetConfigTrimmer(spy)

	if err := api.RevokeGrant(context.Background(), "fs_allow_read_tmp.cedar"); err != nil {
		t.Fatalf("RevokeGrant: %v", err)
	}

	if len(spy.calls) != 0 {
		t.Errorf("TrimAllowedDir called %d times for non-recipe-dir grant; want 0", len(spy.calls))
	}
}

// TestRevokeGrant_NilTrimmer_StillSucceeds verifies that a nil configTrimmer
// does not cause RevokeGrant to fail. The Cedar-level revocation must
// complete successfully regardless.
func TestRevokeGrant_NilTrimmer_StillSucceeds(t *testing.T) {
	t.Parallel()
	api, _, dir, _ := newAPIForTest(t)

	const canonical = "/tmp/safe"
	grantID := writeFSRecipeDirPolicy(t, dir, "tmpsafe", canonical)
	// configTrimmer is nil (default from newAPIForTest).

	if err := api.RevokeGrant(context.Background(), grantID); err != nil {
		t.Fatalf("RevokeGrant with nil trimmer: %v", err)
	}
	// File must be gone.
	if _, err := os.Stat(filepath.Join(dir, cedar.PolicyDir, grantID)); !os.IsNotExist(err) {
		t.Fatalf("grant file still present after revoke: %v", err)
	}
}

// TestExtractFilesystemOpPath verifies the Cedar body parser extracts the
// path correctly from well-formed and malformed bodies.
func TestExtractFilesystemOpPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "well_formed",
			body: "permit(\n  principal,\n  action == Action::\"recipe_dir_add\",\n  resource == FilesystemOp::\"/Users/alice/docs\"\n);\n",
			want: "/Users/alice/docs",
		},
		{
			name: "no_marker",
			body: `permit(principal, action, resource);`,
			want: "",
		},
		{
			name: "unterminated_quote",
			body: `resource == FilesystemOp::"no closing quote`,
			want: "",
		},
		{
			name: "empty",
			body: "",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractFilesystemOpPath(tc.body)
			if got != tc.want {
				t.Errorf("extractFilesystemOpPath = %q, want %q", got, tc.want)
			}
		})
	}
}
