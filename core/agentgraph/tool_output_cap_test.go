package agentgraph

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// fakeToolOutputArchive is a race-safe in-memory stand-in for the
// production bash-store-backed archive. Writes arrive from the
// tool_dispatch fan-out goroutines; the test body reads through
// snapshot() (CLAUDE.md race-safe fake pattern).
type fakeToolOutputArchive struct {
	mu      sync.Mutex
	stored  map[string]string
	failing bool
}

func newFakeToolOutputArchive() *fakeToolOutputArchive {
	return &fakeToolOutputArchive{stored: map[string]string{}}
}

func (f *fakeToolOutputArchive) fail() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failing = true
}

func (f *fakeToolOutputArchive) ArchiveToolOutput(_ context.Context, ref ToolOutputRef, content string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failing {
		return "", errors.New("archive unavailable")
	}
	handle := "archived-" + ref.Tool + "-" + ref.CallID
	f.stored[handle] = content
	return handle, nil
}

func (f *fakeToolOutputArchive) snapshot() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]string, len(f.stored))
	for k, v := range f.stored {
		out[k] = v
	}
	return out
}

// dispatchOnce runs the tool_dispatch executor over a single call whose
// tool returns payload, and returns the resulting tool message content.
func dispatchOnce(t *testing.T, env *Env, payload string) (Result, string) {
	t.Helper()
	tools := newStubTools()
	tools.allow("bigtool", payload, false)
	env.Tools = tools
	if env.Counters == nil {
		env.Counters = &RunCounters{}
	}
	if env.State == nil {
		env.State = NewRunState()
	}
	applyEnvDefaults(env)

	node := &Node{
		ID:    "td",
		Kind:  NodeKindToolDispatch,
		Attrs: ToolDispatchAttrs{MaxConcurrent: 1},
	}
	res, err := toolDispatchExecutor{}.Execute(context.Background(), env, node,
		PortValues{"tool_calls": []ToolCallRequest{
			{ID: "call-1", Name: "bigtool", Arguments: `{}`},
		}})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	msgs, ok := res.Outputs["tool_messages"].([]Message)
	if !ok {
		t.Fatalf("tool_messages: got %T", res.Outputs["tool_messages"])
	}
	if len(msgs) != 1 {
		t.Fatalf("len(tool_messages) = %d, want 1", len(msgs))
	}
	return res, msgs[0].Content
}

// TestToolDispatch_LargeResultIsBoundedAndArchived is the WP01
// acceptance case (turn-context-runway-01PMAG03): a 1MB tool result
// must not enter the context whole. It yields a bounded message
// carrying an explicit elision marker naming a handle, and the full
// payload is resolvable through the archive seam.
func TestToolDispatch_LargeResultIsBoundedAndArchived(t *testing.T) {
	t.Parallel()
	const payloadSize = 1 << 20
	payload := strings.Repeat("A", payloadSize/2) + strings.Repeat("Z", payloadSize/2)

	archive := newFakeToolOutputArchive()
	env := &Env{RunID: "r-cap", ToolOutputArchive: archive}
	res, got := dispatchOnce(t, env, payload)

	if len(got) >= payloadSize {
		t.Fatalf("tool result entered the context unbounded: %d bytes (payload %d)", len(got), payloadSize)
	}
	if len(got) > DefaultToolResultMaxBytes+1024 {
		t.Fatalf("bounded message is %d bytes, want <= cap(%d) + marker",
			len(got), DefaultToolResultMaxBytes)
	}
	if !strings.Contains(got, "tool output truncated by the harness") {
		t.Fatalf("bounded message carries no elision marker")
	}
	if !strings.HasPrefix(got, "AAAA") {
		t.Fatalf("head slice not retained")
	}
	if !strings.HasSuffix(got, "ZZZZ") {
		t.Fatalf("tail slice not retained")
	}

	handle := "archived-bigtool-call-1"
	stored := archive.snapshot()
	full, ok := stored[handle]
	if !ok {
		t.Fatalf("full payload was not archived under %q", handle)
	}
	if len(full) != payloadSize {
		t.Fatalf("archived payload = %d bytes, want %d", len(full), payloadSize)
	}
	if !strings.Contains(got, handle) {
		t.Fatalf("elision marker does not name the handle %q", handle)
	}

	// The tool_result event must disclose the truncation so the run
	// trace shows what the model actually saw.
	var sawTruncated bool
	for _, e := range res.Events.Events {
		if e.Kind != EventToolResult {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			t.Fatalf("decode tool_result payload: %v", err)
		}
		if v, ok := payload["truncated"].(bool); !ok || !v {
			continue
		}
		sawTruncated = true
		if got, want := payload["bytes_original"], float64(payloadSize); got != want {
			t.Fatalf("event bytes_original = %v, want %v", got, want)
		}
		if got := payload["output_handle"]; got != handle {
			t.Fatalf("event output_handle = %v, want %q", got, handle)
		}
	}
	if !sawTruncated {
		t.Fatalf("no tool_result event reported truncated=true")
	}
}

