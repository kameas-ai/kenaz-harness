package modelcalls

// stats.go — the OpenRouter generation-stats pull (llm-call-telemetry-
// 01LLMTEL01 WP02, owner ruling 2026-10-09: "a pull of provider-side stats
// where a provider offers them … on by default, asynchronous and
// rate-limited, and is disabled only by a setting").
//
// After an OpenRouter call that succeeded with a generation id lands in
// the store, the puller asks its StatsSource — the LLM registry, which
// resolves the profile's own API key for the one request and GETs
// {base}/api/v1/generation?id=<id> (core/llm/openrouter
// FetchGenerationStats) — so this package never holds a credential. The
// stats appear on OpenRouter's side a few
// seconds after the stream ends, so a 404 (and a 429 / 5xx / network
// failure) is retried with backoff; after the last retry the job is
// dropped and the row marked gave_up. Every GET passes a token bucket
// (default 1/s, burst 5) so a burst of calls cannot become a burst of
// requests.
//
// Only numeric fields and two provider enum tokens are decoded from the
// response (by the openrouter package), and each is bounded by the
// genaitrace allowlist (BoundProviderStats) before it reaches the store.
// No text field of the response is ever read.

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/genaitrace"
)

// openRouterProvider is OpenRouter's gen_ai.provider.name.
const openRouterProvider = "openrouter"

// StatsSource performs one provider-side stats pull for a generation of a
// profile. Implemented by core/llm/registry.Registry (FetchProviderStats),
// which resolves and zeroes the credential itself. An error wrapping
// llm.ErrProviderStatsNotReady means "retry later"; any other is final.
type StatsSource interface {
	FetchProviderStats(ctx context.Context, profileID, generationID string) (genaitrace.RawProviderStats, error)
}

// StatsPullerOptions configures NewStatsPuller. Zero values take the
// production defaults.
type StatsPullerOptions struct {
	Store  *Store
	Source StatsSource
	// Enabled reads the "Fetch OpenRouter generation stats" setting; it is
	// consulted for every call (nil = always on, the setting's default).
	Enabled func() bool
	// RatePerSec / Burst shape the token bucket (default 1/s, burst 5).
	RatePerSec float64
	Burst      int
	// Backoff is the wait before each retry; its length is the retry
	// count (default 2s, 4s, 8s, 16s).
	Backoff []time.Duration
	Logger  *slog.Logger

	// now / sleep are test seams for the token bucket.
	now   func() time.Time
	sleep func(d time.Duration, stop <-chan struct{}) bool
}

// StatsPuller runs the pull. Consider is its only input (wired as the
// Recorder's OnStored hook); one worker goroutine performs every GET.
type StatsPuller struct {
	store   *Store
	source  StatsSource
	enabled func() bool
	backoff []time.Duration
	logger  *slog.Logger
	bucket  *tokenBucket
	sleep   func(d time.Duration, stop <-chan struct{}) bool

	queue   chan statsJob
	stopCh  chan struct{}
	done    chan struct{}
	stopped atomic.Bool

	startOnce sync.Once
	stopOnce  sync.Once

	requests atomic.Uint64
	fetched  atomic.Uint64
	gaveUp   atomic.Uint64
	dropped  atomic.Uint64
}

type statsJob struct {
	spanID    string
	profileID string
	genID     string
	attempt   int
}

const statsQueueSize = 256

// NewStatsPuller returns a puller; call Start.
func NewStatsPuller(o StatsPullerOptions) *StatsPuller {
	if o.RatePerSec <= 0 {
		o.RatePerSec = 1
	}
	if o.Burst <= 0 {
		o.Burst = 5
	}
	if o.Backoff == nil {
		o.Backoff = []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second}
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.now == nil {
		o.now = time.Now
	}
	if o.sleep == nil {
		o.sleep = sleepOrStop
	}
	if o.Enabled == nil {
		o.Enabled = func() bool { return true }
	}
	return &StatsPuller{
		store:   o.Store,
		source:  o.Source,
		enabled: o.Enabled,
		backoff: o.Backoff,
		logger:  o.Logger,
		bucket:  newTokenBucket(o.RatePerSec, o.Burst, o.now),
		sleep:   o.sleep,
		queue:   make(chan statsJob, statsQueueSize),
		stopCh:  make(chan struct{}),
		done:    make(chan struct{}),
	}
}

