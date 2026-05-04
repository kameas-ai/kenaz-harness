package cedar

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	cedar "github.com/cedar-policy/cedar-go"
)

// defaultPolicySource is the bundled starter policy. It is embedded so
// a fresh DataDir boot still has a reasonable allow-by-default posture
// without any on-disk file. Users who write their own `.cedar` files
// to <DataDir>/policy/ supplement (not replace) the bundled bundle —
// the engine concatenates the embedded source with every disk file in
// lexicographic order.
//
//go:embed policies/default_policy.cedar
var defaultPolicySource []byte

// defaultCredentialPolicySource is the embedded Credential-family
// default for mission cedar-credential-policy-01KQ8TDE (WP01). See the
// file's leading comment for the exact posture; the short form is
// "permit routine purposes, forbid manual_export, leave mcp_spawn
// implicit so the prompt flow handles it".
//
//go:embed policies/default_credential_policy.cedar
var defaultCredentialPolicySource []byte

// defaultBashPolicySource is the embedded BashCommand-family default.
// Header-only by design — every command starts as NotApplicable; the
// universal prompt flow takes over.
//
//go:embed policies/default_bash_policy.cedar
var defaultBashPolicySource []byte

// defaultFilesystemPolicySource is the embedded FilesystemOp-family
// default. Permits reads inside recipe-declared directories (via the
// `recipe_dir_match` context attribute); everything else falls
// through to the prompt flow.
//
//go:embed policies/default_filesystem_policy.cedar
var defaultFilesystemPolicySource []byte

// defaultToolPolicySource is the embedded Tool-family default for the
// universal permission system. Permits built-in `kaneaz__*` tools by
// matching on the `server_name` context attribute; non-builtin tools
// fall through to NotApplicable + prompt.
//
//go:embed policies/default_tool_policy.cedar
var defaultToolPolicySource []byte

// defaultWorkflowsPolicySource is the embedded Workflow-family default
// for mission workflows-01KQ8TDG (WP11). Posture: permissive defaults
// for run/save/delete; deny shell-bearing workflows on save in strict
// mode. See the file's leading comment for the full posture.
//
//go:embed policies/default_workflows_policy.cedar
var defaultWorkflowsPolicySource []byte

// DefaultPolicyName is the synthetic filename used when reporting the
// embedded policy to the frontend.
const DefaultPolicyName = "default_policy.cedar"

// DefaultCredentialPolicyName / DefaultBashPolicyName /
// DefaultFilesystemPolicyName / DefaultToolPolicyName are the
// synthetic filenames the per-family embedded bundles surface as in
// `Engine.ListPolicies`. They mirror the on-disk names so the audit
// view does not distinguish between embedded and on-disk variants of
// the same file (the user's accumulated `.cedar` files in
// `<DataDir>/policy/` compose with the embedded set).
const (
	DefaultCredentialPolicyName = "default_credential_policy.cedar"
	DefaultBashPolicyName       = "default_bash_policy.cedar"
	DefaultFilesystemPolicyName = "default_filesystem_policy.cedar"
	DefaultToolPolicyName       = "default_tool_policy.cedar"
	DefaultWorkflowsPolicyName  = "default_workflows_policy.cedar"
)

// PolicyDir is the directory under DataDir where user-authored
// .cedar files live. Engine.Reload walks this directory on each call.
const PolicyDir = "policy"

// PolicyFile is the parse-status report for one source file shown in
// the frontend's policy panel. Bytes are NOT included to keep the
// surface compact; the frontend opens the file directly if it needs
// to display source.
type PolicyFile struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Bytes    int    `json:"bytes"`
	Embedded bool   `json:"embedded"`
	ParseOK  bool   `json:"parse_ok"`
	ParseErr string `json:"parse_err,omitempty"`
}

