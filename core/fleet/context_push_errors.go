package fleet

// context_push_errors.go — the context-graph write path's real error codes.
//
// Shapes pinned from kenaz-fleet PR #173 (branch at d8adf1e, UNMERGED as of
// 2026-10-05). Every whole-batch error is {"code","message","details"?}:
//
//	403 not_team_member            details {node_id} | {edge_id}
//	403 load_policy_requires_admin node set load_policy "always" without org_admin/org_owner
//	403 capability_not_in_tier     team_shared needs team_graph_sharing; org_shared needs org_graph_sharing
//	422 lint_blocked               details {node_id, findings:[{rule,severity,excerpt}]}
//	400 invalid_classification     bad edge classification / team_id combination
//	400 missing_node_reference     edge endpoint not visible in your org
//	404 node_not_found             promote / merge-request create on a node you can't see
//
// A 200 may additionally carry per-item `rejected: [{id,kind,reason}]`
// (reason "not_permitted": the id exists but belongs to another user or
// org). Those are permission failures, distinct from version `conflicts`.
//
// Everything here tolerates the CURRENT server too: no `rejected` field
// means all-accepted (as before), and a 403 without a recognised code is a
// generic refusal rather than being assumed to be a tier problem.

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// Context-push error sentinels. *ContextPushError matches the one its Code
// names via errors.Is; capability_not_in_tier matches ErrCapabilityNotInTier.
var (
	// ErrNotTeamMember — the push named a team_id the user is not in.
	ErrNotTeamMember = errors.New("fleet: you are not a member of that team")
	// ErrLoadPolicyRequiresAdmin — load_policy "always" needs org admin/owner.
	ErrLoadPolicyRequiresAdmin = errors.New("fleet: only an org admin or owner can set an entry to always load")
	// ErrContextLintBlocked — fleet's secret lint blocked a body.
	ErrContextLintBlocked = errors.New("fleet: blocked — a body looks like it contains a secret")
	// ErrInvalidClassification — the classification / team_id combination is invalid.
	ErrInvalidClassification = errors.New("fleet: invalid sharing classification")
	// ErrMissingNodeReference — an edge points at a node not visible in your org.
	ErrMissingNodeReference = errors.New("fleet: an edge references a node that isn't visible in your org")
	// ErrContextNodeNotFound — the node does not exist or is not yours / your team's.
	ErrContextNodeNotFound = errors.New("fleet: context entry not found (or not yours to change)")
	// ErrContextPushForbidden — a 403 with no recognised code.
	ErrContextPushForbidden = errors.New("fleet: the server refused the change")
	// ErrContextPushRejected — a 200 push whose own node was rejected per-item.
	ErrContextPushRejected = errors.New("fleet: the server rejected this entry")
)

// LintFinding is one block-severity secret-lint finding (excerpt is
// server-redacted, ~80 chars).
type LintFinding struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Excerpt  string `json:"excerpt"`
}

// ContextPushError is a whole-batch refusal from the context write path.
type ContextPushError struct {
	Op       string // "push" | "promote" | "merge request"
	Status   int
	Code     string
	Message  string
	NodeID   string
	EdgeID   string
	Findings []LintFinding
}

func (e *ContextPushError) sentinel() error {
	switch e.Code {
	case "capability_not_in_tier":
		return ErrCapabilityNotInTier
	case "not_team_member":
		return ErrNotTeamMember
	case "load_policy_requires_admin":
		return ErrLoadPolicyRequiresAdmin
	case "lint_blocked":
		return ErrContextLintBlocked
	case "invalid_classification":
		return ErrInvalidClassification
	case "missing_node_reference":
		return ErrMissingNodeReference
	case "node_not_found":
		return ErrContextNodeNotFound
	}
	if e.Status == 403 {
		return ErrContextPushForbidden
	}
	return nil
}

// Is lets callers branch with errors.Is on the code's sentinel.
func (e *ContextPushError) Is(target error) bool {
	s := e.sentinel()
	return s != nil && target == s
}

