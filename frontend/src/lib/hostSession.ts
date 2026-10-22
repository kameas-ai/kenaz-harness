/**
 * hostSession.ts — the host app's sign-in state, as told by the Kenaz
 * workbench chrome (kenaz audit HR-8).
 *
 * In served mode the harness runs in an iframe inside the Kenaz workbench
 * window. The host auth broker owns the session, so the harness has no
 * sign-in of its own — and until now no way to know the HOST was signed in:
 * its profile menu read as signed out while Kenaz showed an Enterprise
 * account. The chrome posts a display-only message to this frame:
 *
 *   { kind: 'kameas/host-session', signed_in, display_name, org, plan }
 *
 * (kenaz frontend/src/views/Workbench/Chrome/hostSession.ts). It carries no
 * token, email or id — names for display and a flag. This module accepts it
 * ONLY from `window.parent` (the chrome) and only in served mode, validates
 * every field, and exposes the result as a read-only ref. It is display
 * state: nothing here authenticates anything.
 */

import { readonly, ref, type Ref } from "vue";
import { isServedMode } from "./useServedMode";

export const HOST_SESSION_KIND = "kameas/host-session";

const MAX_FIELD = 120;

export interface HostSession {
  signedIn: boolean;
  displayName: string;
  org: string;
  plan: string;
}

function cleanField(v: unknown): string | null {
  if (v === undefined) return "";
  if (typeof v !== "string") return null;
  return v
    .replace(/[\u0000-\u001f\u007f]/g, "")
    .trim()
    .slice(0, MAX_FIELD);
}

/**
 * parseHostSessionMessage validates a postMessage payload. Null for anything
 * that is not a well-formed host-session message (wrong kind, non-boolean
 * flag, non-string fields). Unknown extra fields are ignored, never copied.
 */
export function parseHostSessionMessage(data: unknown): HostSession | null {
  if (!data || typeof data !== "object") return null;
  const d = data as Record<string, unknown>;
  if (d.kind !== HOST_SESSION_KIND || typeof d.signed_in !== "boolean")
    return null;
  const displayName = cleanField(d.display_name);
  const org = cleanField(d.org);
  const plan = cleanField(d.plan);
  if (displayName === null || org === null || plan === null) return null;
  if (!d.signed_in)
    return { signedIn: false, displayName: "", org: "", plan: "" };
  return { signedIn: true, displayName, org, plan };
}

const _host = ref<HostSession | null>(null);
let _listener: ((ev: MessageEvent) => void) | null = null;

function install(): void {
  if (_listener || typeof window === "undefined") return;
  if (!isServedMode() || window.parent === window) return;
  _listener = (ev: MessageEvent) => {
    // Only the embedding chrome may speak for the host.
    if (ev.source !== window.parent) return;
    const parsed = parseHostSessionMessage(ev.data);
    if (parsed) _host.value = parsed;
  };
  window.addEventListener("message", _listener);
}

/**
 * useHostSession — the latest host session the chrome reported, or null
 * when none has arrived (desktop mode, standalone served mode, or the
 * chrome has not spoken yet).
 */
export function useHostSession(): Readonly<Ref<HostSession | null>> {
  install();
  return readonly(_host) as Readonly<Ref<HostSession | null>>;
}

/** Initials for the avatar: first letters of the display name, else org. */
export function hostInitials(h: HostSession | null): string {
  if (!h?.signedIn) return "";
  const parts = (h.displayName || "").split(/\s+/).filter(Boolean);
  if (parts.length > 0) {
    return ((parts[0]?.[0] ?? "") + (parts[1]?.[0] ?? ""))
      .toUpperCase()
      .slice(0, 2);
  }
  return h.org ? h.org[0]!.toUpperCase() : "·";
}

/** Test-only: drop state and the listener. */
export function _resetHostSessionForTest(): void {
  if (_listener && typeof window !== "undefined")
    window.removeEventListener("message", _listener);
  _listener = null;
  _host.value = null;
}
