package rpc

// api_ctx_graph_construction_test.go — construction-path regression guard for
// the ContextGraphSyncer audit emitter wire.
//
// FR-006/007/008 (context-graph-e2e-01NINTG03): the three MustEmit calls in
// context_graph_sync.go (KindFleetContextPublished, KindFleetContextPulled,
// KindFleetContextPromoted) are dead in production unless
// ContextGraphSyncer.WithAuditEmitter is chained during construction.  The
// previous code omitted that call, leaving s.auditEmitter == nil forever.
//
// This test boots the REAL chassis (rpc.New) with a DataDir and asserts that
// ctxGraphSyncer is non-nil and its AuditEmitter() is non-nil — meaning the
// three MustEmit call sites will actually fire at runtime.

import (
	"testing"

	"github.com/kameas-ai/kenaz-harness/core"
)

// TestContextGraphSyncer_AuditEmitterWired verifies that New wires a non-nil
// contextaudit.Emitter on the ContextGraphSyncer when a DataDir is present.
//
// This test WOULD HAVE CAUGHT the pre-fix gap: without
// .WithAuditEmitter(&contextSyncAuditBridge{...}) the auditEmitter field stays
// nil and all three MustEmit call sites become silent no-ops in production.
func TestContextGraphSyncer_AuditEmitterWired(t *testing.T) {
	c, err := core.New(core.Options{
		DataDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}

	api := New(c)

	if api.ctxGraphSyncer == nil {
		t.Fatal("ctxGraphSyncer is nil — ContextGraphSyncer was not wired; check the contextsAPI type-assertion gate in api.go")
	}
	if api.ctxGraphSyncer.AuditEmitter() == nil {
		t.Error("ctxGraphSyncer.AuditEmitter() is nil — WithAuditEmitter was not called during construction; " +
			"MustEmit calls at context_graph_sync.go:473/:612/:710 are silent no-ops in production")
	}
}
