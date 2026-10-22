/**
 * kenaz audit HR-8 — host session from the Kenaz workbench chrome.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

vi.mock("@/lib/useServedMode", async () => {
  const { readonly, ref } = await import("vue");
  const f = ref(true);
  return { isServedMode: () => f.value, useServedMode: () => readonly(f) };
});

import {
  _resetHostSessionForTest,
  hostInitials,
  parseHostSessionMessage,
  useHostSession,
} from "@/lib/hostSession";

describe("parseHostSessionMessage", () => {
  it("accepts the display-only shape and ignores extra fields", () => {
    expect(
      parseHostSessionMessage({
        kind: "kameas/host-session",
        signed_in: true,
        display_name: "Nick",
        org: "Kameas",
        plan: "Enterprise",
        access_token: "never-copied",
      }),
    ).toEqual({
      signedIn: true,
      displayName: "Nick",
      org: "Kameas",
      plan: "Enterprise",
    });
  });

  it("drops a signed-out identity", () => {
    expect(
      parseHostSessionMessage({
        kind: "kameas/host-session",
        signed_in: false,
        display_name: "stale",
      }),
    ).toEqual({ signedIn: false, displayName: "", org: "", plan: "" });
  });

  it("rejects malformed payloads", () => {
    for (const bad of [
      null,
      "kameas/host-session",
      { kind: "kameas/focus", signed_in: true },
      { kind: "kameas/host-session" },
      { kind: "kameas/host-session", signed_in: "yes" },
      { kind: "kameas/host-session", signed_in: true, display_name: 42 },
    ]) {
      expect(parseHostSessionMessage(bad)).toBeNull();
    }
  });

  it("strips control characters and caps fields", () => {
    const p = parseHostSessionMessage({
      kind: "kameas/host-session",
      signed_in: true,
      display_name: `A\nB${"x".repeat(300)}`,
    });
    expect(p?.displayName.startsWith("AB")).toBe(true);
    expect(p?.displayName.length).toBe(120);
  });

  it("initials come from the display name, else the org", () => {
    expect(
      hostInitials({
        signedIn: true,
        displayName: "Nick Wisner",
        org: "",
        plan: "",
      }),
    ).toBe("NW");
    expect(
      hostInitials({
        signedIn: true,
        displayName: "",
        org: "kameas",
        plan: "",
      }),
    ).toBe("K");
    expect(
      hostInitials({ signedIn: false, displayName: "x", org: "", plan: "" }),
    ).toBe("");
  });
});

describe("useHostSession — only the embedding chrome may speak", () => {
  let frame: HTMLIFrameElement;
  let fakeParent: Window;

  beforeEach(() => {
    _resetHostSessionForTest();
    frame = document.createElement("iframe");
    document.body.appendChild(frame);
    fakeParent = frame.contentWindow as Window;
    Object.defineProperty(window, "parent", {
      value: fakeParent,
      configurable: true,
    });
  });

  afterEach(() => {
    _resetHostSessionForTest();
    Object.defineProperty(window, "parent", {
      value: window,
      configurable: true,
    });
    frame.remove();
  });

  const msg = {
    kind: "kameas/host-session",
    signed_in: true,
    display_name: "Nick",
    org: "Kameas",
    plan: "Enterprise",
  };

  it("accepts a message whose source is window.parent", () => {
    const host = useHostSession();
    window.dispatchEvent(
      new MessageEvent("message", { data: msg, source: fakeParent }),
    );
    expect(host.value).toEqual({
      signedIn: true,
      displayName: "Nick",
      org: "Kameas",
      plan: "Enterprise",
    });
  });

  it("ignores the same message from any other source", () => {
    const host = useHostSession();
    window.dispatchEvent(
      new MessageEvent("message", { data: msg, source: window }),
    );
    window.dispatchEvent(new MessageEvent("message", { data: msg }));
    expect(host.value).toBeNull();
  });
});
