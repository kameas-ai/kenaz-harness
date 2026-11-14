package fleet

import (
	"encoding/json"
	"strings"
	"testing"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestComputeNodeFacts_BareInstall(t *testing.T) {
	f := computeNodeFacts(envOf(nil), "arm64", "macOS 14.5")
	want := NodeFacts{Arch: "arm64", OSVersion: "macOS 14.5", ClientKind: ClientKindHarness}
	if f != want {
		t.Fatalf("facts = %+v, want %+v", f, want)
	}
}

func TestComputeNodeFacts_WorkbenchVM(t *testing.T) {
	f := computeNodeFacts(envOf(map[string]string{
		EnvWorkbenchID: " wb-01HZX ",
		EnvHostNodeID:  "01JBMHOSTNODE0000000000000",
	}), "amd64", "NixOS 26.05")
	if !f.InVM || f.VMID != "wb-01HZX" || f.HostNodeID != "01JBMHOSTNODE0000000000000" {
		t.Fatalf("facts = %+v", f)
	}
}

// A Kenaz that predates KENAZ_WORKBENCH_ID still writes SIGIL_WORKBENCH_ID
// to kenaz-meta.env; it names the same workbench.
func TestComputeNodeFacts_LegacyWorkbenchID(t *testing.T) {
	f := computeNodeFacts(envOf(map[string]string{envLegacyWorkbenchID: "wb-legacy"}), "arm64", "")
	if !f.InVM || f.VMID != "wb-legacy" || f.HostNodeID != "" {
		t.Fatalf("facts = %+v", f)
	}
	// The new name wins when both are present.
	f = computeNodeFacts(envOf(map[string]string{envLegacyWorkbenchID: "old", EnvWorkbenchID: "new"}), "arm64", "")
	if f.VMID != "new" {
		t.Fatalf("VMID = %q, want new", f.VMID)
	}
}

// Malformed ids are dropped, never sent; a host id without a workbench id
// is meaningless and dropped too.
func TestComputeNodeFacts_RejectsBadIDs(t *testing.T) {
	f := computeNodeFacts(envOf(map[string]string{
		EnvWorkbenchID: "wb 1;rm -rf", EnvHostNodeID: "ok-host",
	}), "arm64", "")
	if f.InVM || f.VMID != "" || f.HostNodeID != "" {
		t.Fatalf("facts = %+v, want no VM fields", f)
	}
	f = computeNodeFacts(envOf(map[string]string{EnvHostNodeID: "ok-host"}), "arm64", "")
	if f.InVM || f.HostNodeID != "" {
		t.Fatalf("host id without a workbench id leaked: %+v", f)
	}
	f = computeNodeFacts(envOf(map[string]string{EnvWorkbenchID: "wb", EnvHostNodeID: "bad\nid"}), "arm64", "")
	if !f.InVM || f.HostNodeID != "" {
		t.Fatalf("facts = %+v, want VM without host id", f)
	}
}

func TestNormalizeArch(t *testing.T) {
	for in, want := range map[string]string{"amd64": "amd64", "arm64": "arm64", "386": "amd64", "riscv64": "", "": ""} {
		if got := normalizeArch(in); got != want {
			t.Errorf("normalizeArch(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanNodeFact(t *testing.T) {
	if got := cleanNodeFact(" Ubuntu\t24.04\n"); got != "Ubuntu 24.04" {
		t.Errorf("got %q", got)
	}
	long := cleanNodeFact(strings.Repeat("é", 200))
	if len(long) > maxNodeFactBytes || !strings.HasSuffix(long, "é") {
		t.Errorf("truncation broke a rune or the cap: len=%d", len(long))
	}
}

func TestOSReleaseVersion(t *testing.T) {
	cases := map[string]string{
		"NAME=NixOS\nID=nixos\nVERSION_ID=\"26.05\"\nPRETTY_NAME=\"NixOS 26.05 (Xantusia)\"\n": "NixOS 26.05",
		"PRETTY_NAME=\"Ubuntu 24.04.1 LTS\"\nNAME=\"Ubuntu\"\nVERSION_ID=\"24.04\"\n":          "Ubuntu 24.04",
		"NAME=\"Fedora Linux\"\nVERSION_ID=41\n":                                               "Fedora 41",
		"NAME=Arch Linux\nID=arch\n":                                                           "Arch",
		"ID=unknown\n":                                                                         "",
	}
	for in, want := range cases {
		if got := osReleaseVersion(in); got != want {
			t.Errorf("osReleaseVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

// The enroll body: facts ride as Fleet's field names; nothing is sent for a
// bare install beyond arch/os_version/client_kind. Node facts never set the
// hostname key: the device name is the member's setting (device_name.go).
func TestEnrollRequest_NodeFactsWire(t *testing.T) {
	var r enrollRequest
	r.NodeID, r.Platform, r.Version = "n1", "linux", "1.0.0"
	r.withNodeFacts(NodeFacts{Arch: "amd64", OSVersion: "Ubuntu 24.04", ClientKind: ClientKindHarness})
	b, _ := json.Marshal(r)
	got := map[string]any{}
	_ = json.Unmarshal(b, &got)
	for _, k := range []string{"in_vm", "vm_id", "host_node_id", "hostname"} {
		if _, ok := got[k]; ok {
			t.Errorf("bare install sent %q", k)
		}
	}
	if got["arch"] != "amd64" || got["os_version"] != "Ubuntu 24.04" || got["client_kind"] != "kenaz-harness" {
		t.Fatalf("body = %s", b)
	}

	r.withNodeFacts(NodeFacts{Arch: "arm64", OSVersion: "NixOS 26.05", ClientKind: ClientKindHarness,
		InVM: true, VMID: "wb-7", HostNodeID: "host-node-1"})
	b, _ = json.Marshal(r)
	got = map[string]any{}
	_ = json.Unmarshal(b, &got)
	if got["in_vm"] != true || got["vm_id"] != "wb-7" || got["host_node_id"] != "host-node-1" {
		t.Fatalf("VM body = %s", b)
	}
	if _, ok := got["hostname"]; ok {
		t.Fatalf("node facts must not set hostname: %s", b)
	}
}

func TestCurrentNodeFacts_OverrideAndRestore(t *testing.T) {
	restore := SetNodeFactsForTesting(NodeFacts{Arch: "arm64", ClientKind: ClientKindHarness, InVM: true, VMID: "wb-x"})
	if f := CurrentNodeFacts(); !f.InVM || f.VMID != "wb-x" {
		t.Fatalf("override not applied: %+v", f)
	}
	restore()
	f := CurrentNodeFacts()
	if f.InVM && f.VMID == "wb-x" {
		t.Fatal("override not restored")
	}
	if f.ClientKind != ClientKindHarness {
		t.Fatalf("ClientKind = %q", f.ClientKind)
	}
}
