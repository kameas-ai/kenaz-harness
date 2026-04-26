package artifacts

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// ToolOutputDetectorConfig gates the detector. The Enabled field
// mirrors CaptureConfig.AutoCaptureToolOutputs; passing
// `Enabled: false` short-circuits the entire pass.
type ToolOutputDetectorConfig struct {
	Enabled bool
}

// DefaultToolOutputConfig returns the spec's default (enabled).
func DefaultToolOutputConfig() ToolOutputDetectorConfig {
	return ToolOutputDetectorConfig{Enabled: true}
}

// PostToolUseEventLike is the structural shape of the toolloop
// PostToolUseEvent that DetectToolOutput consumes. We avoid importing
// core/toolloop to keep the dep graph acyclic — toolloop will pass
// its native event into a thin shim at hook-wiring time (WP02).
type PostToolUseEventLike struct {
	// Tool is the human-readable tool name (e.g. "image-gen-server.draw").
	Tool string
	// Server is the MCP server identifier (may be empty for built-ins).
	Server string
	// Args is the JSON-encoded tool arguments. Reserved for future
	// matching — v1 does not key off args.
	Args json.RawMessage
	// Result is the JSON-encoded tool result. Detection keys off this.
	Result json.RawMessage
	// ToolCallID is the per-call id propagated into the artifact's
	// SourceRef.ToolCallID.
	ToolCallID string
}

// DetectToolOutput inspects ev.Result for the three MCP-style
// file-shaped envelopes laid out in the spec's FR-005:
//
//   - {"type":"image","data":"<base64>","mimeType":"<mime>"}
//   - {"type":"resource","resource":{"uri":"...","mimeType":"...","blob":"<base64>"}}
//   - {"content":[{"type":"image"|"file","data":"<base64>","mimeType":"..."}]}
//
// Plain text returns are NEVER captured — we don't speculate. Malformed
// JSON returns the empty slice (no panic).
//
// Title hints are derived per-shape:
//   - image envelopes: "tool-output." + extension(mime).
//   - resource envelopes: the basename of the resource uri when
//     present, else "resource." + extension(mime).
//   - content-array elements: "tool-output[i]." + extension(mime).
func DetectToolOutput(messageID string, ev PostToolUseEventLike, cfg ToolOutputDetectorConfig) []CaptureCandidate {
	if !cfg.Enabled {
		return nil
	}
	if len(ev.Result) == 0 {
		return nil
	}
	// Try parse as a generic JSON object first. If the result isn't
	// JSON at all, we're done — strings / numbers / arrays at the
	// top level are not the file-shape envelopes we recognize.
	var top map[string]json.RawMessage
	if err := json.Unmarshal(ev.Result, &top); err != nil {
		return nil
	}

	out := make([]CaptureCandidate, 0, 1)

	// Shape 1: top-level "type":"image".
	if t := readJSONString(top, "type"); t == "image" {
		if c, ok := buildImageCandidate(messageID, ev.ToolCallID, top, "tool-output"); ok {
			out = append(out, c)
		}
	}

	// Shape 2: top-level "type":"resource".
	if t := readJSONString(top, "type"); t == "resource" {
		if rawRes, ok := top["resource"]; ok {
			var res map[string]json.RawMessage
			if err := json.Unmarshal(rawRes, &res); err == nil {
				if c, ok := buildResourceCandidate(messageID, ev.ToolCallID, res); ok {
					out = append(out, c)
				}
			}
		}
	}

	// Shape 3: "content":[{...}, {...}].
	if rawContent, ok := top["content"]; ok {
		var items []map[string]json.RawMessage
		if err := json.Unmarshal(rawContent, &items); err == nil {
			for i, item := range items {
				typ := readJSONString(item, "type")
				if typ != "image" && typ != "file" {
					continue
				}
				title := fmt.Sprintf("tool-output[%d]", i)
				if c, ok := buildImageCandidate(messageID, ev.ToolCallID, item, title); ok {
					out = append(out, c)
				}
			}
		}
	}

	return out
}

// buildImageCandidate hydrates a CaptureCandidate from a {type, data,
// mimeType} envelope. The data field is base64-decoded; ill-formed
// base64 fails the candidate (we don't capture truncated images).
func buildImageCandidate(messageID, toolCallID string, env map[string]json.RawMessage, titleStem string) (CaptureCandidate, bool) {
	data := readJSONString(env, "data")
	if data == "" {
		return CaptureCandidate{}, false
	}
	mt := readJSONString(env, "mimeType")
	if mt == "" {
		// Some MCP implementations use "media_type" — accept that too.
		mt = readJSONString(env, "media_type")
	}
	if mt == "" {
		return CaptureCandidate{}, false
	}
	bytes, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return CaptureCandidate{}, false
	}
	title := titleStem + extensionFromMime(mt)
	return CaptureCandidate{
		Title:    title,
		MimeType: mt,
		Bytes:    bytes,
		Source:   SourceToolOutput,
		SourceRef: ArtifactSourceRef{
			MessageID:  messageID,
			ToolCallID: toolCallID,
			Filename:   title,
		},
	}, true
}

// buildResourceCandidate hydrates a CaptureCandidate from a
// {uri, mimeType, blob} envelope (the inner resource of a
// "type":"resource" envelope).
func buildResourceCandidate(messageID, toolCallID string, res map[string]json.RawMessage) (CaptureCandidate, bool) {
	blob := readJSONString(res, "blob")
	if blob == "" {
		return CaptureCandidate{}, false
	}
	mt := readJSONString(res, "mimeType")
	if mt == "" {
		mt = readJSONString(res, "media_type")
	}
	if mt == "" {
		return CaptureCandidate{}, false
	}
	bytes, err := base64.StdEncoding.DecodeString(blob)
	if err != nil {
		return CaptureCandidate{}, false
	}
	title := basenameFromURI(readJSONString(res, "uri"))
	if title == "" {
		title = "resource" + extensionFromMime(mt)
	}
	return CaptureCandidate{
		Title:    title,
		MimeType: mt,
		Bytes:    bytes,
		Source:   SourceToolOutput,
		SourceRef: ArtifactSourceRef{
			MessageID:  messageID,
			ToolCallID: toolCallID,
			Filename:   title,
		},
	}, true
}

// readJSONString unwraps a json.RawMessage string field in a map. The
// helper returns "" when the key is absent OR not a string — either
// way the candidate fails the shape check.
func readJSONString(m map[string]json.RawMessage, key string) string {
	raw, ok := m[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

// extensionFromMime maps a MIME type to a filesystem extension
// (including the leading '.'). Unknown types fall through to ".bin".
// Only the subset the spec calls out for the artifact UI is mapped;
// expanding is a follow-up.
func extensionFromMime(mt string) string {
	switch mt {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/svg+xml":
		return ".svg"
	case "application/pdf":
		return ".pdf"
	case "text/plain":
		return ".txt"
	case "text/markdown":
		return ".md"
	case "text/html":
		return ".html"
	case "application/json":
		return ".json"
	}
	return ".bin"
}

// basenameFromURI returns the last path segment of a URI. Empty when
// the URI is empty or has no path component.
func basenameFromURI(uri string) string {
	if uri == "" {
		return ""
	}
	// Strip query and fragment.
	for _, sep := range []byte{'?', '#'} {
		for i := 0; i < len(uri); i++ {
			if uri[i] == sep {
				uri = uri[:i]
				break
			}
		}
	}
	last := -1
	for i := 0; i < len(uri); i++ {
		if uri[i] == '/' {
			last = i
		}
	}
	if last < 0 || last == len(uri)-1 {
		return ""
	}
	return uri[last+1:]
}
