package autotitle

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeLLM is a test double for LLMCaller.
type fakeLLM struct {
	// text is the raw text the fake returns.
	text string
	// err is the error the fake returns (if non-nil, text is ignored).
	err error
	// calls counts how many times Call was invoked.
	calls int
	// capturedSystem records the system prompt passed to the last Call.
	capturedSystem string
	// capturedUser records the user prompt passed to the last Call.
	capturedUser string
}

func (f *fakeLLM) Call(_ context.Context, sys, user string) (string, int, int, error) {
	f.calls++
	f.capturedSystem = sys
	f.capturedUser = user
	if f.err != nil {
		return "", 0, 0, f.err
	}
	return f.text, len(user) / 4, len(f.text) / 4, nil
}

// blockingLLM blocks until the context is cancelled, to test timeout
// behaviour.
type blockingLLM struct{}

func (b *blockingLLM) Call(ctx context.Context, _, _ string) (string, int, int, error) {
	<-ctx.Done()
	return "", 0, 0, ctx.Err()
}

func TestGenerator_GenerateTitle(t *testing.T) {
	tests := []struct {
		name       string
		llmText    string
		llmErr     error
		transcript Transcript
		want       string
		wantErr    error
	}{
		{
			name:    "happy path returns sanitized title",
			llmText: "Learning Rust",
			transcript: Transcript{
				{Role: "user", Text: "What's a good way to learn Rust?"},
				{Role: "assistant", Text: "Start with the Rust Book then small projects."},
			},
			want: "Learning Rust",
		},
		{
			name:    "model output with outer quotes is sanitized",
			llmText: `"Learning Rust"`,
			transcript: Transcript{
				{Role: "user", Text: "What's a good way to learn Rust?"},
				{Role: "assistant", Text: "Start with the Rust Book."},
			},
			want: "Learning Rust",
		},
		{
			name:    "oversize model output truncated to 49 runes + ellipsis",
			llmText: strings.Repeat("a", 60),
			transcript: Transcript{
				{Role: "user", Text: "Tell me about algorithms"},
				{Role: "assistant", Text: "Sure, algorithms are fundamental to computer science."},
			},
			want: strings.Repeat("a", 49) + "…",
		},
		{
			name:    "model refusal returns ErrModelRefused",
			llmText: "Sorry, I can't generate a title for this conversation.",
			transcript: Transcript{
				{Role: "user", Text: "Tell me something"},
				{Role: "assistant", Text: "Here you go."},
			},
			wantErr: ErrModelRefused,
		},
		{
			name:    "model returns too-short output → ErrTitleTooShort",
			llmText: "ok",
			transcript: Transcript{
				{Role: "user", Text: "Hi"},
				{Role: "assistant", Text: "Hello."},
			},
			wantErr: ErrTitleTooShort,
		},
		{
			name:    "model returns empty string → ErrTitleTooShort",
			llmText: "",
			transcript: Transcript{
				{Role: "user", Text: "Hi"},
				{Role: "assistant", Text: "Hello."},
			},
			wantErr: ErrTitleTooShort,
		},
		{
			name:   "llm error is propagated",
			llmErr: errors.New("provider unavailable"),
			transcript: Transcript{
				{Role: "user", Text: "Hi"},
				{Role: "assistant", Text: "Hello."},
			},
			wantErr: errors.New("autotitle: llm call failed: provider unavailable"),
		},
		{
			name: "only user message (no assistant reply yet) still works",
			transcript: Transcript{
				{Role: "user", Text: "How do I learn Rust?"},
			},
			llmText: "Learning Rust",
			want:    "Learning Rust",
		},
		{
			name:       "empty transcript → ErrTitleTooShort (no prompt built)",
			transcript: Transcript{},
			wantErr:    ErrTitleTooShort,
		},
		{
			name: "title with Title: prefix is stripped",
			transcript: Transcript{
				{Role: "user", Text: "Tell me about Go"},
				{Role: "assistant", Text: "Go is a great language."},
			},
			llmText: "Title: Introduction to Go",
			want:    "Introduction to Go",
		},
		{
			name: "multiline model output uses first non-empty line",
			transcript: Transcript{
				{Role: "user", Text: "Explain channels"},
				{Role: "assistant", Text: "Channels enable safe concurrency in Go."},
			},
			llmText: "Go Channels\nMore detail here",
			want:    "Go Channels",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeLLM{text: tc.llmText, err: tc.llmErr}
			g := New(fake)

			got, err := g.GenerateTitle(context.Background(), tc.transcript)

			if tc.wantErr != nil {
				if err == nil {
					t.Fatalf("want error %v, got nil (title=%q)", tc.wantErr, got)
				}
				// For wrapped errors, check via errors.Is or string contains.
				if !errors.Is(err, tc.wantErr) && !strings.Contains(err.Error(), tc.wantErr.Error()) {
					t.Errorf("error = %v, want error containing %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("GenerateTitle() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestGenerator_SystemPromptLocked verifies the system prompt is exactly
// the locked text from plan §2.1.
func TestGenerator_SystemPromptLocked(t *testing.T) {
	const want = "Produce a concise (≤ 50 chars) title summarizing this conversation. Output ONLY the title, no quotes, no explanation."
	if systemPrompt != want {
		t.Errorf("systemPrompt = %q, want %q", systemPrompt, want)
	}
}

// TestGenerator_ContextTimeout verifies that a cancelled/timed-out
// context cancels the in-flight LLM call (NFR-001 / acceptance criterion 5).
func TestGenerator_ContextTimeout(t *testing.T) {
	g := New(&blockingLLM{})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	transcript := Transcript{
		{Role: "user", Text: "What is Go?"},
		{Role: "assistant", Text: "Go is a programming language."},
	}

	_, err := g.GenerateTitle(ctx, transcript)
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "context") {
		t.Errorf("expected context-related error, got: %v", err)
	}
}

// TestGenerator_UserPromptBuilding verifies that GenerateTitle builds the
// user prompt from the most recent user-assistant pair (plan §2.1).
func TestGenerator_UserPromptBuilding(t *testing.T) {
	fake := &fakeLLM{text: "A Good Title"}
	g := New(fake)

	transcript := Transcript{
		{Role: "user", Text: "first turn"},
		{Role: "assistant", Text: "first reply"},
		{Role: "user", Text: "second turn"},
		{Role: "assistant", Text: "second reply"},
	}

	_, err := g.GenerateTitle(context.Background(), transcript)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The user prompt should include the most recent pair.
	if !strings.Contains(fake.capturedUser, "second turn") {
		t.Errorf("user prompt missing 'second turn'; got: %s", fake.capturedUser)
	}
	if !strings.Contains(fake.capturedUser, "second reply") {
		t.Errorf("user prompt missing 'second reply'; got: %s", fake.capturedUser)
	}
}

// TestGenerator_UserPromptTruncation verifies that oversized transcripts
// are truncated to stay within the 6 KB budget (NFR-002).
func TestGenerator_UserPromptTruncation(t *testing.T) {
	fake := &fakeLLM{text: "A Good Title"}
	g := New(fake)

	// Build a transcript with a huge user message.
	bigText := strings.Repeat("x", maxUserPromptBytes*3)
	transcript := Transcript{
		{Role: "user", Text: bigText},
		{Role: "assistant", Text: "Ok."},
	}

	_, err := g.GenerateTitle(context.Background(), transcript)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(fake.capturedUser) > maxUserPromptBytes {
		t.Errorf("user prompt len = %d, want <= %d", len(fake.capturedUser), maxUserPromptBytes)
	}
}

// TestBuildUserPrompt covers the prompt-rendering logic directly.
func TestBuildUserPrompt(t *testing.T) {
	tests := []struct {
		name       string
		transcript Transcript
		wantEmpty  bool
		wantContains []string
	}{
		{
			name:      "empty transcript yields empty prompt",
			transcript: Transcript{},
			wantEmpty: true,
		},
		{
			name: "user + assistant pair rendered correctly",
			transcript: Transcript{
				{Role: "user", Text: "Hello"},
				{Role: "assistant", Text: "Hi there"},
			},
			wantContains: []string{"User: Hello", "Assistant: Hi there"},
		},
		{
			name: "only user message renders without assistant line",
			transcript: Transcript{
				{Role: "user", Text: "Hello"},
			},
			wantContains: []string{"User: Hello"},
		},
		{
			name: "most recent pair selected from long transcript",
			transcript: Transcript{
				{Role: "user", Text: "first"},
				{Role: "assistant", Text: "reply1"},
				{Role: "user", Text: "second"},
				{Role: "assistant", Text: "reply2"},
			},
			wantContains: []string{"User: second", "Assistant: reply2"},
		},
		{
			name: "assistant-only transcript yields empty prompt",
			transcript: Transcript{
				{Role: "assistant", Text: "I am an assistant"},
			},
			wantEmpty: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := buildUserPrompt(tc.transcript)
			if tc.wantEmpty {
				if got != "" {
					t.Errorf("want empty, got %q", got)
				}
				return
			}
			for _, want := range tc.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("prompt missing %q; got: %s", want, got)
				}
			}
		})
	}
}
