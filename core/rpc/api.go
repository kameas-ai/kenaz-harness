// Package rpc is the Wails-binding façade for the harness frontend.
//
// HarnessAPI mirrors Kenaz's KenazAPI shape — top-level cross-cutting
// methods (ShellStatus, AppInfo) plus 12 view-scoped sub-interfaces
// returning stable Go pointers for the lifetime of the API value.
//
// DIRECTIVE_001: the frontend talks to core/ ONLY through this package.
// No core/ package imports anything from frontend/.
package rpc

import (
	"context"
	cryptorand "crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"
	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	contextaudit "github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/fleet"
	corenodes "github.com/kameas-ai/kenaz-harness/core/agentgraph/nodes"
	coreatt "github.com/kameas-ai/kenaz-harness/core/attachments"
	corecompaction "github.com/kameas-ai/kenaz-harness/core/compaction"
	compactionwiring "github.com/kameas-ai/kenaz-harness/core/compaction/wiring"
	corecontexts "github.com/kameas-ai/kenaz-harness/core/contexts"
	corecorpus "github.com/kameas-ai/kenaz-harness/core/corpus"
	coreupdate "github.com/kameas-ai/kenaz-harness/core/update"
	"github.com/kameas-ai/kenaz-harness/core/event"
	kindpkg "github.com/kameas-ai/kenaz-harness/core/event/kind"
	"github.com/kameas-ai/kenaz-harness/core/hooks"
	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	llmcap "github.com/kameas-ai/kenaz-harness/core/llm/capabilities"
	"github.com/kameas-ai/kenaz-harness/core/llm/credref"
	"github.com/kameas-ai/kenaz-harness/core/llm/personal"
	llmregistry "github.com/kameas-ai/kenaz-harness/core/llm/registry"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
	harnessmcp "github.com/kameas-ai/kenaz-harness/core/mcp/builtin/harness"
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
	"github.com/kameas-ai/kenaz-harness/core/mcp/stdio"
	corememory "github.com/kameas-ai/kenaz-harness/core/memory"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/units"
	autotitle "github.com/kameas-ai/kenaz-harness/core/sessions/autotitle"
	autotitlewiring "github.com/kameas-ai/kenaz-harness/core/sessions/autotitle/wiring"
	coreart "github.com/kameas-ai/kenaz-harness/core/artifacts"
	coreconv "github.com/kameas-ai/kenaz-harness/core/conversation"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/a2a"
	graphview "github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph/chat"
	onboardingview "github.com/kameas-ai/kenaz-harness/core/rpc/views/onboarding"
	artifactsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/artifacts"
	attachmentsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/attachments"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/audit"
	agentsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/agents"
	branchesview "github.com/kameas-ai/kenaz-harness/core/rpc/views/branches"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/bundle"
	cedarpolicyview "github.com/kameas-ai/kenaz-harness/core/rpc/views/cedarpolicy"
	compactionview "github.com/kameas-ai/kenaz-harness/core/rpc/views/compaction"
	contextsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/contexts"
	contextview "github.com/kameas-ai/kenaz-harness/core/rpc/views/contextview"
	corpusview "github.com/kameas-ai/kenaz-harness/core/rpc/views/corpus"
	dialsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/dials"
	hooksview "github.com/kameas-ai/kenaz-harness/core/rpc/views/hooks"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/llm"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/mcp"
	memoryview "github.com/kameas-ai/kenaz-harness/core/rpc/views/memory"
	nodesview "github.com/kameas-ai/kenaz-harness/core/rpc/views/nodes"
	permissionsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/permissions"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/policy"
	projectsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/projects"
	searchview "github.com/kameas-ai/kenaz-harness/core/rpc/views/search"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/sessions"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/shell"
	slashview "github.com/kameas-ai/kenaz-harness/core/rpc/views/slashcmd"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/tools"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/trust"
	updateview "github.com/kameas-ai/kenaz-harness/core/rpc/views/update"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/workflow"
	workflowsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/workflows"
	scheduledchatview "github.com/kameas-ai/kenaz-harness/core/rpc/views/scheduledchat"
	storageview "github.com/kameas-ai/kenaz-harness/core/rpc/views/storage"
	elicitview "github.com/kameas-ai/kenaz-harness/core/rpc/views/elicit"
	secretsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/secrets"
	planmodeview "github.com/kameas-ai/kenaz-harness/core/rpc/views/planmode"
	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
	coreslashcmd "github.com/kameas-ai/kenaz-harness/core/slashcmd"
	corebash "github.com/kameas-ai/kenaz-harness/core/tools/bash"
	coreskill "github.com/kameas-ai/kenaz-harness/core/tools/skill"
	coreplanmode "github.com/kameas-ai/kenaz-harness/core/tools/planmode"
	secretsref "github.com/kameas-ai/kenaz-harness/core/secrets/ref"
	credstoreRefs "github.com/kameas-ai/kenaz-harness/core/credstore/refs"
	sentryview "github.com/kameas-ai/kenaz-harness/core/rpc/views/sentry"
	fleetview "github.com/kameas-ai/kenaz-harness/core/rpc/views/fleet"
	tasksview "github.com/kameas-ai/kenaz-harness/core/rpc/views/tasks"
	coretasks "github.com/kameas-ai/kenaz-harness/core/tasks"
	catalogview "github.com/kameas-ai/kenaz-harness/core/rpc/views/catalog"
	syncview "github.com/kameas-ai/kenaz-harness/core/rpc/views/sync"
	cedarview "github.com/kameas-ai/kenaz-harness/core/rpc/views/cedar"
	sitesview "github.com/kameas-ai/kenaz-harness/core/rpc/views/sites"
	acpview "github.com/kameas-ai/kenaz-harness/core/rpc/views/acp"
	acppeers "github.com/kameas-ai/kenaz-harness/core/acp/peers"
	acpenvelope "github.com/kameas-ai/kenaz-harness/core/acp/envelope"
	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/eval"
	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
	"github.com/kameas-ai/kenaz-harness/core/usage"
	corewf "github.com/kameas-ai/kenaz-harness/core/workflows"
	wfcatalogpkg "github.com/kameas-ai/kenaz-harness/core/workflows/catalog"
	wfsched "github.com/kameas-ai/kenaz-harness/core/workflows/scheduler"
	schedulerPkg "github.com/kameas-ai/kenaz-harness/core/scheduler"
	"github.com/zalando/go-keyring"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// HarnessAPI is the boundary between the Wails-hosted Vue frontend and
// the Go core. Top-level cross-cutting methods (ShellStatus, AppInfo)
// live here; view-specific surfaces are accessed through stable,
// view-scoped sub-interfaces. Implementations MUST be safe for
// concurrent use.
type HarnessAPI interface {
	ShellStatus(ctx context.Context) (ShellStatus, error)
	AppInfo(ctx context.Context) (AppInfo, error)

	LLMConnector() llm.LLMConnectorAPI
	MCP() mcp.MCPAPI
	// MCPImport returns the clipboard-import RPC surface (mission
	// mcp-server-install-01KQ8TDP, WP08). It is a separate sub-API
	// rather than a method on MCPAPI so the existing MCP view's
	// stub-server contract stays minimal. May return nil when the boot
	// wiring did not configure the import dependencies (catalog +
	// data-dir); the binding layer handles nil with a typed
	// ErrImportNotConfigured.
	MCPImport() *mcp.ImportAPI
	A2A() a2a.A2AAPI
	Workflow() workflow.WorkflowAPI
	// Workflows is the agentic workflows surface (mission
	// workflows-01KQ8TDG, v0.3.0 beta). Distinct from Workflow
	// (scheduler/jobs) — the names will reconcile in a follow-up.
	Workflows() workflowsview.WorkflowsAPI
	Sessions() sessions.SessionsAPI
	Trust() trust.TrustAPI
	Context() contextview.ContextAPI
	Contexts() contextsview.ContextsAPI
	Bundle() bundle.BundleAPI
	Policy() policy.PolicyAPI
	Audit() audit.AuditAPI
	Settings() settings.SettingsAPI
	Memory() memoryview.MemoryAPI
	Hooks() hooksview.HooksAPI
	Projects() projectsview.ProjectsAPI
	Attachments() attachmentsview.AttachmentsAPI
	Artifacts() artifactsview.ArtifactsAPI
	Tools() tools.ToolsAPI
	Shell() shell.ShellAPI
	Slash() slashview.SlashAPI
	Corpus() corpusview.CorpusAPI
	Graph() graphview.API
	Compaction() compactionview.CompactionAPI
	Branches() branchesview.BranchesAPI
	// CedarPolicy exposes the cedarpolicy view-scoped RPC surface
	// (mission cedar-credential-policy-01KQ8TDE, WP02). It lists
	// loaded policy files, triggers reload, and surfaces recent
	// gate decisions to the frontend Policy panel.
	CedarPolicy() cedarpolicyview.CedarPolicyAPI

	// Permissions exposes the universal interactive permission RPC
	// surface (mission cedar-credential-policy-01KQ8TDE, WP02).
	// Resolves modal decisions from the four prompt topics, lists
	// accumulated grants, and revokes them.
	Permissions() permissionsview.PermissionsAPI
	Dials() dialsview.DialsAPI
	Nodes() nodesview.NodesAPI
	Search() searchview.SearchAPI
	// Update is the auto-update view (mission auto-update, v0.4.0 WP03).
	// Backed by core/update.Service; nil-service chassis path returns a
	// surface that surfaces ErrServiceUnavailable on every state-mutating
	// method.
	Update() updateview.UpdateAPI
	// Storage exposes the storage-health RPC surface (v0.5.1
	// migration-doctor). Surfaces drift between the live ledger and the
	// registered migration set; provides an automated repair for
	// id_mismatch entries. Wired when a real Core (storage.DB) is
	// available; otherwise returns ErrStorageUnavailable on every call.
	Storage() storageview.StorageAPI
	// CedarProposeResolve delivers a user decision (accept | reject)
	// for a pending cedar-policy proposal surfaced by CedarProposeModal
	// (harness-self-mcp-onboarding-01KQ8TDU WP07). requestID comes in on
	// the "cedar:propose-pending" broker topic.
	CedarProposeResolve(requestID, decision string) error

	// Onboarding exposes the first-run onboarding RPC surface (mission
	// harness-self-mcp-onboarding-01KQ8TDU WP08). The frontend's
	// OnboardingDialog binds here to drive the Phase-1 FSM, read
	// OnboardingState on boot, dismiss the dialog, and restart Phase 2
	// via "Reconfigure with assistant".
	Onboarding() onboardingview.OnboardingAPI

	// Elicit exposes the ask-user-question RPC surface (mission
	// ask-user-question-interactive-01KZNP3G WP04). The frontend's
	// AskUserQuestion dialog submits answers via Elicit_SubmitAnswer;
	// the kenaz__ask_user_question tool blocks on OpenDialog until
	// the answer arrives.
	Elicit() elicitview.ElicitAPI

	// ScheduledChat exposes the scheduled-chat-runs CRUD + dispatch surface
	// (mission scheduled-chat-runs-01KX5R8B, v0.10.0). The frontend's
	// Settings → Scheduled Chats panel creates and manages prompt-template
	// jobs fired by the existing core/scheduler cron engine.
	ScheduledChat() scheduledchatview.ScheduledChatAPI

	// Secrets exposes the model-accessible secrets RPC surface (mission
	// model-secret-references-01KW7M5A WP10). The frontend's
	// ModelAccessibleSecretsPanel reads SecretRows, exposes new secrets
	// via ExposeSecret, and revokes them via RevokeSecret. No plaintext
	// is ever returned to the frontend.
	Secrets() secretsview.SecretsAPI

	// Agents exposes the sub-agent profile registry RPC surface (mission
	// branch-subagent-interactive-01KZNP3B WP01). The Settings → Agents
	// panel lists, edits, and duplicates bundled + user-authored profiles.
	Agents() *agentsview.API

	// Planmode_Approve clears the plan_mode posture and allows the model
	// to continue with write-capable tools. Called by the frontend's
	// PlanApprovalModal when the user clicks "Approve & continue".
	// (plan-mode-posture-01KZNP3F WP05)
	Planmode_Approve(ctx context.Context, req planmodeview.ApproveRequest) (planmodeview.ApproveResponse, error)

	// Planmode_Discard clears the plan_mode posture and returns the model
	// to normal execution without approving its plan. The plan artifact
	// is retained for history. Called by the frontend's PlanApprovalModal.
	// (plan-mode-posture-01KZNP3F WP05)
	Planmode_Discard(ctx context.Context, req planmodeview.DiscardRequest) (planmodeview.DiscardResponse, error)

	// Planmode_Edit updates the plan artifact with edited markdown and
	// then approves. Called by the frontend's PlanApprovalModal inline
	// editor when the user clicks "Save & approve".
	// (plan-mode-posture-01KZNP3F WP05)
	Planmode_Edit(ctx context.Context, req planmodeview.EditRequest) (planmodeview.EditResponse, error)

	// Sessions_StartCapture begins recording an eval capture for the
	// given sessionID. Idempotent: calling on an already-active session
	// is a no-op. The capture file is written to
	// <DataDir>/eval-captures/<sessionID>.jsonl. (eval-harness-replay)
	Sessions_StartCapture(ctx context.Context, sessionID string) error

	// Sessions_StopCapture finalizes and closes the eval capture for
	// sessionID. No-op when no active capture exists. (eval-harness-replay)
	Sessions_StopCapture(ctx context.Context, sessionID string) error

	// Sentry exposes the crash-reporting RPC surface (mission
	// sentry-error-monitoring-01KX5R8G WP05). Provides GetLastFive,
	// GenerateLocalReport, and TestDSN for the Settings → Privacy panel.
	Sentry() sentryview.SentryAPI

	// Fleet exposes the fleet telemetry consent RPC surface (mission
	// fleet-otel-archival-01NDFSEX11 WP07). Provides GetTelemetryConsent
	// and SetTelemetryConsent for the Settings → Privacy panel.
	Fleet() fleetview.FleetAPI

	// Catalog exposes the fleet catalog publish/list/install/uninstall surface
	// (fleet-share-and-sync-01NDFSEX14 WP02). Backed by core/fleet/catalog.go.
	Catalog() catalogview.CatalogAPI

	// Sync exposes the per-category settings sync surface
	// (fleet-share-and-sync-01NDFSEX14 WP05). Backed by core/fleet/sync.go.
	Sync() syncview.SyncAPI

	// CedarPublish exposes the team Cedar policy publish surface
	// (fleet-share-and-sync-01NDFSEX14 WP07). Admin-gated.
	CedarPublish() cedarview.CedarAPI

	// Sites exposes the fleet-hosted sites RPC surface (sites-ui-01NSITE06).
	// The view is gated on the sites_hosting capability; it is the same
	// core/sites + core/fleet/sites.go layer used by the MCP server.
	Sites() sitesview.SitesAPI

	// Tasks exposes the background-task registry RPC surface
	// (background-task-monitor-01KZNP3C WP05). Provides List, Get, Tail,
	// Abort, and AbortBySession for the Tasks panel.
	Tasks() tasksview.TasksAPI

	// ACP exposes the ACP peer management + envelope dispatch surface
	// (acp-orchestration-integration-01NDFSEX06). Provides ListPeers,
	// TrustPeer, RevokePeer, Dispatch, and GetTrace.
	ACP() acpview.ACPAPI
}

// ShellStatus drives the Toolbar status pills + LegendBar live-rate
// indicators. Polled every 5 s while focused; future optimization
// replaces the poll with a `shell:status-changed` push event.
type ShellStatus struct {
	ActiveProvider string  `json:"activeProvider"` // FR-001f
	TrustTier      string  `json:"trustTier"`      // FR-001f
	HarnessBuild   string  `json:"harnessBuild"`   // FR-001f
	Connection     string  `json:"connection"`     // connecting | ready | degraded | lost (FR-013, FR-017)
	EventRate      float64 `json:"eventRate"`      // events/sec (FR-001g)
	PolicyApplied  bool    `json:"policyApplied"`  // FR-001e
	RedactionOn    bool    `json:"redactionOn"`    // FR-001e
	LocalFirstOn   bool    `json:"localFirstOn"`   // FR-001e
}

// AppInfo is read once on app start; cached frontend-side for the session.
type AppInfo struct {
	Build      string     `json:"build"`
	Commit     string     `json:"commit"`
	BuildTime  string     `json:"buildTime"`
	GoVersion  string     `json:"goVersion"`
	Platform   string     `json:"platform"`
	WindowSize WindowSize `json:"windowSize"`
	// PolicyEditorEnabled is true when HARNESS_POLICY_EDITOR_UI != "0".
	// Controls whether the /policy route registers and write-path RPCs are
	// available (cedar-policy-editor-ui-01KQ8TD6 WP01).
	PolicyEditorEnabled bool `json:"policyEditorEnabled"`
	// KeychainRotationEnabled is true when the HARNESS_KEYCHAIN_ROTATION env
	// var is not set to "off", "0", or "false". The frontend uses this to
	// hide the "Auto-resume after rotating an API key" Settings toggle and
	// the AuthFailureToast rotate button when the feature is disabled.
	// (provider-keychain-rotation-01KQ8TD9 WP07)
	KeychainRotationEnabled bool `json:"keychainRotationEnabled"`
	// CustomOpenAIEnabled is true when HARNESS_CUSTOM_OPENAI is not "0".
	// The frontend uses this to hide the "Custom OpenAI-compatible" kind
	// in the provider-add form when the feature is disabled.
	// (custom-openai-compatible-endpoint-01KQ8VN0 WP08)
	CustomOpenAIEnabled bool `json:"customOpenAIEnabled"`
	// Capabilities is the current fleet capability gate state, keyed by the
	// snake_case wire keys (e.g. "hosted_inference"). Empty map when the user
	// is signed out or fleet is disabled. Populated from the capability poller
	// so the frontend does not need a separate RPC call on first load.
	// (fleet-capability-surface-01NDFSEX09 WP11)
	Capabilities map[string]bool `json:"capabilities,omitempty"`
	// Tier is the user's current fleet subscription tier (e.g. "pro", "team",
	// "enterprise"). Empty string when signed out or fleet is disabled.
	Tier string `json:"tier,omitempty"`
}

// WindowSize mirrors the charter shape.
type WindowSize struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// API is the concrete HarnessAPI implementation backed by core.
// Currently a stub: each view accessor returns a no-op implementation
// stable for the lifetime of API. Real wiring lands in feature missions.
type API struct {
	core *core.Core

	// builtins holds the in-binary tool registry so the chat-input
	// `!cmd` shell-escape can dispatch directly to kenaz__bash without
	// going through the toolloop. Populated at boot from the same
	// registry the LLM tool catalog reads.
	builtins *toolloop.BuiltinRegistry

	// Stable view-accessor instances (plan §4.2).
	llmAPI      llm.LLMConnectorAPI
	mcpAPI      mcp.MCPAPI
	// mcpImportAPI is the clipboard-import sub-surface (WP08 of
	// mission mcp-server-install-01KQ8TDP). Wired in New when the
	// merged catalog + data-dir are available; nil otherwise (the
	// binding returns ErrImportNotConfigured).
	mcpImportAPI *mcp.ImportAPI
	a2aAPI      a2a.A2AAPI
	workflowAPI workflow.WorkflowAPI
	workflowsAPI workflowsview.WorkflowsAPI
	sessionsAPI sessions.SessionsAPI
	trustAPI    trust.TrustAPI
	contextAPI  contextview.ContextAPI
	contextsAPI contextsview.ContextsAPI
	bundleAPI    bundle.BundleAPI
	policyAPI    policy.PolicyAPI
	auditImpl    *audit.API
	auditAPI     audit.AuditAPI
	settingsImpl *settings.API
	settingsAPI  settings.SettingsAPI
	memoryAPI       memoryview.MemoryAPI
	hooksAPI        hooksview.HooksAPI
	projectsAPI     projectsview.ProjectsAPI
	attachmentsMgr  *coreatt.Manager
	attachmentsAPI  attachmentsview.AttachmentsAPI
	artifactsMgr    *coreart.Manager
	artifactsStore  coreart.Store
	artifactsAPI    artifactsview.ArtifactsAPI
	mediaStore      coreatt.MediaStore
	toolsAPI        tools.ToolsAPI
	shellImpl       *shell.API
	shellAPI        shell.ShellAPI
	slashAPI        slashview.SlashAPI
	corpusMgr       *corecorpus.Manager
	corpusAPI       corpusview.CorpusAPI
	graphMgr        *graphview.Manager
	graphAPI        graphview.API
	compactionAPI   compactionview.CompactionAPI
	convMgr         *coreconv.Manager
	branchesAPI     branchesview.BranchesAPI
	// cedarPolicyAPI is the policy-panel RPC surface (mission
	// cedar-credential-policy-01KQ8TDE, WP02). Constructed in New
	// when a real *cedar.Engine is available; nil falls back to the
	// cedarpolicy.NewAPI(nil) graceful-empty surface.
	cedarPolicyAPI  cedarpolicyview.CedarPolicyAPI
	// policyEditorEnabled mirrors the HARNESS_POLICY_EDITOR_UI env flag.
	// Read once at boot; cached here so AppInfo can report it without
	// an os.Getenv on every call (cedar-policy-editor-ui-01KQ8TD6 WP01).
	policyEditorEnabled bool
	// keychainRotationEnabled mirrors the HARNESS_KEYCHAIN_ROTATION env flag.
	// Read once at boot; cached here so AppInfo can report it without
	// an os.Getenv on every call (provider-keychain-rotation-01KQ8TD9 WP07).
	keychainRotationEnabled bool
	// customOpenAIEnabled mirrors the HARNESS_CUSTOM_OPENAI env flag.
	// Read once at boot (custom-openai-compatible-endpoint-01KQ8VN0 WP08).
	customOpenAIEnabled bool

	// cedarProposeResolver is the in-process resolver for pending
	// cedar:propose-pending requests (WP07 of
	// harness-self-mcp-onboarding-01KQ8TDU). Set by the onboarding
	// flow when it wires a CedarProposerImpl; nil falls back to a
	// stub that returns ErrCedarProposeNotWired.
	cedarProposeResolver CedarProposeResolver

	// permissionsAPI is the universal interactive-permission RPC
	// surface (mission cedar-credential-policy-01KQ8TDE, WP02). Backed
	// by a process-singleton *cedar.Registry shared with the gate
	// callers in WP03–WP06 (not yet wired). Until then the registry
	// has no producers and ListPending returns empty.
	permissionsAPI permissionsview.PermissionsAPI

	// promptRegistry is the process-singleton cedar prompt registry.
	// Held on the stack so future WPs (cedar WP03 bash gate, WP04 fs
	// gate, etc.) can pass it into their gate constructors without
	// re-plumbing through api.New.
	promptRegistry *cedar.Registry
	dialsAPI        dialsview.DialsAPI
	searchAPI       searchview.SearchAPI
	storageAPI      storageview.StorageAPI
	// memStoreRef is the long-term memory store held for the search adapter
	// (unified-search-01KX5R8C WP03). The main memory path (memoryAPI) is
	// already wired; this ref lets the search lazy-init access it without
	// re-opening the gob file.
	memStoreRef corememory.Store

	// Node manifest catalog (mission agent-kernel-graph-node-catalog;
	// WP07). The manager owns the resolved catalog + user-override
	// directory. The optional hot-reload watcher polls when the
	// chassis-level --enable-manifest-hot-reload flag is set.
	nodesMgr     *nodesview.Manager
	nodesAPI     nodesview.NodesAPI
	nodesWatcher *corenodes.Watcher

	// stdioPool is the production *stdio.Pool wired into newLLMStack.
	// Held on the API value so the tools view's InstallRecipe /
	// UninstallRecipe path can call OpenOne / CloseOne against the
	// same pool the toolloop dispatches against.
	stdioPool *stdio.Pool

	// usageMgr is the per-session token + cost aggregate store
	// (token-cost-telemetry-01KQ8TD7). Wired in New when a real
	// Core is available; noop manager when not.
	usageMgr usage.Manager

	// broker fans typed source channels to Wails event topics. Held for
	// the lifetime of the API value; per-view bridges (llm, sessions,
	// audit, mcp, …) emit through it so the privacy CI invariant —
	// only emitter.go / stream_broker.go call runtime.EventsEmit —
	// stays intact.
	broker *StreamBroker

	// eventBus is the in-process non-Wails emission sink.  It receives
	// every event the broker publishes via the MultiEmitter fan-out, so
	// served-mode WebSocket connections can subscribe to real-time pushes
	// without needing the Wails runtime context.  The desktop path is
	// unaffected: WailsEmitter still fires for every event.
	eventBus *EventBus

	// bindings is the Wails-reflected surface; held for the lifetime of
	// API so OnStartup can call SetContext on it.
	bindings *Bindings

	// updateAPI is the auto-update view (mission auto-update, v0.4.0
	// WP03). Wraps a core/update.Service plus an in-memory state
	// mirror for the StatusOutput shape.
	updateAPI *updateview.Manager
	// updateSvc is the concrete update.Service held so the
	// SetContext hook can kick off BackgroundPoll on the Wails-supplied
	// app context (the only context that can produce the broker emit
	// runtime.EventsEmit accepts).
	updateSvc coreupdate.Service
	// updatePollCancel cancels the BackgroundPoll goroutine on chassis
	// shutdown / context replacement.
	updatePollCancel context.CancelFunc

	// harnessServer is the in-process harness-self MCP server (WP04/WP05).
	// Constructed once in New with real manager adapters; held here so the
	// in-process transport (WP09) can attach it to the session's MCP pool
	// without re-constructing.
	harnessServer *harnessServer

	// onboardingAPI is the first-run onboarding view (WP08). Wired in
	// New when a real Core is available; falls back to a zero-config
	// stub that returns sensible defaults so the chassis-only test
	// fixture compiles.
	onboardingAPI onboardingview.OnboardingAPI

	// elicitAPI is the ask-user-question RPC surface (mission
	// ask-user-question-interactive-01KZNP3G WP04). Constructed in New
	// and wired with a concrete Delegate into the askuserquestion tool.
	elicitAPI *elicitview.API

	// secretsAPI is the model-accessible secrets RPC surface (mission
	// model-secret-references-01KW7M5A WP10). Backed by exposureIdx.
	secretsAPI secretsview.SecretsAPI
	// exposureIdx is the process-singleton ExposureIndex used by the
	// list_secrets and web_fetch built-in tools. Constructed once in
	// New() so every component that needs to read or write the exposure
	// set (builtins, secrets view, future slash commands) sees the same
	// instance.
	exposureIdx *secrets.ExposureIndex

	// wfScheduler is the cron-backed workflow scheduler
	// (workflows-agentic-01KW2D3X WP02). Started on SetContext; stopped
	// on Shutdown. nil when the workflows feature is disabled or when the
	// chassis has no DB.
	wfScheduler *wfsched.CronScheduler

	// scheduledChatAPI is the scheduled-chat-runs RPC surface
	// (mission scheduled-chat-runs-01KX5R8B, WP04). Wired in New when
	// a real Core with a DB is available; nil DB path returns ErrStoreUnavailable.
	scheduledChatAPI scheduledchatview.ScheduledChatAPI

	// agentsAPI is the sub-agent profile registry RPC surface
	// (branch-subagent-interactive-01KZNP3B WP01). Backed by core/agents
	// loaded from <DataDir>/agents/*.yaml + bundled profiles.
	agentsAPI *agentsview.API

	// planmodeAPI is the plan-mode approval RPC surface (mission
	// plan-mode-posture-01KZNP3F WP05). Exposes Approve, Discard, and
	// Edit to the frontend's PlanApprovalModal. Wired in New when a real
	// sessionsAPI + sessionsAPI is available; nil on the test harness path.
	planmodeAPI *planmodeview.API

	// evalRecorder is the per-session eval-capture writer (eval-harness-replay).
	// Wired in New when a real Core with a DataDir is available; nil on the
	// test-chassis path, in which case Sessions_StartCapture / StopCapture
	// return ErrEvalNotConfigured.
	evalRecorder *eval.Recorder

	// sentryAPI is the crash-reporting RPC surface (sentry-error-monitoring-
	// 01KX5R8G WP05). Provides GetLastFive, GenerateLocalReport, TestDSN.
	sentryAPI sentryview.SentryAPI

	// fleetAPI is the fleet telemetry consent RPC surface
	// (fleet-otel-archival-01NDFSEX11 WP07).
	fleetAPI fleetview.FleetAPI

	// catalogAPI is the fleet catalog publish/list/install/uninstall surface
	// (fleet-share-and-sync-01NDFSEX14 WP02).
	catalogAPI catalogview.CatalogAPI

	// syncAPI is the per-category settings sync surface
	// (fleet-share-and-sync-01NDFSEX14 WP05).
	syncAPI syncview.SyncAPI

	// cedarPublishAPI is the team Cedar policy publish surface
	// (fleet-share-and-sync-01NDFSEX14 WP07).
	cedarPublishAPI cedarview.CedarAPI

	// sitesAPI is the fleet-hosted sites RPC surface (sites-ui-01NSITE06).
	// Backed by core/sites/packager.go + core/fleet/sites.go.
	sitesAPI sitesview.SitesAPI

	// settingsSyncer is the per-category settings Syncer started by
	// registerSyncCategories. Held for Shutdown teardown (FR-001).
	settingsSyncer *corefleet.Syncer

	// ctxGraphSyncer is the fleet context-graph pull syncer started in New.
	// Held for Shutdown teardown (FR-011).
	ctxGraphSyncer *corefleet.ContextGraphSyncer

	// unitSyncer is the Phase-3 unified-Unit fleet sync engine (promote-as-MR
	// client + pull-conflict surface). Held for Shutdown teardown.
	// (unified-context-artifacts-01NCTXU01 / Phase 3)
	unitSyncer *corefleet.UnitSyncer

	// unitsMgr is the fleet-free unified Unit store manager. Held so the
	// fleet view's resolution/enshrine RPCs reach it.
	unitsMgr *units.Manager

	// contextsLib is the open Context Library. Held so the fleet merger
	// closure (FR-012) can call MergeFleetEntries without re-opening the lib.
	contextsLib *corecontexts.Library

	// taskReg is the background-task registry (background-task-monitor-01KZNP3C).
	// Created at boot with RecoverOrphansWithPIDCheck so orphaned running rows
	// are marked crashed before any new runs register (FR-003 / WP03).
	taskReg  *coretasks.Registry
	tasksAPI tasksview.TasksAPI

	// acpAPI is the ACP peer management + envelope dispatch surface
	// (acp-orchestration-integration-01NDFSEX06). Wired in New when the
	// ACP layer is available; falls back to NullAPI for graceful empty
	// operation on the test-chassis path.
	acpAPI acpview.ACPAPI
}

// Builtins returns the in-binary tool registry. Used by the chat-input
// `!cmd` shell-escape binding to dispatch directly to kenaz__bash.
// Concrete-type method; the HarnessAPI interface does not expose it
// because no view-scoped consumer needs it.
func (a *API) Builtins() *toolloop.BuiltinRegistry { return a.builtins }

// SetContext threads the Wails app context to the Bindings surface
// AND to the StreamBroker, which needs the OnStartup-supplied context
// so runtime.EventsEmit dispatches correctly (background contexts
// crash Wails). main.go calls this from OnStartup.
//
// The shell view also captures this ctx — runtime.BrowserOpenURL has
// the same context-validation behaviour as EventsEmit.
func (a *API) SetContext(ctx context.Context) {
	if a.bindings != nil {
		a.bindings.SetContext(ctx)
	}
	if a.broker != nil {
		a.broker.SetContext(ctx)
	}
	if a.shellImpl != nil {
		a.shellImpl.SetContext(ctx)
	}
	// Elicitation RPC bridge: thread the Wails context so OpenDialog can
	// emit events on TopicElicitPending via the broker. Without a valid
	// Wails context, EventsEmit would crash ("invalid context passed").
	if a.elicitAPI != nil {
		a.elicitAPI.SetContext(ctx)
	}
	// Start the workflow cron scheduler (workflows-agentic-01KW2D3X WP02).
	// SetContext is called with the Wails-supplied app context, which is
	// the correct lifetime for background goroutines. Idempotent.
	if a.wfScheduler != nil {
		a.wfScheduler.Start()
	}

	// Bootstrap lockdown state before any user-facing surface mounts so
	// the harness boots into locked state when fleet says so. Runs in a
	// goroutine so the SetContext critical path is never delayed by a
	// network round-trip. The Watcher's long-poll loop catches any state
	// that changes after boot. (fleet-emergency-lockdown-01NDFSEX12 WP02)
	if a.settingsImpl != nil {
		go func() {
			c := a.settingsImpl.FleetClientForBootstrap()
			if c != nil {
				fleet.BootstrapLockdownStatus(ctx, c)
			}
		}()
	}

	// Audit the bypass env var on every process start so operators can
	// detect runs that skipped the lockdown gate. Runs inline (fast: just
	// an os.Getenv + a ring-buffer Push). (fleet-emergency-lockdown-01NDFSEX12 WP03)
	fleet.AuditLockdownBypass(ctx, lockdownAuditEmitter{impl: a.auditImpl})

	// Boot-time migration drift detection (v0.5.1 migration-doctor).
	// Run in a goroutine so the SetContext critical path (which must
	// complete before the UI renders) is not delayed by the ledger read.
	// Emits KindMigrationDriftDetected into the audit log when N > 0
	// drifts are found so operators can correlate the incident via the
	// audit trail even before they open Settings → Health.
	if a.storageAPI != nil {
		driftCtx := ctx
		go func() {
			report, driftErr := a.storageAPI.GetMigrationDriftReport(driftCtx)
			if driftErr != nil {
				logging.L().Warn("migration.drift.detect.failed", "err", driftErr.Error())
				return
			}
			n := len(report.Drifts)
			if n == 0 {
				return
			}
			versions := make([]int, 0, n)
			for _, d := range report.Drifts {
				versions = append(versions, d.Version)
			}
			logging.L().Warn("migration.drift.detected",
				"count", n,
				"versions", fmt.Sprint(versions),
			)
			if a.auditImpl != nil {
				a.auditImpl.Push(audit.Entry{
					ID:        fmt.Sprintf("migration-drift-%d", len(versions)),
					Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
					Category:  "STORAGE",
					Subject:   string(kindpkg.KindMigrationDriftDetected),
					Trailing:  fmt.Sprintf("count=%d", n),
				})
			}
		}()
	}

	// Start the auto-update background poller on the Wails-supplied
	// app context. The 6h interval matches the WP01 spec; channel is
	// "stable" — switching to "prerelease" requires a separate UI
	// path that hasn't shipped yet. Cancel any prior poller so
	// repeated SetContext calls (test harness re-init) don't pile up
	// goroutines.
	if a.updateSvc != nil {
		if a.updatePollCancel != nil {
			a.updatePollCancel()
		}
		pollCtx, cancel := context.WithCancel(ctx)
		a.updatePollCancel = cancel
		go func() {
			if err := a.updateSvc.BackgroundPoll(pollCtx, 6*time.Hour, "stable"); err != nil &&
				!errors.Is(err, context.Canceled) {
				logging.L().Warn("update.poll.exit", "err", err.Error())
			}
		}()
	}
}

// Shutdown cancels the auto-update background poller and stops the
// workflow cron scheduler. main.go calls this from OnShutdown so all
// background goroutines exit cleanly. Safe to call when no poller is
// running.
func (a *API) Shutdown() {
	if a == nil {
		return
	}
	if a.updatePollCancel != nil {
		a.updatePollCancel()
		a.updatePollCancel = nil
	}
	if a.wfScheduler != nil {
		a.wfScheduler.Stop()
	}
	// Flush any active eval captures so partial files get a KindCaptureStop
	// record even on clean shutdown.
	if a.evalRecorder != nil {
		a.evalRecorder.StopAll()
	}
	// FR-001: stop the settings sync background poller.
	if a.settingsSyncer != nil {
		a.settingsSyncer.Stop()
	}
	// FR-011: stop the context-graph pull background poller.
	if a.ctxGraphSyncer != nil {
		a.ctxGraphSyncer.Stop()
	}
	// Phase-3: stop the unified-Unit pull background poller.
	if a.unitSyncer != nil {
		a.unitSyncer.Stop()
	}
	// Fleet background goroutines (capability poller, config poller, lockdown
	// watcher). StopFleetBackground is idempotent and nil-safe.
	if a.settingsImpl != nil {
		a.settingsImpl.StopFleetBackground()
	}
}

// ErrEvalNotConfigured is returned by Sessions_StartCapture /
// Sessions_StopCapture when the eval recorder was not wired at boot
// (e.g. test-chassis path where c == nil or DataDir is empty).
var ErrEvalNotConfigured = errors.New("rpc: eval capture not configured (no DataDir)")

// Sessions_StartCapture implements HarnessAPI. Begins recording the eval
// capture for sessionID to <DataDir>/eval-captures/<sessionID>.jsonl.
// Idempotent: repeated calls for the same session are no-ops.
func (a *API) Sessions_StartCapture(ctx context.Context, sessionID string) error {
	if a.evalRecorder == nil {
		return ErrEvalNotConfigured
	}
	return a.evalRecorder.StartCapture(ctx, sessionID)
}

// Sessions_StopCapture implements HarnessAPI. Finalizes and closes the eval
// capture for sessionID. No-op when no active capture exists for the session.
func (a *API) Sessions_StopCapture(ctx context.Context, sessionID string) error {
	if a.evalRecorder == nil {
		return ErrEvalNotConfigured
	}
	return a.evalRecorder.StopCapture(ctx, sessionID)
}

// New constructs a HarnessAPI implementation. Sub-interfaces start as
// stubs and are replaced by real impls as each feature mission lands.
//
// Sessions wiring: when c is non-nil, New wires the real
// session.Manager-backed impl; otherwise the surface falls back to a
// safe stub so test fixtures can call New(nil) without booting core.
//
// LLMConnector wiring:
//   - A connector Registry is constructed with the embedded capability
//     catalog and a credref bridge pointing at a memory secrets backend
//     so env-var-resolved API keys (e.g. ANTHROPIC_API_KEY) flow through
//     without bundle support.
//   - The view-scoped llm.API is constructed with a streamSinkAdapter
//     that wraps a lazily-created StreamBroker — chunks emit on the
//     "llm:stream-chunk" topic via the broker so the privacy CI
//     invariant (only emitter.go and stream_broker.go call
//     runtime.EventsEmit) stays intact.
func New(c *core.Core) *API {
	// Capture the user's login PATH and prepend it to the process PATH
	// before any tool construction or LookPath call site runs. macOS
	// app-bundle launches inherit a stripped PATH (/usr/bin:/bin:...)
	// so Homebrew + user-installed binaries are invisible to the bash
	// tool. The shell spawn happens once at boot (~50–200ms); the
	// merged PATH is process-wide so every subsequent exec.LookPath
	// and child process sees the user's full setup.
	if captured, err := corebash.CaptureLoginShellPath(context.Background()); err == nil {
		if changed, merged := corebash.AugmentProcessPATH(captured); changed {
			logging.L().Info("rpc.boot.path_augmented",
				"prepended_chars", len(captured),
				"merged_chars", len(merged),
			)
		}
	} else {
		logging.L().Warn("rpc.boot.path_augment_skipped", "err", err.Error())
	}

	media := newMediaStore(c)
	attMgr := newAttachmentsManager(c, media)
	artStore, artMgr := newArtifactsStack(c, media)

	// Token + cost telemetry manager (token-cost-telemetry-01KQ8TD7).
	// Backed by the same storage.DB as every other session-table writer.
	// usage.New returns a noop manager when c is nil or HARNESS_COST_TELEMETRY=off.
	var db storage.DB
	if c != nil {
		db = c.Storage()
	}
	usageMgr := usage.New(db)

	// Storage health view (v0.5.1 migration-doctor). Wired only when a
	// real Core (= a real storage.DB) is available; otherwise the API
	// returns ErrStorageUnavailable on every call so the Settings panel
	// can display a graceful "not available" state.
	var dataDir string
	if c != nil {
		dataDir = c.DataDir()
	}

	// Unified Unit store manager (unified-context-artifacts-01NCTXU01). Backed
	// by the same storage.DB; nil on the test chassis (no real storage.DB). The
	// Phase-3 fleet view resolution/enshrine RPCs reach it; the UnitSyncer is
	// wired later (once the fleet client is resolved).
	var unitsMgr *units.Manager
	if db != nil {
		unitsMgr = units.NewManager(units.NewSQLStore(db))
	}

	// Background-task registry (background-task-monitor-01KZNP3C WP05 / WP03).
	// Create early so RecoverOrphansWithPIDCheck marks orphaned running rows
	// crashed before any new task runs register (FR-003).
	var taskReg *coretasks.Registry
	var tasksAPI tasksview.TasksAPI
	{
		var taskStore coretasks.SQLStore
		if db != nil {
			type sqlHandle interface{ SQL() *sql.DB }
			if h, ok := db.(sqlHandle); ok {
				if rawDB := h.SQL(); rawDB != nil {
					taskStore = coretasks.NewSQLiteStore(rawDB)
				}
			}
		}
		logDir := ""
		if dataDir != "" {
			logDir = filepath.Join(dataDir, "task_logs")
		}
		taskReg = coretasks.NewRegistry(coretasks.Options{
			Store:  taskStore,
			LogDir: logDir,
			Logger: logging.L(),
		})
		// FR-003: mark orphaned running rows crashed at boot. Best-effort;
		// errors are absorbed — the registry operates in-memory-only on
		// failures.
		aliveCount := coretasks.RecoverOrphansWithPIDCheck(context.Background(), taskReg, logDir)
		logging.L().Info("rpc.boot.task_orphan_recovery",
			"alive_tasks", aliveCount,
		)
		tasksAPI = tasksview.NewAPI(taskReg)
	}

	a := &API{
		core:           c,
		a2aAPI:         &stubA2A{},
		workflowAPI:    &stubWorkflow{},
		sessionsAPI:    newSessionsAPI(c, attMgr, artStore, media, usageMgr),
		trustAPI:       &stubTrust{},
		contextAPI:     &stubContext{},
		policyAPI:      &stubPolicy{},
		projectsAPI:    newProjectsAPI(c),
		attachmentsMgr: attMgr,
		artifactsMgr:   artMgr,
		artifactsStore: artStore,
		mediaStore:     media,
		usageMgr:       usageMgr,
		storageAPI:     storageview.NewAPI(db, dataDir),
		unitsMgr:       unitsMgr,
		taskReg:        taskReg,
		tasksAPI:       tasksAPI,
		acpAPI:         acpview.NewNullAPI(),
	}
	a.attachmentsAPI = newAttachmentsAPI(c, attMgr)
	a.artifactsAPI = newArtifactsAPI(c, artStore, artMgr, media)
	a.eventBus = NewEventBus()
	a.broker = NewStreamBroker(NewMultiEmitter(WailsEmitter{}, &busEmitter{bus: a.eventBus}))

	// Cedar prompt registry — process-singleton shared by every gate
	// site (bash, fs, cred, tool) AND by the permissions view. Built
	// here, right after the broker, so both newLLMStack (bash gate) and
	// the cedar block below see the same instance. The dispatcher
	// closure emits each pending request on the family's broker topic
	// (`bash:permission-pending` / `fs:...` / `cred:...` / `tool:...`)
	// using the broker's OnStartup-captured context — that's the only
	// context Wails accepts for EventsEmit. Without this dispatcher
	// the registry enqueues but never notifies the frontend, so the
	// permission modal never renders and the gate hangs until timeout.
	a.promptRegistry = cedar.NewRegistry(cedar.WithDispatcher(
		cedar.PromptDispatcherFunc(func(_ context.Context, topic string, payload cedar.PendingRequest) {
			if a.broker == nil {
				return
			}
			// Project the typed surface into the flat PermissionRequest
			// shape the frontend modal binds to (`resource_display`,
			// `dangerous_tier`, etc.). The raw nested `surface` field
			// stays included so future modal features (e.g. arg-list
			// preview, working-dir display) can read it without a
			// second backend trip.
			a.broker.emitter.Emit(a.broker.EmitCtx(), topic, flattenPendingRequest(payload))
		}),
	))

	// Wire the Cedar gate for BulkPurge (F-001 security fix). The gate is
	// built from the same DataDir as every other Cedar gate in the chassis.
	// When DataDir is empty (e.g. test mode) buildCedarGate returns AllowAll
	// which is overridden by CheckAuditBulkPurge's fail-closed NotApplicable
	// handling — a nil gate passed to WithGate means "ungated" (test posture).
	var auditGate cedar.Gate
	if c != nil && c.DataDir() != "" {
		auditGate = buildCedarGate(c.DataDir())
	}
	a.auditImpl = audit.NewAPI(audit.WithSubscriber(a.broker), audit.WithGate(auditGate))
	a.auditAPI = a.auditImpl
	a.mcpAPI = mcp.NewAPI(mcp.WithSubscriber(a.broker))
	// MCP boot-time directory creation (mission mcp-server-install-01KQ8TDP,
	// WP10). Best-effort: a failure here must never prevent the chassis from
	// booting. The directory is needed by UserStore.Load; without it a fresh
	// install would see a missing-dir warning on every load tick.
	if c != nil && c.DataDir() != "" {
		mcpRecipesDir := filepath.Join(c.DataDir(), "mcp", "recipes")
		if err := os.MkdirAll(mcpRecipesDir, 0o700); err != nil {
			logging.L().Warn("rpc: boot: could not create mcp/recipes dir",
				"dir", mcpRecipesDir,
				"err", err.Error(),
			)
		}
	}
	// Build the merged catalog once so both TestRecipe and the import
	// surface share the same shipped + registry + user view.
	mergedCat := recipes.NewMergedCatalog(
		func() []recipes.Recipe { return recipes.Shipped().List() },
		func() []recipes.Recipe { return recipes.Registry().List() },
		nil, // user source wired by WP10 boot sequence
	)
	a.mcpAPI = mcp.NewAPI(mcp.WithSubscriber(a.broker), mcp.WithCatalog(mergedCat))
	// MCP clipboard-import surface (mission mcp-server-install-01KQ8TDP,
	// WP08). Wired only when we have a real Core (= a real DataDir);
	// rpc.New(nil) test harness leaves it nil and the binding returns
	// ErrImportNotConfigured.
	if c != nil && c.DataDir() != "" {
		a.mcpImportAPI = mcp.NewImportAPI(mcp.ImportConfig{
			Catalog: importCatalogReader{},
			DataDir: c.DataDir,
		})
	}
	contextsAPI, contextsLib := newContextsAPI(c)
	a.contextsAPI = contextsAPI
	a.contextsLib = contextsLib
	startContextsWatcher(contextsLib, a.broker)
	// Wire the attachments manager into the contexts view so AttachModule
	// can persist context_attachments rows. Uses a type assertion because
	// newContextsAPI always returns *contextsview.API (the interface is
	// widened only for the field type). A nil attMgr leaves the adder
	// unwired — AttachModule will still resolve module content but won't
	// persist an attachment row (test/nil-core path).
	if impl, ok := contextsAPI.(*contextsview.API); ok && attMgr != nil {
		impl.WithAttachmentAdder(&contextsAttachmentAdder{mgr: attMgr})
	}

	// Settings: file-backed when we have a user config dir; in-memory
	// fallback for the test harness path so New(nil) keeps working.
	var settingsStore settings.SettingsStore
	if fs, err := settings.NewFileStoreFromEnv(); err == nil {
		settingsStore = fs
	}
	settingsImpl := settings.NewAPI(settingsStore)
	a.settingsAPI = settingsImpl
	a.settingsImpl = settingsImpl

	// FR-008 (agent-loop-robustness-parity WP08): boot health error strings
	// collected during subsystem init. Passed to SetBootErrors at the end of
	// api.New so the frontend's BootHealthBanner can display targeted warnings.
	var bootMCPErr, bootSkillsErr, bootFleetErr string

	// Wire the fleet client (fleet-auth-foundation-01NDFSEX08 chassis-boot wire-
	// up). Without this every fleet RPC returns ErrFleetDisabled because
	// SettingsAPI.fleetClient() is nil. NewClient returns a nopClient when
	// HARNESS_FLEET_DISABLED=1, which preserves the OSS-first behaviour: the
	// settings RPC code still short-circuits via the isNop check.
	if fleetClient, ferr := fleet.NewClient(fleet.ClientOpts{DataDir: dataDir}); ferr == nil {
		settingsImpl.SetFleetClient(fleetClient, dataDir)
	} else {
		logging.L().Warn("fleet.client.init_error", "err", ferr.Error())
		bootFleetErr = ferr.Error()
	}

	// Wire the lockdown broker so fleet:lockdown:changed events reach the
	// frontend banner. Must be called after both a.broker and a.settingsImpl
	// are assigned. SetLockdownBroker is idempotent; if SetFleetClient was
	// already called (rare in tests) it updates the running Watcher's sink.
	// (fleet-emergency-lockdown-01NDFSEX12 WP02)
	if a.broker != nil {
		settingsImpl.SetLockdownBroker(chatBrokerAdapter{broker: a.broker})
	}

	// Threshold scheduler wiring (token-cost-telemetry-01KQ8TD7 WP06).
	// Built once; reads the dial fresh on every Manager.Add tail via
	// the LoadMonthlyCostNotifyUSD callback so changes to the setting
	// take effect on the next chat turn without restarting. The
	// publisher routes events through the same broker used by the
	// permission-pending topics, so the privacy-CI single-emitter
	// invariant stays intact.
	if mwc, ok := usageMgr.(usage.ManagerWithChecker); ok && a.broker != nil {
		thresholdReader := usage.ThresholdReader(func() (float64, error) {
			if settingsImpl == nil || settingsImpl.Store() == nil {
				return 0, nil
			}
			return settingsImpl.Store().LoadMonthlyCostNotifyUSD()
		})
		publisher := &thresholdPublisher{broker: a.broker}
		if checker, err := usage.NewCheckerFromManager(mwc, thresholdReader, publisher); err == nil {
			usageMgr.SetThresholdChecker(checker)
		} else {
			logging.L().Warn("usage.threshold.wire_failed", "err", err.Error())
		}
	}

	// LLM stack uses the settings store as the opt-in gate for
	// retrieval, and shares the memory store with the MemoryAPI so a
	// pin in the chat surface and a retrieval at send-time see the
	// same gob file.
	memStore := openMemoryStore(c)
	// Cedar gate-hook wiring (FR-026): wrap every memory.Store.Add with
	// cedar.CheckMemoryWrite. AllowAll is the boot-stage default; a
	// future engine-load path swaps in a real Cedar engine without
	// touching this wiring.
	if gs, ok := memStore.(corememory.GateSetter); ok && gs != nil {
		gs.SetGate(&memoryGateAdapter{gate: cedar.AllowAll{}})
	}
	personalForLLM := newPersonalStore(c)
	embedder := newEmbedder(c, personalForLLM, settingsImpl)
	memoryEnabled := func() bool {
		if settingsImpl == nil || settingsImpl.Store() == nil {
			return false
		}
		v, err := settingsImpl.Store().LoadMemory()
		if err != nil {
			return false
		}
		return v
	}
	retriever := corememory.NewRetriever(memStore, embedder, memoryEnabled, 0.7)

	// model-invoked-skills-catalog-01KZNP3E + user-slash-commands-01KQ8TD9:
	// user command Store + Dispatch are constructed early so they can be
	// passed to both newHooksStack (skill catalog hook) and newLLMStack
	// (__skill tool registration). Gated by HARNESS_USER_SLASHCMD
	// (default on, slash-commands WP09).
	var slashStore *coreslashcmd.Store
	var slashDispatch *coreslashcmd.Dispatch
	if c != nil && c.Storage() != nil && c.DataDir() != "" && coreslashcmd.UserSlashcmdEnabled() {
		slashStore = coreslashcmd.NewStore(c.Storage(), c.DataDir())
		slashDispatch = coreslashcmd.NewDispatch(slashStore, nil)

		// Install bundled skill templates on first launch (idempotent).
		// Best-effort: a failure here never prevents the chassis from booting.
		go func() {
			if err := coreskill.InstallBundledSkills(context.Background(), slashStore); err != nil {
				logging.L().Warn("slashcmd.bundled_skills.install_failed", "err", err.Error())
			}
		}()
	}

	// fleet-skills-sync-01NDFSEX18 WP01/WP02: SkillStore persists fleet-installed
	// skills to <DataDir>/slashcmds/*.json. Constructed early alongside slashStore
	// so BootLoad can run before the chassis serves requests. Declared here so the
	// fleet block below can wire SkillDeps onto slashAPI without a scope violation.
	var skillStore *coreslashcmd.SkillStore
	if c != nil && c.DataDir() != "" {
		skillStore = coreslashcmd.NewSkillStore(c.DataDir())
	}

	// model-secret-references-01KW7M5A WP10: construct the process-singleton
	// ExposureIndex. All secrets exposed via /secret add or the Settings
	// Secrets panel are stored here. The list_secrets and web_fetch built-in
	// tools, the refs.Resolver, and the secrets view all share this instance.
	a.exposureIdx = secrets.NewExposureIndex()
	a.secretsAPI = secretsview.NewAPI(a.exposureIdx)
	logging.L().Info("rpc.boot.exposure_index_created")

	hooksRunner, hookRegistry, hookBuiltins := newHooksStack(c, retriever, memStore, embedder)
	// Register skill-catalog pre_send hook so the model sees the
	// model-invokable commands at send time (model-invoked-skills-catalog-01KZNP3E WP03).
	if hookBuiltins != nil && slashStore != nil {
		hooks.RegisterSkillCatalogBuiltin(hookBuiltins, hooks.SkillCatalogDeps{
			Store: slashStore,
		})
	}

	confirmEachEnabled := func() bool {
		if settingsImpl == nil || settingsImpl.Store() == nil {
			return true
		}
		v, err := settingsImpl.Store().LoadConfirmEach()
		if err != nil {
			return true
		}
		return v
	}
	// Artifacts sink — wires the code-block detector at the
	// assistant-finalize site and the tool-output detector against
	// the toolloop's post-tool-use hook listener fan-out. The sink is
	// shared between the LLM view's Config.Artifacts and the
	// toolloop's RegisterPostListener registration so a single
	// instance owns both capture paths.
	var artifactSink artifactsview.ArtifactSink
	var artifactSinkConcrete *artifactsview.Sink
	if a.artifactsMgr != nil {
		cfgFn := func() coreart.CaptureConfig {
			cfg := coreart.DefaultCaptureConfig()
			if settingsImpl != nil && settingsImpl.Store() != nil {
				if loaded, err := settingsImpl.Store().LoadAll(); err == nil {
					cfg.AutoCaptureCodeBlocks = loaded.AutoCaptureCodeBlocks()
					cfg.AutoCaptureToolOutputs = loaded.AutoCaptureToolOutputs()
					cfg.CodeBlockMinLines = loaded.EffectiveCodeBlockMinLines()
					cfg.CodeBlockMinBytes = loaded.EffectiveCodeBlockMinBytes()
					// multimodal-io-extended-01KQ8TD2 WP02: image-capture dials.
					// Settings fields added below in this file; default ON (zero-value
					// of bool = false, and we invert via the Disabled bit pattern).
					cfg.AutoCaptureGeneratedImages = loaded.AutoCaptureGeneratedImages()
					cfg.MaxGeneratedImageBytes = loaded.EffectiveMaxGeneratedImageBytes()
				}
			}
			return cfg
		}
		artifactSinkConcrete = artifactsview.NewSinkConcrete(a.artifactsMgr, cfgFn, nil)
		// edit-file-artifact-sync-01KQ8TD5 WP05: wrap the concrete sink
		// with the edit-file sync pipeline. The wrapper intercepts
		// kenaz__edit_file post-tool calls when HARNESS_EDIT_FILE_ARTIFACT_SYNC=on
		// and the per-user settings dial is enabled, then captures a
		// post-edit snapshot of the file as an artifact with AbsolutePath set
		// in the SourceRef. The CoalesceBuffer deduplicates edits to the same
		// file within one turn.
		editSyncBuf := artifactsview.NewCoalesceBuffer()
		var editFileSyncEnabled artifactsview.EditFileArtifactSyncEnabled
		if settingsStore != nil {
			editFileSyncEnabled = func() bool {
				v, err := settingsStore.LoadEditFileArtifactSyncEnabled()
				if err != nil {
					return true // default-on: soft-fail to enabled
				}
				return v
			}
		}
		if swe := artifactsview.NewSinkWithEditSync(artifactSinkConcrete, editSyncBuf, editFileSyncEnabled); swe != nil {
			artifactSink = swe
		} else {
			artifactSink = artifactSinkConcrete
		}
	}

	// Bash output cache — shared between the bash built-in tool (writes
	// transcripts at Call time) and the agent-graph kernel's
	// read_bash_output executor (reads transcripts by run-id). Both
	// halves of the FR-057b loop bind to the SAME store instance so a
	// transcript written by a bash call is visible to a downstream
	// read_bash_output node in the same chat session.
	a_bashStore := corebash.NewStore()

	// Agent-graph subsystem (mission agent-kernel-graph; Bundle A WP06).
	// Constructed BEFORE the LLM stack so the chat-migration ChatRunner
	// can share the kernel + EnvDeps with the graph view's runtime.
	// Manager construction is best-effort; on failure the API surface
	// falls back to ErrManagerUnavailable so the chassis still boots.
	a.convMgr = newConversationManager(c)
	a.corpusMgr = newCorpusManager(c, embedder)
	a.graphMgr = newGraphManagerWithDeps(c, a.convMgr, a.corpusMgr, memStore, embedder, a_bashStore)

	stack := newLLMStack(c, a.broker, personalForLLM, hooksRunner, attMgr, confirmEachEnabled, artifactSink, artifactSinkConcrete, settingsImpl, a_bashStore, artMgr, a.graphMgr, a.promptRegistry, usageMgr, a.elicitAPI, slashDispatch, a.exposureIdx, a.sessionsAPI, contextsLib)
	a.llmAPI = stack.api
	a.stdioPool = stack.pool
	a.builtins = stack.builtins
	// long-turn-resilience-01KR3PRS WP03: now that both the chat
	// runner and the session manager are constructed, wire the
	// ResumeStarter onto the existing sessionsAPI so
	// Sessions_ResumeMessage can open continuation streams against
	// the partial assistant rows the runner persists on backend-error.
	if stack.chatRunner != nil && c != nil && c.SessionManager() != nil {
		starter := buildResumeStarter(stack.chatRunner, c.SessionManager(), "", "")
		if starter != nil {
			a.sessionsAPI = sessions.WithResumeStarter(a.sessionsAPI, starter)
		}
	}
	// autonomy-dial-01KR3M2A WP03: wire the AutonomyContextProvider so
	// Sessions_ResolveAutonomy folds global → project → session layers
	// using the live settings store + project manager. Tolerates a nil
	// store (returns the empty Layer for that side of the chain).
	if c != nil && c.SessionManager() != nil {
		store := settingsStore
		projects := c.ProjectManager()
		sessionMgr := c.SessionManager()
		ctxProvider := sessions.AutonomyContextFunc{
			Global: func(ctx context.Context) (autonomy.Layer, error) {
				if store == nil {
					return autonomy.Layer{}, nil
				}
				return store.LoadAutonomyProfile()
			},
			ProjectForSession: func(ctx context.Context, sessionID string) (autonomy.Layer, error) {
				rec, err := sessionMgr.Get(ctx, sessionID)
				if err != nil {
					return autonomy.Layer{}, nil
				}
				if rec.ProjectID == nil || *rec.ProjectID == "" || projects == nil {
					return autonomy.Layer{}, nil
				}
				layer, err := projects.GetAutonomyProfile(ctx, *rec.ProjectID)
				if err != nil {
					return autonomy.Layer{}, nil
				}
				return layer, nil
			},
		}
		a.sessionsAPI = sessions.WithAutonomyContext(a.sessionsAPI, ctxProvider)
	}
	// Bug #1 fix: wire TitleGenerator so Sessions_SuggestTitle works.
	// The manual "Suggest title" path uses the same autotitle.Generator
	// and wiring as the chat-runner's automatic post-run trigger, but is
	// attached here (on the sessions API) rather than inside the chat
	// runner.  A ProfileResolver that picks the first registered profile
	// as the fallback ensures the generator works with ANY provider the
	// user has configured (OpenRouter-only, Anthropic-only, etc.) and
	// picks whichever model the chat runner is already using so manual
	// titling matches the conversation experience.
	if stack.reg != nil {
		capturedPersonalStore := personalForLLM
		llmCaller := autotitlewiring.NewLLMCaller(stack.reg,
			autotitlewiring.WithProfileResolver(func(_ context.Context, profileID, modelOverride string) (string, string, bool) {
				// If a specific profileID was requested (e.g. forwarded
				// from a chat session context), use it unchanged.
				if profileID != "" {
					return profileID, modelOverride, true
				}
				// Fallback: pick the first personal provider profile so
				// a "Suggest title" click in any session (even one whose
				// profileID wasn't threaded down to this closure) still
				// calls through to a real model.  This ensures titling
				// matches the active chat model without hard-coding a kind.
				if capturedPersonalStore != nil {
					profs, perr := capturedPersonalStore.List()
					if perr == nil && len(profs) > 0 {
						return profs[0].ID, profs[0].Model, true
					}
				}
				return "", "", false
			}),
		)
		gen := autotitle.New(llmCaller)
		a.sessionsAPI = sessions.WithTitleGeneratorOpt(a.sessionsAPI, gen)
		logging.L().Info("sessions.titlegenerator.wired")
	}
	// Wire the broker into the sessions API so every session-list mutation
	// (create, rename, delete, move, title-set, title-cleared) emits
	// TopicSessionListChanged. The LeftRail's useSessions() composable
	// subscribes and debounces a refresh() call on receipt (v0.5.3 fix).
	if a.broker != nil {
		a.sessionsAPI = sessions.WithBrokerOpt(a.sessionsAPI, a.broker)
	}
	// Wire export dependencies (Cedar gate) at boot time so the Cedar
	// check is ready before the first Export call. The FilePicker is
	// intentionally left nil here; it is wired per-invocation in the
	// bindings layer via sessions.WithExportPicker because it captures
	// the Wails runtime context. The audit emitter is nil for now —
	// core/context/audit.Emitter is not yet bridged to the ring-buffer
	// path in the rpc layer (consistent with workflows, cedarpolicy,
	// branches which also pass nil today); tracked for follow-up
	// (session-export-01NDFSEX05 WP02).
	if c != nil {
		a.sessionsAPI = sessions.WithExportOpts(a.sessionsAPI, buildCedarGate(c.DataDir()), nil, nil)
	}
	if c != nil && a.stdioPool != nil {
		c.SetMCP(a.stdioPool)
		// Persisted-recipes bootstrap — Core.Start invokes this once
		// Storage() is up, so the pool is populated before the chat
		// surface accepts a turn (FR-030).
		// Pass the Cedar engine so AllowAlways grants are persisted to
		// disk (cedar-credential-policy follow-up: AllowAlways mcp_spawn).
		c.SetMCPRecipeBootstrap(makeMCPRecipeBootstrap(c, a.stdioPool, stack.secrets, a.promptRegistry, buildCedarEngineOrNil(c.DataDir())))
	}
	a.toolsAPI = newToolsAPI(c, stack.pool, stack.secrets, a.promptRegistry, a.cedarPolicyAPI)
	// Register the fsrequest built-in after toolsAPI is wired so the
	// tool's delegate can be the real (non-stub) implementation. The
	// tool is registered unconditionally; the EnabledFilter gates
	// dispatch based on the LoadFSRequestAccessEnabled setting.
	registerFSRequestTool(a.builtins, a.toolsAPI)
	// Wire the recipe-config trimmer into the permissions view now that
	// toolsAPI is available. The tools.API implements
	// permissions.RecipeConfigTrimmer via its TrimAllowedDir method.
	if toolsImpl, ok := a.toolsAPI.(interface {
		TrimAllowedDir(ctx context.Context, recipeID, path string)
	}); ok {
		if permsImpl, ok2 := a.permissionsAPI.(*permissionsview.API); ok2 {
			permsImpl.SetConfigTrimmer(trimmerAdapter{toolsImpl})
		}
	}
	a.shellImpl = shell.New(nil)
	a.shellAPI = a.shellImpl
	a.memoryAPI = memoryview.New(memoryview.Config{
		Store:    memStore,
		Embedder: embedder,
		Reader:   newMemoryMessageReader(c),
		Profiles: &personalProfileLister{store: personalForLLM},
	})
	// Keep a ref for the search adapter (unified-search-01KX5R8C WP03).
	a.memStoreRef = memStore
	if hookRegistry != nil {
		a.hooksAPI = hooksview.New(hooksview.Config{
			Registry: hookRegistry,
			Builtins: &hooksBuiltinDescriber{r: hookBuiltins},
		})
	} else {
		a.hooksAPI = hooksview.New(hooksview.Config{})
	}

	// Wire the bundle reader against the core data dir. nil core (test
	// harness path) leaves the impl with a nil reader — List returns an
	// empty slice and Get returns "not found", which is the contract the
	// frontend's empty-state path expects.
	bundleOpts := []bundle.Option{}
	if c != nil {
		bundleOpts = append(bundleOpts,
			bundle.WithReader(bundle.NewFSReader(c.DataDir())),
			bundle.WithWriter(bundle.NewFSWriter(c.DataDir())),
		)
		if cas, err := c.BundleCache(); err == nil && cas != nil {
			bundleOpts = append(bundleOpts, bundle.WithCAS(bundle.CASFromCache(cas)))
		}
	}
	a.bundleAPI = bundle.NewAPI(bundleOpts...)

	// Corpora subsystem (mission agent-kernel-graph; Bundle C). Wired
	// only when the chassis has a real DataDir + storage; otherwise the
	// view falls back to ErrManagerUnavailable so the frontend renders
	// an empty state. corpusMgr was constructed earlier so the graph
	// manager could thread it through as a kernel EnvDep.
	a.corpusAPI = corpusview.New(a.corpusMgr)

	// Branches subsystem (mission agent-kernel-graph; Bundle B WP07/08).
	// Wired only when storage is up — falls back to a nil-manager
	// surface (ErrManagerUnavailable) when c is nil so test harness
	// callers (New(nil)) don't crash.
	a.branchesAPI = branchesview.New(branchesview.Config{
		Conversations: a.convMgr,
		Sessions:      sessionManagerOrNil(c),
		Recommender:   newBranchRecommender(),
		// Broker enables LeftRail real-time updates on branch creation
		// (branch creates a new child session row): v0.5.3 fix.
		Broker: a.broker,
	})

	// Agent-graph view surface — graph manager already built above so
	// the chat-migration ChatRunner could share its kernel.
	a.graphAPI = graphview.New(a.graphMgr)

	// Cascading dials (Bundle E WP17). The view degrades to in-memory-
	// only when no kernel resumer is wired — the chassis still boots
	// and BumpAndResume returns ErrNoPause until a kernel binds.
	a.dialsAPI = dialsview.New(dialsview.Config{})

	// Workflows subsystem (mission workflows-01KQ8TDG, v0.3.0 beta).
	// Loads embedded builtin/*.yaml at boot; HARNESS_WORKFLOWS=off
	// disables the surface entirely.
	{
		disabled := strings.EqualFold(os.Getenv("HARNESS_WORKFLOWS"), "off") ||
			os.Getenv("HARNESS_WORKFLOWS") == "0"
		var catalog []corewf.Workflow
		if !disabled {
			catalog, _ = corewf.LoadBuiltins()
		}
		// WP07: thread the WP06 SQLite storage layer through so Save +
		// Delete can persist user workflows. nil DB (test chassis) leaves
		// Store unset; the view returns ErrStorageUnavailable on those
		// methods rather than crashing.
		var wfStore corewf.Store
		if db != nil && !disabled {
			wfStore = corewf.NewSQLiteStore(db)
		}
		// WP02 (workflows-agentic-01KW2D3X): cron scheduler with DB
		// persistence. Constructed before the view so the Config.Scheduler
		// field can reference it. Nil when disabled or no DB.
		var sched *wfsched.CronScheduler
		if !disabled && db != nil {
			schedStore := wfsched.NewSQLiteStorage(db)
			var err error
			sched, err = wfsched.New(context.Background(), wfsched.Config{
				Store: schedStore,
			})
			if err != nil {
				logging.L().Warn("wf.scheduler.init_failed", "err", err.Error())
				sched = nil
			} else {
				a.wfScheduler = sched
				logging.L().Info("wf.scheduler.init_ok")
			}
		}
		// WP03 (workflows-agentic-01KW2D3X): catalog backend wired with
		// the same Store + Scheduler constructed above so Install can
		// persist and arm schedules. nil Store / Scheduler degrade
		// gracefully inside the catalog implementation.
		wfCatalog := wfcatalogpkg.New(wfcatalogpkg.Config{
			Store:     wfStore,
			Scheduler: sched,
		})
		a.workflowsAPI = workflowsview.New(workflowsview.Config{
			Engine:          corewf.NewEngine(),
			Catalog:         catalog,
			Publisher:       brokerPublisher{broker: a.broker},
			Disabled:        disabled,
			Store:           wfStore,
			Scheduler:       sched,
			WorkflowCatalog: wfCatalog,
		})
	}

	// Slash-command surface — registry constructed after the workflows
	// subsystem so the /wf gateway can reference a.workflowsAPI. A
	// construction failure soft-fails to a nil-registry surface; the
	// chassis still boots and Execute returns a friendly "not wired"
	// error per command.
	//
	// slashStore + slashDispatch are constructed earlier (before newHooksStack)
	// so the skill-catalog builtin hook is already registered on hookBuiltins.
	// When either is nil (test-chassis path or HARNESS_USER_SLASHCMD=false),
	// the view degrades to the registry-only API which returns "not wired"
	// for user commands.
	{
		slashRegistry := newSlashRegistry(c, a.llmAPI, memStore, embedder, a.branchesAPI, a.workflowsAPI, a.exposureIdx)
		if slashStore != nil && slashDispatch != nil {
			a.slashAPI = slashview.NewWithStore(slashRegistry, slashStore, slashDispatch)
			logging.L().Info("rpc.slashcmd.user_wired",
				"data_dir", c.DataDir())
		} else {
			a.slashAPI = slashview.New(slashRegistry)
			logging.L().Info("rpc.slashcmd.user_skipped",
				"reason", "no core / no storage / disabled by flag")
		}

		// fleet-skills-sync-01NDFSEX18 WP01: BootLoad fleet skills from disk
		// into the registry at startup. Best-effort: errors are logged but do
		// not block the chassis from booting.
		if skillStore != nil {
			go func() {
				n, err := coreslashcmd.BootLoad(skillStore, slashRegistry)
				if err != nil {
					logging.L().Warn("slashcmd.skills.boot_load.failed", "err", err.Error())
				} else if n > 0 {
					logging.L().Info("slashcmd.skills.boot_load.ok", "count", n)
				}
			}()
		}

		// fleet-skills-sync-01NDFSEX18 WP05: wire skill refs into the fleet
		// settings state so the compositeConfigApplier can call
		// fleet.ApplyMandatedSkills when a config bundle carries mandated_skills.
		if skillStore != nil && a.settingsImpl != nil {
			a.settingsImpl.SetSkillRefs(skillStore, slashRegistry)
		}
	}

	// Auto-update subsystem (mission auto-update, v0.4.0 WP03).
	// Constructs the production core/update.Service when a real Core
	// is available; the test-chassis path (c == nil / empty DataDir /
	// empty BuildVersion) leaves both updateSvc and updateAPI nil, in
	// which case the Update() accessor falls back to a graceful-empty
	// surface that returns ErrServiceUnavailable on every state-mutating
	// method.
	if c != nil && c.DataDir() != "" && c.BuildVersion() != "" {
		svc, err := coreupdate.NewService(coreupdate.Config{
			CurrentVersion: c.BuildVersion(),
			DataDir:        c.DataDir(),
			Publisher:      brokerPublisher{broker: a.broker},
		})
		if err != nil {
			logging.L().Warn("update.service.init_failed", "err", err.Error())
		} else {
			a.updateSvc = svc
			a.updateAPI = updateview.New(updateview.Config{
				Service:        svc,
				Publisher:      brokerPublisher{broker: a.broker},
				CurrentVersion: c.BuildVersion(),
			})
			logging.L().Info("update.service.init_ok")
		}
	} else {
		// Log a loud warning so that misconfiguration (missing BuildVersion
		// or DataDir) is never silent. This is the class of bug that silently
		// broke auto-update in v0.4.0–v0.4.2.
		logging.L().Warn("update.service.skipped",
			"core_nil", c == nil,
			"data_dir_empty", c == nil || c.DataDir() == "",
			"build_version_empty", c == nil || c.BuildVersion() == "",
		)
	}

	// Node manifest catalog (mission agent-kernel-graph-node-catalog
	// WP07). Loads shipped manifests + user-overrides at chassis boot;
	// the hot-reload watcher is only started when the chassis-level
	// `--enable-manifest-hot-reload` flag is enabled (FR-023).
	a.nodesMgr, a.nodesAPI = newNodesStack(c)
	if c != nil && c.HotReloadEnabled() && a.nodesMgr != nil {
		a.nodesWatcher = startNodesWatcher(c, a.nodesMgr)
	}

	// CedarPolicy view (mission cedar-credential-policy-01KQ8TDE, WP02 + WP09).
	// Constructs a process-singleton *cedar.Engine so the policy-panel
	// RPC surface can list loaded policy files, surface recent decisions,
	// and (WP09) write/revoke `<family>_allow_*.cedar` snippets with an
	// engine reload after each mutation. nil Core / empty DataDir falls
	// back to NewAPIWithDataDir(nil, "") which serves empty slices and
	// rejects snippet writes with a typed error.
	{
		var cedarDataDir string
		if c != nil {
			cedarDataDir = c.DataDir()
		}
		var cedarEng cedarpolicyview.Engine
		if eng := buildCedarEngineOrNil(cedarDataDir); eng != nil {
			if e2, ok := any(eng).(cedarpolicyview.Engine); ok {
				cedarEng = e2
			}
		}
		// Read HARNESS_POLICY_EDITOR_UI once at boot. Default = on ("").
		// Set to "0" or "false" to disable editor write paths.
		editorEnv := os.Getenv("HARNESS_POLICY_EDITOR_UI")
		policyEditorEnabled := editorEnv != "0" && editorEnv != "false"
		a.policyEditorEnabled = policyEditorEnabled
		a.cedarPolicyAPI = cedarpolicyview.NewAPIWithOptions(cedarEng, cedarDataDir, nil, policyEditorEnabled)

		// Read HARNESS_KEYCHAIN_ROTATION once at boot. Default = on ("").
		// Set to "off", "0", or "false" to disable the rotation UI.
		// (provider-keychain-rotation-01KQ8TD9 WP07)
		kcrEnv := os.Getenv("HARNESS_KEYCHAIN_ROTATION")
		switch kcrEnv {
		case "off", "0", "false":
			a.keychainRotationEnabled = false
		default:
			a.keychainRotationEnabled = true
		}

		// Read HARNESS_CUSTOM_OPENAI once at boot. Default = on ("").
		// Set to "0" to disable the custom endpoint adapter + UI.
		// (custom-openai-compatible-endpoint-01KQ8VN0 WP08)
		customOpenAIEnv := os.Getenv("HARNESS_CUSTOM_OPENAI")
		a.customOpenAIEnabled = customOpenAIEnv != "0"

		// Permissions view — uses the process-singleton prompt registry
		// constructed at api.New() time (right after the broker) so the
		// gate sites (bash, fs, cred, tool) and the permissions view
		// share one pending-request map. Without sharing, a Resolve()
		// call would hit a different registry from the one the gate
		// enqueued in, and the resolution would never reach the waiter.
		a.permissionsAPI = permissionsview.New(permissionsview.Config{
			DataDir:  cedarDataDir,
			Registry: a.promptRegistry,
			// Engine left nil for now — RevokeGrant skips the reload
			// gracefully when the engine is unset.
			// ConfigTrimmer is wired after toolsAPI is constructed; see
			// the wiring step below that calls setPermissionsConfigTrimmer.
		})
	}

	// ACP peer management + envelope dispatch (mission
	// acp-orchestration-integration-01NDFSEX06). Wire the real API when
	// DataDir is available; keep the NullAPI stub on the test-chassis
	// path (c == nil or empty DataDir) so all five verbs return a clear
	// "not configured" error rather than panicking.
	if c != nil && c.DataDir() != "" {
		acpReg := acppeers.NewRegistry(nil, acppeers.NoopEmitter{})
		acpEnv := acpenvelope.New()
		acpOpts := acpview.Options{
			// Audit emitter — bridge the audit API ring buffer.
			Audit: &acpAuditBridge{impl: a.auditImpl},
		}
		// Wire the Cedar engine so the acp_send and acp_receive gates
		// actually enforce policy. buildCedarEngineOrNil returns nil on
		// construction failure (logged as a warning); the API tolerates
		// nil and falls back to permissive (default-allow posture).
		if eng := buildCedarEngineOrNil(c.DataDir()); eng != nil {
			acpOpts.Cedar = acpview.NewEngineAdapter(eng)
		}
		a.acpAPI = acpview.NewAPI(acpReg, acpEnv, acpOpts)
	}

	// Elicitation view + ask_user_question tool bridge (mission
	// ask-user-question-interactive-01KZNP3G WP04). The elicitAPI is
	// constructed unconditionally so the tool's Delegate is always
	// available; it just emits no events when wailsCtx is nil (test path).
	// WailsEmitter is the same authorised EventsEmit wrapper used by the
	// stream broker — the CI check allows it in this file.
	a.elicitAPI = elicitview.New(elicitview.Config{
		Emitter: WailsEmitter{},
	})

	// Scheduled-chat-runs view (mission scheduled-chat-runs-01KX5R8B, WP04).
	// Wired with the SQLiteChatStore when a real DB is available; test
	// chassis path (c == nil or no storage) silently leaves the store nil
	// which causes the accessor to return a graceful-empty surface.
	{
		var chatStore schedulerPkg.ScheduledChatStore
		if c != nil {
			if db := c.Storage(); db != nil {
				chatStore = schedulerPkg.NewSQLiteChatStore(db)
			}
		}
		a.scheduledChatAPI = scheduledchatview.New(scheduledchatview.Config{
			Store: chatStore,
		})
	}

	// Sub-agent profile registry (branch-subagent-interactive-01KZNP3B WP01).
	// Wire with DataDir when available; falls back to bundled-only when nil Core.
	{
		dataDir := ""
		if c != nil {
			dataDir = c.DataDir()
		}
		a.agentsAPI = agentsview.New(dataDir)
		logging.L().Info("rpc.agents.wired", "data_dir", dataDir)
	}

	// Plan-mode approval view (plan-mode-posture-01KZNP3F WP05). Wired
	// whenever a real sessionsAPI is available. The EventEmitter bridges
	// to the StreamBroker so plan_mode_changed events reach the frontend
	// via the same authorised Publish path as all other broker events.
	// The updater is nil-tolerant (Edit works but skips artifact mutation
	// when no artifacts manager is wired — test-chassis path).
	if a.sessionsAPI != nil {
		var planEmitter planmodeview.EventEmitter = &brokerPlanEmitter{broker: a.broker}
		var planUpdater planmodeview.ArtifactUpdater
		if a.artifactsMgr != nil {
			planUpdater = a.artifactsMgr
		}
		a.planmodeAPI = planmodeview.NewAPI(a.sessionsAPI, planEmitter, planUpdater)
	}

	// Eval-capture recorder (eval-harness-replay). Wired when we have a
	// real DataDir; the test-chassis path (c == nil) leaves it nil and
	// Sessions_StartCapture returns ErrEvalNotConfigured.
	if c != nil && c.DataDir() != "" {
		a.evalRecorder = eval.NewRecorder(
			filepath.Join(c.DataDir(), "eval-captures"),
			c.BuildVersion(),
		)
		logging.L().Info("eval.recorder.wired", "data_dir", c.DataDir())
	}

	// Wire Sentry view (sentry-error-monitoring-01KX5R8G WP05).
	if c != nil && c.DataDir() != "" {
		a.sentryAPI = &sentryview.Impl{DataDir: c.DataDir()}
	} else {
		a.sentryAPI = &sentryview.Impl{DataDir: ""}
	}

	// Wire Fleet telemetry consent view (fleet-otel-archival-01NDFSEX11 WP07).
	if c != nil && c.DataDir() != "" {
		// Back the consent tier off the live capability poller (settingsImpl was
		// wired + SetFleetClient'd above, so the poller exists). Without this the
		// consent clamps every level to "none" at tier=free — EffectiveLevel
		// fails closed — so ConsentFull/Aggregate could never activate the OTLP
		// pipeline regardless of the user's real (enterprise) tier. Lazy read so
		// the poller's first refresh (which carries the enrolled tier) is picked
		// up by activation time. (completes the StaticTierReader placeholder)
		tierReader := corefleet.TierReaderFunc(func() string {
			if a.settingsImpl == nil {
				return "free"
			}
			p := a.settingsImpl.CapabilityPoller()
			if p == nil {
				return "free"
			}
			if t := p.Current().Tier; t != "" {
				return t
			}
			return "free"
		})
		tc, err := corefleet.NewTelemetryConsent(c.DataDir(), tierReader)
		if err != nil {
			logging.L().Warn("fleet.consent.init.failed", "err", err)
			tc, _ = corefleet.NewTelemetryConsent(os.TempDir(), tierReader)
		}
		a.fleetAPI = &fleetview.Impl{Consent: tc}

		// Wire the fleet OTLP export pipeline (harness-fleet-otlp-export-01NTLMEX01).
		//
		// OSS-first boundary (fleet-auth-foundation-01NDFSEX08 WP07): core must
		// not import core/fleet. The concrete pipeline is constructed here in
		// the rpc layer (which is allowed to import core/fleet) and wired into
		// core via the FleetPipeline interface setter before c.Start runs
		// initTelemetry. The rpc layer also holds a typed ref for the Activate
		// call in settings/fleet.go.
		//
		// c.Start() has NOT run yet at this point (it fires in the Wails
		// OnStartup callback). SetFleetPipeline must be called here so
		// initTelemetry (inside c.Start) finds the exporters already registered.
		if !corefleet.Disabled() {
			profile := corefleet.ResolveProfile()
			if profile.Configured() {
				fleetPipeline := corefleet.NewFleetOTLPPipeline(nil)
				// Wire into core via the interface so core stays fleet-free.
				if c != nil {
					c.SetFleetPipeline(fleetPipeline)
				}
				// Wire into settings (Activate hook post-enroll) with the concrete type.
				if settingsImpl != nil {
					var otlpRes *resource.Resource
					var otlpTP *sdktrace.TracerProvider
					if tel := c.Telemetry(); tel != nil {
						otlpRes = tel.Resource
						otlpTP = tel.TracerProvider
					}
					settingsImpl.SetFleetOTLPPipeline(
						fleetPipeline,
						otlpRes,
						otlpTP,
						tc,
					)
				}
			}
		}
	} else {
		// Test-chassis path: create a consent with a temp dir so the
		// RPC surface is non-nil (callers get "none" and SetLevel is a no-op
		// at tier=free, which is correct for the test chassis).
		tc, _ := corefleet.NewTelemetryConsent(os.TempDir(), corefleet.StaticTierReader{})
		a.fleetAPI = &fleetview.Impl{Consent: tc}
	}

	// Wire Catalog / Sync / CedarPublish views (fleet-share-and-sync-01NDFSEX14).
	// All three degrade gracefully (fleet.ErrFleetDisabled) when the fleet client
	// is a nop or settingsImpl is nil, so the nil-check pattern mirrors Sentry.
	{
		var flCl *corefleet.Client
		var flDataDir string
		if a.settingsImpl != nil {
			flCl = a.settingsImpl.FleetClientForBootstrap()
			flDataDir = dataDir
		}
		flAudit := &fleetAuditEmitter{impl: a.auditImpl}

		// Catalog (WP02)
		var catalogSigner *corefleet.DeviceSigner // kept for SkillDeps wiring below
		if flDataDir != "" {
			signer, signerErr := corefleet.NewDeviceSigner(flDataDir)
			if signerErr != nil {
				logging.L().Warn("fleet.signer.init.failed", "err", signerErr)
				// Still wire with nil signer — Publish will return an error on
				// attempt; List/Install/Installed remain functional.
				a.catalogAPI = catalogview.NewAPI(flCl, nil, flDataDir).WithEmitter(flAudit)
			} else {
				catalogSigner = signer
				a.catalogAPI = catalogview.NewAPI(flCl, signer, flDataDir).WithEmitter(flAudit)
			}
		} else {
			a.catalogAPI = catalogview.NewAPI(nil, nil, "").WithEmitter(flAudit)
		}

		// Sync (WP05) — wired with a nil Syncer when fleet is disabled.
		// The Syncer is a lightweight object; we create it unconditionally but
		// its Push/Pull methods short-circuit via ErrFleetDisabled when flCl is nil.
		syncer := corefleet.NewSyncer(flCl)
		syncPending := &corefleet.SecretPromptQueue{}
		a.syncAPI = syncview.NewAPI(syncer, syncPending)

		// harness-fleet-sync-activation-01NSYNC01 gap #1: register the five
		// sync categories on the Syncer and start the debounced background
		// poll loop. Without this the Syncer foundation was dormant — no
		// categories registered + StartPolling never called. The MCP category
		// shares syncPending so the SyncPanel banner sees MCPs that arrive via
		// pull and still need credentials. registerSyncCategories no-ops when
		// the syncer or store is nil, preserving the offline posture.
		var syncStore settings.SettingsStore
		if a.settingsImpl != nil {
			syncStore = a.settingsImpl.Store()
		}
		mcpSyncCat := corefleet.NewMCPSyncCategory(nil, nil, nil, syncPending)
		registerSyncCategories(context.Background(), syncer, syncStore, mcpSyncCat)

		// Connect settings mutations to the Syncer's debounced push so a theme
		// change schedules a push-up (no-op when the category is disabled).
		if a.settingsImpl != nil {
			a.settingsImpl.SetSyncNotifier(func(category string) {
				syncer.NotifyMutation(corefleet.SyncCategory(category))
			})
		}

		// Store for Shutdown teardown (FR-001).
		a.settingsSyncer = syncer

		// CedarPublish (WP07)
		identityFn := func() (string, error) {
			if flDataDir == "" {
				return "", nil
			}
			id, err := corefleet.LoadIdentity(flDataDir)
			if err != nil {
				return "", err
			}
			return id.Email, nil
		}
		a.cedarPublishAPI = cedarview.NewAPI(flCl, identityFn, flAudit)

		// Sites (sites-ui-01NSITE06): capability-gated sites RPC surface.
		// Deploy progress events are published via the existing broker.
		a.sitesAPI = sitesview.New(flCl, flDataDir, brokerPublisher{broker: a.broker})

		// fleet-context-graph-sync-01NDFSEX17: wire the ContextGraphSyncer so
		// Context_Publish / Context_Promote / Context_SyncStatus actually reach
		// fleet. Without this wire the methods short-circuit via ErrFleetDisabled
		// because contextsview.New() is called early (before flCl is resolved)
		// and the syncer is left nil.
		//
		// Gating: a type assertion to *contextsview.API is safe because
		// newContextsAPI always returns *contextsview.API (the interface is only
		// widened for the field type). A nil flCl / isNop client is deliberately
		// allowed — NewContextGraphSyncer handles the nop case via canPull /
		// canPush which return ErrFleetDisabled, preserving the offline posture.
		if impl, ok := a.contextsAPI.(*contextsview.API); ok {
			var caps *corefleet.CapabilityPoller
			if a.settingsImpl != nil {
				caps = a.settingsImpl.CapabilityPoller()
			}
			ctxSyncer := corefleet.NewContextGraphSyncer(flCl, flDataDir, caps)
			impl.WithSyncer(ctxSyncer)

			// FR-012: wire the library merger so each successful PullDelta
			// applies team/org entries to the local context library. The
			// closure converts ContextNodeEntry → contexts.FleetEntry here in
			// the rpc layer (the only layer allowed to import both packages).
			// a.contextsLib is set just above from newContextsAPI; may be nil
			// when the chassis booted without a DataDir (test path) — the
			// merger closure nil-guards against that.
			lib := a.contextsLib // captured for the closure below
			ctxSyncer.SetLibraryMerger(func(entries []corefleet.ContextNodeEntry) {
				if lib == nil {
					return
				}
				// Convert fleet.ContextNodeEntry → contexts.FleetEntry (mirror
				// type; avoids importing core/fleet from core/contexts).
				converted := make([]corecontexts.FleetEntry, 0, len(entries))
				for _, e := range entries {
					fe := corecontexts.FleetEntry{
						ID:        e.ID,
						Layer:     e.Layer,
						Kind:      e.Kind,
						Title:     e.Title,
						Body:      e.Body,
						Metadata:  e.Metadata,
						Version:   e.Version,
						DeletedAt: e.DeletedAt,
					}
					converted = append(converted, fe)
				}
				lib.MergeFleetEntries(converted)
			})

			// FR-013: wire the conflict notifier so pull-time conflicts are
			// emitted as broker events that the frontend can surface.
			if lib != nil && a.broker != nil {
				brokerRef := a.broker
				lib.SetConflictNotifier(func(c corecontexts.ContextConflict) {
					brokerRef.emitter.Emit(brokerRef.EmitCtx(), "contexts:pull-conflict", c)
				})
			}

			// harness-fleet-sync-activation-01NSYNC01 gap #2: start the
			// background context-pull loop so f->h team/org read-layer deltas
			// merge into the local pulled cache (surfaced via PulledEntries)
			// without a manual Context_SyncStatus poke. Self-gates on
			// sign-in + team-graph capability; personal stays local.
			ctxSyncer.StartPoller(context.Background())

			// Store for Shutdown teardown (FR-011).
			a.ctxGraphSyncer = ctxSyncer

			logging.L().Info("rpc.context_graph_syncer.wired",
				"fleet_client_nil", flCl == nil,
				"data_dir", flDataDir,
			)
		}

		// unified-context-artifacts-01NCTXU01 / Phase 3: wire the UnitSyncer so
		// the fleet view's promote-as-MR + conflict-resolution RPCs reach fleet
		// and surface pull-time conflicts. Gated like the context syncer: a nil
		// flCl / isNop client degrades to local-only (canSync short-circuits).
		// The units.Manager is fleet-free and was constructed earlier from the
		// storage.DB; the syncer is the fleet-touching half. teamID is sourced
		// from the enrolled identity (empty when signed-out).
		if a.unitsMgr != nil {
			var caps *corefleet.CapabilityPoller
			if a.settingsImpl != nil {
				caps = a.settingsImpl.CapabilityPoller()
			}
			teamID := ""
			if flDataDir != "" {
				if id, idErr := corefleet.LoadIdentity(flDataDir); idErr == nil {
					teamID = id.TeamID
				}
			}
			unitMapper := corefleet.NewUnitMapper(teamID)
			unitSyncer := corefleet.NewUnitSyncer(flCl, a.unitsMgr, unitMapper, caps, flDataDir)
			// Read-down-auto: pull org/team units into the local clone as read
			// layers. Self-gates on sign-in + team-graph capability.
			unitSyncer.StartPoller(context.Background())
			a.unitSyncer = unitSyncer

			// Attach the manager + syncer to the already-wired fleet view Impl so
			// the Phase-3 RPCs (Unit_PromoteAsMergeRequest / Unit_ResolveMerge /
			// Unit_ResolveEnshrine / Unit_ResolveLoadable / Unit_ListConflicts) are
			// live. a.fleetAPI is always a *fleetview.Impl (set above on both the
			// real and test paths).
			if fi, ok := a.fleetAPI.(*fleetview.Impl); ok {
				fi.Units = a.unitsMgr
				fi.Syncer = unitSyncer
			}

			logging.L().Info("rpc.unit_syncer.wired",
				"fleet_client_nil", flCl == nil,
				"data_dir", flDataDir,
			)
		}

		// fleet-skills-sync-01NDFSEX18 WP02: wire fleet skill dependencies onto
		// the slashAPI. The capability snapshot is read lazily from the poller at
		// call time via GetCaps so tier changes propagate within one poll
		// interval without restart.
		if slashAPI, ok := a.slashAPI.(*slashview.API); ok && skillStore != nil {
			// Capture a reference to settingsImpl so the GetCaps closure holds
			// the exact API instance used for capability polling.
			settingsRef := a.settingsImpl
			getCaps := func() *corefleet.Capabilities {
				if settingsRef == nil {
					return nil
				}
				p := settingsRef.CapabilityPoller()
				if p == nil {
					return nil
				}
				c := p.Current()
				return &c
			}
			slashAPI.WithSkillDeps(slashview.SkillDeps{
				SkillStore:   skillStore,
				FleetClient:  flCl,
				Signer:       catalogSigner,
				GetCaps:      getCaps,
				PubKeyBase64: "", // fleet-level pub key; empty = skip verify (same as catalog)
			})
			logging.L().Info("rpc.slashcmd.skill_deps_wired",
				"fleet_client_nil", flCl == nil,
				"signer_nil", catalogSigner == nil,
			)
		}
	}

	// Sites capability reconciler (sites-mcp-server-01NSITE05 WP04).
	// Enables the "fleet-sites" recipe when sites_hosting appears and
	// disables it when it disappears or goes stale (24 h TTL). Wired
	// here because core/rpc already owns the CapabilityPoller and
	// recipes.EnabledRecipes — this avoids core/core.go importing fleet.
	if a.settingsImpl != nil && dataDir != "" {
		if poller := a.settingsImpl.CapabilityPoller(); poller != nil {
			enabled, err := recipes.LoadEnabled(dataDir)
			if err != nil {
				logging.L().Warn("rpc.sites_reconciler.load_enabled_failed", "err", err.Error())
				enabled = &recipes.EnabledRecipes{}
			}
			corefleet.NewSitesReconciler(poller, enabled, dataDir).Start()
		}
	}

	a.bindings = NewBindings(a)
	if a.settingsImpl != nil {
		a.bindings.SetSettingsStore(a.settingsImpl.Store())
	}

	// Bash allowlist → Cedar migration bootstrap (WP10). Wired only
	// when both a real Core with a DataDir and a settings store are
	// available. The hook is best-effort: errors are logged at warn
	// inside Core.Start and never block boot.
	if c != nil && c.DataDir() != "" && a.settingsImpl != nil && a.settingsImpl.Store() != nil {
		snippetWriter := cedarpolicyview.NewAPIWithDataDir(nil, c.DataDir())
		store := a.settingsImpl.Store()
		c.SetBashMigrationBootstrap(func(ctx context.Context) error {
			return corebash.MigrateBashAllowlist(ctx, snippetWriter, store)
		})
	}

	// Harness-self MCP server (WP04/WP05). Build with real adapters so
	// the onboarding agent can call list_settings, list_providers, etc.
	// The server is held on a.harnessServer; the in-process transport
	// wiring (WP09) will attach it to the session pool.
	{
		var sessionMgr *session.Manager
		if c != nil {
			sessionMgr = c.SessionManager()
		}
		hManagers := buildHarnessManagers(
			a.llmAPI,
			a.settingsAPI,
			a.sessionsAPI,
			sessionMgr,
			mergedCat,
		)
		a.harnessServer = &harnessServer{
			srv: harnessmcp.RegisterAll(harnessmcp.NewServer(), hManagers),
		}
		logging.L().Info("harness.self.server.ready",
			"tools", len(a.harnessServer.srv.Tools()),
		)
	}

	// Onboarding view (harness-self-mcp-onboarding-01KQ8TDU WP08).
	// Wired with real providers when a core is available; the zero-value
	// onboardingview.New(onboardingview.Config{}) stub handles the nil
	// chassis path gracefully.
	{
		firstRunDetector := onboardingFirstRunAdapter{llmAPI: a.llmAPI}
		var sessionStarter onboardingSessionStarterAdapter
		if c != nil {
			sessionStarter.sessionMgr = c.SessionManager()
			sessionStarter.dataDir = dataDir
		}
		a.onboardingAPI = onboardingview.New(onboardingview.Config{
			FirstRun:       firstRunDetector,
			Completion:     onboardingCompletionAdapter{},
			SessionStarter: sessionStarter,
			SettingsDial:   onboardingSettingsDialAdapter{},
			DataDir:        dataDir,
		})
		logging.L().Info("onboarding.api.ready")
	}

	// FR-008 (agent-loop-robustness-parity WP08): record the boot-phase
	// error strings so the frontend's BootHealthBanner can surface them.
	// Called once at the end of api.New when all subsystems have had a
	// chance to log their init errors. Async subsystems (MCP pool, skills
	// BootLoad) are not yet captured here; they update the store when their
	// goroutines complete (future follow-up). Fleet init is synchronous.
	SetBootErrors(bootMCPErr, bootSkillsErr, bootFleetErr)

	return a
}

// newSlashRegistry wires the slash-command registry against the
// session manager (used by /clear), the LLM connector view (used
// by /model), the memory store + embedder (used by /memorize,
// /recall, /forget), the branches API (used by /branch), and the
// workflows API (used by /wf).
// Returns nil when registry construction fails; the view degrades
// to a friendly error response on every Execute.
func newSlashRegistry(c *core.Core, llmAPI llm.LLMConnectorAPI, memStore corememory.Store, embedder corememory.Embedder, branchesAPI branchesview.BranchesAPI, workflowsAPI workflowsview.WorkflowsAPI, exposureIdx *secrets.ExposureIndex) *coreslashcmd.Registry {
	deps := coreslashcmd.Deps{}
	if c != nil && c.SessionManager() != nil {
		deps.Sessions = &slashSessionAppender{mgr: c.SessionManager()}
	}
	if llmAPI != nil {
		deps.Providers = &slashProviderLister{inner: llmAPI}
	}
	if memStore != nil && embedder != nil {
		deps.Memory = &slashMemoryGateway{store: memStore, embedder: embedder}
	}
	if branchesAPI != nil {
		deps.Branches = &slashBranchGateway{inner: branchesAPI}
	}
	if workflowsAPI != nil {
		deps.Workflows = &slashWorkflowsGateway{inner: workflowsAPI}
	}
	if exposureIdx != nil {
		deps.Secrets = &slashSecretExposer{idx: exposureIdx}
	}
	registry, err := coreslashcmd.NewRegistry(deps)
	if err != nil {
		logging.L().Warn("slashcmd.registry.construct_failed", "err", err.Error())
		return nil
	}
	return registry
}

// slashSessionAppender adapts session.Manager into the narrow
// SessionAppender contract /clear consumes. Maps the manager's
// AppendMessage shape onto the slashcmd-side AppendSystemMessage
// shape (always role=system, returns the persisted message id).
type slashSessionAppender struct {
	mgr *session.Manager
}

func (a *slashSessionAppender) AppendSystemMessage(ctx context.Context, sessionID, content string) (string, error) {
	if a == nil || a.mgr == nil {
		return "", errors.New("slashcmd: session manager not wired")
	}
	stored, err := a.mgr.AppendMessage(ctx, sessionID, session.Message{
		Role:    session.RoleSystem,
		Content: content,
	})
	if err != nil {
		return "", err
	}
	return stored.ID, nil
}

// slashProviderLister adapts the LLM connector view into the
// ProviderLister contract /model consumes. The translation projects
// the rich llm.Provider shape onto the smaller slashcmd.Provider so
// the slashcmd package never imports the rpc/views/llm package.
type slashProviderLister struct {
	inner llm.LLMConnectorAPI
}

func (a *slashProviderLister) ListProviders(ctx context.Context) ([]coreslashcmd.Provider, error) {
	if a == nil || a.inner == nil {
		return nil, nil
	}
	rows, err := a.inner.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]coreslashcmd.Provider, 0, len(rows))
	for _, p := range rows {
		out = append(out, coreslashcmd.Provider{
			ID:           p.ID,
			Name:         p.Name,
			Kind:         p.Kind,
			DefaultModel: p.Model,
			Models:       append([]string(nil), p.Models...),
		})
	}
	return out, nil
}

