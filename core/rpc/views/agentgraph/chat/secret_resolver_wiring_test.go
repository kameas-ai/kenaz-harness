package chat

// trust-surfaces-that-fire-01PMZ202 WP19.
//
// Before this WP, refs.WithResolver / refs.WithTurnSanitizer had zero
// non-test callers anywhere in core/ or cmd/: core/tools/bash,
// core/tools/webfetch and core/mcp/transport/stdio/server.go all guard
// with `if resolver := refs.ResolverFromContext(ctx); resolver != nil`,
// and nothing ever installed one, so the guard was always false in a
// shipped build and the literal "@secret:<locator>" token reached the
// wire unchanged. These tests drive a REAL ChatRunner (production
// Config.Pool / kernelToolAdapter path, production chat_default.yaml
// graph, production turnJournal) to prove driveRun's new wiring block
// closes that gap end-to-end, and that it never ships the resolver
// without the sanitizer that keeps the resolved plaintext out of the
// persisted transcript.

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/credstore/refs"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
)

// secretSentinelWP19 is a distinctive plaintext value — collision with
// ordinary transcript text is implausible, mirroring the sentinel
// convention core/credstore/refs/no_plaintext_test.go already uses.
const secretSentinelWP19 = "wp19_SENTINEL_0x_NoLeak_ChatRunner"

// echoingSecretPool is a ToolPool whose one tool mimics the EXACT
// contract core/tools/bash.go / core/tools/webfetch.go already
// implement in production:
//
//  1. Resolve any @secret: reference in its input via
//     refs.ResolverFromContext(ctx) — a nil resolver leaves the input
//     unchanged (the pre-WP19 / no-lookup-configured behaviour).
//  2. "Execute" and echo the (possibly-substituted) value back into the
//     tool result — modelling any tool whose output can reflect its own
//     input, which is not hypothetical: the no_plaintext_test.go
//     echo-server case exercises exactly this against the real
//     webfetch tool.
//  3. Redact the result via refs.SanitizerFromContext(ctx) before
//     returning, again mirroring bash.go/webfetch.go's own
//     "sanitize stdout/stderr" step.
//
// resolved records what the tool's "command" argument actually resolved
// to, so a test can assert on what reached the tool-dispatch boundary
// independent of what got persisted.
type echoingSecretPool struct {
	mu       sync.Mutex
	resolved []string
}

func (p *echoingSecretPool) Tools(context.Context) ([]ToolEntry, error) {
	return []ToolEntry{{Server: "shell", Name: "run"}}, nil
}

func (p *echoingSecretPool) Call(ctx context.Context, _, tool string, argsJSON []byte) ([]byte, error) {
	var args struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(argsJSON, &args)

	cmd := args.Command
	if resolver := refs.ResolverFromContext(ctx); resolver != nil && refs.HasReference(cmd) {
		sub, _, err := resolver.Substitute(ctx, cmd, cedar.ResolveContext{ToolName: tool})
		if err == nil {
			cmd = sub
		}
	}

	p.mu.Lock()
	p.resolved = append(p.resolved, cmd)
	p.mu.Unlock()

	result := []byte("ran: " + cmd)
	if s := refs.SanitizerFromContext(ctx); s != nil {
		result = s.Sanitize(result)
	}
	return result, nil
}

func (p *echoingSecretPool) snapshotResolved() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.resolved))
	copy(out, p.resolved)
	return out
}

