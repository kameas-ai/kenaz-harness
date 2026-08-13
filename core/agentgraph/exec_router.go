package agentgraph

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// ---- RouterNode (agentgraph-total-convergence-01PMGX01 WP11a) ----
//
// Design: agentic-turn-routing-01PMAG01 §3.1 (FR-001/FR-003/FR-004).
//
// THE GAP THIS CLOSES. Before this kind, the only next step a model
// could express was "call this tool". `reflect`, `review` and `planner`
// were implemented, registered and tested — and unreachable, because no
// shipped graph contained a node of those kinds and nothing let the
// model ask for one. A `router` node turns the authored graph into a
// menu the model picks from: the author declares the choices and where
// each one goes, the model names one, and the kernel promotes only that
// successor (routerExecutor.LiveOutEdges, below, is the whole of the
// routing — no downstream node "reads the decision").
//
// TWO MODES, AND WHY FUSED IS THE DEFAULT. `standalone` makes its own
// constrained LLM call listing the menu. `fused` reads the choice out
// of an upstream model node's own output, so a routed turn costs the
// same number of model calls as an unrouted one. FR-004 ("hi" stays a
// single model call) is only satisfiable in fused mode, which is why it
// is the default and what chat_default uses.

type routerExecutor struct{}

func (routerExecutor) Kind() NodeKind { return NodeKindRouter }

// routerModeFused / routerModeStandalone are the two `mode` values.
// The empty string means fused: the cheap path is what an author gets
// for not thinking about it.
const (
	routerModeFused      = "fused"
	routerModeStandalone = "standalone"
)

// defaultRouterChoiceField is the field name fused mode looks for in
// the source node's structured output when `choice_field` is unset.
const defaultRouterChoiceField = "next_choice"

// routerStateKey is the per-node RunState key holding the anti-thrash
// counter: the last choice this router made and how many times in a row
// it has made it. Keyed by node ID so two routers in one graph count
// independently.
func routerStateKey(nodeID string) string { return "__router__" + nodeID }

// routerChoice is one decoded menu entry.
type routerChoice struct {
	ID          string
	Target      string
	Description string
}

// routerChoices decodes RouterAttrs.Choices into a slice ordered by
// choice id.
//
// Ordering is not cosmetic. The map comes out of YAML with no defined
// iteration order, and the slice is what renders the menu into a
// standalone prompt and what the validator walks — an unstable order
// would make the same graph produce a different prompt on every run and
// a different error order on every validation. Sorting by id is the
// cheapest stable rule and is documented in the manifest.
func routerChoices(a RouterAttrs) []routerChoice {
	if len(a.Choices) == 0 {
		return nil
	}
	ids := make([]string, 0, len(a.Choices))
	for id := range a.Choices {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]routerChoice, 0, len(ids))
	for _, id := range ids {
		c := routerChoice{ID: id}
		if m, ok := a.Choices[id].(map[string]any); ok {
			if s, ok := m["target"].(string); ok {
				c.Target = s
			}
			if s, ok := m["description"].(string); ok {
				c.Description = s
			}
		}
		out = append(out, c)
	}
	return out
}

// routerTargets maps choice id -> target node id.
func routerTargets(choices []routerChoice) map[string]string {
	out := make(map[string]string, len(choices))
	for _, c := range choices {
		out[c.ID] = c.Target
	}
	return out
}

