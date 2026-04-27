package nodes_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/agentgraph/nodes"
)

// writeManifest is a small test helper that drops a YAML file under
// the caller-provided dir.
func writeManifest(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// TestResolverOneDeep: a kind extending an archetype gets the
// archetype's attrs/ports/budget merged in, with provenance correctly
// pointing at the archetype layer for inherited fields.
func TestResolverOneDeep(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifest(t, dir, "compute.yaml", `archetype: compute
category: compute
description: archetype-compute
ports:
  inputs:
    - {name: input, type: messages}
  outputs:
    - {name: output, type: messages}
attrs:
  provider: {type: string}
  temperature: {type: float, min: 0, max: 2}
budget: llm
`)
	writeManifest(t, dir, "model.yaml", `id: model
extends: compute
display_name: Model
executor: agentgraph.ExecModel
attrs:
  model: {type: string, required: true}
defaults:
  temperature: 0.7
`)
	cat, err := nodes.LoadCatalog(nodes.LoadOptions{SkipBundled: true, UserDir: dir})
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	rm, err := cat.Get("model")
	if err != nil {
		t.Fatalf("Get(model): %v", err)
	}
	if got, want := len(rm.Chain), 2; got != want {
		t.Fatalf("chain len: got %d, want %d (chain=%v)", got, want, rm.Chain)
	}
	if rm.Chain[0] != "compute" || rm.Chain[1] != "model" {
		t.Errorf("chain: %v, want [compute model]", rm.Chain)
	}
	// Inherited attrs from archetype.
	for _, name := range []string{"provider", "temperature"} {
		if _, ok := rm.Manifest.Attrs[name]; !ok {
			t.Errorf("missing inherited attr %q", name)
		}
	}
	if _, ok := rm.Manifest.Attrs["model"]; !ok {
		t.Error("missing self attr model")
	}
	if got, want := rm.Provenance["attrs.provider"], "compute"; got != want {
		t.Errorf("provenance attrs.provider: got %q, want %q", got, want)
	}
	if got, want := rm.Provenance["attrs.model"], "model"; got != want {
		t.Errorf("provenance attrs.model: got %q, want %q", got, want)
	}
	if rm.Manifest.Category != nodes.CategoryCompute {
		t.Errorf("category: got %q, want compute", rm.Manifest.Category)
	}
	if rm.Manifest.Budget != nodes.BudgetLLM {
		t.Errorf("budget: got %q, want llm", rm.Manifest.Budget)
	}
	if v, ok := rm.Manifest.Defaults["temperature"]; !ok || !floatEq(v, 0.7) {
		t.Errorf("defaults.temperature: got %v", v)
	}
	if !rm.Manifest.IsCallable() {
		t.Error("model should be callable")
	}
}

// TestResolverTwoDeep: state → write archetype chain.
func TestResolverTwoDeep(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifest(t, dir, "_archetype.state.yaml", `archetype: state
category: state
attrs:
  provenance: {type: bool, default: true}
budget: none
`)
	writeManifest(t, dir, "_archetype.write.yaml", `archetype: write
extends: state
ports:
  inputs:
    - {name: payload, type: any}
  outputs:
    - {name: ack, type: bool}
attrs:
  target:
    type: enum
    enum: [memory, corpus, trace, file, artifact]
    required: true
`)
	cat, err := nodes.LoadCatalog(nodes.LoadOptions{SkipBundled: true, UserDir: dir})
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	rm, err := cat.Get("write")
	if err != nil {
		t.Fatalf("Get(write): %v", err)
	}
	if got, want := len(rm.Chain), 2; got != want {
		t.Fatalf("chain len: got %d, want %d", got, want)
	}
	if _, ok := rm.Manifest.Attrs["provenance"]; !ok {
		t.Error("missing inherited provenance attr")
	}
	if rm.Provenance["attrs.provenance"] != "state" {
		t.Errorf("provenance.provenance: got %q", rm.Provenance["attrs.provenance"])
	}
	if rm.Manifest.Category != nodes.CategoryState {
		t.Errorf("category: got %q, want state", rm.Manifest.Category)
	}
	if rm.Manifest.IsCallable() {
		t.Error("write archetype should NOT be callable")
	}
}

// TestResolverThreeDeep verifies the spec amendment: state → read →
// <kind> resolves correctly. corpus_read is a placeholder name (real
// kind manifest lands in WP04); the test only cares about chain
// resolution mechanics.
func TestResolverThreeDeep(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifest(t, dir, "_archetype.state.yaml", `archetype: state
category: state
attrs:
  provenance: {type: bool, default: true}
`)
	writeManifest(t, dir, "_archetype.read.yaml", `archetype: read
extends: state
ports:
  inputs:
    - {name: query, type: any}
  outputs:
    - {name: result, type: messages}
attrs:
  source:
    type: enum
    enum: [history, corpus, memory, attachment, file, bash_output]
    required: true
`)
	writeManifest(t, dir, "corpus_read.yaml", `id: corpus_read
extends: read
display_name: Corpus Read
executor: agentgraph.ExecCorpusRead
attrs:
  corpus_ids: {type: "[]string"}
  top_k: {type: int, default: 10}
defaults:
  source: corpus
  top_k: 10
`)
	cat, err := nodes.LoadCatalog(nodes.LoadOptions{SkipBundled: true, UserDir: dir})
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	rm, err := cat.Get("corpus_read")
	if err != nil {
		t.Fatalf("Get(corpus_read): %v", err)
	}
	if got, want := len(rm.Chain), 3; got != want {
		t.Fatalf("chain len: got %d, want %d (chain=%v)", got, want, rm.Chain)
	}
	if rm.Chain[0] != "state" || rm.Chain[1] != "read" || rm.Chain[2] != "corpus_read" {
		t.Errorf("chain: %v, want [state read corpus_read]", rm.Chain)
	}
	for _, name := range []string{"provenance", "source", "corpus_ids", "top_k"} {
		if _, ok := rm.Manifest.Attrs[name]; !ok {
			t.Errorf("missing attr %q in 3-deep resolution", name)
		}
	}
	// Provenance traces each attr to its source layer.
	wantProv := map[string]string{
		"attrs.provenance": "state",
		"attrs.source":     "read",
		"attrs.corpus_ids": "corpus_read",
		"attrs.top_k":      "corpus_read",
	}
	for k, v := range wantProv {
		if rm.Provenance[k] != v {
			t.Errorf("provenance[%s]: got %q, want %q", k, rm.Provenance[k], v)
		}
	}
	if v, ok := rm.Manifest.Defaults["source"]; !ok || v != "corpus" {
		t.Errorf("defaults.source: got %v", v)
	}
	if got, want := len(rm.Manifest.Ports.Outputs), 1; got != want {
		t.Errorf("ports.outputs len: got %d, want %d", got, want)
	}
	if rm.Manifest.Ports.Outputs[0].Name != "result" {
		t.Errorf("ports.outputs[0].name: got %q, want result", rm.Manifest.Ports.Outputs[0].Name)
	}
	if rm.Manifest.Category != nodes.CategoryState {
		t.Errorf("category: got %q, want state", rm.Manifest.Category)
	}
	if rm.Provenance["category"] != "state" {
		t.Errorf("provenance.category: got %q, want state", rm.Provenance["category"])
	}
	if !rm.Manifest.IsCallable() {
		t.Error("corpus_read should be callable")
	}
	if rm.Manifest.Executor != "agentgraph.ExecCorpusRead" {
		t.Errorf("executor: got %q", rm.Manifest.Executor)
	}
}

// TestResolverChildOverride: a kind that re-declares an inherited attr
// wins over the archetype, with provenance updated.
func TestResolverChildOverride(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifest(t, dir, "compute.yaml", `archetype: compute
category: compute
attrs:
  temperature: {type: float, min: 0.0, max: 2.0, default: 0.5}
budget: llm
`)
	writeManifest(t, dir, "ask.yaml", `id: ask
extends: compute
executor: agentgraph.ExecAsk
budget: none
attrs:
  temperature: {type: float, min: 0.0, max: 1.0, default: 0.0}
`)
	cat, err := nodes.LoadCatalog(nodes.LoadOptions{SkipBundled: true, UserDir: dir})
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	rm, err := cat.Get("ask")
	if err != nil {
		t.Fatalf("Get(ask): %v", err)
	}
	temp := rm.Manifest.Attrs["temperature"]
	if temp.Max == nil || *temp.Max != 1.0 {
		t.Errorf("ask.temperature.max: got %v, want 1.0 (overridden)", temp.Max)
	}
	if rm.Provenance["attrs.temperature"] != "ask" {
		t.Errorf("provenance: got %q, want ask", rm.Provenance["attrs.temperature"])
	}
	// Budget override: leaf wins.
	if rm.Manifest.Budget != nodes.BudgetNone {
		t.Errorf("budget: got %q, want none", rm.Manifest.Budget)
	}
	if rm.Provenance["budget"] != "ask" {
		t.Errorf("provenance.budget: got %q, want ask", rm.Provenance["budget"])
	}
}

// TestResolverCycleDetection: a → b, b → a produces ErrCycleDetected.
func TestResolverCycleDetection(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifest(t, dir, "a.yaml", `id: a
extends: b
`)
	writeManifest(t, dir, "b.yaml", `id: b
extends: a
`)
	_, err := nodes.LoadCatalog(nodes.LoadOptions{SkipBundled: true, UserDir: dir})
	if err == nil {
		t.Fatal("expected cycle detection error")
	}
	if !errors.Is(err, nodes.ErrCycleDetected) {
		t.Errorf("expected ErrCycleDetected, got: %v", err)
	}
}

// TestResolverSelfCycle: a manifest extending itself rejected.
func TestResolverSelfCycle(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifest(t, dir, "a.yaml", `id: a
extends: a
`)
	_, err := nodes.LoadCatalog(nodes.LoadOptions{SkipBundled: true, UserDir: dir})
	if err == nil {
		t.Fatal("expected self-cycle detection")
	}
	if !errors.Is(err, nodes.ErrCycleDetected) {
		t.Errorf("expected ErrCycleDetected, got: %v", err)
	}
}

// TestResolverMissingParent: extending a non-existent archetype is a
// load-time error.
func TestResolverMissingParent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifest(t, dir, "x.yaml", `id: x
extends: nonexistent
executor: agentgraph.ExecX
`)
	_, err := nodes.LoadCatalog(nodes.LoadOptions{SkipBundled: true, UserDir: dir})
	if err == nil {
		t.Fatal("expected missing-parent error")
	}
}

