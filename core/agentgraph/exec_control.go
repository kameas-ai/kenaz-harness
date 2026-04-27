package agentgraph

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/semaphore"
)

// This file holds the control-primitive executors (FR-013 .. FR-018):
// Branch, Parallel, Join, Loop, Retry, Fork, Merge.
//
// Fork + Merge are STUBS in this WP — real implementations land in
// Bundle B (WP08). The stubs emit `fork_requested` / `merge_requested`
// events so downstream code can wire to them, and return synthetic
// branch IDs to keep dependent graph topologies validating.

// ---- BranchNode ----

type branchExecutor struct{}

func (branchExecutor) Kind() NodeKind { return NodeKindBranch }

func (branchExecutor) Execute(_ context.Context, env *Env, node *Node, inputs PortValues) (Result, error) {
	res := NewResult()
	a, ok := node.Attrs.(BranchAttrs)
	if !ok {
		return res, fmt.Errorf("branch: node %q has wrong attrs type %T", node.ID, node.Attrs)
	}

	// Tiny expression evaluator: eq / lt / gt / and / or / not over
	// named inputs. Per spec §10/1 this is intentionally minimal; CEL
	// is deferred until complexity demands it.
	verdict, err := evalBranchExpr(a.Condition, inputs)
	if err != nil {
		return res, fmt.Errorf("branch: node %q: %w", node.ID, err)
	}
	if verdict {
		res.Outputs["true"] = inputs["in"]
		res.Outputs["next"] = a.NextTrue
	} else {
		res.Outputs["false"] = inputs["in"]
		res.Outputs["next"] = a.NextFalse
	}
	res.Outputs["verdict"] = verdict
	_ = env // silence unused
	return res, nil
}

// evalBranchExpr is the hand-rolled tiny evaluator described in the
// spec §10/1. Supported syntax (loosely):
//
//   - `name == "literal"`   string / bool equality
//   - `name != "literal"`
//   - `name < N`, `name > N`, `name <= N`, `name >= N`  numeric
//   - `expr and expr`, `expr or expr`, `not expr`
//   - parentheses for grouping
//
// The grammar is intentionally toy — anything beyond this rejects
// with a clear "unsupported syntax" error so authors who need richer
// branching reach for `TransformNode` instead.
func evalBranchExpr(expr string, inputs PortValues) (bool, error) {
	tokens, err := tokenizeBranch(expr)
	if err != nil {
		return false, err
	}
	p := branchParser{tokens: tokens, inputs: inputs}
	v, err := p.parseOr()
	if err != nil {
		return false, err
	}
	if p.pos < len(p.tokens) {
		return false, fmt.Errorf("branch: unexpected trailing tokens at pos %d", p.pos)
	}
	return v, nil
}

type branchTokenKind int

const (
	tkIdent branchTokenKind = iota
	tkString
	tkNumber
	tkBool
	tkOp
	tkLParen
	tkRParen
	tkAnd
	tkOr
	tkNot
)

type branchToken struct {
	kind branchTokenKind
	text string
}

