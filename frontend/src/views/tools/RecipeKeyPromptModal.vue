<script setup lang="ts">
/**
 * RecipeKeyPromptModal — modal that prompts the user for the recipe's
 * env-key credentials AND its declared ConfigOption values, calls
 * `installRecipe(id, env, config)`, and emits `installed` on success.
 *
 * Design notes:
 *   - Shape mirrors the home-grown PinMenu pattern: no radix-vue, no
 *     teleport — a fixed-position overlay + centred panel.
 *   - Required keys carry an asterisk; submit is disabled until every
 *     required env key has a non-empty value AND every required config
 *     option resolves (non-empty for strings, ≥1 entry for
 *     directory_list, booleans always count as filled).
 *   - "Get a key →" links resolve to `EnvKey.docsUrl`; `target=_blank`
 *     + `rel="noopener"` keep browser-dev parity.
 *   - Keyboard: Esc closes; Enter on the LAST text input submits when
 *     all required fields are filled. Directory chips and inline-edits
 *     never submit on Enter (they commit the edit).
 *   - The plaintext env map and config map are passed once to
 *     `installRecipe` and not retained beyond that call (cleared on
 *     close).
 *   - ConfigOption defaults are pre-filled. directory_list defaults
 *     often carry the `${DATA_DIR}` token; we render it literally as
 *     a chip placeholder and the backend expands at install time.
 *
 * `initialConfig` lets the caller seed the form with the persisted
 * config (used by the "Edit configuration" path so the modal opens
 * with the user's current allowed_directories instead of the recipe
 * default).
 */

import { computed, nextTick, ref, watch } from 'vue';
import type { Recipe, RecipeStatus, ConfigOption, MissingPrereq } from '@/lib/types';
import DirectoryPicker from './DirectoryPicker.vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { push as pushToast } from '@/composables/useToastQueue';

// KAMEAS_GOOGLE_OAUTH_CLIENT_ID seam:
// TODO: When a Kameas-registered Google OAuth client completes restricted-
// scope verification (Gmail + CASA tier 2) and is approved by Google, the
// gmail-autoauth server can be bundled with its own client credentials and
// KAMEAS_GOOGLE_OAUTH_CLIENT_ID will be set at build time (env var / build
// override). At that point, the guided BYO-credentials flow below will be
// removed: the in-app guided section renders only when fileSetupGuide is
// present (i.e., when the env var is not set / creds not registered).
// Blocked on: Google restricted-scope review + CASA; do NOT implement until
// Google approval lands. One-line wiring: point the gmail recipe auth.kind
// to "mcp_oauth" with auth.client_id = process.env.KAMEAS_GOOGLE_OAUTH_CLIENT_ID.

const props = withDefaults(
  defineProps<{
    open: boolean;
    recipe: Recipe;
    /**
     * Caller-provided installer. Decoupled from the harness client so
     * the surrounding panel can wire in its `useToolsRecipes().install`
     * (or a test stub) without the modal owning the polling lifecycle.
     */
    install: (
      id: string,
      env: Record<string, string>,
      config: Record<string, unknown>,
    ) => Promise<RecipeStatus>;
    /**
     * Optional pre-fill for the config form. Keys outside the
     * recipe's declared ConfigOptions are ignored; missing keys fall
     * back to the option's `default`. Used by the "Edit configuration"
     * path on the filesystem row.
     */
    initialConfig?: Record<string, unknown>;
  }>(),
  {
    initialConfig: () => ({}),
  },
);

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'installed', status: RecipeStatus): void;
}>();

const client = useHarnessClient();

// ── recommended-policy install state ─────────────────────────────────
/** null → not yet attempted; true → installed (button shows "Installed"); false → available/can install */
const policyInstalled = ref<boolean | null>(null);
const policyInstalling = ref(false);

const envValues = ref<Record<string, string>>({});
const configValues = ref<Record<string, unknown>>({});
const submitting = ref(false);
const errorMsg = ref<string | null>(null);
const inputsContainer = ref<HTMLElement | null>(null);

// ── primary_auth UX ────────────────────────────────────────────────────
// Determines the leading presentation in the install modal.
//
// 'oauth'       → lead with "Sign in" OAuth button (requires auth.kind=mcp_oauth).
// 'device_code' → lead with device-code browser sign-in message; optional
//                 env keys (Azure IDs) are collapsed under "Advanced".
// 'keys'        → lead with env-key fields (standard).
// 'none'        → no credentials required; optional env keys collapse to "Advanced".
// undefined     → legacy (no reordering; render as-declared).
const primaryAuth = computed(() => props.recipe.primaryAuth);

// 'Advanced' section open/closed state. Defaults open when all env keys
// are required (so the user can see what's needed), closed when all are
// optional (device_code / oauth / none paths lead with a friendlier CTA).
const advancedOpen = ref(false);

// Whether the env-key section should be presented as secondary/optional.
// True when the primaryAuth hint signals that browser-based sign-in or
// no-credential flow is the primary path, and all env keys are optional.
const envKeysAreSecondary = computed(() => {
  const pa = primaryAuth.value;
  if (!pa || pa === 'keys') return false;
  // If any env key is required, we can't hide it under "Advanced".
  const hasRequired = props.recipe.envKeys.some((k) => k.required);
  if (hasRequired) return false;
  return pa === 'device_code' || pa === 'oauth' || pa === 'none';
});

// Prereq pre-flight — populated when the modal opens.
const missingPrereqs = ref<MissingPrereq[]>([]);
const prereqsChecked = ref(false);

// File-kind prereqs: prereqs where the user needs to place a credentials
// file. These render as guided setup sections rather than generic banners.
// Present when any MissingPrereq has kind === "file" AND fileSetupGuide.
const filePrereqs = computed(() =>
  missingPrereqs.value.filter(
    (p): p is MissingPrereq & Required<Pick<MissingPrereq, 'fileSetupGuide'>> =>
      p.kind === 'file' && p.fileSetupGuide !== undefined,
  ),
);

// Runtime-kind prereqs (binary missing from PATH). These use the existing
// generic banner. Includes any prereq without an explicit kind (backwards compat).
const runtimePrereqs = computed(() =>
  missingPrereqs.value.filter((p) => p.kind !== 'file'),
);