func (routerExecutor) Execute(ctx context.Context, env *Env, node *Node, inputs PortValues) (Result, error) {
	res := NewResult()
	a, ok := node.Attrs.(RouterAttrs)
	if !ok {
		return res, fmt.Errorf("router: node %q has wrong attrs type %T", node.ID, node.Attrs)
	}
	choices := routerChoices(a)
	if len(choices) == 0 {
		return res, fmt.Errorf("router: node %q: choices is empty — a router with no menu cannot route", node.ID)
	}
	valid := make(map[string]bool, len(choices))
	for _, c := range choices {
		valid[c.ID] = true
	}
	if !valid[a.DefaultChoice] {
		return res, fmt.Errorf("router: node %q: default_choice %q is not one of the declared choices", node.ID, a.DefaultChoice)
	}

	mode := a.Mode
	if mode == "" {
		mode = routerModeFused
	}

	// ---- doom-loop override (WP11c; design in 01PMAG01 §3.7) ----
	//
	// tool_dispatch's guard sets `should_replan` + `doom_loop_hits` when
	// the same (tool, normalized-args) pair repeats past the threshold.
	// Until now NOTHING READ EITHER: events.go called the intended
	// consumer "a future ladder controller" and it was never built, so
	// a model stuck calling the same failing tool kept calling it until
	// the budget ran out (01PMAG01 G4). This is that consumer.
	//
	// It overrides the model's stated choice rather than being one
	// input among several, and that is the point: a thrashing model's
	// own opinion about what to do next is exactly the thing that has
	// been demonstrated to be unreliable.
	if a.ReplanChoice != "" && valid[a.ReplanChoice] && truthy(inputs["should_replan"]) {
		hits := 0
		if h, ok := inputs["doom_loop_hits"].([]map[string]any); ok {
			hits = len(h)
		}
		stated, _ := routerFusedChoice(env, a, inputs, valid)
		_ = res.Events.AppendKind(env.RunID, node.ID, EventRouterOverride, map[string]any{
			"reason":         "doom_loop",
			"overridden":     stated,
			"choice":         a.ReplanChoice,
			"doom_loop_hits": hits,
		})
		logging.L().Warn("agentgraph.router.doom_loop_override",
			"run_id", env.RunID, "session_id", env.SessionID, "node_id", node.ID,
			"choice", a.ReplanChoice, "overridden", stated, "doom_loop_hits", hits)
		return routerFinish(env, node, a, choices, inputs, res, a.ReplanChoice, "doom_loop", mode)
	}

	// ---- pick ----
	choice, source := "", ""
	switch mode {
	case routerModeStandalone:
		picked, err := routerAskModel(ctx, env, node, a, choices)
		if err != nil {
			return res, err
		}
		choice, source = picked, "llm"
	default: // fused
		if picked, ok := routerFusedChoice(env, a, inputs, valid); ok {
			choice, source = picked, "source_node"
		} else if a.FallbackToStandalone {
			// The source node produced nothing parsable — the shape a
			// provider with no structured-output support produces.
			// Degrade to an explicit call rather than silently taking
			// the default (01PMAG01 §3.1 capability fallback). Opt-in,
			// because it converts a free routing decision into a paid
			// one.
			picked, err := routerAskModel(ctx, env, node, a, choices)
			if err != nil {
				return res, err
			}
			choice, source = picked, "llm"
		}
	}
	if !valid[choice] {
		choice, source = a.DefaultChoice, "default"
	}

	// ---- anti-thrash ----
	// Counted on the RESOLVED choice, not the model's stated one: a
	// router that keeps landing on the same branch is thrashing whether
	// the model asked for it or a fallback produced it.
	if a.MaxConsecutiveSame > 0 && choice != a.DefaultChoice {
		prev := env.State.Outputs(routerStateKey(node.ID))
		lastChoice, _ := prev["choice"].(string)
		consecutive, _ := prev["consecutive"].(int)
		if lastChoice == choice && consecutive+1 > a.MaxConsecutiveSame {
			_ = res.Events.AppendKind(env.RunID, node.ID, EventRouterOverride, map[string]any{
				"reason":      "thrash",
				"overridden":  choice,
				"choice":      a.DefaultChoice,
				"consecutive": consecutive + 1,
				"cap":         a.MaxConsecutiveSame,
			})
			logging.L().Warn("agentgraph.router.thrash",
				"run_id", env.RunID, "node_id", node.ID,
				"choice", choice, "consecutive", consecutive+1, "cap", a.MaxConsecutiveSame)
			choice, source = a.DefaultChoice, "thrash_guard"
		}
	}

	return routerFinish(env, node, a, choices, inputs, res, choice, source, mode)
}