// slashMemoryGateway adapts core/memory.Store + core/memory.Embedder
// onto the narrow MemoryGateway contract /memorize, /recall, /forget
// consume. The translation owns chunk-id allocation, the pin-after-add
// step (so /memorize chunks survive the prune sweep), and the
// "store reports not found" → ErrMemoryChunkNotFound mapping that
// /forget reads.
type slashMemoryGateway struct {
	store    corememory.Store
	embedder corememory.Embedder
}

func (g *slashMemoryGateway) Memorize(ctx context.Context, sessionID, text string) (string, error) {
	if g == nil || g.store == nil {
		return "", errors.New("slashcmd: memory store unavailable")
	}
	if g.embedder == nil {
		return "", corememory.ErrEmbedderUnavailable
	}
	if _, ok := g.embedder.(corememory.NoopEmbedder); ok {
		return "", corememory.ErrEmbedderUnavailable
	}
	vecs, err := g.embedder.Embed(ctx, []string{text})
	if err != nil {
		return "", fmt.Errorf("slashcmd: embed: %w", err)
	}
	if len(vecs) == 0 {
		return "", errors.New("slashcmd: embedder returned no vectors")
	}
	id, err := newSlashChunkID()
	if err != nil {
		return "", err
	}
	chunk := corememory.Chunk{
		ID:          id,
		SessionID:   sessionID,
		ScopeKind:   corememory.ScopeKindSession,
		ScopeID:     sessionID,
		Content:     text,
		ContentHash: corememory.HashContent(text),
		Embedding:   vecs[0],
		CreatedAt:   time.Now().UTC(),
		Pinned:      true,
		Source:      "slash:/memorize",
	}
	if err := g.store.Add(ctx, chunk); err != nil {
		return "", err
	}
	// Belt-and-braces: if the store implements PruneCapable, set the
	// pin flag explicitly. The chunk-level Pinned field above already
	// covers the chromem store; this guards against future stores that
	// honor SetPinned but not the chunk field.
	if pruner, ok := g.store.(corememory.PruneCapable); ok {
		_ = pruner.SetPinned(ctx, id, true)
	}
	return id, nil
}

