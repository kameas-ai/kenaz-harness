package rpc

import (
	"testing"

	"github.com/kameas-ai/kenaz-harness/core"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
	coredocuments "github.com/kameas-ai/kenaz-harness/core/tools/documents"
	corefsbuiltins "github.com/kameas-ai/kenaz-harness/core/tools/fsbuiltins"
	coresaveartifact "github.com/kameas-ai/kenaz-harness/core/tools/saveartifact"
)

// TestDocumentTools_RegisteredOnRealChassis pins the spec-092 late
// registration in New(): a chassis with a database registers all three
// document tools. Without this the tools could silently fall out of the
// catalog (for example if a.unitsMgr were read before it is assigned) and
// the all-tools predicate tripwire, which only walks registered names,
// would not notice.
func TestDocumentTools_RegisteredOnRealChassis(t *testing.T) {
	c, err := core.New(core.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c, WithSettingsStore(newTestStore(t)))
	t.Cleanup(api.Shutdown)

	names := map[string]bool{}
	for _, n := range api.Builtins().Names() {
		names[n] = true
	}
	for _, want := range []string{coredocuments.NameSave, coredocuments.NameUpdate, coredocuments.NameBuildSite} {
		if !names[want] {
			t.Errorf("%s not registered", want)
		}
	}
}

// TestDocumentTools_PredicateFollowsSharedDials asserts the gates the
// registerDocumentTools doc comment promises: save_document rides the
// save_artifact dial, and update_document / build_knowledge_site ride
// exactly the gate kenaz__write_file rides, in both dial positions.
//
// It deliberately does not assert a default for the write dial. The
// Settings comments and the Tools panel test say FSWriteEnabled defaults
// to false, but Settings.FSWriteEnabled() is !FSWriteDisabled, so a fresh
// install reports true. That pre-existing discrepancy is recorded in the
// spec-092 knowledge-site plan for its owner; pinning either value here
// would bake one side of it in.
func TestDocumentTools_PredicateFollowsSharedDials(t *testing.T) {
	t.Parallel()
	api := settings.NewAPI(nil)
	store := api.Store()
	pred := builtinEnabledPredicate(api)

	for _, enabled := range []bool{false, true} {
		if err := store.SaveFSWriteEnabled(enabled); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveSaveArtifactEnabled(enabled); err != nil {
			t.Fatal(err)
		}
		if got := pred(coredocuments.NameSave); got != pred(coresaveartifact.ToolName) || got != enabled {
			t.Errorf("save_document=%v, save_artifact=%v with dial %v", got, pred(coresaveartifact.ToolName), enabled)
		}
		for _, name := range []string{coredocuments.NameUpdate, coredocuments.NameBuildSite} {
			if got := pred(name); got != pred(corefsbuiltins.NameWriteFile) || got != enabled {
				t.Errorf("%s=%v, write_file=%v with write dial %v", name, got, pred(corefsbuiltins.NameWriteFile), enabled)
			}
		}
	}
}
