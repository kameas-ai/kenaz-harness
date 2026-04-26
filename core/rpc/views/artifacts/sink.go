package artifacts

import (
	"context"
	"log/slog"

	coreart "github.com/sigil-tech/kaneaz-harness/core/artifacts"
	"github.com/sigil-tech/kaneaz-harness/core/toolloop"
)

// CaptureManager is the slice of *coreart.Manager the sink consumes.
// We accept an interface (not the concrete pointer) so tests can
// substitute a recording fake without spinning up a MediaStore.
type CaptureManager interface {
	Capture(ctx context.Context, candidates []coreart.CaptureCandidate, sessionID string) ([]coreart.Artifact, error)
}

// sink is the concrete ArtifactSink + post-tool-use listener. The
// same value satisfies both surfaces so a single registration on the
// llm view's Config.Artifacts AND a single hookRunner.RegisterPostListener
// call wire the full capture pipeline.
type sink struct {
	mgr CaptureManager
	cfg CaptureConfigReader
	log *slog.Logger
}

// NewSink constructs a sink. mgr is required; cfgFn returns the live
// CaptureConfig (settings → detector tunables); log defaults to
// slog.Default when nil.
func NewSink(mgr CaptureManager, cfgFn CaptureConfigReader, log *slog.Logger) ArtifactSink {
	if mgr == nil {
		panic("rpc/artifacts: NewSink: nil manager")
	}
	if cfgFn == nil {
		cfgFn = func() coreart.CaptureConfig { return coreart.DefaultCaptureConfig() }
	}
	if log == nil {
		log = slog.Default()
	}
	return &sink{mgr: mgr, cfg: cfgFn, log: log}
}

// OnAssistantMessage runs the code-block detector against the
// supplied assistant message text and forwards every candidate to
// the artifact manager's Capture path. Disabled-cfg short-circuits
// before the scanner runs so the chat hot path stays clean.
func (s *sink) OnAssistantMessage(ctx context.Context, sessionID, messageID, text string) error {
	if s == nil || s.mgr == nil {
		return nil
	}
	cfg := s.cfg()
	if !cfg.AutoCaptureCodeBlocks {
		return nil
	}
	candidates := coreart.DetectCodeBlocks(messageID, text, coreart.CodeBlockDetectorConfig{
		MinLines: cfg.CodeBlockMinLines,
		MinBytes: cfg.CodeBlockMinBytes,
	})
	if len(candidates) == 0 {
		return nil
	}
	captured, err := s.mgr.Capture(ctx, candidates, sessionID)
	if err != nil {
		return err
	}
	if len(captured) > 0 {
		s.log.Info("artifacts.captured",
			"count", len(captured),
			"session_id", sessionID,
			"message_id", messageID,
			"source", coreart.SourceCodeBlock,
		)
	}
	return nil
}

// OnPostToolUse runs the tool-output detector against a post-tool-use
// event and forwards every candidate to the artifact manager. Wired
// via toolloop.HookRunner.RegisterPostListener at chassis startup.
func (s *sink) OnPostToolUse(ev toolloop.PostToolUseEvent) {
	if s == nil || s.mgr == nil {
		return
	}
	cfg := s.cfg()
	if !cfg.AutoCaptureToolOutputs {
		return
	}
	if ev.SessionID == "" {
		return
	}
	candidates := coreart.DetectToolOutput(
		"", // PostToolUseEvent has no associated assistant message id;
		// the tool-output capture's MessageID falls back to empty and
		// the SourceRef.ToolCallID below carries the back-link instead.
		coreart.PostToolUseEventLike{
			Tool:       ev.Tool,
			Server:     ev.Server,
			Args:       ev.Args,
			Result:     ev.Result,
			ToolCallID: ev.ToolCallID,
		},
		coreart.ToolOutputDetectorConfig{Enabled: true},
	)
	if len(candidates) == 0 {
		return
	}
	captured, err := s.mgr.Capture(context.Background(), candidates, ev.SessionID)
	if err != nil {
		s.log.Warn("artifacts.tool_capture.failed",
			"session_id", ev.SessionID,
			"tool_call_id", ev.ToolCallID,
			"err", err.Error(),
		)
		return
	}
	if len(captured) > 0 {
		s.log.Info("artifacts.captured",
			"count", len(captured),
			"session_id", ev.SessionID,
			"tool_call_id", ev.ToolCallID,
			"source", coreart.SourceToolOutput,
		)
	}
}

// PostListener returns a toolloop.PostToolUseListener that delegates
// to OnPostToolUse. The chassis passes this to
// HookRunner.RegisterPostListener at boot. Splitting it out from the
// method body keeps the registration signature explicit and exposes
// a stable *sink test seam.
func (s *sink) PostListener() toolloop.PostToolUseListener {
	return func(ev toolloop.PostToolUseEvent) {
		s.OnPostToolUse(ev)
	}
}

// Sink is the concrete sink type — exported so the chassis wiring in
// core/rpc/api.go can reach PostListener without a type assertion.
type Sink struct {
	*sink
}

// NewSinkConcrete is the chassis-facing ctor that returns *Sink so
// the caller can call PostListener directly. The interface ctor
// NewSink wraps this for callers that only need ArtifactSink.
func NewSinkConcrete(mgr CaptureManager, cfgFn CaptureConfigReader, log *slog.Logger) *Sink {
	if mgr == nil {
		panic("rpc/artifacts: NewSinkConcrete: nil manager")
	}
	if cfgFn == nil {
		cfgFn = func() coreart.CaptureConfig { return coreart.DefaultCaptureConfig() }
	}
	if log == nil {
		log = slog.Default()
	}
	return &Sink{sink: &sink{mgr: mgr, cfg: cfgFn, log: log}}
}