func (g *slashMemoryGateway) Recall(ctx context.Context, sessionID, query string, k int) ([]coreslashcmd.MemoryHit, error) {
	if g == nil || g.store == nil {
		return nil, errors.New("slashcmd: memory store unavailable")
	}
	if g.embedder == nil {
		return nil, corememory.ErrEmbedderUnavailable
	}
	if _, ok := g.embedder.(corememory.NoopEmbedder); ok {
		return nil, corememory.ErrEmbedderUnavailable
	}
	vecs, err := g.embedder.Embed(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("slashcmd: embed: %w", err)
	}
	if len(vecs) == 0 {
		return nil, nil
	}
	scopes := []corememory.ScopeFilter{
		{Kind: corememory.ScopeKindGlobal},
	}
	if sessionID != "" {
		scopes = append(scopes, corememory.ScopeFilter{Kind: corememory.ScopeKindSession, ID: sessionID})
	}
	results, err := g.store.Query(ctx, vecs[0], k, scopes...)
	if err != nil {
		return nil, err
	}
	out := make([]coreslashcmd.MemoryHit, 0, len(results))
	for _, r := range results {
		out = append(out, coreslashcmd.MemoryHit{
			ID:      r.Chunk.ID,
			Content: r.Chunk.Content,
			Score:   r.Similarity,
		})
	}
	return out, nil
}

