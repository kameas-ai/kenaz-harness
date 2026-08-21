package agentgraph

import (
	"errors"
	"fmt"
	"strings"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph/nodes"
)

// KnownDials is the canonical list of dial names the validator accepts
// in graph- and node-scoped DialOverrides. Authors can introduce new
// dials in later WPs by extending this set; WP01 enumerates the dials
// the spec already documents in §4.9.
//
// The map value is a tiny "kind" descriptor used to validate the type
// of a supplied override (`int`, `float`, `bool`, `string`).
var KnownDials = map[string]dialSpec{
	// Budget dials (FR-048).
	"max_tokens_per_run":             {kind: "int"},
	"max_wallclock_per_run_seconds":  {kind: "int"},
	"max_llm_calls_per_run":          {kind: "int"},
	"max_tool_calls_per_run":         {kind: "int"},
	"max_cost_usd_per_run":           {kind: "float"},
	// Behavior dials (FR-049).
	"plan_verbosity":           {kind: "string", enum: []string{"terse", "standard", "verbose"}},
	"plan_threshold":           {kind: "float"},
	"ask_threshold":            {kind: "float"},
	"reflect_frequency":        {kind: "string", enum: []string{"never", "on_demand", "every_n_turns"}},
	"review_iterations_cap":    {kind: "int"},
	"compaction_aggressiveness": {kind: "string", enum: []string{"gentle", "default", "aggressive"}},
	"escalation_enabled":       {kind: "bool"},
	// Compaction-subsystem dials (FR-041..FR-045).
	"pre_call_threshold":        {kind: "float"},
	"tool_result_max_bytes":     {kind: "int"},
	"compaction_recursion_max":  {kind: "int"},
	// Memory hooks (FR-027).
	"memory_hooks_enabled":  {kind: "bool"},
	// Concurrency (FR-060).
	"max_in_flight_nodes": {kind: "int"},
}

type dialSpec struct {
	kind string   // "int" | "float" | "bool" | "string"
	enum []string // optional whitelist for string-kind dials
}

// ActivityRegistry is the lookup hook the validator uses to resolve
// `ActivityNode.activity_id` references. WP01 ships the
// nilActivityRegistry default which accepts every reference; WP05
// installs the real registry. Tests can supply a fake registry that
// rejects/accepts specific IDs.
type ActivityRegistry interface {
	HasActivity(activityID string) bool
}

// nilActivityRegistry is the default — always reports "yes" so WP01
// validation passes for any activity reference. The validator only
// surfaces an error when the user installs a stricter registry.
type nilActivityRegistry struct{}

func (nilActivityRegistry) HasActivity(string) bool { return true }

// ValidateOption tunes Validate's strictness.
type ValidateOption func(*validateConfig)

type validateConfig struct {
	activities ActivityRegistry
	// extraDials are author-specific dial names accepted on top of
	// KnownDials. Empty in WP01.
	extraDials map[string]dialSpec
	// catalog drives the manifest-driven validation rules (WP03 /
	// FR-013..FR-016). Nil means "fall back to package default" — the
	// validator lazily loads shipped manifests on first use. Tests
	// inject a fake via WithCatalog to drive deterministic catalogs
	// without filesystem coupling.
	catalog CatalogProvider
	// catalogExplicit records whether the caller injected a catalog
	// (true) or whether we should fall back to the package default
	// (false). When true and catalog is nil, the manifest path is
	// disabled outright — useful for tests asserting hand-coded
	// behaviour.
	catalogExplicit bool
}

// WithActivityRegistry plugs in a custom registry. Use it in tests
// when you want the validator to reject unknown activity references.
func WithActivityRegistry(r ActivityRegistry) ValidateOption {
	return func(c *validateConfig) { c.activities = r }
}

// WithExtraDials extends the recognised dial set for the duration of a
// single Validate call. Useful for tests pinning dial behaviour.
func WithExtraDials(d map[string]dialSpec) ValidateOption {
	return func(c *validateConfig) {
		if c.extraDials == nil {
			c.extraDials = make(map[string]dialSpec, len(d))
		}
		for k, v := range d {
			c.extraDials[k] = v
		}
	}
}

// ValidationError aggregates one or more violations. Each rule reports
// its own message; the validator collects them rather than failing
// fast so authors see every problem in one round-trip.
type ValidationError struct {
	Issues []string
}

