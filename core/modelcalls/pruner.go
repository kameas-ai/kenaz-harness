package modelcalls

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// DefaultRetentionDays is the retention when the setting is unset (0).
const DefaultRetentionDays = 30

// MaxRetentionDays bounds the setting (ten years).
const MaxRetentionDays = 3650

// EffectiveRetentionDays normalises a stored setting: 0 (unset) or a
// negative value is the 30-day default; anything above MaxRetentionDays is
// clamped.
func EffectiveRetentionDays(days int) int {
	switch {
	case days <= 0:
		return DefaultRetentionDays
	case days > MaxRetentionDays:
		return MaxRetentionDays
	}
	return days
}

// pruneInterval is how often the pruner runs after its first pass at
// Start.
const pruneInterval = 6 * time.Hour

// Pruner deletes calls older than the retention window. The window is
// read from RetentionDays on EVERY pass (dials-to-consumer: a changed
// setting applies at the next pass with no restart), through
// EffectiveRetentionDays.
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
// (nil = always the default).
func NewPruner(store *Store, retentionDays func() int, logger *slog.Logger) *Pruner {
	if logger == nil {
		logger = slog.Default()
	}
	if retentionDays == nil {
		retentionDays = func() int { return 0 }
	}
	return &Pruner{
		store:         store,
		retentionDays: retentionDays,
		logger:        logger,
		now:           time.Now,
		stopCh:        make(chan struct{}),
		done:          make(chan struct{}),
	}
}

// PruneOnce runs one pass: it deletes every call that started more than
// the effective retention window ago and returns the count.
func (p *Pruner) PruneOnce(ctx context.Context) (int64, error) {
	days := EffectiveRetentionDays(p.retentionDays())
	cutoff := p.now().Add(-time.Duration(days) * 24 * time.Hour)
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
	n, err := p.PruneOnce(ctx)
	if err != nil {
		p.logger.Warn("modelcalls.prune_failed", "err", err.Error())
		return
	}
	if n > 0 {
		p.logger.Info("modelcalls.pruned", "rows", n)
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
