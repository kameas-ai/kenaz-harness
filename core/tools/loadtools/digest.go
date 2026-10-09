package loadtools

import (
	"fmt"
	"sort"
	"strings"

	"github.com/kameas-ai/kenaz-harness/core/toolexposure"
)

// maxExamples bounds the example tool names a digest line carries.
const maxExamples = 5

// maxPurposeRunes bounds a digest line's purpose text.
const maxPurposeRunes = 140

// baseDescription opens kenaz__load_tools' description on every call.
const baseDescription = "Load the full definitions of tools you do not yet see. " +
	"A loaded tool's definition is sent on your next calls; call it then."

// inputLine closes the description: the call shape, in one line.
const inputLine = `Input: {"servers": [...], "tools": ["server__tool" or "server__prefix*"], "sticky": bool}`

// DigestServer is one server line of the capability digest.
type DigestServer struct {
	Name    string
	Purpose string
	// Count is the number of the server's tools that are available but
	// not loaded. Zero for a stopped server: its tool list is unknown.
	Count int
	// Examples are up to five bare tool names, alphabetical.
	Examples []string
	// Stopped marks a server that is installed but not running; its tools
	// cannot be loaded until it runs.
	Stopped bool
}

// BuildDigest groups the partition's digest and stopped segments by
// server, attaching each server's purpose from purposes (keyed by server
// name; a missing entry renders no purpose). Off tools are already
// absent from the partition, so they never reach the digest (FR-E2).
// A server with both summary tools and a stopped probe cannot occur: a
// running server has no probe.
func BuildDigest(p toolexposure.Partition, purposes map[string]string) []DigestServer {
	by := map[string]*DigestServer{}
	get := func(server string) *DigestServer {
		d, ok := by[server]
		if !ok {
			d = &DigestServer{Name: server, Purpose: purposes[server]}
			by[server] = d
		}
		return d
	}
	for _, t := range p.Digest {
		d := get(t.Server)
		d.Count++
		if len(d.Examples) < maxExamples {
			d.Examples = append(d.Examples, strings.TrimPrefix(t.Name, t.Server+toolexposure.NameSeparator))
		}
	}
	for _, t := range p.Stopped {
		get(t.Server).Stopped = true
	}
	out := make([]DigestServer, 0, len(by))
	for _, d := range by {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// RenderDigest renders kenaz__load_tools' description: what the tool
// does, one line per server that has tools available but not loaded
// (name, tool count, purpose, up to five example names) or that is
// stopped, and the input shape. The output depends only on its input,
// in a fixed order, so an unchanged catalog renders byte-identical text
// call after call.
func RenderDigest(servers []DigestServer) string {
	var b strings.Builder
	b.WriteString(baseDescription)
	if len(servers) == 0 {
		b.WriteString(" Every available tool is already loaded.\n")
		b.WriteString(inputLine)
		return b.String()
	}
	b.WriteString(" Available but not loaded:\n")
	for _, s := range servers {
		b.WriteString("  ")
		b.WriteString(s.Name)
		switch {
		case s.Stopped:
			b.WriteString(" (stopped)")
		case s.Count == 1:
			b.WriteString(" (1 tool)")
		default:
			fmt.Fprintf(&b, " (%d tools)", s.Count)
		}
		if p := oneSentence(s.Purpose); p != "" {
			b.WriteString(": ")
			b.WriteString(p)
		}
		if len(s.Examples) > 0 {
			b.WriteString(" — e.g. ")
			b.WriteString(strings.Join(s.Examples, ", "))
			if s.Count > len(s.Examples) {
				b.WriteString(", …")
			}
		}
		if s.Stopped {
			b.WriteString(" — not running; its tools cannot be loaded until it is started")
		}
		b.WriteString("\n")
	}
	b.WriteString(inputLine)
	return b.String()
}

// oneSentence trims a recipe description to its first sentence and a
// bounded length, without a trailing period.
func oneSentence(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if i := strings.Index(s, ". "); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSuffix(s, ".")
	if r := []rune(s); len(r) > maxPurposeRunes {
		s = strings.TrimSpace(string(r[:maxPurposeRunes-1])) + "…"
	}
	return s
}
