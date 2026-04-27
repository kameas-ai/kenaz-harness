package agentgraph

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// strictRegistry rejects every activity ID — used to surface the
// missing-activity validation rule.
type strictRegistry struct{}

func (strictRegistry) HasActivity(string) bool { return false }

// allowOnlyRegistry accepts a fixed allow-list.
type allowOnlyRegistry struct{ allow map[string]struct{} }

func (r allowOnlyRegistry) HasActivity(id string) bool {
	_, ok := r.allow[id]
	return ok
}

// TestValidate_PositiveFixtures confirms the canonical example graphs
// pass validation.
func TestValidate_PositiveFixtures(t *testing.T) {
	t.Parallel()

	cases := []string{
		"testdata/default_toolloop.yaml",
		"testdata/branch_v1_simple.yaml",
		"testdata/reflect_loop.yaml",
	}
	for _, path := range cases {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			g, err := LoadYAML(raw)
			if err != nil {
				t.Fatalf("LoadYAML: %v", err)
			}
			if err := Validate(g); err != nil {
				t.Fatalf("Validate failed unexpectedly: %v", err)
			}
		})
	}
}

// TestValidate_NegativeFixtures table-drives every invalid fixture and
// asserts the validator emits an issue containing the expected
// rule-prefix substring.
func TestValidate_NegativeFixtures(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		path    string
		opts    []ValidateOption
		mustHas string
	}{
		{
			name:    "cycle_outside_loop",
			path:    "testdata/invalid/cycle_outside_loop.yaml",
			mustHas: "cycle:",
		},
		{
			name:    "unbounded_loop",
			path:    "testdata/invalid/unbounded_loop.yaml",
			mustHas: "max_iterations",
		},
		{
			name:    "unbounded_retry",
			path:    "testdata/invalid/unbounded_retry.yaml",
			mustHas: "max_attempts",
		},
		{
			name:    "unbounded_review",
			path:    "testdata/invalid/unbounded_review.yaml",
			mustHas: "max_iterations",
		},
		{
			name:    "port_type_mismatch",
			path:    "testdata/invalid/port_type_mismatch.yaml",
			mustHas: "type mismatch",
		},
		{
			name:    "missing_activity_ref",
			path:    "testdata/invalid/missing_activity_ref.yaml",
			opts:    []ValidateOption{WithActivityRegistry(strictRegistry{})},
			mustHas: "unknown activity",
		},
		{
			name:    "unknown_dial",
			path:    "testdata/invalid/unknown_dial.yaml",
			mustHas: "unknown dial",
		},
		{
			name:    "dial_type_mismatch",
			path:    "testdata/invalid/dial_type_mismatch.yaml",
			mustHas: "expected int",
		},
		{
			name:    "missing_entrypoint",
			path:    "testdata/invalid/missing_entrypoint.yaml",
			mustHas: "entrypoint",
		},
		{
			name:    "unknown_entrypoint",
			path:    "testdata/invalid/unknown_entrypoint.yaml",
			mustHas: "entrypoint",
		},
		{
			name:    "duplicate_node_id",
			path:    "testdata/invalid/duplicate_node_id.yaml",
			mustHas: "duplicate node id",
		},
		{
			name:    "missing_required_input",
			path:    "testdata/invalid/missing_required_input.yaml",
			mustHas: "required input",
		},
		{
			name:    "loop_body_unknown",
			path:    "testdata/invalid/loop_body_unknown.yaml",
			mustHas: "body references unknown node",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw, err := os.ReadFile(tc.path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			// unknown_node_kind cannot make it through LoadYAML; we
			// handle that fixture in TestValidate_LoaderRejectsUnknownKind.
			g, err := LoadYAML(raw)
			if err != nil {
				t.Fatalf("LoadYAML: %v", err)
			}
			err = Validate(g, tc.opts...)
			if err == nil {
				t.Fatalf("Validate succeeded; expected failure containing %q", tc.mustHas)
			}
			ve, ok := err.(*ValidationError)
			if !ok {
				t.Fatalf("error is not *ValidationError (got %T: %v)", err, err)
			}
			if !ve.Has(tc.mustHas) {
				t.Errorf("issues do not mention %q: %v", tc.mustHas, ve.Error())
			}
		})
	}
}

// TestValidate_LoaderRejectsUnknownKind: an unknown kind is caught at
// LoadYAML (since attrs decoding has no path for it).
func TestValidate_LoaderRejectsUnknownKind(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("testdata/invalid/unknown_node_kind.yaml")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := LoadYAML(raw); err == nil {
		t.Fatalf("expected loader to reject unknown kind; got nil")
	}
}

