package credstore

import (
	"context"
	"net/http"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/context/audit"
	"github.com/sigil-tech/kaneaz-harness/core/secrets"
	"github.com/sigil-tech/kaneaz-harness/core/secrets/ref"
)

// CredentialRef is a type alias for ref.CredentialReference. It is the
// indirect pointer to a credential — never the credential bytes
// themselves. Use it wherever a credential reference needs to be named.
type CredentialRef = ref.CredentialReference

// AccessPurpose enumerates the reasons a caller is requesting access to
// a credential. The purpose is recorded verbatim in every audit row
// (FR-003, FR-008).
type AccessPurpose string

const (
	// PurposeProviderCall is used when an LLM adapter is about to make
	// an outbound API call on behalf of a chat turn.
	PurposeProviderCall AccessPurpose = "provider_call"
	// PurposeProviderTest is used when the harness tests whether a
	// provider credential is valid (e.g. the "Test Connection" RPC).
	PurposeProviderTest AccessPurpose = "provider_test"
	// PurposeMCPSpawn is used when credstore injects credentials into an
	// MCP stdio child process environment at spawn time (FR-009).
	PurposeMCPSpawn AccessPurpose = "mcp_spawn"
	// PurposeToolDispatch is used when a built-in tool needs a
	// credential to perform its operation (e.g. a web-search tool that
	// needs an API key).
	PurposeToolDispatch AccessPurpose = "tool_dispatch"
	// PurposeManualExport is used when a human operator explicitly
	// requests a credential value through an administrative interface.
	// This purpose is always audited and may be policy-gated more
	// tightly than the others.
	PurposeManualExport AccessPurpose = "manual_export"
)

// Redacted is the wire shape returned by Peek. It carries everything the
// frontend needs to render a credential reference without ever exposing
// the raw bytes. The Display field follows the spec's length rules:
//   - Length > 12: "<first-4-runes>...<last-4-runes>" (e.g. "sk-a...XYZQ").
//   - Length 1–12: "••••••••" (8 bullets).
//   - Length 0 / resolution error: "••••••••" with Kind = "unset".
type Redacted struct {
	Display string `json:"display"`
	Kind    string `json:"kind"`
	Locator string `json:"locator"`
}

// IssueOption is a functional option for Issue.
type IssueOption func(*issueOptions)

type issueOptions struct {
	expiresAt  time.Time
	oneShot    bool
	requestID  string
	toolCallID string
}

// WithExpiry overrides the default 60-second handle TTL.
func WithExpiry(t time.Time) IssueOption {
	return func(o *issueOptions) { o.expiresAt = t }
}

// WithOneShot marks the handle as single-use; it is removed from the
// table after the first successful Use call.
func WithOneShot() IssueOption {
	return func(o *issueOptions) { o.oneShot = true }
}

// WithRequestID attaches an ambient request identifier to the handle so
// it appears in the audit row.
func WithRequestID(id string) IssueOption {
	return func(o *issueOptions) { o.requestID = id }
}

// WithToolCallID attaches a chat-runner tool-call identifier to the
// handle so it appears in the audit row (FR-008).
func WithToolCallID(id string) IssueOption {
	return func(o *issueOptions) { o.toolCallID = id }
}

