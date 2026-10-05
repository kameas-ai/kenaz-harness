package rpc

import (
	"context"
	"fmt"

	"github.com/kameas-ai/kenaz-harness/core/install"
	"github.com/kameas-ai/kenaz-harness/core/mcp/stdio"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/tools"
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
