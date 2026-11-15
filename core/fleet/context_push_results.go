package fleet

// context_push_results.go — per-item push outcomes and the explicit
// audience (kenaz-fleet spec 018 H-3, docs/contract-context.md §1b).
//
// POST /api/v1/context/push answers `results[]`, one row per pushed node:
//
//	published       written, or an idempotent re-push
//	pending_review  not written; a merge request was opened. Same content
//	                → the same merge_request_id; changed content → the
//	                open request is superseded and a NEW id is returned
//	refused         not written; `reason` says why
//
// An older Fleet sends no results[]; the outcome is then derived from
// accepted_nodes, conflicts[] and rejected[] exactly as before.

import "strings"

// Push item statuses.
const (
	PushStatusPublished     = "published"
	PushStatusPendingReview = "pending_review"
	PushStatusRefused       = "refused"
)

// ContextPushItemResult is one row of the push response's results[].
type ContextPushItemResult struct {
	ID             string  `json:"id"`
	Status         string  `json:"status"`
	MergeRequestID *string `json:"merge_request_id,omitempty"`
	Reason         string  `json:"reason,omitempty"`
}

// MergeRequest returns the merge request id ("" when none).
func (r ContextPushItemResult) MergeRequest() string {
	if r.MergeRequestID == nil {
		return ""
	}
	return *r.MergeRequestID
}

// itemResultFor returns the outcome for wireID: Fleet's results[] row when
// present, else one derived from the legacy fields.
func itemResultFor(res ContextPushResult, wireID string) ContextPushItemResult {
	for _, r := range res.Results {
		if r.ID == wireID {
			if r.Status == "" {
				break // malformed row: derive instead
			}
			return r
		}
	}
	for _, r := range res.Rejected {
		if r.Kind == "node" && r.ID == wireID {
			return ContextPushItemResult{ID: wireID, Status: PushStatusRefused, Reason: r.Reason}
		}
	}
	if pushConflicted(res.Conflicts, wireID) {
		return ContextPushItemResult{ID: wireID, Status: PushStatusRefused, Reason: "version_conflict"}
	}
	if res.AcceptedNodes > 0 {
		return ContextPushItemResult{ID: wireID, Status: PushStatusPublished}
	}
	// Nothing accepted and nothing reported: an older Fleet's idempotent
	// re-push at the stored version also answers accepted_nodes 0.
	return ContextPushItemResult{ID: wireID, Status: PushStatusPublished}
}

// PushRefusalText is a refusal reason in plain words for the share UI.
// Unknown codes are shown as served, after a fixed lead-in.
func PushRefusalText(reason string) string {
	switch reason {
	case "team_not_found":
		return "That team no longer exists"
	case "previously_rejected":
		return "This exact text was already rejected"
	case "version_conflict":
		return "Fleet has a newer version of this entry"
	case "not_permitted":
		return "You can't change this entry"
	case "owned_by_another_member":
		return "Another member already shares an entry with this id"
	case "classification_change_requires_admin":
		return "Only an org admin can change where this entry is shared"
	case "version_out_of_range":
		return "This entry's version number is out of range"
	case "audience_not_available":
		return "That audience isn't available"
	case "governed_requires_admin":
		return "Only an org admin can publish this org-wide"
	case "lint_blocked":
		return "It looks like it contains a secret"
	case "":
		return "Fleet refused it"
	}
	return "Fleet refused it (" + strings.TrimSpace(reason) + ")"
}
