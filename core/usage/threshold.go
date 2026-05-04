// Package usage's threshold scheduler implements the per-month spend
// notification dial wired into Manager.Add (token-cost-telemetry-01KQ8TD7
// WP06).
//
// Architecture:
//
//   - Each Manager.Add call invokes Checker.Check after the per-turn row
//     has been persisted. Check reads the calendar-month total of
//     cost_usd from session_messages (assistant rows only), divides by
//     the user-configured MonthlyCostNotifyUSD threshold, and walks
//     the locked tier list [50, 80, 100, 150, 200].
//
//   - For each tier whose pct is met by the current month's spend AND
//     which has not yet been recorded in cost_threshold_fired for the
//     current YYYY-MM, Check INSERT-OR-IGNOREs a row and (on a fresh
//     insert) publishes a `cost.threshold.crossed` event through the
//     injected Publisher.
//
//   - Calendar boundary uses time.Now().Local() so the rollover matches
//     how a human reads a billing month. A turn at 23:59:59 local on
//     April 30 fires under "2026-04"; the next turn at 00:00:01 May 1
//     queries "2026-05" which has zero spend, so the May tier list
//     starts fresh.
//
//   - Idempotency: cost_threshold_fired (year_month, pct) is the
//     primary key. INSERT OR IGNORE makes restart + replay safe.
//
//   - Timezone: time.Now().Local() is captured ONCE per Check call and
//     reused for both the SUM-by-month query window and the year_month
//     key. No drift inside a single Check.
package usage

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ThresholdTiers is the locked escalating-percent list the spec calls
// out (plan §2.5). Each tier fires at most once per (year_month, pct)
// thanks to the cost_threshold_fired primary key.
var ThresholdTiers = []int{50, 80, 100, 150, 200}

// Publisher is the broker-emit surface the threshold checker uses to
// announce a `cost.threshold.crossed` event. The rpc layer adapts its
// StreamBroker to this interface in api.go; tests can pass a recording
// fake. A nil Publisher silently disables event emission (the database
// row still goes in so a later restart with a real publisher does not
// re-fire the same tier).
type Publisher interface {
	Publish(topic string, payload any)
}

// ThresholdEventTopic is the broker topic the checker publishes on
// when a new tier crosses. Frontend's CostThresholdToast listens on
// this topic via the existing event-stream composable.
const ThresholdEventTopic = "cost.threshold.crossed"

// ThresholdCrossedPayload is the typed event payload. The frontend
// types in lib/types.ts mirror these field names exactly (camelCase
// via the json tags) so the toast can render without any translation
// shim.
type ThresholdCrossedPayload struct {
	// Pct is the tier that just crossed (50/80/100/150/200).
	Pct int `json:"pct"`
	// MonthTotalUSD is the calendar-month total spend in USD as of the
	// turn that triggered the crossing.
	MonthTotalUSD float64 `json:"monthTotalUsd"`
	// ThresholdUSD is the user-configured MonthlyCostNotifyUSD value
	// (the dial setting) at the moment of the firing. Surfaced so the
	// toast can render "you've used 80% of your $25/mo budget."
	ThresholdUSD float64 `json:"thresholdUsd"`
	// YearMonth is the local-time YYYY-MM key the row was written under.
	YearMonth string `json:"yearMonth"`
	// FiredAt is the wall clock at which the tier was recorded. Useful
	// for downstream UIs that want to show "fired 3 minutes ago."
	FiredAt time.Time `json:"firedAt"`
}

// ThresholdReader is the func() (float64, error) callback the checker
// uses to fetch the current MonthlyCostNotifyUSD setting. We use a
// callback (not a fixed float64) so the dial takes effect on the next
// Add without restarting the process — the settings store reads the
// freshest value from disk on every call.
type ThresholdReader func() (float64, error)

// MonthlyTotalReader returns the total cost_usd in USD for the
// calendar month containing now (in now's local timezone). Empty
// session_messages or NULL cost_usd contribute zero.
type MonthlyTotalReader func(ctx context.Context, now time.Time) (float64, error)

// FiredRecorder is the dedup primitive the checker uses to attempt
// recording a (year_month, pct) firing. It MUST INSERT OR IGNORE and
// return inserted=true ONLY when a fresh row was written. A second
// caller for the same (year_month, pct) MUST return inserted=false.
type FiredRecorder func(ctx context.Context, ym string, pct int, firedAt time.Time) (inserted bool, err error)

// Checker is the threshold scheduler. Built once at API construction
// and called from Manager.Add's tail.
//
// The checker holds no per-month state: every Check call re-reads the
// total from MonthlyTotalReader and the threshold from ThresholdReader,
// so changes to the dial take effect on the very next turn.
type Checker struct {
	threshold   ThresholdReader
	monthly     MonthlyTotalReader
	fired       FiredRecorder
	publisher   Publisher
	tiers       []int
	nowLocal    func() time.Time
}

