package cedar

import (
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	cedar "github.com/cedar-policy/cedar-go"
)

// PolicyEvalError is one policy that failed to EVALUATE (as opposed to
// failing to parse) on at least one request since the last Reload.
//
// cedar-go skips a policy whose evaluation errors and reports it only
// in Diagnostic.Errors. The engine turns an erroring forbid into a Deny
// (fromCedar, fail-closed) and an erroring permit into "did not match";
// either way the policy author needs to see that the rule is broken,
// so every erroring policy is recorded here and attached to its source
// file in ListPolicies (the policy view's status surface).
//
// Message is sanitized (sanitizeEvalErrorMessage): it carries only what
// the POLICY TEXT supplied (an attribute name, a type name), never a
// value from the request context or the resource id.
type PolicyEvalError struct {
	PolicyID string `json:"policy_id"`
	// File is the source file the policy came from (the name ListPolicies
	// reports), or "" when the policy carries no filename.
	File string `json:"file,omitempty"`
	// Effect is "forbid" or "permit". An erroring forbid denies the
	// request (fail-closed); an erroring permit is skipped.
	Effect  string `json:"effect"`
	Message string `json:"message"`
	// Count is how many evaluations hit this error since the last Reload.
	Count int64 `json:"count"`
}

// evalErrorLogInterval bounds how often one policy id's evaluation
// error is re-logged. The first occurrence after a Reload always logs;
// later ones log at most once per interval (with the running count), so
// a broken policy on a hot gate never produces a log line per request.
const evalErrorLogInterval = 10 * time.Minute

type evalErrorEntry struct {
	PolicyEvalError
	lastLogged time.Time
}

// evalErrorTracker is the engine's per-policy evaluation-error record.
// Only touched when a request actually produced diag.Errors, so the
// common (error-free) Evaluate path never takes its lock.
type evalErrorTracker struct {
	mu      sync.Mutex
	entries map[string]*evalErrorEntry
	now     func() time.Time // test seam; nil → time.Now
}

func (t *evalErrorTracker) clock() time.Time {
	if t.now != nil {
		return t.now()
	}
	return time.Now()
}

// record notes every error in errs. ps resolves each policy's effect.
func (t *evalErrorTracker) record(ps *cedar.PolicySet, errs []cedar.DiagnosticError) {
	if len(errs) == 0 {
		return
	}
	now := t.clock()
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.entries == nil {
		t.entries = make(map[string]*evalErrorEntry)
	}
	for _, de := range errs {
		id := string(de.PolicyID)
		msg := sanitizeEvalErrorMessage(de.Message)
		ent, ok := t.entries[id]
		if !ok {
			ent = &evalErrorEntry{PolicyEvalError: PolicyEvalError{
				PolicyID: id,
				File:     policySourceFile(de),
				Effect:   policyEffectName(ps, de.PolicyID),
			}}
			t.entries[id] = ent
		}
		ent.Message = msg
		ent.Count++
		if ent.lastLogged.IsZero() || now.Sub(ent.lastLogged) >= evalErrorLogInterval {
			ent.lastLogged = now
			// Policy id, effect, sanitized message and a count only —
			// never request context values (check-no-user-content-in-slog).
			slog.Warn("cedar.policy_eval_error",
				"policy_id", id,
				"effect", ent.Effect,
				"message", msg,
				"count", ent.Count,
			)
		}
	}
}

// reset drops every record. Called when the policy text is replaced
// (Reload / SetPolicyText) — a fixed policy must stop being reported.
func (t *evalErrorTracker) reset() {
	t.mu.Lock()
	t.entries = nil
	t.mu.Unlock()
}

// snapshot returns every record, sorted by policy id.
func (t *evalErrorTracker) snapshot() []PolicyEvalError {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.entries) == 0 {
		return nil
	}
	out := make([]PolicyEvalError, 0, len(t.entries))
	for _, ent := range t.entries {
		out = append(out, ent.PolicyEvalError)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PolicyID < out[j].PolicyID })
	return out
}

