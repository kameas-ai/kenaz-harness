package fleet

// sync_dirty.go — an edit made while Fleet is unreachable is not lost.
//
// A failed PUT (offline, a transport error, a 5xx) used to record lastErr and
// nothing else. Nothing retried, so the edit reached Fleet only with the next
// local change, and the next poll's pull applied any newer write from another
// device straight over it.
//
// Now every local change marks its category dirty (NotifyMutation, before
// the 5s debounce) and so does a PUT that did not reach storage. The mark is
// persisted with the LWW stamp in settings_sync_state.json, so it survives a
// restart while offline. It is cleared only by a successful PUT of a collect
// taken after the last edit (dirtyGen), or by ForcePull, which discards the
// edit on the user's say-so.
//
// While a category is dirty:
//   - the poll loop retries the upload after each successful pull (syncOnce).
//     The retry goes through Push, so the upload-consent gate and the clock
//     check still apply, and it rides the poll loop's own backoff
//     (60s -> 5m -> 30m), so an unreachable Fleet is not hammered;
//   - a pull that finds a newer remote marks the category in conflict
//     (sync_conflict.go) instead of applying it.
//
// A PUT Fleet refused for its content (422 schema/secret, 413, 403) stays
// dirty but is not retried until the next local change (retryHeld): resending
// identical bytes would only be refused again.

import (
	"context"
	"errors"
)

// markDirty records a local change to cat that Fleet does not have yet.
// Only a category that would upload is marked: enabled, Fleet-accepted,
// built, and with the upload gate open. A change made before the user
// confirmed uploads is not "unsent"; confirming pulls first by design
// (PushHeld).
func (s *Syncer) markDirty(cat SyncCategory) {
	s.mu.RLock()
	st, ok := s.categories[cat]
	cfg := s.configs[cat]
	s.mu.RUnlock()
	if !ok || cfg.NotSyncingYet || categoryLocalOnly(cat, st) || !s.uploadsAllowed() {
		return
	}
	st.mu.Lock()
	if !st.enabled {
		st.mu.Unlock()
		return
	}
	was := st.dirty
	st.dirty = true
	st.dirtyGen++
	st.retryHeld = false
	st.mu.Unlock()
	if !was {
		s.persist()
	}
}

// markUnsent records that a PUT of an enabled category did not reach
// Fleet's storage, so its local state is still unsent.
func (s *Syncer) markUnsent(st *CategoryState) {
	st.mu.Lock()
	was := st.dirty
	st.dirty = true
	st.mu.Unlock()
	if !was {
		s.persist()
	}
}

// isDirty reports st's unsent-change mark.
func isDirty(st *CategoryState) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.dirty
}

// syncOnce is one poll of cat: pull, then, when an unsent local change is
// pending and the pull did not turn it into a conflict, retry its upload.
// A failed pull skips the upload: pushing without knowing what Fleet holds
// is what overwrote other devices' settings. Outcomes that are holds, not
// failures (local-only, not built, consent pending, conflict), return nil
// so they do not drive the poll into backoff.
func (s *Syncer) syncOnce(ctx context.Context, cat SyncCategory) error {
	if _, err := s.pull(ctx, cat); err != nil {
		if errors.Is(err, ErrSyncCategoryLocalOnly) || errors.Is(err, ErrSyncCategoryNotSyncingYet) {
			return nil
		}
		return err
	}
	s.mu.RLock()
	st, ok := s.categories[cat]
	s.mu.RUnlock()
	if !ok {
		return nil
	}
	st.mu.Lock()
	retry := st.dirty && !st.conflict && !st.retryHeld
	st.mu.Unlock()
	if !retry {
		return nil
	}
	err := s.Push(ctx, cat)
	switch {
	case err == nil,
		errors.Is(err, ErrSyncUploadNotConfirmed),
		errors.Is(err, ErrSyncCategoryLocalOnly),
		errors.Is(err, ErrSyncCategoryNotSyncingYet),
		errors.Is(err, ErrSyncConflict):
		return nil
	}
	return err
}
