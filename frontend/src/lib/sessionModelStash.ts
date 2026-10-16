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
 * The stash also holds the chat's key selection, `credentialId` — a
 * CredentialKey.id from LLM_ListCredentialKeys (provider-credentials-client-
 * 01PCCLI01 WP04, §7a H2: the user picks a key per chat). Model switches
 * preserve it. Nothing dispatches with it yet: the per-chat key picker and
 * the send path that honours it are WP07 (see docs/unwired-ledger.md,
 * 2026-10-16 entry).
 *
 * Storage can be unavailable (private window, blocked site data); both
 * helpers degrade to "no stash" rather than throwing.
 */
export interface SessionModelConfig {
  providerId: string;
  modelId: string;
  /** The chat's selected key (CredentialKey.id); absent = the profile's own. */
  credentialId?: string;
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
      const cfg: SessionModelConfig = { providerId: parsed.providerId, modelId: parsed.modelId };
      if (typeof parsed.credentialId === 'string' && parsed.credentialId) {
        cfg.credentialId = parsed.credentialId;
      }
      return cfg;
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

/**
 * writeSessionCredential records (or, with null, clears) the chat's key
 * selection without touching its provider and model.
 */
export function writeSessionCredential(sessionId: string, credentialId: string | null): void {
  if (!sessionId) return;
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
    if (credentialId) {
      base.credentialId = credentialId;
    } else {
      delete base.credentialId;
    }
    window.localStorage.setItem(key(sessionId), JSON.stringify(base));
  } catch {
    /* storage unavailable */
  }
}
