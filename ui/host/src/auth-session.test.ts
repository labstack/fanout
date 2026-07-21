import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { authorizedFetch, oauthReturnTo, unauthorizedEvent } from "./auth-session";

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

  function requestsTo(url: string) {
    return fetchMock.mock.calls.filter(([input]) => String(input) === url);
  }

  it("passes non-401 responses through with the stored bearer token", async () => {
    fetchMock.mockResolvedValueOnce(json({ ok: true }));
    const response = await authorizedFetch("/api/dashboards");
    expect(response.status).toBe(200);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const headers = new Headers(fetchMock.mock.calls[0][1]?.headers);
    expect(headers.get("Authorization")).toBe("Bearer stale-token");
  });

  it("refreshes once and retries after a 401", async () => {
    fetchMock.mockImplementation(async (input) => {
      if (String(input) === "/api/auth/refresh") return json({ access_token: "fresh-token" });
      return requestsTo("/api/data").length === 1 ? new Response("", { status: 401 }) : json({ ok: true });
    });
    const response = await authorizedFetch("/api/data");
    expect(response.status).toBe(200);
    expect(requestsTo("/api/auth/refresh")).toHaveLength(1);
    expect(requestsTo("/api/data")).toHaveLength(2);
    const retryHeaders = new Headers(requestsTo("/api/data")[1][1]?.headers);
    expect(retryHeaders.get("Authorization")).toBe("Bearer fresh-token");
    expect(localStorage.getItem(tokenKey)).toBe("fresh-token");
  });

  it("shares a single refresh between concurrent 401 callers", async () => {
    let releaseRefresh!: (response: Response) => void;
    const pendingRefresh = new Promise<Response>((resolve) => { releaseRefresh = resolve; });
    const firstAttempts = new Set<string>();
    fetchMock.mockImplementation(async (input) => {
      const url = String(input);
      if (url === "/api/auth/refresh") return pendingRefresh;
      if (!firstAttempts.has(url)) { firstAttempts.add(url); return new Response("", { status: 401 }); }
      return json({ ok: true });
    });
    const inflight = Promise.all([authorizedFetch("/api/a"), authorizedFetch("/api/b")]);
    await vi.waitFor(() => expect(requestsTo("/api/auth/refresh")).toHaveLength(1));
    releaseRefresh(json({ access_token: "fresh-token" }));
    const [a, b] = await inflight;
    expect(a.status).toBe(200);
    expect(b.status).toBe(200);
    expect(requestsTo("/api/auth/refresh")).toHaveLength(1);
  });

  it("clears the session and returns the 401 when refresh fails", async () => {
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);
    const unauthorized = vi.fn();
    window.addEventListener(unauthorizedEvent, unauthorized);
    fetchMock.mockImplementation(async (input) => {
      if (String(input) === "/api/auth/refresh") return new Response("", { status: 500 });
      return new Response("", { status: 401 });
    });
    const response = await authorizedFetch("/api/data");
    expect(response.status).toBe(401);
    expect(localStorage.getItem(tokenKey)).toBeNull();
    expect(unauthorized).toHaveBeenCalledTimes(1);
    expect(consoleError).toHaveBeenCalled();
    window.removeEventListener(unauthorizedEvent, unauthorized);
    consoleError.mockRestore();
  });
});
