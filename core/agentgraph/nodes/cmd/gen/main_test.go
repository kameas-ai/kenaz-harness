package main

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/agentgraph/nodes"
)

// updateGolden, when set, rewrites the testdata/golden/*.go files
// from the current generator output. Use:
//
//	go test ./core/agentgraph/nodes/cmd/gen/ -run TestGoldenFiles -update
//
// Reviewers should manually inspect any golden delta before committing.
var updateGolden = flag.Bool("update", false, "regenerate golden files from current output")

const (
	fixtureDir = "testdata/manifests"
	goldenDir  = "testdata/golden"
)

// TestEmptyCatalog ensures the generator handles the "no callable kinds"
// case cleanly. WP02's shipped manifest set is exactly this case
// (archetypes only); WP04 lands kinds and the test in TestGoldenFiles
// covers the populated output.
func TestEmptyCatalog(t *testing.T) {
	t.Parallel()

	// Build a minimal archetype-only fixture: a single archetype that
	// declares callable=false. We don't reuse the testdata fixtures
	// because those include kind manifests; this test specifically
	// covers the empty-output branch in the templates.
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "_archetype.foo.yaml"), `
schema_version: "1"
archetype: foo
category: compute
description: "Test archetype."
callable: false
budget: none
`)

	cfg := genConfig{
		outDir:     t.TempDir(),
		fixtureDir: dir,
		pkgName:    "agentgraph",
	}
	if err := run(cfg); err != nil {
		t.Fatalf("run: %v", err)
	}

	attrs := mustRead(t, filepath.Join(cfg.outDir, "attrs_gen.go"))
	wire := mustRead(t, filepath.Join(cfg.outDir, "wire_gen.go"))

	if !strings.HasPrefix(string(attrs), generatedHeader) {
		t.Errorf("attrs_gen.go missing canonical header")
	}
	if !strings.HasPrefix(string(wire), generatedHeader) {
		t.Errorf("wire_gen.go missing canonical header")
	}
	if !strings.Contains(string(attrs), "package agentgraph") {
		t.Errorf("attrs_gen.go missing package declaration")
	}
	// Empty-catalog mode emits the "no callable kinds yet" sentinel
	// comment so reviewers can tell the file is real, not a stub.
	if !strings.Contains(string(attrs), "No callable kinds in the catalog yet") {
		t.Errorf("attrs_gen.go missing empty-catalog sentinel; got:\n%s", string(attrs))
	}
	if !strings.Contains(string(wire), "No callable kinds in the catalog yet") {
		t.Errorf("wire_gen.go missing empty-catalog sentinel; got:\n%s", string(wire))
	}
	ports := mustRead(t, filepath.Join(cfg.outDir, "ports_gen.go"))
	if !strings.HasPrefix(string(ports), generatedHeader) {
		t.Errorf("ports_gen.go missing canonical header")
	}
	if !strings.Contains(string(ports), "No callable kinds in the catalog yet") {
		t.Errorf("ports_gen.go missing empty-catalog sentinel; got:\n%s", string(ports))
	}
}

// TestArchetypeSkipped verifies that archetype manifests in the
// fixture set never produce per-kind structs in the generated output.
// The fixture set ships two archetypes (compute, control); only the
// kind manifests (model, decision) should round-trip into structs.
func TestArchetypeSkipped(t *testing.T) {
	t.Parallel()

	cfg := genConfig{
		outDir:     t.TempDir(),
		fixtureDir: fixtureDir,
		pkgName:    "agentgraph",
	}
	if err := run(cfg); err != nil {
		t.Fatalf("run: %v", err)
	}

	attrs := string(mustRead(t, filepath.Join(cfg.outDir, "attrs_gen.go")))
	ports := string(mustRead(t, filepath.Join(cfg.outDir, "ports_gen.go")))

	// Affirmative: kind structs present.
	if !strings.Contains(attrs, "type ModelAttrs struct") {
		t.Errorf("ModelAttrs missing from generated attrs")
	}
	if !strings.Contains(attrs, "type DecisionAttrs struct") {
		t.Errorf("DecisionAttrs missing from generated attrs")
	}
	// Negative: archetype names must NOT appear as struct types.
	if strings.Contains(attrs, "type ComputeAttrs struct") {
		t.Errorf("archetype 'compute' leaked into generated attrs as ComputeAttrs")
	}
	if strings.Contains(attrs, "type ControlAttrs struct") {
		t.Errorf("archetype 'control' leaked into generated attrs as ControlAttrs")
	}

	// ports_gen.go: typed port structs present for kind manifests.
	if !strings.Contains(ports, "type ModelInputs struct") {
		t.Errorf("ModelInputs missing from generated ports")
	}
	if !strings.Contains(ports, "type ModelOutputs struct") {
		t.Errorf("ModelOutputs missing from generated ports")
	}
	if !strings.Contains(ports, "func ReadModelInputs") {
		t.Errorf("ReadModelInputs accessor missing from generated ports")
	}
	// Archetype port structs must NOT appear.
	if strings.Contains(ports, "type ComputeInputs struct") {
		t.Errorf("archetype 'compute' leaked into generated ports as ComputeInputs")
	}
}

