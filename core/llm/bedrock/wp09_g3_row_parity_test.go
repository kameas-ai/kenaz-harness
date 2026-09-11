package bedrock

// WP09 (structured-output-is-reachable-01PMZE14, UNIT-6, G-3) companion
// to core/llm/registry/wp09_g3_capability_row_parity_test.go.
//
// The shared cross-adapter parity test in package registry can only
// exercise bedrock's FALSE-row half generically (Gate.Check refusal
// needs no adapter-specific code). Its TRUE-row half — "does the
// adapter's own encoder actually carry the schema when the row says
// it can" — is not reachable from outside this package for bedrock
// specifically: the exported StructuredOutputAdapter method
// (ApplyResponseFormat, bedrock.go:186) is a DOCUMENTED no-op for
// wireBody, and the real encoder
// (applyResponseFormatToConverseInput) is unexported. This file is
// the white-box half of G-3 that reaches it, tied to the SAME
// capabilities.LoadDefault() catalog the registry-package test uses
// (never a raw YAML read — spec §8 rule 2).
//
// anthropic.claude-3-5-sonnet* is bedrock.yaml's one true row
// (:64) — the exact row spec §1.8 discusses ("bedrock wires it on
// both transports"). If this test starts failing because the row
// changed, that is new information, not a broken test.

import (
	"encoding/json"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	llm "github.com/kameas-ai/kenaz-harness/core/llm"
	"github.com/kameas-ai/kenaz-harness/core/llm/capabilities"
)

func TestG3_Bedrock_TrueRowMatchesConverseEncoder(t *testing.T) {
	cat, err := capabilities.LoadDefault()
	if err != nil {
		t.Fatalf("capabilities.LoadDefault: %v", err)
	}

	const probeModel = "anthropic.claude-3-5-sonnet-20241022-v2:0"
	desc := cat.Describe(Kind, probeModel)
	if !desc.Has(llm.CapStructuredOutput) {
		t.Fatalf(
			"bedrock.yaml no longer marks structured_output=true for %s — "+
				"this test's whole premise (a real true row exists for bedrock) no longer holds; "+
				"update the probe model or record why the row changed",
			probeModel,
		)
	}

	// Gate.Check must agree independently (same assertion the
	// registry-package test makes for every other kind).
	gate := capabilities.NewGate(cat)
	schema := json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"}}}`)
	req := llm.GenerationRequest{ResponseFormat: &llm.ResponseFormat{Mode: "json_schema", Schema: schema}}
	prof := llm.ProviderProfile{Kind: Kind, Model: probeModel}
	if _, gateErr := gate.Check(req, prof); gateErr != nil {
		t.Fatalf("row says structured_output=true for (%s, %s) but Gate.Check refused it: %v", Kind, probeModel, gateErr)
	}

	// The real encoder: does it actually carry the schema onto the
	// Converse SDK input? This is the part ApplyResponseFormat cannot
	// answer (it's a documented no-op).
	in := &bedrockruntime.ConverseStreamInput{}
	rf := &llm.ResponseFormat{Mode: "json_schema", Schema: schema}
	if err := applyResponseFormatToConverseInput(in, rf); err != nil {
		t.Fatalf("applyResponseFormatToConverseInput: %v", err)
	}
	if in.ToolConfig == nil || len(in.ToolConfig.Tools) == 0 {
		t.Fatalf(
			"row says structured_output=true for (%s, %s) but applyResponseFormatToConverseInput "+
				"did not inject the synthetic structured-output tool — the row advertises a capability "+
				"the SDK-path encoder cannot serve",
			Kind, probeModel,
		)
	}
	tool, ok := in.ToolConfig.Tools[0].(*types.ToolMemberToolSpec)
	if !ok || tool.Value.Name == nil || *tool.Value.Name != "_structured_output" {
		t.Fatalf("row says structured_output=true for (%s, %s) but the injected tool is not the structured-output tool: %+v", Kind, probeModel, in.ToolConfig.Tools[0])
	}
}
