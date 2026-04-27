// Package conversation owns the conversation-tree (branch) subsystem
// for the agent-kernel-graph mission's Bundle B (branching v1 simple).
//
// A branch is a sub-session — a child session.Record — anchored to a
// parent session via a Branch row. The implementation stays minimal on
// purpose: spec §4.6 / FR-040 explicitly defers semantic merge,
// multi-branch merge, and branch-of-branch to v2. This package handles
// only the simple case: one parent → one child, one merge action.
//
// DIRECTIVE_001: this package does NOT touch the sessions table
// directly; it goes through core/session.Manager. It owns the branches
// and branch_message_refs tables (migration 0306).
package conversation

import "time"

// BranchKind discriminates the branch's lifecycle origin.
type BranchKind string

const (
	// BranchKindFork is the canonical "compact-handoff fork" — the user
	// asked for a side conversation off a particular parent leaf.
	BranchKindFork BranchKind = "fork"
	// BranchKindLinearContinuation is reserved for v2 (linear context
	// hand-off without divergence). Documented here so the column value
	// stays self-explanatory; v1 never writes it.
	BranchKindLinearContinuation BranchKind = "linear_continuation"
)

// BranchStatus is the branch's lifecycle state.
type BranchStatus string

const (
	// BranchStatusActive — the child run is alive and the user can
	// continue chatting on it.
	BranchStatusActive BranchStatus = "active"
	// BranchStatusMerging — a merge is in flight (compaction running).
	BranchStatusMerging BranchStatus = "merging"
	// BranchStatusMerged — the merge completed; the parent session has
	// the summary message; the child session stays around as a
	// read-only sidebranch.
	BranchStatusMerged BranchStatus = "merged"
	// BranchStatusAbandoned — the user discarded the branch; the child
	// session is preserved but flagged so the rail UI can grey it out.
	BranchStatusAbandoned BranchStatus = "abandoned"
)

// Branch is the durable representation of one fork off a parent
// session. The mapping to the wire shape (rpc/views/branches.Branch)
// lives in the rpc package — this struct is the storage projection.
type Branch struct {
	ID              string
	ParentSessionID string
	ChildSessionID  string
	Kind            BranchKind
	Status          BranchStatus
	ProviderID      string
	ModelID         string
	Title           string
	TaskHint        string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	MergedAt        *time.Time
	AbandonedAt     *time.Time
}

// MessageRef is one row of the copy-on-write reference table. Each
// row says "branch <BranchID> message at sequence <Seq> aliases parent
// message <ParentMsgID>", and once the child has produced its own
// novel turn the row is upgraded with ChildMsgID. The simple v1 store
// only writes ChildMsgID = "" until the first divergence; lookups
// simply fall through to the parent message id.
type MessageRef struct {
	BranchID    string
	Seq         int64
	ParentMsgID string
	ChildMsgID  string
}

// CreateOptions configures a new branch.
//
// ParentSessionID is required.
// ChildSessionID is the already-allocated child session.
// All other fields are optional with sensible defaults.
type CreateOptions struct {
	ParentSessionID string
	ChildSessionID  string
	Kind            BranchKind
	ProviderID      string
	ModelID         string
	Title           string
	TaskHint        string
}