// TestValidatorBodies asserts that the generated Validate() bodies
// honour the manifest's required / min / max / enum constraints.
func TestValidatorBodies(t *testing.T) {
	t.Parallel()

	cfg := genConfig{
		outDir:     t.TempDir(),
		fixtureDir: fixtureDir,
		pkgName:    "agentgraph",
	}
	if err := run(cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	attrs := string(mustRead(t, filepath.Join(cfg.outDir, "attrs_gen.go")))

	// model.attrs.model: required=true → "model is required"
	if !strings.Contains(attrs, `"model: model is required (manifest constraint)"`) {
		t.Errorf("ModelAttrs.Validate missing required check on Model")
	}
	// model.attrs.max_tokens: min=1, max=100000
	if !strings.Contains(attrs, "max_tokens.min:") {
		t.Errorf("ModelAttrs.Validate missing min check on MaxTokens")
	}
	if !strings.Contains(attrs, "max_tokens.max:") {
		t.Errorf("ModelAttrs.Validate missing max check on MaxTokens")
	}
	// model.attrs.temperature: min=0.0, max=2.0
	if !strings.Contains(attrs, "temperature.min:") {
		t.Errorf("ModelAttrs.Validate missing min check on Temperature")
	}
	if !strings.Contains(attrs, "temperature.max:") {
		t.Errorf("ModelAttrs.Validate missing max check on Temperature")
	}
	// decision.attrs.mode: enum=[strict, lenient]
	if !strings.Contains(attrs, "mode.enum:") {
		t.Errorf("DecisionAttrs.Validate missing enum check on Mode")
	}
	// decision.attrs.condition: required
	if !strings.Contains(attrs, `"condition: condition is required (manifest constraint)"`) {
		t.Errorf("DecisionAttrs.Validate missing required check on Condition")
	}
}

// TestIdempotent runs the generator twice against the fixture set and
// asserts the output is byte-equal. This is the central NFR-004 / FR-009
// idempotency guarantee.
func TestIdempotent(t *testing.T) {
	t.Parallel()

	dir1 := t.TempDir()
	dir2 := t.TempDir()
	cfg1 := genConfig{outDir: dir1, fixtureDir: fixtureDir, pkgName: "agentgraph"}
	cfg2 := genConfig{outDir: dir2, fixtureDir: fixtureDir, pkgName: "agentgraph"}

	if err := run(cfg1); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := run(cfg2); err != nil {
		t.Fatalf("second run: %v", err)
	}

	for _, name := range []string{"attrs_gen.go", "wire_gen.go", "ports_gen.go", "manifest_versions_gen.go"} {
		a := mustRead(t, filepath.Join(dir1, name))
		b := mustRead(t, filepath.Join(dir2, name))
		if !bytes.Equal(a, b) {
			t.Errorf("%s differs between runs (idempotency violation)\n--- first ---\n%s\n--- second ---\n%s", name, a, b)
		}
	}
}

// TestIdempotentSameDir runs the generator twice against the SAME
// output directory and asserts the second run produces identical
// bytes. This catches re-write side effects (e.g., trailing newline
// drift) that the cross-directory test would miss.
func TestIdempotentSameDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfg := genConfig{outDir: dir, fixtureDir: fixtureDir, pkgName: "agentgraph"}

	if err := run(cfg); err != nil {
		t.Fatalf("first run: %v", err)
	}
	first := map[string][]byte{
		"attrs_gen.go":             mustRead(t, filepath.Join(dir, "attrs_gen.go")),
		"wire_gen.go":              mustRead(t, filepath.Join(dir, "wire_gen.go")),
		"ports_gen.go":             mustRead(t, filepath.Join(dir, "ports_gen.go")),
		"manifest_versions_gen.go": mustRead(t, filepath.Join(dir, "manifest_versions_gen.go")),
	}
	if err := run(cfg); err != nil {
		t.Fatalf("second run: %v", err)
	}
	for name, want := range first {
		got := mustRead(t, filepath.Join(dir, name))
		if !bytes.Equal(got, want) {
			t.Errorf("%s drifted on second run (idempotency violation)", name)
		}
	}
}

