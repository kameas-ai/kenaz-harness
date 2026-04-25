// Package bedrock is the AWS Bedrock streaming adapter.
//
// Bedrock is unique among the providers in three ways:
//
//  1. Authentication is AWS-native: the cred bytes carried by the
//     credref pipeline are an AWS profile name, not an API key. The
//     adapter loads ~/.aws/credentials via the SDK's default chain
//     and never sees raw access keys.
//  2. The wire protocol is the universal Converse / ConverseStream
//     API — model-agnostic chat-style messages that work for Llama,
//     Claude, Mistral, Cohere, and any future Bedrock model without
//     per-model JSON shape work in this package.
//  3. The region is read from ProviderProfile.Region rather than a
//     constant; AWS Bedrock has different model availability per
//     region so the registry must thread the region through.
package bedrock

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/sigil-tech/kaneaz-harness/core/llm"
)

// Kind is the canonical provider kind ("bedrock").
const Kind = "bedrock"

// Option configures an Adapter at construction time.
type Option func(*Adapter)

// WithListModelsRegion overrides the region used for the
// bedrock.ListFoundationModels call. Most callers pass per-profile
// regions through Stream's prof argument and don't need this.
func WithListModelsRegion(region string) Option {
	return func(a *Adapter) {
		if region != "" {
			a.listModelsRegion = region
		}
	}
}

// WithHTTPClient overrides the HTTP client used by the bearer-auth
// path. The SDK path uses its own transport; tests typically swap
// this client to point at an httptest.Server.
func WithHTTPClient(c *http.Client) Option {
	return func(a *Adapter) {
		if c != nil {
			a.httpc = c
		}
	}
}

// Adapter implements llm.ProviderAdapter and llm.ModelLister against
// AWS Bedrock. It supports two auth flavours:
//
//   - aws_profile: cred bytes are an AWS profile name. The SDK loads
//     ~/.aws/credentials and signs requests with SigV4.
//   - keychain (long-lived API key): cred bytes are a Bedrock API
//     key. We bypass the SDK and call the REST endpoint directly
//     with `Authorization: Bearer <key>`, parsing the
//     vnd.amazon.eventstream binary protocol ourselves.
type Adapter struct {
	listModelsRegion string
	httpc            *http.Client
}

