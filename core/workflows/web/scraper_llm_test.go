package web_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/workflows/web"
)

// ---------------------------------------------------------------------------
// Fake LLM implementation — mirrors the LLMStreamer interface in web/.
// ---------------------------------------------------------------------------

type fakeLLM struct {
	chunks []string
	err    error
}

func (f *fakeLLM) Stream(_ context.Context, req web.LLMRequest) (web.LLMStream, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &fakeStream{chunks: f.chunks, prompt: req.Prompt}, nil
}

type fakeStream struct {
	chunks []string
	prompt string
	once   sync.Once
	ch     chan web.LLMStreamEvent
}

func (s *fakeStream) Events() <-chan web.LLMStreamEvent {
	s.once.Do(func() {
		s.ch = make(chan web.LLMStreamEvent, len(s.chunks)+1)
		for _, c := range s.chunks {
			s.ch <- web.LLMStreamEvent{Text: c}
		}
		close(s.ch)
	})
	return s.ch
}

func (s *fakeStream) Final() (string, error) {
	return strings.Join(s.chunks, ""), nil
}

// ---------------------------------------------------------------------------
// ScrapeLLM tests
// ---------------------------------------------------------------------------

func TestScrapeLLM_AccumulatesChunks(t *testing.T) {
	llm := &fakeLLM{chunks: []string{"headline: ", "Big News"}}
	out, err := web.ScrapeLLM(t.Context(), llm, "<html><h1>Big News</h1></html>",
		web.ScrapeLLMOptions{
			Profile: "p1",
			Prompt:  "Extract the headline",
		})
	if err != nil {
		t.Fatalf("ScrapeLLM: %v", err)
	}
	if out != "headline: Big News" {
		t.Errorf("output: got %q want %q", out, "headline: Big News")
	}
}

func TestScrapeLLM_PromptIncludesHTMLBody(t *testing.T) {
	var capturedPrompt string
	llm := &captureLLM{fn: func(prompt string) []string {
		capturedPrompt = prompt
		return []string{"done"}
	}}

	body := "<html><p>some content</p></html>"
	_, err := web.ScrapeLLM(t.Context(), llm, body,
		web.ScrapeLLMOptions{
			Profile: "p1",
			Prompt:  "Extract articles",
		})
	if err != nil {
		t.Fatalf("ScrapeLLM: %v", err)
	}
	if !strings.Contains(capturedPrompt, body) {
		t.Errorf("prompt did not contain HTML body")
	}
	if !strings.Contains(capturedPrompt, "Extract articles") {
		t.Errorf("prompt did not contain extract directive")
	}
}

func TestScrapeLLM_ErrorsWithoutLLM(t *testing.T) {
	_, err := web.ScrapeLLM(t.Context(), nil, "<html></html>",
		web.ScrapeLLMOptions{Profile: "p1", Prompt: "extract"})
	if err == nil {
		t.Fatal("expected error for nil LLM")
	}
}

func TestScrapeLLM_ErrorsWithoutProfile(t *testing.T) {
	llm := &fakeLLM{chunks: []string{"ok"}}
	_, err := web.ScrapeLLM(t.Context(), llm, "<html></html>",
		web.ScrapeLLMOptions{Prompt: "extract"}) // no Profile
	if err == nil {
		t.Fatal("expected error for missing profile")
	}
}

func TestScrapeLLM_ErrorsWithoutPrompt(t *testing.T) {
	llm := &fakeLLM{chunks: []string{"ok"}}
	_, err := web.ScrapeLLM(t.Context(), llm, "<html></html>",
		web.ScrapeLLMOptions{Profile: "p1"}) // no Prompt
	if err == nil {
		t.Fatal("expected error for missing prompt")
	}
}

func TestScrapeLLM_PropagatesStreamError(t *testing.T) {
	llm := &fakeLLM{err: errors.New("model overloaded")}
	_, err := web.ScrapeLLM(t.Context(), llm, "<html></html>",
		web.ScrapeLLMOptions{Profile: "p1", Prompt: "extract"})
	if err == nil {
		t.Fatal("expected error when stream fails")
	}
	if !strings.Contains(err.Error(), "model overloaded") {
		t.Errorf("error: got %v want to contain 'model overloaded'", err)
	}
}

// ---------------------------------------------------------------------------
// captureLLM — captures the prompt for inspection
// ---------------------------------------------------------------------------

type captureLLM struct {
	fn func(string) []string
}

func (c *captureLLM) Stream(_ context.Context, req web.LLMRequest) (web.LLMStream, error) {
	chunks := c.fn(req.Prompt)
	return &fakeStream{chunks: chunks, prompt: req.Prompt}, nil
}
