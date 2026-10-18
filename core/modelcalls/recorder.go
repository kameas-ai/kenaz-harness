package modelcalls

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/llm/genaitrace"
)

const (
	// recorderQueueSize bounds the calls waiting to be written. A full
	// queue drops the record (counted) rather than block the stream that
	// is ending — RecordCall runs on the chat path.
	recorderQueueSize = 512
	recorderBatchSize = 64
	recorderFlushTick = time.Second
)

// Recorder is the production genaitrace.Recorder: it queues each finished
// call without blocking and a single writer goroutine inserts them in
// batches. After a batch lands, OnStored (when set) sees every record in
// it — the stats pull hangs off that hook.
type Recorder struct {
	store  *Store
	logger *slog.Logger

	// OnStored is called from the writer goroutine for each record of a
	// batch that was written. Set before Start; nil = no hook.
	OnStored func(genaitrace.CallRecord)

	queue   chan genaitrace.CallRecord
	dropped atomic.Uint64
	written atomic.Uint64
	closed  atomic.Bool
	started atomic.Bool

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	done      chan struct{}
}

// NewRecorder returns a Recorder writing to store. Call Start, then
// install it with genaitrace.SetRecorder.
func NewRecorder(store *Store, logger *slog.Logger) *Recorder {
	if logger == nil {
		logger = slog.Default()
	}
	return &Recorder{
		store:  store,
		logger: logger,
		queue:  make(chan genaitrace.CallRecord, recorderQueueSize),
		stopCh: make(chan struct{}),
		done:   make(chan struct{}),
	}
}

// RecordCall enqueues rec. Never blocks: a full queue, or a recorder that
// has been closed, drops it and counts the drop.
func (r *Recorder) RecordCall(rec genaitrace.CallRecord) {
	if r == nil {
		return
	}
	if r.closed.Load() {
		r.dropped.Add(1)
		return
	}
	select {
	case r.queue <- rec:
	default:
		r.dropped.Add(1)
	}
}

// Dropped is the number of records dropped (queue full or closed).
func (r *Recorder) Dropped() uint64 { return r.dropped.Load() }

// Written is the number of records handed to the store successfully.
func (r *Recorder) Written() uint64 { return r.written.Load() }

// Start launches the writer goroutine. Idempotent.
func (r *Recorder) Start() {
	r.startOnce.Do(func() {
		r.started.Store(true)
		go r.run()
	})
}

// Close stops accepting records, drains the queue into the store and
// waits for the writer (bounded by ctx). Idempotent; a recorder that was
// never started has nothing to drain.
func (r *Recorder) Close(ctx context.Context) error {
	r.stopOnce.Do(func() {
		r.closed.Store(true)
		close(r.stopCh)
	})
	if !r.started.Load() {
		return nil
	}
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Recorder) run() {
	defer close(r.done)
	batch := make([]genaitrace.CallRecord, 0, recorderBatchSize)
	tick := time.NewTicker(recorderFlushTick)
	defer tick.Stop()
	flush := func() {
		if len(batch) == 0 {
			return
		}
		r.write(batch)
		batch = batch[:0]
	}
	for {
		select {
		case rec := <-r.queue:
			batch = append(batch, rec)
			if len(batch) >= recorderBatchSize {
				flush()
			}
		case <-tick.C:
			flush()
		case <-r.stopCh:
			for {
				select {
				case rec := <-r.queue:
					batch = append(batch, rec)
					if len(batch) >= recorderBatchSize {
						flush()
					}
				default:
					flush()
					return
				}
			}
		}
	}
}

func (r *Recorder) write(batch []genaitrace.CallRecord) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.store.Insert(ctx, batch); err != nil {
		// Telemetry degrades silently; the error text is a storage error,
		// never record content.
		r.logger.Warn("modelcalls.insert_failed", "err", err.Error(), "batch_size", len(batch))
		r.dropped.Add(uint64(len(batch)))
		return
	}
	r.written.Add(uint64(len(batch)))
	if r.OnStored != nil {
		for _, rec := range batch {
			r.OnStored(rec)
		}
	}
}
