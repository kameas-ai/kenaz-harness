package kind

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// ErrInvalid is returned when a kind name fails well-formedness rules.
// (Mirrored as core/event.ErrInvalidKind for the public surface; this
// package keeps a local sentinel to avoid an import cycle.)
var ErrInvalid = errors.New("kind: invalid")

// Kind is the registry-internal representation. core/event.Kind aliases
// it via string conversion at the call site.
type Kind string

// Built-in self-event kinds. Consumers register additional kinds under
// their own namespace prefix.
const (
	KindRetentionStarted    Kind = "event-log.retention.started"
	KindRetentionCompleted  Kind = "event-log.retention.completed"
	KindSaltRotated         Kind = "event-log.redaction.salt-rotated"
	KindSessionBranched     Kind = "event-log.session.branched"
	KindRawReplayOpened     Kind = "event-log.replay.raw-opened"
	KindRedactionSupersede  Kind = "event-log.redaction.supersede"
	KindChainRebased        Kind = "event-log.chain.rebased"
	KindCancellation        Kind = "event-log.cancellation"
	KindError               Kind = "event-log.error"
	KindTimeout             Kind = "event-log.timeout"
	KindDatabaseOpened      Kind = "event-log.database.opened"

	// Universal permission audit kinds (cedar-credential-policy-01KQ8TDE WP07).
	// These fire regardless of which resource family triggered the gate.
	KindPermissionGranted  Kind = "permission.granted"
	KindPermissionDenied   Kind = "permission.denied"
	KindPermissionPrompted Kind = "permission.prompted"
	KindPermissionTimeout  Kind = "permission.timeout"
	KindPermissionRevoked  Kind = "permission.revoked"

	// Per-family permission audit kinds (cedar-credential-policy-01KQ8TDE WP07).
	// Each fires in addition to the universal kind above when the gate
	// is for the named resource family.
	KindBashPermission       Kind = "permission.bash"
	KindFilesystemPermission Kind = "permission.filesystem"
	KindCredentialPermission Kind = "permission.credential"
	KindToolPermission       Kind = "permission.tool"

	// MCP recipe lifecycle audit kinds (mission mcp-server-install-01KQ8TDP).
	//
	//   KindMCPRecipeAdded   — emitted by AddRecipe on success (WP10).
	//                          Payload: {recipe_id, source, transport}.
	//   KindMCPRecipeRemoved — emitted by RemoveRecipe on success (WP10).
	//                          Payload: {recipe_id, source}.
	//   KindMCPRecipeTested  — emitted by TestRecipe on completion (WP07).
	//                          Payload: {recipe_id, ok, transport,
	//                                    tool_count, resource_count,
	//                                    prompt_count, duration_ms}.
	KindMCPRecipeAdded   Kind = "mcp.recipe.added"
	KindMCPRecipeRemoved Kind = "mcp.recipe.removed"
	KindMCPRecipeTested  Kind = "mcp.recipe.tested"

	// Harness-self MCP audit kind (mission harness-self-mcp-onboarding-01KQ8TDU
	// WP10). Emitted by the in-process harness-self server on every tool
	// dispatch; payload values respect the per-tool Redact list (api_key
	// values are removed before emission).
	//
	//   KindHarnessSelfToolCalled — every harness_read_*/harness_write_*
	//                               tool call. Payload:
	//                               {tool_name, success, duration_ms}.
	//
	// KindHarnessSelfPolicyProposed/Written/Rejected were deleted by
	// mcp-connector-lifecycle-01PMMC01 WP01: they described a Cedar-policy
	// propose/accept/reject round-trip through
	// harness_write_propose_cedar_policy, a tool that was itself deleted
	// by the 2026-08-14 sweep — no emit site for any of the three ever
	// existed. See docs/unwired-ledger.md's harness-self entry and
	// kitty-specs/harness-self-attach-01PMHS01/research/attach-decision.md
	// — NOTE that path is gitignored and local-only, so the tracked
	// ledger entry above is the authoritative reference for anyone
	// reading from a clone. (The mission this comment previously named,
	// mcp-connector-lifecycle-01PMMC01, is archived and has no research/
	// directory; the file it cited never existed.)
	KindHarnessSelfToolCalled Kind = "harness-self.tool.called"

	// KindMigrationDriftDetected is emitted at most once per chassis boot
	// when the migration drift detector finds a discrepancy between the
	// harness_migrations ledger and the registered migration set (v0.5.1
	// migration-doctor) that a user needs to know about.
	//
	// EMITTED ONLY FOR id_mismatch / ledger_only. A report containing
	// nothing but code_only (ordinary pending) entries emits NOTHING —
	// see runMigrationDriftCheck in core/rpc/api.go, which branches on
	// severity rather than on len(report.Drifts).
	//
	// This comment previously specified a `{drift_count int, versions
	// []int}` payload and an "info" severity for the code_only-only case.
	// Neither was ever implemented, and the code_only-only case is now
	// deliberately silent rather than informational, so the contract is
	// restated here to match what the code does (corrected 2026-08-18,
	// upgrade-path-coverage-01PMUG01 FR-3b review). drift_count and the
	// version list travel on the rpc.MigrationDriftDetectedPayload the
	// broker publishes, not on audit.Entry, which has no payload field.
	KindMigrationDriftDetected Kind = "storage.migration.drift-detected"

	// KindKnobUnsupported is emitted when a RequestKnobs field is rejected
	// by the KnobPolicy because the target model does not support it AND
	// no silent fallback applies (provider-implementation-uniformity-01KQ8V4F WP08).
	// Payload: {session_id, provider, model_id, feature, hint}.
	KindKnobUnsupported Kind = "llm.knob.unsupported"

	// KindKnobDropped is emitted when a RequestKnobs field is silently
	// removed by the KnobPolicy because the target model does not support it
	// but the request can proceed without it
	// (provider-implementation-uniformity-01KQ8V4F WP08).
	// Payload: {session_id, provider, model_id, knob, reason}.
	KindKnobDropped Kind = "llm.knob.dropped"

	// KindSecretReferenceResolved is emitted by refs.Resolver on every
	// resolution attempt — successful or not — for a model-side @secret:
	// reference (model-secret-references-01KW7M5A WP03). The payload
	// carries only metadata (session, agent, tool, locator, destination
	// host, Cedar decision id, outcome, run id). No plaintext, no
	// resolved-bytes hash, and no fingerprint are ever included in the
	// payload.
	KindSecretReferenceResolved Kind = "secret_reference.resolved"
)

