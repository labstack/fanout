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

  if (!res.ok) {
    const body = await res.json().catch(() => ({ detail: res.statusText }));
    throw new ApiError(res.status, body.detail ?? res.statusText);
  }

  if (res.status === 204) return undefined as T;

  try {
    return await res.json();
  } catch {
    throw new ApiError(res.status, `Invalid JSON response from ${path}`);
  }
}