// Options configures a new Engine.
type Options struct {
	// DataDir is the harness data root. The engine reads policy files
	// from filepath.Join(DataDir, PolicyDir). Required when LoadFromDisk
	// is true; ignored otherwise (in-memory engines for tests).
	DataDir string

	// LoadFromDisk controls whether Reload walks <DataDir>/policy/.
	// Tests that drive policy from raw bytes set this to false and
	// call SetPolicyText directly.
	LoadFromDisk bool

	// IncludeEmbedded controls whether the embedded default_policy is
	// concatenated alongside on-disk files. Defaults to true; set to
	// false when the harness is hosting a fully-custom policy bundle
	// and the embedded permits would be wrong.
	IncludeEmbedded bool

	// DefaultDeny flips the engine's posture when no policy matches.
	// false (default): NotApplicable (default-allow with audit).
	// true: Deny (fail-closed; the user opts in).
	DefaultDeny bool

	// Decisions is the audit-log seam. nil falls back to a fresh
	// MemoryDecisionStore with capacity 256.
	Decisions DecisionStore
}

// Engine is the harness-facing policy gate. Each gate hook in the
// harness calls Engine.Evaluate before performing a gated action; the
// returned Decision tells the caller whether to proceed.
//
// Engine is safe for concurrent use after construction. Reload swaps
// the active *cedar.PolicySet atomically so in-flight Evaluate calls
// see either the old or the new bundle, never a torn state.
type Engine struct {
	opts Options

	// policies is an atomic.Pointer to the active PolicySet so the
	// hot path (Evaluate) is lock-free.
	policies atomic.Pointer[cedar.PolicySet]

	// reloadMu serialises Reload so concurrent reload requests do not
	// race on directory walks or partial-set installation.
	reloadMu sync.Mutex

	// files captures the per-file parse status from the most recent
	// Reload. Read by ListPolicies; written under reloadMu.
	filesMu sync.RWMutex
	files   []PolicyFile

	decisions DecisionStore
}

// NewEngine constructs an Engine and runs an initial Reload. When
// LoadFromDisk is true and the policy directory does not exist, the
// engine treats the directory as empty (the embedded default still
// applies) and returns the engine without error. A genuine I/O or
// parse error is surfaced.
func NewEngine(opts Options) (*Engine, error) {
	e := &Engine{opts: opts}
	if opts.Decisions != nil {
		e.decisions = opts.Decisions
	} else {
		e.decisions = NewMemoryDecisionStore(0)
	}
	if !opts.LoadFromDisk && !opts.IncludeEmbedded {
		// Pure-test engine: caller will install policy via
		// SetPolicyText. Install an empty PolicySet so Evaluate is
		// well-defined.
		e.policies.Store(cedar.NewPolicySet())
		return e, nil
	}
	if err := e.Reload(context.Background()); err != nil {
		return nil, err
	}
	return e, nil
}

// SetPolicyText installs a single in-memory policy bundle from raw
// Cedar source. It is the test seam and the "engine boots even when
// every disk file is broken" path: callers that want a deterministic
// known-good bundle bypass the disk walk.
//
// The embedded default policy is NOT concatenated here — the caller
// is fully in charge of the bundle.
func (e *Engine) SetPolicyText(name string, source []byte) error {
	ps, err := cedar.NewPolicySetFromBytes(name, source)
	if err != nil {
		return fmt.Errorf("cedar: parse %s: %w", name, err)
	}
	e.policies.Store(ps)
	e.filesMu.Lock()
	e.files = []PolicyFile{{
		Name:    name,
		Bytes:   len(source),
		ParseOK: true,
	}}
	e.filesMu.Unlock()
	return nil
}