func tokenizeBranch(s string) ([]branchToken, error) {
	var out []branchToken
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n':
			i++
		case c == '(':
			out = append(out, branchToken{kind: tkLParen})
			i++
		case c == ')':
			out = append(out, branchToken{kind: tkRParen})
			i++
		case c == '"':
			j := i + 1
			for j < len(s) && s[j] != '"' {
				j++
			}
			if j >= len(s) {
				return nil, fmt.Errorf("branch: unterminated string at pos %d", i)
			}
			out = append(out, branchToken{kind: tkString, text: s[i+1 : j]})
			i = j + 1
		case c == '=' && i+1 < len(s) && s[i+1] == '=':
			out = append(out, branchToken{kind: tkOp, text: "=="})
			i += 2
		case c == '!' && i+1 < len(s) && s[i+1] == '=':
			out = append(out, branchToken{kind: tkOp, text: "!="})
			i += 2
		case c == '<' || c == '>':
			if i+1 < len(s) && s[i+1] == '=' {
				out = append(out, branchToken{kind: tkOp, text: string(c) + "="})
				i += 2
			} else {
				out = append(out, branchToken{kind: tkOp, text: string(c)})
				i++
			}
		case c >= '0' && c <= '9':
			j := i
			for j < len(s) && (s[j] == '.' || (s[j] >= '0' && s[j] <= '9')) {
				j++
			}
			out = append(out, branchToken{kind: tkNumber, text: s[i:j]})
			i = j
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_':
			j := i
			for j < len(s) && ((s[j] >= 'a' && s[j] <= 'z') || (s[j] >= 'A' && s[j] <= 'Z') ||
				(s[j] >= '0' && s[j] <= '9') || s[j] == '_' || s[j] == '.') {
				j++
			}
			ident := s[i:j]
			i = j
			switch strings.ToLower(ident) {
			case "and":
				out = append(out, branchToken{kind: tkAnd})
			case "or":
				out = append(out, branchToken{kind: tkOr})
			case "not":
				out = append(out, branchToken{kind: tkNot})
			case "true":
				out = append(out, branchToken{kind: tkBool, text: "true"})
			case "false":
				out = append(out, branchToken{kind: tkBool, text: "false"})
			default:
				out = append(out, branchToken{kind: tkIdent, text: ident})
			}
		default:
			return nil, fmt.Errorf("branch: unexpected char %q at pos %d", c, i)
		}
	}
	return out, nil
}

type branchParser struct {
	tokens []branchToken
	pos    int
	inputs PortValues
}

func (p *branchParser) peek() (branchToken, bool) {
	if p.pos >= len(p.tokens) {
		return branchToken{}, false
	}
	return p.tokens[p.pos], true
}

func (p *branchParser) advance() branchToken {
	t := p.tokens[p.pos]
	p.pos++
	return t
}

func (p *branchParser) parseOr() (bool, error) {
	left, err := p.parseAnd()
	if err != nil {
		return false, err
	}
	for {
		t, ok := p.peek()
		if !ok || t.kind != tkOr {
			return left, nil
		}
		p.advance()
		right, err := p.parseAnd()
		if err != nil {
			return false, err
		}
		left = left || right
	}
}

func (p *branchParser) parseAnd() (bool, error) {
	left, err := p.parseNot()
	if err != nil {
		return false, err
	}
	for {
		t, ok := p.peek()
		if !ok || t.kind != tkAnd {
			return left, nil
		}
		p.advance()
		right, err := p.parseNot()
		if err != nil {
			return false, err
		}
		left = left && right
	}
}

func (p *branchParser) parseNot() (bool, error) {
	t, ok := p.peek()
	if ok && t.kind == tkNot {
		p.advance()
		v, err := p.parseNot()
		if err != nil {
			return false, err
		}
		return !v, nil
	}
	return p.parseAtom()
}

func (p *branchParser) parseAtom() (bool, error) {
	t, ok := p.peek()
	if !ok {
		return false, fmt.Errorf("branch: unexpected end of expression")
	}
	if t.kind == tkLParen {
		p.advance()
		v, err := p.parseOr()
		if err != nil {
			return false, err
		}
		end, ok := p.peek()
		if !ok || end.kind != tkRParen {
			return false, fmt.Errorf("branch: expected ')' at pos %d", p.pos)
		}
		p.advance()
		return v, nil
	}
	if t.kind == tkBool {
		p.advance()
		return t.text == "true", nil
	}
	if t.kind == tkIdent {
		// `name op rhs` — peek ahead for the operator.
		left := p.advance()
		op, ok := p.peek()
		if !ok || op.kind != tkOp {
			// bare identifier — truthy check.
			return truthy(p.lookup(left.text)), nil
		}
		p.advance()
		rhs, ok := p.peek()
		if !ok {
			return false, fmt.Errorf("branch: expected rhs after %q", op.text)
		}
		p.advance()
		return compareValues(p.lookup(left.text), op.text, rhs)
	}
	return false, fmt.Errorf("branch: unexpected token kind %v at pos %d", t.kind, p.pos)
}