func (g *slashMemoryGateway) Forget(ctx context.Context, id string) error {
	if g == nil || g.store == nil {
		return errors.New("slashcmd: memory store unavailable")
	}
	if err := g.store.Delete(ctx, id); err != nil {
		// chromemStore returns a fmt.Errorf("memory: chunk %q not
		// found", id) — match on the substring since the underlying
		// error is not a typed sentinel.
		if strings.Contains(err.Error(), "not found") {
			return coreslashcmd.ErrMemoryChunkNotFound
		}
		return err
	}
	return nil
}

// newSlashChunkID returns a 16-byte hex-encoded random id with the
// "mem-" prefix the rest of the memory subsystem uses. crypto/rand so
// concurrent /memorize calls cannot collide.
func newSlashChunkID() (string, error) {
	b := make([]byte, 16)
	if _, err := cryptorand.Read(b); err != nil {
		return "", fmt.Errorf("slashcmd: random id: %w", err)
	}
	return "mem-" + hex.EncodeToString(b), nil
}

// slashBranchGateway adapts the BranchesAPI onto the narrow BranchGateway
// contract /branch consumes. CreateBranch translates a (sessionID,
// modelID) pair into a CreateBranchOptions with the "exact" preference;
// RecommendModels asks the BranchesAPI for the smaller / larger / same
// triple by calling RecommendModel three times.
type slashBranchGateway struct {
	inner branchesview.BranchesAPI
}

func (g *slashBranchGateway) CreateBranch(ctx context.Context, parentSessionID, modelID string) (coreslashcmd.BranchHandle, error) {
	if g == nil || g.inner == nil {
		return coreslashcmd.BranchHandle{}, errors.New("slashcmd: branches surface unavailable")
	}
	br, err := g.inner.CreateBranch(ctx, branchesview.CreateBranchOptions{
		ParentSessionID: parentSessionID,
		ModelPreference: "exact",
		ExactModelID:    modelID,
	})
	if err != nil {
		return coreslashcmd.BranchHandle{}, err
	}
	return coreslashcmd.BranchHandle{
		BranchID:       br.ID,
		ChildSessionID: br.ChildSessionID,
		ProviderID:     br.ProviderID,
		ModelID:        br.ModelID,
	}, nil
}

func (g *slashBranchGateway) RecommendModels(ctx context.Context, parentSessionID string) (coreslashcmd.BranchRecommendations, error) {
	if g == nil || g.inner == nil {
		return coreslashcmd.BranchRecommendations{}, errors.New("slashcmd: branches surface unavailable")
	}
	pick := func(pref string) coreslashcmd.BranchModel {
		rec, err := g.inner.RecommendModel(ctx, parentSessionID, "", pref)
		if err != nil {
			return coreslashcmd.BranchModel{}
		}
		return coreslashcmd.BranchModel{
			ProviderID: rec.ProviderID,
			ModelID:    rec.ModelID,
			Tier:       rec.Tier,
			Reason:     rec.Reason,
		}
	}
	return coreslashcmd.BranchRecommendations{
		Smaller: pick("smaller"),
		Same:    pick("same"),
		Larger:  pick("larger"),
	}, nil
}

// slashWorkflowsGateway adapts the WorkflowsAPI onto the narrow
// WorkflowsGateway contract /wf consumes. List, Get, and Run project
// the wire shapes down to the slashcmd-local types so the slashcmd
// package never imports the rpc/views/workflows package directly.
type slashWorkflowsGateway struct {
	inner workflowsview.WorkflowsAPI
}

func (g *slashWorkflowsGateway) List(ctx context.Context) ([]coreslashcmd.WorkflowSummary, error) {
	if g == nil || g.inner == nil {
		return nil, errors.New("slashcmd: workflows surface unavailable")
	}
	rows, err := g.inner.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]coreslashcmd.WorkflowSummary, 0, len(rows))
	for _, r := range rows {
		out = append(out, coreslashcmd.WorkflowSummary{
			ID:          r.ID,
			Name:        r.Name,
			Description: r.Description,
		})
	}
	return out, nil
}

func (g *slashWorkflowsGateway) Get(ctx context.Context, id string) (coreslashcmd.WorkflowDetail, error) {
	if g == nil || g.inner == nil {
		return coreslashcmd.WorkflowDetail{}, errors.New("slashcmd: workflows surface unavailable")
	}
	wf, err := g.inner.Get(ctx, id)
	if err != nil {
		return coreslashcmd.WorkflowDetail{}, err
	}
	inputs := make([]coreslashcmd.WorkflowInput, 0, len(wf.Inputs))
	for _, inp := range wf.Inputs {
		inputs = append(inputs, coreslashcmd.WorkflowInput{
			Name:     inp.Name,
			Required: inp.Required,
			Default:  inp.Default,
		})
	}
	return coreslashcmd.WorkflowDetail{
		ID:          wf.ID,
		Name:        wf.Name,
		Description: wf.Description,
		Inputs:      inputs,
	}, nil
}

func (g *slashWorkflowsGateway) Run(ctx context.Context, id string, inputs map[string]string, opts coreslashcmd.WorkflowRunOptions) (<-chan coreslashcmd.WorkflowProgressEvent, error) {
	if g == nil || g.inner == nil {
		return nil, errors.New("slashcmd: workflows surface unavailable")
	}
	res, err := g.inner.RunWithOptions(ctx, workflowsview.RunRequest{
		ID:     id,
		Inputs: inputs,
		Inline: opts.Inline,
	})
	if err != nil {
		return nil, err
	}
	// Project the synchronous RunResult into a channel so the /wf handler
	// can drain it with the same ranging loop regardless of whether the
	// underlying engine ran synchronously or asynchronously.
	ch := make(chan coreslashcmd.WorkflowProgressEvent, len(res.Steps)+1)
	for _, s := range res.Steps {
		ch <- coreslashcmd.WorkflowProgressEvent{
			RunID:  res.RunID,
			Step:   s.Name,
			Status: s.Status,
			Output: s.Output,
			Err:    s.Err,
		}
	}
	close(ch)
	return ch, nil
}

// slashSecretExposer adapts *secrets.ExposureIndex onto the narrow
// SecretExposer contract /secret consumes.
// (model-secret-references-01KW7M5A WP11)
type slashSecretExposer struct {
	idx *secrets.ExposureIndex
}

func (s *slashSecretExposer) Expose(_ context.Context, locator, description, kind string, plaintext []byte) error {
	if s == nil || s.idx == nil {
		return errors.New("slashcmd: secrets subsystem not wired")
	}
	entry := secrets.ExposedEntry{
		Locator:     locator,
		Description: description,
		Scope:       secrets.ScopeSession,
		KindHint:    secrets.KindHint(kind),
	}
	s.idx.Add(entry, plaintext)
	// Zero the caller's buffer too.
	for i := range plaintext {
		plaintext[i] = 0
	}
	return nil
}

func (s *slashSecretExposer) ListLocators(_ context.Context) ([]string, error) {
	if s == nil || s.idx == nil {
		return nil, nil
	}
	entries := s.idx.List(context.Background(), "")
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Locator)
	}
	return out, nil
}

// newSessionsAPI returns the real Manager-backed SessionsAPI when c
// is non-nil; otherwise a noop stub for callers that pass New(nil)
// (see api_test.go's TestViewAccessorStability).
//
// When attMgr is non-nil the returned impl drives the attachments table
// for SetSystemPrompt, with the session.system_prompt column kept for
// the one-release compat buffer.
//
// artStore + media plumb the artifacts cascade extension (FR-014):
// session-delete reads the artifact list, drops the rows via the
// session FK CASCADE, then refcount-sweeps any orphaned CAS files.
// Both nil falls back to the pre-WP02 cascade (attachments + session
// row, no artifacts cleanup).
//
// mgr is the optional usage manager (token-cost-telemetry-01KQ8TD7).
// nil disables GetUsage — it returns a zeroed aggregate with
// CostSource="unknown" (the noopManager contract).
func newSessionsAPI(c *core.Core, attMgr *coreatt.Manager, artStore coreart.Store, media coreatt.MediaStore, mgr usage.Manager) sessions.SessionsAPI {
	if c == nil {
		return &stubSessions{}
	}
	var base sessions.SessionsAPI
	if attMgr == nil {
		base = sessions.NewManagerAPI(c.SessionManager())
	} else {
		base = sessions.NewManagerAPIWithAttachmentsAndArtifacts(c.SessionManager(), attMgr, artStore, media, c.DataDir())
	}
	return sessions.WithUsageManager(base, mgr)
}

// newProjectsAPI returns the real Manager-backed ProjectsAPI when c is
// non-nil; otherwise a noop stub.
func newProjectsAPI(c *core.Core) projectsview.ProjectsAPI {
	if c == nil {
		return &stubProjects{}
	}
	return projectsview.New(c.ProjectManager(), c.SessionManager())
}

// newMediaStore constructs the core/attachments.MediaStore that owns
// the on-disk CAS at <DataDir>/media/. Returns nil when c is nil or
// storage isn't available. The returned store has the multimodal-io
// AttachmentsRefcountSource pre-registered; the artifacts mission's
// ArtifactsRefcountSource is registered separately by the caller
// after the artifacts store has been constructed.
func newMediaStore(c *core.Core) coreatt.MediaStore {
	if c == nil {
		return nil
	}
	s := c.Storage()
	if s == nil {
		return nil
	}
	media := coreatt.NewSQLMediaStore(s, c.DataDir())
	media.RegisterRefcountSource(coreatt.AttachmentsRefcountSource{DB: s})
	return media
}

// newAttachmentsManager constructs the core/attachments.Manager
// against the supplied (already-constructed) MediaStore. Returns nil
// when either core or media is nil; the rpc surface treats nil as
// "attachments disabled" and the SessionsAPI / LLM stack fall back
// to legacy behaviour.
func newAttachmentsManager(c *core.Core, media coreatt.MediaStore) *coreatt.Manager {
	if c == nil || media == nil {
		return nil
	}
	s := c.Storage()
	if s == nil {
		return nil
	}
	return coreatt.NewManager(
		coreatt.NewSQLStore(s),
		coreatt.WithMediaStore(media),
	)
}

// newAttachmentsAPI returns the real Manager-backed AttachmentsAPI
// when both c and the attachments manager are wired; otherwise a noop
// stub keeps the chassis bootable.
func newAttachmentsAPI(c *core.Core, mgr *coreatt.Manager) attachmentsview.AttachmentsAPI {
	if c == nil || mgr == nil {
		return &stubAttachments{}
	}
	return attachmentsview.New(mgr, &sessionProjectReader{mgr: c.SessionManager()})
}

// newArtifactsStack constructs the artifacts Store + Manager and
// wires the ArtifactsRefcountSource onto the supplied MediaStore so
// the composite refcount sees both attachments AND artifacts before
// any on-disk file is reclaimed. Returns (nil, nil) when core or
// media is nil; the chassis treats that as "artifacts disabled" and
// the rpc surface falls back to the noop sink + stub view.
func newArtifactsStack(c *core.Core, media coreatt.MediaStore) (coreart.Store, *coreart.Manager) {
	if c == nil || media == nil {
		return nil, nil
	}
	s := c.Storage()
	if s == nil {
		return nil, nil
	}
	store := coreart.NewSQLStore(s,
		coreart.WithSessionProjectReader(&artifactSessionProjectReader{mgr: c.SessionManager()}),
	)
	// Register the artifacts refcount source on the SHARED MediaStore
	// (the same instance the attachments manager already wired the
	// AttachmentsRefcountSource into). This is the WP02 risk-note
	// hookup: the on-disk file is only reclaimed when no attachments
	// row AND no artifacts row references the hash.
	media.RegisterRefcountSource(coreart.ArtifactsRefcountSource{Store: store})
	mgr := coreart.NewManager(store, &mediaStorePutAdapter{inner: media},
		coreart.WithSessionReader(&artifactSessionProjectReader{mgr: c.SessionManager()}),
	)
	return store, mgr
}

// newArtifactsAPI returns the real Store + Manager-backed
// ArtifactsAPI when wired; otherwise a noop stub keeps the chassis
// bootable.
func newArtifactsAPI(c *core.Core, store coreart.Store, mgr *coreart.Manager, media coreatt.MediaStore) artifactsview.ArtifactsAPI {
	if c == nil || store == nil || mgr == nil || media == nil {
		return &stubArtifacts{}
	}
	return artifactsview.New(artifactsview.Config{
		Store:    store,
		Manager:  mgr,
		Media:    media,
		Messages: &artifactMessageReader{mgr: c.SessionManager()},
		DataDir:  c.DataDir(),
	})
}

// artifactSessionProjectReader adapts session.Manager into the narrow
// SessionProjectReader the artifacts package expects. Empty-string
// projectID means "session has no project"; matches the artifacts
// package's "skip the promote" contract.
type artifactSessionProjectReader struct {
	mgr *session.Manager
}

func (r *artifactSessionProjectReader) SessionProject(ctx context.Context, sessionID string) (string, error) {
	if r == nil || r.mgr == nil {
		return "", nil
	}
	rec, err := r.mgr.Get(ctx, sessionID)
	if err != nil {
		return "", err
	}
	if rec.ProjectID == nil {
		return "", nil
	}
	return *rec.ProjectID, nil
}

// mediaStorePutAdapter projects the wider MediaStore surface onto the
// narrow MediaStorer interface the artifacts manager consumes (Put
// only). Lets the artifacts manager stay decoupled from the on-disk
// + SQL details while sharing the same store instance.
type mediaStorePutAdapter struct {
	inner coreatt.MediaStore
}

func (a *mediaStorePutAdapter) Put(ctx context.Context, b []byte, mediaType, originalName string) (coreatt.MediaArtifact, error) {
	return a.inner.Put(ctx, b, mediaType, originalName)
}

// artifactMessageReader adapts session.Manager to the artifacts view's
// MessageReader. Used by SaveFromMessage to pull the message text by
// (session, message) id.
type artifactMessageReader struct {
	mgr *session.Manager
}

func (r *artifactMessageReader) GetMessage(ctx context.Context, sessionID, messageID string) (artifactsview.Message, error) {
	if r == nil || r.mgr == nil {
		return artifactsview.Message{}, errors.New("rpc: session manager not wired")
	}
	msgs, err := r.mgr.ListMessages(ctx, sessionID)
	if err != nil {
		return artifactsview.Message{}, err
	}
	for _, m := range msgs {
		if m.ID == messageID {
			return artifactsview.Message{
				ID:        m.ID,
				SessionID: m.SessionID,
				Role:      string(m.Role),
				Content:   m.Content,
			}, nil
		}
	}
	return artifactsview.Message{}, fmt.Errorf("rpc: message %q not found in session %q", messageID, sessionID)
}

// sessionProjectReader adapts session.Manager into the small
// SessionProjectReader the attachments view needs.
type sessionProjectReader struct {
	mgr *session.Manager
}

