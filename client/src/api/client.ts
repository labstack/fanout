export class ApiError extends Error {
  readonly status: number;

  constructor(status: number, detail: string) {
    super(detail);
    this.name = "ApiError";
    this.status = status;
  }
}

export function isApiError(e: unknown): e is ApiError {
  return e instanceof ApiError;
}

let apiToken: string | null = null;

export function setApiToken(t: string | null) {
  apiToken = t;
}

export function getApiToken(): string | null {
  return apiToken;
}

async function fetchWithAuth(
  path: string,
  opts: RequestInit = {},
): Promise<Response> {
  const { headers: optsHeaders, ...rest } = opts;
  const headers: Record<string, string> = {
    ...(apiToken ? { Authorization: `Bearer ${apiToken}` } : {}),
    ...(optsHeaders as Record<string, string>),
  };
  if (opts.body && typeof opts.body === "string") {
    headers["Content-Type"] = "application/json";
  }
  return fetch(path, { ...rest, headers });
}

// Deduplicate concurrent refresh calls
let refreshPromise: Promise<boolean> | null = null;

/** Try to refresh the access token using the httpOnly refresh cookie. */
export async function tryRefresh(): Promise<boolean> {
  if (refreshPromise) return refreshPromise;

  refreshPromise = (async () => {
    try {
      const res = await fetch("/api/auth/refresh", {
        method: "POST",
        credentials: "include",
      });
      if (res.ok) {
        const data = await res.json();
        apiToken = data.access_token;
        return true;
      }
      if (res.status >= 500) return false; // server error — keep token, may still work
      apiToken = null; // 401/403 — token is invalid
      return false;
    } catch {
      return false;
    } finally {
      refreshPromise = null;
    }
  })();

  return refreshPromise;
}

export async function api<T>(
  path: string,
  opts: RequestInit = {},
): Promise<T> {
  let res: Response;
  try {
    res = await fetchWithAuth(path, opts);
  } catch {
    throw new ApiError(0, `Network error: unable to reach ${path}`);
  }

  // Auto-refresh on 401
  if (res.status === 401 && apiToken) {
    const refreshed = await tryRefresh();
    if (refreshed) {
      try {
        res = await fetchWithAuth(path, opts);
      } catch {
        throw new ApiError(0, `Network error: unable to reach ${path}`);
      }
    }
  }

  if (!res.ok) {
    const body = await res.json().catch(() => ({ detail: res.statusText }));
    throw new ApiError(res.status, body.detail ?? body.message ?? res.statusText);
  }

  if (res.status === 204) return undefined as T;

  try {
    return await res.json();
  } catch {
    throw new ApiError(res.status, `Invalid JSON response from ${path}`);
  }
}