// Reload re-walks <DataDir>/policy/ and rebuilds the active PolicySet.
// Files are concatenated in lexicographic order; the embedded default
// is prepended when Options.IncludeEmbedded is true. Per-file parse
// failures are recorded in the per-file status report (visible via
// ListPolicies) but do NOT abort the reload — the engine prefers a
// degraded bundle over a hard outage. If EVERY source fails to parse,
// the prior PolicySet remains active.
func (e *Engine) Reload(ctx context.Context) error {
	e.reloadMu.Lock()
	defer e.reloadMu.Unlock()

	var (
		sources []policySource
		files   []PolicyFile
	)

	if e.opts.IncludeEmbedded {
		// The base agent-kernel-graph default plus the four
		// permission-family defaults from WP01. Order matters only
		// for cosmetic listing; Cedar's permit/forbid semantics are
		// commutative across files.
		embedded := []struct {
			name  string
			bytes []byte
		}{
			{DefaultPolicyName, defaultPolicySource},
			{DefaultCredentialPolicyName, defaultCredentialPolicySource},
			{DefaultBashPolicyName, defaultBashPolicySource},
			{DefaultFilesystemPolicyName, defaultFilesystemPolicySource},
			{DefaultToolPolicyName, defaultToolPolicySource},
			{DefaultWorkflowsPolicyName, defaultWorkflowsPolicySource},
		}
		for _, em := range embedded {
			sources = append(sources, policySource{
				name:  em.name,
				bytes: em.bytes,
			})
			files = append(files, PolicyFile{
				Name:     em.name,
				Bytes:    len(em.bytes),
				Embedded: true,
			})
		}
	}

	if e.opts.LoadFromDisk {
		dir := filepath.Join(e.opts.DataDir, PolicyDir)
		entries, err := os.ReadDir(dir)
		switch {
		case err == nil:
			// Sort lexicographically for deterministic ordering.
			sort.Slice(entries, func(i, j int) bool {
				return entries[i].Name() < entries[j].Name()
			})
			for _, ent := range entries {
				if ent.IsDir() {
					continue
				}
				if !strings.HasSuffix(strings.ToLower(ent.Name()), ".cedar") {
					continue
				}
				path := filepath.Join(dir, ent.Name())
				body, rerr := os.ReadFile(path)
				if rerr != nil {
					files = append(files, PolicyFile{
						Name:     ent.Name(),
						Path:     path,
						ParseOK:  false,
						ParseErr: rerr.Error(),
					})
					continue
				}
				sources = append(sources, policySource{
					name:  ent.Name(),
					bytes: body,
				})
				files = append(files, PolicyFile{
					Name:    ent.Name(),
					Path:    path,
					Bytes:   len(body),
					ParseOK: true,
				})
			}
		case errors.Is(err, os.ErrNotExist):
			// Empty bundle is fine; embedded policy still applies.
		default:
			// Recording the directory error AND continuing keeps the
			// engine usable when a permission glitch hides the dir
			// momentarily.
			files = append(files, PolicyFile{
				Name:     PolicyDir,
				Path:     dir,
				ParseOK:  false,
				ParseErr: err.Error(),
			})
		}
	}

	// Compile every source into a fresh PolicySet. We assign each
	// policy a deterministic ID so audit reasons reference a stable
	// name across reloads.
	ps := cedar.NewPolicySet()
	anyOK := false
	for srcIdx, src := range sources {
		fileSet, perr := cedar.NewPolicySetFromBytes(src.name, src.bytes)
		if perr != nil {
			// Mark the file's status as failed; keep going.
			for i := range files {
				if files[i].Name == src.name {
					files[i].ParseOK = false
					files[i].ParseErr = perr.Error()
					break
				}
			}
			continue
		}
		anyOK = true
		idx := 0
		for _, p := range fileSet.Map() {
			id := cedar.PolicyID(fmt.Sprintf("%s#%d#%d", src.name, srcIdx, idx))
			ps.Add(id, p)
			idx++
		}
	}

	if !anyOK && len(sources) > 0 {
		// Every source failed; keep the prior set active.
		e.filesMu.Lock()
		e.files = files
		e.filesMu.Unlock()
		return errors.New("cedar: every policy source failed to parse")
	}

	e.policies.Store(ps)
	e.filesMu.Lock()
	e.files = files
	e.filesMu.Unlock()
	_ = ctx
	return nil
}

