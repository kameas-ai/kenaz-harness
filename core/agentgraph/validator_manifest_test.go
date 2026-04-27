package agentgraph

import (
	"errors"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/agentgraph/nodes"
)

// fakeCatalog is a tiny test double for CatalogProvider. It lets tests
// register one manifest per kind without going through the YAML
// loader, so the assertions are deterministic and decoupled from any
// shipped manifest fixture.
type fakeCatalog struct {
	byID map[string]*nodes.ResolvedManifest
}

func newFakeCatalog() *fakeCatalog {
	return &fakeCatalog{byID: map[string]*nodes.ResolvedManifest{}}
}

func (f *fakeCatalog) Put(id string, m nodes.Manifest) {
	m.ID = id
	if m.SchemaVersion == "" {
		m.SchemaVersion = nodes.SchemaVersion
	}
	if m.KindName == "" {
		m.KindName = id
	}
	f.byID[id] = &nodes.ResolvedManifest{
		Manifest: m,
		Chain:    []string{id},
	}
}

func (f *fakeCatalog) Get(id string) (*nodes.ResolvedManifest, error) {
	rm, ok := f.byID[id]
	if !ok {
		return nil, nodes.ErrUnknownKind
	}
	return rm, nil
}

func (f *fakeCatalog) IsCallable(id string) bool {
	rm, ok := f.byID[id]
	if !ok {
		return false
	}
	return rm.Manifest.IsCallable()
}

// floatPtr / intPtr are local helpers; the AttrSpec uses *float64 /
// *int so we can distinguish "0" from "unset".
func floatPtr(f float64) *float64 { return &f }
func intPtr(i int) *int           { return &i }

// callableTrue returns a *bool pointing at true; manifests that omit
// `callable:` default to true via ApplyCallableDefault, but tests
// build manifests directly so they need to set the pointer.
func callableTrue() *bool {
	t := true
	return &t
}

func callableFalse() *bool {
	f := false
	return &f
}

// llmKindManifest is a minimal kind manifest that mirrors ModelAttrs's
// rules expressed in the manifest schema. Used by the manifest-driven
// tests to drive each rule (required, min/max, enum, length).
//
// The kind id matches NodeKindModel ("llm") so the validator's catalog
// lookup finds it for any ModelAttrs-bearing node in the test fixtures.
func llmKindManifest() nodes.Manifest {
	return nodes.Manifest{
		Archetype:   "",
		Category:    nodes.CategoryCompute,
		Callable:    callableTrue(),
		Description: "LLM kind manifest used in WP03 tests.",
		Attrs: map[string]nodes.AttrSpec{
			"model": {
				Type:     nodes.AttrTypeString,
				Required: true,
			},
			"max_tokens": {
				Type: nodes.AttrTypeInt,
				Min:  floatPtr(1),
				Max:  floatPtr(1_000_000),
			},
			"temperature": {
				Type: nodes.AttrTypeFloat,
				Min:  floatPtr(0),
				Max:  floatPtr(2),
			},
			"tool_allowlist": {
				Type:      nodes.AttrTypeStringSlice,
				MinLength: intPtr(0),
				MaxLength: intPtr(8),
			},
		},
		Ports: nodes.PortSet{
			Inputs: []nodes.PortSpec{
				{Name: "messages", Type: string(PortTypeMessages), Required: true},
			},
			Outputs: []nodes.PortSpec{
				{Name: "response", Type: string(PortTypeMessages)},
			},
		},
	}
}

// reflectKindManifest mirrors ReflectAttrs's enum + non-negative
// constraint. Used for the enum-violation test.
func reflectKindManifest() nodes.Manifest {
	return nodes.Manifest{
		Category: nodes.CategoryCompute,
		Callable: callableTrue(),
		Attrs: map[string]nodes.AttrSpec{
			"severity_threshold": {
				Type: nodes.AttrTypeEnum,
				Enum: []string{"low", "medium", "high"},
			},
			"max_iterations": {
				Type: nodes.AttrTypeInt,
				Min:  floatPtr(0),
			},
		},
		Ports: nodes.PortSet{
			Inputs: []nodes.PortSpec{
				{Name: "draft", Type: string(PortTypeMessages)},
			},
			Outputs: []nodes.PortSpec{
				{Name: "critique", Type: string(PortTypeJSON)},
				{Name: "revision", Type: string(PortTypeMessages)},
			},
		},
	}
}

