/**
 * shareOutcome — what a share did, per item, in plain words
 * (kenaz-fleet spec 018 H-3; Context_Publish in core/rpc/views/contexts).
 *
 * Fleet answers every pushed node with a status:
 *   - published       → "Published to your team / your organisation"
 *   - pending_review  → "Waiting for review" (an admin reviews an org-wide
 *                       share of a member's own team entry); the merge
 *                       request id is shown when Fleet gives one
 *   - refused         → "Refused: <reason in plain words>" (reason_text,
 *                       built in Go by fleet.PushRefusalText)
 *
 * An older Fleet (or a result without `status`) is read as before: a node
 * refused on version only is "Not updated", anything else published.
 */
import type { ContextPublishResult } from '@/lib/types';

export type ShareTone = 'success' | 'warning' | 'danger';

export interface ShareOutcome {
  status: 'published' | 'pending_review' | 'refused';
  tone: ShareTone;
  text: string;
}

const VERSION_CONFLICT = 'Not updated: Fleet has a newer version of this entry.';

/** Where a published entry went, from the explicit audience or the layer. */
function whereText(res: ContextPublishResult): string {
  if (res.audience === 'org' || res.effective_layer === 'org') return 'your organisation';
  return 'your team';
}

export function publishOutcome(res: ContextPublishResult): ShareOutcome {
  const conflicts = res.conflicts?.length ?? 0;
  const status: string = res.status ?? (res.accepted_nodes === 0 && conflicts > 0 ? 'refused' : 'published');
  if (status === 'pending_review') {
    const id = res.merge_request_id ? ` (request ${res.merge_request_id})` : '';
    return { status, tone: 'warning', text: `Waiting for review${id}` };
  }
  if (status === 'refused') {
    if (res.reason === 'version_conflict' || (!res.reason && conflicts > 0)) {
      return { status, tone: 'warning', text: VERSION_CONFLICT };
    }
    return { status, tone: 'danger', text: `Refused: ${res.reason_text || 'Fleet refused it'}` };
  }
  if (status !== 'published') {
    // A status this build does not know is never reported as success.
    return { status: 'refused', tone: 'warning', text: `Fleet returned an unexpected result: ${status}` };
  }
  let text = `Published to ${whereText(res)}`;
  if (conflicts > 0) text += ` · ${conflicts} version conflict${conflicts === 1 ? '' : 's'}`;
  return { status: 'published', tone: 'success', text };
}
