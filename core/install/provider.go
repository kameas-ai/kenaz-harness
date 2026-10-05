package install

import (
	"context"
	"errors"
)

// Kind names one install path. Values are wire keys (lowercase) and, for
// the four fleet catalog kinds, equal core/fleet's CatalogItemKind values
// (scripts/ci/check-install-provider-coverage.sh pins that every
// CatalogItemKind is a Kind here).
type Kind string

const (
	// KindMCPRecipe is an MCP server recipe: shipped, curated registry,
	// user-authored, imported or org-provisioned. Consumer: the MCP
	// supervisor (the persisted enabled list + the transport pools).
	KindMCPRecipe Kind = "mcp_recipe"
	// KindSkill is a user slash-command skill. Consumer: the slash
	// registry via slashcmd.LiveRegister / the SkillStore.
	KindSkill Kind = "skill"
	// KindWorkflow is a workflow definition. Consumer: the workflows
	// store (Store.Save) + the cron scheduler, read back by Workflows_List.
	KindWorkflow Kind = "workflow"
	// KindBundle is a kenaz.yaml bundle. Consumer: Bundle.Install from a
	// manifest directory, read back by Bundle_List (kenaz.lock).
	KindBundle Kind = "bundle"
	// KindAgentPack is a set of agent profiles. Consumer: the agents loader
	// (<dataDir>/agents) — or the kind is dropped (decision record §3).
	KindAgentPack Kind = "agent_pack"
)

// AllKinds is every install kind, registered or not. The coverage gate
// requires each one to have a registered provider or a dated allowlist
// entry naming its blocker.
var AllKinds = []Kind{KindMCPRecipe, KindSkill, KindWorkflow, KindBundle, KindAgentPack}

// Source is where an item comes from — the FR-4 source filter.
type Source string

const (
	// SourceBuiltin ships inside the app binary.
	SourceBuiltin Source = "builtin"
	// SourceRegistry is the curated registry shipped with the app.
	SourceRegistry Source = "registry"
	// SourceOrgCatalog is the fleet org catalog (org_public items, and
	// items the org provisions onto this device).
	SourceOrgCatalog Source = "org_catalog"
	// SourceTeamCatalog is the fleet catalog scoped to the caller's team
	// (team and private visibility).
	SourceTeamCatalog Source = "team_catalog"
	// SourceLocal is authored or imported on this device.
	SourceLocal Source = "local"
)

// RequirementKind is one thing a per-kind flow must collect before an
// install can succeed.
type RequirementKind string

const (
	RequirementKey       RequirementKind = "key"       // a secret value (API key, token)
	RequirementOAuth     RequirementKind = "oauth"     // an OAuth / device-code sign-in
	RequirementDirectory RequirementKind = "directory" // a directory pick
	RequirementConfig    RequirementKind = "config"    // a non-secret config value
	RequirementConsent   RequirementKind = "consent"   // an explicit warning acknowledgement
	RequirementFile      RequirementKind = "file"      // a credentials file placed on disk
)

// Requirement describes one input. Satisfied reports whether the device
// already holds it (a key already in the keychain, a config value already
// persisted), in which case the install needs nothing for it.
//
// The framework enforces only the input-bearing kinds (key, config,
// directory) — Install refuses with ErrRequirementsUnmet when a Required,
// unsatisfied one is absent from Inputs. consent / oauth / file are owned
// by the per-kind flow (the MCP key-prompt modal, the OAuth sign-in), which
// is why they are declared here: they route the UI to that flow.
type Requirement struct {
	Kind      RequirementKind `json:"kind"`
	Name      string          `json:"name"`
	Display   string          `json:"display,omitempty"`
	Required  bool            `json:"required"`
	Satisfied bool            `json:"satisfied"`
}

// State is an item's installed state AS ITS CONSUMER REPORTS IT. A provider
// must never derive it from a side directory or a cached flag: that is the
// badge-only install this framework exists to end.
type State struct {
	Installed bool `json:"installed"`
	// Version is the installed version when the consumer records one.
	Version string `json:"version,omitempty"`
	// UpdateAvailable means a newer version than Version is installable;
	// Framework.Update installs it.
	UpdateAvailable bool `json:"update_available,omitempty"`
	// Consumer names the runtime consumer that confirmed the state, e.g.
	// "MCP supervisor" — shown in the detail pane, logged on events.
	Consumer string `json:"consumer,omitempty"`
	// Detail is consumer-specific status text (e.g. the supervisor's
	// "running" / "failed").
	Detail string `json:"detail,omitempty"`
}

