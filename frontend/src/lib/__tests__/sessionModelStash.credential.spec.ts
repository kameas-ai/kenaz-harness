import { describe, it, expect, beforeEach } from 'vitest';
import { readSessionModel, writeSessionCredential, writeSessionModel } from '@/lib/sessionModelStash';

describe('sessionModelStash credential selection (WP04)', () => {
  beforeEach(() => window.localStorage.clear());

  it('persists the chat key selection beside provider and model', () => {
    writeSessionModel('s1', { providerId: 'p', modelId: 'm' });
    writeSessionCredential('s1', 'c-org');
    expect(readSessionModel('s1')).toEqual({ providerId: 'p', modelId: 'm', credentialId: 'c-org' });
  });

  it('keeps the selection across a model switch', () => {
    writeSessionModel('s1', { providerId: 'p', modelId: 'm' });
    writeSessionCredential('s1', 'local:p');
    writeSessionModel('s1', { providerId: 'p', modelId: 'm2' });
    expect(readSessionModel('s1')?.credentialId).toBe('local:p');
  });

  it('clears the selection with null', () => {
    writeSessionModel('s1', { providerId: 'p', modelId: 'm' });
    writeSessionCredential('s1', 'c-org');
    writeSessionCredential('s1', null);
    expect(readSessionModel('s1')).toEqual({ providerId: 'p', modelId: 'm' });
  });
});
