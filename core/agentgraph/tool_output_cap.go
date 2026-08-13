package agentgraph

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Tool output is the dominant accumulator in a long agentic turn
// (turn-context-runway-01PMAG03 WP01). A single `bash` or `read_file`
// result can be megabytes; once it lands in a Message it is in the
// context for the rest of the turn and the only way back out is
// compaction — which costs a real LLM call to summarise bytes that
// could have been truncated for free.
//
// ToolResultCap bounds a tool result *at dispatch*, before it becomes a
// Message, with head+tail retention and an explicit elision marker so
// the omission is visible to the model rather than silent. The full
// payload is handed to the optional ToolOutputArchive seam and the
// marker names the handle, so the model can re-read deliberately.
const (
	// DefaultToolResultMaxBytes is the largest single tool result
	// allowed into the context when the Env leaves ToolResultCap at its
	// zero value. 64 KiB is roughly 16k tokens — big enough that
	// ordinary results (file reads, command output, MCP responses) are
	// untouched byte-for-byte, small enough that a runaway result cannot
	// consume a whole context window on its own.
	DefaultToolResultMaxBytes = 64 * 1024

	// DefaultToolResultHeadBytes / DefaultToolResultTailBytes split the
	// retained budget. The split is tail-weighted: for the tools that
	// actually produce huge output (bash, log tails, test runs) the end
	// of the stream — exit status, the failing assertion, the summary
	// line — is what the model needs. A head slice is still retained so
	// the model can tell *what* it was reading.
	//
	// plan.md open question 2 ("per-tool retention ratio") stays open:
	// a per-tool table would plug in behind ToolResultCap.forTool without
	// changing any call site.
	DefaultToolResultHeadBytes = 24 * 1024
	DefaultToolResultTailBytes = 40 * 1024
)

// ToolResultCap is the per-result byte budget applied to tool output
// before it enters the context.
//
// The zero value selects the Default* constants above. A negative
// MaxBytes disables the cap entirely (pre-mission behaviour: unbounded
// tool output).
type ToolResultCap struct {
	// MaxBytes is the largest payload allowed through untouched. Zero
	// selects DefaultToolResultMaxBytes; negative disables the cap.
	MaxBytes int
	// HeadBytes is the retained prefix. Zero selects
	// DefaultToolResultHeadBytes.
	HeadBytes int
	// TailBytes is the retained suffix. Zero selects
	// DefaultToolResultTailBytes.
	TailBytes int
}

// resolved fills the zero fields with the package defaults and reports
// whether the cap is active at all.
func (c ToolResultCap) resolved() (maxBytes, head, tail int, active bool) {
	if c.MaxBytes < 0 {
		return 0, 0, 0, false
	}
	maxBytes = c.MaxBytes
	if maxBytes == 0 {
		maxBytes = DefaultToolResultMaxBytes
	}
	head = c.HeadBytes
	if head == 0 {
		head = DefaultToolResultHeadBytes
	}
	tail = c.TailBytes
	if tail == 0 {
		tail = DefaultToolResultTailBytes
	}
	if head < 0 {
		head = 0
	}
	if tail < 0 {
		tail = 0
	}
	// Retention can never exceed the cap: a head+tail budget larger than
	// MaxBytes would make the "bounded" message bigger than the
	// threshold that triggered truncation. Shrink proportionally,
	// preserving the head/tail ratio.
	if head+tail > maxBytes {
		total := head + tail
		head = maxBytes * head / total
		tail = maxBytes - head
	}
	return maxBytes, head, tail, true
}

// Exceeds reports whether a payload of n bytes is over the cap.
func (c ToolResultCap) Exceeds(n int) bool {
	maxBytes, _, _, active := c.resolved()
	return active && n > maxBytes
}

// Apply truncates content to the cap's head+tail budget and splices in
// an elision marker naming handle (empty handle == the full payload was
// not retained anywhere, which the marker says out loud).
//
// A payload at or under the cap is returned unchanged and elided == 0 —
// this is the FR-004 guarantee for WP01: ordinary results are
// byte-identical to pre-mission.
//
// The head/tail slices are taken on RUNE boundaries, not raw byte
// offsets. The budget is a byte budget (context cost is bytes, and a
// tool can return anything), but cutting a multi-byte sequence in half
// hands the model orphan continuation bytes and an invalid-UTF-8
// message — for CJK, emoji, or any non-ASCII output that is the common
// case, not the edge case. `elided` is recomputed from the ACTUAL
// retained lengths after that backoff so the marker's arithmetic stays
// truthful rather than reporting the pre-backoff intent.
func (c ToolResultCap) Apply(content, handle string) (out string, elided int) {
	maxBytes, head, tail, active := c.resolved()
	if !active || len(content) <= maxBytes {
		return content, 0
	}
	if head > len(content) {
		head = len(content)
	}
	if tail > len(content)-head {
		tail = len(content) - head
	}
	headStr := safeUTF8Prefix(content, head)
	tailStr := safeUTF8Suffix(content, tail)
	elided = len(content) - len(headStr) - len(tailStr)
	if elided <= 0 {
		return content, 0
	}
	var sb strings.Builder
	sb.Grow(len(headStr) + len(tailStr) + 256)
	sb.WriteString(headStr)
	sb.WriteString(toolResultElisionMarker(
		len(content), len(headStr), len(tailStr), elided, handle))
	sb.WriteString(tailStr)
	return sb.String(), elided
}

