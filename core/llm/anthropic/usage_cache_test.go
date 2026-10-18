package anthropic

import (
	"context"
	"net/http"
	"testing"
)

// Anthropic reports the prompt-cache split on message_start's usage and
// may repeat it on message_delta. Either source lands on
// Usage.CachedInputRead / CachedInputWrite; input_tokens stays the
// uncached remainder as reported.
func TestStream_ParsesPromptCacheUsage(t *testing.T) {
	cases := []struct {
		name      string
		start     string
		delta     string
		wantIn    int
		wantRead  int
		wantWrite int
	}{
		{
			name:     "split on message_start only",
			start:    `{"input_tokens":12,"output_tokens":1,"cache_read_input_tokens":9000,"cache_creation_input_tokens":300}`,
			delta:    `{"output_tokens":4}`,
			wantIn:   12,
			wantRead: 9000, wantWrite: 300,
		},
		{
			name:     "message_delta repeats the split",
			start:    `{"input_tokens":12,"output_tokens":1,"cache_read_input_tokens":9000,"cache_creation_input_tokens":300}`,
			delta:    `{"input_tokens":12,"output_tokens":4,"cache_read_input_tokens":9000,"cache_creation_input_tokens":300}`,
			wantIn:   12,
			wantRead: 9000, wantWrite: 300,
		},
		{
			name:   "no cache fields",
			start:  `{"input_tokens":7,"output_tokens":1}`,
			delta:  `{"input_tokens":7,"output_tokens":4}`,
			wantIn: 7,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
				writeSSE(w, []sseFrame{
					{event: "message_start", data: `{"type":"message_start","message":{"id":"m","role":"assistant","model":"claude-sonnet-4-5","usage":` + tc.start + `}}`},
					{event: "content_block_start", data: `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
					{event: "content_block_delta", data: `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`},
					{event: "content_block_stop", data: `{"type":"content_block_stop","index":0}`},
					{event: "message_delta", data: `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":` + tc.delta + `}`},
					{event: "message_stop", data: `{"type":"message_stop"}`},
				})
			})
			req, prof := stdReq()
			stream, err := newAdapter(fs).Stream(context.Background(), req, prof, []byte("k"))
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			_, resp, ferr := drain(t, stream)
			if ferr != nil {
				t.Fatalf("Final: %v", ferr)
			}
			u := resp.Usage
			if u.InputTokens != tc.wantIn || u.OutputTokens != 4 ||
				u.CachedInputRead != tc.wantRead || u.CachedInputWrite != tc.wantWrite {
				t.Fatalf("usage = %+v, want in=%d out=4 read=%d write=%d",
					u, tc.wantIn, tc.wantRead, tc.wantWrite)
			}
		})
	}
}
