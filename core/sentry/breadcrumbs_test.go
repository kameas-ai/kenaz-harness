package sentry_test

import (
	"sync"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/sentry"
)

func TestRingBuffer_BasicFIFO(t *testing.T) {
	var rb sentry.RingBuffer
	now := time.Now()
	for i := 0; i < 5; i++ {
		rb.Add(sentry.Breadcrumb{
			TS:      now,
			Level:   "info",
			Message: "msg",
		})
	}
	snap := rb.Snapshot()
	if len(snap) != 5 {
		t.Fatalf("expected 5 entries, got %d", len(snap))
	}
}

func TestRingBuffer_EvictsOldest(t *testing.T) {
	var rb sentry.RingBuffer
	now := time.Now()
	// Fill past capacity (50).
	for i := 0; i < 60; i++ {
		rb.Add(sentry.Breadcrumb{
			TS:      now,
			Level:   "info",
			Message: "msg",
			Data:    map[string]any{"i": i},
		})
	}
	snap := rb.Snapshot()
	if len(snap) != 50 {
		t.Fatalf("expected 50 entries after eviction, got %d", len(snap))
	}
	// Oldest entry should be i==10 (60-50).
	first := snap[0].Data["i"].(int)
	if first != 10 {
		t.Errorf("expected oldest i=10, got %d", first)
	}
}

func TestRingBuffer_RaceSafe(t *testing.T) {
	var rb sentry.RingBuffer
	var wg sync.WaitGroup
	now := time.Now()
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rb.Add(sentry.Breadcrumb{TS: now, Level: "info", Message: "concurrent"})
		}()
	}
	// Concurrent reads.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = rb.Snapshot()
		}()
	}
	wg.Wait()
}