// Error is user-facing copy: the frontend shows it verbatim
// (ContextsView publishError / promote error).
func (e *ContextPushError) Error() string {
	var b strings.Builder
	if s := e.sentinel(); s != nil {
		b.WriteString(s.Error())
	} else {
		fmt.Fprintf(&b, "fleet: context %s status %d", e.Op, e.Status)
		if e.Code != "" {
			fmt.Fprintf(&b, " (%s)", e.Code)
		}
	}
	if e.Code == "lint_blocked" && len(e.Findings) > 0 {
		parts := make([]string, 0, len(e.Findings))
		for _, f := range e.Findings {
			p := f.Rule
			if f.Excerpt != "" {
				p += `: "` + f.Excerpt + `"`
			}
			parts = append(parts, p)
		}
		b.WriteString(" (" + strings.Join(parts, "; ") + ")")
	} else if e.Code == "" && e.Message != "" {
		b.WriteString(": " + e.Message)
	}
	return b.String()
}

// pushErrorEnvelope is the fleet {code,message,details} error body.
type pushErrorEnvelope struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details struct {
		NodeID   string        `json:"node_id"`
		EdgeID   string        `json:"edge_id"`
		Findings []LintFinding `json:"findings"`
	} `json:"details"`
}

// parseContextPushError builds a *ContextPushError from a non-200 response.
// A body that is not an envelope leaves Code empty (generic handling).
func parseContextPushError(op string, status int, body []byte) *ContextPushError {
	e := &ContextPushError{Op: op, Status: status}
	var env pushErrorEnvelope
	if json.Unmarshal(body, &env) == nil {
		e.Code = strings.TrimSpace(env.Code)
		e.Message = env.Message
		e.NodeID = env.Details.NodeID
		e.EdgeID = env.Details.EdgeID
		e.Findings = env.Details.Findings
	}
	return e
}

// ContextPushRejection is one per-item rejection in a 200 push response
// (kenaz-fleet PR #173). Reason is "not_permitted" today.
type ContextPushRejection struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"` // "node" | "edge"
	Reason string `json:"reason"`
}

// ContextPushRejectedError reports that the pushed node itself was rejected
// on an otherwise-200 response.
type ContextPushRejectedError struct {
	NodeID string
	Reason string
}

func (e *ContextPushRejectedError) Error() string {
	if e.Reason == "not_permitted" {
		return "fleet: the server rejected this entry — that id belongs to another user or org (not_permitted)"
	}
	return fmt.Sprintf("fleet: the server rejected this entry (%s)", e.Reason)
}

// Is matches ErrContextPushRejected.
func (e *ContextPushRejectedError) Is(target error) bool { return target == ErrContextPushRejected }

// describeRejections renders rejections for the sync-status push error.
func describeRejections(rs []ContextPushRejection) string {
	parts := make([]string, 0, len(rs))
	for _, r := range rs {
		parts = append(parts, fmt.Sprintf("%s %s %s", r.Kind, r.ID, r.Reason))
	}
	return "rejected: " + strings.Join(parts, ", ")
}

// LogValue keeps lint excerpts out of logs: a *ContextPushError logged as a
// slog value carries only op / status / code / finding count. The excerpt
// (server-redacted, but still a fragment of the user's body) belongs in the
// UI error copy only.
func (e *ContextPushError) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("op", e.Op),
		slog.Int("status", e.Status),
		slog.String("code", e.Code),
		slog.Int("findings", len(e.Findings)),
	)
}

// LogSafeErr renders err for a log line. A *ContextPushError anywhere in the
// chain is rendered without its lint excerpts or server message; any other
// error is err.Error(). Use it wherever a context-push error is logged as a
// string ("err", err.Error()).
func LogSafeErr(err error) string {
	if err == nil {
		return ""
	}
	var pe *ContextPushError
	if errors.As(err, &pe) {
		return fmt.Sprintf("fleet: context %s refused (status %d, code %q, %d finding(s))", pe.Op, pe.Status, pe.Code, len(pe.Findings))
	}
	return err.Error()
}
