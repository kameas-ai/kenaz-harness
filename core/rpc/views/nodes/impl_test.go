package nodes_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	corenodes "github.com/kameas-ai/kenaz-harness/core/agentgraph/nodes"
	nodesview "github.com/kameas-ai/kenaz-harness/core/rpc/views/nodes"
)

// helper that loads the bundled-only catalog for happy-path tests.
func bundledCatalog(t *testing.T) *corenodes.Catalog {
	t.Helper()
	cat, err := corenodes.LoadCatalog(corenodes.LoadOptions{})
	if err != nil {
		t.Fatalf("LoadCatalog(bundled): %v", err)
	}
	return cat
}

// writeManifest is the small helper mirroring the resolver test rig.
func writeManifest(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// TestCatalogHappyPath asserts the catalog list returns every shipped
// archetype with non-empty hashes + sources.
func TestCatalogHappyPath(t *testing.T) {
	t.Parallel()
	mgr := nodesview.NewManager(nodesview.ManagerConfig{Catalog: bundledCatalog(t)})
	api := nodesview.New(mgr)
	rows, err := api.Catalog(context.Background())
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected at least one row")
	}
	wantIDs := map[string]bool{
		"compute": false, "control": false, "state": false,
		"read": false, "write": false, "marker": false,
	}
	for _, r := range rows {
		if _, ok := wantIDs[r.ID]; ok {
			wantIDs[r.ID] = true
		}
		if r.Hash == "" {
			t.Errorf("row %q has empty hash", r.ID)
		}
		if r.Source == "" {
			t.Errorf("row %q has empty source", r.ID)
		}
	}
	for id, found := range wantIDs {
		if !found {
			t.Errorf("missing archetype %q in catalog", id)
		}
	}
}

// TestGetHappyPath returns the full detail including provenance for
// the inherited `read` archetype (chain: state → read).
func TestGetHappyPath(t *testing.T) {
	t.Parallel()
	mgr := nodesview.NewManager(nodesview.ManagerConfig{Catalog: bundledCatalog(t)})
	api := nodesview.New(mgr)

	detail, err := api.Get(context.Background(), "read")
	if err != nil {
		t.Fatalf("Get(read): %v", err)
	}
	if got, want := len(detail.Chain), 2; got != want {
		t.Errorf("chain len: got %d, want %d (chain=%v)", got, want, detail.Chain)
	}
	// Provenance must surface the `provenance` attr (inherited from state).
	var sawAttrProv bool
	for _, p := range detail.Provenance {
		if p.FieldPath == "attrs.provenance" {
			sawAttrProv = true
			// The layer for that field path should be "archetype-state"
			// (inherited) per the resolver's WP01 semantics.
			if !strings.HasPrefix(p.Layer, "archetype-") {
				t.Errorf("attrs.provenance layer: got %q, want archetype-*", p.Layer)
			}
		}
	}
	if !sawAttrProv {
		t.Errorf("provenance missing attrs.provenance entry; got %+v", detail.Provenance)
	}
}

// TestGetUnknownKindWraps surfaces ErrUnknownKind via errors.Is so the
// frontend can branch on missing-id vs. other failures.
func TestGetUnknownKindWraps(t *testing.T) {
	t.Parallel()
	mgr := nodesview.NewManager(nodesview.ManagerConfig{Catalog: bundledCatalog(t)})
	api := nodesview.New(mgr)
	_, err := api.Get(context.Background(), "definitely-not-a-kind")
	if err == nil {
		t.Fatal("expected error for missing kind")
	}
	if !errors.Is(err, corenodes.ErrUnknownKind) {
		t.Errorf("expected ErrUnknownKind, got: %v", err)
	}
}

// TestNilManagerEmptyState: New(nil) returns ErrManagerUnavailable on
// every method.
func TestNilManagerEmptyState(t *testing.T) {
	t.Parallel()
	api := nodesview.New(nil)
	if _, err := api.Catalog(context.Background()); !errors.Is(err, nodesview.ErrManagerUnavailable) {
		t.Errorf("Catalog: expected ErrManagerUnavailable, got %v", err)
	}
	if _, err := api.Get(context.Background(), "compute"); !errors.Is(err, nodesview.ErrManagerUnavailable) {
		t.Errorf("Get: expected ErrManagerUnavailable, got %v", err)
	}
	if _, err := api.ReloadOverrides(context.Background()); !errors.Is(err, nodesview.ErrManagerUnavailable) {
		t.Errorf("ReloadOverrides: expected ErrManagerUnavailable, got %v", err)
	}
	if _, err := api.ListUserOverrides(context.Background()); !errors.Is(err, nodesview.ErrManagerUnavailable) {
		t.Errorf("ListUserOverrides: expected ErrManagerUnavailable, got %v", err)
	}
}

