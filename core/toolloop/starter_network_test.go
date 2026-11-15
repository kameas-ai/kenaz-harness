package toolloop

import (
	"context"
	"testing"
)

type fixedResolver struct{ policy ToolPolicy }

func (f fixedResolver) Resolve(_ context.Context, _, server, tool string) (Resolution, error) {
	return Resolution{Server: server, Tool: tool, Policy: f.policy}, nil
}

func TestStarterNetworkResolver(t *testing.T) {
	ctx := context.Background()
	on := true
	r := NewStarterNetworkResolver(fixedResolver{PolicyAutoAllow})

	// Inactive: the inner verdict, unchanged.
	got, _ := r.Resolve(ctx, "s", "kenaz", "web_fetch")
	if got.Policy != PolicyAutoAllow || got.AskDespitePermit {
		t.Fatalf("inactive: got %+v", got)
	}

	r.SetActive(func() bool { return on })
	for _, tool := range []string{"web_fetch", "web_search"} {
		got, _ = r.Resolve(ctx, "s", "kenaz", tool)
		if got.Policy != PolicyConfirmEach || !got.AskDespitePermit || got.Reason == "" {
			t.Fatalf("active %s: got %+v, want confirm_each + AskDespitePermit", tool, got)
		}
	}
	// Other tools are untouched.
	if got, _ = r.Resolve(ctx, "s", "kenaz", "read_file"); got.Policy != PolicyAutoAllow {
		t.Fatalf("read_file: got %+v", got)
	}
	if got, _ = r.Resolve(ctx, "s", "github", "web_fetch"); got.Policy != PolicyAutoAllow {
		t.Fatalf("mcp server tool named web_fetch: got %+v", got)
	}
	// Live: switching the starter policy off takes effect on the next call.
	on = false
	if got, _ = r.Resolve(ctx, "s", "kenaz", "web_fetch"); got.Policy != PolicyAutoAllow {
		t.Fatalf("after off: got %+v", got)
	}
	// A stricter inner verdict always wins.
	on = true
	d := NewStarterNetworkResolver(fixedResolver{PolicyDeny})
	d.SetActive(func() bool { return true })
	if got, _ = d.Resolve(ctx, "s", "kenaz", "web_fetch"); got.Policy != PolicyDeny || got.AskDespitePermit {
		t.Fatalf("deny inner: got %+v", got)
	}
}