func (r *sessionProjectReader) ProjectID(ctx context.Context, sessionID string) (*string, error) {
	if r == nil || r.mgr == nil {
		return nil, nil
	}
	rec, err := r.mgr.Get(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if rec.ProjectID == nil {
		return nil, nil
	}
	v := *rec.ProjectID
	return &v, nil
}

// importCatalogReader satisfies mcp.MergedCatalogReader by snapshotting
// the same shipped+registry merge mergedRecipeCatalog produces. The
// import RPC uses it to drive collision detection: an entry whose id
// already exists in the merged catalog is flagged
// `collision_warning`. WP10 will swap this for a live MergedCatalog
// pointer once the user-source hot-reload is wired into rpc boot.
type importCatalogReader struct{}

func (importCatalogReader) Recipes() []recipes.Recipe {
	cat := mergedRecipeCatalog()
	if cat == nil {
		return nil
	}
	return cat.List()
}

// mergedRecipeCatalog returns a snapshot *recipes.Catalog containing
// the shipped + curated-registry recipes merged in source-tagged
// order. WP06 introduces the curated registry; the boot path
// previously consulted only Shipped(), which meant a user who had
// enabled e.g. the registry "github" recipe wouldn't find it on
// startup.
//
// The merge happens through the WP05 *recipes.MergedCatalog so the
// id-keyed precedence rules (user > registry > shipped) stay
// centralised; a future hot-reloading user source can plug in via
// MergedCatalog.SetUserSource without re-touching this helper.
//
// Callers receive a fresh *recipes.Catalog whose Recipes slice they
// may mutate freely (copy-on-write semantics from MergedCatalog).
func mergedRecipeCatalog() *recipes.Catalog {
	mc := recipes.NewMergedCatalog(
		func() []recipes.Recipe { return recipes.Shipped().List() },
		func() []recipes.Recipe { return recipes.Registry().List() },
		nil,
	)
	return &recipes.Catalog{Version: 1, Recipes: mc.Recipes()}
}

// makeMCPRecipeBootstrap returns a closure suitable for
// Core.SetMCPRecipeBootstrap. The closure walks the persisted enabled-
// recipes list, gates credential access via the Cedar prompt registry
// (mission cedar-credential-policy-01KQ8TDE WP05), resolves env via
// the shared secrets backend, builds ServerSpec values via
// recipe.ToServerSpec, and Opens them onto the pool. Per-recipe
// failures (Cedar deny, missing required env, OS-keychain entry purged
// out-of-band, recipe id no longer in the catalog) log at warn and
// skip — the chat surface stays usable without that recipe's tools
// (FR-030).
//
// promptRegistry may be nil (default-allow; pre-boot posture). When
// wired, a recipe with env keys triggers an interactive credential
// prompt via the CredentialPermissionModal before resolution proceeds.
//
// cedarEngine may be nil. When non-nil, an AllowAlways decision writes
// a persistent .cedar snippet so the grant survives restarts
// (cedar-credential-policy follow-up: AllowAlways mcp_spawn).
func makeMCPRecipeBootstrap(c *core.Core, pool *stdio.Pool, secretsBackend *secrets.MemoryBackend, promptRegistry *cedar.Registry, cedarEngine *cedar.Engine) func(context.Context) error {
	if c == nil || pool == nil || secretsBackend == nil {
		return nil
	}
	return func(ctx context.Context) error {
		dataDir := c.DataDir()
		if dataDir == "" {
			return nil
		}
		enabled, err := recipes.LoadEnabled(dataDir)
		if err != nil {
			return fmt.Errorf("rpc: load enabled recipes: %w", err)
		}
		// Bootstrap consults the merged catalog so a user who has
		// enabled e.g. the curated-registry "github" recipe finds the
		// entry on boot. WP06 wires shipped + registry; WP05 already
		// provided the merge surface and WP10 will plug in the user
		// source.
		catalog := mergedRecipeCatalog()
		entries := enabled.List()
		specs := make([]coremcp.ServerSpec, 0, len(entries))
		for _, entry := range entries {
			recipe, ok := catalog.Get(entry.ID)
			if !ok {
				logging.L().Warn("rpc.mcp_bootstrap.unknown_recipe", "recipe_id", entry.ID)
				continue
			}
			// Cedar credential gate (WP05): recipes with env keys trigger
			// the mcp_spawn gate. The gate fires best-effort here —
			// promptRegistry nil = default-allow (no engine wired at
			// boot). An explicit deny or user-deny skips the recipe.
			// cedarEngine + dataDir enable AllowAlways persistent grants
			// (cedar-credential-policy follow-up).
			if len(recipe.EnvKeys) > 0 {
				if gateErr := cedar.GateMCPSpawn(ctx, nil, promptRegistry, recipe.ID, dataDir, cedarEngine); gateErr != nil {
					logging.L().Warn("rpc.mcp_bootstrap.credential_gate_denied",
						"recipe_id", recipe.ID, "err", gateErr.Error())
					continue
				}
			}
			resolved, err := recipes.ResolveEnv(ctx, secretsBackend, recipe)
			if err != nil {
				// Most likely cause: the user uninstalled the OS
				// keychain entry out-of-band, so a required env
				// key fails to resolve. Skip + log; the user can
				// re-install the recipe from the Tools panel.
				logging.L().Warn("rpc.mcp_bootstrap.resolve_env_failed",
					"recipe_id", entry.ID, "err", err.Error())
				continue
			}
			specs = append(specs, recipe.ToServerSpec(resolved, entry.Config))
		}
		if len(specs) == 0 {
			return nil
		}
		if err := pool.Open(ctx, specs); err != nil {
			// Pool.Open aggregates per-spec failures; treat the
			// aggregate as non-fatal so a single bad recipe
			// doesn't prevent the others from coming up.
			logging.L().Warn("rpc.mcp_bootstrap.partial_open", "err", err.Error())
		}
		return nil
	}
}

// newToolsAPI constructs the view-scoped Tools surface. The view
// shares the same *stdio.Pool the toolloop dispatches against (so
// install/uninstall is visible to the chat surface immediately) and
// the same in-memory secrets backend the LLM stack uses (so newly-
// staged keychain entries are visible to the next ResolveEnv without
// an OS round-trip).
//
// Returns the stub when c is nil — the test harness path constructs
// rpc.New(nil) and we keep the chassis bootable without crashing on
// the catalog access.
func newToolsAPI(c *core.Core, pool *stdio.Pool, secretsBackend *secrets.MemoryBackend, promptReg *cedar.Registry, cedarPolicyAPI cedarpolicyview.CedarPolicyAPI) tools.ToolsAPI {
	if c == nil {
		return &stubTools{}
	}
	dataDir := c.DataDir()
	enabled, err := recipes.LoadEnabled(dataDir)
	if err != nil {
		logging.L().Warn("tools.load_enabled_failed", "data_dir", dataDir, "err", err.Error())
		enabled = &recipes.EnabledRecipes{}
	}
	cfg := tools.Config{
		Catalog:        mergedRecipeCatalog(),
		Enabled:        enabled,
		Pool:           pool,
		Secrets:        secretsBackend,
		DataDir:        dataDir,
		Audit:          nil, // TODO(audit-wired): reuse process-wide event.Emitter once it's available
		Keychain:       &keychainWriter{backend: secretsBackend},
		Forgetter:      &keychainForgetter{backend: secretsBackend},
		PromptRegistry: promptReg,
		CedarPolicy:    cedarPolicyAPI,
	}
	return tools.New(cfg)
}

// trimmerAdapter adapts tools.API.TrimAllowedDir to the
// permissions.RecipeConfigTrimmer interface. Defined here so api.go
// can wire the two packages without creating an import cycle.
type trimmerAdapter struct {
	inner interface {
		TrimAllowedDir(ctx context.Context, recipeID, path string)
	}
}

func (t trimmerAdapter) TrimAllowedDir(ctx context.Context, recipeID, path string) {
	if t.inner != nil {
		t.inner.TrimAllowedDir(ctx, recipeID, path)
	}
}

// keychainForgetter is the deletion counterpart to keychainWriter.
// It pops the locator out of the OS keychain (best-effort — Linux
// installs without libsecret return an error, which we swallow) and
// out of the in-memory secrets backend.
type keychainForgetter struct {
	backend *secrets.MemoryBackend
}

func (f *keychainForgetter) Forget(ctx context.Context, locator string) error {
	if f == nil {
		return nil
	}
	// OS-keychain: best-effort on the deletion path — a missing entry is
	// treated as success by keychainDelete, and other errors are WARN-logged
	// (FR-004) so a failing keychain delete is no longer fully silent.
	// Also clear any legacy-namespace entry so a forgotten secret doesn't
	// linger under the old service name.
	if err := keychainDelete(ctx, keyringService, locator); err != nil {
		slog.WarnContext(ctx, "secret delete: keychain delete failed; entry may persist",
			"locator", locator,
			"error",   err.Error(),
		)
	}
	if err := keychainDelete(ctx, legacyKeyringService, locator); err != nil {
		// Legacy namespace: best-effort; log but don't accumulate the error.
		slog.WarnContext(ctx, "secret delete: legacy keychain delete failed",
			"locator", locator,
			"error",   err.Error(),
		)
	}
	if f.backend != nil {
		f.backend.ClearEntry(secretsref.RefKeychain, locator)
	}
	return nil
}

// newLLMStack constructs the connector Registry + view-scoped API. It
// is split out of New so the Wails wiring stays declarative.
//
// When core is nil (test path: api_test.go's chassis tests construct
// API{nil}), we still build a working stack — the registry is
// process-local and does not depend on the harness data directory for
// anything other than the personal-providers escape hatch.
//
// The shared broker is supplied by New so all view bridges fan out to
// the same StreamBroker instance.
//
// confirmEachEnabled is read on every loop construction (effectively
// every newLLMStack invocation, which is once at boot). The flag is
// captured into the loop's Config rather than re-read per call — the
// rpc layer's settings binding mutates the FileStore directly, and a
// process restart picks up the new value. This keeps the loop's
// per-Run hot path free of settings-store I/O.
// llmStack bundles the artefacts newLLMStack constructs that the
// outer New func also needs to wire into other views (the tools view
// shares the same *stdio.Pool the toolloop dispatches against, and the
// shared secrets backend is what InstallRecipe writes credentials
// into so the resolver finds them on the next ResolveEnv).
type llmStack struct {
	api      llm.LLMConnectorAPI
	pool     *stdio.Pool
	secrets  *secrets.MemoryBackend
	// reg is the LLM registry used by the auto-title generator and any
	// other post-stack consumers that need direct registry access. Held
	// here so New() can wire the TitleGenerator without refactoring
	// newLLMStack's signature.
	reg corellm.Registry
	// builtins is the registry of in-binary tools (websearch, bash)
	// the chassis fills in based on the Settings toggles. Holding it
	// on the stack so the chassis-level wiring path can register and
	// unregister tools as the user toggles them in Settings.
	builtins *toolloop.BuiltinRegistry
	// bashStore is the bash tool's per-process output cache. Held so
	// the agent-graph manager (which constructs its read_bash_output
	// adapter against the SAME instance) wires both halves of the
	// FR-057b loop without separate plumbing.
	bashStore *corebash.Store
	// compactionScheduler is the soft-archive sweep scheduler
	// (compaction-strategy-ui-01KQ8TDI WP05 + WP08). Started during
	// boot when HARNESS_COMPACTION != "off"; nil when compaction is
	// disabled at boot. The caller is responsible for invoking
	// Stop() on shutdown so the in-flight sweep returns cleanly.
	compactionScheduler *corecompaction.Scheduler
	// compactionLLM is the LLM-call adapter the compaction engine
	// dispatches summarization through. Held on the stack so the
	// rpc layer can expose its OverheadTotals on the per-session
	// cost surface (FR §2.11). nil when compaction is disabled.
	compactionLLM *compactionwiring.LLMCaller
	// compactionAudit is the in-memory audit ring buffer the rpc
	// layer queries when surfacing recent compaction events to the
	// frontend. nil when compaction is disabled.
	compactionAudit *compactionwiring.AuditEmitter
	// chatRunner is the kernel-driven entry point that powers the
	// chat path. Held on the stack so the rpc.New caller can wire a
	// ResumeStarter onto the SessionsAPI
	// (long-turn-resilience-01KR3PRS WP03). nil when graphMgr was
	// unavailable at boot.
	chatRunner *chat.ChatRunner
}

func newLLMStack(
	c *core.Core,
	broker *StreamBroker,
	store personal.Store,
	hooksRunner llm.HookRunner,
	attMgr *coreatt.Manager,
	confirmEachEnabled func() bool,
	artifactSink llm.ArtifactSink,
	artifactSinkConcrete *artifactsview.Sink,
	settingsImpl *settings.API,
	bashStore *corebash.Store,
	artifactsMgr *coreart.Manager,
	graphMgr *graphview.Manager,
	promptRegistry *cedar.Registry,
	usageMgr usage.Manager,
	elicitAPI *elicitview.API,
	slashDispatch *coreslashcmd.Dispatch,
	exposureIdx *secrets.ExposureIndex,
	postureManager coreplanmode.SessionPostureManager,
	// contextsLib is the open Context Library used to register the
	// kenaz__read_context_file built-in. nil is safe; the tool is
	// simply not registered when no library is wired.
	contextsLib *corecontexts.Library,
) llmStack {
	// Share ONE secrets backend between the credref resolver (which
	// reads keys when streaming) and the keychain writer (which stages
	// keys when the user submits AddProvider). Without this sharing,
	// AddProvider would write into a backend the resolver can't see.
	secretsBackend := secrets.NewMemoryBackend()
	// Bundle E bonus — wire the Cedar LLM policy guard into the
	// registry pipeline. AllowAll is the boot-stage default per
	// `cedar.AllowAll` doc; production callers swap a real Engine
	// in once the policy bundle has loaded. The pipeline shape
	// (profile → CapabilityGate → PolicyGuard → CredentialResolver)
	// stays unchanged; only the PolicyGuard implementation is now
	// Cedar-driven.
	cedarGuard := cedar.NewLLMPolicyGuard(cedar.AllowAll{})
	reg, err := llmregistry.New(llmregistry.Options{
		Resolver: credref.New(secretsBackend),
		Policy:   cedarGuard,
	})
	if err != nil {
		// Fall back to the stub on a registry construction failure so
		// the chassis still boots. The error path is exercised only by
		// catalog-load failures, which should never happen in
		// production builds.
		return llmStack{api: &stubLLM{}, secrets: secretsBackend}
	}
	// Compile-time witness: *llmregistry.Registry satisfies the local
	// Registry interface used by the view impl.
	var _ corellm.Registry = (*llmregistry.Registry)(nil)

	credResolver := credref.New(secretsBackend)
	historyAdapter := newSessionHistoryReader(c)
	// WP02 — wire the global static resolver against
	// <DataDir>/mcp_servers.json. A missing file soft-fails to
	// auto_allow (the file is opt-in); a malformed file logs a
	// warning and the resolver is left nil so the loop's built-in
	// auto_allow default applies. Per-session overrides (C2) are
	// composed on top of this when the session manager grows the
	// MCPOverrides reader.
	var perms toolloop.PermissionResolver
	if c != nil && c.DataDir() != "" {
		staticPerms, permErr := toolloop.NewStaticResolverFromDataDir(c.DataDir())
		if permErr != nil {
			logging.L().Warn("toolloop.permissions.static_load_failed",
				"data_dir", c.DataDir(), "err", permErr.Error())
		} else {
			perms = staticPerms
		}
	}
	// WP03 — pre/post-tool-use hooks and audit emission. core/hooks
	// only exposes pre_send / post_send for chat-pipeline events;
	// there is no pre_tool_use / post_tool_use surface there yet, so
	// the toolloop falls back to its built-in noopHookRunner. A real
	// runner lands when core/hooks grows the tool-use lifecycle (see
	// kitty-specs/mcp-tool-execution-01KQ3JCS/spec.md FR-003).
	//
	// TODO(audit): wire a real AuditEmitter once the rpc layer
	// materializes a process-wide event.Emitter (core/event.NewEmitter
	// + redact.Pipeline). Until then audit emission is silenced and
	// the privacy-CI guard is "no emitter, no leak".
	// confirm-each modal flow retired alongside core/toolloop in the
	// chat-migration cutover; v1 alpha relies on Cedar policy gates
	// to gate dispatch. confirmEachEnabled is preserved on the
	// chassis settings store so a future re-introduction can read the
	// same toggle without a settings migration.
	_ = confirmEachEnabled
	// Stdio MCP pool — empty at boot. Persisted recipes are spawned
	// onto this pool from core.Core.Start (so they're up before the
	// chat surface accepts a turn), and the tools view's
	// InstallRecipe / UninstallRecipe paths use the same pool's
	// OpenOne / CloseOne for dynamic add/remove.
	dataDir := ""
	if c != nil {
		dataDir = c.DataDir()
	}
	mcpPool := stdio.NewPool(stdio.PoolOptions{
		Sampler: stdio.LLMSamplingHandler(reg, func() (string, string) {
			// v1: no active-provider selector wired yet — sampling
			// callbacks land on whichever profile resolves first via
			// the registry's Stream contract. The user-trust mission
			// owns the explicit selector knob.
			return "", ""
		}),
		Roots:  stdio.DefaultRoots(dataDir, nil),
		Broker: &poolEventPublisher{broker: broker},
		Logger: nil, // defaults to slog.Default
	})
	// Built-in tools registry. The chassis registers websearch + bash
	// here when Settings toggles are ON. The BuiltinPool merges them
	// into the pool's tool catalog AND dispatches to them without
	// going through MCP. Gating is done via an EnabledFilter composed
	// from the Settings store so a toggle takes effect on the next
	// chat turn without a process restart.
	//
	// The wrapped pool is what the chat runner adapts onto the kernel's
	// ToolRegistry seam (chat-migration cutover); the kernel ToolNode
	// dispatches against the same surface the legacy toolloop did.
	builtinRegistry := toolloop.NewBuiltinRegistry()
	var settingsStore settings.SettingsStore
	if settingsImpl != nil {
		settingsStore = settingsImpl.Store()
	}
	// Cedar engine for the bash gate (WP03). Built per-stack so the
	// bash tool's Cedar gate is wired at construction time. The prompt
	// registry is the process-singleton constructed in api.New() (with
	// a broker dispatcher) and threaded in here so every gate emits on
	// the same topics the permissions view reads from. Both are nil-
	// tolerant: when nil the bash tool falls back to the legacy
	// allowlist gate so the test harness path (New(nil)) keeps working.
	var bashCedarEngine *cedar.Engine
	if dataDir != "" {
		bashCedarEngine = buildCedarEngineOrNil(dataDir)
	}
	// A default resolution budget of DefaultBudget (50) per locator per session.
	// A nil exposureIdx safely skips list_secrets registration.
	var secretsBudget *credstoreRefs.Budget
	if exposureIdx != nil {
		secretsBudget = credstoreRefs.NewBudget(credstoreRefs.DefaultBudget)
	}
	registerBuiltinTools(c, builtinRegistry, bashStore, artifactsMgr, settingsStore, bashCedarEngine, promptRegistry, elicitAPI, slashDispatch, exposureIdx, secretsBudget, postureManager)
	// builtin-filesystem-tools-01KR3N4P: register the read/write family of
	// in-process filesystem tools. Gated behind per-family settings dials
	// (FSReadEnabled / FSWriteEnabled) so the Tools panel toggles take effect
	// on the next chat turn. Uses the same Cedar engine as the bash tool.
	registerFSBuiltinTools(builtinRegistry, bashCedarEngine, settingsStore)
	// unified-context-artifacts-01NCTXU01: register the read_context_file
	// built-in so the agent can read on-demand files from attached context
	// modules. Requires both the contexts library AND an attachment manager;
	// nil-safe: if either is absent the tool is simply not registered.
	if attMgr != nil && contextsLib != nil {
		modSrc := &moduleSourceAdapter{
			mgr:    attMgr,
			reader: &sessionProjectReader{mgr: c.SessionManager()},
		}
		registerReadContextFileTool(builtinRegistry, contextsLib, modSrc)
	}
	builtinFilter := toolloop.NewEnabledFilter(builtinRegistry, builtinEnabledPredicate(settingsImpl))
	wrappedPool := toolloop.NewBuiltinPool(&mcpPoolAdapter{inner: mcpPool}, builtinFilter)
	var attResolver llm.AttachmentsResolver
	if attMgr != nil {
		attResolver = &attachmentsResolverAdapter{
			mgr:    attMgr,
			reader: &sessionProjectReader{mgr: c.SessionManager()},
		}
	}
	// Tool discovery wiring — the discoverer projects the same MCP
	// pool the toolloop dispatches against onto each GenerationRequest's
	// Tools field, namespaced as "<server>__<tool>" so the toolloop can
	// split the response back into a (server, tool) pair at dispatch.
	// Without this, the model never sees any tools and the loop is
	// dead code from the user's perspective.
	//
	// The discoverer also threads the built-in tool registry through
	// (gated by the same Settings filter as the dispatch path), so the
	// model SEES kenaz__web_search / kenaz__bash in its tool catalog
	// when those Settings toggles are ON.
	toolDiscoverer := llm.NewMCPToolDiscovererWithBuiltins(mcpPool, perms, builtinFilter)

	// chat-migration cutover (this mission, WP-A): construct the kernel-
	// driven ChatRunner and hand it to the LLM impl via Config.ChatRunner.
	// When the runner is wired the LLM view's StartStream forwards every
	// chat turn into the kernel's chat_default graph; the legacy toolloop
	// pump path is unreachable in production once this lands. The
	// toolloop construction above stays intact only because tests still
	// reference it; WP-C deletes the loop wholesale.
	// Compaction wiring (mission compaction-strategy-ui-01KQ8TDI WP08).
	// Constructs the engine + sweep scheduler when:
	//   - HARNESS_COMPACTION != "off" (env-flag opt-out).
	//   - The session manager is wired (need session.Store for the
	//     MessageStore + SweepStore adapters).
	//
	// Failure paths degrade gracefully: a missing session store leaves
	// compactionDeps nil and the chat runner falls through without the
	// pre-send hook (the existing chat path stays intact). Engine
	// construction errors log at warn and produce nil too.
	compactionDeps, sweepScheduler, compactionLLM, compactionAudit := buildCompactionWiring(c, reg, settingsImpl)
	if sweepScheduler != nil {
		// Start the sweep loop in a background goroutine. Errors are
		// swallowed by the loop; the optional onSweep callback inside
		// the scheduler logs failures.
		sweepScheduler.Start(context.Background())
	}

	var sessionMgrForUsage *session.Manager
	if c != nil {
		sessionMgrForUsage = c.SessionManager()
	}

	// Build the autotitle generator for the chat runner's post-run trigger.
	// Uses the same registry + profile resolver pattern as the sessions API.
	var chatAutoTitleGen chat.AutoTitleGenerator
	if reg != nil {
		capturedStore := store
		llmCaller := autotitlewiring.NewLLMCaller(reg,
			autotitlewiring.WithProfileResolver(func(_ context.Context, profileID, modelOverride string) (string, string, bool) {
				if profileID != "" {
					return profileID, modelOverride, true
				}
				if capturedStore != nil {
					profs, perr := capturedStore.List()
					if perr == nil && len(profs) > 0 {
						return profs[0].ID, profs[0].Model, true
					}
				}
				return "", "", false
			}),
		)
		chatAutoTitleGen = autotitle.New(llmCaller)
	}

	chatRunner := buildChatRunner(broker, reg, wrappedPool, perms, historyAdapter, settingsImpl, graphMgr, toolDiscoverer, artifactSinkConcrete, compactionDeps, usageMgr, sessionMgrForUsage, chatAutoTitleGen)
	var capCatalog llm.CapCatalog
	if cat, err := llmcap.LoadDefault(); err == nil {
		capCatalog = &capCatalogAdapter{cat: cat}
	}
	api := llm.New(llm.Config{
		Registry:      reg,
		Sink:          &streamSinkAdapter{broker: broker},
		Store:         store,
		Keychain:      &keychainWriter{backend: secretsBackend},
		Prober:        &registryProber{reg: reg, creds: credResolver},
		History:       historyAdapter,
		HistoryWriter: &llmHistoryWriter{inner: historyAdapter},
		Hooks:         hooksRunner,
		Attachments:   attResolver,
		ChatRunner:    chatRunner,
		Tools:         toolDiscoverer,
		Artifacts:     &llmArtifactSinkAdapter{inner: artifactSink},
		CapCatalog:    capCatalog,
	})

	// Boot-time warm-up: kick a one-shot async ListModels refresh on every
	// adapter that exposes the AdapterRefresher capability. By the time
	// the user opens the first chat session, the per-adapter cache is
	// populated and ListProviders surfaces real context_window values
	// instead of the frontend's MODEL_CONTEXT_FALLBACK.
	//
	// This is best-effort and rate-limited inside each adapter, so a
	// down upstream API simply leaves the cache empty until the next
	// ListProviders call kicks another attempt.
	for _, kind := range []string{"anthropic", "openai", "openrouter", "bedrock"} {
		ad := reg.Adapter(kind)
		if ad == nil {
			continue
		}
		if rf, ok := ad.(interface{ RefreshModelsAsync() }); ok {
			logging.L().Info("llm.boot.warmup_models", "kind", kind)
			rf.RefreshModelsAsync()
		}
	}
	return llmStack{
		api:                 api,
		pool:                mcpPool,
		secrets:             secretsBackend,
		reg:                 reg,
		builtins:            builtinRegistry,
		bashStore:           bashStore,
		compactionScheduler: sweepScheduler,
		compactionLLM:       compactionLLM,
		compactionAudit:     compactionAudit,
		chatRunner:          chatRunner,
	}
}

// buildCompactionWiring constructs the compaction engine, sweep
// scheduler, and the per-stream chat-runner deps bundle (mission
// compaction-strategy-ui-01KQ8TDI WP08). Returns nil values when
// compaction is disabled (env flag) or its dependencies are not yet
// wired (test harness boot path) — every nil is a graceful no-op
// downstream.
//
// The HARNESS_COMPACTION=off env check is read ONCE at boot for the
// scheduler (mid-day env changes don't toggle the scheduler);
// the chat-runner pre-send hook re-reads the env on every send so
// users can A/B test by setting it without restarting (see
// chat_runner.compactionDisabledByEnv).
func buildCompactionWiring(
	c *core.Core,
	reg corellm.Registry,
	settingsImpl *settings.API,
) (deps *chat.CompactionDeps, sched *corecompaction.Scheduler, llm *compactionwiring.LLMCaller, audit *compactionwiring.AuditEmitter) {
	if compactionEnvDisabled() {
		logging.L().Info("compaction.disabled", "reason", "HARNESS_COMPACTION=off")
		return nil, nil, nil, nil
	}
	if c == nil {
		return nil, nil, nil, nil
	}
	mgr := c.SessionManager()
	if mgr == nil {
		return nil, nil, nil, nil
	}
	sessionStore := mgr.Store()
	if sessionStore == nil {
		return nil, nil, nil, nil
	}

	messageStoreAdapter := compactionwiring.NewMessageStore(sessionStore)
	sweepStoreAdapter := compactionwiring.NewSweepStore(sessionStore)
	caps := compactionwiring.NewCapabilityLookup()
	auditAdapter := compactionwiring.NewAuditEmitter()
	llmAdapter := compactionwiring.NewLLMCaller(reg)

	// Recent-window resolver — closes over the settings store so a UI
	// change takes effect on the next send.
	recentWindow := func() int {
		if settingsImpl == nil || settingsImpl.Store() == nil {
			return settings.DefaultCompactionRecentWindow
		}
		s, err := settingsImpl.Store().LoadAll()
		if err != nil {
			return settings.DefaultCompactionRecentWindow
		}
		return s.EffectiveCompactionRecentWindow()
	}

	engine, err := corecompaction.NewEngine(corecompaction.EngineConfig{
		Store:        messageStoreAdapter,
		LLM:          llmAdapter,
		Capabilities: caps,
		Audit:        auditAdapter,
		RecentWindow: recentWindow,
	})
	if err != nil {
		logging.L().Warn("compaction.engine.construct_failed", "err", err.Error())
		return nil, nil, nil, nil
	}

	// Sweep scheduler — re-reads settings on every tick so the user-
	// tuned archive-days takes effect without a restart. The OFF tier
	// disables the sweep at the closure level (per WP08 acceptance).
	sweepRunner := func(ctx context.Context) (int, error) {
		if compactionEnvDisabled() {
			return 0, nil
		}
		if settingsImpl == nil || settingsImpl.Store() == nil {
			return 0, nil
		}
		s, err := settingsImpl.Store().LoadAll()
		if err != nil {
			return 0, err
		}
		if s.EffectiveCompactionAggressiveness() == corecompaction.AggressivenessOff {
			// User opted out: sweep is also disabled so a deliberate
			// "I want full transparency" install never deletes archived
			// rows out from under the user.
			return 0, nil
		}
		return corecompaction.RunSweep(ctx, sweepStoreAdapter, auditAdapter,
			s.EffectiveCompactionArchiveDays(), nil)
	}
	scheduler := corecompaction.NewScheduler(sweepRunner,
		corecompaction.WithOnSweep(func(deleted int, err error) {
			if err != nil {
				logging.L().Warn("compaction.sweep.failed", "err", err.Error())
				return
			}
			if deleted > 0 {
				logging.L().Info("compaction.sweep.ok", "deleted", deleted)
			}
		}),
	)

	deps = &chat.CompactionDeps{
		Engine: engine,
		Aggressiveness: func() corecompaction.CompactionAggressiveness {
			if settingsImpl == nil || settingsImpl.Store() == nil {
				return corecompaction.AggressivenessBalanced
			}
			s, err := settingsImpl.Store().LoadAll()
			if err != nil {
				return corecompaction.AggressivenessBalanced
			}
			return s.EffectiveCompactionAggressiveness()
		},
		CompactionModel: func() (corecompaction.ProviderProfileRef, bool) {
			if settingsImpl == nil || settingsImpl.Store() == nil {
				return corecompaction.ProviderProfileRef{}, false
			}
			s, err := settingsImpl.Store().LoadAll()
			if err != nil {
				return corecompaction.ProviderProfileRef{}, false
			}
			if s.CompactionModel.IsZero() {
				return corecompaction.ProviderProfileRef{}, false
			}
			return corecompaction.ProviderProfileRef{
				ProviderID: s.CompactionModel.ProviderID,
				ModelID:    s.CompactionModel.ModelID,
			}, true
		},
		RecentWindow: recentWindow,
		MaxContextTokens: func(model corecompaction.ProviderProfileRef) (int, bool) {
			return caps.MaxContextTokens(model)
		},
	}
	return deps, scheduler, llmAdapter, auditAdapter
}

// compactionEnvDisabled reports whether HARNESS_COMPACTION=off is
// set. Mirrors the chat-runner's compactionDisabledByEnv but lives in
// the rpc layer so the boot-time scheduler check stays self-contained.
func compactionEnvDisabled() bool {
	return osGetenv("HARNESS_COMPACTION") == "off"
}

// osGetenv is a thin wrapper so tests can stub the env without
// global mutation. Production reads through os.Getenv directly.
var osGetenv = func(key string) string {
	return os.Getenv(key)
}

// buildChatRunner constructs the *chat.ChatRunner that replaces
// core/toolloop as the chassis chat path. Returns nil when the graph
// manager is unavailable (test path or boot failure) so the LLM view
// falls through to the legacy toolloop pump.
//
// The runner shares the graph manager's kernel + EnvDeps so a chat run
// uses the same executors / EventLog the graph view's runtime tab
// debugs against. The MaxTurns dial reads Settings.EffectiveMaxAgentTurns
// on every StartStream so the LoopNode max_iterations override picks up
// user-tuned caps without a restart.
func buildChatRunner(
	broker *StreamBroker,
	reg corellm.Registry,
	wrappedPool toolloop.MCPPool,
	perms toolloop.PermissionResolver,
	historyAdapter *sessionHistoryReader,
	settingsImpl *settings.API,
	graphMgr *graphview.Manager,
	tools corellm.ToolDiscoverer,
	artifactSinkConcrete *artifactsview.Sink,
	compactionDeps *chat.CompactionDeps,
	usageMgr usage.Manager,
	sessionMgr *session.Manager,
	autoTitleGen chat.AutoTitleGenerator,
) *chat.ChatRunner {
	if graphMgr == nil || graphMgr.Kernel() == nil {
		logging.L().Warn("chat.runner.disabled", "reason", "graph manager unavailable")
		return nil
	}
	maxTurns := func() int {
		if settingsImpl == nil || settingsImpl.Store() == nil {
			return settings.DefaultMaxAgentTurns
		}
		raw, err := settingsImpl.Store().LoadMaxAgentTurns()
		if err != nil {
			return settings.DefaultMaxAgentTurns
		}
		s := settings.Settings{MaxAgentTurns: raw}
		return s.EffectiveMaxAgentTurns()
	}
	historyReader := chatSessionMessageReader{inner: historyAdapter}
	historyWriter := &llmHistoryWriter{inner: historyAdapter}
	baseEnvDefaults := graphMgr.EnvDefaults()
	// Compose the manager's seam defaults with chat-migration WP-D
	// post-LLM hook wiring: pre-construct the kernel HookManager so we
	// can register the artifacts listener before kernel.Run lands its
	// own lazy default. The Memory store inside the HookManager is
	// applied by graphMgr.EnvDefaults via env.Memory, which fires
	// before this closure.
	envDefaults := func(env *coreag.Env) {
		if baseEnvDefaults != nil {
			baseEnvDefaults(env)
		}
		if env.Hooks == nil {
			env.Hooks = coreag.NewHookManager(env.Memory, env.SessionID, env.ProjectID)
			if env.JournalWriter != nil {
				env.Hooks.SetJournalWriter(env.JournalWriter)
			}
		}
		if artifactSinkConcrete != nil {
			env.Hooks.RegisterPostHook(coreag.HookPostLLM, func(ctx context.Context, sessionID, messageID, text string) {
				_ = artifactSinkConcrete.OnAssistantMessage(ctx, sessionID, messageID, text)
			})
			// Tool-output artifact capture: re-introduces the deleted
			// toolloop PostToolUseListener pipeline. ToolDispatchNode
			// fires this boundary per tool result; the sink runs the
			// code-block detector against the tool payload.
			env.Hooks.RegisterToolPostHook(artifactSinkConcrete.OnPostToolMessage)
		}
		// WP07 (agent-loop-robustness-parity FR-007): populate the two
		// dispatcher configuration fields that were defined but never wired
		// from the production path.
		//
		// ToolCallTimeout: 5 minutes covers the slowest realistic tool calls
		// (long bash scripts, large file writes) while preventing permanent
		// hangs from a stuck subprocess. Overridable in tests via the
		// runner's EnvDefaults callback.
		if env.ToolCallTimeout == 0 {
			env.ToolCallTimeout = 5 * time.Minute
		}
		// MutatingTools: the write-side builtin tools that must NOT run
		// concurrently with one another. Read-only tools (read_file,
		// list_dir, glob, grep, web_search, web_fetch, list_secrets,
		// ask_user_question, sleep, monitor, skill, subagent_dispatch)
		// stay parallel. MCP tools are not in-process, so they are
		// conservatively treated as read-only here; the MCP pool serialises
		// concurrent calls on its own. kenaz__bash is included because bash
		// commands can mutate shared state (filesystem, processes).
		if env.MutatingTools == nil {
			env.MutatingTools = map[string]bool{
				corebash.Name:                true, // "kenaz__bash"
				"kenaz__write_file":           true,
				"kenaz__edit_file":            true,
				"kenaz__save_artifact":        true,
				"kenaz__update_artifact":      true,
				"kenaz__todo_write":           true,
				"kenaz__request_filesystem_access": true,
			}
		}
	}
	// long-turn-resilience-01KR3PRS WP03: PartialPersister wires the
	// partial-output drop path to session.Manager. AppendMessage lands
	// the partial assistant row (so a fresh ListMessages refetch sees
	// it), then MarkStreamingFailure stamps the streaming_failed_at +
	// classification + recoverable columns onto the same row. The
	// runner's terminal goroutine fires this on every backend-error
	// close path where the StreamBridge accumulated text deltas.
	var partialPersister chat.PartialPersister
	if historyAdapter != nil && historyAdapter.mgr != nil {
		mgr := historyAdapter.mgr
		partialPersister = chat.PartialPersisterFunc(func(ctx context.Context, sessionID, partialText, kind string, recoverable bool) (string, error) {
			stored, err := mgr.AppendMessage(ctx, sessionID, session.Message{
				Role:    session.RoleAssistant,
				Content: partialText,
			})
			if err != nil {
				return "", err
			}
			if merr := mgr.MarkStreamingFailure(ctx, sessionID, stored.ID, kind, recoverable); merr != nil {
				logging.L().Warn("chat.partial_persist.mark_failed",
					"session_id", sessionID, "message_id", stored.ID, "err", merr.Error())
			}
			return stored.ID, nil
		})
	}

	// Usage hook (token-cost-telemetry-01KQ8TD7 WP02 + backend-context-
	// window-length-01KQ8TD3 WP02 + WP03). The closure fires from
	// HookPostLLM (after session_write persists the assistant message, so
	// messageID is valid). It reads the provider cost and source from the
	// llm.Response that the LLMProviderAdapter stored in LastResponse(), then:
	//   1. records via usageMgr.Add (token-cost-telemetry).
	//   2. persists the per-session last_usage_json snapshot so the
	//      frontend context-window indicator can update in near-real-time
	//      (backend-context-window-length-01KQ8TD3 WP02).
	//   3. publishes session.usage.updated on the broker so the frontend
	//      updates the context-window indicator in near-real-time without
	//      polling (backend-context-window-length-01KQ8TD3 WP03).
	var usageHookFn chat.UsageHookFunc
	if usageMgr != nil || sessionMgr != nil {
		capturedUsageMgr := usageMgr
		capturedSessionMgr := sessionMgr
		capturedBroker := broker
		usageHookFn = func(ctx context.Context, sessionID, messageID, providerKind, modelID string, resp corellm.Response) {
			var costUSD *float64
			source := "unknown"
			switch {
			case resp.Cost.Source == "provider" && resp.Cost.Total > 0:
				v := resp.Cost.Total
				costUSD = &v
				source = "provider"
			case !resp.Cost.Indeterminate && resp.Cost.Total > 0:
				v := resp.Cost.Total
				costUSD = &v
				source = "derived"
			}
			if capturedUsageMgr != nil {
				turn := usage.UsageTurn{
					SessionID:        sessionID,
					MessageID:        messageID,
					ProviderKind:     providerKind,
					ModelID:          modelID,
					PromptTokens:     resp.Usage.InputTokens,
					CompletionTokens: resp.Usage.OutputTokens,
					CostUSD:          costUSD,
					CostSource:       source,
				}
				if err := capturedUsageMgr.Add(ctx, turn); err != nil {
					logging.L().Warn("usage.add.failed",
						"session_id", sessionID,
						"message_id", messageID,
						"err", err.Error())
				}
			}
			costVal := 0.0
			if costUSD != nil {
				costVal = *costUSD
			}
			snap := session.LastUsage{
				PromptTokens:     resp.Usage.InputTokens,
				CompletionTokens: resp.Usage.OutputTokens,
				TotalTokens:      resp.Usage.InputTokens + resp.Usage.OutputTokens,
				CostUSD:          costVal,
				CostSource:       source,
			}
			// Persist the per-session last_usage_json snapshot so the frontend
			// context-window indicator refreshes without a full GetUsage RPC.
			if capturedSessionMgr != nil {
				if err := capturedSessionMgr.SetLastUsage(ctx, sessionID, snap); err != nil {
					logging.L().Warn("session.last_usage.set.failed",
						"session_id", sessionID,
						"err", err.Error())
				}
			}
			// Push session.usage.updated to the frontend so the context-window
			// indicator updates in near-real-time (WP03). The broker's
			// Publish method uses the stored Wails ctx and is safe to call
			// from any goroutine.
			if capturedBroker != nil {
				capturedBroker.Publish(TopicSessionUsageUpdated, SessionUsagePayload{
					SessionID:        sessionID,
					PromptTokens:     snap.PromptTokens,
					CompletionTokens: snap.CompletionTokens,
					TotalTokens:      snap.TotalTokens,
					CostUSD:          snap.CostUSD,
					CostSource:       snap.CostSource,
				})
			}
		}
	}
	// WP05 (p0-wiring-fixes): wire AutoTitle deps so the post-run trigger
	// actually gates on the user's toggle.
	var autoTitleDeps *chat.AutoTitleDeps
	if sessionMgr != nil && autoTitleGen != nil {
		capturedSettings := settingsImpl
		capturedBroker := broker
		autoTitleDeps = &chat.AutoTitleDeps{
			Manager:   sessionMgr,
			Generator: autoTitleGen,
			EffectiveEnabled: func() bool {
				if capturedSettings == nil {
					return true
				}
				enabled, err := capturedSettings.GetAutoTitleEnabled(context.Background())
				if err != nil {
					return true // default on
				}
				return enabled
			},
			Broker: capturedBroker,
		}
	}
	runner, err := chat.New(chat.Config{
		Kernel:           graphMgr.Kernel(),
		Registry:         reg,
		Pool:             chatToolPoolAdapter{inner: wrappedPool},
		Perms:            chatPermsAdapter{inner: perms},
		Broker:           chatBrokerAdapter{broker: broker},
		History:          historyReader,
		HistoryWriter:    historyWriter,
		GraphLoader:      func() (coreag.Graph, error) { return graphMgr.LoadGraphSpec("chat_default") },
		MaxTurns:         maxTurns,
		EnvDefaults:      envDefaults,
		ToolDiscoverer:   chatToolDiscovererAdapter{inner: tools},
		Compaction:             compactionDeps,
		PartialPersister:       partialPersister,
		UsageHook:              usageHookFn,
		AutoTitle:              autoTitleDeps,
		// multimodal-io-extended-01KQ8TD2 WP02: wire the concrete artifact
		// sink as the generated-image capturer so StreamGeneratedImage
		// events land in the artifact store with Source=="model_output".
		GeneratedImageCapturer: artifactSinkConcrete,
	})
	if err != nil {
		logging.L().Error("chat.runner.construct_failed", "err", err.Error())
		return nil
	}
	return runner
}

// continuationPromptPrefix is the system-injection wrapper used by the
// resume flow's continuation prompt. The model sees:
//
//	"Your previous reply was cut off by a network error. Continue from
//	 where you stopped. Your interrupted reply ended with: <last 200 chars>"
//
// The literal "Continue from where you stopped." string keeps the
// continuation deterministic across providers (DeepSeek, OpenAI,
// Anthropic). Tail truncation at 200 chars matches the spec
// (long-turn-resilience-01KR3PRS plan §Layer 3).
//
// long-turn-resilience-01KR3PRS WP03.
const continuationPromptPrefix = "Your previous reply was cut off by a network error. Continue from where you stopped. Your interrupted reply ended with: "

// continuationPromptTailLen is the number of trailing characters of the
// partial reply we surface back to the model.
const continuationPromptTailLen = 200

// buildContinuationPrompt builds the continuation prompt the resume
// RPC hands to chat.ChatRunner.StartStream as the userMessage.
func buildContinuationPrompt(partial string) string {
	tail := partial
	if len(tail) > continuationPromptTailLen {
		// Truncate from the back so the model sees the most-recent
		// context, not the prefix.
		tail = tail[len(tail)-continuationPromptTailLen:]
	}
	return continuationPromptPrefix + tail
}

// buildResumeStarter wires a sessions.ResumeStarter onto the supplied
// chat runner + session manager so Sessions_ResumeMessage can open a
// continuation chat-stream against a partial assistant row.
//
// The starter:
//
//  1. Loads the partial row to extract the tail text + the session's
//     last-known (profileID, modelOverride). Today the chat runner does
//     not store a per-session profile/model mapping, so the resume
//     reuses whatever profile/model the caller threads in (production
//     wiring uses the chassis-default profile id; the frontend can
//     override via a future RPC arg).
//  2. Synthesizes the continuation prompt via buildContinuationPrompt.
//  3. Calls runner.StartStream with the synthesized prompt as the
//     userMessage so the existing AskBus/HistoryReadNode pump delivers
//     it on the first kernel fire.
//
// long-turn-resilience-01KR3PRS WP03.
func buildResumeStarter(runner *chat.ChatRunner, mgr *session.Manager, defaultProfileID, defaultModel string) sessions.ResumeStarter {
	if runner == nil || mgr == nil {
		return nil
	}
	return sessions.ResumeStarterFunc(func(ctx context.Context, sessionID, originalMessageID, profileID, modelOverride string) (string, error) {
		partial, err := mgr.GetMessage(ctx, sessionID, originalMessageID)
		if err != nil {
			return "", fmt.Errorf("rpc: load partial for resume: %w", err)
		}
		prompt := buildContinuationPrompt(partial.Content)
		if profileID == "" {
			profileID = defaultProfileID
		}
		if modelOverride == "" {
			modelOverride = defaultModel
		}
		// StartStream persists the continuation prompt as a user turn
		// before opening the kernel run. That's the wrong shape for a
		// resume — we want the continuation row to be assistant-role
		// and stamped with continuation_of. The chat runner will need
		// a dedicated resume entrypoint to land the right shape; for
		// now we emit a user-turn with the continuation prompt, which
		// produces a fresh assistant turn that the frontend can wire
		// into the partial bubble manually via the OriginalMessageID
		// returned by the resume RPC.
		//
		// TODO(long-turn-resilience-WP04): plumb a dedicated
		// runner.StartResume(originalMessageID, prompt) entrypoint so
		// the persisted assistant row carries continuation_of natively
		// (via session.Manager.AppendContinuation) instead of relying
		// on the frontend to stitch the bubbles.
		return runner.StartStream(ctx, profileID, sessionID, modelOverride, prompt)
	})
}

// chatToolDiscovererAdapter bridges corellm.ToolDiscoverer onto the
// chat package's narrower ToolCatalogDiscoverer surface so the chat
// runner can populate the LLM provider adapter's tool catalog on each
// StartStream without importing the LLM view.
type chatToolDiscovererAdapter struct {
	inner corellm.ToolDiscoverer
}

func (a chatToolDiscovererAdapter) Tools(ctx context.Context, sessionID string) ([]corellm.ToolSpec, error) {
	if a.inner == nil {
		return nil, nil
	}
	return a.inner.Tools(ctx, sessionID)
}

// chatSessionMessageReader bridges *sessionHistoryReader (which returns
// llm.SessionMessage) onto the chat package's narrower SessionMessageReader
// shape (returns []agentgraph.Message).
type chatSessionMessageReader struct {
	inner *sessionHistoryReader
}

func (r chatSessionMessageReader) History(ctx context.Context, sessionID string, n int) ([]coreag.Message, error) {
	if r.inner == nil {
		return nil, nil
	}
	stored, err := r.inner.ListMessages(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if n > 0 && len(stored) > n {
		stored = stored[len(stored)-n:]
	}
	out := make([]coreag.Message, 0, len(stored))
	for _, m := range stored {
		out = append(out, coreag.Message{
			Role:    m.Role,
			Content: m.Content,
		})
	}
	return out, nil
}

// chatBrokerAdapter wraps *StreamBroker so the chat package's narrow
// Broker interface (Emit(topic, payload)) sees the same emitter the LLM
// view's StreamSink uses. Topic naming stays byte-equal to the legacy
// pump path so frontend useSession.ts is untouched.
type chatBrokerAdapter struct {
	broker *StreamBroker
}

func (b chatBrokerAdapter) Emit(topic string, payload any) {
	if b.broker == nil {
		return
	}
	b.broker.emitter.Emit(b.broker.EmitCtx(), topic, payload)
}

// chatToolPoolAdapter wraps the wrapped toolloop.MCPPool (built-in pool
// merged with the MCP stdio pool) onto the chat package's narrower
// ToolPool surface. Marshalling identical to toolloop.dispatchOne so a
// chat-driven dispatch fans through the same builtin / MCP stdio path
// the legacy pump used.
type chatToolPoolAdapter struct {
	inner toolloop.MCPPool
}

func (a chatToolPoolAdapter) Tools(ctx context.Context) ([]chat.ToolEntry, error) {
	if a.inner == nil {
		return nil, nil
	}
	raw, err := a.inner.Tools(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]chat.ToolEntry, 0, len(raw))
	for _, t := range raw {
		out = append(out, chat.ToolEntry{Server: t.Server, Name: t.Name})
	}
	return out, nil
}

func (a chatToolPoolAdapter) Call(ctx context.Context, server, tool string, args []byte) ([]byte, error) {
	if a.inner == nil {
		return nil, errors.New("chat: pool not wired")
	}
	out, err := a.inner.Call(ctx, server, tool, args)
	if err != nil {
		return nil, err
	}
	return []byte(out), nil
}

// chatPermsAdapter wraps toolloop.PermissionResolver onto the chat
// package's PermVerdict shape so the chat runner's kernel adapter can
// gate dispatches without a direct toolloop import.
type chatPermsAdapter struct {
	inner toolloop.PermissionResolver
}

func (p chatPermsAdapter) Resolve(ctx context.Context, sessionID, server, tool string) (chat.PermVerdict, error) {
	if p.inner == nil {
		return chat.PermVerdict{Server: server, Tool: tool, Policy: "auto_allow"}, nil
	}
	res, err := p.inner.Resolve(ctx, sessionID, server, tool)
	if err != nil {
		return chat.PermVerdict{}, err
	}
	return chat.PermVerdict{
		Server: res.Server,
		Tool:   res.Tool,
		Policy: string(res.Policy),
		Reason: res.Reason,
	}, nil
}

// poolEventPublisher adapts the rpc.StreamBroker to the stdio
// pool's EventPublisher contract. Nil broker → no-op so embedding
// tests can leave the broker unset.
type poolEventPublisher struct {
	broker *StreamBroker
}

func (p *poolEventPublisher) Publish(topic string, payload any) {
	if p == nil || p.broker == nil {
		return
	}
	p.broker.emitter.Emit(p.broker.EmitCtx(), topic, payload)
}

// thresholdPublisher adapts the rpc.StreamBroker to the
// usage.Publisher contract used by the WP06 threshold scheduler.
// Identical-shape sibling of poolEventPublisher; lives separately so
// the usage package never imports rpc internals.
type thresholdPublisher struct {
	broker *StreamBroker
}

func (p *thresholdPublisher) Publish(topic string, payload any) {
	if p == nil || p.broker == nil {
		return
	}
	p.broker.emitter.Emit(p.broker.EmitCtx(), topic, payload)
}

// attachmentsResolverAdapter bridges core/attachments.Manager into the
// llm view's AttachmentsResolver shape so the LLM impl never imports
// core/attachments directly.
type attachmentsResolverAdapter struct {
	mgr    *coreatt.Manager
	reader coreatt.SessionProjectReader
}

func (a *attachmentsResolverAdapter) ListResolved(ctx context.Context, sessionID string) ([]llm.ResolvedAttachment, error) {
	if a == nil || a.mgr == nil {
		return nil, nil
	}
	rows, err := a.mgr.ListResolved(ctx, a.reader, sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]llm.ResolvedAttachment, 0, len(rows))
	for _, r := range rows {
		out = append(out, llm.ResolvedAttachment{
			Content: r.Content,
			Kind:    r.Kind,
		})
	}
	return out, nil
}