// Item is one installable (or installed) capability.
type Item struct {
	Kind        Kind   `json:"kind"`
	ID          string `json:"id"`
	Version     string `json:"version,omitempty"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Category    string `json:"category,omitempty"`
	Source      Source `json:"source"`
	// Keywords extend search beyond name/description (recipe aliases).
	Keywords []string `json:"keywords,omitempty"`
	State    State    `json:"state"`
	// ReadOnly items (org-provisioned / org-mandated) cannot be
	// uninstalled here; ReadOnlyReason says why. Framework.Uninstall
	// refuses them with ErrReadOnly.
	ReadOnly       bool          `json:"read_only,omitempty"`
	ReadOnlyReason string        `json:"read_only_reason,omitempty"`
	Requirements   []Requirement `json:"requirements,omitempty"`
}

// Filter narrows List. Zero fields match everything.
type Filter struct {
	Kind   Kind   `json:"kind,omitempty"`
	Source Source `json:"source,omitempty"`
	// Query matches name, description, id and keywords, case-insensitively.
	Query string `json:"query,omitempty"`
}

// Unavailable is a source a provider could not list, with the reason —
// rendered as a row, never as a hidden tab (FR-4, P-5). Reason is a
// stable code ("signed_out", "fleet_disabled", "error"); Message is the
// human text.
type Unavailable struct {
	Kind    Kind   `json:"kind"`
	Source  Source `json:"source"`
	Reason  string `json:"reason"`
	Message string `json:"message,omitempty"`
}

// Listing is a provider's (or the framework's merged) List result.
type Listing struct {
	Items       []Item        `json:"items"`
	Unavailable []Unavailable `json:"unavailable,omitempty"`
}

// Ref addresses one installable version.
type Ref struct {
	Kind    Kind   `json:"kind"`
	ID      string `json:"id"`
	Version string `json:"version,omitempty"`
}

// Inputs are what a per-kind flow collected. Secrets are never logged,
// never echoed in an event or an error, and never persisted by the
// framework — the provider hands them to its consumer's own secret store.
type Inputs struct {
	Secrets map[string]string
	Config  map[string]any
}

// VerifyMethod is how an item's integrity is established.
type VerifyMethod string

const (
	// VerifyBuiltin: the item ships inside the signed app binary.
	VerifyBuiltin VerifyMethod = "builtin"
	// VerifyLocal: authored, imported or provisioned on this device; the
	// user (or the org bundle applier, which verifies its own bundles) is
	// the authority.
	VerifyLocal VerifyMethod = "local"
	// VerifySignature: a fleet payload with a detached signature. The
	// framework's SignatureVerifier decides.
	VerifySignature VerifyMethod = "signature"
)

// Verification is what Provider.Verify gathered. For VerifySignature the
// provider fetches the payload once, here, and Install receives it back in
// InstallRequest.Verification — so the bytes installed are the bytes
// verified. Verified / Reason are filled by the framework.
type Verification struct {
	Method    VerifyMethod
	Payload   []byte
	Signature string
	Verified  bool
	Reason    string
}

// InstallRequest is what Provider.Install receives.
type InstallRequest struct {
	Ref          Ref
	Inputs       Inputs
	Verification Verification
}

// Provider is one install path behind the framework (FR-3). Every method
// is safe for concurrent use.
type Provider interface {
	// Kind is the kind this provider installs.
	Kind() Kind
	// List returns the provider's items with their consumer-derived State,
	// plus any source it could not reach (as Unavailable, not an error).
	List(ctx context.Context, f Filter) (Listing, error)
	// Detail returns one item. Unknown ids return an error wrapping
	// ErrNotFound.
	Detail(ctx context.Context, id string) (Item, error)
	// Requirements lists what an install of id needs (keys, OAuth,
	// directory picks, consent).
	Requirements(ctx context.Context, id string) ([]Requirement, error)
	// Verify reports how ref is verified and, for a signed payload, fetches
	// it. It must not install anything.
	Verify(ctx context.Context, ref Ref) (Verification, error)
	// Install registers the capability with its runtime consumer. The
	// returned value is per-kind detail the per-kind binding hands back
	// (e.g. the MCP supervisor's status snapshot); the framework does not
	// interpret it.
	Install(ctx context.Context, req InstallRequest) (any, error)
	// Uninstall unregisters id from its consumer.
	Uninstall(ctx context.Context, id string) error
	// InstalledState reads id's state from the consumer.
	InstalledState(ctx context.Context, id string) (State, error)
	// Update resolves the ref an update of id installs (the newest
	// installable version). Returns ErrNoUpdate when there is none; the
	// framework then runs the full install pipeline on the ref.
	Update(ctx context.Context, id string) (Ref, error)
}

var (
	// ErrUnknownKind: no provider is registered for the kind.
	ErrUnknownKind = errors.New("install: no provider registered for this kind")
	// ErrNotFound: the provider does not know the id.
	ErrNotFound = errors.New("install: item not found")
	// ErrNotConsumed: Provider.Install returned success but the consumer
	// does not list the capability — FR-1 refuses to call that installed.
	ErrNotConsumed = errors.New("install: the install did not reach its consumer")
	// ErrStillConsumed: Provider.Uninstall returned success but the
	// consumer still lists the capability.
	ErrStillConsumed = errors.New("install: the consumer still lists the capability after uninstall")
	// ErrReadOnly: the item is provisioned by the org and cannot be
	// removed on this device.
	ErrReadOnly = errors.New("install: this item is managed by your org")
	// ErrRequirementsUnmet: a required key / config value is neither on
	// the device nor in Inputs.
	ErrRequirementsUnmet = errors.New("install: required input missing")
	// ErrNoUpdate: there is no newer version to install.
	ErrNoUpdate = errors.New("install: no update available")
	// ErrVerificationFailed: the signature verifier rejected the payload.
	ErrVerificationFailed = errors.New("install: verification failed")
	// ErrUnverifiable: a signed payload arrived and no verifier is wired —
	// fail closed rather than install bytes nobody checked.
	ErrUnverifiable = errors.New("install: no signature verifier wired")
	// ErrKindMismatch: Register was given a provider for another kind.
	ErrKindMismatch = errors.New("install: provider kind does not match registration")
	// ErrDuplicateProvider: a provider is already registered for the kind.
	ErrDuplicateProvider = errors.New("install: provider already registered for this kind")
)