// ListPolicies returns the per-file parse status from the last Reload.
// Empty until the first Reload completes.
func (e *Engine) ListPolicies() []PolicyFile {
	e.filesMu.RLock()
	defer e.filesMu.RUnlock()
	out := make([]PolicyFile, len(e.files))
	copy(out, e.files)
	return out
}

// RecentDecisions returns up to limit most-recent decisions, newest
// first. Backed by the configured DecisionStore.
func (e *Engine) RecentDecisions(limit int) []Decision {
	return e.decisions.Recent(limit)
}

// Evaluate runs the active policy bundle against (principal, action,
// resource, context) and returns a harness-shaped Decision. The
// decision is appended to the audit log before return.
//
// principal MAY be a zero-value EntityUID, in which case the
// canonical UserUID() is substituted (single-user invariant). resource
// MAY be a zero-value EntityUID; some actions (e.g. broad-allow on
// model_select) match a wildcard resource.
//
// Evaluate is the hot path — it MUST NOT block on I/O. It performs
// the cedar.Authorize call, classifies the result, and writes the
// audit record. Errors are not returned: a Cedar-level evaluation
// error is mapped to a Deny decision with the diagnostic message in
// Reason so the caller can refuse the action and surface why.
func (e *Engine) Evaluate(
	ctx context.Context,
	principal cedar.EntityUID,
	action string,
	resource cedar.EntityUID,
	contextAttrs map[cedar.String]cedar.Value,
) Decision {
	_ = ctx
	if principal.IsZero() {
		principal = UserUID()
	}
	ps := e.policies.Load()
	if ps == nil {
		// No bundle loaded — treat as Cedar-style empty (no permits,
		// no forbids). DefaultDeny decides the final outcome.
		ps = cedar.NewPolicySet()
	}
	contextAttrs = populateFamilyContext(action, resource, contextAttrs)
	req := cedar.Request{
		Principal: principal,
		Action:    ActionUID(action),
		Resource:  resource,
		Context:   cedar.NewRecord(contextAttrs),
	}
	dec, diag := cedar.Authorize(ps, cedar.EntityMap{}, req)
	out := fromCedar(action, principal, resource, dec, diag, e.opts.DefaultDeny)
	e.decisions.Append(out)
	return out
}

// PolicyDeniedError is the typed error returned by gate-hook helpers
// when Evaluate produces a Deny outcome. The frontend pattern-matches
// on this type so it can render a structured denial notice.
type PolicyDeniedError struct {
	// Decision is the full audit record explaining the denial.
	Decision Decision
}

// Error implements error.
func (e *PolicyDeniedError) Error() string {
	if e == nil {
		return "policy denied"
	}
	if e.Decision.MatchedPolicy != "" {
		return fmt.Sprintf(
			"policy denied %s on %s: %s (policy=%s)",
			e.Decision.Action,
			e.Decision.Resource,
			e.Decision.Reason,
			e.Decision.MatchedPolicy,
		)
	}
	return fmt.Sprintf(
		"policy denied %s on %s: %s",
		e.Decision.Action,
		e.Decision.Resource,
		e.Decision.Reason,
	)
}

// IsPolicyDenied reports whether err wraps a *PolicyDeniedError.
// Consumers use it instead of a direct type assertion to keep the
// error-chain semantics composable.
func IsPolicyDenied(err error) bool {
	var pe *PolicyDeniedError
	return errors.As(err, &pe)
}

// policySource pairs a virtual filename with its body. Internal type.
type policySource struct {
	name  string
	bytes []byte
}

