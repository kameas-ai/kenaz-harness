package settings

import (
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
)

func TestMemorySyncView_CarriesDisclosureAck(t *testing.T) {
	v := memorySyncView(fleet.MemorySyncStatus{LocalEnabled: true, DisclosureAcked: true})
	if !v.DisclosureAcked || v.DisclosureRevision != fleet.MemoryDisclosureRevision {
		t.Fatalf("view = acked %v revision %q", v.DisclosureAcked, v.DisclosureRevision)
	}
	if v.CurrentConsentVersion != fleet.MemoryConsentVersion {
		t.Fatalf("consent version changed: %q", v.CurrentConsentVersion)
	}
	if v := memorySyncView(fleet.MemorySyncStatus{LocalEnabled: true}); v.DisclosureAcked {
		t.Fatal("unacknowledged status reported as acknowledged")
	}
}