func (p *branchParser) lookup(name string) any {
	// Dotted lookup — split on '.' and walk maps. Rooted at the
	// inputs map so authors write `finish_reason == "tool_use"` even
	// when `finish_reason` is nested inside `assistant`.
	parts := strings.Split(name, ".")
	if v, ok := p.inputs[parts[0]]; ok {
		cur := v
		for _, seg := range parts[1:] {
			m, ok := cur.(map[string]any)
			if !ok {
				return nil
			}
			cur = m[seg]
		}
		return cur
	}
	return nil
}

func compareValues(left any, op string, rhs branchToken) (bool, error) {
	switch op {
	case "==", "!=":
		var rv any
		switch rhs.kind {
		case tkString:
			rv = rhs.text
		case tkBool:
			rv = rhs.text == "true"
		case tkNumber:
			n, err := strconv.ParseFloat(rhs.text, 64)
			if err != nil {
				return false, err
			}
			rv = n
		case tkIdent:
			rv = rhs.text
		}
		eq := equalsLoose(left, rv)
		if op == "==" {
			return eq, nil
		}
		return !eq, nil
	case "<", ">", "<=", ">=":
		ln, err := toFloat(left)
		if err != nil {
			return false, err
		}
		rn, err := strconv.ParseFloat(rhs.text, 64)
		if err != nil {
			return false, err
		}
		switch op {
		case "<":
			return ln < rn, nil
		case ">":
			return ln > rn, nil
		case "<=":
			return ln <= rn, nil
		case ">=":
			return ln >= rn, nil
		}
	}
	return false, fmt.Errorf("branch: unsupported operator %q", op)
}

func toFloat(v any) (float64, error) {
	switch t := v.(type) {
	case int:
		return float64(t), nil
	case int64:
		return float64(t), nil
	case float32:
		return float64(t), nil
	case float64:
		return t, nil
	case string:
		return strconv.ParseFloat(t, 64)
	}
	return 0, fmt.Errorf("branch: %T not numeric", v)
}

func equalsLoose(a, b any) bool {
	// Treat string ↔ string + number ↔ number; mixed types compare as
	// strings via fmt.Sprintf so authors don't need to know the exact
	// runtime type of a port value.
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case int:
		return t != 0
	case float64:
		return t != 0
	}
	return v != nil
}

// ---- ParallelNode + JoinNode ----

type parallelExecutor struct{}

func (parallelExecutor) Kind() NodeKind { return NodeKindParallel }

func (parallelExecutor) Execute(ctx context.Context, env *Env, node *Node, inputs PortValues) (Result, error) {
	res := NewResult()
	a, ok := node.Attrs.(ParallelAttrs)
	if !ok {
		return res, fmt.Errorf("parallel: node %q has wrong attrs type %T", node.ID, node.Attrs)
	}
	if len(a.Targets) == 0 {
		return res, fmt.Errorf("parallel: node %q: no targets", node.ID)
	}
	concurrency := a.MaxConcurrency
	if concurrency <= 0 {
		concurrency = 4 // matches the default in the testdata fixture
	}
	sem := semaphore.NewWeighted(int64(concurrency))
	results := make([]PortValues, len(a.Targets))
	errs := make([]error, len(a.Targets))
	var wg sync.WaitGroup

	// Build a sub-kernel per target so each target gets its own
	// fan-out scope. The targets themselves are node IDs in the
	// surrounding graph, so we look them up in env.Graph.
	for i, tID := range a.Targets {
		i, tID := i, tID
		if err := sem.Acquire(ctx, 1); err != nil {
			errs[i] = err
			continue
		}
		wg.Add(1)
		go func() {
			defer sem.Release(1)
			defer wg.Done()
			target := lookupNode(env.Graph, tID)
			if target == nil {
				errs[i] = fmt.Errorf("parallel: unknown target %q", tID)
				return
			}
			ex, err := resolveRegistry(env).lookup(target.Kind)
			if err != nil {
				errs[i] = err
				return
			}
			r, err := ex.Execute(ctx, env, target, inputs)
			if err != nil {
				errs[i] = err
				return
			}
			results[i] = r.Outputs
			for _, e := range r.Events.Events {
				e.RunID = env.RunID
				e.NodeID = target.ID
				res.Events.Append(e)
			}
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return res, err
		}
	}
	res.Outputs["out"] = results
	return res, nil
}