// routerFinish records the resolved choice, writes the output ports and
// emits the EventRouterChoice audit record. Shared by every path that
// resolves a choice — model-stated, defaulted, thrash-broken, and
// doom-loop-overridden — so no path can quietly skip the counter or the
// event.
func routerFinish(
	env *Env, node *Node, a RouterAttrs, choices []routerChoice,
	inputs PortValues, res Result, choice, source, mode string,
) (Result, error) {
	stateKey := routerStateKey(node.ID)
	prev := env.State.Outputs(stateKey)
	lastChoice, _ := prev["choice"].(string)
	consecutive, _ := prev["consecutive"].(int)
	if lastChoice == choice {
		consecutive++
	} else {
		consecutive = 1
	}
	env.State.SetOutputs(stateKey, PortValues{"choice": choice, "consecutive": consecutive})

	// ---- outputs ----
	// Pass the inbound payload through first, so a router placed at the
	// end of a Loop body does not truncate the value surface the loop
	// flattens onto its own outputs (chat_default's assistant_text
	// reaches assistant_write across exactly this hop). Canonical ports
	// are written after, so they always win over a same-named inbound
	// key.
	for k, v := range inputs {
		res.Outputs[k] = v
	}
	res.Outputs["out"] = inputs["in"]
	res.Outputs["next"] = choice
	// The winning choice's own port carries the payload; every other
	// choice port stays ABSENT. Absence is documentation here, not
	// mechanism — LiveOutEdges is what the kernel routes on (a
	// port-presence rule is provably wrong for this kernel, see the
	// liveness block comment in kernel.go).
	res.Outputs[choice] = inputs["in"]

	_ = res.Events.AppendKind(env.RunID, node.ID, EventRouterChoice, map[string]any{
		"choice":      choice,
		"mode":        mode,
		"source":      source,
		"consecutive": consecutive,
		"target":      routerTargets(choices)[choice],
	})
	logging.L().Info("agentgraph.router.choice",
		"run_id", env.RunID, "session_id", env.SessionID, "node_id", node.ID,
		"choice", choice, "mode", mode, "source", source, "consecutive", consecutive)

	return res, nil
}

// LiveOutEdges implements EdgeRouter (executor.go). Exactly the choice
// the router landed on is promoted; every out-edge belonging to another
// choice is skipped and the kernel propagates that skip transitively.
//
// An out-edge belongs to a choice when it leaves the port named for
// that choice OR when it arrives at that choice's declared target node.
// Both authorities are honored for the same reason `decision` honors
// both (WP02c): the attrs are the author's stated intent and the ports
// are the wiring, and a graph that validates cannot make them disagree.
// Edges off `next` / `out` / any author-declared extra port are audit
// surfaces and stay live.
func (routerExecutor) LiveOutEdges(nd *Node, outs PortValues) func(Edge) bool {
	if nd == nil {
		return nil
	}
	a, ok := nd.Attrs.(RouterAttrs)
	if !ok {
		return nil
	}
	// The production Execute above always writes `next`. Its absence
	// means something reached this method with a node that did not
	// record one — reachable through DELEGATION: a wrapper executor
	// registered for this kind that forwards LiveOutEdges here while
	// its own Execute writes no choice. Promote unconditionally rather
	// than guessing.
	//
	// This matters MORE than the equivalent fallback on `decision`.
	// Without it, an empty `chosen` marks every declared choice port
	// dead, so the router would skip its ENTIRE downstream subgraph
	// rather than merely picking a wrong branch.
	// TestRouterExecutor_MissingChoiceFallbackIsReachable drives it and
	// fails if it is removed (review finding B4).
	chosen, ok := outs.GetString("next")
	if !ok || chosen == "" {
		logging.L().Warn("agentgraph.router.no_choice",
			"node_id", nd.ID,
			"detail", "router node produced no `next` output; promoting every branch unconditionally")
		return nil
	}
	choices := routerChoices(a)
	targets := routerTargets(choices)
	takenTarget := targets[chosen]

	deadPorts := make(map[string]bool, len(choices))
	deadTargets := make(map[string]bool, len(choices))
	for _, c := range choices {
		if c.ID == chosen {
			continue
		}
		deadPorts[c.ID] = true
		// A target shared with the taken choice is a reconvergence
		// point, not a dead branch: never mark it dead.
		if c.Target != "" && c.Target != takenTarget {
			deadTargets[c.Target] = true
		}
	}

	return func(e Edge) bool {
		if e.To.Node != "" && takenTarget != "" && e.To.Node == takenTarget {
			return true
		}
		if deadTargets[e.To.Node] {
			return false
		}
		if e.From.Port == chosen {
			return true
		}
		if deadPorts[e.From.Port] {
			return false
		}
		return true
	}
}

