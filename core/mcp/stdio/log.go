package stdio

import (
	"context"
	"encoding/json"
	"log/slog"
)

// LogSink translates `notifications/message` frames into structured
// slog records. The MCP spec defines eight syslog-style levels;
// LogSink folds them onto slog's four with the mapping recorded in
// research §"notifications/message → slog":
//
//	debug                          → slog.Debug
//	info, notice                   → slog.Info
//	warning                        → slog.Warn
//	error, critical, alert,        → slog.Error
//	  emergency
//
// Unrecognized levels fall back to slog.Info — the harness does not
// drop log lines for an unknown level since the alternative is the
// developer losing diagnostic context from a misbehaving server.
type LogSink struct {
	logger *slog.Logger
}

// NewLogSink constructs a LogSink writing to the given slog.Logger.
// nil logger falls back to slog.Default so callers don't have to
// guard against the no-logger case.
func NewLogSink(logger *slog.Logger) *LogSink {
	if logger == nil {
		logger = slog.Default()
	}
	return &LogSink{logger: logger}
}

// Handle ingests one notifications/message payload. recipeID is the
// originating server's id (used as both an attr and a prefix in the
// slog message key so log filters can target a single server). The
// raw JSON is best-effort decoded; malformed payloads are dropped
// with a debug log so the reader loop never crashes.
func (s *LogSink) Handle(ctx context.Context, recipeID string, raw json.RawMessage) {
	if s == nil || s.logger == nil {
		return
	}
	if len(raw) == 0 {
		s.logger.DebugContext(ctx, "stdio.log.empty_payload", "mcp.recipe", recipeID)
		return
	}
	var params MessageParams
	if err := json.Unmarshal(raw, &params); err != nil {
		s.logger.DebugContext(ctx, "stdio.log.parse_error", "mcp.recipe", recipeID, "err", err.Error())
		return
	}

	level := mapLogLevel(params.Level)
	msgKey := "mcp." + recipeID + ".message"
	attrs := []slog.Attr{
		slog.String("mcp.recipe", recipeID),
		slog.String("mcp.level", params.Level),
	}
	if params.Logger != "" {
		attrs = append(attrs, slog.String("mcp.logger", params.Logger))
	}
	if len(params.Data) > 0 {
		attrs = append(attrs, slog.Any("mcp.data", json.RawMessage(params.Data)))
	}
	s.logger.LogAttrs(ctx, level, msgKey, attrs...)
}

// mapLogLevel translates the MCP spec's syslog-style level strings
// onto slog levels per research §"notifications/message → slog".
func mapLogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "info", "notice":
		return slog.LevelInfo
	case "warning":
		return slog.LevelWarn
	case "error", "critical", "alert", "emergency":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
