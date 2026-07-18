/**
 * memoryFormatters — unit tests (FR-004 negative-zero fix).
 *
 * Verifies that formatActivitySigned correctly normalises -0 and
 * suppresses the sign when the value is zero.
 */
import { describe, it, expect } from 'vitest';
import { formatActivitySigned } from '@/lib/memoryFormatters';

describe('formatActivitySigned (FR-004)', () => {
  it('formats positive n with + sign', () => {
    expect(formatActivitySigned(12, '+')).toBe('+12');
  });

  it('formats positive n with - sign', () => {
    expect(formatActivitySigned(2, '-')).toBe('-2');
  });

  it('formats zero as "0" (no sign — avoids "+0")', () => {
    expect(formatActivitySigned(0, '+')).toBe('0');
  });

  it('formats zero as "0" (no sign — avoids "-0" for pruned)', () => {
    expect(formatActivitySigned(0, '-')).toBe('0');
  });

  // IEEE 754: -0 === 0 in JS, so negative-zero must also normalise to "0".
  it('format(-0) === "0" — never shows "-0"', () => {
    expect(formatActivitySigned(-0, '-')).toBe('0');
  });

  it('format(-0, "+") === "0"', () => {
    expect(formatActivitySigned(-0, '+')).toBe('0');
  });

  it('formats 1 with + sign', () => {
    expect(formatActivitySigned(1, '+')).toBe('+1');
  });

  it('formats large numbers (1000 separator handled by toLocaleString)', () => {
    // We do not assert the exact separator (locale-dependent) — just that
    // the sign is present and no "-0" appears.
    const result = formatActivitySigned(1000, '+');
    expect(result.startsWith('+')).toBe(true);
    expect(result).not.toContain('-0');
  });
});
