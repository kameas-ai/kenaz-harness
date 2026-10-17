package activity

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// retention_test.go — retention never widens or shortens on a settings
// read failure (pruning is irreversible). Real sqlite throughout.

// flakyRetention is a settings read that can be switched to failing.
type flakyRetention struct {
	mu   sync.Mutex
	days int
	err  error
}

func (f *flakyRetention) set(days int, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.days, f.err = days, err
}

func (f *flakyRetention) read() (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.days, f.err
}

// errSettingsSecret stands in for a read error whose text carries file
// contents; it must never reach a log line.
var errSettingsSecret = errors.New(`settings: parse {"apiKey":"sk-SECRET"}`)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// (a) After a successful read, a failing read prunes to the LAST GOOD
// window: not the 90-day default, and not skipped.
func TestPruner_ReadErrorUsesLastGoodRetention(t *testing.T) {
	s := openAt(t, Path(t.TempDir()))
	ctx := context.Background()
	clk := &clock{t: time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)}
	src := &flakyRetention{days: 7}
	p := NewPruner(s, src.read, clk.now, nil)
	if _, _, err := p.PruneOnce(ctx); err != nil {
		t.Fatal(err)
	}
	// 30 days old: inside the 90-day default, outside the member's 7.
	if _, err := s.Commit(ctx, sampleWrite(clk.now().Add(-30*24*time.Hour).UnixMilli(), "agent-aaaaaaaaaaaaaaaa")); err != nil {
		t.Fatal(err)
	}
	src.set(0, errSettingsSecret)
	ev, tk, err := p.PruneOnce(ctx)
	if err != nil || ev != 2 || tk != 1 {
		t.Fatalf("PruneOnce after a read failure = %d/%d, %v; want 2/1 pruned to the last good 7 days", ev, tk, err)
	}

	// And a long setting is not shortened to the default either.
	src.set(300, nil)
	if _, _, err := p.PruneOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit(ctx, sampleWrite(clk.now().Add(-200*24*time.Hour).UnixMilli(), "agent-bbbbbbbbbbbbbbbb")); err != nil {
		t.Fatal(err)
	}
	src.set(0, errSettingsSecret)
	if ev, tk, err := p.PruneOnce(ctx); err != nil || ev != 0 || tk != 0 {
		t.Fatalf("PruneOnce after a read failure = %d/%d, %v; want nothing (last good 300 days)", ev, tk, err)
	}
	if st, _ := s.Stats(ctx); st.Events != 2 || st.Tasks != 1 {
		t.Fatalf("rows = %+v; the 200-day-old write must survive a 300-day last-good window", st)
	}
}

// (b) The first pass of the process fails: nothing is deleted, and a
// later successful read prunes normally.
func TestPruner_FirstPassReadFailsDeletesNothing(t *testing.T) {
	s := openAt(t, Path(t.TempDir()))
	ctx := context.Background()
	clk := &clock{t: time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)}
	if _, err := s.Commit(ctx, sampleWrite(clk.now().Add(-200*24*time.Hour).UnixMilli(), "agent-ffffffffffffffff")); err != nil {
		t.Fatal(err)
	}
	src := &flakyRetention{err: errSettingsSecret}
	p := NewPruner(s, src.read, clk.now, nil)
	if ev, tk, err := p.PruneOnce(ctx); err != ErrRetentionUnreadable || ev != 0 || tk != 0 {
		t.Fatalf("first PruneOnce = %d/%d, %v; want skipped", ev, tk, err)
	}
	if st, _ := s.Stats(ctx); st.Events != 2 || st.Tasks != 1 {
		t.Fatalf("rows after a skipped first pass = %+v", st)
	}
	src.set(0, nil)
	if ev, tk, err := p.PruneOnce(ctx); err != nil || ev != 2 || tk != 1 {
		t.Fatalf("PruneOnce once readable = %d/%d, %v; want 2/1", ev, tk, err)
	}
}

