package agentgraph_test

import (
	"strings"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/agentgraph/prompts"
	graphview "github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph"
)

// TestChatDefaultGraphLoader_SeedsConstitution mirrors the composition the
// chat GraphLoader closure in core/rpc/api.go performs on every load:
//
//	g, err := graphMgr.LoadGraphSpec("chat_default")
//	g.SystemPrompt = prompts.Compose(nil, prompts.DefaultBaseConstitution(), g.SystemPrompt)
//
// This guards that the wiring stays intact: the composed graph's
// SystemPrompt must begin with the shared constitution (WP02), and the
// assistant_turn model node must carry its own role text (not the
// constitution — that would duplicate the shared text into YAML).
func TestChatDefaultGraphLoader_SeedsConstitution(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mgr, err := graphview.NewManager(graphview.WithDataDir(dir))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	g, err := mgr.LoadGraphSpec("chat_default")
	if err != nil {
		t.Fatalf("LoadGraphSpec(chat_default): %v", err)
	}

	// Before composition the bundled YAML must NOT set a top-level
	// system_prompt itself — the loader is the single seeding point so
	// base.md stays the one source of truth for the constitution.
	if g.SystemPrompt != "" {
		t.Errorf("chat_default.yaml sets a top-level system_prompt = %q; expected unset (loader seeds it)", g.SystemPrompt)
	}

	composed := prompts.Compose(nil, prompts.DefaultBaseConstitution(), g.SystemPrompt)
	trimmedConstitution := strings.TrimSpace(prompts.DefaultBaseConstitution())
	if !strings.HasPrefix(composed, trimmedConstitution) {
		n := len(composed)
		if n > 120 {
			n = 120
		}
		t.Fatalf("composed graph SystemPrompt does not begin with the shared constitution;\ngot: %q", composed[:n])
	}

	// The assistant_turn node should carry a role-only system_prompt —
	// it must not itself restate the constitution.
	var found bool
	for _, node := range g.Nodes {
		if node.ID != "assistant_turn" {
			continue
		}
		found = true
		attrs, ok := node.Attrs.(coreag.ModelAttrs)
		if !ok {
			t.Fatalf("assistant_turn attrs type = %T, want coreag.ModelAttrs", node.Attrs)
		}
		if strings.TrimSpace(attrs.SystemPrompt) == "" {
			t.Fatal("assistant_turn node has no system_prompt attr; expected an assistant role")
		}
		if strings.Contains(attrs.SystemPrompt, prompts.DefaultBaseConstitution()) {
			t.Error("assistant_turn system_prompt re-pastes the shared constitution; it should only carry the role")
		}
	}
	if !found {
		t.Fatal("chat_default graph has no assistant_turn node")
	}
}
