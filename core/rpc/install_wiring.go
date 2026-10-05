package rpc

import (
	"context"
	"errors"
	"fmt"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/install"
	"github.com/kameas-ai/kenaz-harness/core/mcp/stdio"
	capabilitiesview "github.com/kameas-ai/kenaz-harness/core/rpc/views/capabilities"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/tools"
	workflowsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/workflows"
)

// install_wiring.go — the chassis side of the one install framework
// (install-framework-01DOGF0B). The per-kind install bindings keep their
// typed signatures (the MCP key prompt needs env + config in and a
// supervisor status out; the workflow preview drawer needs the scheduled /
// missing-credential result) but every one of them routes through
// install.Framework, so verification, the consumer check and the
// capability:* events happen exactly once, in one place.

// installRecipe is Tools_InstallRecipe's body: the framework install with
// the key-prompt flow's inputs, returning the supervisor's status snapshot.
func installRecipe(ctx context.Context, fw *install.Framework, t tools.ToolsAPI, id string, env map[string]string, config map[string]any) (stdio.RecipeStatus, error) {
	if fw == nil {
		return stdio.RecipeStatus{}, fmt.Errorf("tools: install framework not wired")
	}
	res, err := fw.Install(ctx, install.Ref{Kind: install.KindMCPRecipe, ID: id}, install.Inputs{Secrets: env, Config: config})
	if err != nil {
		return stdio.RecipeStatus{}, err
	}
	if st, ok := res.Detail.(stdio.RecipeStatus); ok {
		return st, nil
	}
	return t.RecipeStatus(ctx, id)
}

// observeRecipeFlow is called after a per-kind MCP flow that installs on its
// own (OAuth sign-in, device-code approval) succeeds: the framework reads
// the supervisor and announces the install if — and only if — it is there.
func observeRecipeFlow(ctx context.Context, fw *install.Framework, id string) {
	if fw == nil {
		return
	}
	_, _ = fw.Observe(ctx, install.KindMCPRecipe, id)
}

// frameworkRoutedTools is the ToolsAPI the fleet MCP sync applier
// (sync_mcp_registry.go) sees: InstallRecipe goes through the framework,
// everything else passes straight to the tools view. Only that consumer
// gets the wrapper — the tools view itself stays the raw implementation the
// MCP provider adapts, so the framework never calls itself.
type frameworkRoutedTools struct {
	tools.ToolsAPI
	fw *install.Framework
}

func (r frameworkRoutedTools) InstallRecipe(ctx context.Context, id string, env map[string]string, config map[string]any) (stdio.RecipeStatus, error) {
	return installRecipe(ctx, r.fw, r.ToolsAPI, id, env, config)
}

// ── WP05: fleet-backed providers ──────────────────────────────────────────

// fleetCatalogSeam implements capabilitiesview.FleetCatalog over the fleet
// client — core/rpc is the only chassis package that may hold one, so the
// fleet-free providers receive it through this seam.
type fleetCatalogSeam struct {
	client *corefleet.Client
}

func (s fleetCatalogSeam) List(ctx context.Context, kind string) ([]capabilitiesview.CatalogEntry, error) {
	items, err := s.client.List(ctx, corefleet.CatalogFilter{Kind: corefleet.CatalogItemKind(kind)})
	if err != nil {
		return nil, err
	}
	out := make([]capabilitiesview.CatalogEntry, 0, len(items))
	for _, it := range items {
		out = append(out, capabilitiesview.CatalogEntry{
			ID: it.ID, Slug: it.Slug, Version: it.Version,
			Description: it.Description, Visibility: string(it.Visibility),
		})
	}
	return out, nil
}

func (s fleetCatalogSeam) Fetch(ctx context.Context, id, version string) ([]byte, string, error) {
	item, err := corefleet.FetchCatalogItem(ctx, s.client, id, version)
	if err != nil {
		return nil, "", err
	}
	return item.PayloadBytes, item.Signature, nil
}

// UnavailableReason classifies a catalog List failure for the reason row
// (P-5): fleet not configured on this build/profile, not signed in, or a
// real error. Disabled() is checked first so a fleet-less profile never
// reads the keychain to answer.
func (s fleetCatalogSeam) UnavailableReason(err error) string {
	switch {
	case corefleet.Disabled():
		return "fleet_disabled"
	case !corefleet.ReadTokenState().Usable():
		return "signed_out"
	case errors.Is(err, corefleet.ErrFleetDisabled):
		return "fleet_disabled"
	default:
		return "error"
	}
}

// installSignatureVerifier is the install framework's single
// SignatureVerifier — register C-2's per-device catalog key lands here,
// once, for every fleet-backed kind. The key is read per call from the
// catalog view (catalogview.API.PubKey, set via its WithPubKey seam): empty
// today, so every fleet payload installs recorded as unverified with the
// C-2 reason (this replaces the old SkillDeps.PubKeyBase64: "" placeholder
// — fleet-enforcement-truth-01PMZ505 WP10, owner alec, 2026-08-19: a
// standing blocker, not a settled "empty means skip" design).
func installSignatureVerifier(pubKey func() string) install.SignatureVerifier {
	return func(_ context.Context, _ install.Ref, payload []byte, signature string) (bool, string, error) {
		return corefleet.CatalogSignatureVerdict(pubKey(), payload, signature)
	}
}

// installSkill is Slashcmd_SkillInstall's body: the framework install of a
// fleet catalog skill.
func installSkill(ctx context.Context, fw *install.Framework, catalogID, version string) error {
	if fw == nil {
		return capabilitiesview.ErrUnavailable
	}
	_, err := fw.Install(ctx, install.Ref{Kind: install.KindSkill, ID: catalogID, Version: version}, install.Inputs{})
	return err
}

// installWorkflowTemplate is Workflows_CatalogInstall's body: the framework
// install of a shipped workflow template, returning the scheduled /
// missing-credential result the preview drawer renders.
func installWorkflowTemplate(ctx context.Context, fw *install.Framework, id string) (workflowsview.CatalogInstallResult, error) {
	if fw == nil {
		return workflowsview.CatalogInstallResult{}, capabilitiesview.ErrUnavailable
	}
	res, err := fw.Install(ctx, install.Ref{Kind: install.KindWorkflow, ID: id}, install.Inputs{})
	if err != nil {
		return workflowsview.CatalogInstallResult{}, err
	}
	if r, ok := res.Detail.(workflowsview.CatalogInstallResult); ok {
		return r, nil
	}
	return workflowsview.CatalogInstallResult{WorkflowID: id}, nil
}
