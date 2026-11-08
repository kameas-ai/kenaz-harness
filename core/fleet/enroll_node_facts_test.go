package fleet

import (
	"testing"
)

// The served harness inside a workbench VM enrolls with the facts the Kenaz
// host handed it at boot; a bare install sends no VM fields. Both bodies
// keep every pre-existing field (node_id, platform, version, keys) and never
// carry a hostname.
func TestEnroll_BodyCarriesNodeFacts(t *testing.T) {
	f := newKeyFleet(t)
	c, _ := keyedClient(t, f)
	if err := StoreContextSeed(seq32()); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(SetNodeFactsForTesting(NodeFacts{
		Arch: "arm64", OSVersion: "NixOS 26.05", ClientKind: ClientKindHarness,
		InVM: true, VMID: "wb-01HZX", HostNodeID: "01JBMHOSTNODE0000000000000",
	}))
	if _, err := c.RefreshIdentity(t.Context(), "node-VM", "linux", "0.101.0"); err != nil {
		t.Fatalf("enroll: %v", err)
	}

	t.Cleanup(SetNodeFactsForTesting(NodeFacts{Arch: "amd64", OSVersion: "macOS 14.5", ClientKind: ClientKindHarness}))
	if _, err := c.RefreshIdentity(t.Context(), "node-VM", "darwin", "0.101.0"); err != nil {
		t.Fatalf("enroll (bare): %v", err)
	}

	reqs := f.snapshot()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(reqs))
	}
	vm, bare := reqs[0].Body, reqs[1].Body

	for _, b := range []map[string]any{vm, bare} {
		if b["node_id"] != "node-VM" || b["version"] != "0.101.0" || b["client_kind"] != "kenaz-harness" {
			t.Fatalf("base fields missing: %v", b)
		}
		if b["handoff_public_key"] == nil || b["signing_public_key"] == nil {
			t.Fatalf("keys dropped from enroll: %v", b)
		}
		if _, ok := b["hostname"]; ok {
			t.Fatalf("hostname on the wire: %v", b)
		}
	}
	if vm["arch"] != "arm64" || vm["os_version"] != "NixOS 26.05" || vm["platform"] != "linux" ||
		vm["in_vm"] != true || vm["vm_id"] != "wb-01HZX" || vm["host_node_id"] != "01JBMHOSTNODE0000000000000" {
		t.Fatalf("VM enroll body = %v", vm)
	}
	if bare["arch"] != "amd64" || bare["os_version"] != "macOS 14.5" || bare["platform"] != "darwin" {
		t.Fatalf("bare enroll body = %v", bare)
	}
	for _, k := range []string{"in_vm", "vm_id", "host_node_id"} {
		if _, ok := bare[k]; ok {
			t.Fatalf("bare install sent %q: %v", k, bare)
		}
	}
}
