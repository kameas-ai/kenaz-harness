package cedar_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// TestToolDispatchNeverAllowRecommended_DeniesAtLayer1 is WP09's proof
// (risk-rated-autonomy-01PMRA01, tasks.md WP09): "for each new rule, a
// test that the action denies at layer 1 and the rater is never
// invoked." This half exercises the Cedar layer directly (Deny at layer
// 1); TestKernelToolAdapter_NeverAllowPolicy_RaterNeverInvoked
// (kernel_tool_adapter package) exercises the "rater never invoked" half
// through the real dispatch path.
//
// Loads tool-dispatch-never-allow-recommended.cedar the SAME way an
// operator activates it in production — copied into <DataDir>/policy/,
// composed with the embedded default bundle via a real Engine.Reload —
// rather than via SetPolicyText's single-bundle test seam, so this test
// also stands as evidence the file is actually loadable end to end, not
// just syntactically valid (TestAllEmbeddedCedarTemplatesParse already
// covers syntax; this covers activation + matching).
func TestToolDispatchNeverAllowRecommended_DeniesAtLayer1(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	policyDir := filepath.Join(dataDir, cedar.PolicyDir)
	if err := os.MkdirAll(policyDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	src, err := cedar.PoliciesFS.ReadFile("policies/tool-dispatch-never-allow-recommended.cedar")
	if err != nil {
		t.Fatalf("reading embedded tool-dispatch-never-allow-recommended.cedar: %v", err)
	}
	if err := os.WriteFile(filepath.Join(policyDir, "tool-dispatch-never-allow-recommended.cedar"), src, 0o644); err != nil {
		t.Fatalf("writing to DataDir/policy: %v", err)
	}

	e, err := cedar.NewEngine(cedar.Options{DataDir: dataDir, LoadFromDisk: true, IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	cases := []struct {
		name       string
		server     string
		tool       string
		wantDenied bool
	}{
		// One representative match per never-allow class named in
		// spec.md's security section.
		{"credential-exfiltration", "somemcp", "export_secret_bundle", true},
		{"force-push", "gitmcp", "force_push_branch", true},
		{"mass-delete", "filesmcp", "bulk_delete_workspace", true},
		{"spend", "paymcp", "create_payment_intent", true},
		// Negative controls: an ordinary, unrelated tool name must NOT
		// be denied by this file (it should fall through to layer 3 —
		// Confirm — same as before this file existed).
		{"unrelated-read", "somemcp", "read_file", false},
		{"unrelated-write", "somemcp", "write_file", false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := cedar.ThreeLayerResolve(
				context.Background(), e, cedar.UserUID(), cedar.ActionUseTool,
				cedar.ToolUID(tc.server, tc.tool), nil, 80,
			)
			if tc.wantDenied {
				if d.Outcome != cedar.Deny {
					t.Fatalf("tool %s__%s: Outcome = %v, want Deny (layer 1) — tool-dispatch-never-allow-"+
						"recommended.cedar should have matched and denied this call before any rating "+
						"could occur", tc.server, tc.tool, d.Outcome)
				}
			} else {
				if d.Outcome == cedar.Deny {
					t.Fatalf("tool %s__%s: Outcome = Deny, want Allow/Confirm — an unrelated tool name must "+
						"not be caught by the never-allow wildcards", tc.server, tc.tool)
				}
			}
		})
	}
}

// TestToolDispatchNeverAllowRecommended_AuditPurgeIsOutOfScope pins the
// documented WP09 scope decision: audit bulk purge is NOT reachable via
// Action::"use_tool" (no model tool call can trigger it — see
// tool-dispatch-never-allow-recommended.cedar's own header), so no
// use_tool-shaped rule for it exists in this file. Its own layer-1
// forbid (default_audit_bulk_purge_policy.cedar, Action::"audit.bulk_purge"
// on resource AuditLog) is asserted separately by
// TestOutcomeConfirmAuditLedger_CheckAuditBulkPurge-adjacent coverage;
// this test only pins that an ordinary use_tool dispatch naming
// something audit-purge-shaped is NOT accidentally caught by (or
// silently absent from) this file in a way that would suggest the two
// action families were conflated.
func TestToolDispatchNeverAllowRecommended_AuditPurgeIsOutOfScope(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	policyDir := filepath.Join(dataDir, cedar.PolicyDir)
	if err := os.MkdirAll(policyDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	src, err := cedar.PoliciesFS.ReadFile("policies/tool-dispatch-never-allow-recommended.cedar")
	if err != nil {
		t.Fatalf("reading embedded tool-dispatch-never-allow-recommended.cedar: %v", err)
	}
	if err := os.WriteFile(filepath.Join(policyDir, "tool-dispatch-never-allow-recommended.cedar"), src, 0o644); err != nil {
		t.Fatalf("writing to DataDir/policy: %v", err)
	}
	e, err := cedar.NewEngine(cedar.Options{DataDir: dataDir, LoadFromDisk: true, IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	// A use_tool dispatch named like an audit-purge tool is NOT denied by
	// this file (it is out of scope for use_tool — see the header) and
	// falls through to layer 3, same as any other unmatched tool.
	d := cedar.ThreeLayerResolve(
		context.Background(), e, cedar.UserUID(), cedar.ActionUseTool,
		cedar.ToolUID("somemcp", "audit_bulk_purge"), nil, 80,
	)
	if d.Outcome == cedar.Deny {
		t.Fatalf("Outcome = Deny, want Confirm/Allow — audit-purge is out of scope for use_tool "+
			"dispatch by design (see the file's header); a Deny here would mean an accidental rule "+
			"was added that the header does not document")
	}
}
