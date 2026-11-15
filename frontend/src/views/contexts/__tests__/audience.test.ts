import { describe, expect, it } from 'vitest';
import { audienceLabel } from '../audience';

describe('audienceLabel', () => {
  it('labels served audiences and shows unknown ones as served', () => {
    expect(audienceLabel({ audience: 'team' })).toBe('Team');
    expect(audienceLabel({ audience: 'org' })).toBe('Organisation');
    expect(audienceLabel({ audience: 'org', governed: true })).toBe('Organisation · admin-governed');
    expect(audienceLabel({ audience: 'division' })).toBe('division');
    expect(audienceLabel({})).toBe('');
  });
});