// TestManifestValidation_RequiredAttrViolation: when the manifest
// marks an attr `required: true`, an empty value on the node must
// surface a manifest-attributed error (FR-015).
func TestManifestValidation_RequiredAttrViolation(t *testing.T) {
	t.Parallel()

	cat := newFakeCatalog()
	cat.Put(string(NodeKindModel), llmKindManifest())

	// ModelAttrs.Model is "" — manifest declares it required.
	g := Graph{
		SpecVersion: SpecVersion,
		ID:          "g",
		Entrypoints: []string{"a"},
		Nodes: []Node{
			{
				ID:    "a",
				Kind:  NodeKindModel,
				Attrs: ModelAttrs{Model: ""},
			},
		},
	}
	err := Validate(g, WithCatalog(cat))
	if err == nil {
		t.Fatalf("expected required-attr violation; got nil")
	}
	ve := mustValidationError(t, err)
	wantSubstrs := []string{
		`node "a"`,
		"manifest model.attrs.model.required",
	}
	for _, s := range wantSubstrs {
		if !ve.Has(s) {
			t.Errorf("error missing %q: %v", s, ve.Error())
		}
	}
}

// TestManifestValidation_MinMaxViolation: numeric min/max bounds
// surface FR-015 errors that name the constraint that fired.
func TestManifestValidation_MinMaxViolation(t *testing.T) {
	t.Parallel()

	cat := newFakeCatalog()
	cat.Put(string(NodeKindModel), llmKindManifest())

	cases := []struct {
		name    string
		attrs   ModelAttrs
		mustHas []string
	}{
		{
			name:  "max_tokens_below_min",
			attrs: ModelAttrs{Model: "m", MaxTokens: -1},
			mustHas: []string{
				`node "a"`,
				"manifest model.attrs.max_tokens.min",
				"got -1",
				"want >= 1",
			},
		},
		{
			name:  "temperature_above_max",
			attrs: ModelAttrs{Model: "m", Temperature: 3.5},
			mustHas: []string{
				"manifest model.attrs.temperature.max",
				"got 3.5",
				"want <= 2",
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := Graph{
				SpecVersion: SpecVersion,
				ID:          "g",
				Entrypoints: []string{"a"},
				Nodes: []Node{
					{ID: "a", Kind: NodeKindModel, Attrs: tc.attrs},
				},
			}
			err := Validate(g, WithCatalog(cat))
			if err == nil {
				t.Fatalf("expected violation; got nil")
			}
			ve := mustValidationError(t, err)
			for _, s := range tc.mustHas {
				if !ve.Has(s) {
					t.Errorf("error missing %q: %v", s, ve.Error())
				}
			}
		})
	}
}

// TestManifestValidation_EnumViolation: a non-allowed enum value
// fires the manifest enum rule with the offending value + allowed
// set in the message.
func TestManifestValidation_EnumViolation(t *testing.T) {
	t.Parallel()

	cat := newFakeCatalog()
	cat.Put(string(NodeKindReflect), reflectKindManifest())

	g := Graph{
		SpecVersion: SpecVersion,
		ID:          "g",
		Entrypoints: []string{"r"},
		Nodes: []Node{
			{
				ID:   "r",
				Kind: NodeKindReflect,
				Attrs: ReflectAttrs{
					SeverityThreshold: "shouty",
				},
			},
		},
	}
	err := Validate(g, WithCatalog(cat))
	if err == nil {
		t.Fatalf("expected enum violation; got nil")
	}
	ve := mustValidationError(t, err)
	for _, s := range []string{
		"manifest reflect.attrs.severity_threshold.enum",
		`got "shouty"`,
		"low|medium|high",
	} {
		if !ve.Has(s) {
			t.Errorf("error missing %q: %v", s, ve.Error())
		}
	}
}

