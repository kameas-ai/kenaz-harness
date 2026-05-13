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
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrock"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrock/types"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/sigil-tech/kaneaz-harness/core/llm"
	"github.com/sigil-tech/kaneaz-harness/core/llm/capabilities"
	"github.com/sigil-tech/kaneaz-harness/core/llm/httpx"
	"github.com/sigil-tech/kaneaz-harness/core/llm/structured"
)

// imageFormatFromMediaType maps an IANA mime-type to the Bedrock
// Converse image format enum value. Unknown / unsupported types fall
// through to "" so the caller can detect and skip the block.
func imageFormatFromMediaType(mt string) string {
	switch strings.ToLower(strings.TrimSpace(mt)) {
	case "image/png":
		return "png"
	case "image/jpeg", "image/jpg":
		return "jpeg"
	case "image/gif":
		return "gif"
	case "image/webp":
		return "webp"
	}
	return ""
}

// documentFormatFromMediaType maps an IANA mime-type to the Bedrock
// Converse document format enum value. Returns "" for unsupported types.
func documentFormatFromMediaType(mt string) string {
	switch strings.ToLower(strings.TrimSpace(mt)) {
	case "application/pdf":
		return "pdf"
	case "text/csv":
		return "csv"
	case "text/html":
		return "html"
	case "text/plain":
		return "txt"
	case "text/markdown":
		return "md"
	}
	return ""
}

// defaultDocumentName is the placeholder Bedrock document name used
// when the caller hasn't supplied one. Converse rejects requests with
// a missing or empty document name (verified against the Converse
// API docs — the field is annotated "This member is required.").
const defaultDocumentName = "document"

// Kind is the canonical provider kind ("bedrock").
const Kind = "bedrock"

// Option configures an Adapter at construction time.
type Option func(*Adapter)

// WithCatalog overrides the capability catalog. Tests inject a fixture
// catalog; production uses the embedded default (loaded at construction).
func WithCatalog(cat *capabilities.Catalog) Option {
	return func(a *Adapter) {
		if cat != nil {
			a.cat = cat
		}
	}
}

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
	cat              *capabilities.Catalog
}