// TestToolDispatch_SmallResultUntouched is the FR-004 half of WP01: a
// result under the cap reaches the model byte-for-byte, with no marker
// and nothing written to the archive.
func TestToolDispatch_SmallResultUntouched(t *testing.T) {
	t.Parallel()
	payload := strings.Repeat("small output line\n", 64)

	archive := newFakeToolOutputArchive()
	env := &Env{RunID: "r-cap-small", ToolOutputArchive: archive}
	_, got := dispatchOnce(t, env, payload)

	if got != payload {
		t.Fatalf("small result was mutated:\n got %d bytes\nwant %d bytes", len(got), len(payload))
	}
	if n := len(archive.snapshot()); n != 0 {
		t.Fatalf("small result was archived (%d entries); the cap must be a pure no-op under the threshold", n)
	}
}

// TestToolDispatch_ArchiveFailureStillBounds proves the degraded mode:
// an archive error must not let the unbounded payload through. The
// truncation still happens and the marker says the bytes are gone.
func TestToolDispatch_ArchiveFailureStillBounds(t *testing.T) {
	t.Parallel()
	payload := strings.Repeat("q", 1<<20)
	archive := newFakeToolOutputArchive()
	archive.fail()

	env := &Env{RunID: "r-cap-failarchive", ToolOutputArchive: archive}
	_, got := dispatchOnce(t, env, payload)

	if len(got) >= len(payload) {
		t.Fatalf("archive failure let the unbounded payload through: %d bytes", len(got))
	}
	if !strings.Contains(got, "cannot be recovered") {
		t.Fatalf("marker does not disclose the lost bytes")
	}
}

// TestToolDispatch_CapDisabledPassesThrough pins the opt-out: a
// negative MaxBytes restores pre-mission unbounded behaviour.
func TestToolDispatch_CapDisabledPassesThrough(t *testing.T) {
	t.Parallel()
	payload := strings.Repeat("u", 1<<20)
	env := &Env{RunID: "r-cap-off", ToolResultCap: ToolResultCap{MaxBytes: -1}}
	_, got := dispatchOnce(t, env, payload)
	if got != payload {
		t.Fatalf("disabled cap truncated the payload: %d bytes, want %d", len(got), len(payload))
	}
}

// TestToolResultCap_Apply_IsRuneSafe is the regression for the
// byte-offset slicing bug: the cap's budget is a byte budget, but
// cutting head/tail on raw byte offsets splits multi-byte sequences and
// hands the model orphan continuation bytes. For CJK, emoji, or any
// non-ASCII tool output that is the common case, not the edge case.
func TestToolResultCap_Apply_IsRuneSafe(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		content string
	}{
		// Every fixture must comfortably exceed DefaultToolResultMaxBytes
		// (64 KiB) so the cap actually engages; 256 KiB of payload does.
		//
		// 3-byte runes. The 24576-byte head budget divides evenly by 3,
		// so the aligned case would cut cleanly by luck — the 1-byte
		// lead-in in "cjk" is what forces a mid-rune boundary and is the
		// case that failed before the fix.
		{"cjk", "x" + strings.Repeat("世界", 256*1024/6)},
		{"cjk-aligned", strings.Repeat("世界", 256*1024/6)},
		// 4-byte runes.
		{"emoji", strings.Repeat("🙂", 256*1024/4)},
		{"emoji-offset", "ab" + strings.Repeat("🙂", 256*1024/4)},
		// 2-byte runes.
		{"latin1", strings.Repeat("é", 256*1024/2)},
		{"mixed", strings.Repeat("ascii 世界 🙂 é ", 16384)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if !utf8.ValidString(tc.content) {
				t.Fatalf("fixture is not valid UTF-8")
			}
			out, elided := ToolResultCap{}.Apply(tc.content, "handle-1")
			if elided == 0 {
				t.Fatalf("fixture (%d bytes) was not truncated", len(tc.content))
			}
			if !utf8.ValidString(out) {
				t.Fatalf("truncated output is not valid UTF-8 — the cap cut mid-rune")
			}
			if strings.ContainsRune(out, utf8.RuneError) {
				t.Fatalf("truncated output contains U+FFFD; a rune was severed")
			}
			// The marker arithmetic must describe what actually
			// happened, not the pre-backoff intent.
			marker := toolResultElisionMarker(
				len(tc.content),
				len(safeUTF8Prefix(tc.content, DefaultToolResultHeadBytes)),
				len(safeUTF8Suffix(tc.content, DefaultToolResultTailBytes)),
				elided, "handle-1")
			if !strings.Contains(out, marker) {
				t.Fatalf("marker does not match the actual retained lengths")
			}
			retained := len(out) - len(marker)
			if retained+elided != len(tc.content) {
				t.Fatalf("arithmetic is untruthful: retained %d + elided %d != total %d",
					retained, elided, len(tc.content))
			}
		})
	}
}

