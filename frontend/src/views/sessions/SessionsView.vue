<script setup lang="ts">
/**
 * SessionsView — the chat surface (FR-001b). Renders:
 *   - CanvasHead with the active session name + numbered section header.
 *   - MessageList of historical + currently-streaming messages.
 *   - ChatInput at the bottom.
 *
 * Route shape:
 *   `/sessions`        → empty state ("pick a session in the rail")
 *   `/sessions/<id>`   → load + render that session
 *
 * Uses the chat-ui composables `useSession` + `useShellStatus` and the
 * typed RPC client from `useHarnessClient`. Components never reach into
 * `wailsjs/*` directly — that's the FR-007 isolation rule.
 */

import { computed, nextTick, onMounted, ref, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import CanvasHead from '@/shell/CanvasHead.vue';
import NewSessionDialog from '@/shell/NewSessionDialog.vue';
import MessageList from '@/components/chat/MessageList.vue';
import SessionHeader from '@/components/chat/SessionHeader.vue';
import ChatInput from '@/components/chat/ChatInput.vue';
import ComposerError from '@/components/chat/ComposerError.vue';
import ReasoningControl from '@/components/chat/ReasoningControl.vue';
import SlashArgFill from '@/components/chat/SlashArgFill.vue';
import ResolvedContextPanel from '@/views/sessions/ResolvedContextPanel.vue';
import ConfirmToolModal from '@/components/chat/ConfirmToolModal.vue';
import PlanApprovalModal from '@/views/sessions/PlanApprovalModal.vue';
import BranchSidebar from '@/components/chat/BranchSidebar.vue';
import BranchBreadcrumb from '@/components/chat/BranchBreadcrumb.vue';
import CreateBranchModal from '@/components/chat/CreateBranchModal.vue';
import MergeSuggestionToast from '@/components/chat/MergeSuggestionToast.vue';
import CostThresholdToast from '@/components/chat/CostThresholdToast.vue';
import AuthFailureToast from '@/components/chat/AuthFailureToast.vue';
import RetryAfterRotateToast from '@/components/chat/RetryAfterRotateToast.vue';
import FallbackActivePill from '@/components/chat/FallbackActivePill.vue';
import BashPermissionModal from '@/components/permissions/BashPermissionModal.vue';
import FilesystemPermissionModal from '@/components/permissions/FilesystemPermissionModal.vue';
import CredentialPermissionModal from '@/components/permissions/CredentialPermissionModal.vue';
import ToolPermissionModal from '@/components/permissions/ToolPermissionModal.vue';
import MigrationToast from '@/components/permissions/MigrationToast.vue';
import ArtifactPreview from '@/views/artifacts/ArtifactPreview.vue';
import CostCell from '@/components/chat/CostCell.vue';
import LongSessionNudge from '@/components/chat/LongSessionNudge.vue';
import { useArtifacts, useHarnessClient, useSessions } from '@/lib/useHarnessAPI';
import { useSession } from '@/lib/useSession';
import { useEventStream } from '@/lib/useEventStream';
import { useLongSessionNudge } from '@/lib/useLongSessionNudge';
import { usePlanMode } from '@/lib/planmode';
import type {
  Artifact,
  ArtifactScope,
  ArtifactWithBytes,
  MemoryScopeKind,
  Message,
  Provider,
  ReasoningConfig,
  ReasoningStyle,
  SessionUsage,
  Settings,
  SlashExecuteResult,
  UserCommand,
} from '@/lib/types';
import { flattenChoices, inferFamily } from '@/lib/modelFamily';
import { parseUnsupportedFeatureError } from '@/lib/errors';

const route = useRoute();
const router = useRouter();
const client = useHarnessClient();
const { list: sessionList, refresh: refreshSessions } = useSessions();

// Refresh the rail's session list when SessionsView mounts so the
// empty-state checks against an up-to-date count instead of an
// initially-empty ref.
void refreshSessions();

// Session id is the route's :id param (e.g. /sessions/sess-abc).
const sessionId = computed(() => {
  const p = route.params.id;
  return typeof p === 'string' ? p : '';
});

const hasSession = computed(() => sessionId.value.length > 0);

// Three distinct empty / error states:
//   - 'no-sessions'        no sessions exist anywhere → big welcome
//   - 'no-selection'       sessions exist, none selected → pick from rail
//   - 'session-not-found'  url id has no matching backend row
//   - 'loaded'             session loaded successfully (may have a
//                          send-time error — that's surfaced inline,
//                          not by replacing the surface)
const surfaceState = computed<
  'no-sessions' | 'no-selection' | 'session-not-found' | 'loaded'
>(() => {
  if (!hasSession.value) {
    return sessionList.value.length === 0 ? 'no-sessions' : 'no-selection';
  }
  // session.value goes to null only when load() failed (404 or
  // backend error during sessions.get / listMessages). Send-time
  // errors set session.error but leave session.value populated, so
  // we don't conflate them here.
  if (session.session.value === null && !session.loading.value) {
    return 'session-not-found';
  }
  return 'loaded';
});

const sessionIdRef = computed(() => sessionId.value);
const session = useSession(sessionIdRef);

// ── Plan approval (plan-mode-posture-01KZNP3F WP06 / p0-wiring-fixes WP01) ──
// usePlanMode subscribes to `plan_mode_changed` Wails events and exposes
// pendingPlanId when the model is awaiting user approval on a plan artifact.
// NOTE: usePlanMode is a composable — it must be called at setup time with
// a static sessionId. We pass the computed sessionId.value; the composable
// is re-created whenever SessionsView re-mounts, which is sufficient since
// plan approval is a per-session transient state.
const { pendingPlanId, setActive: setPlanModeActive } = usePlanMode(sessionId.value);

// Tracks the plan text fetched from the artifacts store when pendingPlanId is set.
const pendingPlanText = ref<string | null>(null);

watch(
  pendingPlanId,
  async (planId) => {
    if (!planId) {
      pendingPlanText.value = null;
      return;
    }
    try {
      const artifact = await client.artifacts.get(planId);
      // bytes is base64-encoded; plan artifacts are plain text (markdown).
      pendingPlanText.value = artifact.bytes ? atob(artifact.bytes) : '';
    } catch {
      pendingPlanText.value = '';
    }
  },
);

function onPlanApproved(_planId: string) {
  setPlanModeActive(false);
  pendingPlanText.value = null;
  void session.refresh();
}

function onPlanDiscarded(_planId: string) {
  setPlanModeActive(false);
  pendingPlanText.value = null;
}

function onPlanEdited(_planId: string, _editedPlan: string) {
  setPlanModeActive(false);
  pendingPlanText.value = null;
  void session.refresh();
}

const providers = ref<readonly Provider[]>([]);
const providersLoaded = ref(false);

async function refreshProviders() {
  try {
    providers.value = await client.llm.listProviders();
  } catch {
    providers.value = [];
  } finally {
    providersLoaded.value = true;
  }
}

// Refresh providers whenever the user lands on / changes sessions —
// vue-router keeps SessionsView mounted across /sessions/<id> param
// changes, so a once-on-setup fetch misses providers that were added
// while we were already on the page.
watch(
  () => route.path,
  () => {
    void refreshProviders();
  },
  { immediate: true },
);

// Also refresh when the window regains focus, in case the user just
// returned from the providers tab.
window.addEventListener('focus', () => {
  void refreshProviders();
});

// Kinds the backend currently has a working stream adapter for.
// Adding others (openai, bedrock, …) is a per-kind mission; until then
// the chat surface picks the first provider whose kind we know works,
// rather than blindly using providers[0] and hitting "no adapter for
// kind X" on send.
const SUPPORTED_KINDS = new Set([
  'anthropic',
  'bedrock',
  'openai',
  'openrouter',
]);

// Active (provider, model) tuple for this session. Model defaults to
// the profile's primary; the switcher pill below lets the user pick
// a different model from the same FAMILY (cross-family transfers
// would mean swapping tokenisers mid-conversation, so we block them).
const activeProviderId = ref<string>('');
const activeModelId = ref<string>('');

// ── Reasoning effort state (provider-implementation-uniformity-01KQ8V4F WP07) ──
// Set via the /effort slash command; the ReasoningControl chip reads it.
// Both fields are retained so switching providers mid-session and back
// restores the previously-set value for that provider's style.
const activeReasoningConfig = ref<ReasoningConfig | undefined>(undefined);

// ── ComposerError state (provider-implementation-uniformity-01KQ8V4F WP08) ──
// Holds an ErrUnsupportedFeature string when the last stream failed due to
// a knob-policy rejection. Cleared on dismiss or on next successful send.
// We watch session.error separately so we can distinguish knob errors from
// other stream errors (auth failures, transient errors, etc.).
const composerErrorText = ref<string | null>(null);

watch(
  () => session.error.value,
  (err) => {
    if (!err) return;
    // Only surface ErrUnsupportedFeature in the ComposerError banner;
    // other errors continue to render via MessageList's error-message prop.
    if (parseUnsupportedFeatureError(err)) {
      composerErrorText.value = err;
    }
  },
);

function onComposerErrorDismiss() {
  composerErrorText.value = null;
}

const activeProvider = computed<Provider | null>(() => {
  if (providers.value.length === 0) return null;
  // Prefer a provider whose kind has a working stream adapter.
  const supported = providers.value.filter((p) => {
    const kind = p.kind ?? '';
    return SUPPORTED_KINDS.has(kind);
  });
  const pool = supported.length > 0 ? supported : providers.value;
  // Honour an explicitly-selected provider when it exists.
  if (activeProviderId.value) {
    const match = pool.find((p) => p.id === activeProviderId.value);
    if (match) return match;
  }
  return pool[0] ?? null;
});

const activeProviderUnsupported = computed(() => {
  const p = activeProvider.value;
  if (!p) return false;
  return !SUPPORTED_KINDS.has(p.kind ?? '');
});

const activeFamily = computed<string>(() => {
  const p = activeProvider.value;
  if (!p || !activeModelId.value) return '';
  return inferFamily(p.kind, activeModelId.value);
});

// All (provider, model) tuples available across all configured
// providers, with their inferred family. Drives the switcher pill.
const allChoices = computed(() => flattenChoices(providers.value));

// ── Reasoning style (provider-implementation-uniformity-01KQ8V4F WP07) ──
// Derived from the active provider kind + model ID. No round-trip needed;
// the capability YAML rules are: Anthropic sonnet-* / opus-* → token_budget;
// OpenAI o-series → effort_string.
const activeReasoningStyle = computed<ReasoningStyle>(() => {
  const p = activeProvider.value;
  if (!p) return 'none';
  const kind = p.kind ?? '';
  const model = activeModelId.value.toLowerCase();
  if (kind === 'anthropic' || kind === 'bedrock') {
    // claude-sonnet-* and claude-opus-* (with and without anthropic. prefix)
    if (/claude[-.]sonnet/.test(model) || /claude[-.]opus/.test(model)) {
      return 'token_budget';
    }
  }
  if (kind === 'openai' || kind === 'openrouter') {
    // o1, o3, o4, o1-mini, o3-mini, etc.
    if (/^(openai\/)?o\d/.test(model) || /\/(o\d)/.test(model)) {
      return 'effort_string';
    }
  }
  return 'none';
});

// Same family as the active session — these are click-able. Other
// families render disabled with a tooltip.
const familyChoices = computed(() =>
  allChoices.value.filter((c) => c.family === activeFamily.value),
);
const otherFamilyChoices = computed(() =>
  allChoices.value.filter((c) => c.family !== activeFamily.value),
);

// Read the new-session-dialog's localStorage stash for this session,
// if present. NewSessionDialog writes the user's chosen
// (providerId, modelId) under "kenaz.session.config.<id>" so we
// can honour cross-family choices that the mid-conversation switcher
// would otherwise block.
function readSessionConfig(sessionID: string): {
  providerId: string;
  modelId: string;
} | null {
  if (!sessionID) return null;
  try {
    const raw = window.localStorage.getItem(
      `kenaz.session.config.${sessionID}`,
    );
    if (!raw) return null;
    const parsed = JSON.parse(raw) as {
      providerId?: string;
      modelId?: string;
    };
    if (parsed.providerId && parsed.modelId) {
      return { providerId: parsed.providerId, modelId: parsed.modelId };
    }
  } catch {
    /* malformed stash — ignore */
  }
  return null;
}

// On session id or provider list change, seed the active selection.
// Priority: stashed dialog config > previously-set value > provider
// primary model.
watch(
  [sessionId, providers],
  ([newSid]) => {
    // Reset prior selection when switching sessions.
    if (newSid) {
      const stashed = readSessionConfig(newSid);
      if (stashed) {
        activeProviderId.value = stashed.providerId;
        activeModelId.value = stashed.modelId;
        return;
      }
    }
    const p = activeProvider.value;
    if (!p) return;
    if (!activeProviderId.value) activeProviderId.value = p.id;
    if (!activeModelId.value) {
      const list = p.models && p.models.length > 0 ? p.models : [p.model];
      activeModelId.value = list[0] || '';
    }
  },
  { immediate: true },
);

const switcherOpen = ref(false);
function toggleSwitcher() {
  switcherOpen.value = !switcherOpen.value;
}
function pickModel(providerId: string, modelId: string) {
  activeProviderId.value = providerId;
  activeModelId.value = modelId;
  switcherOpen.value = false;
}

const sessionTitle = computed(() => {
  switch (surfaceState.value) {
    case 'no-sessions':
      return 'Welcome';
    case 'no-selection':
      return 'Sessions';
    case 'session-not-found':
      return 'Session not found';
    default:
      return session.session.value?.name ?? 'Session';
  }
});
const sessionSubtitle = computed(() => {
  switch (surfaceState.value) {
    case 'no-sessions':
      return 'Configure a provider, then start your first conversation.';
    case 'no-selection':
      return 'Pick a session from the rail or start a new one.';
    case 'session-not-found':
      return 'This session was deleted or its id is wrong.';
    default:
      // Active chat: show no subtitle. The session name in the header
      // is sufficient; chrome below it just wastes vertical space.
      return undefined;
  }
});

// In the active-chat default, drop the breadcrumb (`01 / SESSIONS`)
// chrome too — the session name alone is enough orientation. Empty/
// error states keep the breadcrumb so the user knows where they are.
const showSessionBreadcrumb = computed(
  () => surfaceState.value !== 'loaded',
);

// ── Branch breadcrumb (branching-ux-polish-01KQ8TD7 WP04) ───────────────────
// Displayed just below CanvasHead when the active session is a branch.

/** Whether the current session is a branch (has a parent). */
const isBranchSession = computed(
  () => !!session.session.value?.parentSessionId,
);

/** The parent session ID (empty string for root sessions). */
const breadcrumbParentSessionId = computed(
  () => session.session.value?.parentSessionId ?? '',
);

/** Parent session's display name — looked up in the rail's session list. */
const breadcrumbParentTitle = computed(() => {
  const parentId = breadcrumbParentSessionId.value;
  if (!parentId) return '';
  const parent = sessionList.value.find((s) => s.id === parentId);
  if (parent) return parent.name;
  // Parent may have been deleted — use branchTitle snapshot if available.
  return session.session.value?.branchTitle ?? 'Deleted session';
});

/** Whether the parent session no longer exists in the session list. */
const breadcrumbParentDeleted = computed(() => {
  const parentId = breadcrumbParentSessionId.value;
  if (!parentId) return false;
  return !sessionList.value.some((s) => s.id === parentId);
});

const isStreaming = computed(
  () => session.streamSubscriptionId.value !== null,
);

// "Thinking…" — stream is open but no chunks have arrived yet.
const isWaitingForFirstChunk = computed(
  () =>
    session.streamSubscriptionId.value !== null &&
    session.currentlyStreaming.value === null,
);

// Per-model context window — sourced from the backend's curated
// capability table via Provider.modelInfos. 0 means "unknown"; the
// meter enters an explicit "unknown" state rather than a misleading
// 200k fallback.
//
// Resolution order (WP05):
//   1. User override from contextWindowOverrides[provider.kind]
//   2. Catalog value from provider.modelInfos[].contextWindow
//   3. 0 (unknown — meter shows "—")
//
// contextDenominator: >0 = known window; 0 = unknown.
const contextDenominator = computed((): number => {
  const provider = activeProvider.value;
  const modelId = activeModelId.value;
  if (!provider || !modelId) return 0;
  // WP05: prefer any user-supplied override for this provider kind.
  const providerKind = provider.kind ?? '';
  const override =
    providerKind && contextWindowOverrides.value
      ? contextWindowOverrides.value[providerKind]
      : undefined;
  if (typeof override === 'number' && override > 0) return override;
  // Fall back to the catalog-derived value from ModelInfos.
  const info = provider.modelInfos?.find((m) => m.id === modelId);
  const cw = info?.contextWindow ?? 0;
  return cw > 0 ? cw : 0;
});

// hasContextWindow: true when the backend has supplied a non-zero cap.
const hasContextWindow = computed(() => contextDenominator.value > 0);

// contextNumerator: the cumulative input-token count for the session.
// Reads from session.lastUsage which is updated in near-real-time via
// the `session.usage.updated` broker event after each LLM turn
// (backend-context-window-length-01KQ8TD3 WP03+WP04).
// Falls back to 0 when no turn has completed yet.
const contextNumerator = computed(
  () => session.lastUsage.value?.promptTokens ?? 0,
);

const contextWindowPct = computed(() => {
  const max = contextDenominator.value;
  if (max <= 0) return 0;
  return Math.min(100, Math.round((contextNumerator.value / max) * 100));
});

const contextWindowLabel = computed(() => {
  const max = contextDenominator.value;
  const used = contextNumerator.value;
  // Compact thousands formatter ("1.2k", "12k", "128k").
  const fmt = (n: number) => {
    if (n < 1_000) return String(n);
    if (n < 10_000) return (n / 1_000).toFixed(1).replace(/\.0$/, '') + 'k';
    return Math.round(n / 1_000) + 'k';
  };
  if (!hasContextWindow.value) return '—';
  return `${fmt(used)} / ${fmt(max)}`;
});

const contextBarTone = computed<'ok' | 'warn' | 'danger'>(() => {
  const pct = contextWindowPct.value;
  if (pct >= 85) return 'danger';
  if (pct >= 65) return 'warn';
  return 'ok';
});

// ── send queue ─────────────────────────────────────────────────────
//
// While a turn is streaming, follow-up sends are queued instead of
// blocked. The watch on `isStreaming` drains the queue as soon as the
// current turn ends (whether by completion or cancel). A queued turn
// dispatches with whatever provider/model is active at drain time, so
// the user can switch models between queueing and dispatch.
type QueuedTurn =
  | { kind: 'text'; content: string }
  | { kind: 'blocks'; blocks: import('@/lib/types').ContentBlock[] };

const sendQueue = ref<QueuedTurn[]>([]);

async function onSend(content: string) {
  if (!hasSession.value || !activeProvider.value) return;
  // When the user staged any multimodal attachments, the
  // sendBlocks event has already fired and routed through
  // session.sendBlocks — skip the legacy text-only path so we don't
  // double-persist the user turn. We also bail when content is empty
  // (e.g. attachments-only sends).
  if (sentBlocksThisTurn.value || content.length === 0) {
    sentBlocksThisTurn.value = false;
    return;
  }
  if (isStreaming.value) {
    sendQueue.value = [...sendQueue.value, { kind: 'text', content }];
    return;
  }
  await session.send(content, activeProvider.value.id, activeModelId.value);
}

const sentBlocksThisTurn = ref(false);

async function onSendBlocks(contentBlocks: import('@/lib/types').ContentBlock[]) {
  if (!hasSession.value || !activeProvider.value) return;
  sentBlocksThisTurn.value = true;
  if (isStreaming.value) {
    sendQueue.value = [
      ...sendQueue.value,
      { kind: 'blocks', blocks: contentBlocks },
    ];
    return;
  }
  await session.sendBlocks(
    contentBlocks,
    activeProvider.value.id,
    activeModelId.value,
  );
}

// Drain one queued turn each time the stream ends. Recursively re-fires
// itself via the watch — one streaming turn at a time, in FIFO order.
watch(isStreaming, async (now, prev) => {
  if (!prev || now) return; // only act on true → false transitions
  if (sendQueue.value.length === 0) return;
  if (!hasSession.value || !activeProvider.value) return;
  const [next, ...rest] = sendQueue.value;
  sendQueue.value = rest;
  if (next.kind === 'text') {
    await session.send(
      next.content,
      activeProvider.value.id,
      activeModelId.value,
    );
  } else {
    await session.sendBlocks(
      next.blocks,
      activeProvider.value.id,
      activeModelId.value,
    );
  }
});

async function onCancel() {
  await session.cancel();
}

// ── slash commands ──────────────────────────────────────────────────
//
// Slash commands ride alongside the regular chat flow. The composer
// emits `slashCommand(raw)` instead of `send` / `sendBlocks` when the
// user submits a `/...` line. We dispatch via the typed RPC, then
// surface the result inline as a synthetic system / assistant-shaped
// Message in the chat. `/model` results carry metadata that updates
// the local active provider+model. `/clear` returns metadata that
// triggers a refresh of the on-disk message list (the divider was
// persisted server-side).

interface SlashTransientMessage {
  id: string;
  role: 'system' | 'assistant';
  content: string;
  createdAt: string;
}

// Per-session transient slash output. Keyed by session id so
// switching sessions doesn't leak previous-session results into the
// new view.
const slashResults = ref<Record<string, SlashTransientMessage[]>>({});

const activeSlashResults = computed<readonly SlashTransientMessage[]>(() => {
  const sid = sessionId.value;
  if (!sid) return [];
  return slashResults.value[sid] ?? [];
});

function appendSlashResult(sid: string, kind: string, text: string) {
  // Map slash result kinds to message-bubble roles. The system role
  // renders centered + italic which fits info / warning / system; the
  // assistant role renders left-aligned which fits errors well
  // enough that the user notices something went wrong without us
  // shipping a new bubble variant in v1.
  const role: 'system' | 'assistant' = kind === 'error' ? 'assistant' : 'system';
  const id = `slash-${sid}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
  const entry: SlashTransientMessage = {
    id,
    role,
    content: text,
    createdAt: new Date().toISOString(),
  };
  const existing = slashResults.value[sid] ?? [];
  slashResults.value = {
    ...slashResults.value,
    [sid]: [...existing, entry],
  };
}

// ── SlashArgFill state (WP06) ────────────────────────────────────────
//
// When the user picks a user-defined slash command that declares one or
// more inputs, we intercept the slashCommand event, look up the full
// command shape (which carries the inputs array), and surface the
// SlashArgFill panel above the composer instead of immediately dispatching.
// Once all args are resolved the panel emits `submit(args)` and we call
// `client.slashcmd.run`.

interface PendingArgFill {
  command: UserCommand;
  /** The original raw text the user typed (e.g. "/deploy"); kept for
   * display but not used in the run call — args come from the panel. */
  raw: string;
}

const pendingArgFill = ref<PendingArgFill | null>(null);

function dismissArgFill() {
  pendingArgFill.value = null;
}

async function onArgFillSubmit(args: Record<string, string>) {
  const fill = pendingArgFill.value;
  pendingArgFill.value = null;
  if (!fill) return;
  const sid = sessionId.value;
  if (!sid) return;
  let result;
  try {
    result = await client.slashcmd.run(fill.command.name, args, sid, fill.command.projectId ?? '', '', '');
  } catch (err) {
    appendSlashResult(sid, 'error', err instanceof Error ? err.message : String(err));
    return;
  }
  appendSlashResult(sid, result.kind, result.text);
}

async function onSlashCommand(raw: string) {
  const sid = sessionId.value;
  if (!sid) {
    return;
  }

  // ── User-command arg-fill intercept (WP06) ───────────────────────────
  // Parse the command name from the raw string (first token after `/`).
  // If it resolves to a user-defined command with declared inputs, open
  // the SlashArgFill panel rather than dispatching immediately.
  const trimmedRaw = raw.trim();
  if (trimmedRaw.startsWith('/')) {
    const token = trimmedRaw.slice(1).split(/\s+/)[0] ?? '';
    if (token) {
      let userCmd: UserCommand | null = null;
      try {
        userCmd = await client.slashcmd.get(token, '');
      } catch {
        // not a user command — fall through to built-in dispatch
      }
      if (userCmd && userCmd.inputs && userCmd.inputs.length > 0) {
        pendingArgFill.value = { command: userCmd, raw };
        return;
      }
    }
  }

  // ── Built-in / no-input dispatch ─────────────────────────────────────
  let result: SlashExecuteResult;
  try {
    result = await client.slash.execute(sid, raw);
  } catch (err) {
    appendSlashResult(
      sid,
      'error',
      err instanceof Error ? err.message : String(err),
    );
    return;
  }
  appendSlashResult(sid, result.kind, result.text);

  // /model — apply provider+model metadata to the local active state
  // so the next regular message dispatches to the chosen tuple.
  const modelId = result.metadata?.['modelId'];
  const providerId = result.metadata?.['providerId'];
  if (typeof modelId === 'string' && typeof providerId === 'string' && modelId !== '' && providerId !== '') {
    pickModel(providerId, modelId);
  }

  // /effort — apply the reasoning knob to local state so ReasoningControl
  // reflects the new setting immediately without a round-trip.
  // (provider-implementation-uniformity-01KQ8V4F WP07)
  if (result.metadata?.['reasoningKnob']) {
    const knob = result.metadata['reasoningKnob'] as Record<string, unknown>;
    const newConfig: ReasoningConfig = {};
    if (typeof knob['openAIEffort'] === 'string' && knob['openAIEffort']) {
      newConfig.openAIEffort = knob['openAIEffort'];
    }
    if (typeof knob['anthropicThinkingBudget'] === 'number' && knob['anthropicThinkingBudget'] > 0) {
      newConfig.anthropicThinkingBudget = knob['anthropicThinkingBudget'];
    }
    // Merge: keep the non-active style's stored value so a provider switch
    // and switch-back restores the previous setting for that provider.
    activeReasoningConfig.value = {
      ...activeReasoningConfig.value,
      ...newConfig,
    };
  }

  // /clear — the backend appended a system divider; refresh the
  // message list so the divider shows up in the transcript.
  if (trimmedRaw.startsWith('/clear')) {
    void refreshActiveMessages();
  }
}

async function onBashCommand(cmd: string) {
  const sid = sessionId.value;
  if (!sid) return;
  // Echo the user's command first (mirrors what the user typed) so the
  // transcript reads naturally — same convention slash commands follow.
  appendSlashResult(sid, 'info', `$ ${cmd}`);
  try {
    const result = await client.bash.exec(sid, cmd);
    const parts: string[] = [];
    if (result.stdout) parts.push(result.stdout.trimEnd());
    if (result.stderr) parts.push(`stderr:\n${result.stderr.trimEnd()}`);
    if (result.exitCode !== 0) parts.push(`exit ${result.exitCode}`);
    if (result.truncated) parts.push('… (output truncated at 64 KiB)');
    const body = parts.length > 0 ? parts.join('\n\n') : '(no output)';
    appendSlashResult(sid, result.exitCode === 0 ? 'info' : 'error', body);
  } catch (err) {
    appendSlashResult(sid, 'error', err instanceof Error ? err.message : String(err));
  }
}

async function refreshActiveMessages() {
  const sid = sessionId.value;
  if (!sid) return;
  try {
    await session.refresh();
  } catch {
    /* swallow — the next user turn will re-fetch anyway. */
  }
}

// Combined view: persisted messages + transient slash results,
// ordered by createdAt so a slash result lands in the right spot.
const visibleMessages = computed<readonly Message[]>(() => {
  const persisted = session.messages.value;
  const transient = activeSlashResults.value;
  if (transient.length === 0) return persisted;
  // Transient messages are not real Message objects; map them up.
  const sid = sessionId.value;
  const synthetic: Message[] = transient.map((t) => ({
    id: t.id,
    sessionId: sid,
    role: t.role,
    content: t.content,
    createdAt: t.createdAt,
  }));
  // Append after persisted; v1 doesn't reorder by createdAt — the
  // user just submitted the slash command, so it's freshest.
  return [...persisted, ...synthetic];
});

function gotoProviders() {
  void router.push('/providers');
}

const newSessionDialogOpen = ref(false);
function openNewSession() {
  newSessionDialogOpen.value = true;
}
async function onNewSessionDialogClose() {
  newSessionDialogOpen.value = false;
  await refreshSessions();
}

// ── branches sidebar (agent-kernel-graph; Bundle B WP09) ──────────────
const createBranchModalOpen = ref(false);
function openCreateBranchModal() {
  createBranchModalOpen.value = true;
}
function closeCreateBranchModal() {
  createBranchModalOpen.value = false;
}
function onBranchOpen(childSessionId: string) {
  if (!childSessionId) return;
  void router.push(`/sessions/${childSessionId}`);
}

// ── "Branch from this turn" handler (branching-ux-polish-01KQ8TD7 WP05) ─────
const branchFromTurnError = ref<string | null>(null);
const branchFromTurnLoading = ref(false);

async function onBranchFromTurn(message: { id?: string }) {
  const parentSessionId = sessionId.value;
  const parentMessageId = message.id;
  if (!parentSessionId || !parentMessageId) return;
  if (branchFromTurnLoading.value) return;
  branchFromTurnLoading.value = true;
  branchFromTurnError.value = null;
  try {
    const branch = await client.branches.createExplicit({
      parentSessionId,
      parentMessageId,
    });
    if (branch.childSessionId) {
      void router.push(`/sessions/${branch.childSessionId}`);
    }
  } catch (err) {
    branchFromTurnError.value = err instanceof Error ? err.message : String(err);
  } finally {
    branchFromTurnLoading.value = false;
  }
}

const hasAnyProvider = computed(() => providers.value.length > 0);

// long-turn-resilience WP03: resume a partial message stream.
// resumeMessage opens a continuation stream; errors surface via the
// stream-closed event rather than here.
async function onResumeMessage(messageId: string) {
  const sid = sessionId.value;
  if (!sid || !messageId) return;
  try {
    await client.sessions.resumeMessage(sid, messageId);
  } catch (e) {
    // resumeMessage opens a stream; errors surface via the stream-closed event
    console.warn('resume failed', e);
  }
}

// Long-term-memory opt-in. Off by default (privacy posture); read once
// on mount and again when the window regains focus so toggling it in
// settings takes effect on the next chat.
const memoryEnabled = ref(false);
const lastRememberError = ref<string | null>(null);

// per-message-token-meter-01KR3PQR: loaded alongside compaction settings
// so the chip visibility is reactive to settings changes.
const showPerMessageTokenMeter = ref(false);

// Soft-archive retention window — drives the swept-count placeholder
// copy in MessageList. Falls back to the locked default 90 (plan §2.9)
// when settings have not been loaded or the call fails.
const compactionArchiveDays = ref<number>(90);

// Per-provider-kind context-window overrides
// (backend-context-window-length-01KQ8TD3 WP05). Loaded once on mount
// and refreshed on window focus so settings changes in the Settings
// panel take effect without a full reload. When a key is absent the
// contextDenominator falls back to the catalog-derived value.
const contextWindowOverrides = ref<Settings['contextWindowOverrides']>({});

// provider-keychain-rotation-01KQ8TD9 WP05 — auto-resume on key rotation.
// Default true on a fresh install.
const autoResumeOnKeyRotation = ref(true);

// Compaction overhead surface (mission compaction-strategy-ui-01KQ8TDI
// WP08 / plan §2.11). The backend tags every compaction-driven LLM
// call with cost.kind = "compaction" and accumulates a running total
// (see core/compaction/wiring/llm.go OverheadTotals). The frontend
// surface here renders a compact "Compaction overhead: $X.XX of $Y.YY
// total" line on the chat header when the overhead is non-zero so
// users can see the cost of compaction without opening a dedicated
// panel.
//
// TODO(compaction-strategy-ui WP08+): wire an RPC method that returns
// the per-session OverheadTotals shape (Total / Currency / Calls /
// Indeterminate / InputTokens / OutputTokens — defined in
// core/compaction/wiring/llm.go). Until the RPC lands the values
// stay at zero and the row is hidden by compactionOverheadVisible.
const compactionOverheadUSD = ref<number>(0);
const compactionOverheadTotalUSD = ref<number>(0);
const compactionOverheadVisible = computed(
  () => compactionOverheadUSD.value > 0,
);

async function refreshMemoryFlag() {
  try {
    memoryEnabled.value = await client.settings.getMemory();
  } catch {
    memoryEnabled.value = false;
  }
}

async function refreshCompactionSettings() {
  try {
    const s = await client.settings.get();
    if (typeof s.compactionArchiveDays === 'number' && s.compactionArchiveDays > 0) {
      compactionArchiveDays.value = s.compactionArchiveDays;
    }
    // WP05: pull context-window overrides so contextDenominator respects
    // any user-supplied values without requiring a full reload.
    if (s.contextWindowOverrides && typeof s.contextWindowOverrides === 'object') {
      contextWindowOverrides.value = s.contextWindowOverrides;
    }
    // per-message-token-meter-01KR3PQR
    showPerMessageTokenMeter.value = s.showPerMessageTokenMeter === true;
  } catch {
    // Soft-fail: leave the locked default in place.
  }
  // provider-keychain-rotation-01KQ8TD9 WP05
  try {
    autoResumeOnKeyRotation.value = await client.settings.getAutoResumeOnKeyRotation();
  } catch {
    autoResumeOnKeyRotation.value = true;
  }
}

onMounted(() => {
  void refreshMemoryFlag();
  void refreshCompactionSettings();
});
window.addEventListener('focus', () => {
  void refreshMemoryFlag();
  void refreshCompactionSettings();
});

// ── search-focus pulse (cross-session-search WP05) ─────────────────────
//
// When the SearchModal navigates to /sessions/<id>#<messageId>, scroll
// the matching message into view and apply a brief 'search-focus-pulse'
// CSS class. The class is removed after the animation finishes (3 s)
// so the pulse plays exactly once per arrival.
//
// We poll for a short window because the message list mounts
// asynchronously after the session loads. If the message is never found
// (e.g. archived / wrong session) the polling gives up silently.
function focusSearchHashTarget(messageId: string) {
  if (!messageId) return;
  const start = Date.now();
  const tryFocus = () => {
    const el = document.querySelector(
      `[data-message-id="${CSS.escape(messageId)}"]`,
    ) as HTMLElement | null;
    if (el) {
      el.scrollIntoView({ behavior: 'smooth', block: 'center' });
      el.classList.add('search-focus-pulse');
      window.setTimeout(() => el.classList.remove('search-focus-pulse'), 3000);
      return;
    }
    if (Date.now() - start > 5000) return;
    window.setTimeout(tryFocus, 100);
  };
  tryFocus();
}

watch(
  () => [route.path, route.hash] as const,
  ([_path, hash]) => {
    if (!hash || !hash.startsWith('#')) return;
    void nextTick(() => focusSearchHashTarget(hash.slice(1)));
  },
  { immediate: true },
);

async function onRemember(m: Message, scope: MemoryScopeKind = 'session') {
  if (!sessionId.value || !m.id) return;
  lastRememberError.value = null;
  try {
    await client.memory.rememberMessage(sessionId.value, m.id, scope);
  } catch (err) {
    lastRememberError.value =
      err instanceof Error ? err.message : String(err);
  }
}

// ── Artifacts (artifacts-storage WP03) ───────────────────────────────
//
// Session-scoped artifact list — fetched once per session via the
// useArtifacts composable, projected to a per-message map so individual
// MessageBubbles never trigger their own RPC fetches (FR-009 / plan §5).
const sessionArtifacts = useArtifacts({});
const activeTab = ref<'chat' | 'artifacts' | 'context'>('chat');
const artifactPreviewOpen = ref(false);
const artifactPreviewPayload = ref<ArtifactWithBytes | null>(null);
const lastArtifactError = ref<string | null>(null);

function refreshSessionArtifacts() {
  if (!sessionId.value) return;
  void sessionArtifacts.setFilter({ sessionId: sessionId.value });
}

watch(sessionId, () => {
  refreshSessionArtifacts();
}, { immediate: true });

// Refresh the session artifact list when the LLM stream closes so that
// tool-created artifacts (e.g. kenaz__save_artifact) appear immediately
// without requiring a manual navigation or session switch.
useEventStream<{ session_id?: string }>('llm:stream-closed', (payload) => {
  if (payload?.session_id && payload.session_id !== sessionId.value) return;
  refreshSessionArtifacts();
  // Refresh cost pill on every stream close (token-cost-telemetry WP04).
  void refreshSessionUsage();
});

// ── Cost telemetry (token-cost-telemetry-01KQ8TD7 WP04) ──────────────
const sessionUsage = ref<SessionUsage | null>(null);
const sessionUsageLoading = ref(false);

async function refreshSessionUsage() {
  if (!sessionId.value) return;
  sessionUsageLoading.value = true;
  try {
    sessionUsage.value = await client.sessions.getUsage(sessionId.value);
  } catch {
    // Non-fatal: leave existing value, don't surface error to user.
  } finally {
    sessionUsageLoading.value = false;
  }
}

watch(sessionId, () => {
  sessionUsage.value = null;
  void refreshSessionUsage();
}, { immediate: true });

const artifactsByMessage = computed<ReadonlyMap<string, readonly Artifact[]>>(() => {
  const map = new Map<string, Artifact[]>();
  for (const a of sessionArtifacts.list.value) {
    const mid = a.sourceRef.messageId;
    if (!mid) continue;
    const list = map.get(mid) ?? [];
    list.push(a);
    map.set(mid, list);
  }
  return map;
});

function defaultArtifactTitle(content: string): string {
  const flat = content.replace(/\s+/g, ' ').trim();
  return flat.slice(0, 60) || 'pinned message';
}

async function onSaveArtifactFromMessage(m: Message) {
  if (!sessionId.value || !m.id) return;
  const suggested = defaultArtifactTitle(m.content);
  const title =
    typeof window !== 'undefined' && typeof window.prompt === 'function'
      ? window.prompt('Save as artifact — title:', suggested)
      : suggested;
  if (title === null) return;
  const trimmed = title.trim() || suggested;
  lastArtifactError.value = null;
  try {
    await sessionArtifacts.saveFromMessage(
      sessionId.value,
      m.id,
      trimmed,
    );
  } catch (err) {
    lastArtifactError.value = err instanceof Error ? err.message : String(err);
  }
}

async function openArtifactPreview(a: Artifact) {
  lastArtifactError.value = null;
  try {
    artifactPreviewPayload.value = await client.artifacts.get(a.id);
    artifactPreviewOpen.value = true;
  } catch (err) {
    lastArtifactError.value = err instanceof Error ? err.message : String(err);
  }
}

function closeArtifactPreview() {
  artifactPreviewOpen.value = false;
  artifactPreviewPayload.value = null;
}

async function promoteArtifact(
  id: string,
  scopeKind: ArtifactScope,
  scopeId: string,
): Promise<Artifact> {
  const updated = await client.artifacts.promote(id, scopeKind, scopeId);
  refreshSessionArtifacts();
  if (
    artifactPreviewPayload.value &&
    artifactPreviewPayload.value.artifact.id === id
  ) {
    artifactPreviewPayload.value = {
      artifact: updated,
      bytes: artifactPreviewPayload.value.bytes,
    };
  }
  return updated;
}

async function deleteArtifact(id: string): Promise<void> {
  await client.artifacts.remove(id);
  refreshSessionArtifacts();
}

function formatTimestamp(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString();
}

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const kb = bytes / 1024;
  if (kb < 1024) return kb < 10 ? `${kb.toFixed(1)} KB` : `${Math.round(kb)} KB`;
  const mb = kb / 1024;
  return mb < 10 ? `${mb.toFixed(1)} MB` : `${Math.round(mb)} MB`;
}

// ── Long-session nudge (v0.5.6 memory-trust-signals) ──────────────────────
//
// The nudge banner fires when the session crosses either threshold:
//   - 30 user-assistant turn pairs (60 total messages), OR
//   - 50,000 cumulative prompt tokens
// Both thresholds are configurable via Settings → Display.
//
// Per-session dismiss: the user can click "Dismiss for this session" and
// the banner won't re-appear until the next session (no persistence needed).
//
// Reset the composable when the session changes so crossing thresholds in
// a previous session doesn't immediately re-trigger the banner.

const _nudgeMessageCount = computed(() => visibleMessages.value.length);
const _nudgePromptTokens = computed(() => session.lastUsage.value?.promptTokens ?? 0);

const longSessionNudge = useLongSessionNudge({
  messageCount: _nudgeMessageCount,
  promptTokens: _nudgePromptTokens,
});

// Reset dismiss state when navigating to a different session.
watch(sessionId, () => {
  longSessionNudge.dismiss(); // dismiss carries across sessions; re-set by the
  // composable's internal ref which resets on re-instantiation. Because
  // SessionsView is kept alive across navigation, we re-create by recreating
  // the dismissed ref indirectly. Use a stable "last dismissed session" ref
  // to achieve per-session semantics without full re-mount.
});

// Per-session dismiss: track which session the user dismissed so that
// navigating to a new session shows the banner fresh.
const nudgeDismissedForSessionId = ref<string>('');

const nudgeVisible = computed<boolean>(() => {
  if (!hasSession.value) return false;
  if (activeTab.value !== 'chat') return false;
  if (nudgeDismissedForSessionId.value === sessionId.value) return false;
  return longSessionNudge.nudgeVisible.value;
});

function onNudgeDismiss() {
  nudgeDismissedForSessionId.value = sessionId.value;
}

async function onNudgeBranch() {
  // Open the CreateBranchModal — same flow as the header "Branch" button.
  openCreateBranchModal();
}

async function onNudgeNewSession() {
  // Open NewSessionDialog in the same project.
  newSessionDialogOpen.value = true;
}
</script>

<template>
  <div class="grid h-full" style="grid-template-rows: auto 1fr">
    <!-- header -->
    <div>
      <CanvasHead
        :number="showSessionBreadcrumb ? '01' : undefined"
        :section="showSessionBreadcrumb ? 'SESSIONS' : undefined"
        :title="sessionTitle"
        :subtitle="sessionSubtitle"
      />
      <!-- Branch breadcrumb (branching-ux-polish-01KQ8TD7 WP04) -->
      <BranchBreadcrumb
        v-if="isBranchSession"
        :parent-session-id="breadcrumbParentSessionId"
        :parent-title="breadcrumbParentTitle"
        :parent-deleted="breadcrumbParentDeleted"
      />
    </div>

    <!-- main canvas -->
    <div class="flex flex-col min-h-0">
      <div
        v-if="providersLoaded && providers.length === 0 && surfaceState === 'loaded'"
        class="mx-6 my-3 rounded-md border border-signal-warn bg-surface-1 px-4 py-3 font-ui text-[12px]"
        role="status"
      >
        <div class="text-signal-warn uppercase tracking-[0.18em] text-[11px]">
          No provider configured
        </div>
        <p class="mt-1 text-ink">
          Add an LLM provider to start a conversation.
        </p>
        <button
          type="button"
          class="mt-2 px-3 py-1 rounded-md border border-accent text-accent text-[11px] uppercase tracking-[0.18em] hover:bg-surface-2"
          @click="gotoProviders"
        >
          Configure providers
        </button>
      </div>

      <div
        v-else-if="activeProviderUnsupported && surfaceState === 'loaded'"
        class="mx-6 my-3 rounded-md border border-signal-warn bg-surface-1 px-4 py-3 font-ui text-[12px]"
        role="status"
      >
        <div class="text-signal-warn uppercase tracking-[0.18em] text-[11px]">
          Provider not yet supported
        </div>
        <p class="mt-1 text-ink">
          Streaming for this provider's kind isn't wired in this build.
          Add an Anthropic provider to start chatting today.
        </p>
        <button
          type="button"
          class="mt-2 px-3 py-1 rounded-md border border-accent text-accent text-[11px] uppercase tracking-[0.18em] hover:bg-surface-2"
          @click="gotoProviders"
        >
          Configure providers
        </button>
      </div>

      <!-- ────── State A: no sessions exist anywhere ────── -->
      <div
        v-if="surfaceState === 'no-sessions'"
        class="flex-1 grid place-items-center px-6 py-12"
      >
        <div class="max-w-md text-center">
          <div
            class="mx-auto mb-6 flex h-16 w-16 items-center justify-center rounded-md border border-accent-hairline bg-surface-1 text-accent text-3xl"
            aria-hidden="true"
          >
            +
          </div>
          <h2 class="font-ui text-xl font-semibold text-ink">
            Start your first conversation
          </h2>
          <p class="mt-2 font-ui text-sm text-ink-muted">
            {{
              hasAnyProvider
                ? 'Pick a model and start chatting — sessions live locally and persist across restarts.'
                : 'Configure an LLM provider, then start chatting. Sessions live locally and persist across restarts.'
            }}
          </p>
          <div class="mt-6 flex items-center justify-center gap-2">
            <button
              v-if="!hasAnyProvider"
              type="button"
              class="rounded-md border border-accent text-accent px-4 py-2 text-xs uppercase tracking-[0.18em] font-ui hover:bg-surface-2"
              @click="gotoProviders"
            >
              Configure providers
            </button>
            <button
              v-else
              type="button"
              class="rounded-md border border-accent bg-surface-1 text-accent px-4 py-2 text-xs uppercase tracking-[0.18em] font-ui hover:bg-surface-2"
              :data-testid="'welcome-new-session'"
              @click="openNewSession"
            >
              New session
            </button>
          </div>
        </div>
      </div>

      <!-- ────── State B: sessions exist but none selected ────── -->
      <div
        v-else-if="surfaceState === 'no-selection'"
        class="flex-1 grid place-items-center px-6 py-12"
      >
        <div class="max-w-md text-center">
          <div
            class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
          >
            {{ sessionList.length }}
            {{ sessionList.length === 1 ? 'session' : 'sessions' }} ready
          </div>
          <h2 class="mt-2 font-ui text-lg font-semibold text-ink">
            Pick a session from the rail
          </h2>
          <p class="mt-1 font-ui text-sm text-ink-muted">
            Or start a fresh one — the new-session dialog lets you pick a
            different model up front.
          </p>
          <button
            type="button"
            class="mt-5 rounded-md border border-accent text-accent px-4 py-2 text-xs uppercase tracking-[0.18em] font-ui hover:bg-surface-2"
            @click="openNewSession"
          >
            New session
          </button>
        </div>
      </div>

      <!-- ────── State C: url id has no matching session ────── -->
      <div
        v-else-if="surfaceState === 'session-not-found'"
        class="flex-1 grid place-items-center px-6 py-12"
      >
        <div class="max-w-md text-center">
          <div
            class="font-ui text-[11px] uppercase tracking-[0.18em] text-signal-warn"
          >
            Gone
          </div>
          <h2 class="mt-2 font-ui text-lg font-semibold text-ink">
            This session doesn't exist
          </h2>
          <p class="mt-1 font-ui text-sm text-ink-muted">
            It may have been deleted, or the link is wrong. Pick another
            from the rail or start a new one.
          </p>
          <div class="mt-5 flex items-center justify-center gap-2">
            <button
              v-if="sessionList.length > 0"
              type="button"
              class="rounded-md border border-border-muted text-ink px-4 py-2 text-xs uppercase tracking-[0.18em] font-ui hover:bg-surface-2"
              @click="router.push('/sessions')"
            >
              Back to sessions
            </button>
            <button
              type="button"
              class="rounded-md border border-accent text-accent px-4 py-2 text-xs uppercase tracking-[0.18em] font-ui hover:bg-surface-2"
              @click="openNewSession"
            >
              New session
            </button>
          </div>
        </div>
      </div>

      <!-- ────── State D: live chat surface ────── -->
      <template v-else>
        <div
          v-if="session.streamingTimedOut.value"
          class="px-4 py-2 border-b border-signal-warn bg-surface-1 font-ui text-[12px] text-signal-warn"
          role="status"
        >
          waiting for stream… (no chunks received in 30s)
        </div>
        <div
          v-if="lastRememberError"
          class="mx-4 my-2 rounded-md border border-signal-warn bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-warn"
          role="alert"
          data-testid="remember-error-toast"
        >
          <template v-if="lastRememberError.includes('embedder unavailable')">
            <p class="font-semibold">Memory needs an embedder.</p>
            <p class="mt-0.5 text-ink-muted">
              Add a memory-capable provider to enable pin/search/retrieval.
            </p>
            <button
              type="button"
              class="mt-2 font-ui text-[11px] rounded-sm border border-signal-warn px-2 py-1 hover:bg-surface-2"
              data-testid="remember-error-open-memory-settings"
              @click="router.push('/settings')"
            >
              Open Memory settings
            </button>
          </template>
          <template v-else>
            Could not remember message: {{ lastRememberError }}
          </template>
        </div>
        <div
          class="px-4 pt-2 flex items-center gap-1 border-b border-border-muted"
          data-testid="session-tabs"
        >
          <button
            type="button"
            class="px-3 py-1 rounded-t-sm font-ui text-[11px] uppercase tracking-[0.18em] border-b-2"
            :class="
              activeTab === 'chat'
                ? 'text-accent border-accent'
                : 'text-ink-muted border-transparent hover:text-ink'
            "
            data-testid="session-tab-chat"
            @click="activeTab = 'chat'"
          >
            Chat
          </button>
          <button
            type="button"
            class="px-3 py-1 rounded-t-sm font-ui text-[11px] uppercase tracking-[0.18em] border-b-2 flex items-center gap-1"
            :class="
              activeTab === 'artifacts'
                ? 'text-accent border-accent'
                : 'text-ink-muted border-transparent hover:text-ink'
            "
            data-testid="session-tab-artifacts"
            @click="activeTab = 'artifacts'"
          >
            <span>Artifacts</span>
            <span class="text-ink-dim font-mono text-[10px]">
              ({{ sessionArtifacts.list.value.length }})
            </span>
          </button>
          <button
            type="button"
            class="px-3 py-1 rounded-t-sm font-ui text-[11px] uppercase tracking-[0.18em] border-b-2"
            :class="
              activeTab === 'context'
                ? 'text-accent border-accent'
                : 'text-ink-muted border-transparent hover:text-ink'
            "
            data-testid="session-tab-context"
            @click="activeTab = 'context'"
          >
            Context
          </button>
          <!-- Compaction overhead readout (compaction-strategy-ui WP08
               / plan §2.11). Hidden until at least one compaction call
               has run; renders the running total tagged with
               cost.kind = "compaction" alongside the session's grand
               total so users can see what fraction of their spend
               compaction is responsible for. -->
          <span
            v-if="compactionOverheadVisible"
            class="ml-auto px-3 py-1 font-ui text-[11px] tracking-[0.05em] text-ink-muted"
            data-testid="compaction-overhead-line"
          >
            Compaction overhead:
            <span class="text-ink font-mono">
              ${{ compactionOverheadUSD.toFixed(2) }}
            </span>
            of
            <span class="text-ink font-mono">
              ${{ compactionOverheadTotalUSD.toFixed(2) }}
            </span>
            total
          </span>
          <!-- Show full history toggle (compaction-strategy-ui WP07).
               Per-session UI state (NOT persisted) — flips the
               scrollback fetch between listMessagesActive (default,
               hides archived rows) and listMessagesAll (reveals them).
               Always rendered; the toggle is harmless on sessions
               with no compacted history. -->
          <button
            type="button"
            class="px-3 py-1 rounded-sm font-ui text-[11px] uppercase tracking-[0.18em] border"
            :class="[
              compactionOverheadVisible ? '' : 'ml-auto',
              session.showFullHistory.value
                ? 'border-accent text-accent bg-surface-1'
                : 'border-border-muted text-ink-muted hover:text-ink hover:bg-surface-2',
            ]"
            data-testid="show-full-history-toggle"
            :aria-pressed="session.showFullHistory.value"
            @click="session.showFullHistory.value = !session.showFullHistory.value"
          >
            {{ session.showFullHistory.value ? 'Hide archived' : 'Show full history' }}
          </button>
        </div>
        <div
          v-if="lastArtifactError"
          class="mx-4 my-2 rounded-md border border-signal-warn bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-warn"
          role="alert"
        >
          {{ lastArtifactError }}
        </div>
        <SessionHeader
          v-if="session.session.value && activeTab === 'chat'"
          :session="session.session.value"
          @title-changed="session.refresh()"
        />
        <!-- Reasoning-effort chip: shown when the active model has a reasoning
             knob (provider-implementation-uniformity-01KQ8V4F WP07). -->
        <div
          v-if="session.session.value && activeTab === 'chat' && activeReasoningStyle !== 'none'"
          class="flex justify-end px-4 py-1 border-b border-hairline"
        >
          <ReasoningControl
            :reasoning-style="activeReasoningStyle"
            :config="activeReasoningConfig"
            :session-id="sessionId"
            @config-change="(cfg) => { activeReasoningConfig = cfg ?? undefined }"
          />
        </div>
        <div
          v-if="activeTab === 'chat'"
          class="flex-1 min-h-0 grid grid-cols-[minmax(0,1fr)_auto]"
          style="grid-template-rows: minmax(0, 1fr)"
        >
          <MessageList
            :messages="visibleMessages"
            :streaming-message="session.currentlyStreaming.value"
            :waiting="isWaitingForFirstChunk"
            :error-message="session.error.value"
            :rememberable="memoryEnabled"
            :saveable="true"
            :project-id="session.session.value?.projectId ?? ''"
            :artifacts-by-message="artifactsByMessage"
            :swept-count="session.sweptCount.value"
            :archive-days="compactionArchiveDays"
            :show-token-meter="showPerMessageTokenMeter"
            @remember="onRemember"
            @save-artifact="onSaveArtifactFromMessage"
            @open-artifact="openArtifactPreview"
            @branch-from-turn="onBranchFromTurn"
            @resume="onResumeMessage"
          />
          <BranchSidebar
            v-if="hasSession"
            :parent-session-id="sessionId"
            class="hidden lg:flex"
            @open="onBranchOpen"
            @create="openCreateBranchModal"
          />
        </div>
        <div
          v-else-if="activeTab === 'artifacts'"
          class="flex-1 min-h-0 overflow-y-auto px-4 py-3"
          data-testid="session-artifacts-tab"
        >
          <p
            v-if="sessionArtifacts.list.value.length === 0"
            class="font-ui text-xs text-ink-muted"
            data-testid="session-artifacts-empty"
          >
            No artifacts yet. Code blocks with
            <code class="font-mono">title="filename.ext"</code> and tool
            outputs are captured automatically.
          </p>
          <table
            v-else
            class="w-full text-left font-ui text-xs"
            data-testid="session-artifacts-table"
          >
            <thead>
              <tr
                class="text-[10px] uppercase tracking-[0.18em] text-ink-subtle"
              >
                <th class="px-2 py-1.5 font-medium">Source</th>
                <th class="px-2 py-1.5 font-medium">Title</th>
                <th class="px-2 py-1.5 font-medium">Mime</th>
                <th class="px-2 py-1.5 font-medium">Size</th>
                <th class="px-2 py-1.5 font-medium">Created</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="a in sessionArtifacts.list.value"
                :key="a.id"
                class="cursor-pointer hover:bg-surface-2"
                :data-testid="`session-artifacts-row-${a.id}`"
                @click="openArtifactPreview(a)"
              >
                <td class="px-2 py-1.5 text-ink-dim font-mono text-[11px]">
                  {{ a.source }}
                </td>
                <td class="px-2 py-1.5 text-ink truncate max-w-[40ch]">
                  {{ a.title }}
                </td>
                <td class="px-2 py-1.5 text-ink-muted font-mono text-[11px]">
                  {{ a.mimeType }}
                </td>
                <td class="px-2 py-1.5 text-ink-muted font-mono text-[11px]">
                  {{ formatSize(a.byteSize) }}
                </td>
                <td class="px-2 py-1.5 text-ink-dim font-mono text-[11px]">
                  {{ formatTimestamp(a.createdAt) }}
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <div
          v-else-if="activeTab === 'context'"
          class="flex-1 min-h-0 overflow-y-auto"
          data-testid="session-context-tab"
        >
          <ResolvedContextPanel :session-id="sessionId" />
        </div>

        <!-- footer status bar: muted model picker + context window meter -->
        <div
          v-if="activeProvider && allChoices.length > 0"
          class="relative flex items-center justify-between gap-3 border-t border-border-muted bg-surface-0 px-4 py-1.5 font-ui text-[11px] text-ink-dim"
          data-testid="session-status-bar"
        >
          <button
            type="button"
            class="flex items-center gap-1.5 rounded-sm px-1.5 py-0.5 hover:bg-surface-2 hover:text-ink"
            :data-testid="'session-model-switcher'"
            @click="toggleSwitcher"
          >
            <span class="uppercase tracking-[0.14em] text-ink-subtle">
              model
            </span>
            <span class="font-mono text-ink-muted">
              {{ activeModelId || '—' }}
            </span>
            <span aria-hidden="true">▾</span>
          </button>
          <div
            v-if="switcherOpen"
            class="absolute z-30 bottom-full mb-1 left-3 max-h-72 w-80 overflow-y-auto rounded-sm border border-border-muted bg-surface-1 shadow-lg"
            role="menu"
          >
            <div
              class="px-3 py-1.5 text-[10px] uppercase tracking-[0.18em] text-ink-subtle border-b border-border-muted"
            >
              {{
                activeFamily ? `Same family (${activeFamily})` : 'Available'
              }}
            </div>
            <button
              v-for="c in familyChoices"
              :key="`${c.providerId}::${c.modelId}`"
              type="button"
              class="block w-full px-3 py-1.5 text-left text-sm font-ui hover:bg-surface-2"
              :class="
                c.providerId === activeProviderId &&
                c.modelId === activeModelId
                  ? 'text-accent'
                  : 'text-ink'
              "
              @click="pickModel(c.providerId, c.modelId)"
            >
              <div class="font-mono text-xs">{{ c.modelId }}</div>
              <div class="text-[10px] text-ink-dim">
                {{ c.providerName }}
              </div>
            </button>
            <div
              v-if="otherFamilyChoices.length > 0"
              class="px-3 py-1.5 text-[10px] uppercase tracking-[0.18em] text-ink-subtle border-t border-border-muted"
            >
              Other families (cross-family blocked)
            </div>
            <div
              v-for="c in otherFamilyChoices"
              :key="`disabled-${c.providerId}::${c.modelId}`"
              class="block w-full px-3 py-1.5 text-left text-sm font-ui text-ink-dim opacity-60 cursor-not-allowed"
              :title="
                `Different family (${c.family}) — would swap tokenisers ` +
                `mid-conversation. Start a new session to use this model.`
              "
            >
              <div class="font-mono text-xs">{{ c.modelId }}</div>
              <div class="text-[10px]">
                {{ c.providerName }} · {{ c.family }}
              </div>
            </div>
          </div>
          <!-- Cost pill (token-cost-telemetry-01KQ8TD7 WP04) -->
          <CostCell
            :usage="sessionUsage"
            :loading="sessionUsageLoading"
          />
          <!-- Context window meter.
               Known window (hasContextWindow): bar + pct + used/max label.
               Unknown window (!hasContextWindow): greyed label only — no bar,
               no percentage, no misleading 200k fallback. -->
          <div
            class="flex items-center gap-2"
            data-testid="session-context-meter"
            :title="hasContextWindow
              ? `Context use — ${contextNumerator.toLocaleString()} of ${contextDenominator.toLocaleString()} tokens`
              : 'Context window size unknown for this model'"
          >
            <span
              class="uppercase tracking-[0.14em]"
              :class="hasContextWindow ? 'text-ink-subtle' : 'text-ink-dim'"
            >
              context
            </span>
            <template v-if="hasContextWindow">
              <div class="h-1 w-24 rounded-full bg-surface-2 overflow-hidden">
                <div
                  class="h-full transition-[width] duration-300"
                  :class="{
                    'bg-signal-ok': contextBarTone === 'ok',
                    'bg-signal-warn': contextBarTone === 'warn',
                    'bg-signal-danger': contextBarTone === 'danger',
                  }"
                  :style="{ width: contextWindowPct + '%' }"
                ></div>
              </div>
              <span class="font-mono text-ink-muted tabular-nums">
                {{ contextWindowPct }}%
              </span>
              <span class="font-mono text-ink-subtle">
                {{ contextWindowLabel }}
              </span>
            </template>
            <span
              v-else
              class="font-mono text-ink-dim"
              data-testid="session-context-meter-unknown"
            >
              unknown
            </span>
          </div>
        </div>
        <!-- Long-session nudge banner (v0.5.6 memory-trust-signals).
             Appears once per session when message/token thresholds are crossed.
             Dismissed per-session via nudgeDismissedForSessionId. -->
        <LongSessionNudge
          v-if="nudgeVisible"
          data-testid="long-session-nudge-banner"
          @branch="onNudgeBranch"
          @new-session="onNudgeNewSession"
          @dismiss="onNudgeDismiss"
        />
        <!-- SlashArgFill panel (WP06): shown when the user picks a
             user-defined slash command that declares input args.
             Rendered above the composer so the arg-fill panel and the
             input are both visible. The panel owns its own submit/cancel
             buttons; the composer stays visible for context. -->
        <SlashArgFill
          v-if="pendingArgFill"
          :command="pendingArgFill.command"
          class="mx-4 mb-2"
          data-testid="session-slash-arg-fill"
          @submit="onArgFillSubmit"
          @cancel="dismissArgFill"
        />
        <!-- ComposerError: surfaced when a knob-policy rejection (ErrUnsupportedFeature)
             stops the last message (provider-implementation-uniformity-01KQ8V4F WP08). -->
        <ComposerError
          v-if="composerErrorText"
          :error="composerErrorText"
          class="mx-3 mb-2"
          @dismiss="onComposerErrorDismiss"
        />
        <ChatInput
          v-model="session.draft.value"
          :streaming="isStreaming"
          :queue-depth="sendQueue.length"
          :disabled="
            !activeProvider ||
            activeProviderUnsupported ||
            session.loading.value
          "
          :estimate="{ tokens: 0, usd: 0 }"
          :session-id="sessionId"
          :error-banner="session.error.value"
          :provider-kind="activeProvider?.kind ?? ''"
          :model-id="activeModelId"
          @send="onSend"
          @send-blocks="onSendBlocks"
          @cancel="onCancel"
          @slash-command="onSlashCommand"
          @bash-command="onBashCommand"
        />
      </template>
    </div>
    <NewSessionDialog
      :open="newSessionDialogOpen"
      @close="onNewSessionDialogClose"
    />
    <ConfirmToolModal />
    <!-- Plan approval modal (p0-wiring-fixes WP01) — renders when the model
         has exited plan_mode and the toolloop is awaiting user approval. -->
    <PlanApprovalModal
      v-if="pendingPlanId && pendingPlanText !== null && sessionId"
      :session-id="sessionId"
      :plan-id="pendingPlanId"
      :plan="pendingPlanText"
      @approved="onPlanApproved"
      @discarded="onPlanDiscarded"
      @edited="onPlanEdited"
    />
    <CreateBranchModal
      v-if="hasSession"
      :parent-session-id="sessionId"
      :open="createBranchModalOpen"
      @close="closeCreateBranchModal"
      @created="closeCreateBranchModal"
    />
    <MergeSuggestionToast />
    <CostThresholdToast />
    <!-- provider-keychain-rotation-01KQ8TD9 WP05 — auth failure + post-rotation toasts -->
    <AuthFailureToast :auto-resume-enabled="autoResumeOnKeyRotation" />
    <RetryAfterRotateToast />
    <!-- model-fallback-routing-01NDFSEX04 WP05 — fallback-active indicator pill -->
    <FallbackActivePill />
    <!-- WP08 — universal permission modals (one per family) -->
    <BashPermissionModal />
    <FilesystemPermissionModal />
    <CredentialPermissionModal />
    <ToolPermissionModal />
    <MigrationToast />
    <ArtifactPreview
      :open="artifactPreviewOpen"
      :payload="artifactPreviewPayload"
      :project-id="session.session.value?.projectId ?? ''"
      :on-promote="promoteArtifact"
      :on-delete="deleteArtifact"
      @close="closeArtifactPreview"
    />
  </div>
</template>
