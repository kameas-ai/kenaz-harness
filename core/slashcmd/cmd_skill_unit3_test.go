package slashcmd

// cmd_skill_unit3_test.go — automation-actually-runs-01PMZ404 UNIT-3,
// AC-004. Package slashcmd (not slashcmd_test) because skillCommand is
// unexported.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSkillCommand_KindTool_ReturnsError is AC-004's second site: before
// UNIT-3, skillCommand.Run's KindTool arm returned ResultKindSystem with
// text "/<trigger>: dispatching <tool>" and a nil error — no frontend
// tool-routing branch has ever existed to fulfil that announcement. Now
// it returns ResultKindError and a non-nil error naming the gap.
func TestSkillCommand_KindTool_ReturnsError(t *testing.T) {
	sk := Skill{
		ID:      "zz-unit3-probe",
		Trigger: "unit3probe",
		Kind:    KindTool,
		Tool:    "bash",
	}
	cmd := skillCommand{skill: sk}

	res, err := cmd.Run(context.Background(), Env{}, nil)
	if err == nil {
		t.Fatal("skillCommand.Run for kind:tool returned a nil error — a caller branching on err would treat this as success")
	}
	if res.Kind != ResultKindError {
		t.Errorf("Kind = %q, want error", res.Kind)
	}
	if !strings.Contains(err.Error(), "bash") {
		t.Errorf("err = %v, want it to name the tool %q", err, "bash")
	}
}

// TestNoSlashDispatchLiesRemain is the tasks.md UNIT-3 regression guard:
// grep core/ for the two retired phrases ("would dispatch" and
// "dispatching %s", assembled at runtime so this file's own source can't
// trip its own check) and assert zero hits.
func TestNoSlashDispatchLiesRemain(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("Abs: %v", err)
	}
	coreDir := filepath.Join(root, "core")
	if info, statErr := os.Stat(coreDir); statErr != nil || !info.IsDir() {
		t.Skipf("cannot locate core/ from test working directory (root=%s): %v", root, statErr)
	}

	banned := []string{
		strings.Join([]string{"would ", "dispatch: %s %s"}, ""),
		strings.Join([]string{": dispatching ", "%s\""}, ""),
	}
	var hits []string
	walkErr := filepath.Walk(coreDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		base := filepath.Base(path)
		if base == "cmd_skill_unit3_test.go" || base == "dispatch_test.go" {
			return nil // these files' own comments/history reference the banned phrases
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, b := range banned {
			if strings.Contains(string(data), b) {
				hits = append(hits, path+" :: "+b)
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk core/: %v", walkErr)
	}
	if len(hits) != 0 {
		t.Errorf("found retired dry-run phrasing: %v", hits)
	}
}
