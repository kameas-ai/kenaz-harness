/**
 * modelFit — pure RAM-fit computation helpers for the local model picker.
 *
 * All functions are pure (no side effects) so they can be tested without
 * mounting a Vue component.
 *
 * (local-model-runtimes-01KQ8VMZ WP06)
 */

/** The amber threshold: a model is flagged when it exceeds this fraction of RAM. */
export const RAM_FIT_THRESHOLD = 0.85;

/**
 * modelFitsInRAM returns true when the model's byte size is within the
 * 85% RAM threshold.
 *
 * @param sizeBytes  Model file size in bytes (0 = unknown → always fits).
 * @param effectiveRamBytes Effective RAM from EffectiveLocalRuntimeRAMBytes (0 = unknown).
 */
export function modelFitsInRAM(sizeBytes: number, effectiveRamBytes: number): boolean {
  if (sizeBytes === 0 || effectiveRamBytes === 0) return true;
  return sizeBytes <= effectiveRamBytes * RAM_FIT_THRESHOLD;
}

/**
 * effectiveLocalRuntimeRAMBytes mirrors the Go helper
 * Settings.EffectiveLocalRuntimeRAMBytes(detected int64).
 *
 * When overrideGB > 0, the override is converted to bytes and returned.
 * Otherwise, `detectedBytes` is returned.
 *
 * @param overrideGB       User-supplied GiB override (from settings). 0 = no override.
 * @param detectedBytes    Detected system RAM in bytes.
 */
export function effectiveLocalRuntimeRAMBytes(
  overrideGB: number,
  detectedBytes: number,
): number {
  if (overrideGB > 0) {
    return Math.floor(overrideGB * (1 << 30));
  }
  return detectedBytes;
}

/** Formats a byte count to a human-readable GiB string ("4.7 GB"). */
export function formatBytes(bytes: number): string {
  if (bytes === 0) return '';
  const gb = bytes / (1024 * 1024 * 1024);
  return `${gb.toFixed(1)} GB`;
}
