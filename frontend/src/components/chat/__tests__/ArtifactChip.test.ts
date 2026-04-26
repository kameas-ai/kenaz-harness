import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import ArtifactChip from '@/components/chat/ArtifactChip.vue';
import type { Artifact, ArtifactSource } from '@/lib/types';

function makeArtifact(overrides: Partial<Artifact> = {}): Artifact {
  return {
    id: 'art-1',
    sessionId: 'sess-1',
    title: 'tictactoe.html',
    mimeType: 'text/html',
    contentHash: 'sha256:abc',
    byteSize: 3200,
    source: 'code_block',
    sourceRef: { messageId: 'm-1' },
    scopeKind: 'session',
    createdAt: '2026-04-26T00:00:00Z',
    ...overrides,
  };
}

describe('ArtifactChip', () => {
  it('renders the title and a formatted size', () => {
    const w = mount(ArtifactChip, {
      props: { artifact: makeArtifact({ byteSize: 3072 }) },
    });
    expect(w.text()).toContain('tictactoe.html');
    expect(w.text()).toMatch(/3\.0 KB/);
  });

  it('emits open with the artifact when clicked', async () => {
    const a = makeArtifact();
    const w = mount(ArtifactChip, { props: { artifact: a } });
    await w.find('button').trigger('click');
    const events = w.emitted('open');
    expect(events).toBeTruthy();
    expect(events![0][0]).toEqual(a);
  });

  it('uses different icons for each source kind', () => {
    const sources: ArtifactSource[] = ['code_block', 'tool_output', 'user_pin'];
    const renders = sources.map((s) => {
      const w = mount(ArtifactChip, { props: { artifact: makeArtifact({ source: s }) } });
      return w.html();
    });
    // Each render must contain an SVG (the lucide icon); verify all three
    // were distinct so the source-icon mapping fired.
    for (const html of renders) {
      expect(html).toContain('<svg');
    }
    expect(new Set(renders).size).toBe(sources.length);
  });

  it('uses no raw color literal (privacy CI invariant #4)', () => {
    const w = mount(ArtifactChip, { props: { artifact: makeArtifact() } });
    expect(w.html()).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
  });
});
