/**
 * Tests for the shared recipe-category taxonomy (FR-005).
 *
 * Exercises the real `recipeCategories.ts` module: canonical values resolve
 * to their curated label + icon; unknown slugs fall back to a title-cased
 * label + the generic Wrench icon; empty/undefined categories render as
 * "Other". This is the single source of truth both the Registry catalog and
 * the built-in Tools panel resolve icons + labels through.
 */
import { describe, it, expect } from 'vitest';
import {
  CANONICAL_RECIPE_CATEGORIES,
  categoryIconFor,
  categoryLabel,
  isCanonicalCategory,
} from '@/lib/recipeCategories';
import { CircleUser, Server, Wrench, Zap } from '@/shell/icons';

describe('isCanonicalCategory', () => {
  it('accepts every canonical value', () => {
    for (const cat of CANONICAL_RECIPE_CATEGORIES) {
      expect(isCanonicalCategory(cat)).toBe(true);
    }
  });

  it('rejects unknown, mixed-case, and empty values', () => {
    expect(isCanonicalCategory('deployment')).toBe(false);
    expect(isCanonicalCategory('Automation')).toBe(false); // case-sensitive by design
    expect(isCanonicalCategory('')).toBe(false);
  });
});

describe('categoryLabel', () => {
  it('maps a canonical value to its curated display label', () => {
    expect(categoryLabel('automation')).toBe('Automation & iPaaS');
    expect(categoryLabel('hr_people')).toBe('HR & People');
    expect(categoryLabel('support')).toBe('Support & ITSM');
  });

  it('title-cases an unknown slug rather than dropping it', () => {
    expect(categoryLabel('deployment')).toBe('Deployment');
    expect(categoryLabel('legacy_bi_tool')).toBe('Legacy Bi Tool');
  });

  it('falls back to "Other" for an empty category', () => {
    expect(categoryLabel('')).toBe('Other');
  });
});

describe('categoryIconFor', () => {
  it('maps canonical values to their curated icon', () => {
    expect(categoryIconFor('automation')).toBe(Zap);
    expect(categoryIconFor('hr_people')).toBe(CircleUser);
    expect(categoryIconFor('support')).toBe(Server);
  });

  it('falls back to the generic Wrench icon for unknown / empty values', () => {
    expect(categoryIconFor('deployment')).toBe(Wrench);
    expect(categoryIconFor('')).toBe(Wrench);
  });
});