// Error joins the issues into a single message. The concrete shape is
// stable: each issue starts with a rule prefix (`schema:`, `cycle:`,
// `port:`, `dial:`, `activity:`).
func (e *ValidationError) Error() string {
	if len(e.Issues) == 0 {
		return "agentgraph: validation failed (no issues recorded)"
	}
	return "agentgraph: validation failed:\n  - " + strings.Join(e.Issues, "\n  - ")
}

// Has returns true when one of the issues contains substr — used by
// table-driven tests to assert specific rules fired without coupling
// to the entire error surface.
func (e *ValidationError) Has(substr string) bool {
	if e == nil {
		return false
	}
	for _, m := range e.Issues {
		if strings.Contains(m, substr) {
			return true
		}
	}
	return false
}

// Validate runs every WP01 rule against the spec and returns nil on
// success or a *ValidationError on failure. Callers should call this
// at load time before handing the graph to the kernel.
//
// The validator drives per-kind attr / port checks off the resolved
// manifest catalog (WP03 / FR-013). Graph-level concerns — cycle
// detection, orphan detection, edge endpoints, dial-ref + activity-ref
// resolution — remain hand-coded (FR-014). When a kind has no
// manifest registered yet, the validator falls back to the
// hand-written `Validate()` methods on each *Attrs struct (this is
// the WP03→WP04 transitional bridge: today's catalog only has
// archetype manifests; WP04 lands the kind manifests).
func Validate(g Graph, opts ...ValidateOption) error {
	cfg := validateConfig{activities: nilActivityRegistry{}}
	for _, o := range opts {
		o(&cfg)
	}
	// Resolve the catalog once: explicit injection wins; otherwise we
	// fall back to the package-level default (lazily loaded from the
	// embedded shipped manifests). A nil result is acceptable — the
	// manifest-driven checks short-circuit on nil and only the
	// hand-coded path runs.
	if !cfg.catalogExplicit {
		if cat := defaultCatalog(); cat != nil {
			cfg.catalog = cat
		}
	}

	v := &validator{cfg: cfg, errs: &ValidationError{}}
	v.checkSpecVersion(g)
	v.checkBasics(g)
	idx := v.indexNodes(g)
	v.checkAttrs(g)
	v.checkPorts(g, idx)
	v.checkEdges(g, idx)
	v.checkEntrypoints(g, idx)
	v.checkDialOverrides(g)
	v.checkActivityRefs(g)
	v.checkDecisionRouting(g, idx)
	v.checkRouterRouting(g, idx)
	v.checkLoopBodies(g, idx)
	v.checkReviewBodies(g, idx)
	v.checkAcyclicityOutsideLoops(g, idx)
	v.checkApprovalPolicyLabel(g)

	if len(v.errs.Issues) == 0 {
		return nil
	}
	return v.errs
}

type validator struct {
	cfg  validateConfig
	errs *ValidationError
}

func (v *validator) addf(format string, args ...any) {
	v.errs.Issues = append(v.errs.Issues, fmt.Sprintf(format, args...))
}

func (v *validator) checkSpecVersion(g Graph) {
	if g.SpecVersion == "" {
		v.addf("schema: spec_version is required (current = %q)", SpecVersion)
		return
	}
	if g.SpecVersion != SpecVersion {
		v.addf("schema: unsupported spec_version %q (want %q)", g.SpecVersion, SpecVersion)
	}
}

func (v *validator) checkBasics(g Graph) {
	if g.ID == "" {
		v.addf("schema: graph id is required")
	}
	if len(g.Nodes) == 0 {
		v.addf("schema: graph must declare at least one node")
	}
}