// TestManifestValidation_ListBoundsViolation: a list attr exceeds the
// manifest's max_length.
func TestManifestValidation_ListBoundsViolation(t *testing.T) {
	t.Parallel()

	cat := newFakeCatalog()
	cat.Put(string(NodeKindModel), llmKindManifest())

	tools := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} // 9 > 8
	g := Graph{
		SpecVersion: SpecVersion,
		ID:          "g",
		Entrypoints: []string{"a"},
		Nodes: []Node{
			{
				ID:    "a",
				Kind:  NodeKindModel,
				Attrs: ModelAttrs{Model: "m", ToolAllowlist: tools},
			},
		},
	}
	err := Validate(g, WithCatalog(cat))
	if err == nil {
		t.Fatalf("expected list-length violation; got nil")
	}
	ve := mustValidationError(t, err)
	for _, s := range []string{
		"manifest model.attrs.tool_allowlist.max_length",
		"len=9",
		"want <= 8",
	} {
		if !ve.Has(s) {
			t.Errorf("error missing %q: %v", s, ve.Error())
		}
	}
}

// TestManifestValidation_PortTypeMismatch: a node whose explicit port
// type disagrees with the manifest's declared type fires a port-rule
// error attributed to the manifest port path.
func TestManifestValidation_PortTypeMismatch(t *testing.T) {
	t.Parallel()

	cat := newFakeCatalog()
	cat.Put(string(NodeKindModel), llmKindManifest())

	g := Graph{
		SpecVersion: SpecVersion,
		ID:          "g",
		Entrypoints: []string{"a"},
		Nodes: []Node{
			{
				ID:   "a",
				Kind: NodeKindModel,
				// Override the input port with an incompatible type.
				Inputs: []Port{
					{Name: "messages", Type: PortTypeBool, Required: true},
				},
				Attrs: ModelAttrs{Model: "m"},
			},
		},
	}
	err := Validate(g, WithCatalog(cat))
	if err == nil {
		t.Fatalf("expected port-type mismatch; got nil")
	}
	ve := mustValidationError(t, err)
	for _, s := range []string{
		"manifest model.ports.inputs.messages.type",
		"got bool",
		"compatible with messages",
	} {
		if !ve.Has(s) {
			t.Errorf("error missing %q: %v", s, ve.Error())
		}
	}
}

// TestManifestValidation_RequiredInputNotConnected confirms the
// existing checkEdges rule keeps firing under the manifest-driven
// regime (FR-014: required-input-must-be-connected stays hand-coded
// because it's a graph-level concern not a manifest-attr rule). The
// manifest path adds an additional layer; the pre-existing graph rule
// is the authoritative one.
func TestManifestValidation_RequiredInputNotConnected(t *testing.T) {
	t.Parallel()

	cat := newFakeCatalog()
	cat.Put(string(NodeKindModel), llmKindManifest())

	// Two LLM nodes, neither connected. The non-entrypoint node has
	// `messages` required on the default surface.
	g := Graph{
		SpecVersion: SpecVersion,
		ID:          "g",
		Entrypoints: []string{"a"},
		Nodes: []Node{
			{ID: "a", Kind: NodeKindModel, Attrs: ModelAttrs{Model: "m"}},
			{ID: "b", Kind: NodeKindModel, Attrs: ModelAttrs{Model: "m"}},
		},
	}
	err := Validate(g, WithCatalog(cat))
	if err == nil {
		t.Fatalf("expected required-input-not-connected; got nil")
	}
	ve := mustValidationError(t, err)
	if !ve.Has("required input b.messages is not connected") {
		t.Errorf("missing required-input rule: %v", ve.Error())
	}
}

