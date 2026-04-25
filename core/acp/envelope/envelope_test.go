package envelope

import (
	"context"
	"encoding/json"
	"errors"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"kaneaz-harness/core/acp"
)

// TestPublicSurfaceUsesNoSDKTypes is a structural assertion that no
// exported method on *Envelope takes or returns a type whose package
// path includes "a2a-go". It's the canary for FR-015 / NFR-010: if a
// future edit accidentally exposes an SDK type on the Envelope
// surface, this test fails.
func TestPublicSurfaceUsesNoSDKTypes(t *testing.T) {
	t.Parallel()
	tp := reflect.TypeOf(&Envelope{})
	for i := 0; i < tp.NumMethod(); i++ {
		m := tp.Method(i)
		fn := m.Type
		for j := 0; j < fn.NumIn(); j++ {
			if pkg := typePkg(fn.In(j)); strings.Contains(pkg, "a2a-go") {
				t.Errorf("method %s parameter %d has SDK type %s", m.Name, j, pkg)
			}
		}
		for j := 0; j < fn.NumOut(); j++ {
			if pkg := typePkg(fn.Out(j)); strings.Contains(pkg, "a2a-go") {
				t.Errorf("method %s result %d has SDK type %s", m.Name, j, pkg)
			}
		}
	}
}

func typePkg(t reflect.Type) string {
	for t.Kind() == reflect.Ptr || t.Kind() == reflect.Slice || t.Kind() == reflect.Chan || t.Kind() == reflect.Array {
		t = t.Elem()
	}
	return t.PkgPath()
}