// Tracks whether the file placement step has been completed for each file prereq.
// Key is the prereq name; value is true once the user has placed the file.
const filePlaced = ref<Record<string, boolean>>({});

// filePicking is true while the OS file picker is open for a given prereq name.
const filePicking = ref<Record<string, boolean>>({});

/**
 * Opens the OS native file picker, copies the selected file into the
 * recipe's declared target path (via Tools_PlaceRecipeFile), then
 * re-checks prereqs to confirm placement before marking filePlaced.
 */
async function pickCredentialsFile(prereq: MissingPrereq) {
  if (!prereq.fileSetupGuide) return;
  const name = prereq.name;
  if (filePicking.value[name]) return;
  filePicking.value = { ...filePicking.value, [name]: true };
  errorMsg.value = null;
  try {
    // Open the OS file picker — the user selects the downloaded credentials
    // JSON. Filters to JSON files for discoverability.
    const pickedPath = await client.shell.pickFile(
      `Select ${name}`,
      '',
      ['*.json'],
    );
    if (!pickedPath) return; // user cancelled

    // Copy the picked file into the recipe's declared target path.
    // PlaceRecipeFile creates ~/.gmail-mcp/ (0700) if absent and writes
    // the file at mode 0600. Destination is always the recipe's declared
    // TargetPath — not caller-controlled.
    await client.tools.recipes.placeRecipeFile(props.recipe.id, pickedPath);

    // Re-run the prereq check to confirm the file is now present at the
    // expected target path. This is a belt-and-braces verification step;
    // if PlaceRecipeFile succeeded the file should be there.
    const stillMissing = await client.tools.recipes.checkPrereqs(props.recipe.id);
    const stillMissingFile = stillMissing.some(
      (p) => p.kind === 'file' && p.name === name,
    );
    if (!stillMissingFile) {
      // File is in place — switch the section to the confirmation view.
      // We keep the prereq in missingPrereqs so the guided section stays
      // visible; the v-else branch inside the section shows the confirmation.
      filePlaced.value = { ...filePlaced.value, [name]: true };
    } else {
      // Unexpected: PlaceRecipeFile succeeded but the post-check still fails.
      // Surface a clear error so the user can investigate.
      errorMsg.value = `The file was copied but the harness still reports it missing at ${prereq.fileSetupGuide.targetPath}. Please try again or check filesystem permissions.`;
    }
  } catch (e) {
    errorMsg.value = e instanceof Error ? e.message : String(e);
  } finally {
    filePicking.value = { ...filePicking.value, [name]: false };
  }
}

// OAuth sign-in (preferred over pasting a token). When the recipe declares
// auth.kind === 'mcp_oauth', the modal leads with a "Sign in" button that runs
// the browser OAuth flow; any env keys become an optional fallback.
const oauthAuth = computed(() =>
  props.recipe.auth?.kind === 'mcp_oauth' ? props.recipe.auth : null,
);
const signingIn = ref(false);

// Whether this recipe's OAuth client id is still an unresolved "${VAR}"
// token — i.e. a bring-your-own-OAuth-app recipe (ruling D-3,
// kitty-specs/connector-lifecycle-truth-01PMZ303 FR-003b): the operator
// must register their own app with the provider and paste its client id
// into the env-key field below before sign-in can work. Kameas does not
// register or host an app for these recipes. The backend substitutes the
// token once the operator supplies the key (core/rpc/views/tools/oauth.go
// resolveOAuthClientCredentials) — until then the listing still carries the
// raw literal, which is what this checks.
const isByoOAuth = computed(
  () => !!oauthAuth.value?.clientId && oauthAuth.value.clientId.includes('${'),
);

async function signIn() {
  if (signingIn.value || !oauthAuth.value) return;
  signingIn.value = true;
  errorMsg.value = null;
  try {
    const status = await client.tools.recipes.signIn(props.recipe.id);
    emit('installed', status);
    emit('close');
  } catch (e) {
    errorMsg.value = e instanceof Error ? e.message : String(e);
  } finally {
    signingIn.value = false;
  }
}

// ── Device-flow (RFC 8628) sign-in (GitHub) ────────────────────────────
// When primary_auth === 'device_code' AND auth.kind === 'mcp_oauth', the
// harness drives the device flow directly (GitHub rejects random-port loopback
// redirects so the PKCE loopback flow cannot be used). The UI:
//   1. "Start GitHub sign-in" → call beginDeviceAuth → show user_code + link
//   2. User opens github.com/login/device, enters user_code
//   3. pollDeviceAuth blocks until approved/expired/denied
//   4. On success: recipe is installed/respawned, modal closes.
//
// The ghu_ token is stored in the OS keychain server-side and NEVER returned
// via any RPC binding or frontend state.

/** True when this recipe uses the harness-driven device flow (GitHub). */
const isHarnessDeviceFlow = computed(
  () => primaryAuth.value === 'device_code' && props.recipe.auth?.kind === 'mcp_oauth',
);

interface DeviceCodeState {
  userCode: string;
  verificationUri: string;
  expiresIn: number;
  startedAt: number; // Date.now() ms
}

const deviceCode = ref<DeviceCodeState | null>(null);
const devicePolling = ref(false);
const deviceDone = ref(false);

async function startDeviceSignIn() {
  if (devicePolling.value) return;
  errorMsg.value = null;
  deviceCode.value = null;
  deviceDone.value = false;
  try {
    const result = await client.tools.recipes.beginDeviceAuth(props.recipe.id);
    deviceCode.value = {
      userCode: result.userCode,
      verificationUri: result.verificationUri,
      expiresIn: result.expiresIn,
      startedAt: Date.now(),
    };
  } catch (e) {
    errorMsg.value = e instanceof Error ? e.message : String(e);
    return;
  }

  // Auto-open the verification URL in the system browser.
  try {
    await client.shell.openInOSBrowser(deviceCode.value!.verificationUri);
  } catch {
    // Non-fatal: user can click the link manually.
  }

  // Start polling — this blocks until approved/denied/expired.
  devicePolling.value = true;
  try {
    const status = await client.tools.recipes.pollDeviceAuth(props.recipe.id);
    deviceDone.value = true;
    emit('installed', status);
    emit('close');
  } catch (e) {
    errorMsg.value = e instanceof Error ? e.message : String(e);
    deviceCode.value = null;
  } finally {
    devicePolling.value = false;
  }
}
// A recipe's `warning` renders at one of two severities. Only `danger`
// (a genuine trust hazard) shows the red banner + requires acknowledgment;
// the default (`info`) is a calm setup/latency/maturity notice that never
// blocks install. Most recipes that carry a warning are the latter.
const warningAck = ref(false);
const hasWarning = computed(() => Boolean(props.recipe.warning));
const isHazard = computed(
  () => hasWarning.value && props.recipe.warningSeverity === 'danger',
);
// A calm, informational notice — has a message but is not a hazard.
const hasNotice = computed(() => hasWarning.value && !isHazard.value);

