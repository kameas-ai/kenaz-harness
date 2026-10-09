package openrouter

import (
	"context"
	"net/http"
	"testing"
)

// The usage frame's prompt_tokens_details carries the prompt-cache split.
// cached_tokens / cache_write_tokens land on Usage.CachedInputRead /
// CachedInputWrite; prompt_tokens stays the inclusive total as reported.
func TestStream_ParsesPromptCacheUsage(t *testing.T) {
	cases := []struct {
		name               string
		usage              string
		wantIn, wantRead   int
		wantWrite, wantOut int
	}{
		{
			name:     "cache read and write",
			usage:    `{"prompt_tokens":224798,"completion_tokens":9,"total_tokens":224807,"prompt_tokens_details":{"cached_tokens":200000,"cache_write_tokens":24000}}`,
			wantIn:   224798,
			wantRead: 200000, wantWrite: 24000, wantOut: 9,
		},
		{
			name:   "no details object",
			usage:  `{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}`,
			wantIn: 5, wantOut: 2,
		},
		{
			name:   "zero cache",
			usage:  `{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7,"prompt_tokens_details":{"cached_tokens":0,"cache_write_tokens":0}}`,
			wantIn: 5, wantOut: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := newFakeServer(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
				writeSSEFrames(w, []string{
					`data: {"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
					``,
					`data: {"choices":[],"usage":` + tc.usage + `}`,
					``,
					`data: [DONE]`,
					``,
				})
			})
			req, prof := stdReq()
			stream, err := newAdapter(fs).Stream(context.Background(), req, prof, []byte("sk-or-test"))
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			_, resp, ferr := drain(t, stream)
			if ferr != nil {
				t.Fatalf("Final: %v", ferr)
			}
			u := resp.Usage
			if u.InputTokens != tc.wantIn || u.OutputTokens != tc.wantOut ||
				u.CachedInputRead != tc.wantRead || u.CachedInputWrite != tc.wantWrite {
				t.Fatalf("usage = %+v, want in=%d out=%d read=%d write=%d",
					u, tc.wantIn, tc.wantOut, tc.wantRead, tc.wantWrite)
			}
		})
	}
}
