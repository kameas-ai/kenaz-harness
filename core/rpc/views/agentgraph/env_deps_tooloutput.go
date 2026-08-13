package agentgraph

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync/atomic"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	corebash "github.com/kameas-ai/kenaz-harness/core/tools/bash"
)

// ToolOutputArchiveAdapter satisfies agentgraph.ToolOutputArchive by
// writing the full payload of a truncated tool result into the same
// process-local store the read_bash_output executor reads from
// (turn-context-runway-01PMAG03 WP01).
//
// Reusing the bash store rather than standing up a second cache is
// deliberate: read_bash_output already exists as a node kind, already
// resolves through EnvDeps.BashOutput, and already has the TTL /
// eviction discipline this needs. That makes the handle named in the
// elision marker genuinely resolvable end-to-end instead of decorative.
//
// DIRECTIVE_001 holds: core/tools/bash does not import this package;
// the chassis constructs the adapter and hands it to the kernel.
type ToolOutputArchiveAdapter struct {
	store *corebash.Store
	seq   atomic.Uint64
}

// NewToolOutputArchiveAdapter constructs the adapter. A nil store makes
// every archive attempt fail, which the kernel degrades to a
// no-handle elision marker.
func NewToolOutputArchiveAdapter(store *corebash.Store) *ToolOutputArchiveAdapter {
	return &ToolOutputArchiveAdapter{store: store}
}

// ArchiveToolOutput satisfies agentgraph.ToolOutputArchive.
func (a *ToolOutputArchiveAdapter) ArchiveToolOutput(_ context.Context, ref coreag.ToolOutputRef, content string) (string, error) {
	if a == nil || a.store == nil {
		return "", fmt.Errorf("tool output archive: no store wired")
	}
	handle := a.mintHandle(ref, content)
	a.store.Put(handle, corebash.Record{
		Command:  "tool:" + ref.Tool,
		Stdout:   content,
		ExitCode: 0,
	})
	return handle, nil
}

// mintHandle builds a stable, human-legible id. The call id is used
// when the provider supplied one (it is unique per turn); otherwise we
// fall back to a content digest plus a monotonic counter so two
// truncations of the same tool in one run cannot collide.
func (a *ToolOutputArchiveAdapter) mintHandle(ref coreag.ToolOutputRef, content string) string {
	tool := sanitizeHandleSegment(ref.Tool)
	if tool == "" {
		tool = "tool"
	}
	if id := sanitizeHandleSegment(ref.CallID); id != "" {
		return "tool-output-" + tool + "-" + id
	}
	sum := sha256.Sum256([]byte(content))
	return fmt.Sprintf("tool-output-%s-%s-%d", tool, hex.EncodeToString(sum[:4]), a.seq.Add(1))
}

// sanitizeHandleSegment keeps handles safe to paste back into a tool
// argument: alphanumerics, dash and underscore survive; everything else
// becomes a dash.
func sanitizeHandleSegment(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var sb strings.Builder
	sb.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			sb.WriteRune(r)
		default:
			sb.WriteByte('-')
		}
	}
	return sb.String()
}

// Compile-time witness.
var _ coreag.ToolOutputArchive = (*ToolOutputArchiveAdapter)(nil)