const configOptions = computed<readonly ConfigOption[]>(
  () => props.recipe.configOptions ?? [],
);

const hasEnvSection = computed(() => props.recipe.envKeys.length > 0);
const hasConfigSection = computed(() => configOptions.value.length > 0);

function defaultForOption(opt: ConfigOption): unknown {
  if (props.initialConfig && opt.name in props.initialConfig) {
    return props.initialConfig[opt.name];
  }
  if (opt.default !== undefined) return opt.default;
  switch (opt.kind) {
    case 'directory_list':
      return [];
    case 'boolean':
      return false;
    case 'enum':
      // First choice is the safe fallback when no `default` is set;
      // the recipe author should normally declare a default explicitly.
      return opt.choices?.[0] ?? '';
    case 'string':
    default:
      return '';
  }
}

function resetForm() {
  const env: Record<string, string> = {};
  for (const k of props.recipe.envKeys) env[k.name] = '';
  envValues.value = env;

  const cfg: Record<string, unknown> = {};
  for (const opt of configOptions.value) {
    cfg[opt.name] = cloneDefault(defaultForOption(opt), opt);
  }
  configValues.value = cfg;
  submitting.value = false;
  errorMsg.value = null;
}

function cloneDefault(value: unknown, opt: ConfigOption): unknown {
  if (opt.kind === 'directory_list') {
    if (Array.isArray(value)) {
      return value.filter((v): v is string => typeof v === 'string').slice();
    }
    return [];
  }
  if (opt.kind === 'boolean') {
    return typeof value === 'boolean' ? value : false;
  }
  if (opt.kind === 'enum') {
    // Coerce to a valid choice — fall back to the first declared
    // choice if the persisted value is unknown. Empty choices yields
    // "" which the required-validator catches.
    if (typeof value === 'string' && opt.choices?.includes(value)) {
      return value;
    }
    return opt.choices?.[0] ?? '';
  }
  return typeof value === 'string' ? value : '';
}

async function installRecommendedPolicy() {
  const template = props.recipe.recommendedPolicyTemplate;
  if (!template || policyInstalling.value) return;
  policyInstalling.value = true;
  try {
    await client.cedarPolicy.installTemplate(template, template);
    policyInstalled.value = true;
    pushToast(`Policy installed: ${template}`);
  } catch (e) {
    const msg = e instanceof Error ? e.message : String(e);
    if (msg.toLowerCase().includes('already exists') || msg.toLowerCase().includes('already installed')) {
      pushToast('Policy already installed');
      policyInstalled.value = true;
    } else {
      pushToast(`Install failed: ${msg}`);
    }
  } finally {
    policyInstalling.value = false;
  }
}

watch(
  () => props.open,
  (isOpen) => {
    if (isOpen) {
      resetForm();
      warningAck.value = false;
      // Re-arm the policy-install button each time the modal opens so
      // the user can install after a delete without having to close/reopen.
      policyInstalled.value = null;
      // Reset "Advanced" section: start collapsed for device_code/none/oauth
      // (the friendly headline is the primary CTA), open for standard key paths.
      advancedOpen.value = !envKeysAreSecondary.value;
      // Prereq check — runs in background; doesn't block modal render.
      missingPrereqs.value = [];
      prereqsChecked.value = false;
      void client.tools.recipes.checkPrereqs(props.recipe.id).then((missing) => {
        missingPrereqs.value = missing;
        prereqsChecked.value = true;
      }).catch(() => {
        // Non-fatal: if the check fails, we proceed optimistically.
        prereqsChecked.value = true;
      });
      void nextTick(() => {
        const first = inputsContainer.value?.querySelector(
          'input[type="password"], input[type="text"]:not([data-testid^="dirpicker-edit"])',
        ) as HTMLInputElement | null;
        first?.focus();
      });
    } else {
      const cleared: Record<string, string> = {};
      for (const k of props.recipe.envKeys) cleared[k.name] = '';
      envValues.value = cleared;
      configValues.value = {};
      missingPrereqs.value = [];
      prereqsChecked.value = false;
    }
  },
  { immediate: true },
);

// access_tier (recipe-author-defined enum) → allowed_directories
// (recipe-author-defined directory_list) coupling. When the recipe
// declares both options, changing the tier rewrites the directory
// list to the tier's canonical paths so the user doesn't have to
// understand that the backend reads `allowed_directories`. The user
// can still edit the list afterward — we only update on tier change,
// not on every render. Sandbox tier maps to the data-dir workspace,
// project tier clears the list (user must pick), full tier expands
// to "/" by convention.
const TIER_DIR_PRESETS: Record<string, string[]> = {
  sandbox: ['${DATA_DIR}/agent-workspace'],
  project: [],
  full: ['/'],
};

watch(
  () => configValues.value.access_tier,
  (tier, prev) => {
    if (!prev || tier === prev) return;
    const dirsOpt = configOptions.value.find((o) => o.name === 'allowed_directories');
    if (!dirsOpt || dirsOpt.kind !== 'directory_list') return;
    if (typeof tier !== 'string') return;
    const preset = TIER_DIR_PRESETS[tier];
    if (preset === undefined) return;
    configValues.value = { ...configValues.value, allowed_directories: preset.slice() };
  },
);

const requiredEnvFilled = computed(() => {
  for (const k of props.recipe.envKeys) {
    if (k.required && !envValues.value[k.name]?.trim()) return false;
  }
  return true;
});

