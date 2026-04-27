package conversation

import (
	"context"
)

// TreeNode is a thin tree projection used by the rail UI to render the
// "branches off this session" sidebar. v1 is intentionally one level
// deep — branch-of-branch is OUT OF SCOPE per spec FR-040 — so the
// TreeNode has children only because the storage layer technically
// allows it. Callers can rely on len(Children) == 0 for v1 branches.
type TreeNode struct {
	Branch   Branch
	Children []TreeNode
}

// ListChildren returns the immediate branch children of a parent
// session. For v1 this is the one-deep sidebar projection.
func ListChildren(ctx context.Context, store Store, parentSessionID string) ([]Branch, error) {
	return store.ListByParent(ctx, parentSessionID)
}

// WalkToRoot walks from a child session up the parent chain, returning
// each Branch row encountered. The returned slice is ordered
// child-first (the immediate parent first, root-most last). v1 only
// supports one-deep, so the returned slice is at most length 1; the
// implementation is recursion-safe in case a future v2 introduces
// branch-of-branch.
func WalkToRoot(ctx context.Context, store Store, childSessionID string) ([]Branch, error) {
	out := make([]Branch, 0, 4)
	cursor := childSessionID
	seen := make(map[string]bool, 4)
	for {
		if seen[cursor] {
			// cycle break — should not happen in v1 but defensive against
			// hand-corrupted rows.
			break
		}
		seen[cursor] = true
		rows, err := store.ListByChild(ctx, cursor)
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			break
		}
		// In v1 there is at most one row; pick the most-recent.
		picked := rows[0]
		for _, r := range rows[1:] {
			if r.CreatedAt.After(picked.CreatedAt) {
				picked = r
			}
		}
		out = append(out, picked)
		cursor = picked.ParentSessionID
	}
	return out, nil
}

// FindCommonAncestor returns the (sessionID, branchPath) closest common
// ancestor session of two child sessions. Returns empty string when
// the two sessions don't share an ancestor (i.e. they are in different
// trees). v1 implementation simply walks each branch up to root and
// returns the first session that appears in both walks.
func FindCommonAncestor(ctx context.Context, store Store, sessionA, sessionB string) (string, error) {
	if sessionA == sessionB {
		return sessionA, nil
	}
	walkA, err := WalkToRoot(ctx, store, sessionA)
	if err != nil {
		return "", err
	}
	walkB, err := WalkToRoot(ctx, store, sessionB)
	if err != nil {
		return "", err
	}
	pathA := make(map[string]bool, len(walkA)+1)
	pathA[sessionA] = true
	for _, b := range walkA {
		pathA[b.ParentSessionID] = true
	}
	if pathA[sessionB] {
		return sessionB, nil
	}
	for _, b := range walkB {
		if pathA[b.ParentSessionID] {
			return b.ParentSessionID, nil
		}
	}
	return "", nil
}
