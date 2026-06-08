import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import SessionHeader from '@/components/chat/SessionHeader.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { Session } from '@/lib/types';

const baseSession: Session = {
  id: 'ses-1',
  name: 'Test Session',
  createdAt: '',
  updatedAt: '',
};

function mountHeader(
  session: Session,
  suggestTitle?: () => Promise<string>,
  exportSession?: () => Promise<{ path: string; byteCount: number }>,
) {
  const suggestMock = suggestTitle ?? vi.fn().mockResolvedValue('New auto title');
  const exportMock = exportSession ?? vi.fn().mockResolvedValue({ path: '/tmp/out.md', byteCount: 42 });
  // Build the full fake client first, then patch the two session methods
  // we care about so the rest remain the default stubs.
  const base = createFakeHarnessClient();
  const client = {
    ...base,
    sessions: {
      ...base.sessions,
      suggestTitle: suggestMock,
      clearTitle: async (_id: string) => undefined as void,
      export: exportMock,
    },
  };
  const w = mount(SessionHeader, {
    props: { session },
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
  return { w, suggestMock, exportMock };
}

describe('SessionHeader (WP05 suggest-title affordance)', () => {
  it('renders the session-header container', async () => {
    const { w } = mountHeader(baseSession);
    await flushPromises();
    expect(w.find('[data-testid="session-header"]').exists()).toBe(true);
  });

  it('renders the "Suggest new title" button', async () => {
    const { w } = mountHeader(baseSession);
    await flushPromises();
    expect(w.find('[data-testid="suggest-title-btn"]').exists()).toBe(true);
    expect(w.text()).toContain('Suggest new title');
  });

  it('calls suggestTitle directly when session name is empty (no modal)', async () => {
    const session: Session = { ...baseSession, name: '' };
    const { w, suggestMock } = mountHeader(session);
    await flushPromises();
    await w.find('[data-testid="suggest-title-btn"]').trigger('click');
    await flushPromises();
    expect(suggestMock).toHaveBeenCalledWith('ses-1');
    expect(w.find('[data-testid="suggest-title-confirm-modal"]').exists()).toBe(false);
  });

  it('calls suggestTitle directly when session is auto-titled (no modal)', async () => {
    const session: Session = { ...baseSession, autoTitled: true };
    const { w, suggestMock } = mountHeader(session);
    await flushPromises();
    await w.find('[data-testid="suggest-title-btn"]').trigger('click');
    await flushPromises();
    expect(suggestMock).toHaveBeenCalledWith('ses-1');
    expect(w.find('[data-testid="suggest-title-confirm-modal"]').exists()).toBe(false);
  });

  it('shows confirm modal when session has user-set title', async () => {
    const session: Session = { ...baseSession, name: 'My title', autoTitled: false };
    const { w, suggestMock } = mountHeader(session);
    await flushPromises();
    await w.find('[data-testid="suggest-title-btn"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="suggest-title-confirm-modal"]').exists()).toBe(true);
    expect(suggestMock).not.toHaveBeenCalled();
  });

  it('calls suggestTitle after the user confirms overwrite', async () => {
    const session: Session = { ...baseSession, name: 'My title', autoTitled: false };
    const { w, suggestMock } = mountHeader(session);
    await flushPromises();
    await w.find('[data-testid="suggest-title-btn"]').trigger('click');
    await flushPromises();
    await w.find('[data-testid="suggest-title-confirm-ok"]').trigger('click');
    await flushPromises();
    expect(suggestMock).toHaveBeenCalledWith('ses-1');
  });

  it('does NOT call suggestTitle when the user cancels the confirm modal', async () => {
    const session: Session = { ...baseSession, name: 'My title', autoTitled: false };
    const { w, suggestMock } = mountHeader(session);
    await flushPromises();
    await w.find('[data-testid="suggest-title-btn"]').trigger('click');
    await flushPromises();
    await w.find('[data-testid="suggest-title-confirm-cancel"]').trigger('click');
    await flushPromises();
    expect(suggestMock).not.toHaveBeenCalled();
    expect(w.find('[data-testid="suggest-title-confirm-modal"]').exists()).toBe(false);
  });

  it('emits title-changed after suggestTitle resolves', async () => {
    const session: Session = { ...baseSession, name: '', autoTitled: true };
    const { w } = mountHeader(session);
    await flushPromises();
    await w.find('[data-testid="suggest-title-btn"]').trigger('click');
    await flushPromises();
    expect(w.emitted('title-changed')).toBeTruthy();
  });

  it('disables the button while suggesting is in flight', async () => {
    let resolveTitle!: (v: string) => void;
    const slow = vi.fn(
      () => new Promise<string>((res) => { resolveTitle = res; }),
    );
    const session: Session = { ...baseSession, name: '', autoTitled: true };
    const { w } = mountHeader(session, slow);
    await flushPromises();
    await w.find('[data-testid="suggest-title-btn"]').trigger('click');
    await flushPromises();
    const btn = w.find<HTMLButtonElement>('[data-testid="suggest-title-btn"]');
    expect(btn.attributes('disabled')).toBeDefined();
    resolveTitle('done');
    await flushPromises();
    expect(btn.attributes('disabled')).toBeUndefined();
  });
});

describe('SessionHeader — export affordance (session-export-01NDFSEX05 WP03)', () => {
  it('renders the Export… button', async () => {
    const { w } = mountHeader(baseSession);
    await flushPromises();
    expect(w.find('[data-testid="export-session-btn"]').exists()).toBe(true);
    expect(w.text()).toContain('Export…');
  });

  it('calls sessions.export with markdown when window.confirm returns true', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    const { w, exportMock } = mountHeader(baseSession);
    await flushPromises();
    await w.find('[data-testid="export-session-btn"]').trigger('click');
    await flushPromises();
    expect(exportMock).toHaveBeenCalledWith('ses-1', 'markdown');
    vi.restoreAllMocks();
  });

  it('calls sessions.export with json when window.confirm returns false', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(false);
    const { w, exportMock } = mountHeader(baseSession);
    await flushPromises();
    await w.find('[data-testid="export-session-btn"]').trigger('click');
    await flushPromises();
    expect(exportMock).toHaveBeenCalledWith('ses-1', 'json');
    vi.restoreAllMocks();
  });

  it('silently ignores a cancelled export (no error shown)', async () => {
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    const cancelledExport = vi.fn().mockRejectedValue(new Error('export cancelled by user'));
    const { w } = mountHeader(baseSession, undefined, cancelledExport);
    await flushPromises();
    await w.find('[data-testid="export-session-btn"]').trigger('click');
    await flushPromises();
    // No error text should appear for a cancel.
    expect(w.find('[data-testid="export-session-btn"]').attributes('disabled')).toBeUndefined();
    vi.restoreAllMocks();
  });
});