// indexNodes builds the lookup map and reports duplicate IDs. It also
// fires the FR-016 archetype-non-callable rule when the catalog
// reports the kind as a non-callable manifest.
//
// The unknown-kind rule is satisfied either when the catalog rejects
// the kind (post-WP04) or when the legacy IsKnown() set rejects it
// (pre-WP04 / no catalog). When the catalog has the kind we trust
// it — `IsKnown()` is a hand-coded subset that WP04 will retire.
func (v *validator) indexNodes(g Graph) map[string]*Node {
	out := make(map[string]*Node, len(g.Nodes))
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.ID == "" {
			v.addf("schema: node #%d has empty id", i)
			continue
		}
		// Resolve via catalog first. When the catalog has the kind we
		// fire the archetype rule (FR-016) and skip the IsKnown
		// subset (the catalog supersedes it). When the catalog does
		// NOT have the kind, fall back to IsKnown — that path remains
		// the source of truth for the WP03 transitional state.
		rm, found, err := resolveCatalogManifest(v.cfg.catalog, n.Kind)
		switch {
		case err != nil:
			v.addf("schema: node %q: catalog lookup failed: %v", n.ID, err)
		case found && !rm.Manifest.IsCallable():
			alts := archetypeAlternatives(v.cfg.catalog, rm.Manifest.ID)
			v.errs.Issues = append(v.errs.Issues,
				archetypeNotCallableMsg(n.ID, rm, alts))
		case found:
			// Recognised concrete kind; the catalog is the authority.
		default:
			// Catalog miss: enforce the legacy IsKnown subset so authors
			// constructing graphs in Go (and YAML graphs the loader
			// already accepted) keep their existing behaviour.
			if !n.Kind.IsKnown() {
				v.addf("schema: node %q has unknown kind %q", n.ID, n.Kind)
			}
		}
		if _, dup := out[n.ID]; dup {
			v.addf("schema: duplicate node id %q", n.ID)
			continue
		}
		out[n.ID] = n
	}
	return out
}

// checkAttrs runs per-node attr-level validation. The manifest-driven
// path (validateAgainstManifest) is preferred when the catalog has the
// kind; otherwise we fall back to the hand-written `Validate()`
// methods on each *Attrs struct.
//
// Belt-and-suspenders policy (WP03 transitional): when the manifest
// path fires we ALSO run the hand-coded Validate(). The hand-coded
// path stays around until WP04 deletes the per-kind methods; running
// both is redundant in steady state but catches drift where a kind
// has BOTH a registered manifest and a hand-coded method (the
// expected state during the WP03→WP04 cutover for any kind authors
// land manifests for first).
func (v *validator) checkAttrs(g Graph) {
	for i := range g.Nodes {
		n := &g.Nodes[i]

		// Manifest-driven path. Only fires when the catalog has the
		// kind AND the kind is callable; archetype-non-callable is
		// reported once by indexNodes, no need to double-emit attr
		// errors against an archetype's attrs.
		var manifest *nodes.ResolvedManifest
		if v.cfg.catalog != nil {
			if rm, found, _ := resolveCatalogManifest(v.cfg.catalog, n.Kind); found && rm.Manifest.IsCallable() {
				manifest = rm
			}
		}
		if manifest != nil {
			for _, msg := range validateAgainstManifest(n, manifest) {
				v.errs.Issues = append(v.errs.Issues, msg)
			}
			for _, msg := range validatePortsAgainstManifest(n, manifest) {
				v.errs.Issues = append(v.errs.Issues, msg)
			}
		}

		// Hand-coded path. Runs in parallel with the manifest path
		// during the WP03 transitional window so unmigrated kinds
		// (which have no manifest yet) keep their existing checks.
		if n.Attrs == nil {
			defaults := defaultAttrsFor(n.Kind)
			if defaults == nil {
				continue
			}
			if err := defaults.Validate(); err != nil {
				v.addf("schema: node %q (%s): %v", n.ID, n.Kind, err)
			}
			continue
		}
		if err := n.Attrs.Validate(); err != nil {
			v.addf("schema: node %q (%s): %v", n.ID, n.Kind, err)
		}
	}
}

// checkApprovalPolicyLabel refuses `policy_label` at graph load
// (approval-node-01PMZC12 UNIT-5, FR-008, E-004). Evaluating a Cedar
// label per approval is a per-call gate — trust-surfaces-that-fire-
// 01PMZ202 E-005's territory, and 01PMZ202 tasks.md:105-107 explicitly
// forbids inventing that evaluation here. Until E-004 rules otherwise,
// an author who sets the attr gets an error naming the blocker rather
// than a dial the manifest documents but which silently does nothing
// (spec.md §5.4) — the same class as X-6's parallel_dispatch
// disposition ("wire, or reject the key at load with an error"). This
// is cheap only because no shipped graph sets policy_label
// (spec.md §1.5); re-verified before UNIT-5 landed.
func (v *validator) checkApprovalPolicyLabel(g Graph) {
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.Kind != NodeKindApproval {
			continue
		}
		a, ok := n.Attrs.(ApprovalAttrs)
		if !ok || a.PolicyLabel == "" {
			continue
		}
		v.addf("schema: node %q (approval): policy_label is not evaluated yet — per-call Cedar evaluation is trust-surfaces-that-fire-01PMZ202 E-005's territory (approval-node-01PMZC12 E-004); remove policy_label until that lands", n.ID)
	}
}

