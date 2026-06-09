package artifacts_test

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/artifacts"
)

// b64 is a tiny helper for building base64-encoded fixtures.
func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func TestDetectToolOutput_ImageShape_OneCandidate(t *testing.T) {
	t.Parallel()
	body := []byte(`{"type":"image","data":"` + b64("png-bytes") + `","mimeType":"image/png"}`)
	got := artifacts.DetectToolOutput("m1",
		artifacts.PostToolUseEventLike{
			Tool:       "image-gen",
			Result:     json.RawMessage(body),
			ToolCallID: "tc1",
		},
		artifacts.DefaultToolOutputConfig(),
	)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].MimeType != "image/png" {
		t.Errorf("MimeType = %q", got[0].MimeType)
	}
	if string(got[0].Bytes) != "png-bytes" {
		t.Errorf("Bytes = %q", got[0].Bytes)
	}
	if got[0].SourceRef.ToolCallID != "tc1" {
		t.Errorf("ToolCallID = %q", got[0].SourceRef.ToolCallID)
	}
	if got[0].Source != artifacts.SourceToolOutput {
		t.Errorf("Source = %q", got[0].Source)
	}
}

func TestDetectToolOutput_ResourceShape_OneCandidate(t *testing.T) {
	t.Parallel()
	body := []byte(`{
        "type":"resource",
        "resource":{"uri":"file://server/output/data.pdf","mimeType":"application/pdf","blob":"` + b64("pdf-bytes") + `"}
    }`)
	got := artifacts.DetectToolOutput("m1",
		artifacts.PostToolUseEventLike{Result: json.RawMessage(body), ToolCallID: "tc"},
		artifacts.DefaultToolOutputConfig(),
	)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].MimeType != "application/pdf" {
		t.Errorf("MimeType = %q", got[0].MimeType)
	}
	if string(got[0].Bytes) != "pdf-bytes" {
		t.Errorf("Bytes = %q", got[0].Bytes)
	}
	if got[0].Title != "data.pdf" {
		t.Errorf("Title = %q, want data.pdf", got[0].Title)
	}
}

func TestDetectToolOutput_ContentArrayWithTwoImages_TwoCandidates(t *testing.T) {
	t.Parallel()
	body := []byte(`{
        "content":[
            {"type":"image","data":"` + b64("a") + `","mimeType":"image/png"},
            {"type":"image","data":"` + b64("b") + `","mimeType":"image/jpeg"}
        ]
    }`)
	got := artifacts.DetectToolOutput("m1",
		artifacts.PostToolUseEventLike{Result: json.RawMessage(body), ToolCallID: "tc"},
		artifacts.DefaultToolOutputConfig(),
	)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].MimeType != "image/png" || got[1].MimeType != "image/jpeg" {
		t.Errorf("mimes = %q / %q", got[0].MimeType, got[1].MimeType)
	}
	if string(got[0].Bytes) != "a" || string(got[1].Bytes) != "b" {
		t.Errorf("bytes = %q / %q", got[0].Bytes, got[1].Bytes)
	}
}

func TestDetectToolOutput_PlainStringResult_NotCaptured(t *testing.T) {
	t.Parallel()
	body := []byte(`"just a string the tool returned"`)
	got := artifacts.DetectToolOutput("m1",
		artifacts.PostToolUseEventLike{Result: json.RawMessage(body)},
		artifacts.DefaultToolOutputConfig(),
	)
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestDetectToolOutput_EmptyResult_NotCaptured(t *testing.T) {
	t.Parallel()
	got := artifacts.DetectToolOutput("m1",
		artifacts.PostToolUseEventLike{Result: nil},
		artifacts.DefaultToolOutputConfig(),
	)
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestDetectToolOutput_DisabledConfig_AlwaysSkips(t *testing.T) {
	t.Parallel()
	body := []byte(`{"type":"image","data":"` + b64("x") + `","mimeType":"image/png"}`)
	got := artifacts.DetectToolOutput("m1",
		artifacts.PostToolUseEventLike{Result: json.RawMessage(body)},
		artifacts.ToolOutputDetectorConfig{Enabled: false},
	)
	if len(got) != 0 {
		t.Errorf("len = %d, want 0 (disabled)", len(got))
	}
}

func TestDetectToolOutput_MalformedJSON_NoCrashNoCapture(t *testing.T) {
	t.Parallel()
	body := []byte(`{not valid json`)
	got := artifacts.DetectToolOutput("m1",
		artifacts.PostToolUseEventLike{Result: json.RawMessage(body)},
		artifacts.DefaultToolOutputConfig(),
	)
	if got != nil && len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestDetectToolOutput_ImageMissingFields_NotCaptured(t *testing.T) {
	t.Parallel()
	// Missing data field — shape check fails.
	body := []byte(`{"type":"image","mimeType":"image/png"}`)
	got := artifacts.DetectToolOutput("m1",
		artifacts.PostToolUseEventLike{Result: json.RawMessage(body)},
		artifacts.DefaultToolOutputConfig(),
	)
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestDetectToolOutput_AcceptsMediaTypeAlias(t *testing.T) {
	t.Parallel()
	// Some MCP servers emit "media_type" instead of "mimeType".
	body := []byte(`{"type":"image","data":"` + b64("x") + `","media_type":"image/png"}`)
	got := artifacts.DetectToolOutput("m1",
		artifacts.PostToolUseEventLike{Result: json.RawMessage(body)},
		artifacts.DefaultToolOutputConfig(),
	)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
}
