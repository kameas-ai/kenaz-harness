package rpc

import (
	"sort"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/toolexposure"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
	coreaskuser "github.com/kameas-ai/kenaz-harness/core/tools/askuserquestion"
	corebash "github.com/kameas-ai/kenaz-harness/core/tools/bash"
	coreforkconv "github.com/kameas-ai/kenaz-harness/core/tools/forkconversation"
	corefsbuiltins "github.com/kameas-ai/kenaz-harness/core/tools/fsbuiltins"
	coresaveartifact "github.com/kameas-ai/kenaz-harness/core/tools/saveartifact"
	coreskill "github.com/kameas-ai/kenaz-harness/core/tools/skill"
	coretodo "github.com/kameas-ai/kenaz-harness/core/tools/todo"
	corewebfetch "github.com/kameas-ai/kenaz-harness/core/tools/webfetch"
	corewebsearch "github.com/kameas-ai/kenaz-harness/core/tools/websearch"
)

// hotSetAnchors ties every toolexposure hot-set entry to the constant
// its tool registers under, so renaming a built-in breaks the build or
// this test rather than silently dropping it out of the default-full
// set. kenaz__load_tools has no tool package until
// tool-context-budget-01TCBUD01 WP03 adds it.
var hotSetAnchors = []string{
	toolexposure.LoadToolsName,
	corefsbuiltins.NameReadFile,
	corefsbuiltins.NameListDir,
	corefsbuiltins.NameGlob,
	corefsbuiltins.NameGrep,
	corefsbuiltins.NameWriteFile,
	corefsbuiltins.NameEditFile,
	corebash.Name,
	corewebfetch.ToolName,
	corewebsearch.ToolName,
	coreaskuser.ToolName,
	coretodo.ToolName,
	coresaveartifact.ToolName,
	coreskill.ToolName,
	coreforkconv.ToolName,
}

func TestToolExposureHotSet_MatchesToolConstants(t *testing.T) {
	want := append([]string(nil), hotSetAnchors...)
	sort.Strings(want)
	got := toolexposure.HotSet()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("toolexposure.HotSet() = %v\nanchored constants   = %v", got, want)
	}
}

// Every hot-set entry except kenaz__load_tools is a tool the production
// registrars register, under the server name the toolloop dispatches
// built-ins on.
//
// registerBuiltinTools and registerFSBuiltinTools register with nil
// dependencies. registerForkConversationTool (live branches view +
// session manager) and save_artifact (live artifacts manager) are only
// registered with real dependencies, so their names come from each
// tool's Name() constant, which is what those registrars register.
func TestToolExposureHotSet_NamesRegisteredBuiltins(t *testing.T) {
	if toolexposure.BuiltinServer != toolloop.BuiltinServerName {
		t.Fatalf("toolexposure.BuiltinServer = %q, toolloop.BuiltinServerName = %q", toolexposure.BuiltinServer, toolloop.BuiltinServerName)
	}
	registry := toolloop.NewBuiltinRegistry()
	registerBuiltinTools(nil, registry, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	registerFSBuiltinTools(registry, nil, nil, nil, "", nil, nil)

	prefix := toolexposure.BuiltinServer + toolexposure.NameSeparator
	registered := map[string]bool{coreforkconv.ToolName: true, coresaveartifact.ToolName: true}
	for _, n := range registry.Names() {
		registered[prefix+strings.TrimPrefix(n, prefix)] = true
	}
	for _, name := range toolexposure.HotSet() {
		if name == toolexposure.LoadToolsName {
			continue
		}
		if !registered[name] {
			t.Errorf("hot-set entry %q is not a registered built-in", name)
		}
	}
}
