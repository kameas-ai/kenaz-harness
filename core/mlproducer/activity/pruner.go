package activity

import (
	"context"
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
// window is read from RetentionDays on EVERY pass (dials-to-consumer: a
// changed setting applies at the next pass with no restart).
type Pruner struct {
	store         *Store
	retentionDays func() int
	logger        *slog.Logger
	now           func() time.Time

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	done      chan struct{}
}

// NewPruner returns a Pruner over store. retentionDays reads the setting
// (nil = always the default); now is the clock (nil = time.Now).
func NewPruner(store *Store, retentionDays func() int, now func() time.Time, logger *slog.Logger) *Pruner {
	if logger == nil {
		logger = slog.Default()
	}
	if retentionDays == nil {
		retentionDays = func() int { return 0 }
	}
	if now == nil {
		now = time.Now
	}
	return &Pruner{
		store: store, retentionDays: retentionDays, logger: logger, now: now,
		stopCh: make(chan struct{}), done: make(chan struct{}),
	}
}

// PruneOnce runs one pass and returns the events and tasks deleted.
func (p *Pruner) PruneOnce(ctx context.Context) (events, tasks int64, err error) {
	days := EffectiveRetentionDays(p.retentionDays())
	cutoff := p.now().Add(-time.Duration(days) * 24 * time.Hour).UnixMilli()
	return p.store.PruneBefore(ctx, cutoff)
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
