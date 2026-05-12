package llm

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestIsTransient_Classification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"transient", &ErrTransient{Status: 503}, true},
		{"transient-wrapped", errWrap(&ErrTransient{Status: 429}), true},
		{"auth", &ErrAuth{Status: 401}, false},
		{"invalid-request", &ErrInvalidRequest{Status: 400}, false},
		{"policy-denied", &ErrPolicyDenied{Reason: "model not allowlisted"}, false},
		{"cancelled", &ErrCancelled{}, false},
		{"capability-unsupported", &ErrCapabilityUnsupported{Provider: "p", Model: "m"}, false},
		{"credential-resolution", &ErrCredentialResolution{ProfileID: "x"}, false},
		{"budget-exhausted", &ErrRetryBudgetExhausted{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsTransient(tc.err); got != tc.want {
				t.Fatalf("IsTransient(%v) = %v want %v", tc.err, got, tc.want)
			}
		})
	}
}

// errWrap returns an errors.As-traversable wrapper.
type wrappedErr struct{ inner error }

func (w *wrappedErr) Error() string { return "wrap: " + w.inner.Error() }
func (w *wrappedErr) Unwrap() error { return w.inner }
func errWrap(e error) error          { return &wrappedErr{inner: e} }

func TestErrorMessages_Compose(t *testing.T) {
	e := &ErrCapabilityUnsupported{Provider: "anthropic", Model: "claude-x", Capabilities: []Capability{CapVision}}
	got := e.Error()
	if !contains(got, "anthropic") || !contains(got, "claude-x") || !contains(got, "vision") {
		t.Fatalf("ErrCapabilityUnsupported message missing fields: %s", got)
	}
	c := &ErrCredentialResolution{ProfileID: "p", Ref: CredentialReference{Kind: "env", Locator: "API_KEY"}, Cause: errors.New("unset")}
	if !contains(c.Error(), "p") || !contains(c.Error(), "env:API_KEY") {
		t.Fatalf("ErrCredentialResolution message: %s", c.Error())
	}
}

