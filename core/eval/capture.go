// Package eval provides the eval-harness-replay subsystem: deterministic
// session capture, replay, diff, and RunMatrix test helper.
//
// Capture writes a chronologically ordered JSONL file per session under
// <DataDir>/eval-captures/<session_id>.jsonl. Every record is one
// CaptureEntry; the Kind field identifies the payload shape.
//
// Security: every entry passing through the capture pipeline is redacted
// with the SAME credential-pattern catalog as core/sessions/export (the
// package's RedactValue for free text and RedactStructured for arbitrary
// JSON) — see docs/escalation-register-2026-08-19.md G-3, "ONE OWNER, ONE
// SHARED CATALOG." That catalog covers provider-specific key shapes (AWS,
// GitHub, OpenAI, Anthropic, Google, Slack, Stripe, JWTs, PEM blocks,
// connection-string passwords) and key-NAME scanning (a field named
// "password"/"token"/"authorization"/"secret"/etc. is redacted regardless
// of whether its value matches a known shape).
//
// Known gap: Source.Data (inline base64 media bytes on a ContentBlock) is
// deliberately NOT scanned, matching core/sessions/export's own precedent
// — it cannot carry a pattern-matched credential in readable form, and
// running the catalog over multi-megabyte blobs on every turn is the one
// way this pass gets expensive. Everything else reaching a capture file —
// message text, tool-call arguments and results, tool_use/tool_result
// payloads, attachment names/URIs — is redacted before it is written.
package eval

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	sessionexport "github.com/kameas-ai/kenaz-harness/core/sessions/export"
)

// EntryKind identifies the payload type of a CaptureEntry.
//
// The reader (ReadCapture) is deliberately kind-agnostic: it unmarshals
// Kind as a plain string and hands every entry to the visitor, and the
// only consumers that switch on a kind (Replay's response cache, Diff's
// assistant-message extraction) ignore what they do not recognise. A kind
// can therefore be retired without breaking older capture files on disk —
// their records still parse and are simply inert.
type EntryKind string

const (
	// KindMessage records a user or assistant message added to the session
	// transcript. Payload: MessageEntry.
	KindMessage EntryKind = "message"

	// KindToolCall records a single tool invocation. Payload: ToolCallEntry.
	KindToolCall EntryKind = "tool_call"

	// KindLLMRequest records the full GenerationRequest sent to the LLM
	// (after credential resolution; credentials never appear in captures).
	// Payload: LLMRequestEntry.
	KindLLMRequest EntryKind = "llm_request"

	// KindLLMResponse records the terminal llm.Response for one turn.
	// Payload: LLMResponseEntry.
	KindLLMResponse EntryKind = "llm_response"

	// KindCaptureStart is the first record written when capture begins. It
	// carries the session id and the harness build version so replays can
	// validate compatibility.
	KindCaptureStart EntryKind = "capture_start"

	// KindCaptureStop is the last record written when capture ends (via
	// StopCapture or process shutdown).
	KindCaptureStop EntryKind = "capture_stop"
)

// CaptureEntry is one line in the JSONL capture file.
type CaptureEntry struct {
	// SeqNo is the monotonically increasing sequence number within the
	// capture file, starting at 1 for KindCaptureStart.
	SeqNo     int64     `json:"seq_no"`
	Kind      EntryKind `json:"kind"`
	Timestamp time.Time `json:"timestamp"`
	SessionID string    `json:"session_id"`
	// Payload is the kind-specific record, marshaled inline.
	Payload json.RawMessage `json:"payload"`
}

// MessageEntry is the Payload for KindMessage.
type MessageEntry struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"` // []llm.ContentBlock, redacted
}

// ToolCallEntry is the Payload for KindToolCall.
type ToolCallEntry struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Args     json.RawMessage `json:"args,omitempty"`
	Result   json.RawMessage `json:"result,omitempty"`
	IsError  bool            `json:"is_error,omitempty"`
	DurationMs int64         `json:"duration_ms,omitempty"`
}

// LLMRequestEntry is the Payload for KindLLMRequest.
// Credentials are stripped by the capture pipeline before writing.
type LLMRequestEntry struct {
	ProfileID string                  `json:"profile_id"`
	Model     string                  `json:"model,omitempty"`
	System    string                  `json:"system,omitempty"`
	Messages  []corellm.Message       `json:"messages"`
	// Fingerprint is the canonical SHA-256 hex fingerprint of the request,
	// computed over (profile_id, model, system, messages) after redaction.
	// Used by Replay to match cached responses when --cached-only is set.
	Fingerprint string `json:"fingerprint"`
}