// newContextsAPI opens the Context Library rooted at <DataDir>/contexts/
// and wraps it behind the view-scoped surface. A nil core (test path)
// or any open failure soft-fails to a nil-library API — Contexts_List
// returns ErrLibraryUnavailable so the frontend's empty-state card is
// the user-visible behaviour instead of a hard chassis crash.
//
// Returns the wrapped API and the open library (or nil) so the caller
// can wire the fsnotify watcher → broker bridge.
func newContextsAPI(c *core.Core) (contextsview.ContextsAPI, *corecontexts.Library) {
	if c == nil || c.DataDir() == "" {
		return contextsview.New(nil), nil
	}
	lib, err := corecontexts.Open(filepath.Join(c.DataDir(), "contexts"))
	if err != nil {
		return contextsview.New(nil), nil
	}
	// SweepTrash is best-effort on boot — a stale trash directory
	// shouldn't keep the surface from coming up.
	_ = lib.SweepTrash()
	return contextsview.New(lib), lib
}

// moduleSourceAdapter bridges core/attachments.Manager into the
// readcontextfile.ModuleSource interface so the read_context_file tool
// can enumerate the currently attached module directories for a session.
// It queries the attachment manager for all resolved attachments whose
// ContentSource has the "module:" scheme.
type moduleSourceAdapter struct {
	mgr    *coreatt.Manager
	reader coreatt.SessionProjectReader
}

func (s *moduleSourceAdapter) AttachedModuleDirs(ctx context.Context, sessionID string) ([]string, error) {
	if s == nil || s.mgr == nil {
		return nil, nil
	}
	rows, err := s.mgr.ListResolved(ctx, s.reader, sessionID)
	if err != nil {
		return nil, err
	}
	const prefix = "module:"
	var dirs []string
	seen := make(map[string]bool)
	for _, r := range rows {
		if !strings.HasPrefix(r.ContentSource, prefix) {
			continue
		}
		dir := r.ContentSource[len(prefix):]
		if dir != "" && !seen[dir] {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}
	return dirs, nil
}

// contextsAttachmentAdder bridges core/attachments.Manager into the
// contextsview.AttachmentAdder interface so AttachModule can persist a
// context_attachments row without the contexts view importing the core
// attachments package directly.
type contextsAttachmentAdder struct {
	mgr *coreatt.Manager
}

func (a *contextsAttachmentAdder) Add(ctx context.Context, in contextsview.AttachmentInput) (contextsview.ModuleAttachment, error) {
	stored, err := a.mgr.Add(ctx, coreatt.Attachment{
		ScopeKind:     in.ScopeKind,
		ScopeID:       in.ScopeID,
		ContentSource: in.ContentSource,
		Content:       in.Content,
		Kind:          in.Kind,
	})
	if err != nil {
		return contextsview.ModuleAttachment{}, err
	}
	out := contextsview.ModuleAttachment{
		ID:            stored.ID,
		ScopeKind:     stored.ScopeKind,
		ScopeID:       stored.ScopeID,
		ContentSource: stored.ContentSource,
		Content:       stored.Content,
		Kind:          stored.Kind,
		Position:      stored.Position,
		CreatedAt:     stored.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	return out, nil
}

// startContextsWatcher wires the library's fsnotify-backed watcher into
// the StreamBroker's Wails event surface so the frontend's listener on
// "contexts:tree-changed" receives ticks for both in-process mutations
// and external writes (operator drops a file in via Finder). A failing
// StartWatching is soft — Library mutators still notify subscribers
// in-process, just not external writes.
func startContextsWatcher(lib *corecontexts.Library, broker *StreamBroker) {
	if lib == nil || broker == nil {
		return
	}
	if err := lib.StartWatching(); err != nil {
		// Sandboxed runners (some CI configurations) deny inotify;
		// the in-process notification path still works via
		// notifyOp on every Save / CreateFolder / Rename / Delete.
		logging.L().Warn("contexts.watcher.start_failed", "err", err.Error())
		return
	}
	w := lib.Subscribe()
	go func() {
		for range w.Events() {
			broker.emitter.Emit(broker.EmitCtx(), "contexts:tree-changed", struct{}{})
		}
	}()
}

// mcpPoolAdapter bridges the wider core/mcp.Pool surface (which knows
// about ServerSpec, Open, Close) to the toolloop's narrow MCPPool
// view. The toolloop only needs Tools + Call; this adapter projects
// the pool's coremcp.Tool slice into toolloop.Tool form.
type mcpPoolAdapter struct {
	inner coremcp.Pool
}

func (a *mcpPoolAdapter) Tools(ctx context.Context) ([]toolloop.Tool, error) {
	tools, err := a.inner.Tools(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]toolloop.Tool, 0, len(tools))
	for _, t := range tools {
		out = append(out, toolloop.Tool{Server: t.Server, Name: t.Name})
	}
	return out, nil
}

func (a *mcpPoolAdapter) Call(ctx context.Context, server, tool string, args json.RawMessage) (json.RawMessage, error) {
	return a.inner.Call(ctx, server, tool, args)
}

// memoryGateAdapter wraps a cedar.Gate onto the memory.MemoryWriteGate
// interface so the memory store can fire cedar.CheckMemoryWrite on
// every Add without importing the cedar package directly. The adapter
// keeps DIRECTIVE_001 intact: core/memory has no policy/cedar import.
type memoryGateAdapter struct {
	gate cedar.Gate
}

// CheckWrite delegates to cedar.CheckMemoryWrite. nil gate is treated
// as AllowAll (every check passes), which matches the boot-stage
// default the chassis ships with.
func (a *memoryGateAdapter) CheckWrite(ctx context.Context, scope string) error {
	if a == nil || a.gate == nil {
		return nil
	}
	return cedar.CheckMemoryWrite(ctx, a.gate, scope)
}

// openMemoryStore opens the long-term-memory vector DB at
// <DataDir>/memory.gob. A nil core or any IO failure soft-fails to nil
// so the chassis still boots — the rpc surface treats nil as "memory
// unavailable" and the toggle in settings becomes a no-op.
func openMemoryStore(c *core.Core) corememory.Store {
	if c == nil || c.DataDir() == "" {
		return nil
	}
	store, err := corememory.NewChromemStore(filepath.Join(c.DataDir(), "memory.gob"))
	if err != nil {
		return nil
	}
	return store
}

// newEmbedder picks an eligible OpenAI-API-compatible personal provider
// and wires its keychain credential as the embedder's key source.
//
// Eligible provider Kinds (Bug #2 fix — universal embedder):
//
//   - "openai"                   → https://api.openai.com/v1/embeddings
//   - "openrouter"               → https://openrouter.ai/api/v1/embeddings
//   - "custom_openai_compatible" → <profile.Endpoint>/embeddings
//     (any profile whose Endpoint is set and whose Kind signals
//     OpenAI-API compatibility; covers llama-server, LM Studio,
//     Ollama, and any self-hosted proxy)
//   - "azure"                    → only when profile.Defaults contains
//     "deployment_id", "api_version", and "resource_name"; skipped
//     otherwise.
//
// Non-eligible Kinds (explicit exclusions):
//
//   - "anthropic" — Anthropic's API has no /v1/embeddings endpoint.
//   - "bedrock"   — AWS Titan Embeddings uses a different wire shape;
//     deferred — needs a dedicated Titan embedder (separate mission).
//
// Selection order: if Settings.EmbedderProviderProfileID is non-empty,
// that profile is used (when eligible). Otherwise the first eligible
// profile in store order is chosen.
//
// The selected model is Settings.EmbedderModelOverride when non-empty;
// otherwise a per-Kind default applies.
func newEmbedder(_ *core.Core, store personal.Store, settingsImpl *settings.API) corememory.Embedder {
	if store == nil {
		return corememory.NoopEmbedder{}
	}
	profiles, err := store.List()
	if err != nil {
		return corememory.NoopEmbedder{}
	}
	// Read user preferences from settings.
	var profileIDOverride, modelOverride string
	if settingsImpl != nil && settingsImpl.Store() != nil {
		if all, lerr := settingsImpl.Store().LoadAll(); lerr == nil {
			profileIDOverride = all.EmbedderProviderProfileID
			modelOverride = all.EmbedderModelOverride
		}
	}
	return newEmbedderFromProfiles(profiles, profileIDOverride, modelOverride)
}

// newEmbedderFromProfiles is the testable core of newEmbedder. It picks
// the best eligible profile from profiles given the optional overrides
// for profile ID and model.
func newEmbedderFromProfiles(profiles []corellm.ProviderProfile, profileIDOverride, modelOverride string) corememory.Embedder {
	// Build a key resolver for a keychain-backed profile locator.
	makeKeychainResolver := func(locator string) corememory.KeyResolver {
		return func(_ context.Context) ([]byte, error) {
			val, err := keyringGetMigrating(locator)
			if err != nil {
				return nil, fmt.Errorf("memory: keychain get %q: %w", locator, err)
			}
			return []byte(val), nil
		}
	}

	// resolveKeyForProfile returns a KeyResolver for the supported cred
	// kinds; nil when the cred kind is not supported by the embedder.
	resolveKeyForProfile := func(p corellm.ProviderProfile) corememory.KeyResolver {
		switch p.Cred.Kind {
		case "keychain":
			if p.Cred.Locator == "" {
				return nil
			}
			return makeKeychainResolver(p.Cred.Locator)
		case "env":
			if p.Cred.Locator == "" {
				return nil
			}
			locator := p.Cred.Locator
			return func(_ context.Context) ([]byte, error) {
				val := os.Getenv(locator)
				if val == "" {
					return nil, fmt.Errorf("memory: env var %q is empty", locator)
				}
				return []byte(val), nil
			}
		default:
			// aws_profile, file, kms — not yet supported for embeddings.
			return nil
		}
	}

	// eligibleEmbedder constructs an OpenAIEmbedder for a known-eligible
	// profile. Returns nil when the profile is not eligible.
	eligibleEmbedder := func(p corellm.ProviderProfile, model string) corememory.Embedder {
		resolver := resolveKeyForProfile(p)
		if resolver == nil {
			return nil
		}
		const defaultModel = "text-embedding-3-small"
		switch p.Kind {
		case "openai":
			m := defaultModel
			if model != "" {
				m = model
			}
			return corememory.NewOpenAIEmbedder(resolver,
				corememory.WithOpenAIModel(m),
				corememory.WithOpenAISourceKind(p.Kind))

		case "openrouter":
			m := defaultModel
			if model != "" {
				m = model
			}
			return corememory.NewOpenAIEmbedder(resolver,
				corememory.WithOpenAIEndpoint("https://openrouter.ai/api/v1/embeddings"),
				corememory.WithOpenAIModel(m),
				corememory.WithOpenAISourceKind(p.Kind))

		case "custom_openai_compatible":
			// The profile's Endpoint is the base URL of the
			// OpenAI-compatible server.  We append "/embeddings" to
			// build the full path.  A missing or empty Endpoint is
			// treated as ineligible so we don't silently hit the default
			// OpenAI URL under an unrecognised profile.
			if p.Endpoint == "" {
				return nil
			}
			endpoint := strings.TrimRight(p.Endpoint, "/") + "/embeddings"
			m := defaultModel
			if model != "" {
				m = model
			} else if p.Model != "" {
				// For custom servers the profile's own model is the
				// most sensible default (the server may not serve
				// text-embedding-3-small at all).
				m = p.Model
			}
			return corememory.NewOpenAIEmbedder(resolver,
				corememory.WithOpenAIEndpoint(endpoint),
				corememory.WithOpenAIModel(m),
				corememory.WithOpenAISourceKind(p.Kind))

		case "azure":
			// Azure OpenAI embeddings require a deployment ID and API
			// version, which are stored in the profile's Defaults map.
			// Skip when either is missing rather than falling back to a
			// bad URL.
			defaults := p.Defaults
			deploymentID, _ := defaults["deployment_id"].(string)
			apiVersion, _ := defaults["api_version"].(string)
			resource, _ := defaults["resource_name"].(string)
			if deploymentID == "" || apiVersion == "" || resource == "" {
				return nil
			}
			endpoint := fmt.Sprintf(
				"https://%s.openai.azure.com/openai/deployments/%s/embeddings?api-version=%s",
				resource, deploymentID, apiVersion,
			)
			m := p.Model
			if model != "" {
				m = model
			}
			return corememory.NewOpenAIEmbedder(resolver,
				corememory.WithOpenAIEndpoint(endpoint),
				corememory.WithOpenAIModel(m),
				corememory.WithOpenAISourceKind(p.Kind))

		default:
			// anthropic, bedrock, and any unknown kind are not eligible.
			return nil
		}
	}

	// If a specific profile ID is requested, try it first.
	if profileIDOverride != "" {
		for _, p := range profiles {
			if p.ID == profileIDOverride {
				if emb := eligibleEmbedder(p, modelOverride); emb != nil {
					return emb
				}
				// Requested profile is ineligible — fall through to auto-pick.
				break
			}
		}
	}

	// Auto-pick: first eligible profile in store order.
	for _, p := range profiles {
		if emb := eligibleEmbedder(p, modelOverride); emb != nil {
			return emb
		}
	}
	return corememory.NoopEmbedder{}
}

// personalProfileLister adapts personal.Store to the memoryview.ProfileLister
// interface so the MemoryAPI can inspect configured provider profiles for the
// EmbedderEligibility surface without importing personal directly.
type personalProfileLister struct {
	store personal.Store
}

func (l *personalProfileLister) ListProfiles() []corememory.ProfileEligibilityInput {
	if l == nil || l.store == nil {
		return nil
	}
	profiles, err := l.store.List()
	if err != nil {
		return nil
	}
	out := make([]corememory.ProfileEligibilityInput, 0, len(profiles))
	for _, p := range profiles {
		defaults := p.Defaults
		deploymentID, _ := defaults["deployment_id"].(string)
		apiVersion, _ := defaults["api_version"].(string)
		resource, _ := defaults["resource_name"].(string)
		out = append(out, corememory.ProfileEligibilityInput{
			Kind:          p.Kind,
			Endpoint:      p.Endpoint,
			AzureComplete: deploymentID != "" && apiVersion != "" && resource != "",
		})
	}
	return out
}

// newCorpusManager wires the corpora subsystem against the chassis's
// storage + DataDir + the same embedder the memory retriever uses.
// Returns nil when c lacks a real storage handle (test path) so the
// rpc view surfaces ErrManagerUnavailable.
//
// The corpus.Embedder seam mirrors core/memory.Embedder shape so the
// adapter is trivial; embedding model parity matters because the
// vector index dimensions must align across the two subsystems if a
// future "memory pulls from corpora" path lands.
func newCorpusManager(c *core.Core, embedder corememory.Embedder) *corecorpus.Manager {
	if c == nil || c.DataDir() == "" {
		return nil
	}
	store := c.Storage()
	if store == nil {
		return nil
	}
	var corpusEmb corecorpus.Embedder
	if embedder != nil {
		if _, ok := embedder.(corememory.NoopEmbedder); !ok {
			corpusEmb = &corpusEmbedderAdapter{inner: embedder}
		}
	}
	return corecorpus.NewManager(corecorpus.NewStorageDB(store), c.DataDir(), corpusEmb)
}

// newGraphManager constructs the agent-graph view's Manager. Nil core
// or empty DataDir falls back to a memory-only manager so tests + the
// nil-Core path keep working — the manager still surfaces the bundled
// library and runs in-memory graphs; user-graph persistence is the
// only feature lost when DataDir is empty.
func newGraphManager(c *core.Core) *graphview.Manager {
	return newGraphManagerWithDeps(c, nil, nil, nil, nil, nil)
}

// newGraphManagerWithDeps wires production seams into the graph
// kernel's Env. Each non-nil dep is wrapped in the corresponding
// agentgraph adapter and threaded into every Run. Nil deps fall back
// to the agentgraph nil-stubs so the kernel keeps booting in test
// harnesses that don't care about the wiring.
//
// bashStore must be the SAME instance the bash built-in tool writes
// to so a downstream read_bash_output node sees transcripts written
// by a prior bash call. Pass nil to disable read_bash_output entirely.
func newGraphManagerWithDeps(
	c *core.Core,
	convMgr *coreconv.Manager,
	corpusMgr *corecorpus.Manager,
	memStore corememory.Store,
	embedder corememory.Embedder,
	bashStore *corebash.Store,
) *graphview.Manager {
	dataDir := ""
	if c != nil {
		dataDir = c.DataDir()
	}
	deps := graphview.EnvDeps{}
	if convMgr != nil {
		deps.Branch = graphview.NewBranchSeamAdapter(convMgr, sessionManagerOrNil(c))
	}
	if corpusMgr != nil {
		deps.Corpus = graphview.NewCorpusBackendAdapter(corpusMgr)
	}
	if memStore != nil {
		deps.Memory = graphview.NewMemoryStoreAdapter(memStore, embedder)
	}
	// Cedar policy gate: build a real *cedar.Engine when a DataDir is
	// available so user-supplied policies in <DataDir>/policy/*.cedar
	// take effect immediately. The Engine ships an embedded default
	// policy bundle that permits the five gate categories with logging
	// — so an empty <DataDir>/policy/ still gives the harness's
	// "default-allow with audit" stance, not a fail-closed posture.
	// Falls back to AllowAll when the engine cannot be constructed
	// (e.g. nil Core, nil DataDir, or a corrupt policy file) so the
	// chassis still boots; the user sees the failure in the audit log.
	deps.Policy = graphview.NewPolicyGateAdapter(buildCedarGate(dataDir))
	if bashStore != nil {
		deps.BashStore = bashStore
		deps.BashOutput = graphview.NewBashOutputStoreAdapter(bashStore)
	}
	// Memory hook journal: bind the SQL writer (migration 0308) when
	// the storage layer exposes a stdlib *sql.DB. The HookManager's
	// in-memory ring buffer continues to work either way; the SQL
	// writer just adds durable persistence.
	if c != nil {
		if jw := buildJournalWriter(c); jw != nil {
			deps.JournalWriter = jw
		}
	}

	// Agent-graph event log: persist run events to SQLite (migration
	// 0309) when the storage layer exposes a *sql.DB. Without this
	// the manager defaults to NewMemoryEventLog and `agent_graph_events`
	// stays empty across runs — RecentDecisions and the run-trace
	// replay surfaces would have nothing to show. Best-effort: when
	// the handle isn't available the manager falls back to memory.
	mgrOpts := []graphview.ManagerOption{
		graphview.WithDataDir(dataDir),
		graphview.WithEnvDeps(deps),
	}
	if c != nil {
		if log := buildAgentGraphEventLog(c); log != nil {
			mgrOpts = append(mgrOpts, graphview.WithEventLog(log))
		}
	}

	mgr, err := graphview.NewManager(mgrOpts...)
	if err != nil {
		// Construction is best-effort; surface returns
		// ErrManagerUnavailable when nil.
		return nil
	}
	return mgr
}

// newNodesStack constructs the manifest-catalog view (mission
// agent-kernel-graph-node-catalog WP07). LoadCatalog is best-effort:
// shipped manifests load from the embedded YAML set, user overrides
// at <DataDir>/agent_graph/nodes/*.yaml deep-merge in. A load error
// is logged at warn but does not abort wiring — the API surface still
// lists whatever loaded cleanly so the frontend's empty-state path
// stays tolerant of a single malformed override file.
func newNodesStack(c *core.Core) (*nodesview.Manager, nodesview.NodesAPI) {
	userDir := ""
	hotReload := false
	if c != nil && c.DataDir() != "" {
		userDir = filepath.Join(c.DataDir(), "agent_graph", "nodes")
	}
	if c != nil {
		hotReload = c.HotReloadEnabled()
	}
	cat, err := corenodes.LoadCatalog(corenodes.LoadOptions{UserDir: userDir})
	if err != nil {
		logging.L().Warn("nodes.load_warn", "err", err.Error(), "user_dir", userDir)
	}
	mgr := nodesview.NewManager(nodesview.ManagerConfig{
		Catalog:          cat,
		UserDir:          userDir,
		HotReloadEnabled: hotReload,
	})
	return mgr, nodesview.New(mgr)
}

// startNodesWatcher launches the dev-flag-gated polling watcher on
// <DataDir>/agent_graph/nodes/. The watcher swaps the manager's
// catalog atomically when *.yaml content changes are detected. nil
// returns are tolerated by the caller; the chassis still boots.
func startNodesWatcher(c *core.Core, mgr *nodesview.Manager) *corenodes.Watcher {
	if c == nil || mgr == nil {
		return nil
	}
	userDir := mgr.UserDir()
	if userDir == "" {
		return nil
	}
	w := corenodes.NewWatcher(corenodes.WatcherConfig{
		UserDir: userDir,
		OnReload: func(cat *corenodes.Catalog) {
			res := mgr.SwapCatalog(cat)
			logging.L().Info("nodes.hot_reload",
				"added", res.Added,
				"removed", res.Removed,
				"modified", res.Modified)
		},
		OnError: func(err error) {
			logging.L().Warn("nodes.hot_reload_err", "err", err.Error())
		},
	})
	w.Start(context.Background())
	logging.L().Info("nodes.hot_reload.started", "user_dir", userDir)
	return w
}

// corpusEmbedderAdapter bridges core/memory.Embedder onto the narrower
// corpus.Embedder seam. The two interfaces share the Embed signature;
// keeping them disjoint avoids a corpus -> memory import edge.
type corpusEmbedderAdapter struct {
	inner corememory.Embedder
}

func (a *corpusEmbedderAdapter) Dimensions() int {
	if a == nil || a.inner == nil {
		return 0
	}
	return a.inner.Dimensions()
}

func (a *corpusEmbedderAdapter) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if a == nil || a.inner == nil {
		return nil, corecorpus.ErrEmbedderUnavailable
	}
	vecs, err := a.inner.Embed(ctx, texts)
	if err != nil {
		if errors.Is(err, corememory.ErrEmbedderUnavailable) {
			return nil, corecorpus.ErrEmbedderUnavailable
		}
		return nil, err
	}
	return vecs, nil
}

