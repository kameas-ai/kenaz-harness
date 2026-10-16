/**
 * fleetBundleCompat — plain-language notices for what this build cannot do
 * with the organization's latest settings bundle
 * (signed-bundle-bytes-01SBBYT01 WP03, spec §4.5 / §6 R1 / Fleet ruling 4).
 *
 * One source for both surfaces that show it: the Settings issues banner
 * (settingsIssues.ts provider) and Settings → Account. Copy follows the
 * settingsIssues copy rule: no internal names in titles or bodies; the raw
 * setting names appear only as a secondary detail line.
 */
import type { FleetConfigPullStatusView, FleetUnenforceableSettingView } from '@/lib/types';

export interface FleetBundleNotice {
  id: 'fleet-update-required' | 'fleet-unenforceable-settings' | 'fleet-unknown-settings';
  severity: 'error' | 'warning';
  title: string;
  body: string;
  /** Raw setting names, for support (never values). */
  detail?: string;
}

/** The manual steps every notice shares: the fix is always "update Kenaz". */
export const FLEET_UPDATE_STEPS: readonly string[] = [
  'Open the Help menu and choose Check for Updates.',
  'Install the update, then restart Kenaz.',
];

/** Human labels for the restriction-bearing settings Fleet ruling 6 names. */
const SETTING_LABELS: Record<string, string> = {
  cedar_delta: 'team permission rules',
  cedar: 'team permission rules',
  mcp_allowlist: 'the approved tools list',
  'model_prefs.provider_allowlist': 'the approved AI providers',
  'model_prefs.allow_user_credentials': 'the rule on using your own AI provider keys',
  lockdown: 'emergency lockdown',
};

/** A plain label for a bundle setting name. */
export function settingLabel(name: string): string {
  if (SETTING_LABELS[name]) return SETTING_LABELS[name];
  return name.replace(/\[\]|\.\*/g, '').replace(/[._]+/g, ' ').trim();
}

function describeUnenforceable(u: FleetUnenforceableSettingView): string {
  return `${settingLabel(u.name)} (needs Kenaz ${u.minVersion.replace(/^v/, '')} or later)`;
}

/** Every notice the status warrants, most severe first. */
export function fleetBundleNotices(st: FleetConfigPullStatusView | null | undefined): FleetBundleNotice[] {
  if (!st) return [];
  const out: FleetBundleNotice[] = [];
  if (st.updateRequired) {
    out.push({
      id: 'fleet-update-required',
      severity: 'error',
      title: 'Update Kenaz to keep receiving your organization’s settings',
      body: 'Your organization’s settings have stopped updating on this device. The last settings you received are still in effect.',
    });
  }
  const unenforceable = st.unenforceableSettings ?? [];
  if (unenforceable.length > 0) {
    out.push({
      id: 'fleet-unenforceable-settings',
      severity: 'warning',
      title:
        unenforceable.length === 1
          ? 'This device can’t enforce one of your organization’s rules until Kenaz updates'
          : `This device can’t enforce ${unenforceable.length} of your organization’s rules until Kenaz updates`,
      body: `Can’t enforce: ${unenforceable.map(describeUnenforceable).join('; ')}. Everything else your organization set is in effect.`,
      detail: unenforceable.map((u) => u.name).join(', '),
    });
  }
  const unknown = st.unknownSettingsCount ?? 0;
  if (unknown > 0) {
    out.push({
      id: 'fleet-unknown-settings',
      severity: 'warning',
      title:
        unknown === 1
          ? '1 newer setting from your organization needs a Kenaz update'
          : `${unknown} newer settings from your organization need a Kenaz update`,
      body: 'Kenaz applies every setting it understands. Update Kenaz to apply the rest.',
      detail: (st.unknownSettings ?? []).join(', '),
    });
  }
  return out;
}