// New constructs an Adapter with default settings.
func New(opts ...Option) *Adapter {
	a := &Adapter{
		listModelsRegion: "us-east-1",
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Kind returns the canonical provider kind.
func (a *Adapter) Kind() string { return Kind }

// Capabilities reports a conservative descriptor — Bedrock supports
// streaming + tool calls universally via Converse, but per-model
// support for vision / reasoning is variable. We surface the safe
// minimum and let the registry's CapabilityGate refine over time.
func (a *Adapter) Capabilities(model string) llm.CapabilityDescriptor {
	return llm.CapabilityDescriptor{
		Provider: Kind,
		Model:    model,
		Supported: map[llm.Capability]bool{
			llm.CapStreaming:      true,
			llm.CapToolCalling:    true,
			llm.CapUsageReporting: true,
		},
	}
}

// resolveAWSConfig loads the AWS SDK config for the given profile
// name + region. cred is the profile name as bytes (per credref's
// RefAWSProfile resolution).
func resolveAWSConfig(ctx context.Context, profile, region string) (aws.Config, error) {
	loaders := []func(*awsconfig.LoadOptions) error{}
	if profile != "" && profile != "default" {
		loaders = append(loaders, awsconfig.WithSharedConfigProfile(profile))
	}
	if region != "" {
		loaders = append(loaders, awsconfig.WithRegion(region))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, loaders...)
	if err != nil {
		return aws.Config{}, fmt.Errorf("bedrock: load aws config: %w", err)
	}
	return cfg, nil
}

// Stream opens a Bedrock ConverseStream and returns a llm.Stream that
// pumps converted events to the caller.
//
// The auth path is selected by the profile's CredentialReference kind:
//
//   - "keychain"    long-lived Bedrock API key (bearer token); the
//                    cred bytes ARE the key. Routed through the REST
//                    endpoint with our own event-stream parser.
//   - "aws_profile" AWS shared-credentials profile name; the cred
//                    bytes ARE the profile name. Routed through the
//                    SDK with SigV4 signing.
func (a *Adapter) Stream(ctx context.Context, req llm.GenerationRequest, prof llm.ProviderProfile, cred []byte) (llm.Stream, error) {
	if len(cred) == 0 {
		return nil, &llm.ErrAuth{Message: "bedrock: empty credential"}
	}
	if strings.TrimSpace(prof.Region) == "" {
		return nil, &llm.ErrAuth{Message: "bedrock: profile region required"}
	}
	if prof.Cred.Kind == "keychain" {
		return a.streamWithBearer(ctx, req, prof, strings.TrimSpace(string(cred)))
	}
	profile := strings.TrimSpace(string(cred))

	cfg, err := resolveAWSConfig(ctx, profile, prof.Region)
	if err != nil {
		return nil, &llm.ErrAuth{Message: "bedrock: aws config: " + err.Error()}
	}
	client := bedrockruntime.NewFromConfig(cfg)

	bedrockMessages, system, err := toBedrockMessages(req.Messages)
	if err != nil {
		return nil, fmt.Errorf("bedrock: convert messages: %w", err)
	}

	in := &bedrockruntime.ConverseStreamInput{
		ModelId:  &prof.Model,
		Messages: bedrockMessages,
	}
	if len(system) > 0 {
		in.System = system
	}

	out, err := client.ConverseStream(ctx, in)
	if err != nil {
		return nil, classifyBedrockError(err)
	}

	stream := &converseStream{
		ctx:    ctx,
		out:    out,
		events: make(chan llm.StreamEvent, 64),
		done:   make(chan struct{}),
	}
	go stream.pump()
	return stream, nil
}

// ListModels implements llm.ModelLister.
//
// Returns the merged list of (a) inference profiles available in the
// caller's region, and (b) foundation models with on-demand throughput.
// Inference profiles come first because newer models (Llama 4, Claude
// 3.5+, Nova) can ONLY be invoked via an inference-profile id like
// "us.meta.llama4-maverick-17b-instruct-v1:0" — calling them by their
// bare foundation-model id returns a 400 ValidationException.
//
// The cred bytes carry the profile name OR a Bedrock API key — the
// caller routes via the AddProvider form's auth-method choice. We
// distinguish heuristically: a string starting with "ABSK" or
// containing dots / colons / forward-slashes is treated as a bearer
// token; otherwise it's a profile name.
func (a *Adapter) ListModels(ctx context.Context, cred []byte) ([]llm.ModelInfo, error) {
	if len(cred) == 0 {
		return nil, errors.New("bedrock: empty credential")
	}
	credStr := strings.TrimSpace(string(cred))
	if looksLikeBearerToken(credStr) {
		return a.listModelsWithBearer(ctx, credStr)
	}
	profile := credStr
	cfg, err := resolveAWSConfig(ctx, profile, a.listModelsRegion)
	if err != nil {
		return nil, err
	}
	client := bedrock.NewFromConfig(cfg)

	out := []llm.ModelInfo{}

	// 1. Inference profiles — what users actually call for newer models.
	if profiles, err := client.ListInferenceProfiles(ctx, &bedrock.ListInferenceProfilesInput{}); err == nil {
		for _, p := range profiles.InferenceProfileSummaries {
			id := stringOrEmpty(p.InferenceProfileId)
			if id == "" {
				continue
			}
			name := stringOrEmpty(p.InferenceProfileName)
			if name == "" {
				name = id
			}
			out = append(out, llm.ModelInfo{
				ID:          id,
				DisplayName: name + " (inference profile)",
			})
		}
	}

	// 2. Foundation models with on-demand throughput. Newer models
	// (Llama 4, Claude 3.5+, Nova) advertise throughput=PROVISIONED
	// only; their inference-profile ids surfaced above are how callers
	// reach them. We filter to ON_DEMAND-supporting entries so the
	// dropdown doesn't list IDs that will fail at stream time.
	resp, err := client.ListFoundationModels(ctx, &bedrock.ListFoundationModelsInput{
		ByOutputModality: bedrocktypes.ModelModalityText,
	})
	if err != nil && len(out) == 0 {
		return nil, fmt.Errorf("bedrock: list foundation models: %w", err)
	}
	if err == nil {
		for _, m := range resp.ModelSummaries {
			if !supportsStreaming(m) {
				continue
			}
			if !supportsOnDemand(m) {
				continue
			}
			id := stringOrEmpty(m.ModelId)
			if id == "" {
				continue
			}
			display := stringOrEmpty(m.ModelName)
			if display == "" {
				display = id
			}
			out = append(out, llm.ModelInfo{
				ID:          id,
				DisplayName: display,
			})
		}
	}
	return out, nil
}

// looksLikeBearerToken returns true when cred looks like a long-lived
// Bedrock API key rather than an AWS profile name. AWS-issued bearer
// tokens are base64-ish strings >= 40 chars, often with dots / colons.
// Profile names are short identifiers like "default" or "work".
func looksLikeBearerToken(s string) bool {
	if len(s) >= 40 {
		return true
	}
	if strings.ContainsAny(s, ".:/+") {
		return true
	}
	if strings.HasPrefix(s, "ABSK") {
		return true
	}
	return false
}

func supportsStreaming(m bedrocktypes.FoundationModelSummary) bool {
	if m.ResponseStreamingSupported == nil {
		return true
	}
	return *m.ResponseStreamingSupported
}

// supportsOnDemand reports whether the model can be invoked with
// on-demand throughput (vs only PROVISIONED, which requires reserved
// capacity, or only INFERENCE_PROFILE, which forces callers to use
// the inference-profile id surfaced separately).
func supportsOnDemand(m bedrocktypes.FoundationModelSummary) bool {
	if len(m.InferenceTypesSupported) == 0 {
		return true
	}
	for _, t := range m.InferenceTypesSupported {
		if t == bedrocktypes.InferenceTypeOnDemand {
			return true
		}
	}
	return false
}

func stringOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// toBedrockMessages converts the harness's GenerationRequest message
// slice into Bedrock's Converse-style messages. System messages are
// hoisted into a separate System block per the API contract.
func toBedrockMessages(msgs []llm.Message) ([]types.Message, []types.SystemContentBlock, error) {
	var bedrockMsgs []types.Message
	var system []types.SystemContentBlock
	for _, m := range msgs {
		text := flattenContent(m.Content)
		if text == "" {
			continue
		}
		switch m.Role {
		case llm.RoleSystem:
			system = append(system, &types.SystemContentBlockMemberText{Value: text})
		case llm.RoleUser:
			bedrockMsgs = append(bedrockMsgs, types.Message{
				Role:    types.ConversationRoleUser,
				Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: text}},
			})
		case llm.RoleAssistant:
			bedrockMsgs = append(bedrockMsgs, types.Message{
				Role:    types.ConversationRoleAssistant,
				Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: text}},
			})
		case llm.RoleTool:
			// Tool results aren't yet wired through the harness — skip
			// silently rather than producing a malformed Converse turn.
			continue
		default:
			// Unknown role — skip rather than fail the stream open.
			continue
		}
	}
	if len(bedrockMsgs) == 0 {
		return nil, nil, errors.New("bedrock: no user/assistant messages in request")
	}
	return bedrockMsgs, system, nil
}