// newHooksStack constructs a hooks.Runner with the memory builtins
// preregistered and returns the runner alongside the registry +
// builtins so the hooksview surface can list / mutate hooks against
// the same instances the runner dispatches on. Returns (nil, nil, nil)
// when memStore is nil — the LLM impl falls back to "no hooks
// configured" and the hooksview surface renders an empty list.
//
// Starter memory hooks are NOT auto-installed on boot; the settings
// "long-term memory" toggle owns that lifecycle so a fresh install
// stays quiet until the user opts in.
func newHooksStack(
	c *core.Core,
	retriever *corememory.Retriever,
	memStore corememory.Store,
	embedder corememory.Embedder,
) (llm.HookRunner, *hooks.Registry, *hooks.BuiltinRegistry) {
	if memStore == nil {
		return nil, nil, nil
	}
	dataDir := ""
	if c != nil {
		dataDir = c.DataDir()
	}
	registry, err := hooks.NewRegistry(dataDir)
	if err != nil {
		return nil, nil, nil
	}
	builtins := hooks.NewBuiltinRegistry()
	hooks.RegisterMemoryBuiltins(builtins, hooks.MemoryDeps{
		Retriever: &retrieverAdapter{r: retriever},
		Store:     memStore,
		Embedder:  embedder,
	})
	runner := hooks.NewRunner(hooks.Config{
		Registry: registry,
		Builtins: builtins,
	})
	return &hooksRunnerAdapter{r: runner}, registry, builtins
}

// hooksBuiltinDescriber adapts *hooks.BuiltinRegistry to the
// hooksview.BuiltinDescriber interface (Builtins()) by forwarding to
// the underlying Describe() method.
type hooksBuiltinDescriber struct {
	r *hooks.BuiltinRegistry
}

func (a *hooksBuiltinDescriber) Builtins() []hooks.BuiltinDescriptor {
	if a == nil || a.r == nil {
		return nil
	}
	return a.r.Describe()
}

// retrieverAdapter satisfies hooks.MemoryRetriever (which uses
// memory.Snippet) by delegating to core/memory.Retriever.
type retrieverAdapter struct {
	r *corememory.Retriever
}

func (a *retrieverAdapter) Retrieve(ctx context.Context, query string, k int) ([]corememory.Snippet, error) {
	if a == nil || a.r == nil {
		return nil, nil
	}
	return a.r.Retrieve(ctx, query, k)
}

func (a *retrieverAdapter) RetrieveScoped(ctx context.Context, query, sessionID, projectID string, k int) ([]corememory.Snippet, error) {
	if a == nil || a.r == nil {
		return nil, nil
	}
	return a.r.RetrieveScoped(ctx, query, sessionID, projectID, k)
}

// hooksRunnerAdapter bridges hooks.Runner to llm.HookRunner. The
// translation between event payload shapes is mechanical — both sides
// carry the same fields under different package types.
type hooksRunnerAdapter struct {
	r *hooks.Runner
}

func (a *hooksRunnerAdapter) RunPreSend(ctx context.Context, ev llm.PreSendHookEvent) (llm.PreSendHookEvent, error) {
	if a == nil || a.r == nil {
		return ev, nil
	}
	in := hooks.PreSendEvent{
		SessionID: ev.SessionID,
		Messages:  hookMessagesFromLLM(ev.Messages),
		Model:     ev.Model,
		Kind:      ev.Kind,
		UserText:  ev.UserText,
	}
	out, err := a.r.RunPreSend(ctx, in)
	if err != nil {
		return ev, err
	}
	ev.Messages = hookMessagesToLLM(out.Messages)
	return ev, nil
}

func (a *hooksRunnerAdapter) RunPostSend(ctx context.Context, ev llm.PostSendHookEvent) {
	if a == nil || a.r == nil {
		return
	}
	a.r.RunPostSend(ctx, hooks.PostSendEvent{
		SessionID:     ev.SessionID,
		UserTurn:      ev.UserTurn,
		AssistantTurn: ev.AssistantTurn,
		Model:         ev.Model,
		Kind:          ev.Kind,
		FinishReason:  ev.FinishReason,
	})
}

func hookMessagesFromLLM(in []llm.HookMessage) []hooks.HookMessage {
	out := make([]hooks.HookMessage, len(in))
	for i, m := range in {
		out[i] = hooks.HookMessage{Role: m.Role, Content: m.Content}
	}
	return out
}

func hookMessagesToLLM(in []hooks.HookMessage) []llm.HookMessage {
	out := make([]llm.HookMessage, len(in))
	for i, m := range in {
		out[i] = llm.HookMessage{Role: m.Role, Content: m.Content}
	}
	return out
}

// newMemoryMessageReader adapts the session manager to the
// MessageReader shape the memory view uses to look up a message by id
// before embedding its content.
func newMemoryMessageReader(c *core.Core) memoryview.MessageReader {
	if c == nil {
		return nil
	}
	mgr := c.SessionManager()
	if mgr == nil {
		return nil
	}
	return &memoryMessageReader{mgr: mgr}
}

type memoryMessageReader struct {
	mgr *session.Manager
}

func (r *memoryMessageReader) ListMessages(ctx context.Context, sessionID string) ([]memoryview.Message, error) {
	stored, err := r.mgr.ListMessages(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]memoryview.Message, 0, len(stored))
	for _, m := range stored {
		out = append(out, memoryview.Message{
			ID:      m.ID,
			Role:    string(m.Role),
			Content: m.Content,
		})
	}
	return out, nil
}

// newSessionHistoryReader wires the LLM impl to the session manager
// so buildMessages can thread the user's actual conversation into the
// upstream provider call. Without this, the connector falls back to a
// hardcoded demo prompt — the symptom is "my messages aren't reaching
// the LLM" no matter what the user types.
//
// The returned value satisfies BOTH SessionMessageReader and
// SessionMessageWriter so the LLM impl can also persist the
// assistant turn at stream completion.
func newSessionHistoryReader(c *core.Core) *sessionHistoryReader {
	if c == nil {
		return nil
	}
	mgr := c.SessionManager()
	if mgr == nil {
		return nil
	}
	return &sessionHistoryReader{mgr: mgr}
}

type sessionHistoryReader struct {
	mgr *session.Manager
}

func (r *sessionHistoryReader) ListMessages(ctx context.Context, sessionID string) ([]llm.SessionMessage, error) {
	if r == nil || r.mgr == nil {
		return nil, nil
	}
	stored, err := r.mgr.ListMessages(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	out := make([]llm.SessionMessage, 0, len(stored))
	for _, m := range stored {
		out = append(out, llm.SessionMessage{
			Role:          string(m.Role),
			Content:       m.Content,
			ContentBlocks: m.ContentBlocks,
		})
	}
	return out, nil
}

// AppendMessage persists the assistant turn at stream completion so a
// future ListMessages call rehydrates it. Implements the toolloop's
// SessionHistoryRW.AppendMessage shape (error-only return).
func (r *sessionHistoryReader) AppendMessage(ctx context.Context, sessionID, role, content string) error {
	if r == nil || r.mgr == nil {
		return nil
	}
	_, err := r.mgr.AppendMessage(ctx, sessionID, session.Message{
		Role:    session.Role(role),
		Content: content,
	})
	return err
}

// llmHistoryWriter wraps sessionHistoryReader to satisfy the LLM
// view's SessionMessageWriter shape, which returns the persisted
// message id alongside the error so the post-finalize hooks
// (artifacts code-block detector) can anchor SourceRef.MessageID to
// the freshly persisted row.
type llmHistoryWriter struct {
	inner *sessionHistoryReader
}

func (w *llmHistoryWriter) ListMessages(ctx context.Context, sessionID string) ([]llm.SessionMessage, error) {
	if w == nil || w.inner == nil {
		return nil, nil
	}
	return w.inner.ListMessages(ctx, sessionID)
}

func (w *llmHistoryWriter) SystemPromptFor(ctx context.Context, sessionID string) (string, string, error) {
	if w == nil || w.inner == nil {
		return "", "", nil
	}
	return w.inner.SystemPromptFor(ctx, sessionID)
}

func (w *llmHistoryWriter) AppendMessage(ctx context.Context, sessionID, role, content string) (string, error) {
	if w == nil || w.inner == nil || w.inner.mgr == nil {
		return "", nil
	}
	stored, err := w.inner.mgr.AppendMessage(ctx, sessionID, session.Message{
		Role:    session.Role(role),
		Content: content,
	})
	if err != nil {
		return "", err
	}
	return stored.ID, nil
}

// SystemPromptFor implements llm.SessionContextReader by reading the
// session's persisted starting context. A nil receiver is the test
// fallback path; return zero values so buildMessages skips the prepend.
func (r *sessionHistoryReader) SystemPromptFor(ctx context.Context, sessionID string) (string, string, error) {
	if r == nil || r.mgr == nil {
		return "", "", nil
	}
	rec, err := r.mgr.Get(ctx, sessionID)
	if err != nil {
		return "", "", err
	}
	return rec.SystemPrompt, rec.ContextKind, nil
}

// registryProber satisfies llm.ProviderProber by routing through the
// adapter's ModelLister capability when one is registered. A successful
// /models call proves the credential resolves and the provider API
// answers. Adapters that have no ModelLister (or kinds with no adapter
// registered) report a "not yet supported" message instead of a
// confusing "no provider prober configured" — the chassis still reports
// success=false but the row no longer looks broken.
type registryProber struct {
	reg   *llmregistry.Registry
	creds *credref.Resolver
}

func (p *registryProber) Probe(ctx context.Context, profile corellm.ProviderProfile) llm.ProberResult {
	if p == nil || p.reg == nil {
		return llm.ProberResult{Message: "prober unavailable"}
	}
	adapter := p.reg.Adapter(profile.Kind)
	if adapter == nil {
		return llm.ProberResult{
			Message: fmt.Sprintf("no adapter for kind %q yet", profile.Kind),
		}
	}
	lister, ok := adapter.(corellm.ModelLister)
	if !ok {
		return llm.ProberResult{
			Message: fmt.Sprintf("%s adapter has no probe endpoint", profile.Kind),
		}
	}
	cred, err := p.creds.Resolve(ctx, profile.ID, profile.Cred)
	if err != nil {
		return llm.ProberResult{Message: fmt.Sprintf("credential: %v", err)}
	}
	defer cred.Destroy()
	var models []corellm.ModelInfo
	probeErr := cred.Use(func(buf []byte) error {
		out, err := lister.ListModels(ctx, buf)
		if err != nil {
			return err
		}
		models = out
		return nil
	})
	if probeErr != nil {
		return llm.ProberResult{Message: probeErr.Error()}
	}
	return llm.ProberResult{
		Success: true,
		Message: fmt.Sprintf("ok — %d model(s) available", len(models)),
	}
}

// keychainWriter implements llm.KeychainWriter by storing the
// plaintext in the OS keychain (zalando/go-keyring routes to macOS
// Keychain, Windows Credential Manager, or libsecret on Linux) AND
// in the shared in-memory backend so the credref resolver can read
// it without an OS-keychain round-trip mid-session.
//
// Persistence: the OS-keychain copy survives Wails restarts, which
// is what users expect from the "API key" flow. The in-memory copy
// is a hot cache — Resolve checks it after the keychain miss path.
type keychainWriter struct {
	backend *secrets.MemoryBackend
}

// keyringService matches secrets.MemoryBackend's namespace so reads
// via Resolve(RefKeychain) find the entry written here.
const keyringService = "kenaz-harness"

// legacyKeyringService is the previous (misspelled) namespace. Reads fall
// back to it and migrate forward so credentials stored before the
// kaneaz->kenaz rename survive. New writes only use keyringService.
const legacyKeyringService = "kaneaz-harness"

// keyringGetMigrating reads locator from the current namespace, falling
// back to the legacy namespace on not-found and migrating the value
// forward (best-effort).
func keyringGetMigrating(locator string) (string, error) {
	v, err := keyring.Get(keyringService, locator)
	if err == nil {
		return v, nil
	}
	if errors.Is(err, keyring.ErrNotFound) {
		if lv, lerr := keyring.Get(legacyKeyringService, locator); lerr == nil {
			_ = keyring.Set(keyringService, locator, lv)
			return lv, nil
		}
	}
	return "", err
}

// Write stores plaintext in the OS keychain under the harness's
// service namespace, mirrors it to the in-memory backend, and zeroes
// the supplied buffer.
//
// (FR-004) On keychain-set failure the error is WARN-logged so
// operators can diagnose API-key persistence failures instead of
// silently losing keys across restarts.  We still fall through to the
// in-memory backend so the user can chat in the current session even
// when the OS keychain is unavailable (CI / sandbox / Linux without
// libsecret).  The returned error is non-nil on keychain failure so
// RPC callers (e.g. Settings_SaveAPIKey) can surface the warning.
func (w *keychainWriter) Write(ctx context.Context, locator string, plaintext []byte) error {
	if w == nil {
		return nil
	}
	var keychainErr error
	if err := keychainSet(ctx, keyringService, locator, string(plaintext)); err != nil {
		// Log is already emitted by keychainSet — record for return.
		slog.WarnContext(ctx, "API key written to in-memory cache only; keychain unavailable",
			"locator", locator,
		)
		keychainErr = err
	}
	if w.backend != nil {
		w.backend.SetEntry(secretsref.RefKeychain, locator, plaintext)
	}
	for i := range plaintext {
		plaintext[i] = 0
	}
	// Return the keychain error so the RPC layer can surface it.
	// The in-memory backend is always updated, so the current session
	// is unaffected even when this returns non-nil.
	return keychainErr
}

// newPersonalStore constructs the personal-providers FileStore. It
// prefers c.DataDir()/providers.json when core is wired so test
// harnesses with an explicit DataDir stay isolated; otherwise it falls
// back to personal.DefaultPath() ($USER_CONFIG_DIR/kenaz-harness).
// A construction failure returns nil; the rpc impl treats a nil store
// as "personal store unavailable" and the chassis still boots.
func newPersonalStore(c *core.Core) personal.Store {
	var path string
	if c != nil && c.DataDir() != "" {
		path = filepath.Join(c.DataDir(), "providers.json")
	} else {
		p, err := personal.DefaultPath()
		if err != nil {
			return nil
		}
		path = p
	}
	store, err := personal.NewFileStore(path)
	if err != nil {
		return nil
	}
	return store
}

// llmArtifactSinkAdapter bridges the artifacts view's ArtifactSink
// shape onto the llm view's structurally-identical interface. The
// indirection keeps the import direction one-way: the llm view does
// not import the artifacts view, and a nil inner sink is treated as
// "no artifacts wired" (chat path stays clean).
type llmArtifactSinkAdapter struct {
	inner artifactsview.ArtifactSink
}

func (a *llmArtifactSinkAdapter) OnAssistantMessage(ctx context.Context, sessionID, messageID, text string) error {
	if a == nil || a.inner == nil {
		return nil
	}
	return a.inner.OnAssistantMessage(ctx, sessionID, messageID, text)
}

// streamSinkAdapter wraps a *StreamBroker so the view package never
// imports the rpc package directly (keeps the import graph acyclic).
//
// The adapter calls broker.emitter.Emit under the hood. Privacy CI
// invariant #1 still holds: runtime.EventsEmit lives only in
// emitter.go and stream_broker.go.
type streamSinkAdapter struct {
	broker *StreamBroker
}

// Emit forwards topic+payload to the broker's underlying emitter
// using the Wails-supplied context — context.Background() crashes
// runtime.EventsEmit with "invalid context was passed".
func (s *streamSinkAdapter) Emit(topic string, payload any) {
	if s == nil || s.broker == nil {
		return
	}
	s.broker.emitter.Emit(s.broker.EmitCtx(), topic, payload)
}

// AuditObserver returns a function suitable for passing to
// event.WithObserver — every successful Append fans into the audit
// API's ring buffer + active subscribers. Wiring lives at the call
// site (main.go) so core/rpc stays decoupled from the emitter
// constructor (DIRECTIVE_001).
func (a *API) AuditObserver() func(event.Event) {
	if a.auditImpl == nil {
		return func(event.Event) {}
	}
	return a.auditImpl.ObserveEvent
}

// ShellStatus returns a default shell status. Real values are filled by
// downstream missions; for now the chassis renders a quiet baseline.
func (a *API) ShellStatus(_ context.Context) (ShellStatus, error) {
	build := buildLabel(a.core)
	return ShellStatus{
		ActiveProvider: "—",
		TrustTier:      "Local",
		HarnessBuild:   build,
		Connection:     "ready",
		EventRate:      0,
		PolicyApplied:  true,
		RedactionOn:    true,
		LocalFirstOn:   true,
	}, nil
}

// buildLabel returns the harness build version stamped via
// `wails build -ldflags "-X main.Version=vX.Y.Z"` and threaded through
// core.Options.BuildVersion. Local untagged builds show "dev".
func buildLabel(c *core.Core) string {
	if c == nil {
		return "dev"
	}
	if v := c.BuildVersion(); v != "" {
		return v
	}
	return "dev"
}

// AppInfo returns build metadata. Build comes from main.Version via
// core.BuildVersion(); GoVersion and Platform come from runtime.
func (a *API) AppInfo(ctx context.Context) (AppInfo, error) {
	policyEditorEnabled := true
	keychainRotationEnabled := true
	customOpenAIEnabled := true
	if a != nil {
		policyEditorEnabled = a.policyEditorEnabled
		keychainRotationEnabled = a.keychainRotationEnabled
		customOpenAIEnabled = a.customOpenAIEnabled
	}
	info := AppInfo{
		Build:                   buildLabel(a.core),
		Commit:                  "unknown",
		BuildTime:               "",
		GoVersion:               runtime.Version(),
		Platform:                runtime.GOOS + "/" + runtime.GOARCH,
		WindowSize:              WindowSize{Width: 1280, Height: 800},
		PolicyEditorEnabled:     policyEditorEnabled,
		KeychainRotationEnabled: keychainRotationEnabled,
		CustomOpenAIEnabled:     customOpenAIEnabled,
	}
	// Attach fleet capability state when the Settings view has a poller wired.
	// This avoids a separate frontend RPC call on first load.
	if a != nil {
		if capView, err := a.settingsAPI.FleetCapabilities(ctx); err == nil && capView.Source != "default-deny" {
			info.Capabilities = capView.Enabled
			info.Tier = capView.Tier
		}
	}
	return info, nil
}

// brokerPublisher adapts *StreamBroker to the workflowsview.ProgressPublisher
// interface so the workflows engine can fan progress events onto the
// `workflows:run-progress` topic without importing rpc.
type brokerPublisher struct{ broker *StreamBroker }

func (b brokerPublisher) Publish(topic string, payload any) {
	if b.broker == nil || b.broker.emitter == nil {
		return
	}
	b.broker.emitter.Emit(b.broker.EmitCtx(), topic, payload)
}

// View accessors return the stable instance constructed in New.
func (a *API) LLMConnector() llm.LLMConnectorAPI { return a.llmAPI }
func (a *API) MCP() mcp.MCPAPI                   { return a.mcpAPI }
func (a *API) MCPImport() *mcp.ImportAPI         { return a.mcpImportAPI }
func (a *API) A2A() a2a.A2AAPI                   { return a.a2aAPI }
func (a *API) Workflow() workflow.WorkflowAPI    { return a.workflowAPI }

// Workflows returns the agentic workflows view surface (mission
// workflows-01KQ8TDG, v0.3.0 beta). When the chassis hasn't wired a
// real engine (test-harness rpc.New(nil) path), the fallback returns
// an empty-catalog API whose Run / Get surface ErrEngineUnavailable.
func (a *API) Workflows() workflowsview.WorkflowsAPI {
	if a == nil || a.workflowsAPI == nil {
		return workflowsview.New(workflowsview.Config{})
	}
	return a.workflowsAPI
}
func (a *API) Sessions() sessions.SessionsAPI    { return a.sessionsAPI }
func (a *API) Trust() trust.TrustAPI             { return a.trustAPI }
func (a *API) Context() contextview.ContextAPI   { return a.contextAPI }
func (a *API) Contexts() contextsview.ContextsAPI {
	if a.contextsAPI == nil {
		return contextsview.New(nil)
	}
	return a.contextsAPI
}
func (a *API) Bundle() bundle.BundleAPI          { return a.bundleAPI }
func (a *API) Policy() policy.PolicyAPI          { return a.policyAPI }
// CedarPolicy returns the policy-panel + snippet writer/revoker view
// (mission cedar-credential-policy-01KQ8TDE, WP02 + WP09). The
// nil-engine fallback returns a view that serves empty slices for
// reads and rejects WritePolicySnippet / RevokePolicySnippet with a
// typed error when no DataDir is wired (test-harness path).
func (a *API) CedarPolicy() cedarpolicyview.CedarPolicyAPI {
	if a == nil || a.cedarPolicyAPI == nil {
		return cedarpolicyview.NewAPIWithDataDir(nil, "")
	}
	return a.cedarPolicyAPI
}

// CedarProposeResolver is the narrow surface the Bindings layer calls to
// resolve an in-flight cedar:propose-pending request from the frontend
// CedarProposeModal (harness-self-mcp-onboarding-01KQ8TDU WP07).
//
// In production it is satisfied by *harness.CedarProposerImpl.
// decision is one of: "accept" | "reject".
type CedarProposeResolver interface {
	ResolveProposeRequestRaw(id, decision string) error
}

// ErrCedarProposeNotWired is returned by CedarProposeResolve when no
// CedarProposerImpl has been wired into the API.
var ErrCedarProposeNotWired = errors.New("cedar: propose resolver not wired; no onboarding session active")

// CedarProposeResolve delivers a user decision (accept | reject) to the
// in-flight propose-pending request identified by requestID. The
// Bindings layer calls this via CedarPolicy_ResolvePropose (WP07).
func (a *API) CedarProposeResolve(requestID, decision string) error {
	if a == nil || a.cedarProposeResolver == nil {
		return ErrCedarProposeNotWired
	}
	return a.cedarProposeResolver.ResolveProposeRequestRaw(requestID, decision)
}

// Onboarding returns the first-run onboarding view surface (mission
// harness-self-mcp-onboarding-01KQ8TDU WP08). Always non-nil; the
// zero-config stub returns safe defaults when the chassis has no core.
func (a *API) Onboarding() onboardingview.OnboardingAPI {
	if a.onboardingAPI == nil {
		return onboardingview.New(onboardingview.Config{})
	}
	return a.onboardingAPI
}

// Elicit returns the ask-user-question RPC surface. If elicitAPI has not
// been wired (test harness path with New(nil)) a zero-config stub is
// returned that surfaces ErrUnknownRequest on SubmitAnswer and empty
// slices on ListPending.
func (a *API) Elicit() elicitview.ElicitAPI {
	if a.elicitAPI == nil {
		return elicitview.New(elicitview.Config{})
	}
	return a.elicitAPI
}

// ScheduledChat implements HarnessAPI. Returns a graceful-empty surface
// (ErrStoreUnavailable on mutating methods) when the DB is not wired.
func (a *API) ScheduledChat() scheduledchatview.ScheduledChatAPI {
	if a.scheduledChatAPI == nil {
		return scheduledchatview.New(scheduledchatview.Config{})
	}
	return a.scheduledChatAPI
}

// Secrets returns the model-accessible secrets RPC surface (mission
// model-secret-references-01KW7M5A WP10). When not yet wired, returns
// a stub backed by an empty ExposureIndex.
func (a *API) Secrets() secretsview.SecretsAPI {
	if a.secretsAPI == nil {
		return secretsview.NewAPI(secrets.NewExposureIndex())
	}
	return a.secretsAPI
}

// Planmode_Approve clears plan_mode and approves the pending plan.
// (plan-mode-posture-01KZNP3F WP05)
func (a *API) Planmode_Approve(ctx context.Context, req planmodeview.ApproveRequest) (planmodeview.ApproveResponse, error) {
	if a.planmodeAPI == nil {
		return planmodeview.ApproveResponse{}, fmt.Errorf("planmode: not configured")
	}
	return a.planmodeAPI.Approve(ctx, req)
}

// Planmode_Discard clears plan_mode and discards the pending plan.
// (plan-mode-posture-01KZNP3F WP05)
func (a *API) Planmode_Discard(ctx context.Context, req planmodeview.DiscardRequest) (planmodeview.DiscardResponse, error) {
	if a.planmodeAPI == nil {
		return planmodeview.DiscardResponse{}, fmt.Errorf("planmode: not configured")
	}
	return a.planmodeAPI.Discard(ctx, req)
}

// Planmode_Edit updates the plan artifact with edited content and approves.
// (plan-mode-posture-01KZNP3F WP05)
func (a *API) Planmode_Edit(ctx context.Context, req planmodeview.EditRequest) (planmodeview.EditResponse, error) {
	if a.planmodeAPI == nil {
		return planmodeview.EditResponse{}, fmt.Errorf("planmode: not configured")
	}
	return a.planmodeAPI.Edit(ctx, req)
}

// SetCedarProposeResolver wires a resolver at runtime. Called by the
// onboarding flow when it creates a CedarProposerImpl (WP07).
func (a *API) SetCedarProposeResolver(r CedarProposeResolver) {
	if a == nil {
		return
	}
	a.cedarProposeResolver = r
}

// Permissions returns the universal interactive-permission view surface
// (mission cedar-credential-policy-01KQ8TDE, WP02). When the chassis
// has not wired a registry (test harness path with New(nil)), a stub
// view is returned that surfaces ErrRegistryUnavailable on Resolve and
// empty slices on List* operations.
func (a *API) Permissions() permissionsview.PermissionsAPI {
	if a.permissionsAPI == nil {
		return permissionsview.New(permissionsview.Config{})
	}
	return a.permissionsAPI
}
func (a *API) Audit() audit.AuditAPI             { return a.auditAPI }
func (a *API) Settings() settings.SettingsAPI    { return a.settingsAPI }
func (a *API) Memory() memoryview.MemoryAPI {
	if a.memoryAPI == nil {
		return &stubMemory{}
	}
	return a.memoryAPI
}
func (a *API) Dials() dialsview.DialsAPI {
	if a.dialsAPI == nil {
		return &stubDials{}
	}
	return a.dialsAPI
}
func (a *API) Hooks() hooksview.HooksAPI {
	if a.hooksAPI == nil {
		return &stubHooks{}
	}
	return a.hooksAPI
}
func (a *API) Projects() projectsview.ProjectsAPI {
	if a.projectsAPI == nil {
		return &stubProjects{}
	}
	return a.projectsAPI
}
func (a *API) Attachments() attachmentsview.AttachmentsAPI {
	if a.attachmentsAPI == nil {
		return &stubAttachments{}
	}
	return a.attachmentsAPI
}
func (a *API) Artifacts() artifactsview.ArtifactsAPI {
	if a.artifactsAPI == nil {
		return &stubArtifacts{}
	}
	return a.artifactsAPI
}

// Units returns the fleet-free unified Unit store manager, or nil when the
// chassis has no backing storage.DB (the test chassis). The in-VM Phase-G read
// service consults it for the units.list / artifacts.list read RPCs; callers
// must nil-check the result.
func (a *API) Units() *units.Manager { return a.unitsMgr }
func (a *API) Tools() tools.ToolsAPI {
	if a.toolsAPI == nil {
		return &stubTools{}
	}
	return a.toolsAPI
}
func (a *API) Shell() shell.ShellAPI {
	if a.shellAPI == nil {
		return &stubShell{}
	}
	return a.shellAPI
}
func (a *API) Slash() slashview.SlashAPI {
	if a.slashAPI == nil {
		// nil-registry surface — Execute returns the friendly
		// "not wired" result; List returns empty.
		return slashview.New(nil)
	}
	return a.slashAPI
}
func (a *API) Corpus() corpusview.CorpusAPI {
	if a.corpusAPI == nil {
		// nil-manager surface — methods return ErrManagerUnavailable
		// so the frontend renders the empty state.
		return corpusview.New(nil)
	}
	return a.corpusAPI
}
func (a *API) Branches() branchesview.BranchesAPI {
	if a.branchesAPI == nil {
		// nil-manager surface — methods return ErrManagerUnavailable
		// so the frontend renders the empty state. Mirrors Corpus().
		return branchesview.New(branchesview.Config{})
	}
	return a.branchesAPI
}

// Graph returns the view-scoped accessor for the agent-graph subsystem
// (mission agent-kernel-graph; Bundle A WP06). The accessor lets the
// frontend list the graph library, edit specs, run graphs, and tail the
// EventLog. nil-manager fallback returns the ErrManagerUnavailable
// empty-state surface.
func (a *API) Graph() graphview.API {
	if a.graphAPI == nil {
		return graphview.New(nil)
	}
	return a.graphAPI
}

// Nodes returns the view-scoped accessor for the manifest-driven
// node catalog (mission agent-kernel-graph-node-catalog; WP07). The
// accessor lets the frontend list every callable kind + archetype with
// provenance so WP06's palette and attribute editor can render
// inheritance metadata. nil-manager fallback returns the
// ErrManagerUnavailable empty-state surface.
func (a *API) Nodes() nodesview.NodesAPI {
	if a == nil || a.nodesAPI == nil {
		return nodesview.New(nil)
	}
	return a.nodesAPI
}

// Compaction returns the configurable-compaction view (mission
// agent-kernel-graph; Bundle D WP12/WP13). The defensive nil-pipeline
// branch keeps parallel-agent edits safe: callers may construct an
// API value before the kernel wires the pipeline in.
func (a *API) Compaction() compactionview.CompactionAPI {
	if a.compactionAPI == nil {
		return compactionview.New(nil)
	}
	return a.compactionAPI
}

// SetCompactionAPI wires the compaction view onto the API. Production
// chassis calls this once the kernel constructs its pipeline; tests
// pass a fake.
func (a *API) SetCompactionAPI(c compactionview.CompactionAPI) {
	a.compactionAPI = c
}

// Search returns the full-text search view (cross-session-search
// mission + unified-search-01KX5R8C WP03). Uses the raw *sql.DB handle
// from the storage backend to query the messages_fts FTS5 virtual table
// and wires per-corpus adapters for the unified fan-out.
func (a *API) Search() searchview.SearchAPI {
	if a.searchAPI != nil {
		return a.searchAPI
	}
	// Wire lazily on first call using the structural SQL() interface.
	if a.core != nil {
		store := a.core.Storage()
		if store != nil {
			type sqlHandle interface{ SQL() *sql.DB }
			if h, ok := store.(sqlHandle); ok {
				if rawDB := h.SQL(); rawDB != nil {
					cfg := searchview.Config{}
					// Artifacts + corpus name adapters share the raw DB.
					cfg.ArtifactsDB = rawDB
					cfg.CorpusDB = rawDB
					// Memory adapter — nil when HARNESS_MEMORY is off.
					if a.memStoreRef != nil {
						cfg.MemoryStore = &memoryStoreListAdapter{store: a.memStoreRef}
					}
					// Audit adapter — always available when the ring buffer is live.
					if a.auditImpl != nil {
						cfg.AuditLister = &auditRingAdapter{api: a.auditImpl}
					}
					a.searchAPI = searchview.NewManagerAPIWithConfig(rawDB, cfg)
					return a.searchAPI
				}
			}
		}
	}
	// Fallback: return a nil-safe stub that returns empty results.
	return &stubSearch{}
}

// memoryStoreListAdapter adapts corememory.Store to searchview.MemoryLister.
type memoryStoreListAdapter struct {
	store corememory.Store
}

func (m *memoryStoreListAdapter) List(ctx context.Context) ([]searchview.MemoryChunk, error) {
	chunks, err := m.store.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]searchview.MemoryChunk, len(chunks))
	for i, c := range chunks {
		out[i] = searchview.MemoryChunk{
			ID:        c.ID,
			SessionID: c.SessionID,
			ProjectID: c.ProjectID,
			Content:   c.Content,
			CreatedAt: c.CreatedAt,
		}
	}
	return out, nil
}

