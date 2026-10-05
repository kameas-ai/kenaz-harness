package workflows_test

import (
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/workflows"
)

// The install provenance side file survives a restart (a fresh store over
// the same data dir) and Remove deletes the record durably
// (install-framework-01DOGF0B WP05 review H1/H2/H4).
func TestFileProvenanceStore_RoundTripsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	s := workflows.NewFileProvenanceStore(dir)
	if err := s.Put(workflows.InstallProvenance{WorkflowID: "wf", Source: workflows.ProvenanceCatalog, CatalogID: "cat-a", Slug: "wf", Version: "1.0.0"}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, ok, err := workflows.NewFileProvenanceStore(dir).Get("wf")
	if err != nil || !ok || got.CatalogID != "cat-a" || got.Version != "1.0.0" || got.InstalledAt.IsZero() {
		t.Fatalf("after restart: %+v, %v, %v", got, ok, err)
	}
	if err := s.Remove("wf"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, ok, _ := workflows.NewFileProvenanceStore(dir).Get("wf"); ok {
		t.Fatal("record survived Remove across a restart")
	}
	if err := s.Put(workflows.InstallProvenance{}); err == nil {
		t.Fatal("empty workflow id accepted")
	}
}