// buildSecretAwareRunner wires a ChatRunner through the production
// Config.Pool / kernelToolAdapter path (not the EnvDefaults override
// some other integration tests use, which would bypass driveRun's new
// wiring entirely) against the real chat_default.yaml graph.
func buildSecretAwareRunner(t *testing.T, reg *scriptedRegistry, pool *echoingSecretPool, lookup refs.SecretLookup) (
	*ChatRunner, *recordingBroker, *recordingHistoryWriter) {
	t.Helper()
	broker := &recordingBroker{}
	writer := &recordingHistoryWriter{}
	graph := loadProductionChatGraph(t)
	runner, err := New(Config{
		Kernel:        coreag.NewKernel(),
		Registry:      reg,
		Pool:          pool,
		Broker:        broker,
		HistoryWriter: writer,
		History:       staticHistoryReader{},
		GraphLoader:   func() (coreag.Graph, error) { return graph, nil },
		MaxTurns:      func() int { return 25 },
		SecretLookup:  lookup,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return runner, broker, writer
}

// newExposureIndexWithSentinel builds a *secrets.ExposureIndex exposing
// one locator resolving to secretSentinelWP19.
func newExposureIndexWithSentinel(locator string) *secrets.ExposureIndex {
	idx := secrets.NewExposureIndex()
	pt := []byte(secretSentinelWP19)
	idx.Add(secrets.ExposedEntry{
		Locator:  locator,
		Scope:    secrets.ScopeSession,
		KindHint: secrets.KindHintRaw,
	}, pt)
	for i := range pt {
		pt[i] = 0
	}
	return idx
}

// firstToolResult returns the tool_result move (there is exactly one in
// these single-tool-call fixtures).
func firstToolResult(t *testing.T, w *recordingHistoryWriter) coreag.HistoryEntry {
	t.Helper()
	for _, e := range moveEntries(w) {
		if e.MoveKind == moveKindToolResult {
			return e
		}
	}
	t.Fatalf("no tool_result move persisted; entries=%s", describeMoves(moveEntries(w)))
	return coreag.HistoryEntry{}
}

// TestSecretResolver_ChatRunnerSubstitutesAndSanitizes is the AC-13
// acceptance case: an @secret: reference in a tool argument is
// substituted with the real value at the execution boundary (proven by
// echoingSecretPool.resolved), and the resolved value does not appear
// in the persisted turn — the actual stored HistoryEntry.Content the
// journal wrote through recordingHistoryWriter, not just the in-memory
// ToolResult struct.
//
// MUTATION EVIDENCE (see this WP's commit for the before/after): deleting
// driveRun's `ctx = refs.WithResolver(...)` / `refs.WithTurnSanitizer(...)`
// block collapses this test to TestSecretResolver_NilLookupLeavesLiteralUnsubstituted's
// behaviour — the resolved command stays the literal token and the
// substitution assertion below fails.
func TestSecretResolver_ChatRunnerSubstitutesAndSanitizes(t *testing.T) {
	t.Parallel()

	const locator = "user:wp19-chat-runner"
	idx := newExposureIndexWithSentinel(locator)

	reg := &scriptedRegistry{}
	reg.push(toolTurn("running it", "tu-1", "shell__run",
		`{"command":"echo @secret:`+locator+`"}`))
	reg.push(textTurn("done"))

	pool := &echoingSecretPool{}
	runner, broker, writer := buildSecretAwareRunner(t, reg, pool, idx)

	if _, err := runner.StartStream(context.Background(), "profile-1", "session-1", "", "run it"); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	closed := waitForClosed(t, broker)
	if closed.Reason == "backend-error" {
		t.Fatalf("run failed: %s", closed.Message)
	}

	// The tool actually received the resolved plaintext, not the
	// literal token — this is FR-010's "substituted at the execution
	// boundary" half.
	resolved := pool.snapshotResolved()
	if len(resolved) != 1 {
		t.Fatalf("pool saw %d calls, want 1: %v", len(resolved), resolved)
	}
	want := "echo " + secretSentinelWP19
	if resolved[0] != want {
		t.Errorf("tool saw command %q, want %q (the resolver never substituted — driveRun's WithResolver wiring is not reaching the tool-dispatch ctx)", resolved[0], want)
	}

	// The STORED record — not the in-memory ToolResult, the actual
	// HistoryEntry the journal persisted through the HistoryWriter seam
	// — must not contain the plaintext, and must show the redaction
	// placeholder in its place. This is FR-010's "redacted from output"
	// half, and the reason driveRun always installs the Sanitizer in the
	// same block as the Resolver.
	stored := firstToolResult(t, writer)
	if strings.Contains(stored.Content, secretSentinelWP19) {
		t.Errorf("persisted tool_result Content contains the plaintext secret: %q", stored.Content)
	}
	wantPlaceholder := "[redacted: " + locator + "]"
	if !strings.Contains(stored.Content, wantPlaceholder) {
		t.Errorf("persisted tool_result Content = %q, want it to contain %q", stored.Content, wantPlaceholder)
	}
}

// TestSecretResolver_NilLookupLeavesLiteralUnsubstituted pins the
// pre-WP19 / no-lookup-configured behaviour: with Config.SecretLookup
// nil, driveRun's `if r.cfg.SecretLookup != nil` guard never installs a
// Resolver, so the literal "@secret:<locator>" token reaches the tool
// exactly as it did before this WP — the defect this WP closes,
// reproduced on demand. Config.SecretLookup nil is the same code path a
// reverted driveRun wiring would leave every session in regardless of
// what production wiring passes, so this test also stands in for "the
// resolver installation was removed."
func TestSecretResolver_NilLookupLeavesLiteralUnsubstituted(t *testing.T) {
	t.Parallel()

	const locator = "user:wp19-no-lookup"
	literal := "echo @secret:" + locator

	reg := &scriptedRegistry{}
	reg.push(toolTurn("running it", "tu-1", "shell__run",
		`{"command":"`+literal+`"}`))
	reg.push(textTurn("done"))

	pool := &echoingSecretPool{}
	runner, broker, _ := buildSecretAwareRunner(t, reg, pool, nil)

	if _, err := runner.StartStream(context.Background(), "profile-1", "session-1", "", "run it"); err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	closed := waitForClosed(t, broker)
	if closed.Reason == "backend-error" {
		t.Fatalf("run failed: %s", closed.Message)
	}

	resolved := pool.snapshotResolved()
	if len(resolved) != 1 {
		t.Fatalf("pool saw %d calls, want 1: %v", len(resolved), resolved)
	}
	if resolved[0] != literal {
		t.Errorf("tool saw command %q, want the untouched literal %q", resolved[0], literal)
	}
}