// routerFusedChoice reads the model's stated choice out of the source
// node's already-recorded output — the "no extra LLM call" half of
// FR-003. It looks, in order, at:
//
//  1. the router's OWN inbound ports, so a graph can wire
//     `source:next_choice -> router:next_choice` explicitly;
//  2. the source node's recorded outputs, under the same field name;
//  3. the source node's assistant text, parsed for a JSON object
//     carrying the field.
//
// (3) exists because the LLM seam (LLMRequest, seams.go) carries no
// provider-level structured-output constraint today — there is no
// response_format field to set, so a model asked for `{content,
// next_choice}` returns it as text. Parsing it here is the honest
// implementation of "reads a next_choice field from the model's
// structured output" against the seam that actually exists; the day the
// seam grows a real constraint, (2) starts hitting first and this stays
// as the fallback.
func routerFusedChoice(env *Env, a RouterAttrs, inputs PortValues, valid map[string]bool) (string, bool) {
	field := a.ChoiceField
	if field == "" {
		field = defaultRouterChoiceField
	}

	// (1) an explicitly-wired inbound port.
	if s, ok := inputs.GetString(field); ok && valid[strings.TrimSpace(s)] {
		return strings.TrimSpace(s), true
	}

	if env != nil && env.State != nil && a.SourceNode != "" {
		outs := env.State.Outputs(a.SourceNode)
		// (2) the source node's recorded output port.
		if s, ok := outs.GetString(field); ok && valid[strings.TrimSpace(s)] {
			return strings.TrimSpace(s), true
		}
		// (3) parse the assistant text.
		for _, key := range []string{"assistant", "assistant_text", "content"} {
			if s, ok := routerTextOf(outs[key]); ok {
				if picked, ok := parseChoiceField(s, field, valid); ok {
					return picked, true
				}
			}
		}
	}
	return "", false
}

// routerTextOf coerces a recorded port value to its text, covering the
// two shapes the model executor emits (`assistant` is a Message,
// `assistant_text` is a string).
func routerTextOf(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, t != ""
	case Message:
		return t.Content, t.Content != ""
	case []Message:
		if len(t) == 0 {
			return "", false
		}
		last := t[len(t)-1].Content
		return last, last != ""
	}
	return "", false
}

// parseChoiceField finds `field` inside text and returns its value when
// it names a valid choice.
//
// Two passes, cheapest first: scan every balanced JSON object in the
// text and decode it, then fall back to a literal `"field": value` scan
// for output that is JSON-ish but not valid JSON (a truncated object, a
// fenced block with a trailing comment). Both passes are rune-safe —
// they index by byte only at positions a rune-decode produced, so a
// multi-byte character in a description or a reply can never be split.
func parseChoiceField(text, field string, valid map[string]bool) (string, bool) {
	for _, obj := range jsonObjectCandidates(text) {
		var m map[string]any
		if err := json.Unmarshal([]byte(obj), &m); err != nil {
			continue
		}
		if s, ok := m[field].(string); ok {
			s = strings.TrimSpace(s)
			if valid[s] {
				return s, true
			}
		}
	}
	// Literal scan: `field` followed by a separator and a bare or
	// quoted token.
	idx := strings.Index(text, field)
	for idx >= 0 {
		rest := text[idx+len(field):]
		rest = strings.TrimLeftFunc(rest, func(r rune) bool {
			return r == '"' || r == ':' || r == '=' || unicode.IsSpace(r)
		})
		token := strings.TrimFunc(firstToken(rest), func(r rune) bool {
			return r == '"' || r == '\'' || r == ',' || unicode.IsSpace(r)
		})
		if valid[token] {
			return token, true
		}
		next := strings.Index(text[idx+len(field):], field)
		if next < 0 {
			break
		}
		idx = idx + len(field) + next
	}
	return "", false
}

