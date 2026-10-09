/**
 * sessionModelStash — the per-session (providerId, modelId) the chat
 * surface dispatches with, persisted under
 * `kenaz.session.config.<sessionId>`.
 *
 * Written by NewSessionDialog (so a cross-family choice the
 * mid-conversation switcher would block survives a reload) and by every
 * model switch (switcher and /model); SessionsView re-seeds the session's
 * selection from it whenever the routed session changes, so a switch
 * survives leaving and returning (e.g. a branch Merge). These helpers
 * are the only readers and writers of the key format.
 *
 * Local to this device: served mode and other devices do not see it (no
 * backend binding stores a session's model).
 *
 * Storage can be unavailable (private window, blocked site data); both
 * helpers degrade to "no stash" rather than throwing.
 */
export interface SessionModelConfig {
  providerId: string;
  modelId: string;
}

function key(sessionId: string): string {
  return `kenaz.session.config.${sessionId}`;
}

export function readSessionModel(sessionId: string): SessionModelConfig | null {
  if (!sessionId) return null;
  try {
    const raw = window.localStorage.getItem(key(sessionId));
    if (!raw) return null;
    const parsed = JSON.parse(raw) as Partial<SessionModelConfig>;
    if (parsed.providerId && parsed.modelId) {
      return { providerId: parsed.providerId, modelId: parsed.modelId };
    }
  } catch {
    /* malformed stash or storage unavailable — ignore */
  }
  return null;
}

export function writeSessionModel(sessionId: string, cfg: SessionModelConfig): void {
  if (!sessionId || !cfg.providerId || !cfg.modelId) return;
  try {
    const prev = window.localStorage.getItem(key(sessionId));
    let base: Record<string, unknown> = {};
    if (prev) {
      try {
        const parsed = JSON.parse(prev) as unknown;
        if (parsed && typeof parsed === 'object') base = parsed as Record<string, unknown>;
      } catch {
        /* overwrite a malformed stash */
      }
    }
    window.localStorage.setItem(
      key(sessionId),
      JSON.stringify({ ...base, providerId: cfg.providerId, modelId: cfg.modelId }),
    );
  } catch {
    /* storage unavailable — the in-memory selection still applies */
  }
}