// (d) Three consecutive failed passes, or more than 24 h since the last
// good read, flip Blocked; once blocked the pruner deletes nothing even
// with a last-good value; a good read clears it. The log carries counts
// only.
func TestRetention_BlockedThresholds(t *testing.T) {
	s := openAt(t, Path(t.TempDir()))
	ctx := context.Background()
	clk := &clock{t: time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)}
	src := &flakyRetention{days: 7}
	var logs bytes.Buffer
	p := NewPruner(s, src.read, clk.now, slog.New(slog.NewJSONHandler(&logs, nil)))
	r := p.Retention()
	p.pass()
	src.set(0, errSettingsSecret)
	p.pass()
	// Two failures: not blocked, so the last good window still applies.
	if _, err := s.Commit(ctx, sampleWrite(clk.now().Add(-30*24*time.Hour).UnixMilli(), "agent-aaaaaaaaaaaaaaaa")); err != nil {
		t.Fatal(err)
	}
	p.pass()
	if h := r.Health(); h.Blocked || h.ConsecutiveFailedPasses != 2 {
		t.Fatalf("after 2 failed passes: %+v; want not blocked", h)
	}
	if st, _ := s.Stats(ctx); st.Events != 0 || st.Tasks != 0 {
		t.Fatalf("rows after an unblocked failed pass = %+v; want pruned to the last good 7 days", st)
	}
	// The third failure blocks: from then on nothing is deleted.
	if _, err := s.Commit(ctx, sampleWrite(clk.now().Add(-30*24*time.Hour).UnixMilli(), "agent-bbbbbbbbbbbbbbbb")); err != nil {
		t.Fatal(err)
	}
	p.pass()
	if h := r.Health(); !h.Blocked || h.ConsecutiveFailedPasses != 3 {
		t.Fatalf("after 3 failed passes: %+v; want blocked", h)
	}
	if ev, tk, err := p.PruneOnce(ctx); err != ErrRetentionUnreadable || ev != 0 || tk != 0 {
		t.Fatalf("blocked PruneOnce = %d/%d, %v; want nothing deleted", ev, tk, err)
	}
	if st, _ := s.Stats(ctx); st.Events != 2 || st.Tasks != 1 {
		t.Fatalf("rows while blocked = %+v; want the second write kept", st)
	}
	out := logs.String()
	if !strings.Contains(out, "mlproducer.activity.retention_blocked") || !strings.Contains(out, `"consecutive_failures":3`) || !strings.Contains(out, `"hours_since_last_good":0`) {
		t.Fatalf("blocked log missing counts:\n%s", out)
	}
	if strings.Contains(out, "SECRET") || strings.Contains(out, "settings: parse") {
		t.Fatalf("log leaked the read error:\n%s", out)
	}

	// A good read (any reader, e.g. the status panel) clears it.
	src.set(7, nil)
	if _, fresh, ok := r.Read(); !fresh || !ok || r.Health().Blocked {
		t.Fatalf("good read did not clear blocked: %+v", r.Health())
	}

	// 24 h: one failed pass more than a day after the last good read.
	src.set(0, errSettingsSecret)
	clk.advance(BlockedAfter - time.Minute)
	p.pass()
	if h := r.Health(); h.Blocked {
		t.Fatalf("23h59m since last good, 1 failure: %+v; want not blocked", h)
	}
	clk.advance(2 * time.Minute)
	if h := r.Health(); !h.Blocked || h.ConsecutiveFailedPasses != 1 {
		t.Fatalf("24h01m since last good: %+v; want blocked", h)
	}

	// No good read ever: the 24 h counts from construction.
	never := NewRetention(func() (int, error) { return 0, fmt.Errorf("boom") }, clk.now)
	never.readPass()
	if never.Health().Blocked {
		t.Fatal("fresh Retention blocked after one failure")
	}
	clk.advance(BlockedAfter + time.Minute)
	if h := never.Health(); !h.Blocked || h.HaveGood {
		t.Fatalf("never-good after 24h: %+v; want blocked", h)
	}
}
