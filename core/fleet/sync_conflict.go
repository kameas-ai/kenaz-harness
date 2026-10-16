package fleet

// sync_conflict.go — settings sync never silently loses a write.
//
// Conflicts. Fleet answers a PUT whose lww_ts is not newer than the stored
// one with 409 stale_lww_ts. That used to trigger a silent pull, reverting
// this device's change without a word. Now the category is marked in
// conflict (Status.Conflict, LastError, a fleet.sync.push.conflict log
// line). Automatic pushes (NotifyMutation, enable, PushHeld) and the poll
// loop's pulls leave a conflicted category alone, so neither side is
// overwritten behind the user's back, until the user picks one: ForcePush
// keeps this device's settings (stamped above Fleet's row), ForcePull takes
// the other device's. The flag is persisted with the LWW stamp
// (sync_state.go), so a restart does not quietly resolve it either.
//
// Clock skew. Fleet accepts any lww_ts up to five minutes ahead of its own
// clock (422 lww_ts_in_future beyond that). A device four minutes fast
// therefore stamps every write four minutes "later" than it happened and
// beats genuinely newer edits from other devices; a device that is slow
// loses to older ones. Every Fleet response carries an HTTP Date header, so
// the Syncer records local-minus-Fleet from it (observeClock) and Push
// refuses to upload while the two differ by more than syncMaxClockSkew
// (checkClock). Refusing, rather than correcting the stamp by the measured
// offset, is the smallest design that cannot write a skewed stamp: Date has
// one-second resolution, so a corrected stamp would still be off by up to a
// second plus latency, and a clock that wrong breaks more than sync, so the
// user is told to fix it. Pulls are not refused: applying Fleet's state
// does not write a stamp.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// ErrSyncConflict is returned when Fleet holds a newer write for a category
// than the one this device tried to upload (409 stale_lww_ts), and by
// automatic pushes while that conflict is unresolved. Nothing was uploaded
// and nothing was applied.
var ErrSyncConflict = errors.New("fleet/sync: another device changed these settings after this device last synced; nothing was uploaded or applied")

// ErrSyncClockMismatch is returned by Push when this device's clock differs
// from Fleet's by more than syncMaxClockSkew. Nothing was uploaded.
var ErrSyncClockMismatch = errors.New("fleet/sync: this device's clock differs from Fleet's by more than a minute; settings are not uploaded until it is corrected")

// syncConflictMessage is Status.LastError for a conflicted category.
const syncConflictMessage = "Another device changed these settings after this device last synced, " +
	"so this device's change was not uploaded. Push keeps this device's settings; Pull takes the other device's."

// syncMaxClockSkew is the largest local-minus-Fleet clock difference at
// which Push still uploads: far inside Fleet's 5-minute lww_ts_in_future
// window, far above the Date header's one-second resolution plus latency.
const syncMaxClockSkew = 60 * time.Second

// observeClock records local-minus-Fleet from a response's Date header.
func (s *Syncer) observeClock(resp *http.Response) {
	if resp == nil {
		return
	}
	date, err := http.ParseTime(resp.Header.Get("Date"))
	if err != nil {
		return
	}
	offset := time.Since(date)
	s.clockMu.Lock()
	s.clockOffset = offset
	s.clockKnown = true
	s.clockMu.Unlock()
}

// clockSkew returns the last observed local-minus-Fleet offset.
func (s *Syncer) clockSkew() (time.Duration, bool) {
	s.clockMu.Lock()
	defer s.clockMu.Unlock()
	return s.clockOffset, s.clockKnown
}

// checkClock refuses a push while this clock is off by more than
// syncMaxClockSkew. With no observation yet it asks Fleet first (one GET,
// nothing applied), so even the first push of a run is checked.
func (s *Syncer) checkClock(ctx context.Context, cat SyncCategory, st *CategoryState) error {
	if _, known := s.clockSkew(); !known {
		if resp, err := s.client.Get(ctx, "/api/v1/sync/"+url.PathEscape(string(cat))); err == nil {
			s.observeClock(resp)
			_ = resp.Body.Close()
		}
	}
	offset, known := s.clockSkew()
	if !known || (offset <= syncMaxClockSkew && offset >= -syncMaxClockSkew) {
		// In range, or no Date header from this server to compare against.
		return nil
	}
	dir, mag := "ahead of", offset
	if offset < 0 {
		dir, mag = "behind", -offset
	}
	mag = mag.Round(time.Second)
	st.mu.Lock()
	st.lastErr = fmt.Sprintf("This device's clock is %s %s Fleet's, so settings are not uploaded until it is corrected.", mag, dir)
	st.mu.Unlock()
	logging.L().Warn("fleet.sync.push.clock_mismatch",
		"category", string(cat),
		"offset_seconds", int64(offset/time.Second),
	)
	return fmt.Errorf("%w (%s %s)", ErrSyncClockMismatch, mag, dir)
}

// markConflict records an unresolved 409 and says so.
func (s *Syncer) markConflict(cat SyncCategory, st *CategoryState) {
	st.mu.Lock()
	st.conflict = true
	st.lastErr = syncConflictMessage
	st.mu.Unlock()
	s.persist()
	logging.L().Warn("fleet.sync.push.conflict", "category", string(cat))
}

// clearConflict ends a conflict after the user picked a side.
func (s *Syncer) clearConflict(st *CategoryState) {
	st.mu.Lock()
	was := st.conflict
	st.conflict = false
	if was && st.lastErr == syncConflictMessage {
		st.lastErr = ""
	}
	st.mu.Unlock()
	if was {
		s.persist()
	}
}

// inConflict reports st's conflict flag.
func inConflict(st *CategoryState) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.conflict
}

// remoteStamp fetches Fleet's current lww_ts for cat without applying it.
// ForcePush stamps above it to keep this device's settings.
func (s *Syncer) remoteStamp(ctx context.Context, cat SyncCategory) (int64, error) {
	resp, err := s.client.Get(ctx, "/api/v1/sync/"+url.PathEscape(string(cat)))
	if err != nil {
		return 0, fmt.Errorf("fleet/sync: read %s: %w", cat, err)
	}
	defer resp.Body.Close()
	s.observeClock(resp)
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("fleet/sync: read %s: %s", cat, readFleetError(resp).describe(resp.StatusCode))
	}
	var remote syncStateResponse
	if err := json.NewDecoder(resp.Body).Decode(&remote); err != nil {
		return 0, fmt.Errorf("fleet/sync: read %s: decode: %w", cat, err)
	}
	return int64(remote.LWWTs), nil
}

// ForcePush is the user's explicit Push. It uploads like Push and, when the
// category is in conflict, resolves it by keeping this device's settings:
// the stamp goes above Fleet's current row, so the upload wins.
func (s *Syncer) ForcePush(ctx context.Context, cat SyncCategory) error {
	return s.push(ctx, cat, true)
}

// ForcePull is the user's explicit Pull. It pulls like Pull and, when the
// category is in conflict, resolves it by taking the other device's
// settings.
func (s *Syncer) ForcePull(ctx context.Context, cat SyncCategory) error {
	_, err := s.pullMode(ctx, cat, true)
	return err
}
