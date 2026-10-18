// Package blockedrequests is the view-scoped RPC surface for surfacing
// denied permission requests (model-scheduled-jobs-01PMSJ01 WP07,
// FR-004's second half — owner decision 2: "surfaced to the user next
// time they open the app so they can grant a durable permit and re-run").
//
// The durable storage layer is core/policy/blockedrequests (deliberately
// a different package: this view is chassis-facing RPC surface, that one
// is a plain storage.DB-backed store with no rpc dependency).
package blockedrequests

import "context"

// PendingRequest is the wire shape for one blocked_permission_requests
// row — mirrors core/policy/blockedrequests.Record 1:1 with
// JSON-friendly field names and ISO 8601 timestamps.
type PendingRequest struct {
	ID         string `json:"id"`
	Origin     string `json:"origin"`   // "scheduled_chat_run" | "interactive"
	OriginID   string `json:"originId"` // scheduled_chat_runs.id, or ""
	SessionID  string `json:"sessionId"`
	Family     string `json:"family"` // "fs" (only family with a Grant path today — see Grant's doc)
	Action     string `json:"action"` // e.g. "write_filesystem"
	Resource   string `json:"resource"`
	Reason     string `json:"reason"`
	Status     string `json:"status"` // "pending" | "granted" | "dismissed"
	CreatedAt  string `json:"createdAt"`
	ResolvedAt string `json:"resolvedAt,omitempty"`
}

// BlockedRequestsAPI is the view-scoped accessor consumed by the
// frontend's pending-permissions surface.
//
// A nil store (Config.Store) is allowed — methods return
// ErrStoreUnavailable so the frontend can render an empty state without
// crashing, same convention as scheduledchat.ScheduledChatAPI.
type BlockedRequestsAPI interface {
	// ListPending returns every row with status="pending", newest first.
	ListPending(ctx context.Context) ([]PendingRequest, error)

	// Grant promotes a pending row to a durable permit: writes a Cedar
	// policy snippet permitting the row's (action, resource) exactly,
	// through the same WritePolicySnippet path every other persisted
	// grant in the tree uses (so the SAME reload-on-write behaviour
	// applies), then transitions the row to status="granted".
	//
	// ONLY family="fs" is supported today — Grant returns ErrUnsupportedFamily
	// for any other family. This is a dated, named gap, not a silent one:
	// bash/cred/tool denials are recorded (RecordingPrompter's Family
	// field exists precisely so a future WP can extend this without a
	// schema change) but this WP only builds the fs grant path, matching
	// WP06's own scope (the fs gate's RecordingPrompter is the only
	// producer wired as of this mission).
	//
	// Returns ErrNotFound when id does not exist, and ErrAlreadyResolved
	// when the row is not status="pending" (granting or dismissing twice
	// must not silently succeed a second time — the second attempt would
	// otherwise write a duplicate, harmless-but-confusing snippet file).
	Grant(ctx context.Context, id string) error

	// Dismiss transitions a pending row to status="dismissed" without
	// writing any policy. The next identical write attempt will be
	// denied and recorded again (dismissing does not suppress future
	// denials — only Grant does that).
	Dismiss(ctx context.Context, id string) error
}