// Store is the central credential broker interface. All raw-bytes access
// goes through Use or RoundTrip; callers never hold credential bytes
// directly.
//
// WP01 declares the interface; WP02 provides the concrete implementation
// returned by New.
type Store interface {
	// Issue allocates a new Handle for the given credential reference and
	// access purpose. The handle expires after 60 seconds by default
	// (override with WithExpiry). The raw credential bytes are NOT
	// resolved at issue time — resolution is deferred to Use.
	//
	// Returns ErrEmptyCredential if pre-flight resolution reveals the
	// credential is empty. Returns an error from secrets.Resolver if the
	// reference kind is not registered.
	Issue(ctx context.Context, cr CredentialRef, purpose AccessPurpose, opts ...IssueOption) (Handle, error)

	// Use looks up handle in the table, resolves the underlying
	// credential, and calls op with the raw bytes. The bytes are valid
	// only for the duration of the op call; they are zeroed immediately
	// after op returns (including on panic). Emits a KindCredentialAccessed
	// audit event regardless of op's outcome.
	//
	// Returns ErrUnknownHandle if handle is not in the table.
	// Returns ErrHandleExpired if the handle's TTL has elapsed.
	// Returns ErrUseAfterFree if a one-shot handle has already been consumed.
	Use(ctx context.Context, handle Handle, op func([]byte) error) error

	// Peek resolves the credential reference and returns redacted display
	// metadata. Peek does NOT emit an audit event and is NOT
	// policy-gated. It is the read path for the Settings → Providers
	// panel and the RPC redaction layer (FR-006, plan §5).
	Peek(ctx context.Context, cr CredentialRef) (Redacted, error)

	// RoundTrip issues a one-shot handle internally, resolves the
	// credential, injects the appropriate authorization header into req,
	// executes the request via the store's HTTP client, and returns the
	// response. The credential bytes are zeroed on the deferred path
	// regardless of whether the request succeeds or the stream is
	// cancelled. Emits a KindCredentialAccessed audit event (via Use).
	//
	// The header injected depends on cr.Kind and the scheme encoded by
	// the existing adapter conventions:
	//   - Authorization: Bearer <bytes>  (openai, openrouter, bedrock-bearer)
	//   - x-api-key: <bytes>             (anthropic)
	//   - api-key: <bytes>               (azure, future)
	RoundTrip(ctx context.Context, cr CredentialRef, req *http.Request) (*http.Response, error)

	// IssueForMCPSpawn reads the recipe's declared env_credentials, resolves
	// each one through Use with PurposeMCPSpawn, and returns a map of
	// environment variable name → value suitable for merging into
	// cmd.Env. The parent MUST zero the map values immediately after
	// cmd.Start returns. Emits one KindCredentialAccessed row per env var.
	//
	// Cedar policy gate is added in Mission B
	// (cedar-credential-policy-01KQ8TDE). This mission ships the
	// plumbing with a default-permit posture.
	IssueForMCPSpawn(ctx context.Context, recipeID string) (envvars map[string]string, err error)

	// SweepAuditLog deletes KindCredentialAccessed rows older than the
	// configured retention period. If CredentialAuditRetentionDays is 0
	// (default) the sweep is a no-op and returns (0, nil). Deletions are
	// paginated (LIMIT 5000 per batch) so a large backlog does not lock
	// the SQLite writer for too long.
	SweepAuditLog(ctx context.Context) (deleted int, err error)

	// Close stops the expiration-sweep goroutine and releases all
	// outstanding handles. After Close, any Use call returns
	// ErrUnknownHandle. Close is idempotent.
	Close() error
}

// Config holds the dependencies required to construct a Store. Zero
// values are filled with safe defaults in New where possible.
type Config struct {
	// Resolver is the secrets layer used to resolve CredentialRef values
	// to raw bytes. Required.
	Resolver secrets.ResolverAPI

	// Emitter receives audit events. May be nil — audit emission is
	// silently skipped when no emitter is wired (matches the existing
	// audit.Emit nil-safe contract).
	Emitter audit.Emitter

	// HTTPClient is used by RoundTrip. If nil, http.DefaultClient is used.
	HTTPClient *http.Client

	// CredentialAuditRetentionDays controls SweepAuditLog behaviour.
	// Zero (default) keeps rows forever. Non-zero deletes rows older than
	// N days.
	CredentialAuditRetentionDays int
}

// store is the concrete implementation returned by New. Its fields are
// populated in New; method bodies are stubs until WP02.
type store struct {
	cfg Config
}

// New constructs a Store from cfg. The returned Store's background
// expiration-sweep goroutine is started immediately; call Close to stop
// it. Returns an error if cfg.Resolver is nil.
//
// WP02 fills in the implementation. WP01 returns an error so callers
// know not to rely on a working store from this skeleton.
func New(_ Config) (Store, error) {
	return nil, errNotImplemented
}

// Ensure *store satisfies Store at compile time.
// (Commented out until WP02 wires the method bodies; the interface
// check would fail today because *store has no methods. WP02 uncomments
// this line as its first act.)
//
// var _ Store = (*store)(nil)

// Issue is not yet implemented. WP02 fills this in.
func (s *store) Issue(_ context.Context, _ CredentialRef, _ AccessPurpose, _ ...IssueOption) (Handle, error) {
	return 0, errNotImplemented
}

// Use is not yet implemented. WP02 fills this in.
func (s *store) Use(_ context.Context, _ Handle, _ func([]byte) error) error {
	return errNotImplemented
}

// Peek is not yet implemented. WP02 fills this in.
func (s *store) Peek(_ context.Context, _ CredentialRef) (Redacted, error) {
	return Redacted{}, errNotImplemented
}

// RoundTrip is not yet implemented. WP02 fills this in.
func (s *store) RoundTrip(_ context.Context, _ CredentialRef, _ *http.Request) (*http.Response, error) {
	return nil, errNotImplemented
}

// IssueForMCPSpawn is not yet implemented. WP02 fills this in.
func (s *store) IssueForMCPSpawn(_ context.Context, _ string) (map[string]string, error) {
	return nil, errNotImplemented
}

// SweepAuditLog is not yet implemented. WP02 fills this in.
func (s *store) SweepAuditLog(_ context.Context) (int, error) {
	return 0, errNotImplemented
}

// Close is not yet implemented. WP02 fills this in.
func (s *store) Close() error {
	return errNotImplemented
}

// Compile-time assertion: *store must implement Store.
var _ Store = (*store)(nil)
