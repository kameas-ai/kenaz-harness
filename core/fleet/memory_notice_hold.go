package fleet

// memory_notice_hold.go: hold memory uploads until the member has seen the
// memory-access notice in their current org (legal D168(2),
// contract-harness-sync §17.4).
//
// The hold exists ONLY while Fleet actually serves a memory-access text for
// this org on the kenaz-harness surface. While the registry is empty, every
// notice-text request answers 404 and nothing is held: memory sync behaves
// exactly as it did before this file existed. That ordering is the contract's
// sequencing rule, and an empty registry must never stall anyone's sync.
//
// While held:
//   - upserts are not pushed (no POST /memory/push carrying an upsert); the
//     notes stay local and are pushed, unchanged, once the notice is seen.
//     Nothing is dropped.
//   - forgets are still pushed, and forget-all, export and sync-off never
//     pass through here: they are data rights.
//   - pull continues.
//
// The decision is per account (org and user), so an org switch re-checks
// before the first push for the new org.

import (
	"context"
	"errors"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

const (
	// noticeDormantRecheck: how long a "nothing registered" answer is
	// trusted before asking again. A legal registration reaches a running
	// harness within this window; an org switch asks at once.
	noticeDormantRecheck = 15 * time.Minute
	// noticeSeenRecheck: how long a "seen in this org" answer is trusted.
	// Seeing the notice is not undone (a withdraw has seen it too), so this
	// only bounds how stale the cache may get.
	noticeSeenRecheck = time.Hour
)

// Notice hold reasons (MemorySyncStatus.NoticeHoldReason).
const (
	// NoticeHoldUnread: a text is served and this org has no ack or
	// withdraw row for it yet.
	NoticeHoldUnread = "unread"
	// NoticeHoldTextRefused: Fleet says a text is registered, but the
	// served bytes failed the hash check, so it can't be shown or acked.
	NoticeHoldTextRefused = "text_refused"
	// NoticeHoldCheckFailed: a text is (or may be) served, but this cycle
	// could not confirm whether it has been seen.
	NoticeHoldCheckFailed = "check_failed"
)

// noticeHold is the last decision, for one account.
type noticeHold struct {
	account   string
	checkedAt time.Time
	served    bool // Fleet serves a memory-access text for this org
	seen      bool // this org has an ack or withdraw row at or above the minimum
	held      bool
	reason    string
	// recheck: this answer may not stand for the usual window (a lookup
	// failed, or an ack/withdraw was just recorded): ask again next cycle.
	recheck bool
}

// fresh reports whether the cached decision may stand without asking Fleet.
// A held decision is never fresh: it is re-checked every cycle so an ack made
// on another device (or the dashboard) releases this one promptly.
func (h noticeHold) fresh(account string, now time.Time) bool {
	if h.account != account || h.account == "" || h.held || h.recheck {
		return false
	}
	age := now.Sub(h.checkedAt)
	if !h.served {
		return age < noticeDormantRecheck
	}
	return h.seen && age < noticeSeenRecheck
}

// memoryNoticeHeld reports whether upserts must be held this cycle. The
// caller holds cycleMu.
func (m *MemorySync) memoryNoticeHeld(ctx context.Context) bool {
	id, ok := m.verifiedIdentity()
	if !ok {
		// No confirmed account: the push's own account check refuses to
		// send anything, as it always has.
		return false
	}
	account := AccountKey(id)
	now := m.now()
	m.mu.Lock()
	prev := m.notice
	m.mu.Unlock()
	if prev.fresh(account, now) {
		return prev.held
	}
	next := m.checkMemoryNotice(requireTokenKey(ctx, id.TokenKey), account, prev)
	next.account, next.checkedAt = account, now
	m.mu.Lock()
	m.notice = next
	m.mu.Unlock()
	if next.held != (prev.held && prev.account == account) || next.reason != prev.reason {
		logging.L().Info("fleet.memory_sync.notice_hold",
			"held", next.held, "reason", next.reason, "served", next.served)
	}
	return next.held
}

func (m *MemorySync) checkMemoryNotice(ctx context.Context, account string, prev noticeHold) noticeHold {
	_, err := m.cfg.Client.GetNoticeText(ctx, NoticeFamilyMemoryAccess, "")
	switch {
	case errors.Is(err, ErrNoticeNotRegistered):
		// Dormant: nothing registered for this org. No hold.
		return noticeHold{}
	case errors.Is(err, ErrNoticeTextMismatch):
		logging.L().Warn("fleet.memory_sync.notice_text_refused", "err", err.Error())
		return noticeHold{served: true, held: true, reason: NoticeHoldTextRefused}
	case errors.Is(err, ErrFleetDisabled), errors.Is(err, ErrNotSignedIn),
		errors.Is(err, ErrTokenExpired), errors.Is(err, errNoAccount):
		// The push that follows fails the same way and reports it.
		return noticeHold{}
	case err != nil:
		logging.L().Warn("fleet.memory_sync.notice_check_failed", "stage", "text", "err", err.Error())
		return holdOnTextFailure(account, prev)
	}
	rows, err := m.cfg.Client.GetNoticeAcks(ctx)
	if err != nil {
		logging.L().Warn("fleet.memory_sync.notice_check_failed", "stage", "acks", "err", err.Error())
		return holdOnAcksFailure(account, prev)
	}
	if MemoryNoticeSeen(rows) {
		return noticeHold{served: true, seen: true}
	}
	return noticeHold{served: true, held: true, reason: NoticeHoldUnread}
}

// holdOnTextFailure decides a cycle whose text lookup failed for a reason
// other than "not registered" (5xx, timeout, 4xx, an undecodable body). There
// is no hold unless an earlier answer for this account said a memory-access
// text is served and not yet seen (§17.4: nothing is held until an edition
// ships); a failed lookup must never stall sync on its own. Either way the
// next cycle asks again.
func holdOnTextFailure(account string, prev noticeHold) noticeHold {
	if prev.account == account && prev.served && !prev.seen {
		return noticeHold{served: true, held: true, reason: NoticeHoldCheckFailed, recheck: true}
	}
	known := prev.account == account
	return noticeHold{served: known && prev.served, seen: known && prev.seen, recheck: true}
}

// holdOnAcksFailure decides a cycle where the text IS served but the read
// state couldn't be fetched. An account last seen as having read it keeps
// syncing; otherwise the upserts wait (a text is served, so §17.4 applies).
func holdOnAcksFailure(account string, prev noticeHold) noticeHold {
	if prev.account == account && prev.seen {
		return noticeHold{served: true, seen: true, recheck: true}
	}
	return noticeHold{served: true, held: true, reason: NoticeHoldCheckFailed, recheck: true}
}

// InvalidateNoticeHold makes the next cycle ask Fleet again (keeping what is
// known about the account, so a failed re-check can't forget that a text is
// served), and kicks a cycle. Called after this device records an ack or
// withdraw.
func (m *MemorySync) InvalidateNoticeHold() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.notice.recheck = true
	m.mu.Unlock()
	m.Kick()
}

// noticeHoldFor is the cached decision for account (no request).
func (m *MemorySync) noticeHoldFor(account string) (held bool, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if account == "" || m.notice.account != account {
		return false, ""
	}
	return m.notice.held, m.notice.reason
}