// TestManifestValidation_ArchetypeNotCallable (FR-016): a graph that
// names an archetype id directly fails to load with a clear message
// citing alternatives.
func TestManifestValidation_ArchetypeNotCallable(t *testing.T) {
	t.Parallel()

	cat := newFakeCatalog()
	// Register an archetype manifest under id "read".
	cat.Put("read", nodes.Manifest{
		Archetype: "read",
		Category:  nodes.CategoryState,
		Callable:  callableFalse(),
	})
	// Register a couple of concrete kinds whose chain includes the
	// archetype, so the alternatives suffix can populate.
	// (Note: fakeCatalog doesn't drive Chain via inheritance — so we
	// just exercise the message format with the empty alternatives
	// path. The shipped *nodes.Catalog populates ListByArchetype via
	// the resolver chain, which the fake doesn't emulate.)

	g := Graph{
		SpecVersion: SpecVersion,
		ID:          "g",
		Entrypoints: []string{"r"},
		Nodes: []Node{
			{ID: "r", Kind: NodeKind("read"), Attrs: nil},
		},
	}
	err := Validate(g, WithCatalog(cat))
	if err == nil {
		t.Fatalf("expected archetype-not-callable; got nil")
	}
	ve := mustValidationError(t, err)
	for _, s := range []string{
		`node "r"`,
		`archetype "read" is not directly callable in v1`,
	} {
		if !ve.Has(s) {
			t.Errorf("error missing %q: %v", s, ve.Error())
		}
	}
	// The "use a concrete kind" suffix populates from
	// ListByArchetype(); since fakeCatalog isn't a *nodes.Catalog,
	// the suffix is empty here. We assert the prefix only.
}

// TestManifestValidation_ArchetypeNotCallable_RealCatalog drives the
// rule through a real *nodes.Catalog loaded from the embedded shipped
// manifests. The shipped set today contains only archetype manifests
// (`compute`, `control`, `state`, `read`, `write`, `marker`) so a
// graph that names any of them must fail.
func TestManifestValidation_ArchetypeNotCallable_RealCatalog(t *testing.T) {
	t.Parallel()

	cat, err := nodes.LoadCatalog(nodes.LoadOptions{})
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	if cat == nil {
		t.Fatalf("LoadCatalog returned nil")
	}

	g := Graph{
		SpecVersion: SpecVersion,
		ID:          "g",
		Entrypoints: []string{"r"},
		Nodes: []Node{
			{ID: "r", Kind: NodeKind("read"), Attrs: nil},
		},
	}
	verr := Validate(g, WithCatalog(cat))
	if verr == nil {
		t.Fatalf("expected archetype rule to fire on shipped catalog")
	}
	ve := mustValidationError(t, verr)
	if !ve.Has(`archetype "read" is not directly callable`) {
		t.Errorf("missing archetype rule: %v", ve.Error())
	}
}

// TestManifestValidation_UnknownKind: a kind the catalog does not
// know AND that the legacy IsKnown set rejects fires the
// unknown-kind error.
func TestManifestValidation_UnknownKind(t *testing.T) {
	t.Parallel()

	cat := newFakeCatalog() // empty — no kinds registered

	g := Graph{
		SpecVersion: SpecVersion,
		ID:          "g",
		Entrypoints: []string{"a"},
		Nodes: []Node{
			{ID: "a", Kind: NodeKind("definitely-not-a-kind"), Attrs: nil},
		},
	}
	err := Validate(g, WithCatalog(cat))
	if err == nil {
		t.Fatalf("expected unknown-kind; got nil")
	}
	ve := mustValidationError(t, err)
	if !ve.Has("unknown kind") {
		t.Errorf("missing unknown-kind: %v", ve.Error())
	}
}

// TestManifestValidation_FallbackPath_LegacyKind confirms that when
// the explicit fakeCatalog has no manifest for the kind, the
// codegen-emitted *Attrs.Validate() (sourced from the SHIPPED
// manifests) still fires. As of WP04 the hand-written Validate()
// methods are gone — every per-kind rule is now codegen-emitted from
// the manifest. The "fallback" path therefore relies on the bundled
// catalog alone.
func TestManifestValidation_FallbackPath_LegacyKind(t *testing.T) {
	t.Parallel()

	cat := newFakeCatalog() // no model manifest in the fake catalog

	// ModelAttrs.Model = "" — the codegen-emitted Validate() rejects
	// because the shipped model.yaml marks `model:` as required.
	g := Graph{
		SpecVersion: SpecVersion,
		ID:          "g",
		Entrypoints: []string{"a"},
		Nodes: []Node{
			{ID: "a", Kind: NodeKindModel, Attrs: ModelAttrs{}},
		},
	}
	err := Validate(g, WithCatalog(cat))
	if err == nil {
		t.Fatalf("expected codegen-emitted rule to fire")
	}
	ve := mustValidationError(t, err)
	if !ve.Has("model: model is required") {
		t.Errorf("expected codegen-emitted rule message: %v", ve.Error())
	}
}

