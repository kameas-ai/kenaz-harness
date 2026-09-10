package bedrock

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/kameas-ai/kenaz-harness/core/llm"
)

// fakeConverseReader implements bedrockruntime.ConverseStreamOutputReader
// (Events/Close/Err) well enough to drive converseStream.pump() with
// fabricated SDK-shaped events. No HTTP transport, no SigV4 signing —
// this exercises the SDK-auth path's own decode logic the same way
// TestBearer_ReasoningContentEmitsStreamReasoning exercises the
// bearer/REST path's decode logic, just via the SDK's typed union
// instead of raw eventstream bytes (which the SDK path's own signing
// requirement makes impractical to fake over httptest — see the
// pre-existing note on TestToBedrockContentBlocks_ImageDocument).
type fakeConverseReader struct {
	events chan types.ConverseStreamOutput
}

func (f *fakeConverseReader) Events() <-chan types.ConverseStreamOutput { return f.events }
func (f *fakeConverseReader) Close() error                              { return nil }
func (f *fakeConverseReader) Err() error                                { return nil }

// fakeConverseStreamOutput implements converseStreamSource by wrapping a
// *bedrockruntime.ConverseStreamEventStream the same way the real
// *bedrockruntime.ConverseStreamOutput.GetStream() does — it just
// returns a pre-built one instead of the one the SDK's HTTP layer would
// have deserialized.
type fakeConverseStreamOutput struct {
	es *bedrockruntime.ConverseStreamEventStream
}

func (f *fakeConverseStreamOutput) GetStream() *bedrockruntime.ConverseStreamEventStream {
	return f.es
}

// newFakeConverseStreamSource builds a converseStreamSource around the
// SDK's own bedrockruntime.NewConverseStreamEventStream constructor,
// which that type's doc comment says exists specifically "for testing
// and mocking the event stream" via a caller-supplied Reader. Frames are
// buffered and the channel closed immediately — pump() only ranges over
// it once, so there is no ordering hazard.
func newFakeConverseStreamSource(frames ...types.ConverseStreamOutput) converseStreamSource {
	reader := &fakeConverseReader{events: make(chan types.ConverseStreamOutput, len(frames))}
	for _, f := range frames {
		reader.events <- f
	}
	close(reader.events)
	es := bedrockruntime.NewConverseStreamEventStream(func(s *bedrockruntime.ConverseStreamEventStream) {
		s.Reader = reader
	})
	return &fakeConverseStreamOutput{es: es}
}

// runConverseStream drives pump() to completion against the given fake
// source and returns every StreamEvent emitted plus the terminal Final().
func runConverseStream(t *testing.T, src converseStreamSource) ([]llm.StreamEvent, llm.Response, error) {
	t.Helper()
	s := &converseStream{
		ctx:    context.Background(),
		out:    src,
		events: make(chan llm.StreamEvent, 64),
		done:   make(chan struct{}),
	}
	go s.pump()
	var got []llm.StreamEvent
	for ev := range s.Events() {
		got = append(got, ev)
	}
	resp, err := s.Final()
	return got, resp, err
}

// TestConverseStream_ReasoningContentEmitsStreamReasoning drives the
// SDK-auth path's pump() — the aws_profile route — with a
// ContentBlockDeltaMemberReasoningContent frame and asserts it emits
// llm.StreamReasoning with the reasoning text
// (model-settings-reach-the-model-01PMZ101 WP09, review follow-up: this
// arm had zero coverage anywhere in the package before this test — the
// existing bearer-path test only exercises bearer.go's independent JSON
// decode of the same wire concept, not pump()'s SDK-typed decode).
func TestConverseStream_ReasoningContentEmitsStreamReasoning(t *testing.T) {
	idx0 := int32(0)
	idx1 := int32(1)
	src := newFakeConverseStreamSource(
		&types.ConverseStreamOutputMemberContentBlockDelta{
			Value: types.ContentBlockDeltaEvent{
				ContentBlockIndex: &idx0,
				Delta: &types.ContentBlockDeltaMemberReasoningContent{
					Value: &types.ReasoningContentBlockDeltaMemberText{Value: "Let me "},
				},
			},
		},
		&types.ConverseStreamOutputMemberContentBlockDelta{
			Value: types.ContentBlockDeltaEvent{
				ContentBlockIndex: &idx0,
				Delta: &types.ContentBlockDeltaMemberReasoningContent{
					Value: &types.ReasoningContentBlockDeltaMemberText{Value: "think about this."},
				},
			},
		},
		// A signature delta carries no renderable text and must not
		// produce a StreamReasoning event with empty content.
		&types.ConverseStreamOutputMemberContentBlockDelta{
			Value: types.ContentBlockDeltaEvent{
				ContentBlockIndex: &idx0,
				Delta: &types.ContentBlockDeltaMemberReasoningContent{
					Value: &types.ReasoningContentBlockDeltaMemberSignature{Value: "sig-abc"},
				},
			},
		},
		&types.ConverseStreamOutputMemberContentBlockDelta{
			Value: types.ContentBlockDeltaEvent{
				ContentBlockIndex: &idx1,
				Delta:             &types.ContentBlockDeltaMemberText{Value: "42"},
			},
		},
		&types.ConverseStreamOutputMemberMessageStop{
			Value: types.MessageStopEvent{StopReason: types.StopReasonEndTurn},
		},
	)

	events, resp, err := runConverseStream(t, src)
	if err != nil {
		t.Fatalf("Final: %v", err)
	}
	if resp.FinishReason != string(types.StopReasonEndTurn) {
		t.Fatalf("FinishReason = %q", resp.FinishReason)
	}

	var reasoning, texts []string
	for _, ev := range events {
		switch ev.Kind {
		case llm.StreamReasoning:
			if ev.Reasoning == nil {
				t.Fatalf("StreamReasoning event with nil Reasoning payload")
			}
			if ev.Reasoning.Content == "" {
				t.Fatalf("StreamReasoning event with empty content (signature delta leaked through)")
			}
			reasoning = append(reasoning, ev.Reasoning.Content)
		case llm.StreamText:
			texts = append(texts, ev.Text)
		}
	}

	gotReasoning := strings.Join(reasoning, "")
	if gotReasoning != "Let me think about this." {
		t.Fatalf("reasoning content = %q, want %q", gotReasoning, "Let me think about this.")
	}
	gotText := strings.Join(texts, "")
	if gotText != "42" {
		t.Fatalf("text content = %q, want %q (reasoning must not leak into the text stream)", gotText, "42")
	}
}

// TestConverseStream_NoReasoning_NoReasoningEvent is the SDK-path
// no-reasoning control: an ordinary response with no
// ContentBlockDeltaMemberReasoningContent frame must not produce any
// StreamReasoning event.
func TestConverseStream_NoReasoning_NoReasoningEvent(t *testing.T) {
	idx0 := int32(0)
	src := newFakeConverseStreamSource(
		&types.ConverseStreamOutputMemberContentBlockDelta{
			Value: types.ContentBlockDeltaEvent{
				ContentBlockIndex: &idx0,
				Delta:             &types.ContentBlockDeltaMemberText{Value: "hello"},
			},
		},
		&types.ConverseStreamOutputMemberMessageStop{
			Value: types.MessageStopEvent{StopReason: types.StopReasonEndTurn},
		},
	)

	events, _, err := runConverseStream(t, src)
	if err != nil {
		t.Fatalf("Final: %v", err)
	}
	for _, ev := range events {
		if ev.Kind == llm.StreamReasoning {
			t.Fatalf("unexpected StreamReasoning event for a no-reasoning response: %+v", ev)
		}
	}
}