// checkPorts ensures every node has a port surface (default or
// explicit) and detects duplicate port names.
func (v *validator) checkPorts(g Graph, _ map[string]*Node) {
	for i := range g.Nodes {
		n := &g.Nodes[i]
		ins, outs := portSurface(n)
		if err := checkPortNames(ins); err != nil {
			v.addf("port: node %q inputs: %v", n.ID, err)
		}
		if err := checkPortNames(outs); err != nil {
			v.addf("port: node %q outputs: %v", n.ID, err)
		}
	}
}

// portSurface returns the effective input/output ports for a node:
// explicit if listed, otherwise the per-kind defaults.
func portSurface(n *Node) (inputs, outputs []Port) {
	defIns, defOuts := defaultPortsFor(n.Kind)
	if len(n.Inputs) > 0 {
		inputs = n.Inputs
	} else {
		inputs = defIns
	}
	if len(n.Outputs) > 0 {
		outputs = n.Outputs
	} else {
		outputs = defOuts
	}
	return
}

func checkPortNames(ports []Port) error {
	seen := make(map[string]struct{}, len(ports))
	for _, p := range ports {
		if p.Name == "" {
			return errors.New("empty port name")
		}
		if p.Type == "" {
			return fmt.Errorf("port %q has empty type", p.Name)
		}
		if _, dup := seen[p.Name]; dup {
			return fmt.Errorf("duplicate port name %q", p.Name)
		}
		seen[p.Name] = struct{}{}
	}
	return nil
}

// checkEdges validates every edge: endpoints exist, ports exist on the
// referenced nodes, types are compatible, required input ports are
// reached.
func (v *validator) checkEdges(g Graph, idx map[string]*Node) {
	type sig struct {
		nodeID string
		port   string
	}
	inputsCovered := make(map[sig]struct{})
	for ei, e := range g.Edges {
		// The per-edge rules live in edgecheck.go so the drag-time
		// `CheckEdge` RPC and this save-time pass cannot drift
		// (visual-graph-authoring-01PMUX01 WP03). `resolved` is false
		// when an endpoint or port was missing, in which case the edge
		// covers no input port.
		issues, resolved := edgePortIssues(idx, fmt.Sprintf("edge #%d", ei), e)
		v.errs.Issues = append(v.errs.Issues, issues...)
		if !resolved {
			continue
		}
		inputsCovered[sig{nodeID: e.To.Node, port: e.To.Port}] = struct{}{}
	}
	// Required input ports must be reached by some edge — unless the
	// node is an entrypoint (then the kernel feeds the initial value).
	entrypoints := make(map[string]struct{}, len(g.Entrypoints))
	for _, ep := range g.Entrypoints {
		entrypoints[ep] = struct{}{}
	}
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if _, isEntry := entrypoints[n.ID]; isEntry {
			continue
		}
		ins, _ := portSurface(n)
		for _, p := range ins {
			if !p.Required {
				continue
			}
			if _, ok := inputsCovered[sig{nodeID: n.ID, port: p.Name}]; !ok {
				v.addf("port: required input %s.%s is not connected", n.ID, p.Name)
			}
		}
	}
}

func lookupPort(ports []Port, name string) (Port, bool) {
	for _, p := range ports {
		if p.Name == name {
			return p, true
		}
	}
	return Port{}, false
}

// checkEntrypoints ensures the graph names at least one entrypoint and
// every entrypoint refers to an existing node.
func (v *validator) checkEntrypoints(g Graph, idx map[string]*Node) {
	if len(g.Entrypoints) == 0 {
		v.addf("schema: graph must declare at least one entrypoint")
		return
	}
	seen := make(map[string]struct{}, len(g.Entrypoints))
	for _, ep := range g.Entrypoints {
		if ep == "" {
			v.addf("schema: empty entrypoint id")
			continue
		}
		if _, dup := seen[ep]; dup {
			v.addf("schema: duplicate entrypoint %q", ep)
			continue
		}
		seen[ep] = struct{}{}
		if _, ok := idx[ep]; !ok {
			v.addf("schema: entrypoint %q does not match any node", ep)
		}
	}
}

