package rpc

// FR-H3 / acceptance criterion 5 of tool-context-budget-01TCBUD01 (WP08):
// the composition readout's numbers against the provider's prompt_tokens
// on recorded usage frames.
//
// The frames are the first three model calls of dogfood session
// b0c22dc553fb96ddeea681b7371f1ea2 (2026-10-08, v0.93.0, OpenRouter
// Anthropic Haiku; docs/dogfood/2026-10-08-round2.md): every call carried
// all 143 tool schemas — outlook 94, kenaz 27, filesystem 14,
// harness-self 7, fetch 1 — and a 2,292-char system prompt. prompt_tokens
// are the usage rows' persisted values.
//
// What each part of the estimate is built from:
//   - MCP tools (109): the servers' real tools/list output, captured
//     2026-10-09 from the same installs the dogfood ran
//     (@softeria/ms-365-mcp-server 0.159.1 --preset outlook,
//     @modelcontextprotocol/server-filesystem 2026.8.31, mcp-server-fetch
//     from the uvx cache) into testdata/dogfood-2026-10-08/mcp-tools.json;
//   - built-ins (34): this tree's schemas for the 34 names the dogfood's
//     llm.request.tools line listed, read through New()'s
//     Tools_SchemaCosts (the composition's own per-tool TokenEst);
//   - system: 2,292 characters under the system slot's rule;
//   - history: the stored rows' content lengths under the per-message
//     rule (≤ 0.2 % of each frame).
//
// RESULT (2026-10-09): the estimate is 63.2 % of prompt_tokens on all
// three frames — off by 36.8 %, not within FR-H3's 10 %. The shared
// estimator counts ceil(runes / 4); the ~566 KB of tool-definition text
// on these requests came to ~224k provider tokens, ~2.5 characters per
// token (JSON schemas are punctuation- and short-key-dense). The spec's
// own ceil(bytes / 3.5) rule would reach ~72 % — also outside 10 %. Per the WP08 brief the estimator is NOT tuned to fit here:
// the gap is recorded in docs/unwired-ledger.md and
// docs/dogfood/2026-10-09-tool-context-acceptance.md (AC5), and this test
// pins the measured band so a change to the estimator — or to the
// built-in schemas — shows up as a failure that names this record.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/toolexposure"
)

// dogfoodBuiltins are the 34 non-MCP tool names on every 2026-10-08
// request (llm.request.tools, session b0c22dc5…).
var dogfoodBuiltins = []string{
	"harness-self__harness_read_get_onboarding_recommendations", "harness-self__harness_read_list_mcp_recipes",
	"harness-self__harness_read_list_models", "harness-self__harness_read_list_providers",
	"harness-self__harness_read_list_sessions", "harness-self__harness_read_list_settings",
	"harness-self__harness_read_materialize_run",
	"kenaz__ask_user_question", "kenaz__bash", "kenaz__build_knowledge_site", "kenaz__edit_file",
	"kenaz__enter_plan_mode", "kenaz__exit_plan_mode", "kenaz__fork_conversation", "kenaz__glob",
	"kenaz__grep", "kenaz__list_dir", "kenaz__list_open_worklist", "kenaz__list_secrets",
	"kenaz__monitor", "kenaz__read_context_file", "kenaz__read_file", "kenaz__request_filesystem_access",
	"kenaz__save_artifact", "kenaz__save_document", "kenaz__skill", "kenaz__sleep",
	"kenaz__subagent_dispatch", "kenaz__todo_write", "kenaz__update_artifact", "kenaz__update_document",
	"kenaz__web_fetch", "kenaz__web_search", "kenaz__write_file",
}

// recordedFrame is one usage frame: the provider's prompt_tokens and the
// content lengths (runes) of the history rows the call carried.
type recordedFrame struct {
	name         string
	promptTokens int
	historyRunes []int
}

// The session's stored rows 0..8: user 50; assistant 4; user 152;
// tool-call 40+75 (args); tool-result 447+75; assistant 4; user 197;
// tool-call 41+77; tool-result 39+92.
var dogfoodFrames = []recordedFrame{
	{"turn 1, call 1", 224798, []int{50}},
	{"turn 2, call 2", 225104, []int{50, 4, 152, 40 + 75, 447 + 75}},
	{"turn 3, call 1", 225296, []int{50, 4, 152, 40 + 75, 447 + 75, 4, 197, 41 + 77, 39 + 92}},
}

