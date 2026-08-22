package rpc

// wf_network_authz_adapter.go — automation-actually-runs-01PMZ404 UNIT-7.
//
// wfNetworkAuthorizerAdapter is the production implementation of
// corewf.NetworkAuthorizer. Before this unit, corewf.Deps.NetAuthz was
// never assigned, so both nil-guards in core/workflows/runners.go
// (webFetchRunner.Run, webScrapeRunner.Run) were permanently skipped and
// cedarStrictWorkflowMode could never deny a network-bearing step —
// despite the shipped policy bundle already declaring a permissive-mode
// permit for Action::"workflow.network.fetch" and a settings dial that
// was live and user-facing.
//
// X-9 (spec §1.11): wiring the adapter alone is NOT sufficient. The
// shipped policy's strict arm used to be *absence* — no permit matches
// in strict mode, so the Cedar evaluation outcome is NotApplicable — and
// core/policy/cedar/hooks.go's enforce() maps NotApplicable to nil, same
// as Allow. UNIT-7 also adds an explicit `forbid` rule to
// default_workflows_policy.cedar's network.fetch section so strict mode
// actually evaluates to Deny. Both halves are required; this file is
// only the adapter half.

import (
	"context"
	"fmt"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// wfNetworkAuthorizerAdapter gates a workflow's outbound network step
// through the SAME Cedar engine + live mode resolver GateWorkflowRun /
// GateWorkflowSave / GateWorkflowDelete use — no new bypass (spec D-5).
type wfNetworkAuthorizerAdapter struct {
	gate   cedar.Gate
	modeFn func() string
}

// Authorize implements corewf.NetworkAuthorizer. resourceID is the
// running workflow's id (core/workflows/runners.go's call sites pass
// rc.Workflow.ID, not the step name — see corewf.NetworkAuthorizer's doc
// comment). mode is read live on every call via modeFn, matching the
// same "no boot snapshot" rule workflowCedarModeFn documents — a user
// flipping the strict-mode setting takes effect on the next fetch, not
// after a restart.
func (n *wfNetworkAuthorizerAdapter) Authorize(ctx context.Context, action, resourceID string) error {
	mode := "permissive"
	if n != nil && n.modeFn != nil {
		mode = n.modeFn()
	}
	var gate cedar.Gate
	if n != nil {
		gate = n.gate
	}
	if _, err := cedar.GateWorkflowNetworkFetch(ctx, gate, resourceID, mode); err != nil {
		return fmt.Errorf("%s: %w", action, err)
	}
	return nil
}
