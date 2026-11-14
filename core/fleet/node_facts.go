package fleet

// node_facts.go — the non-identifying facts about this install that ride the
// POST /api/v1/enroll body so Fleet can tell a bare harness install from a
// workbench VM harness, and link a VM to the Kenaz host that runs it
// (fleet contract §16, "Node identity on enroll").
//
// Nothing here is a hardware or user identifier: arch and a short OS
// version string, the client kind constant, and — only inside a workbench
// VM — the ids the Kenaz host hands the guest at boot. The device display
// name (enroll `hostname`) is not a node fact: it is the member's setting,
// resolved per enroll in device_name.go.

import (
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"unicode"
)

// Env vars the Kenaz host writes into the workbench's KENAZMETA disk
// (kenaz-meta.env, sourced by the served harness systemd unit via
// EnvironmentFile) so the in-VM harness can report where it runs. Both are
// optional: absent means "not in a workbench" and the enroll body carries
// no VM fields — exactly the pre-existing behaviour.
const (
	// EnvWorkbenchID is the workbench (VM) id → enroll `vm_id`, with
	// `in_vm: true`.
	EnvWorkbenchID = "KENAZ_WORKBENCH_ID"
	// EnvHostNodeID is the Kenaz host's own Fleet node id (its
	// ~/.kenaz/fleet/node_id.txt) → enroll `host_node_id`, the host ↔ VM
	// link Fleet's Hosts page shows.
	EnvHostNodeID = "KENAZ_HOST_NODE_ID"
	// envLegacyWorkbenchID is the sigild attribution id Kenaz has written
	// to kenaz-meta.env since the first KENAZMETA disk. It names the same
	// workbench, so it is the fallback for images booted by a Kenaz that
	// predates EnvWorkbenchID.
	envLegacyWorkbenchID = "SIGIL_WORKBENCH_ID"
)

// ClientKindHarness is the enroll `client_kind` for this program. Fleet
// keys the kind per node (sticky) and defaults an omitted kind to this
// value, so sending it explicitly changes nothing for existing nodes.
const ClientKindHarness = "kenaz-harness"

// maxNodeFactBytes bounds every free-text fact before it leaves the device.
const maxNodeFactBytes = 128

// nodeIDFactRe is the charset accepted for the ids the host hands over
// (ULID-like node ids, workbench slugs/UUIDs). Anything else is dropped,
// never sent.
var nodeIDFactRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// NodeFacts is what enroll sends about this install besides node_id,
// platform and version.
type NodeFacts struct {
	// Arch is runtime.GOARCH normalised to Fleet's enum: "amd64" | "arm64";
	// "" (omitted) for anything else.
	Arch string
	// OSVersion is a short, hostname-free description such as
	// "macOS 14.5", "NixOS 26.05", "Ubuntu 24.04" or "Windows 10.0.22631".
	// "" when it cannot be determined.
	OSVersion string
	// ClientKind is always ClientKindHarness.
	ClientKind string
	// InVM is true when the harness runs inside a Kenaz workbench VM
	// (a workbench id was handed to it at boot).
	InVM bool
	// VMID is the workbench id (only with InVM).
	VMID string
	// HostNodeID is the Kenaz host's Fleet node id (only with InVM; may be
	// empty when the host did not know its own id at VM start).
	HostNodeID string
}

var (
	nodeFactsMu       sync.RWMutex
	nodeFactsCached   *NodeFacts
	nodeFactsOverride *NodeFacts
)

// CurrentNodeFacts returns the facts enroll sends for this process. They are
// computed once: the env vars are fixed for the life of a served process
// (systemd sets them at boot) and the OS does not change underneath a
// running desktop app. A restart recomputes them, and both the desktop
// session supervisor and the served enroll supervisor enroll on every
// start, so a changed value reaches Fleet on the next boot (Fleet's enroll
// is an upsert of these columns; there is no PATCH).
func CurrentNodeFacts() NodeFacts {
	nodeFactsMu.RLock()
	if nodeFactsOverride != nil {
		f := *nodeFactsOverride
		nodeFactsMu.RUnlock()
		return f
	}
	if nodeFactsCached != nil {
		f := *nodeFactsCached
		nodeFactsMu.RUnlock()
		return f
	}
	nodeFactsMu.RUnlock()

	f := computeNodeFacts(os.Getenv, runtime.GOARCH, detectOSVersion())
	nodeFactsMu.Lock()
	if nodeFactsCached == nil {
		nodeFactsCached = &f
	}
	f = *nodeFactsCached
	nodeFactsMu.Unlock()
	return f
}

// SetNodeFactsForTesting pins the facts enroll sends and returns a restore
// func. Tests only.
func SetNodeFactsForTesting(f NodeFacts) (restore func()) {
	nodeFactsMu.Lock()
	prev := nodeFactsOverride
	nodeFactsOverride = &f
	nodeFactsMu.Unlock()
	return func() {
		nodeFactsMu.Lock()
		nodeFactsOverride = prev
		nodeFactsMu.Unlock()
	}
}

// computeNodeFacts is the pure core of CurrentNodeFacts: env lookup, GOARCH
// and the detected OS version in, wire-safe facts out.
func computeNodeFacts(getenv func(string) string, goarch, osVersion string) NodeFacts {
	f := NodeFacts{
		Arch:       normalizeArch(goarch),
		OSVersion:  cleanNodeFact(osVersion),
		ClientKind: ClientKindHarness,
	}
	vmID := strings.TrimSpace(getenv(EnvWorkbenchID))
	if vmID == "" {
		vmID = strings.TrimSpace(getenv(envLegacyWorkbenchID))
	}
	if nodeIDFactRe.MatchString(vmID) {
		f.InVM = true
		f.VMID = vmID
		if host := strings.TrimSpace(getenv(EnvHostNodeID)); nodeIDFactRe.MatchString(host) {
			f.HostNodeID = host
		}
	}
	return f
}

// normalizeArch maps GOARCH onto Fleet's enroll `arch` enum.
func normalizeArch(goarch string) string {
	switch goarch {
	case "amd64", "arm64":
		return goarch
	case "386":
		return "amd64" // 32-bit x86 has no Fleet value; report the family
	default:
		return ""
	}
}

// cleanNodeFact collapses whitespace, strips control characters and bounds
// the length of a free-text fact.
func cleanNodeFact(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > maxNodeFactBytes {
		cut := maxNodeFactBytes
		for cut > 0 && (s[cut]&0xC0) == 0x80 {
			cut--
		}
		s = s[:cut]
	}
	return s
}

// osReleaseVersion renders /etc/os-release content as "<NAME> <VERSION_ID>"
// ("NixOS 26.05", "Ubuntu 24.04"). PRETTY_NAME is deliberately not used:
// it can carry codenames and build strings that only widen the fingerprint.
func osReleaseVersion(content string) string {
	var name, version string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		switch k {
		case "NAME":
			name = v
		case "VERSION_ID":
			version = v
		}
	}
	if name == "" {
		return ""
	}
	// "NixOS" / "Ubuntu" / "Fedora Linux" — drop a trailing " Linux" so the
	// value stays short and comparable across distros.
	name = strings.TrimSuffix(name, " Linux")
	if version == "" {
		return name
	}
	return name + " " + version
}