// TestGoldenFiles compares the generator's output against committed
// golden bytes. Run with -update to refresh the goldens after
// intentional changes.
func TestGoldenFiles(t *testing.T) {
	cfg := genConfig{
		outDir:     t.TempDir(),
		fixtureDir: fixtureDir,
		pkgName:    "agentgraph",
	}
	if err := run(cfg); err != nil {
		t.Fatalf("run: %v", err)
	}

	for _, name := range []string{"attrs_gen.go", "wire_gen.go", "ports_gen.go", "manifest_versions_gen.go"} {
		got := mustRead(t, filepath.Join(cfg.outDir, name))
		goldenPath := filepath.Join(goldenDir, name)
		if *updateGolden {
			if err := os.MkdirAll(goldenDir, 0o755); err != nil {
				t.Fatalf("mkdir golden: %v", err)
			}
			if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
				t.Fatalf("write golden %s: %v", goldenPath, err)
			}
			continue
		}
		want, err := os.ReadFile(goldenPath)
		if err != nil {
			t.Fatalf("read golden %s: %v (run -update to seed)", goldenPath, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s differs from golden\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
		}
	}
}

// TestSelectCallable verifies the kind selector is deterministic and
// strips archetypes. Drives off the loader's behaviour (we do not mock
// the catalog).
func TestSelectCallable(t *testing.T) {
	t.Parallel()

	cat, err := nodes.LoadCatalog(nodes.LoadOptions{
		SkipBundled: true,
		UserDir:     fixtureDir,
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got := selectCallable(cat)
	wantIDs := []string{"decision", "model"}
	if len(got) != len(wantIDs) {
		t.Fatalf("got %d kinds, want %d (%v)", len(got), len(wantIDs), wantIDs)
	}
	for i, rm := range got {
		if rm.Manifest.ID != wantIDs[i] {
			t.Errorf("position %d: got %q, want %q", i, rm.Manifest.ID, wantIDs[i])
		}
		if !rm.Manifest.IsCallable() {
			t.Errorf("kind %q reported non-callable but selector returned it", rm.Manifest.ID)
		}
	}
}

// TestPascalCase covers the snake/kebab-case → PascalCase conversion.
// Important because kind IDs go through it and dictate every Go-side
// identifier the rest of the codebase binds to.
func TestPascalCase(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"model", "Model"},
		{"history_read", "HistoryRead"},
		{"corpus-write", "CorpusWrite"},
		{"read_bash_output", "ReadBashOutput"},
		{"", ""},
		{"a", "A"},
	}
	for _, tc := range cases {
		got := pascalCase(tc.in)
		if got != tc.want {
			t.Errorf("pascalCase(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestGeneratedFilesParse round-trips the generator output through
// go/format. format.Source is already invoked inside renderAttrs /
// renderWire / renderPorts; this test verifies the output is valid by
// re-parsing it (parser invokes the lexer+parser, which is stricter
// than format).
func TestGeneratedFilesParse(t *testing.T) {
	t.Parallel()
	cfg := genConfig{
		outDir:     t.TempDir(),
		fixtureDir: fixtureDir,
		pkgName:    "agentgraph",
	}
	if err := run(cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, name := range []string{"attrs_gen.go", "wire_gen.go", "ports_gen.go"} {
		path := filepath.Join(cfg.outDir, name)
		raw := mustRead(t, path)
		// format.Source on already-formatted bytes is a no-op when
		// successful; parse failures bubble through as errors.
		if _, err := mustReformat(raw); err != nil {
			t.Errorf("%s did not re-parse: %v", name, err)
		}
	}
}

func mustReformat(src []byte) ([]byte, error) {
	return formatGo(string(src))
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
