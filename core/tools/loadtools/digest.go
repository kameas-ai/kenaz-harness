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

// DigestHeading titles the system-prompt section the digest renders as;
// kenaz__load_tools' static description points at it.
const DigestHeading = "Available but not loaded"

// staticDescription is kenaz__load_tools' description on every call. It
// never changes, so the tool stays inside the cacheable prefix; the
// changing list of unloaded capabilities is the digest, sent as a
// per-call system section after the cache marker.
const staticDescription = "Load the full definitions of tools you do not yet see. " +
	"The current list of unloaded capabilities is in the system prompt section '" + DigestHeading + "'."

// introLine follows the heading: how to use the list.
const introLine = "Call " + Name + ` with {"servers": [...]} or {"tools": ["server__tool" or "server__prefix*"]} to load any of these; their definitions arrive on your next call.`

// NoteDefaultsApply is the digest note for a call whose tool settings
// could not be read: the list then reflects the harness defaults.
const NoteDefaultsApply = "Tool settings could not be read; defaults apply."

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

// RenderDigest renders the digest: a system-prompt section listing one
// line per server that has tools available but not loaded (name, tool
// count, purpose, up to five example names) or that is stopped, plus an
// optional note. It renders "" when there is nothing to list and no
// note. The output depends only on its input, in a fixed order, so an
// unchanged catalog renders byte-identical text call after call.
func RenderDigest(servers []DigestServer, note string) string {
	if len(servers) == 0 && note == "" {
		return ""
	}
	var lines []string
	lines = append(lines, "## "+DigestHeading)
	if note != "" {
		lines = append(lines, note)
	}
	if len(servers) > 0 {
		lines = append(lines, introLine)
	}
	for _, s := range servers {
		var b strings.Builder
		b.WriteString("- ")
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
		lines = append(lines, b.String())
	}
	return strings.Join(lines, "\n")
}

// DefaultDigest builds the digest servers for a catalog whose tiers
// could not be resolved: every entry the harness default puts in the
// summary tier (toolexposure.DefaultTier), grouped by server.
func DefaultDigest(entries []CatalogEntry, purposes map[string]string) []DigestServer {
	var p toolexposure.Partition
	for _, e := range entries {
		ct := toolexposure.CatalogTool{Name: e.Name, Server: e.Server, Running: true}
		if toolexposure.DefaultTier(ct) == toolexposure.TierSummary {
			p.Digest = append(p.Digest, toolexposure.ResolvedTool{Name: e.Name, Server: e.Server, Running: true, Tier: toolexposure.TierSummary})
		}
	}
	sort.SliceStable(p.Digest, func(i, j int) bool { return p.Digest[i].Name < p.Digest[j].Name })
	return BuildDigest(p, purposes)
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