// TestManifestValidation_NoCatalogInjection_UsesPackageDefault
// confirms that a Validate call with no WithCatalog option still
// loads the shipped catalog so the archetype rule fires.
func TestManifestValidation_NoCatalogInjection_UsesPackageDefault(t *testing.T) {
	t.Parallel()

	g := Graph{
		SpecVersion: SpecVersion,
		ID:          "g",
		Entrypoints: []string{"r"},
		Nodes: []Node{
			{ID: "r", Kind: NodeKind("compute"), Attrs: nil},
		},
	}
	err := Validate(g)
	if err == nil {
		t.Fatalf("expected archetype rule to fire via package default")
	}
	ve := mustValidationError(t, err)
	if !ve.Has(`archetype "compute" is not directly callable`) {
		t.Errorf("missing archetype rule from default catalog: %v", ve.Error())
	}
}

// TestManifestValidation_AttrsToMap_Helpers exercises the conversion
// helpers directly so a bug in the JSON round-trip surfaces here
// rather than as a downstream validator regression.
func TestManifestValidation_AttrsToMap_Helpers(t *testing.T) {
	t.Parallel()

	// nil attrs → empty map.
	m, err := attrsToMap(nil)
	if err != nil {
		t.Fatalf("attrsToMap(nil): %v", err)
	}
	if len(m) != 0 {
		t.Errorf("attrsToMap(nil) = %v; want empty", m)
	}

	// Typed attrs round-trip.
	m, err = attrsToMap(ModelAttrs{Model: "claude-opus", MaxTokens: 512})
	if err != nil {
		t.Fatalf("attrsToMap(ModelAttrs): %v", err)
	}
	if m["model"] != "claude-opus" {
		t.Errorf("model lost: %v", m)
	}
}

// TestManifestValidation_PortMissing: the manifest declares a port
// the node's effective surface lacks.
func TestManifestValidation_PortMissing(t *testing.T) {
	t.Parallel()

	cat := newFakeCatalog()
	// Manifest demands an output named `extra`; the LLM default
	// surface only emits `response`.
	m := llmKindManifest()
	m.Ports.Outputs = append(m.Ports.Outputs, nodes.PortSpec{
		Name: "extra",
		Type: string(PortTypeJSON),
	})
	cat.Put(string(NodeKindModel), m)

	g := Graph{
		SpecVersion: SpecVersion,
		ID:          "g",
		Entrypoints: []string{"a"},
		Nodes: []Node{
			{ID: "a", Kind: NodeKindModel, Attrs: ModelAttrs{Model: "m"}},
		},
	}
	err := Validate(g, WithCatalog(cat))
	if err == nil {
		t.Fatalf("expected missing-port violation; got nil")
	}
	ve := mustValidationError(t, err)
	for _, s := range []string{
		"manifest model.ports.outputs.extra.missing",
		"effective surface omits port",
	} {
		if !ve.Has(s) {
			t.Errorf("error missing %q: %v", s, ve.Error())
		}
	}
}

// TestManifestValidation_FormatNum smoke-tests the number formatter
// used in the FR-015 messages.
func TestManifestValidation_FormatNum(t *testing.T) {
	t.Parallel()

	if got := formatNum(1); got != "1" {
		t.Errorf("formatNum(1) = %q; want 1", got)
	}
	if got := formatNum(-1); got != "-1" {
		t.Errorf("formatNum(-1) = %q; want -1", got)
	}
	if got := formatNum(2.5); got != "2.5" {
		t.Errorf("formatNum(2.5) = %q; want 2.5", got)
	}
}

// mustValidationError extracts a *ValidationError from err or fails
// the test. Helps every assertion stay focused on the rule fired.
func mustValidationError(t *testing.T, err error) *ValidationError {
	t.Helper()
	ve := &ValidationError{}
	if !errors.As(err, &ve) {
		t.Fatalf("error is not *ValidationError: got %T (%v)", err, err)
	}
	if len(ve.Issues) == 0 {
		t.Fatalf("ValidationError has no issues")
	}
	return ve
}

