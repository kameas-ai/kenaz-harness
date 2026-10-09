/**
 * sessionModelStash — the per-session (providerId, modelId) the chat
 * surface dispatches with, persisted under
 * `kenaz.session.config.<sessionId>`.
 *
 * NewSessionDialog has always written this key so a cross-family choice
 * the mid-conversation switcher would block survives a reload. The
 * switcher itself (and `/model`) never wrote it — so the next re-seed of
 * the session's selection (switching sessions, or a branch Merge that
 * navigates back) restored the dialog's original model and silently
 * undid the switch (dogfood 2026-10-08 round 2: after Merge the MODEL
 * label reverted from sonnet-latest to haiku-latest). Every switch now
 * writes the same key the seed reads.
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
