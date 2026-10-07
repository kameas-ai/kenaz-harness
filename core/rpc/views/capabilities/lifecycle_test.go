package capabilities_test

// skill-library-01SKLIB01 WP02 (backend half): a catalog version's org
// lifecycle reaches the Capabilities rows. Deprecated is a label on an
// installable row; a deprecated org-REQUIRED copy stays installed, read-only
// and labelled (fleet §3.5: the bundle is unchanged); active is unlabelled.

import (
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/rpc/views/capabilities"
	coreslashcmd "github.com/kameas-ai/kenaz-harness/core/slashcmd"
)

func TestSkillRows_CarryLifecycle(t *testing.T) {
	f := newSkillFixture(t)
	f.cat.publish("skill", capabilities.CatalogEntry{ID: "c-active", Slug: "a", Version: "1", Lifecycle: "active"}, skillPayload(t, "a", "a"))
	f.cat.publish("skill", capabilities.CatalogEntry{ID: "c-dep", Slug: "d", Version: "1", Lifecycle: "deprecated", LifecycleReason: "use v2", SupersededBy: "c-active"}, skillPayload(t, "d", "d"))
	f.cat.publish("skill", capabilities.CatalogEntry{ID: "c-rev", Slug: "r", Version: "1", Lifecycle: "revoked"}, nil)
	f.cat.publish("skill", capabilities.CatalogEntry{ID: "c-old", Slug: "o", Version: "1"}, skillPayload(t, "o", "o")) // pre-0114: no key

	if it := f.row(t, "c-active"); it.Lifecycle != "" {
		t.Errorf("active row labelled %q", it.Lifecycle)
	}
	if it := f.row(t, "c-old"); it.Lifecycle != "" {
		t.Errorf("pre-0114 row labelled %q", it.Lifecycle)
	}
	if it := f.row(t, "c-dep"); it.Lifecycle != "deprecated" || it.LifecycleReason != "use v2" || it.SupersededBy != "c-active" {
		t.Errorf("deprecated row = %+v", it)
	}
	if it := f.row(t, "c-rev"); it.Lifecycle != "revoked" || it.State.Installed {
		t.Errorf("revoked row = %+v", it)
	}
}

// A deprecated version that the org REQUIRES stays installed and read-only,
// and the row says Deprecated — deprecation changes pixels only.
func TestSkillRows_DeprecatedMandatedCopyStaysInstalled(t *testing.T) {
	f := newSkillFixture(t)
	if err := coreslashcmd.LiveRegister(f.store, f.registry, coreslashcmd.Skill{
		ID: "policy", Trigger: "policy", Kind: coreslashcmd.KindText, Body: "x",
		Source: coreslashcmd.SkillSourceMandated, OrgManaged: true, CatalogID: "c-req", Version: "2",
	}); err != nil {
		t.Fatal(err)
	}
	f.cat.publish("skill", capabilities.CatalogEntry{ID: "c-req", Slug: "policy", Version: "2", Lifecycle: "deprecated"}, nil)
	it := f.row(t, "c-req")
	if !it.State.Installed || !it.ReadOnly || it.Lifecycle != "deprecated" {
		t.Fatalf("deprecated required row = %+v, want installed + read-only + deprecated", it)
	}
	if _, err := f.store.Get("policy"); err != nil {
		t.Fatalf("listing touched the mandated copy: %v", err)
	}
}
