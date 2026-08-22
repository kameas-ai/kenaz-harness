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
	"testing"
	"time"
)

const (
	defaultDir  = ".kenaz"
	defaultFile = "harness.log"

	// maxLogBytes is the size at which the live log is rotated to
	// harness.log.1 (one generation, previous backup discarded).
	//
	// There was no cap at all until 2026-08-21. The developer log on the
	// machine this was found on had reached 669 MB and was still growing
	// — roughly 16 MB per test session, because every `go test` that
	// opened a real sqlite database logged into it (see initLogger's
	// go-test branch below). Unbounded growth and the missing sandbox
	// were two separate defects that happened to compound.
	maxLogBytes = 32 << 20
)

var (
	once          sync.Once
	loggerInt     *slog.Logger
	fileHandler   slog.Handler
	logFile       *os.File
	openErr       error
	replaceMu     sync.Mutex
	configuredDir string
)

// Configure sets the directory the log file is written to. It MUST be called
// before the first L()/FileHandler() call (the file is opened lazily on first
// use); calls after the logger has opened are ignored. When left unset, the
// logger falls back to ~/.kenaz for backward compatibility.
//
// main calls this with the per-env log directory (paths.LogDir,
// ~/.kenaz/harness/<env>/logs) so dev/stage/prod logs never interleave.
func Configure(dir string) {
	replaceMu.Lock()
	defer replaceMu.Unlock()
	configuredDir = dir
}

// L returns the process-global slog.Logger writing JSON lines to
// ~/.kenaz/harness.log. On the first call the file is opened (parent
// dir created with mode 0700) and a JSON-line handler is wired. If
// the file can't be opened the logger falls back to stderr. Once
// telemetry.Init has run with InstallSlogBridge=true, the returned
// logger is fronted by a fan-out handler that ALSO emits each record
// through the OTel logger pipeline.
func L() *slog.Logger {
	once.Do(initLogger)
	replaceMu.Lock()
	defer replaceMu.Unlock()
	return loggerInt
}

func initLogger() {
	dir := configuredDir
	switch {
	case dir != "":
		// main calls Configure(paths.LogDir), so a real app run lands in
		// the per-env directory.
	case testing.Testing():
		// Under `go test` NOTHING calls Configure, so the home fallback
		// below used to apply: every test that opened a real sqlite
		// database appended to the developer's live ~/.kenaz/harness.log,
		// regardless of t.TempDir sandboxing elsewhere. Confirmed by
		// finding storage.opened lines in it whose paths were
		// /var/folders/.../TestSearch_* — sandboxed databases logging to
		// an unsandboxed sink. check-tests-are-hermetic.sh explicitly
		// CARVED OUT this write, so it was known and unowned rather than
		// undetected.
		//
		// A test that wants to read its own log output can still call
		// Configure(t.TempDir()) before the first L(); this branch only
		// changes where the DEFAULT goes.
		d, err := os.MkdirTemp("", "kenaz-logging-test-")
		if err != nil {
			fallback("test temp dir: " + err.Error())
			return
		}
		dir = d
	default:
		home, err := os.UserHomeDir()
		if err != nil {
			fallback("home dir: " + err.Error())
			return
		}
		dir = filepath.Join(home, defaultDir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fallback("mkdir " + dir + ": " + err.Error())
		return
	}
	path := filepath.Join(dir, defaultFile)
	rotateIfLarge(path)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		fallback("open " + path + ": " + err.Error())
		return
	}
	logFile = f
	// Tee to stderr in dev so wails-dev console shows logs too — the
	// MultiWriter is cheap and the volume is low.
	w := io.MultiWriter(os.Stderr, f)
	fileHandler = slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:     slog.LevelDebug,
		AddSource: false,
	})
	loggerInt = slog.New(fileHandler)
	loggerInt.Info("logger.opened",
		"path", path,
		"pid", os.Getpid(),
		"opened_at", time.Now().UTC().Format(time.RFC3339Nano),
	)
}

func fallback(reason string) {
	openErr = fmt.Errorf("logging fallback: %s", reason)
	fileHandler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	loggerInt = slog.New(fileHandler)
	loggerInt.Warn("logger.fallback", "reason", reason)
}

// FileHandler exposes the file-backed slog.Handler so the telemetry
// package can wrap it in a fan-out bridge (file + OTel) without
// depending on the unexported state. Returns nil if L() has not yet
// been called.
func FileHandler() slog.Handler {
	once.Do(initLogger)
	replaceMu.Lock()
	defer replaceMu.Unlock()
	return fileHandler
}

// Handler returns the *current* active slog.Handler (including any
// installed bridges). Unlike FileHandler, this returns whatever is
// currently wired — including test overrides. Callers that want to
// wrap the current handler (e.g. to tee into a ring buffer while
// preserving the active chain) should use this instead of FileHandler.
func Handler() slog.Handler {
	once.Do(initLogger)
	replaceMu.Lock()
	defer replaceMu.Unlock()
	if loggerInt == nil {
		return fileHandler
	}
	return loggerInt.Handler()
}

// Replace swaps the process-global slog.Logger's handler. The
// telemetry package uses this to install its slog→OTel bridge once
// the SDK providers are constructed. The bridge SHOULD wrap the
// handler returned by FileHandler so file-logging behaviour is
// preserved. Idempotent: a second call swaps to the new handler.
func Replace(h slog.Handler) {
	if h == nil {
		return
	}
	once.Do(initLogger)
	replaceMu.Lock()
	defer replaceMu.Unlock()
	loggerInt = slog.New(h)
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

// rotateIfLarge renames path to path+".1" when it has grown past
// maxLogBytes, discarding any previous backup. Called once, at open.
//
// One generation on purpose: the point is a bounded ceiling, not an
// archive. Checking only at open means a single very long-running
// process can still exceed the cap between restarts — acceptable for a
// desktop app, and cheaper than a size check on every write. Any error
// is ignored: failing to rotate must never prevent logging.
func rotateIfLarge(path string) {
	fi, err := os.Stat(path)
	if err != nil || fi.Size() < maxLogBytes {
		return
	}
	_ = os.Remove(path + ".1")
	_ = os.Rename(path, path+".1")
}
