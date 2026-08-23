package stdio

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/credstore/refs"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

// TestServer_CallTool_SanitizesResolvedSecret is the falsification for
// B1(a), trust-surfaces-that-fire-01PMZ202 review (2026-08-23): before
// this fix, ServerInstance.CallTool called refs.ResolverFromContext to
// substitute plaintext into outbound MCP tool arguments but never
// called refs.SanitizerFromContext on the result, so a server that
// echoed the plaintext back (a hostile server intentionally, or simply
// a buggy one) put it straight into the value CallTool returns, which
// is what the tool-loop then persists to session history and streams
// to the UI. bash.go and webfetch.go were the only two production
// Sanitize callers; this test proves the MCP stdio transport is now a
// third.
//
// This drives the REAL production CallTool path against a spawned
// child process (the compiled fake-mcp-server, whose fake_echo tool
// echoes its arguments verbatim as "echo:<args>") — not a hand-rolled
// fixture — so the fix is proven at the actual boundary the plaintext
// crosses, matching the "drive a real code path" testing rule.
func TestServer_CallTool_SanitizesResolvedSecret(t *testing.T) {
	t.Parallel()
	bin := buildFakeServer(t)
	inst := newServerInstance("fake", nil, nil, nil, nil, instanceOptions{})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := inst.Spawn(ctx, SpawnSpec{ID: "fake", Command: []string{bin}}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer inst.Close(context.Background())

	const locator = "user:mcp-sanitize-test"
	const plaintext = "SUPER_SECRET_PLAINTEXT_VALUE_0xB1a"

	idx := secrets.NewExposureIndex()
	pt := []byte(plaintext)
	idx.Add(secrets.ExposedEntry{
		Locator:  locator,
		Scope:    secrets.ScopeSession,
		KindHint: secrets.KindHintRaw,
	}, pt)
	for i := range pt {
		pt[i] = 0
	}

	resolver := refs.NewResolver(refs.ResolverOptions{
		Lookup:    idx,
		SessionID: "ses_test",
		Agent:     "test",
	})
	sanitizer := refs.NewSanitizer()

	callCtx := refs.WithResolver(ctx, resolver)
	callCtx = refs.WithTurnSanitizer(callCtx, sanitizer)

	args, err := json.Marshal(map[string]string{"text": "@secret:" + locator})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	res, err := inst.CallTool(callCtx, "fake_echo", args)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	if strings.Contains(string(res), plaintext) {
		t.Fatalf("plaintext leaked into CallTool result (unsanitized): %s", res)
	}
	if !strings.Contains(string(res), "[redacted: "+locator+"]") {
		t.Fatalf("result missing redaction placeholder for %q: %s", locator, res)
	}
}

// TestServer_CallTool_AgentKindUntrustedDenied is the falsification
// for B1(c): the MCP stdio transport is the one call site among the
// three secret-reference consumers (bash, web_fetch, MCP) whose
// resolutions must be evaluated as agent_kind="untrusted" — the exact
// class secret_reference.cedar's default forbid targets (spec
// model-secret-references-01KW7M5A §2.4, "forbid when the agent_kind
// is untrusted (covers MCP servers that haven't been audited)").
//
// This wires a REAL *cedar.Engine (the same IncludeEmbedded
// construction core/rpc/api.go uses) as the Resolver's Gate, so the
// test exercises both B1(b) (the policy is actually loaded) and
// B1(c) (the call site labels itself correctly) together through the
// production CallTool path — a resolver constructed with a fake
// always-permit Gate would not catch a regression in either.
func TestServer_CallTool_AgentKindUntrustedDenied(t *testing.T) {
	t.Parallel()
	bin := buildFakeServer(t)
	inst := newServerInstance("fake", nil, nil, nil, nil, instanceOptions{})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := inst.Spawn(ctx, SpawnSpec{ID: "fake", Command: []string{bin}}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer inst.Close(context.Background())

	const locator = "user:mcp-agentkind-test"

	idx := secrets.NewExposureIndex()
	pt := []byte("value-should-never-resolve")
	idx.Add(secrets.ExposedEntry{
		Locator:  locator,
		Scope:    secrets.ScopeSession,
		KindHint: secrets.KindHintRaw,
	}, pt)
	for i := range pt {
		pt[i] = 0
	}

	engine, err := cedar.NewEngine(cedar.Options{LoadFromDisk: false, IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("cedar.NewEngine: %v", err)
	}

	resolver := refs.NewResolver(refs.ResolverOptions{
		Lookup:    idx,
		Gate:      engine,
		Budget:    refs.NewBudget(refs.DefaultBudget),
		SessionID: "ses_test",
		Agent:     "test",
	})

	callCtx := refs.WithResolver(ctx, resolver)

	args, err := json.Marshal(map[string]string{"text": "@secret:" + locator})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	_, err = inst.CallTool(callCtx, "fake_echo", args)
	if err == nil {
		t.Fatal("CallTool: expected secret resolution to be denied for an MCP (untrusted) caller, got nil error")
	}
	if !strings.Contains(err.Error(), "secret resolution failed") {
		t.Fatalf("CallTool err = %v, want a secret-resolution-denied error", err)
	}
}