// TestToolResultCap_Apply_BinaryPayloadTerminates pins the bounded
// backoff: a payload where NO prefix is valid UTF-8 (a tool that cats a
// binary file) must still be capped promptly rather than degrading to a
// quadratic walk that retains nothing.
func TestToolResultCap_Apply_BinaryPayloadTerminates(t *testing.T) {
	t.Parallel()
	// 0xFF is never valid in any UTF-8 sequence.
	binary := strings.Repeat("\xff", 1<<20)
	out, elided := ToolResultCap{}.Apply(binary, "h")
	if elided == 0 {
		t.Fatalf("binary payload was not truncated")
	}
	if len(out) > DefaultToolResultMaxBytes+1024 {
		t.Fatalf("binary payload bounded to %d bytes, want <= cap + marker", len(out))
	}
	// Bounded backoff gives up after at most one rune's worth of bytes,
	// so we keep essentially the whole budget rather than nothing.
	if retained := len(binary) - elided; retained < DefaultToolResultMaxBytes-utf8.UTFMax*2 {
		t.Fatalf("retained only %d bytes of a %d-byte budget; the backoff is unbounded",
			retained, DefaultToolResultMaxBytes)
	}
}

// TestToolDispatch_MultibyteResultStaysValid is the same guarantee at
// the dispatch seam the model actually reads from.
func TestToolDispatch_MultibyteResultStaysValid(t *testing.T) {
	t.Parallel()
	payload := "x" + strings.Repeat("世界", (1<<20)/6)
	env := &Env{RunID: "r-cap-utf8", ToolOutputArchive: newFakeToolOutputArchive()}
	_, got := dispatchOnce(t, env, payload)

	if len(got) >= len(payload) {
		t.Fatalf("multibyte result entered the context unbounded")
	}
	if !utf8.ValidString(got) {
		t.Fatalf("the tool message the model sees is not valid UTF-8")
	}
	if strings.ContainsRune(got, utf8.RuneError) {
		t.Fatalf("the tool message contains U+FFFD; a rune was severed at dispatch")
	}
}

// TestToolResultCap_Apply_Arithmetic pins the pure policy value: the
// retained slice never exceeds MaxBytes and the reported elision is
// exact.
func TestToolResultCap_Apply_Arithmetic(t *testing.T) {
	t.Parallel()
	c := ToolResultCap{MaxBytes: 100, HeadBytes: 30, TailBytes: 40}
	content := strings.Repeat("x", 500)

	out, elided := c.Apply(content, "h1")
	if want := 500 - 70; elided != want {
		t.Fatalf("elided = %d, want %d", elided, want)
	}
	if !strings.HasPrefix(out, strings.Repeat("x", 30)) {
		t.Fatalf("head not retained")
	}
	if !strings.Contains(out, `"h1"`) {
		t.Fatalf("handle not named in marker: %q", out)
	}

	small := strings.Repeat("y", 100)
	if out, elided = c.Apply(small, "h1"); elided != 0 || out != small {
		t.Fatalf("at-cap payload was mutated (elided=%d)", elided)
	}

	off := ToolResultCap{MaxBytes: -1}
	if out, elided = off.Apply(content, "h1"); elided != 0 || out != content {
		t.Fatalf("disabled cap mutated the payload")
	}

	// Head+tail wider than MaxBytes shrinks to fit rather than producing
	// a "bounded" message larger than the threshold that triggered it.
	wide := ToolResultCap{MaxBytes: 50, HeadBytes: 100, TailBytes: 100}
	_, elided = wide.Apply(content, "")
	if elided == 0 {
		t.Fatalf("wide retention did not truncate")
	}
	if retained := len(content) - elided; retained > 50 {
		t.Fatalf("retained %d bytes, want <= MaxBytes(50)", retained)
	}
}
