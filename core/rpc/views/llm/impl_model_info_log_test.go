package llm

// Dogfood 2026-10-08 P2: ListProviders logged one DEBUG line per model per
// resolution — 408 llm.model_info.dynamic_hit rows per refresh, a fifth of
// the in-app Logs ring each time. This pins the replacement: one summary
// line per provider, plus at most one miss line with a capped sample.

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"

	connectorllm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/logging"
)

type modelInfoLogCapture struct {
	mu      sync.Mutex
	records []slog.Record
}

func (c *modelInfoLogCapture) Enabled(context.Context, slog.Level) bool { return true }
func (c *modelInfoLogCapture) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, r.Clone())
	return nil
}
func (c *modelInfoLogCapture) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *modelInfoLogCapture) WithGroup(string) slog.Handler      { return c }

func (c *modelInfoLogCapture) withPrefix(prefix string) []slog.Record {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []slog.Record
	for _, r := range c.records {
		if strings.HasPrefix(r.Message, prefix) {
			out = append(out, r)
		}
	}
	return out
}

func recordAttr(r slog.Record, key string) slog.Value {
	var v slog.Value
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			v = a.Value
			return false
		}
		return true
	})
	return v
}

// partialCatalog resolves every model whose id does not start with "unk-".
type partialCatalog struct{}

func (partialCatalog) ContextWindow(_, model string) int {
	if strings.HasPrefix(model, "unk-") {
		return 0
	}
	return 128000
}
func (partialCatalog) MaxOutputTokens(_, _ string) int { return 4096 }
func (partialCatalog) AttachmentLimits(_, _ string) AttachmentLimitsResult {
	return AttachmentLimitsResult{}
}

func TestListProviders_ModelInfoLogsOneSummaryPerProvider(t *testing.T) {
	prev := logging.Handler()
	capture := &modelInfoLogCapture{}
	logging.Replace(capture)
	t.Cleanup(func() { logging.Replace(prev) })

	models := make([]string, 0, 407)
	for i := 0; i < 400; i++ {
		models = append(models, fmt.Sprintf("known-%03d", i))
	}
	for i := 0; i < 7; i++ {
		models = append(models, fmt.Sprintf("unk-%d", i))
	}
	api := New(Config{
		Bundles: &fakeBundles{profiles: []connectorllm.ProviderProfile{{
			ID: "bundle-or", Kind: "openrouter", Model: models[0], Models: models,
		}}},
		CapCatalog: partialCatalog{},
	})

	provs, err := api.ListProviders(context.Background())
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if len(provs) != 1 || len(provs[0].ModelInfos) != len(models) {
		t.Fatalf("ListProviders returned %d providers / %d model infos, want 1 / %d", len(provs), len(provs[0].ModelInfos), len(models))
	}

	all := capture.withPrefix("llm.model_info.")
	if len(all) > 3 {
		t.Fatalf("one refresh of %d models emitted %d llm.model_info.* lines, want at most a handful (<=3)", len(models), len(all))
	}
	for _, r := range all {
		if r.Message == "llm.model_info.dynamic_hit" || r.Message == "llm.model_info.catalog_hit" {
			t.Errorf("per-model %s line still emitted", r.Message)
		}
	}

	resolved := capture.withPrefix("llm.model_info.resolved")
	if len(resolved) != 1 {
		t.Fatalf("got %d llm.model_info.resolved lines, want exactly 1", len(resolved))
	}
	r := resolved[0]
	if r.Level != slog.LevelDebug {
		t.Errorf("resolved summary level = %v, want DEBUG", r.Level)
	}
	if got := recordAttr(r, "catalog_hits").Int64(); got != 400 {
		t.Errorf("catalog_hits = %d, want 400", got)
	}
	if got := recordAttr(r, "misses").Int64(); got != 7 {
		t.Errorf("misses = %d, want 7", got)
	}
	if got := recordAttr(r, "provider_id").String(); got != "bundle-or" {
		t.Errorf("provider_id = %q, want bundle-or", got)
	}

	misses := capture.withPrefix("llm.model_info.miss")
	if len(misses) != 1 {
		t.Fatalf("got %d llm.model_info.miss lines, want exactly 1 (per provider, not per model)", len(misses))
	}
	if got := recordAttr(misses[0], "miss_count").Int64(); got != 7 {
		t.Errorf("miss_count = %d, want 7", got)
	}
	sample := strings.Split(recordAttr(misses[0], "model_ids_sample").String(), ",")
	if len(sample) != modelInfoMissSampleMax {
		t.Errorf("model_ids_sample has %d ids, want capped at %d", len(sample), modelInfoMissSampleMax)
	}
}
