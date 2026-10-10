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
 * comes back. Migration drift is the first provider. A later issue (an MCP
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

/** Every issue provider the banner consults, in display order. */
const SETTINGS_ISSUE_PROVIDERS: readonly SettingsIssueProvider[] = [databaseIssues];

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