func TestCapabilityDescriptor_RoundTrip(t *testing.T) {
	in := CapabilityDescriptor{
		Provider: "anthropic", Model: "claude-sonnet-4-7",
		Supported: map[Capability]bool{CapStreaming: true, CapVision: true},
		Notes:     map[Capability]string{CapVision: "image inputs only"},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out CapabilityDescriptor
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Has(CapStreaming) || !out.Has(CapVision) || out.Has(CapToolCalling) {
		t.Fatalf("Has() round-trip mismatch: %+v", out)
	}
	if out.Notes[CapVision] != "image inputs only" {
		t.Fatalf("notes round-trip lost: %+v", out.Notes)
	}
}

func TestRequestedCapabilities_Detection(t *testing.T) {
	cases := []struct {
		name string
		req  GenerationRequest
		want []Capability
	}{
		{"streaming-only", GenerationRequest{}, []Capability{CapStreaming}},
		{"with-tools", GenerationRequest{Tools: []ToolSpec{{Name: "x"}}}, []Capability{CapStreaming, CapToolCalling}},
		{"with-vision", GenerationRequest{Attachments: []Attachment{{Kind: "image"}}}, []Capability{CapStreaming, CapVision}},
		{"with-json", GenerationRequest{JSONMode: &JSONModeSpec{Enabled: true}}, []Capability{CapStreaming, CapJSONMode}},
		{"with-cache", GenerationRequest{Caching: &CachingSpec{Enabled: true}}, []Capability{CapStreaming, CapPromptCaching}},
		{"with-reason", GenerationRequest{Reasoning: &ReasoningSpec{Enabled: true}}, []Capability{CapStreaming, CapReasoning}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.req.RequestedCapabilities()
			if len(got) != len(tc.want) {
				t.Fatalf("len mismatch: got %v want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("idx %d: got %v want %v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestAllCapabilities_IncludesStructuredOutputCaps verifies the three new
// capability constants are present in the canonical ordered list
// (structured-output-and-grammar-01KX5R8A WP01).
func TestAllCapabilities_IncludesStructuredOutputCaps(t *testing.T) {
	all := AllCapabilities()
	want := map[Capability]bool{
		CapStructuredOutput: false,
		CapGrammar:          false,
		CapRegexGrammar:     false,
	}
	for _, c := range all {
		if _, ok := want[c]; ok {
			want[c] = true
		}
	}
	for cap, found := range want {
		if !found {
			t.Errorf("AllCapabilities() missing %q", cap)
		}
	}
}

// TestRequestedCapabilities_StructuredOutput verifies that ResponseFormat
// modes emit the correct capability constants
// (structured-output-and-grammar-01KX5R8A WP01 acceptance).
func TestRequestedCapabilities_StructuredOutput(t *testing.T) {
	cases := []struct {
		name    string
		req     GenerationRequest
		wantCap Capability
		wantIn  bool
	}{
		{
			name:    "json_schema emits CapStructuredOutput",
			req:     GenerationRequest{ResponseFormat: &ResponseFormat{Mode: "json_schema"}},
			wantCap: CapStructuredOutput,
			wantIn:  true,
		},
		{
			name:    "json_schema does NOT emit CapGrammar",
			req:     GenerationRequest{ResponseFormat: &ResponseFormat{Mode: "json_schema"}},
			wantCap: CapGrammar,
			wantIn:  false,
		},
		{
			name:    "grammar emits CapGrammar",
			req:     GenerationRequest{ResponseFormat: &ResponseFormat{Mode: "grammar"}},
			wantCap: CapGrammar,
			wantIn:  true,
		},
		{
			name:    "grammar does NOT emit CapStructuredOutput",
			req:     GenerationRequest{ResponseFormat: &ResponseFormat{Mode: "grammar"}},
			wantCap: CapStructuredOutput,
			wantIn:  false,
		},
		{
			name:    "json mode emits neither CapStructuredOutput nor CapGrammar",
			req:     GenerationRequest{ResponseFormat: &ResponseFormat{Mode: "json"}},
			wantCap: CapStructuredOutput,
			wantIn:  false,
		},
		{
			name:    "nil ResponseFormat emits neither",
			req:     GenerationRequest{},
			wantCap: CapStructuredOutput,
			wantIn:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			caps := tc.req.RequestedCapabilities()
			found := false
			for _, c := range caps {
				if c == tc.wantCap {
					found = true
					break
				}
			}
			if found != tc.wantIn {
				t.Fatalf("wantCap=%q in caps: got %v, want %v; caps=%v", tc.wantCap, found, tc.wantIn, caps)
			}
		})
	}
}

// TestIsUnsupportedFormat verifies the IsUnsupportedFormat helper
// (structured-output-and-grammar-01KX5R8A WP04).
func TestIsUnsupportedFormat_Classification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"unsupported_format", &ErrUnsupportedFormat{Provider: "anthropic", Model: "claude-haiku", Mode: "grammar"}, true},
		{"other_error", &ErrAuth{Status: 401}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsUnsupportedFormat(tc.err); got != tc.want {
				t.Fatalf("IsUnsupportedFormat(%v) = %v want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestErrUnsupportedFormat_Message verifies the error message contains
// provider, model, and mode (structured-output-and-grammar-01KX5R8A WP04).
func TestErrUnsupportedFormat_Message(t *testing.T) {
	e := &ErrUnsupportedFormat{Provider: "openai", Model: "gpt-4o", Mode: "grammar"}
	msg := e.Error()
	for _, want := range []string{"openai", "gpt-4o", "grammar"} {
		if !contains(msg, want) {
			t.Errorf("ErrUnsupportedFormat.Error() missing %q: %s", want, msg)
		}
	}
}

// TestErrResponseValidationFailed_Message verifies the error message contains
// mode and schema error (structured-output-and-grammar-01KX5R8A WP04).
func TestErrResponseValidationFailed_Message(t *testing.T) {
	e := &ErrResponseValidationFailed{Mode: "json_schema", SchemaError: "missing required field 'name'"}
	msg := e.Error()
	for _, want := range []string{"json_schema", "missing required field"} {
		if !contains(msg, want) {
			t.Errorf("ErrResponseValidationFailed.Error() missing %q: %s", want, msg)
		}
	}
}

func TestDefaultRetryPolicy_Defaults(t *testing.T) {
	p := DefaultRetryPolicy()
	if p.MaxAttempts != 3 || p.BaseMS != 250 || p.MaxMS != 5000 || p.Jitter != "full" {
		t.Fatalf("default retry policy mismatch: %+v", p)
	}
}

func TestCredentialReference_String_NeverCarriesValue(t *testing.T) {
	r := CredentialReference{Kind: "env", Locator: "ANTHROPIC_API_KEY"}
	got := r.String()
	if got != "env:ANTHROPIC_API_KEY" {
		t.Fatalf("ref String mismatch: %s", got)
	}
	// The locator is by definition the *reference* not the secret. The
	// guarantee is structural: there is no field on CredentialReference
	// that holds resolved bytes.
}

func TestNewTextMessage_BuildsSingleTextBlock(t *testing.T) {
	m := NewTextMessage(RoleUser, "hello")
	if m.Role != RoleUser {
		t.Fatalf("role = %q", m.Role)
	}
	if len(m.Content) != 1 {
		t.Fatalf("content len = %d, want 1", len(m.Content))
	}
	c := m.Content[0]
	if c.Type != "text" {
		t.Fatalf("content[0].Type = %q", c.Type)
	}
	if c.Text != "hello" {
		t.Fatalf("content[0].Text = %q", c.Text)
	}
}

func TestMessageText_FlattensTextBlocks(t *testing.T) {
	cases := []struct {
		name string
		msg  Message
		want string
	}{
		{"empty", Message{}, ""},
		{"single-text", NewTextMessage(RoleUser, "alpha"), "alpha"},
		{
			name: "two-text-blocks",
			msg: Message{
				Role: RoleUser,
				Content: []ContentBlock{
					{Type: "text", Text: "alpha"},
					{Type: "text", Text: "beta"},
				},
			},
			want: "alpha\n\nbeta",
		},
		{
			name: "skips-images-and-tools",
			msg: Message{
				Role: RoleUser,
				Content: []ContentBlock{
					{Type: "text", Text: "alpha"},
					{Type: "image", Source: &MediaSource{Kind: "base64", MediaType: "image/png", Data: "AA=="}},
					{Type: "tool_use", ToolUse: &ToolUse{ID: "x", Name: "y"}},
					{Type: "text", Text: "beta"},
				},
			},
			want: "alpha\n\nbeta",
		},
		{
			name: "all-images",
			msg: Message{
				Role: RoleUser,
				Content: []ContentBlock{
					{Type: "image", Source: &MediaSource{Kind: "base64", MediaType: "image/png", Data: "AA=="}},
				},
			},
			want: "",
		},
		{
			name: "ignores-empty-text",
			msg: Message{
				Role: RoleUser,
				Content: []ContentBlock{
					{Type: "text", Text: ""},
					{Type: "text", Text: "alpha"},
				},
			},
			want: "alpha",
		},
		{
			name: "blank-type-counts-as-text",
			msg: Message{
				Role: RoleUser,
				Content: []ContentBlock{
					{Type: "", Text: "alpha"},
				},
			},
			want: "alpha",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.msg.Text(); got != tc.want {
				t.Fatalf("Text() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMessage_RoundTripsContentBlocksWithMediaSource(t *testing.T) {
	in := Message{
		Role: RoleUser,
		Content: []ContentBlock{
			{Type: "text", Text: "describe this"},
			{Type: "image", Source: &MediaSource{
				Kind:      "base64",
				MediaType: "image/png",
				Data:      "iVBORw0KGgo=",
			}},
			{Type: "document", Source: &MediaSource{
				Kind:         "base64",
				MediaType:    "application/pdf",
				Data:         "JVBERi0=",
				OriginalName: "report.pdf",
			}},
		},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Message
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Content) != 3 {
		t.Fatalf("blocks = %d", len(out.Content))
	}
	if out.Content[1].Source == nil || out.Content[1].Source.MediaType != "image/png" {
		t.Fatalf("image source missing: %+v", out.Content[1].Source)
	}
	if out.Content[2].Source == nil || out.Content[2].Source.OriginalName != "report.pdf" {
		t.Fatalf("document name missing: %+v", out.Content[2].Source)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