// lockdownAuditEmitter implements contextaudit.Emitter by forwarding to
// the rpc/views/audit.API ring buffer via Push. Used exclusively by
// fleet.AuditLockdownBypass so the bypass event reaches the in-process
// audit ring without a separate goroutine or wiring path.
// (fleet-emergency-lockdown-01NDFSEX12 WP03)
type lockdownAuditEmitter struct {
	impl *audit.API
}

func (e lockdownAuditEmitter) Emit(_ context.Context, ev contextaudit.Event) error {
	if e.impl == nil {
		return nil
	}
	e.impl.Push(audit.Entry{
		ID:        fmt.Sprintf("fleet-lockdown-%d", ev.TS.UnixNano()),
		Timestamp: ev.TS.UTC().Format(time.RFC3339Nano),
		Category:  "FLEET",
		Subject:   string(ev.Kind),
		Trailing:  fmt.Sprintf("payload_bytes=%d", len(ev.Payload)),
	})
	return nil
}

// fleetAuditEmitter bridges the catalog/cedar/sync views' auditEmitter interface
// (which requires EmitFleetEvent) to the rpc/views/audit.API ring buffer via Push.
// Implements the subset of contextaudit.Emitter needed by the SaS surfaces.
// (fleet-share-and-sync-01NDFSEX14)
type fleetAuditEmitter struct {
	impl *audit.API
}

func (e *fleetAuditEmitter) EmitFleetEvent(_ context.Context, kind contextaudit.Kind, payload any) error {
	if e.impl == nil {
		return nil
	}
	e.impl.Push(audit.Entry{
		ID:        fmt.Sprintf("fleet-sas-%d", time.Now().UnixNano()),
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Category:  "FLEET",
		Subject:   string(kind),
		Trailing:  fmt.Sprintf("payload_type=%T", payload),
	})
	return nil
}

// acpAuditBridge implements contextaudit.Emitter by forwarding to the
// rpc/views/audit.API ring buffer via Push. Used exclusively by the ACP
// view to route KindACPEnvelope events to the in-process audit ring.
// (acp-orchestration-integration-01NDFSEX06)
type acpAuditBridge struct {
	impl *audit.API
}

func (e *acpAuditBridge) Emit(_ context.Context, ev contextaudit.Event) error {
	if e.impl == nil {
		return nil
	}
	e.impl.Push(audit.Entry{
		ID:        fmt.Sprintf("acp-%d", ev.TS.UnixNano()),
		Timestamp: ev.TS.UTC().Format(time.RFC3339Nano),
		Category:  "ACP",
		Subject:   string(ev.Kind),
		Trailing:  fmt.Sprintf("payload_bytes=%d", len(ev.Payload)),
	})
	return nil
}

// auditRingAdapter adapts audit.API to searchview.AuditLister.
type auditRingAdapter struct {
	api *audit.API
}

func (ar *auditRingAdapter) ListEntriesForSearch(ctx context.Context, limit int) ([]searchview.AuditEntry, error) {
	filter := audit.Filter{Limit: limit}
	entries, err := ar.api.ListEntries(ctx, filter)
	if err != nil {
		return nil, err
	}
	out := make([]searchview.AuditEntry, len(entries))
	for i, e := range entries {
		out[i] = searchview.AuditEntry{
			ID:        e.ID,
			Timestamp: e.Timestamp,
			Category:  e.Category,
			Subject:   e.Subject,
			Trailing:  e.Trailing,
		}
	}
	return out, nil
}

// Update returns the auto-update view (mission auto-update, v0.4.0 WP03).
// When the chassis booted without a real core/update.Service (test
// path / empty DataDir / empty BuildVersion), the fallback returns a
// graceful-empty surface that surfaces ErrServiceUnavailable on every
// state-mutating method.
func (a *API) Update() updateview.UpdateAPI {
	if a == nil || a.updateAPI == nil {
		return updateview.New(updateview.Config{})
	}
	return a.updateAPI
}

// Storage returns the storage-health view accessor (v0.5.1 migration-doctor).
// Always non-nil; returns ErrStorageUnavailable on every method when the
// chassis has no real storage.DB (e.g. rpc.New(nil) test path).
func (a *API) Storage() storageview.StorageAPI {
	if a == nil || a.storageAPI == nil {
		return storageview.NewAPI(nil, "")
	}
	return a.storageAPI
}

// stubSearch is a safe no-op SearchAPI for use before storage is wired.
type stubSearch struct{}

func (s *stubSearch) Search(_ context.Context, _ string, _ searchview.SearchFilters) ([]searchview.SearchHit, error) {
	return nil, nil
}

func (s *stubSearch) UnifiedSearch(_ context.Context, _ string, _ searchview.SearchFilters) ([]searchview.SearchHit, error) {
	return nil, nil
}

// Bindings returns the slice of Wails-bound objects. The Bindings struct
// (bindings.go) is the flat-method surface Wails reflects. Stable for the
// lifetime of API.
func (a *API) Bindings() []any { return []any{a.bindings} }

// StreamBroker returns the lazily-constructed broker. Future view
// bridges (sessions, audit, …) reuse this instance so the privacy CI
// invariant #1 — only emitter.go / stream_broker.go call
// runtime.EventsEmit — keeps holding.
func (a *API) StreamBroker() *StreamBroker { return a.broker }

// EventBus returns the in-process event bus that mirrors every event the
// StreamBroker publishes.  Served-mode WebSocket handlers subscribe here
// to receive real-time push notifications without the Wails runtime
// context.  The desktop Wails path is unaffected.
func (a *API) EventBus() *EventBus { return a.eventBus }

// buildCedarEngineOrNil constructs a *cedar.Engine for callers that
// need the concrete Engine type — the cedarpolicy view (ListPolicies /
// Reload / RecentDecisions / WritePolicySnippet) and the bash gate
// (WP03+). Returns nil when dataDir is empty so callers can degrade
// gracefully rather than booting a disk-walk engine with nowhere to
// walk. Mirrors buildCedarGate's options but returns *Engine instead
// of the Gate interface.
func buildCedarEngineOrNil(dataDir string) *cedar.Engine {
	if dataDir == "" {
		return nil
	}
	engine, err := cedar.NewEngine(cedar.Options{
		DataDir:         dataDir,
		LoadFromDisk:    true,
		IncludeEmbedded: true,
		DefaultDeny:     false,
	})
	if err != nil {
		slog.Warn("cedar engine construction failed; consumer falls back to its safe-default behaviour",
			"err", err, "data_dir", dataDir)
		return nil
	}
	return engine
}

// buildCedarGate constructs the production Cedar policy gate. It loads
// any user-supplied policies from <DataDir>/policy/*.cedar in addition
// to the Engine's embedded default bundle (which permits the five gate
// categories with logging — the harness's documented default-allow
// stance, not fail-closed).
//
// Failure modes:
//   - empty dataDir: returns AllowAll (no policy loading possible)
//   - DataDir/policy/ doesn't exist: Engine.Reload creates an empty
//     PolicySet on top of the default bundle; that's still default-allow
//   - policy file fails to parse: Engine.Reload reports per-file errors
//     in ListPolicies(); the audit panel surfaces them. The Engine
//     keeps its prior PolicySet active (or the embedded default if
//     this is the first load) so the chassis never boots in an
//     unexpected fail-closed posture due to a typo.
//   - construction itself errors: log a warning + fall back to
//     AllowAll so the chassis boots
func buildCedarGate(dataDir string) cedar.Gate {
	if dataDir == "" {
		return cedar.AllowAll{}
	}
	engine, err := cedar.NewEngine(cedar.Options{
		DataDir:         dataDir,
		LoadFromDisk:    true,
		IncludeEmbedded: true,
		// DefaultDeny=false — preserve the documented default-allow
		// stance. Users opt into fail-closed by setting DefaultDeny=true
		// once the policy engine settles down (a future settings toggle
		// will surface this).
		DefaultDeny: false,
	})
	if err != nil {
		slog.Warn("cedar engine construction failed; falling back to AllowAll",
			"err", err, "data_dir", dataDir)
		return cedar.AllowAll{}
	}
	return engine
}

// buildJournalWriter constructs the SQL-backed memory hook journal
// writer (migration 0308). Returns nil when the storage layer doesn't
// expose a stdlib *sql.DB (in which case the HookManager falls back
// to its in-memory ring buffer; persistence is just not available).
//
// The structural type assertion below is the wiring bridge: it asks
// the storage.DB whether it satisfies the SQL-handle shape without
// requiring storage.DB to grow a public method. The sqlite-backed
// concreteDB does satisfy it.
func buildJournalWriter(c *core.Core) coreag.JournalWriter {
	if c == nil {
		return nil
	}
	store := c.Storage()
	if store == nil {
		return nil
	}
	type sqlHandle interface{ SQL() *sql.DB }
	h, ok := store.(sqlHandle)
	if !ok {
		return nil
	}
	rawDB := h.SQL()
	if rawDB == nil {
		return nil
	}
	return coreag.NewSQLJournalWriter(rawDB)
}

// flatPermissionRequest mirrors frontend `PermissionRequest` (see
// frontend/src/lib/types.ts). Built per-emit so the modal binds
// directly without walking the typed surface.
type flatPermissionRequest struct {
	RequestID       string             `json:"request_id"`
	SessionID       string             `json:"session_id,omitempty"`
	Family          string             `json:"family"`
	ResourceDisplay string             `json:"resource_display"`
	ResourceUID     string             `json:"resource_uid,omitempty"`
	Reason          string             `json:"reason,omitempty"`
	DangerousTier   bool               `json:"dangerous_tier,omitempty"`
	DangerCopy      string             `json:"danger_copy,omitempty"`
	Op              string             `json:"op,omitempty"`
	Surface         cedar.PromptSurface `json:"surface"`
	IssuedAt        string             `json:"issued_at"`
	DeadlineAt      string             `json:"deadline_at"`
}

// flattenPendingRequest projects cedar.PendingRequest into the flat
// shape the frontend permission modals bind to. Each family fills
// resource_display from its surface fields:
//   - bash: full argv joined (e.g. "aws --version") — what the user
//     needs to see to make a decision, NOT just the derived pattern.
//   - fs: canonical path + op (e.g. "read /Users/alice/code/main.go").
//   - cred: provider_id + purpose (e.g. "openai · stream").
//   - tool: server_name__tool_name (e.g. "filesystem__read_file").
func flattenPendingRequest(p cedar.PendingRequest) flatPermissionRequest {
	out := flatPermissionRequest{
		RequestID:  p.RequestID,
		SessionID:  p.Surface.SessionID,
		Family:     string(p.Family),
		Surface:    p.Surface,
		IssuedAt:   p.IssuedAt.UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		DeadlineAt: p.DeadlineAt.UTC().Format("2006-01-02T15:04:05.000Z07:00"),
	}
	switch {
	case p.Surface.Bash != nil:
		argv := p.Surface.Bash.Argv
		if len(argv) > 0 {
			out.ResourceDisplay = strings.Join(argv, " ")
		} else {
			out.ResourceDisplay = p.Surface.Bash.Pattern
		}
		out.ResourceUID = p.Surface.Bash.Pattern
		out.DangerousTier = p.Surface.Bash.Dangerous
	case p.Surface.FS != nil:
		op := p.Surface.FS.Op
		path := p.Surface.FS.CanonicalPath
		if op != "" && path != "" {
			out.ResourceDisplay = op + " " + path
		} else if path != "" {
			out.ResourceDisplay = path
		} else {
			out.ResourceDisplay = op
		}
		out.ResourceUID = path
		out.Op = op
		out.DangerousTier = p.Surface.FS.Dangerous
	case p.Surface.Cred != nil:
		provider := p.Surface.Cred.ProviderID
		purpose := p.Surface.Cred.Purpose
		switch {
		case provider != "" && purpose != "":
			out.ResourceDisplay = provider + " · " + purpose
		case provider != "":
			out.ResourceDisplay = provider
		default:
			out.ResourceDisplay = purpose
		}
		out.ResourceUID = provider
	case p.Surface.Tool != nil:
		server := p.Surface.Tool.ServerName
		tool := p.Surface.Tool.ToolName
		switch {
		case server != "" && tool != "":
			out.ResourceDisplay = server + "__" + tool
		case tool != "":
			out.ResourceDisplay = tool
		default:
			out.ResourceDisplay = server
		}
		out.ResourceUID = out.ResourceDisplay
	}
	return out
}

// buildAgentGraphEventLog wires the agentgraph kernel's EventLog to the
// SQLite-backed implementation when the storage layer exposes a stdlib
// *sql.DB. Same structural-interface dance as buildJournalWriter so
// storage.DB doesn't need to grow a public method. Returns nil when
// the handle is unavailable; the manager falls back to NewMemoryEventLog.
func buildAgentGraphEventLog(c *core.Core) coreag.EventLog {
	if c == nil {
		return nil
	}
	store := c.Storage()
	if store == nil {
		return nil
	}
	type sqlHandle interface{ SQL() *sql.DB }
	h, ok := store.(sqlHandle)
	if !ok {
		return nil
	}
	rawDB := h.SQL()
	if rawDB == nil {
		return nil
	}
	return coreag.NewSQLEventLog(rawDB)
}

// capCatalogAdapter wraps *llmcap.Catalog to satisfy the llm.CapCatalog
// interface, translating the capabilities-package AttachmentDescriptor
// return type to the view-layer AttachmentLimitsResult.
// (multimodal-io-01KQ8TDF WP04)
type capCatalogAdapter struct {
	cat *llmcap.Catalog
}

func (a *capCatalogAdapter) ContextWindow(provider, model string) int {
	return a.cat.ContextWindow(provider, model)
}

func (a *capCatalogAdapter) MaxOutputTokens(provider, model string) int {
	return a.cat.MaxOutputTokens(provider, model)
}

func (a *capCatalogAdapter) AttachmentLimits(provider, model string) llm.AttachmentLimitsResult {
	d := a.cat.AttachmentLimits(provider, model)
	return llm.AttachmentLimitsResult{
		ImageInput:              d.ImageInput,
		DocumentInput:           d.DocumentInput,
		MaxImageBytes:           d.MaxImageBytes,
		MaxDocumentBytes:        d.MaxDocumentBytes,
		MaxImageCountPerMessage: d.MaxImageCountPerMessage,
		MaxImagePixels:          d.MaxImagePixels,
		MaxDocumentPages:        d.MaxDocumentPages,
		ImageInputMimeTypes:     d.ImageInputMimeTypes,
		DocumentInputMimeTypes:  d.DocumentInputMimeTypes,
	}
}

// ── Sub-agent profile registry RPCs (branch-subagent-interactive-01KZNP3B WP01) ──

// Agents returns the sub-agent profile registry RPC surface. When not yet
// wired (test harness path with New(nil)), returns an empty-DataDir API.
func (a *API) Agents() *agentsview.API {
	if a.agentsAPI == nil {
		return agentsview.New("")
	}
	return a.agentsAPI
}

// Agents_ListProfiles returns summary entries for all known profiles
// (bundled + user-authored). Wails-bound.
func (a *API) Agents_ListProfiles(ctx context.Context) ([]agentsview.ProfileSummaryWire, error) {
	return a.Agents().ListProfiles(ctx)
}

// Agents_LoadProfile returns the full profile for the given id. Wails-bound.
func (a *API) Agents_LoadProfile(ctx context.Context, id string) (agentsview.ProfileWire, error) {
	return a.Agents().LoadProfile(ctx, id)
}

// Agents_SaveProfile creates or updates a user-authored profile. Wails-bound.
func (a *API) Agents_SaveProfile(ctx context.Context, profile agentsview.ProfileWire) error {
	return a.Agents().SaveProfile(ctx, profile)
}

// Agents_DeleteProfile removes a user-authored profile by id. Wails-bound.
func (a *API) Agents_DeleteProfile(ctx context.Context, id string) error {
	return a.Agents().DeleteProfile(ctx, id)
}

// Sentry implements HarnessAPI. Returns the crash-reporting RPC surface.
// (sentry-error-monitoring-01KX5R8G WP05)
func (a *API) Sentry() sentryview.SentryAPI { return a.sentryAPI }

// Fleet implements HarnessAPI. Returns the fleet telemetry consent RPC surface.
// (fleet-otel-archival-01NDFSEX11 WP07)
func (a *API) Fleet() fleetview.FleetAPI { return a.fleetAPI }

// Catalog implements HarnessAPI. Returns the fleet catalog publish/list/install surface.
// (fleet-share-and-sync-01NDFSEX14 WP02)
func (a *API) Catalog() catalogview.CatalogAPI { return a.catalogAPI }

// Sync implements HarnessAPI. Returns the per-category settings sync surface.
// (fleet-share-and-sync-01NDFSEX14 WP05)
func (a *API) Sync() syncview.SyncAPI { return a.syncAPI }

// CedarPublish implements HarnessAPI. Returns the team Cedar policy publish surface.
// (fleet-share-and-sync-01NDFSEX14 WP07)
func (a *API) CedarPublish() cedarview.CedarAPI { return a.cedarPublishAPI }

// Sites implements HarnessAPI. Returns the fleet-hosted sites RPC surface.
// (sites-ui-01NSITE06)
func (a *API) Sites() sitesview.SitesAPI { return a.sitesAPI }

// Tasks implements HarnessAPI. Returns the background-task registry RPC surface.
// (background-task-monitor-01KZNP3C WP05 / FR-003)
func (a *API) Tasks() tasksview.TasksAPI { return a.tasksAPI }

// ACP implements HarnessAPI. Returns the ACP peer management + envelope
// dispatch surface (acp-orchestration-integration-01NDFSEX06).
func (a *API) ACP() acpview.ACPAPI { return a.acpAPI }

// brokerPlanEmitter adapts a *StreamBroker to the planmodeview.EventEmitter
// interface. The broker's Publish method broadcasts to all subscribers
// on the given topic; the planmode events are published as-is on the
// event name so the frontend's usePlanMode composable receives them via
// the Wails runtime.EventsOn subscription.
//
// The topic is the event name directly (e.g. "plan_mode_changed") rather
// than a session-namespaced topic, because the frontend subscribes by
// the bare event name and filters on payload.session_id. This is
// consistent with how elicit/ask events are published.
type brokerPlanEmitter struct {
	broker *StreamBroker
}

// Emit publishes the payload on the event name. sessionID is embedded in
// the payload by the planmodeview.API.emitChanged helper; this adapter
// does not re-embed it.
func (e *brokerPlanEmitter) Emit(_ context.Context, _ string, event string, payload map[string]any) error {
	if e.broker == nil {
		return nil
	}
	e.broker.Publish(event, payload)
	return nil
}

// Broker returns the StreamBroker so main.go can wire it into the native
// menu controller (os-menu-bar-01NDFSEX16 WP03). The broker is always
// non-nil on the production chassis; tests that call rpc.New(nil) may
// receive a broker without a Wails context (safe to call Publish on).
func (a *API) Broker() *StreamBroker {
	if a == nil {
		return nil
	}
	return a.broker
}

// SettingsStore returns the underlying settings persistence store so the
// native menu can read the current theme at startup without going through
// the Wails-bound Settings_Get RPC (which needs a Wails context that is
// not yet available at menu construction time).
// (os-menu-bar-01NDFSEX16 WP03)
func (a *API) SettingsStore() settings.SettingsStore {
	if a == nil || a.settingsImpl == nil {
		return nil
	}
	return a.settingsImpl.Store()
}

// UpdateStartCheck triggers an immediate update check via the Manager if wired.
// Used by the native menu "Check for Updates" handler (os-menu-bar-01NDFSEX16 WP03).
// Safe to call with a nil updateAPI.
func (a *API) UpdateStartCheck(ctx context.Context) {
	if a == nil || a.updateAPI == nil {
		return
	}
	if err := a.updateAPI.StartCheck(ctx); err != nil {
		logging.L().Warn("menu.update.check_failed", "err", err.Error())
	}
}
