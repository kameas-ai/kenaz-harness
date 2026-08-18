package rpc

import (
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// There are now three copies of the approval concept in this codebase: the
// cedar registry, the served (:7880) permission modal's wire, and the :7881
// approval wire. They share the approval_id, and they share this projection —
// those two facts are the only thing keeping them from drifting into naming
// the same approval differently on two surfaces the same operator is looking
// at.
//
// This test pins FlatPermissionRequest to cedar.PendingRequest.Project(). If
// someone reintroduces a private flattening switch in core/rpc, this fails.
func TestFlattenPendingRequest_ProjectsFromCedar(t *testing.T) {
	t.Parallel()
	issued := time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC)
	surfaces := []struct {
		name    string
		surface cedar.PromptSurface
	}{
		{"bash with argv", cedar.PromptSurface{Bash: &cedar.BashPromptSurface{
			Pattern: "git *", Argv: []string{"git", "push", "--force"}, Dangerous: true,
		}}},
		{"bash without argv", cedar.PromptSurface{Bash: &cedar.BashPromptSurface{Pattern: "ls *"}}},
		{"fs", cedar.PromptSurface{FS: &cedar.FSPromptSurface{
			Op: "write", CanonicalPath: "/tmp/out.txt", Dangerous: true,
		}}},
		{"fs path only", cedar.PromptSurface{FS: &cedar.FSPromptSurface{CanonicalPath: "/tmp/out.txt"}}},
		{"cred", cedar.PromptSurface{Cred: &cedar.CredPromptSurface{
			ProviderID: "anthropic", Purpose: "chat",
		}}},
		{"cred provider only", cedar.PromptSurface{Cred: &cedar.CredPromptSurface{ProviderID: "openai"}}},
		{"tool", cedar.PromptSurface{Tool: &cedar.ToolPromptSurface{
			ServerName: "filesystem", ToolName: "read_file",
		}}},
		{"tool without server", cedar.PromptSurface{Tool: &cedar.ToolPromptSurface{ToolName: "Bash"}}},
	}

	for _, tc := range surfaces {
		t.Run(tc.name, func(t *testing.T) {
			p := cedar.PendingRequest{
				RequestID:  "rid-000102030405060708090a0b",
				Family:     tc.surface.Family(),
				Surface:    tc.surface,
				IssuedAt:   issued,
				DeadlineAt: issued.Add(5 * time.Minute),
			}
			flat := FlattenPendingRequest(p)
			proj := p.Project()

			if flat.ResourceDisplay != proj.ResourceDisplay {
				t.Errorf("ResourceDisplay: flat=%q projection=%q", flat.ResourceDisplay, proj.ResourceDisplay)
			}
			if flat.ResourceUID != proj.ResourceUID {
				t.Errorf("ResourceUID: flat=%q projection=%q", flat.ResourceUID, proj.ResourceUID)
			}
			if flat.Op != proj.Op {
				t.Errorf("Op: flat=%q projection=%q", flat.Op, proj.Op)
			}
			if flat.DangerousTier != proj.Dangerous {
				t.Errorf("Dangerous: flat=%v projection=%v", flat.DangerousTier, proj.Dangerous)
			}
			// The identity fields the two wires correlate on.
			if flat.RequestID != p.RequestID {
				t.Errorf("RequestID rewritten to %q", flat.RequestID)
			}
			if flat.Family != string(p.Family) {
				t.Errorf("Family = %q; want %q", flat.Family, p.Family)
			}
		})
	}
}

// PromptRegistry is the accessor cmd/harness-vm uses to attach the :7881
// bridge to the process's existing gate. A nil-safe accessor matters: the
// in-VM read surface bootstraps best-effort, and a chassis that failed to come
// up must yield "no gate" rather than a panic during connection setup.
func TestPromptRegistry_NilSafe(t *testing.T) {
	t.Parallel()
	var a *API
	if got := a.PromptRegistry(); got != nil {
		t.Fatalf("nil API returned a registry: %v", got)
	}
	if got := (&API{}).PromptRegistry(); got != nil {
		t.Fatalf("zero API returned a registry: %v", got)
	}
}