// TestResolverPortReplaceWholeList: a kind that declares its own ports
// replaces the archetype's ports entirely (FR-004 list-replace rule).
func TestResolverPortReplaceWholeList(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifest(t, dir, "compute.yaml", `archetype: compute
category: compute
ports:
  inputs:
    - {name: input, type: messages}
  outputs:
    - {name: output, type: messages}
budget: llm
`)
	writeManifest(t, dir, "compact.yaml", `id: compact
extends: compute
executor: agentgraph.ExecCompact
ports:
  outputs:
    - {name: branch_result, type: any}
`)
	cat, err := nodes.LoadCatalog(nodes.LoadOptions{SkipBundled: true, UserDir: dir})
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	rm, err := cat.Get("compact")
	if err != nil {
		t.Fatalf("Get(compact): %v", err)
	}
	// Inputs are inherited (kind didn't declare any).
	if got, want := len(rm.Manifest.Ports.Inputs), 1; got != want {
		t.Errorf("ports.inputs: got %d, want %d", got, want)
	}
	// Outputs were redeclared at the kind layer; the archetype's
	// `output: messages` is replaced by `branch_result: any`.
	if got, want := len(rm.Manifest.Ports.Outputs), 1; got != want {
		t.Fatalf("ports.outputs: got %d, want %d", got, want)
	}
	if rm.Manifest.Ports.Outputs[0].Name != "branch_result" {
		t.Errorf("ports.outputs[0]: got %q, want branch_result", rm.Manifest.Ports.Outputs[0].Name)
	}
	if rm.Provenance["ports.outputs"] != "compact" {
		t.Errorf("provenance ports.outputs: got %q, want compact", rm.Provenance["ports.outputs"])
	}
	if rm.Provenance["ports.inputs"] != "compute" {
		t.Errorf("provenance ports.inputs: got %q, want compute", rm.Provenance["ports.inputs"])
	}
}

func floatEq(v interface{}, want float64) bool {
	switch x := v.(type) {
	case float64:
		return x == want
	case int:
		return float64(x) == want
	case int64:
		return float64(x) == want
	}
	return false
}
