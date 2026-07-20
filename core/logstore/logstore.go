// Package logstore provides a bounded, concurrency-safe in-memory ring
// buffer for structured runtime log rows. It is the "in-app logs" store
// for the Audit Log + Logs observability surface (mission 01NLOGS01).
//
// Architecture
//
//   - Store holds up to Cap rows (default DefaultCap). Oldest rows are
//     evicted when the buffer is full.
//   - A slog.Handler wraps the Store so callers can tee the harness
//     slog handler through it: existing log output is unchanged; Store
//     captures every record for in-app display.
//   - Redaction: rows emitted via the slog handler pass through the
//     RedactMessage helper, which strips patterns that look like API
//     keys or bearer tokens. No plaintext secret is stored.
//   - MCP stderr can be appended via AppendRaw (tagged with the recipe
//     id as the source); it also goes through RedactMessage.
package logstore

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"
)

// DefaultCap is the maximum number of log rows held in the ring buffer
// when callers do not supply an explicit cap. 10 000 rows is ~8 MiB at
// ~800 bytes/row — acceptable for a desktop app.
const DefaultCap = 10_000

// Level is the severity level of a log row.
type Level string

const (
	LevelDebug Level = "debug"
	LevelInfo  Level = "info"
	LevelWarn  Level = "warn"
	LevelError Level = "error"
)

// Row is a single log entry as returned to the frontend. Every field
// is safe to serialise to JSON and display verbatim — no secret bytes
// are stored here; redaction is applied on write.
type Row struct {
	// Timestamp is the RFC 3339 nano representation of the log record time.
	Timestamp string `json:"timestamp"`
	// Level is one of debug / info / warn / error.
	Level Level `json:"level"`
	// Source names the subsystem that emitted the row (e.g. "mcp",
	// "sessions", or a recipe id like "mcp:my-recipe").
	Source string `json:"source"`
	// Message is the (redacted) log message.
	Message string `json:"message"`
}

// Filter controls which rows Tail / List returns.
type Filter struct {
	// Level, if non-empty, restricts results to rows at or above this
	// severity (debug < info < warn < error).
	Level Level `json:"level,omitempty"`
	// Source, if non-empty, restricts to rows whose Source field
	// contains this substring (case-insensitive).
	Source string `json:"source,omitempty"`
	// Search, if non-empty, restricts to rows whose Message contains
	// this substring (case-insensitive).
	Search string `json:"search,omitempty"`
	// Limit caps the number of rows returned. 0 means "return all
	// matching rows". Applied after filtering; rows are newest-first.
	Limit int `json:"limit,omitempty"`
}

// levelOrder maps Level strings to integer ranks for severity comparison.
var levelOrder = map[Level]int{
	LevelDebug: 0,
	LevelInfo:  1,
	LevelWarn:  2,
	LevelError: 3,
}

// Store is the bounded ring buffer for log rows.
//
// Safe for concurrent use.
type Store struct {
	mu   sync.Mutex
	rows []Row
	cap  int
	head int // index of oldest row when full
	size int // number of valid rows (0 ≤ size ≤ cap)
}

// New returns a Store with the given capacity. Cap ≤ 0 uses DefaultCap.
func New(cap int) *Store {
	if cap <= 0 {
		cap = DefaultCap
	}
	return &Store{
		rows: make([]Row, cap),
		cap:  cap,
	}
}

// Append adds a row to the ring buffer, evicting the oldest when full.
// The row's Message is redacted before storage.
func (s *Store) Append(r Row) {
	r.Message = RedactMessage(r.Message)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.size < s.cap {
		s.rows[s.size] = r
		s.size++
	} else {
		// Overwrite the oldest slot.
		s.rows[s.head] = r
		s.head = (s.head + 1) % s.cap
	}
}

// AppendRaw appends a raw text block (e.g. MCP stderr) as one or more
// rows, splitting on newlines. source is tagged as the row Source.
// Empty lines are skipped. Each line is redacted.
func (s *Store) AppendRaw(source, text string, level Level) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		s.Append(Row{
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			Level:     level,
			Source:    source,
			Message:   line,
		})
	}
}

// Snapshot returns a copy of the stored rows in chronological order
// (oldest first). The caller receives its own slice; mutations do not
// affect the store.
func (s *Store) Snapshot() []Row {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.size == 0 {
		return nil
	}
	out := make([]Row, s.size)
	if s.size < s.cap {
		copy(out, s.rows[:s.size])
	} else {
		// Ring has wrapped: oldest is at head.
		n := copy(out, s.rows[s.head:])
		copy(out[n:], s.rows[:s.head])
	}
	return out
}