// flattenContent collapses a multi-part content slice into a single
// string. Bedrock Converse supports image parts, but the harness's
// chat surface today only emits text — extending to multimodal parts
// is a future enhancement.
func flattenContent(parts []llm.ContentPart) string {
	var b strings.Builder
	for _, p := range parts {
		if p.Type == "text" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// converseStream wraps a Bedrock ConverseStream response so it
// satisfies llm.Stream. Events are translated on the fly so the
// frontend never has to learn the AWS event-stream binary protocol.
type converseStream struct {
	ctx       context.Context
	out       *bedrockruntime.ConverseStreamOutput
	events    chan llm.StreamEvent
	done      chan struct{}
	finalResp llm.Response
	finalErr  error
	cancelled bool
}

// pump reads from the underlying Bedrock event stream, converts each
// frame to the harness's StreamEvent vocabulary, and closes events
// when the stream terminates.
func (s *converseStream) pump() {
	defer close(s.events)
	defer close(s.done)
	defer s.out.GetStream().Close()

	for ev := range s.out.GetStream().Events() {
		switch v := ev.(type) {
		case *types.ConverseStreamOutputMemberContentBlockDelta:
			if v.Value.Delta != nil {
				if td, ok := v.Value.Delta.(*types.ContentBlockDeltaMemberText); ok {
					s.events <- llm.StreamEvent{
						Kind: llm.StreamText,
						Text: td.Value,
					}
				}
			}
		case *types.ConverseStreamOutputMemberMessageStop:
			finish := string(v.Value.StopReason)
			s.finalResp.FinishReason = finish
			s.events <- llm.StreamEvent{Kind: llm.StreamFinish, Finish: finish}
		case *types.ConverseStreamOutputMemberMetadata:
			if v.Value.Usage != nil {
				var usage llm.Usage
				if v.Value.Usage.InputTokens != nil {
					usage.InputTokens = int(*v.Value.Usage.InputTokens)
				}
				if v.Value.Usage.OutputTokens != nil {
					usage.OutputTokens = int(*v.Value.Usage.OutputTokens)
				}
				s.finalResp.Usage = usage
				usageCopy := usage
				s.events <- llm.StreamEvent{Kind: llm.StreamUsage, Usage: &usageCopy}
			}
		default:
			// content_block_start, content_block_stop, message_start,
			// tool_use deltas — not surfaced to the UI yet. Fall
			// through silently.
		}
	}
	if err := s.out.GetStream().Err(); err != nil {
		// io.EOF from the SDK after a clean close is normal.
		if !errors.Is(err, io.EOF) {
			s.finalErr = err
			s.events <- llm.StreamEvent{Kind: llm.StreamError, Err: err.Error()}
		}
	}
}

func (s *converseStream) Events() <-chan llm.StreamEvent { return s.events }

func (s *converseStream) Cancel() error {
	if s.cancelled {
		return nil
	}
	s.cancelled = true
	if s.out != nil {
		_ = s.out.GetStream().Close()
	}
	return nil
}

func (s *converseStream) Final() (llm.Response, error) {
	<-s.done
	if s.cancelled {
		return s.finalResp, &llm.ErrCancelled{Reason: "cancelled"}
	}
	return s.finalResp, s.finalErr
}

// classifyBedrockError maps AWS SDK errors into the harness's typed
// error taxonomy so retry middleware can act without per-provider
// knowledge.
func classifyBedrockError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "validationexception"):
		return &llm.ErrInvalidRequest{Status: 400, Message: msg}
	case strings.Contains(lower, "accessdenied"),
		strings.Contains(lower, "unrecognizedclientexception"),
		strings.Contains(lower, "expiredtoken"),
		strings.Contains(lower, "invalidsignature"):
		return &llm.ErrAuth{Status: 403, Message: msg}
	case strings.Contains(lower, "throttling"),
		strings.Contains(lower, "ratelimit"):
		return &llm.ErrTransient{Status: 429, Message: msg, Cause: err}
	case strings.Contains(lower, "modelnotreadyexception"),
		strings.Contains(lower, "serviceunavailable"),
		strings.Contains(lower, "internalfailure"):
		return &llm.ErrTransient{Status: 503, Message: msg, Cause: err}
	default:
		return &llm.ErrTransient{Message: msg, Cause: err}
	}
}

