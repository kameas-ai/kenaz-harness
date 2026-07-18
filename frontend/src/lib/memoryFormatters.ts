/**
 * memoryFormatters — display helpers for Memory health counters.
 *
 * Extracted from MemoryHealthPanel so they are unit-testable independently
 * of the Vue component (FR-004 negative-zero fix).
 */

/**
 * FR-004: Format a last-7-days activity counter with a sign prefix (+N / -N).
 *
 * Negative-zero (-0) is normalised to plain 0 before applying the sign so
 * the display never shows "-0". When the value is exactly 0 the sign is
 * suppressed entirely ("+0" and "-0" are both misleading in the context of
 * "how many were pruned this week").
 *
 * @param n    Raw counter value from the backend (always non-negative
 *             in normal operation; -0 is an IEEE 754 edge case).
 * @param sign '+' for captured / promoted; '-' for pruned.
 */
export function formatActivitySigned(n: number, sign: '+' | '-'): string {
  // Object.is(-0, 0) is false in JS, but n === 0 catches both +0 and -0
  // because === coerces -0 to 0. This is intentional: we normalise both to 0.
  const v = n === 0 ? 0 : n;
  const abs = Math.abs(v);
  // Suppress sign when there is nothing to show (avoids "+0" / "-0" noise).
  if (v === 0) return abs.toLocaleString();
  return `${sign}${abs.toLocaleString()}`;
}