// PolicyEvalErrors returns every policy that failed to evaluate on at
// least one request since the last Reload, sorted by policy id. nil when
// none has. The same records are attached per file by ListPolicies.
func (e *Engine) PolicyEvalErrors() []PolicyEvalError {
	if e == nil {
		return nil
	}
	return e.evalErrors.snapshot()
}

// erroringForbid returns the id of a forbid policy in errs (the
// lexically smallest, for a stable audit record), or "" when only
// permits errored. A policy id the set cannot resolve is treated as a
// forbid: an unknown effect must not be assumed permissive.
func erroringForbid(ps *cedar.PolicySet, errs []cedar.DiagnosticError) string {
	var found string
	for _, de := range errs {
		if policyEffectName(ps, de.PolicyID) != "forbid" {
			continue
		}
		if id := string(de.PolicyID); found == "" || id < found {
			found = id
		}
	}
	return found
}

func policyEffectName(ps *cedar.PolicySet, id cedar.PolicyID) string {
	if ps != nil {
		if p := ps.Get(id); p != nil && p.Effect() == cedar.Permit {
			return "permit"
		}
	}
	return "forbid"
}

// policySourceFile names the file a policy came from: the filename
// cedar-go recorded at parse time (Reload and LoadHarnessSnippets parse
// each source under its own name), falling back to the id's prefix.
func policySourceFile(de cedar.DiagnosticError) string {
	if de.Position.Filename != "" {
		return de.Position.Filename
	}
	id := strings.TrimPrefix(string(de.PolicyID), "harness-self/")
	if i := strings.IndexByte(id, '#'); i > 0 {
		return id[:i]
	}
	return ""
}

// sanitizeEvalErrorMessage reduces a cedar-go evaluation error to the
// parts the policy text supplied. cedar-go messages can embed request
// data — an entity UID (`entity \`Tool::"x"\` does not exist`), integer
// operands of an overflow, a malformed extension-function argument —
// and this record is shown in the UI and logged, so anything that might
// be a request value is dropped:
//
//   - attribute / tag access: keep the attribute name (from the policy),
//     drop the entity the access was attempted on;
//   - type errors: kept verbatim — cedar-go formats them from type names
//     only;
//   - everything else: the leading clause up to the first ':' or '`',
//     marked as redacted.
func sanitizeEvalErrorMessage(msg string) string {
	switch {
	case strings.Contains(msg, "does not have the attribute"),
		strings.Contains(msg, "does not have the tag"):
		kind := "attribute"
		if strings.Contains(msg, "does not have the tag") {
			kind = "tag"
		}
		subject := "entity"
		if strings.HasPrefix(msg, "record ") {
			subject = "record"
		}
		return subject + " does not have the " + kind + " `" + lastBacktickSpan(msg) + "`"
	case strings.HasPrefix(msg, "cannot access attribute"),
		strings.HasPrefix(msg, "cannot access tag"):
		kind := "attribute"
		if strings.HasPrefix(msg, "cannot access tag") {
			kind = "tag"
		}
		return "cannot access " + kind + " `" + firstBacktickSpan(msg) + "` of unspecified entity"
	case strings.HasPrefix(msg, "type error"):
		return msg
	case strings.HasPrefix(msg, "entity ") && strings.HasSuffix(msg, "does not exist"):
		return "entity does not exist"
	case strings.HasPrefix(msg, "integer overflow"):
		return "integer overflow"
	}
	head := msg
	if i := strings.IndexAny(head, ":`"); i >= 0 {
		head = strings.TrimSpace(head[:i])
	}
	if head == "" {
		head = "evaluation error"
	}
	if head == msg {
		return head
	}
	return head + " (detail redacted)"
}

func lastBacktickSpan(s string) string {
	end := strings.LastIndexByte(s, '`')
	if end <= 0 {
		return ""
	}
	start := strings.LastIndexByte(s[:end], '`')
	if start < 0 {
		return ""
	}
	return s[start+1 : end]
}

func firstBacktickSpan(s string) string {
	start := strings.IndexByte(s, '`')
	if start < 0 {
		return ""
	}
	end := strings.IndexByte(s[start+1:], '`')
	if end < 0 {
		return ""
	}
	return s[start+1 : start+1+end]
}
