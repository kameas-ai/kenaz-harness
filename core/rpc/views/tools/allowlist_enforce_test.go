package tools

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
	"github.com/kameas-ai/kenaz-harness/core/mcp/stdio"
)

// runningPool models running servers: CloseOne on a non-running id returns
// stdio.ErrServerNotFound, like the real pool.
type runningPool struct {
	mu      sync.Mutex
	running map[string]bool
	failFor string
	closed  []string
}

func (p *runningPool) OpenOne(_ context.Context, spec coremcp.ServerSpec) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.running[spec.Name] = true
	return nil
}

func (p *runningPool) CloseOne(_ context.Context, id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if id == p.failFor {
		return errors.New("boom")
	}
	if !p.running[id] {
		return stdio.ErrServerNotFound
	}
	delete(p.running, id)
	p.closed = append(p.closed, id)
	return nil
}

func (p *runningPool) RecipeStatus(id string) (stdio.RecipeStatus, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.running[id] {
		return stdio.RecipeStatus{ID: id, State: string(stdio.StateRunning)}, true
	}
	return stdio.RecipeStatus{}, false
}

func (p *runningPool) ServerTools(string) []coremcp.Tool { return nil }

func (p *runningPool) isRunning(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running[id]
}

// fleet-policy push-down: a narrowed fleet allow-list must stop servers
// that are already running, not only gate the next install / boot.
// Not parallel: it sets the process-global allow-list.
func TestStopDisallowedRecipes_StopsRunningBlockedServers(t *testing.T) {
	t.Cleanup(func() { recipes.ApplyFleetAllowlist(nil) })
	enabled := &recipes.EnabledRecipes{}
	for _, id := range []string{"github", "slack", "idle", "stuck"} {
		enabled.Add(recipes.EnabledRecipe{ID: id, EnabledAt: time.Now()})
	}
	pool := &runningPool{running: map[string]bool{"github": true, "slack": true, "stuck": true}, failFor: "stuck"}
	api := New(Config{Catalog: testCatalog("github"), Enabled: enabled, Pool: pool, DataDir: t.TempDir()})

	// No fleet restriction: nothing blocked, nothing stopped.
	if got := api.StopDisallowedRecipes(context.Background()); len(got) != 0 {
		t.Fatalf("unrestricted: got %+v", got)
	}

	recipes.ApplyFleetAllowlist([]string{"github"})
	got := api.StopDisallowedRecipes(context.Background())
	want := map[string]struct {
		stopped bool
		err     bool
	}{"idle": {false, false}, "slack": {true, false}, "stuck": {false, true}}
	if len(got) != len(want) {
		t.Fatalf("results = %+v", got)
	}
	for _, r := range got {
		w, ok := want[r.RecipeID]
		if !ok || r.Stopped != w.stopped || (r.Err != nil) != w.err {
			t.Errorf("result %+v, want %+v", r, w)
		}
	}
	if !pool.isRunning("github") {
		t.Error("allowed server was stopped")
	}
	if pool.isRunning("slack") {
		t.Error("blocked server still running")
	}
	// Policy block, not uninstall: the enabled entry stays.
	if _, ok := enabled.Get("slack"); !ok {
		t.Error("blocked recipe removed from the enabled list")
	}
}
