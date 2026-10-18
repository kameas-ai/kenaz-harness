package rpc

// mlproducer_builtin_tools_test.go — counsel D43/C3: agent.tool's `tool`
// carries a name in clear only when it is in mlproducer.BuiltinToolNames,
// a CLOSED list of first-party tools. This test is its generator and pin:
// it boots the real API the way production does (rpc.New), lists every
// builtin the registry holds, adds every tool of the in-process
// first-party MCP servers (harness-self, fleet-sites) as the dispatched
// "<server>__<tool>" name, and requires BuiltinToolNames to equal that set
// exactly and stay sorted. On a difference it prints the list to paste
// into core/mlproducer/record.go.

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core"
	harnessmcp "github.com/kameas-ai/kenaz-harness/core/mcp/builtin/harness"
	sitesmcp "github.com/kameas-ai/kenaz-harness/core/mcp/builtin/sites"
	"github.com/kameas-ai/kenaz-harness/core/mcp/builtin/toolserver"
	"github.com/kameas-ai/kenaz-harness/core/mlproducer"
)

// firstPartyToolNames is the generated set: registered builtins plus the
// first-party in-process MCP servers' tools.
func firstPartyToolNames(t *testing.T) []string {
	t.Helper()
	c, err := core.New(core.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c, WithSettingsStore(newTestStore(t)))
	t.Cleanup(api.Shutdown)
	if api.Builtins() == nil {
		t.Fatal("builtins registry is nil")
	}
	names := append([]string(nil), api.Builtins().Names()...)
	if len(names) == 0 {
		t.Fatal("builtin registry is empty — construction broke")
	}
	for _, s := range harnessmcp.RegisterAll(nil, harnessmcp.Managers{}).Tools() {
		names = append(names, harnessmcp.ServerName+"__"+s.Name)
	}
	sites := toolserver.NewServer(sitesmcp.ServerName, "pin")
	sitesmcp.RegisterAll(sites, nil, "")
	for _, s := range sites.Tools() {
		names = append(names, sitesmcp.ServerName+"__"+s.Name)
	}
	sort.Strings(names)
	return names
}

func TestMLBuiltinToolNames_MatchRegisteredFirstPartyTools(t *testing.T) {
	got := firstPartyToolNames(t)
	want := mlproducer.BuiltinToolNames
	if !sort.StringsAreSorted(want) {
		t.Error("mlproducer.BuiltinToolNames is not sorted")
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		var b strings.Builder
		for _, n := range got {
			fmt.Fprintf(&b, "\t%q,\n", n)
		}
		inGot, inWant := map[string]bool{}, map[string]bool{}
		for _, n := range got {
			inGot[n] = true
		}
		for _, n := range want {
			inWant[n] = true
		}
		var missing, extra []string
		for _, n := range got {
			if !inWant[n] {
				missing = append(missing, n)
			}
		}
		for _, n := range want {
			if !inGot[n] {
				extra = append(extra, n)
			}
		}
		t.Errorf("mlproducer.BuiltinToolNames drifted from the registered first-party tools.\n"+
			"registered but not listed (would ship as mcp:<hash>): %v\n"+
			"listed but not registered (a stale clear name): %v\n"+
			"paste this into core/mlproducer/record.go:\n%s", missing, extra, b.String())
	}
	for _, n := range got {
		if !mlproducer.IsBuiltinToolName(n) {
			t.Errorf("IsBuiltinToolName(%q) = false", n)
		}
	}
}
