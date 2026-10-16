/**
 * settingsIssues — the issue model behind SettingsIssuesBanner
 * (settings-cleanup-01SETUX01 WP01, FR-3 / FR-4).
 *
 * Owner ruling (2026-10-09): problems reach the user as a banner at the top
 * of every Settings page, shown only while an issue exists. Each issue
 * either offers a fix button (the fix is automated) or plain instructions
 * plus "Copy diagnostics" (it is not).
 *
 * The model is generic on purpose. An issue comes from a provider; the
 * banner asks every provider for its current issues and renders whatever
 * comes back. Migration drift was the first provider; Fleet's pending
 * approvals (ml-producer-01MLPRD01 WP06) the second; settings sync waiting
 * for its one-time confirmation (provider-credentials-client-01PCCLI01 A2)
 * the third. A later issue (an MCP
 * server stuck in an error state, say) plugs in by adding a provider to
 * SETTINGS_ISSUE_PROVIDERS — the banner does not change.
 *
 * Copy rule: `title`, `body`, `instructions` and `fix.label` are what the
 * user reads, so they use plain language — no "ledger", "migration",
 * version ids or internal names. `diagnostics()` output is for support and
 * may contain all of those.
 */
import type { HarnessClient, LogRow } from '@/lib/harnessClient';
import type { DriftEntry, DriftReport } from '@/lib/types';
import { deferredUntil, formatAcceptByDate } from '@/lib/approvalDeferral';
import { openPendingApprovalsDialog } from '@/lib/pendingApprovalsDialog';

export interface SettingsIssueFix {
  /** Button label, e.g. "Repair". */
  label: string;
  /**
   * Applies the fix. Resolves when the fix landed; the banner then re-runs
   * every provider, so a fixed issue disappears on its own. Rejects with a
   * user-presentable Error when it did not.
   */
  run: () => Promise<void>;
}

export interface SettingsIssue {
  /** Stable id, unique across providers. */
  id: string;
  severity: 'error' | 'warning';
  title: string;
  body: string;
  /**
   * Automated fix. When present, `instructions` and `diagnostics` are the
   * fallback: the banner shows them only after `fix.run()` fails. When
   * absent, they are shown straight away.
   */
  fix?: SettingsIssueFix;
  /** Ordered manual steps, in plain language. */
  instructions?: string[];
  /** Builds the text the "Copy diagnostics" button puts on the clipboard. */
  diagnostics?: () => Promise<string>;
  /**
   * A secondary path, shown beside the fix: an in-app route (hash-router
   * path, e.g. "/settings?tab=sync") where the user can make a different
   * choice than the fix makes for them.
   */
  link?: { label: string; to: string };
}

export interface SettingsIssueProvider {
  id: string;
  /**
   * True when the provider's RPCs have no served-mode dispatch
   * (core/serve/methods.go). The banner skips it in a served build rather
   * than calling a method that can only fail.
   */
  desktopOnly: boolean;
  collect: (client: HarnessClient) => Promise<SettingsIssue[]>;
}

// ── shared diagnostics ──────────────────────────────────────────────────

/** How many recent warn+error runtime log lines diagnostics carry. */
const DIAGNOSTIC_LOG_LINES = 200;

