const tokenKey = "fanout.access-token";
export const unauthorizedEvent = "fanout:unauthorized";

let refreshPromise: Promise<string> | null = null;

export function getToken(): string {
  return localStorage.getItem(tokenKey) ?? "";
}

export function saveToken(token: string) {
  localStorage.setItem(tokenKey, token);
}

export function oauthReturnTo(): string {
  const value = new URLSearchParams(window.location.search).get("return_to");
  if (!value) return "";
  const target = new URL(value, window.location.origin);
  if (target.origin !== window.location.origin || target.pathname !== "/api/auth/oauth/authorize") return "";
  return `${target.pathname}${target.search}`;
}

export function clearSession() {
  localStorage.removeItem(tokenKey);
  window.dispatchEvent(new Event(unauthorizedEvent));
}

// SessionExpiredError marks a definitive rejection of the refresh token; only
// that may end the session. Transient failures (network blips, server
// restarts, 5xx) must not log the user out of every open tab.
export class SessionExpiredError extends Error {}

export async function refreshAccessToken(): Promise<string> {
  if (!refreshPromise) {
    refreshPromise = fetch("/api/auth/refresh", { method: "POST", credentials: "same-origin" })
      .then(async (response) => {
        if (response.status === 401 || response.status === 403) throw new SessionExpiredError(`Session refresh rejected (${response.status})`);
        if (!response.ok) throw new Error(`Session refresh failed (${response.status})`);
        const payload = await response.json() as { access_token?: string };
        if (!payload.access_token) throw new Error("Session refresh returned no access token");
        saveToken(payload.access_token);
        return payload.access_token;
      })
      .finally(() => { refreshPromise = null; });
  }
  return refreshPromise;
}

export async function authorizedFetch(input: RequestInfo | URL, init: RequestInit = {}): Promise<Response> {
  const request = (token: string) => {
    const headers = new Headers(init.headers);
    if (token) headers.set("Authorization", `Bearer ${token}`);
    return fetch(input, { ...init, headers, credentials: "same-origin" });
  };
  const response = await request(getToken());
  if (response.status !== 401) return response;
  try {
    return await request(await refreshAccessToken());
  } catch (cause) {
    if (cause instanceof SessionExpiredError) {
      clearSession();
    } else {
      console.error("Session refresh failed", cause);
    }
    return response;
  }
}

export async function logout() {
  try {
    await fetch("/api/auth/logout", { method: "POST", credentials: "same-origin" });
  } finally {
    clearSession();
  }
}
