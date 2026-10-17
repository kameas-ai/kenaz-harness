package activity

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// DefaultRetentionDays is the local retention when the setting is unset
// (spec §12 A-16 item 4: default 90 days).
const DefaultRetentionDays = 90

// MaxRetentionDays bounds the setting (ten years).
const MaxRetentionDays = 3650

// EffectiveRetentionDays normalises a stored setting: 0 (unset) or a
// negative value is the 90-day default; anything above MaxRetentionDays
// is clamped.
func EffectiveRetentionDays(days int) int {
	switch {
	case days <= 0:
		return DefaultRetentionDays
	case days > MaxRetentionDays:
		return MaxRetentionDays
	}
	return days
}

// pruneInterval is how often the pruner runs after its first pass.
const pruneInterval = 6 * time.Hour

// Pruner deletes local activity older than the retention window. The
// window is read on EVERY pass (dials-to-consumer: a changed setting
// applies at the next pass with no restart) through a Retention, which
// fails safe: a pass whose read fails uses the last successfully read
// window; with none yet in this process — or once failures have reached
// the visible-guard threshold (Retention.Health().Blocked) — it deletes
// nothing. An unreadable setting is never treated as the default, which
// could be far shorter (or longer) than what the member chose.
type Pruner struct {
	store     func() *Store
	retention *Retention
	logger    *slog.Logger
	now       func() time.Time

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	done      chan struct{}
}

// NewPruner returns a Pruner over store. retentionDays reads the setting:
// (0, nil) is the default; on an error the pass uses the last good value,
// or skips with none. nil = always the default. now is the clock (nil =
// time.Now).
func NewPruner(store *Store, retentionDays func() (int, error), now func() time.Time, logger *slog.Logger) *Pruner {
	return NewLazyPruner(func() *Store { return store }, retentionDays, now, logger)
}

// NewLazyPruner is NewPruner over a store that may open later: store()
// is read on every pass, and a pass with no open store does nothing.
func NewLazyPruner(store func() *Store, retentionDays func() (int, error), now func() time.Time, logger *slog.Logger) *Pruner {
	if now == nil {
		now = time.Now
	}
	return NewRetentionPruner(store, NewRetention(retentionDays, now), now, logger)
}

// NewRetentionPruner is NewLazyPruner over a shared Retention (so its
// last-good value and failure state are the same ones the status readout
// and the engine's spawn env see).
func NewRetentionPruner(store func() *Store, retention *Retention, now func() time.Time, logger *slog.Logger) *Pruner {
	if logger == nil {
		logger = slog.Default()
	}
	if retention == nil {
		retention = NewRetention(nil, now)
	}
	if now == nil {
		now = time.Now
	}
	return &Pruner{
		store: store, retention: retention, logger: logger, now: now,
		stopCh: make(chan struct{}), done: make(chan struct{}),
	}
}

// Retention is the pruner's retention reader.
func (p *Pruner) Retention() *Retention { return p.retention }

// ErrRetentionUnreadable: the retention setting could not be read and no
// usable last-good value exists (none yet, or the failures have reached
// the blocked threshold), so the pass deleted nothing.
var ErrRetentionUnreadable = fmt.Errorf("activity: retention setting unreadable; prune pass skipped")

// PruneOnce runs one pass and returns the events and tasks deleted. When
// the setting cannot be read it prunes to the last good window, or —
// with none, or once blocked — deletes nothing and returns
// ErrRetentionUnreadable.
func (p *Pruner) PruneOnce(ctx context.Context) (events, tasks int64, err error) {
	st := p.store()
	if st == nil {
		return 0, 0, nil
	}
	raw, fresh, ok := p.retention.readPass()
	if !ok || (!fresh && p.retention.Health().Blocked) {
		return 0, 0, ErrRetentionUnreadable
	}
	days := EffectiveRetentionDays(raw)
	cutoff := p.now().Add(-time.Duration(days) * 24 * time.Hour).UnixMilli()
	return st.PruneBefore(ctx, cutoff)
}

// Start runs a pass now and then every pruneInterval until Stop.
// Idempotent.
func (p *Pruner) Start() {
	p.startOnce.Do(func() {
		go func() {
			defer close(p.done)
			p.pass()
			t := time.NewTicker(pruneInterval)
			defer t.Stop()
			for {
				select {
				case <-p.stopCh:
					return
				case <-t.C:
					p.pass()
				}
			}
		}()
	})
}

func (p *Pruner) pass() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ev, tk, err := p.PruneOnce(ctx)
	// Counts only: never the setting's content or the read error.
	if h := p.retention.Health(); h.ConsecutiveFailedPasses > 0 {
		attrs := []any{
			"consecutive_failures", h.ConsecutiveFailedPasses,
			"hours_since_last_good", int64(h.SinceLastGood / time.Hour),
		}
		switch {
		case h.Blocked:
			p.logger.Warn("mlproducer.activity.retention_blocked", attrs...)
		case err == ErrRetentionUnreadable:
			p.logger.Warn("mlproducer.activity.prune_skipped_retention_unreadable", attrs...)
		default:
			p.logger.Warn("mlproducer.activity.prune_used_last_good_retention", attrs...)
		}
	}
	if err == ErrRetentionUnreadable {
		return
	}
	if err != nil {
		p.logger.Warn("mlproducer.activity.prune_failed", "err", err.Error())
		return
	}
	if ev > 0 || tk > 0 {
		p.logger.Info("mlproducer.activity.pruned", "events", ev, "tasks", tk)
	}
}

// Stop ends the loop. Idempotent; safe without Start.
func (p *Pruner) Stop() {
	p.stopOnce.Do(func() { close(p.stopCh) })
	started := true
	p.startOnce.Do(func() { started = false; close(p.done) })
	if started {
		<-p.done
	}
}
