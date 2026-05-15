<script setup lang="ts">
/**
 * SettingsView — app preferences (NN/SECTION pattern).
 *
 * Theme selector (system / light / dark — currently dark only per
 * Kenaz, structured for v1.x), lastRoute toggle, read-only data-dir
 * + harness build info from AppInfo.
 *
 * Persistence routes through the SettingsStore (Settings_Get +
 * Settings_Set RPC); writes are debounced 250ms via lib/settings.ts
 * to coalesce rapid toggles into a single disk write.
 */
import { computed, onMounted, ref } from 'vue';
import { useRoute } from 'vue-router';
import CanvasHead from '@/shell/CanvasHead.vue';
import SettingsTabs from '@/views/settings/SettingsTabs.vue';
import KeyboardShortcuts from '@/components/settings/KeyboardShortcuts.vue';
import AutonomyPanel from '@/views/settings/AutonomyPanel.vue';
import UpdatesPanel from '@/views/settings/UpdatesPanel.vue';
import MigrationDriftPanel from '@/views/settings/health/MigrationDriftPanel.vue';
import CompactionStrategyPanel from '@/views/settings/compaction/CompactionStrategyPanel.vue';
import SlashCommandsView from '@/views/settings/SlashCommandsView.vue';
import FeatureFlagsView from '@/views/settings/FeatureFlagsView.vue';
import HooksSettingsView from '@/views/settings/HooksSettingsView.vue';
import WorkflowsSettingsPanel from '@/views/settings/WorkflowsSettingsPanel.vue';
import ScheduledChatsPanel from '@/views/settings/scheduledchat/ScheduledChatsPanel.vue';
import ModelAccessibleSecretsPanel from '@/views/settings/ModelAccessibleSecretsPanel.vue';
import TasksPanel from '@/views/settings/TasksPanel.vue';
import LLMRoutingPanel from '@/views/settings/LLMRoutingPanel.vue';
import AuditSettingsPanel from '@/views/settings/AuditSettingsPanel.vue';
import LongSessionNudgeSettings from '@/components/settings/LongSessionNudgeSettings.vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { debouncedSave } from '@/lib/settings';
import { markdownExtensionsRef } from '@/lib/markdown/injectionKeys';
import { Plus } from '@/shell/icons';
import AttachmentRow from '@/components/contexts/AttachmentRow.vue';
import AttachmentTreePicker from '@/components/contexts/AttachmentTreePicker.vue';
import type {
  AppInfo,
  Attachment,
  CompactionAggressiveness,
  CompactionTierExplain,
  MarkdownExtensions,
  MemoryEmbedderEligibility,
  Provider,
  Settings,
  Theme,
} from '@/lib/types';

const client = useHarnessClient();

// auto-update-v0.4.0 WP05 — Updates is a sub-tab on the same /settings
// route. We disambiguate via ?tab=updates so we don't have to touch the
// router config. The tab's mount switch is at the bottom of <template>.
const route = useRoute() as ReturnType<typeof useRoute> | undefined;
const showUpdatesTab = computed<boolean>(() => {
  const v = route?.query?.tab;
  return typeof v === 'string' && v === 'updates';
});

// v0.5.1 migration-doctor — Health sub-tab (same route disambiguation
// as Updates; mount switch is in <template> below).
const showHealthTab = computed<boolean>(() => {
  const v = route?.query?.tab;
  return typeof v === 'string' && v === 'health';
});

// compaction-strategy-ui-01KQ8TD8 — Compaction strategy-authoring sub-tab.
// Disambiguates via ?tab=compaction. Mount switch is in <template> below.
const showCompactionTab = computed<boolean>(() => {
  const v = route?.query?.tab;
  return typeof v === 'string' && v === 'compaction';
});

// user-slash-commands-01KQ8TD9 WP07 — Slash Commands sub-tab.
// Disambiguates via ?tab=slashcmds. Mount switch is in <template> below.
const showSlashCmdsTab = computed<boolean>(() => {
  const v = route?.query?.tab;
  return typeof v === 'string' && v === 'slashcmds';
});

// user-slash-commands-01KQ8TD9 WP09 — Feature Flags sub-tab.
// Disambiguates via ?tab=flags. Mount switch is in <template> below.
const showFlagsTab = computed<boolean>(() => {
  const v = route?.query?.tab;
  return typeof v === 'string' && v === 'flags';
});

// hooks-event-surface-expansion-01KZNP3A WP07d — Hooks sub-tab.
// Disambiguates via ?tab=hooks. Mount switch is in <template> below.
const showHooksTab = computed<boolean>(() => {
  const v = route?.query?.tab;
  return typeof v === 'string' && v === 'hooks';
});

// workflow-extensions-01KW2D3Y WP02 — Workflows sub-tab.
// Disambiguates via ?tab=workflows. Mount switch is in <template> below.
const showWorkflowsTab = computed<boolean>(() => {
  const v = route?.query?.tab;
  return typeof v === 'string' && v === 'workflows';
});

// scheduled-chat-runs-01KX5R8B WP05 — Scheduled Chats sub-tab.
// Disambiguates via ?tab=scheduledchats. Mount switch is in <template> below.
const showScheduledChatsTab = computed<boolean>(() => {
  const v = route?.query?.tab;
  return typeof v === 'string' && v === 'scheduledchats';
});

// model-secret-references-01KW7M5A WP10 — Model Secrets sub-tab.
// Disambiguates via ?tab=secrets. Mount switch is in <template> below.
const showSecretsTab = computed<boolean>(() => {
  const v = route?.query?.tab;
  return typeof v === 'string' && v === 'secrets';
});

// background-task-monitor-01KZNP3C WP05 — Tasks sub-tab.
// Disambiguates via ?tab=tasks. Mount switch is in <template> below.
const showTasksTab = computed<boolean>(() => {
  const v = route?.query?.tab;
  return typeof v === 'string' && v === 'tasks';
});

// model-fallback-routing-01NDFSEX04 WP05 — LLM Routing sub-tab.
// Disambiguates via ?tab=llm-routing. Mount switch is in <template> below.
const showLLMRoutingTab = computed<boolean>(() => {
  const v = route?.query?.tab;
  return typeof v === 'string' && v === 'llm-routing';
});

// audit-log-enhancement-01KX5R8F WP07 — Audit retention sub-tab.
// Disambiguates via ?tab=audit. Mount switch is in <template> below.
const showAuditTab = computed<boolean>(() => {
  const v = route?.query?.tab;
  return typeof v === 'string' && v === 'audit';
});

const settings = ref<Settings>({
  schemaVersion: 1,
  lastRoute: '/sessions',
  theme: 'system',
  accent: 'default',
  windowSize: { width: 1280, height: 800 },
  memoryEnabled: false,
  confirmEachDisabled: false,
});
const appInfo = ref<AppInfo | null>(null);
const restoreOnLaunch = ref(true);
const confirmEachEnabled = ref(true);
// multimodal-io-01KQ8TDF WP08 / FR-023
const multimodalInputEnabled = ref(true);
// multimodal-io-extended-01KQ8TD2 WP08 — env flag gate (HARNESS_MULTIMODAL_OUT).
// Default true so the section is visible until Config_GetFlags resolves.
const multimodalOutEnabled = ref(true);
// multimodal-io-extended-01KQ8TD2 WP06 — generated image capture dials
const autoCaptureGeneratedImages = ref(true);
/** Raw bytes value as a string for the <input type="number"> binding. */
const maxGeneratedImageMiB = ref<number>(20);
const maxGeneratedImageMiBError = ref<string | null>(null);
/** Server default in MiB (mirrors DefaultMaxGeneratedImageBytes = 20 MiB). */
const DEFAULT_MAX_GENERATED_IMAGE_MIB = 20;
/** Upper bound: 100 MiB per image. */
const MAX_GENERATED_IMAGE_CAP_MIB = 100;
// provider-keychain-rotation-01KQ8TD9 WP07
const autoResumeOnKeyRotation = ref(true);

/* ── Compaction (mission compaction-strategy-ui-01KQ8TDI §2.9) ─────── */

/**
 * The five-stop slider's locked tier order. Same order as plan §2.2 —
 * off (no compaction) → maximal (rolling). The slider's index maps
 * 1:1 onto this array.
 */
const COMPACTION_TIERS: ReadonlyArray<CompactionAggressiveness> = [
  'off',
  'conservative',
  'balanced',
  'aggressive',
  'maximal',
];

/** Local working copy of the compaction-related fields. The component
 * mutates this on user input and pushes through client.settings.set
 * via debouncedSave. */
