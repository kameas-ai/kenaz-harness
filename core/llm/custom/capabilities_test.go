package custom

import (
	"context"
	"testing"
)

// TestProviderCapabilitiesAt_ProbesLiveEndpoint is WP14's core proof for
// custom-openai (the mission's designated "probe vehicle", register
// A-5): a live three-step probe (Prober.Probe, already built and tested
// for the pre-flight gate) determines Streaming/StreamingUsage/
// ToolCalling, overlaid onto the static custom-openai.yaml baseline for
// every other field.
func TestProviderCapabilitiesAt_ProbesLiveEndpoint(t *testing.T) {
	srv := groqShapeServer(t)
	defer srv.Close()

	a := New(WithHTTPClient(srv.Client()))
	caps, err := a.ProviderCapabilitiesAt(context.Background(), srv.URL+"/v1", "llama-3.1-70b")
	if err != nil {
		t.Fatalf("ProviderCapabilitiesAt: %v", err)
	}
	if !caps.Streaming {
		t.Error("want Streaming=true from a live probe reporting SSE frames")
	}
	if !caps.ToolCalling {
		t.Error("want ToolCalling=true from a live probe whose step-2 request succeeds (groqShapeServer)")
	}
	// groqShapeServer's own doc comment: "streaming works, tool calling
	// works, but streaming_usage is false (no usage frame in the stream)".
	if caps.UsageReporting {
		t.Error("want UsageReporting=false — groqShapeServer emits no usage frame")
	}
	// Vision comes from the static custom-openai.yaml baseline
	// (image_input: false, WP02) since the probe never touches it.
	if caps.Vision {
		t.Error("want Vision to come from the static catalog baseline (false), got true")
	}
	wantProbed := map[string]bool{"streaming": false, "usage_reporting": false, "tool_calling": false}
	for _, c := range caps.ProbedCapabilities {
		if _, ok := wantProbed[string(c)]; !ok {
			t.Errorf("unexpected key in ProbedCapabilities: %s", c)
			continue
		}
		wantProbed[string(c)] = true
	}
	for k, seen := range wantProbed {
		if !seen {
			t.Errorf("ProbedCapabilities is missing %q — a stale cache hit would shadow this key "+
				"even though the probe never determined it", k)
		}
	}
}

// TestProviderCapabilitiesAt_ToolCallingFalseIsProbed asserts the
// negative direction: an endpoint that rejects tool-calling (vLLM
// shape) reports ToolCalling=false AND still names it in
// ProbedCapabilities — this is what lets the false value overlay the
// static baseline's (possibly true) default rather than being
// indistinguishable from "not probed".
func TestProviderCapabilitiesAt_ToolCallingFalseIsProbed(t *testing.T) {
	srv := vllmShapeServer(t)
	defer srv.Close()

	a := New(WithHTTPClient(srv.Client()))
	caps, err := a.ProviderCapabilitiesAt(context.Background(), srv.URL+"/v1", "some-model")
	if err != nil {
		t.Fatalf("ProviderCapabilitiesAt: %v", err)
	}
	if caps.ToolCalling {
		t.Error("want ToolCalling=false — vllmShapeServer rejects the tool-calling probe step")
	}
	found := false
	for _, c := range caps.ProbedCapabilities {
		if string(c) == "tool_calling" {
			found = true
		}
	}
	if !found {
		t.Error("tool_calling must be named in ProbedCapabilities even when the probed value is false")
	}
}

// TestProviderCapabilitiesAt_AuthFailureReturnsError asserts AC-017(c)'s
// invariant from the custom-openai side: a probe failure (here, an
// unauthenticated probe against an endpoint that requires auth) must be
// a returned error, never a ProviderCapabilities value with everything
// zeroed — Registry only Puts into the cache on a nil error, which is
// what keeps a probe failure from ever overlaying a false-everything
// record onto the static baseline (A-5's stated invariant).
func TestProviderCapabilitiesAt_AuthFailureReturnsError(t *testing.T) {
	srv := authFailServer(t)
	defer srv.Close()

	a := New(WithHTTPClient(srv.Client()))
	if _, err := a.ProviderCapabilitiesAt(context.Background(), srv.URL+"/v1", "some-model"); err == nil {
		t.Fatal("want an error from a 401 probe response, got nil")
	}
}

// TestProviderCapabilitiesAt_EmptyEndpointErrors pins the documented
// contract: unlike ollama (which has a process-wide DefaultBaseURL),
// custom-openai has no sensible universal default endpoint, so an empty
// endpoint must be a hard error, never a silent no-op probe against
// nothing.
func TestProviderCapabilitiesAt_EmptyEndpointErrors(t *testing.T) {
	a := New()
	if _, err := a.ProviderCapabilitiesAt(context.Background(), "", "some-model"); err == nil {
		t.Fatal("want an error for an empty endpoint, got nil")
	}
}

// TestProviderCapabilities_DelegatesToEndpointAtWithEmptyEndpoint pins
// the plain llm.CapabilitiesProvider method's contract: it exists so
// registry.go's read-path gate (`if _, ok := adapter.(llm.
// CapabilitiesProvider); ok`) sees this adapter as capability-aware at
// all, but the real production path always goes through
// ProviderCapabilitiesAt with a real profile endpoint instead (H4 fix)
// — this method degrades to the same "no endpoint" error as calling
// ProviderCapabilitiesAt("", ...) directly.
func TestProviderCapabilities_DelegatesToEndpointAtWithEmptyEndpoint(t *testing.T) {
	a := New()
	if _, err := a.ProviderCapabilities(context.Background(), "some-model"); err == nil {
		t.Fatal("want an error (no endpoint in scope), got nil")
	}
}
