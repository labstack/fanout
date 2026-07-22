import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { authorizedFetch, logout, oauthReturnTo, unauthorizedEvent } from "./auth-session";

declare global {
  interface Window { happyDOM: { setURL(url: string): void } }
}

const tokenKey = "fanout.access-token";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

describe("oauthReturnTo", () => {
  function withReturnTo(value: string | null) {
    const url = value === null ? "http://localhost:3000/" : `http://localhost:3000/?return_to=${encodeURIComponent(value)}`;
    window.happyDOM.setURL(url);
  }

  it("returns empty without a return_to parameter", () => {
    withReturnTo(null);
    expect(oauthReturnTo()).toBe("");
  });

  it("accepts the same-origin authorize path", () => {
    withReturnTo("/api/auth/oauth/authorize?client_id=abc&state=xyz");
    expect(oauthReturnTo()).toBe("/api/auth/oauth/authorize?client_id=abc&state=xyz");
  });

  it("rejects an external origin", () => {
    withReturnTo("https://evil.example/api/auth/oauth/authorize");
    expect(oauthReturnTo()).toBe("");
  });

  it("rejects a protocol-relative URL", () => {
    withReturnTo("//evil.example/api/auth/oauth/authorize");
    expect(oauthReturnTo()).toBe("");
  });

  it("rejects other same-origin paths", () => {
    withReturnTo("/dashboards");
    expect(oauthReturnTo()).toBe("");
    withReturnTo("/api/auth/oauth/authorize/../../../admin");
    expect(oauthReturnTo()).toBe("");
  });
});

describe("authorizedFetch", () => {
  const fetchMock = vi.fn<typeof fetch>();

  beforeEach(() => {
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockReset();
    localStorage.clear();
    localStorage.setItem(tokenKey, "stale-token");
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("uses same-origin credentials and the browser-mutation header", async () => {
    fetchMock.mockResolvedValueOnce(json({ ok: true }));
    const response = await authorizedFetch("/api/dashboards");
    expect(response.status).toBe(200);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const headers = new Headers(fetchMock.mock.calls[0][1]?.headers);
    expect(headers.get("Authorization")).toBeNull();
    expect(headers.get("X-Fanout-Request")).toBe("1");
    expect(fetchMock.mock.calls[0][1]?.credentials).toBe("same-origin");
  });

  it("clears legacy state and announces a rejected session without retrying", async () => {
    const unauthorized = vi.fn();
    window.addEventListener(unauthorizedEvent, unauthorized);
    fetchMock.mockResolvedValueOnce(new Response("", { status: 401 }));
    const response = await authorizedFetch("/api/data");
    expect(response.status).toBe(401);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(localStorage.getItem(tokenKey)).toBeNull();
    expect(unauthorized).toHaveBeenCalledTimes(1);
    window.removeEventListener(unauthorizedEvent, unauthorized);
  });

  it("surfaces authorization errors without announcing logout", async () => {
    const unauthorized = vi.fn();
    window.addEventListener(unauthorizedEvent, unauthorized);
    fetchMock.mockResolvedValueOnce(json({ message: "insufficient permissions" }, 403));
    await expect(authorizedFetch("/api/data")).rejects.toThrow("insufficient permissions");
    fetchMock.mockResolvedValueOnce(new Response("", { status: 503 }));
    expect((await authorizedFetch("/api/data")).status).toBe(503);
    expect(unauthorized).not.toHaveBeenCalled();
    window.removeEventListener(unauthorizedEvent, unauthorized);
  });

  it("treats an already-missing server session as a successful logout", async () => {
    const unauthorized = vi.fn();
    window.addEventListener(unauthorizedEvent, unauthorized);
    fetchMock.mockResolvedValueOnce(new Response("", { status: 401 }));
    await expect(logout()).resolves.toBeUndefined();
    expect(localStorage.getItem(tokenKey)).toBeNull();
    expect(unauthorized).toHaveBeenCalledTimes(1);
    expect(window.location.pathname).toBe("/");
    window.removeEventListener(unauthorizedEvent, unauthorized);
  });

  it("keeps local state when server-side logout fails", async () => {
    const unauthorized = vi.fn();
    window.addEventListener(unauthorizedEvent, unauthorized);
    fetchMock.mockResolvedValueOnce(new Response("", { status: 500 }));
    await expect(logout()).rejects.toThrow("Sign-out failed — your session is still active.");
    expect(localStorage.getItem(tokenKey)).toBe("stale-token");
    expect(unauthorized).not.toHaveBeenCalled();
    window.removeEventListener(unauthorizedEvent, unauthorized);
  });
});