// TestReloadOverridesAddsModifies: stage a fresh user override under
// the manager's UserDir; ReloadOverrides should detect a "modified"
// entry on the same id (the shipped compute archetype has its
// description tuned).
func TestReloadOverridesAddsModifies(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	mgr := nodesview.NewManager(nodesview.ManagerConfig{
		Catalog: bundledCatalog(t),
		UserDir: dir,
	})
	api := nodesview.New(mgr)

	// First reload over an empty user dir: no-op (no diff).
	res, err := api.ReloadOverrides(context.Background())
	if err != nil {
		t.Fatalf("first reload: %v", err)
	}
	if len(res.Added)+len(res.Removed)+len(res.Modified) != 0 {
		t.Errorf("expected empty diff over empty user dir, got %+v", res)
	}

	// Drop a user override of compute and re-run.
	writeManifest(t, dir, "_archetype.compute.yaml", `archetype: compute
category: compute
description: tuned-by-reload-test
budget: llm
budget_consumes: [tokens, llm_calls, cost_usd, custom_dial]
`)
	res, err = api.ReloadOverrides(context.Background())
	if err != nil {
		t.Fatalf("second reload: %v", err)
	}
	if len(res.Modified) == 0 {
		t.Errorf("expected at least one modified id, got %+v", res)
	}
	// And the manager's catalog now reflects the override.
	got, gErr := api.Get(context.Background(), "compute")
	if gErr != nil {
		t.Fatalf("Get after reload: %v", gErr)
	}
	if got.Summary.Description != "tuned-by-reload-test" {
		t.Errorf("description after reload: got %q", got.Summary.Description)
	}
}

// TestReloadOverridesSurfacesParseError: a malformed user file does
// not crash the reload but lands its error in ReloadResult.Errors.
func TestReloadOverridesSurfacesParseError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifest(t, dir, "broken.yaml", "not: [valid: yaml")
	mgr := nodesview.NewManager(nodesview.ManagerConfig{
		Catalog: bundledCatalog(t),
		UserDir: dir,
	})
	api := nodesview.New(mgr)
	res, err := api.ReloadOverrides(context.Background())
	if err != nil {
		t.Fatalf("ReloadOverrides: %v", err)
	}
	if len(res.Errors) == 0 {
		t.Errorf("expected at least one error in ReloadResult, got %+v", res)
	}
}

// TestListUserOverridesHappyPath: a single user override is listed
// with a recognised id and "ok" status.
func TestListUserOverridesHappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifest(t, dir, "_archetype.compute.yaml", `archetype: compute
category: compute
description: listed
budget: llm
`)
	mgr := nodesview.NewManager(nodesview.ManagerConfig{
		Catalog: bundledCatalog(t),
		UserDir: dir,
	})
	api := nodesview.New(mgr)
	rows, err := api.ListUserOverrides(context.Background())
	if err != nil {
		t.Fatalf("ListUserOverrides: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d (%+v)", len(rows), rows)
	}
	if rows[0].Filename != "_archetype.compute.yaml" {
		t.Errorf("filename: got %q", rows[0].Filename)
	}
	if rows[0].Status != "ok" {
		t.Errorf("status: got %q (err=%q)", rows[0].Status, rows[0].Error)
	}
	if rows[0].SizeBytes <= 0 {
		t.Errorf("expected non-zero size, got %d", rows[0].SizeBytes)
	}
}

// TestListUserOverridesMissingDir returns an empty slice with no
// error (silent no-op per FR-023).
func TestListUserOverridesMissingDir(t *testing.T) {
	t.Parallel()
	mgr := nodesview.NewManager(nodesview.ManagerConfig{
		Catalog: bundledCatalog(t),
		UserDir: "/path/that/does/not/exist",
	})
	api := nodesview.New(mgr)
	rows, err := api.ListUserOverrides(context.Background())
	if err != nil {
		t.Fatalf("ListUserOverrides: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected empty list, got %+v", rows)
	}
}

