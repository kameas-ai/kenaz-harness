import { describe, expect, it } from 'vitest';
import { publishOutcome } from '../shareOutcome';
import type { ContextPublishResult } from '@/lib/types';

const base: ContextPublishResult = { accepted_nodes: 1, accepted_edges: 0, conflicts: [], effective_layer: 'team' };

describe('publishOutcome (spec 018 H-3)', () => {
  it('reads an older backend with no status as before', () => {
    expect(publishOutcome(base)).toEqual({ status: 'published', tone: 'success', text: 'Published to your team' });
    const conflict = { ...base, accepted_nodes: 0, conflicts: [{ node_id: 'n', server_version: 2, client_version: 1 }] };
    expect(publishOutcome(conflict).text).toBe('Not updated: Fleet has a newer version of this entry.');
  });
  it('maps each status to plain words', () => {
    expect(publishOutcome({ ...base, status: 'pending_review' }).text).toBe('Waiting for review');
    expect(publishOutcome({ ...base, status: 'pending_review', merge_request_id: 'mr-1' }).text).toBe(
      'Waiting for review (request mr-1)',
    );
    expect(
      publishOutcome({ ...base, status: 'refused', reason: 'team_not_found', reason_text: 'That team no longer exists' }),
    ).toEqual({ status: 'refused', tone: 'danger', text: 'Refused: That team no longer exists' });
    expect(publishOutcome({ ...base, status: 'refused', reason: 'version_conflict' }).tone).toBe('warning');
    expect(publishOutcome({ ...base, status: 'published', audience: 'org' }).text).toBe('Published to your organisation');
    const odd = publishOutcome({ ...base, status: 'queued' as unknown as 'published' });
    expect(odd.tone).toBe('warning');
    expect(odd.text).toBe('Fleet returned an unexpected result: queued');
  });
});
