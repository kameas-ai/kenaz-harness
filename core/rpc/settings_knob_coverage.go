// settings_knob_coverage.go registers every exported field of
// settings.Settings with core/wiring/knobcoverage
// (controls-and-readouts-that-tell-the-truth-01PMZ808 WP22, spec §5 G-1).
//
// Before this file, settings.Settings — 82 exported fields as of this
// commit — was entirely outside the knobcoverage mechanism:
// scripts/ci/check-knob-coverage.sh's own header conceded "the inert
// dials in [settings.Settings and dials.DialConfig] were found by hand
// this sweep and not by this gate." Five of this mission's own findings
// (MaxVisibleBranchDepth, AutoCollapseBranchesInSidebar,
// DeleteBranchesWithParent, WindowSize, BranchAdvisorDefaultModel, plus
// LocalRuntimeRAMOverrideGB) are exactly the class this registration
// pass now makes visible to CI on every future PR.
//
// # One field is registered elsewhere, on purpose
//
// HarnessSelfMCPDisabled is NOT registered here.
// core/rpc/harness_self_mcp_disabled_knob_coverage.go
// (harness-self-attach-01PMHS01 UNIT-7) registered it ahead of this WP,
// specifically so this pass would not have to discover it cold —
// knobcoverage.Register panics on a duplicate (type, field) pair, so
// re-registering it here would panic at init time. Both files live in
// package rpc, so both init() functions run in the same test binary
// whenever anything under ./core/rpc/... is tested (including the
// TestKnobCoverage_Settings guard in settings_knob_coverage_test.go),
// and knobcoverage.Uncovered[settings.Settings]() sees the union of
// both files' registrations.
//
// # Bookkeeping, not a wiring proof
//
// knobcoverage.Register[T] accepts any non-empty description string and
// verifies nothing about the named consumer — this pass is a bookkeeping
// forcing function (a future field with no entry here fails CI loudly),
// not independent re-verification of every consumer cited below. Three
// known scope limits (spec §5 G-1, "coverage this actually buys — five of
// eight"):
//
//   - SchemaVersion registers clean (three real default-backfill reads)
//     while its documented defect — no code compares it against any value
//     other than zero, so it gates no migration — survives. NARROWED,
//     not wired (spec §4.4, D-4).
//   - MaxGeneratedImageBytes registers clean (EffectiveMaxGeneratedImageBytes
//     has a real caller at core/rpc/api.go:2240) while its Save-time
//     validator gap (FR-032/SD-14 — neither this field nor
//     LocalRuntimeRAMOverrideGB is reachable from any of SaveAll's four
//     validators) is a doc-vs-validator divergence this mechanism cannot
//     express.
//   - The two SD-06/SD-07 (serve) ids spec §1.12 could not spec at all are
//     interface *methods*, not struct fields — knobcoverage reflects over
//     reflect.StructField and is structurally blind to that class.
package rpc

import (
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
	"github.com/kameas-ai/kenaz-harness/core/wiring/knobcoverage"
)