const requiredConfigFilled = computed(() => {
  for (const opt of configOptions.value) {
    if (!opt.required) continue;
    const v = configValues.value[opt.name];
    if (opt.kind === 'directory_list') {
      if (!Array.isArray(v) || v.length === 0) return false;
    } else if (opt.kind === 'string') {
      if (typeof v !== 'string' || v.trim() === '') return false;
    } else if (opt.kind === 'enum') {
      if (typeof v !== 'string' || !opt.choices?.includes(v)) return false;
    }
    // boolean: presence is implied (false is a valid filled value).
  }
  return true;
});

// allFilesPlaced is true when every file prereq has been copied into place
// (or when there are no file prereqs). This gates the Install button so the
// UX does not mislead the user into thinking they can install before creds
// are present. The backend PrereqError remains the authoritative gate.
const allFilesPlaced = computed(() =>
  filePrereqs.value.every((fp) => filePlaced.value[fp.name]),
);

const canSubmit = computed(
  () =>
    requiredEnvFilled.value &&
    requiredConfigFilled.value &&
    allFilesPlaced.value &&
    // Only a genuine hazard gates install on acknowledgment; a calm
    // notice never blocks the button.
    (!isHazard.value || warningAck.value),
);

const lastEnvName = computed(
  () =>
    props.recipe.envKeys[props.recipe.envKeys.length - 1]?.name ?? '',
);

function close() {
  if (submitting.value) return;
  emit('close');
}

function dirListDirHint(opt: ConfigOption): string | null {
  if (opt.kind !== 'directory_list') return null;
  const def = opt.default;
  if (!Array.isArray(def)) return null;
  const tokenised = def.find(
    (s): s is string => typeof s === 'string' && s.includes('${DATA_DIR}'),
  );
  if (!tokenised) return null;
  return tokenised;
}

function defaultHintFor(opt: ConfigOption): string | null {
  const tokenised = dirListDirHint(opt);
  if (tokenised) {
    return `default: workspace folder under your harness data directory (${tokenised})`;
  }
  return null;
}

/** Returns true when a string ConfigOption looks like a filesystem path field. */
function isPathStringOption(opt: ConfigOption): boolean {
  if (opt.kind !== 'string') return false;
  const n = opt.name.toLowerCase();
  const d = (opt.display ?? '').toLowerCase();
  // Heuristic: name or display contains path-like keywords.
  return (
    n.includes('path') || n.includes('file') || n.includes('dir') ||
    d.includes('path') || d.includes('file') || d.includes('directory') ||
    d.includes('folder')
  );
}

/** Opens the OS native file picker and populates the given string config option. */
async function pickFileForOption(opt: ConfigOption) {
  try {
    const picked = await client.tools.pickDirectory(opt.display || opt.name);
    if (picked) configValues.value = { ...configValues.value, [opt.name]: picked };
  } catch {
    // Ignore cancellation; the user can type the path manually.
  }
}

async function submit() {
  if (submitting.value) return;
  if (!canSubmit.value) return;
  submitting.value = true;
  errorMsg.value = null;

  // Snapshot env so the install callback receives a frozen copy
  // independent of further keystrokes.
  const env: Record<string, string> = {};
  for (const k of props.recipe.envKeys) {
    const v = envValues.value[k.name]?.trim() ?? '';
    if (v) env[k.name] = v;
  }

  // Snapshot config — only fields the recipe actually declares end
  // up in the payload. directory_list values are filtered to non-empty
  // strings; boolean / string fields are forwarded verbatim.
  const config: Record<string, unknown> = {};
  for (const opt of configOptions.value) {
    const raw = configValues.value[opt.name];
    if (opt.kind === 'directory_list') {
      if (Array.isArray(raw)) {
        const list = raw
          .filter((v): v is string => typeof v === 'string')
          .map((s) => s.trim())
          .filter((s) => s !== '');
        config[opt.name] = list;
      } else {
        config[opt.name] = [];
      }
    } else if (opt.kind === 'boolean') {
      config[opt.name] = typeof raw === 'boolean' ? raw : false;
    } else if (opt.kind === 'enum') {
      // Snapshot only valid choices; fall back to the declared default
      // (and ultimately first choice) so a tampered form value never
      // ships an out-of-set string to the backend.
      const v = typeof raw === 'string' ? raw : '';
      config[opt.name] = opt.choices?.includes(v)
        ? v
        : (typeof opt.default === 'string' ? opt.default : (opt.choices?.[0] ?? ''));
    } else {
      config[opt.name] = typeof raw === 'string' ? raw : '';
    }
  }

  try {
    const status = await props.install(props.recipe.id, env, config);
    for (const k of props.recipe.envKeys) envValues.value[k.name] = '';
    emit('installed', status);
    emit('close');
  } catch (e) {
    errorMsg.value = e instanceof Error ? e.message : String(e);
  } finally {
    submitting.value = false;
  }
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.preventDefault();
    close();
    return;
  }
  if (event.key !== 'Enter') return;
  const target = event.target as HTMLInputElement | null;
  if (!target || target.tagName !== 'INPUT') return;
  // Inline-edit chips inside DirectoryPicker handle Enter themselves —
  // they carry a `data-testid` starting with `dirpicker-edit-`. The
  // chip's @keydown handler stops propagation by calling
  // `event.preventDefault()` after committing; we still bail here as
  // a belt-and-braces guard against accidental form submission.
  const testId = target.getAttribute('data-testid') ?? '';
  if (testId.startsWith('dirpicker-edit-')) return;
  if (target.type === 'checkbox') return;
  // Only the last env-key input submits on Enter (matching the
  // pre-WP03 behaviour) — earlier inputs (env or config) let the
  // user tab forward.
  if (
    hasEnvSection.value &&
    target.name === lastEnvName.value &&
    canSubmit.value
  ) {
    event.preventDefault();
    void submit();
  }
}
</script>

