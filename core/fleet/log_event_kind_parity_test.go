package fleet

// log_event_kind_parity_test.go — the anti-drift gate for the vendored Fleet
// kind schema.
//
// Same shape as kenaz's vendored harness MCP connector catalog
// (internal/connector/catalog/parity_test.go): a vendored data file, a
// source.json pin recording where it came from, and two assertions —
//
//  1. sha256(schema/kinds.json) == schema/source.json.sha256. Unconditional;
//     the sidecar and the bytes cannot disagree.
//  2. The vendored table equals the Kind/kindToClass table in kenaz-fleet
//     service/telemetry/schema/v1/kind.go + class.go. Needs a kenaz-fleet
//     checkout: KENAZ_FLEET_REPO names it, else a walk-up finds a sibling
//     `kenaz-fleet`. Absent checkout ⇒ skip with instructions, so a laptop
//     without the sibling repo does not fail `go test ./...`.
//
// Failure direction is safe either way: an out-of-date harness under-reports
// (a kind Fleet added is simply never emitted), it never over-shares. Fleet
// adding a ninth kind therefore breaks nothing at runtime — it shows up here
// as a red parity test the next time someone runs the cross-repo gate.

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestVendoredKindSchemaMatchesItsSHA(t *testing.T) {
	t.Parallel()
	got := VendoredKindSchemaSHA256()
	want := VendoredKindSchemaSource().SHA256
	if got != want {
		t.Fatalf("vendored schema/kinds.json drifted from its sidecar:\n"+
			"  sha256(kinds.json)  = %s\n"+
			"  source.json.sha256  = %s\n"+
			"re-vendor both files in the same commit", got, want)
	}
}

func TestVendoredKindSchemaSourceIsPinned(t *testing.T) {
	t.Parallel()
	src := VendoredKindSchemaSource()
	if src.FleetRev == "" {
		t.Error("schema/source.json has no fleet_rev — the vendored table has no provenance")
	}
	if src.SourcePath == "" {
		t.Error("schema/source.json has no source_path")
	}
	if len(CeilingLogEventKinds()) == 0 {
		t.Error("the compiled ceiling is empty; the embed did not load")
	}
}

// ── cross-repo parity ────────────────────────────────────────────────────────

var (
	// KindHarnessToolInvoked Kind = "harness.tool_invoked"
	fleetKindConstRe = regexp.MustCompile(`(?m)^\s*Kind\w+\s+Kind\s*=\s*"([^"]+)"`)
	// KindHarnessToolInvoked: ClassHarnessToolCalls,
	fleetKindToClassRe = regexp.MustCompile(`(?m)^\s*(Kind\w+):\s*(Class\w+),`)
	// KindHarnessToolInvoked Kind = "harness.tool_invoked"  → const name → value
	fleetKindConstNameRe = regexp.MustCompile(`(?m)^\s*(Kind\w+)\s+Kind\s*=\s*"([^"]+)"`)
	// ClassHarnessToolCalls Class = "harness.tool_calls"
	fleetClassConstRe = regexp.MustCompile(`(?m)^\s*(Class\w+)\s+Class\s*=\s*"([^"]+)"`)
)

// findFleetSchemaDir locates kenaz-fleet's service/telemetry/schema/v1.
func findFleetSchemaDir(t *testing.T) (string, bool) {
	t.Helper()
	const rel = "service/telemetry/schema/v1"
	if repo := os.Getenv("KENAZ_FLEET_REPO"); repo != "" {
		p := filepath.Join(repo, rel)
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("KENAZ_FLEET_REPO is set (%q) but %s is not readable: %v", repo, p, err)
		}
		return p, true
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for range 8 {
		p := filepath.Join(dir, "kenaz-fleet", rel)
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", false
}

// TestVendoredKindSchemaMatchesFleetSource is the cross-repo half of the gate:
// the vendored kind→class table must equal what kenaz-fleet actually compiles.
func TestVendoredKindSchemaMatchesFleetSource(t *testing.T) {
	t.Parallel()

	schemaDir, ok := findFleetSchemaDir(t)
	if !ok {
		t.Skip("kenaz-fleet checkout not found: set KENAZ_FLEET_REPO to its root " +
			"(or clone it as a sibling of this repo) to run the kind-schema parity gate")
	}

	kindSrc, err := os.ReadFile(filepath.Join(schemaDir, "kind.go"))
	if err != nil {
		t.Fatalf("read kind.go: %v", err)
	}
	classSrc, err := os.ReadFile(filepath.Join(schemaDir, "class.go"))
	if err != nil {
		t.Fatalf("read class.go: %v", err)
	}

	// const name → kind string
	kindByConst := map[string]string{}
	for _, m := range fleetKindConstNameRe.FindAllStringSubmatch(string(kindSrc), -1) {
		kindByConst[m[1]] = m[2]
	}
	if len(kindByConst) == 0 {
		t.Fatalf("no Kind constants parsed from %s/kind.go — the gate's regexes are stale", schemaDir)
	}

	// const name → class string
	classByConst := map[string]string{}
	for _, m := range fleetClassConstRe.FindAllStringSubmatch(string(classSrc), -1) {
		classByConst[m[1]] = m[2]
	}
	if len(classByConst) == 0 {
		t.Fatalf("no Class constants parsed from %s/class.go — the gate's regexes are stale", schemaDir)
	}

	// kindToClass entries live in class.go.
	upstream := map[string]string{}
	for _, m := range fleetKindToClassRe.FindAllStringSubmatch(string(classSrc), -1) {
		kind, kok := kindByConst[m[1]]
		class, cok := classByConst[m[2]]
		if !kok || !cok {
			t.Fatalf("kindToClass row %s: %s references a constant this gate could not resolve", m[1], m[2])
		}
		upstream[kind] = class
	}
	if len(upstream) == 0 {
		t.Fatalf("no kindToClass rows parsed from %s/class.go — the gate's regexes are stale", schemaDir)
	}

	// Sanity: every declared Kind should be mapped upstream. If Fleet ever
	// declares a Kind with no class, ClassFor returns !ok for it and the
	// receiver drops it, so it is correctly absent from our ceiling too.
	if len(upstream) != len(kindByConst) {
		t.Logf("note: kenaz-fleet declares %d Kind constants but maps %d in kindToClass; "+
			"unmapped kinds are not admissible upstream and are correctly absent here",
			len(kindByConst), len(upstream))
	}

	// Compare against the vendored ceiling.
	var diffs []string
	for kind, class := range upstream {
		got, ok := LogEventKindClass(kind)
		if !ok {
			diffs = append(diffs, "MISSING from vendored table: "+kind+" ("+class+")")
			continue
		}
		if got != class {
			diffs = append(diffs, "class mismatch for "+kind+": vendored="+got+" upstream="+class)
		}
	}
	for _, kind := range CeilingLogEventKinds() {
		if _, ok := upstream[kind]; !ok {
			diffs = append(diffs, "EXTRA in vendored table (not in kenaz-fleet): "+kind)
		}
	}

	if len(diffs) > 0 {
		sort.Strings(diffs)
		t.Fatalf("vendored kind schema has drifted from kenaz-fleet %s:\n  %s\n\n"+
			"Re-vendor core/fleet/schema/kinds.json from %s, update schema/source.json "+
			"(fleet_rev + sha256) in the same commit, and cite the kenaz-fleet rev in the PR body.\n"+
			"Note: drift is safe at runtime — an out-of-date ceiling under-reports, it never "+
			"over-shares — so this is a housekeeping failure, not an incident.",
			VendoredKindSchemaSource().FleetRev, strings.Join(diffs, "\n  "), schemaDir)
	}
}