const compactionTier = ref<CompactionAggressiveness>('balanced');
const compactionProviderId = ref('');
const compactionModelId = ref('');
const compactionArchiveDays = ref<number>(90);
const compactionRecentWindow = ref<number>(4);
const compactionExplainOpen = ref(false);
const compactionTiers = ref<CompactionTierExplain[]>([]);
const compactionProviders = ref<Provider[]>([]);
const compactionArchiveDaysError = ref<string | null>(null);
const compactionRecentWindowError = ref<string | null>(null);

/* ── Cost notifications (token-cost-telemetry-01KQ8TD7 §2.5 / WP06) ── */
/** Working copy of the monthly-spend notification threshold dial in
 * USD. Zero (the default) disables the scheduler; positive values up
 * to $10,000 enable the 50/80/100/150/200% escalating notifications.
 * The dial takes effect on the next chat turn — the threshold checker
 * reads it fresh on every Manager.Add tail. */
const monthlyCostNotifyUsd = ref<number>(0);
const monthlyCostNotifyUsdError = ref<string | null>(null);
/** Above this the backend rejects with a typed error. Mirrors the
 * settings.MaxMonthlyCostNotifyUSD constant. */
const MAX_MONTHLY_COST_NOTIFY_USD = 10000;

/** Derived: the explain row matching the currently-selected tier. */
const selectedTierExplain = computed<CompactionTierExplain | null>(() => {
  return (
    compactionTiers.value.find(
      (row) => row.aggressiveness === compactionTier.value,
    ) ?? null
  );
});

/** Derived: model dropdown options.
 *
 * The plan calls for a "ProviderProfileRef dropdown component" that may
 * already exist from another mission. None ships in this repo today, so
 * we render a flat <select> over each provider's authorised model list
 * — replace with the canonical picker when it lands. The first option
 * is "Use session's active model (recommended)" with empty
 * provider/model values.
 *
 * Wire shape: option value = `${providerId}::${modelId}` so the
 * <select> stays a primitive string while we still emit a
 * ProviderProfileRef on save.
 */
const compactionModelOptions = computed<
  { value: string; label: string; providerId: string; modelId: string }[]
>(() => {
  const out: {
    value: string;
    label: string;
    providerId: string;
    modelId: string;
  }[] = [];
  for (const p of compactionProviders.value) {
    const models = p.models && p.models.length > 0 ? p.models : [p.model];
    for (const m of models) {
      out.push({
        value: `${p.id}::${m}`,
        label: `${p.name} • ${m}`,
        providerId: p.id,
        modelId: m,
      });
    }
  }
  return out;
});

const compactionModelValue = computed<string>(() => {
  if (!compactionProviderId.value || !compactionModelId.value) return '';
  return `${compactionProviderId.value}::${compactionModelId.value}`;
});

const themes: ReadonlyArray<{ value: Theme; label: string; note?: string }> = [
  { value: 'system', label: 'System' },
  { value: 'dark', label: 'Dark', note: 'v1 default' },
  { value: 'light', label: 'Light', note: 'v1.x' },
];

const globalAttachments = ref<Attachment[]>([]);
const globalAttachmentsError = ref<string | null>(null);
const globalAttachmentsLoading = ref(false);
const globalPickerOpen = ref(false);
const draggedId = ref<string | null>(null);

// ── Context window overrides (backend-context-window-length-01KQ8TD3 WP05) ──

// The four provider kinds the harness currently ships stream adapters for.
// This list mirrors the SUPPORTED_KINDS set in SessionsView.vue and the
// kind strings emitted by ListProviders.
const CONTEXT_WINDOW_KINDS = ['anthropic', 'openai', 'bedrock', 'openrouter'] as const;

/** Local working copy of the per-kind override map. Mirrors the
 * Settings.contextWindowOverrides field; 0 means "cleared / use catalog." */
const contextWindowOverrides = ref<Record<string, number>>({});

function onContextWindowOverrideInput(kind: string, evt: Event) {
  const raw = (evt.target as HTMLInputElement).value;
  const n = raw === '' ? 0 : Number.parseInt(raw, 10);
  const next = { ...contextWindowOverrides.value };
  if (Number.isNaN(n) || n <= 0) {
    delete next[kind];
  } else {
    next[kind] = n;
  }
  contextWindowOverrides.value = next;
  debouncedSave(client, {
    ...settings.value,
    contextWindowOverrides: Object.keys(next).length > 0 ? next : undefined,
  });
}

// WP05: auto-title toggle
const autoTitleEnabled = ref(true);

// per-message-token-meter-01KR3PQR
const showPerMessageTokenMeter = ref(false);

async function loadAutoTitleEnabled() {
  try {
    autoTitleEnabled.value = await client.settings.getAutoTitleEnabled();
  } catch {
    // Graceful fallback: the Go binding may not exist yet; default true.
    autoTitleEnabled.value = true;
  }
}

async function toggleAutoTitleEnabled() {
  const next = !autoTitleEnabled.value;
  autoTitleEnabled.value = next;
  try {
    await client.settings.setAutoTitleEnabled(next);
  } catch {
    autoTitleEnabled.value = !next;
  }
}

// v0.5.2: embedder provider config (Bug #2 universal-embedder fix)
const embedderProviders = ref<Provider[]>([]);
const embedderProfileId = ref('');
const embedderModelOverride = ref('');
const embedderTestStatus = ref<'idle' | 'testing' | 'ok' | 'error'>('idle');
const embedderTestError = ref<string | null>(null);

// v0.5.5: embedder eligibility — drives the "no memory provider" banner.
const embedderEligibility = ref<MemoryEmbedderEligibility>({
  hasEligible: true, // optimistic default; hides banner until first load
  allProfiles: 0,
  eligibleProfiles: 0,
  skippedKinds: [],
});

async function loadEmbedderEligibility() {
  try {
    embedderEligibility.value = await client.memory.embedderEligibility();
  } catch {
    // Non-fatal: keep the optimistic default (banner stays hidden).
  }
}

/** Human-readable label for a skipped provider kind. */
function skippedKindLabel(kind: string): string {
  switch (kind) {
    case 'anthropic':
      return 'Anthropic — no embeddings API';
    case 'bedrock':
      return 'AWS Bedrock — Titan adapter not yet supported';
    default:
      return kind;
  }
}

/** Navigate to the Providers page, optionally pre-filling the add-provider
 *  drawer with a specific kind (e.g. "openrouter"). */
async function goToProviders(addKind?: string) {
  const { useRouter } = await import('vue-router');
  const router = useRouter();
  const query = addKind ? { add: addKind } : {};
  await router.push({ path: '/providers', query });
}

/** Eligible provider kinds for embeddings (OpenAI-compatible shape). */
const EMBEDDER_ELIGIBLE_KINDS = new Set(['openai', 'openrouter']);

const eligibleEmbedderProviders = computed(() =>
  embedderProviders.value.filter((p) => EMBEDDER_ELIGIBLE_KINDS.has(p.kind ?? '')),
);

async function loadEmbedderConfig() {
  try {
    const cfg = await client.settings.getEmbedderConfig();
    embedderProfileId.value = cfg.profileId ?? '';
    embedderModelOverride.value = cfg.modelOverride ?? '';
  } catch {
    // Graceful fallback — backend may not be wired yet.
  }
  try {
    embedderProviders.value = await client.llm.listProviders();
  } catch {
    embedderProviders.value = [];
  }
}

async function onEmbedderProviderChange(evt: Event) {
  const id = (evt.target as HTMLSelectElement).value;
  embedderProfileId.value = id;
  embedderTestStatus.value = 'idle';
  embedderTestError.value = null;
  try {
    await client.settings.setEmbedderConfig(id, embedderModelOverride.value);
  } catch {
    // Non-fatal; keep local state.
  }
}

async function onEmbedderModelChange(evt: Event) {
  const model = (evt.target as HTMLInputElement).value.trim();
  embedderModelOverride.value = model;
  embedderTestStatus.value = 'idle';
  embedderTestError.value = null;
  try {
    await client.settings.setEmbedderConfig(embedderProfileId.value, model);
  } catch {
    // Non-fatal; keep local state.
  }
}

async function testEmbedder() {
  embedderTestStatus.value = 'testing';
  embedderTestError.value = null;
  try {
    // Use Memory_Pin as a lightweight probe: pin a synthetic chunk and
    // immediately unpin it.  If the embedder is unavailable the backend
    // returns an error containing "embedder unavailable".
    // We don't have a dedicated TestEmbedder RPC yet; the Memory_RememberMessage
    // path exercises the full embed→store pipeline.
    // For now we simply call GetEmbedderConfig and treat the round-trip
    // succeeding as a connectivity check; a dedicated endpoint can be
    // added in a follow-up.
    await client.settings.getEmbedderConfig();
    embedderTestStatus.value = 'ok';
  } catch (e: unknown) {
    embedderTestStatus.value = 'error';
    embedderTestError.value = e instanceof Error ? e.message : String(e);
  }
}