const dogfoodSystemChars = 2292

func TestComposition_RecordedDogfoodFrames_FRH3Gap(t *testing.T) {
	// MCP schemas, as served.
	raw, err := os.ReadFile(filepath.Join("testdata", "dogfood-2026-10-08", "mcp-tools.json"))
	if err != nil {
		t.Fatal(err)
	}
	var servers map[string][]struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"inputSchema"`
	}
	if err := json.Unmarshal(raw, &servers); err != nil {
		t.Fatal(err)
	}
	mcpTools, mcpBytes := 0, 0
	var mcpSpecs []corellm.ToolSpec
	for server, tools := range servers {
		for _, tl := range tools {
			spec := corellm.ToolSpec{Name: server + toolexposure.NameSeparator + tl.Name, Description: tl.Description, InputSchema: tl.InputSchema}
			mcpSpecs = append(mcpSpecs, spec)
			mcpBytes += len(spec.Name) + len(spec.Description) + len(spec.InputSchema)
			mcpTools++
		}
	}
	if mcpTools != 109 {
		// outlook 94 + filesystem 14 + fetch 1, as on the dogfood request.
		t.Fatalf("testdata MCP tools = %d, want 109", mcpTools)
	}
	mcpEst := corellm.ToolsTokens(mcpSpecs)

	// Built-ins, through New()'s Tools_SchemaCosts with the dials the
	// dogfood profile had on.
	sandboxUserConfigDir(t)
	c, err := core.New(core.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c)
	t.Cleanup(func() {
		api.Shutdown()
		_ = c.Shutdown(context.Background())
	})
	assertSettingsStoreIsSandboxed(t, api)
	ctx := context.Background()
	st, err := api.Settings().Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	st.BashEnabled, st.WebSearchEnabled, st.WebFetchEnabled = true, true, true
	if err := api.Settings().Set(ctx, st); err != nil {
		t.Fatalf("Settings Set: %v", err)
	}
	costs, err := api.ToolSchemaCosts(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	builtinEst := map[string]int{}
	for _, sc := range costs {
		for _, tc := range sc.Tools {
			builtinEst[sc.Server+toolexposure.NameSeparator+tc.Name] = tc.TokenEst
		}
	}
	builtinTotal := 0
	var missing []string
	for _, n := range dogfoodBuiltins {
		est, ok := builtinEst[n]
		if !ok {
			missing = append(missing, n)
			continue
		}
		builtinTotal += est
	}
	if len(missing) > 0 {
		t.Fatalf("built-ins on the dogfood request that this tree's catalog does not list: %v", missing)
	}

	system := corellm.SystemTokens(strings.Repeat("x", dogfoodSystemChars))
	for _, f := range dogfoodFrames {
		var msgs []corellm.Message
		for _, n := range f.historyRunes {
			msgs = append(msgs, corellm.NewTextMessage(corellm.RoleUser, strings.Repeat("x", n)))
		}
		history := corellm.MessagesTokens(msgs)
		est := system + mcpEst + builtinTotal + history
		ratio := float64(est) / float64(f.promptTokens)
		t.Logf("%s: estimate %d (system %d + MCP tools %d + built-ins %d + history %d) vs provider prompt_tokens %d → %.1f %% (off by %.1f %%)",
			f.name, est, system, mcpEst, builtinTotal, history, f.promptTokens, 100*ratio, 100*(1-ratio))
		// FR-H3 wants 0.90..1.10. The measured band is pinned instead
		// (see the header): leaving it means the estimator or the schemas
		// changed — update docs/dogfood/2026-10-09-tool-context-acceptance.md
		// (AC5) and the ledger entry, and if the estimate is now within
		// 10 %, make this test assert FR-H3.
		if ratio < 0.60 || ratio > 0.72 {
			t.Errorf("%s: estimate/provider = %.3f, outside the recorded 0.60..0.72 band — the FR-H3 record is stale", f.name, ratio)
		}
	}
	t.Logf("MCP schema text: %d bytes; estimator %d tokens (%.2f bytes per estimated token)",
		mcpBytes, mcpEst, float64(mcpBytes)/float64(mcpEst))
}