// New constructs an Adapter with default settings.
//
// The default *http.Client is built with the shared keepalive
// transport from core/llm/httpx so long-lived bearer-auth Converse
// streams survive edge-proxy idle timeouts. WithHTTPClient still
// wins for tests/custom gateways.
func New(opts ...Option) *Adapter {
	a := &Adapter{
		listModelsRegion: "us-east-1",
		httpc:            &http.Client{Transport: httpx.DefaultTransport()}, // no Timeout — context drives lifetime
	}
	if cat, err := capabilities.LoadDefault(); err == nil {
		a.cat = cat
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Compile-time assertion: Adapter satisfies the optional StructuredOutputAdapter
// interface (structured-output-and-grammar-01KX5R8A WP03).
var _ llm.StructuredOutputAdapter = (*Adapter)(nil)

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

// ApplyResponseFormat implements llm.StructuredOutputAdapter for the Bedrock
// adapter. Unlike the OpenAI/Anthropic adapters this method does NOT mutate
// wireBody (Bedrock uses typed SDK structs, not a raw map). The real work
// happens in applyResponseFormatToConverseInput which is called from Stream().
//
// wireBody is accepted to satisfy the interface but is always ignored.
// (structured-output-and-grammar-01KX5R8A WP03d)
func (a *Adapter) ApplyResponseFormat(req *llm.GenerationRequest, wireBody map[string]any) error {
	if req == nil || req.ResponseFormat == nil {
		return nil
	}
	switch req.ResponseFormat.Mode {
	case "json":
		// json mode is handled by appending a system prompt in Stream(). No-op here.
		return nil
	case "json_schema":
		// Validation only: ensure the schema is well-formed JSON before the wire
		// call. Actual injection is done in applyResponseFormatToConverseInput.
		if len(req.ResponseFormat.Schema) > 0 {
			if !json.Valid(req.ResponseFormat.Schema) {
				return &llm.ErrInvalidRequest{Message: "bedrock: response_format.schema is not valid JSON"}
			}
		}
		return nil
	case "grammar":
		// GBNF grammar constraints require token-level support not available in
		// the Bedrock Converse API. Cloud providers always return ErrUnsupportedFormat.
		return &llm.ErrUnsupportedFormat{
			Provider: Kind,
			Mode:     "grammar",
		}
	default:
		return nil
	}
}

// applyResponseFormatToConverseInput mutates in to carry structured-output
// instructions derived from req.ResponseFormat. Called from Stream() after
// the message conversion step. For Bedrock, the two approaches are:
//
//   - Mode="json"       → append a system text block instructing JSON-only output
//   - Mode="json_schema" → inject a synthetic tool ("_structured_output") carrying
//     the caller's schema as the tool's input_schema; set forced tool_choice so
//     the model MUST call the tool and return structured JSON (mirrors the
//     Anthropic tool-call workaround but via Converse ToolConfiguration).
//
// Grammar mode returns ErrUnsupportedFormat — callers should gate on CapGrammar
// before reaching here.
func applyResponseFormatToConverseInput(in *bedrockruntime.ConverseStreamInput, rf *llm.ResponseFormat) error {
	if rf == nil {
		return nil
	}
	switch rf.Mode {
	case "json":
		// Append system instruction for plain JSON output.
		in.System = append(in.System, &types.SystemContentBlockMemberText{
			Value: "Respond with valid JSON only. Do not include any explanation, markdown fences, or prose — output raw JSON.",
		})
		return nil

	case "json_schema":
		// Build an injected schema. InjectAdditionalProperties prevents unknown
		// keys from slipping past later validation.
		schema := rf.Schema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		injected, err := structured.InjectAdditionalProperties(schema)
		if err != nil {
			return &llm.ErrInvalidRequest{Message: "bedrock: inject additionalProperties: " + err.Error()}
		}

		// Unmarshal to any so the document marshaler reproduces it faithfully.
		var schemaAny any
		if err := json.Unmarshal(injected, &schemaAny); err != nil {
			return &llm.ErrInvalidRequest{Message: "bedrock: response_format schema unmarshal: " + err.Error()}
		}

		syntheticTool := &types.ToolMemberToolSpec{
			Value: types.ToolSpecification{
				Name:        aws.String("_structured_output"),
				Description: aws.String("Return a JSON object that matches the required schema. Always call this tool."),
				InputSchema: &types.ToolInputSchemaMemberJson{
					Value: document.NewLazyDocument(schemaAny),
				},
			},
		}

		// Force the model to call our synthetic tool. Converse uses ToolChoice
		// (a union type); ToolChoiceMemberTool pins to a specific tool name.
		forcedChoice := &types.ToolChoiceMemberTool{
			Value: types.SpecificToolChoice{
				Name: aws.String("_structured_output"),
			},
		}

		// If there's an existing ToolConfig (caller-supplied tools), merge our
		// synthetic tool in and override the tool choice. If not, build fresh.
		if in.ToolConfig != nil {
			in.ToolConfig.Tools = append([]types.Tool{syntheticTool}, in.ToolConfig.Tools...)
			in.ToolConfig.ToolChoice = forcedChoice
		} else {
			in.ToolConfig = &types.ToolConfiguration{
				Tools:      []types.Tool{syntheticTool},
				ToolChoice: forcedChoice,
			}
		}
		return nil

	case "grammar":
		return &llm.ErrUnsupportedFormat{
			Provider: Kind,
			Mode:     "grammar",
		}
	default:
		return nil
	}
}

// applyJSONModeToConverseInput translates a JSONModeSpec into the Bedrock
// Converse wire shape. Mirrors applyResponseFormatToConverseInput:
//
//   - Schema absent  → append JSON-only instruction to system
//   - Schema present → inject synthetic tool + forced tool_choice
//
// (multimodal-io-extended-01KQ8TD2 WP03)
func applyJSONModeToConverseInput(in *bedrockruntime.ConverseStreamInput, jm *llm.JSONModeSpec) error {
	if jm == nil || !jm.Enabled {
		return nil
	}
	if len(jm.Schema) == 0 {
		in.System = append(in.System, &types.SystemContentBlockMemberText{
			Value: "Respond with valid JSON only. Do not include any explanation, markdown fences, or prose — output raw JSON.",
		})
		return nil
	}
	// Schema present: use synthetic tool injection (same as json_schema path).
	schema := jm.Schema
	injected, err := structured.InjectAdditionalProperties(schema)
	if err != nil {
		return &llm.ErrInvalidRequest{Message: "bedrock: json_mode inject additionalProperties: " + err.Error()}
	}
	var schemaAny any
	if err := json.Unmarshal(injected, &schemaAny); err != nil {
		return &llm.ErrInvalidRequest{Message: "bedrock: json_mode schema unmarshal: " + err.Error()}
	}
	toolName := "_structured_output"
	if jm.Name != "" {
		toolName = "_" + jm.Name
	}
	syntheticTool := &types.ToolMemberToolSpec{
		Value: types.ToolSpecification{
			Name:        aws.String(toolName),
			Description: aws.String("Return a JSON object that matches the required schema. Always call this tool."),
			InputSchema: &types.ToolInputSchemaMemberJson{
				Value: document.NewLazyDocument(schemaAny),
			},
		},
	}
	forcedChoice := &types.ToolChoiceMemberTool{
		Value: types.SpecificToolChoice{Name: aws.String(toolName)},
	}
	if in.ToolConfig != nil {
		in.ToolConfig.Tools = append([]types.Tool{syntheticTool}, in.ToolConfig.Tools...)
		in.ToolConfig.ToolChoice = forcedChoice
	} else {
		in.ToolConfig = &types.ToolConfiguration{
			Tools:      []types.Tool{syntheticTool},
			ToolChoice: forcedChoice,
		}
	}
	return nil
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
	// Tool serialization — Converse wraps each spec in a
	// toolConfig.tools[].toolSpec envelope. The tool's input schema is
	// carried as a smithy `document.Interface` whose JSON encoding is
	// the raw JSON Schema. ToolSpec.InputSchema is already JSON Schema
	// bytes; we unmarshal into a generic map so the document marshaler
	// emits it as-is.
	// Reference: https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_Converse.html
	if len(req.Tools) > 0 {
		toolCfg, err := buildToolConfig(req.Tools)
		if err != nil {
			return nil, &llm.ErrInvalidRequest{Message: err.Error()}
		}
		in.ToolConfig = toolCfg
	}

	// Structured-output injection (structured-output-and-grammar-01KX5R8A WP03d).
	// applyResponseFormatToConverseInput modifies in.System and/or in.ToolConfig
	// in place. It must run after the caller-supplied ToolConfig (if any) is set
	// so the synthetic _structured_output tool can be prepended safely.
	if req.ResponseFormat != nil {
		if err := applyResponseFormatToConverseInput(in, req.ResponseFormat); err != nil {
			return nil, err
		}
	}

	// JSONMode injection (multimodal-io-extended-01KQ8TD2 WP03).
	// When only JSONMode (not ResponseFormat) is set, translate it to the
	// Converse wire shape via the same path as ResponseFormat.
	if req.JSONMode != nil && req.JSONMode.Enabled && req.ResponseFormat == nil {
		if err := applyJSONModeToConverseInput(in, req.JSONMode); err != nil {
			return nil, err
		}
	}

	out, err := client.ConverseStream(ctx, in)
	if err != nil {
		return nil, classifyBedrockError(err)
	}

	// Determine the synthetic tool name for WP04 normalization.
	syntheticName := bedrockSyntheticToolName(req)

	stream := &converseStream{
		ctx:               ctx,
		out:               out,
		events:            make(chan llm.StreamEvent, 64),
		done:              make(chan struct{}),
		syntheticToolName: syntheticName,
	}
	go stream.pump()
	return stream, nil
}

// bedrockSyntheticToolName derives the synthetic tool name injected for a
// structured-output request. Returns "" when no synthetic tool was injected.
// (multimodal-io-extended-01KQ8TD2 WP04)
func bedrockSyntheticToolName(req llm.GenerationRequest) string {
	if req.ResponseFormat != nil && req.ResponseFormat.Mode == "json_schema" {
		return "_structured_output"
	}
	if req.JSONMode != nil && req.JSONMode.Enabled && len(req.JSONMode.Schema) > 0 {
		if req.JSONMode.Name != "" {
			return "_" + req.JSONMode.Name
		}
		return "_structured_output"
	}
	return ""
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
			cw := 0
			if a.cat != nil {
				cw = a.cat.ContextWindow(Kind, id)
			}
			out = append(out, llm.ModelInfo{
				ID:            id,
				DisplayName:   name + " (inference profile)",
				ContextWindow: cw,
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
			cw := 0
			if a.cat != nil {
				cw = a.cat.ContextWindow(Kind, id)
			}
			out = append(out, llm.ModelInfo{
				ID:            id,
				DisplayName:   display,
				ContextWindow: cw,
			})
		}
	}
	return out, nil
}

// TestKey implements llm.KeyTester for the bearer-token credential path only.
// Bedrock has two auth shapes: (a) AWS profile name — rotated via
// ~/.aws/credentials outside the key-rotation UI, and (b) bearer token — a
// keychain-backed credential this method validates via ListModels.
//
// If cred does not look like a bearer token, TestKey returns *ErrInvalidRequest
// with a helpful message so the rotation UI can surface "use aws configure."
// The rejection happens before any wire call (step 1 in plan §3).
//
// provider-keychain-rotation-01KQ8TD9 WP02.
func (a *Adapter) TestKey(ctx context.Context, cred []byte) error {
	if len(cred) == 0 {
		return &llm.ErrAuth{Status: 0, Message: "bedrock: empty credential"}
	}
	if !looksLikeBearerToken(string(cred)) {
		return &llm.ErrInvalidRequest{
			Status:  0,
			Message: "bedrock: TestKey requires a bearer token; AWS profile rotation is not supported via this UI — rotate via `aws configure` instead",
		}
	}
	tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	models, err := a.ListModels(tctx, cred)
	if err != nil {
		return err
	}
	if len(models) == 0 {
		return &llm.ErrInvalidRequest{Status: 200, Message: "bedrock: key accepted but returned empty model list"}
	}
	return nil
}

// Compile-time assertion: Adapter implements llm.KeyTester.
var _ llm.KeyTester = (*Adapter)(nil)

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
// hoisted into a separate System block per the API contract; image and
// document blocks land as ContentBlockMemberImage / ContentBlockMemberDocument
// per https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_ContentBlock.html.
func toBedrockMessages(msgs []llm.Message) ([]types.Message, []types.SystemContentBlock, error) {
	var bedrockMsgs []types.Message
	var system []types.SystemContentBlock
	for _, m := range msgs {
		switch m.Role {
		case llm.RoleSystem:
			text := m.Text()
			if text == "" {
				continue
			}
			system = append(system, &types.SystemContentBlockMemberText{Value: text})
		case llm.RoleUser, llm.RoleAssistant:
			blocks := toBedrockContentBlocks(m.Content)
			if len(blocks) == 0 {
				continue
			}
			role := types.ConversationRoleUser
			if m.Role == llm.RoleAssistant {
				role = types.ConversationRoleAssistant
			}
			bedrockMsgs = append(bedrockMsgs, types.Message{
				Role:    role,
				Content: blocks,
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

// toBedrockContentBlocks projects connector ContentBlocks onto the
// AWS SDK's ContentBlock union. Unknown / unsupported types are
// dropped silently — capability gating in WP03 surfaces the error to
// the user before we get here.
//
// The SDK's ImageSourceMemberBytes / DocumentSourceMemberBytes accept
// raw `[]byte`; the SDK encodes them on the wire (no caller-side base64
// re-encoding). We base64-decode the connector's MediaSource.Data here.
func toBedrockContentBlocks(parts []llm.ContentBlock) []types.ContentBlock {
	out := make([]types.ContentBlock, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case "", "text":
			if p.Text == "" {
				continue
			}
			out = append(out, &types.ContentBlockMemberText{Value: p.Text})
		case "image":
			if p.Source == nil {
				continue
			}
			format := imageFormatFromMediaType(p.Source.MediaType)
			if format == "" {
				continue
			}
			data, err := base64.StdEncoding.DecodeString(p.Source.Data)
			if err != nil {
				continue
			}
			out = append(out, &types.ContentBlockMemberImage{
				Value: types.ImageBlock{
					Format: types.ImageFormat(format),
					Source: &types.ImageSourceMemberBytes{Value: data},
				},
			})
		case "document":
			if p.Source == nil {
				continue
			}
			format := documentFormatFromMediaType(p.Source.MediaType)
			if format == "" {
				continue
			}
			data, err := base64.StdEncoding.DecodeString(p.Source.Data)
			if err != nil {
				continue
			}
			name := p.Source.OriginalName
			if name == "" {
				name = defaultDocumentName
			}
			out = append(out, &types.ContentBlockMemberDocument{
				Value: types.DocumentBlock{
					Name:   aws.String(name),
					Format: types.DocumentFormat(format),
					Source: &types.DocumentSourceMemberBytes{Value: data},
				},
			})
		}
	}
	return out
}

// buildToolConfig converts the harness's ToolSpec slice into the
// Converse SDK's nested ToolConfiguration shape. Each spec becomes
// one ToolMemberToolSpec; the JSON Schema is wrapped in a
// ToolInputSchemaMemberJson document.
func buildToolConfig(specs []llm.ToolSpec) (*types.ToolConfiguration, error) {
	tools := make([]types.Tool, 0, len(specs))
	for _, t := range specs {
		var schema any
		if len(t.InputSchema) > 0 {
			if err := json.Unmarshal(t.InputSchema, &schema); err != nil {
				return nil, fmt.Errorf("bedrock: tool %q input_schema: %w", t.Name, err)
			}
		} else {
			schema = map[string]any{"type": "object"}
		}
		spec := types.ToolSpecification{
			Name:        aws.String(t.Name),
			InputSchema: &types.ToolInputSchemaMemberJson{Value: document.NewLazyDocument(schema)},
		}
		if t.Description != "" {
			spec.Description = aws.String(t.Description)
		}
		tools = append(tools, &types.ToolMemberToolSpec{Value: spec})
	}
	return &types.ToolConfiguration{Tools: tools}, nil
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

	// toolPartial reassembles tool_use blocks across content_block_*
	// frames; the model streams the tool's input JSON in fragments
	// keyed by ContentBlockIndex.
	toolPartial map[int32]*toolUseAccum

	// syntheticToolName is the name of the structured-output synthetic tool
	// injected by applyResponseFormatToConverseInput or applyJSONModeToConverseInput.
	// On ContentBlockStop, tool_use blocks whose Name matches this value are
	// normalized: their Input JSON becomes a text ContentBlock instead.
	// (multimodal-io-extended-01KQ8TD2 WP04)
	syntheticToolName string
}

type toolUseAccum struct {
	id    string
	name  string
	input strings.Builder
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
		case *types.ConverseStreamOutputMemberContentBlockStart:
			if v.Value.Start == nil || v.Value.ContentBlockIndex == nil {
				continue
			}
			if start, ok := v.Value.Start.(*types.ContentBlockStartMemberToolUse); ok {
				if s.toolPartial == nil {
					s.toolPartial = map[int32]*toolUseAccum{}
				}
				accum := &toolUseAccum{}
				if start.Value.Name != nil {
					accum.name = *start.Value.Name
				}
				if start.Value.ToolUseId != nil {
					accum.id = *start.Value.ToolUseId
				}
				s.toolPartial[*v.Value.ContentBlockIndex] = accum
			}
		case *types.ConverseStreamOutputMemberContentBlockDelta:
			if v.Value.Delta == nil {
				continue
			}
			switch d := v.Value.Delta.(type) {
			case *types.ContentBlockDeltaMemberText:
				s.events <- llm.StreamEvent{
					Kind: llm.StreamText,
					Text: d.Value,
				}
			case *types.ContentBlockDeltaMemberToolUse:
				if v.Value.ContentBlockIndex == nil || d.Value.Input == nil {
					continue
				}
				if accum, ok := s.toolPartial[*v.Value.ContentBlockIndex]; ok {
					accum.input.WriteString(*d.Value.Input)
				}
			}
		case *types.ConverseStreamOutputMemberContentBlockStop:
			if v.Value.ContentBlockIndex == nil {
				continue
			}
			if accum, ok := s.toolPartial[*v.Value.ContentBlockIndex]; ok {
				delete(s.toolPartial, *v.Value.ContentBlockIndex)
				input := accum.input.String()
				if input == "" {
					input = "{}"
				}
				tool := llm.ToolUse{
					ID:    accum.id,
					Name:  accum.name,
					Input: json.RawMessage(input),
				}
				// Normalize synthetic structured-output tool (WP04): when the
				// tool name matches the injected synthetic tool, unwrap its Input
				// JSON as a text ContentBlock instead of a tool call.
				if s.syntheticToolName != "" && accum.name == s.syntheticToolName {
					// Emit a text StreamEvent so consumers see valid JSON text.
					s.events <- llm.StreamEvent{Kind: llm.StreamText, Text: input}
					// Build a text content block in the final response.
					s.finalResp.Content = append(s.finalResp.Content, llm.ContentBlock{
						Type: "text",
						Text: input,
					})
				} else {
					s.finalResp.ToolCalls = append(s.finalResp.ToolCalls, tool)
					s.events <- llm.StreamEvent{Kind: llm.StreamTool, Tool: &tool}
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
			// message_start and other frames are not surfaced yet.
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