// TestEnvelopeIsSoleSDKImporter is a structural assertion that no Go
// source file under core/acp/ outside core/acp/envelope/ imports
// github.com/a2aproject/a2a-go. This complements the depguard CI
// rule (FR-015 / C-001) so the invariant is enforced even in
// repositories that have not yet wired golangci-lint into CI.
func TestEnvelopeIsSoleSDKImporter(t *testing.T) {
	t.Parallel()
	// Locate the repo root by walking up from this file.
	_, here, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(here), "..", ".."))
	// We want the core/acp/ root (one level up from envelope/).
	acpRoot := filepath.Join(root, "acp")

	fset := token.NewFileSet()
	violations := []string{}
	if err := walkGo(acpRoot, func(path string) error {
		// Allow envelope/.
		rel, _ := filepath.Rel(acpRoot, path)
		if strings.HasPrefix(filepath.ToSlash(rel), "envelope/") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			if strings.Contains(imp.Path.Value, "a2a-go") {
				violations = append(violations, path+" imports "+imp.Path.Value)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("walk error: %v", err)
	}
	if len(violations) > 0 {
		t.Errorf("FR-015 violations:\n  %s", strings.Join(violations, "\n  "))
	}
}

// TestInProcessDispatchRoundTrip exercises the v1 fixture path: an
// in-process peer registered on the envelope receives a dispatched
// payload and returns a response message. This is the round-trip the
// rest of the mission's tests build on.
func TestInProcessDispatchRoundTrip(t *testing.T) {
	t.Parallel()
	e := New()
	card := acp.AgentCard{
		AgentID:         "test-peer",
		Name:            "Test Peer",
		Version:         "0.0.1",
		ProtocolVersion: acp.ProtocolVersion,
		EndpointURL:     "memory://test-peer",
		Skills: []acp.Skill{
			{ID: "echo", Name: "Echo"},
		},
	}
	e.RegisterInProcessPeer("test-peer", card, func(_ context.Context, in AcceptedTask) (json.RawMessage, error) {
		var got map[string]string
		if err := json.Unmarshal(in.Body, &got); err != nil {
			return nil, err
		}
		got["echoed"] = "yes"
		return json.Marshal(got)
	})

	peer := acp.PeerProfile{
		PeerID:     "test-peer",
		Transport:  acp.TransportLoopback,
		CardSource: acp.CardSourceWellKnown,
	}
	body, _ := json.Marshal(map[string]string{"hello": "world"})
	taskID, stream, err := e.Dispatch(context.Background(), peer, "echo", body)
	if err != nil {
		t.Fatalf("Dispatch err = %v", err)
	}
	if taskID == "" {
		t.Fatal("expected a non-empty wire task id")
	}

	var msgs []acp.Message
	for m := range stream {
		msgs = append(msgs, m)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Direction != acp.DirOutbound || msgs[0].Kind != acp.KindRequest {
		t.Errorf("msg[0] = %+v, want outbound request", msgs[0])
	}
	if msgs[1].Direction != acp.DirInbound || msgs[1].Kind != acp.KindResponse {
		t.Errorf("msg[1] = %+v, want inbound response", msgs[1])
	}
	var got map[string]string
	if err := json.Unmarshal(msgs[1].Payload, &got); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if got["echoed"] != "yes" {
		t.Errorf("response payload = %v, want echoed=yes", got)
	}
}

// TestInProcessDispatchHandlerError converts a handler error into an
// inbound KindError message rather than failing the dispatch outright.
func TestInProcessDispatchHandlerError(t *testing.T) {
	t.Parallel()
	e := New()
	card := acp.AgentCard{Name: "x", ProtocolVersion: acp.ProtocolVersion, EndpointURL: "memory://x"}
	e.RegisterInProcessPeer("x", card, func(_ context.Context, _ AcceptedTask) (json.RawMessage, error) {
		return nil, errors.New("boom")
	})
	peer := acp.PeerProfile{PeerID: "x", Transport: acp.TransportLoopback, CardSource: acp.CardSourceWellKnown}
	_, stream, err := e.Dispatch(context.Background(), peer, "skill", []byte(`{}`))
	if err != nil {
		t.Fatalf("Dispatch err = %v", err)
	}
	var sawError bool
	for m := range stream {
		if m.Kind == acp.KindError {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("expected an inbound KindError message")
	}
}

// TestFetchCardInline returns the inline card with no transport call.
func TestFetchCardInline(t *testing.T) {
	t.Parallel()
	e := New()
	card := &acp.AgentCard{Name: "inline", ProtocolVersion: acp.ProtocolVersion, EndpointURL: "memory://inline"}
	peer := acp.PeerProfile{
		PeerID:     "p",
		Transport:  acp.TransportLoopback,
		CardSource: acp.CardSourceInline,
		InlineCard: card,
	}
	got, err := e.FetchCard(context.Background(), peer)
	if err != nil {
		t.Fatalf("FetchCard err = %v", err)
	}
	if got.Name != "inline" {
		t.Errorf("FetchCard.Name = %q, want inline", got.Name)
	}
}

// TestFetchCardInlineMissing surfaces a clear error when CardSourceInline
// is declared without a card payload.
func TestFetchCardInlineMissing(t *testing.T) {
	t.Parallel()
	e := New()
	peer := acp.PeerProfile{
		PeerID:     "p",
		Transport:  acp.TransportLoopback,
		CardSource: acp.CardSourceInline,
	}
	if _, err := e.FetchCard(context.Background(), peer); err == nil {
		t.Fatal("expected error when InlineCard is nil")
	}
}

// TestFetchCardInMemoryWellKnown uses an in-process peer registration
// to satisfy a well_known fetch in v1 fixtures.
func TestFetchCardInMemoryWellKnown(t *testing.T) {
	t.Parallel()
	e := New()
	want := acp.AgentCard{Name: "wk", ProtocolVersion: acp.ProtocolVersion, EndpointURL: "memory://wk"}
	e.RegisterInProcessPeer("wk", want, func(_ context.Context, _ AcceptedTask) (json.RawMessage, error) {
		return nil, nil
	})
	peer := acp.PeerProfile{PeerID: "wk", Transport: acp.TransportLoopback, CardSource: acp.CardSourceWellKnown}
	got, err := e.FetchCard(context.Background(), peer)
	if err != nil {
		t.Fatalf("FetchCard err = %v", err)
	}
	if got.Name != "wk" {
		t.Errorf("FetchCard.Name = %q, want wk", got.Name)
	}
}

// TestRegisterDialerListener exercises the transport seam.
func TestRegisterDialerListener(t *testing.T) {
	t.Parallel()
	e := New()
	d := stubDialer{kind: acp.TransportLoopback}
	l := stubListener{kind: acp.TransportLoopback}
	e.RegisterDialer(d)
	e.RegisterListener(l)

	if _, err := e.DialerFor(acp.TransportLoopback); err != nil {
		t.Fatalf("DialerFor err = %v", err)
	}
	if _, err := e.ListenerFor(acp.TransportLoopback); err != nil {
		t.Fatalf("ListenerFor err = %v", err)
	}
	if _, err := e.DialerFor(acp.TransportPublic); err == nil {
		t.Fatal("DialerFor missing kind should error")
	}
}

// TestConvertCardRoundTrip ensures the converters are lossless for the
// fields the harness cares about. Belongs here rather than convert_test
// to keep the suite together until the SDK pin lands.
func TestConvertCardRoundTrip(t *testing.T) {
	t.Parallel()
	in := acp.AgentCard{
		AgentID:         "ag",
		Name:            "Agent",
		Description:     "desc",
		Version:         "1.2.3",
		ProtocolVersion: acp.ProtocolVersion,
		EndpointURL:     "memory://ag",
		Skills: []acp.Skill{{
			ID: "s1", Name: "Skill 1", Description: "d",
			InputSchema:  json.RawMessage(`{"type":"object"}`),
			OutputSchema: json.RawMessage(`{"type":"object"}`),
		}},
		AuthSchemes: []string{"none"},
	}
	out := fromSDKCard(toSDKCard(in), in.AgentID)
	if out.Name != in.Name || out.Version != in.Version || len(out.Skills) != 1 {
		t.Fatalf("round-trip mismatch: %+v", out)
	}
	if out.Signed {
		t.Errorf("v1 cards must not be marked signed")
	}
	if out.ProtocolVersion != acp.ProtocolVersion {
		t.Errorf("ProtocolVersion = %q, want %q", out.ProtocolVersion, acp.ProtocolVersion)
	}
}

type stubDialer struct{ kind acp.TransportKind }

func (s stubDialer) Dial(_ context.Context, _ acp.PeerProfile) (Conn, error) { return nil, nil }
func (s stubDialer) Kind() acp.TransportKind                                 { return s.kind }

type stubListener struct{ kind acp.TransportKind }

func (s stubListener) Listen(_ context.Context, _ string) (TransportListener, error) {
	return nil, nil
}
func (s stubListener) Kind() acp.TransportKind { return s.kind }
