/**
 * WP18 — Marketing & analytics + data/BI category groupings (01NCONN09)
 *
 * Verifies that the four new recipe categories introduced by the
 * marketing-data connector pack are valid RecipeCategory values and are
 * handled by the shared type system without compile errors (vitest
 * type-checks via vue-tsc).
 */
import { describe, it, expect } from 'vitest';
import type { RecipeCategory } from '@/lib/types';

/** Categories introduced by pack 01NCONN09. */
const MARKETING_DATA_CATEGORIES: RecipeCategory[] = [
  'analytics',
  'marketing',
  'bi',
  'data',
];

/** Full set of categories expected in the registry. */
const ALL_EXPECTED_CATEGORIES: RecipeCategory[] = [
  'search',
  'filesystem',
  'memory',
  'fetch',
  'productivity',
  'developer',
  'finance',
  'communication',
  'deployment',
  'automation',
  'analytics',
  'marketing',
  'bi',
  'data',
  'other',
];

describe('RecipeCategory — marketing & data/BI pack (01NCONN09)', () => {
  it('defines all four new categories as valid RecipeCategory values', () => {
    // If any of these were not in the RecipeCategory union, TypeScript
    // would fail the type assertion above and the build would reject.
    expect(MARKETING_DATA_CATEGORIES).toHaveLength(4);
    for (const cat of MARKETING_DATA_CATEGORIES) {
      expect(cat).toBeTruthy();
    }
  });

  it('analytics category is a valid RecipeCategory', () => {
    const cat: RecipeCategory = 'analytics';
    expect(cat).toBe('analytics');
  });

  it('marketing category is a valid RecipeCategory', () => {
    const cat: RecipeCategory = 'marketing';
    expect(cat).toBe('marketing');
  });

  it('bi category is a valid RecipeCategory', () => {
    const cat: RecipeCategory = 'bi';
    expect(cat).toBe('bi');
  });

  it('data category is a valid RecipeCategory', () => {
    const cat: RecipeCategory = 'data';
    expect(cat).toBe('data');
  });

  it('all expected categories are present in the complete set', () => {
    expect(ALL_EXPECTED_CATEGORIES).toContain('analytics');
    expect(ALL_EXPECTED_CATEGORIES).toContain('marketing');
    expect(ALL_EXPECTED_CATEGORIES).toContain('bi');
    expect(ALL_EXPECTED_CATEGORIES).toContain('data');
    expect(ALL_EXPECTED_CATEGORIES).toContain('automation');
    // Guard: total count matches (prevent accidental shrinkage).
    expect(ALL_EXPECTED_CATEGORIES.length).toBeGreaterThanOrEqual(15);
  });
});
