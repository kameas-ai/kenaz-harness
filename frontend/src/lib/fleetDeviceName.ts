/**
 * Shared signal for the Fleet device name (Fleet ER-43): FleetDeviceNamePanel
 * bumps it after a save so every FleetDeviceNameNotice on screen re-reads
 * the name the organization will see.
 */
import { ref } from 'vue';

export const fleetDeviceNameVersion = ref(0);

export function notifyFleetDeviceNameChanged(): void {
  fleetDeviceNameVersion.value++;
}

/**
 * The one line that tells a member, before and after sign-in, what name
 * their organization sees for this device. '' when sharing is off (nothing
 * is sent, so there is nothing to disclose).
 */
export function fleetDeviceNameNoticeText(dn: { share: boolean; effectiveName: string }): string {
  if (!dn.share) return '';
  const where = 'You can change this in Settings › Account.';
  if (dn.effectiveName) {
    return `Your organization's admins will see this device as "${dn.effectiveName}". ${where}`;
  }
  return `Your organization's admins will see this device under a neutral name. ${where}`;
}