<template>
  <div
    v-if="open"
    class="fixed inset-0 z-50 flex items-center justify-center"
    role="dialog"
    aria-modal="true"
    :aria-label="`Configure ${recipe.displayName}`"
    data-testid="recipe-key-prompt-modal"
    @keydown="onKeydown"
  >
    <div class="absolute inset-0 bg-modal-overlay" @click="close" />
    <div
      class="relative z-10 w-[520px] max-w-[90vw] max-h-[80vh] overflow-hidden flex flex-col rounded-md border border-border-muted bg-surface-0 shadow-lg"
    >
      <header
        class="flex items-center justify-between border-b border-border-muted px-5 py-3"
      >
        <div>
          <div
            class="text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
          >
            INSTALL TOOL
          </div>
          <h2 class="mt-1 font-ui text-base font-semibold text-ink">
            Install {{ recipe.displayName }}
          </h2>
        </div>
        <button
          type="button"
          class="text-[11px] text-ink-dim hover:text-ink"
          data-testid="recipe-key-modal-close"
          @click="close"
        >
          Close
        </button>
      </header>

      <div
        ref="inputsContainer"
        class="flex-1 overflow-y-auto px-5 py-4 space-y-5 font-ui"
      >
        <p class="text-[12px] text-ink-muted max-w-prose">
          {{ recipe.description }}
        </p>

        <!-- Prereq pre-flight banner (runtime kind): shown when required runtimes
             (uv, npx) are not installed. Surfaces an actionable message early
             rather than letting the user click Install and see a cryptic spawn
             error. Only shown for binary-in-PATH prereqs (kind != "file"). -->
        <section
          v-if="prereqsChecked && runtimePrereqs.length > 0"
          class="rounded-sm border border-signal-warn bg-signal-warn/10 px-3 py-3 space-y-1"
          role="alert"
          data-testid="recipe-modal-prereq-banner"
        >
          <div class="flex items-center gap-1.5 text-[11px] font-semibold uppercase tracking-[0.18em] text-signal-warn">
            <svg class="w-3.5 h-3.5 flex-shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
              <path d="M12 9v4M12 17h.01M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
            </svg>
            Missing runtime{{ runtimePrereqs.length > 1 ? 's' : '' }}
          </div>
          <ul class="space-y-1.5">
            <li
              v-for="p in runtimePrereqs"
              :key="p.name"
              class="text-[12px] text-ink leading-snug"
              :data-testid="`recipe-modal-prereq-${p.name}`"
            >
              <span class="font-semibold">{{ p.name }}</span>
              <span class="text-ink-muted"> — {{ p.installHint }}</span>
            </li>
          </ul>
        </section>

        <!-- File prereq guided-setup section (kind === "file"): shown when a
             required credentials file is absent from the filesystem. Replaces
             the raw "stdio: read initialize: EOF" error surface with an in-app
             guided setup flow that:
               1. Lists the concrete steps to create the credentials file.
               2. Provides a native file picker to place it at the target path.
               3. Notes what happens after the file is placed.
             One section is rendered per file prereq (typically just one). -->
        <section
          v-for="fp in filePrereqs"
          :key="fp.name"
          class="rounded-sm border border-accent-hairline bg-accent-glow/20 px-3 py-3 space-y-3"
          role="region"
          :aria-label="`Setup required: ${fp.name}`"
          :data-testid="`recipe-modal-file-prereq-${fp.name}`"
        >
          <div class="flex items-center gap-1.5 text-[11px] font-semibold uppercase tracking-[0.18em] text-ink-subtle">
            <svg class="w-3.5 h-3.5 flex-shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
              <path d="M12 2L2 7l10 5 10-5-10-5zM2 17l10 5 10-5M2 12l10 5 10-5" />
            </svg>
            Setup required: {{ fp.name }}
          </div>

          <!-- Steps list -->
          <div class="space-y-2">
            <p class="text-[11px] text-ink-muted leading-snug">
              Complete these steps to create your OAuth credentials:
            </p>
            <ol class="space-y-1.5 list-none" data-testid="recipe-modal-file-prereq-steps">
              <li
                v-for="(step, i) in fp.fileSetupGuide.steps"
                :key="i"
                class="flex items-start gap-2 text-[12px] text-ink leading-snug"
                :data-testid="`recipe-modal-file-prereq-step-${i}`"
              >
                <span class="flex-shrink-0 w-4 h-4 rounded-full bg-accent-glow border border-accent-hairline text-[9px] font-mono flex items-center justify-center text-accent mt-0.5">
                  {{ i + 1 }}
                </span>
                <span>{{ step }}</span>
              </li>
            </ol>
          </div>

          <!-- File placement: target path + file picker -->
          <div
            v-if="!filePlaced[fp.name]"
            class="space-y-2"
            data-testid="recipe-modal-file-prereq-picker"
          >
            <p class="text-[11px] text-ink-muted leading-snug">
              Select the downloaded credentials JSON using the button below.
              The file will be used from:
              <code class="font-mono text-[11px] text-ink bg-surface-1 px-1 rounded-sm break-all" data-testid="recipe-modal-file-prereq-target-path">
                {{ fp.fileSetupGuide.targetPath }}
              </code>
            </p>
            <button
              type="button"
              class="rounded-sm border border-accent-hairline bg-surface-0 px-3 py-1.5 font-ui text-[12px] text-accent hover:bg-accent-glow disabled:opacity-50 disabled:cursor-not-allowed"
              :disabled="filePicking[fp.name]"
              data-testid="recipe-modal-file-prereq-pick-btn"
              @click="pickCredentialsFile(fp)"
            >
              {{ filePicking[fp.name] ? 'Opening file picker…' : 'Select credentials file…' }}
            </button>
          </div>

          <!-- Placed confirmation -->
          <div
            v-else
            class="flex items-center gap-1.5 text-[12px] text-signal-ok"
            data-testid="recipe-modal-file-prereq-placed"
          >
            <svg class="w-3.5 h-3.5 flex-shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
              <path d="M20 6 9 17l-5-5" />
            </svg>
            Credentials file detected — you can now click Install.
          </div>

          <!-- What happens next note -->
          <p class="text-[11px] text-ink-subtle leading-snug max-w-prose">
            After install, the server will open a browser sign-in once to authorise
            Gmail access. The token is saved locally and does not need to be
            re-entered.
          </p>

          <!-- Docs link -->
          <a
            v-if="fp.fileSetupGuide.docsUrl"
            :href="fp.fileSetupGuide.docsUrl"
            target="_blank"
            rel="noopener noreferrer"
            class="inline-block text-[11px] text-accent hover:text-accent-muted"
            data-testid="recipe-modal-file-prereq-docs-link"
          >
            Google Cloud setup guide →
          </a>
        </section>

        <!-- Device-code sign-in banner — two variants:
             1. Harness-driven (GitHub): harness calls the device-auth endpoint,
                shows user_code + link, and polls until done.
             2. Server-driven (e.g. Outlook/MS365): the MCP server itself handles
                the flow; the user just clicks Install and the server opens the
                browser. -->

        <!-- Harness-driven GitHub device flow (primary_auth=device_code + auth.kind=mcp_oauth) -->
        <section
          v-if="isHarnessDeviceFlow"
          class="rounded-sm border border-accent-hairline bg-accent-glow/30 px-3 py-3 space-y-3"
          data-testid="recipe-modal-device-code-section"
        >
          <div class="text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
            Sign in to {{ recipe.displayName }}
          </div>

          <!-- Before device auth starts -->
          <template v-if="!deviceCode && !deviceDone">
            <p class="text-[12px] text-ink-muted leading-snug max-w-prose">
              Click below to start GitHub sign-in. You will see a short code to
              enter at <span class="font-mono text-ink">github.com/login/device</span>.
              No token to paste — just sign in once and the session is saved.
            </p>
            <button
              type="button"
              class="rounded-sm border border-accent-hairline bg-surface-0 px-3 py-1.5 font-ui text-[12px] text-accent hover:bg-accent-glow disabled:opacity-50 disabled:cursor-not-allowed"
              :disabled="devicePolling"
              data-testid="recipe-modal-device-start-btn"
              @click="startDeviceSignIn"
            >
              Start GitHub sign-in
            </button>
          </template>

          <!-- After beginDeviceAuth: show user_code + link; polling in progress -->
          <template v-if="deviceCode && !deviceDone">
            <p class="text-[12px] text-ink-muted leading-snug">
              Open <a
                :href="deviceCode.verificationUri"
                target="_blank"
                rel="noopener noreferrer"
                class="text-accent underline hover:text-accent-muted"
                data-testid="recipe-modal-device-verify-link"
              >{{ deviceCode.verificationUri }}</a>
              in your browser and enter this code:
            </p>
            <div
              class="inline-flex items-center justify-center rounded-sm border border-accent-hairline bg-surface-1 px-4 py-2"
              data-testid="recipe-modal-device-user-code"
            >
              <span class="font-mono text-xl font-bold tracking-widest text-ink select-all">
                {{ deviceCode.userCode }}
              </span>
            </div>
            <p
              v-if="devicePolling"
              class="text-[11px] text-ink-subtle leading-snug"
              data-testid="recipe-modal-device-polling"
            >
              Waiting for you to approve in GitHub…
            </p>
          </template>

          <!-- Done (success handled by closing modal; this is a brief flash) -->
          <template v-if="deviceDone">
            <p class="text-[12px] text-signal-ok">Signed in. Installing…</p>
          </template>
        </section>

        <!-- Server-driven device flow (e.g. Outlook ms-365-mcp-server).
             The MCP server handles the flow itself; just click Install. -->
        <section
          v-if="primaryAuth === 'device_code' && !isHarnessDeviceFlow"
          class="rounded-sm border border-accent-hairline bg-accent-glow/30 px-3 py-3 space-y-2"
          data-testid="recipe-modal-device-code-section"
        >
          <div class="text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
            Sign in via browser
          </div>
          <p class="text-[12px] text-ink-muted leading-snug max-w-prose">
            When you click Install, the server will open your browser and ask
            you to sign in (device code flow). No token to paste — just sign in
            once and the session is remembered automatically.
          </p>
          <p class="text-[12px] text-ink-subtle leading-snug max-w-prose">
            Personal accounts work without any extra config. For work or school
            accounts you may need to expand "Advanced" below to set your Azure
            app ID.
          </p>
        </section>

        <!-- OAuth sign-in (preferred). Leads the modal for mcp_oauth recipes;
             env keys below become an optional fallback. -->
        <section
          v-if="oauthAuth"
          class="rounded-sm border border-accent-hairline bg-accent-glow/30 px-3 py-3 space-y-2"
          data-testid="recipe-modal-oauth-section"
        >
          <div class="text-[11px] uppercase tracking-[0.18em] text-ink-subtle">
            Sign in
          </div>
          <p v-if="!isByoOAuth" class="text-[12px] text-ink-muted leading-snug max-w-prose">
            Sign in with your account in the browser — no token to paste. The
            harness stores the credential securely and refreshes it
            automatically.
          </p>
          <!-- Bring-your-own OAuth app (ruling D-3, FR-003b): the operator
               registers their own app with the provider and pastes its
               client id into the env-key field below (stored in the OS
               keychain, not an environment variable). Kameas does not
               register or host this app. -->
          <p
            v-else
            class="text-[12px] text-ink-muted leading-snug max-w-prose"
            data-testid="recipe-modal-byo-oauth-notice"
          >
            This connector needs your own OAuth app —
            <a
              v-if="recipe.docsUrl"
              :href="recipe.docsUrl"
              target="_blank"
              rel="noopener"
              class="underline hover:text-ink"
              data-testid="recipe-modal-byo-oauth-link"
              >register one with {{ recipe.displayName }}</a
            ><span v-else>register one with {{ recipe.displayName }}</span>
            and add its client ID below. Kameas does not register or host
            this app for you. Once the client ID is set, sign in with your
            account in the browser.
          </p>
          <!-- TODO: this recipe's Auth.Kind is mcp_oauth, but not every
               primary_auth arm has a working sign-in path yet — the
               browser_oauth_dcr arm (dynamic client registration) and the
               bare oauth arm's four non-Google recipes are still unbuilt as
               of this commit (kitty-specs/connector-lifecycle-truth-01PMZ303
               UNIT-3, not yet landed). The button renders enabled
               regardless of arm; clicking it for those recipes still
               surfaces oauth.go's "has no OAuth client_id configured"
               error rather than failing closed in the UI. See spec.md
               FR-002 / AC-002 for the render-guard fix. -->
          <button
            type="button"
            class="rounded-sm border border-accent-hairline bg-surface-0 px-3 py-1.5 font-ui text-[12px] text-accent hover:bg-accent-glow disabled:opacity-50 disabled:cursor-not-allowed"
            :disabled="signingIn"
            data-testid="recipe-modal-signin-btn"
            @click="signIn"
          >
            {{ signingIn ? 'Waiting for browser…' : `Sign in to ${recipe.displayName}` }}
          </button>
        </section>

        <!-- Recommended policy install (cedar-policy-editor-ui-01KQ8TD6 WP03) -->
        <!-- Shown when the recipe ships with a recommended Cedar policy template,
             independent of the hazard-warning section so non-hazard recipes
             (e.g. filesystem with access_tier=sandbox) also surface the button. -->
        <div
          v-if="recipe.recommendedPolicyTemplate"
          class="flex items-center gap-2 flex-wrap rounded-sm border border-border-muted bg-surface-1 px-3 py-2"
          data-testid="recipe-modal-policy-pointer"
        >
          <span class="text-[11px] text-ink-muted">
            Recommended Cedar policy:
            <code class="font-mono text-[11px] text-ink">{{ recipe.recommendedPolicyTemplate }}</code>
          </span>
          <button
            v-if="policyInstalled !== true"
            type="button"
            class="rounded-sm border border-accent-hairline bg-surface-0 px-2 py-0.5 text-[11px] text-accent hover:bg-accent-glow disabled:opacity-50 disabled:cursor-not-allowed"
            :disabled="policyInstalling"
            data-testid="recipe-modal-install-policy-btn"
            @click="installRecommendedPolicy"
          >
            {{ policyInstalling ? 'Installing…' : 'Install recommended policy' }}
          </button>
          <span
            v-else
            class="text-[11px] text-signal-ok"
            data-testid="recipe-modal-policy-installed-badge"
          >
            Installed
          </span>
        </div>

        <!-- Calm notice (default severity): a setup/latency/maturity note.
             No red, no acknowledgment — never blocks install. -->
        <section
          v-if="hasNotice"
          class="rounded-sm border border-border-muted bg-surface-2 px-3 py-2.5 flex items-start gap-2"
          data-testid="recipe-modal-notice"
        >
          <svg class="w-4 h-4 mt-0.5 shrink-0 text-ink-dim" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <circle cx="12" cy="12" r="10" />
            <path d="M12 16v-4M12 8h.01" />
          </svg>
          <p
            class="text-[12px] text-ink-muted leading-snug max-w-prose"
            data-testid="recipe-modal-notice-text"
          >
            {{ recipe.warning }}
          </p>
        </section>

        <!-- Hazard banner (only genuinely dangerous recipes, warning_severity=danger) -->
        <section
          v-if="isHazard"
          class="rounded-sm border-2 border-signal-danger bg-signal-danger/10 px-3 py-3 space-y-2"
          role="alert"
          data-testid="recipe-modal-warning"
        >
          <div
            class="flex items-center gap-2 text-[11px] uppercase tracking-[0.18em] font-semibold text-signal-danger"
          >
            <svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
              <path d="M12 9v4M12 17h.01M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
            </svg>
            Hazard
          </div>
          <p
            class="text-[12px] text-signal-danger leading-snug max-w-prose"
            data-testid="recipe-modal-warning-text"
          >
            {{ recipe.warning }}
          </p>
          <label
            class="inline-flex items-start gap-2 cursor-pointer select-none mt-1"
            data-testid="recipe-modal-warning-ack-label"
          >
            <input
              v-model="warningAck"
              type="checkbox"
              class="accent-signal-danger w-4 h-4 mt-0.5"
              data-testid="recipe-modal-warning-ack"
            />
            <span class="font-ui text-[12px] text-ink leading-snug">
              I understand the risk and accept that the model will have the access described above.
            </span>
          </label>
        </section>

        <!-- API Keys section.
             When envKeysAreSecondary (device_code / oauth / none primary_auth),
             the keys are all optional; they're presented under an "Advanced"
             disclosure so the primary CTA ("just install / sign in") stays clean.
             When not secondary, keys are shown directly as before. -->
        <section
          v-if="hasEnvSection && !envKeysAreSecondary"
          class="space-y-3"
          data-testid="recipe-modal-env-section"
        >
          <h3
            class="text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
          >
            API Keys
          </h3>
          <div
            v-for="key in recipe.envKeys"
            :key="key.name"
            class="space-y-1"
          >
            <label
              :for="`recipe-key-${recipe.id}-${key.name}`"
              class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
            >
              <span>{{ key.display }}</span>
              <span
                v-if="key.required"
                class="ml-1 text-signal-warn"
                aria-label="required"
              >*</span>
            </label>
            <input
              :id="`recipe-key-${recipe.id}-${key.name}`"
              v-model="envValues[key.name]"
              :name="key.name"
              type="password"
              autocomplete="off"
              spellcheck="false"
              class="w-full rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 font-mono text-sm text-ink focus:border-accent focus:outline-none"
              :data-testid="`recipe-key-input-${key.name}`"
            />
            <a
              v-if="key.docsUrl"
              :href="key.docsUrl"
              target="_blank"
              rel="noopener"
              class="inline-block text-[11px] text-accent hover:text-accent-muted"
              :data-testid="`recipe-key-docs-${key.name}`"
            >
              Get a key →
            </a>
          </div>
        </section>

        <!-- Advanced collapse: optional env keys for device_code / oauth / none
             recipes. Personal accounts typically don't need these; work/school
             accounts may. The section starts collapsed. -->
        <details
          v-if="hasEnvSection && envKeysAreSecondary"
          :open="advancedOpen"
          class="rounded-sm border border-border-muted"
          data-testid="recipe-modal-advanced-section"
          @toggle="advancedOpen = ($event.target as HTMLDetailsElement).open"
        >
          <summary
            class="flex items-center gap-2 cursor-pointer select-none px-3 py-2 text-[11px] uppercase tracking-[0.18em] text-ink-subtle hover:text-ink list-none"
            data-testid="recipe-modal-advanced-toggle"
          >
            <svg
              class="w-3 h-3 flex-shrink-0 transition-transform"
              :class="{ 'rotate-90': advancedOpen }"
              viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"
            >
              <path d="m9 18 6-6-6-6" />
            </svg>
            Advanced
          </summary>
          <div class="px-3 pb-3 pt-1 space-y-3 border-t border-border-muted">
            <p class="text-[11px] text-ink-muted leading-snug max-w-prose">
              Optional — only needed for work or school accounts, or custom Azure app registrations.
            </p>
            <div
              v-for="key in recipe.envKeys"
              :key="key.name"
              class="space-y-1"
            >
              <label
                :for="`recipe-key-adv-${recipe.id}-${key.name}`"
                class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
              >
                <span>{{ key.display }}</span>
              </label>
              <input
                :id="`recipe-key-adv-${recipe.id}-${key.name}`"
                v-model="envValues[key.name]"
                :name="key.name"
                type="password"
                autocomplete="off"
                spellcheck="false"
                class="w-full rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 font-mono text-sm text-ink focus:border-accent focus:outline-none"
                :data-testid="`recipe-key-input-${key.name}`"
              />
              <a
                v-if="key.docsUrl"
                :href="key.docsUrl"
                target="_blank"
                rel="noopener"
                class="inline-block text-[11px] text-accent hover:text-accent-muted"
                :data-testid="`recipe-key-docs-${key.name}`"
              >
                Learn more →
              </a>
            </div>
          </div>
        </details>

        <!-- Configuration section -->
        <section
          v-if="hasConfigSection"
          class="space-y-3"
          data-testid="recipe-modal-config-section"
        >
          <h3
            class="text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
          >
            Configuration
          </h3>
          <div
            v-for="opt in configOptions"
            :key="opt.name"
            class="space-y-1"
            :data-testid="`recipe-config-row-${opt.name}`"
          >
            <label
              class="block text-[11px] uppercase tracking-[0.18em] text-ink-subtle"
            >
              <span>{{ opt.display }}</span>
              <span
                v-if="opt.required"
                class="ml-1 text-signal-warn"
                aria-label="required"
              >*</span>
            </label>
            <p
              v-if="opt.description"
              class="text-[11px] text-ink-muted max-w-prose"
              :data-testid="`recipe-config-desc-${opt.name}`"
            >
              {{ opt.description }}
            </p>
            <DirectoryPicker
              v-if="opt.kind === 'directory_list'"
              :model-value="(configValues[opt.name] as string[]) ?? []"
              :input-id="opt.name"
              @update:model-value="(v: string[]) => configValues[opt.name] = v"
            />
            <!-- String option: for path-like fields, show a native folder-picker
                 button beside the text input so the user doesn't have to type
                 an absolute path manually. The button opens the OS dialog via
                 the Tools_PickDirectory Wails binding. -->
            <div
              v-else-if="opt.kind === 'string'"
              class="flex items-center gap-1.5"
            >
              <input
                v-model="configValues[opt.name] as string"
                type="text"
                spellcheck="false"
                autocomplete="off"
                :aria-label="`${opt.display || opt.name} value`"
                class="flex-1 min-w-0 rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 font-mono text-sm text-ink focus:border-accent focus:outline-none"
                :data-testid="`recipe-config-string-${opt.name}`"
              />
              <button
                v-if="isPathStringOption(opt)"
                type="button"
                class="flex-shrink-0 rounded-sm border border-border-muted bg-surface-1 px-2 py-1.5 text-[11px] text-ink-dim hover:text-ink hover:border-accent"
                :aria-label="`Pick ${opt.display || opt.name} using file browser`"
                :data-testid="`recipe-config-string-picker-${opt.name}`"
                @click="pickFileForOption(opt)"
              >
                Browse…
              </button>
            </div>
            <select
              v-else-if="opt.kind === 'enum'"
              v-model="configValues[opt.name] as string"
              :aria-label="`${opt.display || opt.name} option`"
              class="w-full rounded-sm border border-border-muted bg-surface-1 px-2.5 py-1.5 font-mono text-sm text-ink focus:border-accent focus:outline-none"
              :data-testid="`recipe-config-enum-${opt.name}`"
            >
              <option v-for="choice in opt.choices ?? []" :key="choice" :value="choice">
                {{ choice }}
              </option>
            </select>
            <label
              v-else-if="opt.kind === 'boolean'"
              class="inline-flex items-center gap-2 cursor-pointer select-none"
            >
              <input
                v-model="configValues[opt.name] as boolean"
                type="checkbox"
                class="accent-accent w-4 h-4"
                :data-testid="`recipe-config-bool-${opt.name}`"
              />
              <span class="font-ui text-[12px] text-ink">{{ opt.display }}</span>
            </label>
            <p
              v-if="defaultHintFor(opt)"
              class="text-[11px] text-ink-subtle"
              :data-testid="`recipe-config-hint-${opt.name}`"
            >
              {{ defaultHintFor(opt) }}
            </p>
          </div>
        </section>

        <div
          v-if="errorMsg"
          class="rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 text-[11px] text-signal-danger"
          role="alert"
          data-testid="recipe-key-modal-error"
        >
          {{ errorMsg }}
        </div>
      </div>

      <footer
        class="flex items-center justify-end gap-2 border-t border-border-muted px-5 py-3"
      >
        <button
          type="button"
          class="rounded-sm border border-border-muted px-3 py-1 text-[12px] text-ink-dim hover:text-ink"
          :disabled="submitting"
          @click="close"
        >
          Cancel
        </button>
        <button
          type="button"
          :class="[
            'rounded-sm px-3 py-1 text-[12px] disabled:opacity-50 disabled:cursor-not-allowed',
            isHazard
              ? 'border border-signal-danger bg-signal-danger/10 text-signal-danger hover:bg-signal-danger/20'
              : 'border border-accent-hairline bg-surface-1 text-accent hover:bg-accent-glow',
          ]"
          :disabled="!canSubmit || submitting"
          data-testid="recipe-key-modal-submit"
          @click="submit"
        >
          {{ submitting ? 'Installing…' : isHazard ? 'Install with risk' : 'Install' }}
        </button>
      </footer>
    </div>
  </div>
</template>
