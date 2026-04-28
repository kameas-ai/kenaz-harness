package artifacts

import (
	"context"
	"log/slog"
	"time"

	coreart "github.com/sigil-tech/kaneaz-harness/core/artifacts"
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

// OnPostLLMMessage runs the code-block detector against a freshly
// persisted assistant message. The chassis registers this as a
// kernel HookManager post-LLM hook listener (chat-migration WP-D).
//
// Signature mirrors agentgraph.HookManager's post-hook callback shape:
// (ctx, sessionID, messageID, text). The chassis adapts between
// HookManager.RegisterPostHook and this method.
func (s *sink) OnPostLLMMessage(ctx context.Context, sessionID, messageID, text string) {
	_ = s.OnAssistantMessage(ctx, sessionID, messageID, text)
}

// OnPostToolMessage runs the code-block detector against a tool result
// payload. The chassis registers this as the kernel HookManager
// HookPostTool tool-shaped listener (mission tool-dispatch-node) so
// tool-output capture matches the deleted toolloop's PostToolUseListener
// behaviour. The toolName is folded into the candidate's source so
// captured artifacts surface "tool: <name>" in the artifacts library.
//
// Signature mirrors agentgraph.HookManager's tool post-hook callback
// shape: (ctx, sessionID, toolName, toolArgs, toolResult, duration).
func (s *sink) OnPostToolMessage(ctx context.Context, sessionID, toolName, toolArgs, toolResult string, _ time.Duration) {
	if s == nil || s.mgr == nil || toolResult == "" {
		return
	}
	cfg := s.cfg()
	if !cfg.AutoCaptureCodeBlocks {
		return
	}
	// Use the tool name as the synthetic message id so callers correlate
	// captured artifacts with the originating tool dispatch. The detector
	// keys candidates by (messageID, byteOffset) so a stable suffix is
	// enough to keep the dedup invariant.
	syntheticID := "tool:" + toolName
	candidates := coreart.DetectCodeBlocks(syntheticID, toolResult, coreart.CodeBlockDetectorConfig{
		MinLines: cfg.CodeBlockMinLines,
		MinBytes: cfg.CodeBlockMinBytes,
	})
	if len(candidates) == 0 {
		return
	}
	captured, err := s.mgr.Capture(ctx, candidates, sessionID)
	if err != nil {
		s.log.Warn("artifacts.tool_capture.failed",
			"session_id", sessionID,
			"tool", toolName,
			"err", err.Error())
		return
	}
	if len(captured) > 0 {
		s.log.Info("artifacts.captured",
			"count", len(captured),
			"session_id", sessionID,
			"tool", toolName,
			"source", coreart.SourceToolOutput,
		)
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