// TestListUserOverridesEmptyDir: empty UserDir reports an empty list
// (no error).
func TestListUserOverridesEmptyDir(t *testing.T) {
	t.Parallel()
	mgr := nodesview.NewManager(nodesview.ManagerConfig{
		Catalog: bundledCatalog(t),
		UserDir: "",
	})
	api := nodesview.New(mgr)
	rows, err := api.ListUserOverrides(context.Background())
	if err != nil {
		t.Fatalf("ListUserOverrides: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected empty list, got %+v", rows)
	}
}

// TestDoctorReportsCounters asserts that Doctor() returns the catalog
// header counters and the configured user-dir / hot-reload flag. This
// is the WP08 surface backing the frontend NodesView debug panel.
func TestDoctorReportsCounters(t *testing.T) {
	t.Parallel()
	mgr := nodesview.NewManager(nodesview.ManagerConfig{
		Catalog:          bundledCatalog(t),
		UserDir:          "/tmp/never-exists-doctor",
		HotReloadEnabled: true,
	})
	api := nodesview.New(mgr)
	rep, err := api.Doctor(context.Background())
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if rep.ShippedCount == 0 {
		t.Errorf("expected shippedCount > 0, got %d", rep.ShippedCount)
	}
	if rep.ArchetypeCount == 0 {
		t.Errorf("expected archetypeCount > 0, got %d", rep.ArchetypeCount)
	}
	if rep.CallableCount == 0 {
		t.Errorf("expected callableCount > 0, got %d", rep.CallableCount)
	}
	if rep.UserDir != "/tmp/never-exists-doctor" {
		t.Errorf("userDir: got %q, want /tmp/never-exists-doctor", rep.UserDir)
	}
	if !rep.HotReloadEnabled {
		t.Error("expected hotReloadEnabled to round-trip")
	}
	if rep.SunsetVersion == "" {
		t.Error("expected SunsetVersion to be populated")
	}
}

// TestDoctorTracksUserOverrideAndReload: after a successful reload of
// a UserDir containing one override, the Doctor report shows the
// user override in the count, populates the lastReloadAt timestamp,
// and clears any prior errors.
func TestDoctorTracksUserOverrideAndReload(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifest(t, dir, "model.yaml", `id: model
extends: compute
description: doctor-test-override
`)
	cat, err := corenodes.LoadCatalog(corenodes.LoadOptions{UserDir: dir})
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	mgr := nodesview.NewManager(nodesview.ManagerConfig{
		Catalog: cat,
		UserDir: dir,
	})
	api := nodesview.New(mgr)
	// Trigger a reload so reloadedAt populates.
	if _, err := api.ReloadOverrides(context.Background()); err != nil {
		t.Fatalf("ReloadOverrides: %v", err)
	}
	rep, err := api.Doctor(context.Background())
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if rep.UserOverrideCount == 0 {
		t.Errorf("expected at least one user override, got %d", rep.UserOverrideCount)
	}
	if rep.LastReloadAt == "" {
		t.Errorf("expected LastReloadAt to be populated after reload")
	}
	if len(rep.UserOverrideErrors) != 0 {
		t.Errorf("expected no errors, got %v", rep.UserOverrideErrors)
	}
}

// TestProvenanceUserOverrideTagged: when a user override modifies a
// scalar, the resolved manifest's provenance for that field surfaces
// as "user-override".
func TestProvenanceUserOverrideTagged(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifest(t, dir, "_archetype.compute.yaml", `archetype: compute
category: compute
description: tuned-for-prov-test
budget: llm
`)
	cat, err := corenodes.LoadCatalog(corenodes.LoadOptions{UserDir: dir})
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	mgr := nodesview.NewManager(nodesview.ManagerConfig{
		Catalog: cat,
		UserDir: dir,
	})
	api := nodesview.New(mgr)
	detail, err := api.Get(context.Background(), "compute")
	if err != nil {
		t.Fatalf("Get(compute): %v", err)
	}
	// We expect the description's provenance to mark the leaf layer
	// AND the source string carries the "user:" prefix, so the wire
	// layer normaliser surfaces "user-override".
	var saw bool
	for _, p := range detail.Provenance {
		if p.FieldPath == "description" {
			saw = true
			if p.Layer != nodesview.LayerUserOverride {
				t.Errorf("description.layer: got %q, want %q",
					p.Layer, nodesview.LayerUserOverride)
			}
		}
	}
	if !saw {
		t.Errorf("provenance missing description entry; got %+v", detail.Provenance)
	}
}
