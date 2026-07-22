const legacyTokenKey = "fanout.access-token";
export const unauthorizedEvent = "fanout:unauthorized";

export function oauthReturnTo(): string {
  const value = new URLSearchParams(window.location.search).get("return_to");
  if (!value) return "";
  const target = new URL(value, window.location.origin);
  if (target.origin !== window.location.origin || target.pathname !== "/api/auth/oauth/authorize") return "";
  return `${target.pathname}${target.search}`;
}

export function clearLegacySession() {
  localStorage.removeItem(legacyTokenKey);
}

export function clearSession() {
  clearLegacySession();
  window.dispatchEvent(new Event(unauthorizedEvent));
}

export async function authorizedFetch(input: RequestInfo | URL, init: RequestInit = {}): Promise<Response> {
  const headers = new Headers(init.headers);
  headers.set("X-Fanout-Request", "1");
  const response = await fetch(input, { ...init, headers, credentials: "same-origin" });
  if (response.status === 401) clearSession();
  if (response.status === 403) {
    const payload = await response.clone().json().catch(() => ({})) as { message?: string; error?: string };
    throw new Error(payload.message ?? payload.error ?? "You do not have permission to perform this action.");
  }
  return response;
}

export async function logout() {
  const response = await authorizedFetch("/api/auth/logout", { method: "POST" });
  if (!response.ok && response.status !== 401) {
    throw new Error("Sign-out failed — your session is still active.");
  }
  if (response.status !== 401) clearSession();
  window.location.assign("/");
}
