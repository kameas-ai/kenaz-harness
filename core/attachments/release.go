package attachments

import (
	"context"
	"fmt"
)

// ReleaseUnreferenced drops the media_artifacts metadata rows for every
// hash in hashes that nothing else references any more, then runs
// PruneOrphans so the now-unreferenced CAS files are removed. A hash some
// registered RefcountSource (an attachment, an artifact unit, a unit
// version, …) still counts is left completely alone — row and bytes.
//
// It is the hash-list form of the non-self-refcount sweep the sessions
// delete funnel performs inline (rpc/views/sessions DeleteWithOptions
// step 7), for callers that learn the hashes after the fact: the
// artifacts-as-units session/project purge observers
// (artifacts-as-units-01DOGF0C WP03, spec FR-6).
//
// Returns the number of on-disk files removed by the prune.
func ReleaseUnreferenced(ctx context.Context, m MediaStore, hashes []string) (int, error) {
	if m == nil || len(hashes) == 0 {
		return 0, nil
	}
	seen := make(map[string]struct{}, len(hashes))
	released := false
	for _, h := range hashes {
		if h == "" {
			continue
		}
		if _, dup := seen[h]; dup {
			continue
		}
		seen[h] = struct{}{}
		rows, err := m.List(ctx, MediaFilter{ContentHash: h})
		if err != nil {
			return 0, fmt.Errorf("attachments: release %s: list media rows: %w", h, err)
		}
		total, err := m.RefcountFor(ctx, h)
		if err != nil {
			return 0, fmt.Errorf("attachments: release %s: refcount: %w", h, err)
		}
		// RefcountFor includes the media rows themselves; anything above
		// that is a live reference from another source.
		if total > len(rows) {
			continue
		}
		for _, r := range rows {
			if err := m.Delete(ctx, r.ID); err != nil {
				return 0, fmt.Errorf("attachments: release %s: delete media row: %w", h, err)
			}
		}
		released = true
	}
	if !released {
		return 0, nil
	}
	return m.PruneOrphans(ctx)
}
