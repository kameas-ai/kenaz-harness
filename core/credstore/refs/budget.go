package refs

import (
	"sync"
)

// DefaultBudget is the default maximum number of secret reference
// resolutions allowed per session per locator (spec §2.4, FR-008).
const DefaultBudget = 50

// Budget tracks per-session per-locator resolution counts. Thread-safe.
type Budget struct {
	mu      sync.Mutex
	counts  map[string]int64
	maxPerLocator int64
}

// NewBudget creates a Budget with the given per-locator maximum.
// A maximum of 0 disables enforcement (unlimited resolutions).
func NewBudget(maxPerLocator int64) *Budget {
	return &Budget{
		counts:        make(map[string]int64),
		maxPerLocator: maxPerLocator,
	}
}

// DefaultNewBudget creates a Budget with the default maximum (DefaultBudget).
func DefaultNewBudget() *Budget {
	return NewBudget(DefaultBudget)
}

// Remaining returns the number of resolutions remaining for the given locator.
// When maxPerLocator is 0 (unlimited), it returns math.MaxInt64.
func (b *Budget) Remaining(locator string) int64 {
	if b.maxPerLocator == 0 {
		return ^int64(0) >> 1 // MaxInt64
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	used := b.counts[locator]
	rem := b.maxPerLocator - used
	if rem < 0 {
		return 0
	}
	return rem
}

// Consume records one resolution for the given locator and reports
// whether the budget was within limits before the increment. Returns
// false (exhausted) if the budget would be exceeded.
func (b *Budget) Consume(locator string) bool {
	if b.maxPerLocator == 0 {
		return true // unlimited
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	used := b.counts[locator]
	if used >= b.maxPerLocator {
		return false
	}
	b.counts[locator] = used + 1
	return true
}

// Reset clears all counters. Typically called at session teardown.
func (b *Budget) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.counts = make(map[string]int64)
}
