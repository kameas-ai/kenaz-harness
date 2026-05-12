package narrative

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// NarrativeWriter is the narrow interface the Promoter uses to add
// narrative chunks to the memory store. The rpc layer wires the full
// core/memory.Store through an adapter; tests pass a fake.
//
// The interface is intentionally narrow: the Promoter never reads from
// the store, only writes new chunks and deletes extractive fallbacks.
type NarrativeWriter interface {
	// WriteNarrative adds a narrative chunk with the given content,
	// kind, sessionID, projectID, turnID, and retrieval weight.
	// Returns the new chunk's ID, or ErrDuplicate if the chunk was
	// already written in this cycle.
	WriteNarrative(ctx context.Context, req NarrativeWriteReq) (id string, err error)
	// DeleteByTurnFallback deletes the extractive-fallback chunk for
	// (sessionID, turnID), if one exists. Missing chunk is not an
	// error (idempotent).
	DeleteByTurnFallback(ctx context.Context, sessionID, turnID string) error
}

// NarrativeWriteReq is the write request shape for WriteNarrative.
type NarrativeWriteReq struct {
	SessionID       string
	ProjectID       string
	TurnID          string
	Content         string
	Kind            ChunkKind
	RetrievalWeight float32
	Source          string
}

// PromoterConfig tunes the Promoter worker pool.
type PromoterConfig struct {
	// Parallelism is the number of worker goroutines. Default 2.
	Parallelism int
	// PollInterval is how often the promoter scans the job queue for
	// ready jobs. Default 5s.
	PollInterval time.Duration
	// BackpressureLimit is the pending-job count above which Enqueue
	// blocks for up to BackpressureTimeout before dropping + logging.
	// Default 1000.
	BackpressureLimit int
	// BackpressureTimeout is how long Enqueue blocks when the queue is
	// above BackpressureLimit. Default 10s.
	BackpressureTimeout time.Duration
}

func (c *PromoterConfig) defaults() {
	if c.Parallelism <= 0 {
		c.Parallelism = 2
	}
	if c.PollInterval <= 0 {
		c.PollInterval = 5 * time.Second
	}
	if c.BackpressureLimit <= 0 {
		c.BackpressureLimit = 1000
	}
	if c.BackpressureTimeout <= 0 {
		c.BackpressureTimeout = 10 * time.Second
	}
}

// Promoter is the async worker pool that processes narrative synthesis
// jobs. Start it once at boot; it runs until ctx is cancelled.
type Promoter struct {
	cfg        PromoterConfig
	queue      JobQueue
	writer     NarrativeWriter
	synthetic  *SyntheticBuilder
	extractive *ExtractiveBuilder
	now        func() time.Time

	// pending is a buffered channel used for backpressure tracking.
	// Its cap matches BackpressureLimit; a full channel means we're at
	// the limit and Enqueue should block/drop.
	pending chan struct{}

	mu     sync.Mutex
	stats  PromoterStats
}

// PromoterStats holds running counters for observability.
type PromoterStats struct {
	Processed   int64
	Succeeded   int64
	Failed      int64
	FallbacksWritten int64
}

// NewPromoter constructs a Promoter. Call Start to begin processing.
func NewPromoter(cfg PromoterConfig, queue JobQueue, writer NarrativeWriter, synthetic *SyntheticBuilder) *Promoter {
	cfg.defaults()
	return &Promoter{
		cfg:        cfg,
		queue:      queue,
		writer:     writer,
		synthetic:  synthetic,
		extractive: NewExtractiveBuilder(),
		now:        time.Now,
		pending:    make(chan struct{}, cfg.BackpressureLimit),
	}
}

// SetClock overrides the clock. Used in tests.
func (p *Promoter) SetClock(fn func() time.Time) {
	p.now = fn
}

// Stats returns a snapshot of running counters.
func (p *Promoter) Stats() PromoterStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stats
}

// Start launches the worker pool goroutines and a scanner goroutine.
// Returns immediately. The goroutines exit when ctx is cancelled.
func (p *Promoter) Start(ctx context.Context) {
	work := make(chan Job, p.cfg.Parallelism*2)

	// Scanner: periodically pops ready jobs from the queue and sends
	// them to the work channel.
	go p.scanner(ctx, work)

	// Workers: one goroutine per parallelism slot.
	for i := 0; i < p.cfg.Parallelism; i++ {
		go p.worker(ctx, work)
	}
}

