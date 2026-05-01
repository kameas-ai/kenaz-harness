package credstore

import (
	"context"
	"errors"
	"runtime"

	"github.com/sigil-tech/kaneaz-harness/core/mcp/recipes"
	"github.com/sigil-tech/kaneaz-harness/core/policy/cedar"
	"github.com/sigil-tech/kaneaz-harness/core/secrets"
	"github.com/sigil-tech/kaneaz-harness/core/secrets/ref"
)

// IssueForMCPSpawn resolves the credentials required to spawn an MCP
// stdio child process and returns a plain env map the spawn path passes
// to the pool's existing mergeEnv helper (via mcp.ServerSpec.Env).
//
// Flow (mission cedar-credential-policy-01KQ8TDE WP05):
//
//  1. Cedar gate fires via cedar.GateMCPSpawn:
//     - Allow:          proceed to resolution.
//     - Deny:           return ErrMCPSpawnDenied. Raw bytes never flow.
//     - NotApplicable + promptRegistry != nil:
//       fire Registry.RequestInteractive with a CredPromptSurface
//       (ProviderID = recipeID, Purpose = "mcp_spawn"). Block until the
//       user resolves, the 5-minute timeout fires, or ctx cancels.
//       Allow-once / Allow-always → proceed; Deny → ErrMCPSpawnDenied.
//     - NotApplicable + promptRegistry == nil: default-allow (pre-boot
//       posture, identical to the old lenient behaviour).
//
//  2. Resolution: each envKey is resolved through backend using the
//     canonical mcp/<recipeID>/<key> keychain locator. Missing optional
//     keys are skipped (the child process sees the var absent). Raw
//     bytes are zeroed before this function returns.
//
// The returned map is ready to merge into exec.Cmd.Env and never
// carries raw locator strings as values. On any error the map is nil.
func (s *store) IssueForMCPSpawn(
	ctx context.Context,
	recipeID string,
	envKeys []string,
	backend secrets.Backend,
) (map[string]string, error) {
	if backend == nil {
		return nil, errors.New("credstore: IssueForMCPSpawn: nil backend")
	}

	// ── 1. Cedar gate + optional interactive prompt ───────────────────
	if err := cedar.GateMCPSpawn(ctx, s.cedarGate, s.promptRegistry, recipeID, s.policyDataDir, s.policyEngine); err != nil {
		return nil, ErrMCPSpawnDenied
	}

	// ── 2. Resolve each env key ───────────────────────────────────────
	out := make(map[string]string, len(envKeys))

	var acquired []secrets.Secret
	defer func() {
		for _, sec := range acquired {
			sec.Destroy()
		}
	}()

	for _, key := range envKeys {
		cr := ref.CredentialReference{
			Kind:    ref.RefKeychain,
			Locator: recipes.KeychainLocator(recipeID, key),
		}
		sec, err := backend.Resolve(ctx, cr)
		if err != nil {
			// Missing credential: skip — the child process decides
			// whether the var is required. Required-vs-optional
			// enforcement is the caller's responsibility (see
			// recipes.ResolveEnv for the strict variant).
			continue
		}
		acquired = append(acquired, sec)

		var value string
		if useErr := sec.Use(func(raw []byte) error {
			buf := make([]byte, len(raw))
			copy(buf, raw)
			defer func() {
				for i := range buf {
					buf[i] = 0
				}
				runtime.KeepAlive(buf)
			}()
			value = string(buf)
			return nil
		}); useErr != nil {
			continue
		}
		out[key] = value
	}

	return out, nil
}

// ErrMCPSpawnDenied is returned by IssueForMCPSpawn when the Cedar
// policy gate or the interactive-prompt user denies the credential
// request. The bootstrap caller (rpc/api.go makeMCPRecipeBootstrap)
// logs this and skips the affected recipe rather than blocking the
// entire boot sequence.
var ErrMCPSpawnDenied = errors.New("credstore: mcp_spawn credential access denied")