// checkDialOverrides validates graph-scope and per-node dial keys +
// value types.
func (v *validator) checkDialOverrides(g Graph) {
	v.validateDialMap("graph", g.DialOverrides)
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if len(n.DialOverrides) == 0 {
			continue
		}
		v.validateDialMap("node "+n.ID, n.DialOverrides)
	}
}

func (v *validator) validateDialMap(scope string, dials map[string]any) {
	for k, raw := range dials {
		spec, ok := KnownDials[k]
		if !ok {
			if extra, present := v.cfg.extraDials[k]; present {
				spec, ok = extra, true
			}
		}
		if !ok {
			v.addf("dial: %s: unknown dial %q", scope, k)
			continue
		}
		if err := dialValueOK(spec, raw); err != nil {
			v.addf("dial: %s: dial %q: %v", scope, k, err)
		}
	}
}

func dialValueOK(spec dialSpec, raw any) error {
	switch spec.kind {
	case "int":
		switch v := raw.(type) {
		case int, int32, int64:
			return nil
		case float64:
			// JSON / YAML lossily promote ints to float64; accept whole
			// numbers, reject fractional.
			if v == float64(int64(v)) {
				return nil
			}
			return fmt.Errorf("expected int, got fractional %.3f", v)
		}
		return fmt.Errorf("expected int, got %T", raw)
	case "float":
		switch raw.(type) {
		case float32, float64, int, int32, int64:
			return nil
		}
		return fmt.Errorf("expected number, got %T", raw)
	case "bool":
		if _, ok := raw.(bool); ok {
			return nil
		}
		return fmt.Errorf("expected bool, got %T", raw)
	case "string":
		s, ok := raw.(string)
		if !ok {
			return fmt.Errorf("expected string, got %T", raw)
		}
		if len(spec.enum) == 0 {
			return nil
		}
		for _, allowed := range spec.enum {
			if s == allowed {
				return nil
			}
		}
		return fmt.Errorf("value %q not in {%s}", s, strings.Join(spec.enum, "|"))
	}
	return fmt.Errorf("unrecognised dial kind %q", spec.kind)
}

// checkActivityRefs walks every ActivityNode and asks the registry to
// confirm the activity exists.
func (v *validator) checkActivityRefs(g Graph) {
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.Kind != NodeKindActivity {
			continue
		}
		attrs, ok := n.Attrs.(ActivityAttrs)
		if !ok {
			// Schema check already flagged a malformed payload.
			continue
		}
		if attrs.ActivityId == "" {
			continue // schema-check already reported missing id
		}
		if !v.cfg.activities.HasActivity(attrs.ActivityId) {
			v.addf("activity: node %q references unknown activity %q",
				n.ID, attrs.ActivityId)
		}
	}
}

// checkDecisionRouting makes `decision`'s next_true / next_false attrs
// mean something (agentgraph-total-convergence-01PMGX01 WP02c; design
// in agentic-turn-routing-01PMAG01 §3.3).
//
// They were declared `required: true` in decision.yaml and then ignored
// by every consumer in the repo — the kernel routed purely on edges,
// and since the promotion loop decremented in-degree unconditionally it
// did not really route at all. WP02b made the kernel consult them
// (edgeLivenessFor in kernel.go): the attr-named target of the taken
// verdict is promoted, the other is skipped.
//
// That makes them load-bearing, which makes an author typo a silent
// mis-route. This rule closes that: the two targets must exist, must
// differ, and must agree with whatever the `true` / `false` port edges
// say — so the kernel's two routing authorities (attr target, source
// port) can never disagree in a graph that validates.
func (v *validator) checkDecisionRouting(g Graph, idx map[string]*Node) {
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.Kind != NodeKindDecision {
			continue
		}
		a, ok := n.Attrs.(DecisionAttrs)
		if !ok {
			continue
		}
		for _, ref := range []struct{ attr, target string }{
			{"next_true", a.NextTrue},
			{"next_false", a.NextFalse},
		} {
			if ref.target == "" {
				v.addf("schema: node %q (decision): %s is required — it selects which successor the kernel promotes", n.ID, ref.attr)
				continue
			}
			if _, ok := idx[ref.target]; !ok {
				v.addf("schema: node %q (decision): %s references unknown node %q", n.ID, ref.attr, ref.target)
			}
		}
		if a.NextTrue != "" && a.NextTrue == a.NextFalse {
			v.addf("schema: node %q (decision): next_true and next_false both name %q — a decision that routes both verdicts to the same node is not a decision", n.ID, a.NextTrue)
		}
		// Port edges must agree with the attrs. The rule itself lives in
		// edgecheck.go's decisionEdgeIssues so the drag-time RPC applies
		// the identical check (WP03).
		for _, e := range g.Edges {
			v.errs.Issues = append(v.errs.Issues, decisionEdgeIssues(n.ID, a, e)...)
		}
	}
}