type joinExecutor struct{}

func (joinExecutor) Kind() NodeKind { return NodeKindJoin }

func (joinExecutor) Execute(_ context.Context, env *Env, node *Node, inputs PortValues) (Result, error) {
	res := NewResult()
	a, ok := node.Attrs.(JoinAttrs)
	if !ok {
		return res, fmt.Errorf("join: node %q has wrong attrs type %T", node.ID, node.Attrs)
	}
	collected := make([]any, 0, len(a.From))
	for _, fromID := range a.From {
		out := env.State.Outputs(fromID)
		if v, ok := out["out"]; ok {
			collected = append(collected, v)
		} else if v, ok := out["response"]; ok {
			collected = append(collected, v)
		} else {
			collected = append(collected, out)
		}
	}
	res.Outputs["out"] = collected
	_ = inputs
	return res, nil
}

// ---- LoopNode ----

type loopExecutor struct{}

func (loopExecutor) Kind() NodeKind { return NodeKindLoop }

func (loopExecutor) Execute(ctx context.Context, env *Env, node *Node, inputs PortValues) (Result, error) {
	res := NewResult()
	a, ok := node.Attrs.(LoopAttrs)
	if !ok {
		return res, fmt.Errorf("loop: node %q has wrong attrs type %T", node.ID, node.Attrs)
	}
	if a.MaxIterations <= 0 {
		return res, fmt.Errorf("loop: node %q: max_iterations must be > 0", node.ID)
	}

	current := inputs.Clone()
	var iter int
	for iter = 0; iter < a.MaxIterations; iter++ {
		// Optional condition stops early when the body's outputs no
		// longer satisfy it.
		if a.Condition != "" && iter > 0 {
			ok, err := evalBranchExpr(a.Condition, current)
			if err != nil {
				return res, fmt.Errorf("loop: node %q: condition: %w", node.ID, err)
			}
			if !ok {
				break
			}
		}
		for _, bID := range a.Body {
			target := lookupNode(env.Graph, bID)
			if target == nil {
				return res, fmt.Errorf("loop: node %q: body references unknown %q", node.ID, bID)
			}
			ex, err := resolveRegistry(env).lookup(target.Kind)
			if err != nil {
				return res, err
			}
			r, err := ex.Execute(ctx, env, target, current)
			if err != nil {
				return res, fmt.Errorf("loop: node %q: body %s: %w", node.ID, bID, err)
			}
			if r.Pause {
				return res, ErrPaused
			}
			env.State.SetOutputs(target.ID, r.Outputs)
			for _, e := range r.Events.Events {
				e.RunID = env.RunID
				if e.NodeID == "" {
					e.NodeID = target.ID
				}
				res.Events.Append(e)
			}
			// thread the body's outputs forward as inputs to the next body node.
			current = r.Outputs
		}
	}
	res.Outputs["out"] = current
	res.Outputs["iterations"] = iter
	return res, nil
}

// ---- RetryNode ----

type retryExecutor struct{}

func (retryExecutor) Kind() NodeKind { return NodeKindRetry }

