package cedar

import (
	"context"
	"testing"

	cedarlib "github.com/cedar-policy/cedar-go"
)

// BenchmarkEvaluate_Family_* benchmarks each WP01 family's hot path
// against the embedded default bundle. The mission acceptance gate is
// "<200µs per evaluate" — these benches assert it directly so a
// regression in the policy bundle, the context-attr derivation, or
// cedar-go itself trips CI before it ships.
//
// Hardware note: the threshold is calibrated for an Apple M-series
// laptop (the spec author's local rig). On slower CI runners the
// threshold is still well within reach because the default bundle is
// tiny — a handful of permit/forbid clauses each.

const evalBudgetNS = 200_000 // 200µs in nanoseconds

// runEvaluateBench exercises Engine.Evaluate in the canonical hot-path
// shape and asserts the per-iteration cost stays under
// evalBudgetNS. b.Loop() (Go 1.24+) is used so the framework owns the
// timer lifecycle and we don't accidentally include setup cost in the
// measurement.
func runEvaluateBench(
	b *testing.B,
	action string,
	resource cedarlib.EntityUID,
	contextAttrs map[cedarlib.String]cedarlib.Value,
) {
	b.Helper()
	e, err := NewEngine(Options{IncludeEmbedded: true})
	if err != nil {
		b.Fatalf("NewEngine: %v", err)
	}
	// Warm up the policy set so the first iteration's cold-cache cost
	// doesn't dominate the report.
	_ = e.Evaluate(context.Background(), UserUID(), action, resource, contextAttrs)

	ctx := context.Background()
	principal := UserUID()

	b.ResetTimer()
	for b.Loop() {
		_ = e.Evaluate(ctx, principal, action, resource, contextAttrs)
	}
	b.StopTimer()

	// b.NsPerOp() is set after the run; assert against the budget.
	nsPerOp := float64(b.Elapsed().Nanoseconds()) / float64(b.N)
	if nsPerOp > float64(evalBudgetNS) {
		b.Fatalf("Evaluate over budget: %.0fns/op (budget %dns/op)",
			nsPerOp, evalBudgetNS)
	}
}

// BenchmarkEvaluate_Credential_Allow exercises the routine-purpose
// permit branch — the common "every chat turn pulls an API key" path.
func BenchmarkEvaluate_Credential_Allow(b *testing.B) {
	runEvaluateBench(b,
		ActionUseCredential,
		CredentialUID("openai", "provider_call"),
		map[cedarlib.String]cedarlib.Value{
			cedarlib.String(CtxKeyPurpose): cedarlib.String("provider_call"),
		},
	)
}

// BenchmarkEvaluate_Credential_Deny exercises the manual_export
// forbid branch.
func BenchmarkEvaluate_Credential_Deny(b *testing.B) {
	runEvaluateBench(b,
		ActionUseCredential,
		CredentialUID("openai", "manual_export"),
		map[cedarlib.String]cedarlib.Value{
			cedarlib.String(CtxKeyPurpose): cedarlib.String("manual_export"),
		},
	)
}

// BenchmarkEvaluate_Bash_NotApplicable exercises the header-only
// default — every bash command is NotApplicable until a sibling
// `bash_allow_*.cedar` lands.
func BenchmarkEvaluate_Bash_NotApplicable(b *testing.B) {
	runEvaluateBench(b,
		ActionRunBashCommand,
		BashCommandUID("git status"),
		nil,
	)
}

// BenchmarkEvaluate_Filesystem_ReadAllow exercises the recipe-dir
// read-permit branch — the common "MCP filesystem read inside the
// recipe scope" path.
func BenchmarkEvaluate_Filesystem_ReadAllow(b *testing.B) {
	runEvaluateBench(b,
		ActionReadFilesystem,
		FilesystemOpUID("/Users/alec/recipe/notes.md"),
		map[cedarlib.String]cedarlib.Value{
			cedarlib.String(CtxKeyRecipeDirMatch): cedarlib.Boolean(true),
		},
	)
}

// BenchmarkEvaluate_Filesystem_WriteNotApplicable exercises a write
// against a recipe-scoped path — should fall through.
func BenchmarkEvaluate_Filesystem_WriteNotApplicable(b *testing.B) {
	runEvaluateBench(b,
		ActionWriteFilesystem,
		FilesystemOpUID("/Users/alec/recipe/notes.md"),
		map[cedarlib.String]cedarlib.Value{
			cedarlib.String(CtxKeyRecipeDirMatch): cedarlib.Boolean(true),
		},
	)
}

// BenchmarkEvaluate_Tool_BuiltinAllow exercises the built-in tool
// permit branch.
func BenchmarkEvaluate_Tool_BuiltinAllow(b *testing.B) {
	runEvaluateBench(b,
		ActionUseTool,
		PermissionToolUID("kenaz__bash"),
		nil,
	)
}

// BenchmarkEvaluate_Tool_NonBuiltinNotApplicable exercises the MCP-tool
// fall-through path.
func BenchmarkEvaluate_Tool_NonBuiltinNotApplicable(b *testing.B) {
	runEvaluateBench(b,
		ActionUseTool,
		PermissionToolUID("filesystem__read_file"),
		nil,
	)
}