function errorText(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

/**
 * The deployment environment name (dev / stage / prod / local) — the
 * `<env>` segment of the data folder. Settings_FleetProfile resolves it via
 * fleet.ResolveProfile, which reads KENAZ_HARNESS_ENV exactly as
 * core/paths.EnvName does. It errors when fleet is disabled; callers then
 * fall back to describing the folder without the env segment.
 */
async function envName(client: HarnessClient): Promise<string | null> {
  try {
    const p = await client.settings.fleetProfile();
    return p.name ? p.name : null;
  } catch {
    return null;
  }
}

/**
 * Where the user's data lives (core/paths.DataDir: ~/.kenaz/harness/<env>).
 * No binding returns the resolved directory itself, so this is assembled
 * from the env name; see the WP01 report.
 */
function dataFolder(env: string | null): string {
  return env ? `~/.kenaz/harness/${env}` : '~/.kenaz/harness';
}

function formatLogRow(r: LogRow): string {
  return `${r.timestamp} ${r.level.toUpperCase()} [${r.source}] ${r.message}`;
}

/**
 * Diagnostics text (FR-4): app version and env, the given report as JSON,
 * and the last DIAGNOSTIC_LOG_LINES warn+error runtime log lines.
 *
 * Privacy: no credentials and no user content. AppInfo carries build
 * metadata only. The runtime log ring (core/logstore) stores each slog
 * record's message, not its attrs, and runs every message through
 * RedactMessage on Append; the slog call sites feeding it are covered by
 * check-no-user-content-in-slog.sh.
 */
async function buildDiagnostics(
  client: HarnessClient,
  reportTitle: string,
  report: unknown,
): Promise<string> {
  const lines: string[] = ['Kenaz diagnostics', `Generated: ${new Date().toISOString()}`];

  try {
    const info = await client.appInfo();
    lines.push(`App version: ${info.build} (commit ${info.commit})`);
    lines.push(`Platform: ${info.platform || 'unknown'}`);
  } catch (e) {
    lines.push(`App version: unavailable (${errorText(e)})`);
  }
  lines.push(`Environment: ${(await envName(client)) ?? 'unknown'}`);

  lines.push('', `== ${reportTitle} ==`, JSON.stringify(report, null, 2));

  lines.push('', `== Recent warnings and errors (newest first, up to ${DIAGNOSTIC_LOG_LINES}) ==`);
  try {
    const rows = await client.runtimeLogs.tail({ level: 'warn', limit: DIAGNOSTIC_LOG_LINES });
    if (rows.length === 0) lines.push('(none)');
    for (const r of rows) lines.push(formatLogRow(r));
  } catch (e) {
    lines.push(`(unavailable: ${errorText(e)})`);
  }

  return lines.join('\n') + '\n';
}

function manualSteps(env: string | null): string[] {
  return [
    'Click “Copy diagnostics” below and paste the text into an email or a note.',
    'Quit Kenaz.',
    `Make a backup copy of your Kenaz data folder, ${dataFolder(env)} (the .kenaz folder is in your home folder). Keep the copy somewhere safe and don’t change anything inside the original.`,
    'Send the diagnostics to Kenaz support, and wait for their reply before deleting or reinstalling anything.',
  ];
}

// ── provider: database (migration drift) ────────────────────────────────

/**
 * Migration drift, formerly Settings › Health's MigrationDriftPanel.
 *   id_mismatch (error)   → one repairable issue; Repair applies
 *                           Storage_ApplyDriftFix to every entry (the
 *                           backend backs up data.db first).
 *   ledger_only (warning) → one manual issue; nothing here mutates.
 *   code_only (info)      → not shown: a pending migration that applies on
 *                           the next boot is not a problem.
 * Storage_* has no served dispatch, hence desktopOnly.
 */
const databaseIssues: SettingsIssueProvider = {
  id: 'database',
  desktopOnly: true,
  async collect(client) {
    const report: DriftReport = await client.storage.getMigrationDriftReport();
    const drifts = report?.drifts ?? [];
    const repairable = drifts.filter((d: DriftEntry) => d.kind === 'id_mismatch');
    const manual = drifts.filter((d: DriftEntry) => d.kind === 'ledger_only');
    if (repairable.length === 0 && manual.length === 0) return [];

    const env = await envName(client);
    const diagnostics = () => buildDiagnostics(client, 'Database check (migration drift report)', report);
    const issues: SettingsIssue[] = [];

    if (repairable.length > 0) {
      issues.push({
        id: 'database-repairable',
        severity: 'error',
        title: 'Kenaz found a database inconsistency it can repair.',
        body: 'Some features may not work correctly until it is fixed. Repair backs up your data first, then fixes the problem.',
        fix: {
          label: 'Repair',
          run: async () => {
            for (const d of repairable) {
              await client.storage.applyDriftFix(d.version);
            }
          },
        },
        instructions: manualSteps(env),
        diagnostics,
      });
    }

    if (manual.length > 0) {
      issues.push({
        id: 'database-needs-support',
        severity: 'warning',
        title: 'Kenaz found a database problem it can’t repair on its own.',
        body: 'Your data includes changes this copy of Kenaz doesn’t recognize, usually left behind by a newer or test build. Kenaz keeps working, but please follow these steps so nothing is lost.',
        instructions: manualSteps(env),
        diagnostics,
      });
    }

    return issues;
  },
};

// ── provider: Fleet pending approvals (ml-producer-01MLPRD01 WP06) ───────

/**
 * "N items need your approval" — Fleet's pending-approvals hub (legal
 * re-acceptance, the hosted-inference notice, informational changes).
 * Shown only when signed in and something is pending. Severity:
 *   error   — a required item is blocking now (something is paused);
 *   warning — every required item is still deferrable ("Accept by <date>",
 *             the earliest deferral), or everything is informational.
 * A deferral that has passed counts as blocking now, so the banner turns to
 * an error on its own once the date goes by. The harness itself pauses
 * nothing on a legal item; Fleet enforces it.
 * The fix opens the step-through modal (PendingApprovalsModal, mounted by
 * the banner) and resolves when it closes, so the banner re-reads the hub.
 * Closing approves nothing. Fleet_* hub bindings have no served dispatch,
 * hence desktopOnly.
 */
const pendingApprovalsIssues: SettingsIssueProvider = {
  id: 'pending-approvals',
  desktopOnly: true,
  async collect(client) {
    const v = await client.fleet.pendingApprovals();
    const n = v.items?.length ?? 0;
    if (!v.signedIn || n === 0) return [];
    const now = new Date();
    const required = v.items.filter((it) => it.required);
    const deferrals = required.map((it) => deferredUntil(it, now));
    const blockingNow = deferrals.some((d) => d === null);
    const title = n === 1 ? '1 item needs your approval' : `${n} items need your approval`;
    const review = { label: 'Review', run: () => openPendingApprovalsDialog() };
    if (required.length > 0 && blockingNow) {
      return [
        {
          id: 'pending-approvals',
          severity: 'error',
          title,
          body: 'Some things are paused until you review and approve them.',
          fix: review,
        },
      ];
    }
    if (required.length > 0) {
      const earliest = new Date(Math.min(...deferrals.map((d) => (d as Date).getTime())));
      return [
        {
          id: 'pending-approvals',
          severity: 'warning',
          title,
          body: `Accept by ${formatAcceptByDate(earliest)} to keep using Fleet. Nothing is paused yet.`,
          fix: review,
        },
      ];
    }
    return [
      {
        id: 'pending-approvals',
        severity: 'warning',
        title,
        body: 'Your organization changed something you may want to review. Nothing is paused.',
        fix: review,
      },
    ];
  },
};

// ── provider: settings sync waiting for confirmation (01PCCLI01 A2, H1) ──

/** Plain-language names for the sync categories, as the Sync panel shows them. */
const SYNC_CATEGORY_NAMES: Record<string, string> = {
  provider_profiles: 'provider profiles',
  model_prefs: 'model preferences',
  mcp_recipes: 'MCP recipes',
  installed_mcp: 'installed MCP servers',
  ui_theme: 'UI theme',
  slash_commands: 'slash commands',
};

function syncCategoryName(id: string): string {
  return SYNC_CATEGORY_NAMES[id] ?? id.split('_').filter(Boolean).join(' ');
}

/**
 * Owner ruling H1 (provider-credentials-client-01PCCLI01 §7a): settings sync
 * never reached Fleet before the A2 wire fix, so a category the user turned
 * on had never left the machine. Before the first upload, ask once. The
 * backend holds every upload until Sync_ConfirmUploads saves the answer
 * (Settings.SettingsSyncUploadsConfirmed); Sync_Status marks the held rows
 * `awaiting_confirmation`. Downloads from Fleet are not held.
 * Sync_* has no served dispatch, hence desktopOnly.
 */
const settingsSyncConfirmIssues: SettingsIssueProvider = {
  id: 'settings-sync-confirm',
  desktopOnly: true,
  async collect(client) {
    const rows = await client.sync.status();
    const held = rows.filter((r) => r.awaiting_confirmation).map((r) => syncCategoryName(r.category));
    if (held.length === 0) return [];
    return [
      {
        id: 'settings-sync-confirm',
        severity: 'warning',
        title: 'Start syncing your settings?',
        body:
          `You turned on sync for: ${held.join(', ')}. ` +
          'Until now, none of these settings has left this device. ' +
          'When you choose Start syncing, Kenaz uploads them so your other signed-in devices can use them. ' +
          'API keys and other credentials are not included.',
        fix: { label: 'Start syncing', run: () => client.sync.confirmUploads() },
        link: { label: 'Change what syncs', to: '/settings?tab=sync' },
      },
    ];
  },
};

/** Every issue provider the banner consults, in display order. */
const SETTINGS_ISSUE_PROVIDERS: readonly SettingsIssueProvider[] = [
  pendingApprovalsIssues,
  settingsSyncConfirmIssues,
  databaseIssues,
];

/**
 * Runs every provider that can run in this build and concatenates their
 * issues. A provider that throws contributes nothing — a broken check must
 * not take the banner (or the settings page) down with it — and is
 * reported to the console.
 */
export async function collectSettingsIssues(
  client: HarnessClient,
  served: boolean,
): Promise<SettingsIssue[]> {
  const providers = SETTINGS_ISSUE_PROVIDERS.filter((p) => !(served && p.desktopOnly));
  const results = await Promise.all(
    providers.map(async (p) => {
      try {
        return await p.collect(client);
      } catch (e) {
        console.warn(`settings issue check "${p.id}" failed:`, e);
        return [];
      }
    }),
  );
  return results.flat();
}
