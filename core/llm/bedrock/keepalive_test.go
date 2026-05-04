package bedrock

import (
	"net/http"
	"testing"
)

// TestNew_DefaultUsesKeepaliveTransport asserts that the
// production-default Adapter constructed with no options carries the
// shared httpx keepalive transport on its HTTP client. The default
// http.Client.Transport is nil; any non-nil value here means our
// custom transport landed.
//
// Bedrock has two auth flavours: SDK (aws_profile) uses its own
// transport, but the bearer-auth path goes through a.httpc — that's
// the surface this WP01 change covers.
func TestNew_DefaultUsesKeepaliveTransport(t *testing.T) {
	a := New()
	if a.httpc == nil {
		t.Fatal("New() left httpc nil; bearerClient() would fall through to http.DefaultClient with no keepalive")
	}
	if a.httpc.Transport == nil {
		t.Fatal("New() default httpc.Transport is nil; expected the keepalive transport from core/llm/httpx")
	}
}

// TestNew_BearerClient_UsesDefaultByDefault verifies that the
// bearer-auth code path resolves to the keepalive client when no
// WithHTTPClient override is supplied.
func TestNew_BearerClient_UsesDefaultByDefault(t *testing.T) {
	a := New()
	c := a.bearerClient()
	if c == nil {
		t.Fatal("bearerClient() returned nil")
	}
	if c.Transport == nil {
		t.Fatal("bearerClient() returned a client with nil Transport; keepalive default missing")
	}
}

// TestWithHTTPClient_OverridesKeepaliveDefault guarantees the
// WithHTTPClient option still wins. Tests that point this at an
// httptest.Server.Client must not get a transport-shadowed client.
func TestWithHTTPClient_OverridesKeepaliveDefault(t *testing.T) {
	custom := &http.Client{}
	a := New(WithHTTPClient(custom))
	if a.httpc != custom {
		t.Fatal("WithHTTPClient did not override the default client")
	}
	if a.httpc.Transport != nil {
		t.Fatal("WithHTTPClient client should be used as-is; non-nil Transport indicates the default leaked through")
	}
	// And bearerClient must surface the override too.
	if a.bearerClient() != custom {
		t.Fatal("bearerClient() did not honour WithHTTPClient override")
	}
}