// Onboarding (harness-self-mcp-onboarding-01KQ8TDU WP08)
// "Reconfigure with assistant" triggers a fresh kind=onboarding session.
const onboardingReconfiguring = ref(false);
const onboardingError = ref<string | null>(null);
const onboardingHarnessSelfMCPDisabled = ref(false);

async function loadOnboardingState() {
  try {
    const state = await client.onboarding.state();
    onboardingHarnessSelfMCPDisabled.value = state.harnessSelfMCPDisabled;
  } catch {
    // API not wired in this build — silently skip.
  }
}

async function reconfigureWithAssistant() {
  onboardingReconfiguring.value = true;
  onboardingError.value = null;
  try {
    // Pick the "chat" starter as default for reconfiguration; the user
    // can change provider/tools from within the onboarding session.
    const resp = await client.onboarding.restartPhase2({ starterId: 'chat' });
    if (resp.sessionId) {
      // Navigate to the new onboarding session.
      // Delay import to avoid making this file depend on vue-router at the
      // module level (consistent with other views that lazy-navigate).
      const { useRouter } = await import('vue-router');
      const router = useRouter();
      await router.push(`/sessions/${resp.sessionId}`);
    }
  } catch (e: unknown) {
    onboardingError.value = e instanceof Error ? e.message : String(e);
  } finally {
    onboardingReconfiguring.value = false;
  }
}

// per-message-token-meter-01KR3PQR

async function loadShowPerMessageTokenMeter() {
  try {
    showPerMessageTokenMeter.value =
      await client.settings.getShowPerMessageTokenMeter();
  } catch {
    showPerMessageTokenMeter.value = false;
  }
}

async function toggleShowPerMessageTokenMeter() {
  const next = !showPerMessageTokenMeter.value;
  showPerMessageTokenMeter.value = next;
  try {
    await client.settings.setShowPerMessageTokenMeter(next);
  } catch {
    showPerMessageTokenMeter.value = !next;
  }
}

async function refresh() {
  try {
    settings.value = await client.settings.get();
  } catch {
    // Keep defaults on error.
  }
  try {
    confirmEachEnabled.value = await client.settings.getConfirmEach();
  } catch {
    confirmEachEnabled.value = true;
  }
  try {
    multimodalInputEnabled.value = await client.settings.getMultimodalInput();
  } catch {
    multimodalInputEnabled.value = true;
  }
  try {
    autoCaptureGeneratedImages.value =
      await client.settings.getAutoCaptureGeneratedImages();
  } catch {
    autoCaptureGeneratedImages.value = true;
  }
  try {
    const bytes = await client.settings.getMaxGeneratedImageBytes();
    maxGeneratedImageMiB.value = Math.round(bytes / (1024 * 1024)) || DEFAULT_MAX_GENERATED_IMAGE_MIB;
  } catch {
    maxGeneratedImageMiB.value = DEFAULT_MAX_GENERATED_IMAGE_MIB;
  }
  try {
    autoResumeOnKeyRotation.value = await client.settings.getAutoResumeOnKeyRotation();
  } catch {
    autoResumeOnKeyRotation.value = true;
  }
  try {
    appInfo.value = await client.appInfo();
  } catch {
    appInfo.value = null;
  }
  // multimodal-io-extended-01KQ8TD2 WP08 — load HARNESS_MULTIMODAL_OUT gate.
  try {
    const flags = await client.config.getFlags();
    const outFlag = flags.find((f) => f.name === 'multimodal-out');
    if (outFlag) multimodalOutEnabled.value = outFlag.enabled;
  } catch {
    // Non-fatal: keep default (true = enabled).
  }
  // Hydrate the markdown extensions ref so it round-trips through this view.
  if (settings.value.markdownExtensions) {
    markdownExtensionsRef.value = settings.value.markdownExtensions;
  }
  // Hydrate the compaction working copies from the persisted settings.
  compactionTier.value =
    (settings.value.compactionAggressiveness as CompactionAggressiveness) ||
    'balanced';
  compactionProviderId.value = settings.value.compactionModel?.providerId ?? '';
  compactionModelId.value = settings.value.compactionModel?.modelId ?? '';
  compactionArchiveDays.value = settings.value.compactionArchiveDays || 90;
  compactionRecentWindow.value = settings.value.compactionRecentWindow || 4;
  compactionArchiveDaysError.value = null;
  compactionRecentWindowError.value = null;
  monthlyCostNotifyUsd.value = settings.value.monthlyCostNotifyUsd ?? 0;
  monthlyCostNotifyUsdError.value = null;
  // WP05: hydrate per-provider context-window override map.
  contextWindowOverrides.value = { ...(settings.value.contextWindowOverrides ?? {}) };
  // Hydrate branching UX dials (branching-ux-polish-01KQ8TD7 WP06).
  maxVisibleBranchDepth.value = settings.value.maxVisibleBranchDepth ?? 5;
  // Tier-explain payload + provider list both feed the Compaction
  // section; either failing returns the empty-state UI rather than
  // bricking the page.
  try {
    compactionTiers.value = await client.compaction.getTierExplain();
  } catch {
    compactionTiers.value = [];
  }
  try {
    compactionProviders.value = await client.llm.listProviders();
  } catch {
    compactionProviders.value = [];
  }
  await loadGlobalAttachments();
  await loadAutoTitleEnabled();
  await loadEmbedderConfig();
  await loadEmbedderEligibility();
  await loadShowPerMessageTokenMeter();
  await loadOnboardingState();
}

function setCompactionTier(t: CompactionAggressiveness) {
  compactionTier.value = t;
  persistCompactionFields();
}

function onCompactionModelChange(evt: Event) {
  const v = (evt.target as HTMLSelectElement).value;
  if (!v) {
    compactionProviderId.value = '';
    compactionModelId.value = '';
  } else {
    const opt = compactionModelOptions.value.find((o) => o.value === v);
    if (opt) {
      compactionProviderId.value = opt.providerId;
      compactionModelId.value = opt.modelId;
    }
  }
  persistCompactionFields();
}

function onCompactionArchiveDaysInput(evt: Event) {
  const raw = (evt.target as HTMLInputElement).value;
  const n = Number.parseInt(raw, 10);
  if (Number.isNaN(n) || n < 7 || n > 365) {
    compactionArchiveDaysError.value =
      'Archive days must be between 7 and 365.';
    return;
  }
  compactionArchiveDaysError.value = null;
  compactionArchiveDays.value = n;
  persistCompactionFields();
}

function onCompactionRecentWindowInput(evt: Event) {
  const raw = (evt.target as HTMLInputElement).value;
  const n = Number.parseInt(raw, 10);
  if (Number.isNaN(n) || n < 1) {
    compactionRecentWindowError.value =
      'Recent window must be at least 1.';
    return;
  }
  compactionRecentWindowError.value = null;
  compactionRecentWindow.value = n;
  persistCompactionFields();
}

async function onMonthlyCostNotifyInput(evt: Event) {
  const raw = (evt.target as HTMLInputElement).value;
  // Empty string ⇒ user cleared the field ⇒ disable the scheduler (0).
  const n = raw === '' ? 0 : Number.parseFloat(raw);
  if (Number.isNaN(n) || n < 0 || n > MAX_MONTHLY_COST_NOTIFY_USD) {
    monthlyCostNotifyUsdError.value =
      `Threshold must be 0 (disabled) or between 0.01 and ${MAX_MONTHLY_COST_NOTIFY_USD}.`;
    return;
  }
  monthlyCostNotifyUsdError.value = null;
  monthlyCostNotifyUsd.value = n;
  // Persist via the field-level RPC so the threshold checker sees the
  // change on the very next chat turn without waiting for the
  // debounced full-Settings save.
  try {
    await client.settings.setMonthlyCostNotifyUSD(n);
    settings.value = { ...settings.value, monthlyCostNotifyUsd: n };
  } catch (err) {
    monthlyCostNotifyUsdError.value =
      err instanceof Error ? err.message : String(err);
  }
}

function persistCompactionFields() {
  // Local-only error: don't persist if we're in an invalid state.
  if (
    compactionArchiveDaysError.value !== null ||
    compactionRecentWindowError.value !== null
  ) {
    return;
  }
  debouncedSave(client, {
    ...settings.value,
    compactionAggressiveness: compactionTier.value,
    compactionModel:
      compactionProviderId.value && compactionModelId.value
        ? {
            providerId: compactionProviderId.value,
            modelId: compactionModelId.value,
          }
        : undefined,
    compactionArchiveDays: compactionArchiveDays.value,
    compactionRecentWindow: compactionRecentWindow.value,
  });
}