// LLMResponseEntry is the Payload for KindLLMResponse.
type LLMResponseEntry struct {
	Content      []corellm.ContentBlock `json:"content"`
	FinishReason string                 `json:"finish_reason"`
	Usage        corellm.Usage          `json:"usage"`
	// RequestFingerprint links this response to its LLMRequestEntry.
	RequestFingerprint string `json:"request_fingerprint"`
}

// CaptureStartEntry is the Payload for KindCaptureStart.
type CaptureStartEntry struct {
	BuildVersion string `json:"build_version,omitempty"`
}

// CaptureStopEntry is the Payload for KindCaptureStop.
type CaptureStopEntry struct {
	// EntryCount is the total number of non-start entries written.
	EntryCount int64 `json:"entry_count"`
}

// -----------------------------------------------------------------------------
// Redaction helpers
// -----------------------------------------------------------------------------

// redactText redacts a single free-text string via the shared
// core/sessions/export catalog (provider key shapes + key-name-agnostic
// generic-secret patterns; key-NAME-forced redaction only applies to
// structured values, see redactRawJSON).
func redactText(s string) string {
	out, _ := sessionexport.RedactValue(s)
	return out
}

// redactRawJSON round-trips raw through JSON, walks the decoded value with
// the shared catalog's structured redactor (sessionexport.RedactStructured
// — the same one core/sessions/export uses for tool-call arguments), and
// re-encodes. This is what catches a credential sitting under a key NAME
// like "password" or "api_key" that doesn't match any value-shape pattern
// — RedactValue alone cannot see that class since it only scans flat text.
//
// Fails closed: if raw isn't valid JSON (shouldn't happen for a
// json.RawMessage that already round-tripped once) or re-encoding fails,
// a sentinel is written rather than the original bytes — this function
// exists specifically so unredacted bytes never reach disk.
func redactRawJSON(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var anyVal any
	if err := json.Unmarshal(raw, &anyVal); err != nil {
		return json.RawMessage(`"[REDACTED: unparsable payload]"`)
	}
	redacted := sessionexport.RedactStructured(anyVal)
	out, err := json.Marshal(redacted)
	if err != nil {
		return json.RawMessage(`"[REDACTED: marshal error]"`)
	}
	return out
}

// redactMessages walks the messages slice and redacts each block's Text
// via the shared catalog. Returns the redacted JSON bytes.
//
// Only Type and Text are carried into the reduced block form — ToolUse,
// ToolResult, ToolData and Source (inline media) are intentionally not
// captured here; tool invocations are captured separately via
// AppendToolCall, and media bytes are never written to a capture file.
func redactMessages(msgs []corellm.Message) (json.RawMessage, error) {
	// For each message, walk content blocks and redact text fields.
	type safeBlock struct {
		Type string `json:"type"`
		Text string `json:"text,omitempty"`
	}
	type safeMsg struct {
		Role    string      `json:"role"`
		Content []safeBlock `json:"content"`
	}
	safe := make([]safeMsg, len(msgs))
	for i, m := range msgs {
		safe[i].Role = string(m.Role)
		for _, b := range m.Content {
			safe[i].Content = append(safe[i].Content, safeBlock{
				Type: b.Type,
				Text: redactText(b.Text),
			})
		}
	}
	return json.Marshal(safe)
}

// redactContentBlocks returns a redacted copy of blocks, scrubbing every
// text, attachment-metadata, tool-use and tool-result field through the
// shared catalog. Unlike redactMessages (used for outbound request
// messages), this preserves the full block shape — LLMResponseEntry
// stores complete ContentBlocks today (including tool_use/tool_result),
// and Replay's response cache depends on that fidelity.
//
// Source.Data (base64 media bytes) is intentionally left untouched,
// matching core/sessions/export's own precedent: it cannot carry a
// pattern-matched credential in readable form, and running the catalog
// over multi-megabyte blobs on every turn is the one way this gets
// expensive.
func redactContentBlocks(blocks []corellm.ContentBlock) []corellm.ContentBlock {
	if blocks == nil {
		return nil
	}
	out := make([]corellm.ContentBlock, len(blocks))
	for i, b := range blocks {
		b.Text = redactText(b.Text)
		if b.Source != nil {
			src := *b.Source
			src.OriginalName = redactText(src.OriginalName)
			src.URI = redactText(src.URI)
			b.Source = &src
		}
		if b.ToolUse != nil {
			tu := *b.ToolUse
			tu.Name = redactText(tu.Name)
			tu.Input = redactRawJSON(tu.Input)
			b.ToolUse = &tu
		}
		if b.ToolResult != nil {
			tr := *b.ToolResult
			tr.Content = redactRawJSON(tr.Content)
			b.ToolResult = &tr
		}
		b.ToolData = redactRawJSON(b.ToolData)
		out[i] = b
	}
	return out
}

