// Package logging provides a file-backed structured logger writing
// to ~/.kenaz/harness.log. Used to debug the LLM send / stream path
// without surfacing every event into the UI.
//
// The logger is process-global; callers reach it via L() to avoid
// threading a *Logger through every function. File handle is opened
// lazily on first call and held for the process lifetime.
package logging

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	defaultDir  = ".kenaz"
	defaultFile = "harness.log"
)

var (
	once      sync.Once
	loggerInt *slog.Logger
	logFile   *os.File
	openErr   error
)

// L returns the process-global slog.Logger writing JSON lines to
// ~/.kenaz/harness.log. On the first call the file is opened (parent
// dir created with mode 0700) and a JSON-line handler is wired. If
// the file can't be opened the logger falls back to stderr.
func L() *slog.Logger {
	once.Do(initLogger)
	return loggerInt
}

func initLogger() {
	home, err := os.UserHomeDir()
	if err != nil {
		fallback("home dir: " + err.Error())
		return
	}
	dir := filepath.Join(home, defaultDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fallback("mkdir " + dir + ": " + err.Error())
		return
	}
	path := filepath.Join(dir, defaultFile)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		fallback("open " + path + ": " + err.Error())
		return
	}
	logFile = f
	// Tee to stderr in dev so wails-dev console shows logs too — the
	// MultiWriter is cheap and the volume is low.
	w := io.MultiWriter(os.Stderr, f)
	loggerInt = slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:     slog.LevelDebug,
		AddSource: false,
	}))
	loggerInt.Info("logger.opened",
		"path", path,
		"pid", os.Getpid(),
		"opened_at", time.Now().UTC().Format(time.RFC3339Nano),
	)
}

func fallback(reason string) {
	openErr = fmt.Errorf("logging fallback: %s", reason)
	loggerInt = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	loggerInt.Warn("logger.fallback", "reason", reason)
}

// LogClientEvent is the entry point bound through the Wails surface
// so the frontend can append structured records into the same file.
// level: "debug" | "info" | "warn" | "error". Anything else maps to
// info.
func LogClientEvent(level, message string, attrs map[string]any) {
	l := L()
	args := flattenAttrs(attrs)
	switch level {
	case "debug":
		l.Debug("client."+message, args...)
	case "warn":
		l.Warn("client."+message, args...)
	case "error":
		l.Error("client."+message, args...)
	default:
		l.Info("client."+message, args...)
	}
}

func flattenAttrs(m map[string]any) []any {
	if len(m) == 0 {
		return nil
	}
	out := make([]any, 0, len(m)*2)
	for k, v := range m {
		out = append(out, k, v)
	}
	return out
}

// Path returns the resolved log file path (debug helper for status
// surfaces). Empty when fallback to stderr was used.
func Path() string {
	if logFile == nil {
		return ""
	}
	return logFile.Name()
}

// PathOrError returns the path or the open-time error message so the
// status panel can show "logging to ~/.kenaz/harness.log" or "logger
// fallback: <reason>".
func PathOrError() string {
	if logFile != nil {
		return logFile.Name()
	}
	if openErr != nil {
		return openErr.Error()
	}
	return ""
}

// MarshalJSON helper used by callers when slog's default formatting
// is awkward (e.g. when logging a payload that's already a structured
// record). Returns "<json: ...>" on failure so callers can keep going
// without dropping the log line.
func JSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("<json:%v>", err)
	}
	return string(b)
}
