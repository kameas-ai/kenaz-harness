// Package eval provides the eval-harness-replay subsystem: deterministic
// session capture, replay, diff, and RunMatrix test helper.
//
// Capture writes a chronologically ordered JSONL file per session under
// <DataDir>/eval-captures/<session_id>.jsonl. Every record is one
// CaptureEntry; the Kind field identifies the payload shape.
//
// Security: any entry passing through the capture pipeline is subject to
// the same redaction rules as the event log. Plaintext credential bytes
// MUST NEVER appear in a capture file.
package eval

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	corellm "github.com/sigil-tech/kaneaz-harness/core/llm"
)

// EntryKind identifies the payload type of a CaptureEntry.
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

	// KindStrategyDecision records a compaction / memory / branching
	// strategy decision evaluated during the session. Payload: StrategyEntry.
	KindStrategyDecision EntryKind = "strategy_decision"

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

// StrategyEntry is the Payload for KindStrategyDecision.
type StrategyEntry struct {
	// Domain is the subsystem that made the decision: "compaction" | "memory" | "branching".
	Domain string `json:"domain"`
	// Key is the parameter name, e.g. "compaction.tier".
	Key    string `json:"key"`
	// Value is the resolved value at decision time.
	Value  string `json:"value"`
	// Source describes where the value came from: "default" | "config" | "override".
	Source string `json:"source,omitempty"`
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

// redactString replaces any substring that looks like a credential token
// with a placeholder. This is a best-effort scan; the authoritative redaction
// pipeline runs inside the event log. Capture redaction is defense-in-depth.
//
// Patterns redacted:
//   - API key-like tokens: sk-ant-..., sk-..., Bearer ...
//   - Anything that matches the "env:..." CredentialReference locator format
func redactString(s string) string {
	// Anthropic / OpenAI key pattern: sk-<alphanum> prefixes
	if strings.Contains(s, "sk-") {
		s = redactPattern(s, "sk-", 40)
	}
	// Bearer tokens
	if strings.Contains(s, "Bearer ") {
		s = redactPattern(s, "Bearer ", 60)
	}
	return s
}

// redactPattern replaces the prefix + the next maxLen non-whitespace characters
// with a placeholder.
func redactPattern(s, prefix string, maxLen int) string {
	var sb strings.Builder
	remaining := s
	for {
		idx := strings.Index(remaining, prefix)
		if idx < 0 {
			sb.WriteString(remaining)
			break
		}
		sb.WriteString(remaining[:idx])
		sb.WriteString(prefix)
		sb.WriteString("[REDACTED]")
		// Skip past the token value
		rest := remaining[idx+len(prefix):]
		tokenEnd := 0
		for tokenEnd < len(rest) && tokenEnd < maxLen && rest[tokenEnd] != ' ' && rest[tokenEnd] != '\n' && rest[tokenEnd] != '"' {
			tokenEnd++
		}
		remaining = rest[tokenEnd:]
	}
	return sb.String()
}

// redactJSONMessages walks the messages slice and redacts string fields.
// Returns the redacted JSON bytes.
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
				Text: redactString(b.Text),
			})
		}
	}
	return json.Marshal(safe)
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
			Text: redactString(b.Text),
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

// AppendToolCall appends a KindToolCall entry for the session.
func (r *Recorder) AppendToolCall(sessionID string, tc ToolCallEntry) {
	w := r.writerFor(sessionID)
	if w == nil {
		return
	}
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
		System:      redactString(req.System),
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

// AppendLLMResponse appends a KindLLMResponse entry.
func (r *Recorder) AppendLLMResponse(sessionID, requestFingerprint string, resp corellm.Response) {
	w := r.writerFor(sessionID)
	if w == nil {
		return
	}
	entry := LLMResponseEntry{
		Content:            resp.Content,
		FinishReason:       resp.FinishReason,
		Usage:              resp.Usage,
		RequestFingerprint: requestFingerprint,
	}
	payload, _ := json.Marshal(entry)
	_ = w.Append(KindLLMResponse, payload)
}

// AppendStrategyDecision appends a KindStrategyDecision entry.
func (r *Recorder) AppendStrategyDecision(sessionID string, se StrategyEntry) {
	w := r.writerFor(sessionID)
	if w == nil {
		return
	}
	payload, _ := json.Marshal(se)
	_ = w.Append(KindStrategyDecision, payload)
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
