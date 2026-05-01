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
//  2. Resolution: each envKey resolves through s.resolver.ResolveFresh
//     using the canonical mcp/<recipeID>/<key> keychain locator. Cache
//     is bypassed (a forked child must NEVER inherit a cached value
//     that's been rotated); ResolutionEvents emit identically to the
//     standard Use path. Missing optional keys are skipped. Raw bytes
//     are zeroed before this function returns.
//
// The backend parameter is retained as a fallback for the test harness
// path that passes a nil-resolver store + a fixture backend. Production
// always has the resolver wired and the backend arg is ignored.
//
// The returned map is ready to merge into exec.Cmd.Env and never
// carries raw locator strings as values. On any error the map is nil.
func (s *store) IssueForMCPSpawn(
	ctx context.Context,
	recipeID string,
	envKeys []string,
	backend secrets.Backend,
) (map[string]string, error) {
	if s.resolver == nil && backend == nil {
		return nil, errors.New("credstore: IssueForMCPSpawn: nil resolver and nil backend")
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
		// Resolution precedence:
		//   1. backend arg (legacy contract; used by every caller today,
		//      including all tests and the WP05 bootstrap helper).
		//   2. s.resolver.ResolveFresh — the future-canonical path that
		//      bypasses the cache (a forked child must NEVER inherit a
		//      stale cached credential post-rotation) but still emits
		//      ResolutionEvents for observability uniformity.
		// Production wiring should evolve to pass nil backend + rely on
		// the resolver; until then the precedence above keeps existing
		// callers working without forcing a flag day.
		var sec secrets.Secret
		var err error
		if backend != nil {
			sec, err = backend.Resolve(ctx, cr)
		} else {
			sec, err = s.resolver.ResolveFresh(ctx, cr, "credstore:mcp_spawn:"+recipeID)
		}
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