func (retryExecutor) Execute(ctx context.Context, env *Env, node *Node, inputs PortValues) (Result, error) {
	res := NewResult()
	a, ok := node.Attrs.(RetryAttrs)
	if !ok {
		return res, fmt.Errorf("retry: node %q has wrong attrs type %T", node.ID, node.Attrs)
	}
	if a.MaxAttempts <= 0 {
		return res, fmt.Errorf("retry: node %q: max_attempts must be > 0", node.ID)
	}
	base := a.BackoffBase
	if base <= 0 {
		base = 50
	}
	cap := a.BackoffMax
	if cap <= 0 {
		cap = 5000
	}

	current := inputs.Clone()
	var lastErr error
	for attempt := 1; attempt <= a.MaxAttempts; attempt++ {
		var stepErr error
		for _, bID := range a.Body {
			target := lookupNode(env.Graph, bID)
			if target == nil {
				return res, fmt.Errorf("retry: node %q: body references unknown %q", node.ID, bID)
			}
			ex, err := resolveRegistry(env).lookup(target.Kind)
			if err != nil {
				return res, err
			}
			r, err := ex.Execute(ctx, env, target, current)
			if err != nil {
				stepErr = err
				lastErr = err
				break
			}
			env.State.SetOutputs(target.ID, r.Outputs)
			for _, e := range r.Events.Events {
				e.RunID = env.RunID
				if e.NodeID == "" {
					e.NodeID = target.ID
				}
				res.Events.Append(e)
			}
			current = r.Outputs
		}
		if stepErr == nil {
			res.Outputs["out"] = current
			res.Outputs["attempts"] = attempt
			return res, nil
		}
		// classify retryable / fatal: anything not ErrBudgetExceeded /
		// ErrPaused / ErrNotImplemented is retryable for now.
		if stepErr == ErrBudgetExceeded || stepErr == ErrPaused {
			return res, stepErr
		}
		if attempt < a.MaxAttempts {
			delay := time.Duration(base) * time.Millisecond
			for i := 1; i < attempt; i++ {
				delay *= 2
				if delay > time.Duration(cap)*time.Millisecond {
					delay = time.Duration(cap) * time.Millisecond
					break
				}
			}
			select {
			case <-ctx.Done():
				return res, ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	return res, fmt.Errorf("retry: node %q: exhausted %d attempts: %w", node.ID, a.MaxAttempts, lastErr)
}

// ---- ForkNode (STUB) ----

type forkExecutor struct{}

func (forkExecutor) Kind() NodeKind { return NodeKindFork }

func (forkExecutor) Execute(_ context.Context, env *Env, node *Node, _ PortValues) (Result, error) {
	res := NewResult()
	a, ok := node.Attrs.(ForkAttrs)
	if !ok {
		return res, fmt.Errorf("fork: node %q has wrong attrs type %T", node.ID, node.Attrs)
	}
	syntheticID := env.RunID + ":fork:" + node.ID
	res.Outputs["branch_id"] = syntheticID
	_ = res.Events.AppendKind(env.RunID, node.ID, EventForkRequested, map[string]any{
		"title":          a.Title,
		"branch_id":      syntheticID,
		"model_override": a.ModelOverride,
	})
	return res, nil
}

// ---- MergeNode (STUB) ----

type mergeExecutor struct{}

func (mergeExecutor) Kind() NodeKind { return NodeKindMerge }

func (mergeExecutor) Execute(_ context.Context, env *Env, node *Node, inputs PortValues) (Result, error) {
	res := NewResult()
	a, ok := node.Attrs.(MergeAttrs)
	if !ok {
		return res, fmt.Errorf("merge: node %q has wrong attrs type %T", node.ID, node.Attrs)
	}
	mode := a.Mode
	if mode == "" {
		mode = "summarize_append"
	}
	branchID, _ := inputs.GetString("branch")
	if branchID == "" {
		branchID = a.BranchID
	}

	// Trivial append: mirror the branch_output back as the merged
	// output. Real impl in WP08.
	out := PortValues{"merged": inputs["branch_output"]}
	res.Outputs = out
	_ = res.Events.AppendKind(env.RunID, node.ID, EventMergeRequest, map[string]any{
		"branch_id": branchID,
		"mode":      mode,
	})
	return res, nil
}

// ---- helpers ----

// resolveRegistry returns the registry the control executors use to
// dispatch into peer nodes. When env.registry is set (kernel pinned
// it on Run()), we honor it — that's how WithExecutor overrides reach
// nested dispatches. Otherwise fall back to a fresh registry per
// call. Per-call construction is cheap (one map alloc) and removes
// the package-global mutation that the previous defaultRegistry had.
func resolveRegistry(env *Env) *executorRegistry {
	if env != nil && env.registry != nil {
		return env.registry
	}
	return newExecutorRegistry()
}

func lookupNode(g *Graph, id string) *Node {
	if g == nil {
		return nil
	}
	for i := range g.Nodes {
		if g.Nodes[i].ID == id {
			return &g.Nodes[i]
		}
	}
	return nil
}
