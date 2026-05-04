package web

import (
	"context"
	"fmt"
	"strings"
)

// LLMStreamer is the narrow interface the LLM scraper dispatches
// against. It mirrors the workflows-package LLMStreamer so callers can
// pass the same dependency through without import cycles.
type LLMStreamer interface {
	Stream(ctx context.Context, req LLMRequest) (LLMStream, error)
}

// LLMRequest is a minimal generation request.
type LLMRequest struct {
	ProfileID string
	Model     string
	Prompt    string
}

// LLMStream is the streaming surface the scraper consumes.
type LLMStream interface {
	Events() <-chan LLMStreamEvent
	Final() (string, error)
}

// LLMStreamEvent is one text-delta from the model.
type LLMStreamEvent struct {
	Text string
	Err  string
}

// ScrapeLLMOptions configures an LLM-driven extraction call.
type ScrapeLLMOptions struct {
	// Profile is the LLM provider profile ID passed to Stream.
	Profile string
	// Model overrides the profile's default model.
	Model string
	// Prompt is the extraction directive appended after the HTML body.
	Prompt string
}

// ScrapeLLM sends the HTML body plus the extraction prompt to the LLM
// and returns the model's text output. The caller is responsible for
// parsing the output into a structured form if desired.
//
// ScrapeLLM never logs or stores htmlBody — it is only passed as
// context to the model and discarded after the call.
func ScrapeLLM(ctx context.Context, llm LLMStreamer, htmlBody string, opts ScrapeLLMOptions) (string, error) {
	if llm == nil {
		return "", fmt.Errorf("web: LLM scraper: no LLMStreamer wired")
	}
	if opts.Profile == "" {
		return "", fmt.Errorf("web: LLM scraper: profile required")
	}
	if opts.Prompt == "" {
		return "", fmt.Errorf("web: LLM scraper: extract_prompt required")
	}

	// Compose the full prompt: directive followed by the HTML body so
	// the model can ground its extraction in the actual content.
	full := strings.Join([]string{
		opts.Prompt,
		"",
		"HTML:",
		"```html",
		htmlBody,
		"```",
	}, "\n")

	stream, err := llm.Stream(ctx, LLMRequest{
		ProfileID: opts.Profile,
		Model:     opts.Model,
		Prompt:    full,
	})
	if err != nil {
		return "", fmt.Errorf("web: LLM scraper: stream: %w", err)
	}

	var buf strings.Builder
	for ev := range stream.Events() {
		if ev.Err != "" {
			return buf.String(), fmt.Errorf("web: LLM scraper: stream error: %s", ev.Err)
		}
		buf.WriteString(ev.Text)
	}
	final, err := stream.Final()
	if err != nil {
		return buf.String(), fmt.Errorf("web: LLM scraper: final: %w", err)
	}
	out := buf.String()
	if out == "" {
		out = final
	}
	return out, nil
}