// Context-attr keys consulted by the family-default policies. Exported
// so consumers in WP02–WP08 (bash gate, fs gate, tool gate) can build
// their context maps with the same keys the embedded policy bundle
// reads against — no chance of a typo splitting the namespace.
const (
	// Credential family.
	CtxKeyPurpose = "purpose"

	// BashCommand family.
	CtxKeyPattern       = "pattern"
	CtxKeyDangerousTier = "dangerous_tier"
	CtxKeyWorkingDir    = "working_dir"

	// FilesystemOp family.
	CtxKeyCanonicalPath  = "canonical_path"
	CtxKeyRecipeDirMatch = "recipe_dir_match"

	// Tool family.
	CtxKeyToolName         = "tool_name"
	CtxKeyServerName       = "server_name"
	CtxKeyPromptOnFirstUse = "prompt_on_first_use"

	// MCPRecipe family (mission mcp-server-install-01KQ8TDP WP10).
	CtxKeyRecipeID        = "recipe_id"
	CtxKeyRecipeCommand   = "recipe_command"
	CtxKeyRecipeTransport = "recipe_transport"

	// Harness-self session gating (harness-self-mcp-onboarding-01KQ8TDU WP06).
	// Callers populate this key with the calling session's Kind string
	// ("onboarding" | "chat") so the harness_write_* default policies can
	// gate by session kind without needing a separate mechanism.
	CtxKeySessionKind = "session_kind"
)

// populateFamilyContext fills in context-attribute defaults for the
// four WP01 resource families. Caller-provided keys ALWAYS win — this
// helper only fills in attributes the caller did not set, derived
// from the resource UID and action where possible.
//
// Why derive from the resource? Two reasons:
//
//  1. The bundled default policies reference these attrs in `when {
//     ... }` clauses; a request that omits a referenced attr produces
//     a Cedar evaluation error and lands as Deny. Filling sane
//     zero-values keeps the request well-formed.
//  2. Derived attrs (server_name from a "<server>__<tool>" UID,
//     pattern from a BashCommand UID, canonical_path from a
//     FilesystemOp UID) are 1:1 with the resource id. Computing them
//     once at the engine boundary lets callers pass less boilerplate
//     and ensures the audit trail records the same canonical view
//     the policy saw.
//
// The resulting map is the input the engine hands to cedar.NewRecord.
// Returns the same map when nothing needs filling so the hot path
// keeps zero allocations on the common "Allow via existing policy"
// shape.
func populateFamilyContext(
	action string,
	resource cedar.EntityUID,
	in map[cedar.String]cedar.Value,
) map[cedar.String]cedar.Value {
	// Cheap exit: actions outside the four WP01 families pass through
	// untouched. The agent-kernel-graph callers (tool_exec, file_*,
	// memory_write, model_select, network_request, state_*) do not
	// rely on family-attr derivation and many pass a nil map — we
	// MUST NOT widen their context.
	if !isFamilyAction(action) {
		return in
	}

	resourceID := string(resource.ID)
	out := in
	ensure := func(key, value string) {
		k := cedar.String(key)
		if out == nil {
			out = make(map[cedar.String]cedar.Value, 4)
		}
		if _, set := out[k]; set {
			return
		}
		out[k] = cedar.String(value)
	}
	ensureBool := func(key string, value bool) {
		k := cedar.String(key)
		if out == nil {
			out = make(map[cedar.String]cedar.Value, 4)
		}
		if _, set := out[k]; set {
			return
		}
		out[k] = cedar.Boolean(value)
	}

	switch action {
	case ActionUseCredential:
		// purpose is the only attr the credential default policy
		// inspects. We refuse to derive it from the UID because
		// callers might encode a non-purpose suffix (Spec leaves the
		// purpose enum closed; callers MUST pass it explicitly). The
		// engine simply ensures the key exists so a caller-omission
		// surfaces as NotApplicable rather than a Cedar eval error.
		ensure(CtxKeyPurpose, "")

	case ActionRunBashCommand:
		// Pattern derivation is the BashGate's responsibility (WP03);
		// we mirror the resource id into context.pattern so policies
		// can reference whichever they prefer.
		ensure(CtxKeyPattern, resourceID)
		ensure(CtxKeyWorkingDir, "")
		ensureBool(CtxKeyDangerousTier, false)

	case ActionReadFilesystem, ActionWriteFilesystem:
		ensure(CtxKeyCanonicalPath, resourceID)
		ensureBool(CtxKeyRecipeDirMatch, false)
		ensureBool(CtxKeyDangerousTier, false)

	case ActionUseTool:
		// Derive server_name + tool_name from the canonical
		// "<server>__<tool>" shape. The split is deterministic; a
		// resource id that doesn't contain "__" is treated as a
		// no-server tool name (server_name="") so the default policy
		// — which permits server_name=="kaneaz" — refuses to match.
		toolName := resourceID
		serverName := ""
		if idx := strings.Index(resourceID, "__"); idx >= 0 {
			serverName = resourceID[:idx]
			toolName = resourceID[idx+2:]
		}
		ensure(CtxKeyToolName, toolName)
		ensure(CtxKeyServerName, serverName)
		ensureBool(CtxKeyPromptOnFirstUse, false)
		// session_kind is populated by the caller for harness-self tool
		// calls (WP06); ensure a sane zero-value so the harness_write_*
		// forbid policy never produces a Cedar evaluation error from a
		// missing attribute.
		ensure(CtxKeySessionKind, "")

	case ActionAddRecipe, ActionSpawnRecipe:
		// Mirror the resource id as recipe_id; callers supply the
		// fuller recipe_command and recipe_transport via the context
		// map. We ensure zero-values here so the policy's `when`
		// clauses never see a missing attribute (Cedar evaluation error
		// on a missing attr lands as Deny).
		ensure(CtxKeyRecipeID, resourceID)
		ensure(CtxKeyRecipeCommand, "")
		ensure(CtxKeyRecipeTransport, "stdio")
	}

	return out
}

