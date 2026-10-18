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
 * The chat's KEY selection (provider-credentials-client-01PCCLI01 WP04,
 * §7a H2: the user picks a key per chat) is stored separately, under
 * `kenaz.session.credential.<accountKey>.<sessionId>`, because unlike the
 * model it belongs to a Fleet account: the key is scoped by the opaque
 * FleetSessionView.accountKey ("local" when signed out), every selection is
 * dropped when the account changes or signs out (clearSessionCredentials,
 * called by lib/fleetSession.ts), and a selection is bound to the provider
 * it was made for — switching the chat's provider drops it, so it can never
 * name another provider's key. Nothing dispatches with it yet: the per-chat
 * key picker and the send path are WP07 (docs/unwired-ledger.md, 2026-10-16).
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
    // A credentialId left by the first WP04 build would outlive a provider
    // change here; the selection lives under its own key now.
    delete base.credentialId;
    if (typeof base.providerId === 'string' && base.providerId !== cfg.providerId) {
      dropSessionCredentials(sessionId);
    }
    window.localStorage.setItem(
      key(sessionId),
      JSON.stringify({ ...base, providerId: cfg.providerId, modelId: cfg.modelId }),
    );
  } catch {
    /* storage unavailable — the in-memory selection still applies */
  }
}

const CREDENTIAL_PREFIX = 'kenaz.session.credential.';

function accountBucket(accountKey: string | null | undefined): string {
  return accountKey && /^[a-z0-9.]+$/.test(accountKey) ? accountKey : 'local';
}

function credentialKey(accountKey: string | null | undefined, sessionId: string): string {
  return `${CREDENTIAL_PREFIX}${accountBucket(accountKey)}.${sessionId}`;
}

function storageKeys(): string[] {
  const out: string[] = [];
  for (let i = 0; i < window.localStorage.length; i++) {
    const k = window.localStorage.key(i);
    if (k) out.push(k);
  }
  return out;
}

/**
 * readSessionCredential returns the chat's selected key (a CredentialKey.id)
 * for this account, or null. It returns null unless the selection was made
 * for `providerId`, so a stale selection never crosses providers.
 *
 * TODO(WP07): a stored selection is a preference, not an authorisation.
 * Every use must re-check it against the current key list and org policy
 * (CredentialKey.userSelectable, allow_user_selection, the Cedar Credential
 * family) and fall back to the default key when it no longer passes.
 */
export function readSessionCredential(
  accountKey: string | null | undefined,
  sessionId: string,
  providerId: string,
): string | null {
  if (!sessionId || !providerId) return null;
  try {
    const raw = window.localStorage.getItem(credentialKey(accountKey, sessionId));
    if (!raw) return null;
    const parsed = JSON.parse(raw) as { providerId?: unknown; credentialId?: unknown };
    if (parsed.providerId === providerId && typeof parsed.credentialId === 'string' && parsed.credentialId) {
      return parsed.credentialId;
    }
  } catch {
    /* malformed or storage unavailable */
  }
  return null;
}

/**
 * writeSessionCredential records (or, with null, clears) the chat's key
 * selection for this account, bound to `providerId`.
 */
export function writeSessionCredential(
  accountKey: string | null | undefined,
  sessionId: string,
  providerId: string,
  credentialId: string | null,
): void {
  if (!sessionId) return;
  try {
    const k = credentialKey(accountKey, sessionId);
    if (!credentialId || !providerId) {
      window.localStorage.removeItem(k);
      return;
    }
    window.localStorage.setItem(k, JSON.stringify({ providerId, credentialId }));
  } catch {
    /* storage unavailable */
  }
}

/** dropSessionCredentials removes one chat's selection for every account. */
function dropSessionCredentials(sessionId: string): void {
  const suffix = `.${sessionId}`;
  for (const k of storageKeys()) {
    if (k.startsWith(CREDENTIAL_PREFIX) && k.endsWith(suffix)) window.localStorage.removeItem(k);
  }
}

/**
 * clearSessionCredentials removes every per-chat key selection, for every
 * account except `keepAccountKey` when given. lib/fleetSession.ts calls it
 * when the signed-in account changes or signs out, however that happened
 * (menu, Settings, node removal), and on the first snapshot of a run with
 * the current account kept.
 */
export function clearSessionCredentials(keepAccountKey?: string): void {
  const keep = keepAccountKey === undefined ? null : `${CREDENTIAL_PREFIX}${accountBucket(keepAccountKey)}.`;
  try {
    for (const k of storageKeys()) {
      if (k.startsWith(CREDENTIAL_PREFIX) && !(keep && k.startsWith(keep))) window.localStorage.removeItem(k);
    }
  } catch {
    /* storage unavailable */
  }
}