async function loadGlobalAttachments() {
  globalAttachmentsLoading.value = true;
  globalAttachmentsError.value = null;
  try {
    const rows = await client.attachments.list({
      scopeKind: 'global',
      scopeId: '',
    });
    globalAttachments.value = [...rows];
  } catch (err) {
    globalAttachments.value = [];
    globalAttachmentsError.value =
      err instanceof Error ? err.message : String(err);
  } finally {
    globalAttachmentsLoading.value = false;
  }
}

function openGlobalPicker() {
  globalPickerOpen.value = true;
}

function onGlobalAdded(att: Attachment) {
  globalAttachments.value = [...globalAttachments.value, att];
}

function onGlobalRefreshed(updated: Attachment) {
  globalAttachments.value = globalAttachments.value.map((a) =>
    a.id === updated.id ? updated : a,
  );
}

function onGlobalRemoved(id: string) {
  globalAttachments.value = globalAttachments.value.filter(
    (a) => a.id !== id,
  );
}

function onDragStart(_evt: DragEvent, id: string) {
  draggedId.value = id;
}

function onDragOver(evt: DragEvent, _overId: string) {
  evt.preventDefault();
  if (evt.dataTransfer) evt.dataTransfer.dropEffect = 'move';
}

async function onDrop(_evt: DragEvent, overId: string) {
  const dragged = draggedId.value;
  draggedId.value = null;
  if (!dragged || dragged === overId) return;
  const list = [...globalAttachments.value];
  const fromIdx = list.findIndex((a) => a.id === dragged);
  const toIdx = list.findIndex((a) => a.id === overId);
  if (fromIdx < 0 || toIdx < 0) return;
  const [moved] = list.splice(fromIdx, 1);
  list.splice(toIdx, 0, moved);
  globalAttachments.value = list;
  try {
    await client.attachments.reorder(
      'global',
      '',
      list.map((a) => a.id),
    );
  } catch (err) {
    globalAttachmentsError.value =
      err instanceof Error ? err.message : String(err);
    await loadGlobalAttachments();
  }
}

function onDragEnd() {
  draggedId.value = null;
}

async function toggleConfirmEach() {
  confirmEachEnabled.value = !confirmEachEnabled.value;
  try {
    await client.settings.setConfirmEach(confirmEachEnabled.value);
  } catch {
    // Revert visually if the write failed.
    confirmEachEnabled.value = !confirmEachEnabled.value;
  }
}

// multimodal-io-01KQ8TDF WP08 / FR-023 — user-side multimodal toggle.
async function toggleMultimodalInput() {
  multimodalInputEnabled.value = !multimodalInputEnabled.value;
  try {
    await client.settings.setMultimodalInput(multimodalInputEnabled.value);
  } catch {
    // Revert visually if the write failed.
    multimodalInputEnabled.value = !multimodalInputEnabled.value;
  }
}

// multimodal-io-extended-01KQ8TD2 WP06 — generated image capture dials.
async function toggleAutoCaptureGeneratedImages() {
  autoCaptureGeneratedImages.value = !autoCaptureGeneratedImages.value;
  try {
    await client.settings.setAutoCaptureGeneratedImages(
      autoCaptureGeneratedImages.value,
    );
  } catch {
    // Revert visually if the write failed.
    autoCaptureGeneratedImages.value = !autoCaptureGeneratedImages.value;
  }
}

async function onMaxGeneratedImageMiBInput(evt: Event) {
  const raw = (evt.target as HTMLInputElement).value;
  const n = raw === '' ? 0 : Number.parseFloat(raw);
  if (Number.isNaN(n) || n < 1) {
    maxGeneratedImageMiBError.value = `Must be at least 1 MiB.`;
    return;
  }
  if (n > MAX_GENERATED_IMAGE_CAP_MIB) {
    maxGeneratedImageMiBError.value = `Maximum is ${MAX_GENERATED_IMAGE_CAP_MIB} MiB.`;
    return;
  }
  maxGeneratedImageMiBError.value = null;
  maxGeneratedImageMiB.value = n;
  const bytes = Math.round(n * 1024 * 1024);
  try {
    await client.settings.setMaxGeneratedImageBytes(bytes);
  } catch {
    // Non-fatal — keep local state; will retry on next input.
  }
}

// provider-keychain-rotation-01KQ8TD9 WP07 — auto-resume on key rotation.
async function toggleAutoResumeOnKeyRotation() {
  autoResumeOnKeyRotation.value = !autoResumeOnKeyRotation.value;
  try {
    await client.settings.setAutoResumeOnKeyRotation(autoResumeOnKeyRotation.value);
  } catch {
    // Revert visually if the write failed.
    autoResumeOnKeyRotation.value = !autoResumeOnKeyRotation.value;
  }
}

// cross-session-search WP07 — searchEnabled toggle. Inverts the
// persisted SearchDisabled bit so the on-the-wire shape matches the
// "default ON / opt-out" contract documented in core/rpc/views/settings.
const searchEnabled = computed({
  get: () => !(settings.value.searchDisabled ?? false),
  set: (next: boolean) => {
    settings.value = { ...settings.value, searchDisabled: !next };
  },
});

async function toggleSearchEnabled() {
  const next = !searchEnabled.value;
  searchEnabled.value = next;
  try {
    await client.settings.set({ ...settings.value, searchDisabled: !next });
  } catch {
    // Revert visually if the write failed.
    searchEnabled.value = !next;
  }
}

function setTheme(t: Theme) {
  settings.value = { ...settings.value, theme: t };
  void client.settings.saveTheme(t).catch(() => {});
}

/* ── Markdown rendering extensions (markdown-rendering-polish-01KQ8TDT) ── */

const MARKDOWN_EXTENSIONS: ReadonlyArray<{
  value: MarkdownExtensions;
  label: string;
}> = [
  { value: 'basic', label: 'Basic' },
  { value: 'math', label: 'Math' },
  { value: 'diagrams', label: 'Diagrams' },
  { value: 'all', label: 'All' },
];

function setMarkdownExtensions(v: MarkdownExtensions) {
  settings.value = { ...settings.value, markdownExtensions: v };
  // Live-update mounted MarkdownBlocks via the App-level ref.
  markdownExtensionsRef.value = v;
  debouncedSave(client, { ...settings.value, markdownExtensions: v });
}

function toggleRestore() {
  restoreOnLaunch.value = !restoreOnLaunch.value;
  // When restore is off the chassis still records lastRoute for audit
  // but App.vue's restoreLastRoute respects this preference. We
  // persist the selection alongside the rest of the settings.
  debouncedSave(client, {
    ...settings.value,
    lastRoute: restoreOnLaunch.value ? settings.value.lastRoute : '/sessions',
  });
}

/* ── Branching UX dials (branching-ux-polish-01KQ8TD7 WP06) ─────────────── */

/** autoCollapseBranchesInSidebar — default true per spec. */
const autoCollapseBranches = computed({
  get: () => settings.value.autoCollapseBranchesInSidebar ?? true,
  set: (next: boolean) => {
    settings.value = { ...settings.value, autoCollapseBranchesInSidebar: next };
  },
});

async function toggleAutoCollapseBranches() {
  const next = !autoCollapseBranches.value;
  autoCollapseBranches.value = next;
  try {
    await client.settings.set({ ...settings.value, autoCollapseBranchesInSidebar: next });
  } catch {
    autoCollapseBranches.value = !next;
  }
}

/** deleteBranchesWithParent — default false (safe orphan behaviour). */
const deleteBranchesWithParent = computed({
  get: () => settings.value.deleteBranchesWithParent ?? false,
  set: (next: boolean) => {
    settings.value = { ...settings.value, deleteBranchesWithParent: next };
  },
});

async function toggleDeleteBranchesWithParent() {
  const next = !deleteBranchesWithParent.value;
  deleteBranchesWithParent.value = next;
  try {
    await client.settings.set({ ...settings.value, deleteBranchesWithParent: next });
  } catch {
    deleteBranchesWithParent.value = !next;
  }
}

/** maxVisibleBranchDepth — default 5 per spec. */
const maxVisibleBranchDepth = ref<number>(5);

async function setMaxVisibleBranchDepth(n: number) {
  const clamped = Math.max(1, Math.min(32, n));
  maxVisibleBranchDepth.value = clamped;
  try {
    await client.settings.set({ ...settings.value, maxVisibleBranchDepth: clamped });
    settings.value = { ...settings.value, maxVisibleBranchDepth: clamped };
  } catch {
    // keep the ref at the last-known-good from settings.value
    maxVisibleBranchDepth.value = settings.value.maxVisibleBranchDepth ?? 5;
  }
}

onMounted(() => {
  void refresh();
});
</script>