func init() {
	// ── Identity / shell chrome ─────────────────────────────────────
	knobcoverage.Register[settings.Settings](
		"SchemaVersion",
		"NARROWED (controls-and-readouts-that-tell-the-truth-01PMZ808 "+
			"§4.4/D-4): three default-backfill reads only "+
			"(core/rpc/views/settings/impl.go:84-85,:308-309,:1580-1581, "+
			"'if got.SchemaVersion == 0 { ... = 1 }'); no migration "+
			"dispatcher compares it to anything else. Registered per spec "+
			"§5 G-1's own instruction that an honest implementer registers "+
			"a field with a real, if partial, reader rather than deferring it.",
	)
	knobcoverage.Register[settings.Settings](
		"LastRoute",
		"frontend/src/lib/routing.ts's restoreLastRoute, called from "+
			"App.vue:164 on boot, navigates the router to this value.",
	)
	knobcoverage.Register[settings.Settings](
		"Theme",
		"main.go:610-611 seeds coremenus.MenuState.ThemeMode from it at "+
			"startup (menu radio-check), and core/rpc/sync_categories.go's "+
			"ui_theme fleet-sync category marshals/applies it alongside "+
			"Accent; frontend/src/App.vue's useTheme() applies the live value.",
	)
	knobcoverage.Register[settings.Settings](
		"Accent",
		"core/rpc/sync_categories.go:71,:83 — the ui_theme fleet-sync "+
			"category marshals and applies this field alongside Theme.",
	)
	knobcoverage.Register[settings.Settings](
		"WindowSize",
		"controls-and-readouts-that-tell-the-truth-01PMZ808 WP06/UNIT-3: "+
			"main.go's OnStartup/OnShutdown hooks call wailsruntime."+
			"WindowSetSize/WindowGetSize against this value, so a resize "+
			"survives a relaunch (mission-branch commit b1cf9029).",
	)
	knobcoverage.Register[settings.Settings](
		"MemoryEnabled",
		"frontend/src/views/tools/KenazToolsPanel.vue reads it via "+
			"client.settings.getMemory() (Settings_GetMemory binding) to "+
			"gate visibility of the memory-tool toggles (v-if=\"memoryEnabled\").",
	)

	// ── Confirm-each / artifact-capture toggles ─────────────────────
	knobcoverage.Register[settings.Settings](
		"ConfirmEachDisabled",
		"ConfirmEachEnabled() accessor; the confirm-each tool-call modal "+
			"flag (WP05) read by the toolloop confirmation gate.",
	)
	knobcoverage.Register[settings.Settings](
		"AutoCaptureCodeBlocksDisabled",
		"AutoCaptureCodeBlocks() accessor consumed by the artifacts-storage "+
			"code-block detector (FR-017).",
	)
	knobcoverage.Register[settings.Settings](
		"CodeBlockMinLines",
		"EffectiveCodeBlockMinLines() accessor consumed by the code-block "+
			"auto-capture detector's line threshold.",
	)
	knobcoverage.Register[settings.Settings](
		"CodeBlockMinBytes",
		"EffectiveCodeBlockMinBytes() accessor consumed by the code-block "+
			"auto-capture detector's byte threshold.",
	)
	knobcoverage.Register[settings.Settings](
		"AutoCaptureToolOutputsDisabled",
		"AutoCaptureToolOutputs() accessor consumed by the tool-output "+
			"auto-capture detector (artifacts-storage FR-017).",
	)

	// ── Builtin-tool enable dials ────────────────────────────────────
	knobcoverage.Register[settings.Settings](
		"WebSearchEnabled",
		"core/tools/websearch builtin registration predicate — gates "+
			"whether kenaz__web_search is attached to a session.",
	)
	knobcoverage.Register[settings.Settings](
		"BashEnabled",
		"core/tools/bash builtin registration predicate — gates whether "+
			"kenaz__bash is attached to a session.",
	)
	knobcoverage.Register[settings.Settings](
		"WebFetchEnabled",
		"kenaz__web_fetch builtin registration predicate "+
			"(crash-recovery-tool-gating-0XQTC4RK FR-005).",
	)
	knobcoverage.Register[settings.Settings](
		"SaveArtifactDisabled",
		"SaveArtifactEnabled() accessor — kenaz__save_artifact builtin "+
			"registration predicate.",
	)
	knobcoverage.Register[settings.Settings](
		"FSReadDisabled",
		"FSReadEnabled() accessor — the read-family builtin filesystem "+
			"tools' (kenaz__read_file etc.) registration predicate "+
			"(builtin-filesystem-tools-01KR3N4P).",
	)
	knobcoverage.Register[settings.Settings](
		"FSWriteDisabled",
		"FSWriteEnabled() accessor — the write-family builtin filesystem "+
			"tools' (kenaz__write_file, kenaz__edit_file) registration "+
			"predicate (builtin-filesystem-tools-01KR3N4P).",
	)
	knobcoverage.Register[settings.Settings](
		"TodoDisabled",
		"TodoEnabled() accessor — kenaz__todo_write builtin registration "+
			"predicate (builtin-tools-search-and-elicitation-01KZNP3D).",
	)
	knobcoverage.Register[settings.Settings](
		"FSRequestAccessDisabled",
		"FSRequestAccessEnabled() accessor — kenaz__request_filesystem_access "+
			"builtin registration predicate.",
	)
	knobcoverage.Register[settings.Settings](
		"SearchDisabled",
		"SearchEnabled() accessor — the cross-session search modal's "+
			"backend short-circuit (cross-session-search-01KQ8TDQ WP07): "+
			"when true, SearchAPI returns an empty result set regardless "+
			"of the FTS5 index.",
	)
	knobcoverage.Register[settings.Settings](
		"MultimodalInputDisabled",
		"Settings_GetMultimodalInput/-SetMultimodalInput bindings; the "+
			"frontend hides the paperclip button + drop overlay when false "+
			"(multimodal-io-01KQ8TDF WP08).",
	)
	knobcoverage.Register[settings.Settings](
		"EditFileArtifactSyncDisabled",
		"EditFileArtifactSyncEnabled() accessor gating the edit-file "+
			"artifact sync feature (edit-file-artifact-sync-01KQ8TD5 WP04).",
	)

	// ── Chat-turn / graph dials ──────────────────────────────────────
	knobcoverage.Register[settings.Settings](
		"MaxAgentTurns",
		"EffectiveMaxAgentTurns() threaded onto the chat graph's LoopNode "+
			"body max_iterations on every run start.",
	)
	knobcoverage.Register[settings.Settings](
		"ReasoningBudgetTokens",
		"EffectiveReasoningBudgetTokens() threaded onto the chat graph's "+
			"model node on every run start (wiring-integrity-01PMAG04 WP08).",
	)
	knobcoverage.Register[settings.Settings](
		"AgenticTurnRouting",
		"read at graph-load time by core/rpc/api.go's GraphLoader per "+
			"StartStream; gates agentgraph.GateAgenticTurnRouting's "+
			"router/review node insertion (agentgraph-total-convergence-"+
			"01PMGX01 WP11b).",
	)
	knobcoverage.Register[settings.Settings](
		"ChatCustomInstructions",
		"LoadChatCustomInstructions, read by the chat runner on every "+
			"StartStream as the final layer of the system prompt "+
			"(system-prompt-layers WP04).",
	)
	knobcoverage.Register[settings.Settings](
		"MoveFidelityHistoryDisabled",
		"MoveFidelityHistoryEnabled() accessor, read at the point of "+
			"model-visible history composition on every request "+
			"(model-moves-transcript-01PMCH01 WP03).",
	)
	knobcoverage.Register[settings.Settings](
		"Autonomy",
		"core/rpc/api.go:6277 computeAutonomyKnobs calls "+
			"settingsImpl.LoadAutonomyProfile(ctx) to decode this field as "+
			"the global layer folded into every session's resolved "+
			"autonomy knobs (autonomy-dial-01KR3M2A WP02).",
	)

	// ── Compaction dials ─────────────────────────────────────────────
	knobcoverage.Register[settings.Settings](
		"CompactionAggressiveness",
		"EffectiveCompactionAggressiveness() read by the chat runner on "+
			"every send to select the summarisation tier.",
	)
	knobcoverage.Register[settings.Settings](
		"CompactionModel",
		"read directly by the chat runner's summarisation call, and as "+
			"EffectiveBranchAdvisorDefaultModel's fallback target.",
	)
	knobcoverage.Register[settings.Settings](
		"CompactionArchiveDays",
		"EffectiveCompactionArchiveDays() read by the soft-archive "+
			"retention sweep.",
	)
	knobcoverage.Register[settings.Settings](
		"CompactionRecentWindow",
		"EffectiveCompactionRecentWindow() read by the compaction pass "+
			"to exclude the most-recent user-assistant pairs.",
	)
	knobcoverage.Register[settings.Settings](
		"MonthlyCostNotifyUSD",
		"controls-and-readouts-that-tell-the-truth-01PMZ808 WP19/R-11: "+
			"core/rpc/api.go:1486-1499 builds a usage.ThresholdReader that "+
			"calls LoadMonthlyCostNotifyUSD() fresh on every Manager.Add "+
			"tail (core/usage/usage.go:110-114).",
	)
	knobcoverage.Register[settings.Settings](
		"MaxGeneratedImageBytes",
		"EffectiveMaxGeneratedImageBytes() consumed at core/rpc/api.go:2240 "+
			"(cfg.MaxGeneratedImageBytes = loaded.EffectiveMaxGeneratedImageBytes()) "+
			"for the generated-image auto-capture pipeline's byte cap. "+
			"FR-032/SD-14's Save-validator gap (this field is unreachable "+
			"from any of SaveAll's four validators) is a separate, "+
			"documented defect this registration does not paper over.",
	)

	// ── Permission / Cedar dials ─────────────────────────────────────
	knobcoverage.Register[settings.Settings](
		"PermissionMode",
		"EffectivePermissionMode() read by the universal permission gate "+
			"across bash/filesystem/credential/tool resource families.",
	)
	knobcoverage.Register[settings.Settings](
		"PermissionCacheDangerousOps",
		"read by the bash and filesystem permission modals to decide "+
			"whether 'Allow always' is offered for dangerous-tier ops.",
	)
	knobcoverage.Register[settings.Settings](
		"BashAllowlistMigrated",
		"read by the first-boot bash-allowlist migration runner to "+
			"suppress its one-time UI toast after the migration has run.",
	)
	knobcoverage.Register[settings.Settings](
		"PermissionsMigrationToastShown",
		"read by the permissions-migration one-time toast to avoid "+
			"re-showing after the user has seen it once.",
	)
	knobcoverage.Register[settings.Settings](
		"CedarStrictCredentialMode",
		"read by the credstore Cedar gate to decide whether a "+
			"NotApplicable outcome is treated as allow (lenient) or deny "+
			"(strict) for non-mcp_spawn purposes (cedar-credential-policy-"+
			"01KQ8TDE WP05).",
	)
	knobcoverage.Register[settings.Settings](
		"CedarStrictWorkflowMode",
		"read live on every workflow run/save as the `mode` context "+
			"attribute the Workflow-family Cedar bundle branches on "+
			"(strict makes default_workflows_policy.cedar's shell-step "+
			"forbid rule fire).",
	)
	knobcoverage.Register[settings.Settings](
		"GraphAuthoringEnabled",
		"read live on every graph.author Cedar evaluation as "+
			"context.authoring_enabled (model-authored-graphs-01PMGA01 "+
			"UNIT-4, FR-006).",
	)
	knobcoverage.Register[settings.Settings](
		"CredentialAuditRetentionDays",
		"read by the daily credstore sweep goroutine to prune "+
			"KindCredentialAccessed audit rows older than N days "+
			"(credential-store-01KQ8TDD WP07).",
	)
	knobcoverage.Register[settings.Settings](
		"BundleSigningPolicy",
		"EffectiveBundleSigningPolicy() read by `harness bundle install` "+
			"signature verification (bundle-download-and-verify-01PMZ909 "+
			"UNIT-4).",
	)

	// ── Branch advisor / branching-UX dials ───────────────────────────
	// BranchAdvisorDefaultModel is intentionally absent: NARROWED
	// (controls-and-readouts-that-tell-the-truth-01PMZ808 WP05/spec §4.5,
	// mission-branch commit 2367ed3d) — neither reader nor writer exists,
	// and EffectiveBranchAdvisorDefaultModel's fallback target
	// (views/branches/impl.go's parentModel) is itself a stub that
	// discards both parameters. Deferred rather than registered so the
	// gate does not certify a fallback chain that resolves to nothing.
	knobcoverage.RegisterDeferred[settings.Settings](
		"BranchAdvisorDefaultModel",
		"NARROWED (controls-and-readouts-that-tell-the-truth-01PMZ808 "+
			"WP05, spec §4.5/R-6, mission-branch commit 2367ed3d): neither "+
			"reader nor writer exists, and EffectiveBranchAdvisorDefaultModel's "+
			"fallback target (core/rpc/views/branches/impl.go's parentModel) "+
			"is a stub returning (\"\", \"\") unconditionally. Wiring this "+
			"field alone would produce a dial that appears to work and "+
			"resolves to nothing. Blocked on model-settings-reach-the-model-"+
			"01PMZ101 wiring CompactionModel end-to-end. Owner: alec, "+
			"dated 2026-08-20 in docs/unwired-ledger.md.",
	)
	knobcoverage.Register[settings.Settings](
		"BranchAdvisorEnabled",
		"master on/off read by ChatInput.vue's runAdvisorDetector before "+
			"the confidence check — when false the branch-advisor banner "+
			"never mounts.",
	)
	knobcoverage.Register[settings.Settings](
		"BranchAdvisorMinConfidence",
		"EffectiveBranchAdvisorMinConfidence() read by the branch-advisor "+
			"heuristic-score gate; the banner mounts only at or above this "+
			"threshold (Q29.1).",
	)
	knobcoverage.RegisterDeferred[settings.Settings](
		"BranchAdvisorUseLLM",
		"documented reserved field (api.go doc comment, FR-013): "+
			"'no implementation in v1' — the optional LLM-backed detector "+
			"was never built.",
	)
	knobcoverage.RegisterDeferred[settings.Settings](
		"BranchAutoMode",
		"documented reserved field (api.go doc comment, FR-014): "+
			"'no implementation in v1' — auto-branching above a higher "+
			"confidence threshold was never built.",
	)
	knobcoverage.Register[settings.Settings](
		"BranchReintegrationMaxTokens",
		"EffectiveBranchReintegrationMaxTokens() read by "+
			"ProposeReintegrationSummary to cap the reintegration summary "+
			"length (FR-008a).",
	)
	knobcoverage.Register[settings.Settings](
		"AutoCollapseBranchesInSidebar",
		"controls-and-readouts-that-tell-the-truth-01PMZ808 WP03/UNIT-1: "+
			"LeftRail.vue's branchCollapsed initial-state seed, read via "+
			"EffectiveAutoCollapseBranchesInSidebar() (mission-branch "+
			"commit e6578093). *bool, not bool — see the field's own doc "+
			"comment for why.",
	)
	knobcoverage.Register[settings.Settings](
		"DeleteBranchesWithParent",
		"controls-and-readouts-that-tell-the-truth-01PMZ808 WP04/UNIT-2 "+
			"(spec §4.3 Option B): branched on inside "+
			"core/rpc/views/sessions/impl.go's DeleteWithOptions funnel via "+
			"a sessions.Config field (mission-branch commit 2367ed3d).",
	)
	knobcoverage.Register[settings.Settings](
		"MaxVisibleBranchDepth",
		"controls-and-readouts-that-tell-the-truth-01PMZ808 WP01/UNIT-1: "+
			"EffectiveMaxVisibleBranchDepth(), read by LeftRail.vue's "+
			"maxBranchDepth and clamped into SessionTreeRow's indentPx "+
			"(mission-branch commit e6578093).",
	)

	// ── Embedder / memory dials ────────────────────────────────────────
	knobcoverage.Register[settings.Settings](
		"EmbedderProviderProfileID",
		"read by the embedder construction path (core/rpc/api.go's "+
			"newEmbedder) to select the personal-provider profile used "+
			"for embedding.",
	)
	knobcoverage.Register[settings.Settings](
		"EmbedderModelOverride",
		"read by the embedder construction path to override the "+
			"per-Kind default embeddings model.",
	)
	knobcoverage.Register[settings.Settings](
		"MemoryNarrativeEnabled",
		"read by the compactor to decide whether the narrative_first "+
			"strategy runs at all (memory-narrative-layer-01KQ8TD1 WP12).",
	)
	knobcoverage.Register[settings.Settings](
		"SummarizerProfileID",
		"read by the narrative synthesis worker to select the provider "+
			"profile for per-turn LLM synthesis (WP04).",
	)
	knobcoverage.Register[settings.Settings](
		"NarrativePromotionWeights",
		"read by the long-term promotion scorer as the per-signal weight "+
			"map (retrieval/citation/pin) (WP06).",
	)
	knobcoverage.Register[settings.Settings](
		"NarrativePromotionThreshold",
		"read by the long-term promotion scorer as the score floor "+
			"(WP06).",
	)
	knobcoverage.Register[settings.Settings](
		"NarrativeRetrievalWeight",
		"read by similarity search as the narrative-chunk score "+
			"multiplier (WP01).",
	)
	knobcoverage.Register[settings.Settings](
		"NarrativePromoterParallelism",
		"read by the narrative promoter to size its synthesis worker "+
			"pool (WP03).",
	)
	knobcoverage.Register[settings.Settings](
		"NarrativePreludeTopN",
		"read at session start to size the long-term-chunk prelude "+
			"loaded into the system prompt (WP09).",
	)
	knobcoverage.Register[settings.Settings](
		"ShowPerMessageTokenMeter",
		"read by the frontend to gate the per-message token-cost chip "+
			"(per-message-token-meter-01KR3PQR).",
	)
	knobcoverage.Register[settings.Settings](
		"LongSessionNudgeTurns",
		"controls-and-readouts-that-tell-the-truth-01PMZ808 WP13/UNIT-8: "+
			"EffectiveLongSessionNudgeTurns() read by the long-session "+
			"nudge to decide the turn-count trigger (mission-branch "+
			"commit 12906668).",
	)
	knobcoverage.Register[settings.Settings](
		"LongSessionNudgeTokens",
		"controls-and-readouts-that-tell-the-truth-01PMZ808 WP13/UNIT-8: "+
			"EffectiveLongSessionNudgeTokens() read by the long-session "+
			"nudge against cumulative usage (mission-branch commit "+
			"12906668).",
	)
	knobcoverage.Register[settings.Settings](
		"ContextWindowOverrides",
		"read by the frontend context-window meter (backend-context-"+
			"window-length-01KQ8TD3 WP05) as a per-provider-kind override "+
			"of the catalog's context-window size.",
	)
	knobcoverage.RegisterDeferred[settings.Settings](
		"LocalRuntimeRAMOverrideGB",
		"documented, ledgered inert field (docs/unwired-ledger.md:750, "+
			":765-766, F-1, Owner: unassigned until "+
			"controls-and-readouts-that-tell-the-truth-01PMZ808 WP21 "+
			"re-dated it to owner: alec): EffectiveLocalRuntimeRAMBytes "+
			"has zero callers; its doc names a model-fit filter and a "+
			"settings panel, neither of which was ever built.",
	)

	// ── Auto-update dials ────────────────────────────────────────────
	knobcoverage.Register[settings.Settings](
		"AutoCheckUpdatesDisabled",
		"controls-and-readouts-that-tell-the-truth-01PMZ808 WP07/UNIT-4: "+
			"core/rpc/api.go's SetContext gates BackgroundPoll on "+
			"store.LoadAutoCheckUpdates() (mission-branch commit 05bea0a9).",
	)
	knobcoverage.Register[settings.Settings](
		"UpdateChannel",
		"controls-and-readouts-that-tell-the-truth-01PMZ808 WP07/UNIT-4: "+
			"EffectiveUpdateChannel() threaded into BackgroundPoll's "+
			"channel argument on every SetContext/save cycle.",
	)
	knobcoverage.Register[settings.Settings](
		"UpdateCheckIntervalSec",
		"controls-and-readouts-that-tell-the-truth-01PMZ808 WP07/UNIT-4: "+
			"EffectiveUpdateCheckIntervalSec() threaded into "+
			"BackgroundPoll's interval argument on every SetContext/save "+
			"cycle.",
	)
	knobcoverage.Register[settings.Settings](
		"SkippedUpdateVersions",
		"read by the updater's release-check to filter out versions the "+
			"user explicitly skipped.",
	)
	knobcoverage.Register[settings.Settings](
		"MCPAutoRestartDisabled",
		"MCPAutoRestart() accessor read by the MCP health monitor to "+
			"decide whether to auto-restart a server after consecutive "+
			"ping failures (mcp-server-health-ui-01KQ8TD6 WP06).",
	)
	knobcoverage.Register[settings.Settings](
		"AutoTitleDisabled",
		"core/rpc/api.go:6387 GetAutoTitleEnabled(ctx) gates whether "+
			"session auto-titling runs after a turn "+
			"(p0-wiring-fixes-3TVMG0MX WP05).",
	)
	knobcoverage.Register[settings.Settings](
		"AutoResumeOnKeyRotationDisabled",
		"EffectiveAutoResumeOnKeyRotation() exposed via binding and read "+
			"by the frontend to decide whether to auto-resume a failed "+
			"turn after an API-key rotation (provider-keychain-rotation-"+
			"01KQ8TD9 WP07).",
	)
	knobcoverage.Register[settings.Settings](
		"AutoCaptureGeneratedImagesDisabled",
		"AutoCaptureGeneratedImages() consumed at core/rpc/api.go:2239 "+
			"(cfg.AutoCaptureGeneratedImages = loaded."+
			"AutoCaptureGeneratedImages()) to gate the generated-image "+
			"auto-capture pipeline (multimodal-io-extended-01KQ8TD2 WP02).",
	)

	// ── Crash reporting dials ────────────────────────────────────────
	knobcoverage.Register[settings.Settings](
		"CrashReportingTier",
		"controls-and-readouts-that-tell-the-truth-01PMZ808 WP12/UNIT-7: "+
			"main.go threads it into coresentry.ResolveTier alongside the "+
			"real fleet-login state (mission-branch commit b91797f4).",
	)
	knobcoverage.Register[settings.Settings](
		"SentryDSN",
		"main.go's coresentry.Init(tier, s.SentryDSN, Version, \"\") — "+
			"the Sentry client's data-source name.",
	)
	knobcoverage.Register[settings.Settings](
		"HasSeenCrashReportingOnboarding",
		"read by the frontend to gate the first-launch crash-reporting "+
			"onboarding modal (sentry-error-monitoring-01KX5R8G WP05).",
	)
	knobcoverage.Register[settings.Settings](
		"HasSeenFleetTelemetryOnboarding",
		"read by the frontend to gate the first-launch fleet-telemetry "+
			"onboarding modal shown after sign-in "+
			"(fleet-otel-archival-01NDFSEX11 WP06).",
	)

	// ── Onboarding / shortcuts ─────────────────────────────────────────
	knobcoverage.Register[settings.Settings](
		"FirstRunOnboardingCompleted",
		"core/rpc/views/onboarding/impl.go's State() aggregate ANDs the "+
			"independent FirstRunChecker signal with !Completed "+
			"(out.FirstRun = firstRunRaw && !out.Completed) so a user who "+
			"has completed onboarding is never re-shown it on a cold "+
			"start regardless of provider-configuration state "+
			"(harness-onboarding-01NHON01 WP01; SD-02 hand-off accepted "+
			"per spec §1.13 from trust-surfaces-that-fire-01PMZ202).",
	)
	knobcoverage.Register[settings.Settings](
		"KeyboardShortcuts",
		"controls-and-readouts-that-tell-the-truth-01PMZ808 WP09/UNIT-5: "+
			"Shell.vue's shortcutOverrides, consumed by onGlobalKeydown "+
			"via resolveBinding/matchesEvent so a rebind changes the "+
			"actual handler, not just the cheat sheet.",
	)
	knobcoverage.RegisterDeferred[settings.Settings](
		"KeyboardShortcutsPreset",
		"documented reserved field (api.go doc comment): 'reserved for a "+
			"future preset-gallery follow-up mission. v1 always persists "+
			"as empty string' (keyboard-shortcuts-settings-01KQ8TDR "+
			"plan Q1=C).",
	)
}