// CheckerConfig wires the checker. All fields except Publisher are
// required; passing a nil Publisher disables event emission but keeps
// the dedup writes intact.
type CheckerConfig struct {
	Threshold ThresholdReader
	Monthly   MonthlyTotalReader
	Fired     FiredRecorder
	Publisher Publisher
	// Tiers overrides the default ThresholdTiers. Empty/nil falls back
	// to the canonical [50, 80, 100, 150, 200].
	Tiers []int
	// NowLocal is the time source. nil falls back to time.Now().Local().
	// Tests inject a fake clock here.
	NowLocal func() time.Time
}

// NewChecker constructs a Checker. Returns an error when any required
// callback is missing — the caller should fail loudly at boot rather
// than silently shipping a no-op scheduler.
func NewChecker(cfg CheckerConfig) (*Checker, error) {
	if cfg.Threshold == nil {
		return nil, errors.New("usage/threshold: nil ThresholdReader")
	}
	if cfg.Monthly == nil {
		return nil, errors.New("usage/threshold: nil MonthlyTotalReader")
	}
	if cfg.Fired == nil {
		return nil, errors.New("usage/threshold: nil FiredRecorder")
	}
	tiers := cfg.Tiers
	if len(tiers) == 0 {
		tiers = ThresholdTiers
	}
	now := cfg.NowLocal
	if now == nil {
		now = func() time.Time { return time.Now().Local() }
	}
	return &Checker{
		threshold: cfg.Threshold,
		monthly:   cfg.Monthly,
		fired:     cfg.Fired,
		publisher: cfg.Publisher,
		tiers:     tiers,
		nowLocal:  now,
	}, nil
}

// Check evaluates the calendar-month total against the dial and
// publishes a `cost.threshold.crossed` event for every newly-crossed
// tier. Returns the slice of tiers actually fired on this call (mostly
// useful for tests; production callers can ignore it).
//
// Errors from the underlying readers are returned wrapped — the chat
// runner logs and continues so a transient SQL failure does not break
// the user's chat turn.
func (c *Checker) Check(ctx context.Context) ([]int, error) {
	threshold, err := c.threshold()
	if err != nil {
		return nil, fmt.Errorf("usage/threshold: read dial: %w", err)
	}
	if threshold <= 0 {
		// Dial disabled — no firings, no SQL writes.
		return nil, nil
	}

	now := c.nowLocal()
	total, err := c.monthly(ctx, now)
	if err != nil {
		return nil, fmt.Errorf("usage/threshold: read month total: %w", err)
	}

	ym := now.Format("2006-01")
	pct := total / threshold * 100.0

	var firedNow []int
	for _, tier := range c.tiers {
		if pct < float64(tier) {
			continue
		}
		inserted, err := c.fired(ctx, ym, tier, now)
		if err != nil {
			return firedNow, fmt.Errorf("usage/threshold: record fired (ym=%s pct=%d): %w", ym, tier, err)
		}
		if !inserted {
			continue
		}
		firedNow = append(firedNow, tier)
		if c.publisher != nil {
			c.publisher.Publish(ThresholdEventTopic, ThresholdCrossedPayload{
				Pct:           tier,
				MonthTotalUSD: total,
				ThresholdUSD:  threshold,
				YearMonth:     ym,
				FiredAt:       now,
			})
		}
	}
	return firedNow, nil
}

// MonthBoundsLocal returns the [start, end) instants for the calendar
// month containing now in now's location. Exposed for the SQL
// MonthlyTotalReader implementation in usage.go.
func MonthBoundsLocal(now time.Time) (start, end time.Time) {
	loc := now.Location()
	start = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
	end = start.AddDate(0, 1, 0)
	return start, end
}

// ManagerWithChecker is the subset of Manager the threshold helper
// needs to wire a Checker. Both the production sqlManager and any
// test fake satisfying these methods qualify.
type ManagerWithChecker interface {
	MonthlyTotalUSD(ctx context.Context, now time.Time) (float64, error)
	RecordFired(ctx context.Context, ym string, pct int, firedAt time.Time) (bool, error)
}

// NewCheckerFromManager is the convenience constructor the rpc layer
// uses at boot. It wires the manager's MonthlyTotalUSD + RecordFired
// methods straight through and threads the supplied threshold +
// publisher callbacks. Returns a ready-to-use *Checker that can be
// installed via Manager.SetThresholdChecker.
func NewCheckerFromManager(m ManagerWithChecker, threshold ThresholdReader, publisher Publisher) (*Checker, error) {
	return NewChecker(CheckerConfig{
		Threshold: threshold,
		Monthly:   m.MonthlyTotalUSD,
		Fired:     m.RecordFired,
		Publisher: publisher,
	})
}