// List returns rows matching f, newest first. Limit is applied after
// filtering; 0 means unlimited.
func (s *Store) List(f Filter) []Row {
	all := s.Snapshot()
	// Reverse so newest-first.
	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}
	minRank, filterLevel := 0, false
	if f.Level != "" {
		if r, ok := levelOrder[f.Level]; ok {
			minRank = r
			filterLevel = true
		}
	}
	src := strings.ToLower(f.Source)
	search := strings.ToLower(f.Search)

	var out []Row
	for _, row := range all {
		if filterLevel {
			if rank, ok := levelOrder[row.Level]; !ok || rank < minRank {
				continue
			}
		}
		if src != "" && !strings.Contains(strings.ToLower(row.Source), src) {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(row.Message), search) {
			continue
		}
		out = append(out, row)
		if f.Limit > 0 && len(out) >= f.Limit {
			break
		}
	}
	return out
}

// ── slog handler ────────────────────────────────────────────────────────────

// Handler is a slog.Handler that appends every record to a Store. It
// delegates Enabled / WithAttrs / WithGroup to an optional inner
// handler (the file handler), so the file log is unaffected.
type Handler struct {
	store  *Store
	inner  slog.Handler // nil-safe; file handler or composite bridge
	source string       // subsystem tag pre-set via WithSource
	attrs  []slog.Attr  // accumulated structured attrs (for context propagation)
}

// NewHandler wraps inner (may be nil) and writes every record to store.
func NewHandler(store *Store, inner slog.Handler) *Handler {
	return &Handler{store: store, inner: inner}
}

// WithSource returns a copy of the handler with a fixed source tag.
// Used by subsystem loggers to tag their output without caller-site changes.
func (h *Handler) WithSource(source string) *Handler {
	cp := *h
	cp.source = source
	return &cp
}

func (h *Handler) Enabled(ctx context.Context, level slog.Level) bool {
	if h.inner != nil {
		return h.inner.Enabled(ctx, level)
	}
	return true
}

func (h *Handler) Handle(ctx context.Context, rec slog.Record) error {
	// Best-effort store append — never block or fail the caller.
	h.store.Append(Row{
		Timestamp: rec.Time.UTC().Format(time.RFC3339Nano),
		Level:     slogLevelToLevel(rec.Level),
		Source:    h.sourceFor(rec),
		Message:   rec.Message,
	})
	if h.inner != nil {
		return h.inner.Handle(ctx, rec)
	}
	return nil
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	cp := *h
	cp.attrs = append(cp.attrs, attrs...)
	if cp.inner != nil {
		cp.inner = cp.inner.WithAttrs(attrs)
	}
	return &cp
}

func (h *Handler) WithGroup(name string) slog.Handler {
	cp := *h
	if cp.inner != nil {
		cp.inner = cp.inner.WithGroup(name)
	}
	return &cp
}

// sourceFor extracts a subsystem name from the record. It tries (in
// order): the handler's pre-set source tag; a "subsystem" slog attribute
// on the record; the package path extracted from the caller's PC.
func (h *Handler) sourceFor(rec slog.Record) string {
	if h.source != "" {
		return h.source
	}
	// Walk structured attrs to find a "subsystem" key.
	var found string
	rec.Attrs(func(a slog.Attr) bool {
		if a.Key == "subsystem" {
			found = a.Value.String()
			return false
		}
		return true
	})
	if found != "" {
		return found
	}
	// Fallback: derive from the logger PC if available.
	if rec.PC != 0 {
		// Use the package-level source (trim function name).
		fs := strings.Split(rec.Source().Function, "/")
		if len(fs) > 0 {
			pkg := fs[len(fs)-1]
			if dot := strings.Index(pkg, "."); dot > 0 {
				pkg = pkg[:dot]
			}
			return pkg
		}
	}
	return "harness"
}

func slogLevelToLevel(l slog.Level) Level {
	switch {
	case l >= slog.LevelError:
		return LevelError
	case l >= slog.LevelWarn:
		return LevelWarn
	case l >= slog.LevelInfo:
		return LevelInfo
	default:
		return LevelDebug
	}
}

// ── redaction ────────────────────────────────────────────────────────────────

// sensitivePatterns matches common API-key / bearer-token patterns in log
// messages. The regexes are deliberately conservative to avoid false
// positives; they catch the patterns the harness itself emits (Bearer
// tokens, @secret:// refs, "sk-*" OpenAI keys, and hex strings long
// enough to be secrets).
var sensitivePatterns = []*regexp.Regexp{
	// Bearer / token header values
	regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9\-._~+/]+=*`),
	// @secret:// credential references (should not appear in logs, but guard it)
	regexp.MustCompile(`@secret://[^\s"']+`),
	// OpenAI-style keys: sk-<alphanumeric>
	regexp.MustCompile(`\bsk-[A-Za-z0-9]{20,}`),
	// Long hex strings (>= 32 chars) that look like tokens/secrets
	regexp.MustCompile(`\b[0-9a-f]{32,}\b`),
}

// RedactMessage replaces any sensitive-looking substrings in msg with
// "[REDACTED]". Exported so callers (e.g. MCP stderr) can pre-clean
// text before calling AppendRaw.
func RedactMessage(msg string) string {
	for _, re := range sensitivePatterns {
		msg = re.ReplaceAllString(msg, "[REDACTED]")
	}
	return msg
}