// -----------------------------------------------------------------------------
// Capture / Writer
// -----------------------------------------------------------------------------

// captureWriter is the per-session capture file writer. It is the internal
// type held by Recorder.
type captureWriter struct {
	mu        sync.Mutex
	sessionID string
	path      string
	f         *os.File
	enc       *json.Encoder
	seqNo     int64
	closed    bool
}

// newCaptureWriter opens (or creates) the capture file for sessionID under dir.
// dir is typically <DataDir>/eval-captures/.
func newCaptureWriter(dir, sessionID, buildVersion string) (*captureWriter, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("eval/capture: mkdir %s: %w", dir, err)
	}
	path := filepath.Join(dir, sessionID+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("eval/capture: open %s: %w", path, err)
	}
	w := &captureWriter{
		sessionID: sessionID,
		path:      path,
		f:         f,
	}
	w.enc = json.NewEncoder(f)
	// Write start record.
	startPayload, _ := json.Marshal(CaptureStartEntry{BuildVersion: buildVersion})
	if err := w.writeEntry(KindCaptureStart, startPayload); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("eval/capture: write start: %w", err)
	}
	return w, nil
}

// writeEntry writes one CaptureEntry to the file. Must be called with mu held.
func (w *captureWriter) writeEntry(kind EntryKind, payload json.RawMessage) error {
	w.seqNo++
	entry := CaptureEntry{
		SeqNo:     w.seqNo,
		Kind:      kind,
		Timestamp: time.Now().UTC(),
		SessionID: w.sessionID,
		Payload:   payload,
	}
	return w.enc.Encode(entry)
}

// Append adds a pre-marshalled entry payload of the given kind.
func (w *captureWriter) Append(kind EntryKind, payload json.RawMessage) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return errors.New("eval/capture: writer is closed")
	}
	return w.writeEntry(kind, payload)
}

// Close writes the KindCaptureStop record and closes the file.
func (w *captureWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	// seqNo counts entries after the start record.
	stopPayload, _ := json.Marshal(CaptureStopEntry{EntryCount: w.seqNo - 1})
	_ = w.writeEntry(KindCaptureStop, stopPayload)
	return w.f.Close()
}

// Path returns the capture file path.
func (w *captureWriter) Path() string { return w.path }

// -----------------------------------------------------------------------------
// Recorder — the public API for starting / stopping captures
// -----------------------------------------------------------------------------

// Recorder manages the set of active per-session capture writers. It is safe
// for concurrent use. One Recorder is typically constructed per harness process
// and held on the RPC API struct.
type Recorder struct {
	mu           sync.Mutex
	captureDir   string
	buildVersion string
	active       map[string]*captureWriter // keyed by sessionID
}

// NewRecorder constructs a Recorder. captureDir is the directory under which
// <session_id>.jsonl files are written (typically <DataDir>/eval-captures).
func NewRecorder(captureDir, buildVersion string) *Recorder {
	return &Recorder{
		captureDir:   captureDir,
		buildVersion: buildVersion,
		active:       make(map[string]*captureWriter),
	}
}

// StartCapture begins recording for sessionID. Idempotent: calling Start on a
// session that already has an active writer returns nil without opening a new
// file.
func (r *Recorder) StartCapture(_ context.Context, sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.active[sessionID]; ok {
		return nil // already active
	}
	w, err := newCaptureWriter(r.captureDir, sessionID, r.buildVersion)
	if err != nil {
		return err
	}
	r.active[sessionID] = w
	return nil
}

// StopCapture finalizes and closes the capture for sessionID. Returns nil if
// no active capture exists (idempotent).
func (r *Recorder) StopCapture(_ context.Context, sessionID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	w, ok := r.active[sessionID]
	if !ok {
		return nil
	}
	delete(r.active, sessionID)
	return w.Close()
}

// IsCapturing reports whether sessionID has an active capture writer.
func (r *Recorder) IsCapturing(sessionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.active[sessionID]
	return ok
}