var builtIn = []Kind{
	KindRetentionStarted, KindRetentionCompleted, KindSaltRotated,
	KindSessionBranched, KindRawReplayOpened, KindRedactionSupersede,
	KindChainRebased, KindCancellation, KindError, KindTimeout,
	KindDatabaseOpened,
	// Universal permission audit kinds (cedar WP07).
	KindPermissionGranted, KindPermissionDenied, KindPermissionPrompted,
	KindPermissionTimeout, KindPermissionRevoked,
	// Per-family permission audit kinds (cedar WP07).
	KindBashPermission, KindFilesystemPermission, KindCredentialPermission,
	KindToolPermission,
	// MCP recipe lifecycle (WP07 + WP10).
	KindMCPRecipeAdded, KindMCPRecipeRemoved, KindMCPRecipeTested,
	// Harness-self MCP audit (harness-self-mcp-onboarding-01KQ8TDU WP10).
	KindHarnessSelfToolCalled,
	// Migration drift detector (v0.5.1 migration-doctor).
	KindMigrationDriftDetected,
	// Model-side secret reference audit (model-secret-references-01KW7M5A WP03).
	KindSecretReferenceResolved,
	// Knob policy audit (provider-implementation-uniformity-01KQ8V4F WP08).
	KindKnobUnsupported, KindKnobDropped,
}

var (
	mu       sync.RWMutex
	registry = func() map[Kind]struct{} {
		m := make(map[Kind]struct{}, len(builtIn))
		for _, k := range builtIn {
			m[k] = struct{}{}
		}
		return m
	}()
)

// Register adds k to the process-local registry. Idempotent. Returns
// ErrInvalid if k is malformed.
func Register(k Kind) error {
	if err := Validate(k); err != nil {
		return err
	}
	mu.Lock()
	registry[k] = struct{}{}
	mu.Unlock()
	return nil
}

// IsRegistered reports whether k is in the process-local registry.
// (Unknown but well-formed kinds are still accepted by Validate.)
func IsRegistered(k Kind) bool {
	mu.RLock()
	_, ok := registry[k]
	mu.RUnlock()
	return ok
}

// All returns every registered kind. Order is undefined.
func All() []Kind {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Kind, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	return out
}

// Validate checks the grammar of k:
//
//	<namespace>.<dotted.path>
//
// where namespace is one or more lowercase ASCII letters / digits /
// hyphens (matching the emitter-id namespace prefixes minus the slash
// boundary), and the dotted path is one or more dot-separated segments
// of the same character class. At least one dot must separate the
// namespace from a path segment.
//
// Forward-compat: well-formed kinds outside the registry are NOT
// rejected here; they are accepted as opaque (NFR-006). Callers
// distinguish "registered" via IsRegistered.
func Validate(k Kind) error {
	s := string(k)
	if s == "" {
		return fmt.Errorf("%w: empty", ErrInvalid)
	}
	if strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") {
		return fmt.Errorf("%w: leading/trailing dot in %q", ErrInvalid, s)
	}
	if strings.Contains(s, "..") {
		return fmt.Errorf("%w: empty segment in %q", ErrInvalid, s)
	}
	parts := strings.Split(s, ".")
	if len(parts) < 2 {
		return fmt.Errorf("%w: missing dotted path in %q", ErrInvalid, s)
	}
	for _, p := range parts {
		if p == "" {
			return fmt.Errorf("%w: empty segment in %q", ErrInvalid, s)
		}
		for _, r := range p {
			ok := (r >= 'a' && r <= 'z') ||
				(r >= 'A' && r <= 'Z') ||
				(r >= '0' && r <= '9') ||
				r == '-' || r == '_'
			if !ok {
				return fmt.Errorf("%w: invalid char %q in segment %q of %q", ErrInvalid, r, p, s)
			}
		}
	}
	return nil
}