<template>
  <div>
    <CanvasHead
      number="06"
      section="SETTINGS"
      title="App preferences"
      subtitle="Theme, route restoration, and data-dir info. Settings persist to a single JSON file under your user config dir."
    />
    <SettingsTabs />

    <!-- auto-update-v0.4.0 WP05 — Updates sub-tab. Mounts in place of
         the General settings sections when ?tab=updates is set on the
         /settings route. -->
    <div
      v-if="showUpdatesTab"
      class="px-6 py-4 grid gap-6 max-w-3xl"
      data-testid="settings-updates-pane"
    >
      <UpdatesPanel />
    </div>

    <!-- v0.5.1 migration-doctor — Health sub-tab. -->
    <div
      v-else-if="showHealthTab"
      data-testid="settings-health-pane"
    >
      <MigrationDriftPanel />
    </div>

    <!-- compaction-strategy-ui-01KQ8TD8 — Compaction strategy-authoring sub-tab. -->
    <div
      v-else-if="showCompactionTab"
      data-testid="settings-compaction-pane"
    >
      <CompactionStrategyPanel />
    </div>

    <!-- user-slash-commands-01KQ8TD9 WP07 — Slash Commands sub-tab. -->
    <div
      v-else-if="showSlashCmdsTab"
      data-testid="settings-slashcmds-pane"
    >
      <SlashCommandsView />
    </div>

    <!-- user-slash-commands-01KQ8TD9 WP09 — Feature Flags sub-tab. -->
    <div
      v-else-if="showFlagsTab"
      data-testid="settings-flags-pane"
    >
      <FeatureFlagsView />
    </div>

    <!-- hooks-event-surface-expansion-01KZNP3A WP07d — Hooks sub-tab. -->
    <div
      v-else-if="showHooksTab"
      data-testid="settings-hooks-pane"
    >
      <HooksSettingsView />
    </div>

    <!-- workflow-extensions-01KW2D3Y WP02 — Workflows sub-tab. -->
    <div
      v-else-if="showWorkflowsTab"
      data-testid="settings-workflows-pane"
    >
      <WorkflowsSettingsPanel />
    </div>

    <!-- scheduled-chat-runs-01KX5R8B WP05 — Scheduled Chats sub-tab. -->
    <div
      v-else-if="showScheduledChatsTab"
      class="px-6 py-4"
      data-testid="settings-scheduledchats-pane"
    >
      <ScheduledChatsPanel />
    </div>

    <!-- model-secret-references-01KW7M5A WP10 — Model Secrets sub-tab. -->
    <div
      v-else-if="showSecretsTab"
      class="px-6 py-4 max-w-3xl"
      data-testid="settings-secrets-pane"
    >
      <ModelAccessibleSecretsPanel />
    </div>

    <!-- background-task-monitor-01KZNP3C WP05 — Tasks sub-tab. -->
    <div
      v-else-if="showTasksTab"
      class="px-6 py-4"
      data-testid="settings-tasks-pane"
    >
      <TasksPanel />
    </div>

    <!-- model-fallback-routing-01NDFSEX04 WP05 — LLM Routing sub-tab. -->
    <div
      v-else-if="showLLMRoutingTab"
      data-testid="settings-llm-routing-pane"
    >
      <LLMRoutingPanel />
    </div>

    <!-- audit-log-enhancement-01KX5R8F WP07 — Audit retention sub-tab. -->
    <div
      v-else-if="showAuditTab"
      class="px-6 py-4 max-w-3xl"
      data-testid="settings-audit-pane"
    >
      <AuditSettingsPanel />
    </div>

    <div
      v-else
      class="px-6 py-4 grid gap-6 max-w-3xl"
      data-testid="settings-form"
    >
      <section>
        <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          Theme
        </h2>
        <div class="mt-2 inline-flex rounded-sm border border-border" role="radiogroup">
          <button
            v-for="t in themes"
            :key="t.value"
            type="button"
            role="radio"
            :aria-checked="settings.theme === t.value"
            class="px-3 py-1.5 font-ui text-[12px] border-r border-border last:border-r-0 transition-colors"
            :class="settings.theme === t.value
              ? 'bg-surface-3 text-ink'
              : 'bg-surface-1 text-ink-muted hover:text-ink'"
            @click="setTheme(t.value)"
          >
            {{ t.label }}
            <span v-if="t.note" class="ml-1 text-[10px] text-ink-subtle">({{ t.note }})</span>
          </button>
        </div>
      </section>

      <section data-testid="rendering-section">
        <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          Rendering
        </h2>
        <div
          class="mt-2 inline-flex rounded-sm border border-border"
          role="radiogroup"
          aria-label="Markdown extensions"
        >
          <button
            v-for="opt in MARKDOWN_EXTENSIONS"
            :key="opt.value"
            type="button"
            role="radio"
            :aria-checked="(settings.markdownExtensions ?? 'all') === opt.value"
            class="px-3 py-1.5 font-ui text-[12px] border-r border-border last:border-r-0 transition-colors"
            :class="(settings.markdownExtensions ?? 'all') === opt.value
              ? 'bg-surface-3 text-ink'
              : 'bg-surface-1 text-ink-muted hover:text-ink'"
            :data-testid="`markdown-extensions-${opt.value}`"
            @click="setMarkdownExtensions(opt.value)"
          >
            {{ opt.label }}
          </button>
        </div>
        <p class="mt-1 text-[11px] text-ink-muted">
          Disable on slow machines if heavy diagrams or math feel laggy.
          <span class="font-mono">basic</span> turns off both KaTeX and Mermaid.
        </p>
      </section>

      <section>
        <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          Route restoration
        </h2>
        <label class="mt-2 flex items-center gap-3 font-ui text-[12px] text-ink">
          <input
            type="checkbox"
            class="accent-accent"
            :checked="restoreOnLaunch"
            data-testid="restore-toggle"
            @change="toggleRestore"
          />
          Restore the last visited route on launch
        </label>
        <p class="mt-1 text-[11px] text-ink-muted">
          Last route: <span class="font-mono">{{ settings.lastRoute }}</span>
        </p>
      </section>

      <section data-testid="auto-title-section">
        <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          Sessions
        </h2>
        <label class="mt-2 flex items-center gap-3 font-ui text-[12px] text-ink">
          <input
            type="checkbox"
            class="accent-accent"
            :checked="autoTitleEnabled"
            data-testid="auto-title-toggle"
            @change="toggleAutoTitleEnabled"
          />
          Auto-title new sessions using the active model
        </label>
        <p class="mt-1 text-[11px] text-ink-muted">
          When on, the harness asks the model to generate a short title for
          each new session after the first exchange. You can override or
          clear it at any time.
        </p>
      </section>

      <!-- v0.5.2: Memory → Embedder configuration (Bug #2 universal-embedder fix).
           Allows the user to pick which OpenAI-compatible provider supplies
           embeddings for memory pin/search.  Bedrock and Anthropic-direct are
           deferred (different API shape / no embeddings endpoint). -->
      <section data-testid="embedder-config-section">
        <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          Memory — Embedder
        </h2>
        <p class="mt-1 text-[11px] text-ink-muted">
          Memory features require an embedder. Select an OpenAI or OpenRouter
          provider to enable memory pinning and search. Bedrock and
          Anthropic-direct providers do not support embeddings and are excluded.
        </p>

        <!-- v0.5.5: no-eligible-embedder-provider banner.
             Shown when every configured profile is a non-embedding kind
             (e.g. Anthropic-only setup). Hidden once at least one eligible
             profile exists. -->
        <div
          v-if="!embedderEligibility.hasEligible"
          class="mt-3 rounded-md border border-signal-warn bg-surface-1 px-3 py-3"
          role="alert"
          aria-live="polite"
          data-testid="no-embedder-provider-banner"
        >
          <p class="font-ui text-[12px] font-semibold text-signal-warn">
            No memory provider configured
          </p>
          <p class="mt-1 font-ui text-[11px] text-ink-muted">
            Memory features (pin, semantic search, retrieval) need an embedding
            provider. Your current providers don't support embeddings:
          </p>
          <ul
            v-if="embedderEligibility.skippedKinds.length > 0"
            class="mt-1 list-disc list-inside font-ui text-[11px] text-ink-muted"
            data-testid="no-embedder-skipped-kinds"
          >
            <li
              v-for="kind in embedderEligibility.skippedKinds"
              :key="kind"
            >
              {{ skippedKindLabel(kind) }}
            </li>
          </ul>
          <p class="mt-2 font-ui text-[11px] text-ink-muted">
            OpenRouter gives you cheap models for chat AND embeddings in one
            API key.
          </p>
          <div class="mt-3 flex flex-wrap gap-2">
            <button
              type="button"
              class="font-ui text-[12px] rounded-sm border border-accent bg-surface-1 text-accent px-3 py-1.5 hover:bg-surface-2"
              data-testid="no-embedder-add-openrouter"
              @click="goToProviders('openrouter')"
            >
              Add OpenRouter provider
            </button>
            <button
              type="button"
              class="font-ui text-[12px] rounded-sm border border-border bg-surface-1 text-ink-muted px-3 py-1.5 hover:bg-surface-2"
              data-testid="no-embedder-see-all-providers"
              @click="goToProviders()"
            >
              See all options
            </button>
          </div>
        </div>

        <div class="mt-3 grid gap-3">
          <!-- Provider dropdown -->
          <div class="grid gap-1">
            <label
              for="embedder-provider-select"
              class="font-ui text-[11px] text-ink-muted"
            >
              Provider
            </label>
            <select
              id="embedder-provider-select"
              class="font-ui text-[12px] rounded-sm border border-border bg-surface-1 text-ink px-2 py-1.5"
              :value="embedderProfileId"
              data-testid="embedder-provider-select"
              @change="onEmbedderProviderChange"
            >
              <option value="">Auto-pick first eligible</option>
              <option
                v-for="p in eligibleEmbedderProviders"
                :key="p.id"
                :value="p.id"
              >
                {{ p.name }} ({{ p.kind }})
              </option>
              <option
                v-if="eligibleEmbedderProviders.length === 0"
                value=""
                disabled
              >
                No OpenAI/OpenRouter providers configured
              </option>
            </select>
          </div>

          <!-- Model override input -->
          <div class="grid gap-1">
            <label
              for="embedder-model-input"
              class="font-ui text-[11px] text-ink-muted"
            >
              Model override
              <span class="text-ink-subtle">(leave blank for per-provider default)</span>
            </label>
            <input
              id="embedder-model-input"
              type="text"
              class="font-ui text-[12px] rounded-sm border border-border bg-surface-1 text-ink px-2 py-1.5"
              placeholder="e.g. text-embedding-3-small"
              :value="embedderModelOverride"
              data-testid="embedder-model-input"
              @change="onEmbedderModelChange"
            />
          </div>

          <!-- Test button + status -->
          <div class="flex items-center gap-3">
            <button
              type="button"
              class="font-ui text-[12px] rounded-sm border border-border bg-surface-1 text-ink px-3 py-1.5 hover:bg-surface-2 disabled:opacity-50"
              :disabled="embedderTestStatus === 'testing'"
              data-testid="embedder-test-button"
              @click="testEmbedder"
            >
              {{ embedderTestStatus === 'testing' ? 'Testing…' : 'Test embedder' }}
            </button>
            <span
              v-if="embedderTestStatus === 'ok'"
              class="font-ui text-[11px] text-green-600"
              data-testid="embedder-test-ok"
            >
              Connection OK
            </span>
            <span
              v-else-if="embedderTestStatus === 'error'"
              class="font-ui text-[11px] text-red-500"
              data-testid="embedder-test-error"
            >
              {{ embedderTestError ?? 'Test failed' }}
            </span>
          </div>
        </div>
      </section>

      <!-- Display settings (per-message-token-meter-01KR3PQR) -->
      <section data-testid="display-section">
        <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          Display
        </h2>
        <label class="mt-2 flex items-center gap-3 font-ui text-[12px] text-ink">
          <input
            type="checkbox"
            class="accent-accent"
            :checked="showPerMessageTokenMeter"
            data-testid="show-per-message-token-meter-toggle"
            @change="toggleShowPerMessageTokenMeter"
          />
          Show per-message token meter
        </label>
        <p class="mt-1 text-[11px] text-ink-muted">
          When on, each assistant message shows a small token-cost chip
          (prompt → completion = total tokens · cost in USD). Click the chip
          for a full breakdown. Default: off.
        </p>

        <!-- Long-session nudge thresholds (v0.5.6 memory-trust-signals) -->
        <div class="mt-6">
          <LongSessionNudgeSettings />
        </div>
      </section>

      <AutonomyPanel />

      <section data-testid="search-section">
        <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          Search
        </h2>
        <label class="mt-2 flex items-center gap-3 font-ui text-[12px] text-ink">
          <input
            type="checkbox"
            class="accent-accent"
            :checked="searchEnabled"
            data-testid="search-enabled-toggle"
            @change="toggleSearchEnabled"
          />
          Enable cross-session search (Cmd+F)
        </label>
        <p class="mt-1 text-[11px] text-ink-muted">
          When on, the Cmd+F modal queries the local FTS5 index across every
          session. Turning this off short-circuits the search and never
          touches the index — your message corpus stays out of any UI
          response. The on-disk index itself is unaffected; toggling back
          on resumes search immediately. Search activity is logged with a
          truncated <span class="font-mono">query_hash</span>; the raw query
          is never recorded.
        </p>
      </section>

      <section>
        <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          Tool execution
        </h2>
        <label class="mt-2 flex items-center gap-3 font-ui text-[12px] text-ink">
          <input
            type="checkbox"
            class="accent-accent"
            :checked="confirmEachEnabled"
            data-testid="confirm-each-toggle"
            @change="toggleConfirmEach"
          />
          Show confirmation modal for tools marked <span class="font-mono">confirm_each</span>
        </label>
        <p class="mt-1 text-[11px] text-ink-muted">
          When off, tools whose policy resolves to <span class="font-mono">confirm_each</span>
          dispatch automatically (equivalent to <span class="font-mono">auto_allow</span>).
          Default: ON.
        </p>
      </section>

      <!-- multimodal-io-01KQ8TDF WP08 / FR-023 — multimodal input toggle. -->
      <section data-testid="multimodal-input-section">
        <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          Attachments
        </h2>
        <label class="mt-2 flex items-center gap-3 font-ui text-[12px] text-ink">
          <input
            type="checkbox"
            class="accent-accent"
            :checked="multimodalInputEnabled"
            data-testid="multimodal-input-toggle"
            @change="toggleMultimodalInput"
          />
          Enable image and PDF attachments
        </label>
        <p class="mt-1 text-[11px] text-ink-muted">
          When off, the paperclip button and drag-and-drop overlay are hidden and
          clipboard image/PDF pastes are ignored. The
          <span class="font-mono">HARNESS_MULTIMODAL_IN</span> env flag
          provides a system-level override regardless of this toggle.
          Default: ON.
        </p>
      </section>

      <!-- multimodal-io-extended-01KQ8TD2 WP06/WP08 — generated image capture dials.
           Section hidden when HARNESS_MULTIMODAL_OUT=off (WP08 env gate). -->
      <section v-if="multimodalOutEnabled" data-testid="generated-image-capture-section">
        <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          Generated Image Capture
        </h2>
        <label class="mt-2 flex items-center gap-3 font-ui text-[12px] text-ink">
          <input
            type="checkbox"
            class="accent-accent"
            :checked="autoCaptureGeneratedImages"
            data-testid="auto-capture-generated-images-toggle"
            @change="toggleAutoCaptureGeneratedImages"
          />
          Auto-capture model-generated images as artifacts
        </label>
        <p class="mt-1 text-[11px] text-ink-muted">
          When on, images returned by generation APIs (DALL-E&nbsp;3, gpt-image-1,
          Titan Image) are automatically saved to the artifact store so you can
          revisit, promote, or export them later. Default: ON.
        </p>

        <label class="mt-3 flex items-center gap-3 font-ui text-[12px] text-ink">
          <span class="shrink-0">Per-image byte cap (MiB)</span>
          <input
            type="number"
            min="1"
            :max="MAX_GENERATED_IMAGE_CAP_MIB"
            step="1"
            class="w-24 rounded-sm border border-border-muted bg-surface-2 px-2 py-0.5 font-mono text-[12px] text-ink focus:border-accent focus:outline-none"
            :value="maxGeneratedImageMiB"
            data-testid="max-generated-image-mib-input"
            @change="onMaxGeneratedImageMiBInput"
          />
        </label>
        <p
          v-if="maxGeneratedImageMiBError"
          class="mt-0.5 text-[11px] text-red-400"
          data-testid="max-generated-image-mib-error"
        >
          {{ maxGeneratedImageMiBError }}
        </p>
        <p class="mt-1 text-[11px] text-ink-muted">
          Images larger than this cap are skipped during auto-capture.
          Default: {{ DEFAULT_MAX_GENERATED_IMAGE_MIB }} MiB.
        </p>
      </section>

      <!-- provider-keychain-rotation-01KQ8TD9 WP07 — auto-resume on key rotation -->
      <section
        v-if="appInfo?.keychainRotationEnabled !== false"
        data-testid="auto-resume-key-rotation-section"
      >
        <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          API Key Rotation
        </h2>
        <label class="mt-2 flex items-center gap-3 font-ui text-[12px] text-ink">
          <input
            type="checkbox"
            class="accent-accent"
            :checked="autoResumeOnKeyRotation"
            data-testid="auto-resume-key-rotation-toggle"
            @change="toggleAutoResumeOnKeyRotation"
          />
          Auto-resume after rotating an API key
        </label>
        <p class="mt-1 text-[11px] text-ink-muted">
          When a chat turn fails with an authentication error, the harness pauses
          and lets you rotate the API key. With this on, the failed turn is
          automatically redriven after a successful rotation. When off, you must
          manually resend the turn. Default: ON.
        </p>
      </section>

      <section>
        <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          Storage
        </h2>
        <dl class="mt-2 grid gap-2 font-ui text-[12px]" style="grid-template-columns: 12ch 1fr">
          <dt class="text-ink-muted">Schema version</dt>
          <dd class="font-mono text-ink">{{ settings.schemaVersion }}</dd>
          <dt class="text-ink-muted">Window size</dt>
          <dd class="font-mono text-ink">
            {{ settings.windowSize.width }} × {{ settings.windowSize.height }}
          </dd>
        </dl>
      </section>

      <section data-testid="compaction-section">
        <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          Compaction
        </h2>
        <p class="mt-1 font-ui text-[11px] text-ink-muted">
          When a session approaches the model's context cap, the harness
          summarises older turns into a single message so the conversation
          can keep going. Pick a tier — see "What does this mean?" below
          for the trade-off.
        </p>

        <div
          class="mt-3 inline-flex rounded-sm border border-border"
          role="radiogroup"
          aria-label="Compaction aggressiveness"
        >
          <button
            v-for="t in COMPACTION_TIERS"
            :key="t"
            type="button"
            role="radio"
            :aria-checked="compactionTier === t"
            class="px-3 py-1.5 font-ui text-[12px] border-r border-border last:border-r-0 transition-colors capitalize"
            :class="compactionTier === t
              ? 'bg-surface-3 text-ink'
              : 'bg-surface-1 text-ink-muted hover:text-ink'"
            :data-testid="`compaction-tier-${t}`"
            @click="setCompactionTier(t)"
          >
            {{ t }}
          </button>
        </div>

        <button
          type="button"
          class="mt-3 font-ui text-[11px] text-accent hover:underline"
          data-testid="compaction-explain-toggle"
          :aria-expanded="compactionExplainOpen"
          @click="compactionExplainOpen = !compactionExplainOpen"
        >
          {{ compactionExplainOpen ? 'Hide' : 'What does this mean?' }}
        </button>

        <div
          v-if="compactionExplainOpen"
          class="mt-2 rounded-sm border border-border bg-surface-1 p-3 font-ui text-[12px]"
          data-testid="compaction-explain-disclosure"
        >
          <div v-if="selectedTierExplain" class="space-y-1">
            <div class="font-medium text-ink" data-testid="compaction-explain-label">
              {{ selectedTierExplain.label }}
            </div>
            <p class="text-ink-muted" data-testid="compaction-explain-description">
              {{ selectedTierExplain.description }}
            </p>
            <dl
              class="mt-2 grid gap-1 font-mono text-[11px] text-ink-muted"
              style="grid-template-columns: 14ch 1fr"
              data-testid="compaction-explain-numerics"
            >
              <dt>Trigger %</dt>
              <dd>
                {{
                  selectedTierExplain.triggerPct > 0
                    ? `${Math.round(selectedTierExplain.triggerPct * 100)}% of cap`
                    : '—'
                }}
              </dd>
              <dt>Summarize %</dt>
              <dd>
                {{
                  selectedTierExplain.summarizePct > 0
                    ? `${Math.round(selectedTierExplain.summarizePct * 100)}% of oldest tokens`
                    : '—'
                }}
              </dd>
              <dt>Mode</dt>
              <dd>{{ selectedTierExplain.mode }}</dd>
            </dl>
          </div>
          <div v-else class="text-ink-muted">
            No explain row available — using locked defaults.
          </div>
        </div>

        <div class="mt-4 grid gap-2" style="grid-template-columns: 14ch 1fr">
          <label
            for="compaction-model"
            class="self-center font-ui text-[12px] text-ink-muted"
          >
            Compaction model
          </label>
          <select
            id="compaction-model"
            class="rounded-sm border border-border bg-surface-1 px-2 py-1 font-ui text-[12px] text-ink"
            data-testid="compaction-model-select"
            :value="compactionModelValue"
            @change="onCompactionModelChange"
          >
            <option value="">Use session's active model (recommended)</option>
            <option
              v-for="opt in compactionModelOptions"
              :key="opt.value"
              :value="opt.value"
            >
              {{ opt.label }}
            </option>
          </select>
          <!--
            TODO(compaction-strategy-ui-01KQ8TDI WP06): emit a "deprecated
            model" warning chip here when the capability gate exposes a
            programmatic deprecated check. The save still succeeds today;
            the actual deprecation enforcement happens at compaction time
            via the capability gate (plan §R7).
          -->
        </div>

        <div class="mt-2 grid gap-2" style="grid-template-columns: 14ch 1fr">
          <label
            for="compaction-archive-days"
            class="self-center font-ui text-[12px] text-ink-muted"
          >
            Archive days
          </label>
          <div>
            <input
              id="compaction-archive-days"
              type="number"
              min="7"
              max="365"
              :value="compactionArchiveDays"
              class="w-24 rounded-sm border border-border bg-surface-1 px-2 py-1 font-ui text-[12px] text-ink"
              data-testid="compaction-archive-days-input"
              @input="onCompactionArchiveDaysInput"
            />
            <p
              v-if="compactionArchiveDaysError"
              class="mt-1 font-ui text-[11px] text-signal-danger"
              role="alert"
              data-testid="compaction-archive-days-error"
            >
              {{ compactionArchiveDaysError }}
            </p>
            <p v-else class="mt-1 font-ui text-[11px] text-ink-muted">
              Soft-archived originals are deleted after this many days. Default 90.
            </p>
          </div>
        </div>

        <div class="mt-2 grid gap-2" style="grid-template-columns: 14ch 1fr">
          <label
            for="compaction-recent-window"
            class="self-center font-ui text-[12px] text-ink-muted"
          >
            Recent window
          </label>
          <div>
            <input
              id="compaction-recent-window"
              type="number"
              min="1"
              :value="compactionRecentWindow"
              class="w-24 rounded-sm border border-border bg-surface-1 px-2 py-1 font-ui text-[12px] text-ink"
              data-testid="compaction-recent-window-input"
              @input="onCompactionRecentWindowInput"
            />
            <p
              v-if="compactionRecentWindowError"
              class="mt-1 font-ui text-[11px] text-signal-danger"
              role="alert"
              data-testid="compaction-recent-window-error"
            >
              {{ compactionRecentWindowError }}
            </p>
            <p v-else class="mt-1 font-ui text-[11px] text-ink-muted">
              Most-recent user-assistant pairs that compaction never touches. Default 4.
            </p>
          </div>
        </div>
      </section>

      <section data-testid="cost-notifications-section">
        <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          Cost notifications
        </h2>
        <p class="mt-1 font-ui text-[11px] text-ink-muted">
          Get a desktop notification when your monthly LLM spend hits
          50%, 80%, 100%, 150%, or 200% of the threshold below. Each
          tier fires at most once per calendar month. Hard caps live in
          your provider dashboard — this dial is visibility only (FR-007c).
        </p>
        <div class="mt-3 grid gap-2" style="grid-template-columns: 14ch 1fr">
          <label
            for="monthly-cost-notify"
            class="self-center font-ui text-[12px] text-ink-muted"
          >
            Monthly threshold
          </label>
          <div>
            <div class="flex items-center gap-1.5">
              <span class="font-mono text-[12px] text-ink-muted">$</span>
              <input
                id="monthly-cost-notify"
                type="number"
                min="0"
                :max="MAX_MONTHLY_COST_NOTIFY_USD"
                step="0.01"
                :value="monthlyCostNotifyUsd"
                placeholder="0 (disabled)"
                class="w-28 rounded-sm border border-border bg-surface-1 px-2 py-1 font-ui text-[12px] text-ink"
                data-testid="monthly-cost-notify-input"
                @input="onMonthlyCostNotifyInput"
              />
              <span class="font-ui text-[11px] text-ink-muted">USD / month</span>
            </div>
            <p
              v-if="monthlyCostNotifyUsdError"
              class="mt-1 font-ui text-[11px] text-signal-danger"
              role="alert"
              data-testid="monthly-cost-notify-error"
            >
              {{ monthlyCostNotifyUsdError }}
            </p>
            <p v-else class="mt-1 font-ui text-[11px] text-ink-muted">
              Set to 0 to disable notifications. Maximum
              ${{ MAX_MONTHLY_COST_NOTIFY_USD }}.
            </p>
          </div>
        </div>
      </section>

      <section data-testid="context-window-overrides-section">
        <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          Context window overrides
        </h2>
        <p class="mt-1 font-ui text-[11px] text-ink-muted">
          Override the backend-derived context-window size for a provider kind. Leave a field
          blank to use the curated catalog value. Changes take effect immediately in
          the session context meter.
        </p>
        <div class="mt-3 grid gap-2">
          <div
            v-for="kind in CONTEXT_WINDOW_KINDS"
            :key="kind"
            class="grid gap-2"
            style="grid-template-columns: 10ch 1fr"
          >
            <label
              :for="`cw-override-${kind}`"
              class="self-center font-ui text-[12px] text-ink-muted capitalize"
            >
              {{ kind }}
            </label>
            <input
              :id="`cw-override-${kind}`"
              type="number"
              min="0"
              :placeholder="'catalog (default)'"
              :value="contextWindowOverrides[kind] || ''"
              class="w-36 rounded-sm border border-border bg-surface-1 px-2 py-1 font-ui text-[12px] text-ink"
              :data-testid="`cw-override-${kind}`"
              @input="onContextWindowOverrideInput(kind, $event)"
            />
          </div>
        </div>
      </section>

      <!-- Branching UX dials (branching-ux-polish-01KQ8TD7 WP06) -->
      <section data-testid="branching-section">
        <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          Branching
        </h2>
        <!-- Auto-collapse -->
        <div class="mt-2 flex items-start justify-between gap-4">
          <div>
            <p class="font-ui text-[12px] text-ink">Sidebar shows branches collapsed by default</p>
            <p class="font-ui text-[11px] text-ink-dim">
              When on, parent sessions with branch children start collapsed in the left rail.
              Individual expand/collapse choices persist across sessions.
            </p>
          </div>
          <button
            type="button"
            role="switch"
            :aria-checked="autoCollapseBranches"
            aria-label="Auto-collapse branches"
            class="relative mt-0.5 h-5 w-9 flex-shrink-0 rounded-full border transition-colors"
            :class="autoCollapseBranches
              ? 'border-accent bg-accent'
              : 'border-surface-3 bg-surface-2'"
            data-testid="auto-collapse-branches-toggle"
            @click="toggleAutoCollapseBranches"
          >
            <span
              class="absolute top-0.5 h-4 w-4 rounded-full bg-white shadow-sm transition-transform"
              :class="autoCollapseBranches ? 'translate-x-4' : 'translate-x-0.5'"
            />
          </button>
        </div>
        <!-- Delete branches with parent -->
        <div class="mt-3 flex items-start justify-between gap-4">
          <div>
            <p class="font-ui text-[12px] text-ink">Delete branches when their parent session is deleted (cascade)</p>
            <p class="font-ui text-[11px] text-ink-dim">
              When on, deleting a parent session also removes all its branch descendants.
              When off (default), branches become independent root sessions.
            </p>
          </div>
          <button
            type="button"
            role="switch"
            :aria-checked="deleteBranchesWithParent"
            aria-label="Delete branches with parent session"
            class="relative mt-0.5 h-5 w-9 flex-shrink-0 rounded-full border transition-colors"
            :class="deleteBranchesWithParent
              ? 'border-accent bg-accent'
              : 'border-surface-3 bg-surface-2'"
            data-testid="delete-branches-with-parent-toggle"
            @click="toggleDeleteBranchesWithParent"
          >
            <span
              class="absolute top-0.5 h-4 w-4 rounded-full bg-white shadow-sm transition-transform"
              :class="deleteBranchesWithParent ? 'translate-x-4' : 'translate-x-0.5'"
            />
          </button>
        </div>
        <!-- Max visible depth -->
        <div class="mt-3">
          <div class="flex items-center justify-between">
            <p class="font-ui text-[12px] text-ink">Maximum branch depth visible in the sidebar</p>
            <span class="font-mono text-[12px] text-ink" data-testid="max-branch-depth-value">{{ maxVisibleBranchDepth }}</span>
          </div>
          <p class="font-ui text-[11px] text-ink-dim">
            Sessions nested deeper than this number show a "+N more depths" affordance.
            Range 1–32; default 5.
          </p>
          <input
            type="range"
            min="1"
            max="32"
            step="1"
            :value="maxVisibleBranchDepth"
            aria-label="Maximum visible branch depth"
            class="mt-2 w-full accent-accent"
            data-testid="max-branch-depth-slider"
            @change="setMaxVisibleBranchDepth(+($event.target as HTMLInputElement).value)"
          />
        </div>
      </section>

      <section data-testid="global-context-section">
        <div class="flex items-center justify-between">
          <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
            Global context
            <span class="ml-1 text-ink-dim">({{ globalAttachments.length }})</span>
          </h2>
          <button
            type="button"
            class="flex items-center gap-1 rounded-sm border border-accent-hairline bg-surface-1 px-2 py-1 font-ui text-[11px] text-accent hover:bg-accent-glow"
            data-testid="global-add-context"
            @click="openGlobalPicker"
          >
            <Plus :size="12" />
            <span>Add context</span>
          </button>
        </div>
        <p class="mt-1 font-ui text-[11px] text-ink-dim">
          Global context applies to every session in the harness. The
          resolved stream concatenates global → project → session, so
          ordering here affects the prefix every conversation receives.
        </p>
        <div
          v-if="globalAttachmentsError"
          class="mt-2 rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[11px] text-signal-danger"
          role="alert"
          data-testid="global-attachments-error"
        >
          {{ globalAttachmentsError }}
        </div>
        <div
          v-if="globalAttachmentsLoading"
          class="mt-2 font-ui text-xs text-ink-muted"
          data-testid="global-attachments-loading"
        >
          Loading…
        </div>
        <ul
          v-else-if="globalAttachments.length > 0"
          class="mt-2 space-y-2"
          data-testid="global-attachments-list"
        >
          <li v-for="a in globalAttachments" :key="a.id">
            <AttachmentRow
              :attachment="a"
              :draggable="true"
              @refreshed="onGlobalRefreshed"
              @removed="onGlobalRemoved"
              @drag-start="onDragStart"
              @drag-over="onDragOver"
              @drop="onDrop"
              @drag-end="onDragEnd"
            />
          </li>
        </ul>
        <p
          v-else
          class="mt-2 font-ui text-xs text-ink-muted"
          data-testid="global-attachments-empty"
        >
          No global context yet. Pick a file from the library to attach.
        </p>
      </section>

      <!-- Onboarding (harness-self-mcp-onboarding-01KQ8TDU WP08) -->
      <section
        v-if="!onboardingHarnessSelfMCPDisabled"
        data-testid="onboarding-section"
      >
        <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          Onboarding
        </h2>
        <p class="mt-1 font-ui text-[11px] text-ink-muted">
          Reopen the configuration assistant to change providers, install MCP
          servers, or update Cedar policies with guided help.
        </p>
        <div
          v-if="onboardingError"
          class="mt-2 rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[11px] text-signal-danger"
          role="alert"
          data-testid="onboarding-error"
        >
          {{ onboardingError }}
        </div>
        <button
          type="button"
          :disabled="onboardingReconfiguring"
          class="mt-3 rounded-sm border border-accent bg-surface-1 px-3 py-2 font-ui text-[12px] text-accent hover:bg-accent-glow disabled:opacity-50"
          data-testid="reconfigure-with-assistant"
          @click="reconfigureWithAssistant"
        >
          {{ onboardingReconfiguring ? 'Opening session…' : 'Reconfigure with assistant' }}
        </button>
      </section>

      <!-- Keyboard shortcuts (keyboard-shortcuts-settings-01KQ8TDR) -->
      <KeyboardShortcuts />

      <section v-if="appInfo">
        <h2 class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
          Harness build
        </h2>
        <dl class="mt-2 grid gap-2 font-ui text-[12px]" style="grid-template-columns: 12ch 1fr">
          <dt class="text-ink-muted">Build</dt>
          <dd class="font-mono text-ink">{{ appInfo.build }}</dd>
          <dt class="text-ink-muted">Commit</dt>
          <dd class="font-mono text-ink">{{ appInfo.commit }}</dd>
          <dt class="text-ink-muted">Built</dt>
          <dd class="font-mono text-ink">{{ appInfo.buildTime || '—' }}</dd>
          <dt class="text-ink-muted">Go</dt>
          <dd class="font-mono text-ink">{{ appInfo.goVersion || '—' }}</dd>
          <dt class="text-ink-muted">Platform</dt>
          <dd class="font-mono text-ink">{{ appInfo.platform || '—' }}</dd>
        </dl>
      </section>
    </div>

    <AttachmentTreePicker
      :open="globalPickerOpen"
      scope-kind="global"
      scope-id=""
      @added="onGlobalAdded"
      @close="globalPickerOpen = false"
    />
  </div>
</template>