// firstToken returns s up to the first whitespace, comma, or closing
// brace/bracket. Rune-safe: the cut index always lands on a rune
// boundary because it comes from a range over the string.
func firstToken(s string) string {
	for i, r := range s {
		if unicode.IsSpace(r) || r == ',' || r == '}' || r == ']' {
			return s[:i]
		}
	}
	return s
}

// jsonObjectCandidates returns every balanced `{...}` span in s, longest
// (outermost) first. String literals are tracked so a brace inside a
// quoted value does not unbalance the scan.
//
// The loop ranges over runes and only ever slices at indices the range
// produced, so multi-byte content is never cut mid-rune.
func jsonObjectCandidates(s string) []string {
	var out []string
	var starts []int
	inString := false
	escaped := false
	for i, r := range s {
		switch {
		case escaped:
			escaped = false
		case r == '\\' && inString:
			escaped = true
		case r == '"':
			inString = !inString
		case inString:
			// nothing
		case r == '{':
			starts = append(starts, i)
		case r == '}':
			if n := len(starts); n > 0 {
				out = append(out, s[starts[n-1]:i+len("}")])
				starts = starts[:n-1]
			}
		}
	}
	// Outermost-first: the scan closes inner objects before outer ones,
	// so reversing puts the widest span first and a `{"content": "...",
	// "next_choice": "done"}` wrapper is decoded before any object
	// nested inside it.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// routerAskModel is standalone mode: one constrained call that lists
// the menu and asks for a single choice id.
//
// The system prompt is composed through composePrompt(
// resolvePromptTemplate, graphBaseOf, a.SystemPrompt) — the same ladder
// every other compute executor uses. Skipping graphBaseOf is the
// missing-prompt bug the escalation ladder's rungs shipped with
// (wiring-integrity-01PMAG04 WP00); a router that cannot see the run's
// goal or its rejected approaches routes blind.
func routerAskModel(ctx context.Context, env *Env, node *Node, a RouterAttrs, choices []routerChoice) (string, error) {
	model := a.Model
	if model == "" {
		model = "default"
	}
	maxTokens := a.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 64
	}

	var b strings.Builder
	b.WriteString("Choose the next step. Reply with exactly one id from this list and nothing else.\n\n")
	for _, c := range choices {
		b.WriteString("- ")
		b.WriteString(c.ID)
		if c.Description != "" {
			b.WriteString(": ")
			b.WriteString(c.Description)
		}
		b.WriteString("\n")
	}

	resp, err := env.LLM.Generate(ctx, LLMRequest{
		Provider:     a.Provider,
		Model:        model,
		MaxTokens:    maxTokens,
		SystemPrompt: composePrompt(resolvePromptTemplate(env, a.Provider, model), graphBaseOf(env), a.SystemPrompt),
		Messages:     []Message{{Role: "user", Content: b.String()}},
	})
	if err != nil {
		return "", fmt.Errorf("router: node %q: %w", node.ID, err)
	}
	if env.Counters != nil {
		env.Counters.AddLLM(resp.TokensUsed)
		env.Counters.AddCost(resp.CostUSD)
	}

	valid := make(map[string]bool, len(choices))
	for _, c := range choices {
		valid[c.ID] = true
	}
	answer := strings.TrimSpace(resp.Content)
	if valid[answer] {
		return answer, nil
	}
	// A model that wrapped the id in a sentence or a JSON object still
	// gets honored — the "Sure! PASS" defect class, avoided by design
	// rather than re-lived.
	if picked, ok := parseChoiceField(answer, defaultRouterChoiceField, valid); ok {
		return picked, nil
	}
	for _, c := range choices {
		if strings.Contains(answer, c.ID) {
			return c.ID, nil
		}
	}
	// Unrecognised: the caller falls back to default_choice.
	return "", nil
}