// TestValidate_ActivityRegistryAllowList confirms the allow-list path
// passes when the activity is registered.
func TestValidate_ActivityRegistryAllowList(t *testing.T) {
	t.Parallel()

	g := Graph{
		SpecVersion: SpecVersion,
		ID:          "act-graph",
		Entrypoints: []string{"a"},
		Nodes: []Node{
			{
				ID:    "a",
				Kind:  NodeKindActivity,
				Attrs: ActivityAttrs{ActivityId: "summarize"},
			},
		},
	}
	registry := allowOnlyRegistry{allow: map[string]struct{}{"summarize": {}}}
	if err := Validate(g, WithActivityRegistry(registry)); err != nil {
		t.Fatalf("Validate with allow-list: %v", err)
	}
	registry2 := allowOnlyRegistry{allow: map[string]struct{}{"plan": {}}}
	if err := Validate(g, WithActivityRegistry(registry2)); err == nil {
		t.Fatalf("Validate should fail when activity is not in registry")
	}
}

// TestValidate_DialOverrides_Cascade exercises both graph-scope and
// node-scope dial overrides + the type checks.
func TestValidate_DialOverrides_Cascade(t *testing.T) {
	t.Parallel()

	good := Graph{
		SpecVersion: SpecVersion,
		ID:          "d",
		Entrypoints: []string{"n"},
		DialOverrides: map[string]any{
			"max_tokens_per_run":  1000,
			"escalation_enabled":  true,
			"plan_verbosity":      "verbose",
			"compaction_aggressiveness": "aggressive",
		},
		Nodes: []Node{
			{
				ID:    "n",
				Kind:  NodeKindModel,
				Attrs: ModelAttrs{Model: "m"},
				DialOverrides: map[string]any{
					"max_cost_usd_per_run": 2.5,
				},
			},
		},
	}
	if err := Validate(good); err != nil {
		t.Errorf("good cascade rejected: %v", err)
	}

	bad := good
	bad.DialOverrides = map[string]any{
		"plan_verbosity": "shouty", // not in enum
	}
	err := Validate(bad)
	if err == nil {
		t.Fatalf("bad enum value should fail")
	}
	ve := &ValidationError{}
	if !errors.As(err, &ve) {
		t.Fatalf("not a *ValidationError")
	}
	if !ve.Has("plan_verbosity") {
		t.Errorf("error did not mention plan_verbosity: %v", ve.Error())
	}
}

// TestValidate_CycleAllowedInLoopBody confirms that a cycle that lives
// entirely inside a LoopNode body is NOT flagged.
func TestValidate_CycleAllowedInLoopBody(t *testing.T) {
	t.Parallel()

	g := Graph{
		SpecVersion: SpecVersion,
		ID:          "cycle-in-loop",
		Entrypoints: []string{"l"},
		Nodes: []Node{
			{
				ID:   "l",
				Kind: NodeKindLoop,
				Attrs: LoopAttrs{
					MaxIterations: 4,
					Body:          []string{"a", "b"},
				},
			},
			{ID: "a", Kind: NodeKindModel, Attrs: ModelAttrs{Model: "m"}},
			{ID: "b", Kind: NodeKindModel, Attrs: ModelAttrs{Model: "m"}},
		},
		Edges: []Edge{
			{From: EndpointRef{Node: "a", Port: "response"},
				To: EndpointRef{Node: "b", Port: "messages"}},
			{From: EndpointRef{Node: "b", Port: "response"},
				To: EndpointRef{Node: "a", Port: "messages"}}, // cycle, but inside loop body
		},
	}
	if err := Validate(g); err != nil {
		t.Errorf("cycle-inside-loop should be allowed: %v", err)
	}
}