describe('SessionHeader — rename affordance (relocated from left rail)', () => {
  function mountRename(
    session: Session,
    rename = vi.fn().mockResolvedValue(undefined),
    clearTitle = vi.fn().mockResolvedValue(undefined),
  ) {
    const base = createFakeHarnessClient();
    const client = {
      ...base,
      sessions: { ...base.sessions, rename, clearTitle },
    };
    const w = mount(SessionHeader, {
      props: { session },
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    return { w, rename, clearTitle };
  }

  it('clicking the title opens an inline rename input', async () => {
    const { w } = mountRename(baseSession);
    await flushPromises();
    const title = w.find('[data-testid="rename-session-title"]');
    expect(title.exists()).toBe(true);
    await title.trigger('click');
    expect(w.find('[data-testid="rename-session-input-ses-1"]').exists()).toBe(true);
  });

  it('committing a new title calls sessions.rename and emits title-changed', async () => {
    const { w, rename } = mountRename(baseSession);
    await flushPromises();
    await w.find('[data-testid="rename-session-title"]').trigger('click');
    const input = w.find<HTMLInputElement>('[data-testid="rename-session-input-ses-1"]');
    await input.setValue('Renamed here');
    await input.trigger('keydown.enter');
    await flushPromises();
    expect(rename).toHaveBeenCalledWith('ses-1', 'Renamed here');
    expect(w.emitted('title-changed')).toBeTruthy();
  });

  it('empty submission clears the title instead of renaming', async () => {
    const { w, rename, clearTitle } = mountRename(baseSession);
    await flushPromises();
    await w.find('[data-testid="rename-session-title"]').trigger('click');
    const input = w.find<HTMLInputElement>('[data-testid="rename-session-input-ses-1"]');
    await input.setValue('   ');
    await input.trigger('keydown.enter');
    await flushPromises();
    expect(clearTitle).toHaveBeenCalledWith('ses-1');
    expect(rename).not.toHaveBeenCalled();
  });
});

describe('SessionHeader — move-to-project affordance (relocated from left rail)', () => {
  const projects = [
    { id: 'p-a', name: 'Project A', description: '', createdAt: '', updatedAt: '' },
    { id: 'p-b', name: 'Project B', description: '', createdAt: '', updatedAt: '' },
  ];
  function mountMove(session: Session, moveToProject = vi.fn().mockResolvedValue(undefined)) {
    const base = createFakeHarnessClient();
    const client = {
      ...base,
      sessions: { ...base.sessions, moveToProject },
      projects: { ...base.projects, list: async () => projects },
    };
    const w = mount(SessionHeader, {
      props: { session },
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    return { w, moveToProject };
  }

  it('renders the Move… button', async () => {
    const { w } = mountMove(baseSession);
    await flushPromises();
    expect(w.find('[data-testid="move-session-btn"]').exists()).toBe(true);
  });

  it('opens the menu (excluding the current project) and moves on click', async () => {
    const session: Session = { ...baseSession, projectId: 'p-a' };
    const { w, moveToProject } = mountMove(session);
    await flushPromises();
    expect(w.find('[data-testid="move-session-menu"]').exists()).toBe(false);
    await w.find('[data-testid="move-session-btn"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="move-session-menu"]').exists()).toBe(true);
    // Current project (p-a) excluded; p-b offered; Global offered (in a project).
    expect(w.find('[data-testid="move-session-to-p-b"]').exists()).toBe(true);
    expect(w.find('[data-testid="move-session-to-p-a"]').exists()).toBe(false);
    expect(w.find('[data-testid="move-session-to-global"]').exists()).toBe(true);
    await w.find('[data-testid="move-session-to-p-b"]').trigger('click');
    await flushPromises();
    expect(moveToProject).toHaveBeenCalledWith('ses-1', 'p-b');
    expect(w.find('[data-testid="move-session-menu"]').exists()).toBe(false);
  });
});