// checkRouterRouting is checkDecisionRouting's counterpart for the
// `router` kind (agentgraph-total-convergence-01PMGX01 WP11a; design in
// agentic-turn-routing-01PMAG01 §3.1).
//
// The router's `choices` map is load-bearing at run time — the kernel
// promotes the winning choice's target and skips the others through
// routerExecutor.LiveOutEdges — so an author typo is a silent
// mis-route, exactly the failure mode WP02c closed for `decision`.
// This rule closes it here before the first run rather than after.
//
// It deliberately does NOT require an out-edge per choice. A choice
// whose only effect is "stop looping" (chat_default's `done`) is a
// legitimate menu entry with no successor of its own; requiring an edge
// would force authors to invent one.
func (v *validator) checkRouterRouting(g Graph, idx map[string]*Node) {
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.Kind != NodeKindRouter {
			continue
		}
		a, ok := n.Attrs.(RouterAttrs)
		if !ok {
			continue
		}
		choices := routerChoices(a)
		if len(choices) == 0 {
			v.addf("schema: node %q (router): choices is required and must be a non-empty map of {id: {target, description}} — a router with no menu cannot route", n.ID)
			continue
		}
		valid := make(map[string]bool, len(choices))
		for _, c := range choices {
			valid[c.ID] = true
			if c.Target == "" {
				v.addf("schema: node %q (router): choice %q has no target — the target names the node the kernel promotes when this choice wins", n.ID, c.ID)
				continue
			}
			if _, ok := idx[c.Target]; !ok {
				v.addf("schema: node %q (router): choice %q targets unknown node %q", n.ID, c.ID, c.Target)
			}
		}
		if a.DefaultChoice == "" {
			v.addf("schema: node %q (router): default_choice is required — it is what an unparseable or unknown model answer resolves to", n.ID)
		} else if !valid[a.DefaultChoice] {
			v.addf("schema: node %q (router): default_choice %q is not one of the declared choices", n.ID, a.DefaultChoice)
		}
		if a.ReplanChoice != "" && !valid[a.ReplanChoice] {
			v.addf("schema: node %q (router): replan_choice %q is not one of the declared choices", n.ID, a.ReplanChoice)
		}
		// Fused mode is the default (empty mode == fused), and it has
		// nothing to read without a source node.
		if a.Mode == "" || a.Mode == routerModeFused {
			if a.SourceNode == "" {
				v.addf("schema: node %q (router): mode %q requires source_node — fused routing reads the choice off that node's output", n.ID, routerModeFused)
			} else if _, ok := idx[a.SourceNode]; !ok {
				v.addf("schema: node %q (router): source_node references unknown node %q", n.ID, a.SourceNode)
			}
		}
		// Port edges must agree with the menu, mirroring the decision
		// rule. Shared with the drag-time RPC via routerEdgeIssues
		// (WP03).
		for _, e := range g.Edges {
			v.errs.Issues = append(v.errs.Issues, routerEdgeIssues(n.ID, a, e)...)
		}
	}
}

// checkLoopBodies ensures Loop and Retry bodies refer to declared
// nodes and carry their mandatory caps. The cap presence is enforced
// per-attrs Validate() too — but we double-check here so authors who
// hand-construct attrs in Go cannot bypass it via a zero-value max.
func (v *validator) checkLoopBodies(g Graph, idx map[string]*Node) {
	for i := range g.Nodes {
		n := &g.Nodes[i]
		switch n.Kind {
		case NodeKindLoop:
			a, ok := n.Attrs.(LoopAttrs)
			if !ok {
				continue
			}
			if a.MaxIterations <= 0 {
				v.addf("schema: node %q (loop): max_iterations must be > 0 (NFR-004)", n.ID)
			}
			for _, b := range a.Body {
				if _, ok := idx[b]; !ok {
					v.addf("schema: node %q (loop): body references unknown node %q", n.ID, b)
				}
			}
		case NodeKindRetry:
			a, ok := n.Attrs.(RetryAttrs)
			if !ok {
				continue
			}
			if a.MaxAttempts <= 0 {
				v.addf("schema: node %q (retry): max_attempts must be > 0 (NFR-004)", n.ID)
			}
			for _, b := range a.Body {
				if _, ok := idx[b]; !ok {
					v.addf("schema: node %q (retry): body references unknown node %q", n.ID, b)
				}
			}
		}
	}
}