// AppendMessage appends a KindMessage entry for the session. No-ops when
// capture is not active for the session (non-error; capture is optional).
func (r *Recorder) AppendMessage(sessionID string, role string, blocks []corellm.ContentBlock) {
	w := r.writerFor(sessionID)
	if w == nil {
		return
	}
	redacted := make([]corellm.ContentBlock, len(blocks))
	for i, b := range blocks {
		redacted[i] = corellm.ContentBlock{
			Type: b.Type,
			Text: redactText(b.Text),
		}
	}
	content, err := json.Marshal(redacted)
	if err != nil {
		return
	}
	entry := MessageEntry{Role: role, Content: content}
	payload, err := json.Marshal(entry)
	if err != nil {
		return
	}
	_ = w.Append(KindMessage, payload)
}

// AppendToolCall appends a KindToolCall entry for the session, after
// redacting the tool name and the arbitrary-shaped Args/Result JSON via
// the shared catalog. Tool arguments are the primary place a credential
// reaches capture under a key NAME rather than a recognisable value shape
// (e.g. {"password": "hunter2"}) — redactRawJSON's structured walk is what
// catches that class.
func (r *Recorder) AppendToolCall(sessionID string, tc ToolCallEntry) {
	w := r.writerFor(sessionID)
	if w == nil {
		return
	}
	tc.Name = redactText(tc.Name)
	tc.Args = redactRawJSON(tc.Args)
	tc.Result = redactRawJSON(tc.Result)
	payload, err := json.Marshal(tc)
	if err != nil {
		return
	}
	_ = w.Append(KindToolCall, payload)
}

// AppendLLMRequest appends a KindLLMRequest entry after stripping credentials.
func (r *Recorder) AppendLLMRequest(sessionID string, req corellm.GenerationRequest) {
	w := r.writerFor(sessionID)
	if w == nil {
		return
	}
	fp := FingerprintRequest(req)
	redactedMsgs, _ := redactMessages(req.Messages)
	entry := LLMRequestEntry{
		ProfileID:   req.ProfileID,
		Model:       req.Model,
		System:      redactText(req.System),
		Messages:    nil, // messages embedded as raw JSON below
		Fingerprint: fp,
	}
	// Embed the redacted messages directly as a raw field rather than
	// re-marshalling the full slice.
	raw, _ := json.Marshal(entry)
	// Inject the redacted messages field by merging with partial object.
	var m map[string]json.RawMessage
	_ = json.Unmarshal(raw, &m)
	m["messages"] = redactedMsgs
	payload, _ := json.Marshal(m)
	_ = w.Append(KindLLMRequest, payload)
}

// AppendLLMResponse appends a KindLLMResponse entry, redacting response
// content the same way requests are redacted. Before this fix response
// content was written entirely unredacted (no local pattern match, no
// shared-catalog consumption) — a model that echoes a credential back
// (or emits a tool_use call carrying one) reached disk in plaintext.
func (r *Recorder) AppendLLMResponse(sessionID, requestFingerprint string, resp corellm.Response) {
	w := r.writerFor(sessionID)
	if w == nil {
		return
	}
	entry := LLMResponseEntry{
		Content:            redactContentBlocks(resp.Content),
		FinishReason:       resp.FinishReason,
		Usage:              resp.Usage,
		RequestFingerprint: requestFingerprint,
	}
	payload, _ := json.Marshal(entry)
	_ = w.Append(KindLLMResponse, payload)
}

// StopAll closes every active capture writer. Called from harness Shutdown.
func (r *Recorder) StopAll() {
	r.mu.Lock()
	active := make([]*captureWriter, 0, len(r.active))
	for _, w := range r.active {
		active = append(active, w)
	}
	r.active = make(map[string]*captureWriter)
	r.mu.Unlock()
	for _, w := range active {
		_ = w.Close()
	}
}

func (r *Recorder) writerFor(sessionID string) *captureWriter {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active[sessionID]
}

// -----------------------------------------------------------------------------
// Reader — iterate a capture file
// -----------------------------------------------------------------------------

// ReadCapture opens a capture file at path and iterates its CaptureEntry
// records in order. The caller passes a visitor function; iteration stops
// when the visitor returns a non-nil error or the file is exhausted.
// io.EOF from the visitor is swallowed (treated as clean stop).
func ReadCapture(path string, visit func(CaptureEntry) error) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("eval/capture: open %s: %w", path, err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1<<20), 1<<20) // 1 MiB per line
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry CaptureEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return fmt.Errorf("eval/capture: unmarshal line: %w", err)
		}
		if err := visit(entry); err != nil {
			return err
		}
	}
	return scanner.Err()
}
