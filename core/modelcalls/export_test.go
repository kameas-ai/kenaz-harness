package modelcalls

import "time"

// WithClock installs the token bucket's clock and sleep seams (test-only).
func WithClock(o StatsPullerOptions, now func() time.Time, sleep func(time.Duration, <-chan struct{}) bool) StatsPullerOptions {
	o.now = now
	o.sleep = sleep
	return o
}

// SetPrunerClock overrides the pruner's clock (test-only).
func SetPrunerClock(p *Pruner, now func() time.Time) { p.now = now }