// safeUTF8Prefix returns the longest prefix of s that fits in n bytes
// without ending mid-rune.
//
// The backoff is bounded to utf8.UTFMax steps rather than walking down
// to the empty string the way core/memory/narrative's safePrefix does.
// That matters here because tool output is not assumed to be text at
// all: a tool that cats a binary file yields a payload where NO prefix
// is valid UTF-8, and an unbounded backoff would degrade to O(n²) over
// a multi-megabyte result while retaining nothing. Bounded backoff
// gives up after at most one rune's worth of bytes and keeps the raw
// cut — correct, because there was no rune boundary to respect.
func safeUTF8Prefix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	for i := 0; i < utf8.UTFMax && len(cut) > 0; i++ {
		if r, size := utf8.DecodeLastRuneInString(cut); r != utf8.RuneError || size > 1 {
			break
		}
		cut = cut[:len(cut)-1]
	}
	return cut
}

// safeUTF8Suffix returns the longest suffix of s that fits in n bytes
// without starting mid-rune. Bounded backoff, for the same reason as
// safeUTF8Prefix.
func safeUTF8Suffix(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[len(s)-n:]
	for i := 0; i < utf8.UTFMax && len(cut) > 0; i++ {
		if r, size := utf8.DecodeRuneInString(cut); r != utf8.RuneError || size > 1 {
			break
		}
		cut = cut[1:]
	}
	return cut
}

// toolResultElisionMarker renders the model-visible notice that bytes
// were dropped. It is deliberately explicit about the arithmetic: the
// model can tell how much it is missing and decide whether re-reading
// is worth a tool call.
func toolResultElisionMarker(total, head, tail, elided int, handle string) string {
	recovery := "The elided bytes were not retained and cannot be recovered."
	if handle != "" {
		recovery = fmt.Sprintf(
			"The full %d-byte output is stored under handle %q — re-read it with read_bash_output (bash_run_id=%q) if you need the elided region.",
			total, handle, handle)
	}
	return fmt.Sprintf(
		"\n\n[... tool output truncated by the harness: %d bytes total, %d retained (first %d + last %d), %d elided. %s ...]\n\n",
		total, head+tail, head, tail, elided, recovery)
}

// ToolOutputRef identifies the tool call whose output is being archived
// so the archive can mint a stable, human-legible handle.
type ToolOutputRef struct {
	RunID  string
	NodeID string
	Tool   string
	CallID string
}

// ToolOutputArchive persists the full payload of a tool result the
// kernel is about to truncate, and returns a handle the elision marker
// can name.
//
// Production wiring binds this to the same store the read_bash_output
// executor reads from, so the handle is genuinely resolvable rather
// than decorative (see core/rpc/views/agentgraph/env_deps_tooloutput.go).
// nil is the honest degraded mode: truncation still happens — bounding
// the context is the point — and the marker says the bytes are gone.
type ToolOutputArchive interface {
	ArchiveToolOutput(ctx context.Context, ref ToolOutputRef, content string) (handle string, err error)
}

// ToolOutputArchiveFunc adapts a function to ToolOutputArchive.
type ToolOutputArchiveFunc func(ctx context.Context, ref ToolOutputRef, content string) (string, error)

// ArchiveToolOutput satisfies ToolOutputArchive.
func (f ToolOutputArchiveFunc) ArchiveToolOutput(ctx context.Context, ref ToolOutputRef, content string) (string, error) {
	return f(ctx, ref, content)
}

// capToolOutput is the one place the cap + archive are composed. It
// archives first (so the marker can name a handle) and truncates second.
// An archive failure is non-fatal: losing the full payload is strictly
// better than letting a 1MB result into the context, so we degrade to
// the no-handle marker and report the error to the caller for event
// annotation.
func capToolOutput(ctx context.Context, env *Env, ref ToolOutputRef, content string) (out string, elided int, handle string, archiveErr error) {
	if env == nil {
		return content, 0, "", nil
	}
	if !env.ToolResultCap.Exceeds(len(content)) {
		return content, 0, "", nil
	}
	if env.ToolOutputArchive != nil {
		h, err := env.ToolOutputArchive.ArchiveToolOutput(ctx, ref, content)
		if err != nil {
			archiveErr = err
		} else {
			handle = h
		}
	}
	out, elided = env.ToolResultCap.Apply(content, handle)
	return out, elided, handle, archiveErr
}