// Enqueue adds a synthesis job for the given turn. If backpressure
// limit is exceeded, it blocks for up to BackpressureTimeout and then
// drops the job with a log warning. The extractive fallback is always
// available regardless.
func (p *Promoter) Enqueue(ctx context.Context, j Job) error {
	if !Enabled() {
		return nil
	}
	// Backpressure: try to acquire a slot.
	select {
	case p.pending <- struct{}{}:
		// Slot acquired; release it after the job is enqueued.
		defer func() { <-p.pending }()
	default:
		// Full — wait with timeout.
		timer := time.NewTimer(p.cfg.BackpressureTimeout)
		defer timer.Stop()
		select {
		case p.pending <- struct{}{}:
			defer func() { <-p.pending }()
		case <-timer.C:
			log.Printf("narrative: promoter backpressure: dropping job for session=%s turn=%s", j.SessionID, j.TurnID)
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return p.queue.Enqueue(ctx, j)
}

func (p *Promoter) scanner(ctx context.Context, work chan<- Job) {
	ticker := time.NewTicker(p.cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			jobs, err := p.queue.Pop(ctx, p.now(), p.cfg.Parallelism*4)
			if err != nil {
				log.Printf("narrative: queue pop error: %v", err)
				continue
			}
			for _, j := range jobs {
				select {
				case work <- j:
				case <-ctx.Done():
					return
				}
			}
		}
	}
}

func (p *Promoter) worker(ctx context.Context, work <-chan Job) {
	for {
		select {
		case <-ctx.Done():
			return
		case j, ok := <-work:
			if !ok {
				return
			}
			p.processJob(ctx, j)
		}
	}
}

func (p *Promoter) processJob(ctx context.Context, j Job) {
	p.mu.Lock()
	p.stats.Processed++
	p.mu.Unlock()

	result, err := p.synthetic.Build(ctx, j.Payload)
	if err != nil {
		p.handleJobFailure(ctx, j, err)
		return
	}

	// Success: write synthesised narrative and delete extractive fallback
	// in a logical transaction (best-effort — if delete fails, the
	// fallback is orphaned but harmless).
	content := result.String()
	_, writeErr := p.writer.WriteNarrative(ctx, NarrativeWriteReq{
		SessionID:       j.SessionID,
		ProjectID:       j.ProjectID,
		TurnID:          j.TurnID,
		Content:         content,
		Kind:            ChunkKindNarrativeSynthesised,
		RetrievalWeight: DefaultRetrievalWeight,
		Source:          "narrative_promoter",
	})
	if writeErr != nil {
		log.Printf("narrative: write synthesised failed for job %s: %v", j.ID, writeErr)
		p.handleJobFailure(ctx, j, writeErr)
		return
	}

	// Delete extractive fallback for this turn (best-effort).
	_ = p.writer.DeleteByTurnFallback(ctx, j.SessionID, j.TurnID)

	if err := p.queue.Update(ctx, j.ID, JobStatusSucceeded, time.Time{}, ""); err != nil {
		log.Printf("narrative: queue update succeeded failed for job %s: %v", j.ID, err)
	}

	p.mu.Lock()
	p.stats.Succeeded++
	p.mu.Unlock()
}

func (p *Promoter) handleJobFailure(ctx context.Context, j Job, err error) {
	nextAttempt := j.Attempt + 1
	if Exhausted(nextAttempt) {
		// Mark failed; extractive fallback remains visible.
		if uErr := p.queue.Update(ctx, j.ID, JobStatusFailed, time.Time{}, fmt.Sprintf("%v", err)); uErr != nil {
			log.Printf("narrative: queue update failed for job %s: %v", j.ID, uErr)
		}
		p.mu.Lock()
		p.stats.Failed++
		p.mu.Unlock()

		log.Printf("narrative: job %s for turn %s exhausted retries: %v", j.ID, j.TurnID, err)
		return
	}

	// First failure on attempt 0: write extractive fallback immediately.
	if nextAttempt == 1 {
		p.writeFallback(ctx, j)
	}

	nextRetry := NextRetryAt(nextAttempt, p.now())
	status := JobStatusRetrying
	if err := p.queue.Update(ctx, j.ID, status, nextRetry, fmt.Sprintf("%v", err)); err != nil {
		log.Printf("narrative: queue schedule retry failed for job %s: %v", j.ID, err)
	}
}

func (p *Promoter) writeFallback(ctx context.Context, j Job) {
	fb := p.extractive.BuildTurnFallback(
		j.Payload.LastUserMsg,
		j.Payload.LastAssistantMsg,
		j.Payload.ToolNarratives,
	)
	_, err := p.writer.WriteNarrative(ctx, NarrativeWriteReq{
		SessionID:       j.SessionID,
		ProjectID:       j.ProjectID,
		TurnID:          j.TurnID,
		Content:         fb.String(),
		Kind:            ChunkKindNarrativeExtractiveFallback,
		RetrievalWeight: DefaultRetrievalWeight,
		Source:          "narrative_extractive_fallback",
	})
	if err != nil {
		log.Printf("narrative: write extractive fallback failed for job %s: %v", j.ID, err)
		return
	}
	p.mu.Lock()
	p.stats.FallbacksWritten++
	p.mu.Unlock()
}