// LoadHarnessSnippets ingests the three default harness-self Cedar
// policy snippets into the engine's active PolicySet. Called at boot so
// the harness_read_default / harness_write_onboarding /
// harness_write_forbid policies are active without any on-disk file.
//
// snippets is a map of virtual filename → Cedar source. Each snippet is
// added under a deterministic synthetic PolicyID "harness-self/<filename>#<n>"
// so later Reload calls can coexist without stomping the harness entries.
//
// The function is additive and serialised under reloadMu. Calling it
// more than once replaces the previously installed harness-self entries.
//
// WP06: harness-self-mcp-onboarding-01KQ8TDU
func (e *Engine) LoadHarnessSnippets(snippets map[string][]byte) error {
	if len(snippets) == 0 {
		return nil
	}
	e.reloadMu.Lock()
	defer e.reloadMu.Unlock()

	// Start from the currently active PolicySet and extend it.
	current := e.policies.Load()
	if current == nil {
		current = cedar.NewPolicySet()
	}
	ps := current

	for name, body := range snippets {
		fileSet, err := cedar.NewPolicySetFromBytes(name, body)
		if err != nil {
			return fmt.Errorf("cedar: LoadHarnessSnippets parse %s: %w", name, err)
		}
		idx := 0
		for _, p := range fileSet.Map() {
			id := cedar.PolicyID(fmt.Sprintf("harness-self/%s#%d", name, idx))
			ps.Add(id, p)
			idx++
		}
		// Track in the files list so ListPolicies shows the snippets.
		e.filesMu.Lock()
		found := false
		for i, f := range e.files {
			if f.Name == name {
				e.files[i].ParseOK = true
				e.files[i].ParseErr = ""
				found = true
				break
			}
		}
		if !found {
			e.files = append(e.files, PolicyFile{
				Name:     name,
				Bytes:    len(body),
				Embedded: true,
				ParseOK:  true,
			})
		}
		e.filesMu.Unlock()
	}
	e.policies.Store(ps)
	return nil
}

// isFamilyAction reports whether action is one of the WP01
// universal-permission family actions or the WP10 MCP recipe family
// actions. Used by populateFamilyContext to keep agent-kernel-graph
// call sites unaffected.
func isFamilyAction(action string) bool {
	switch action {
	case ActionUseCredential,
		ActionRunBashCommand,
		ActionReadFilesystem,
		ActionWriteFilesystem,
		ActionUseTool,
		ActionAddRecipe,
		ActionSpawnRecipe:
		return true
	}
	return false
}
