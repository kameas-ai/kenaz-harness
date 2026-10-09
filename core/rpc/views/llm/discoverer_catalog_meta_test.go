package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/mcp"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// Every catalog entry carries its source server and the estimator's size
// of its definition.
func TestMCPToolDiscoverer_CatalogEntriesCarryServerAndTokenEst(t *testing.T) {
	pool := &stubPool{tools: []mcp.Tool{
		{Server: "outlook", Name: "send-mail", Description: strings.Repeat("x", 100), InputSchema: json.RawMessage(`{"type":"object"}`)},
	}}
	reg := toolloop.NewBuiltinRegistry()
	reg.Register(namedBuiltin("kenaz__read_file"))
	d := NewMCPToolDiscovererWithBuiltins(pool, nil, toolloop.NewEnabledFilter(reg, func(string) bool { return true }))

	got, err := d.Tools(context.Background(), "sess-meta")
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("catalog = %d entries, want 2", len(got))
	}
	want := map[string]string{"outlook__send-mail": "outlook", "kenaz__read_file": "kenaz"}
	for _, spec := range got {
		if spec.Server != want[spec.Name] {
			t.Errorf("%s: Server = %q, want %q", spec.Name, spec.Server, want[spec.Name])
		}
		if spec.TokenEst == 0 || spec.TokenEst != corellm.EstimateToolSpecTokens(spec) {
			t.Errorf("%s: TokenEst = %d, want %d", spec.Name, spec.TokenEst, corellm.EstimateToolSpecTokens(spec))
		}
	}
}

func schemaSizeLines(buf *bytes.Buffer) []string {
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if strings.Contains(line, `"msg":"tools.schema_size_changed"`) {
			out = append(out, line)
		}
	}
	return out
}

// A server's summed estimate is re-measured on every discovery; a move of
// more than 20 % logs tools.schema_size_changed once, a smaller move does
// not, and a per-session permission filter is not a size change.
func TestMCPToolDiscoverer_SchemaSizeChangeLogsAbove20Percent(t *testing.T) { //nolint:paralleltest // swaps the process logger
	var buf bytes.Buffer
	prev := logging.L().Handler()
	logging.Replace(slog.NewJSONHandler(&buf, nil))
	t.Cleanup(func() { logging.Replace(prev) })

	tool := func(name string, descBytes int) mcp.Tool {
		return mcp.Tool{Server: "fs", Name: name, Description: strings.Repeat("d", descBytes), InputSchema: json.RawMessage(`{}`)}
	}
	pool := &stubPool{tools: []mcp.Tool{tool("a", 1000)}}
	perms := &stubResolver{}
	d := NewMCPToolDiscoverer(pool, perms)
	ctx := context.Background()
	discover := func() {
		t.Helper()
		if _, err := d.Tools(ctx, "sess-size"); err != nil {
			t.Fatalf("Tools: %v", err)
		}
	}

	discover() // baseline: first sighting never logs
	if n := len(schemaSizeLines(&buf)); n != 0 {
		t.Fatalf("baseline logged %d lines, want 0", n)
	}

	pool.tools = []mcp.Tool{tool("a", 1100)} // +10 %
	discover()
	if n := len(schemaSizeLines(&buf)); n != 0 {
		t.Fatalf("a 10 %% move logged %d lines, want 0", n)
	}

	// The session's resolver now denies a tool: the session sees less,
	// but the server's tools/list did not change.
	pool.tools = []mcp.Tool{tool("a", 1100), tool("b", 1100)}
	discover() // +100 %: logs once
	perms.policies = map[string]toolloop.ToolPolicy{"fs::b": toolloop.PolicyDeny}
	discover() // same listing, filtered for this session: no log
	lines := schemaSizeLines(&buf)
	if len(lines) != 1 {
		t.Fatalf("schema_size_changed lines = %d, want exactly 1:\n%s", len(lines), buf.String())
	}
	if !strings.Contains(lines[0], `"server":"fs"`) || !strings.Contains(lines[0], `"tools":2`) {
		t.Errorf("line = %s, want server fs with 2 tools", lines[0])
	}
}
