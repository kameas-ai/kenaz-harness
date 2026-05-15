import { describe, it, expect } from 'vitest';
import {
  modelFitsInRAM,
  effectiveLocalRuntimeRAMBytes,
  formatBytes,
  RAM_FIT_THRESHOLD,
} from '@/lib/modelFit';

describe('modelFitsInRAM', () => {
  const GB = 1024 * 1024 * 1024;

  it('returns true when sizeBytes is 0 (unknown)', () => {
    expect(modelFitsInRAM(0, 16 * GB)).toBe(true);
  });

  it('returns true when effectiveRamBytes is 0 (unknown)', () => {
    expect(modelFitsInRAM(8 * GB, 0)).toBe(true);
  });

  it('returns true when model is within 85% of RAM', () => {
    // 8 GB model, 16 GB RAM → 50% → fits
    expect(modelFitsInRAM(8 * GB, 16 * GB)).toBe(true);
  });

  it('returns false when model exceeds 85% of RAM', () => {
    // 14.5 GB model, 16 GB RAM → 90.6% → does not fit
    expect(modelFitsInRAM(14.5 * GB, 16 * GB)).toBe(false);
  });

  it('uses RAM_FIT_THRESHOLD constant', () => {
    const ram = 16 * GB;
    const borderline = ram * RAM_FIT_THRESHOLD;
    expect(modelFitsInRAM(borderline, ram)).toBe(true); // exactly at threshold → fits
    expect(modelFitsInRAM(borderline + 1, ram)).toBe(false); // 1 byte over → does not
  });
});

describe('effectiveLocalRuntimeRAMBytes', () => {
  const GB = 1024 * 1024 * 1024;

  it('returns detectedBytes when overrideGB is 0', () => {
    expect(effectiveLocalRuntimeRAMBytes(0, 16 * GB)).toBe(16 * GB);
  });

  it('converts overrideGB to bytes when > 0', () => {
    expect(effectiveLocalRuntimeRAMBytes(12, 0)).toBe(12 * GB);
  });

  it('supports decimal override values', () => {
    const result = effectiveLocalRuntimeRAMBytes(12.5, 0);
    expect(result).toBe(Math.floor(12.5 * GB));
  });

  it('override wins over detected', () => {
    expect(effectiveLocalRuntimeRAMBytes(8, 16 * GB)).toBe(8 * GB);
  });
});

describe('formatBytes', () => {
  const GB = 1024 * 1024 * 1024;

  it('returns empty string for 0', () => {
    expect(formatBytes(0)).toBe('');
  });

  it('formats bytes as GB', () => {
    expect(formatBytes(4 * GB)).toBe('4.0 GB');
  });

  it('formats fractional GB', () => {
    expect(formatBytes(Math.floor(4.7 * GB))).toMatch(/^4\.\d GB$/);
  });
});