// checkReviewBodies enforces that every ReviewNode's upstream_node
// reference resolves and that its mandatory cap is positive (the per-
// attrs Validate also enforces this; double-check parallels the loop
// rule above).
func (v *validator) checkReviewBodies(g Graph, idx map[string]*Node) {
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.Kind != NodeKindReview {
			continue
		}
		a, ok := n.Attrs.(ReviewAttrs)
		if !ok {
			continue
		}
		if a.MaxIterations <= 0 {
			v.addf("schema: node %q (review): max_iterations must be > 0 (FR-009)", n.ID)
		}
		if a.UpstreamNode != "" {
			if _, ok := idx[a.UpstreamNode]; !ok {
				v.addf("schema: node %q (review): upstream_node %q does not exist", n.ID, a.UpstreamNode)
			}
		}
	}
}

// checkAcyclicityOutsideLoops runs BFS over the graph treating
// Loop/Retry/Review bodies as opaque (their internal cycles are
// allowed and bounded by mandatory caps). FR-002 / NFR-004.
func (v *validator) checkAcyclicityOutsideLoops(g Graph, idx map[string]*Node) {
	// Build the set of node IDs that live INSIDE a Loop / Retry / Review
	// body. We hide them from the outer graph during cycle detection —
	// the container node represents the body to the outside world, and
	// the inside is allowed to cycle (the container's mandatory
	// max_iterations / max_attempts cap bounds it).
	inside := make(map[string]struct{})
	for i := range g.Nodes {
		n := &g.Nodes[i]
		switch n.Kind {
		case NodeKindLoop:
			if a, ok := n.Attrs.(LoopAttrs); ok {
				for _, b := range a.Body {
					inside[b] = struct{}{}
				}
			}
		case NodeKindRetry:
			if a, ok := n.Attrs.(RetryAttrs); ok {
				for _, b := range a.Body {
					inside[b] = struct{}{}
				}
			}
		case NodeKindReview:
			// A ReviewNode re-runs its upstream_node on a `fail` verdict
			// (FR-009). Treat the upstream as inside the review's body
			// so a feedback edge from the review's output back into the
			// upstream does not register as an outside-loop cycle.
			if a, ok := n.Attrs.(ReviewAttrs); ok && a.UpstreamNode != "" {
				inside[a.UpstreamNode] = struct{}{}
			}
		}
	}

	// Build an adjacency list over the OUTER subgraph (skip nodes
	// inside loop bodies). Edges that originate or terminate inside a
	// loop body are also skipped — those are the kernel's job to
	// resolve at run-time, not the validator's.
	adj := make(map[string][]string)
	for _, e := range g.Edges {
		if _, hidden := inside[e.From.Node]; hidden {
			continue
		}
		if _, hidden := inside[e.To.Node]; hidden {
			continue
		}
		adj[e.From.Node] = append(adj[e.From.Node], e.To.Node)
	}
	// Tarjan-style DFS for cycle detection.
	const (
		white = 0
		gray  = 1
		black = 2
	)
	colour := make(map[string]int, len(idx))
	for id := range idx {
		colour[id] = white
	}
	var dfs func(string, []string) bool
	dfs = func(u string, path []string) bool {
		if _, hidden := inside[u]; hidden {
			return false
		}
		colour[u] = gray
		path = append(path, u)
		for _, w := range adj[u] {
			switch colour[w] {
			case gray:
				cycle := append([]string(nil), path...)
				cycle = append(cycle, w)
				v.addf("cycle: outside-loop cycle detected: %s", strings.Join(cycle, " → "))
				return true
			case white:
				if dfs(w, path) {
					return true
				}
			}
		}
		colour[u] = black
		return false
	}
	// Visit every node so disconnected sub-DAGs still get checked.
	for id := range idx {
		if colour[id] == white {
			if dfs(id, nil) {
				return // one cycle is enough; report and stop.
			}
		}
	}
}