// TestValidate_CycleAllowedInReviewBody confirms that a feedback edge
// from a ReviewNode back into its upstream_node (the canonical
// review-with-iteration shape, FR-009 / US7) does NOT trip the cycle
// rule. ReviewNode's mandatory max_iterations bounds the inner loop.
func TestValidate_CycleAllowedInReviewBody(t *testing.T) {
	t.Parallel()

	g := Graph{
		SpecVersion: SpecVersion,
		ID:          "review-feedback",
		Entrypoints: []string{"author"},
		Nodes: []Node{
			{ID: "author", Kind: NodeKindModel, Attrs: ModelAttrs{Model: "m"}},
			{
				ID:   "reviewer",
				Kind: NodeKindReview,
				Outputs: []Port{
					// Override the default `json` verdict port so the
					// feedback edge into the author's `messages` port
					// type-checks. Real impl will translate via a
					// TransformNode; here we just keep the validator
					// happy.
					{Name: "verdict", Type: PortTypeAny},
					{Name: "approved", Type: PortTypeAny},
				},
				Attrs: ReviewAttrs{
					UpstreamNode:  "author",
					MaxIterations: 3,
				},
			},
		},
		Edges: []Edge{
			{From: EndpointRef{Node: "author", Port: "response"},
				To: EndpointRef{Node: "reviewer", Port: "draft"}},
			// Feedback: reviewer's verdict re-feeds the author. Should
			// be permitted because the author is inside reviewer's body.
			{From: EndpointRef{Node: "reviewer", Port: "verdict"},
				To: EndpointRef{Node: "author", Port: "messages"}},
		},
	}
	if err := Validate(g); err != nil {
		t.Errorf("review-with-feedback cycle should be allowed: %v", err)
	}
}

// TestValidate_DAGOutsideLoopRejectsCycle confirms a cycle OUTSIDE any
// loop body is rejected even when the involved nodes are otherwise
// well-formed.
func TestValidate_DAGOutsideLoopRejectsCycle(t *testing.T) {
	t.Parallel()

	g := Graph{
		SpecVersion: SpecVersion,
		ID:          "outside-cycle",
		Entrypoints: []string{"a"},
		Nodes: []Node{
			{ID: "a", Kind: NodeKindModel, Attrs: ModelAttrs{Model: "m"}},
			{ID: "b", Kind: NodeKindModel, Attrs: ModelAttrs{Model: "m"}},
		},
		Edges: []Edge{
			{From: EndpointRef{Node: "a", Port: "response"},
				To: EndpointRef{Node: "b", Port: "messages"}},
			{From: EndpointRef{Node: "b", Port: "response"},
				To: EndpointRef{Node: "a", Port: "messages"}},
		},
	}
	err := Validate(g)
	if err == nil {
		t.Fatalf("expected cycle error")
	}
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("not a *ValidationError")
	}
	if !ve.Has("cycle:") {
		t.Errorf("error missing cycle prefix: %v", ve.Error())
	}
}

// TestValidate_PortRequiredEntrypointBypass confirms that a required
// input on the entrypoint node does not need to be wired (the kernel
// supplies the initial value).
func TestValidate_PortRequiredEntrypointBypass(t *testing.T) {
	t.Parallel()

	g := Graph{
		SpecVersion: SpecVersion,
		ID:          "ep",
		Entrypoints: []string{"a"},
		Nodes: []Node{
			{ID: "a", Kind: NodeKindModel, Attrs: ModelAttrs{Model: "m"}},
		},
	}
	if err := Validate(g); err != nil {
		t.Errorf("entrypoint should bypass required-input check: %v", err)
	}
}

// TestValidate_AcceptsExtraDials proves the WithExtraDials hook works
// (used by tests / future packages adding their own dials).
func TestValidate_AcceptsExtraDials(t *testing.T) {
	t.Parallel()

	g := Graph{
		SpecVersion: SpecVersion,
		ID:          "extras",
		Entrypoints: []string{"n"},
		DialOverrides: map[string]any{
			"my_extra_dial": 42,
		},
		Nodes: []Node{
			{ID: "n", Kind: NodeKindModel, Attrs: ModelAttrs{Model: "m"}},
		},
	}
	if err := Validate(g, WithExtraDials(map[string]dialSpec{
		"my_extra_dial": {kind: "int"},
	})); err != nil {
		t.Errorf("extra dial should pass: %v", err)
	}
	if err := Validate(g); err == nil {
		t.Errorf("extra dial should fail without WithExtraDials")
	}
}

// TestValidate_UnknownKindAtValidatorLayer covers the case where a
// hand-built Graph has Kind = "" or unknown — even though the loader
// rejects it. We construct the Graph via Go to bypass the loader.
func TestValidate_UnknownKindAtValidatorLayer(t *testing.T) {
	t.Parallel()

	g := Graph{
		SpecVersion: SpecVersion,
		ID:          "x",
		Entrypoints: []string{"a"},
		Nodes: []Node{
			{ID: "a", Kind: NodeKind("nope"), Attrs: nil},
		},
	}
	err := Validate(g)
	if err == nil {
		t.Fatalf("expected error")
	}
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("not a *ValidationError")
	}
	if !ve.Has("unknown kind") {
		t.Errorf("missing unknown-kind issue: %v", ve.Error())
	}
}