// StatsPullerCounters is a payload-free snapshot.
type StatsPullerCounters struct {
	Requests uint64 `json:"requests"`
	Fetched  uint64 `json:"fetched"`
	GaveUp   uint64 `json:"gaveUp"`
	Dropped  uint64 `json:"dropped"`
}

// Counters reports the puller's counters.
func (p *StatsPuller) Counters() StatsPullerCounters {
	return StatsPullerCounters{
		Requests: p.requests.Load(), Fetched: p.fetched.Load(),
		GaveUp: p.gaveUp.Load(), Dropped: p.dropped.Load(),
	}
}

// Pending is the number of pulls queued and not yet taken by the worker.
func (p *StatsPuller) Pending() int { return len(p.queue) }

// Consider queues a pull for rec when it is a successful OpenRouter call
// with a generation id and the setting is on. Never blocks; a full queue
// drops the job (counted).
func (p *StatsPuller) Consider(rec genaitrace.CallRecord) {
	if p == nil || p.stopped.Load() {
		return
	}
	if !rec.OK || rec.Provider != openRouterProvider || rec.ResponseID == "" ||
		rec.ProfileID == "" || rec.SpanID == "" {
		return
	}
	if !p.enabled() {
		return
	}
	p.enqueue(statsJob{spanID: rec.SpanID, profileID: rec.ProfileID, genID: rec.ResponseID})
}

func (p *StatsPuller) enqueue(j statsJob) {
	if p.stopped.Load() {
		return
	}
	select {
	case p.queue <- j:
	default:
		p.dropped.Add(1)
	}
}

// Start launches the worker. Idempotent.
func (p *StatsPuller) Start() {
	p.startOnce.Do(func() { go p.run() })
}

// Stop ends the worker; queued and scheduled retries are abandoned.
// Idempotent; safe without Start.
func (p *StatsPuller) Stop() {
	p.stopOnce.Do(func() {
		p.stopped.Store(true)
		close(p.stopCh)
	})
	started := true
	p.startOnce.Do(func() { started = false; close(p.done) })
	if started {
		<-p.done
	}
}

func (p *StatsPuller) run() {
	defer close(p.done)
	for {
		select {
		case <-p.stopCh:
			return
		case j := <-p.queue:
			if wait := p.bucket.reserve(); wait > 0 {
				if !p.sleep(wait, p.stopCh) {
					return
				}
			}
			p.attempt(j)
		}
	}
}

func (p *StatsPuller) attempt(j statsJob) {
	// The setting is re-read per attempt: turning it off stops retries too.
	if !p.enabled() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	p.requests.Add(1)
	raw, err := p.source.FetchProviderStats(ctx, j.profileID, j.genID)
	switch {
	case err == nil:
		st := genaitrace.BoundProviderStats(raw)
		if serr := p.store.ApplyProviderStats(ctx, j.spanID, st); serr != nil {
			p.logger.Warn("modelcalls.stats_store_failed", "err", serr.Error())
			return
		}
		p.fetched.Add(1)
	case errors.Is(err, llm.ErrProviderStatsNotReady) && j.attempt < len(p.backoff):
		next := j
		next.attempt++
		time.AfterFunc(p.backoff[j.attempt], func() { p.enqueue(next) })
	default:
		p.gaveUp.Add(1)
		_ = p.store.SetStatsState(ctx, j.spanID, StatsGaveUp)
	}
}

// ── token bucket ─────────────────────────────────────────────────────────────

type tokenBucket struct {
	mu     sync.Mutex
	rate   float64 // tokens per second
	burst  float64
	tokens float64
	last   time.Time
	now    func() time.Time
}

func newTokenBucket(rate float64, burst int, now func() time.Time) *tokenBucket {
	return &tokenBucket{rate: rate, burst: float64(burst), tokens: float64(burst), last: now(), now: now}
}

// reserve takes one token and returns how long the caller must wait
// before using it (0 when one was available).
func (b *tokenBucket) reserve() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	if el := now.Sub(b.last).Seconds(); el > 0 {
		b.tokens += el * b.rate
		if b.tokens > b.burst {
			b.tokens = b.burst
		}
	}
	b.last = now
	b.tokens--
	if b.tokens >= 0 {
		return 0
	}
	return time.Duration(-b.tokens / b.rate * float64(time.Second))
}

func sleepOrStop(d time.Duration, stop <-chan struct{}) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-stop:
		return false
	}
}
