package cedar

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func newDiskEngine(t *testing.T, files map[string]string) (*Engine, string) {
	t.Helper()
	dir := t.TempDir()
	if len(files) > 0 {
		pd := filepath.Join(dir, PolicyDir)
		if err := os.MkdirAll(pd, 0o755); err != nil {
			t.Fatal(err)
		}
		for n, s := range files {
			if err := os.WriteFile(filepath.Join(pd, n), []byte(s), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	e, err := NewEngine(Options{DataDir: dir, LoadFromDisk: true, IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e, dir
}

func TestFailClosed_HealthyEngine_Delegates(t *testing.T) {
	e, _ := newDiskEngine(t, nil)
	if err := e.UserPolicyLoadError(); err != nil {
		t.Fatalf("healthy engine reports load error: %v", err)
	}
	g := FailClosedOnLoadError(e, nil)
	if err := CheckFileWrite(context.Background(), g, "/tmp/x"); err != nil {
		t.Fatalf("healthy engine denied file_write: %v", err)
	}
}

func TestFailClosed_CorruptUserPolicy_DeniesGuardedActions(t *testing.T) {
	e, dir := newDiskEngine(t, map[string]string{"bad.cedar": "forbid ( {{{"})
	if e.UserPolicyLoadError() == nil {
		t.Fatal("UserPolicyLoadError nil with a corrupt file")
	}
	ctx := context.Background()
	g := FailClosedOnLoadError(e, nil)

	// Raw engine stays fail-open (documented posture for other sites).
	if err := CheckFileWrite(ctx, e, "/tmp/x"); err != nil {
		t.Fatalf("raw engine changed posture: %v", err)
	}

	for name, err := range map[string]error{
		"file_write":  CheckFileWrite(ctx, g, "/tmp/x"),
		"file_read":   CheckFileRead(ctx, g, "/tmp/x"),
		"state_write": CheckStateWrite(ctx, g, "file"),
		"tool_exec":   CheckTool(ctx, g, "kenaz", "bash"),
		"use_tool":    CheckUseTool(ctx, g, "kenaz", "bash"),
	} {
		if !IsPolicyDenied(err) {
			t.Errorf("%s not denied under corrupt policy: %v", name, err)
		}
	}
	// Non-guarded actions still delegate to the engine.
	if err := CheckMemoryWrite(ctx, g, "global"); err != nil {
		t.Errorf("memory_write (not guarded) denied: %v", err)
	}

	found := false
	for _, d := range e.RecentDecisions(50) {
		if d.Outcome == Deny && d.MatchedPolicy == "fail-closed/policy-load-error" {
			found = true
		}
	}
	if !found {
		t.Fatal("fail-closed denial not recorded in decision log")
	}

	// Fix + reload lifts it.
	if err := os.WriteFile(filepath.Join(dir, PolicyDir, "bad.cedar"), []byte("// ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if err := CheckFileWrite(ctx, g, "/tmp/x"); err != nil {
		t.Fatalf("still denied after fix: %v", err)
	}
}

func TestFailClosed_NilEngine(t *testing.T) {
	ctx := context.Background()
	if _, ok := FailClosedOnLoadError(nil, nil).(AllowAll); !ok {
		t.Fatal("no engine + no error must be AllowAll (absence, not corruption)")
	}
	g := FailClosedOnLoadError(nil, errors.New("boom"))
	if !IsPolicyDenied(CheckFileWrite(ctx, g, "/tmp/x")) {
		t.Fatal("boot failure did not deny file_write")
	}
	if err := CheckMemoryWrite(ctx, g, "global"); err != nil {
		t.Fatalf("boot failure denied an unguarded action: %v", err)
	}
}
